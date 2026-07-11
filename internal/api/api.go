package api

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"ai-gateway/internal/adapters"
	"ai-gateway/internal/config"
	"ai-gateway/internal/providers"
	"ai-gateway/internal/proxy"
	"ai-gateway/internal/storage"
	"ai-gateway/internal/utils"
)

func HandleConfigGet(w http.ResponseWriter, r *http.Request) {
	if !storage.IsAuthenticated(r) {
		utils.JSON(w, 401, map[string]string{"error": "Authentication required"})
		return
	}

	cfg, err := storage.GetConfig()
	if err != nil {
		utils.JSON(w, 500, map[string]string{"error": "Config error"})
		return
	}

	// Merge runtime data (provided in config already since they share the same JSON)
	utils.JSON(w, 200, map[string]interface{}{
		"unifiedKey":      cfg.UnifiedKey,
		"providers":       cfg.Providers,
		"stats":           storage.GetStats(),
		"models":          cfg.Models,
		"requestLog":      truncateLog(storage.GetRequestLog(), 50),
		"errorLog":        truncateLogErrors(storage.GetErrorLog(), 50),
		"providerLatency": storage.GetProviderLatency(),
		"providerHealth":  storage.GetProviderHealth(),
		"failureMetrics":  storage.GetFailureMetrics(),
	})
}

func HandleConfigPost(w http.ResponseWriter, r *http.Request) {
	if !storage.IsAuthenticated(r) {
		utils.JSON(w, 401, map[string]string{"error": "Authentication required"})
		return
	}

	var body struct {
		Providers  []config.UserProvider `json:"providers"`
		UnifiedKey string                `json:"unifiedKey"`
	}
	if err := utils.ParseJSON(r, &body); err != nil {
		utils.JSON(w, 400, map[string]string{"error": "Invalid JSON"})
		return
	}

	cfg, err := storage.GetConfig()
	if err != nil {
		utils.JSON(w, 500, map[string]string{"error": "Config error"})
		return
	}

	if body.Providers != nil {
		cfg.Providers = body.Providers
	}
	if body.UnifiedKey != "" {
		cfg.UnifiedKey = body.UnifiedKey
	}

	// Re-merge custom-provider keys into cfg.Providers. The dashboard tracks
	// custom-provider keys in cfg.CustomProviders (server-side only, never sent
	// to the client), so the providers list it POSTs back does NOT include them.
	// Without this, any saveConfig() call would clobber a keyed custom provider
	// and it could no longer fetch models or serve traffic. We treat the
	// custom-provider key as authoritative for its type.
	for _, cp := range cfg.CustomProviders {
		if cp.Key == "" {
			continue
		}
		found := false
		for i := range cfg.Providers {
			if cfg.Providers[i].Type == cp.ID {
				cfg.Providers[i].Key = cp.Key
				found = true
				break
			}
		}
		if !found {
			cfg.Providers = append(cfg.Providers, config.UserProvider{Type: cp.ID, Key: cp.Key})
		}
	}

	// Track which provider types still have a (non-empty) API key, so we can
	// later drop cached models for providers whose key was removed or that were
	// deleted entirely — otherwise the dashboard keeps showing ghost models.
	keyedTypes := make(map[string]bool)
	for _, p := range cfg.Providers {
		if p.Key != "" {
			keyedTypes[p.Type] = true
		}
	}

	// Fetch upstream models for all configured providers
	models := make(map[string][]string)
	for _, p := range cfg.Providers {
		if p.Key == "" {
			continue
		}
		def, ok := providers.ResolveDefinition(p.Type, cfg.CustomProviders)
		if !ok || def.BaseURL == "" {
			continue
		}
		fetched := adapters.FetchProviderModels(def, p.Key)
		if len(fetched) > 0 {
			models[p.Type] = fetched
			storage.SaveCachedModels(p.Type, fetched)
		}
	}

	// Update config.models
	if cfg.Models == nil {
		cfg.Models = make(map[string][]string)
	}
	for k, v := range models {
		cfg.Models[k] = v
	}

	// Drop stale model caches for providers that are no longer keyed (key
	// removed or provider deleted). We deliberately keep models for a keyed
	// provider whose live fetch returned empty (e.g. a transient upstream
	// error) so we don't wipe a working cache on a hiccup.
	for t := range cfg.Models {
		if !keyedTypes[t] {
			delete(cfg.Models, t)
			storage.DeleteCachedModels(t)
		}
	}

	storage.SaveConfig(cfg)
	utils.JSON(w, 200, map[string]interface{}{
		"success": true,
		"models":  models,
	})
}

func HandleKeyRegenerate(w http.ResponseWriter, r *http.Request) {
	if !storage.IsAuthenticated(r) {
		utils.JSON(w, 401, map[string]string{"error": "Authentication required"})
		return
	}

	cfg, _ := storage.GetConfig()
	cfg.UnifiedKey = utils.GenerateToken("sk-gw-")
	storage.SaveConfig(cfg)
	utils.JSON(w, 200, map[string]interface{}{
		"success":    true,
		"unifiedKey": cfg.UnifiedKey,
	})
}

func HandleProviders(w http.ResponseWriter, r *http.Request) {
	categories := providers.GetByCategory()
	list := providers.List()

	var providerList []map[string]interface{}
	for _, p := range list {
		providerList = append(providerList, map[string]interface{}{
			"id":         p.ID,
			"label":      p.Label,
			"category":   p.Category,
			"models":     p.Models,
			"baseUrl":    p.BaseURL,
			"website":    p.Website,
			"docs":       p.Docs,
			"apiKeyUrl":  p.APIKeyURL,
			"icon":       p.Icon,
			"compatible": p.Compatible,
			"adapter":    p.Adapter,
			"priority":   p.Priority,
		})
	}

	utils.JSON(w, 200, map[string]interface{}{
		"categories": categories,
		"providers":  providerList,
	})
}

func HandleCustomProvidersGet(w http.ResponseWriter, r *http.Request) {
	if !storage.IsAuthenticated(r) {
		utils.JSON(w, 401, map[string]string{"error": "Authentication required"})
		return
	}

	cfg, _ := storage.GetConfig()
	list := providers.List()

	var builtin []map[string]interface{}
	for _, p := range list {
		builtin = append(builtin, map[string]interface{}{
			"id":         p.ID,
			"label":      p.Label,
			"category":   p.Category,
			"models":     p.Models,
			"baseUrl":    p.BaseURL,
			"website":    p.Website,
			"docs":       p.Docs,
			"apiKeyUrl":  p.APIKeyURL,
			"icon":       p.Icon,
			"compatible": p.Compatible,
			"adapter":    p.Adapter,
			"priority":   p.Priority,
			"source":     "builtin",
		})
	}

	var custom []map[string]interface{}
	for _, cp := range cfg.CustomProviders {
		// keyMask: show only the last 4 chars so an operator can tell a key is
		// configured without exposing the secret. The full key is server-side only.
		var keyMask string
		if len(cp.Key) > 4 {
			keyMask = "****" + cp.Key[len(cp.Key)-4:]
		} else if cp.Key != "" {
			keyMask = "****"
		}
		custom = append(custom, map[string]interface{}{
			"id":         cp.ID,
			"label":      cp.Label,
			"category":   cp.Category,
			"models":     cp.Models,
			"baseUrl":    cp.BaseURL,
			"website":    cp.Website,
			"docs":       cp.Docs,
			"apiKeyUrl":  cp.APIKeyURL,
			"icon":       cp.Icon,
			"compatible": cp.Compatible,
			"keyMask":    keyMask,
			"adapter":    cp.Adapter,
			"priority":   cp.Priority,
			"source":     "custom",
		})
	}

	utils.JSON(w, 200, map[string]interface{}{
		"builtin": builtin,
		"custom":  custom,
	})
}

func HandleCustomProvidersPost(w http.ResponseWriter, r *http.Request) {
	if !storage.IsAuthenticated(r) {
		utils.JSON(w, 401, map[string]string{"error": "Authentication required"})
		return
	}

	var body config.CustomProvider
	if err := utils.ParseJSON(r, &body); err != nil {
		utils.JSON(w, 400, map[string]string{"error": "Invalid JSON"})
		return
	}

	if body.ID == "" {
		utils.JSON(w, 400, map[string]string{"error": "Missing required field: id"})
		return
	}

	// Upsert semantics: if the provider already exists, merge non-empty fields
	// from the request into the stored one. This lets the dashboard update a
	// single field (e.g. just the API key) without resending everything.
	cfg, _ := storage.GetConfig()
	idx := -1
	for i, cp := range cfg.CustomProviders {
		if cp.ID == body.ID {
			idx = i
			break
		}
	}

	// Reject clobbering a built-in provider ID (only on first creation).
	if idx < 0 {
		if _, ok := providers.Get(body.ID); ok {
			utils.JSON(w, 409, map[string]string{"error": "Provider \"" + body.ID + "\" already exists as a built-in provider"})
			return
		}
	}

	merged := config.CustomProvider{}
	if idx >= 0 {
		merged = cfg.CustomProviders[idx]
	} else {
		merged.ID = body.ID
	}

	// Required fields must be present for a brand-new provider.
	if idx < 0 {
		if body.Label == "" || body.BaseURL == "" {
			utils.JSON(w, 400, map[string]string{"error": "Missing required fields: label, baseUrl"})
			return
		}
		if _, err := url.Parse(body.BaseURL); err != nil {
			utils.JSON(w, 400, map[string]string{"error": "baseUrl must be a valid URL"})
			return
		}
	}

	// Merge provided fields.
	if body.Label != "" {
		merged.Label = body.Label
	}
	if body.BaseURL != "" {
		merged.BaseURL = strings.TrimRight(body.BaseURL, "/")
	}
	if body.Category != "" {
		merged.Category = body.Category
	}
	if body.Website != "" {
		merged.Website = body.Website
	}
	if body.Docs != "" {
		merged.Docs = body.Docs
	}
	if body.APIKeyURL != "" {
		merged.APIKeyURL = body.APIKeyURL
	}
	if body.Icon != "" {
		merged.Icon = body.Icon
	}
	if body.Adapter != "" {
		merged.Adapter = body.Adapter
	}
	if body.Priority != 0 {
		merged.Priority = body.Priority
	}
	if body.Models != nil {
		merged.Models = body.Models
	}

	// Key is optional and never echoed back to the client. On upsert the client
	// always sends the authoritative key value (empty string = clear). This lets
	// the dashboard update just the key without resending all other fields.
	merged.Key = strings.TrimSpace(body.Key)

	if idx >= 0 {
		cfg.CustomProviders[idx] = merged
	} else {
		cfg.CustomProviders = append(cfg.CustomProviders, merged)
	}

	// Mirror the key into config.Providers so the proxy + HandleModels resolve
	// it exactly like a builtin provider. Without this a keyed custom provider
	// could never fetch models or serve traffic. The key only lives server-side.
	ui := -1
	for i, up := range cfg.Providers {
		if up.Type == merged.ID {
			ui = i
			break
		}
	}
	if merged.Key != "" {
		if ui >= 0 {
			cfg.Providers[ui].Key = merged.Key
		} else {
			cfg.Providers = append(cfg.Providers, config.UserProvider{Type: merged.ID, Key: merged.Key})
		}
	} else if ui >= 0 {
		// Key cleared: drop the mirrored entry so stale keys don't linger.
		cfg.Providers = append(cfg.Providers[:ui], cfg.Providers[ui+1:]...)
	}

	storage.SaveConfig(cfg)
	// Never echo the raw key back to the client in the response.
	resp := merged
	resp.Key = ""
	utils.JSON(w, 200, map[string]interface{}{
		"success":  true,
		"provider": resp,
	})
}

func HandleCustomProvidersDelete(w http.ResponseWriter, r *http.Request) {
	if !storage.IsAuthenticated(r) {
		utils.JSON(w, 401, map[string]string{"error": "Authentication required"})
		return
	}

	id := r.URL.Query().Get("id")
	if id == "" {
		utils.JSON(w, 400, map[string]string{"error": "Missing id parameter"})
		return
	}

	cfg, _ := storage.GetConfig()
	before := len(cfg.CustomProviders)
	var filtered []config.CustomProvider
	for _, cp := range cfg.CustomProviders {
		if cp.ID != id {
			filtered = append(filtered, cp)
		}
	}
	if len(filtered) == before {
		utils.JSON(w, 404, map[string]string{"error": "Custom provider not found"})
		return
	}
	cfg.CustomProviders = filtered

	// Drop the mirrored keyed entry so the deleted provider can no longer
	// serve traffic or appear as a configured provider.
	var providers []config.UserProvider
	for _, up := range cfg.Providers {
		if up.Type != id {
			providers = append(providers, up)
		}
	}
	cfg.Providers = providers
	storage.DeleteCachedModels(id)

	storage.SaveConfig(cfg)
	utils.JSON(w, 200, map[string]bool{"success": true})
}

func HandleModels(w http.ResponseWriter, r *http.Request) {
	if !storage.IsAuthenticated(r) {
		utils.JSON(w, 401, map[string]string{"error": "Authentication required"})
		return
	}

	pType := r.URL.Query().Get("type")
	if pType == "" {
		utils.JSON(w, 400, map[string]string{"error": "type parameter is required"})
		return
	}

	cfg, _ := storage.GetConfig()
	var provider *config.UserProvider
	for i, p := range cfg.Providers {
		if p.Type == pType {
			provider = &cfg.Providers[i]
			break
		}
	}
	if provider == nil {
		utils.JSON(w, 404, map[string]string{"error": "Provider not found"})
		return
	}

	def, ok := providers.ResolveDefinition(pType, cfg.CustomProviders)
	if !ok {
		utils.JSON(w, 404, map[string]string{"error": "Provider definition not found"})
		return
	}

	models := adapters.FetchProviderModels(def, provider.Key)
	if len(models) > 0 {
		storage.SaveCachedModels(pType, models)
	}

	utils.JSON(w, 200, map[string]interface{}{
		"models": models,
		"type":   pType,
	})
}

func HandleStats(w http.ResponseWriter, r *http.Request) {
	if !storage.IsAuthenticated(r) {
		utils.JSON(w, 401, map[string]string{"error": "Authentication required"})
		return
	}

	cfg, _ := storage.GetConfig()

	failureMetrics := storage.GetFailureMetrics()
	health := storage.GetProviderHealth()

	failures := 0
	for _, fm := range failureMetrics {
		failures += fm.Total
	}

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

	status := "unknown"
	statusLabel := "未知"
	if circuitOpen > 0 {
		status = "circuit_open"
		statusLabel = "熔断"
	} else if degraded > 0 {
		status = "degraded"
		statusLabel = "降级"
	} else if healthy > 0 {
		status = "healthy"
		statusLabel = "正常"
	}

	utils.JSON(w, 200, map[string]interface{}{
		"stats":           storage.GetStats(),
		"requestLog":      truncateLog(storage.GetRequestLog(), 20),
		"errorLog":        truncateLogErrors(storage.GetErrorLog(), 20),
		"providerLatency": storage.GetProviderLatency(),
		"providerHealth":  health,
		"failureMetrics":  failureMetrics,
		"healthSummary": map[string]interface{}{
			"status":               status,
			"statusLabel":          statusLabel,
			"totalProviders":       len(cfg.Providers),
			"healthyProviders":     healthy,
			"degradedProviders":    degraded,
			"circuitOpenProviders": circuitOpen,
			"totalFailures":        failures,
			"lastUpdated":          utils.NowISO(),
		},
	})
}

func HandleResponses(w http.ResponseWriter, r *http.Request) {
	authHeader := r.Header.Get("Authorization")
	var providedToken string
	if len(authHeader) > 7 && authHeader[:7] == "Bearer " {
		providedToken = authHeader[7:]
	}

	cfg, _ := storage.GetConfig()
	if providedToken == "" || !utils.TimingSafeCompare(providedToken, cfg.UnifiedKey) {
		utils.JSON(w, 401, map[string]string{"error": "Unauthorized"})
		return
	}

	var body map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		utils.JSON(w, 400, map[string]string{"error": "Invalid JSON"})
		return
	}
	defer r.Body.Close()

	model, ok := body["model"].(string)
	if !ok {
		utils.JSON(w, 400, map[string]string{"error": "model field is required"})
		return
	}

	// Convert Responses API to Chat Completions
	chatBody := responsesToChat(body)

	if len(cfg.Providers) == 0 {
		utils.JSON(w, 400, map[string]string{"error": "No providers configured. Add API keys in the dashboard first."})
		return
	}

	provider := providers.ResolveProvider(model, cfg.Providers, cfg.CustomProviders)
	if provider == nil {
		utils.JSON(w, 404, map[string]string{"error": "No matching provider found for model: " + model})
		return
	}

	def, _ := providers.ResolveDefinition(provider.Type, cfg.CustomProviders)

	// Apply the same circuit-breaker / rate-limit precheck as the chat proxy so
	// the Responses API path fails fast instead of hammering a dead/limited
	// provider (the chat path does this in proxy.go before executeProxy).
	if proxy.CircuitOpen(provider.Type) {
		w.Header().Set("Retry-After", "60")
		utils.JSON(w, 503, map[string]string{
			"error": "Provider " + provider.Type + " is temporarily unavailable due to repeated failures.",
		})
		return
	}
	if proxy.RateLimited(provider.Type) {
		w.Header().Set("Retry-After", "60")
		utils.JSON(w, 429, map[string]string{
			"error": "Rate limit exceeded for provider: " + provider.Type,
		})
		return
	}

	stream := false
	if s, ok := body["stream"].(bool); ok && s {
		stream = true
	}
	if stream {
		streamResponses(w, chatBody, def, provider.Key, model)
		return
	}

	result, err := adapters.ProxyWithProvider(chatBody, def, provider.Key)
	if err != nil {
		utils.JSON(w, 502, map[string]string{"error": "Proxy failed: " + err.Error()})
		return
	}

	respBody, _ := readAll(result.Response.Body)
	result.Response.Body.Close()

	var chatResp map[string]interface{}
	json.Unmarshal(respBody, &chatResp)

	responsesResp := chatToResponses(chatResp)
	respJSON, _ := json.Marshal(responsesResp)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(200)
	w.Write(respJSON)
}

// streamResponses translates an upstream OpenAI-format SSE stream (the contract
// returned by StreamingProxyWithProvider for every adapter) into the OpenAI
// Responses API streaming event sequence, emitting each token delta immediately
// so clients see incremental output instead of a buffered full response.
func streamResponses(w http.ResponseWriter, chatBody map[string]interface{}, def config.Provider, apiKey, model string) {
	chatBody["stream"] = true
	upstreamResp, err := adapters.StreamingProxyWithProvider(chatBody, def, apiKey)
	if err != nil {
		utils.JSON(w, 502, map[string]string{"error": "Proxy failed: " + err.Error()})
		return
	}
	defer upstreamResp.Body.Close()

	if upstreamResp.StatusCode < 200 || upstreamResp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(upstreamResp.Body)
		utils.JSON(w, upstreamResp.StatusCode, map[string]interface{}{
			"error": utils.Truncate(string(respBody), 500),
		})
		return
	}

	streamResponsesTranslate(w, upstreamResp, model)
}

// streamResponsesTranslate turns an already-open upstream OpenAI-format SSE
// stream into the OpenAI Responses API streaming event sequence. Split out from
// streamResponses so the translation can be unit-tested with a synthetic stream.
// streamResponsesTranslate turns an already-open upstream OpenAI-format SSE
// stream into the OpenAI Responses API streaming event sequence. Split out from
// streamResponses so the translation can be unit-tested with a synthetic stream.
//
// It supports both plain-text output and tool-calls (function_call) output,
// emitting each token / argument fragment immediately so clients render
// incremental output instead of a buffered full response. Items (message and
// function_call) are emitted lazily on first sight of their content, so a
// tool-calls-only response never carries a spurious empty message item.
func streamResponsesTranslate(w http.ResponseWriter, upstreamResp *http.Response, model string) {
	flusher, _ := w.(http.Flusher)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(200)

	responseID := "resp_" + utils.GenerateToken("")
	seq := 0
	nextSeq := func() int { seq++; return seq }

	type itemState struct {
		itemID      string
		outputIndex int
		kind        string // "message" or "function_call"
		text        string
		name        string
		args        string
	}
	var items []*itemState
	var textItem *itemState
	toolItemByIndex := map[int]*itemState{}
	nextOutputIndex := 0

	writeEvent := func(eventType string, payload map[string]interface{}) {
		payload["type"] = eventType
		payload["sequence_number"] = nextSeq()
		b, _ := json.Marshal(payload)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventType, string(b))
		if flusher != nil {
			flusher.Flush()
		}
	}

	// Lazily open the message (text) output item on first text fragment.
	ensureTextItem := func() {
		if textItem != nil {
			return
		}
		textItem = &itemState{kind: "message"}
		textItem.itemID = "item_" + utils.GenerateToken("")
		textItem.outputIndex = nextOutputIndex
		nextOutputIndex++
		items = append(items, textItem)
		writeEvent("response.output_item.added", map[string]interface{}{
			"output_index": textItem.outputIndex,
			"item": map[string]interface{}{
				"id":      textItem.itemID,
				"type":    "message",
				"status":  "in_progress",
				"role":    "assistant",
				"content": []interface{}{},
			},
		})
		writeEvent("response.content_part.added", map[string]interface{}{
			"item_id":       textItem.itemID,
			"output_index":  textItem.outputIndex,
			"content_index": 0,
			"part":          map[string]interface{}{"type": "output_text", "text": ""},
		})
	}

	lifecycle := func(status string, output []map[string]interface{}) map[string]interface{} {
		return map[string]interface{}{
			"id":         responseID,
			"object":     "response",
			"created_at": time.Now().Unix(),
			"status":     status,
			"model":      model,
			"output":     output,
		}
	}

	writeEvent("response.created", map[string]interface{}{
		"response": lifecycle("in_progress", []map[string]interface{}{}),
	})
	writeEvent("response.in_progress", map[string]interface{}{
		"response": lifecycle("in_progress", []map[string]interface{}{}),
	})

	// Translate upstream OpenAI-format chunks into Responses events
	scanner := bufio.NewScanner(upstreamResp.Body)
	scanner.Buffer(make([]byte, 0, 1024*1024), 8*1024*1024)
	var usage map[string]interface{}
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" || data == "[DONE]" {
			continue
		}
		var chunk map[string]interface{}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}
		if model == "" {
			if m, ok := chunk["model"].(string); ok && m != "" {
				model = m
			}
		}
		if u, ok := chunk["usage"].(map[string]interface{}); ok {
			usage = u
		}
		choices, _ := chunk["choices"].([]interface{})
		for _, c := range choices {
			ch, ok := c.(map[string]interface{})
			if !ok {
				continue
			}
			delta, _ := ch["delta"].(map[string]interface{})
			if content, ok := delta["content"].(string); ok && content != "" {
				ensureTextItem()
				textItem.text += content
				writeEvent("response.output_text.delta", map[string]interface{}{
					"item_id":       textItem.itemID,
					"output_index":  textItem.outputIndex,
					"content_index": 0,
					"delta":         content,
				})
			}
			if toolCalls, ok := delta["tool_calls"].([]interface{}); ok {
				for _, tc := range toolCalls {
					tcm, ok := tc.(map[string]interface{})
					if !ok {
						continue
					}
					idx := 0
					if i, ok := tcm["index"].(float64); ok {
						idx = int(i)
					}
					st, ok := toolItemByIndex[idx]
					if !ok {
						st = &itemState{kind: "function_call"}
						st.itemID = "item_" + utils.GenerateToken("")
						st.outputIndex = nextOutputIndex
						nextOutputIndex++
						toolItemByIndex[idx] = st
						items = append(items, st)
						if fn, ok := tcm["function"].(map[string]interface{}); ok {
							if n, ok := fn["name"].(string); ok && n != "" {
								st.name = n
							}
						}
						writeEvent("response.output_item.added", map[string]interface{}{
							"output_index": st.outputIndex,
							"item": map[string]interface{}{
								"id":        st.itemID,
								"type":      "function_call",
								"status":    "in_progress",
								"call_id":   st.itemID,
								"name":      st.name,
								"arguments": "",
							},
						})
					}
					if fn, ok := tcm["function"].(map[string]interface{}); ok {
						if n, ok := fn["name"].(string); ok && n != "" && st.name == "" {
							st.name = n
						}
						if a, ok := fn["arguments"].(string); ok && a != "" {
							st.args += a
							writeEvent("response.function_call_arguments.delta", map[string]interface{}{
								"item_id":      st.itemID,
								"output_index": st.outputIndex,
								"delta":        a,
							})
						}
					}
				}
			}
		}
	}

	// Completion lifecycle events
	if textItem != nil {
		writeEvent("response.output_text.done", map[string]interface{}{
			"item_id":       textItem.itemID,
			"output_index":  textItem.outputIndex,
			"content_index": 0,
			"text":          textItem.text,
		})
		writeEvent("response.content_part.done", map[string]interface{}{
			"item_id":       textItem.itemID,
			"output_index":  textItem.outputIndex,
			"content_index": 0,
			"part":          map[string]interface{}{"type": "output_text", "text": textItem.text},
		})
		writeEvent("response.output_item.done", map[string]interface{}{
			"output_index": textItem.outputIndex,
			"item": map[string]interface{}{
				"id":      textItem.itemID,
				"type":    "message",
				"status":  "completed",
				"role":    "assistant",
				"content": []map[string]interface{}{{"type": "output_text", "text": textItem.text}},
			},
		})
	}

	// function-call items, in upstream index order
	for i := 0; ; i++ {
		st, ok := toolItemByIndex[i]
		if !ok {
			break
		}
		writeEvent("response.function_call_arguments.done", map[string]interface{}{
			"item_id":      st.itemID,
			"output_index": st.outputIndex,
			"name":         st.name,
			"arguments":    st.args,
		})
		writeEvent("response.output_item.done", map[string]interface{}{
			"output_index": st.outputIndex,
			"item": map[string]interface{}{
				"id":        st.itemID,
				"type":      "function_call",
				"status":    "completed",
				"call_id":   st.itemID,
				"name":      st.name,
				"arguments": st.args,
			},
		})
	}

	// Final output snapshot in emission order
	var output []map[string]interface{}
	for _, it := range items {
		if it.kind == "message" {
			output = append(output, map[string]interface{}{
				"id":      it.itemID,
				"type":    "message",
				"status":  "completed",
				"role":    "assistant",
				"content": []map[string]interface{}{{"type": "output_text", "text": it.text}},
			})
		} else {
			output = append(output, map[string]interface{}{
				"id":        it.itemID,
				"type":      "function_call",
				"status":    "completed",
				"call_id":   it.itemID,
				"name":      it.name,
				"arguments": it.args,
			})
		}
	}
	completed := lifecycle("completed", output)
	if usage != nil {
		completed["usage"] = usage
	}
	writeEvent("response.completed", map[string]interface{}{
		"response": completed,
	})
}

// --- Helpers ---

func truncateLog(log []config.RequestLogEntry, n int) []config.RequestLogEntry {
	if len(log) > n {
		return log[:n]
	}
	return log
}

func truncateLogErrors(log []config.ErrorLogEntry, n int) []config.ErrorLogEntry {
	if len(log) > n {
		return log[:n]
	}
	return log
}

func readAll(r io.Reader) ([]byte, error) {
	return io.ReadAll(r)
}

// Responses API <-> Chat Completions conversion

func responsesToChat(body map[string]interface{}) map[string]interface{} {
	var messages []map[string]interface{}

	if instructions, ok := body["instructions"].(string); ok && instructions != "" {
		messages = append(messages, map[string]interface{}{
			"role":    "system",
			"content": instructions,
		})
	}

	if input, ok := body["input"]; ok {
		switch v := input.(type) {
		case string:
			messages = append(messages, map[string]interface{}{
				"role": "user", "content": v,
			})
		case []interface{}:
			for _, item := range v {
				if m, ok := item.(map[string]interface{}); ok {
					messages = append(messages, m)
				}
			}
		}
	}

	if len(messages) == 0 {
		messages = append(messages, map[string]interface{}{
			"role": "user", "content": "",
		})
	}

	chatBody := map[string]interface{}{
		"model":    body["model"],
		"messages": messages,
	}
	if tools, ok := body["tools"]; ok {
		chatBody["tools"] = tools
	}
	if tc, ok := body["tool_choice"]; ok {
		chatBody["tool_choice"] = tc
	}
	if t, ok := body["temperature"]; ok {
		chatBody["temperature"] = t
	}
	if mot, ok := body["max_output_tokens"]; ok {
		chatBody["max_tokens"] = mot
	}
	if tp, ok := body["top_p"]; ok {
		chatBody["top_p"] = tp
	}
	if s, ok := body["stream"]; ok {
		chatBody["stream"] = s
	}

	return chatBody
}

func chatToResponses(chatResp map[string]interface{}) map[string]interface{} {
	var output []map[string]interface{}
	choices, _ := chatResp["choices"].([]interface{})
	for _, c := range choices {
		choice, ok := c.(map[string]interface{})
		if !ok {
			continue
		}
		msg, _ := choice["message"].(map[string]interface{})
		role := "assistant"
		if r, ok := msg["role"].(string); ok {
			role = r
		}

		var contentItems []map[string]interface{}
		if content, ok := msg["content"].(string); ok && content != "" {
			contentItems = append(contentItems, map[string]interface{}{
				"type": "output_text",
				"text": content,
			})
		}
		if toolCalls, ok := msg["tool_calls"].([]interface{}); ok {
			for _, tc := range toolCalls {
				if tcm, ok := tc.(map[string]interface{}); ok {
					fn, _ := tcm["function"].(map[string]interface{})
					if fn == nil {
						continue
					}
					contentItems = append(contentItems, map[string]interface{}{
						"type":      "function_call",
						"call_id":   tcm["id"],
						"name":      fn["name"],
						"arguments": fn["arguments"],
					})
				}
			}
		}

		output = append(output, map[string]interface{}{
			"type":    "message",
			"role":    role,
			"content": contentItems,
		})
	}

	result := map[string]interface{}{
		"object": "response",
		"output": output,
	}
	if id, ok := chatResp["id"]; ok {
		result["id"] = id
	}
	if model, ok := chatResp["model"]; ok {
		result["model"] = model
	}
	if usage, ok := chatResp["usage"]; ok {
		result["usage"] = usage
	}
	return result
}
