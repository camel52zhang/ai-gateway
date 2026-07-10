package proxy

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"ai-gateway/internal/config"
	"ai-gateway/internal/db"
	"ai-gateway/internal/storage"
)

// TestMain spins up an isolated temp SQLite so these tests never touch the real
// data/gateway.db, and clears proxy env vars so the httptest "upstream" servers
// (bound to 127.0.0.1) are contacted directly instead of being routed to a dead
// HTTP_PROXY.
func TestMain(m *testing.M) {
	tmpDir, err := os.MkdirTemp("", "ai-gateway-auto-test")
	if err != nil {
		panic(err)
	}
	dbPath := filepath.Join(tmpDir, "gateway.db")
	os.Setenv("DB_PATH", dbPath)
	for _, k := range []string{"HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy", "NO_PROXY", "no_proxy"} {
		os.Unsetenv(k)
	}
	env := db.InitStorage()
	storage.Init(env)
	code := m.Run()
	os.Unsetenv("DB_PATH")
	// InitStorage always creates a stray ./data dir in the package CWD; remove it.
	os.RemoveAll("data")
	os.Exit(code)
}

// resetFaultState clears the global circuit-breaker and rate-limiter maps so each
// subtest starts from a clean "all healthy" state.
func resetFaultState() {
	cb.mu.Lock()
	cb.failures = make(map[string]int)
	cb.open = make(map[string]time.Time)
	cb.mu.Unlock()
	rl.mu.Lock()
	rl.counters = make(map[string]*rateLimitEntry)
	rl.mu.Unlock()
}

func autoBody(stream bool) map[string]interface{} {
	b := map[string]interface{}{
		"model": "auto",
		"messages": []interface{}{
			map[string]interface{}{"role": "user", "content": "hello"},
		},
	}
	if stream {
		b["stream"] = true
	}
	return b
}

func autoConfig(providers []config.UserProvider, customs []config.CustomProvider) *config.Config {
	return &config.Config{
		Providers:       providers,
		CustomProviders: customs,
		Models:          make(map[string][]string),
	}
}

func autoRequest() *http.Request {
	return httptest.NewRequest("POST", "/v1/chat/completions", nil)
}

// TestAutoFallbackToHealthyLowerPriority verifies that when the higher-priority
// provider fails (401) the auto loop falls through to a lower-priority provider
// that exposes the *same* model name, and succeeds. This guards the
// provider+model dedup fix: before it, same-named models were deduped by name
// alone and the healthy lower-priority candidate would be shadowed forever.
func TestAutoFallbackToHealthyLowerPriority(t *testing.T) {
	resetFaultState()

	var hitsA, hitsB int
	srvA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hitsA++
		w.WriteHeader(401)
		w.Write([]byte(`{"error":"unauthorized"}`))
	}))
	defer srvA.Close()
	srvB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hitsB++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		w.Write([]byte(`{"id":"x","choices":[{"message":{"content":"B-MARKER"}}],"model":"b-model"}`))
	}))
	defer srvB.Close()

	cfg := autoConfig(
		[]config.UserProvider{
			{Type: "providerA", Key: "kA"},
			{Type: "providerB", Key: "kB"},
		},
		[]config.CustomProvider{
			{ID: "providerA", BaseURL: srvA.URL, Adapter: "openai", Priority: 100},
			{ID: "providerB", BaseURL: srvB.URL, Adapter: "openai", Priority: 50},
		},
	)
	storage.SaveCachedModels("providerA", []string{"shared-model"})
	storage.SaveCachedModels("providerB", []string{"shared-model"})

	rec := httptest.NewRecorder()
	handleAutoProxy(rec, autoRequest(), autoBody(false), cfg, "rid-fallback")

	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	if hitsA < 1 {
		t.Fatalf("expected providerA to be attempted at least once")
	}
	if hitsB < 1 {
		t.Fatalf("expected providerB to be attempted (lower-priority fallback)")
	}
	if !contains(rec.Body.String(), "B-MARKER") {
		t.Fatalf("response should come from providerB, got body=%s", rec.Body.String())
	}
}

// TestAutoDistinctModelDistinguishesProvider is the regression-proof variant: the
// two providers expose *different* model names, so a success proves the
// lower-priority candidate was genuinely attempted (not just the higher one
// coincidentally sharing a name).
func TestAutoDistinctModelDistinguishesProvider(t *testing.T) {
	resetFaultState()

	var hitsA, hitsB int
	srvA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hitsA++
		w.WriteHeader(500)
		w.Write([]byte(`{"error":"boom"}`))
	}))
	defer srvA.Close()
	srvB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hitsB++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		w.Write([]byte(`{"id":"x","choices":[{"message":{"content":"from-B"}}],"model":"b-model"}`))
	}))
	defer srvB.Close()

	cfg := autoConfig(
		[]config.UserProvider{
			{Type: "providerA", Key: "kA"},
			{Type: "providerB", Key: "kB"},
		},
		[]config.CustomProvider{
			{ID: "providerA", BaseURL: srvA.URL, Adapter: "openai", Priority: 100},
			{ID: "providerB", BaseURL: srvB.URL, Adapter: "openai", Priority: 50},
		},
	)
	storage.SaveCachedModels("providerA", []string{"a-model"})
	storage.SaveCachedModels("providerB", []string{"b-model"})

	rec := httptest.NewRecorder()
	handleAutoProxy(rec, autoRequest(), autoBody(false), cfg, "rid-distinct")

	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	if !contains(rec.Body.String(), "b-model") {
		t.Fatalf("response model should be b-model (proves providerB tried), got body=%s", rec.Body.String())
	}
}

// TestAutoSkipsEmptyKeyProvider verifies that a provider configured without a key
// never produces a candidate (no empty Authorization header / wasted request).
func TestAutoSkipsEmptyKeyProvider(t *testing.T) {
	resetFaultState()

	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.WriteHeader(200)
	}))
	defer srv.Close()

	cfg := autoConfig(
		[]config.UserProvider{{Type: "providerEmpty", Key: ""}},
		[]config.CustomProvider{
			{ID: "providerEmpty", BaseURL: srv.URL, Adapter: "openai", Priority: 100},
		},
	)
	storage.SaveCachedModels("providerEmpty", []string{"m"})

	rec := httptest.NewRecorder()
	handleAutoProxy(rec, autoRequest(), autoBody(false), cfg, "rid-emptykey")

	if rec.Code != 404 {
		t.Fatalf("expected 404 (no candidates), got %d (body=%s)", rec.Code, rec.Body.String())
	}
	if hits != 0 {
		t.Fatalf("empty-key provider must never be attempted, but upstream was hit %d times", hits)
	}
}

// TestAutoCircuitOpenSkips verifies that a circuit-open provider is skipped (not
// attempted) and the request returns 503 with a circuit_open detail.
func TestAutoCircuitOpenSkips(t *testing.T) {
	resetFaultState()

	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.WriteHeader(200)
	}))
	defer srv.Close()

	cfg := autoConfig(
		[]config.UserProvider{{Type: "providerCircuit", Key: "k"}},
		[]config.CustomProvider{
			{ID: "providerCircuit", BaseURL: srv.URL, Adapter: "openai", Priority: 100},
		},
	)
	storage.SaveCachedModels("providerCircuit", []string{"m"})

	// Trip the circuit breaker (threshold is 5 failures).
	for i := 0; i < 5; i++ {
		cb.RecordFailure("providerCircuit")
	}

	rec := httptest.NewRecorder()
	handleAutoProxy(rec, autoRequest(), autoBody(false), cfg, "rid-circuit")

	if rec.Code != 503 {
		t.Fatalf("expected 503 (circuit open), got %d (body=%s)", rec.Code, rec.Body.String())
	}
	if hits != 0 {
		t.Fatalf("circuit-open provider must not be attempted, but upstream was hit %d times", hits)
	}
	if !contains(rec.Body.String(), "circuit_open") {
		t.Fatalf("detail should mention circuit_open, got body=%s", rec.Body.String())
	}
}

// TestAutoRateLimitedSkips verifies that a rate-limited provider is skipped and the
// request returns 429 with a rate_limited detail.
func TestAutoRateLimitedSkips(t *testing.T) {
	resetFaultState()

	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.WriteHeader(200)
	}))
	defer srv.Close()

	cfg := autoConfig(
		[]config.UserProvider{{Type: "providerRate", Key: "k"}},
		[]config.CustomProvider{
			{ID: "providerRate", BaseURL: srv.URL, Adapter: "openai", Priority: 100},
		},
	)
	storage.SaveCachedModels("providerRate", []string{"m"})

	// Exhaust the rate-limit budget (rlMax = 100).
	for i := 0; i < 100; i++ {
		rl.IsLimited("providerRate")
	}

	rec := httptest.NewRecorder()
	handleAutoProxy(rec, autoRequest(), autoBody(false), cfg, "rid-ratelimit")

	if rec.Code != 429 {
		t.Fatalf("expected 429 (rate limited), got %d (body=%s)", rec.Code, rec.Body.String())
	}
	if hits != 0 {
		t.Fatalf("rate-limited provider must not be attempted, but upstream was hit %d times", hits)
	}
	if !contains(rec.Body.String(), "rate_limited") {
		t.Fatalf("detail should mention rate_limited, got body=%s", rec.Body.String())
	}
}

// TestAutoAllExhausted verifies that when every candidate fails, the request
// returns 503 and the detail lists each failed provider/model with its status.
func TestAutoAllExhausted(t *testing.T) {
	resetFaultState()

	var hitsA, hitsB int
	srvA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hitsA++
		w.WriteHeader(500)
		w.Write([]byte(`{"error":"a-down"}`))
	}))
	defer srvA.Close()
	srvB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hitsB++
		w.WriteHeader(503)
		w.Write([]byte(`{"error":"b-down"}`))
	}))
	defer srvB.Close()

	cfg := autoConfig(
		[]config.UserProvider{
			{Type: "providerA", Key: "kA"},
			{Type: "providerB", Key: "kB"},
		},
		[]config.CustomProvider{
			{ID: "providerA", BaseURL: srvA.URL, Adapter: "openai", Priority: 100},
			{ID: "providerB", BaseURL: srvB.URL, Adapter: "openai", Priority: 50},
		},
	)
	storage.SaveCachedModels("providerA", []string{"a-model"})
	storage.SaveCachedModels("providerB", []string{"b-model"})

	rec := httptest.NewRecorder()
	handleAutoProxy(rec, autoRequest(), autoBody(false), cfg, "rid-exhausted")

	if rec.Code != 503 {
		t.Fatalf("expected 503 (all exhausted), got %d (body=%s)", rec.Code, rec.Body.String())
	}
	if hitsA < 1 || hitsB < 1 {
		t.Fatalf("expected both providers attempted (hitsA=%d hitsB=%d)", hitsA, hitsB)
	}
	body := rec.Body.String()
	if !contains(body, "providerA/a-model: 500") || !contains(body, "providerB/b-model: 503") {
		t.Fatalf("detail should list every failure, got body=%s", body)
	}
}

// TestAutoStreamingFallback verifies the streaming branch: when the higher-priority
// provider returns a non-2xx stream, the loop falls through to a healthy
// lower-priority streaming provider and forwards its SSE.
func TestAutoStreamingFallback(t *testing.T) {
	resetFaultState()

	var hitsA, hitsB int
	srvA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hitsA++
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(500)
		w.Write([]byte(`{"error":"a-stream-down"}`))
	}))
	defer srvA.Close()
	srvB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hitsB++
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"S2-TOKEN\"}}]}\n\ndata: [DONE]\n\n"))
	}))
	defer srvB.Close()

	cfg := autoConfig(
		[]config.UserProvider{
			{Type: "providerS1", Key: "k1"},
			{Type: "providerS2", Key: "k2"},
		},
		[]config.CustomProvider{
			{ID: "providerS1", BaseURL: srvA.URL, Adapter: "openai", Priority: 100},
			{ID: "providerS2", BaseURL: srvB.URL, Adapter: "openai", Priority: 50},
		},
	)
	storage.SaveCachedModels("providerS1", []string{"s1-model"})
	storage.SaveCachedModels("providerS2", []string{"s2-model"})

	rec := httptest.NewRecorder()
	handleAutoProxy(rec, autoRequest(), autoBody(true), cfg, "rid-stream")

	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	if hitsA < 1 {
		t.Fatalf("expected providerS1 stream attempted")
	}
	if hitsB < 1 {
		t.Fatalf("expected providerS2 stream fallback attempted")
	}
	if !contains(rec.Body.String(), "S2-TOKEN") {
		t.Fatalf("streamed body should contain providerS2 token, got body=%s", rec.Body.String())
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
