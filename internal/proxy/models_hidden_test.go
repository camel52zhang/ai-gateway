package proxy

import (
	"testing"

	"ai-gateway/internal/config"
)

// TestIsModelHidden locks the dashboard-compatible semantics of the model
// hide switch: an absent key defaults to enabled, only an explicit false hides
// a model. The absent-key case guards against the Go map zero-value trap
// (a missing lookup returns false, which would wrongly hide everything).
func TestIsModelHidden(t *testing.T) {
	cfg := &config.Config{}
	cfg.ModelEnabled = map[string]bool{
		"openai/gpt-4":   false, // explicitly hidden
		"openai/gpt-3.5": true,  // explicitly enabled
	}

	// Absent key => enabled (NOT hidden). This is the critical regression guard.
	if isModelHidden(cfg, "openai", "gpt-5") {
		t.Fatal("absent key must default to enabled, not hidden")
	}
	if isModelHidden(cfg, "anthropic", "claude-3") {
		t.Fatal("absent key for any provider must default to enabled")
	}

	// Explicit false => hidden.
	if !isModelHidden(cfg, "openai", "gpt-4") {
		t.Fatal("explicit false must be hidden")
	}

	// Explicit true => visible.
	if isModelHidden(cfg, "openai", "gpt-3.5") {
		t.Fatal("explicit true must be visible")
	}

	// Nil config / nil map => nothing hidden.
	var nilCfg *config.Config
	if isModelHidden(nilCfg, "openai", "gpt-4") {
		t.Fatal("nil config must hide nothing")
	}
	cfg2 := &config.Config{}
	if isModelHidden(cfg2, "openai", "gpt-4") {
		t.Fatal("nil ModelEnabled map must hide nothing")
	}
}
