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
	if got.Model != "terra" || got.Instructions != "be brief" || got.Max != 64 || got.Reasoning.Effort != "high" {
		t.Fatalf("top-level mapping = %#v", got)
	}
	if len(got.Input) != 3 || got.Input[1].Type != "function_call" || got.Input[1].CallID != "toolu_1" || got.Input[1].Name != "ls" || got.Input[1].Args != `{"path":"."}` || got.Input[2].Type != "function_call_output" || got.Input[2].CallID != "toolu_1" || got.Input[2].Output != "a.go\n" {
		t.Fatalf("input = %#v", got.Input)
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
