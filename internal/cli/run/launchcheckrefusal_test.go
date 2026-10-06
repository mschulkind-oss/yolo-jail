package run

// launchcheckrefusal_test.go pins OQ-HD11's ruling (docs/design/host-daemon-ownership.md, ruled
// 2026-10-05) and HD-D5 THROUGH Run(): a host-wide daemon that predates the launching yolo REFUSES
// a fresh launch on both arms that start host services, whether it answers the launch check
// `unknown action: launch-check` or does not speak the connection preamble at all. An attach keeps
// the yellow line and enters the jail (HD-D5 (1)), and a daemon this yolo started whose program
// lacks the check keeps the line too (HD-D6). No launch restarts the daemon.
//
// Each test drives a whole launch, not the boundary launchcheck_test.go drives, so deleting the
// refusal at one call site fails the test for that site: the keeper's (the container arm) or the
// macos-user arm's. Each has a control beside it, the same launch against a daemon that does
// answer the check, which proceeds; without it the refusal tests would pass on a harness that
// never reached the check at all.
//
// The daemon is launchcheck_test.go's fixture, served at the host-wide singleton path with a pid
// file naming this process, so every launch ENSURES it rather than spawning one. Its loophole
// declares no jail daemon, so every launch of it is asked (runLaunchChecks' served rule).

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/broker"
	"github.com/mschulkind-oss/yolo-jail/internal/hostservice"
	"github.com/mschulkind-oss/yolo-jail/internal/image"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	yoloruntime "github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// askedEveryLaunchManifest is the fixture loophole as a local pack ships it: host-wide, declaring
// the launch check, and with no jail daemon, so no payload decides whether it is asked.
const askedEveryLaunchManifest = `{
	"name": "` + launchCheckFixtureName + `",
	"description": "a host-wide service that is asked the launch check",
	"default_enabled": true,
	"transport": "loopback-tls",
	"lifecycle": "spawned",
	"host_daemon": {"cmd": ["/bin/false", "{socket}"], "publishes": "socket",
		"scope": "host", "launch_check": true}
}`

// olderThanTheCheck is a daemon a yolo older than the launch check started: its handler's default
// case answers the action it does not know.
func olderThanTheCheck(s *hostservice.Session) {
	s.Stderr("unknown action: launch-check\n")
	s.Exit(2)
}

// answersTheCheck is the control's daemon: it knows the check and has one note to give.
func answersTheCheck(s *hostservice.Session) {
	_ = s.AnswerLaunchCheck(hostservice.LaunchCheckReport{Notes: []string{"checked by the control"}})
}

// controlNote is the line the control's daemon makes every launch print.
const controlNote = "loophole " + launchCheckFixtureName + ": checked by the control"

// launchCheckRefusalHome isolates a whole launch with the fixture loophole in the conventional local pack.
func launchCheckRefusalHome(t *testing.T) string {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("binds host-wide singleton paths")
	}
	home := packHome(t)
	writeLocalLoopholePack(t, home, launchCheckFixtureName, askedEveryLaunchManifest)
	writeUserPacks(t, home, `[]`)
	return home
}

// assertTheRefusal checks the refusal's words: who refused, the daemon, the one command that fixes
// it, and that the jails already running are safe from that command.
func assertTheRefusal(t *testing.T, out, headline string) {
	t.Helper()
	line := ""
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, headline) {
			line = l
		}
	}
	if line == "" {
		t.Fatalf("nothing says %q:\n%s", headline, out)
	}
	for _, want := range []string{"'" + launchCheckFixtureName + "'", "predates this yolo",
		"does not answer the launch check", broker.CycleCommand(launchCheckFixtureName),
		"reconnect", "Then launch again"} {
		if !strings.Contains(out, want) {
			t.Errorf("the refusal does not say %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Restarting the host-wide daemon") {
		t.Errorf("the launch restarted the daemon itself, which no launch may do:\n%s", out)
	}
}

// assertThePreambleRefusal is assertTheRefusal's twin for a daemon that predates the preamble.
func assertThePreambleRefusal(t *testing.T, out, headline string) {
	t.Helper()
	for _, want := range []string{headline, "'" + launchCheckFixtureName + "' predates this yolo",
		"does not speak the connection preamble", broker.CycleCommand(launchCheckFixtureName),
		"reconnect", "Then launch again"} {
		if !strings.Contains(out, want) {
			t.Errorf("the refusal does not say %q:\n%s", want, out)
		}
	}
}

// assertTheDaemonWasNotRestarted: the fixture's pid file still names this process, which a launch
// that killed and respawned it would have replaced, and its socket still answers.
func assertTheDaemonWasNotRestarted(t *testing.T) {
	t.Helper()
	raw, err := os.ReadFile(paths.HostSingletonPIDFile(launchCheckFixtureName))
	if err != nil || strings.TrimSpace(string(raw)) != strconv.Itoa(os.Getpid()) {
		t.Errorf("the daemon's pid file now reads %q (%v): something restarted it", raw, err)
	}
	if !hostSingletonAccepting(paths.HostSingletonSocket(launchCheckFixtureName), time.Second) {
		t.Error("the daemon no longer accepts: the launch stopped it")
	}
}

// containerLaunch runs a fresh podman launch, its keeper in-process, to its container's start, and
// reports what it printed, its status, and whether the runtime was asked to run the container.
// Each of prep runs once the daemon is up (spawnedByThisYolo, predatesThePreamble).
func containerLaunch(t *testing.T, handler hostservice.Handler,
	prep ...func(*testing.T)) (out string, rc int, ranContainer bool) {
	t.Helper()
	launchCheckRefusalHome(t)
	serveLaunchCheckDaemon(t, handler)
	for _, p := range prep {
		p(t)
	}
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
	repo, _ := o.RepoRoot()
	o.PathExists = func(p string) bool {
		return p == filepath.Join(prebuiltBinDir(repo.Root), "yolo-entrypoint")
	}
	o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
		return ExecResult{Ran: true, RC: 0} // `ps`: no container of the name is running
	}
	o.autoLoad = func(image.AutoLoadOptions) image.LoadResult {
		return image.LoadResult{OK: true, Ref: goldenImageRef}
	}
	t.Cleanup(func() { _ = os.RemoveAll(hostServiceSocketsDir(cname, false)) })
	done := make(chan int, 1)
	go func() { done <- Run(*o) }()
	select {
	case rc = <-done:
	case <-time.After(sealedLaunchBound):
		t.Fatalf("the launch had not returned after %s\nstdout:\n%s\nstderr:\n%s",
			sealedLaunchBound, stdout.String(), stderr.String())
	}
	_, err := os.Stat(ran)
	return stdout.String() + stderr.String(), rc, err == nil
}

// TestAFreshContainerLaunchRefusesADaemonOlderThanTheLaunchCheck is the container arm, whose host
// services its keeper starts: the keeper refuses before the container, and the launch exits 1.
func TestAFreshContainerLaunchRefusesADaemonOlderThanTheLaunchCheck(t *testing.T) {
	out, rc, ran := containerLaunch(t, olderThanTheCheck)
	if ran {
		t.Errorf("the launch started its container past a daemon that predates the check:\n%s", out)
	}
	if rc != 1 {
		t.Errorf("Run() = %d, want 1 for a refused launch:\n%s", rc, out)
	}
	assertTheRefusal(t, out, "Refusing this launch")
	assertTheDaemonWasNotRestarted(t)
}

// TestAFreshContainerLaunchProceedsPastADaemonThatAnswers is its control: the same launch reaches
// the check, prints the daemon's note, and starts its container.
func TestAFreshContainerLaunchProceedsPastADaemonThatAnswers(t *testing.T) {
	out, _, ran := containerLaunch(t, answersTheCheck)
	if !strings.Contains(out, controlNote) {
		t.Fatalf("the launch never asked the daemon, so its refusal twin proves nothing:\n%s", out)
	}
	if !ran {
		t.Errorf("a launch whose daemon answers the check did not start its container:\n%s", out)
	}
	if strings.Contains(out, "Refusing") {
		t.Errorf("a launch whose daemon answers the check refused:\n%s", out)
	}
}

// TestAFreshContainerLaunchRefusesADaemonThatPredatesThePreamble is HD-D5 (3) on the container
// arm: a host-wide daemon started before yolo's front would accept the jail's connections and fail
// every request, so the keeper refuses before the container, as it does for the launch check.
func TestAFreshContainerLaunchRefusesADaemonThatPredatesThePreamble(t *testing.T) {
	out, rc, ran := containerLaunch(t, answersTheCheck, predatesThePreamble)
	if ran {
		t.Errorf("the launch started its container past a daemon that predates the preamble:\n%s", out)
	}
	if rc != 1 {
		t.Errorf("Run() = %d, want 1 for a refused launch:\n%s", rc, out)
	}
	assertThePreambleRefusal(t, out, "Refusing this launch")
	assertTheDaemonWasNotRestarted(t)
}

// TestAFreshContainerLaunchWarnsWhenThisYolosDaemonLacksTheCheck is HD-D6 through Run(): the
// daemon this yolo spawned answers `unknown action: launch-check` because its program lacks what
// its manifest declares. A restart starts the same program, so a refusal naming it would refuse
// again after it, with no step that works; the launch warns and starts its container.
func TestAFreshContainerLaunchWarnsWhenThisYolosDaemonLacksTheCheck(t *testing.T) {
	out, _, ran := containerLaunch(t, olderThanTheCheck, spawnedByThisYolo)
	if strings.Contains(out, "Refusing") {
		t.Errorf("the launch refused a daemon no restart can fix:\n%s", out)
	}
	if !ran {
		t.Errorf("the launch did not start its container:\n%s", out)
	}
	if !strings.Contains(out, "loophole "+launchCheckFixtureName+": its daemon does not answer the "+
		"launch check its manifest declares") {
		t.Errorf("the launch does not say the manifest declares a check its program lacks:\n%s", out)
	}
}

// macosUserLaunchAgainst runs a macos-user launch against a stub backend and reports whether the
// sandboxed command was started.
func macosUserLaunchAgainst(t *testing.T, handler hostservice.Handler,
	prep ...func(*testing.T)) (out string, rc int, started bool) {
	t.Helper()
	launchCheckRefusalHome(t)
	serveLaunchCheckDaemon(t, handler)
	for _, p := range prep {
		p(t)
	}
	ws := t.TempDir()
	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, ws, "macos-user", &stdout, &stderr, nil)
	o.MacosUserRun = func(_ *jsonx.OrderedMap, _ string, _, _ []string, _, _ string, _ macosuser.HomeOverlay,
		_ macosuser.HostContext, _ bool, _ *jsonx.OrderedMap, _ []packload.BlockedTool, _ macosuser.JailDaemons) int {
		started = true
		return 0
	}
	rc = Run(*o)
	return stdout.String() + stderr.String(), rc, started
}

// TestAMacosUserLaunchRefusesADaemonOlderThanTheLaunchCheck is the arm that starts its own host
// services: it refuses before the sandboxed command runs.
func TestAMacosUserLaunchRefusesADaemonOlderThanTheLaunchCheck(t *testing.T) {
	out, rc, started := macosUserLaunchAgainst(t, olderThanTheCheck)
	if started {
		t.Errorf("the macos-user launch ran its command past a daemon that predates the check:\n%s", out)
	}
	if rc != 1 {
		t.Errorf("Run() = %d, want 1 for a refused launch:\n%s", rc, out)
	}
	assertTheRefusal(t, out, "Refusing the macos-user launch")
	assertTheDaemonWasNotRestarted(t)
}

// TestAMacosUserLaunchProceedsPastADaemonThatAnswers is its control.
func TestAMacosUserLaunchProceedsPastADaemonThatAnswers(t *testing.T) {
	out, rc, started := macosUserLaunchAgainst(t, answersTheCheck)
	if !strings.Contains(out, controlNote) {
		t.Fatalf("the launch never asked the daemon, so its refusal twin proves nothing:\n%s", out)
	}
	if !started || rc != 0 {
		t.Errorf("a launch whose daemon answers the check did not run its command (rc %d):\n%s", rc, out)
	}
}

// TestAMacosUserLaunchRefusesADaemonThatPredatesThePreamble is HD-D5 (3) on the macos-user arm.
func TestAMacosUserLaunchRefusesADaemonThatPredatesThePreamble(t *testing.T) {
	out, rc, started := macosUserLaunchAgainst(t, answersTheCheck, predatesThePreamble)
	if started {
		t.Errorf("the macos-user launch ran its command past a daemon that predates the preamble:\n%s", out)
	}
	if rc != 1 {
		t.Errorf("Run() = %d, want 1 for a refused launch:\n%s", rc, out)
	}
	assertThePreambleRefusal(t, out, "Refusing the macos-user launch")
	assertTheDaemonWasNotRestarted(t)
}

// attachAgainst runs an attach into a running jail whose launch published a front to the fixture
// daemon, as a jail an older yolo launched did: that yolo only warned about the daemon. The jail's
// pack tree is this workspace's own staging, so the attach reads the fixture loophole from it.
func attachAgainst(t *testing.T, handler hostservice.Handler) packSkewRun {
	t.Helper()
	launchCheckRefusalHome(t)
	serveLaunchCheckDaemon(t, handler)
	ws := t.TempDir()
	cname := yoloruntime.FromWorkspace(ws)
	var out bytes.Buffer
	fresh := dispatchOptions(t, ws, "podman", &out, &out, nil)
	cfg, ok := fresh.loadAndValidateConfig()
	if !ok {
		t.Fatalf("config:\n%s", out.String())
	}
	fresh.stagingCfg = cfg
	staged, ok := fresh.stageRunPacks(cname)
	if !ok {
		t.Fatalf("staging:\n%s", out.String())
	}
	if err := writeLivePackTree(cname, staged.root); err != nil {
		t.Fatal(err)
	}
	// The running jail's front, published by its launch and still open.
	t.Cleanup(func() { _ = os.RemoveAll(hostServiceSocketsDir(cname, false)) })
	launcher := &Options{}
	fillDefaults(launcher)
	var launchOut bytes.Buffer
	launcher.Stderr, launcher.Stdout = &launchOut, &launchOut
	launcher.PathExists = func(string) bool { return false }
	handles, _ := launcher.startLoopholesDisclosed(cname, "podman", newConfig(), staged.packs, nil)
	t.Cleanup(func() {
		for _, h := range handles {
			if h.stop != nil {
				h.stop()
			}
		}
	})
	if !startedLoophole(handles, launchCheckFixtureName) {
		t.Fatalf("the running jail's front was never published:\n%s", launchOut.String())
	}
	current := "YOLO_VERSION=9.9.9-test\n" + entrypointContractTagsLine() + "\n"
	return runPackSkew(t, ws, current, false, "", nil, nil)
}

// TestAnAttachKeepsTheLineAndEntersPastADaemonOlderThanTheLaunchCheck is HD-D5 (1) through Run():
// an attach starts nothing and restarts nothing, and refusing it would leave the running jail as
// it is, so it prints the yellow line naming the restart, delivers its channel and enters the jail.
func TestAnAttachKeepsTheLineAndEntersPastADaemonOlderThanTheLaunchCheck(t *testing.T) {
	r := attachAgainst(t, olderThanTheCheck)
	out := r.stdout + r.stderr
	if !r.execed || r.rc != 0 {
		t.Errorf("the attach did not enter the jail (rc %d):\n%s", r.rc, out)
	}
	if strings.Contains(out, "Refusing") {
		t.Errorf("the attach refused, which HD-D5 (1) keeps a warning:\n%s", out)
	}
	if _, err := os.Stat(r.envFile); err != nil {
		t.Errorf("the attach did not deliver its channel (%v)", err)
	}
	for _, want := range []string{"loophole " + launchCheckFixtureName + ": ", "predates this yolo",
		broker.CycleCommand(launchCheckFixtureName)} {
		if !strings.Contains(out, want) {
			t.Errorf("the attach does not say %q:\n%s", want, out)
		}
	}
	assertTheDaemonWasNotRestarted(t)
}

// TestAnAttachProceedsPastADaemonThatAnswers is its control: the attach asks, delivers its channel
// and enters the jail.
func TestAnAttachProceedsPastADaemonThatAnswers(t *testing.T) {
	r := attachAgainst(t, answersTheCheck)
	out := r.stdout + r.stderr
	if !strings.Contains(out, controlNote) {
		t.Fatalf("the attach never asked the daemon, so its twin's line proves nothing:\n%s", out)
	}
	if !r.execed || r.rc != 0 {
		t.Errorf("an attach whose daemon answers the check did not enter the jail (rc %d):\n%s", r.rc, out)
	}
	if _, err := os.Stat(r.envFile); err != nil {
		t.Errorf("the attach did not deliver its channel (%v)", err)
	}
}
