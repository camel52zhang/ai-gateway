package storage

import (
	"testing"

	"ai-gateway/internal/config"
)

// TestCloneConfigIsolation ensures the cached config returned by GetConfig is an
// independent copy: a caller that mutates its returned config (e.g.
// HandleConfigPost rewriting Providers/Models) must never corrupt the shared
// in-memory snapshot or other callers' views.
func TestCloneConfigIsolation(t *testing.T) {
	orig := &config.Config{
		UnifiedKey: "sk-test",
		Providers:  []config.UserProvider{{Type: "openai", Key: "k"}},
		Models:     map[string][]string{"openai": {"gpt-4"}},
	}

	cp := cloneConfig(orig)
	if cp == nil || cp.UnifiedKey != "sk-test" {
		t.Fatalf("clone did not copy basic fields")
	}

	// Mutate the clone; the original must be untouched.
	cp.Providers[0].Key = "mutated"
	cp.Models["openai"][0] = "gpt-3"

	if orig.Providers[0].Key != "k" {
		t.Fatalf("clone shares the Providers slice with the original")
	}
	if orig.Models["openai"][0] != "gpt-4" {
		t.Fatalf("clone shares the Models map with the original")
	}

	// A second clone must also be independent of the first clone and the
	// original. cp was already mutated to "gpt-3" above, orig stays "gpt-4".
	cp2 := cloneConfig(orig)
	cp2.Models["openai"][0] = "gpt-2"
	if cp2.Models["openai"][0] != "gpt-2" {
		t.Fatalf("second clone mutation did not apply")
	}
	if cp.Models["openai"][0] != "gpt-3" {
		t.Fatalf("second clone is not isolated from the first clone")
	}
	if orig.Models["openai"][0] != "gpt-4" {
		t.Fatalf("second clone is not isolated from the original")
	}
}

// TestCloneConfigNilSafe checks the nil case does not panic and returns nil.
func TestCloneConfigNilSafe(t *testing.T) {
	if cloneConfig(nil) != nil {
		t.Fatalf("cloneConfig(nil) should return nil")
	}
}
