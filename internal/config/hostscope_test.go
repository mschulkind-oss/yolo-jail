package config

// hostscope_test.go carries `hostScope`, and the reason it needs to exist.
//
// # The class: a verdict that flips on inJail(), green in a jail and red on CI
//
// Several validators are an ERROR ON THE HOST and a WARNING IN A JAIL —
// `validateProviderShorthandRetired`, `validateJournalRetired`,
// `validateHostProcessesRetired`. The reason is good: in a jail the config is the
// HOST-GENERATED SNAPSHOT, so erroring there refuses every nested launch over a key the
// in-jail user cannot fix at its source.
//
// The cost lands on the tests. This repo is DEVELOPED FROM INSIDE ITS OWN JAIL, so
// `YOLO_VERSION` is set for every local `go test` run, and a test that asserts over `errs`
// alone silently exercises the WARNING arm. `just check-ci` is green in here and the same
// commit is red on every CI runner, which is a host.
//
// MEASURED 2026-09-18: `TestValidateProviders` carried a `base_url` fixture that the
// shorthand removal had made invalid. It passed in-jail and took `check-go` and
// `check-macos` red on the push — the second CI-only class this repo has, beside the
// darwin PATH-resolution one AGENTS.md documents, and this one is invisible for exactly
// the opposite reason: the local environment is too PERMISSIVE rather than too different.
//
// # What hostScope does, and when to reach for it
//
// It unsets `YOLO_VERSION` for one test, so the validators take their HOST arm. Use it in
// any test that asserts a validator PRODUCES AN ERROR, or that a fixture produces none —
// both directions are wrong under the warning arm, and the "produces none" direction is
// the silent one.
//
// ⚠ It is not a blanket fix and must not become one. A test that is genuinely ABOUT the
// in-jail arm wants the opposite, and `TestRetiredKeysWarnInsideAJail` below is the guard
// that keeps at least one test on each side — a sweep that applied `hostScope` everywhere
// would leave the downgrade itself untested.

import (
	"strings"
	"testing"
)

// hostScope makes this test run as though on a host, whatever the ambient environment.
func hostScope(t *testing.T) {
	t.Helper()
	t.Setenv("YOLO_VERSION", "")
}

// jailScope is hostScope's twin, for a test that is about the in-jail arm.
func jailScope(t *testing.T) {
	t.Helper()
	t.Setenv("YOLO_VERSION", "9.9.9-test")
}

// TestRetiredKeysAreFatalOnAHost pins the arm CI runs on, which is the arm this repo's own
// `go test` never reaches by default.
func TestRetiredKeysAreFatalOnAHost(t *testing.T) {
	hostScope(t)
	errs, _ := ValidateConfig(decode(t,
		`{"providers": {"glm": {"base_url": "https://example.test/v1"}}}`), t.TempDir(), nil)
	var found bool
	for _, e := range errs {
		if strings.Contains(e, "base_url: REMOVED") {
			found = true
		}
	}
	if !found {
		t.Errorf("the removed shorthand must be an ERROR on a host, got errs=%v", errs)
	}
}

// TestRetiredKeysWarnInsideAJail pins the downgrade itself. Without it, a sweep that
// applied hostScope everywhere would leave the in-jail arm — the whole reason the
// downgrade exists — with no test at all.
func TestRetiredKeysWarnInsideAJail(t *testing.T) {
	jailScope(t)
	errs, warns := ValidateConfig(decode(t,
		`{"providers": {"glm": {"base_url": "https://example.test/v1"}}}`), t.TempDir(), nil)
	for _, e := range errs {
		if strings.Contains(e, "base_url: REMOVED") {
			t.Errorf("in a jail the removed shorthand must WARN, not error: %s", e)
		}
	}
	var found bool
	for _, w := range warns {
		if strings.Contains(w, "base_url: REMOVED") {
			found = true
		}
	}
	if !found {
		t.Errorf("the warning must still be produced in a jail, got warns=%v", warns)
	}
}
