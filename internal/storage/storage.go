package storage

import (
	"encoding/json"
	"net/http"
	"sync"

	"ai-gateway/internal/config"
	"ai-gateway/internal/db"
	"ai-gateway/internal/utils"
)

const (
	KeyConfig         = "gateway_config"
	KeyStats          = "gw_stats"
	KeyRequestLog     = "gw_request_log"
	KeyErrorLog       = "gw_error_log"
	KeyLatency        = "gw_latency"
	KeyHealth         = "gw_health"
	KeyFailureMetrics = "gw_failure_metrics"
	KeyModels         = "gw_models"
	SessionTTL        = 86400
	MaxRequestLog     = 200
	MaxErrorLog       = 100
)

var envRef *db.Env

// rmwMu serializes the read-modify-write KV operations (stats, logs, latency,
// health, cached models). Without it, concurrent proxy requests would race on the
// get-modify-put sequence and silently lose updates.
var rmwMu sync.Mutex

func Init(env *db.Env) { envRef = env }

func GetEnv() *db.Env { return envRef }

// --- Config ---

// configGenerateMu serializes the first-run unified-key generation so two
// concurrent initial requests cannot each mint a different key and clobber one
// another (which would make the first client's Bearer key intermittently 401).
var configGenerateMu sync.Mutex

// configCache keeps an in-memory, DB-coherent snapshot of the gateway config so
// the hot proxy path doesn't re-read SQLite + re-parse the full blob on every
// request. It is populated on load and kept in sync by SaveConfig. Reads use an
// RWMutex; GetConfig returns an independent clone so callers that mutate their
// copy (e.g. HandleConfigPost) can never corrupt the shared snapshot.
var (
	configCacheMu sync.RWMutex
	configCache   *config.Config
	configCached  bool
)

func GetConfig() (*config.Config, error) {
	// Fast path: serve a snapshot of the cached config. This is the common case
	// for proxy traffic and avoids a SQLite read + full JSON parse per request.
	configCacheMu.RLock()
	cached := configCache
	ready := configCached
	configCacheMu.RUnlock()
	if ready && cached != nil {
		return cloneConfig(cached), nil
	}

	// Slow path: load from storage.
	cfg := config.DefaultConfig()
	var raw json.RawMessage
	if err := db.KVGet(KeyConfig, &raw); err != nil {
		return nil, err
	}
	if raw != nil {
		json.Unmarshal(raw, cfg)
	}
	if cfg.UnifiedKey != "" {
		configCacheMu.Lock()
		configCache = cloneConfig(cfg)
		configCached = true
		configCacheMu.Unlock()
		return cloneConfig(cfg), nil
	}

	// First run: generate the unified key. Serialize so concurrent first reads
	// don't each generate a different key; re-read under the lock in case
	// another caller already persisted it.
	configGenerateMu.Lock()
	defer configGenerateMu.Unlock()
	reloaded := config.DefaultConfig()
	var raw2 json.RawMessage
	if db.KVGet(KeyConfig, &raw2) == nil && raw2 != nil {
		json.Unmarshal(raw2, reloaded)
	}
	if reloaded.UnifiedKey == "" {
		reloaded.UnifiedKey = utils.GenerateToken("sk-gw-")
		SaveConfig(reloaded)
	}
	return reloaded, nil
}

func SaveConfig(cfg *config.Config) error {
	err := db.KVPut(KeyConfig, cfg)
	if err == nil {
		configCacheMu.Lock()
		configCache = cloneConfig(cfg)
		configCached = true
		configCacheMu.Unlock()
	}
	return err
}

// cloneConfig returns a deep, independent copy of c. The config blob is stored
// as JSON in the DB, so a JSON round-trip is the simplest correct deep copy and
// stays correct even as the struct evolves. Callers that mutate their returned
// copy therefore never affect the cached snapshot or other callers.
func cloneConfig(c *config.Config) *config.Config {
	if c == nil {
		return nil
	}
	b, err := json.Marshal(c)
	if err != nil {
		cp := *c
		return &cp
	}
	out := &config.Config{}
	if err := json.Unmarshal(b, out); err != nil {
		cp := *c
		return &cp
	}
	return out
}

// --- Runtime getters (read from dedicated KV keys, NOT from the config blob) ---

func GetStats() config.Stats {
	var s config.Stats
	var raw json.RawMessage
	if db.KVGet(KeyStats, &raw) == nil && raw != nil {
		json.Unmarshal(raw, &s)
	}
	return s
}

func GetRequestLog() []config.RequestLogEntry {
	var log_ []config.RequestLogEntry
	var raw json.RawMessage
	if db.KVGet(KeyRequestLog, &raw) == nil && raw != nil {
		json.Unmarshal(raw, &log_)
	}
	return log_
}

func GetErrorLog() []config.ErrorLogEntry {
	var log_ []config.ErrorLogEntry
	var raw json.RawMessage
	if db.KVGet(KeyErrorLog, &raw) == nil && raw != nil {
		json.Unmarshal(raw, &log_)
	}
	return log_
}

func GetProviderLatency() map[string]int {
	var m map[string]int
	var raw json.RawMessage
	if db.KVGet(KeyLatency, &raw) == nil && raw != nil {
		json.Unmarshal(raw, &m)
	}
	if m == nil {
		m = make(map[string]int)
	}
	return m
}

func GetProviderHealth() map[string]string {
	var m map[string]string
	var raw json.RawMessage
	if db.KVGet(KeyHealth, &raw) == nil && raw != nil {
		json.Unmarshal(raw, &m)
	}
	if m == nil {
		m = make(map[string]string)
	}
	return m
}

func GetFailureMetrics() map[string]config.FailureMetrics {
	var m map[string]config.FailureMetrics
	var raw json.RawMessage
	if db.KVGet(KeyFailureMetrics, &raw) == nil && raw != nil {
		json.Unmarshal(raw, &m)
	}
	if m == nil {
		m = make(map[string]config.FailureMetrics)
	}
	return m
}

// --- Session ---

func CreateSession(username string) (string, error) {
	sid := utils.GenerateSessionID()
	return sid, db.KVPut("session:"+sid, username, SessionTTL)
}

func GetSession(sid string) (string, error) {
	var username string
	if err := db.KVGet("session:"+sid, &username); err != nil {
		return "", err
	}
	return username, nil
}

func DeleteSession(sid string) error {
	return db.KVDelete("session:" + sid)
}

// --- Stats ---

func IncrementStats(promptTokens, completionTokens int64) {
	rmwMu.Lock()
	defer rmwMu.Unlock()

	var s config.Stats
	var raw json.RawMessage
	db.KVGet(KeyStats, &raw)
	if raw != nil {
		json.Unmarshal(raw, &s)
	}
	s.Requests++
	s.PromptTokens += promptTokens
	s.CompletionTokens += completionTokens
	s.Tokens += promptTokens + completionTokens
	db.KVPut(KeyStats, s)
}

// --- Request Log ---

func AppendRequestLog(entry config.RequestLogEntry) {
	rmwMu.Lock()
	defer rmwMu.Unlock()

	var log_ []config.RequestLogEntry
	var raw json.RawMessage
	db.KVGet(KeyRequestLog, &raw)
	if raw != nil {
		json.Unmarshal(raw, &log_)
	}
	log_ = append([]config.RequestLogEntry{entry}, log_...)
	if len(log_) > MaxRequestLog {
		log_ = log_[:MaxRequestLog]
	}
	db.KVPut(KeyRequestLog, log_)
}

// --- Error Log ---

func AppendErrorLog(entry config.ErrorLogEntry) {
	rmwMu.Lock()
	defer rmwMu.Unlock()

	var log_ []config.ErrorLogEntry
	var raw json.RawMessage
	db.KVGet(KeyErrorLog, &raw)
	if raw != nil {
		json.Unmarshal(raw, &log_)
	}
	log_ = append([]config.ErrorLogEntry{entry}, log_...)
	if len(log_) > MaxErrorLog {
		log_ = log_[:MaxErrorLog]
	}
	db.KVPut(KeyErrorLog, log_)
}

// --- Provider latency ---

func UpdateProviderLatency(providerType string, latencyMs int) {
	rmwMu.Lock()
	defer rmwMu.Unlock()

	var lat map[string]int
	var raw json.RawMessage
	db.KVGet(KeyLatency, &raw)
	if raw != nil {
		json.Unmarshal(raw, &lat)
	}
	if lat == nil {
		lat = make(map[string]int)
	}
	lat[providerType] = latencyMs
	db.KVPut(KeyLatency, lat)
}

// --- Provider health ---

func UpdateProviderHealth(providerType string, healthy bool) {
	rmwMu.Lock()
	defer rmwMu.Unlock()

	var health map[string]string
	var raw json.RawMessage
	db.KVGet(KeyHealth, &raw)
	if raw != nil {
		json.Unmarshal(raw, &health)
	}
	if health == nil {
		health = make(map[string]string)
	}
	if healthy {
		health[providerType] = "healthy"
	} else {
		health[providerType] = "degraded"
	}
	db.KVPut(KeyHealth, health)
}

// --- Failure metrics ---

var fmMu sync.Mutex

func RecordFailureMetric(providerType, category string) {
	fmMu.Lock()
	defer fmMu.Unlock()
	var fm map[string]config.FailureMetrics
	var raw json.RawMessage
	db.KVGet(KeyFailureMetrics, &raw)
	if raw != nil {
		json.Unmarshal(raw, &fm)
	}
	if fm == nil {
		fm = make(map[string]config.FailureMetrics)
	}
	e := fm[providerType]
	e.Total++
	if e.Categories == nil {
		e.Categories = make(map[string]int)
	}
	e.Categories[category]++
	fm[providerType] = e
	db.KVPut(KeyFailureMetrics, fm)
}

// --- Models cache ---

func GetCachedModels(providerType string) ([]string, error) {
	var all map[string][]string
	var raw json.RawMessage
	if err := db.KVGet(KeyModels, &raw); err != nil {
		return nil, err
	}
	if raw != nil {
		json.Unmarshal(raw, &all)
	}
	return all[providerType], nil
}

func SaveCachedModels(providerType string, models []string) {
	rmwMu.Lock()
	defer rmwMu.Unlock()

	var all map[string][]string
	var raw json.RawMessage
	db.KVGet(KeyModels, &raw)
	if raw != nil {
		json.Unmarshal(raw, &all)
	}
	if all == nil {
		all = make(map[string][]string)
	}
	all[providerType] = models
	db.KVPut(KeyModels, all)
}

// DeleteCachedModels removes the cached model list for a single provider type
// (e.g. when its key is removed or the provider is deleted) so stale models do
// not linger in the dashboard.
func DeleteCachedModels(providerType string) {
	rmwMu.Lock()
	defer rmwMu.Unlock()

	var all map[string][]string
	var raw json.RawMessage
	db.KVGet(KeyModels, &raw)
	if raw != nil {
		json.Unmarshal(raw, &all)
	}
	if all == nil {
		return
	}
	if _, ok := all[providerType]; !ok {
		return
	}
	delete(all, providerType)
	db.KVPut(KeyModels, all)
}

// --- IsAuthenticated ---

func IsAuthenticated(r *http.Request) bool {
	cookie, err := r.Cookie("session_id")
	if err != nil || cookie.Value == "" {
		return false
	}
	username, _ := GetSession(cookie.Value)
	return username != ""
}
