package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ai-gateway/internal/utils"
)

// fakeOpenAISSE returns an OpenAI-format SSE body (role delta, two content
// deltas, stop delta with usage, then [DONE]) like a real streaming upstream.
func fakeOpenAISSE() string {
	return strings.Join([]string{
		`data: {"id":"chatcmpl-1","object":"chat.completion.chunk","model":"gpt-test","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl-1","model":"gpt-test","choices":[{"index":0,"delta":{"content":"Hello "},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl-1","model":"gpt-test","choices":[{"index":0,"delta":{"content":"world"},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl-1","model":"gpt-test","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":2,"total_tokens":7}}`,
		`data: [DONE]`,
		"",
	}, "\n\n")
}

func TestStreamResponses(t *testing.T) {
	upstream := &http.Response{
		StatusCode: 200,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(fakeOpenAISSE())),
	}

	rec := httptest.NewRecorder()
	streamResponsesTranslate(rec, upstream, "gpt-test")

	if ct := rec.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("expected text/event-stream, got %q", ct)
	}

	var events []string
	var deltas []string
	var seqs []int
	var completedText string
	var completedUsage map[string]interface{}

	lines := strings.Split(rec.Body.String(), "\n")
	var curData, curEvent string
	for _, ln := range lines {
		switch {
		case strings.HasPrefix(ln, "event: "):
			curEvent = strings.TrimPrefix(ln, "event: ")
		case strings.HasPrefix(ln, "data: "):
			curData = strings.TrimPrefix(ln, "data: ")
		case ln == "":
			if curEvent != "" && curData != "" {
				events = append(events, curEvent)
				var obj map[string]interface{}
				if err := json.Unmarshal([]byte(curData), &obj); err == nil {
					if s, ok := obj["sequence_number"].(float64); ok {
						seqs = append(seqs, int(s))
					}
					if curEvent == "response.output_text.delta" {
						if d, ok := obj["delta"].(string); ok {
							deltas = append(deltas, d)
						}
					}
					if curEvent == "response.completed" {
						if r, ok := obj["response"].(map[string]interface{}); ok {
							if out, ok := r["output"].([]interface{}); ok && len(out) > 0 {
								if item, ok := out[0].(map[string]interface{}); ok {
									if content, ok := item["content"].([]interface{}); ok && len(content) > 0 {
										if part, ok := content[0].(map[string]interface{}); ok {
											completedText, _ = part["text"].(string)
										}
									}
								}
							}
							if u, ok := r["usage"].(map[string]interface{}); ok {
								completedUsage = u
							}
						}
					}
				}
				curEvent, curData = "", ""
			}
		}
	}

	// 1. Lifecycle ordering
	expectOrder := []string{
		"response.created",
		"response.in_progress",
		"response.output_item.added",
		"response.content_part.added",
		"response.output_text.delta",
		"response.output_text.delta",
		"response.output_text.done",
		"response.content_part.done",
		"response.output_item.done",
		"response.completed",
	}
	if len(events) != len(expectOrder) {
		t.Fatalf("event count = %d, want %d\nfull events: %v", len(events), len(expectOrder), events)
	}
	for i, e := range expectOrder {
		if events[i] != e {
			t.Fatalf("event[%d] = %q, want %q (full: %v)", i, events[i], e, events)
		}
	}

	// 2. Token deltas concatenated == full text
	if got := strings.Join(deltas, ""); got != "Hello world" {
		t.Fatalf("deltas = %v, want [Hello , world]", deltas)
	}

	// 3. Completion carries full text + usage
	if completedText != "Hello world" {
		t.Fatalf("completed text = %q, want %q", completedText, "Hello world")
	}
	if completedUsage == nil {
		t.Fatalf("completed response missing usage")
	}

	// 4. Sequence numbers strictly increasing
	for i := 1; i < len(seqs); i++ {
		if seqs[i] <= seqs[i-1] {
			t.Fatalf("sequence numbers not increasing: %v", seqs)
		}
	}

	_ = utils.GenerateToken // token generation runs inside streamResponses
}

// fakeToolCallsSSE returns an OpenAI-format SSE body streaming a single
// function_call: the name first, then two argument fragments, then a stop with
// tool_calls finish reason — like a real OpenAI tool-call stream.
func fakeToolCallsSSE() string {
	return strings.Join([]string{
		`data: {"id":"c","object":"chat.completion.chunk","model":"gpt-test","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"get_weather","arguments":""}}]},"finish_reason":null}]}`,
		`data: {"id":"c","model":"gpt-test","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"loc"}}]},"finish_reason":null}]}`,
		`data: {"id":"c","model":"gpt-test","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"ation\":\"SF\"}"}}]},"finish_reason":null}]}`,
		`data: {"id":"c","model":"gpt-test","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":5,"completion_tokens":4,"total_tokens":9}}`,
		`data: [DONE]`,
		"",
	}, "\n\n")
}

// parseResponsesEvents is a small helper that splits a Responses SSE body into
// ordered (event, data-json) pairs for assertions.
func parseResponsesEvents(t *testing.T, body string) []struct {
	event string
	data  map[string]interface{}
} {
	t.Helper()
	var out []struct {
		event string
		data  map[string]interface{}
	}
	var curEvent, curData string
	for _, ln := range strings.Split(body, "\n") {
		switch {
		case strings.HasPrefix(ln, "event: "):
			curEvent = strings.TrimPrefix(ln, "event: ")
		case strings.HasPrefix(ln, "data: "):
			curData = strings.TrimPrefix(ln, "data: ")
		case ln == "":
			if curEvent != "" && curData != "" {
				var obj map[string]interface{}
				_ = json.Unmarshal([]byte(curData), &obj)
				out = append(out, struct {
					event string
					data  map[string]interface{}
				}{curEvent, obj})
				curEvent, curData = "", ""
			}
		}
	}
	return out
}

func TestStreamResponsesToolCalls(t *testing.T) {
	upstream := &http.Response{
		StatusCode: 200,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(fakeToolCallsSSE())),
	}

	rec := httptest.NewRecorder()
	streamResponsesTranslate(rec, upstream, "gpt-test")

	events := parseResponsesEvents(t, rec.Body.String())

	// 1. Lifecycle ordering: no message item for a tool-calls-only response
	expectOrder := []string{
		"response.created",
		"response.in_progress",
		"response.output_item.added",
		"response.function_call_arguments.delta",
		"response.function_call_arguments.delta",
		"response.function_call_arguments.done",
		"response.output_item.done",
		"response.completed",
	}
	if len(events) != len(expectOrder) {
		var names []string
		for _, e := range events {
			names = append(names, e.event)
		}
		t.Fatalf("event count = %d, want %d\nfull events: %v", len(events), len(expectOrder), names)
	}
	for i, e := range expectOrder {
		if events[i].event != e {
			t.Fatalf("event[%d] = %q, want %q", i, events[i].event, e)
		}
	}

	// 2. Argument fragments concatenate into the full JSON arguments string
	var argDeltas []string
	for _, e := range events {
		if e.event == "response.function_call_arguments.delta" {
			if d, ok := e.data["delta"].(string); ok {
				argDeltas = append(argDeltas, d)
			}
		}
	}
	if got := strings.Join(argDeltas, ""); got != `{"location":"SF"}` {
		t.Fatalf("arg deltas = %v, want [{\"loc, ation\":\"SF\"}]", argDeltas)
	}

	// 3. Completed snapshot carries a function_call item with name + arguments
	completed := events[len(events)-1].data["response"].(map[string]interface{})
	out, _ := completed["output"].([]interface{})
	if len(out) != 1 {
		t.Fatalf("completed output length = %d, want 1", len(out))
	}
	item := out[0].(map[string]interface{})
	if item["type"] != "function_call" {
		t.Fatalf("item type = %v, want function_call", item["type"])
	}
	if item["name"] != "get_weather" {
		t.Fatalf("item name = %v, want get_weather", item["name"])
	}
	if item["arguments"] != `{"location":"SF"}` {
		t.Fatalf("item arguments = %v, want {\"location\":\"SF\"}", item["arguments"])
	}
	if item["call_id"] == nil || item["call_id"] == "" {
		t.Fatalf("item missing call_id")
	}
}
