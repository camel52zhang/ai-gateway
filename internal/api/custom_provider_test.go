package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"ai-gateway/internal/storage"
)

func postCustomProvider(t *testing.T, sid string, body map[string]interface{}) *httptest.ResponseRecorder {
	payload, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/providers/custom", bytes.NewReader(payload))
	req.AddCookie(&http.Cookie{Name: "session_id", Value: sid})
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	HandleCustomProvidersPost(rec, req)
	return rec
}

// TestCustomProviderAdd verifies that a brand-new custom provider with a valid
// unique ID is accepted (the user reported the ID field could not be filled,
// which blocked adding any custom provider). The backend must accept it.
func TestCustomProviderAdd(t *testing.T) {
	_, sid := setupModelEnabledTest(t)

	rec := postCustomProvider(t, sid, map[string]interface{}{
		"id":      "my-custom",
		"label":   "My Custom",
		"baseUrl": "https://api.example.com/v1",
		"adapter": "openai",
	})
	if rec.Code != 200 {
		t.Fatalf("expected 200 adding custom provider, got %d body=%s", rec.Code, rec.Body.String())
	}

	cfg, _ := storage.GetConfig()
	found := false
	for _, cp := range cfg.CustomProviders {
		if cp.ID == "my-custom" && cp.Label == "My Custom" && cp.BaseURL == "https://api.example.com/v1" {
			found = true
		}
	}
	if !found {
		t.Fatalf("custom provider not persisted: %+v", cfg.CustomProviders)
	}
}

// TestCustomProviderAddBuiltinConflict verifies that choosing an ID that
// collides with a built-in provider is rejected with 409 (clear error, not a
// silent "can't add"). This is the most likely backend cause of an add failure
// when the user types a reserved name like "openai" or "nvidia".
func TestCustomProviderAddBuiltinConflict(t *testing.T) {
	_, sid := setupModelEnabledTest(t)

	rec := postCustomProvider(t, sid, map[string]interface{}{
		"id":      "openai", // built-in provider id
		"label":   "OpenAI Clone",
		"baseUrl": "https://api.example.com/v1",
	})
	if rec.Code != 409 {
		t.Fatalf("expected 409 for built-in id collision, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestCustomProviderAddMissingFields verifies required-field validation returns
// 400 with a clear message (id / label / baseUrl mandatory on creation).
func TestCustomProviderAddMissingFields(t *testing.T) {
	_, sid := setupModelEnabledTest(t)

	cases := []map[string]interface{}{
		{"label": "X", "baseUrl": "https://x/v1"},                       // missing id
		{"id": "x", "baseUrl": "https://x/v1"},                          // missing label
		{"id": "x", "label": "X"},                                       // missing baseUrl
	}
	for i, body := range cases {
		rec := postCustomProvider(t, sid, body)
		if rec.Code != 400 {
			t.Fatalf("case %d: expected 400 for missing fields, got %d body=%s", i, rec.Code, rec.Body.String())
		}
	}
}
