package wirebridge

// This file is the Anthropic Messages <-> OpenAI Responses half of the wire
// bridge.  It deliberately does not share the chat-completions structs: the
// two OpenAI dialects have materially different conversation and tool-result
// grammars, and making the distinction a type boundary prevents a route from
// accidentally posting chat-shaped JSON to /responses.

import (
	"encoding/json"
	"fmt"
	"strings"
)

// TranslateResponsesRequest converts an Anthropic Messages request into an
// OpenAI Responses request.  The bridge owns only documented equivalent
// fields; an input shape with no equivalent is rejected rather than dropped.
func TranslateResponsesRequest(body []byte) ([]byte, error) {
	var req anthropicRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, fmt.Errorf("wirebridge: decoding anthropic request: %w", err)
	}
	system, err := flattenSystem(req.System)
	if err != nil {
		return nil, err
	}
	// ChatGPT's subscription Responses endpoint requires store=false. The bridge
	// has no need for server-side response retention, so this is both required
	// for compatibility and the least-retentive request shape.
	out := responsesRequest{Model: normalizeModel(req.Model), MaxOutputTokens: req.MaxTokens, Temperature: req.Temperature, TopP: req.TopP, Stream: req.Stream, Store: false}
	if system != "" {
		out.Instructions = system
	}
	for i, m := range req.Messages {
		items, err := translateResponsesMessage(m)
		if err != nil {
			return nil, fmt.Errorf("wirebridge: message %d: %w", i, err)
		}
		out.Input = append(out.Input, items...)
	}
	for _, t := range req.Tools {
		tool, err := translateResponsesTool(t)
		if err != nil {
			return nil, err
		}
		out.Tools = append(out.Tools, tool)
	}
	// Claude's enabled thinking has no token-for-token counterpart. Responses'
	// effort is the documented control, so use a conservative stable mapping.
	// Adaptive thinking deliberately leaves effort unset: it asks the provider
	// to choose, and Responses has no adaptive effort value to carry across.
	var thinking struct {
		Type         string `json:"type"`
		BudgetTokens int    `json:"budget_tokens"`
	}
	var raw map[string]json.RawMessage
	if json.Unmarshal(body, &raw) == nil && raw["thinking"] != nil {
		if err := json.Unmarshal(raw["thinking"], &thinking); err != nil {
			return nil, fmt.Errorf("wirebridge: thinking: %w", err)
		}
		switch thinking.Type {
		case "enabled":
			effort := "medium"
			if thinking.BudgetTokens >= 16000 {
				effort = "high"
			}
			out.Reasoning = &responsesReasoning{Effort: effort}
		default:
			// Omit reasoning so Responses uses its model default. Thinking modes
			// without a budget are advisory, and Responses has no equivalent
			// adaptive mode; accepting them also keeps a new Claude mode from
			// turning an otherwise valid request into a bridge outage.
		}
	}
	b, err := json.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("wirebridge: encoding responses request: %w", err)
	}
	return b, nil
}

type responsesRequest struct {
	Model           string              `json:"model,omitempty"`
	Instructions    string              `json:"instructions,omitempty"`
	Input           []responsesInput    `json:"input,omitempty"`
	Tools           []responsesTool     `json:"tools,omitempty"`
	MaxOutputTokens *int                `json:"max_output_tokens,omitempty"`
	Temperature     *float64            `json:"temperature,omitempty"`
	TopP            *float64            `json:"top_p,omitempty"`
	Stream          *bool               `json:"stream,omitempty"`
	Store           bool                `json:"store"`
	Reasoning       *responsesReasoning `json:"reasoning,omitempty"`
}

type responsesReasoning struct {
	Effort string `json:"effort"`
}

type responsesInput struct {
	Type    string               `json:"type"`
	Role    string               `json:"role,omitempty"`
	Content []responsesInputPart `json:"content,omitempty"`
	CallID  string               `json:"call_id,omitempty"`
	Name    string               `json:"name,omitempty"`
	Args    string               `json:"arguments,omitempty"`
	Output  string               `json:"output,omitempty"`
}

type responsesInputPart struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	ImageURL string `json:"image_url,omitempty"`
}

type responsesTool struct {
	Type        string          `json:"type"`
	Name        string          `json:"name,omitempty"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

func translateResponsesTool(t anthropicTool) (responsesTool, error) {
	// Claude Code enables its native web-search server tool by supplying one of
	// Anthropic's versioned web_search_* definitions. The Codex Responses API
	// provides the equivalent hosted capability. It runs server-side in both
	// APIs, so it has no function name or schema to carry across.
	if strings.HasPrefix(t.Type, "web_search_") {
		return responsesTool{Type: "web_search"}, nil
	}
	if t.Type != "" && t.Type != "custom" {
		return responsesTool{}, fmt.Errorf("wirebridge: unrecognized tool type %q (the Responses route translates custom tools and web search only)", t.Type)
	}
	return responsesTool{Type: "function", Name: t.Name, Description: t.Description, Parameters: t.InputSchema}, nil
}

func translateResponsesMessage(m anthropicMsg) ([]responsesInput, error) {
	blocks, plain, err := decodeContent(m.Content)
	if err != nil {
		return nil, err
	}
	switch m.Role {
	case "user":
		return translateResponsesUser(blocks, plain)
	case "assistant":
		return translateResponsesAssistant(blocks)
	case "system":
		return translateResponsesSystem(blocks)
	default:
		return nil, fmt.Errorf("wirebridge: unrecognized message role %q (want user, assistant, or system)", m.Role)
	}
}

// translateResponsesSystem preserves Claude Code's in-conversation system
// reminders as developer messages. They cannot be folded into instructions:
// instructions is top-level and would lose their position among conversation
// turns. The Responses wire accepts text input only for this compatibility
// case; unsupported blocks stay visible as a 400 rather than being dropped.
func translateResponsesSystem(blocks []anthropicBlock) ([]responsesInput, error) {
	parts := make([]responsesInputPart, 0, len(blocks))
	for _, b := range blocks {
		if b.Type != "text" {
			return nil, fmt.Errorf("wirebridge: unsupported content block type %q in system message (the Responses route translates text only)", b.Type)
		}
		parts = append(parts, responsesInputPart{Type: "input_text", Text: b.Text})
	}
	return []responsesInput{{Type: "message", Role: "developer", Content: parts}}, nil
}

func translateResponsesUser(blocks []anthropicBlock, plain bool) ([]responsesInput, error) {
	if plain {
		text := ""
		if len(blocks) == 1 {
			text = blocks[0].Text
		}
		return []responsesInput{{Type: "message", Role: "user", Content: []responsesInputPart{{Type: "input_text", Text: text}}}}, nil
	}
	var out []responsesInput
	var parts []responsesInputPart
	flush := func() {
		if len(parts) > 0 {
			out = append(out, responsesInput{Type: "message", Role: "user", Content: parts})
			parts = nil
		}
	}
	for _, b := range blocks {
		switch b.Type {
		case "text":
			parts = append(parts, responsesInputPart{Type: "input_text", Text: b.Text})
		case "image":
			part, err := imagePart(b)
			if err != nil {
				return nil, err
			}
			parts = append(parts, responsesInputPart{Type: "input_image", ImageURL: part.ImageURL.URL})
		case "tool_result":
			text, err := flattenToolResult(b)
			if err != nil {
				return nil, err
			}
			flush()
			out = append(out, responsesInput{Type: "function_call_output", CallID: b.ToolUseID, Output: text})
		default:
			return nil, fmt.Errorf("wirebridge: unrecognized content block type %q (the Responses route translates text, image, and tool_result)", b.Type)
		}
	}
	flush()
	if len(out) == 0 {
		out = append(out, responsesInput{Type: "message", Role: "user", Content: []responsesInputPart{{Type: "input_text", Text: ""}}})
	}
	return out, nil
}

func translateResponsesAssistant(blocks []anthropicBlock) ([]responsesInput, error) {
	var out []responsesInput
	var texts []string
	for _, b := range blocks {
		switch b.Type {
		case "text":
			texts = append(texts, b.Text)
		case "tool_use":
			args, err := compactJSON(b.Input, fmt.Sprintf("tool_use %s input", b.ID))
			if err != nil {
				return nil, err
			}
			out = append(out, responsesInput{Type: "function_call", CallID: b.ID, Name: b.Name, Args: args})
		default:
			return nil, fmt.Errorf("wirebridge: unsupported content block type %q in assistant message (the Responses route translates text and tool_use)", b.Type)
		}
	}
	if len(texts) > 0 {
		out = append([]responsesInput{{Type: "message", Role: "assistant", Content: []responsesInputPart{{Type: "output_text", Text: strings.Join(texts, "\n\n")}}}}, out...)
	}
	return out, nil
}

// TranslateResponsesResponse converts a completed Responses response into an
// Anthropic Messages response.
func TranslateResponsesResponse(body []byte) ([]byte, error) {
	var r responsesResponse
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("wirebridge: decoding responses response: %w", err)
	}
	out := anthropicMessage{ID: r.ID, Type: "message", Role: "assistant", Model: r.Model, Content: []any{}, StopReason: responsesStopReason(r.Status, r.IncompleteDetails)}
	for _, item := range r.Output {
		switch item.Type {
		case "message":
			for _, p := range item.Content {
				if p.Type == "output_text" {
					out.Content = append(out.Content, anthropicTextBlock{Type: "text", Text: p.Text})
				}
			}
		case "function_call":
			input, err := toolUseInput(item.Name, item.Arguments)
			if err != nil {
				return nil, err
			}
			out.Content = append(out.Content, anthropicToolUseBlock{Type: "tool_use", ID: item.CallID, Name: item.Name, Input: input})
			out.StopReason = "tool_use"
		case "reasoning":
			// Responses reasoning items are opaque (and may carry encrypted state),
			// not an Anthropic thinking block Claude can replay safely.
			continue
		case "web_search_call":
			// Responses has already run this hosted tool before producing its
			// output text. Claude's server-tool transcript is transport-specific;
			// forwarding it as a client tool call would make Claude wait for a
			// result that it must not execute. Keep the completed answer instead.
			continue
		default:
			return nil, fmt.Errorf("wirebridge: unsupported Responses output item type %q", item.Type)
		}
	}
	if r.Usage != nil {
		out.Usage.InputTokens = r.Usage.InputTokens
		out.Usage.OutputTokens = r.Usage.OutputTokens
	}
	b, err := json.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("wirebridge: encoding anthropic response: %w", err)
	}
	return b, nil
}

type responsesResponse struct {
	ID                string            `json:"id"`
	Model             string            `json:"model"`
	Status            string            `json:"status"`
	Output            []responsesOutput `json:"output"`
	Usage             *responsesUsage   `json:"usage"`
	IncompleteDetails *struct {
		Reason string `json:"reason"`
	} `json:"incomplete_details"`
}
type responsesOutput struct {
	Type    string `json:"type"`
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	CallID    string `json:"call_id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}
type responsesUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

func responsesStopReason(status string, details *struct {
	Reason string `json:"reason"`
}) string {
	if status == "incomplete" && details != nil && details.Reason == "max_output_tokens" {
		return "max_tokens"
	}
	return "end_turn"
}

// ResponsesStreamTranslator maps the Responses SSE event grammar to the
// Anthropic event grammar without buffering a completion.  One translator is
// allocated per upstream request.
type ResponsesStreamTranslator struct {
	started, finished, textOpen, toolOpen bool
	id, model, toolID, toolName           string
	index, outputTokens                   int
}

func NewResponsesStreamTranslator() *ResponsesStreamTranslator { return &ResponsesStreamTranslator{} }
func (t *ResponsesStreamTranslator) Chunk(payload []byte) ([]Event, error) {
	if t.finished {
		return nil, nil
	}
	var e struct {
		Type     string `json:"type"`
		Response struct {
			ID                string          `json:"id"`
			Model             string          `json:"model"`
			Status            string          `json:"status"`
			Usage             *responsesUsage `json:"usage"`
			IncompleteDetails *struct {
				Reason string `json:"reason"`
			} `json:"incomplete_details"`
		} `json:"response"`
		Delta string          `json:"delta"`
		Item  responsesOutput `json:"item"`
	}
	if err := json.Unmarshal(payload, &e); err != nil {
		return nil, fmt.Errorf("wirebridge: decoding Responses stream event: %w", err)
	}
	if e.Response.ID != "" {
		t.id, t.model = e.Response.ID, e.Response.Model
	}
	var out []Event
	start := func() error {
		if t.started {
			return nil
		}
		t.started = true
		ev, err := marshalEvent("message_start", messageStartEvent{Type: "message_start", Message: messageStartMsg{ID: t.id, Type: "message", Role: "assistant", Content: []any{}, Model: t.model, Usage: anthropicUsage{}}})
		if err == nil {
			out = append(out, ev)
		}
		return err
	}
	open := func(kind, id, name string) error {
		if err := start(); err != nil {
			return err
		}
		if t.textOpen || t.toolOpen {
			ev, err := marshalEvent("content_block_stop", blockStopEvent{Type: "content_block_stop", Index: t.index})
			if err != nil {
				return err
			}
			out = append(out, ev)
			t.index++
			t.textOpen = false
			t.toolOpen = false
		}
		var block any = anthropicTextBlock{Type: "text", Text: ""}
		if kind == "tool" {
			block = anthropicToolUseBlock{Type: "tool_use", ID: id, Name: name, Input: json.RawMessage("{}")}
			t.toolID, t.toolName = id, name
			t.toolOpen = true
		} else {
			t.textOpen = true
		}
		ev, err := marshalEvent("content_block_start", blockStartEvent{Type: "content_block_start", Index: t.index, ContentBlock: block})
		if err == nil {
			out = append(out, ev)
		}
		return err
	}
	closeAndStop := func(status string, details *struct {
		Reason string `json:"reason"`
	}, usage *responsesUsage) error {
		if err := start(); err != nil {
			return err
		}
		if usage != nil {
			t.outputTokens = usage.OutputTokens
		}
		if t.textOpen || t.toolOpen {
			ev, err := marshalEvent("content_block_stop", blockStopEvent{Type: "content_block_stop", Index: t.index})
			if err != nil {
				return err
			}
			out = append(out, ev)
		}
		reason := responsesStopReason(status, details)
		if t.toolOpen {
			reason = "tool_use"
		}
		ev, err := marshalEvent("message_delta", messageDeltaEvent{Type: "message_delta", Delta: messageDeltaBody{StopReason: reason}, Usage: messageDeltaUsage{OutputTokens: t.outputTokens}})
		if err != nil {
			return err
		}
		out = append(out, ev)
		ev, err = marshalEvent("message_stop", simpleEvent{Type: "message_stop"})
		if err != nil {
			return err
		}
		out = append(out, ev)
		t.finished = true
		return nil
	}
	switch e.Type {
	case "response.output_text.delta":
		if !t.textOpen {
			if err := open("text", "", ""); err != nil {
				return nil, err
			}
		}
		ev, err := marshalEvent("content_block_delta", contentBlockDelta{Type: "content_block_delta", Index: t.index, Delta: textDelta{Type: "text_delta", Text: e.Delta}})
		if err != nil {
			return nil, err
		}
		out = append(out, ev)
	case "response.output_item.added":
		if e.Item.Type == "function_call" {
			if err := open("tool", e.Item.CallID, e.Item.Name); err != nil {
				return nil, err
			}
		}
	case "response.function_call_arguments.delta":
		if !t.toolOpen {
			return nil, fmt.Errorf("wirebridge: Responses function arguments arrived before function_call")
		}
		ev, err := marshalEvent("content_block_delta", contentBlockDelta{Type: "content_block_delta", Index: t.index, Delta: inputJSONDelta{Type: "input_json_delta", PartialJSON: e.Delta}})
		if err != nil {
			return nil, err
		}
		out = append(out, ev)
	case "response.completed":
		if err := closeAndStop(e.Response.Status, e.Response.IncompleteDetails, e.Response.Usage); err != nil {
			return nil, err
		}
	case "response.created", "response.in_progress", "response.output_item.done", "response.content_part.added", "response.content_part.done", "response.output_text.done", "response.output_text.annotation.added", "response.output_text.annotation.done", "response.function_call_arguments.done", "response.web_search_call.searching", "response.web_search_call.in_progress", "response.web_search_call.completed":
		// Lifecycle markers have no Anthropic equivalent; deltas above carry the data.
	default:
		return nil, fmt.Errorf("wirebridge: unsupported Responses stream event %q", e.Type)
	}
	return out, nil
}
