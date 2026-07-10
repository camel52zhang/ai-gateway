package adapters

import (
	"encoding/json"
	"testing"
)

// TestConvertToGeminiMultimodal verifies that a vision request (text + a base64
// data: image URL) is forwarded to Gemini as a multimodal content part instead
// of having the image silently dropped.
func TestConvertToGeminiMultimodal(t *testing.T) {
	body := map[string]interface{}{
		"model": "gemini-pro",
		"messages": []interface{}{
			map[string]interface{}{
				"role": "user",
				"content": []interface{}{
					map[string]interface{}{"type": "text", "text": "What is in this image?"},
					map[string]interface{}{
						"type": "image_url",
						"image_url": map[string]interface{}{
							"url": "data:image/png;base64,iVBORw0KGgo=",
						},
					},
				},
			},
		},
	}

	out := convertToGemini(body)

	contents, _ := out["contents"].([]map[string]interface{})
	if len(contents) != 1 {
		t.Fatalf("expected 1 content entry, got %d", len(contents))
	}
	content := contents[0]
	parts, _ := content["parts"].([]map[string]interface{})
	if len(parts) != 2 {
		t.Fatalf("expected 2 parts (text + image), got %d: %v", len(parts), parts)
	}

	imgPart := parts[1]
	inline, ok := imgPart["inline_data"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected inline_data part for the image, got %v", parts[1])
	}
	if inline["mime_type"] != "image/png" {
		t.Fatalf("expected mime_type image/png, got %v", inline["mime_type"])
	}
	if inline["data"] != "iVBORw0KGgo=" {
		t.Fatalf("expected base64 payload preserved, got %v", inline["data"])
	}
}

// TestConvertToGeminiTextOnlyRegression guards the classic single-string and
// assistant→model role mapping paths so the multimodal change can't regress
// them.
func TestConvertToGeminiTextOnlyRegression(t *testing.T) {
	body := map[string]interface{}{
		"model": "gemini-pro",
		"messages": []interface{}{
			map[string]interface{}{"role": "user", "content": "hello"},
			map[string]interface{}{"role": "assistant", "content": "hi there"},
		},
	}

	out := convertToGemini(body)

	contents, _ := out["contents"].([]map[string]interface{})
	if len(contents) != 2 {
		t.Fatalf("expected 2 content entries, got %d", len(contents))
	}
	if contents[0]["role"] != "user" {
		t.Fatalf("expected first role user, got %v", contents[0]["role"])
	}
	if contents[1]["role"] != "model" {
		t.Fatalf("expected second role model, got %v", contents[1]["role"])
	}

	// The produced payload must be valid JSON (no nil/unsupported values).
	if _, err := json.Marshal(out); err != nil {
		t.Fatalf("gemini body is not JSON-serializable: %v", err)
	}
}

// TestConvertToGeminiSkipsRemoteImageURL confirms that a non-data: (remote) image
// URL is not forwarded inline (we don't fetch remote URLs in the proxy path);
// the text part is still preserved.
func TestConvertToGeminiSkipsRemoteImageURL(t *testing.T) {
	body := map[string]interface{}{
		"model": "gemini-pro",
		"messages": []interface{}{
			map[string]interface{}{
				"role": "user",
				"content": []interface{}{
					map[string]interface{}{"type": "text", "text": "look"},
					map[string]interface{}{
						"type": "image_url",
						"image_url": map[string]interface{}{
							"url": "https://example.com/cat.png",
						},
					},
				},
			},
		},
	}

	out := convertToGemini(body)
	contents, _ := out["contents"].([]map[string]interface{})
	if len(contents) != 1 {
		t.Fatalf("expected 1 content entry, got %d", len(contents))
	}
	content := contents[0]
	parts, _ := content["parts"].([]map[string]interface{})
	if len(parts) != 1 {
		t.Fatalf("expected only the text part (remote image skipped), got %d: %v", len(parts), parts)
	}
	if _, ok := parts[0]["text"]; !ok {
		t.Fatalf("expected the remaining part to be text, got %v", parts[0])
	}
}

// TestConvertCohereToOpenAIV2 verifies that a Cohere v2 (/v2/chat) non-streaming
// response — whose text lives under message.content[].text and token usage under
// usage.tokens — is translated into OpenAI shape correctly (no top-level "text").
func TestConvertCohereToOpenAIV2(t *testing.T) {
	cohereResp := map[string]interface{}{
		"id":      "c-123",
		"message": map[string]interface{}{
			"role": "assistant",
			"content": []interface{}{
				map[string]interface{}{"type": "text", "text": "Hello "},
				map[string]interface{}{"type": "text", "text": "world"},
			},
		},
		"finish_reason": "COMPLETE",
		"usage": map[string]interface{}{
			"tokens": map[string]interface{}{
				"input_tokens":  float64(5),
				"output_tokens": float64(2),
			},
		},
	}
	body := map[string]interface{}{"model": "command-r-plus"}

	out := convertCohereToOpenAI(cohereResp, body)

	choices, _ := out["choices"].([]map[string]interface{})
	if len(choices) != 1 {
		t.Fatalf("expected 1 choice, got %d", len(choices))
	}
	msg, _ := choices[0]["message"].(map[string]interface{})
	if msg["content"] != "Hello world" {
		t.Fatalf("expected concatenated content 'Hello world', got %v", msg["content"])
	}

	usage, ok := out["usage"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected usage in response")
	}
	if usage["prompt_tokens"] != int64(5) || usage["completion_tokens"] != int64(2) {
		t.Fatalf("unexpected usage: %v", usage)
	}
}

// TestConvertToCohereV2Request verifies the OpenAI→Cohere v2 request mapping:
// roles are lowercase (v2), content is the message body (no v1 "message"/"USER"
// fields), and there is no v1 "preamble".
func TestConvertToCohereV2Request(t *testing.T) {
	body := map[string]interface{}{
		"model":    "command-r-plus",
		"messages": []interface{}{
			map[string]interface{}{"role": "system", "content": "be brief"},
			map[string]interface{}{"role": "user", "content": "hi"},
			map[string]interface{}{"role": "assistant", "content": "hello"},
		},
		"temperature": float64(0.7),
	}

	out := convertToCohere(body)

	messages, _ := out["messages"].([]map[string]interface{})
	if len(messages) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(messages))
	}
	if messages[0]["role"] != "system" || messages[1]["role"] != "user" || messages[2]["role"] != "assistant" {
		t.Fatalf("expected lowercase roles, got %v %v %v", messages[0]["role"], messages[1]["role"], messages[2]["role"])
	}
	if _, ok := messages[0]["content"]; !ok {
		t.Fatalf("expected 'content' field on message, got %v", messages[0])
	}
	if _, ok := out["preamble"]; ok {
		t.Fatalf("v2 request must not contain v1 'preamble'")
	}
	if _, ok := messages[0]["message"]; ok {
		t.Fatalf("v2 request must not contain v1 'message' field on message")
	}
}

// TestConvertToCohereV2Tools verifies tool-calling request mapping: OpenAI
// tools (JSON-schema parameters) become Cohere v2 parameter_definitions, an
// assistant message's tool_calls are passed through, and a tool result message
// becomes Cohere's {role:"tool", tool_call_id, content:[{type:"document",...}]}.
func TestConvertToCohereV2Tools(t *testing.T) {
	body := map[string]interface{}{
		"model": "command-r-plus",
		"messages": []interface{}{
			map[string]interface{}{"role": "user", "content": "weather?"},
			map[string]interface{}{
				"role": "assistant",
				"content": nil,
				"tool_calls": []interface{}{
					map[string]interface{}{
						"id":   "call_1",
						"type": "function",
						"function": map[string]interface{}{
							"name":      "get_weather",
							"arguments": "{\"city\":\"SF\"}",
						},
					},
				},
			},
			map[string]interface{}{
				"role":         "tool",
				"tool_call_id": "call_1",
				"content":      "{\"temp\":20}",
			},
		},
		"tools": []interface{}{
			map[string]interface{}{
				"type": "function",
				"function": map[string]interface{}{
					"name":        "get_weather",
					"description": "Get weather",
					"parameters": map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"city": map[string]interface{}{
								"type":        "string",
								"description": "City name",
							},
						},
						"required": []interface{}{"city"},
					},
				},
			},
		},
		"tool_choice": "required",
	}

	out := convertToCohere(body)

	// tools -> parameter_definitions
	tools, _ := out["tools"].([]interface{})
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(tools))
	}
	tool, _ := tools[0].(map[string]interface{})
	if tool["name"] != "get_weather" {
		t.Fatalf("expected tool name get_weather, got %v", tool["name"])
	}
	defs, _ := tool["parameter_definitions"].(map[string]interface{})
	if defs == nil {
		t.Fatalf("expected parameter_definitions, got nil")
	}
	city, _ := defs["city"].(map[string]interface{})
	if city == nil {
		t.Fatalf("expected 'city' param definition")
	}
	if city["type"] != "str" {
		t.Fatalf("expected cohere type 'str', got %v", city["type"])
	}
	if city["required"] != true {
		t.Fatalf("expected 'city' required true, got %v", city["required"])
	}

	// tool_choice REQUIRED
	if out["tool_choice"] != "REQUIRED" {
		t.Fatalf("expected tool_choice REQUIRED, got %v", out["tool_choice"])
	}

	messages, _ := out["messages"].([]map[string]interface{})
	if len(messages) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(messages))
	}
	// assistant tool_calls passthrough
	asst := messages[1]
	tcs, _ := asst["tool_calls"].([]interface{})
	if len(tcs) != 1 {
		t.Fatalf("expected assistant tool_calls len 1, got %d", len(tcs))
	}
	tc, _ := tcs[0].(map[string]interface{})
	if tc["id"] != "call_1" {
		t.Fatalf("expected tool_call id call_1, got %v", tc["id"])
	}
	// tool result -> role tool + tool_call_id + document content
	res := messages[2]
	if res["role"] != "tool" {
		t.Fatalf("expected role tool, got %v", res["role"])
	}
	if res["tool_call_id"] != "call_1" {
		t.Fatalf("expected tool_call_id call_1, got %v", res["tool_call_id"])
	}
	content, _ := res["content"].([]interface{})
	if len(content) != 1 {
		t.Fatalf("expected 1 document content, got %d", len(content))
	}
	doc, _ := content[0].(map[string]interface{})
	if doc["type"] != "document" {
		t.Fatalf("expected document type, got %v", doc["type"])
	}
}

// TestConvertCohereToOpenAIV2ToolCalls verifies that a Cohere v2 non-streaming
// response carrying message.tool_calls is translated to OpenAI tool_calls with
// finish_reason "tool_calls".
func TestConvertCohereToOpenAIV2ToolCalls(t *testing.T) {
	cohereResp := map[string]interface{}{
		"id": "c-456",
		"message": map[string]interface{}{
			"role": "assistant",
			"content": []interface{}{
				map[string]interface{}{"type": "text", "text": "Let me check."},
			},
			"tool_calls": []interface{}{
				map[string]interface{}{
					"id":   "call_1",
					"type": "function",
					"function": map[string]interface{}{
						"name":      "get_weather",
						"arguments": "{\"city\":\"SF\"}",
					},
				},
			},
		},
		"finish_reason": "TOOL_CALL",
	}
	body := map[string]interface{}{"model": "command-r-plus"}

	out := convertCohereToOpenAI(cohereResp, body)

	choices, _ := out["choices"].([]map[string]interface{})
	if len(choices) != 1 {
		t.Fatalf("expected 1 choice, got %d", len(choices))
	}
	if choices[0]["finish_reason"] != "tool_calls" {
		t.Fatalf("expected finish_reason tool_calls, got %v", choices[0]["finish_reason"])
	}
	msg, _ := choices[0]["message"].(map[string]interface{})
	tcs, _ := msg["tool_calls"].([]interface{})
	if len(tcs) != 1 {
		t.Fatalf("expected 1 tool_call, got %d", len(tcs))
	}
	tc, _ := tcs[0].(map[string]interface{})
	if tc["id"] != "call_1" {
		t.Fatalf("expected tool_call id call_1, got %v", tc["id"])
	}
	fn, _ := tc["function"].(map[string]interface{})
	if fn["name"] != "get_weather" {
		t.Fatalf("expected function name get_weather, got %v", fn["name"])
	}
	if fn["arguments"] != "{\"city\":\"SF\"}" {
		t.Fatalf("expected raw arguments JSON string preserved, got %v", fn["arguments"])
	}
}
