package cache

import "testing"

func TestRedisClientAddressAndRoundTripContract(t *testing.T) {
	client := NewRedisClient("localhost", "6379")
	if got := client.Address(); got != "localhost:6379" {
		t.Fatalf("expected localhost:6379, got %q", got)
	}
	if err := client.Set("atlas:test", "ok"); err != nil {
		t.Fatalf("expected Set to succeed, got %v", err)
	}
	value, err := client.Get("atlas:test")
	if err != nil {
		t.Fatalf("expected Get to succeed, got %v", err)
	}
	if value != "ok" {
		t.Fatalf("expected value ok, got %q", value)
	}
}
