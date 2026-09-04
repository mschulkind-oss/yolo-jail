package macosuser

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// The tests here cover config.workspace_readonly on the macos-user backend,
// which before 2026-08-23 was accepted and silently ignored: the key is
// delivered as a `-v …:ro` bind by the CONTAINER run pipeline
// (internal/cli/run/mounts.go), and this backend has no mounts at all.
//
// TestBuildRunPlanWiresWorkspaceReadonly is the one that matters. The others
// pin SeatbeltProfile directly, and a callee-only test would stay green if the
// BuildRunPlan call site dropped the argument — which is precisely the
// "pins the callee while the call site is unpinned" shape AGENTS.md calls out
// as not being a test. Delete the third argument at runplan.go and that test
// must fail.

func TestSeatbeltProfileEmitsWorkspaceReadonlyDenies(t *testing.T) {
	p := SeatbeltProfile("/Users/Shared/proj", "", []string{".git/hooks", ".git/info"})
	for _, want := range []string{
		`(deny file-write*`,
		`(subpath "/Users/Shared/proj/.git/hooks")`,
		`(subpath "/Users/Shared/proj/.git/info")`,
	} {
		if !strings.Contains(p, want) {
			t.Errorf("profile missing %q\n%s", want, p)
		}
	}
}

// TestSeatbeltProfileReadonlyDeniesFollowTheAllow pins the ordering the whole
// mechanism rests on. SBPL is last-match-wins, so a deny emitted BEFORE the
// writable-set allow would be overridden by it and the key would be inert while
// still appearing in the profile — the same silent-no-op failure in a new place.
func TestSeatbeltProfileReadonlyDeniesFollowTheAllow(t *testing.T) {
	p := SeatbeltProfile("/Users/Shared/proj", "", []string{".git/hooks"})
	allow := strings.Index(p, "(allow file-write*")
	deny := strings.Index(p, `(subpath "/Users/Shared/proj/.git/hooks")`)
	if allow < 0 || deny < 0 {
		t.Fatalf("profile missing the allow (%d) or the deny (%d)\n%s", allow, deny, p)
	}
	if deny < allow {
		t.Errorf("readonly deny at %d precedes the writable-set allow at %d — "+
			"last-match-wins makes it inert", deny, allow)
	}
}

// TestSeatbeltProfileHasNoWriteAllowAfterReadonlyDenies is the other half of
// the ordering invariant, and it guards a FUTURE edit rather than today's code:
// the denies are terminal only while nothing later in the profile re-allows
// file-write*. Someone adding a write grant below them would silently reopen
// every path the key names.
func TestSeatbeltProfileHasNoWriteAllowAfterReadonlyDenies(t *testing.T) {
	p := SeatbeltProfile("/Users/Shared/proj", "", []string{".git/hooks"})
	deny := strings.Index(p, `(subpath "/Users/Shared/proj/.git/hooks")`)
	if deny < 0 {
		t.Fatalf("deny not emitted\n%s", p)
	}
	if after := strings.Index(p[deny:], "(allow file-write*"); after >= 0 {
		t.Errorf("a file-write allow appears %d bytes after the readonly denies — "+
			"last-match-wins means it reopens them\n%s", after, p)
	}
}

// TestSeatbeltProfileWithoutReadonlyIsUnchanged keeps the feature a pure no-op
// for anyone not using it, matching how the container path treats the same key.
func TestSeatbeltProfileWithoutReadonlyIsUnchanged(t *testing.T) {
	base := SeatbeltProfile("/Users/Shared/proj", "", nil)
	for _, empty := range [][]string{nil, {}, {""}, {"   "}} {
		if got := SeatbeltProfile("/Users/Shared/proj", "", empty); got != base {
			t.Errorf("profile drifted for %q entries:\n%s", empty, got)
		}
	}
	if strings.Contains(base, "workspace_readonly") {
		t.Errorf("empty case still emits the readonly block\n%s", base)
	}
}

// TestSeatbeltProfileDropsEscapingReadonlyEntries: config validation already
// rejects absolute and `..` entries, so these can only arrive from a caller that
// skipped it. Emitting them would widen the profile to paths OUTSIDE the
// workspace — a deny on "/" or on a real user's home — so they are dropped
// rather than rendered.
func TestSeatbeltProfileDropsEscapingReadonlyEntries(t *testing.T) {
	p := SeatbeltProfile("/Users/Shared/proj", "", []string{
		"/etc", "..", "../../elsewhere", "a/../../b", "ok/..",
	})
	if strings.Contains(p, "workspace_readonly") {
		t.Errorf("escaping-only entry set still emitted a deny block\n%s", p)
	}
	for _, bad := range []string{`(subpath "/etc")`, "/Users/Shared/elsewhere", "/Users/Shared/b"} {
		if strings.Contains(p, bad) {
			t.Errorf("profile leaked escaping entry %q\n%s", bad, p)
		}
	}
}

// TestSeatbeltProfileEscapesReadonlyPaths: the entries are user config and reach
// SBPL as string literals, so they take the same quoting the workspace path
// already gets rather than being interpolated raw.
func TestSeatbeltProfileEscapesReadonlyPaths(t *testing.T) {
	p := SeatbeltProfile("/Users/Shared/proj", "", []string{`a"b\c`})
	if !strings.Contains(p, `(subpath "/Users/Shared/proj/a\"b\\c")`) {
		t.Errorf("readonly path not SBPL-escaped\n%s", p)
	}
}

// TestBuildRunPlanWiresWorkspaceReadonly is the CALL-SITE pin: it fails if
// runplan.go stops passing the config through, which is the failure mode that
// would restore the silent no-op with every unit test above still green.
func TestBuildRunPlanWiresWorkspaceReadonly(t *testing.T) {
	cfg := jsonx.NewOrderedMap()
	cfg.Set("workspace_readonly", []any{".git/hooks", ".git/config"})

	plan := BuildRunPlan("/Users/Shared/proj", cfg, nil, []string{"bash"}, "/usr/local/bin/yolo", "", "", jsonx.NewOrderedMap(), nil, nil)

	for _, want := range []string{
		`(subpath "/Users/Shared/proj/.git/hooks")`,
		`(subpath "/Users/Shared/proj/.git/config")`,
	} {
		if !strings.Contains(plan.Seatbelt, want) {
			t.Errorf("run plan's profile missing %q — is the config still reaching "+
				"SeatbeltProfile?\n%s", want, plan.Seatbelt)
		}
	}
}

// TestBuildRunPlanWithoutWorkspaceReadonlyEmitsNoDenies is the negative half:
// a config without the key must not grow a deny block.
func TestBuildRunPlanWithoutWorkspaceReadonlyEmitsNoDenies(t *testing.T) {
	plan := BuildRunPlan("/Users/Shared/proj", jsonx.NewOrderedMap(), nil, []string{"bash"}, "/usr/local/bin/yolo", "", "", jsonx.NewOrderedMap(), nil, nil)
	if strings.Contains(plan.Seatbelt, "workspace_readonly") {
		t.Errorf("profile emitted a readonly block with no key set\n%s", plan.Seatbelt)
	}
}

// TestCfgStrListIgnoresNonStrings: the key is user-supplied JSON, so a list with
// a number or a nested object in it must degrade to the string entries rather
// than panicking a launch.
func TestCfgStrListIgnoresNonStrings(t *testing.T) {
	cfg := jsonx.NewOrderedMap()
	cfg.Set("workspace_readonly", []any{".git/hooks", 42, nil, map[string]any{}, ".git/info"})
	got := cfgStrList(cfg, "workspace_readonly")
	if len(got) != 2 || got[0] != ".git/hooks" || got[1] != ".git/info" {
		t.Errorf("cfgStrList = %v, want [.git/hooks .git/info]", got)
	}
	if got := cfgStrList(cfg, "absent"); got != nil {
		t.Errorf("absent key = %v, want nil", got)
	}
	cfg.Set("wrong_type", "not-a-list")
	if got := cfgStrList(cfg, "wrong_type"); got != nil {
		t.Errorf("non-list value = %v, want nil", got)
	}
}
