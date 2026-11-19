package tracefile

import (
	"encoding/json"
	"io"
	"sort"
	"strconv"
)

// OTLP JSON uses hex trace/span ids, decimal strings for 64-bit integers, and numeric
// enums. These local wire types deliberately do not depend on protobuf or an OTLP SDK.
type otlpValue struct {
	String *string `json:"stringValue,omitempty"`
	Bool   *bool   `json:"boolValue,omitempty"`
	Int    *string `json:"intValue,omitempty"`
}

type otlpAttribute struct {
	Key   string    `json:"key"`
	Value otlpValue `json:"value"`
}

type otlpEvent struct {
	Time       string          `json:"timeUnixNano"`
	Name       string          `json:"name"`
	Attributes []otlpAttribute `json:"attributes"`
}

type otlpSpan struct {
	TraceID    string          `json:"traceId"`
	SpanID     string          `json:"spanId"`
	ParentID   string          `json:"parentSpanId,omitempty"`
	Name       string          `json:"name"`
	Kind       int             `json:"kind"`
	Start      string          `json:"startTimeUnixNano"`
	End        string          `json:"endTimeUnixNano"`
	Attributes []otlpAttribute `json:"attributes"`
	Events     []otlpEvent     `json:"events"`
}

type otlpScopeSpans struct {
	Scope struct {
		Name string `json:"name"`
	} `json:"scope"`
	Spans []otlpSpan `json:"spans"`
}

type otlpResourceSpans struct {
	Resource struct {
		Attributes []otlpAttribute `json:"attributes"`
	} `json:"resource"`
	ScopeSpans []otlpScopeSpans `json:"scopeSpans"`
}

type otlpOrphanEvent struct {
	Time  string            `json:"timeUnixNano"`
	Kind  string            `json:"kind"`
	Name  string            `json:"name"`
	Attrs map[string]string `json:"attrs"`
}

// WriteOTLP writes an offline OTLP ExportTraceServiceRequest JSON representation.
// Events without an enclosing observed span remain resource metadata, never invented spans.
func WriteOTLP(w io.Writer, t *Trace) error {
	if err := validateExport(w, t, true); err != nil {
		return err
	}
	resources := make([]otlpResourceSpans, 0, len(t.Lanes))
	diagnosticJSON, err := json.Marshal(diagnostics(t))
	if err != nil {
		return err
	}
	for _, lane := range t.Lanes {
		events := make(map[*Span][]otlpEvent)
		orphans := make([]otlpOrphanEvent, 0)
		for _, e := range lane.Events {
			var enclosing *Span
			for _, s := range lane.Spans {
				if s.Start <= e.T && e.T <= s.End && (enclosing == nil || s.Duration() < enclosing.Duration()) {
					enclosing = s
				}
			}
			if enclosing == nil {
				attrs := e.Attrs
				if attrs == nil {
					attrs = map[string]string{}
				}
				orphans = append(orphans, otlpOrphanEvent{strconv.FormatInt(e.T, 10), e.Kind, e.Name, attrs})
				continue
			}
			attrs := otlpAttrs(e.Attrs, "yolo.event_kind")
			attrs = append(attrs, otlpString("yolo.event_kind", e.Kind))
			events[enclosing] = append(events[enclosing], otlpEvent{strconv.FormatInt(e.T, 10), e.Name, attrs})
		}
		scope := otlpScopeSpans{Spans: make([]otlpSpan, 0, len(lane.Spans))}
		scope.Scope.Name = "yolo-jail"
		for _, s := range lane.Spans {
			attrs := otlpAttrs(s.Attrs, "yolo.unfinished")
			if !s.Ended {
				unfinished := true
				attrs = append(attrs, otlpAttribute{"yolo.unfinished", otlpValue{Bool: &unfinished}})
			}
			spanEvents := events[s]
			if spanEvents == nil {
				spanEvents = []otlpEvent{}
			}
			out := otlpSpan{TraceID: t.ID, SpanID: s.ID, Name: s.Name, Kind: 1, Start: strconv.FormatInt(s.Start, 10), End: strconv.FormatInt(s.End, 10), Attributes: attrs, Events: spanEvents}
			if p := exportParent(s, lane); p != nil {
				out.ParentID = p.ID
			}
			scope.Spans = append(scope.Spans, out)
		}
		resource := otlpResourceSpans{ScopeSpans: []otlpScopeSpans{scope}}
		pid := strconv.Itoa(lane.PID)
		resource.Resource.Attributes = []otlpAttribute{
			otlpString("yolo.lane", lane.Name),
			{Key: "process.pid", Value: otlpValue{Int: &pid}},
			otlpString("yolo.diagnostics", string(diagnosticJSON)),
		}
		if len(orphans) > 0 {
			encoded, err := json.Marshal(orphans)
			if err != nil {
				return err
			}
			resource.Resource.Attributes = append(resource.Resource.Attributes, otlpString("yolo.unparented_events", string(encoded)))
		}
		resources = append(resources, resource)
	}
	return json.NewEncoder(w).Encode(struct {
		Resources []otlpResourceSpans `json:"resourceSpans"`
	}{resources})
}

func otlpString(key, value string) otlpAttribute {
	return otlpAttribute{key, otlpValue{String: &value}}
}

func otlpAttrs(attrs map[string]string, reserved string) []otlpAttribute {
	keys := make([]string, 0, len(attrs))
	for k := range attrs {
		if k != reserved {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	out := make([]otlpAttribute, 0, len(keys)+1)
	for _, k := range keys {
		out = append(out, otlpString(k, attrs[k]))
	}
	return out
}
