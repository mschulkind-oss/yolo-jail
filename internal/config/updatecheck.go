package config

import (
	"fmt"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// updatecheck.go is the config half of the update check (internal/selfupdate):
// the persistent spelling of what YOLO_NO_UPDATE_CHECK turns off for one shell.

// updateCheckKey is the top-level opt-OUT of release-channel background checks,
// every cached one-line notice, and the launch's offer to update.
const updateCheckKey = "update_check"

// UpdateCheckEnabled reports whether the user config allows automatic release
// checks and cached update notices/offers.
// Absent or unreadable => true; only an explicit `false` turns it off.
//
// # Why it fails OPEN
//
// perf_logging fails closed because it is an opt-IN to writing files and
// printing reports. This key is the opposite shape — an opt-OUT of a default
// that is on, like agent_updates — and what it gates writes nothing into a
// workspace and prints at most one line to a terminal. An unreadable config
// therefore leaves the default alone rather than inventing a preference.
//
// # Why USER scope, read directly
//
// It is read on every host command, before any command loads a merged config,
// so only the user config is available — the same position perf_logging is in.
// And /workspace is writable by whatever runs in a jail: a workspace value would
// let a jailed agent hide from the user that their yolo is out of date.
// validateUpdateCheck refuses the workspace spelling.
func UpdateCheckEnabled() bool {
	return updateCheckValue(UserScopeConfigOrEmpty())
}

// updateCheckValue is the reading, split from the real-home lookup so the
// validator and the tests exercise one implementation (perfLoggingValue's split).
func updateCheckValue(cfg *jsonx.OrderedMap) bool {
	v, present := cfg.Get(updateCheckKey)
	if !present || v == nil {
		return true
	}
	b, ok := v.(bool)
	return !ok || b
}

// updateCheckProblem reports why a value is not a usable `update_check`, or ""
// when its shape is fine.
func updateCheckProblem(v any) string {
	if _, ok := v.(bool); !ok {
		return fmt.Sprintf("expected a boolean (got %s)", pyReprValue(v))
	}
	return ""
}
