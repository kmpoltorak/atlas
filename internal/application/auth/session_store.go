package auth

import (
	"context"
	"time"
)

// SessionStore persists opaque session tokens with a TTL. Implemented by
// infrastructure/cache.RedisClient (see internal/app/app.go's wiring) — Redis already backs
// caching elsewhere in Atlas, so sessions need no new infrastructure.
type SessionStore interface {
	Set(ctx context.Context, key, value string, ttl time.Duration) error
	Get(ctx context.Context, key string) (value string, found bool, err error)
	Delete(ctx context.Context, key string) error
	// Increment atomically increments key and returns the new count, setting ttl only the
	// first time the key is created — see auth.UseCase's login-failure lockout.
	Increment(ctx context.Context, key string, ttl time.Duration) (count int64, err error)
}
