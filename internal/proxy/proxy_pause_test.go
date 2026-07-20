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

// TestHandleListModelsExcludesPaused verifies the /v1/models listing omits a
// paused provider's models, still lists an active provider's, and only
// advertises the virtual "auto" model when at least one provider is active.
func TestHandleListModelsExcludesPaused(t *testing.T) {
	resetFaultState()

	const key = "sk-test-unified"

	cfg := &config.Config{
		UnifiedKey: key,
		Providers: []config.UserProvider{
			{Type: "paused-prov", Key: "real-key", Paused: true},
			{Type: "active-prov", Key: "real-key"},
		},
		CustomProviders: []config.CustomProvider{
			{ID: "paused-prov", BaseURL: "http://unused", Adapter: "openai", Priority: 100},
			{ID: "active-prov", BaseURL: "http://unused", Adapter: "openai", Priority: 90},
		},
	}
	if err := storage.SaveConfig(cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	storage.SaveCachedModels("paused-prov", []string{"paused-model-a", "paused-model-b"})
	storage.SaveCachedModels("active-prov", []string{"active-model-a"})

	req := httptest.NewRequest("GET", "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer "+key)
	rec := httptest.NewRecorder()
	HandleListModels(rec, req)

	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	ids := map[string]bool{}
	for _, d := range resp.Data {
		ids[d.ID] = true
	}
	if ids["paused-model-a"] || ids["paused-model-b"] {
		t.Fatalf("paused provider models must not be listed: %v", ids)
	}
	if !ids["active-model-a"] {
		t.Fatalf("active provider model must be listed: %v", ids)
	}
	if !ids["auto"] {
		t.Fatalf("auto should be advertised when an active provider exists: %v", ids)
	}
}

// TestHandleListModelsNoAutoWhenAllPaused verifies the virtual "auto" model is
// not advertised when every provider is paused (nothing for auto to route to).
func TestHandleListModelsNoAutoWhenAllPaused(t *testing.T) {
	resetFaultState()

	const key = "sk-test-unified"
	cfg := &config.Config{
		UnifiedKey: key,
		Providers: []config.UserProvider{
			{Type: "p1", Key: "real-key", Paused: true},
			{Type: "p2", Key: "real-key", Paused: true},
		},
		CustomProviders: []config.CustomProvider{
			{ID: "p1", BaseURL: "http://unused", Adapter: "openai", Priority: 100},
			{ID: "p2", BaseURL: "http://unused", Adapter: "openai", Priority: 90},
		},
	}
	if err := storage.SaveConfig(cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	storage.SaveCachedModels("p1", []string{"m1"})

	req := httptest.NewRequest("GET", "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer "+key)
	rec := httptest.NewRecorder()
	HandleListModels(rec, req)

	var resp struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	for _, d := range resp.Data {
		if d.ID == "auto" {
			t.Fatalf("auto must not be advertised when all providers are paused")
		}
	}
}

// TestHandleProxyRejectsPausedProvider verifies an explicit request for a model
// owned by a paused provider returns 404 and never reaches the upstream.
func TestHandleProxyRejectsPausedProvider(t *testing.T) {
	resetFaultState()

	var pausedHits, activeHits int
	pausedSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pausedHits++
		w.WriteHeader(200)
	}))
	defer pausedSrv.Close()
	activeSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		activeHits++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		w.Write([]byte(`{"id":"x","choices":[{"message":{"content":"OK-MARKER"}}]}`))
	}))
	defer activeSrv.Close()

	const key = "sk-test-unified"
	cfg := &config.Config{
		UnifiedKey: key,
		Providers: []config.UserProvider{
			{Type: "paused-prov", Key: "real-key", Paused: true},
		},
		CustomProviders: []config.CustomProvider{
			{ID: "paused-prov", BaseURL: pausedSrv.URL, Adapter: "openai", Priority: 100},
		},
	}
	if err := storage.SaveConfig(cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	storage.SaveCachedModels("paused-prov", []string{"paused-model-a"})

	body, _ := json.Marshal(map[string]interface{}{
		"model": "paused-model-a",
		"messages": []interface{}{
			map[string]interface{}{"role": "user", "content": "hi"},
		},
	})
	req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+key)
	rec := httptest.NewRecorder()
	HandleProxy(rec, req)

	if rec.Code != 404 {
		t.Fatalf("paused provider request expected 404, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	if pausedHits != 0 {
		t.Fatalf("paused provider must not reach upstream, got %d hits", pausedHits)
	}
}

// TestHandleAutoProxySkipsPaused verifies auto routing never picks a paused
// provider's candidate; only the active provider's upstream is used.
func TestHandleAutoProxySkipsPaused(t *testing.T) {
	resetFaultState()

	var pausedHits, activeHits int
	pausedSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pausedHits++
		w.WriteHeader(200)
	}))
	defer pausedSrv.Close()
	activeSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		activeHits++
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"x","choices":[{"message":{"content":"OK-MARKER"}}],"model":"auto"}`))
	}))
	defer activeSrv.Close()

	const key = "sk-test-unified"
	cfg := &config.Config{
		UnifiedKey: key,
		Providers: []config.UserProvider{
			{Type: "paused-prov", Key: "real-key", Paused: true},
			{Type: "active-prov", Key: "real-key"},
		},
		CustomProviders: []config.CustomProvider{
			{ID: "paused-prov", BaseURL: pausedSrv.URL, Adapter: "openai", Priority: 100},
			{ID: "active-prov", BaseURL: activeSrv.URL, Adapter: "openai", Priority: 90},
		},
	}
	if err := storage.SaveConfig(cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	storage.SaveCachedModels("paused-prov", []string{"paused-model-a"})
	storage.SaveCachedModels("active-prov", []string{"active-model-a"})

	autoBody := map[string]interface{}{
		"model": "auto",
		"messages": []interface{}{
			map[string]interface{}{"role": "user", "content": "hi"},
		},
	}
	bodyBytes, _ := json.Marshal(autoBody)
	req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewReader(bodyBytes))
	req.Header.Set("Authorization", "Bearer "+key)
	rec := httptest.NewRecorder()
	handleAutoProxy(rec, req, autoBody, cfg, "")

	if activeHits != 1 {
		t.Fatalf("auto should use active provider exactly once, got activeHits=%d pausedHits=%d", activeHits, pausedHits)
	}
	if pausedHits != 0 {
		t.Fatalf("auto must skip paused provider, got pausedHits=%d", pausedHits)
	}
}
