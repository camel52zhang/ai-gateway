package providers

import (
	"testing"

	"ai-gateway/internal/config"
)

// TestGetFallbackProviderSkipsPaused verifies the circuit-breaker fallback never
// selects a paused provider as the failover target.
func TestGetFallbackProviderSkipsPaused(t *testing.T) {
	ups := []config.UserProvider{
		{Type: "primary", Key: "k"},
		{Type: "paused-prov", Key: "k", Paused: true},
		{Type: "fallback-prov", Key: "k"},
	}

	// Exclude "primary"; the only viable fallback must be fallback-prov, never
	// the paused one.
	got := GetFallbackProvider("gpt-4", ups, "primary", nil)
	if got == nil {
		t.Fatal("expected a non-paused fallback, got nil")
	}
	if got.Type != "fallback-prov" {
		t.Fatalf("expected fallback-prov, got %q", got.Type)
	}

	// When every candidate other than the excluded one is paused, return nil.
	allPaused := []config.UserProvider{
		{Type: "primary", Key: "k"},
		{Type: "paused-a", Key: "k", Paused: true},
		{Type: "paused-b", Key: "k", Paused: true},
	}
	if got := GetFallbackProvider("gpt-4", allPaused, "primary", nil); got != nil {
		t.Fatalf("expected nil when all candidates paused, got %q", got.Type)
	}
}
