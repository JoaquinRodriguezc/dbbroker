package broker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("not found")

type Assignment struct {
	Repository    string    `json:"repository"`
	Worktree      string    `json:"worktree"`
	DatabaseName  string    `json:"database_name"`
	DatabaseURL   string    `json:"database_url"`
	Template      string    `json:"template"`
	Status        string    `json:"status"`
	CreatedAt     time.Time `json:"created_at"`
	LastHeartbeat time.Time `json:"last_heartbeat"`
	// RedisURL carries the per-worktree credentials, so only Acquire returns it.
	RedisURL       string `json:"redis_url,omitempty"`
	RedisKeyPrefix string `json:"redis_key_prefix,omitempty"`
	redisPassword  string
}

type Service struct {
	pool      *pgxpool.Pool
	redis     *redisStore
	adminURL  string
	publicURL string
	ttl       time.Duration
	maxBackup int64
	log       *slog.Logger
}

// New connects to PostgreSQL and, when redisAdminURL is set, to Redis.
func New(ctx context.Context, adminURL, publicURL, redisAdminURL, redisPublicURL string, ttl time.Duration, maxBackup int64, log *slog.Logger) (*Service, error) {
	pool, err := pgxpool.New(ctx, adminURL)
	if err != nil {
		return nil, err
	}
	s := &Service{pool: pool, adminURL: adminURL, publicURL: publicURL, ttl: ttl, maxBackup: maxBackup, log: log}
	if err := s.ensureSchema(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	if redisAdminURL != "" {
		if s.redis, err = newRedisStore(ctx, redisAdminURL, redisPublicURL); err != nil {
			pool.Close()
			return nil, fmt.Errorf("redis: %w", err)
		}
	}
	return s, nil
}

func (s *Service) Close() {
	if s.redis != nil {
		_ = s.redis.Close()
	}
	s.pool.Close()
}

func (s *Service) ensureSchema(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, `
CREATE SCHEMA IF NOT EXISTS dbbroker;
CREATE TABLE IF NOT EXISTS dbbroker.templates (
  repository text NOT NULL, name text NOT NULL, database_name text UNIQUE NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(), PRIMARY KEY (repository, name)
);
ALTER TABLE dbbroker.templates ADD COLUMN IF NOT EXISTS repository text;
UPDATE dbbroker.templates SET repository='__legacy__' WHERE repository IS NULL;
ALTER TABLE dbbroker.templates ALTER COLUMN repository SET NOT NULL;
DO $migration$
BEGIN
  IF EXISTS (
    SELECT 1 FROM pg_constraint
    WHERE conrelid='dbbroker.templates'::regclass
      AND conname='templates_pkey'
      AND pg_get_constraintdef(oid)='PRIMARY KEY (name)'
  ) THEN
    ALTER TABLE dbbroker.templates DROP CONSTRAINT templates_pkey;
    ALTER TABLE dbbroker.templates ADD CONSTRAINT templates_pkey PRIMARY KEY (repository, name);
  END IF;
END $migration$;
CREATE TABLE IF NOT EXISTS dbbroker.assignments (
  repository text NOT NULL, worktree text NOT NULL, database_name text UNIQUE NOT NULL,
  template_name text NOT NULL, status text NOT NULL DEFAULT 'ready',
  created_at timestamptz NOT NULL DEFAULT now(), last_heartbeat timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (repository, worktree)
);
ALTER TABLE dbbroker.assignments ADD COLUMN IF NOT EXISTS redis_password text;`)
	return err
}

func (s *Service) Seed(ctx context.Context, repository, name string, backup io.Reader) (string, error) {
	if strings.TrimSpace(repository) == "" || strings.TrimSpace(name) == "" {
		return "", errors.New("repository and template name are required")
	}
	tmp, err := os.CreateTemp("", "dbbroker-backup-*")
	if err != nil {
		return "", err
	}
	path := tmp.Name()
	defer os.Remove(path)
	hash := sha256.New()
	written, err := io.Copy(io.MultiWriter(tmp, hash), io.LimitReader(backup, s.maxBackup+1))
	closeErr := tmp.Close()
	if err != nil {
		return "", err
	}
	if closeErr != nil {
		return "", closeErr
	}
	if written > s.maxBackup {
		return "", fmt.Errorf("backup exceeds %d bytes", s.maxBackup)
	}
	if written == 0 {
		return "", errors.New("backup is empty")
	}
	dbName := templateDB(repository, name, hex.EncodeToString(hash.Sum(nil)))

	if err := s.withLock(ctx, "template:"+repository+":"+name, func(conn *pgx.Conn) error {
		var oldDB string
		err := conn.QueryRow(ctx, `SELECT database_name FROM dbbroker.templates WHERE repository=$1 AND name=$2`, repository, name).Scan(&oldDB)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if oldDB == dbName {
			return nil
		}
		if err := s.dropDatabase(ctx, conn, dbName); err != nil {
			return err
		}
		if _, err := conn.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{dbName}.Sanitize()); err != nil {
			return err
		}
		if err := s.restore(ctx, path, dbName); err != nil {
			_ = s.dropDatabase(ctx, conn, dbName)
			return err
		}
		_, err = conn.Exec(ctx, `INSERT INTO dbbroker.templates(repository,name,database_name) VALUES($1,$2,$3)
ON CONFLICT(repository,name) DO UPDATE SET database_name=excluded.database_name, created_at=now()`, repository, name, dbName)
		if err == nil && oldDB != "" && oldDB != dbName {
			if dropErr := s.dropDatabase(ctx, conn, oldDB); dropErr != nil {
				s.log.Warn("old template cleanup failed", "database", oldDB, "error", dropErr)
			}
		}
		return err
	}); err != nil {
		return "", err
	}
	return dbName, nil
}

func (s *Service) Acquire(ctx context.Context, repository, worktree, template string) (Assignment, error) {
	if repository == "" || worktree == "" || template == "" {
		return Assignment{}, errors.New("repository, worktree and template are required")
	}
	var result Assignment
	err := s.withLock(ctx, "worktree:"+repository+":"+worktree, func(conn *pgx.Conn) error {
		row := conn.QueryRow(ctx, `SELECT `+assignmentColumns+`
FROM dbbroker.assignments WHERE repository=$1 AND worktree=$2`, repository, worktree)
		if err := scanAssignment(row, &result); err == nil {
			if _, err = conn.Exec(ctx, `UPDATE dbbroker.assignments SET last_heartbeat=now() WHERE repository=$1 AND worktree=$2`, repository, worktree); err != nil {
				return err
			}
			result.LastHeartbeat = time.Now().UTC()
			return s.ensureRedis(ctx, conn, &result)
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}

		var source string
		if err := conn.QueryRow(ctx, `SELECT database_name FROM dbbroker.templates WHERE repository=$1 AND name=$2`, repository, template).Scan(&source); errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("template %q: %w", template, ErrNotFound)
		} else if err != nil {
			return err
		}
		dbName := worktreeDB(repository, worktree)
		if err := s.dropDatabase(ctx, conn, dbName); err != nil {
			return err
		}
		// A heartbeat racing a release can resurrect the ACL user; start clean so
		// stale passwords and keys never carry over.
		if s.redis != nil {
			if err := s.redis.dropUser(ctx, dbName); err != nil {
				return fmt.Errorf("redis cleanup: %w", err)
			}
		}
		if _, err := conn.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{dbName}.Sanitize()+" TEMPLATE "+pgx.Identifier{source}.Sanitize()); err != nil {
			return err
		}
		row = conn.QueryRow(ctx, `INSERT INTO dbbroker.assignments(repository,worktree,database_name,template_name)
VALUES($1,$2,$3,$4) RETURNING `+assignmentColumns, repository, worktree, dbName, template)
		if err := scanAssignment(row, &result); err != nil {
			return err
		}
		return s.ensureRedis(ctx, conn, &result)
	})
	if err != nil {
		return Assignment{}, err
	}
	result.DatabaseURL = databaseURL(s.publicURL, result.DatabaseName)
	if s.redis != nil {
		result.RedisURL = redisURL(s.redis.publicURL, result.DatabaseName, result.redisPassword)
		result.RedisKeyPrefix = redisKeyPrefix(result.DatabaseName)
	}
	return result, nil
}

// ensureRedis stores a password for assignments that predate Redis support and
// (re)creates the worktree's ACL user.
func (s *Service) ensureRedis(ctx context.Context, conn *pgx.Conn, a *Assignment) error {
	if s.redis == nil {
		return nil
	}
	if a.redisPassword == "" {
		password, err := newRedisPassword()
		if err != nil {
			return err
		}
		if _, err := conn.Exec(ctx, `UPDATE dbbroker.assignments SET redis_password=$3 WHERE repository=$1 AND worktree=$2`, a.Repository, a.Worktree, password); err != nil {
			return err
		}
		a.redisPassword = password
	}
	return s.redis.ensureUser(ctx, a.DatabaseName, a.redisPassword)
}

// Heartbeat also restores the Redis user, so a Redis restart heals within one
// watcher cycle.
func (s *Service) Heartbeat(ctx context.Context, repository, worktree string) error {
	var dbName, password string
	err := s.pool.QueryRow(ctx, `UPDATE dbbroker.assignments SET last_heartbeat=now() WHERE repository=$1 AND worktree=$2
RETURNING database_name, coalesce(redis_password,'')`, repository, worktree).Scan(&dbName, &password)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil || s.redis == nil || password == "" {
		return err
	}
	return s.redis.ensureUser(ctx, dbName, password)
}

func (s *Service) Release(ctx context.Context, repository, worktree string) error {
	return s.withLock(ctx, "worktree:"+repository+":"+worktree, func(conn *pgx.Conn) error {
		var dbName string
		if err := conn.QueryRow(ctx, `SELECT database_name FROM dbbroker.assignments WHERE repository=$1 AND worktree=$2`, repository, worktree).Scan(&dbName); errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		} else if err != nil {
			return err
		}
		// Redis goes first: if it is unavailable the assignment stays intact and
		// the next release or collection retries.
		if s.redis != nil {
			if err := s.redis.dropUser(ctx, dbName); err != nil {
				return fmt.Errorf("redis cleanup: %w", err)
			}
		}
		if err := s.dropDatabase(ctx, conn, dbName); err != nil {
			return err
		}
		_, err := conn.Exec(ctx, `DELETE FROM dbbroker.assignments WHERE repository=$1 AND worktree=$2`, repository, worktree)
		return err
	})
}

func (s *Service) List(ctx context.Context) ([]Assignment, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+assignmentColumns+` FROM dbbroker.assignments ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Assignment, 0)
	for rows.Next() {
		var a Assignment
		if err := scanAssignment(rows, &a); err != nil {
			return nil, err
		}
		a.DatabaseURL = databaseURL(s.publicURL, a.DatabaseName)
		if s.redis != nil && a.redisPassword != "" {
			a.RedisKeyPrefix = redisKeyPrefix(a.DatabaseName)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Service) Collect(ctx context.Context) (int, error) {
	cutoff := time.Now().Add(-s.ttl)
	rows, err := s.pool.Query(ctx, `SELECT repository,worktree FROM dbbroker.assignments WHERE last_heartbeat < $1`, cutoff)
	if err != nil {
		return 0, err
	}
	type key struct{ repo, wt string }
	var expired []key
	for rows.Next() {
		var k key
		if err := rows.Scan(&k.repo, &k.wt); err != nil {
			rows.Close()
			return 0, err
		}
		expired = append(expired, k)
	}
	rows.Close()
	count := 0
	for _, k := range expired {
		if err := s.Release(ctx, k.repo, k.wt); err != nil && !errors.Is(err, ErrNotFound) {
			s.log.Error("garbage collection failed", "worktree", k.wt, "error", err)
			continue
		}
		count++
	}
	return count, nil
}

func (s *Service) withLock(ctx context.Context, key string, fn func(*pgx.Conn) error) error {
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock(hashtextextended($1,0))`, key); err != nil {
		return err
	}
	defer conn.Exec(context.Background(), `SELECT pg_advisory_unlock(hashtextextended($1,0))`, key)
	return fn(conn.Conn())
}

func (s *Service) dropDatabase(ctx context.Context, conn *pgx.Conn, name string) error {
	if _, err := conn.Exec(ctx, `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname=$1 AND pid<>pg_backend_pid()`, name); err != nil {
		return err
	}
	_, err := conn.Exec(ctx, "DROP DATABASE IF EXISTS "+pgx.Identifier{name}.Sanitize())
	return err
}

func (s *Service) restore(ctx context.Context, path, dbName string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	magic := make([]byte, 5)
	_, _ = io.ReadFull(f, magic)
	_ = f.Close()
	var cmd *exec.Cmd
	if string(magic) == "PGDMP" {
		cmd = exec.CommandContext(ctx, "pg_restore", "--no-owner", "--no-privileges", "--exit-on-error", "--dbname", databaseURL(s.adminURL, dbName), path)
	} else {
		cmd = exec.CommandContext(ctx, "psql", "--set", "ON_ERROR_STOP=1", "--dbname", databaseURL(s.adminURL, dbName), "--file", path)
	}
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("restore failed: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func databaseURL(base, database string) string {
	u, err := url.Parse(base)
	if err != nil {
		return base
	}
	u.Path = "/" + database
	return u.String()
}

type scanner interface{ Scan(...any) error }

const assignmentColumns = `repository,worktree,database_name,template_name,status,created_at,last_heartbeat,coalesce(redis_password,'')`

func scanAssignment(row scanner, a *Assignment) error {
	return row.Scan(&a.Repository, &a.Worktree, &a.DatabaseName, &a.Template, &a.Status, &a.CreatedAt, &a.LastHeartbeat, &a.redisPassword)
}
