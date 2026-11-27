package run

import (
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jailtrace"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

// traceenv.go is the ARGV half of startup tracing (docs/design/startup-tracing.md, "propagation";
// plan slice U6b3 C): the launch hands the jail its trace context as one `-e YOLO_TRACEPARENT=…`
// pair on the runtime's command line, at the boundary the caller names, after every other
// mutation of that argv. The carrier is deliberate, never ambient: it is the actual joined
// lifetime or waiting span, or EMPTY, which clears a stale context a config or a manifest set.

// traceEnvPrefix is how a carrier names itself in an `-e` value.
const traceEnvPrefix = jailtrace.Env + "="

// isTraceEnvValue reports whether an `-e`/`--env` value sets or passes the carrier variable.
func isTraceEnvValue(v string) bool {
	return v == jailtrace.Env || strings.HasPrefix(v, traceEnvPrefix)
}

// insertTraceEnvBefore returns a COPY of argv with exactly one `-e YOLO_TRACEPARENT=<carrier>` pair
// immediately before boundary (the first argument equal to it), and whether it could: false, with
// argv returned unchanged, when the boundary is empty or absent, so the caller degrades the
// propagation and never guesses a position. Carrier options already before the boundary (`-e` and
// `--env` as two words, `--env=` as one) are removed first; everything else, and everything from
// the boundary on, is preserved in order. carrier may be empty: the explicit empty carrier.
func insertTraceEnvBefore(argv []string, boundary, carrier string) ([]string, bool) {
	if boundary == "" {
		return argv, false
	}
	idx := indexOfSlice(argv, boundary)
	if idx < 0 {
		return argv, false
	}
	out := make([]string, 0, len(argv)+2)
	for i := 0; i < idx; i++ {
		a := argv[i]
		switch {
		case (a == "-e" || a == "--env") && i+1 < idx && isTraceEnvValue(argv[i+1]):
			i++
		case strings.HasPrefix(a, "--env=") && isTraceEnvValue(strings.TrimPrefix(a, "--env=")):
		default:
			out = append(out, a)
		}
	}
	out = append(out, "-e", traceEnvPrefix+carrier)
	return append(out, argv[idx:]...), true
}

// withTraceEnv is insertTraceEnvBefore for a caller: where the boundary is missing it warns once,
// in the launch's own voice, and returns argv unchanged: propagation is lost, the launch is not.
func (o *Options) withTraceEnv(argv []string, boundary, carrier string) []string {
	// A SEALED launch hands its jail an exact environment allowlist (seal.go): the carrier, even the
	// empty one, is a crossing it does not make.
	if o.Sealed {
		return argv
	}
	out, ok := insertTraceEnvBefore(argv, boundary, carrier)
	if !ok {
		msg := "trace propagation declined: the runtime command has no unambiguous place for the trace context; the jail records no joined trace"
		if s := o.InvocationScope; s != nil && s.warn != nil {
			s.warnOnce(msg)
		} else {
			o.pr(o.Stderr).printf("[dim]yolo: %s[/dim]", richtext.Escape(msg))
		}
	}
	return out
}

// keeperLifetimeContext is the traceparent the keeper hands the jail's pid 1: the context of its
// ACTUAL joined lifetime span, once acquired. "" when the keeper joined nothing (gate off, no or a
// malformed context, an admission that declined), which the caller sends as the explicit empty
// carrier. Never a guessed root or an ambient value.
func (o *Options) keeperLifetimeContext() string {
	s := o.InvocationScope
	if s == nil {
		return ""
	}
	s.mu.Lock()
	root := s.root
	s.mu.Unlock()
	if root == nil {
		return ""
	}
	ctx, ok := root.Context()
	if !ok {
		return ""
	}
	return ctx.String()
}
