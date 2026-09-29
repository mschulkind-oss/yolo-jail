package ghbroker

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
)

// Which host gh the broker runs. The launch spawns the broker from the workspace, which the
// agent writes, so a gh the agent planted there must never be the one it finds: the broker
// runs it on the host as the user before any call arrives (`gh --version`, then `gh auth
// token`). None of these tests runs a real gh: every PATH here names only fixture dirs.

// plantedGH writes a gh into dir, the way an agent in the jail could, that leaves a marker
// when it runs. The marker is the observation: the planted program ran on the host.
func plantedGH(t *testing.T, dir string) (marker string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	marker = filepath.Join(t.TempDir(), "planted-gh-ran")
	script := "#!/bin/sh\ntouch '" + marker + "'\necho 'gh version 2.101.0 (planted)'\n"
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return marker
}

func assertNotRun(t *testing.T, marker string) {
	t.Helper()
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the planted gh ran on the host")
	}
}

func envOf(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

// A relative PATH entry (`./bin`, `bin`, `.`) resolves against the cwd, which at spawn is
// the workspace. It is skipped, never resolved, as exec.LookPath refuses such a result.
func TestTheBrokerSkipsARelativePathEntry(t *testing.T) {
	ws := resolvedDir(t)
	marker := plantedGH(t, filepath.Join(ws, "bin"))
	plantedGH(t, ws)
	t.Chdir(ws)
	empty := resolvedDir(t)
	if _, err := lookPathIn("./bin:bin:.:"+empty, "gh"); err == nil {
		t.Fatal("lookPathIn resolved gh through a relative PATH entry")
	}
	_, err := NewRunner(RunnerOptions{RunDir: filepath.Join(resolvedDir(t), "run"),
		Getenv: envOf(map[string]string{"PATH": "./bin:bin:.:" + empty, "HOME": resolvedDir(t)})})
	if err != ErrNoGH {
		t.Fatalf("err %v, want ErrNoGH", err)
	}
	assertNotRun(t, marker)
}

// An ABSOLUTE PATH entry inside the workspace (direnv's `PATH_add bin`) is refused by the
// placement rule, as found and through a symlink out of it.
func TestTheBrokerRefusesAGHInsideTheWorkspace(t *testing.T) {
	ws := resolvedDir(t)
	marker := plantedGH(t, filepath.Join(ws, "bin"))
	_, err := NewRunner(RunnerOptions{RunDir: filepath.Join(resolvedDir(t), "run"),
		Getenv: envOf(map[string]string{"PATH": filepath.Join(ws, "bin"), "HOME": resolvedDir(t)}),
		Refuse: placementRefusal(ws)})
	var refused *RefusedGHError
	if !errors.As(err, &refused) || !strings.Contains(refused.Why, "inside the workspace") {
		t.Fatalf("err %v, want the placement refusal", err)
	}
	assertNotRun(t, marker)

	// A gh outside the workspace that is a symlink into it is the same program.
	outside := resolvedDir(t)
	if err := os.Symlink(filepath.Join(ws, "bin", "gh"), filepath.Join(outside, "gh")); err != nil {
		t.Fatal(err)
	}
	_, err = NewRunner(RunnerOptions{RunDir: filepath.Join(resolvedDir(t), "run"),
		Getenv: envOf(map[string]string{"PATH": outside, "HOME": resolvedDir(t)}),
		Refuse: placementRefusal(ws)})
	if !errors.As(err, &refused) {
		t.Fatalf("a gh linked into the workspace: err %v, want the placement refusal", err)
	}
	assertNotRun(t, marker)
}

// The daemon's call site: newBroker hands the runner the refusal for the scope file's
// workspace, and a refused gh answers every call 69 with the reason, rather than running.
func TestTheDaemonRefusesAGHInsideItsWorkspace(t *testing.T) {
	ws := resolvedDir(t)
	marker := plantedGH(t, filepath.Join(ws, "bin"))
	t.Setenv("HOME", resolvedDir(t))
	t.Setenv("PATH", filepath.Join(ws, "bin"))
	var log bytes.Buffer
	b, cleanup := newBroker(brokerscope.File{Workspace: ws, Repos: []string{"o/r"}}, ws, &log)
	defer cleanup()
	assertNotRun(t, marker)
	if b.runner != nil {
		t.Fatalf("the broker took the planted gh: %s", b.runner.GhPath)
	}
	var errOut bytes.Buffer
	code := b.Serve(Request{Argv: []string{"pr", "view", "1", "-R", "o/r"}}, "jail-1",
		func([]byte) {}, func(p []byte) { errOut.Write(p) })
	if code != ExitUnavailable || !strings.Contains(errOut.String(), "will not run the host gh") {
		t.Fatalf("code %d err %q", code, errOut.String())
	}
	assertNotRun(t, marker)
}

// `yolo check`'s self-check runs from the workspace too.
func TestTheSelfCheckRefusesAGHInsideTheWorkspace(t *testing.T) {
	ws := resolvedDir(t)
	marker := plantedGH(t, filepath.Join(ws, "bin"))
	t.Setenv("HOME", resolvedDir(t))
	t.Setenv("PATH", filepath.Join(ws, "bin"))
	t.Chdir(ws)
	var out bytes.Buffer
	if code := SelfCheck(&out); code != 1 || !strings.Contains(out.String(), "will not run the host gh") {
		t.Fatalf("code %d out %q", code, out.String())
	}
	assertNotRun(t, marker)
}

// The home is never read as a workspace: `yolo check` run from it must not refuse every gh
// installed under it.
func TestThePlacementRefusalIgnoresTheHome(t *testing.T) {
	home := resolvedDir(t)
	t.Setenv("HOME", home)
	if why := placementRefusal(home)(filepath.Join(home, ".local", "bin", "gh")); why != "" {
		t.Fatalf("a gh under the home read as the agent's: %s", why)
	}
	ws := filepath.Join(home, "code", "app")
	if why := placementRefusal(ws)(filepath.Join(ws, "bin", "gh")); why == "" {
		t.Fatal("a gh inside a workspace under the home was not refused")
	}
}
