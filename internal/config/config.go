package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

type Config struct {
	Listen            string
	DatabaseURL       string
	PublicDatabaseURL string
	RedisURL          string
	PublicRedisURL    string
	Token             string
	LeaseTTL          time.Duration
	GCInterval        time.Duration
	MaxBackupBytes    int64
}

func Load() (Config, error) {
	c := Config{
		Listen:            getenv("DBBROKER_LISTEN", ":8080"),
		DatabaseURL:       getenv("DBBROKER_DATABASE_URL", "postgres://dbbroker:dbbroker@127.0.0.1:54329/postgres?sslmode=disable"),
		PublicDatabaseURL: getenv("DBBROKER_PUBLIC_DATABASE_URL", "postgres://dbbroker:dbbroker@127.0.0.1:54329/postgres?sslmode=disable"),
		RedisURL:          os.Getenv("DBBROKER_REDIS_URL"),
		PublicRedisURL:    os.Getenv("DBBROKER_PUBLIC_REDIS_URL"),
		Token:             os.Getenv("DBBROKER_TOKEN"),
	}
	if c.RedisURL != "" && c.PublicRedisURL == "" {
		return Config{}, fmt.Errorf("DBBROKER_PUBLIC_REDIS_URL is required when DBBROKER_REDIS_URL is set")
	}
	var err error
	if c.LeaseTTL, err = duration("DBBROKER_LEASE_TTL", 15*time.Minute); err != nil {
		return Config{}, err
	}
	if c.GCInterval, err = duration("DBBROKER_GC_INTERVAL", time.Minute); err != nil {
		return Config{}, err
	}
	if c.MaxBackupBytes, err = integer("DBBROKER_MAX_BACKUP_BYTES", 2<<30); err != nil {
		return Config{}, err
	}
	return c, nil
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
func duration(key string, fallback time.Duration) (time.Duration, error) {
	v := os.Getenv(key)
	if v == "" {
		return fallback, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", key, err)
	}
	return d, nil
}
func integer(key string, fallback int64) (int64, error) {
	v := os.Getenv(key)
	if v == "" {
		return fallback, nil
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", key)
	}
	return n, nil
}
