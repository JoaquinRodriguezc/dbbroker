package broker

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/url"

	"github.com/redis/go-redis/v9"
)

// redisStore isolates worktrees inside one Redis server with an ACL user per
// worktree, restricted to the keys and channels under its own prefix.
type redisStore struct {
	client    *redis.Client
	publicURL string
}

func newRedisStore(ctx context.Context, adminURL, publicURL string) (*redisStore, error) {
	opts, err := redis.ParseURL(adminURL)
	if err != nil {
		return nil, err
	}
	client := redis.NewClient(opts)
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, err
	}
	return &redisStore{client: client, publicURL: publicURL}, nil
}

func (r *redisStore) Close() error { return r.client.Close() }

// ensureUser is additive and idempotent, so it can run on every heartbeat to
// restore users lost when Redis restarts without touching live connections.
func (r *redisStore) ensureUser(ctx context.Context, user, password string) error {
	prefix := redisKeyPrefix(user)
	return r.client.Do(ctx, "ACL", "SETUSER", user, "on", ">"+password,
		"~"+prefix+"*", "&"+prefix+"*",
		"+@all", "-@admin", "-flushall", "-flushdb", "-swapdb", "-select", "-move").Err()
}

// dropUser removes every key under the user's prefix and then the user, which
// also disconnects its clients.
func (r *redisStore) dropUser(ctx context.Context, user string) error {
	var cursor uint64
	for {
		keys, next, err := r.client.Scan(ctx, cursor, redisKeyPrefix(user)+"*", 500).Result()
		if err != nil {
			return err
		}
		if len(keys) > 0 {
			if err := r.client.Unlink(ctx, keys...).Err(); err != nil {
				return err
			}
		}
		if cursor = next; cursor == 0 {
			break
		}
	}
	return r.client.Do(ctx, "ACL", "DELUSER", user).Err()
}

func redisKeyPrefix(user string) string { return user + ":" }

func redisURL(base, user, password string) string {
	u, err := url.Parse(base)
	if err != nil {
		return base
	}
	u.User = url.UserPassword(user, password)
	return u.String()
}

func newRedisPassword() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
