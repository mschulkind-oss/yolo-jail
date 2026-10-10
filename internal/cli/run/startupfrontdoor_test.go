package run

// startupfrontdoor_test.go pins docs/reference/host-service-startup-diagnostics.md#typed-startup-outcomes
// at the launch FRONT DOORS rather than at the service starter: a host service's
// typed startup refusal reaches what a user sees through each whole launch, Run() driving its
// real keeper (in-process, as keeper_test.go's TestMain installs it), with its frames relayed to
// the launching terminal. The two refusals each arm owes are covered:
//
//   - a settings PREFLIGHT refusal (the pack's settings_check), which the launching terminal
//     prints before any keeper, image or capture work starts; and
//   - a COOPERATIVE daemon refusal sent over the attempt channel, which the keeper receives at
//     its spawn and relays as the launch's refusal before the container or sandboxed command.
//
// Deleting the forwarding call at any of these sites fails its test here: the keeper's
// `refused` print (keeper.go), the container arm's and the macos-user arm's preflight refusal
// (run.go), startPlannedLoopholes' hand-back of o.startupRefusal (packloopholes.go), and
// HostDoorways.Start's hand-back after its services start (hostdoorways.go).

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/image"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/launchservice"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	yoloruntime "github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

const frontDoorFixtureName = "front-door-fixture"

// The per-jail fixture's words (TestPerJailReasonChild), which only the attempt channel carries.
const (
	frontDoorReason = "fixture settings were refused"
	frontDoorRemedy = "correct the user settings and retry"
	// frontDoorStaleLog is a contradictory line from an earlier attempt, planted in the shared
	// service log: nothing a launch says may come from it.
	frontDoorStaleLog = "OLD CONTRADICTORY FAILURE from an unrelated attempt"
)

// cooperativeRefusalManifest is a per-jail daemon that sends a configuration refusal over the
// attempt channel and exits before readiness.
func cooperativeRefusalManifest() string {
	return fmt.Sprintf(`{
  "name": %q,
  "default_enabled": true,
  "host_daemon": {
    "cmd": [%q, "-test.run=^TestPerJailReasonChild$", "{socket}"],
    "startup_reason": true,
    "publishes": "socket"
  }
}`, frontDoorFixtureName, os.Args[0])
}

// preflightRefusalManifest is a per-jail daemon whose settings_check refuses
// (settingsRefusalChildMain); its daemon must never run, and its marker proves it.
func preflightRefusalManifest(daemonMarker string) string {
	return fmt.Sprintf(`{
  "name": %q,
  "default_enabled": true,
  "settings": {"profile": {"type": "string", "scope": "user", "default": "default-profile"}},
  "host_daemon": {
    "cmd": [%q, "-settings-snapshot-daemon", "{socket}", "{settings}", %q],
    "settings_check": [%q, "-settings-refusal-child", "{settings}"],
    "publishes": "socket"
  }
}`, frontDoorFixtureName, os.Args[0], daemonMarker, os.Args[0])
}

// frontDoorHome isolates a whole launch with manifest as the conventional local pack's one
// loophole, and plants the stale log line.
func frontDoorHome(t *testing.T, manifest string) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("spawns host processes and binds AF_UNIX sockets")
	}
	home := packHome(t)
	writeLocalLoopholePack(t, home, frontDoorFixtureName, manifest)
	writeUserPacks(t, home, `[]`)
	logDir := filepath.Join(paths.GlobalStorage(), "logs")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(logDir, "host-service-"+frontDoorFixtureName+".log"),
		[]byte(frontDoorStaleLog+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// frontDoorContainerRun is one fresh podman launch to its container's start: what it printed,
// its status, whether the runtime was asked to run the container, and whether the image step
// (the first expensive work after the preflight) was reached.
type frontDoorContainerRun struct {
	out         string
	rc          int
	ranJail     bool
	loadedImage bool
}

func frontDoorContainerLaunch(t *testing.T) frontDoorContainerRun {
	t.Helper()
	ws := t.TempDir()
	cname := yoloruntime.FromWorkspace(ws)
	bin, ran := t.TempDir(), filepath.Join(t.TempDir(), "ran")
	script := "#!/bin/sh\nif [ \"$1\" = run ]; then : > '" + ran + "'; fi\nexit 3\n"
	if err := os.WriteFile(filepath.Join(bin, "podman"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":/bin:/usr/bin")
	o := dispatchOptions(t, ws, "podman", new(bytes.Buffer), new(bytes.Buffer), nil)
	var stdout, stderr lockedBuffer
	o.Stdout, o.Stderr = &stdout, &stderr
	o.ServiceReadyTimeout = 2 * time.Second
	repo, _ := o.RepoRoot()
	o.PathExists = func(p string) bool {
		return p == filepath.Join(prebuiltBinDir(repo.Root), "yolo-entrypoint")
	}
	o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
		return ExecResult{Ran: true, RC: 0} // `ps`: no container of the name is running
	}
	var result frontDoorContainerRun
	o.autoLoad = func(image.AutoLoadOptions) image.LoadResult {
		result.loadedImage = true
		return image.LoadResult{OK: true, Ref: goldenImageRef}
	}
	t.Cleanup(func() { _ = os.RemoveAll(hostServiceSocketsDir(cname, false)) })
	done := make(chan int, 1)
	go func() { done <- Run(*o) }()
	select {
	case result.rc = <-done:
	case <-time.After(sealedLaunchBound):
		t.Fatalf("the launch had not returned after %s\nstdout:\n%s\nstderr:\n%s",
			sealedLaunchBound, stdout.String(), stderr.String())
	}
	_, err := os.Stat(ran)
	result.ranJail = err == nil
	result.out = stdout.String() + stderr.String()
	return result
}

// frontDoorMacosUserLaunch is one fresh macos-user launch against a stub backend: what it
// printed, its status, and whether the sandboxed command was started.
func frontDoorMacosUserLaunch(t *testing.T) (out string, rc int, started bool) {
	t.Helper()
	ws := t.TempDir()
	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, ws, "macos-user", &stdout, &stderr, nil)
	o.ServiceReadyTimeout = 2 * time.Second
	o.MacosUserRun = func(_ *jsonx.OrderedMap, _ string, _, _ []string, _, _ string, _ macosuser.HomeOverlay,
		_ macosuser.HostContext, _ bool, _ *jsonx.OrderedMap, _ []packload.BlockedTool, _ macosuser.JailDaemons) int {
		started = true
		return 0
	}
	rc = Run(*o)
	return stdout.String() + stderr.String(), rc, started
}

// assertFrontDoorRefusal: the refusal names who refused, the service, the daemon's own cause and
// remedy and the check to run, and nothing from the shared log or a derived socket symptom.
func assertFrontDoorRefusal(t *testing.T, out, headline, reason, remedy string) {
	t.Helper()
	for _, want := range []string{headline, "'" + frontDoorFixtureName + "'", reason, remedy,
		"yolo check --no-build", "retry"} {
		if !strings.Contains(out, want) {
			t.Errorf("the launch output does not say %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, frontDoorStaleLog) {
		t.Errorf("a historical shared-log line reached the launch output:\n%s", out)
	}
	// A configuration refusal is the primary diagnostic; the readiness failure it caused is not
	// reported in its place (docs/reference/host-service-startup-diagnostics.md#typed-startup-outcomes).
	for _, symptom := range []string{"did not bind", "cannot reach it"} {
		if strings.Contains(out, symptom) {
			t.Errorf("the derived symptom %q was printed for a configuration refusal:\n%s", symptom, out)
		}
	}
}

// TestAFreshContainerLaunchRelaysADaemonRefusalThroughItsKeeper: the keeper spawns the daemon,
// reads its refusal from the attempt channel, and the launching terminal prints it as the
// launch's refusal before any container starts.
func TestAFreshContainerLaunchRelaysADaemonRefusalThroughItsKeeper(t *testing.T) {
	frontDoorHome(t, cooperativeRefusalManifest())
	t.Setenv(perJailReasonChildModeEnv, "exit")
	t.Setenv(perJailReasonChildDelayEnv, "")
	t.Setenv(perJailReasonChildClassEnv, "configuration")
	r := frontDoorContainerLaunch(t)
	if r.ranJail {
		t.Errorf("the launch started its container past a refused host service:\n%s", r.out)
	}
	if r.rc != 1 {
		t.Errorf("Run() = %d, want 1 for a refused launch:\n%s", r.rc, r.out)
	}
	if !strings.Contains(r.out, "keeper: started") {
		t.Fatalf("the launch never reached its keeper, so this proves nothing about its relay:\n%s", r.out)
	}
	assertFrontDoorRefusal(t, r.out, "Refusing this launch", frontDoorReason, frontDoorRemedy)
}

// TestAFreshContainerLaunchKeepsANonConfigurationRefusalAWarning is its severity control: a
// cooperative refusal of another class is printed first, as the daemon's own cause, and the
// launch keeps the established reachability severity — it is not refused for it (docs/reference/host-service-startup-diagnostics.md#typed-startup-outcomes).
func TestAFreshContainerLaunchKeepsANonConfigurationRefusalAWarning(t *testing.T) {
	frontDoorHome(t, cooperativeRefusalManifest())
	t.Setenv(perJailReasonChildModeEnv, "exit")
	t.Setenv(perJailReasonChildDelayEnv, "")
	t.Setenv(perJailReasonChildClassEnv, "dependency")
	r := frontDoorContainerLaunch(t)
	if strings.Contains(r.out, "Refusing this launch") {
		t.Errorf("a dependency-class refusal refused the launch:\n%s", r.out)
	}
	if !r.ranJail {
		t.Errorf("the launch did not reach its container:\n%s", r.out)
	}
	cause := strings.Index(r.out, "refused startup: "+frontDoorReason)
	symptom := strings.Index(r.out, "cannot reach it")
	if cause < 0 || symptom < 0 || cause > symptom {
		t.Errorf("the daemon's cause is not printed before the derived symptom (cause %d, symptom %d):\n%s",
			cause, symptom, r.out)
	}
	if strings.Contains(r.out, frontDoorStaleLog) {
		t.Errorf("a historical shared-log line reached the launch output:\n%s", r.out)
	}
}

// TestAFreshContainerLaunchRefusesInvalidSettingsBeforeAnyLaunchWork: the preflight refuses in
// the launching terminal, before the image step and the keeper, and the daemon never runs.
func TestAFreshContainerLaunchRefusesInvalidSettingsBeforeAnyLaunchWork(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "daemon-ran")
	frontDoorHome(t, preflightRefusalManifest(marker))
	r := frontDoorContainerLaunch(t)
	if r.rc != 1 || r.ranJail {
		t.Errorf("Run() = %d (container ran: %v), want a refused launch:\n%s", r.rc, r.ranJail, r.out)
	}
	if r.loadedImage {
		t.Errorf("the launch reached its image step past a refused settings preflight:\n%s", r.out)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Errorf("the refused service's daemon ran:\n%s", r.out)
	}
	if strings.Contains(r.out, "keeper: started") {
		t.Errorf("the launch spawned its keeper past a refused settings preflight:\n%s", r.out)
	}
	assertFrontDoorRefusal(t, r.out, "Refusing to launch", "The AWS profile is not usable.",
		"Run aws sso login --profile <profile>.")
}

// TestAFreshMacosUserLaunchRelaysADaemonRefusalThroughItsKeeper is the macos-user arm, whose
// planned host service makes it a keeper launch (macosUserKeeps): the keeper's refusal reaches
// the terminal and the sandboxed command never runs.
func TestAFreshMacosUserLaunchRelaysADaemonRefusalThroughItsKeeper(t *testing.T) {
	frontDoorHome(t, cooperativeRefusalManifest())
	t.Setenv(perJailReasonChildModeEnv, "exit")
	t.Setenv(perJailReasonChildDelayEnv, "")
	t.Setenv(perJailReasonChildClassEnv, "configuration")
	out, rc, started := frontDoorMacosUserLaunch(t)
	if started {
		t.Errorf("the macos-user launch ran its command past a refused host service:\n%s", out)
	}
	if rc != 1 {
		t.Errorf("Run() = %d, want 1 for a refused launch:\n%s", rc, out)
	}
	assertFrontDoorRefusal(t, out, "Refusing the macos-user launch", frontDoorReason, frontDoorRemedy)
}

// TestAFreshMacosUserLaunchRefusesInvalidSettingsBeforeItsKeeper is the native arm's preflight.
func TestAFreshMacosUserLaunchRefusesInvalidSettingsBeforeItsKeeper(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "daemon-ran")
	frontDoorHome(t, preflightRefusalManifest(marker))
	out, rc, started := frontDoorMacosUserLaunch(t)
	if started || rc != 1 {
		t.Errorf("Run() = %d (command started: %v), want a refused launch:\n%s", rc, started, out)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Errorf("the refused service's daemon ran:\n%s", out)
	}
	if strings.Contains(out, "keeper: started") {
		t.Errorf("the launch spawned its keeper past a refused settings preflight:\n%s", out)
	}
	assertFrontDoorRefusal(t, out, "Refusing to launch", "The AWS profile is not usable.",
		"Run aws sso login --profile <profile>.")
}

// TestAHostDoorwayStartReturnsADaemonRefusal is the `yolo host` front door: HostDoorways.Start
// ensures the doorway's host service, which refuses over the attempt channel, and Start returns
// that refusal as its error before any doorway starts.
func TestAHostDoorwayStartReturnsADaemonRefusal(t *testing.T) {
	frontDoorHome(t, "{}")
	t.Setenv(perJailReasonChildModeEnv, "exit")
	t.Setenv(perJailReasonChildDelayEnv, "")
	t.Setenv(perJailReasonChildClassEnv, "configuration")
	packRoot := t.TempDir()
	module := filepath.Join(packRoot, "loopholes", frontDoorFixtureName)
	if err := os.MkdirAll(module, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(module, "manifest.jsonc"), []byte(cooperativeRefusalManifest()), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packRoot, "pack.json"), []byte(`{"name":"fixture-pack","contributes":`+
		`[{"kind":"loophole","from":"loopholes/`+frontDoorFixtureName+`"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	pack, problems := packload.LoadDir(packRoot, "fixture-pack")
	if len(problems) != 0 {
		t.Fatalf("fixture pack: %v", problems)
	}
	set := loopholes.NewSet(loopholes.DiscoverOptions{
		PackModules: []loopholes.PackModule{{Dir: module, HostExecApproved: true}},
	})
	plan := &launchservice.Plan{Declared: launchservice.Declared{Service: frontDoorFixtureName, Pack: "fixture-pack"}}
	d := &HostDoorways{plans: []*launchservice.Plan{plan}, set: set, packs: []*packload.Pack{pack}}
	var output bytes.Buffer
	startCalled := false
	_, stop, _, err := d.Start(newConfig(), t.TempDir(), "pi", &output,
		func(*launchservice.Plan, map[string]string) (*launchservice.Running, error) {
			startCalled = true
			return nil, errors.New("fixture must not start after a refused service")
		})
	if stop != nil {
		stop()
	}
	if err == nil || startCalled {
		t.Fatalf("the doorway started past a refused host service: err=%v started=%v\n%s", err, startCalled, output.String())
	}
	assertFrontDoorRefusal(t, output.String()+"\n"+err.Error(), "refused startup", frontDoorReason, frontDoorRemedy)
}
