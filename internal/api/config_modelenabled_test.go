package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ai-gateway/internal/config"
	"ai-gateway/internal/db"
	"ai-gateway/internal/storage"
)

// setupModelEnabledTest bootstraps an in-memory SQLite gateway against a temp
// dir and returns a session cookie value so the caller can authenticate
// /api/config POST requests. It does NOT touch the network.
func setupModelEnabledTest(t *testing.T) (env *db.Env, cookie string) {
	tmpDir, err := os.MkdirTemp("", "ai-gateway-modelenabled")
	if err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(tmpDir, "gateway.db")
	os.Setenv("DB_PATH", dbPath)
	// Make sure no ambient proxy leaks into the local upstream fetch.
	for _, k := range []string{"HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy", "NO_PROXY", "no_proxy"} {
		os.Unsetenv(k)
	}
	env = db.InitStorage()
	storage.Init(env)
	sid, err := storage.CreateSession("admin")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		os.Unsetenv("DB_PATH")
		os.RemoveAll("data")
		os.RemoveAll(tmpDir)
	})
	return env, sid
}

// fakeModelsUpstream pretends to be an OpenAI-compatible /v1/models endpoint so
// the gateway can pull a model catalog locally (no external network needed).
func fakeModelsUpstream() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/models") {
			w.WriteHeader(404)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		// Return an OpenAI-shaped model list; credential is ignored, mirroring
		// NVIDIA's behaviour so the test isolates the toggle path from auth.
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"object": "list",
			"data": []map[string]interface{}{
				{"id": "model-a", "object": "model"},
				{"id": "model-b", "object": "model"},
				{"id": "model-c", "object": "model"},
			},
		})
	}))
}

func postConfig(t *testing.T, sid string, body map[string]interface{}) map[string]interface{} {
	payload, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/config", strings.NewReader(string(payload)))
	req.AddCookie(&http.Cookie{Name: "session_id", Value: sid})
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	HandleConfigPost(rec, req)
	if rec.Code != 200 {
		t.Fatalf("HandleConfigPost status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var out map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return out
}

// TestModelEnabledDefaultsOn verifies that models pulled during a config save
// are enabled by default (absent key OR explicit true => usable in Playground).
func TestModelEnabledDefaultsOn(t *testing.T) {
	_, sid := setupModelEnabledTest(t)
	up := fakeModelsUpstream()
	defer up.Close()

	// Register a custom provider pointing at the local upstream, with a key.
	custom := config.CustomProvider{
		ID:      "localtest",
		Label:   "Local Test",
		BaseURL: up.URL,
		Adapter: "openai",
		Key:     "sk-test",
	}
	cfg, _ := storage.GetConfig()
	cfg.CustomProviders = []config.CustomProvider{custom}
	cfg.Providers = []config.UserProvider{{Type: "localtest", Key: "sk-test"}}
	if err := storage.SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}

	out := postConfig(t, sid, map[string]interface{}{
		"providers":  []map[string]string{{"type": "localtest", "key": "sk-test"}},
		"modelEnabled": map[string]bool{},
	})
	// The POST response echoes the freshly fetched models.
	models, ok := out["models"].(map[string]interface{})
	if !ok || len(models["localtest"].([]interface{})) != 3 {
		t.Fatalf("expected 3 fetched models, got %v", out["models"])
	}

	// Reload config; all three models must default to enabled.
	reloaded, _ := storage.GetConfig()
	if reloaded.ModelEnabled == nil {
		t.Fatal("ModelEnabled map missing after save")
	}
	for _, m := range []string{"model-a", "model-b", "model-c"} {
		if v, seen := reloaded.ModelEnabled["localtest/"+m]; !seen || !v {
			t.Fatalf("model localtest/%s should default to enabled, got seen=%v val=%v", m, seen, v)
		}
	}
}

// TestModelEnabledTogglePersists verifies that disabling a model and saving
// persists the false state, and that a later partial update (only one key)
// does NOT re-enable the disabled model (merge semantics).
func TestModelEnabledTogglePersists(t *testing.T) {
	_, sid := setupModelEnabledTest(t)
	up := fakeModelsUpstream()
	defer up.Close()

	custom := config.CustomProvider{ID: "localtest", Label: "Local Test", BaseURL: up.URL, Adapter: "openai", Key: "sk-test"}
	cfg, _ := storage.GetConfig()
	cfg.CustomProviders = []config.CustomProvider{custom}
	cfg.Providers = []config.UserProvider{{Type: "localtest", Key: "sk-test"}}
	storage.SaveConfig(cfg)

	// First save: disable model-b explicitly, leave the rest default.
	postConfig(t, sid, map[string]interface{}{
		"providers": []map[string]string{{"type": "localtest", "key": "sk-test"}},
		"modelEnabled": map[string]bool{
			"localtest/model-b": false,
		},
	})

	reloaded, _ := storage.GetConfig()
	if reloaded.ModelEnabled["localtest/model-b"] != false {
		t.Fatalf("model-b should be disabled after first save")
	}
	if !reloaded.ModelEnabled["localtest/model-a"] {
		t.Fatalf("model-a should remain enabled by default")
	}

	// Second save: only toggle model-c off. model-b must stay off (merge).
	postConfig(t, sid, map[string]interface{}{
		"providers": []map[string]string{{"type": "localtest", "key": "sk-test"}},
		"modelEnabled": map[string]bool{
			"localtest/model-c": false,
		},
	})

	reloaded, _ = storage.GetConfig()
	if reloaded.ModelEnabled["localtest/model-b"] != false {
		t.Fatalf("merge must not re-enable model-b")
	}
	if reloaded.ModelEnabled["localtest/model-c"] != false {
		t.Fatalf("model-c should now be disabled")
	}
	if !reloaded.ModelEnabled["localtest/model-a"] {
		t.Fatalf("model-a should still be enabled")
	}
}

// TestModelEnabledGetEcho verifies the GET /api/config response carries the
// modelEnabled map so the dashboard can render the toggles on load.
func TestModelEnabledGetEcho(t *testing.T) {
	_, sid := setupModelEnabledTest(t)
	up := fakeModelsUpstream()
	defer up.Close()

	custom := config.CustomProvider{ID: "localtest", Label: "Local Test", BaseURL: up.URL, Adapter: "openai", Key: "sk-test"}
	cfg, _ := storage.GetConfig()
	cfg.CustomProviders = []config.CustomProvider{custom}
	cfg.Providers = []config.UserProvider{{Type: "localtest", Key: "sk-test"}}
	cfg.ModelEnabled = map[string]bool{"localtest/model-a": false}
	storage.SaveConfig(cfg)

	// Trigger one save so models get pulled + default-enabled.
	postConfig(t, sid, map[string]interface{}{
		"providers":    []map[string]string{{"type": "localtest", "key": "sk-test"}},
		"modelEnabled": map[string]bool{"localtest/model-a": false},
	})

	req := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	req.AddCookie(&http.Cookie{Name: "session_id", Value: sid})
	rec := httptest.NewRecorder()
	HandleConfigGet(rec, req)
	if rec.Code != 200 {
		t.Fatalf("HandleConfigGet status = %d", rec.Code)
	}
	var got map[string]interface{}
	json.Unmarshal(rec.Body.Bytes(), &got)
	meRaw, ok := got["modelEnabled"]
	if !ok {
		t.Fatal("GET /api/config missing modelEnabled")
	}
	me := meRaw.(map[string]interface{})
	if me["localtest/model-a"] != false {
		t.Fatalf("GET should echo disabled model-a; got %v", me["localtest/model-a"])
	}
	if me["localtest/model-b"] != true {
		t.Fatalf("GET should echo default-enabled model-b; got %v", me["localtest/model-b"])
	}
}
