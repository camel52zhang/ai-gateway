package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeModelsUpstreamWith pretends to be an OpenAI-compatible /v1/models endpoint
// returning the given model ids. Used to verify manual models are merged with
// (or substituted for) the upstream catalog without touching the real network.
func fakeModelsUpstreamWith(ids []string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/models") {
			w.WriteHeader(404)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		data := make([]map[string]interface{}, 0, len(ids))
		for _, id := range ids {
			data = append(data, map[string]interface{}{"id": id, "object": "model"})
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"object": "list", "data": data})
	}))
}

func getModels(t *testing.T, sid, pType string) (models []string, code int) {
	req := httptest.NewRequest(http.MethodGet, "/api/models?type="+pType, nil)
	req.AddCookie(&http.Cookie{Name: "session_id", Value: sid})
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	HandleModels(rec, req)
	if rec.Code != 200 {
		return nil, rec.Code
	}
	var body struct {
		Models []string `json:"models"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode /api/models response: %v body=%s", err, rec.Body.String())
	}
	return body.Models, rec.Code
}

// TestCustomProviderManualModelsMerged verifies that a custom provider's
// manually-entered model list ("模型列表（逗号分隔）") is merged with the
// upstream catalog and both appear in /api/models.
func TestCustomProviderManualModelsMerged(t *testing.T) {
	_, sid := setupModelEnabledTest(t)
	upstream := fakeModelsUpstreamWith([]string{"model-a", "model-b"})
	defer upstream.Close()
	// baseUrl ends with /v1 so fetchOpenAIModels hits <base>/models (the fake).
	baseURL := upstream.URL + "/v1"

	rec := postCustomProvider(t, sid, map[string]interface{}{
		"id":      "zhipu",
		"label":   "Zhipu",
		"baseUrl": baseURL,
		"adapter": "openai",
		"key":     "sk-test",
		"models":  []string{"glm-4.7-flash"},
	})
	if rec.Code != 200 {
		t.Fatalf("expected 200 adding custom provider, got %d body=%s", rec.Code, rec.Body.String())
	}

	models, code := getModels(t, sid, "zhipu")
	if code != 200 {
		t.Fatalf("expected 200 from /api/models, got %d", code)
	}
	if !contains(models, "glm-4.7-flash") {
		t.Fatalf("manual model glm-4.7-flash missing from /api/models; got %v", models)
	}
	if !contains(models, "model-a") {
		t.Fatalf("upstream model model-a missing from merged list; got %v", models)
	}
}

// TestCustomProviderManualModelsWhenUpstreamEmpty verifies that when the
// upstream /models endpoint returns nothing (e.g. a free model not listed by
// the provider's API), the manually-entered model still appears.
func TestCustomProviderManualModelsWhenUpstreamEmpty(t *testing.T) {
	_, sid := setupModelEnabledTest(t)
	upstream := fakeModelsUpstreamWith(nil) // returns an empty data array
	defer upstream.Close()
	baseURL := upstream.URL + "/v1"

	rec := postCustomProvider(t, sid, map[string]interface{}{
		"id":      "zhipu",
		"label":   "Zhipu",
		"baseUrl": baseURL,
		"adapter": "openai",
		"key":     "sk-test",
		"models":  []string{"glm-4.7-flash", "glm-4.6-flash"},
	})
	if rec.Code != 200 {
		t.Fatalf("expected 200 adding custom provider, got %d body=%s", rec.Code, rec.Body.String())
	}

	models, code := getModels(t, sid, "zhipu")
	if code != 200 {
		t.Fatalf("expected 200 from /api/models, got %d", code)
	}
	if !contains(models, "glm-4.7-flash") || !contains(models, "glm-4.6-flash") {
		t.Fatalf("manual models missing when upstream empty; got %v", models)
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
