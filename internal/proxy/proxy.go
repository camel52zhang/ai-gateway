package proxy

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"ai-gateway/internal/adapters"
	"ai-gateway/internal/config"
	"ai-gateway/internal/db"
	"ai-gateway/internal/providers"
	"ai-gateway/internal/storage"
	"ai-gateway/internal/utils"
)

// =============================================================================
// Circuit Breaker
// =============================================================================

type CircuitBreaker struct {
	mu       sync.Mutex
	failures map[string]int
	open     map[string]time.Time
}

var cb = &CircuitBreaker{
	failures: make(map[string]int),
	open:     make(map[string]time.Time),
}

const (
	cbThreshold = 5
	cbCooldown  = 60 * time.Second
)

func (c *CircuitBreaker) IsOpen(providerType string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	openAt, ok := c.open[providerType]
	if !ok {
		return false
	}
	if time.Since(openAt) > cbCooldown {
		delete(c.open, providerType)
		delete(c.failures, providerType)
		return false
	}
	return true
}

func (c *CircuitBreaker) RecordSuccess(providerType string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.failures, providerType)
	delete(c.open, providerType)
}

func (c *CircuitBreaker) RecordFailure(providerType string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.failures[providerType]++
	if c.failures[providerType] >= cbThreshold {
		c.open[providerType] = time.Now()
	}
}

// =============================================================================
// Rate Limiter (in-memory, per-provider)
// =============================================================================

type RateLimiter struct {
	mu       sync.Mutex
	counters map[string]*rateLimitEntry
}

type rateLimitEntry struct {
	count   int
	resetAt time.Time
}

var rl = &RateLimiter{counters: make(map[string]*rateLimitEntry)}

const (
	rlWindowMs = 60000
	rlMax      = 100
)

func (r *RateLimiter) IsLimited(providerType string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	e, ok := r.counters[providerType]
	if !ok || now.After(e.resetAt) {
		r.counters[providerType] = &rateLimitEntry{count: 1, resetAt: now.Add(rlWindowMs * time.Millisecond)}
		return false
	}
	if e.count >= rlMax {
		return true
	}
	e.count++
	return false
}

// =============================================================================
// Error classification (matches Node.js classifyError)
// =============================================================================

func classifyError(err error, statusCode int) string {
	if err == nil {
		if statusCode >= 500 {
			return "upstream_error"
		}
		if statusCode == 429 {
			return "rate_limit"
		}
		if statusCode >= 400 {
			return "client_error"
		}
		return "unknown"
	}

	msg := err.Error()
	if strings.Contains(msg, "timeout") || strings.Contains(msg, "deadline exceeded") {
		return "timeout"
	}
	if statusCode == 429 || strings.Contains(strings.ToLower(msg), "rate limit") {
		return "rate_limit"
	}
	if statusCode >= 500 || strings.Contains(strings.ToLower(msg), "upstream") {
		return "upstream_error"
	}
	if statusCode >= 400 {
		return "client_error"
	}
	return "unknown"
}

func buildErrorEvent(provider, model, category, requestID string, status int, message, body string) config.ErrorLogEntry {
	return config.ErrorLogEntry{
		Timestamp: utils.NowISO(),
		Provider:  provider,
		Model:     model,
		Category:  category,
		RequestID: requestID,
		Status:    status,
		Message:   message,
		Body:      body,
	}
}

// =============================================================================
// GET /v1/models — list models (OpenAI compatible)
// =============================================================================

func HandleListModels(w http.ResponseWriter, r *http.Request) {
	authHeader := r.Header.Get("Authorization")
	var providedToken string
	if len(authHeader) > 7 && authHeader[:7] == "Bearer " {
		providedToken = authHeader[7:]
	}

	cfg, err := storage.GetConfig()
	if err != nil || providedToken == "" || !utils.TimingSafeCompare(providedToken, cfg.UnifiedKey) {
		utils.JSON(w, 401, map[string]string{"error": "Unauthorized"})
		return
	}

	var models []map[string]string
	cached := loadAllCachedModels()

	for _, p := range cfg.Providers {
		cachedModels, ok := cached[p.Type]
		if ok && len(cachedModels) > 0 {
			for _, m := range cachedModels {
				models = append(models, map[string]string{
					"id":       m,
					"object":   "model",
					"owned_by": p.Type,
				})
			}
		} else if p.Key != "" {
			def, defOk := providers.ResolveDefinition(p.Type, cfg.CustomProviders)
			if defOk && def.BaseURL != "" {
				fetched := adapters.FetchProviderModels(def, p.Key)
				if len(fetched) > 0 {
					cached[p.Type] = fetched
					storage.SaveCachedModels(p.Type, fetched)
					for _, m := range fetched {
						models = append(models, map[string]string{
							"id":       m,
							"object":   "model",
							"owned_by": p.Type,
						})
					}
				}
			}
		}
	}

	if models == nil {
		models = []map[string]string{}
	}

	utils.JSON(w, 200, map[string]interface{}{
		"object": "list",
		"data":   models,
	})
}

// =============================================================================
// POST /v1/chat/completions — main proxy entry point
// =============================================================================

func HandleProxy(w http.ResponseWriter, r *http.Request) {
	bodyBytes, err := io.ReadAll(r.Body)
	r.Body.Close()
	if err != nil {
		utils.JSON(w, 400, map[string]string{"error": "Failed to read body"})
		return
	}

	authHeader := r.Header.Get("Authorization")
	var providedToken string
	if len(authHeader) > 7 && authHeader[:7] == "Bearer " {
		providedToken = authHeader[7:]
	}

	cfg, err := storage.GetConfig()
	if err != nil {
		utils.JSON(w, 500, map[string]string{"error": "Config error"})
		return
	}

	if providedToken == "" || !utils.TimingSafeCompare(providedToken, cfg.UnifiedKey) {
		utils.JSON(w, 401, map[string]string{"error": "Unauthorized. Provide a valid API key via Authorization: Bearer <key>"})
		return
	}

	var body map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &body); err != nil {
		utils.JSON(w, 400, map[string]string{"error": "Invalid JSON in request body"})
		return
	}

	model, ok := body["model"].(string)
	if !ok {
		utils.JSON(w, 400, map[string]string{"error": "model field is required"})
		return
	}

	if len(cfg.Providers) == 0 {
		utils.JSON(w, 400, map[string]string{"error": "No providers configured. Add API keys in the dashboard first."})
		return
	}

	requestID := r.Header.Get("X-Request-ID")

	if model == "auto" {
		handleAutoProxy(w, r, body, cfg, requestID)
		return
	}

	provider := providers.ResolveProvider(model, cfg.Providers, cfg.CustomProviders)
	if provider == nil {
		utils.JSON(w, 404, map[string]string{"error": "No matching provider found for model: " + model})
		return
	}

	if cb.IsOpen(provider.Type) {
		fallback := providers.GetFallbackProvider(model, cfg.Providers, provider.Type, cfg.CustomProviders)
		if fallback == nil {
			w.Header().Set("Retry-After", "60")
			utils.JSON(w, 503, map[string]string{
				"error": "Provider " + provider.Type + " is temporarily unavailable due to repeated failures.",
			})
			return
		}
		log.Printf("[proxy] Circuit open for %s, failing over to %s", provider.Type, fallback.Type)
		provider = fallback
	}

	if rl.IsLimited(provider.Type) {
		w.Header().Set("Retry-After", "60")
		utils.JSON(w, 429, map[string]string{"error": "Rate limit exceeded for provider: " + provider.Type})
		return
	}

	executeProxy(w, r, bodyBytes, body, provider, cfg, 0, requestID)
}

// =============================================================================
// executeProxy — recursive proxy execution with retry and failover
// =============================================================================

const MAX_RETRIES = 2

func executeProxy(
	w http.ResponseWriter,
	r *http.Request,
	originalBody []byte,
	body map[string]interface{},
	provider *config.UserProvider,
	cfg *config.Config,
	retry int,
	requestID string,
) {
	model := ""
	if m, ok := body["model"].(string); ok {
		model = m
	}

	isStreaming := false
	if s, ok := body["stream"].(bool); ok && s {
		isStreaming = true
	}

	if isStreaming {
		executeStreamingProxy(w, r, originalBody, body, provider, cfg, retry, requestID, model)
		return
	}

	executeNonStreamingProxy(w, r, originalBody, body, provider, cfg, retry, requestID, model)
}

// executeStreamingProxy handles SSE streaming responses
func executeStreamingProxy(
	w http.ResponseWriter,
	r *http.Request,
	originalBody []byte,
	body map[string]interface{},
	provider *config.UserProvider,
	cfg *config.Config,
	retry int,
	requestID string,
	model string,
) {
	def, _ := providers.ResolveDefinition(provider.Type, cfg.CustomProviders)
	upstreamResp, err := adapters.StreamingProxyWithProvider(body, def, provider.Key)
	if err != nil {
		category := classifyError(err, 0)
		cb.RecordFailure(provider.Type)
		go storage.UpdateProviderHealth(provider.Type, false)
		go storage.RecordFailureMetric(provider.Type, category)
		go storage.AppendErrorLog(buildErrorEvent(provider.Type, model, category, requestID, 0, err.Error(), ""))

		if retry < MAX_RETRIES {
			fallback := providers.GetFallbackProvider(model, cfg.Providers, provider.Type, cfg.CustomProviders)
			if fallback != nil {
				log.Printf("[proxy] Streaming network error for %s, failing over to %s (retry %d)", provider.Type, fallback.Type, retry+1)
				go storage.AppendErrorLog(config.ErrorLogEntry{
					Timestamp: utils.NowISO(),
					Provider:  provider.Type,
					Model:     model,
					Message:   fmt.Sprintf("Network error, failing over to %s", fallback.Type),
				})
				executeProxy(w, r, originalBody, body, fallback, cfg, retry+1, requestID)
				return
			}
		}

		utils.JSON(w, 502, map[string]string{"error": "Proxy failed: " + err.Error()})
		return
	}
	defer upstreamResp.Body.Close()

	statusCode := upstreamResp.StatusCode

	if statusCode >= 200 && statusCode < 300 {
		cb.RecordSuccess(provider.Type)
		go storage.UpdateProviderHealth(provider.Type, true)

		for k, v := range upstreamResp.Header {
			for _, vv := range v {
				w.Header().Add(k, vv)
			}
		}
		w.WriteHeader(statusCode)

		copyErr := copyWithFlush(w, upstreamResp.Body)
		if copyErr != nil {
			log.Printf("[proxy] Streaming copy error for %s: %v", provider.Type, copyErr)
		}
		return
	}

	respBody, _ := io.ReadAll(upstreamResp.Body)
	bodyStr := utils.Truncate(string(respBody), 200)

	category := classifyError(nil, statusCode)
	go storage.AppendErrorLog(buildErrorEvent(provider.Type, model, category, requestID, statusCode, "", bodyStr))
	go storage.RecordFailureMetric(provider.Type, category)

	if statusCode >= 400 && statusCode < 500 {
		// Client/request error (bad key, missing model, malformed request): do
		// NOT trip the circuit breaker — only network errors and 5xx do.
		w.WriteHeader(statusCode)
		w.Write(respBody)
		return
	}

	cb.RecordFailure(provider.Type)
	go storage.UpdateProviderHealth(provider.Type, false)

	if retry < MAX_RETRIES {
		fallback := providers.GetFallbackProvider(model, cfg.Providers, provider.Type, cfg.CustomProviders)
		if fallback != nil {
			log.Printf("[proxy] Streaming error %d for %s, failing over to %s (retry %d)", statusCode, provider.Type, fallback.Type, retry+1)
			go storage.AppendErrorLog(config.ErrorLogEntry{
				Timestamp: utils.NowISO(),
				Provider:  provider.Type,
				Model:     model,
				Status:    statusCode,
				Message:   fmt.Sprintf("Failing over to %s (retry %d)", fallback.Type, retry+1),
			})
			executeProxy(w, r, originalBody, body, fallback, cfg, retry+1, requestID)
			return
		}
		executeProxy(w, r, originalBody, body, provider, cfg, retry+1, requestID)
		return
	}

	w.WriteHeader(statusCode)
	w.Write(respBody)
}

// executeNonStreamingProxy handles non-streaming requests
func executeNonStreamingProxy(
	w http.ResponseWriter,
	r *http.Request,
	originalBody []byte,
	body map[string]interface{},
	provider *config.UserProvider,
	cfg *config.Config,
	retry int,
	requestID string,
	model string,
) {
	def, _ := providers.ResolveDefinition(provider.Type, cfg.CustomProviders)
	result, err := adapters.ProxyWithProvider(body, def, provider.Key)
	if err != nil {
		category := classifyError(err, 0)
		cb.RecordFailure(provider.Type)
		go storage.UpdateProviderHealth(provider.Type, false)
		go storage.RecordFailureMetric(provider.Type, category)
		go storage.AppendErrorLog(buildErrorEvent(provider.Type, model, category, requestID, 0, err.Error(), ""))

		if retry < MAX_RETRIES {
			fallback := providers.GetFallbackProvider(model, cfg.Providers, provider.Type, cfg.CustomProviders)
			if fallback != nil {
				log.Printf("[proxy] Network error for %s, failing over to %s (retry %d)", provider.Type, fallback.Type, retry+1)
				go storage.AppendErrorLog(config.ErrorLogEntry{
					Timestamp: utils.NowISO(),
					Provider:  provider.Type,
					Model:     model,
					Message:   fmt.Sprintf("Network error, failing over to %s", fallback.Type),
				})
				executeProxy(w, r, originalBody, body, fallback, cfg, retry+1, requestID)
				return
			}
		}

		utils.JSON(w, 502, map[string]string{"error": "Proxy failed: " + err.Error()})
		return
	}

	if result.Response.StatusCode >= 200 && result.Response.StatusCode < 300 {
		cb.RecordSuccess(provider.Type)
		go func() {
			storage.IncrementStats(result.Usage.PromptTokens, result.Usage.CompletionTokens)
			storage.UpdateProviderLatency(provider.Type, int(result.LatencyMs))
			storage.UpdateProviderHealth(provider.Type, true)
		}()

		for k, v := range result.Response.Header {
			for _, vv := range v {
				w.Header().Add(k, vv)
			}
		}
		w.WriteHeader(result.Response.StatusCode)
		io.Copy(w, result.Response.Body)
		result.Response.Body.Close()
		return
	}

	respBody, _ := io.ReadAll(result.Response.Body)
	result.Response.Body.Close()

	statusCode := result.Response.StatusCode
	bodyStr := utils.Truncate(string(respBody), 200)

	category := classifyError(nil, statusCode)
	go storage.AppendErrorLog(buildErrorEvent(provider.Type, model, category, requestID, statusCode, "", bodyStr))
	go storage.RecordFailureMetric(provider.Type, category)

	if statusCode >= 400 && statusCode < 500 {
		w.WriteHeader(statusCode)
		w.Write(respBody)
		return
	}

	cb.RecordFailure(provider.Type)
	go storage.UpdateProviderHealth(provider.Type, false)

	if retry < MAX_RETRIES {
		fallback := providers.GetFallbackProvider(model, cfg.Providers, provider.Type, cfg.CustomProviders)
		if fallback != nil {
			log.Printf("[proxy] Upstream error %d for %s, failing over to %s (retry %d)", statusCode, provider.Type, fallback.Type, retry+1)
			go storage.AppendErrorLog(config.ErrorLogEntry{
				Timestamp: utils.NowISO(),
				Provider:  provider.Type,
				Model:     model,
				Status:    statusCode,
				Message:   fmt.Sprintf("Failing over to %s (retry %d)", fallback.Type, retry+1),
			})
			executeProxy(w, r, originalBody, body, fallback, cfg, retry+1, requestID)
			return
		}
		executeProxy(w, r, originalBody, body, provider, cfg, retry+1, requestID)
		return
	}

	w.WriteHeader(statusCode)
	w.Write(respBody)
}

// breakerWorthy reports whether an upstream outcome should count as a
// circuit-breaker failure. Only network errors and 5xx indicate a genuine
// provider-health problem. 4xx (bad key, missing model, malformed request,
// rate limit) are request/client errors and must NOT trip the breaker —
// otherwise a single mistyped model name or a temporarily bad key could
// 503 the entire provider for the breaker's cooldown window (60s).
func breakerWorthy(networkErr error, statusCode int) bool {
	if networkErr != nil {
		return true
	}
	return statusCode >= 500
}

// CircuitOpen and RateLimited expose the shared circuit-breaker / rate-limiter
// state so other entry points (e.g. the Responses API path) can apply the same
// prechecks as the chat proxy without duplicating breaker logic.
func CircuitOpen(providerType string) bool { return cb.IsOpen(providerType) }
func RateLimited(providerType string) bool { return rl.IsLimited(providerType) }

// =============================================================================
// handleAutoProxy — auto mode: iterate all configured providers' models
// =============================================================================

func handleAutoProxy(w http.ResponseWriter, r *http.Request, body map[string]interface{}, cfg *config.Config, requestID string) {
	sorted := providers.SortedByPriority(cfg.Providers, cfg.CustomProviders)
	cached := loadAllCachedModels()

	type candidate struct {
		Type  string
		Key   string
		Model string
	}

	var candidates []candidate
	seen := make(map[string]bool)

	for _, p := range sorted {
		// Skip providers without a key so we never build candidates that would
		// fail with an empty Authorization header (e.g. when gw_models cache is
		// stale relative to the config).
		if p.Key == "" {
			continue
		}
		models := cached[p.Type]
		if len(models) == 0 {
			def, defOk := providers.ResolveDefinition(p.Type, cfg.CustomProviders)
			if defOk && def.BaseURL != "" {
				fetched := adapters.FetchProviderModels(def, p.Key)
				if len(fetched) > 0 {
					cached[p.Type] = fetched
					models = fetched
					storage.SaveCachedModels(p.Type, fetched)
				}
			}
		}
		for _, m := range models {
			// Dedup by provider+model, not model name alone: two providers
			// exposing the same model name must each be tried independently so a
			// healthy lower-priority key is not shadowed by a broken higher one.
			key := p.Type + "/" + m
			if !seen[key] {
				seen[key] = true
				candidates = append(candidates, candidate{Type: p.Type, Key: p.Key, Model: m})
			}
		}
	}

	if len(candidates) == 0 {
		utils.JSON(w, 404, map[string]string{"error": "No models available in auto mode"})
		return
	}

	var errorsList []string
	attempted := 0
	rateLimited := 0
	circuitOpen := 0

	for _, c := range candidates {
		// Respect circuit-breaker and rate-limiter state: don't waste a request
		// (and don't hammer) a provider that is already known-bad or throttled.
		// Skipped providers are still recorded in the detail list.
		if cb.IsOpen(c.Type) {
			circuitOpen++
			errorsList = append(errorsList, fmt.Sprintf("%s/%s: circuit_open", c.Type, c.Model))
			continue
		}
		if rl.IsLimited(c.Type) {
			rateLimited++
			errorsList = append(errorsList, fmt.Sprintf("%s/%s: rate_limited", c.Type, c.Model))
			continue
		}
		attempted++

		candidateBody := make(map[string]interface{})
		for k, v := range body {
			candidateBody[k] = v
		}
		candidateBody["model"] = c.Model

		isStreaming := false
		if s, ok := candidateBody["stream"].(bool); ok && s {
			isStreaming = true
		}

		if isStreaming {
			def, _ := providers.ResolveDefinition(c.Type, cfg.CustomProviders)
			upstreamResp, streamErr := adapters.StreamingProxyWithProvider(candidateBody, def, c.Key)
			if streamErr != nil {
				cb.RecordFailure(c.Type)
				go storage.RecordFailureMetric(c.Type, classifyError(streamErr, 0))
				errorsList = append(errorsList, fmt.Sprintf("%s/%s: %s", c.Type, c.Model, streamErr.Error()))
				continue
			}

			if upstreamResp.StatusCode >= 200 && upstreamResp.StatusCode < 300 {
				cb.RecordSuccess(c.Type)
				go storage.UpdateProviderHealth(c.Type, true)
				for k, v := range upstreamResp.Header {
					for _, vv := range v {
						w.Header().Add(k, vv)
					}
				}
				w.WriteHeader(upstreamResp.StatusCode)
				copyWithFlush(w, upstreamResp.Body)
				upstreamResp.Body.Close()
				return
			}

			respBody, _ := io.ReadAll(upstreamResp.Body)
			upstreamResp.Body.Close()
			bodyStr := utils.Truncate(string(respBody), 200)

		statusCode := upstreamResp.StatusCode
		cat := classifyError(nil, statusCode)
		if breakerWorthy(nil, statusCode) {
			cb.RecordFailure(c.Type)
		}
		go storage.RecordFailureMetric(c.Type, cat)
		go storage.AppendErrorLog(buildErrorEvent(c.Type, c.Model, cat, requestID, statusCode, "", bodyStr))

		errorsList = append(errorsList, fmt.Sprintf("%s/%s: %d", c.Type, c.Model, statusCode))
		continue
	}

		def, _ := providers.ResolveDefinition(c.Type, cfg.CustomProviders)
		result, proxyErr := adapters.ProxyWithProvider(candidateBody, def, c.Key)
		if proxyErr != nil {
			if breakerWorthy(proxyErr, 0) {
				cb.RecordFailure(c.Type)
			}
			go storage.RecordFailureMetric(c.Type, classifyError(proxyErr, 0))
			go storage.AppendErrorLog(buildErrorEvent(c.Type, c.Model, "network_error", requestID, 0, proxyErr.Error(), ""))
			errorsList = append(errorsList, fmt.Sprintf("%s/%s: %s", c.Type, c.Model, proxyErr.Error()))
			continue
		}

		if result.Response.StatusCode >= 200 && result.Response.StatusCode < 300 {
			cb.RecordSuccess(c.Type)
			go func(candidateType string, lt int64) {
				storage.IncrementStats(result.Usage.PromptTokens, result.Usage.CompletionTokens)
				storage.UpdateProviderLatency(candidateType, int(lt))
				storage.UpdateProviderHealth(candidateType, true)
			}(c.Type, result.LatencyMs)

			respBody, _ := io.ReadAll(result.Response.Body)
			result.Response.Body.Close()

			var respJSON map[string]interface{}
			if err := json.Unmarshal(respBody, &respJSON); err != nil || respJSON == nil {
				respJSON = make(map[string]interface{})
			}
			if _, hasModel := respJSON["model"]; !hasModel {
				respJSON["model"] = c.Model
			}

			outBytes, _ := json.Marshal(respJSON)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(200)
			w.Write(outBytes)
			return
		}

		respBody, _ := io.ReadAll(result.Response.Body)
		result.Response.Body.Close()
		bodyStr := utils.Truncate(string(respBody), 200)

	statusCode := result.Response.StatusCode
	cat := classifyError(nil, statusCode)
	if breakerWorthy(nil, statusCode) {
		cb.RecordFailure(c.Type)
	}
	go storage.RecordFailureMetric(c.Type, cat)
	go storage.AppendErrorLog(buildErrorEvent(c.Type, c.Model, cat, requestID, statusCode, "", bodyStr))

	errorsList = append(errorsList, fmt.Sprintf("%s/%s: %d", c.Type, c.Model, statusCode))
}

	if attempted == 0 {
		// Nothing was actually tried because every candidate was skipped by the
		// circuit breaker or rate limiter. Report the most specific status.
		if rateLimited > 0 && circuitOpen == 0 {
			utils.JSON(w, 429, map[string]interface{}{
				"error":  "All providers rate limited in auto mode",
				"detail": errorsList,
			})
			return
		}
		if circuitOpen > 0 && rateLimited == 0 {
			utils.JSON(w, 503, map[string]interface{}{
				"error":  "All providers circuit-open in auto mode",
				"detail": errorsList,
			})
			return
		}
	}

	utils.JSON(w, 503, map[string]interface{}{
		"error":  "All models exhausted in auto mode",
		"detail": errorsList,
	})
}

// =============================================================================
// Health check
// =============================================================================

func HandleHealth(w http.ResponseWriter, r *http.Request) {
	cfg, err := storage.GetConfig()
	if err != nil {
		utils.JSON(w, 500, map[string]string{"error": "Config error"})
		return
	}

	health := storage.GetProviderHealth()
	failureMetrics := storage.GetFailureMetrics()

	healthy := 0
	degraded := 0
	circuitOpen := 0
	for _, state := range health {
		switch state {
		case "healthy":
			healthy++
		case "degraded":
			degraded++
		case "circuit_open":
			circuitOpen++
		}
	}

	failures := 0
	for _, fm := range failureMetrics {
		failures += fm.Total
	}

	status := "unknown"
	if circuitOpen > 0 {
		status = "circuit_open"
	} else if degraded > 0 {
		status = "degraded"
	} else if healthy > 0 {
		status = "healthy"
	}

	utils.JSON(w, 200, map[string]interface{}{
		"status":               status,
		"providerCount":        len(cfg.Providers),
		"healthyProviders":     healthy,
		"degradedProviders":    degraded,
		"circuitOpenProviders": circuitOpen,
		"failureCount":         failures,
		"timestamp":            utils.NowISO(),
	})
}

// =============================================================================
// Helper: stream upstream body to client with per-chunk flush
// =============================================================================

// copyWithFlush streams src to w and flushes after every chunk so SSE tokens
// appear incrementally instead of being buffered by the client until completion.
func copyWithFlush(w http.ResponseWriter, src io.Reader) error {
	flusher, _ := w.(http.Flusher)
	buf := make([]byte, 4096)
	for {
		n, err := src.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return werr
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
	}
}

// =============================================================================
// Helper: load all cached models from KV
// =============================================================================

func loadAllCachedModels() map[string][]string {
	var all map[string][]string
	var raw json.RawMessage
	if err := db.KVGet("gw_models", &raw); err == nil && raw != nil {
		json.Unmarshal(raw, &all)
	}
	if all == nil {
		all = make(map[string][]string)
	}
	return all
}
