package wirebridge

import (
	"encoding/json"
	"strings"
	"testing"
)

func mustChunk(t *testing.T, s *StreamTranslator, payload string) []Event {
	t.Helper()
	evs, err := s.Chunk([]byte(payload))
	if err != nil {
		t.Fatalf("Chunk(%s): unexpected error: %v", payload, err)
	}
	return evs
}

// assertEvents pins the exact event sequence: names in order, payload bytes
// verbatim, and — the anthropic grammar — every payload's own "type" equal to
// its event name.
func assertEvents(t *testing.T, got, want []Event) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("event count: got %d, want %d\n got: %s\nwant: %d events",
			len(got), len(want), formatAll(got), len(want))
	}
	for i := range want {
		if got[i].Name != want[i].Name {
			t.Errorf("event %d name: got %q, want %q", i, got[i].Name, want[i].Name)
			continue
		}
		if string(got[i].Data) != string(want[i].Data) {
			t.Errorf("event %d (%s) payload:\n got: %s\nwant: %s", i, want[i].Name, got[i].Data, want[i].Data)
			continue
		}
		var probe struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(got[i].Data, &probe); err != nil || probe.Type != got[i].Name {
			t.Errorf("event %d (%s) is not a well-formed anthropic event: type=%q err=%v", i, got[i].Name, probe.Type, err)
		}
	}
}

func formatAll(evs []Event) string {
	var sb strings.Builder
	for _, e := range evs {
		sb.Write(e.Format())
	}
	return sb.String()
}

// TestTranslateStreamTextOnly pins the full happy path for a text-only turn:
// message_start, the text block lifecycle, message_delta + message_stop.
func TestTranslateStreamTextOnly(t *testing.T) {
	s := NewStreamTranslator()
	var got []Event
	got = append(got, mustChunk(t, s, `{"id":"chatcmpl-s1","model":"m","choices":[{"index":0,"delta":{"role":"assistant","content":"He"},"finish_reason":null}]}`)...)
	got = append(got, mustChunk(t, s, `{"id":"chatcmpl-s1","model":"m","choices":[{"index":0,"delta":{"content":"llo"},"finish_reason":null}]}`)...)
	got = append(got, mustChunk(t, s, `{"id":"chatcmpl-s1","model":"m","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2}}`)...)

	want := []Event{
		{Name: "message_start", Data: []byte(`{"type":"message_start","message":{"id":"chatcmpl-s1","type":"message","role":"assistant","content":[],"model":"m","stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":0,"output_tokens":0}}}`)},
		{Name: "content_block_start", Data: []byte(`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`)},
		{Name: "content_block_delta", Data: []byte(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"He"}}`)},
		{Name: "content_block_delta", Data: []byte(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"llo"}}`)},
		{Name: "content_block_stop", Data: []byte(`{"type":"content_block_stop","index":0}`)},
		{Name: "message_delta", Data: []byte(`{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":2}}`)},
		{Name: "message_stop", Data: []byte(`{"type":"message_stop"}`)},
	}
	assertEvents(t, got, want)
}

// TestTranslateStreamToolCall pins the tool-call path: text closes when the
// tool_use block opens, argument fragments stream as input_json_delta, and
// the upstream tool_call id rides content_block_start verbatim.
func TestTranslateStreamToolCall(t *testing.T) {
	s := NewStreamTranslator()
	var got []Event
	got = append(got, mustChunk(t, s, `{"id":"chatcmpl-s2","model":"m","choices":[{"index":0,"delta":{"role":"assistant","content":"Running."},"finish_reason":null}]}`)...)
	got = append(got, mustChunk(t, s, `{"id":"chatcmpl-s2","model":"m","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"run","arguments":""}}]},"finish_reason":null}]}`)...)
	got = append(got, mustChunk(t, s, `{"id":"chatcmpl-s2","model":"m","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"pa"}}]},"finish_reason":null}]}`)...)
	got = append(got, mustChunk(t, s, `{"id":"chatcmpl-s2","model":"m","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"th\":\"b.go\"}"}}]},"finish_reason":null}]}`)...)
	got = append(got, mustChunk(t, s, `{"id":"chatcmpl-s2","model":"m","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`)...)

	want := []Event{
		{Name: "message_start", Data: []byte(`{"type":"message_start","message":{"id":"chatcmpl-s2","type":"message","role":"assistant","content":[],"model":"m","stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":0,"output_tokens":0}}}`)},
		{Name: "content_block_start", Data: []byte(`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`)},
		{Name: "content_block_delta", Data: []byte(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Running."}}`)},
		{Name: "content_block_stop", Data: []byte(`{"type":"content_block_stop","index":0}`)},
		{Name: "content_block_start", Data: []byte(`{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"call_1","name":"run","input":{}}}`)},
		{Name: "content_block_delta", Data: []byte(`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"pa"}}`)},
		{Name: "content_block_delta", Data: []byte(`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"th\":\"b.go\"}"}}`)},
		{Name: "content_block_stop", Data: []byte(`{"type":"content_block_stop","index":1}`)},
		{Name: "message_delta", Data: []byte(`{"type":"message_delta","delta":{"stop_reason":"tool_use","stop_sequence":null},"usage":{"output_tokens":0}}`)},
		{Name: "message_stop", Data: []byte(`{"type":"message_stop"}`)},
	}
	assertEvents(t, got, want)
}

// TestStreamUsageOnlyChunkThenFinish covers the standard openai tail — a
// final usage-only chunk with "choices": [] — and that its usage lands in
// message_delta.
func TestStreamUsageOnlyChunkThenFinish(t *testing.T) {
	s := NewStreamTranslator()
	first := mustChunk(t, s, `{"id":"c","model":"m","choices":[{"index":0,"delta":{"role":"assistant","content":"hi"},"finish_reason":null}]}`)
	if len(first) != 3 {
		t.Fatalf("first chunk: got %d events, want 3 (message_start + block start + delta): %s", len(first), formatAll(first))
	}
	usage := mustChunk(t, s, `{"id":"c","model":"m","choices":[],"usage":{"prompt_tokens":5,"completion_tokens":7}}`)
	if len(usage) != 0 {
		t.Fatalf("usage-only chunk must emit nothing, got %s", formatAll(usage))
	}
	fin := mustChunk(t, s, `{"id":"c","model":"m","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`)
	assertEvents(t, fin, []Event{
		{Name: "content_block_stop", Data: []byte(`{"type":"content_block_stop","index":0}`)},
		{Name: "message_delta", Data: []byte(`{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":7}}`)},
		{Name: "message_stop", Data: []byte(`{"type":"message_stop"}`)},
	})
}

// TestStreamAfterFinishIsInert: chunks after message_stop are tolerated and
// translate to nothing — including malformed ones, since the daemon never has
// reason to feed anything past [DONE]. Before finish, a malformed chunk is an
// error.
func TestStreamAfterFinishIsInert(t *testing.T) {
	s := NewStreamTranslator()
	mustChunk(t, s, `{"id":"c","model":"m","choices":[{"index":0,"delta":{"content":"x"},"finish_reason":"stop"}]}`)
	trailing, err := s.Chunk([]byte(`not even json`))
	if err != nil {
		t.Fatalf("post-finish chunk must be tolerated, got %v", err)
	}
	if len(trailing) != 0 {
		t.Fatalf("post-finish chunk must emit nothing, got %s", formatAll(trailing))
	}
}

// TestStreamDropsReasoning pins WB-D5 on the streaming side: a reasoning
// delta has nowhere to land and never surfaces in any event.
func TestStreamDropsReasoning(t *testing.T) {
	s := NewStreamTranslator()
	got := mustChunk(t, s, `{"id":"c","model":"m","choices":[{"index":0,"delta":{"role":"assistant","reasoning":"secret thoughts","content":"Hi"},"finish_reason":null}]}`)
	for _, e := range got {
		if strings.Contains(string(e.Data), "reasoning") {
			t.Errorf("event %s carries reasoning content: %s", e.Name, e.Data)
		}
	}
	assertEvents(t, got, []Event{
		{Name: "message_start", Data: []byte(`{"type":"message_start","message":{"id":"c","type":"message","role":"assistant","content":[],"model":"m","stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":0,"output_tokens":0}}}`)},
		{Name: "content_block_start", Data: []byte(`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`)},
		{Name: "content_block_delta", Data: []byte(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hi"}}`)},
	})
}

func TestStreamBadChunk(t *testing.T) {
	s := NewStreamTranslator()
	if _, err := s.Chunk([]byte(`{"choices":`)); err == nil || !strings.Contains(err.Error(), "stream chunk") {
		t.Errorf("want a stream-chunk decoding error, got %v", err)
	}
	// Non-string delta content is named, not guessed at.
	if _, err := s.Chunk([]byte(`{"choices":[{"index":0,"delta":{"content":[["x"]]},"finish_reason":null}]}`)); err == nil || !strings.Contains(err.Error(), "an array") {
		t.Errorf("want an array-content error, got %v", err)
	}
}

// TestEventFormat pins the wire rendering the daemon writes verbatim.
func TestEventFormat(t *testing.T) {
	e := Event{Name: "message_stop", Data: []byte(`{"type":"message_stop"}`)}
	got := string(e.Format())
	want := "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
	if got != want {
		t.Errorf("Format()\n got: %q\nwant: %q", got, want)
	}
}
