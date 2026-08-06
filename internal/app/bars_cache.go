package app

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"atlas/internal/domain/market"
	"atlas/internal/infrastructure/cache"
)

// barsCacheTTL is deliberately short: this cache exists to collapse redundant provider calls
// within a single burst of requests (e.g. /recommendations and /predictions both collecting
// the same symbol), not to serve meaningfully stale market data.
const barsCacheTTL = 60 * time.Second

// redisBarsCache adapts the infrastructure Redis client into the collector.Cache interface.
// Any Redis or JSON error is treated as a cache miss — a broken cache must never break bar
// collection, only make it fall back to the live provider.
type redisBarsCache struct {
	client *cache.RedisClient
}

func (c *redisBarsCache) key(symbol string, window int) string {
	return fmt.Sprintf("atlas:bars:%s:%d", symbol, window)
}

func (c *redisBarsCache) Get(symbol string, window int) ([]market.Bar, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	raw, found, err := c.client.Get(ctx, c.key(symbol, window))
	if err != nil || !found {
		return nil, false
	}
	var bars []market.Bar
	if err := json.Unmarshal([]byte(raw), &bars); err != nil {
		return nil, false
	}
	return bars, true
}

func (c *redisBarsCache) Set(symbol string, window int, bars []market.Bar) {
	raw, err := json.Marshal(bars)
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = c.client.Set(ctx, c.key(symbol, window), string(raw), barsCacheTTL)
}
