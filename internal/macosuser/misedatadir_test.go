package macosuser

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// misedatadir_test.go pins the machine tier of the mise store on this backend
// (docs/design/macos-user-home-tiers.md §5, and macos-user-provisioning.md's OQ-P3).
//
// WHY IT IS A TEST AND NOT A COMMENT. The failure it guards is SILENT: unset,
// MISE_DATA_DIR resolves to $HOME/.local/share/mise, and under the home-tier layout
// ~/.local is a symlink into <workspace>/.yolo/home — so a deleted line here does not
// break a launch, it moves one machine-wide tool store into every workspace's own
// sidecar, where the container backends keep nothing. Nothing else would notice.

// The three places the value has to agree: the PATH the agent gets, the env the agent
// gets, and the env the bootstrap generates its config from. A value in one and not the
// others is a shim whose install is somewhere else.
func TestMiseDataDirIsMachineWideEverywhereItAppears(t *testing.T) {
	home := SandboxHome()
	want := SandboxMiseData(home)
	assertOutsideTheWorkspaceTier(t, home, want)

	// 1. PATH: the shims dir must be the one inside that store.
	path := SandboxPath(home, nil)
	if !strings.Contains(path, want+"/shims") {
		t.Errorf("SandboxPath does not carry %s/shims:\n%s", want, path)
	}
	if strings.Contains(path, "/.local/share/mise") {
		t.Errorf("SandboxPath still carries the per-workspace default mise shims:\n%s", path)
	}

	// 2. The launch env, as it is actually baked onto the argv.
	launch := strings.Join(LaunchArgv([]string{"claude"}, "/var/yolo-jail/p.sb",
		jsonx.NewOrderedMap(), "/Users/Shared/yolo/proj", "", "", nil), " ")
	if !strings.Contains(launch, "MISE_DATA_DIR="+want) {
		t.Errorf("the launch argv does not set MISE_DATA_DIR=%s:\n%s", want, launch)
	}

	// 3. The bootstrap env, likewise — this is what entrypoint.NewEnv reads, and an
	// absent value there is exactly the silent default.
	plan := BuildRunPlan("/Users/Shared/yolo/proj", jsonx.NewOrderedMap(),
		[]string{"claude"}, []string{"claude"}, "/usr/local/bin/yolo", "", "",
		jsonx.NewOrderedMap(), nil, nil)
	if !containsArg(plan.BootstrapArgv, "MISE_DATA_DIR="+want) {
		t.Errorf("the bootstrap argv does not set MISE_DATA_DIR=%s:\n%v", want, plan.BootstrapArgv)
	}
}

// A caller's own MISE_DATA_DIR is dropped, like the HOME/USER/SHELL/PATH quartet: which
// tier a store belongs to is the backend's decision, and the value a caller could set is
// the per-workspace default this exists to replace.
func TestMiseDataDirIsNotOverridableFromTheLaunchEnv(t *testing.T) {
	env := jsonx.NewOrderedMap()
	env.Set("MISE_DATA_DIR", "/tmp/someone-elses-store")
	launch := strings.Join(LaunchArgv([]string{"claude"}, "/var/yolo-jail/p.sb", env,
		"/Users/Shared/yolo/proj", "", "", nil), " ")
	if strings.Contains(launch, "/tmp/someone-elses-store") {
		t.Errorf("a caller overrode MISE_DATA_DIR:\n%s", launch)
	}
}

// The capture driver bootstraps a THROWAWAY staging home, and its store has to follow
// that home rather than the account's — a capture that installed into the machine-wide
// store would file the machine's existing files as the installer's delta.
func TestMiseDataDirFollowsTheHomeItIsGiven(t *testing.T) {
	staging := "/Users/Shared/yolo-captures/claude/home"
	if got := SandboxMiseData(staging); !strings.HasPrefix(got, staging+"/") {
		t.Errorf("SandboxMiseData(%q) = %q, want it under that home", staging, got)
	}
}

// assertOutsideTheWorkspaceTier fails if `path` resolves through any link the home-tier
// layout lays — i.e. if it is per-workspace.
//
// ⚠ THIS REPLACED AN ASSERTION ABOUT A SENTENCE. The guard above used to read
// `strings.Contains(want, "/.local/")`, which pins the ~/.local spelling the comment
// happens to mention rather than the property the comment is ABOUT. Measured 2026-09-12:
// changing SandboxMiseData to ~/.config/mise-store, or to ~/go/mise, put the machine-wide
// store back inside the per-workspace sidecar with `go test -short ./...` byte-identically
// green, because `.config` and `go` are links in DeriveDarwinHomeLayout too. That is the
// class AGENTS.md's Testing section names — "the test asserts the SENTENCE a comment makes
// rather than the system" — so the question is asked of the layout itself, which is the
// only thing that knows which paths are per-workspace, and which grows new links without
// this file being edited.
func assertOutsideTheWorkspaceTier(t *testing.T, home, path string) {
	t.Helper()
	layout := entrypoint.DeriveDarwinHomeLayout(home, "/Users/Shared/yolo/proj/.yolo/home",
		packload.EmbeddedWritableDirs(), packload.EmbeddedSharedDirs())
	if len(layout.Links) == 0 {
		t.Fatal("the layout derived no links — this assertion would pass vacuously")
	}
	for _, l := range layout.Links {
		if path == l.Path || strings.HasPrefix(path, l.Path+string(filepath.Separator)) {
			t.Fatalf("%q resolves through the workspace-tier link %s -> %s, so it is "+
				"PER-WORKSPACE — which no other backend is, and which is the silent "+
				"failure this file exists to prevent", path, l.Path, l.Target)
		}
	}
}
