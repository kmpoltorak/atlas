package cache

import "fmt"

// RedisClient keeps the minimum cache contract exposed by the approved architecture.
type RedisClient struct {
	Host  string
	Port  string
	store map[string]string
}

func NewRedisClient(host, port string) *RedisClient {
	return &RedisClient{Host: host, Port: port, store: map[string]string{}}
}

// Address returns the host:port address used by the cache client.
func (c *RedisClient) Address() string {
	return fmt.Sprintf("%s:%s", c.Host, c.Port)
}

// Set stores a value in the in-memory cache for the first implementation milestone.
func (c *RedisClient) Set(key, value string) error {
	if c.store == nil {
		c.store = map[string]string{}
	}
	c.store[key] = value
	return nil
}

// Get retrieves a value from the in-memory cache.
func (c *RedisClient) Get(key string) (string, error) {
	if c.store == nil {
		return "", nil
	}
	return c.store[key], nil
}
