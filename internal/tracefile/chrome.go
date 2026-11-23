package tracefile

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
)

type chromeEvent struct {
	Name     string         `json:"name"`
	Phase    string         `json:"ph"`
	PID      int            `json:"pid"`
	TID      int            `json:"tid"`
	Time     json.Number    `json:"ts,omitempty"`
	Duration json.Number    `json:"dur,omitempty"`
	Scope    string         `json:"s,omitempty"`
	Args     map[string]any `json:"args,omitempty"`
}

type exportSkipped struct {
	File   string `json:"file"`
	Reason string `json:"reason"`
}

type exportDiagnostics struct {
	Skipped   []exportSkipped `json:"skipped"`
	Anomalies []string        `json:"anomalies"`
}

func diagnostics(t *Trace) exportDiagnostics {
	d := exportDiagnostics{Skipped: make([]exportSkipped, 0, len(t.Skipped)), Anomalies: append([]string{}, t.Anomalies...)}
	for _, s := range t.Skipped {
		base := filepath.Base(s.Path)
		d.Skipped = append(d.Skipped, exportSkipped{base, strings.ReplaceAll(s.Reason, s.Path, base)})
	}
	return d
}

// WriteChrome writes offline Chrome/Perfetto trace-event JSON. Export-local process ids
// distinguish files even when their recorded OS pids coincide; original pids remain metadata.
func WriteChrome(w io.Writer, t *Trace) error {
	if err := validateExport(w, t, false); err != nil {
		return err
	}
	events := make([]chromeEvent, 0)
	for i, lane := range t.Lanes {
		pid := i + 1
		events = append(events, chromeEvent{Name: "process_name", Phase: "M", PID: pid, Args: map[string]any{"name": lane.Name, "yolo.pid": strconv.Itoa(lane.PID)}})
		for _, s := range lane.Spans {
			args := chromeAttrs(s.Attrs)
			delete(args, "yolo.unfinished")
			delete(args, "yolo.parent_span_id")
			args["yolo.trace_id"], args["yolo.span_id"] = t.ID, s.ID
			if p := exportParent(s, lane); p != nil {
				args["yolo.parent_span_id"] = p.ID
			}
			if !s.Ended {
				args["yolo.unfinished"] = true
			}
			events = append(events, chromeEvent{Name: s.Name, Phase: "X", PID: pid, Time: microseconds(s.Start), Duration: microseconds(s.Duration()), Args: args})
		}
		for _, e := range lane.Events {
			args := chromeAttrs(e.Attrs)
			args["yolo.event_kind"] = e.Kind
			events = append(events, chromeEvent{Name: e.Name, Phase: "i", PID: pid, Time: microseconds(e.T), Scope: "p", Args: args})
		}
	}
	return json.NewEncoder(w).Encode(struct {
		Events          []chromeEvent     `json:"traceEvents"`
		DisplayTimeUnit string            `json:"displayTimeUnit"`
		Diagnostics     exportDiagnostics `json:"yolo.diagnostics"`
	}{events, "ns", diagnostics(t)})
}

func chromeAttrs(attrs map[string]string) map[string]any {
	out := make(map[string]any, len(attrs)+3)
	for k, v := range attrs {
		out[k] = v
	}
	return out
}

// Splitting the integer before encoding preserves nanoseconds as decimal microseconds,
// including epoch timestamps larger than the exact integer range of a float64.
func microseconds(ns int64) json.Number {
	whole := strconv.FormatInt(ns/1000, 10)
	if fraction := ns % 1000; fraction != 0 {
		whole += "." + strings.TrimRight(fmt.Sprintf("%03d", fraction), "0")
	}
	return json.Number(whole)
}

func exportParent(s *Span, lane *Lane) *Span {
	if s.Parent != nil {
		return s.Parent
	}
	return lane.ParentSpan
}

// Validation checks representation prerequisites, not parent placement. The reader's
// resolved graph remains authoritative, including accepted anomalies in Chrome exports.
func validateExport(w io.Writer, t *Trace, uniqueIDs bool) error {
	if w == nil {
		return fmt.Errorf("trace export needs a writer")
	}
	value := reflect.ValueOf(w)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Ptr, reflect.Slice:
		if value.IsNil() {
			return fmt.Errorf("trace export needs a writer")
		}
	}
	if t == nil || !IsTraceID(t.ID) {
		return fmt.Errorf("trace export needs a valid trace id")
	}
	members := make(map[*Span]bool)
	ids := make(map[string]bool)
	lanes := make(map[*Lane]bool)
	for _, lane := range t.Lanes {
		if lane == nil || lanes[lane] || lane.PID < 0 {
			return fmt.Errorf("trace export has an invalid lane")
		}
		lanes[lane] = true
		for _, s := range lane.Spans {
			if s == nil || s.Lane != lane || members[s] || !IsSpanID(s.ID) || s.Start < 0 || s.End < s.Start {
				return fmt.Errorf("trace export has an invalid span")
			}
			if uniqueIDs && ids[s.ID] {
				return fmt.Errorf("OTLP trace export has duplicate span id %s", s.ID)
			}
			members[s], ids[s.ID] = true, true
		}
		for _, e := range lane.Events {
			if e.T < 0 || (e.Kind != KindMark && e.Kind != KindNote) {
				return fmt.Errorf("trace export has an invalid event")
			}
		}
	}
	for _, lane := range t.Lanes {
		if lane.ParentSpan != nil && !members[lane.ParentSpan] {
			return fmt.Errorf("trace export has a parent outside the trace")
		}
		for _, s := range lane.Spans {
			if s.Parent != nil && !members[s.Parent] {
				return fmt.Errorf("trace export has a parent outside the trace")
			}
		}
	}
	for _, s := range t.Spans {
		if s == nil || !members[s] {
			return fmt.Errorf("trace export has an invalid span index")
		}
	}
	return nil
}
