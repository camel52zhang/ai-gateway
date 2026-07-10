package adapters

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"ai-gateway/internal/config"
)

// proxyClient is the shared HTTP client for all upstream proxy calls. It sets a
// ResponseHeaderTimeout so a dead/hung upstream fails fast (letting the circuit
// breaker and failover kick in) instead of hanging the request goroutine forever.
// There is deliberately no overall body timeout, so long generations and SSE
// streams are not cut short.
var proxyClient = &http.Client{
	Transport: &http.Transport{
		ResponseHeaderTimeout: 60 * time.Second,
	},
}

// modelFetchClient is a dedicated client for upstream model listing with a hard
// timeout, so a slow/hanging upstream can never block config-save or model-refresh.
var modelFetchClient = &http.Client{Timeout: 15 * time.Second}

// ProxyResult holds the result of a proxy call
type ProxyResult struct {
	Response   *http.Response
	Usage      Usage
	LatencyMs  int64
	Streaming  bool
}

// Usage tracks token counts
type Usage struct {
	PromptTokens     int64 `json:"promptTokens"`
	CompletionTokens int64 `json:"completionTokens"`
	TotalTokens      int64 `json:"totalTokens"`
}

// buildChatURL converts an OpenAI-compatible provider base URL into its
// chat/completions endpoint. It tolerates base URLs that already end with
// /v1, /v1/chat/completions, or the bare host.
func buildChatURL(baseURL string) string {
	base := strings.TrimRight(baseURL, "/")
	if strings.HasSuffix(base, "/chat/completions") {
		return base
	}
	if strings.HasSuffix(base, "/v1") {
		return base + "/chat/completions"
	}
	return base + "/v1/chat/completions"
}

// ProxyWithProvider dispatches a proxy call using the (possibly custom) provider
// definition, so custom providers resolve their BaseURL/Adapter correctly instead of
// falling back to the builtin providerMap.
func ProxyWithProvider(body map[string]interface{}, def config.Provider, apiKey string) (*ProxyResult, error) {
	switch def.Adapter {
	case "google":
		return googleProxy(body, apiKey, def.BaseURL)
	case "cohere":
		return cohereProxy(body, apiKey, def.BaseURL)
	default:
		return openaiProxy(body, apiKey, buildChatURL(def.BaseURL))
	}
}

// StreamingProxyWithProvider is the streaming counterpart of ProxyWithProvider.
func StreamingProxyWithProvider(body map[string]interface{}, def config.Provider, apiKey string) (*http.Response, error) {
	switch def.Adapter {
	case "google":
		return googleStreamProxy(body, apiKey, def.BaseURL)
	case "cohere":
		return cohereStreamProxy(body, apiKey, def.BaseURL)
	default:
		return openaiStreamProxy(body, apiKey, buildChatURL(def.BaseURL))
	}
}

// --- OpenAI adapter ---

func openaiProxy(body map[string]interface{}, apiKey, targetURL string) (*ProxyResult, error) {
	bodyJSON, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", targetURL, bytes.NewReader(bodyJSON))
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	start := time.Now()
	resp, err := proxyClient.Do(req)
	latency := time.Since(start).Milliseconds()
	if err != nil {
		return nil, err
	}

	bodyBytes, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	var r struct {
		Usage *struct {
			PromptTokens     int64 `json:"prompt_tokens"`
			CompletionTokens int64 `json:"completion_tokens"`
			TotalTokens      int64 `json:"total_tokens"`
		} `json:"usage"`
	}
	json.Unmarshal(bodyBytes, &r)

	usage := Usage{}
	if r.Usage != nil {
		usage = Usage{
			PromptTokens:     r.Usage.PromptTokens,
			CompletionTokens: r.Usage.CompletionTokens,
			TotalTokens:      r.Usage.TotalTokens,
		}
	}

	return &ProxyResult{
		Response: &http.Response{
			StatusCode: resp.StatusCode,
			Body:       io.NopCloser(bytes.NewReader(bodyBytes)),
			Header:     resp.Header,
		},
		Usage:     usage,
		LatencyMs: latency,
	}, nil
}

func openaiStreamProxy(body map[string]interface{}, apiKey, targetURL string) (*http.Response, error) {
	// Clone body and ensure stream: true
	streamBody := make(map[string]interface{})
	for k, v := range body {
		streamBody[k] = v
	}
	streamBody["stream"] = true

	bodyJSON, _ := json.Marshal(streamBody)
	req, _ := http.NewRequest("POST", targetURL, bytes.NewReader(bodyJSON))
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")

	return proxyClient.Do(req)
}

// extractMessageText safely converts a message content field into a plain string.
// OpenAI-style content may be a string, or an array of parts (multimodal: text +
// images). To avoid panics in the Gemini/Cohere converters we extract and join any
// text parts and gracefully ignore non-text parts.
func extractMessageText(content interface{}) string {
	switch c := content.(type) {
	case string:
		return c
	case []interface{}:
		var texts []string
		for _, item := range c {
			if part, ok := item.(map[string]interface{}); ok {
				if t, ok := part["text"].(string); ok {
					texts = append(texts, t)
				} else if t, ok := part["content"].(string); ok {
					texts = append(texts, t)
				}
			}
		}
		return strings.Join(texts, "\n")
	}
	return ""
}

// --- Google Gemini adapter ---

func googleProxy(body map[string]interface{}, apiKey, baseURL string) (*ProxyResult, error) {
	model := ""
	if m, ok := body["model"].(string); ok {
		model = m
	}

	base := strings.TrimRight(baseURL, "/")
	targetURL := base + "/v1beta/models/" + model + ":generateContent"

	geminiBody := convertToGemini(body)
	bodyJSON, _ := json.Marshal(geminiBody)

	req, _ := http.NewRequest("POST", targetURL, bytes.NewReader(bodyJSON))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", apiKey)

	start := time.Now()
	resp, err := proxyClient.Do(req)
	latency := time.Since(start).Milliseconds()
	if err != nil {
		return nil, err
	}

	bodyBytes, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	// Convert Gemini response back to OpenAI format
	var geminiResp map[string]interface{}
	json.Unmarshal(bodyBytes, &geminiResp)

	openaiResp := convertGeminiToOpenAI(geminiResp, model)
	resultJSON, _ := json.Marshal(openaiResp)

	usage := Usage{}
	if um, ok := geminiResp["usageMetadata"].(map[string]interface{}); ok {
		if pt, ok := um["promptTokenCount"].(float64); ok {
			usage.PromptTokens = int64(pt)
		}
		if ct, ok := um["candidatesTokenCount"].(float64); ok {
			usage.CompletionTokens = int64(ct)
		}
		if tt, ok := um["totalTokenCount"].(float64); ok {
			usage.TotalTokens = int64(tt)
		}
	}

	return &ProxyResult{
		Response: &http.Response{
			StatusCode: resp.StatusCode,
			Body:       io.NopCloser(bytes.NewReader(resultJSON)),
			Header: http.Header{
				"Content-Type": []string{"application/json"},
			},
		},
		Usage:     usage,
		LatencyMs: latency,
	}, nil
}

func googleStreamProxy(body map[string]interface{}, apiKey, baseURL string) (*http.Response, error) {
	model := ""
	if m, ok := body["model"].(string); ok {
		model = m
	}

	base := strings.TrimRight(baseURL, "/")
	targetURL := base + "/v1beta/models/" + model + ":streamGenerateContent?alt=sse"

	geminiBody := convertToGemini(body)
	bodyJSON, _ := json.Marshal(geminiBody)

	req, _ := http.NewRequest("POST", targetURL, bytes.NewReader(bodyJSON))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", apiKey)

	resp, err := proxyClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Pass error responses through unchanged so the proxy's error/failover
		// path handles them.
		return resp, nil
	}

	// Stream the upstream Gemini native SSE and translate it into OpenAI-format
	// chunks on the fly via an io.Pipe, so downstream clients (and the Responses
	// translator) receive tokens incrementally.
	pr, pw := io.Pipe()
	go func() {
		defer pw.Close()
		defer resp.Body.Close()
		translateGeminiStreamToOpenAI(resp.Body, pw, model)
	}()

	return &http.Response{
		StatusCode: 200,
		Body:       pr,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
	}, nil
}

// translateGeminiStreamToOpenAI converts a Gemini native SSE stream into an
// OpenAI-format SSE stream, emitting one OpenAI chunk per incremental text
// fragment. Gemini streams either incremental text or periodic full snapshots;
// we keep the running total and only emit the newly-appended suffix so clients
// never see duplicated tokens.
func translateGeminiStreamToOpenAI(src io.Reader, dst io.Writer, model string) {
	id := "gemini-" + fmt.Sprint(time.Now().UnixNano())
	created := time.Now().Unix()

	writeChunk := func(delta map[string]interface{}, finish interface{}, usage map[string]interface{}) {
		obj := map[string]interface{}{
			"id":      id,
			"object":  "chat.completion.chunk",
			"created": created,
			"model":   model,
			"choices": []map[string]interface{}{
				{"index": 0, "delta": delta, "finish_reason": finish},
			},
		}
		if usage != nil {
			obj["usage"] = usage
		}
		b, _ := json.Marshal(obj)
		fmt.Fprintf(dst, "data: %s\n\n", string(b))
	}

	writeChunk(map[string]interface{}{"role": "assistant", "content": ""}, nil, nil)

	scanner := bufio.NewScanner(src)
	scanner.Buffer(make([]byte, 0, 1024*1024), 8*1024*1024)
	lastText := ""
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
		if um, ok := chunk["usageMetadata"].(map[string]interface{}); ok {
			usage = geminiUsageToOpenAI(um)
		}
		text := extractGeminiStreamText(chunk)
		if text == "" {
			// No text in this chunk (usage/finish only) — nothing to stream.
			continue
		}
		if strings.HasPrefix(text, lastText) {
			// Common case: Gemini sends cumulative snapshots, so the new
			// fragment is the suffix of the running total.
			delta := text[len(lastText):]
			lastText = text
			if delta != "" {
				writeChunk(map[string]interface{}{"content": delta}, nil, nil)
			}
		} else {
			// Non-prefix fragment (rare: parallel candidate, or the server sent
			// incremental text instead of a cumulative snapshot). Emit the
			// chunk's text so no tokens are silently dropped. In the cumulative
			// path this branch never triggers, so there is no duplication.
			lastText = text
			writeChunk(map[string]interface{}{"content": text}, nil, nil)
		}
	}

	writeChunk(map[string]interface{}{}, "stop", usage)
	fmt.Fprintf(dst, "data: [DONE]\n\n")
}

// extractGeminiStreamText returns the full assistant text contained in a Gemini
// stream chunk's first candidate (candidates[0].content.parts[].text).
func extractGeminiStreamText(chunk map[string]interface{}) string {
	candidates, _ := chunk["candidates"].([]interface{})
	if len(candidates) == 0 {
		return ""
	}
	cand, ok := candidates[0].(map[string]interface{})
	if !ok {
		return ""
	}
	cont, ok := cand["content"].(map[string]interface{})
	if !ok {
		return ""
	}
	parts, ok := cont["parts"].([]interface{})
	if !ok {
		return ""
	}
	var texts []string
	for _, p := range parts {
		if pm, ok := p.(map[string]interface{}); ok {
			if t, ok := pm["text"].(string); ok {
				texts = append(texts, t)
			}
		}
	}
	return strings.Join(texts, "\n")
}

func geminiUsageToOpenAI(um map[string]interface{}) map[string]interface{} {
	out := map[string]interface{}{}
	if v, ok := um["promptTokenCount"].(float64); ok {
		out["prompt_tokens"] = int64(v)
	}
	if v, ok := um["candidatesTokenCount"].(float64); ok {
		out["completion_tokens"] = int64(v)
	}
	if v, ok := um["totalTokenCount"].(float64); ok {
		out["total_tokens"] = int64(v)
	}
	return out
}

func convertToGemini(body map[string]interface{}) map[string]interface{} {
	gemini := make(map[string]interface{})
	var contents []map[string]interface{}
	var systemTexts []string

	messages, _ := body["messages"].([]interface{})
	for _, msg := range messages {
		m, ok := msg.(map[string]interface{})
		if !ok {
			continue
		}
		role, _ := m["role"].(string)
		if role == "" {
			role = "user"
		}
		parts := convertContentToGeminiParts(m["content"])

		if role == "system" {
			// System instructions are text-only in Gemini; keep any text parts.
			var texts []string
			for _, p := range parts {
				if t, ok := p["text"].(string); ok {
					texts = append(texts, t)
				}
			}
			if joined := strings.Join(texts, "\n"); joined != "" {
				systemTexts = append(systemTexts, joined)
			}
			continue
		}

		// Gemini requires non-empty content; an otherwise-empty turn still gets
		// one (possibly empty) text part so the turn stays valid.
		if len(parts) == 0 {
			parts = []map[string]interface{}{{"text": ""}}
		}

		geminiRole := "user"
		if role == "assistant" {
			geminiRole = "model"
		}

		contents = append(contents, map[string]interface{}{
			"role":  geminiRole,
			"parts": parts,
		})
	}

	gemini["contents"] = contents
	if len(systemTexts) > 0 {
		gemini["systemInstruction"] = map[string]interface{}{
			"parts": []map[string]interface{}{{"text": strings.Join(systemTexts, "\n")}},
		}
	}

	genConfig := make(map[string]interface{})
	if t, ok := body["temperature"].(float64); ok {
		genConfig["temperature"] = t
	}
	if tp, ok := body["top_p"].(float64); ok {
		genConfig["topP"] = tp
	}
	if mt, ok := body["max_tokens"].(float64); ok {
		genConfig["maxOutputTokens"] = int64(mt)
	}
	gemini["generationConfig"] = genConfig

	return gemini
}

// convertContentToGeminiParts turns an OpenAI-style message content (a plain
// string or an array of typed parts) into Gemini content parts, preserving
// multimodal input: text parts stay as text, and base64 data: image URLs become
// inline_data parts so vision requests are forwarded instead of silently
// dropped. Returns nil for empty/unsupported content.
func convertContentToGeminiParts(content interface{}) []map[string]interface{} {
	switch c := content.(type) {
	case string:
		if c == "" {
			return nil
		}
		return []map[string]interface{}{{"text": c}}
	case []interface{}:
		var parts []map[string]interface{}
		for _, item := range c {
			part, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			switch part["type"] {
			case "text":
				if t, ok := part["text"].(string); ok && t != "" {
					parts = append(parts, map[string]interface{}{"text": t})
				}
			case "image_url":
				if img, ok := part["image_url"].(map[string]interface{}); ok {
					if url, ok := img["url"].(string); ok {
						if g := geminiInlineData(url); g != nil {
							parts = append(parts, g)
						}
					}
				}
			}
		}
		return parts
	}
	return nil
}

// geminiInlineData converts an image URL into a Gemini inline_data part. It
// supports base64 data: URLs (the common case for vision requests from
// OpenAI-compatible clients). Remote http(s) URLs are not fetched inline here —
// doing so would add latency and network coupling to the proxy path — so they
// are skipped rather than silently forwarded as malformed text.
func geminiInlineData(url string) map[string]interface{} {
	if !strings.HasPrefix(url, "data:") {
		return nil
	}
	comma := strings.Index(url, ",")
	if comma < 0 {
		return nil
	}
	meta := url[len("data:"):comma]
	data := url[comma+1:]
	if !strings.HasPrefix(meta, "image/") {
		return nil
	}
	mime := meta
	if semi := strings.Index(meta, ";"); semi >= 0 {
		mime = meta[:semi]
	}
	return map[string]interface{}{
		"inline_data": map[string]interface{}{
			"mime_type": mime,
			"data":      data,
		},
	}
}

func convertGeminiToOpenAI(geminiResp map[string]interface{}, model string) map[string]interface{} {
	var choices []map[string]interface{}
	candidates, _ := geminiResp["candidates"].([]interface{})

	for _, c := range candidates {
		cand, ok := c.(map[string]interface{})
		if !ok {
			continue
		}
		content := ""
		if cont, ok := cand["content"].(map[string]interface{}); ok {
			if parts, ok := cont["parts"].([]interface{}); ok {
				var texts []string
				for _, p := range parts {
					if pm, ok := p.(map[string]interface{}); ok {
						if t, ok := pm["text"].(string); ok {
							texts = append(texts, t)
						}
					}
				}
				content = strings.Join(texts, "\n")
			}
		}
		choices = append(choices, map[string]interface{}{
			"index":         len(choices),
			"message":       map[string]interface{}{"role": "assistant", "content": content},
			"finish_reason": "stop",
		})
	}

	result := map[string]interface{}{
		"id":      "gemini-" + time.Now().Format(time.RFC3339Nano),
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   model,
		"choices": choices,
	}

	if um, ok := geminiResp["usageMetadata"].(map[string]interface{}); ok {
		result["usage"] = map[string]interface{}{
			"prompt_tokens":     um["promptTokenCount"],
			"completion_tokens": um["candidatesTokenCount"],
			"total_tokens":      um["totalTokenCount"],
		}
	}

	return result
}

// --- Cohere adapter ---

func cohereProxy(body map[string]interface{}, apiKey, baseURL string) (*ProxyResult, error) {
	base := strings.TrimRight(baseURL, "/")
	targetURL := base + "/v2/chat"

	cohereBody := convertToCohere(body)
	bodyJSON, _ := json.Marshal(cohereBody)

	req, _ := http.NewRequest("POST", targetURL, bytes.NewReader(bodyJSON))
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	start := time.Now()
	resp, err := proxyClient.Do(req)
	latency := time.Since(start).Milliseconds()
	if err != nil {
		return nil, err
	}

	bodyBytes, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	var cohereResp map[string]interface{}
	json.Unmarshal(bodyBytes, &cohereResp)

	openaiResp := convertCohereToOpenAI(cohereResp, body)
	resultJSON, _ := json.Marshal(openaiResp)

	usage := Usage{}
	if u := cohereUsageToOpenAI(cohereResp); u != nil {
		if pt, ok := u["prompt_tokens"].(int64); ok {
			usage.PromptTokens = pt
		}
		if ct, ok := u["completion_tokens"].(int64); ok {
			usage.CompletionTokens = ct
		}
		usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	}

	return &ProxyResult{
		Response: &http.Response{
			StatusCode: resp.StatusCode,
			Body:       io.NopCloser(bytes.NewReader(resultJSON)),
			Header:     http.Header{"Content-Type": []string{"application/json"}},
		},
		Usage:     usage,
		LatencyMs: latency,
	}, nil
}

func cohereStreamProxy(body map[string]interface{}, apiKey, baseURL string) (*http.Response, error) {
	base := strings.TrimRight(baseURL, "/")
	targetURL := base + "/v2/chat"

	cohereBody := convertToCohere(body)
	cohereBody["stream"] = true
	bodyJSON, _ := json.Marshal(cohereBody)

	req, _ := http.NewRequest("POST", targetURL, bytes.NewReader(bodyJSON))
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := proxyClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp, nil
	}

	model := ""
	if m, ok := body["model"].(string); ok {
		model = m
	}

	pr, pw := io.Pipe()
	go func() {
		defer pw.Close()
		defer resp.Body.Close()
		translateCohereStreamToOpenAI(resp.Body, pw, model)
	}()

	return &http.Response{
		StatusCode: 200,
		Body:       pr,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
	}, nil
}

// translateCohereStreamToOpenAI converts Cohere's v2 native SSE stream into an
// OpenAI-format SSE stream. v2 emits "content-delta" events whose
// delta.message.content.text carries the incremental token (NOT cumulative), so
// each fragment is emitted directly for true token-by-token streaming.
func translateCohereStreamToOpenAI(src io.Reader, dst io.Writer, model string) {
	id := "cohere-" + fmt.Sprint(time.Now().UnixNano())
	created := time.Now().Unix()

	writeChunk := func(delta map[string]interface{}, finish interface{}, usage map[string]interface{}) {
		obj := map[string]interface{}{
			"id":      id,
			"object":  "chat.completion.chunk",
			"created": created,
			"model":   model,
			"choices": []map[string]interface{}{
				{"index": 0, "delta": delta, "finish_reason": finish},
			},
		}
		if usage != nil {
			obj["usage"] = usage
		}
		b, _ := json.Marshal(obj)
		fmt.Fprintf(dst, "data: %s\n\n", string(b))
	}

	writeChunk(map[string]interface{}{"role": "assistant", "content": ""}, nil, nil)

	scanner := bufio.NewScanner(src)
	scanner.Buffer(make([]byte, 0, 1024*1024), 8*1024*1024)
	var usage map[string]interface{}
	eventType := ""
	toolCallIndex := -1
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "event:") {
			eventType = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			continue
		}
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
		// Prefer the SSE event name; fall back to the embedded "type" field for
		// providers that omit the event: line.
		typ := eventType
		if typ == "" {
			if t, ok := chunk["type"].(string); ok {
				typ = t
			}
		}

		switch typ {
		case "content-delta":
			// delta.message.content.text is the incremental token for this event.
			if delta, ok := chunk["delta"].(map[string]interface{}); ok {
				if msg, ok := delta["message"].(map[string]interface{}); ok {
					if content, ok := msg["content"].(map[string]interface{}); ok {
						if text, ok := content["text"].(string); ok && text != "" {
							writeChunk(map[string]interface{}{"content": text}, nil, nil)
						}
					}
				}
			}
		case "tool-plan-delta":
			// Cohere emits the model's plan text here; OpenAI clients don't model
			// this, so we skip it — the eventual tool_calls are what matter.
			continue
		case "tool-call-start":
			idx := eventIndex(chunk, toolCallIndex+1)
			toolCallIndex = idx
			id, name := cohereStreamToolCall(chunk)
			writeChunk(map[string]interface{}{
				"tool_calls": []map[string]interface{}{
					{
						"index": idx,
						"id":    id,
						"type":  "function",
						"function": map[string]interface{}{
							"name": name,
						},
					},
				},
			}, nil, nil)
		case "tool-call-delta":
			idx := eventIndex(chunk, toolCallIndex)
			if idx < 0 {
				idx = toolCallIndex
			}
			if args := cohereStreamToolArgs(chunk); args != "" {
				writeChunk(map[string]interface{}{
					"tool_calls": []map[string]interface{}{
						{
							"index": idx,
							"function": map[string]interface{}{
								"arguments": args,
							},
						},
					},
				}, nil, nil)
			}
		case "tool-call-end":
			// No OpenAI equivalent; the next content-delta / message-end closes it.
			continue
		case "message-end", "stream-end":
			// Usage may arrive on either the final message-end or stream-end event.
			if u := cohereUsageToOpenAI(chunk); u != nil {
				usage = u
			} else if se, ok := chunk["stream_end"].(map[string]interface{}); ok {
				if u := cohereUsageToOpenAI(se); u != nil {
					usage = u
				}
			} else if msg, ok := chunk["message_end"].(map[string]interface{}); ok {
				if u := cohereUsageToOpenAI(msg); u != nil {
					usage = u
				}
			}
		}
	}

	writeChunk(map[string]interface{}{}, "stop", usage)
	fmt.Fprintf(dst, "data: [DONE]\n\n")
}

// eventIndex returns the SSE event "index" if present in the payload, otherwise
// the supplied fallback (Cohere includes the index on streaming tool events; we
// still track our own counter for providers that omit it).
func eventIndex(chunk map[string]interface{}, fallback int) int {
	if i, ok := chunk["index"].(float64); ok {
		return int(i)
	}
	return fallback
}

// cohereStreamToolCall extracts the tool id/name from a tool-call-start event.
func cohereStreamToolCall(chunk map[string]interface{}) (id, name string) {
	if tc := cohereStreamToolCallObject(chunk); tc != nil {
		id, _ = tc["id"].(string)
		if fn, ok := tc["function"].(map[string]interface{}); ok {
			name, _ = fn["name"].(string)
		}
	}
	return
}

// cohereStreamToolArgs extracts the incremental arguments JSON from a
// tool-call-delta event.
func cohereStreamToolArgs(chunk map[string]interface{}) string {
	if tc := cohereStreamToolCallObject(chunk); tc != nil {
		if fn, ok := tc["function"].(map[string]interface{}); ok {
			if a, ok := fn["arguments"].(string); ok {
				return a
			}
		}
	}
	return ""
}

// cohereStreamToolCallObject extracts the tool_calls object from a Cohere v2
// tool event. The object may sit directly under "tool_calls" or nested in
// "delta.message.tool_calls" depending on the event variant.
func cohereStreamToolCallObject(chunk map[string]interface{}) map[string]interface{} {
	if tc, ok := chunk["tool_calls"].(map[string]interface{}); ok {
		return tc
	}
	if delta, ok := chunk["delta"].(map[string]interface{}); ok {
		if msg, ok := delta["message"].(map[string]interface{}); ok {
			if tc, ok := msg["tool_calls"].(map[string]interface{}); ok {
				return tc
			}
		}
	}
	return nil
}

// cohereUsageToOpenAI extracts OpenAI-style token counts from a Cohere v2
// response. v2 exposes usage at usage.tokens.{input_tokens,output_tokens}; older
// v1-style payloads nested it under usage.billed_units or meta.billed_units.
func cohereUsageToOpenAI(cohereResp map[string]interface{}) map[string]interface{} {
	var bu map[string]interface{}
	if usage, ok := cohereResp["usage"].(map[string]interface{}); ok {
		if tokens, ok := usage["tokens"].(map[string]interface{}); ok {
			bu = tokens
		} else if billed, ok := usage["billed_units"].(map[string]interface{}); ok {
			bu = billed
		}
	}
	if bu == nil {
		if meta, ok := cohereResp["meta"].(map[string]interface{}); ok {
			if billed, ok := meta["billed_units"].(map[string]interface{}); ok {
				bu = billed
			}
		}
	}
	if bu == nil {
		return nil
	}

	out := map[string]interface{}{}
	if v, ok := bu["input_tokens"].(float64); ok {
		out["prompt_tokens"] = int64(v)
	}
	if v, ok := bu["output_tokens"].(float64); ok {
		out["completion_tokens"] = int64(v)
	}
	if pt, ok := out["prompt_tokens"].(int64); ok {
		if ct, ok := out["completion_tokens"].(int64); ok {
			out["total_tokens"] = pt + ct
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// convertToCohere translates an OpenAI-style request body into Cohere v2
// (/v2/chat) format. v2 uses lowercase roles ("user"/"assistant"/"system"/"tool")
// and a string "content" per message. OpenAI tool definitions are converted to
// Cohere's parameter_definitions shape, assistant tool_calls are passed through,
// and tool results become Cohere's {role:"tool", tool_call_id, content:[{type:
// "document", document:{data}}]} form.
func convertToCohere(body map[string]interface{}) map[string]interface{} {
	cohere := make(map[string]interface{})
	var messages []map[string]interface{}

	rawMessages, _ := body["messages"].([]interface{})
	for _, msg := range rawMessages {
		m, ok := msg.(map[string]interface{})
		if !ok {
			continue
		}
		role, _ := m["role"].(string)
		if role == "" {
			role = "user"
		}

		cm := map[string]interface{}{"role": role}
		switch role {
		case "tool":
			// OpenAI tool result: {role:"tool", tool_call_id, content}.
			if id, ok := m["tool_call_id"].(string); ok {
				cm["tool_call_id"] = id
			}
			cm["content"] = cohereToolResultContent(m["content"])
		case "assistant":
			cm["content"] = extractMessageText(m["content"])
			if tcs, ok := m["tool_calls"].([]interface{}); ok && len(tcs) > 0 {
				cm["tool_calls"] = convertToolCallsToCohere(tcs)
			}
		default:
			cm["content"] = extractMessageText(m["content"])
		}

		messages = append(messages, cm)
	}

	cohere["model"] = body["model"]
	cohere["messages"] = messages
	if t, ok := body["temperature"].(float64); ok {
		cohere["temperature"] = t
	}
	if mt, ok := body["max_tokens"].(float64); ok {
		cohere["max_tokens"] = int64(mt)
	}
	if tools, ok := body["tools"].([]interface{}); ok && len(tools) > 0 {
		cohere["tools"] = convertToolsToCohere(tools)
	}
	if tc, ok := body["tool_choice"].(string); ok && tc == "required" {
		cohere["tool_choice"] = "REQUIRED"
	}

	return cohere
}

// convertToolsToCohere maps OpenAI tool definitions (JSON-schema "parameters")
// into Cohere v2's parameter_definitions shape.
func convertToolsToCohere(tools []interface{}) []interface{} {
	out := make([]interface{}, 0, len(tools))
	for _, t := range tools {
		tool, ok := t.(map[string]interface{})
		if !ok {
			continue
		}
		// Unwrap the OpenAI {type:"function", function:{...}} envelope.
		fn := tool
		if f, ok := tool["function"].(map[string]interface{}); ok {
			fn = f
		}
		name, _ := fn["name"].(string)
		desc, _ := fn["description"].(string)
		cohereTool := map[string]interface{}{"name": name}
		if desc != "" {
			cohereTool["description"] = desc
		}
		if params, ok := fn["parameters"].(map[string]interface{}); ok {
			cohereTool["parameter_definitions"] = convertParametersToCohere(params)
		}
		out = append(out, cohereTool)
	}
	return out
}

// convertParametersToCohere flattens a JSON-schema "parameters" object into
// Cohere's parameter_definitions map ({name: {description, type, required}}).
func convertParametersToCohere(params map[string]interface{}) map[string]interface{} {
	defs := map[string]interface{}{}
	props, _ := params["properties"].(map[string]interface{})
	required, _ := params["required"].([]interface{})
	requiredSet := map[string]bool{}
	for _, r := range required {
		if s, ok := r.(string); ok {
			requiredSet[s] = true
		}
	}
	for pname, pval := range props {
		p, ok := pval.(map[string]interface{})
		if !ok {
			continue
		}
		pd := map[string]interface{}{}
		if d, ok := p["description"].(string); ok {
			pd["description"] = d
		}
		if ty, ok := p["type"].(string); ok {
			pd["type"] = cohereParamType(ty)
		}
		pd["required"] = requiredSet[pname]
		defs[pname] = pd
	}
	return defs
}

// cohereParamType maps a JSON-schema type to Cohere's parameter type vocabulary.
func cohereParamType(t string) string {
	switch t {
	case "string":
		return "str"
	case "integer":
		return "int"
	case "number":
		return "float"
	case "boolean":
		return "bool"
	case "object":
		return "dict"
	case "array":
		return "list"
	default:
		return "str"
	}
}

// convertToolCallsToCohere maps OpenAI assistant tool_calls into Cohere's shape
// (arguments stays a JSON string, matching the v2 response format).
func convertToolCallsToCohere(tcs []interface{}) []interface{} {
	out := make([]interface{}, 0, len(tcs))
	for _, tc := range tcs {
		c, ok := tc.(map[string]interface{})
		if !ok {
			continue
		}
		cc := map[string]interface{}{"type": "function"}
		if id, ok := c["id"].(string); ok {
			cc["id"] = id
		}
		fn := map[string]interface{}{}
		if name, ok := c["function"].(map[string]interface{}); ok {
			if n, ok := name["name"].(string); ok {
				fn["name"] = n
			}
			if a, ok := name["arguments"].(string); ok {
				fn["arguments"] = a
			}
		}
		cc["function"] = fn
		out = append(out, cc)
	}
	return out
}

// cohereToolResultContent turns an OpenAI tool result content (string or
// structured parts) into Cohere's [{type:"document", document:{data}}] form.
func cohereToolResultContent(content interface{}) []interface{} {
	text := extractMessageText(content)
	if text == "" {
		if b, err := json.Marshal(content); err == nil {
			text = string(b)
		}
	}
	return []interface{}{
		map[string]interface{}{
			"type":     "document",
			"document": map[string]interface{}{"data": text},
		},
	}
}

// convertCohereToOpenAI translates a Cohere v2 (/v2/chat) non-streaming
// response into OpenAI format. v2 nests the reply under message.content as an
// array of {type, text} blocks; tool calls arrive under message.tool_calls.
func convertCohereToOpenAI(cohereResp map[string]interface{}, body map[string]interface{}) map[string]interface{} {
	text := ""
	var toolCalls []interface{}
	if msg, ok := cohereResp["message"].(map[string]interface{}); ok {
		if content, ok := msg["content"].([]interface{}); ok {
			var parts []string
			for _, blk := range content {
				if b, ok := blk.(map[string]interface{}); ok {
					if t, ok := b["text"].(string); ok {
						parts = append(parts, t)
					}
				}
			}
			text = strings.Join(parts, "")
		} else if t, ok := msg["content"].(string); ok {
			// Some v2 variants return content as a plain string.
			text = t
		}
		if tcs, ok := msg["tool_calls"].([]interface{}); ok && len(tcs) > 0 {
			toolCalls = convertToolCallsFromCohere(tcs)
		}
	}

	message := map[string]interface{}{
		"role":    "assistant",
		"content": text,
	}
	finishReason := "stop"
	if len(toolCalls) > 0 {
		message["tool_calls"] = toolCalls
		finishReason = "tool_calls"
	}

	result := map[string]interface{}{
		"id":      "cohere-" + time.Now().Format(time.RFC3339Nano),
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   body["model"],
		"choices": []map[string]interface{}{
			{
				"index":        0,
				"message":      message,
				"finish_reason": finishReason,
			},
		},
	}

	if usage := cohereUsageToOpenAI(cohereResp); usage != nil {
		result["usage"] = usage
	}

	return result
}

// convertToolCallsFromCohere maps Cohere v2 tool_calls (arguments is a JSON
// string) into OpenAI format.
func convertToolCallsFromCohere(tcs []interface{}) []interface{} {
	out := make([]interface{}, 0, len(tcs))
	for _, tc := range tcs {
		c, ok := tc.(map[string]interface{})
		if !ok {
			continue
		}
		cc := map[string]interface{}{"type": "function"}
		if id, ok := c["id"].(string); ok {
			cc["id"] = id
		}
		fn := map[string]interface{}{}
		if name, ok := c["function"].(map[string]interface{}); ok {
			if n, ok := name["name"].(string); ok {
				fn["name"] = n
			}
			if a, ok := name["arguments"].(string); ok {
				fn["arguments"] = a
			}
		}
		cc["function"] = fn
		out = append(out, cc)
	}
	return out
}

// FetchProviderModels fetches the upstream model list using the adapter-appropriate
// endpoint. OpenAI-compatible providers are queried live via /v1/models; Google Gemini
// is queried via its v1beta/models endpoint; providers without a public list API
// (e.g. Cohere) fall back to their static model list. Returns nil if nothing usable
// was found.
func FetchProviderModels(def config.Provider, apiKey string) []string {
	switch def.Adapter {
	case "google":
		if models := fetchGoogleModels(def.BaseURL, apiKey); len(models) > 0 {
			return models
		}
		return def.Models
	case "cohere":
		// Cohere exposes no public model-listing endpoint; rely on the static list.
		return def.Models
	default:
		if models := fetchOpenAIModels(def.BaseURL, apiKey); len(models) > 0 {
			return models
		}
		return def.Models
	}
}

func fetchOpenAIModels(baseURL, apiKey string) []string {
	base := strings.TrimRight(baseURL, "/")
	modelsURL := base + "/v1/models"
	if strings.HasSuffix(base, "/v1") {
		modelsURL = base + "/models"
	}
	return listModels(modelsURL, apiKey, func(data map[string]interface{}) []string {
		var out []string
		if arr, ok := data["data"].([]interface{}); ok {
			for _, item := range arr {
				if m, ok := item.(map[string]interface{}); ok {
					if id, ok := m["id"].(string); ok && id != "" {
						out = append(out, id)
					}
				}
			}
		}
		return out
	})
}

func fetchGoogleModels(baseURL, apiKey string) []string {
	base := strings.TrimRight(baseURL, "/")
	modelsURL := base + "/v1beta/models?key=" + apiKey
	return listModels(modelsURL, "", func(data map[string]interface{}) []string {
		var out []string
		if arr, ok := data["models"].([]interface{}); ok {
			for _, item := range arr {
				if m, ok := item.(map[string]interface{}); ok {
					if name, ok := m["name"].(string); ok && name != "" {
						// Upstream returns fully-qualified names like "models/gemini-1.5-pro".
						// The chat path expects the bare id, so strip the "models/" prefix.
						out = append(out, strings.TrimPrefix(name, "models/"))
					}
				}
			}
		}
		return out
	})
}

// listModels performs a GET against the given URL and decodes the model list using
// the provided parse callback. It uses the timeout-guarded modelFetchClient.
func listModels(url, apiKey string, parse func(map[string]interface{}) []string) []string {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	resp, err := modelFetchClient.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil
	}
	var data map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil
	}
	return parse(data)
}

// Simple proxy that passes through OpenAI-compatible request
func openaiProxyRaw(bodyJSON []byte, apiKey, targetURL string) (*http.Response, error) {
	req, _ := http.NewRequest("POST", targetURL, bytes.NewReader(bodyJSON))
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")
	return proxyClient.Do(req)
}

// Import config for types only (alias to avoid conflict)
type UserProvider = config.UserProvider
