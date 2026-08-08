package app

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"atlas/internal/domain/market"
	"atlas/internal/infrastructure/cache"
)

// barsCacheTTL matches ALERT_POLL_INTERVAL_SECONDS' default (config.Load) rather than being
// much shorter: with a 60s TTL, every 5-minute alert-poll iteration missed the cache entirely
// (300s > 60s) and hit the live provider for every watchlist symbol regardless — the cache
// wasn't actually collapsing the poll loop's own repeated calls, only unrelated bursts within
// the same minute (e.g. /recommendations and /predictions both collecting the same symbol back
// to back). At 300s, one poll iteration's fetch is still in cache for the next, cutting
// provider calls roughly in line with how often Yahoo's hourly bars actually change, without
// serving data staler than the poll loop already tolerates.
const barsCacheTTL = 300 * time.Second

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
