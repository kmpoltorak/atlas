package cache

import (
	"context"
	"net"
	"os"
	"testing"
	"time"
)

// connectTestClient opens a real connection using TEST_REDIS_ADDR, skipping the test cleanly
// when it isn't set — see .github/workflows/ci.yml for the CI-side service container.
func connectTestClient(t *testing.T) *RedisClient {
	t.Helper()
	addr := os.Getenv("TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("TEST_REDIS_ADDR not set; skipping Redis integration test")
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("invalid TEST_REDIS_ADDR %q: %v", addr, err)
	}
	client := NewRedisClient(host, port, os.Getenv("TEST_REDIS_PASSWORD"), false)
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func TestPingSucceedsAgainstLiveRedis(t *testing.T) {
	client := connectTestClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := client.Ping(ctx); err != nil {
		t.Fatalf("expected Ping to succeed, got %v", err)
	}
}

func TestSetAndGetRoundTrip(t *testing.T) {
	client := connectTestClient(t)
	ctx := context.Background()

	if err := client.Set(ctx, "atlas:test:roundtrip", "ok", time.Minute); err != nil {
		t.Fatalf("Set failed: %v", err)
	}
	value, found, err := client.Get(ctx, "atlas:test:roundtrip")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if !found {
		t.Fatal("expected key to be found")
	}
	if value != "ok" {
		t.Fatalf("expected value 'ok', got %q", value)
	}
}

func TestGetReturnsNotFoundForMissingKey(t *testing.T) {
	client := connectTestClient(t)
	ctx := context.Background()

	_, found, err := client.Get(ctx, "atlas:test:does-not-exist")
	if err != nil {
		t.Fatalf("expected no error on cache miss, got %v", err)
	}
	if found {
		t.Fatal("expected found=false for a missing key")
	}
}

func TestSetRespectsTTL(t *testing.T) {
	client := connectTestClient(t)
	ctx := context.Background()

	if err := client.Set(ctx, "atlas:test:ttl", "value", 50*time.Millisecond); err != nil {
		t.Fatalf("Set failed: %v", err)
	}
	time.Sleep(150 * time.Millisecond)

	_, found, err := client.Get(ctx, "atlas:test:ttl")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if found {
		t.Fatal("expected the key to have expired")
	}
}
