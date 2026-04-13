package redis

import (
	"context"
	"crypto/tls"
	"fmt"
	"time"

	"github.com/nonchan7720/manifold/pkg/config"
	"github.com/redis/go-redis/v9"
)

// Client represents the Redis client wrapper
type Client struct {
	client redis.UniversalClient
}

// NewClient initializes a new Redis connection based on the provided configuration.
func NewClient(ctx context.Context, cfg *config.RedisConfig) (*Client, error) {
	var tlsConfig *tls.Config
	if cfg.TLS {
		// Provide a basic TLS configuration
		tlsConfig = &tls.Config{
			MinVersion: tls.VersionTLS12,
			// You can add more specific TLS configurations here if needed
			// such as RootCAs or InsecureSkipVerify depending on the environment.
		}
	}

	var rdb redis.UniversalClient

	switch {
	case cfg.ClusterMode:
		rdb = redis.NewClusterClient(&redis.ClusterOptions{
			Addrs:     cfg.Addrs,
			Username:  cfg.User,
			Password:  cfg.Password,
			TLSConfig: tlsConfig,
		})
	case cfg.MasterName != "":
		rdb = redis.NewFailoverClient(&redis.FailoverOptions{
			MasterName:    cfg.MasterName,
			SentinelAddrs: cfg.Addrs,
			Username:      cfg.User,
			Password:      cfg.Password,
			DB:            cfg.DB,
			TLSConfig:     tlsConfig,
		})
	default:
		var opt *redis.Options
		if cfg.URL != "" {
			opt, _ = redis.ParseURL(cfg.URL)
		} else {
			// Single node mode
			addr := "localhost:6379"
			if len(cfg.Addrs) > 0 {
				addr = cfg.Addrs[0]
			}
			opt = &redis.Options{
				Addr:      addr,
				Username:  cfg.User,
				Password:  cfg.Password,
				DB:        cfg.DB,
				TLSConfig: tlsConfig,
			}
		}
		rdb = redis.NewClient(opt)
	}

	// Ping to verify connection
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if err := rdb.Ping(pingCtx).Err(); err != nil {
		return nil, fmt.Errorf("failed to connect to redis: %w", err)
	}

	return &Client{client: rdb}, nil
}

// Set stores a value with an expiration type
func (c *Client) Set(ctx context.Context, key string, value any, expiration time.Duration) error {
	return c.client.Set(ctx, key, value, expiration).Err()
}

// Get retrieves a string value by key
func (c *Client) Get(ctx context.Context, key string) (string, error) {
	return c.client.Get(ctx, key).Result()
}

// Del removes a key
func (c *Client) Del(ctx context.Context, key string) error {
	return c.client.Del(ctx, key).Err()
}

// Close closes the Redis connection
func (c *Client) Close() error {
	return c.client.Close()
}
