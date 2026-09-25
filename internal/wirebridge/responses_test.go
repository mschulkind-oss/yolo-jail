package wirebridge

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestTranslateResponsesRequestPreservesToolConversation(t *testing.T) {
	in := `{"model":"terra","max_tokens":64,"thinking":{"type":"enabled","budget_tokens":20000},"system":"be brief","messages":[` +
		`{"role":"user","content":[{"type":"text","text":"list files"}]},` +
		`{"role":"assistant","content":[{"type":"tool_use","id":"toolu_1","name":"ls","input":{"path":"."}}]},` +
		`{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"a.go\n"}]}],` +
		`"tools":[{"name":"ls","input_schema":{"type":"object"}}]}`
	out, err := TranslateResponsesRequest([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Model        string `json:"model"`
		Instructions string `json:"instructions"`
		Max          int    `json:"max_output_tokens"`
		Store        *bool  `json:"store"`
		Reasoning    struct {
			Effort string `json:"effort"`
		} `json:"reasoning"`
		Input []struct {
			Type   string `json:"type"`
			Role   string `json:"role"`
			CallID string `json:"call_id"`
			Name   string `json:"name"`
			Args   string `json:"arguments"`
			Output string `json:"output"`
		} `json:"input"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out)
	}
	if got.Model != "terra" || got.Instructions != "be brief" || got.Max != 64 || got.Store == nil || *got.Store || got.Reasoning.Effort != "high" {
		t.Fatalf("top-level mapping = %#v", got)
	}
	if len(got.Input) != 3 || got.Input[1].Type != "function_call" || got.Input[1].CallID != "toolu_1" || got.Input[1].Name != "ls" || got.Input[1].Args != `{"path":"."}` || got.Input[2].Type != "function_call_output" || got.Input[2].CallID != "toolu_1" || got.Input[2].Output != "a.go\n" {
		t.Fatalf("input = %#v", got.Input)
	}
}

func TestTranslateResponsesRequestNonBudgetThinkingUsesResponsesDefault(t *testing.T) {
	for _, thinkingType := range []string{"adaptive", "disabled", "future-mode"} {
		t.Run(thinkingType, func(t *testing.T) {
			in := `{"model":"terra","thinking":{"type":"` + thinkingType + `"},"messages":[{"role":"user","content":"hello"}]}`
			out, err := TranslateResponsesRequest([]byte(in))
			if err != nil {
				t.Fatal(err)
			}
			var got struct {
				Reasoning *responsesReasoning `json:"reasoning"`
			}
			if err := json.Unmarshal(out, &got); err != nil {
				t.Fatalf("output is not JSON: %v\n%s", err, out)
			}
			if got.Reasoning != nil {
				t.Fatalf("%s thinking must leave Responses reasoning at its default, got %#v", thinkingType, got.Reasoning)
			}
		})
	}
}

func TestTranslateResponsesRequestMapsClaudeWebSearchToHostedTool(t *testing.T) {
	out, err := TranslateResponsesRequest([]byte(`{"model":"terra","tools":[{"type":"web_search_20260318","name":"web_search","max_uses":5}],"messages":[{"role":"user","content":"today's news"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Tools []responsesTool `json:"tools"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out)
	}
	if len(got.Tools) != 1 || got.Tools[0].Type != "web_search" || got.Tools[0].Name != "" || len(got.Tools[0].Parameters) != 0 {
		t.Fatalf("tools = %#v, want one nameless hosted web_search tool", got.Tools)
	}
}

func TestTranslateResponsesRequestPreservesInConversationSystem(t *testing.T) {
	out, err := TranslateResponsesRequest([]byte(`{"model":"terra","max_tokens":64,"messages":[{"role":"user","content":"before"},{"role":"system","content":[{"type":"text","text":"Use the repository conventions."}]},{"role":"user","content":"after"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Input []struct {
			Type    string `json:"type"`
			Role    string `json:"role"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"input"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out)
	}
	if len(got.Input) != 3 || got.Input[1].Type != "message" || got.Input[1].Role != "developer" || len(got.Input[1].Content) != 1 || got.Input[1].Content[0].Type != "input_text" || got.Input[1].Content[0].Text != "Use the repository conventions." {
		t.Fatalf("input = %#v", got.Input)
	}
}

func TestTranslateResponsesRequestRejectsNonTextInConversationSystem(t *testing.T) {
	_, err := TranslateResponsesRequest([]byte(`{"model":"terra","messages":[{"role":"system","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"aGVsbG8="}}]}]}`))
	if err == nil || !strings.Contains(err.Error(), `"image"`) {
		t.Fatalf("TranslateResponsesRequest error = %v, want named image error", err)
	}
}

func TestTranslateResponsesResponsePreservesToolIdentity(t *testing.T) {
	out, err := TranslateResponsesResponse([]byte(`{"id":"resp_1","model":"terra","status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"I will check."}]},{"type":"function_call","call_id":"call_1","name":"ls","arguments":"{\"path\":\".\"}"}],"usage":{"input_tokens":5,"output_tokens":3}}`))
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	for _, want := range []string{`"text":"I will check."`, `"type":"tool_use"`, `"id":"call_1"`, `"name":"ls"`, `"stop_reason":"tool_use"`, `"input_tokens":5`, `"output_tokens":3`} {
		if !strings.Contains(got, want) {
			t.Errorf("response missing %s: %s", want, got)
		}
	}
}

// The non-streaming Responses answer maps its cached input the same way the
// stream does: input_tokens is the uncached remainder.
func TestTranslateResponsesResponseReportsCachedInputAsCacheReads(t *testing.T) {
	out, err := TranslateResponsesResponse([]byte(`{"id":"resp_1","model":"terra","status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":900,"input_tokens_details":{"cached_tokens":600},"output_tokens":30}}`))
	if err != nil {
		t.Fatal(err)
	}
	if want := `"usage":{"input_tokens":300,"cache_read_input_tokens":600,"output_tokens":30}`; !strings.Contains(string(out), want) {
		t.Fatalf("response usage: want %s in\n%s", want, out)
	}
}

func TestTranslateResponsesResponseSkipsCompletedHostedWebSearch(t *testing.T) {
	out, err := TranslateResponsesResponse([]byte(`{"id":"resp_1","model":"terra","status":"completed","output":[{"type":"web_search_call","id":"ws_1","status":"completed"},{"type":"message","content":[{"type":"output_text","text":"The answer."}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `"text":"The answer."`) {
		t.Fatalf("response omitted final text after hosted web search: %s", out)
	}
}

func TestResponsesStreamTranslatorTextAndTool(t *testing.T) {
	tr := NewResponsesStreamTranslator()
	var got string
	for _, payload := range []string{
		`{"type":"response.created","response":{"id":"resp_1","model":"terra"}}`,
		`{"type":"response.output_text.delta","delta":"hello"}`,
		`{"type":"response.output_item.added","item":{"type":"function_call","call_id":"call_1","name":"ls"}}`,
		`{"type":"response.function_call_arguments.delta","delta":"{\"path\":\".\"}"}`,
		`{"type":"response.completed","response":{"status":"completed","usage":{"input_tokens":2,"output_tokens":4}}}`,
	} {
		events, err := tr.Chunk([]byte(payload))
		if err != nil {
			t.Fatal(err)
		}
		for _, event := range events {
			got += string(event.Format())
		}
	}
	for _, want := range []string{"event: message_start", `"text":"hello"`, `"id":"call_1"`, `"partial_json":`, `"stop_reason":"tool_use"`, "event: message_stop"} {
		if !strings.Contains(got, want) {
			t.Errorf("stream missing %s:\n%s", want, got)
		}
	}
}

// responsesStreamEvents feeds each payload through one translator and returns
// every event it emitted, in order.
func responsesStreamEvents(t *testing.T, payloads ...string) []Event {
	t.Helper()
	tr := NewResponsesStreamTranslator()
	var out []Event
	for _, payload := range payloads {
		events, err := tr.Chunk([]byte(payload))
		if err != nil {
			t.Fatalf("Chunk(%s): %v", payload, err)
		}
		out = append(out, events...)
	}
	end, err := tr.End()
	if err != nil {
		t.Fatalf("End: %v", err)
	}
	return append(out, end...)
}

// The Responses route's half of the streaming-usage defect: response.completed
// carries the whole request's usage, and the bridge kept only output_tokens of
// it. Responses' input_tokens INCLUDES input_tokens_details.cached_tokens, while
// Anthropic's input_tokens is the uncached remainder, so the cached count moves
// to cache_read_input_tokens and out of input_tokens.
func TestResponsesStreamReportsInputAndCachedTokens(t *testing.T) {
	evs := responsesStreamEvents(t,
		`{"type":"response.created","response":{"id":"resp_u","model":"terra","usage":null}}`,
		`{"type":"response.output_text.delta","delta":"hello"}`,
		`{"type":"response.completed","response":{"id":"resp_u","model":"terra","status":"completed","usage":{"input_tokens":900,"input_tokens_details":{"cached_tokens":600},"output_tokens":30,"output_tokens_details":{"reasoning_tokens":10},"total_tokens":930}}}`,
	)
	if len(evs) < 2 {
		t.Fatalf("too few events: %s", formatAll(evs))
	}
	assertEvents(t, evs[len(evs)-2:], []Event{
		{Name: "message_delta", Data: []byte(`{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"input_tokens":300,"cache_read_input_tokens":600,"output_tokens":30}}`)},
		{Name: "message_stop", Data: []byte(`{"type":"message_stop"}`)},
	})
}

// A Responses stream that stops at the model's output limit ends in
// response.incomplete, not response.completed, and that terminal event carries
// the usage too. It used to be refused as an unsupported event, so the client got
// an error event instead of a max_tokens stop, and the usage was lost with it.
func TestResponsesStreamIncompleteIsAMaxTokensStopWithUsage(t *testing.T) {
	evs := responsesStreamEvents(t,
		`{"type":"response.created","response":{"id":"resp_i","model":"terra"}}`,
		`{"type":"response.output_text.delta","delta":"partial"}`,
		`{"type":"response.incomplete","response":{"id":"resp_i","model":"terra","status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"usage":{"input_tokens":50,"output_tokens":64}}}`,
	)
	if len(evs) < 3 {
		t.Fatalf("too few events: %s", formatAll(evs))
	}
	assertEvents(t, evs[len(evs)-3:], []Event{
		{Name: "content_block_stop", Data: []byte(`{"type":"content_block_stop","index":0}`)},
		{Name: "message_delta", Data: []byte(`{"type":"message_delta","delta":{"stop_reason":"max_tokens","stop_sequence":null},"usage":{"input_tokens":50,"output_tokens":64}}`)},
		{Name: "message_stop", Data: []byte(`{"type":"message_stop"}`)},
	})
}

// The output limit can cut a function call off mid-arguments, and then the
// stream's terminal event is response.incomplete with a function_call block
// still open. That is a max_tokens stop, as the chat-completions route maps
// finish_reason "length" with a tool call open: reporting tool_use would send
// Claude off to run a tool whose input is truncated JSON. An open function
// call turns only a finished answer (end_turn) into tool_use.
func TestResponsesStreamIncompleteInsideAFunctionCallIsAMaxTokensStop(t *testing.T) {
	evs := responsesStreamEvents(t,
		`{"type":"response.created","response":{"id":"resp_t","model":"terra"}}`,
		`{"type":"response.output_item.added","item":{"type":"function_call","call_id":"call_w","name":"Write"}}`,
		`{"type":"response.function_call_arguments.delta","delta":"{\"file_path\":\"/tmp/a\",\"content\":\"hel"}`,
		`{"type":"response.incomplete","response":{"id":"resp_t","model":"terra","status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"usage":{"input_tokens":50,"output_tokens":64}}}`,
	)
	if len(evs) < 3 {
		t.Fatalf("too few events: %s", formatAll(evs))
	}
	assertEvents(t, evs[len(evs)-3:], []Event{
		{Name: "content_block_stop", Data: []byte(`{"type":"content_block_stop","index":0}`)},
		{Name: "message_delta", Data: []byte(`{"type":"message_delta","delta":{"stop_reason":"max_tokens","stop_sequence":null},"usage":{"input_tokens":50,"output_tokens":64}}`)},
		{Name: "message_stop", Data: []byte(`{"type":"message_stop"}`)},
	})
}

// The non-streaming answer follows the same rule as the stream, so a turn stops
// for the same reason streamed or not: an incomplete response that holds a
// function call is still a max_tokens stop, and a completed one is tool_use
// (TestTranslateResponsesResponsePreservesToolIdentity).
func TestTranslateResponsesResponseIncompleteWithAFunctionCallIsMaxTokens(t *testing.T) {
	out, err := TranslateResponsesResponse([]byte(`{"id":"resp_1","model":"terra","status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"output":[{"type":"function_call","call_id":"call_1","name":"ls","arguments":"{\"path\":\".\"}"}],"usage":{"input_tokens":5,"output_tokens":3}}`))
	if err != nil {
		t.Fatal(err)
	}
	if want := `"stop_reason":"max_tokens"`; !strings.Contains(string(out), want) {
		t.Fatalf("want %s in\n%s", want, out)
	}
}

func TestResponsesStreamTranslatorSkipsHostedWebSearchEvents(t *testing.T) {
	tr := NewResponsesStreamTranslator()
	var got string
	for _, payload := range []string{
		`{"type":"response.created","response":{"id":"resp_1","model":"terra"}}`,
		`{"type":"response.output_item.added","item":{"type":"web_search_call","id":"ws_1"}}`,
		`{"type":"response.web_search_call.searching","item_id":"ws_1"}`,
		`{"type":"response.web_search_call.in_progress","item_id":"ws_1"}`,
		`{"type":"response.web_search_call.completed","item_id":"ws_1"}`,
		`{"type":"response.output_text.delta","delta":"The answer."}`,
		`{"type":"response.output_text.annotation.added","annotation":{"type":"url_citation"}}`,
		`{"type":"response.output_text.annotation.done","annotation":{"type":"url_citation"}}`,
		`{"type":"response.completed","response":{"status":"completed","usage":{"input_tokens":2,"output_tokens":4}}}`,
	} {
		events, err := tr.Chunk([]byte(payload))
		if err != nil {
			t.Fatal(err)
		}
		for _, event := range events {
			got += string(event.Format())
		}
	}
	for _, want := range []string{`"text":"The answer."`, "event: message_stop"} {
		if !strings.Contains(got, want) {
			t.Errorf("stream missing %s:\n%s", want, got)
		}
	}
}

// OQ-WB1 (docs/reference/wire-bridge.md, ruled (b) 2026-09-25): response.failed is
// the upstream's own terminal failure, and a top-level `error` event is its
// transport-level twin. Both used to reach the default arm as an "unsupported
// Responses stream event", so the agent was told the bridge could not translate a
// known event and the upstream's reason was lost. Each now closes any open block
// and returns an *UpstreamFailedError carrying the upstream's code and message,
// which the daemon forwards as an anthropic `api_error` event.
func TestResponsesStreamFailedIsATerminalUpstreamError(t *testing.T) {
	tr := NewResponsesStreamTranslator()
	for _, payload := range []string{
		`{"type":"response.created","sequence_number":0,"response":{"id":"resp_f","object":"response","model":"terra","status":"in_progress"}}`,
		`{"type":"response.output_text.delta","sequence_number":1,"delta":"partial"}`,
	} {
		if _, err := tr.Chunk([]byte(payload)); err != nil {
			t.Fatalf("Chunk(%s): %v", payload, err)
		}
	}
	evs, err := tr.Chunk([]byte(`{"type":"response.failed","sequence_number":2,"response":{"id":"resp_f","object":"response","model":"terra","status":"failed","error":{"code":"server_error","message":"The model produced invalid content."},"usage":null}}`))
	var failed *UpstreamFailedError
	if !errors.As(err, &failed) {
		t.Fatalf("response.failed must return an *UpstreamFailedError, got %v", err)
	}
	if failed.Code != "server_error" || failed.Message != "The model produced invalid content." {
		t.Fatalf("UpstreamFailedError = %+v, want the upstream's own code and message", failed)
	}
	if got := failed.ClientMessage(); got != "The model produced invalid content." {
		t.Fatalf("ClientMessage() = %q, want the upstream's message verbatim", got)
	}
	// The open text block is closed before the error, so the anthropic grammar the
	// client holds is well-formed up to the error event.
	assertEvents(t, evs, []Event{
		{Name: "content_block_stop", Data: []byte(`{"type":"content_block_stop","index":0}`)},
	})
	// Terminal: nothing after it is translated.
	if evs, err := tr.Chunk([]byte(`{"type":"response.output_text.delta","delta":"late"}`)); err != nil || len(evs) != 0 {
		t.Fatalf("a chunk after response.failed must be dropped, got %v, %v", evs, err)
	}
}

func TestResponsesStreamTopLevelErrorIsATerminalUpstreamError(t *testing.T) {
	for name, payload := range map[string]string{
		"flat":   `{"type":"error","sequence_number":1,"code":"rate_limit_exceeded","message":"Rate limit reached for requests","param":null}`,
		"nested": `{"type":"error","error":{"type":"rate_limit_error","code":"rate_limit_exceeded","message":"Rate limit reached for requests"}}`,
	} {
		t.Run(name, func(t *testing.T) {
			tr := NewResponsesStreamTranslator()
			evs, err := tr.Chunk([]byte(payload))
			var failed *UpstreamFailedError
			if !errors.As(err, &failed) {
				t.Fatalf("a top-level error event must return an *UpstreamFailedError, got %v", err)
			}
			if failed.Code != "rate_limit_exceeded" || failed.Message != "Rate limit reached for requests" {
				t.Fatalf("UpstreamFailedError = %+v", failed)
			}
			// Nothing was open, so nothing is closed: the daemon's error event is the
			// whole of the client's stream.
			if len(evs) != 0 {
				t.Fatalf("no block was open, want no events, got %s", formatAll(evs))
			}
		})
	}
}

// A failure that names no message still tells the agent something true, and
// names the code when there is one.
func TestResponsesStreamFailedWithoutAMessageStillSaysWhat(t *testing.T) {
	tr := NewResponsesStreamTranslator()
	_, err := tr.Chunk([]byte(`{"type":"response.failed","response":{"id":"resp_e","status":"failed","error":{"code":"server_error"}}}`))
	var failed *UpstreamFailedError
	if !errors.As(err, &failed) {
		t.Fatalf("want *UpstreamFailedError, got %v", err)
	}
	if got := failed.ClientMessage(); !strings.Contains(got, "upstream reported the response failed") || !strings.Contains(got, "server_error") {
		t.Fatalf("ClientMessage() = %q, want a fallback naming the failure and its code", got)
	}
}
