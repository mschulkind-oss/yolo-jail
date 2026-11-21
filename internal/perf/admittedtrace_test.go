package perf

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func admittedTraceFile(root string, tr Trace, lane string) string {
	return filepath.Join(root, tr.ID.String(), TraceFileName(lane, os.Getpid()))
}

func admittedTraceIdentity(t *testing.T, claim *TraceClaim) Trace {
	t.Helper()
	id, err := hex.DecodeString(claim.ID())
	if err != nil || len(id) != len(TraceID{}) {
		t.Fatal("fixture claim identity is malformed")
	}
	var tr Trace
	copy(tr.ID[:], id)
	return tr
}

func TestAdmittedTraceSinkWritesEncodedEventsThroughItsClaim(t *testing.T) {
	store, root, _ := traceStoreFixture(t)
	claim := storeMint(t, store, 301, 1)
	tr := admittedTraceIdentity(t, claim)
	var warnings []string
	sink, closer, err := AdmittedTraceSink(claim, "launcher", tr, func(s string) { warnings = append(warnings, s) })
	if err != nil || sink == nil || closer == nil {
		t.Fatalf("admitted writer construction failed: sink=%t closer=%t err=%v", sink != nil, closer != nil, err)
	}
	log := NewTraced(nil, tr, sink)
	span := log.SpanWith("launch.prepare", Attr{Key: "safe", Value: "value"}, Attr{Key: "long", Value: strings.Repeat("x", MaxAttrValueBytes+12)})
	span.End()
	log.Note("launch.sample", "command-output-must-not-appear")
	if err := closer.Close(); err != nil {
		t.Fatalf("owned writer close failed: %v", err)
	}
	if err := closer.Close(); err != nil && (strings.Contains(err.Error(), root) || errors.Unwrap(err) != nil) {
		t.Fatal("repeated writer close exposed its cause or fixture locator")
	}
	if len(warnings) != 0 {
		t.Fatalf("unexpected warning: %q", warnings)
	}
	data, err := os.ReadFile(admittedTraceFile(root, tr, "launcher"))
	if err != nil {
		t.Fatal("admitted lane was not readable after append")
	}
	lines := bytes.Split(bytes.TrimSuffix(data, []byte("\n")), []byte("\n"))
	if len(lines) != 4 {
		t.Fatalf("lane header and three events expected, got %d lines", len(lines))
	}
	var header laneLine
	if err := json.Unmarshal(lines[0], &header); err != nil || header.K != "lane" || header.Trace != tr.ID.String() || header.Name != "launcher" {
		t.Fatalf("unexpected admitted lane identity: %s", lines[0])
	}
	var start traceLine
	if err := json.Unmarshal(lines[1], &start); err != nil || start.K != "start" || start.Name != "launch.prepare" || len(start.Span) != 16 {
		t.Fatalf("span start lacks its minted identity: %s", lines[1])
	}
	var end traceLine
	if err := json.Unmarshal(lines[2], &end); err != nil || end.K != "end" || end.Span != start.Span {
		t.Fatalf("span end did not retain its start ID: %s", lines[2])
	}
	var note traceLine
	if err := json.Unmarshal(lines[3], &note); err != nil || note.K != "note" || note.Name != "launch.sample" {
		t.Fatalf("note event encoding changed: %s", lines[3])
	}
	joined := string(data)
	if strings.Contains(joined, "command-output-must-not-appear") || !strings.Contains(joined, strings.Repeat("x", MaxAttrValueBytes)) || strings.Contains(joined, strings.Repeat("x", MaxAttrValueBytes+1)) {
		t.Fatal("event detail or an over-limit attribute value entered the trace")
	}
}

func TestAdmittedTraceSinkRejectsMismatchedIdentityBeforeCreatingLane(t *testing.T) {
	store, root, _ := traceStoreFixture(t)
	claim := storeMint(t, store, 302, 1)
	tr := MintTrace()
	var warnings []string
	sink, closer, err := AdmittedTraceSink(claim, "launcher", tr, func(s string) { warnings = append(warnings, s) })
	if err == nil || sink != nil || closer != nil {
		t.Fatalf("mismatched claim identity was admitted: sink=%t closer=%t", sink != nil, closer != nil)
	}
	if strings.Contains(err.Error(), root) || errors.Unwrap(err) != nil || strings.Contains(err.Error(), tr.ID.String()) || strings.Contains(err.Error(), claim.ID()) {
		t.Fatal("constructor error exposed a locator, identity, or underlying cause")
	}
	if _, statErr := os.Stat(admittedTraceFile(root, tr, "launcher")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatal("mismatched claim created a lane")
	}
	if len(warnings) != 1 || strings.Contains(warnings[0], root) || strings.Contains(warnings[0], tr.ID.String()) {
		t.Fatal("mismatched claim did not produce one bounded privacy-safe warning")
	}
}

func TestAdmittedTraceSinkDisablesAfterHeaderAdmissionFailure(t *testing.T) {
	store, root, _ := traceStoreFixture(t)
	claim := storeMint(t, store, 303, 1)
	tr := admittedTraceIdentity(t, claim)
	usage, err := claim.Usage()
	if err != nil {
		t.Fatal("fixture usage unavailable")
	}
	if err := claim.Replace("padding.jsonl", bytes.Repeat([]byte{'p'}, int(MaxTraceContentBytes-usage.Bytes-1))); err != nil {
		t.Fatal("could not establish an admitted near-capacity fixture")
	}
	var warnings []string
	sink, closer, err := AdmittedTraceSink(claim, "launcher", tr, func(s string) { warnings = append(warnings, s) })
	if err == nil || sink != nil || closer != nil {
		t.Fatal("header did not fail when its bytes exceeded admitted capacity")
	}
	if strings.Contains(err.Error(), root) || errors.Unwrap(err) != nil {
		t.Fatal("header failure exposed its cause or locator")
	}
	if len(warnings) != 1 {
		t.Fatalf("header failure warnings = %d, want one", len(warnings))
	}
	if _, statErr := os.Stat(admittedTraceFile(root, tr, "launcher")); statErr != nil {
		t.Fatal("header failure should leave its owned empty lane charged, not remove it")
	}
}

func TestAdmittedTraceSinkDisablesAndWarnsOnceAfterAppendFailure(t *testing.T) {
	store, root, _ := traceStoreFixture(t)
	claim := storeMint(t, store, 304, 1)
	tr := admittedTraceIdentity(t, claim)
	usage, err := claim.Usage()
	if err != nil {
		t.Fatal("fixture usage unavailable")
	}
	header, err := json.Marshal(laneLine{K: "lane", Name: "launcher", PID: os.Getpid(), Trace: tr.ID.String()})
	if err != nil {
		t.Fatal("fixture header encoding failed")
	}
	if err := claim.Replace("padding.jsonl", bytes.Repeat([]byte{'p'}, int(MaxTraceContentBytes-usage.Bytes-int64(len(header)+2)))); err != nil {
		t.Fatal("could not establish admitted header-only capacity")
	}
	var warnings []string
	sink, closer, err := AdmittedTraceSink(claim, "launcher", tr, func(s string) { warnings = append(warnings, s) })
	if err != nil {
		t.Fatalf("header should fit admitted capacity: %v", err)
	}
	log := NewTraced(nil, tr, sink)
	log.Mark("first-event-does-not-fit")
	log.Mark("second-event-does-not-retry")
	if len(warnings) != 1 || strings.Contains(warnings[0], root) || strings.Contains(warnings[0], "first-event") {
		t.Fatal("append failure did not disable only this sink with one bounded warning")
	}
	if err := closer.Close(); err != nil {
		t.Fatalf("writer close failed: %v", err)
	}
	data, err := os.ReadFile(admittedTraceFile(root, tr, "launcher"))
	if err != nil || bytes.Contains(data, []byte("first-event")) || bytes.Contains(data, []byte("second-event")) {
		t.Fatal("failed events were reported or retried as stored")
	}
}

func TestAdmittedTraceSinkDeclinesContendedAppendWithoutWaiting(t *testing.T) {
	store, root, _ := traceStoreFixture(t)
	claim := storeMint(t, store, 305, 1)
	tr := admittedTraceIdentity(t, claim)
	var warnings []string
	sink, closer, err := AdmittedTraceSink(claim, "launcher", tr, func(s string) { warnings = append(warnings, s) })
	if err != nil {
		t.Fatal("admitted writer setup failed")
	}
	writer := closer.(*TraceWriter)
	writer.mu.Lock()
	done := make(chan struct{})
	go func() { sink(Event{Kind: KindMark, Name: "contended", At: time.Now()}); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		writer.mu.Unlock()
		t.Fatal("contended append waited on the writer lock")
	}
	writer.mu.Unlock()
	if len(warnings) != 1 || strings.Contains(warnings[0], root) {
		t.Fatal("contended append did not degrade once without exposing the locator")
	}
	if err := closer.Close(); err != nil {
		t.Fatalf("writer close failed: %v", err)
	}
}

type shortTraceWriter struct {
	bytes.Buffer
	short bool
}

func (w *shortTraceWriter) Write(p []byte) (int, error) {
	if w.short {
		return len(p) - 1, nil
	}
	return w.Buffer.Write(p)
}

func TestAdmittedTraceSinkTreatsShortLineWritesAsFailure(t *testing.T) {
	tr := MintTrace()
	w := &shortTraceWriter{short: true}
	var warnings []string
	state, err := newAdmittedTraceState(w, "launcher", os.Getpid(), tr, func(s string) { warnings = append(warnings, s) }, MaxTraceEvents)
	if err == nil || state != nil {
		t.Fatal("short header was accepted")
	}
	w.short = false
	state, err = newAdmittedTraceState(w, "launcher", os.Getpid(), tr, nil, MaxTraceEvents)
	if err != nil {
		t.Fatal("test writer should accept header")
	}
	w.short = true
	state.write(Event{Kind: KindMark, Name: "short", At: time.Now()})
	state.write(Event{Kind: KindMark, Name: "after-short", At: time.Now()})
	if !state.disabled.Load() || len(warnings) != 1 {
		t.Fatal("short event write was not treated as one terminal sink failure")
	}
}

func TestAdmittedTraceSinkEventLimitRequiresAdmittedTruncation(t *testing.T) {
	tr := MintTrace()
	var got [][]byte
	w := &captureTraceWriter{write: func(p []byte) (int, error) { got = append(got, append([]byte(nil), p...)); return len(p), nil }}
	state, err := newAdmittedTraceState(w, "launcher", os.Getpid(), tr, nil, 2)
	if err != nil {
		t.Fatal("capture writer header failed")
	}
	state.write(Event{Kind: KindMark, Name: "one", At: time.Unix(1, 0)})
	state.write(Event{Kind: KindMark, Name: "two", At: time.Unix(2, 0)})
	state.write(Event{Kind: KindMark, Name: "omitted", At: time.Unix(3, 0)})
	state.write(Event{Kind: KindMark, Name: "still-omitted", At: time.Unix(4, 0)})
	if len(got) != 4 {
		t.Fatalf("header, two events and one admitted truncation expected, got %d writes", len(got))
	}
	var mark traceLine
	if err := json.Unmarshal(bytes.TrimSpace(got[3]), &mark); err != nil || mark.Name != TruncatedMark || mark.T != time.Unix(3, 0).UnixNano() {
		t.Fatal("event-limit marker did not use the triggering event and existing encoding")
	}
}

type captureTraceWriter struct{ write func([]byte) (int, error) }

func (w *captureTraceWriter) Write(p []byte) (int, error) { return w.write(p) }
