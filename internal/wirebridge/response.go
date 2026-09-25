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
// usage maps through usageFromOpenAI, the same mapping every streaming path
// uses, so a streamed turn and an unstreamed one report one set of numbers.
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
	out.Usage = usageFromOpenAI(r.Usage).message()
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

// openaiUsage is the chat-completions usage object. prompt_tokens COUNTS the
// cached tokens that prompt_tokens_details.cached_tokens reports, which is the
// one arithmetic fact usageFromOpenAI exists to get right.
type openaiUsage struct {
	PromptTokens        *int `json:"prompt_tokens"`
	CompletionTokens    *int `json:"completion_tokens"`
	PromptTokensDetails *struct {
		CachedTokens *int `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
}

// tokenCounts is one upstream usage report in Anthropic's terms. A nil field is
// a count the upstream did not report, which is different from a reported zero:
// only a reported count may overwrite a number Claude already holds.
//
// The fields follow Anthropic's usage semantics, not OpenAI's. Anthropic's
// input_tokens is the UNCACHED remainder of the prompt (the whole prompt is
// input_tokens + cache_creation_input_tokens + cache_read_input_tokens), while
// both OpenAI dialects count cached tokens inside their prompt figure. So the
// cached count is subtracted from the prompt count and reported as cache reads,
// and a translator that forwarded the prompt figure as input_tokens would bill
// the cached part twice and double it in Claude's context total. OpenAI's wire
// reports no cache WRITES, so cache_creation_input_tokens is never emitted.
type tokenCounts struct {
	Input     *int
	CacheRead *int
	Output    *int
}

// usageFromOpenAI maps a chat-completions usage object; nil in, nothing
// reported out.
func usageFromOpenAI(u *openaiUsage) tokenCounts {
	if u == nil {
		return tokenCounts{}
	}
	var cached *int
	if u.PromptTokensDetails != nil {
		cached = u.PromptTokensDetails.CachedTokens
	}
	return countsFromPrompt(u.PromptTokens, cached, u.CompletionTokens)
}

// countsFromPrompt is the arithmetic both dialects share: prompt includes
// cached, so input = prompt - cached, never below zero.
func countsFromPrompt(prompt, cached, output *int) tokenCounts {
	c := tokenCounts{CacheRead: cached, Output: output}
	if prompt != nil {
		in := *prompt
		if cached != nil {
			in -= *cached
		}
		if in < 0 {
			in = 0
		}
		c.Input = &in
	}
	return c
}

// merge lays a later report over an earlier one. Upstream usage reports are
// running totals (an upstream that reports usage on every chunk reports the
// request so far each time), so the latest reported value of each count wins
// and an unreported one keeps what came before.
func (c tokenCounts) merge(later tokenCounts) tokenCounts {
	if later.Input != nil {
		c.Input = later.Input
	}
	if later.CacheRead != nil {
		c.CacheRead = later.CacheRead
	}
	if later.Output != nil {
		c.Output = later.Output
	}
	return c
}

func (c tokenCounts) reported() bool {
	return c.Input != nil || c.CacheRead != nil || c.Output != nil
}

// message renders the counts as a message's usage object (a non-streaming
// response, and message_start), where input_tokens and output_tokens are always
// present and cache reads appear only when the upstream reported them.
func (c tokenCounts) message() anthropicUsage {
	return anthropicUsage{InputTokens: intOr0(c.Input), CacheReadInputTokens: c.CacheRead,
		OutputTokens: intOr0(c.Output)}
}

// delta renders the counts as message_delta's usage object. Anthropic's
// message_delta usage is CUMULATIVE and carries input and cache counts beside
// output_tokens; Claude Code merges each field it finds there over what
// message_start said, which is why the bridge can report input tokens at the end
// of a stream when OpenAI only reports them at the end. output_tokens is always
// present (the field is required); the others appear only when reported, so an
// upstream that sent no usage does not overwrite anything with a zero.
func (c tokenCounts) delta() messageDeltaUsage {
	return messageDeltaUsage{InputTokens: c.Input, CacheReadInputTokens: c.CacheRead,
		OutputTokens: intOr0(c.Output)}
}

func intOr0(p *int) int {
	if p == nil {
		return 0
	}
	return *p
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

// anthropicUsage is a message's usage object, in Anthropic's field order.
type anthropicUsage struct {
	InputTokens          int  `json:"input_tokens"`
	CacheReadInputTokens *int `json:"cache_read_input_tokens,omitempty"`
	OutputTokens         int  `json:"output_tokens"`
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
