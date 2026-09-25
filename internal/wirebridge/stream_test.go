package wirebridge

import (
	"encoding/json"
	"errors"
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
		// This upstream reports usage ON the finish chunk, so the message closes
		// there, with the input count as well as the output count.
		{Name: "message_delta", Data: []byte(`{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"input_tokens":3,"output_tokens":2}}`)},
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
	// No usage ever arrives, so the message closes at the stream's end.
	end, err := s.End()
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, end...)

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

// TestStreamUsageOnlyChunkThenFinish covers a usage-only chunk ("choices": [])
// that arrives BEFORE the finish: its usage is already in hand at the finish,
// so the message closes there, with that usage.
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
		{Name: "message_delta", Data: []byte(`{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"input_tokens":5,"output_tokens":7}}`)},
		{Name: "message_stop", Data: []byte(`{"type":"message_stop"}`)},
	})
}

// TestStreamUsageChunkAfterFinishIsReported is the recorded shape of an OpenAI
// chat-completions stream that was asked for stream_options.include_usage:
// every chunk carries "usage": null, the finish_reason chunk carries no usage,
// and ONE more chunk follows it, with "choices": [] and the whole request's
// usage. The bridge used to close the anthropic grammar on the finish chunk and
// drop this one, so Claude saw output_tokens 0 and input_tokens 0 for every
// bridged turn. Now message_delta waits for it and reports what Anthropic's own
// stream reports there: input tokens (the uncached remainder, because OpenAI's
// prompt_tokens INCLUDES its cached_tokens and Anthropic's input_tokens does
// not), cache reads, and output tokens.
func TestStreamUsageChunkAfterFinishIsReported(t *testing.T) {
	s := NewStreamTranslator()
	var got []Event
	for _, payload := range []string{
		`{"id":"chatcmpl-u1","object":"chat.completion.chunk","model":"m","choices":[{"index":0,"delta":{"role":"assistant","content":"Hi"},"finish_reason":null}],"usage":null}`,
		`{"id":"chatcmpl-u1","object":"chat.completion.chunk","model":"m","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":null}`,
		`{"id":"chatcmpl-u1","object":"chat.completion.chunk","model":"m","choices":[],"usage":{"prompt_tokens":1200,"completion_tokens":42,"total_tokens":1242,"prompt_tokens_details":{"cached_tokens":1000}}}`,
	} {
		got = append(got, mustChunk(t, s, payload)...)
	}
	assertEvents(t, got, []Event{
		{Name: "message_start", Data: []byte(`{"type":"message_start","message":{"id":"chatcmpl-u1","type":"message","role":"assistant","content":[],"model":"m","stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":0,"output_tokens":0}}}`)},
		{Name: "content_block_start", Data: []byte(`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`)},
		{Name: "content_block_delta", Data: []byte(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hi"}}`)},
		{Name: "content_block_stop", Data: []byte(`{"type":"content_block_stop","index":0}`)},
		{Name: "message_delta", Data: []byte(`{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"input_tokens":200,"cache_read_input_tokens":1000,"output_tokens":42}}`)},
		{Name: "message_stop", Data: []byte(`{"type":"message_stop"}`)},
	})
}

// TestStreamAfterFinishIsInert: after a finish_reason the answer is whole, so a
// malformed chunk while the translator waits on usage closes the message with
// the usage it has rather than failing a finished answer; and after
// message_stop, every chunk (malformed or not) and End translate to nothing.
// Before a finish, a malformed chunk is an error (TestStreamBadChunk).
//
// The malformed chunk is also where the usage would have been, so closing
// without it is not silent: Chunk returns the closing events AND a
// *UsageLostError carrying the decode error, which the daemon logs.
func TestStreamAfterFinishIsInert(t *testing.T) {
	s := NewStreamTranslator()
	mustChunk(t, s, `{"id":"c","model":"m","choices":[{"index":0,"delta":{"content":"x"},"finish_reason":"stop"}]}`)
	// A usage chunk with a type mismatch: the shape an upstream actually sends
	// wrong, rather than bytes that are not JSON at all.
	trailing, err := s.Chunk([]byte(`{"id":"c","model":"m","choices":[],"usage":{"prompt_tokens":"12","completion_tokens":3}}`))
	var lost *UsageLostError
	if !errors.As(err, &lost) {
		t.Fatalf("a malformed chunk after the finish must report the lost usage as a *UsageLostError, got %v", err)
	}
	if !strings.Contains(err.Error(), "prompt_tokens") || lost.Unwrap() == nil {
		t.Errorf("the lost-usage error must carry the decode error that caused it, got %v", err)
	}
	assertEvents(t, trailing, []Event{
		{Name: "message_delta", Data: []byte(`{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":0}}`)},
		{Name: "message_stop", Data: []byte(`{"type":"message_stop"}`)},
	})
	for _, payload := range []string{`still not json`, `{"id":"c","model":"m","choices":[],"usage":{"prompt_tokens":9,"completion_tokens":9}}`} {
		if evs, err := s.Chunk([]byte(payload)); err != nil || len(evs) != 0 {
			t.Fatalf("a chunk after message_stop must emit nothing: %s (err %v)", formatAll(evs), err)
		}
	}
	if evs, err := s.End(); err != nil || len(evs) != 0 {
		t.Fatalf("End after message_stop must emit nothing: %s (err %v)", formatAll(evs), err)
	}
}

// TestStreamEndClosesAFinishWhoseUsageNeverCame: an upstream that ignores
// include_usage sends the finish chunk and then [DONE]. The daemon calls End at
// the sentinel, and that closes the message; the usage it never sent stays out
// of message_delta rather than arriving as a zero input count.
func TestStreamEndClosesAFinishWhoseUsageNeverCame(t *testing.T) {
	s := NewStreamTranslator()
	mustChunk(t, s, `{"id":"c","model":"m","choices":[{"index":0,"delta":{"content":"x"},"finish_reason":null}]}`)
	fin := mustChunk(t, s, `{"id":"c","model":"m","choices":[{"index":0,"delta":{},"finish_reason":"length"}]}`)
	assertEvents(t, fin, []Event{
		{Name: "content_block_stop", Data: []byte(`{"type":"content_block_stop","index":0}`)},
	})
	end, err := s.End()
	if err != nil {
		t.Fatal(err)
	}
	assertEvents(t, end, []Event{
		{Name: "message_delta", Data: []byte(`{"type":"message_delta","delta":{"stop_reason":"max_tokens","stop_sequence":null},"usage":{"output_tokens":0}}`)},
		{Name: "message_stop", Data: []byte(`{"type":"message_stop"}`)},
	})
}

// TestStreamEndWithoutAFinishEmitsNothing: a stream that ended before any
// finish_reason did not complete. End must not close it as though it had; the
// daemon reports the truncation instead.
func TestStreamEndWithoutAFinishEmitsNothing(t *testing.T) {
	s := NewStreamTranslator()
	mustChunk(t, s, `{"id":"c","model":"m","choices":[{"index":0,"delta":{"content":"x"},"finish_reason":null}]}`)
	if evs, err := s.End(); err != nil || len(evs) != 0 {
		t.Fatalf("End before any finish must emit nothing: %s (err %v)", formatAll(evs), err)
	}
}

// TestStreamContinuousUsageReportsTheLatestTotals covers an upstream that
// reports usage on every chunk (running totals): message_start carries the
// first report, and message_delta the last one, closing at the finish chunk
// because the usage is already in hand there. Every count changes between the
// two reports, the prompt and cached counts included, so the latest report
// must win for each field, not just for output tokens.
func TestStreamContinuousUsageReportsTheLatestTotals(t *testing.T) {
	s := NewStreamTranslator()
	var got []Event
	got = append(got, mustChunk(t, s, `{"id":"c","model":"m","choices":[{"index":0,"delta":{"content":"a"},"finish_reason":null}],"usage":{"prompt_tokens":50,"completion_tokens":1,"prompt_tokens_details":{"cached_tokens":0}}}`)...)
	got = append(got, mustChunk(t, s, `{"id":"c","model":"m","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":60,"completion_tokens":6,"prompt_tokens_details":{"cached_tokens":20}}}`)...)
	assertEvents(t, got, []Event{
		{Name: "message_start", Data: []byte(`{"type":"message_start","message":{"id":"c","type":"message","role":"assistant","content":[],"model":"m","stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":50,"cache_read_input_tokens":0,"output_tokens":1}}}`)},
		{Name: "content_block_start", Data: []byte(`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`)},
		{Name: "content_block_delta", Data: []byte(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"a"}}`)},
		{Name: "content_block_stop", Data: []byte(`{"type":"content_block_stop","index":0}`)},
		{Name: "message_delta", Data: []byte(`{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"input_tokens":40,"cache_read_input_tokens":20,"output_tokens":6}}`)},
		{Name: "message_stop", Data: []byte(`{"type":"message_stop"}`)},
	})
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
