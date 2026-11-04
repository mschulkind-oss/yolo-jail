package integration

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// TestTheSuiteClearsAnOuterJailsSetupSelection drives applySuiteEnv, TestMain's environment
// step: inside a launched session every setup-selection variable an outer jail exported is gone
// before any launch reads os.Environ, and on a host the removed two are gone while a job's own
// YOLO_SETUP stays (the macOS workflows select podman-self with it).
func TestTheSuiteClearsAnOuterJailsSetupSelection(t *testing.T) {
	for _, inJail := range []bool{true, false} {
		t.Setenv(autoReapersOffEnv, os.Getenv(autoReapersOffEnv))
		t.Setenv("YOLO_RUNTIME", "podman")
		t.Setenv("YOLO_CONFINEMENT", "jail")
		t.Setenv("YOLO_SETUP", "podman-self")
		if inJail {
			t.Setenv("YOLO_VERSION", "9.9.9-test")
		} else {
			t.Setenv("YOLO_VERSION", "")
		}
		applySuiteEnv()
		env := launchEnvironment(runConfig{})
		for _, kv := range env {
			name, _, _ := strings.Cut(kv, "=")
			if name == "YOLO_RUNTIME" || name == "YOLO_CONFINEMENT" {
				t.Errorf("inJail=%v: a launch inherits the removed %s", inJail, kv)
			}
		}
		if got := slices.Contains(env, "YOLO_SETUP=podman-self"); got == inJail {
			t.Errorf("inJail=%v: a launch inherits YOLO_SETUP=%v, want it only on a host", inJail, got)
		}
	}
}

// Exercise TestMain itself: a helper-only test would stay green if runSuite
// stopped calling the common matched-bundle builder. The Python probe stops
// before any compilation or jail setup, keeping this regression in the short gate.
func TestMatchedHarnessBuildDoesNotRequireJust(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal("the matched-bundle build requires python3; provide it on PATH to run this regression")
	}
	pythonPath, err := exec.Command(python, "-c", "import sys; print(sys.executable)").Output()
	if err != nil {
		t.Fatal(err)
	}
	python = strings.TrimSpace(string(pythonPath))
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	root, err := moduleRoot()
	if err != nil {
		t.Fatal(err)
	}
	dir := resolvedTempDir(t)
	capture := filepath.Join(dir, "build-call.json")
	// Only python3 is on this child's PATH: the recipe runner is absent even
	// on developer machines that have just installed. Use the real interpreter
	// to record the build call's arguments and environment without building.
	probe := "#!" + python + "\n" + `import json, os, sys
with open(os.environ["MATCHED_BUILD_CAPTURE"], "w") as out:
    json.dump({"args": sys.argv[1:], "cwd": os.getcwd(), "home": os.environ["HOME"],
               "selector": os.environ.get("YOLO_REPO_ROOT")}, out)
print("matched build probe reached", flush=True)
sys.exit(17)
`
	if err := os.WriteFile(filepath.Join(dir, "python3"), []byte(probe), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, self, "-test.run=^$")
	cmd.Dir = dir // TestMain must use the source root, not the caller's cwd.
	var env []string
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if !slices.Contains([]string{"PATH", "HOME", "YOLO_REPO_ROOT", "MATCHED_BUILD_CAPTURE"}, key) {
			env = append(env, entry)
		}
	}
	cmd.Env = append(env,
		"PATH="+dir, "HOME="+dir, "YOLO_REPO_ROOT=/obsolete/checkout", "MATCHED_BUILD_CAPTURE="+capture)
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "building matched yolo bundle: exit status 17") ||
		!strings.Contains(string(out), "matched build probe reached") {
		t.Fatalf("TestMain must reach the common builder without just, then report the probe's failure: %v\n%s", err, out)
	}
	data, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	var call struct {
		Args     []string `json:"args"`
		Cwd      string   `json:"cwd"`
		Home     string   `json:"home"`
		Selector *string  `json:"selector"`
	}
	if err := json.Unmarshal(data, &call); err != nil {
		t.Fatal(err)
	}
	wantArgs := []string{"scripts/matched-bundle.py", "build-dev", "--repo", root, "--home", dir}
	if !slices.Equal(call.Args, wantArgs) || call.Cwd != root || call.Home != dir || call.Selector != nil {
		t.Fatalf("build call = %+v, want args %v, source cwd, inherited HOME, and no obsolete selector", call, wantArgs)
	}
}

// The suite's own YOLO_TEST_* dials (set by the workflows: YOLO_TEST_MACOS_USER and friends) are
// read by this test process, never by yolo. Inherited by a launch, each draws yolo's unknown-
// variable warning, whose `YOLO_RUNTIME=podman yolo` hint then satisfied or broke assertions about
// what a REFUSAL offers (TestMacosUserPatchedExtensionConflictRefusesAndSkipStartsWithoutIt failed
// on the macos-user job that way). A call's own withEnv pair still reaches the child.
func TestLaunchEnvironmentDropsTheSuitesOwnDials(t *testing.T) {
	t.Setenv("YOLO_TEST_MACOS_USER", "1")
	t.Setenv("YOLO_TEST_MACOS_USER_RELOCATION_VOLUMES", "1")
	t.Setenv("YOLO_NO_BANNER", "1")
	env := launchEnvironment(runConfig{env: []string{"YOLO_TEST_EXPLICIT=1"}})
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(name, "YOLO_TEST_") && name != "YOLO_TEST_EXPLICIT" {
			t.Errorf("launch environment inherits the suite's dial %s", kv)
		}
	}
	for _, want := range []string{"YOLO_NO_BANNER=1", "YOLO_TEST_EXPLICIT=1"} {
		if !slices.Contains(env, want) {
			t.Errorf("launch environment lacks %s, which yolo reads or the call asked for", want)
		}
	}
}

// An older yolo's export that current yolo does not recognize (YOLO_JAIL=1, from an old host notch)
// is not handed to a launch either: it drew the same unknown-variable warning and broke the same
// refusal's assertions on a Mac. A call's own withEnv pair naming it still reaches the child.
func TestLaunchEnvironmentDropsAnOlderYolosExports(t *testing.T) {
	t.Setenv("YOLO_JAIL", "1")
	if got := launchEnvValue(launchEnvironment(runConfig{}), "YOLO_JAIL"); got != "<unset>" {
		t.Errorf("launch environment inherits YOLO_JAIL=%s from an older yolo", got)
	}
	if got := launchEnvValue(launchEnvironment(runConfig{env: []string{"YOLO_JAIL=asked"}}), "YOLO_JAIL"); got != "asked" {
		t.Errorf("a call's own YOLO_JAIL pair did not reach the launch: %q", got)
	}
}

func TestMatchedHarnessUsesAdjacentBundleAndScrubsObsoleteSelector(t *testing.T) {
	launcher := filepath.Join("/tmp", "dev generation", "bin", "yolo")
	wantBundle := filepath.Join("/tmp", "dev generation", "share", "yolo-jail")
	if got := matchedBundleFlakeRoot(launcher); got != wantBundle {
		t.Fatalf("matchedBundleFlakeRoot(%q) = %q, want %q", launcher, got, wantBundle)
	}
	buildOutput := "build-go: yolo -> dist-go/linux-amd64/yolo\n" +
		"stage-source-bundle: staged 100M bundle\n" + launcher + "\n"
	if got := matchedLauncherFromBuildOutput(buildOutput); got != launcher {
		t.Fatalf("matchedLauncherFromBuildOutput() = %q, want final absolute launcher path %q", got, launcher)
	}

	env := []string{"PATH=/bin", "YOLO_REPO_ROOT=/other/checkout", "HOME=/home/test"}
	got := envWithoutRepoRoot(env)
	if !slices.Equal(got, []string{"PATH=/bin", "HOME=/home/test"}) {
		t.Fatalf("envWithoutRepoRoot() = %v, want selector removed without changing other child environment", got)
	}
	if !slices.Contains(env, "YOLO_REPO_ROOT=/other/checkout") {
		t.Fatal("child sanitation mutated the caller's environment slice")
	}
}

// TestEveryLaunchTheSuiteMakesHasTheAutomaticReapersOff: TestMain turns yolo's automatic reapers
// off for every launch this suite makes (applySuiteEnv), and a test that exercises one turns them
// back on for its own launch (withAutoReapers). The launches are a stand-in yolo that prints what
// it was handed, run through runCommand, the helper every run* helper funnels through.
//
// The first assertion is vacuous where the calling shell already sets the variable, as it holds
// there anyway; CI's shell does not.
func TestEveryLaunchTheSuiteMakesHasTheAutomaticReapersOff(t *testing.T) {
	if got := os.Getenv(autoReapersOffEnv); got != "1" {
		t.Errorf("this test process has %s=%q, want 1: TestMain no longer turns the automatic "+
			"reapers off, so a launch here reaps images and store outputs through whatever "+
			"runtime and nix daemon the machine has, while other tests use them", autoReapersOffEnv, got)
	}
	dir := resolvedTempDir(t)
	fake := filepath.Join(dir, "fake-yolo")
	script := "#!/bin/sh\necho \"REAPERS_OFF=${" + autoReapersOffEnv + "-unset}.\"\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	saved := yoloBin
	yoloBin = fake
	t.Cleanup(func() { yoloBin = saved })

	if r := runCommand(t, dir, []string{"run", "--", "true"}); !strings.Contains(r.stdout, "REAPERS_OFF=1.") {
		t.Errorf("a launch did not have the automatic reapers off: %q", r.stdout)
	}
	isolateReaperStore(t) // runLaunch refuses a reaper launch without one (reaperstore_test.go)
	if r := runCommand(t, dir, []string{"run", "--", "true"}, withAutoReapers()); !strings.Contains(r.stdout, "REAPERS_OFF=.") {
		t.Errorf("withAutoReapers did not turn the automatic reapers back on: %q", r.stdout)
	}
}
