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
// open/delta/stop bookkeeping spans chunks, and the finish is split in two.
// The daemon owns SSE framing (the "data: " prefix, the "data: [DONE]"
// sentinel) and never feeds [DONE] here; it calls End instead.
//
// ⚠ THE FINISH CHUNK IS NOT THE LAST CHUNK. An upstream asked for
// stream_options.include_usage (which TranslateRequest asks for) sends the
// finish_reason on one chunk with no usage, then ONE MORE chunk with
// "choices": [] and the request's usage. So the chunk carrying finish_reason
// closes the open content block and records the stop reason, and message_delta
// + message_stop wait for the usage: they go out with the first chunk after
// the finish that reports usage, or with End when the stream ends without one.
// An upstream that has already reported usage by the finish chunk (in that
// chunk or an earlier one) gets both at once, with no wait. This translator
// used to emit message_stop on the finish chunk and drop everything after it,
// which dropped the usage chunk and gave Claude zero tokens for every bridged
// turn.
//
// Chunks after message_stop are tolerated and translate to nothing, so a chatty
// provider cannot break the grammar after it closes. Between the finish and the
// usage, a chunk that does not decode ends the wait instead of failing the
// stream: the answer is complete, so it closes the grammar with the usage
// reported so far rather than turning a finished answer into an error, and
// returns a *UsageLostError beside those events so that the lost usage is
// reported rather than arriving at Claude as a silent zero.
//
// Blocks are strictly sequential, as the anthropic grammar requires: opening
// a new block closes the one open, and finish closes anything open before
// message_delta. Upstream reasoning deltas (reasoning, reasoning_content)
// have no field to land in and so never surface (WB-D5). message_start
// carries the usage the first chunk reports, which for most upstreams is none
// (so zeros); message_delta carries every count reported by the end, in
// Anthropic's terms (tokenCounts).
type StreamTranslator struct {
	started   bool
	finished  bool    // message_stop has been emitted
	stop      *string // the anthropic stop_reason, once a finish_reason arrived
	nextIndex int
	openIdx   int  // anthropic index of the currently open block, -1 when none
	textOpen  bool // the open block, if any, is a text block
	tools     map[int]*streamToolState
	usage     tokenCounts
}

type streamToolState struct {
	anthropicIndex int
}

// UsageLostError is the error StreamTranslator.Chunk returns when a chunk after
// the finish_reason, where the usage would be, does not decode. It is the one
// error Chunk returns WITH events: the answer is whole, so Chunk also returns
// the message_delta and message_stop that close it, and the caller writes them.
// The error reports that the usage was lost, not that the stream failed; Err is
// the decode error.
type UsageLostError struct{ Err error }

func (e *UsageLostError) Error() string {
	return "wirebridge: a stream chunk after the finish did not decode, so the message " +
		"was closed without the usage it may have carried: " + e.Err.Error()
}

func (e *UsageLostError) Unwrap() error { return e.Err }

// NewStreamTranslator returns a translator for one upstream SSE stream, one
// per request. It is not safe for concurrent use; a request's stream is
// read and translated in order.
func NewStreamTranslator() *StreamTranslator {
	return &StreamTranslator{openIdx: -1}
}

// Chunk translates one upstream data: payload (raw JSON, no "data: " prefix,
// no [DONE]) into zero or more anthropic SSE events, in order. An error means
// the stream failed and the events are nil, with one exception: a
// *UsageLostError comes WITH the events that close the message, which the
// caller must still write.
func (t *StreamTranslator) Chunk(payload []byte) ([]Event, error) {
	if t.finished {
		return nil, nil
	}
	if t.tools == nil {
		t.tools = make(map[int]*streamToolState)
	}
	var ch openaiChunk
	if err := json.Unmarshal(payload, &ch); err != nil {
		if t.stop != nil {
			// Waiting on the usage chunk after a finish: the answer is whole,
			// so an undecodable trailer costs the usage, not the answer. The
			// loss is still reported, beside the events that close the message.
			end, endErr := t.messageEnd()
			if endErr != nil {
				return nil, endErr
			}
			return end, &UsageLostError{Err: fmt.Errorf("decoding openai stream chunk: %w", err)}
		}
		return nil, fmt.Errorf("wirebridge: decoding openai stream chunk: %w", err)
	}
	reported := usageFromOpenAI(ch.Usage)
	t.usage = t.usage.merge(reported)
	var evs []Event
	if !t.started {
		t.started = true
		start, err := t.messageStart(ch)
		if err != nil {
			return nil, err
		}
		evs = append(evs, start)
	}
	if t.stop != nil {
		// After the finish, a chunk matters only for the usage it carries: the
		// grammar has no place for content after the stop, so any delta here
		// is dropped, and the first report of usage closes the message.
		if reported.reported() {
			end, err := t.messageEnd()
			return append(evs, end...), err
		}
		return evs, nil
	}
	if len(ch.Choices) == 0 {
		// A usage-only chunk before any finish carries no delta: its usage is
		// recorded above and nothing else translates.
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
		reason := stopReasonFromFinish(*choice.FinishReason)
		t.stop = &reason
		if t.usage.reported() {
			// This upstream reports usage by the finish (in this chunk or an
			// earlier one), so there is nothing to wait for.
			end, err := t.messageEnd()
			return append(evs, end...), err
		}
	}
	return evs, nil
}

// End is the upstream stream's end — the [DONE] sentinel, or the body closing
// — and the daemon calls it exactly once, after the last Chunk. When a
// finish_reason has arrived and message_stop is still waiting on a usage chunk
// that never came, End emits message_delta (with whatever usage was reported)
// and message_stop. Otherwise it emits nothing: a stream that ended before any
// finish_reason did not complete, and that is the daemon's to report.
func (t *StreamTranslator) End() ([]Event, error) {
	if t.finished || t.stop == nil {
		return nil, nil
	}
	return t.messageEnd()
}

// messageEnd closes the anthropic grammar: message_delta with the stop reason
// and the usage reported so far, then message_stop. It is the one place either
// is emitted.
func (t *StreamTranslator) messageEnd() ([]Event, error) {
	md, err := marshalEvent("message_delta", messageDeltaEvent{
		Type:  "message_delta",
		Delta: messageDeltaBody{StopReason: *t.stop},
		Usage: t.usage.delta(),
	})
	if err != nil {
		return nil, err
	}
	stop, err := marshalEvent("message_stop", simpleEvent{Type: "message_stop"})
	if err != nil {
		return nil, err
	}
	t.finished = true
	return []Event{md, stop}, nil
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
	// What is known at the first chunk, which for an upstream that reports usage
	// only at the end is nothing: zeros here, and the real counts in
	// message_delta, which Anthropic's grammar allows and Claude Code reads.
	start.Message.Usage = t.usage.message()
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

// messageDeltaUsage is message_delta's cumulative usage, in Anthropic's field
// order; see tokenCounts.delta for which fields appear when.
type messageDeltaUsage struct {
	InputTokens          *int `json:"input_tokens,omitempty"`
	CacheReadInputTokens *int `json:"cache_read_input_tokens,omitempty"`
	OutputTokens         int  `json:"output_tokens"`
}

type simpleEvent struct {
	Type string `json:"type"`
}
