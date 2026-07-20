package proxy

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"ai-gateway/internal/config"
	"ai-gateway/internal/storage"
)

// TestHandleProxyRejectsHiddenModel verifies that a model disabled via the
// dashboard's hide switch is completely un-callable: an explicit request for it
// returns 404 (as if it does not exist) and never reaches the upstream. Visible
// models still route normally.
func TestHandleProxyRejectsHiddenModel(t *testing.T) {
	resetFaultState()

	var upstreamHits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamHits++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		w.Write([]byte(`{"id":"x","choices":[{"message":{"content":"OK-MARKER"}}],"model":"visible-model"}`))
	}))
	defer srv.Close()

	const key = "sk-test-unified"
	cfg := &config.Config{
		UnifiedKey: key,
		Providers: []config.UserProvider{
			{Type: "openai", Key: "real-key"},
		},
		CustomProviders: []config.CustomProvider{
			{ID: "openai", BaseURL: srv.URL, Adapter: "openai", Priority: 100},
		},
		ModelEnabled: map[string]bool{
			"openai/hidden-model":  false,
			"openai/visible-model": true,
		},
	}
	if err := storage.SaveConfig(cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	storage.SaveCachedModels("openai", []string{"hidden-model", "visible-model"})

	post := func(model string) *httptest.ResponseRecorder {
		body, _ := json.Marshal(map[string]interface{}{
			"model": model,
			"messages": []interface{}{
				map[string]interface{}{"role": "user", "content": "hi"},
			},
		})
		req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+key)
		rec := httptest.NewRecorder()
		HandleProxy(rec, req)
		return rec
	}

	// Bare hidden model name => 404, upstream never called.
	hiddenRec := post("hidden-model")
	if hiddenRec.Code != 404 {
		t.Fatalf("hidden model expected 404, got %d (body=%s)", hiddenRec.Code, hiddenRec.Body.String())
	}
	if upstreamHits != 0 {
		t.Fatalf("hidden model must not reach upstream, got %d hits", upstreamHits)
	}

	// Explicit "provider/model" form of a hidden model => also 404.
	hiddenQualified := post("openai/hidden-model")
	if hiddenQualified.Code != 404 {
		t.Fatalf("qualified hidden model expected 404, got %d (body=%s)", hiddenQualified.Code, hiddenQualified.Body.String())
	}

	// Visible model => proxied to upstream (200 + marker).
	visibleRec := post("visible-model")
	if visibleRec.Code != 200 {
		t.Fatalf("visible model expected 200, got %d (body=%s)", visibleRec.Code, visibleRec.Body.String())
	}
	if !contains(visibleRec.Body.String(), "OK-MARKER") {
		t.Fatalf("visible model should proxy to upstream, got body=%s", visibleRec.Body.String())
	}
}
