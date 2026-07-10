package adapters

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// collectOpenAIDeltas parses an OpenAI-format SSE body and returns the
// concatenated content deltas, whether the [DONE] sentinel is present, and
// whether a usage object appeared in any chunk.
func collectOpenAIDeltas(body string) (string, bool, bool) {
	var sb strings.Builder
	hasDone := false
	hasUsage := false
	for _, ln := range strings.Split(body, "\n") {
		ln = strings.TrimSpace(ln)
		if !strings.HasPrefix(ln, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(ln, "data:"))
		if data == "" {
			continue
		}
		if data == "[DONE]" {
			hasDone = true
			continue
		}
		var chunk map[string]interface{}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}
		if u, ok := chunk["usage"]; ok && u != nil {
			hasUsage = true
		}
		choices, _ := chunk["choices"].([]interface{})
		if len(choices) == 0 {
			continue
		}
		ch, _ := choices[0].(map[string]interface{})
		delta, _ := ch["delta"].(map[string]interface{})
		if c, ok := delta["content"].(string); ok {
			sb.WriteString(c)
		}
	}
	return sb.String(), hasDone, hasUsage
}

func TestTranslateGeminiStream(t *testing.T) {
	// Two Gemini events: first carries "Hel", second the full "Hello world"
	// snapshot. The translator must emit only the incremental suffix.
	native := strings.Join([]string{
		`data: {"candidates":[{"content":{"parts":[{"text":"Hel"}]}}]}`,
		`data: {"candidates":[{"content":{"parts":[{"text":"Hello world"}]}}],"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":2,"totalTokenCount":7}}`,
		"",
	}, "\n")

	var buf bytes.Buffer
	translateGeminiStreamToOpenAI(strings.NewReader(native), &buf, "gemini-test")

	text, hasDone, hasUsage := collectOpenAIDeltas(buf.String())
	if text != "Hello world" {
		t.Fatalf("gemini deltas = %q, want %q", text, "Hello world")
	}
	if !hasDone {
		t.Fatalf("gemini stream missing [DONE]")
	}
	if !hasUsage {
		t.Fatalf("gemini stream missing usage in final chunk")
	}
}

func TestTranslateCohereStream(t *testing.T) {
	// v2 streams "content-delta" events; each delta.message.content.text is the
	// incremental token (NOT cumulative), so the translator must emit each
	// fragment directly. Usage arrives on the stream-end event under
	// stream_end.usage.tokens.
	native := strings.Join([]string{
		`event: content-delta`,
		`data: {"type":"content-delta","index":0,"delta":{"message":{"content":{"text":"Hel"}}}}`,
		``,
		`event: content-delta`,
		`data: {"type":"content-delta","index":0,"delta":{"message":{"content":{"text":"lo world"}}}}`,
		``,
		`event: stream-end`,
		`data: {"type":"stream-end","stream_end":{"finish_reason":"COMPLETE","usage":{"tokens":{"input_tokens":5,"output_tokens":2}}}}`,
		"",
	}, "\n")

	var buf bytes.Buffer
	translateCohereStreamToOpenAI(strings.NewReader(native), &buf, "cohere-test")

	text, hasDone, hasUsage := collectOpenAIDeltas(buf.String())
	if text != "Hello world" {
		t.Fatalf("cohere deltas = %q, want %q", text, "Hello world")
	}
	if !hasDone {
		t.Fatalf("cohere stream missing [DONE]")
	}
	if !hasUsage {
		t.Fatalf("cohere stream missing usage in final chunk")
	}
}

func TestTranslateCohereStreamNoEventType(t *testing.T) {
	// Some Cohere v2 deployments omit the SSE "event:" line and embed the type
	// in the data JSON. The translator must still pick up content-delta.
	native := strings.Join([]string{
		`data: {"type":"content-delta","delta":{"message":{"content":{"text":"Hi"}}}}`,
		``,
		`data: {"type":"content-delta","delta":{"message":{"content":{"text":" there"}}}}`,
		``,
		`data: {"type":"stream-end","stream_end":{"usage":{"tokens":{"input_tokens":3,"output_tokens":1}}}}`,
		"",
	}, "\n")

	var buf bytes.Buffer
	translateCohereStreamToOpenAI(strings.NewReader(native), &buf, "cohere-test")

	text, hasDone, hasUsage := collectOpenAIDeltas(buf.String())
	if text != "Hi there" {
		t.Fatalf("cohere deltas (no event line) = %q, want %q", text, "Hi there")
	}
	if !hasDone {
		t.Fatalf("cohere stream missing [DONE]")
	}
	if !hasUsage {
		t.Fatalf("cohere stream missing usage in final chunk")
	}
}

// collectOpenAIToolCalls parses an OpenAI-format SSE body and returns, per
// tool_call index, its id/name and the concatenated arguments string.
func collectOpenAIToolCalls(body string) map[int]map[string]string {
	out := map[int]map[string]string{}
	for _, ln := range strings.Split(body, "\n") {
		ln = strings.TrimSpace(ln)
		if !strings.HasPrefix(ln, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(ln, "data:"))
		if data == "" || data == "[DONE]" {
			continue
		}
		var chunk map[string]interface{}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}
		choices, _ := chunk["choices"].([]interface{})
		if len(choices) == 0 {
			continue
		}
		ch, _ := choices[0].(map[string]interface{})
		delta, _ := ch["delta"].(map[string]interface{})
		tcs, _ := delta["tool_calls"].([]interface{})
		for _, tc := range tcs {
			m, ok := tc.(map[string]interface{})
			if !ok {
				continue
			}
			idx := -1
			if i, ok := m["index"].(float64); ok {
				idx = int(i)
			}
			entry, ok := out[idx]
			if !ok {
				entry = map[string]string{}
				out[idx] = entry
			}
			if id, ok := m["id"].(string); ok {
				entry["id"] = id
			}
			if fn, ok := m["function"].(map[string]interface{}); ok {
				if n, ok := fn["name"].(string); ok {
					entry["name"] = n
				}
				if a, ok := fn["arguments"].(string); ok {
					entry["arguments"] = entry["arguments"] + a
				}
			}
		}
	}
	return out
}

// TestTranslateCohereStreamToolCalls verifies that Cohere v2 tool-call streaming
// events (tool-call-start with id+name, tool-call-delta with incremental
// arguments, tool-call-end) are reassembled into OpenAI tool_calls chunks.
func TestTranslateCohereStreamToolCalls(t *testing.T) {
	native := strings.Join([]string{
		`event: tool-call-start`,
		`data: {"type":"tool-call-start","index":0,"delta":{"message":{"tool_calls":{"id":"call_1","type":"function","function":{"name":"get_weather","arguments":""}}}}}`,
		``,
		`event: tool-call-delta`,
		`data: {"type":"tool-call-delta","index":0,"delta":{"message":{"tool_calls":{"function":{"arguments":"{\"city\":"}}}}}`,
		``,
		`event: tool-call-delta`,
		`data: {"type":"tool-call-delta","index":0,"delta":{"message":{"tool_calls":{"function":{"arguments":"\"SF\"}"}}}}}`,
		``,
		`event: tool-call-end`,
		`data: {"type":"tool-call-end","index":0,"delta":{}}`,
		``,
		`event: message-end`,
		`data: {"type":"message-end","message_end":{"finish_reason":"COMPLETE","usage":{"tokens":{"input_tokens":8,"output_tokens":4}}}}`,
		"",
	}, "\n")

	var buf bytes.Buffer
	translateCohereStreamToOpenAI(strings.NewReader(native), &buf, "cohere-test")

	calls := collectOpenAIToolCalls(buf.String())
	c, ok := calls[0]
	if !ok {
		t.Fatalf("expected a tool_call at index 0, got %v", calls)
	}
	if c["id"] != "call_1" {
		t.Fatalf("tool id = %q, want call_1", c["id"])
	}
	if c["name"] != "get_weather" {
		t.Fatalf("tool name = %q, want get_weather", c["name"])
	}
	wantArgs := "{\"city\":\"SF\"}"
	if c["arguments"] != wantArgs {
		t.Fatalf("tool arguments = %q, want %q", c["arguments"], wantArgs)
	}
}

// TestTranslateGeminiStreamNonPrefix verifies the non-prefix fallback: when a
// streamed chunk is NOT a prefix of the accumulated text (e.g. the server sent
// incremental text rather than a cumulative snapshot), the fragment is emitted
// instead of being silently dropped.
func TestTranslateGeminiStreamNonPrefix(t *testing.T) {
	native := strings.Join([]string{
		`data: {"candidates":[{"content":{"parts":[{"text":"Hello"}]}}]}`,
		`data: {"candidates":[{"content":{"parts":[{"text":" world"}]}}]}`,
		"",
	}, "\n")

	var buf bytes.Buffer
	translateGeminiStreamToOpenAI(strings.NewReader(native), &buf, "gemini-test")

	text, _, _ := collectOpenAIDeltas(buf.String())
	if text != "Hello world" {
		t.Fatalf("gemini non-prefix deltas = %q, want %q", text, "Hello world")
	}
}
