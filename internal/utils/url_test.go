package utils

import "testing"

// TestBuildChatURLNoV1Injection mirrors the v4 custom-provider behavior: a bare
// base URL (no /v1 segment) must be honored as-is and only have /chat/completions
// appended. This protects custom providers like Longcat whose base is
// https://api.longcat.chat/openai (correct path .../openai/chat/completions) and
// must NOT be turned into .../openai/v1/chat/completions.
func TestBuildChatURLNoV1Injection(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"https://api.openai.com/v1", "https://api.openai.com/v1/chat/completions"},
		{"https://api.openai.com/v1/", "https://api.openai.com/v1/chat/completions"},
		{"https://api.longcat.chat/openai", "https://api.longcat.chat/openai/chat/completions"},
		{"https://api.example.com", "https://api.example.com/chat/completions"},
		{"https://api.example.com/chat/completions", "https://api.example.com/chat/completions"},
	}
	for _, c := range cases {
		if got := BuildChatURL(c.in); got != c.want {
			t.Errorf("BuildChatURL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestBuildModelsURLNoV1Injection guards the same contract for the /models
// endpoint used when fetching models for a custom provider.
func TestBuildModelsURLNoV1Injection(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"https://api.openai.com/v1", "https://api.openai.com/v1/models"},
		{"https://api.longcat.chat/openai", "https://api.longcat.chat/openai/models"},
		{"https://api.example.com", "https://api.example.com/models"},
		{"https://api.example.com/chat/completions", "https://api.example.com/models"},
	}
	for _, c := range cases {
		if got := BuildModelsURL(c.in); got != c.want {
			t.Errorf("BuildModelsURL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
