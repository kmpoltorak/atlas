package cache

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisClient is a real go-redis-backed cache adapter — used for temporary calculations,
// API response caching, and rate limiting per docs/deployment.md's "redis" service scope.
type RedisClient struct {
	client *redis.Client
	addr   string
}

func NewRedisClient(host, port string) *RedisClient {
	addr := host + ":" + port
	return &RedisClient{
		client: redis.NewClient(&redis.Options{Addr: addr}),
		addr:   addr,
	}
}

// Address returns the host:port address used by the cache client.
func (c *RedisClient) Address() string {
	return c.addr
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

// Delete removes a key. Deleting an already-absent key is not an error (used by auth.UseCase
// to invalidate a session on logout, where the key may have already expired on its own).
func (c *RedisClient) Delete(ctx context.Context, key string) error {
	return c.client.Del(ctx, key).Err()
}
