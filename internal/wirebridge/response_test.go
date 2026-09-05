package wirebridge

import (
	"encoding/json"
	"strings"
	"testing"
)

func mustTranslateResponse(t *testing.T, in string) string {
	t.Helper()
	out, err := TranslateResponse([]byte(in))
	if err != nil {
		t.Fatalf("TranslateResponse: unexpected error: %v", err)
	}
	return string(out)
}

// TestTranslateResponseRows walks the reverse §4 rows with golden pairs.
func TestTranslateResponseRows(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "plain text",
			in:   `{"id":"chatcmpl-1","model":"qwen-3.8-27b","choices":[{"index":0,"message":{"role":"assistant","content":"Hello there."},"finish_reason":"stop"}],"usage":{"prompt_tokens":12,"completion_tokens":4}}`,
			want: `{"id":"chatcmpl-1","type":"message","role":"assistant","model":"qwen-3.8-27b","content":[{"type":"text","text":"Hello there."}],"stop_reason":"end_turn","stop_sequence":null,"usage":{"input_tokens":12,"output_tokens":4}}`,
		},
		{
			name: "tool_calls become tool_use blocks with parsed input",
			in:   `{"id":"chatcmpl-2","model":"qwen","choices":[{"index":0,"message":{"role":"assistant","content":null,"tool_calls":[{"id":"call_9","type":"function","function":{"name":"run","arguments":"{\"path\":\"a.go\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":20,"completion_tokens":9}}`,
			want: `{"id":"chatcmpl-2","type":"message","role":"assistant","model":"qwen","content":[{"type":"tool_use","id":"call_9","name":"run","input":{"path":"a.go"}}],"stop_reason":"tool_use","stop_sequence":null,"usage":{"input_tokens":20,"output_tokens":9}}`,
		},
		{
			name: "missing usage maps to zeros",
			in:   `{"id":"c","model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"x"},"finish_reason":"stop"}]}`,
			want: `{"id":"c","type":"message","role":"assistant","model":"m","content":[{"type":"text","text":"x"}],"stop_reason":"end_turn","stop_sequence":null,"usage":{"input_tokens":0,"output_tokens":0}}`,
		},
		{
			name: "null content with no tool calls yields an empty content array",
			in:   `{"id":"c","model":"m","choices":[{"index":0,"message":{"role":"assistant","content":null},"finish_reason":"stop"}]}`,
			want: `{"id":"c","type":"message","role":"assistant","model":"m","content":[],"stop_reason":"end_turn","stop_sequence":null,"usage":{"input_tokens":0,"output_tokens":0}}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := mustTranslateResponse(t, tc.in); got != tc.want {
				t.Errorf("TranslateResponse mismatch\n got: %s\nwant: %s", got, tc.want)
			}
		})
	}
}

// TestTranslateResponseFinishReasonMapping pins every row of the stop-reason
// mapping, including the "other→end_turn" catch-all.
func TestTranslateResponseFinishReasonMapping(t *testing.T) {
	cases := []struct {
		finish string
		want   string
	}{
		{"stop", "end_turn"},
		{"length", "max_tokens"},
		{"tool_calls", "tool_use"},
		{"content_filter", "end_turn"},
		{"", "end_turn"},
	}
	for _, tc := range cases {
		t.Run("finish="+tc.finish, func(t *testing.T) {
			in := `{"id":"c","model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"x"},"finish_reason":"` + tc.finish + `"}]}`
			out := mustTranslateResponse(t, in)
			var resp struct {
				StopReason string `json:"stop_reason"`
			}
			if err := json.Unmarshal([]byte(out), &resp); err != nil {
				t.Fatalf("output is not JSON: %v", err)
			}
			if resp.StopReason != tc.want {
				t.Errorf("finish_reason %q mapped to %q, want %q", tc.finish, resp.StopReason, tc.want)
			}
		})
	}
}

// TestTranslateResponseDropsReasoning pins WB-D5 on the non-streaming side:
// upstream reasoning fields vanish, nothing grows a thinking block, and the
// visible answer is untouched.
func TestTranslateResponseDropsReasoning(t *testing.T) {
	in := `{"id":"c","model":"m","choices":[{"index":0,"message":{` +
		`"role":"assistant","content":"Answer.","reasoning":"internal monologue","reasoning_content":"more of it"},` +
		`"finish_reason":"stop"}]}`
	got := mustTranslateResponse(t, in)
	for _, banned := range []string{"reasoning", "thinking"} {
		if strings.Contains(got, banned) {
			t.Errorf("translated response carries %q:\n%s", banned, got)
		}
	}
	var resp struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal([]byte(got), &resp); err != nil {
		t.Fatalf("output is not JSON: %v", err)
	}
	if len(resp.Content) != 1 || resp.Content[0].Type != "text" || resp.Content[0].Text != "Answer." {
		t.Fatalf("visible answer must survive the drop:\n%s", got)
	}
}

// TestTranslateResponseFailClosed pins the failure paths: a body the bridge
// cannot honestly translate is an error, never a guess.
func TestTranslateResponseFailClosed(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		wantErr string
	}{
		{
			name:    "no choices",
			in:      `{"id":"c","model":"m","choices":[]}`,
			wantErr: "no choices",
		},
		{
			name:    "array message content is named",
			in:      `{"id":"c","model":"m","choices":[{"index":0,"message":{"role":"assistant","content":[{"type":"text","text":"x"}]},"finish_reason":"stop"}]}`,
			wantErr: "an array",
		},
		{
			name:    "non-object tool arguments are named",
			in:      `{"id":"c","model":"m","choices":[{"index":0,"message":{"role":"assistant","content":null,"tool_calls":[{"id":"t","type":"function","function":{"name":"echo","arguments":"42"}}]},"finish_reason":"tool_calls"}]}`,
			wantErr: "tool echo arguments",
		},
		{
			name:    "malformed JSON names the direction",
			in:      `{"choices":`,
			wantErr: "decoding openai response",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := TranslateResponse([]byte(tc.in))
			if err == nil {
				t.Fatalf("want an error, got %s", out)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error %q does not name %q", err.Error(), tc.wantErr)
			}
		})
	}
}
