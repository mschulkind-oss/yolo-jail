package config

// agentupdateswiring_test.go is the CENSUS for `agent_updates`'s three env-wiring sites.
//
// WHY A CENSUS AND NOT THREE BEHAVIOURAL CELLS. The value travels host→jail as one env
// var, and each backend composes its own env list by hand. Two of the three sites cannot be
// exercised from a unit test at all — `yolo check`'s preflight builds a temp home and runs
// the real generators, and macos-user's plan is consumed by a `sudo`+`sandbox-exec` argv —
// so the failure mode is not "the wiring is wrong" but "one of them was never written". The
// plan's trap 8 names both misses and what each costs: miss `yolo check` and the preflight
// renders launchers under a policy the boot does not have; miss macos-user and the one
// backend with no image to hide it keeps the old behaviour.
//
// So this reads the SOURCE, the way launcherdir_test.go's PATH-authority cell does. It is a
// weak test of behaviour and a strong test of coverage, which is the property at risk.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAgentUpdatesReachesEveryBackend asserts each site names the wire constant AND the
// reader that produces its value. Naming both is what distinguishes a real wiring from a
// variable set to a literal or to something else's value.
func TestAgentUpdatesReachesEveryBackend(t *testing.T) {
	root := repoRootForWiring(t)
	for _, site := range []struct {
		path, what string
	}{
		{"internal/cli/run/assemble.go", "the podman/container run pipeline's -e list"},
		{"internal/cli/check/entrypoint.go", "`yolo check`'s preflight env — miss it and the " +
			"dry run generates launchers under a policy the boot does not have"},
		{"internal/macosuser/runplan.go", "macos-user's bootstrap env — miss it and the one " +
			"backend with no image to hide it keeps the old behaviour"},
	} {
		data, err := os.ReadFile(filepath.Join(root, site.path))
		if err != nil {
			t.Fatalf("reading %s: %v", site.path, err)
		}
		body := string(data)
		if !strings.Contains(body, "entrypoint.AgentUpdatesEnv") {
			t.Errorf("%s does not name entrypoint.AgentUpdatesEnv — %s", site.path, site.what)
		}
		if !strings.Contains(body, "config.AgentUpdatesWire()") &&
			!strings.Contains(body, "AgentUpdatesWire()") {
			t.Errorf("%s names the variable but not AgentUpdatesWire() — the value has to come "+
				"from the USER-scope reader, or a workspace config decides it", site.path)
		}
	}
}

// repoRootForWiring walks up for the directory holding go.mod.
func repoRootForWiring(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the working directory")
		}
		dir = parent
	}
}
