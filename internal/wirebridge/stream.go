package wirebridge

import (
	"encoding/json"
	"fmt"
)

// Event is one anthropic SSE event ready for the wire: Name becomes the
// "event:" line, Data (the JSON payload) the "data:" line.
type Event struct {
	Name string
	Data []byte
}

// Format renders the event exactly as the daemon writes it: an "event:" line,
// a "data:" line, and the blank line that terminates the event.
func (e Event) Format() []byte {
	out := make([]byte, 0, len(e.Name)+len(e.Data)+16)
	out = append(out, "event: "...)
	out = append(out, e.Name...)
	out = append(out, '\n')
	out = append(out, "data: "...)
	out = append(out, e.Data...)
	out = append(out, '\n', '\n')
	return out
}

// StreamTranslator converts upstream chat-completions stream chunks — the
// JSON payload of one "data:" line each — into anthropic SSE events,
// event-for-event (wire-bridge.md §4's streaming rows). It is stateful by
// necessity: message_start goes out with the first chunk, content-block
// open/delta/stop bookkeeping spans chunks, and message_delta + message_stop
// go out with the first chunk that carries a finish_reason. The daemon owns
// SSE framing (the "data: " prefix, the "data: [DONE]" sentinel) and never
// feeds [DONE] here; chunks after the finish are tolerated and translate to
// nothing, so a chatty provider cannot break the grammar after message_stop.
//
// Blocks are strictly sequential, as the anthropic grammar requires: opening
// a new block closes the one open, and finish closes anything open before
// message_delta. Upstream reasoning deltas (reasoning, reasoning_content)
// have no field to land in and so never surface (WB-D5). message_start
// carries empty usage; upstream usage, when the provider sends it at all,
// lands in message_delta's output_tokens.
type StreamTranslator struct {
	started   bool
	finished  bool
	nextIndex int
	openIdx   int  // anthropic index of the currently open block, -1 when none
	textOpen  bool // the open block, if any, is a text block
	tools     map[int]*streamToolState
	lastOut   int
}

type streamToolState struct {
	anthropicIndex int
}

// NewStreamTranslator returns a translator for one upstream SSE stream, one
// per request. It is not safe for concurrent use; a request's stream is
// read and translated in order.
func NewStreamTranslator() *StreamTranslator {
	return &StreamTranslator{openIdx: -1}
}

// Chunk translates one upstream data: payload (raw JSON, no "data: " prefix,
// no [DONE]) into zero or more anthropic SSE events, in order.
func (t *StreamTranslator) Chunk(payload []byte) ([]Event, error) {
	if t.finished {
		return nil, nil
	}
	if t.tools == nil {
		t.tools = make(map[int]*streamToolState)
	}
	var ch openaiChunk
	if err := json.Unmarshal(payload, &ch); err != nil {
		return nil, fmt.Errorf("wirebridge: decoding openai stream chunk: %w", err)
	}
	var evs []Event
	if !t.started {
		t.started = true
		start, err := t.messageStart(ch)
		if err != nil {
			return nil, err
		}
		evs = append(evs, start)
	}
	if ch.Usage != nil && ch.Usage.CompletionTokens != nil {
		t.lastOut = *ch.Usage.CompletionTokens
	}
	if len(ch.Choices) == 0 {
		// A usage-only chunk ("choices": [], the standard final chunk when the
		// provider honors include_usage) carries no delta — usage is recorded
		// above and nothing else translates.
		return evs, nil
	}
	choice := ch.Choices[0]
	text, err := messageText(choice.Delta.Content)
	if err != nil {
		return nil, err
	}
	if text != "" {
		if !t.textOpen {
			// Opening a text block closes whatever is open (a tool block);
			// continued text deltas reuse the open block.
			if err := t.openBlock(&evs, anthropicTextBlock{Type: "text", Text: ""}); err != nil {
				return nil, err
			}
			t.textOpen = true
		}
		delta, err := marshalEvent("content_block_delta", contentBlockDelta{
			Type:  "content_block_delta",
			Index: t.openIdx,
			Delta: textDelta{Type: "text_delta", Text: text},
		})
		if err != nil {
			return nil, err
		}
		evs = append(evs, delta)
	}
	for _, tc := range choice.Delta.ToolCalls {
		st := t.tools[tc.Index]
		if st == nil {
			// First delta for this upstream index: open the tool_use block.
			// openai carries id and name on that first delta; both pass
			// through verbatim, which is the id claude replays as tool_result.
			st = &streamToolState{}
			t.tools[tc.Index] = st
			if err := t.openBlock(&evs, anthropicToolUseBlock{
				Type:  "tool_use",
				ID:    tc.ID,
				Name:  tc.Function.Name,
				Input: json.RawMessage("{}"),
			}); err != nil {
				return nil, err
			}
			st.anthropicIndex = t.openIdx
		}
		if tc.Function.Arguments != "" {
			delta, err := marshalEvent("content_block_delta", contentBlockDelta{
				Type:  "content_block_delta",
				Index: st.anthropicIndex,
				Delta: inputJSONDelta{Type: "input_json_delta", PartialJSON: tc.Function.Arguments},
			})
			if err != nil {
				return nil, err
			}
			evs = append(evs, delta)
		}
	}
	if choice.FinishReason != nil {
		if err := t.closeOpenBlock(&evs); err != nil {
			return nil, err
		}
		md, err := marshalEvent("message_delta", messageDeltaEvent{
			Type:  "message_delta",
			Delta: messageDeltaBody{StopReason: stopReasonFromFinish(*choice.FinishReason)},
			Usage: messageDeltaUsage{OutputTokens: t.lastOut},
		})
		if err != nil {
			return nil, err
		}
		evs = append(evs, md)
		stop, err := marshalEvent("message_stop", simpleEvent{Type: "message_stop"})
		if err != nil {
			return nil, err
		}
		evs = append(evs, stop)
		t.finished = true
	}
	return evs, nil
}

// openBlock closes whatever block is open (blocks are strictly sequential in
// the anthropic grammar) and emits content_block_start for a new one at the
// next index, which becomes the open block. It is the only place block
// indexes are allocated.
func (t *StreamTranslator) openBlock(evs *[]Event, block any) error {
	if err := t.closeOpenBlock(evs); err != nil {
		return err
	}
	idx := t.nextIndex
	t.nextIndex++
	t.openIdx = idx
	t.textOpen = false
	start, err := marshalEvent("content_block_start", blockStartEvent{
		Type:         "content_block_start",
		Index:        idx,
		ContentBlock: block,
	})
	if err != nil {
		return err
	}
	*evs = append(*evs, start)
	return nil
}

func (t *StreamTranslator) closeOpenBlock(evs *[]Event) error {
	if t.openIdx < 0 {
		return nil
	}
	idx := t.openIdx
	t.openIdx = -1
	stop, err := marshalEvent("content_block_stop", blockStopEvent{
		Type:  "content_block_stop",
		Index: idx,
	})
	if err != nil {
		return err
	}
	*evs = append(*evs, stop)
	return nil
}

func (t *StreamTranslator) messageStart(ch openaiChunk) (Event, error) {
	start := messageStartEvent{Type: "message_start"}
	start.Message.ID = ch.ID
	start.Message.Type = "message"
	start.Message.Role = "assistant"
	start.Message.Content = []any{}
	start.Message.Model = ch.Model
	start.Message.Usage = anthropicUsage{} // empty: usage lands in message_delta
	return marshalEvent("message_start", start)
}

// marshalEvent encodes an event payload the package constructed; a marshal
// failure would be a bug here, but the error is carried rather than hidden.
func marshalEvent(name string, payload any) (Event, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return Event{}, fmt.Errorf("wirebridge: encoding %s event: %w", name, err)
	}
	return Event{Name: name, Data: data}, nil
}

type openaiChunk struct {
	ID      string              `json:"id"`
	Model   string              `json:"model"`
	Choices []openaiChunkChoice `json:"choices"`
	Usage   *openaiUsage        `json:"usage"`
}

type openaiChunkChoice struct {
	Index        int         `json:"index"`
	Delta        openaiDelta `json:"delta"`
	FinishReason *string     `json:"finish_reason"`
}

// openaiDelta: delta.reasoning / delta.reasoning_content are dropped by
// construction (WB-D5) — no field decodes them.
type openaiDelta struct {
	Content   json.RawMessage `json:"content"`
	ToolCalls []struct {
		Index    int    `json:"index"`
		ID       string `json:"id"`
		Function struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		} `json:"function"`
	} `json:"tool_calls"`
}

type messageStartEvent struct {
	Type    string          `json:"type"`
	Message messageStartMsg `json:"message"`
}

type messageStartMsg struct {
	ID           string         `json:"id"`
	Type         string         `json:"type"`
	Role         string         `json:"role"`
	Content      []any          `json:"content"`
	Model        string         `json:"model"`
	StopReason   *string        `json:"stop_reason"`
	StopSequence *string        `json:"stop_sequence"`
	Usage        anthropicUsage `json:"usage"`
}

type blockStartEvent struct {
	Type         string `json:"type"`
	Index        int    `json:"index"`
	ContentBlock any    `json:"content_block"`
}

type contentBlockDelta struct {
	Type  string `json:"type"`
	Index int    `json:"index"`
	Delta any    `json:"delta"`
}

type textDelta struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type inputJSONDelta struct {
	Type        string `json:"type"`
	PartialJSON string `json:"partial_json"`
}

type blockStopEvent struct {
	Type  string `json:"type"`
	Index int    `json:"index"`
}

type messageDeltaEvent struct {
	Type  string            `json:"type"`
	Delta messageDeltaBody  `json:"delta"`
	Usage messageDeltaUsage `json:"usage"`
}

type messageDeltaBody struct {
	StopReason   string  `json:"stop_reason"`
	StopSequence *string `json:"stop_sequence"`
}

type messageDeltaUsage struct {
	OutputTokens int `json:"output_tokens"`
}

type simpleEvent struct {
	Type string `json:"type"`
}
