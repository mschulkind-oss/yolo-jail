package macosuser

import (
	"os"
	"path/filepath"
	"slices"
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
	p := SeatbeltProfile("/Users/Shared/proj", "", []string{".git/hooks", ".git/info"}, HomeReadonly{})
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
	p := SeatbeltProfile("/Users/Shared/proj", "", []string{".git/hooks"}, HomeReadonly{})
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
	p := SeatbeltProfile("/Users/Shared/proj", "", []string{".git/hooks"}, HomeReadonly{})
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
	base := SeatbeltProfile("/Users/Shared/proj", "", nil, HomeReadonly{})
	for _, empty := range [][]string{nil, {}, {""}, {"   "}} {
		if got := SeatbeltProfile("/Users/Shared/proj", "", empty, HomeReadonly{}); got != base {
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
	}, HomeReadonly{})
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
	p := SeatbeltProfile("/Users/Shared/proj", "", []string{`a"b\c`}, HomeReadonly{})
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

	plan := BuildRunPlan("/Users/Shared/proj", cfg, nil, []string{"bash"}, "/usr/local/bin/yolo", "", HomeOverlay{},
		HostContext{}, jsonx.NewOrderedMap(), nil, nil)

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
	plan := BuildRunPlan("/Users/Shared/proj", jsonx.NewOrderedMap(), nil, []string{"bash"}, "/usr/local/bin/yolo",
		"", HomeOverlay{}, HostContext{}, jsonx.NewOrderedMap(), nil, nil)
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

// readonlyWorkspace is a resolved temp workspace, because BuildRunPlan resolves the one it is
// given and the profile names what it resolved (on darwin t.TempDir() is under a symlink).
func readonlyWorkspace(t *testing.T) string {
	t.Helper()
	ws, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return ws
}

func readonlyPlan(ws string, entries ...any) RunPlan {
	cfg := jsonx.NewOrderedMap()
	if len(entries) > 0 {
		cfg.Set("workspace_readonly", entries)
	}
	return BuildRunPlan(ws, cfg, nil, []string{"bash"}, "/usr/local/bin/yolo", "", HomeOverlay{},
		HostContext{}, jsonx.NewOrderedMap(), nil, nil)
}

// TestBuildRunPlanLocksTheWorkspaceConfigWithWorkspaceReadonly is the macos-user half of the
// lock the container backends perform beside the declared entries
// (TestWorkspaceReadonlyLocksTheConfigFileTheLoaderReads, internal/cli/run): any entry locks
// the config file the loader reads, under the name it reads it under, so a session cannot
// switch its own protection off. Through BuildRunPlan, so it fails with the call site
// reverted to the bare entry list.
func TestBuildRunPlanLocksTheWorkspaceConfigWithWorkspaceReadonly(t *testing.T) {
	for _, name := range []string{"yolo-jail.jsonc", "yolo-jail.json"} {
		t.Run(name, func(t *testing.T) {
			ws := readonlyWorkspace(t)
			if err := os.WriteFile(filepath.Join(ws, name), []byte("{}"), 0o644); err != nil {
				t.Fatal(err)
			}
			plan := readonlyPlan(ws, "vendored")
			want := "(subpath " + sbplStr(filepath.Join(ws, name)) + ")"
			if !strings.Contains(plan.Seatbelt, want) {
				t.Errorf("workspace_readonly did not lock the config the launch reads (want %q)\n%s",
					want, plan.Seatbelt)
			}
			// Inside the readonly deny block, not anywhere in the text.
			block := plan.Seatbelt[strings.Index(plan.Seatbelt, "#seatbelt-test-id:workspace-readonly-deny#"):]
			if !strings.Contains(block[:strings.Index(block, "))\n")+3], want) {
				t.Errorf("the config is named outside the workspace_readonly deny\n%s", plan.Seatbelt)
			}
		})
	}
}

// TestBuildRunPlanLeavesTheConfigWritableWithoutWorkspaceReadonly: the lock rides the key, as
// on the container backends. A workspace declaring no entry keeps an editable config, and one
// declaring entries but no config file gets only its entries.
func TestBuildRunPlanLeavesTheConfigWritableWithoutWorkspaceReadonly(t *testing.T) {
	ws := readonlyWorkspace(t)
	if err := os.WriteFile(filepath.Join(ws, "yolo-jail.jsonc"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if p := readonlyPlan(ws).Seatbelt; strings.Contains(p, "yolo-jail.json") {
		t.Errorf("the config was locked with no workspace_readonly entry\n%s", p)
	}
	bare := readonlyWorkspace(t)
	p := readonlyPlan(bare, "vendored").Seatbelt
	if strings.Contains(p, "yolo-jail.json") {
		t.Errorf("a config file that does not exist was named in the profile\n%s", p)
	}
	if !strings.Contains(p, "(subpath "+sbplStr(filepath.Join(bare, "vendored"))+")") {
		t.Errorf("the declared entry is missing\n%s", p)
	}
}

// TestBuildRunPlanLocksASymlinkedConfigsTarget: the kernel resolves a write through a link
// before the policy is consulted, so the link's name alone would not stop `>` on it.
func TestBuildRunPlanLocksASymlinkedConfigsTarget(t *testing.T) {
	ws := readonlyWorkspace(t)
	if err := os.MkdirAll(filepath.Join(ws, "cfg"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, "cfg", "jail.jsonc"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("cfg/jail.jsonc", filepath.Join(ws, "yolo-jail.jsonc")); err != nil {
		t.Fatal(err)
	}
	p := readonlyPlan(ws, "vendored").Seatbelt
	for _, path := range []string{filepath.Join(ws, "yolo-jail.jsonc"), filepath.Join(ws, "cfg", "jail.jsonc")} {
		if want := "(subpath " + sbplStr(path) + ")"; !strings.Contains(p, want) {
			t.Errorf("missing %q\n%s", want, p)
		}
	}
}

// TestBuildRunPlanLocksASymlinkedConfigsTargetOutsideTheWorkspace: a target outside the
// workspace is NOT outside the write allow when it sits under a writable root — the fixture's
// TMPDIR is /tmp on Linux and /private/var/folders on macOS, both in profileWritableRoots, which
// is exactly the case. The container backends bind the RESOLVED file `:ro`, locking its content
// wherever it lives, so this backend denies the target by its physical path, inside the same
// workspace_readonly deny form. Through BuildRunPlan, so it fails with the target dropped at the
// call site.
func TestBuildRunPlanLocksASymlinkedConfigsTargetOutsideTheWorkspace(t *testing.T) {
	ws := readonlyWorkspace(t)
	other := readonlyWorkspace(t)
	target := filepath.Join(other, "elsewhere.jsonc")
	if err := os.WriteFile(target, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(ws, "yolo-jail.jsonc")); err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(profileWritableRoots, func(root string) bool {
		return target == root || strings.HasPrefix(target, root+"/")
	}) {
		t.Logf("the fixture's target %s is under no writable root on this machine; the deny is "+
			"asserted regardless, since it can only narrow the profile", target)
	}
	p := readonlyPlan(ws, "vendored").Seatbelt
	idx := strings.Index(p, "#seatbelt-test-id:workspace-readonly-deny#")
	if idx < 0 {
		t.Fatalf("no workspace_readonly deny form\n%s", p)
	}
	block := p[idx:]
	block = block[:strings.Index(block, "))\n")+3]
	for _, want := range []string{
		"(literal " + sbplStr(target) + ")",
		"(subpath " + sbplStr(filepath.Join(ws, "yolo-jail.jsonc")) + ")",
		"(subpath " + sbplStr(filepath.Join(ws, "vendored")) + ")",
	} {
		if !strings.Contains(block, want) {
			t.Errorf("the workspace_readonly deny form lacks %q, so the session can rewrite the "+
				"config its next launch reads\n%s", want, p)
		}
	}
	// Without workspace_readonly the target is not named at all: the lock rides the key.
	if p := readonlyPlan(ws).Seatbelt; strings.Contains(p, "elsewhere.jsonc") {
		t.Errorf("the target was locked with no workspace_readonly entry\n%s", p)
	}
}

// TestReadonlyDeniesKeepsRefusingAbsoluteUserEntries: the yolo-derived targets are a list of
// their own, so an absolute path in the USER's workspace_readonly is still dropped rather than
// rendered as a deny outside the workspace, and a target that is not absolute renders nothing.
func TestReadonlyDeniesKeepsRefusingAbsoluteUserEntries(t *testing.T) {
	got := readonlyDenies("/Users/Shared/proj", []string{"/etc/hosts", "vendored"}, []string{"relative.jsonc", "/private/tmp/x/cfg.jsonc"})
	if strings.Contains(got, "/etc/hosts") {
		t.Errorf("an absolute user entry reached the deny form\n%s", got)
	}
	if strings.Contains(got, "relative.jsonc") {
		t.Errorf("a relative config target reached the deny form\n%s", got)
	}
	for _, want := range []string{`(subpath "/Users/Shared/proj/vendored")`, `(literal "/private/tmp/x/cfg.jsonc")`} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q\n%s", want, got)
		}
	}
	if readonlyDenies("/Users/Shared/proj", nil, nil) != "" {
		t.Error("no entry and no target must render nothing, so the profile stays byte-identical")
	}
}

// A config that already lists the file is not given it twice.
func TestWorkspaceReadonlyRelsDoesNotRepeatADeclaredConfig(t *testing.T) {
	ws := readonlyWorkspace(t)
	if err := os.WriteFile(filepath.Join(ws, "yolo-jail.jsonc"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := jsonx.NewOrderedMap()
	cfg.Set("workspace_readonly", []any{"yolo-jail.jsonc", "vendored"})
	got, targets := workspaceReadonlyRels(ws, cfg)
	if strings.Join(got, ",") != "yolo-jail.jsonc,vendored" || len(targets) != 0 {
		t.Errorf("workspaceReadonlyRels = %v, %v", got, targets)
	}
}
