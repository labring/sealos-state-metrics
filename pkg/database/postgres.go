package database

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type PoolConfig struct {
	MaxConns          int32
	MinConns          int32
	MaxConnLifetime   time.Duration
	MaxConnIdleTime   time.Duration
	HealthCheckPeriod time.Duration
	PingTimeout       time.Duration
}

func DefaultPoolConfig() *PoolConfig {
	return &PoolConfig{
		MaxConns:          25,
		MinConns:          5,
		MaxConnLifetime:   time.Hour,
		MaxConnIdleTime:   30 * time.Minute,
		HealthCheckPeriod: time.Minute,
		PingTimeout:       5 * time.Second,
	}
}

type PoolOption func(*PoolConfig)

func WithMaxConns(n int32) PoolOption {
	return func(c *PoolConfig) {
		c.MaxConns = n
	}
}

func WithMinConns(n int32) PoolOption {
	return func(c *PoolConfig) {
		c.MinConns = n
	}
}

func WithMaxConnLifetime(d time.Duration) PoolOption {
	return func(c *PoolConfig) {
		c.MaxConnLifetime = d
	}
}

func WithMaxConnIdleTime(d time.Duration) PoolOption {
	return func(c *PoolConfig) {
		c.MaxConnIdleTime = d
	}
}

func WithHealthCheckPeriod(d time.Duration) PoolOption {
	return func(c *PoolConfig) {
		c.HealthCheckPeriod = d
	}
}

func WithPingTimeout(d time.Duration) PoolOption {
	return func(c *PoolConfig) {
		c.PingTimeout = d
	}
}

func InitPgClient(dsn string, opts ...PoolOption) (*pgxpool.Pool, error) {
	poolConfig := DefaultPoolConfig()

	for _, opt := range opts {
		opt(poolConfig)
	}

	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to parse DSN: %w", err)
	}

	config.MaxConns = poolConfig.MaxConns
	config.MinConns = poolConfig.MinConns
	config.MaxConnLifetime = poolConfig.MaxConnLifetime
	config.MaxConnIdleTime = poolConfig.MaxConnIdleTime
	config.HealthCheckPeriod = poolConfig.HealthCheckPeriod

	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		return nil, fmt.Errorf("failed to create connection pool: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), poolConfig.PingTimeout)
	defer cancel()

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	return pool, nil
}
