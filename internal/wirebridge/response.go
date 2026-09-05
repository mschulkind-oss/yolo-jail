package wirebridge

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// TranslateResponse converts a non-streaming OpenAI chat-completions response
// body into an Anthropic Messages response body — the reverse rows of
// wire-bridge.md §4's table: message content becomes a text block,
// tool_calls become tool_use blocks, finish_reason maps stop→end_turn,
// length→max_tokens, tool_calls→tool_use and anything else→end_turn, and
// usage maps prompt_tokens→input_tokens, completion_tokens→output_tokens.
//
// Upstream reasoning content (a reasoning or reasoning_content field, or any
// other field the bridge does not carry) is dropped by construction — the
// decoder has nowhere to put it and the output never grows a thinking block
// (WB-D5). Errors here are the daemon's 502 class, not a claude-facing 400:
// the upstream already answered.
func TranslateResponse(body []byte) ([]byte, error) {
	var r openaiResponse
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("wirebridge: decoding openai response: %w", err)
	}
	if len(r.Choices) == 0 {
		return nil, fmt.Errorf("wirebridge: openai response carries no choices")
	}
	choice := r.Choices[0]
	out := anthropicMessage{
		ID:      r.ID,
		Type:    "message",
		Role:    "assistant",
		Model:   r.Model,
		Content: []any{},
	}
	text, err := messageText(choice.Message.Content)
	if err != nil {
		return nil, err
	}
	if text != "" {
		out.Content = append(out.Content, anthropicTextBlock{Type: "text", Text: text})
	}
	for _, tc := range choice.Message.ToolCalls {
		input, err := toolUseInput(tc.Function.Name, tc.Function.Arguments)
		if err != nil {
			return nil, err
		}
		out.Content = append(out.Content, anthropicToolUseBlock{
			Type:  "tool_use",
			ID:    tc.ID,
			Name:  tc.Function.Name,
			Input: input,
		})
	}
	out.StopReason = stopReasonFromFinish(choice.FinishReason)
	if r.Usage != nil {
		if r.Usage.PromptTokens != nil {
			out.Usage.InputTokens = *r.Usage.PromptTokens
		}
		if r.Usage.CompletionTokens != nil {
			out.Usage.OutputTokens = *r.Usage.CompletionTokens
		}
	}
	b, err := json.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("wirebridge: encoding anthropic response: %w", err)
	}
	return b, nil
}

type openaiResponse struct {
	ID      string         `json:"id"`
	Model   string         `json:"model"`
	Choices []openaiChoice `json:"choices"`
	Usage   *openaiUsage   `json:"usage"`
}

type openaiChoice struct {
	Message      openaiRespMessage `json:"message"`
	FinishReason string            `json:"finish_reason"`
}

// openaiRespMessage: reasoning, reasoning_content, and every other field the
// bridge does not carry are dropped by construction — the struct has no field
// to decode them into, so upstream reasoning can never surface (WB-D5).
type openaiRespMessage struct {
	Content   json.RawMessage      `json:"content"`
	ToolCalls []openaiRespToolCall `json:"tool_calls"`
}

type openaiRespToolCall struct {
	ID       string `json:"id"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type openaiUsage struct {
	PromptTokens     *int `json:"prompt_tokens"`
	CompletionTokens *int `json:"completion_tokens"`
}

// anthropicMessage is the output shape; field order is the wire order.
type anthropicMessage struct {
	ID           string         `json:"id"`
	Type         string         `json:"type"`
	Role         string         `json:"role"`
	Model        string         `json:"model"`
	Content      []any          `json:"content"`
	StopReason   string         `json:"stop_reason"`
	StopSequence *string        `json:"stop_sequence"`
	Usage        anthropicUsage `json:"usage"`
}

type anthropicUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

type anthropicTextBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type anthropicToolUseBlock struct {
	Type  string          `json:"type"`
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

// stopReasonFromFinish maps openai finish_reason onto anthropic stop_reason
// per the table; anything unmapped falls to end_turn rather than inventing a
// new reason.
func stopReasonFromFinish(finish string) string {
	switch finish {
	case "stop":
		return "end_turn"
	case "length":
		return "max_tokens"
	case "tool_calls":
		return "tool_use"
	default:
		return "end_turn"
	}
}

// messageText decodes an openai message/delta content that must be a string
// or null. Some compatible servers emit part arrays instead; that is drift
// this package names rather than guesses at.
func messageText(raw json.RawMessage) (string, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return "", nil
	}
	if raw[0] != '"' {
		return "", fmt.Errorf("wirebridge: openai message content is %s, want a string or null", jsonKind(raw))
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", fmt.Errorf("wirebridge: openai message content: %w", err)
	}
	return s, nil
}

// toolUseInput turns an openai arguments string back into the JSON object an
// anthropic tool_use block carries. An empty string becomes the empty object;
// anything that is not a JSON object is named and refused.
func toolUseInput(name, args string) (json.RawMessage, error) {
	trimmed := strings.TrimSpace(args)
	if trimmed == "" {
		return json.RawMessage("{}"), nil
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, []byte(trimmed)); err != nil {
		return nil, fmt.Errorf("wirebridge: tool %s arguments: %w", name, err)
	}
	if buf.Len() == 0 || buf.Bytes()[0] != '{' {
		return nil, fmt.Errorf("wirebridge: tool %s arguments are %s, want a JSON object", name, jsonKind(buf.Bytes()))
	}
	return buf.Bytes(), nil
}
