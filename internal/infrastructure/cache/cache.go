package cache

import (
	"context"
	"crypto/tls"
	"net"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisClient is a real go-redis-backed cache adapter — used for temporary calculations,
// API response caching, and rate limiting per docs/deployment.md's "redis" service scope.
type RedisClient struct {
	client *redis.Client
}

// NewRedisClient dials Redis, optionally authenticating and wrapping the connection in TLS.
// Both are off by default because the docker-compose stack runs Redis on its own private
// network; set REDIS_PASSWORD/REDIS_TLS to reach a Redis that requires them.
func NewRedisClient(host, port, password string, useTLS bool) *RedisClient {
	options := &redis.Options{Addr: net.JoinHostPort(host, port), Password: password}
	if useTLS {
		options.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12, ServerName: host}
	}
	return &RedisClient{client: redis.NewClient(options)}
}

func (c *RedisClient) Close() error {
	return c.client.Close()
}

func (c *RedisClient) Ping(ctx context.Context) error {
	return c.client.Ping(ctx).Err()
}

// Set stores a value with an expiration. ttl <= 0 means the key never expires.
func (c *RedisClient) Set(ctx context.Context, key, value string, ttl time.Duration) error {
	return c.client.Set(ctx, key, value, ttl).Err()
}

// Get retrieves a value. found is false on a cache miss, which is not an error condition.
func (c *RedisClient) Get(ctx context.Context, key string) (value string, found bool, err error) {
	value, err = c.client.Get(ctx, key).Result()
	if err == redis.Nil {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return value, true, nil
}
