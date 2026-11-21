package perf

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"sync"
	"sync/atomic"
)

var (
	errAdmittedTraceIdentity = errors.New("trace lane identity is invalid or does not match its admitted claim")
	errAdmittedTraceWriter   = errors.New("trace lane writer is unavailable; continue with host-perf.log and inspect trace-store admission")
	errAdmittedTraceAppend   = errors.New("trace lane append failed; continue with host-perf.log and inspect trace-store admission")
)

const admittedTraceWarning = "trace lane unavailable; continue with host-perf.log and inspect trace-store admission"

type admittedTraceAppender interface {
	Write([]byte) (int, error)
}

type admittedTraceState struct {
	writer   admittedTraceAppender
	warn     func(string)
	mu       sync.Mutex
	limit    int
	written  int
	disabled atomic.Bool
	warned   atomic.Bool
}

// AdmittedTraceSink adapts an already admitted trace claim into one synchronous JSONL sink.
// The returned closer owns only this process's lane writer; the caller retains ownership of claim.
func AdmittedTraceSink(claim *TraceClaim, lane string, tr Trace, warn func(string)) (Sink, io.Closer, error) {
	if claim == nil || tr.ID.IsZero() || claim.id != tr.ID.String() {
		warnAdmittedTrace(warn)
		return nil, nil, errAdmittedTraceIdentity
	}
	writer, err := claim.CreateWriter(TraceFileName(lane, os.Getpid()))
	if err != nil {
		warnAdmittedTrace(warn)
		return nil, nil, errAdmittedTraceWriter
	}
	state, err := newAdmittedTraceState(writer, laneName(lane), os.Getpid(), tr, warn, MaxTraceEvents)
	if err != nil {
		_ = writer.Close()
		return nil, nil, err
	}
	return state.write, writer, nil
}

func newAdmittedTraceState(writer admittedTraceAppender, lane string, pid int, tr Trace, warn func(string), limit int) (*admittedTraceState, error) {
	if writer == nil || tr.ID.IsZero() || limit < 1 {
		warnAdmittedTrace(warn)
		return nil, errAdmittedTraceIdentity
	}
	header := laneLine{K: "lane", Name: laneName(lane), PID: pid, Trace: tr.ID.String()}
	if !tr.Parent.IsZero() {
		header.Parent = tr.Parent.String()
	}
	if err := writeAdmittedJSONLine(writer, header); err != nil {
		warnAdmittedTrace(warn)
		return nil, errAdmittedTraceWriter
	}
	return &admittedTraceState{writer: writer, warn: warn, limit: limit}, nil
}

func (s *admittedTraceState) write(e Event) {
	if !s.mu.TryLock() {
		s.disabled.Store(true)
		s.warnOnce()
		return
	}
	defer s.mu.Unlock()
	if s.disabled.Load() {
		return
	}
	line := eventLine(e)
	if s.written == s.limit {
		line = traceLine{T: e.At.UnixNano(), K: "mark", Name: TruncatedMark}
		if err := writeAdmittedJSONLine(s.writer, line); err != nil {
			s.fail()
			return
		}
		s.disabled.Store(true)
		return
	}
	if err := writeAdmittedJSONLine(s.writer, line); err != nil {
		s.fail()
		return
	}
	s.written++
}

func (s *admittedTraceState) fail() {
	s.disabled.Store(true)
	s.warnOnce()
}

func (s *admittedTraceState) warnOnce() {
	if s.warned.CompareAndSwap(false, true) {
		warnAdmittedTrace(s.warn)
	}
}

func warnAdmittedTrace(warn func(string)) {
	if warn != nil {
		warn(admittedTraceWarning)
	}
}

func writeAdmittedJSONLine(w io.Writer, value any) error {
	line, err := json.Marshal(value)
	if err != nil {
		return errAdmittedTraceAppend
	}
	line = append(line, '\n')
	n, err := w.Write(line)
	if err != nil || n != len(line) {
		return errAdmittedTraceAppend
	}
	return nil
}
