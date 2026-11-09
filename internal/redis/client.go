package redis

import (
	"context"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
)

var (
	ErrEmptyAddresses = errors.New("redis config: at least one address is required")
)

// Config defines connection options and timeouts for Redis.
type Config struct {
	Addrs        []string      `yaml:"addrs"`
	Password     string        `yaml:"password"`
	DB           int           `yaml:"db"`
	DialTimeout  time.Duration `yaml:"dial_timeout"`
	ReadTimeout  time.Duration `yaml:"read_timeout"`
	WriteTimeout time.Duration `yaml:"write_timeout"`
	PoolSize     int           `yaml:"pool_size"`
}

// Client wraps redis.UniversalClient to provide standalone and cluster compatibility.
type Client struct {
	rdb redis.UniversalClient
	cfg Config
}

// Universal returns the underlying redis.UniversalClient for executing commands.
func (c *Client) Universal() redis.UniversalClient {
	if c == nil {
		return nil
	}
	return c.rdb
}

// NewClient initializes a UniversalClient with configured timeouts and pools.
// If Addrs contains 1 address, it connects to standalone Redis.
// If Addrs contains multiple addresses, it connects as a Redis Cluster.
func NewClient(cfg Config) (*Client, error) {
	if len(cfg.Addrs) == 0 {
		return nil, ErrEmptyAddresses
	}

	// Apply sensible defaults if not provided
	if cfg.DialTimeout == 0 {
		cfg.DialTimeout = 100 * time.Millisecond
	}
	if cfg.ReadTimeout == 0 {
		cfg.ReadTimeout = 100 * time.Millisecond
	}
	if cfg.WriteTimeout == 0 {
		cfg.WriteTimeout = 100 * time.Millisecond
	}
	if cfg.PoolSize == 0 {
		cfg.PoolSize = 50
	}

	// TODO [Learner Brick 3.1]:
	// 1. Initialize redis.NewUniversalClient using &redis.UniversalOptions{ ... }
	//    mapping Addrs, Password, DB, DialTimeout, ReadTimeout, WriteTimeout, and PoolSize.
	// 2. Return &Client{rdb: uClient, cfg: cfg}, nil.

	client := &Client{}
	client.rdb = redis.NewUniversalClient(&redis.UniversalOptions{
		Addrs:        cfg.Addrs,
		Password:     cfg.Password,
		DB:           cfg.DB,
		DialTimeout:  cfg.DialTimeout,
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
		PoolSize:     cfg.PoolSize,
	})
	client.cfg = cfg

	return client, nil
}

// Ping performs a health check against Redis with the given context.
func (c *Client) Ping(ctx context.Context) error {
	// TODO [Learner Brick 3.1]:
	// 1. If c == nil or c.rdb == nil, return an error.
	// 2. Call c.rdb.Ping(ctx).Err() and return the error.

	if c == nil || c.rdb == nil {
		return errors.New("Invalid client")
	}

	if err := c.rdb.Ping(ctx).Err(); err != nil {
		return err
	}

	return nil
}

// Close gracefully closes connections in the Redis pool.
func (c *Client) Close() error {
	// TODO [Learner Brick 3.1]:
	// 1. If c == nil or c.rdb == nil, return nil.
	// 2. Call c.rdb.Close() and return its error.

	if c == nil || c.rdb == nil {
		return nil
	}

	if err := c.rdb.Close(); err != nil {
		return err
	}

	return nil
}
