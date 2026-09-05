package wirebridge

import (
	"encoding/json"
	"strings"
	"testing"
)

func mustTranslateRequest(t *testing.T, in string) string {
	t.Helper()
	out, err := TranslateRequest([]byte(in))
	if err != nil {
		t.Fatalf("TranslateRequest: unexpected error: %v", err)
	}
	return string(out)
}

func translateRequestErr(t *testing.T, in string) string {
	t.Helper()
	out, err := TranslateRequest([]byte(in))
	if err == nil {
		t.Fatalf("TranslateRequest: want an error, got %s", out)
	}
	return err.Error()
}

// TestTranslateRequestRows walks the §4 table row by row with golden pairs.
// The goldens are exact: struct-driven marshalling makes the output order
// deterministic, so any change to the wire shape fails loudly here.
func TestTranslateRequestRows(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "system as string",
			in:   `{"model":"qwen-3.8-27b","max_tokens":256,"system":"You are helpful.","messages":[{"role":"user","content":[{"type":"text","text":"Hi"}]}]}`,
			want: `{"model":"qwen-3.8-27b","messages":[{"role":"system","content":"You are helpful."},{"role":"user","content":[{"type":"text","text":"Hi"}]}],"max_tokens":256}`,
		},
		{
			name: "system as block array flattens to one system message, cache_control stripped",
			in:   `{"model":"m","max_tokens":5,"system":[{"type":"text","text":"Part one.","cache_control":{"type":"ephemeral"}},{"type":"text","text":"Part two."}],"messages":[{"role":"user","content":"Hello"}]}`,
			want: `{"model":"m","messages":[{"role":"system","content":"Part one.\n\nPart two."},{"role":"user","content":"Hello"}],"max_tokens":5}`,
		},
		{
			name: "text and image blocks become content parts",
			in:   `{"model":"m","max_tokens":9,"messages":[{"role":"user","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"aGVsbG8="}},{"type":"text","text":"what is this?"}]}]}`,
			want: `{"model":"m","messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,aGVsbG8="}},{"type":"text","text":"what is this?"}]}],"max_tokens":9}`,
		},
		{
			name: "tool_use and tool_result map bidirectionally, input_schema renamed",
			in: `{"model":"m","max_tokens":10,"messages":[` +
				`{"role":"user","content":[{"type":"text","text":"run it"}]},` +
				`{"role":"assistant","content":[{"type":"tool_use","id":"toolu_01","name":"run","input":{"path":"a.go","line":3}}]},` +
				`{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_01","content":"ok\n"}]}` +
				`],"tools":[{"name":"run","description":"Run a thing","input_schema":{"type":"object","properties":{"path":{"type":"string"}}}}]}`,
			want: `{"model":"m","messages":[` +
				`{"role":"user","content":[{"type":"text","text":"run it"}]},` +
				`{"role":"assistant","content":null,"tool_calls":[{"id":"toolu_01","type":"function","function":{"name":"run","arguments":"{\"path\":\"a.go\",\"line\":3}"}}]},` +
				`{"role":"tool","tool_call_id":"toolu_01","content":"ok\n"}` +
				`],"tools":[{"type":"function","function":{"name":"run","description":"Run a thing","parameters":{"type":"object","properties":{"path":{"type":"string"}}}}}],"max_tokens":10}`,
		},
		{
			name: "tool_result with block content flattens, is_error ignored, following text kept",
			in: `{"model":"m","max_tokens":2,"messages":[{"role":"user","content":[` +
				`{"type":"tool_result","tool_use_id":"t9","is_error":true,"content":[{"type":"text","text":"line1"},{"type":"text","text":"line2"}]},` +
				`{"type":"text","text":"and then?"}]}]}`,
			want: `{"model":"m","messages":[{"role":"tool","tool_call_id":"t9","content":"line1\nline2"},{"role":"user","content":[{"type":"text","text":"and then?"}]}],"max_tokens":2}`,
		},
		{
			name: "sampling maps temperature/top_p, stop_sequences/max_tokens map, stream passes",
			in:   `{"model":"m","max_tokens":8,"temperature":0.7,"top_p":0.9,"top_k":40,"stop_sequences":["END","STOP"],"stream":true,"thinking":{"type":"disabled"},"messages":[{"role":"user","content":"go"}]}`,
			want: `{"model":"m","messages":[{"role":"user","content":"go"}],"max_tokens":8,"temperature":0.7,"top_p":0.9,"stop":["END","STOP"],"stream":true}`,
		},
		{
			name: "model id passes through verbatim",
			in:   `{"model":"Qwen/qwen3-27b-not-a-real-id","max_tokens":1,"messages":[{"role":"user","content":"x"}]}`,
			want: `{"model":"Qwen/qwen3-27b-not-a-real-id","messages":[{"role":"user","content":"x"}],"max_tokens":1}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := mustTranslateRequest(t, tc.in); got != tc.want {
				t.Errorf("TranslateRequest mismatch\n got: %s\nwant: %s", got, tc.want)
			}
		})
	}
}

// TestTranslateRequestStripsUnmapped pins the strip rows as absence: thinking,
// top_k, cache_control, metadata, strict, and reasoning_effort must never
// appear in the translated body, whatever the request carried.
func TestTranslateRequestStripsUnmapped(t *testing.T) {
	in := `{"model":"m","max_tokens":4,` +
		`"thinking":{"type":"enabled","budget_tokens":1024},` +
		`"metadata":{"user_id":"u"},` +
		`"top_k":40,` +
		`"system":[{"type":"text","text":"sys","cache_control":{"type":"ephemeral"}}],` +
		`"messages":[{"role":"user","content":[{"type":"text","text":"hi","cache_control":{"type":"ephemeral"}}]}],` +
		`"tools":[{"name":"f","input_schema":{"type":"object"},"cache_control":{"type":"ephemeral"}}]}`
	got := mustTranslateRequest(t, in)
	for _, banned := range []string{`"thinking"`, `"top_k"`, `"cache_control"`, `"metadata"`, `"strict"`, `"reasoning_effort"`, `"budget_tokens"`} {
		if strings.Contains(got, banned) {
			t.Errorf("translated request carries %s:\n%s", banned, got)
		}
	}
	if !strings.Contains(got, `"parameters"`) {
		t.Errorf("tools must carry the renamed parameters key:\n%s", got)
	}
}

// TestTranslateRequestToolsNeverStrict walks the whole decoded output for a
// strict key anywhere, not just where we expect it — WB-D6 is "never".
func TestTranslateRequestToolsNeverStrict(t *testing.T) {
	in := `{"model":"m","max_tokens":4,"messages":[{"role":"user","content":"x"}],"tools":[` +
		`{"name":"a","input_schema":{"type":"object","properties":{"p":{"type":"string"}}}},` +
		`{"name":"b","description":"d","input_schema":{"type":"object"}}]}`
	got := mustTranslateRequest(t, in)
	var probe any
	if err := json.Unmarshal([]byte(got), &probe); err != nil {
		t.Fatalf("output is not JSON: %v", err)
	}
	var walk func(path string, v any)
	seenTools := 0
	walk = func(path string, v any) {
		switch tv := v.(type) {
		case map[string]any:
			for k, vv := range tv {
				if k == "strict" {
					t.Errorf("strict key present at %s (WB-D6 forbids it outright)", path)
				}
				if k == "type" && vv == "function" {
					seenTools++
				}
				walk(path+"."+k, vv)
			}
		case []any:
			for _, vv := range tv {
				walk(path, vv)
			}
		}
	}
	walk("$", probe)
	if seenTools != 2 {
		t.Errorf("expected both tools translated as function type, saw %d", seenTools)
	}
}

// TestTranslateRequestFailClosed pins WB-D5: unknown block/tool/role shapes
// come back as an error that NAMES the unrecognized type, so drift is visible
// at first request instead of mysterious.
func TestTranslateRequestFailClosed(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		wantErr string
	}{
		{
			name:    "unknown content block type is named",
			in:      `{"model":"m","max_tokens":1,"messages":[{"role":"user","content":[{"type":"document","source":{"type":"base64","media_type":"application/pdf","data":"x"}}]}]}`,
			wantErr: `"document"`,
		},
		{
			name:    "unknown tool type is named",
			in:      `{"model":"m","max_tokens":1,"tools":[{"type":"web_search_20260209","name":"web_search"}],"messages":[{"role":"user","content":"x"}]}`,
			wantErr: `"web_search_20260209"`,
		},
		{
			name:    "non-base64 image source is named",
			in:      `{"model":"m","max_tokens":1,"messages":[{"role":"user","content":[{"type":"image","source":{"type":"url","url":"https://example.com/x.png"}}]}]}`,
			wantErr: `"url"`,
		},
		{
			name:    "non-text block inside tool_result is named",
			in:      `{"model":"m","max_tokens":1,"messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"x"}}]}]}]}`,
			wantErr: `"image"`,
		},
		{
			name:    "unknown message role is named",
			in:      `{"model":"m","max_tokens":1,"messages":[{"role":"system","content":"x"}]}`,
			wantErr: `"system"`,
		},
		{
			name:    "malformed JSON names the direction",
			in:      `{"model":`,
			wantErr: "decoding anthropic request",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := translateRequestErr(t, tc.in); !strings.Contains(got, tc.wantErr) {
				t.Errorf("error %q does not name %q", got, tc.wantErr)
			}
		})
	}
}

// TestToolUseIDRoundTrip pins the id contract across both directions: the
// anthropic tool_use id becomes the openai tool_call id, and the same id
// riding an openai tool_call in a response comes back as the tool_use id
// claude replays as tool_result.
func TestToolUseIDRoundTrip(t *testing.T) {
	const id = "toolu_round1"

	reqOut, err := TranslateRequest([]byte(`{"model":"m","max_tokens":4,"messages":[` +
		`{"role":"user","content":"list files"},` +
		`{"role":"assistant","content":[{"type":"tool_use","id":"` + id + `","name":"ls","input":{"path":"/tmp"}}]}]}`))
	if err != nil {
		t.Fatalf("request direction: %v", err)
	}
	var req struct {
		Messages []struct {
			ToolCalls []struct {
				ID string `json:"id"`
			} `json:"tool_calls"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(reqOut, &req); err != nil {
		t.Fatalf("translated request is not JSON: %v", err)
	}
	if len(req.Messages) != 2 || len(req.Messages[1].ToolCalls) != 1 || req.Messages[1].ToolCalls[0].ID != id {
		t.Fatalf("tool_call id did not survive the request direction:\n%s", reqOut)
	}

	respOut, err := TranslateResponse([]byte(`{"id":"c1","model":"m","choices":[{"index":0,` +
		`"message":{"role":"assistant","content":null,"tool_calls":[{"id":"` + id + `","type":"function","function":{"name":"ls","arguments":"{\"path\":\"/tmp\"}"}}]},` +
		`"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`))
	if err != nil {
		t.Fatalf("response direction: %v", err)
	}
	var resp struct {
		Content []struct {
			Type string `json:"type"`
			ID   string `json:"id"`
		} `json:"content"`
	}
	if err := json.Unmarshal(respOut, &resp); err != nil {
		t.Fatalf("translated response is not JSON: %v", err)
	}
	if len(resp.Content) != 1 || resp.Content[0].Type != "tool_use" || resp.Content[0].ID != id {
		t.Fatalf("tool_use id did not survive the response direction:\n%s", respOut)
	}
}
