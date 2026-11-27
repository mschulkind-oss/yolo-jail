package run

import (
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/perf"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
	"github.com/mschulkind-oss/yolo-jail/internal/version"
)

// invocationtrace.go is the LAUNCHER'S half of startup tracing (docs/design/startup-tracing.md,
// "caller ownership, propagation, and finalization"; plan slice U6b3 A): one trace per launch,
// owned by one scope that acquires it at the collector's boundary, publishes the seven public
// metadata keys once when the launch commits to a branch, and closes everything exactly once.
//
// It is built ON the legacy collector, never in place of it: Options.Perf and PerfRef.Log stay the
// real collector with its file and notice sinks, and the trace is a second, trace-only sink set
// installed beside them (perf.Log.InstallIndependentTrace), so host-perf.log and the report read
// exactly as they did. The keeper's join and the argv carrier are slices B and C.

// invocationBranchUnknown names the jail of a launch that ended before it resolved one.
const invocationBranchUnknown = "unknown"

// traceKindIncomplete is the kind of a launch that never committed to a branch.
const traceKindIncomplete = "incomplete"

// InvocationScope owns one launcher invocation's trace. NewInvocationScope allocates only: no gate
// read, path lookup, mint or file effect. Options carries it by pointer, so every copy shares the
// one scope and its once-only close.
type InvocationScope struct {
	mu   sync.Mutex
	done sync.Once

	// attempted: acquisition has been tried (whatever it came to), so a second Run borrowing
	// this scope never re-acquires.
	attempted bool
	warn      func(string)
	warned    atomic.Bool
	start     int64 // the original collector start, in unix nanoseconds

	store  *perf.TraceStore
	claim  *perf.TraceClaim
	writer io.Closer
	root   *perf.Span

	workspace string // the metadata's workspace code
	jail      string // the actual setup-qualified jail name, once resolved

	// committed is the branch kind a launch reached; frozen says publication is over, by success
	// or by a recorded refusal, and nothing publishes again.
	committed     string
	committedJail string
	frozen        bool
}

// NewInvocationScope allocates a scope and nothing else.
func NewInvocationScope() *InvocationScope { return &InvocationScope{} }

// initLauncherTrace acquires this launch's trace, when its scope is supplied and its recording gate
// is on. Called by Run right after initPerf, before the first ordinary production event: a gate
// that is off (o.Perf nil, the gate frozen at the collector's construction) does no lookup, mint
// or file effect. A declined step warns once, unwinds only what it acquired and leaves the launch
// and its legacy log alone.
func (o *Options) initLauncherTrace() {
	s := o.InvocationScope
	if s == nil || o.Perf == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.attempted {
		return
	}
	s.attempted = true
	s.warn = func(msg string) { o.pr(o.Stderr).printf("[dim]yolo: %s[/dim]", richtext.Escape(msg)) }
	s.start = o.Perf.StartTime().UnixNano()
	s.workspace = paths.JailShortHash(runtime.FromWorkspace(o.Workspace))

	// The parents are made here, with the gate on, because the store makes only its own leaf.
	_ = os.MkdirAll(filepath.Dir(paths.TracesDir()), 0o700)
	_ = os.MkdirAll(filepath.Dir(paths.TraceSourcesDir()), 0o700)
	store, err := perf.OpenTraceStore(paths.TracesDir(), paths.TraceSourcesDir())
	if err != nil {
		s.warnOnce(err.Error())
		return
	}
	tr := perf.MintTrace()
	claim, err := store.MintPending(tr.ID.String(), s.start)
	if err != nil {
		_ = store.Close()
		s.warnOnce(err.Error())
		return
	}
	sink, writer, err := perf.AdmittedLauncherTraceSink(claim, tr, s.warnOnce)
	if err != nil {
		_ = claim.Close()
		_ = store.Close()
		s.warnOnce(err.Error())
		return
	}
	if !o.Perf.InstallIndependentTrace(tr, sink) {
		_ = writer.Close()
		_ = claim.Close()
		_ = store.Close()
		s.warnOnce("trace recording declined: the collector could not install the trace sink; keep the legacy timing log")
		return
	}
	root := o.Perf.IndependentLauncherRoot()
	s.store, s.claim, s.writer, s.root = store, claim, writer, root
	if root == nil {
		// Never rescue a nil root with a fabricated span: release now, the launch records on.
		s.releaseLocked()
		s.warnOnce("trace recording declined: the invocation root could not start; keep the legacy timing log")
	}
}

// warnOnce reports a trace failure once per scope. It takes no lock, so the admitted sink may
// call it from an event while Finalize holds s.mu.
func (s *InvocationScope) warnOnce(msg string) {
	if s.warn == nil || !s.warned.CompareAndSwap(false, true) {
		return
	}
	s.warn(msg)
}

// recordResolvedJail notes the actual setup-qualified jail name once the launch resolved it, for
// the metadata a later commit publishes. It never publishes.
func (s *InvocationScope) recordResolvedJail(cname string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.jail = cname
}

// commitBranch records the branch this launch actually took ("launch" or "attach") and publishes
// the metadata, once, now: never at readiness, return or End. cname is the actual jail identity
// ("" falls back to the resolved one). Identical repeated reports coalesce; a different report, a
// missing jail or invalid provenance freezes publication with one warning and changes no outcome.
func (s *InvocationScope) commitBranch(kind, cname string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.claim == nil || s.root == nil {
		return
	}
	if cname == "" {
		cname = s.jail
	}
	if s.committed != "" {
		if s.committed != kind || s.committedJail != cname {
			s.frozen = true
			s.warnOnce("trace metadata declined: the launch reported two different branches; the trace stays unpublished")
		}
		return
	}
	s.committed, s.committedJail = kind, cname
	if cname == "" {
		s.frozen = true
		s.warnOnce("trace metadata declined: the launch's jail identity is unknown; the trace stays unpublished")
		return
	}
	s.publishLocked(kind, paths.JailShortHash(cname))
}

// publishLocked makes the one physical publication attempt.
func (s *InvocationScope) publishLocked(kind, jail string) {
	meta := perf.TraceMetadata{
		Trace: s.claim.ID(), Kind: kind, Start: s.start,
		Jail: jail, Workspace: s.workspace, Version: version.Baked(), Commit: version.GitCommit,
	}
	s.frozen = true
	res, err := s.claim.PublishPending(meta)
	if err != nil || res.State != perf.TracePublished {
		msg := "trace metadata unavailable; the trace stays unpublished and the legacy timing log is unchanged"
		if err != nil {
			msg = err.Error()
		}
		s.warnOnce(msg)
	}
}

// Finalize closes the invocation once: a launch that never committed publishes `incomplete`, then
// the root ends, the writer closes, the claim closes and the store closes, each attempted whatever
// the last returned. Safe on nil, and from the signal arms' exit as well as a deferred return.
func (s *InvocationScope) Finalize() {
	if s == nil {
		return
	}
	s.done.Do(func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.claim == nil || s.root == nil {
			s.releaseLocked()
			return
		}
		if s.committed == "" && !s.frozen {
			s.committed = traceKindIncomplete
			jail := invocationBranchUnknown
			if s.jail != "" {
				jail = paths.JailShortHash(s.jail)
			}
			s.publishLocked(traceKindIncomplete, jail)
		}
		s.root.End()
		s.releaseLocked()
	})
}

// releaseLocked closes what was acquired, in close order, and forgets it.
func (s *InvocationScope) releaseLocked() {
	if s.writer != nil {
		_ = s.writer.Close()
		s.writer = nil
	}
	if s.claim != nil {
		_ = s.claim.Close()
		s.claim = nil
	}
	if s.store != nil {
		_ = s.store.Close()
		s.store = nil
	}
}
