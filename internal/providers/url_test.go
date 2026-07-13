package providers

import "testing"

// TestUtilsBuildChatURLNoV1Injection guards the providers-internal URL builder
// used by GetTargetURL, keeping it in lock-step with v4 (never inject /v1 for a
// bare custom base URL).
func TestUtilsBuildChatURLNoV1Injection(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"https://api.openai.com/v1", "https://api.openai.com/v1/chat/completions"},
		{"https://api.longcat.chat/openai", "https://api.longcat.chat/openai/chat/completions"},
		{"https://api.example.com", "https://api.example.com/chat/completions"},
	}
	for _, c := range cases {
		if got := utilsBuildChatURL(c.in); got != c.want {
			t.Errorf("utilsBuildChatURL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
