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

func TestAdmittedLauncherTraceSinkWritesDesignatedRoot(t *testing.T) {
	store, root, _ := traceStoreFixture(t)
	m := storeMeta(300, 123)
	claim, err := store.MintPending(m.Trace, m.Start)
	if err != nil {
		t.Fatal(err)
	}
	defer claim.Close()
	tr := admittedTraceIdentity(t, claim)
	sink, closer, err := AdmittedLauncherTraceSink(claim, tr, func(s string) { t.Error(s) })
	if err != nil {
		t.Fatal(err)
	}
	log := New(nil)
	if !log.InstallIndependentTrace(tr, sink) {
		t.Fatal("install")
	}
	r := log.IndependentLauncherRoot()
	if r == nil {
		t.Fatal("root")
	}
	log.Mark("ordinary")
	r.End()
	if err := closer.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(admittedTraceFile(root, tr, "launcher"))
	if err != nil {
		t.Fatal(err)
	}
	lines := bytes.Split(bytes.TrimSpace(data), []byte("\n"))
	if len(lines) != 4 {
		t.Fatalf("lines %d", len(lines))
	}
	var header, event map[string]json.RawMessage
	if json.Unmarshal(lines[0], &header) != nil || len(header) != 4 {
		t.Fatal("header descriptor")
	}
	if json.Unmarshal(lines[1], &event) != nil || string(event["launcher_root"]) != "true" || string(event["name"]) != "\"invocation\"" || string(event["k"]) != "\"start\"" {
		t.Fatal("designated event", string(lines[1]))
	}
	if bytes.Contains(lines[2], []byte("launcher_root")) || bytes.Contains(lines[3], []byte("launcher_root")) {
		t.Fatal("second marker")
	}
	if err := os.Remove(admittedTraceFile(root, tr, "launcher")); err != nil {
		t.Fatal(err)
	}
	if _, err := claim.CreateLauncherWriter(); err == nil {
		t.Fatal("reserved writer recreated after its sole allocation")
	}
}

func TestAdmittedLauncherDesignationRequiresReservedWriter(t *testing.T) {
	store, root, _ := traceStoreFixture(t)
	claim := storeMint(t, store, 302, 1)
	tr := admittedTraceIdentity(t, claim)
	legacy := New(nil)
	legacyRoot := filepath.Join(filepath.Dir(root), "legacy")
	if !legacy.InstallIndependentTrace(tr, TraceSink(legacyRoot, "legacy", tr, nil)) {
		t.Fatal("legacy install")
	}
	legacy.IndependentLauncherRoot().End()
	p, err := os.ReadFile(admittedTraceFile(legacyRoot, tr, "legacy"))
	if err != nil || bytes.Contains(p, []byte("launcher_root")) {
		t.Fatal("legacy writer granted reserved designation", err)
	}
	var warnings []string
	sink, closer, err := AdmittedTraceSink(claim, "launcher", tr, func(s string) { warnings = append(warnings, s) })
	if err != nil {
		t.Fatal(err)
	}
	log := New(nil)
	if !log.InstallIndependentTrace(tr, sink) {
		t.Fatal("generic install")
	}
	log.IndependentLauncherRoot().End()
	if err := closer.Close(); err != nil {
		t.Fatal(err)
	}
	p, err = os.ReadFile(admittedTraceFile(root, tr, "launcher"))
	if err != nil || bytes.Contains(p, []byte("launcher_root")) || len(warnings) != 1 {
		t.Fatal("generic writer granted reserved designation", err, warnings)
	}
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
	if err == nil || state != nil || len(warnings) != 1 {
		t.Fatal("short header was accepted or did not warn once")
	}
	w.short = false
	warnings = nil
	state, err = newAdmittedTraceState(w, "launcher", os.Getpid(), tr, func(s string) { warnings = append(warnings, s) }, MaxTraceEvents)
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
	lineBytes := func(value any) []byte {
		t.Helper()
		line, err := json.Marshal(value)
		if err != nil {
			t.Fatal("could not encode expected trace line")
		}
		return append(line, '\n')
	}
	createState := func(claim *TraceClaim, tr Trace, warn func(string)) (*TraceWriter, *admittedTraceState, func()) {
		t.Helper()
		writer, err := claim.CreateWriter(TraceFileName("launcher", os.Getpid()))
		if err != nil {
			t.Fatal("claim-owned trace writer creation failed")
		}
		closed := false
		t.Cleanup(func() {
			if !closed {
				_ = writer.Close()
			}
		})
		state, err := newAdmittedTraceState(writer, "launcher", os.Getpid(), tr, warn, 1)
		if err != nil {
			t.Fatal("claim-owned trace header was not admitted")
		}
		return writer, state, func() { closed = true }
	}

	// The state-limit marker is persisted only through the real claim-owned writer.
	store, root, _ := traceStoreFixture(t)
	claim := storeMint(t, store, 306, 1)
	tr := admittedTraceIdentity(t, claim)
	pid := os.Getpid()
	header := lineBytes(laneLine{K: "lane", Name: "launcher", PID: pid, Trace: tr.ID.String()})
	first := Event{Kind: KindMark, Name: "retained", At: time.Unix(1, 0)}
	firstLine := lineBytes(eventLine(first))
	trigger := Event{Kind: KindMark, Name: "omitted", At: time.Unix(2, 0)}
	truncatedLine := lineBytes(traceLine{T: trigger.At.UnixNano(), K: "mark", Name: TruncatedMark})
	before, err := claim.Usage()
	if err != nil {
		t.Fatal("initial trace usage unavailable")
	}
	var warnings []string
	writer, state, markWriterClosed := createState(claim, tr, func(s string) { warnings = append(warnings, s) })
	afterHeader, err := claim.Usage()
	if err != nil || afterHeader.Bytes-before.Bytes != int64(len(header)) {
		t.Fatal("claim accounting omitted the physical lane header")
	}
	state.write(first)
	afterFirst, err := claim.Usage()
	if err != nil || afterFirst.Bytes-afterHeader.Bytes != int64(len(firstLine)) {
		t.Fatal("claim accounting omitted the retained event")
	}
	state.write(trigger)
	afterMarker, err := claim.Usage()
	if err != nil || afterMarker.Bytes-afterFirst.Bytes != int64(len(truncatedLine)) {
		t.Fatal("claim did not admit the bounded truncation marker")
	}
	state.write(Event{Kind: KindMark, Name: "after-truncation", At: time.Unix(3, 0)})
	afterRepeat, err := claim.Usage()
	if err != nil || afterRepeat.Bytes != afterMarker.Bytes || len(warnings) != 0 || !state.disabled.Load() {
		t.Fatal("admitted truncation did not disable further writes without warning")
	}
	if err := writer.Close(); err != nil {
		t.Fatal("claim-owned trace writer close failed")
	}
	markWriterClosed()
	data, err := os.ReadFile(admittedTraceFile(root, tr, "launcher"))
	if err != nil || !bytes.Equal(data, bytes.Join([][]byte{header, firstLine, truncatedLine}, nil)) {
		t.Fatal("physical lane bytes differ from the admitted header, event, and truncation marker")
	}

	// Fill the trace to exactly the header+event credit using another owned writer. The marker
	// then has no admission credit and must leave both earlier files byte-for-byte unchanged.
	store, root, _ = traceStoreFixture(t)
	claim = storeMint(t, store, 307, 1)
	tr = admittedTraceIdentity(t, claim)
	pid = os.Getpid()
	header = lineBytes(laneLine{K: "lane", Name: "launcher", PID: pid, Trace: tr.ID.String()})
	first = Event{Kind: KindMark, Name: "retained", At: time.Unix(4, 0)}
	firstLine = lineBytes(eventLine(first))
	trigger = Event{Kind: KindMark, Name: "omitted", At: time.Unix(5, 0)}
	truncatedLine = lineBytes(traceLine{T: trigger.At.UnixNano(), K: "mark", Name: TruncatedMark})
	initial, err := claim.Usage()
	if err != nil {
		t.Fatal("initial trace usage unavailable")
	}
	paddingBytes := MaxTraceContentBytes - initial.Bytes - int64(len(header)+len(firstLine))
	if paddingBytes < 0 || initial.Bytes+paddingBytes+int64(len(header)+len(firstLine)) != MaxTraceContentBytes || len(truncatedLine) == 0 {
		t.Fatal("could not compute bounded capacity for denial fixture")
	}
	padding := bytes.Repeat([]byte{'p'}, int(paddingBytes))
	paddingWriter, err := claim.CreateWriter("padding.jsonl")
	if err != nil {
		t.Fatal("claim-owned padding writer creation failed")
	}
	paddingClosed := false
	t.Cleanup(func() {
		if !paddingClosed {
			_ = paddingWriter.Close()
		}
	})
	if n, err := paddingWriter.Write(padding); err != nil || n != len(padding) {
		t.Fatal("claim-owned capacity fill failed")
	}
	if err := paddingWriter.Close(); err != nil {
		t.Fatal("claim-owned capacity writer close failed")
	}
	paddingClosed = true
	paddingPath := filepath.Join(root, tr.ID.String(), "padding.jsonl")
	paddingBefore, err := os.ReadFile(paddingPath)
	if err != nil || !bytes.Equal(paddingBefore, padding) {
		t.Fatal("admitted capacity-fill bytes differ from the owned writer input")
	}
	beforeLane, err := claim.Usage()
	if err != nil || beforeLane.Bytes != MaxTraceContentBytes-int64(len(header)+len(firstLine)) {
		t.Fatal("owned capacity fill did not leave exactly header+event credit")
	}
	var deniedWarnings []string
	writer, state, markWriterClosed = createState(claim, tr, func(s string) { deniedWarnings = append(deniedWarnings, s) })
	afterHeader, err = claim.Usage()
	if err != nil || afterHeader.Bytes-beforeLane.Bytes != int64(len(header)) {
		t.Fatal("claim accounting omitted the physical lane header")
	}
	state.write(first)
	afterFirst, err = claim.Usage()
	if err != nil || afterFirst.Bytes-afterHeader.Bytes != int64(len(firstLine)) || afterFirst.Bytes != MaxTraceContentBytes {
		t.Fatal("claim accounting omitted the retained event or failed to reach the cap")
	}
	if MaxTraceContentBytes-afterFirst.Bytes >= int64(len(truncatedLine)) {
		t.Fatal("capacity fixture unexpectedly left room for the truncation marker")
	}
	state.write(trigger)
	afterDenied, err := claim.Usage()
	if err != nil || afterDenied.Bytes != afterFirst.Bytes || !state.disabled.Load() || len(deniedWarnings) != 1 {
		t.Fatal("denied truncation changed physical usage or failed to warn once and disable")
	}
	state.write(Event{Kind: KindMark, Name: "still-omitted", At: time.Unix(6, 0)})
	afterRepeat, err = claim.Usage()
	if err != nil || afterRepeat.Bytes != afterDenied.Bytes || len(deniedWarnings) != 1 {
		t.Fatal("denied truncation retried or warned more than once")
	}
	if strings.Contains(deniedWarnings[0], root) || strings.Contains(deniedWarnings[0], "omitted") {
		t.Fatal("denied truncation warning exposed a locator or event name")
	}
	if err := writer.Close(); err != nil {
		t.Fatal("claim-owned trace writer close failed")
	}
	markWriterClosed()
	paddingAfter, err := os.ReadFile(paddingPath)
	if err != nil || !bytes.Equal(paddingAfter, paddingBefore) {
		t.Fatal("capacity-denied marker altered earlier claim-owned file bytes")
	}
	data, err = os.ReadFile(admittedTraceFile(root, tr, "launcher"))
	if err != nil || !bytes.Equal(data, bytes.Join([][]byte{header, firstLine}, nil)) {
		t.Fatal("capacity-denied marker changed earlier admitted lane bytes")
	}
}
