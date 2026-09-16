package wirebridge

import (
	"encoding/json"
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
