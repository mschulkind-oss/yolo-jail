package wirebridge

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// TranslateRequest converts an Anthropic Messages API request body into an
// OpenAI chat-completions request body, exactly per wire-bridge.md §4's
// table: system flattens to one leading system message; text and image blocks
// become content parts (images pass their base64 source through as data
// URIs); tool_use becomes an assistant tool_calls entry (input marshalled
// into the arguments string); tool_result becomes a role:"tool" message with
// its content flattened (is_error gets nothing special upstream); tools
// rename input_schema to parameters and never set strict (WB-D6); thinking
// config and cache_control strip (WB-D15); max_tokens maps, stop_sequences
// becomes stop, temperature and top_p map, top_k drops, the stream flag and
// the model id pass through verbatim.
//
// The request is never forwarded with anything this package does not
// understand: an unrecognized content-block type, tool type, or role fails
// closed with an error naming it (WB-D5) — the daemon renders that as a 400.
func TranslateRequest(body []byte) ([]byte, error) {
	var req anthropicRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, fmt.Errorf("wirebridge: decoding anthropic request: %w", err)
	}
	out := openaiRequest{Model: req.Model, Messages: []openaiMessage{}}
	system, err := flattenSystem(req.System)
	if err != nil {
		return nil, err
	}
	if system != "" {
		out.Messages = append(out.Messages, openaiMessage{Role: "system", Content: system})
	}
	for i, m := range req.Messages {
		msgs, err := translateMessage(m)
		if err != nil {
			return nil, fmt.Errorf("wirebridge: message %d: %w", i, err)
		}
		out.Messages = append(out.Messages, msgs...)
	}
	for _, t := range req.Tools {
		tool, err := translateTool(t)
		if err != nil {
			return nil, err
		}
		out.Tools = append(out.Tools, tool)
	}
	out.MaxTokens = req.MaxTokens
	out.Temperature = req.Temperature
	out.TopP = req.TopP
	out.Stop = req.StopSequences
	out.Stream = req.Stream
	b, err := json.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("wirebridge: encoding openai request: %w", err)
	}
	return b, nil
}

// anthropicRequest decodes the top-level request. Keys the struct does not
// name — thinking, metadata, tool_choice, top_k, cache_control, ... — are
// dropped by construction: the decoder ignores them and the output is built
// field-by-field, so nothing unmapped is ever forwarded (WB-D15 for thinking;
// the table's top_k row is the precedent for the rest).
type anthropicRequest struct {
	Model         string          `json:"model"`
	MaxTokens     *int            `json:"max_tokens"`
	System        json.RawMessage `json:"system"`
	Messages      []anthropicMsg  `json:"messages"`
	Tools         []anthropicTool `json:"tools"`
	StopSequences []string        `json:"stop_sequences"`
	Temperature   *float64        `json:"temperature"`
	TopP          *float64        `json:"top_p"`
	Stream        *bool           `json:"stream"`
}

type anthropicMsg struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type anthropicTool struct {
	Type        string          `json:"type"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
	// cache_control and any anthropic-side strict decode-and-drop: the openai
	// output struct has no field for them, and WB-D6 forbids strict outright.
}

// openaiRequest is the output shape. Field order is the wire order; there is
// deliberately no strict and no reasoning_effort field anywhere below it.
type openaiRequest struct {
	Model       string          `json:"model,omitempty"`
	Messages    []openaiMessage `json:"messages,omitempty"`
	Tools       []openaiTool    `json:"tools,omitempty"`
	MaxTokens   *int            `json:"max_tokens,omitempty"`
	Temperature *float64        `json:"temperature,omitempty"`
	TopP        *float64        `json:"top_p,omitempty"`
	Stop        []string        `json:"stop,omitempty"`
	Stream      *bool           `json:"stream,omitempty"`
}

// openaiMessage is one output message of any role. Content is always present
// (null for an assistant message that carries only tool_calls, "" for an
// empty turn) because a role:"tool" message without content is invalid
// upstream.
type openaiMessage struct {
	Role       string           `json:"role"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
	Content    any              `json:"content"`
	ToolCalls  []openaiToolCall `json:"tool_calls,omitempty"`
}

type openaiContentPart struct {
	Type     string          `json:"type"`
	Text     string          `json:"text,omitempty"`
	ImageURL *openaiImageURL `json:"image_url,omitempty"`
}

type openaiImageURL struct {
	URL string `json:"url"`
}

type openaiTool struct {
	Type     string          `json:"type"`
	Function openaiToolDefFn `json:"function"`
}

// openaiToolDefFn carries the renamed schema. There is no Strict field on
// purpose: WB-D6 — strict is never sent upstream, and the type cannot.
type openaiToolDefFn struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type openaiToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function openaiToolCallFn `json:"function"`
}

type openaiToolCallFn struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

func translateTool(t anthropicTool) (openaiTool, error) {
	// The table maps custom tools only; a server-tool type reaching the
	// bridge is drift and must be named, not dropped.
	if t.Type != "" && t.Type != "custom" {
		return openaiTool{}, fmt.Errorf("wirebridge: unrecognized tool type %q (the bridge translates custom tools only — wire-bridge.md §4)", t.Type)
	}
	return openaiTool{
		Type: "function",
		Function: openaiToolDefFn{
			Name:        t.Name,
			Description: t.Description,
			Parameters:  t.InputSchema,
		},
	}, nil
}

// flattenSystem collapses the system field — a plain string or an array of
// text blocks — into the one system message text. cache_control on blocks
// drops here (cerebras has no prompt cache; silently ignored, per the table).
func flattenSystem(raw json.RawMessage) (string, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return "", nil
	}
	if raw[0] == '"' {
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return "", fmt.Errorf("wirebridge: system: %w", err)
		}
		return s, nil
	}
	if raw[0] == '[' {
		var blocks []anthropicBlock
		if err := json.Unmarshal(raw, &blocks); err != nil {
			return "", fmt.Errorf("wirebridge: system blocks: %w", err)
		}
		var parts []string
		for _, b := range blocks {
			if b.Type != "text" {
				return "", fmt.Errorf("wirebridge: unrecognized system block type %q (the bridge flattens text system blocks only — wire-bridge.md §4)", b.Type)
			}
			parts = append(parts, b.Text)
		}
		return strings.Join(parts, "\n\n"), nil
	}
	return "", fmt.Errorf("wirebridge: system is %s, want a string or an array of text blocks", jsonKind(raw))
}

func translateMessage(m anthropicMsg) ([]openaiMessage, error) {
	blocks, plain, err := decodeContent(m.Content)
	if err != nil {
		return nil, err
	}
	switch m.Role {
	case "user":
		return translateUserMessage(blocks, plain)
	case "assistant":
		return translateAssistantMessage(blocks)
	default:
		return nil, fmt.Errorf("wirebridge: unrecognized message role %q (want user or assistant)", m.Role)
	}
}

// decodeContent accepts an anthropic message content — a plain string or an
// array of blocks — and reports which. Unknown FIELDS on blocks decode-and-
// drop; unknown TYPES fail closed in the per-role translators (WB-D5).
func decodeContent(raw json.RawMessage) ([]anthropicBlock, bool, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil, false, nil
	}
	if raw[0] == '"' {
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil, false, fmt.Errorf("wirebridge: message content: %w", err)
		}
		return []anthropicBlock{{Type: "text", Text: s}}, true, nil
	}
	if raw[0] == '[' {
		var blocks []anthropicBlock
		if err := json.Unmarshal(raw, &blocks); err != nil {
			return nil, false, fmt.Errorf("wirebridge: message content blocks: %w", err)
		}
		return blocks, false, nil
	}
	return nil, false, fmt.Errorf("wirebridge: message content is %s, want a string or an array of blocks", jsonKind(raw))
}

// translateUserMessage maps text/image blocks to content parts and each
// tool_result to its own role:"tool" message, preserving order: a
// tool_result flushes any user parts accumulated before it, and text after a
// tool_result starts a new user message.
func translateUserMessage(blocks []anthropicBlock, plain bool) ([]openaiMessage, error) {
	if plain {
		// A plain-string content is exactly one text block; pass the string
		// through as string content rather than wrapping it in parts.
		var text string
		if len(blocks) == 1 {
			text = blocks[0].Text
		}
		return []openaiMessage{{Role: "user", Content: text}}, nil
	}
	var (
		out   []openaiMessage
		parts []openaiContentPart
	)
	flush := func() {
		if len(parts) > 0 {
			out = append(out, openaiMessage{Role: "user", Content: parts})
			parts = nil
		}
	}
	for _, b := range blocks {
		switch b.Type {
		case "text":
			parts = append(parts, openaiContentPart{Type: "text", Text: b.Text})
		case "image":
			part, err := imagePart(b)
			if err != nil {
				return nil, err
			}
			parts = append(parts, part)
		case "tool_result":
			text, err := flattenToolResult(b)
			if err != nil {
				return nil, err
			}
			flush()
			out = append(out, openaiMessage{Role: "tool", ToolCallID: b.ToolUseID, Content: text})
		default:
			return nil, fmt.Errorf("wirebridge: unrecognized content block type %q (the bridge translates text, image, tool_use and tool_result — wire-bridge.md §4)", b.Type)
		}
	}
	flush()
	if len(out) == 0 {
		out = append(out, openaiMessage{Role: "user", Content: ""})
	}
	return out, nil
}

// imagePart passes the block's base64 source through as a data URI. Only
// base64 sources translate — a URL source would have the bridge fetch
// upstream state the table never licensed.
func imagePart(b anthropicBlock) (openaiContentPart, error) {
	if b.Source == nil {
		return openaiContentPart{}, fmt.Errorf("wirebridge: image block has no source")
	}
	if b.Source.Type != "base64" {
		return openaiContentPart{}, fmt.Errorf("wirebridge: unsupported image source type %q (the bridge passes base64 sources through as data URIs — wire-bridge.md §4)", b.Source.Type)
	}
	return openaiContentPart{
		Type:     "image_url",
		ImageURL: &openaiImageURL{URL: "data:" + b.Source.MediaType + ";base64," + b.Source.Data},
	}, nil
}

// flattenToolResult renders a tool_result's content — a string or an array of
// text blocks — as the role:"tool" message text. is_error is deliberately not
// acted on: the table gives it nothing special upstream.
func flattenToolResult(b anthropicBlock) (string, error) {
	raw := bytes.TrimSpace(b.Content)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return "", nil
	}
	if raw[0] == '"' {
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return "", fmt.Errorf("wirebridge: tool_result content: %w", err)
		}
		return s, nil
	}
	if raw[0] == '[' {
		var blocks []anthropicBlock
		if err := json.Unmarshal(raw, &blocks); err != nil {
			return "", fmt.Errorf("wirebridge: tool_result content blocks: %w", err)
		}
		var parts []string
		for _, tb := range blocks {
			if tb.Type != "text" {
				return "", fmt.Errorf("wirebridge: unsupported block type %q in tool_result content (the bridge flattens text only — wire-bridge.md §4)", tb.Type)
			}
			parts = append(parts, tb.Text)
		}
		return strings.Join(parts, "\n"), nil
	}
	return "", fmt.Errorf("wirebridge: tool_result content is %s, want a string or an array of text blocks", jsonKind(raw))
}

// translateAssistantMessage maps text blocks to the message content and each
// tool_use to a tool_calls entry; the openai grammar carries both on one
// assistant message.
func translateAssistantMessage(blocks []anthropicBlock) ([]openaiMessage, error) {
	var (
		texts []string
		calls []openaiToolCall
	)
	for _, b := range blocks {
		switch b.Type {
		case "text":
			texts = append(texts, b.Text)
		case "tool_use":
			args, err := compactJSON(b.Input, fmt.Sprintf("tool_use %s input", b.ID))
			if err != nil {
				return nil, err
			}
			calls = append(calls, openaiToolCall{
				ID:       b.ID,
				Type:     "function",
				Function: openaiToolCallFn{Name: b.Name, Arguments: args},
			})
		default:
			return nil, fmt.Errorf("wirebridge: unsupported content block type %q in assistant message (the bridge translates text and tool_use there — wire-bridge.md §4)", b.Type)
		}
	}
	msg := openaiMessage{Role: "assistant"}
	if len(texts) > 0 {
		msg.Content = strings.Join(texts, "\n\n")
	}
	if len(calls) > 0 {
		msg.ToolCalls = calls
	}
	return []openaiMessage{msg}, nil
}

// anthropicBlock decodes one entry of a content array. Fields the bridge does
// not translate (cache_control, citations, ...) decode-and-drop; unknown
// TYPES are refused by the role translators.
type anthropicBlock struct {
	Type      string                `json:"type"`
	Text      string                `json:"text"`
	Source    *anthropicImageSource `json:"source"`
	ID        string                `json:"id"`
	Name      string                `json:"name"`
	Input     json.RawMessage       `json:"input"`
	ToolUseID string                `json:"tool_use_id"`
	Content   json.RawMessage       `json:"content"`
	IsError   bool                  `json:"is_error"`
}

type anthropicImageSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
	URL       string `json:"url"`
}

// compactJSON normalizes an embedded JSON value to the compact single-line
// form openai's arguments string carries, preserving key order. A missing or
// null value becomes the empty object.
func compactJSON(raw json.RawMessage, what string) (string, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return "{}", nil
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, trimmed); err != nil {
		return "", fmt.Errorf("wirebridge: %s: %w", what, err)
	}
	return buf.String(), nil
}

// jsonKind names the JSON type of a trimmed raw value, for error messages.
func jsonKind(raw []byte) string {
	switch {
	case len(raw) == 0:
		return "nothing"
	case raw[0] == '"':
		return "a string"
	case raw[0] == '[':
		return "an array"
	case raw[0] == '{':
		return "an object"
	case raw[0] == 't' || raw[0] == 'f':
		return "a boolean"
	default:
		return "a number"
	}
}
