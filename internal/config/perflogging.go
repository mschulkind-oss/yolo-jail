package config

import (
	"fmt"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// perflogging.go is the config half of the timing-span logging
// (docs/reference/perf-logging.md): the persistent spelling of what `--timing`
// turns on for one launch.

// perfLoggingKey is the top-level opt-IN to timing-span logging on every launch.
const perfLoggingKey = "perf_logging"

// PerfLoggingEnabled reports whether the user config turns timing-span logging
// on for every launch. Absent, false, or unreadable => false.
//
// # Why it fails CLOSED
//
// The nearest precedent inverts this (`agent_updates` defaults TRUE because it is
// an opt-OUT of a policy that is on by ruling). This key is an opt-IN to output:
// an enabled launch prints a span table to stderr and appends to
// <workspace>/.yolo/host-perf.log. An unreadable config must not silently start
// writing files and printing reports into every jail the user opens, so anything
// it cannot read is "off" — the same direction HostApplyOnLaunchEnabled takes,
// and for the same reason.
//
// # Why it is read from the USER config directly
//
// Two independent reasons, and either alone would settle it.
//
// MECHANICAL: the collector is constructed at the TOP of run.Run, before Phase 1
// loads and validates any config at all — that placement is what lets the probes
// and the pack staging be spanned. A merged-config read is not available there;
// this one is (it is a single small file).
//
// SCOPE: /workspace is bind-mounted rw and agent-editable, and the same argument
// `agent_updates` makes applies — a workspace value would let whatever runs in
// the jail switch on logging that writes into the workspace it is already
// editing. validatePerfLogging refuses the workspace spelling rather than
// letting it look accepted.
func PerfLoggingEnabled() bool {
	return perfLoggingValue(UserScopeConfigOrEmpty())
}

// perfLoggingValue is the reading, split from the real-home lookup so the
// validator and the tests exercise one implementation — the split
// agentUpdatesWire and hostApplyOnLaunchValue both make, for the same reason.
func perfLoggingValue(cfg *jsonx.OrderedMap) bool {
	v, present := cfg.Get(perfLoggingKey)
	if !present || v == nil {
		return false
	}
	b, ok := v.(bool)
	return ok && b
}

// perfLoggingProblem reports why a value is not a usable `perf_logging`, or ""
// when its shape is fine. A plain boolean, deliberately: the feature has exactly
// one dial today, and a vocabulary invented ahead of a second mode is a
// vocabulary nobody has had to live with. `true` already means "record, print
// nothing" (D12, docs/reference/perf-logging.md); if a second mode ever lands,
// this widens to bool-or-string the way agent_updates widened to bool-or-map —
// a bool keeps meaning what it means.
func perfLoggingProblem(v any) string {
	if _, ok := v.(bool); !ok {
		return fmt.Sprintf("expected a boolean (got %s)", pyReprValue(v))
	}
	return ""
}
