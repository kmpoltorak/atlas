package app

import (
	"context"
	"encoding/json"
	"time"

	"atlas/internal/domain/currency"
	"atlas/internal/infrastructure/cache"
)

// ratesCacheTTL bounds how often Atlas calls out to NBP, not how fresh the underlying data can
// be — NBP itself only republishes Table A once per business day (~11:45 CET), so anything
// sub-daily here is purely to avoid hitting NBP on every page render, not to catch genuinely
// newer rates. An hour is a reasonable, cheap default (Kris asked for roughly this cadence).
const ratesCacheTTL = time.Hour

const ratesCacheKey = "atlas:fxrates:nbp-a"

// redisRatesCache adapts the infrastructure Redis client into the exchangerate.Cache interface
// — the same adapter pattern as redisBarsCache (see bars_cache.go). Any Redis or JSON error is
// treated as a cache miss: a broken cache must never break rate lookups, only make them fall
// back to a live NBP call.
type redisRatesCache struct {
	client *cache.RedisClient
}

func (c *redisRatesCache) Get() (map[currency.Code]float64, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	raw, found, err := c.client.Get(ctx, ratesCacheKey)
	if err != nil || !found {
		return nil, false
	}
	var rates map[currency.Code]float64
	if err := json.Unmarshal([]byte(raw), &rates); err != nil {
		return nil, false
	}
	return rates, true
}

func (c *redisRatesCache) Set(rates map[currency.Code]float64) {
	raw, err := json.Marshal(rates)
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = c.client.Set(ctx, ratesCacheKey, string(raw), ratesCacheTTL)
}
