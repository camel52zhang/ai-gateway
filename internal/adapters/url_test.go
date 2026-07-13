package adapters

import "testing"

// TestBuildChatURLNoV1Injection covers the actual proxy forwarding path
// (openaiProxy receives buildChatURL(def.BaseURL)). A custom provider base URL
// without a /v1 segment must NOT get /v1 injected, matching v4 behavior.
func TestBuildChatURLNoV1Injection(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"https://api.openai.com/v1", "https://api.openai.com/v1/chat/completions"},
		{"https://api.longcat.chat/openai", "https://api.longcat.chat/openai/chat/completions"},
		{"https://api.example.com", "https://api.example.com/chat/completions"},
		{"https://api.example.com/chat/completions", "https://api.example.com/chat/completions"},
	}
	for _, c := range cases {
		if got := buildChatURL(c.in); got != c.want {
			t.Errorf("buildChatURL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
