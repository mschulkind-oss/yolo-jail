package macosuser

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
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
	if strings.Contains(want, "/.local/") {
		t.Fatalf("the mise data dir %q is under ~/.local, which the layout symlinks into the "+
			"workspace sidecar — it would be per-workspace, which no other backend is", want)
	}

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
