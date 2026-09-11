package utils

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestClientIPIgnoresForwardedHeadersWithoutTrustProxy pins the secure default:
// unless a proxy is explicitly trusted, a client must not be able to choose its
// own rate-limit bucket (or forge its own log entry) by sending X-Real-IP /
// X-Forwarded-For.
func TestClientIPIgnoresForwardedHeadersWithoutTrustProxy(t *testing.T) {
	t.Setenv("TRUST_PROXY", "0")

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "192.0.2.10:5000"
	req.Header.Set("X-Real-IP", "10.0.0.1")
	req.Header.Set("X-Forwarded-For", "10.0.0.1")

	if got := ClientIP(req); got != "192.0.2.10" {
		t.Fatalf("expected the socket peer, got %q", got)
	}
}

// TestClientIPUsesHeadersWhenProxyIsTrusted covers the deployment shape this
// gateway actually runs in: nginx in front, so RemoteAddr is always the proxy
// and the real address has to come from the forwarding headers.
func TestClientIPUsesHeadersWhenProxyIsTrusted(t *testing.T) {
	t.Setenv("TRUST_PROXY", "1")

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "172.18.0.5:5000"
	req.Header.Set("X-Real-IP", "203.0.113.7")
	if got := ClientIP(req); got != "203.0.113.7" {
		t.Fatalf("expected X-Real-IP, got %q", got)
	}

	// Without X-Real-IP the LAST hop of X-Forwarded-For wins: nginx appends the
	// address it observed, so a client-supplied prefix must never be trusted —
	// otherwise an attacker could rotate the prefix to dodge the limiter.
	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	req2.RemoteAddr = "172.18.0.5:5000"
	req2.Header.Set("X-Forwarded-For", "1.2.3.4, 203.0.113.9")
	if got := ClientIP(req2); got != "203.0.113.9" {
		t.Fatalf("expected the last XFF hop, got %q", got)
	}
}

// TestClientIPFallsBackToRemoteAddr checks the bare-metal (no proxy) path, where
// RemoteAddr still carries a port that must be stripped. The request log now
// goes through the same helper, so a regression here would surface as
// "1.2.3.4:54321" in the Logs tab instead of a usable address.
func TestClientIPFallsBackToRemoteAddr(t *testing.T) {
	t.Setenv("TRUST_PROXY", "0")

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "198.51.100.4:41234"
	if got := ClientIP(req); got != "198.51.100.4" {
		t.Fatalf("expected the address without its port, got %q", got)
	}
}
