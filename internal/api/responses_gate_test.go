package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ai-gateway/internal/config"
	"ai-gateway/internal/storage"
)

// postResponses issues an authenticated POST /v1/responses. The provider gates
// under test run before any upstream call, so no network is involved.
func postResponses(t *testing.T, unifiedKey, model string) *httptest.ResponseRecorder {
	t.Helper()
	payload, _ := json.Marshal(map[string]interface{}{
		"model": model,
		"input": "hi",
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(string(payload)))
	req.Header.Set("Authorization", "Bearer "+unifiedKey)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	HandleResponses(rec, req)
	return rec
}

// TestResponsesRejectsPausedProvider locks in that the Responses API enforces
// the same provider-pause gate as the chat proxy. Before the fix, a paused
// provider stayed reachable through /v1/responses even though /v1/chat/
// completions rejected it — an inconsistency that made the pause toggle leaky.
func TestResponsesRejectsPausedProvider(t *testing.T) {
	_, _ = setupModelEnabledTest(t)

	cfg, err := storage.GetConfig()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Providers = []config.UserProvider{{Type: "custom-x", Key: "k", Paused: true}}
	cfg.CustomProviders = []config.CustomProvider{{
		ID: "custom-x", Label: "X", BaseURL: "https://example.invalid/v1",
		Models: []string{"m1"},
	}}
	if err := storage.SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}

	rec := postResponses(t, cfg.UnifiedKey, "m1")
	if rec.Code != 404 {
		t.Fatalf("paused provider: status = %d, want 404 (body=%s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "paused") {
		t.Fatalf("paused provider: body = %s, want it to mention paused", rec.Body.String())
	}
}

// TestResponsesRejectsHiddenModel locks in that a model hidden/disabled in the
// dashboard cannot be called through the Responses API, matching the chat path.
func TestResponsesRejectsHiddenModel(t *testing.T) {
	_, _ = setupModelEnabledTest(t)

	cfg, err := storage.GetConfig()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Providers = []config.UserProvider{{Type: "custom-y", Key: "k"}}
	cfg.CustomProviders = []config.CustomProvider{{
		ID: "custom-y", Label: "Y", BaseURL: "https://example.invalid/v1",
		Models: []string{"m2"},
	}}
	cfg.ModelEnabled = map[string]bool{"custom-y/m2": false}
	if err := storage.SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}

	// Both the provider-prefixed and the bare form must be rejected: the gate
	// normalizes "provider/model" to the bare id the ModelEnabled map stores.
	for _, model := range []string{"m2", "custom-y/m2"} {
		rec := postResponses(t, cfg.UnifiedKey, model)
		if rec.Code != 404 {
			t.Fatalf("hidden model %q: status = %d, want 404 (body=%s)", model, rec.Code, rec.Body.String())
		}
		// The body intentionally echoes the requested model string; assert on
		// the "hidden" marker only (the echoed name is checked separately).
		var out struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		if !strings.Contains(out.Error, "hidden") {
			t.Fatalf("hidden model %q: error = %q, want it to mention hidden", model, out.Error)
		}
		if !strings.Contains(out.Error, model) {
			t.Fatalf("hidden model %q: error = %q, want it to echo the requested model", model, out.Error)
		}
	}
}

// TestResponsesBodyLimitRejected verifies the request-body cap: an oversized
// payload is refused rather than buffered without bound.
func TestResponsesBodyLimitRejected(t *testing.T) {
	_, _ = setupModelEnabledTest(t)

	cfg, err := storage.GetConfig()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Providers = []config.UserProvider{{Type: "custom-z", Key: "k"}}
	cfg.CustomProviders = []config.CustomProvider{{
		ID: "custom-z", Label: "Z", BaseURL: "https://example.invalid/v1",
		Models: []string{"m3"},
	}}
	if err := storage.SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}

	// Build a valid JSON body that exceeds the 32 MiB cap.
	big := strings.Repeat("a", maxResponsesBody+1024)
	payload := `{"model":"m3","input":"` + big + `"}`

	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(payload))
	req.Header.Set("Authorization", "Bearer "+cfg.UnifiedKey)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	HandleResponses(rec, req)

	// Over-limit bodies surface as a decode error => 400 (not a crash/OOM).
	if rec.Code != 400 {
		t.Fatalf("oversized body: status = %d, want 400 (body=%s)", rec.Code, rec.Body.String())
	}
}
