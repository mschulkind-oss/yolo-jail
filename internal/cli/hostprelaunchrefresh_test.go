package cli

// hostprelaunchrefresh_test.go pins pi's PRE-LAUNCH REFRESH at `yolo host --`
// (docs/design/host-tool-provisioning.md HP-D19) at its call site, hostLaunch, through hostMain with
// the exec replaced: the shipped pi pack, which declares `update --extensions` watching
// ~/.pi/agent/settings.json; a stub pi on PATH, which the user-scope `host_floor` makes the copy
// that runs; and a log the stub's refresh and the exec stand-in both append to, so "before the exec"
// is a fact about order. Deleting the hostLaunch call fails every cell that expects a refresh. No
// pi runs and nothing is sent anywhere.

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/hostfloor"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/pidlock"
)

// THE PACKAGE'S FAKE AGENTS ANSWER PI'S PRE-LAUNCH REFRESH AND RECORD NOTHING. Every host launch of
// pi now runs `pi update --extensions` against the copy it is about to exec, and a cell whose fake
// agent stands in for pi (hostservices_test.go's and hostawsdoorway_test.go's) records the agent
// SESSION it was handed: a report the refresh wrote would read as an agent that ran on a launch
// refused before it (TestHostRefusesWhenTheDoorwayCannotStart), and a sleeping one would hold the
// launch for the refresh's bound. A refresh is the program's own upkeep, not a session, so the fake
// answers it as a real pi with nothing to update does. It runs before TestMain's dispatch, in the
// child the stand-in's script execs.
func init() {
	if len(os.Args) == 4 && (os.Args[1] == testFakeAgentArg || os.Args[1] == testFakeAWSAgentArg) &&
		os.Args[2] == "update" && os.Args[3] == "--extensions" {
		os.Exit(0)
	}
}

// piOnItsPathCopy selects pi with the floor holding none of it, so the launch runs the PATH copy:
// the refresh runs for whichever copy the launch execs.
const piOnItsPathCopy = `{"packs": ["pi"], "host_floor": {"pi": false}}`

// refreshHost is one scratch host: its HOME, the stub's log, and the stub itself.
type refreshHost struct {
	home, log, stub string
}

// newRefreshHost makes a host with cfg as its user config and a stub pi first on PATH, whose
// `update …` logs its argv, ZAI_API_KEY and PATH, prints one line to each stream, then runs body.
func newRefreshHost(t *testing.T, cfg, body string) *refreshHost {
	t.Helper()
	home := hostGateHome(t, cfg, nil)
	h := &refreshHost{home: home, log: filepath.Join(home, "pi.log"), stub: filepath.Join(home, "stub-bin", "pi")}
	h.setStub(t, body)
	t.Setenv("PATH", filepath.Dir(h.stub)+string(os.PathListSeparator)+os.Getenv("PATH"))
	return h
}

func (h *refreshHost) setStub(t *testing.T, body string) {
	t.Helper()
	writeFile(t, h.stub, "#!/bin/sh\nif [ \"$1\" = update ]; then\n"+
		"  echo \"refresh $*|ZAI_API_KEY=${ZAI_API_KEY-unset}|PATH=$PATH\" >> '"+h.log+"'\n"+
		"  echo refresh-on-stdout\n  echo refresh-on-stderr >&2\n"+body+"\n  exit 0\nfi\nexit 0\n")
	if err := os.Chmod(h.stub, 0o755); err != nil {
		t.Fatal(err)
	}
}

// entries is the log, one entry per line: "refresh …" per refresh, "exec" per exec.
func (h *refreshHost) entries(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile(h.log)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
}

func (h *refreshHost) refreshes(t *testing.T) int {
	t.Helper()
	n := 0
	for _, e := range h.entries(t) {
		if strings.HasPrefix(e, "refresh ") {
			n++
		}
	}
	return n
}

func (h *refreshHost) stamp() string {
	return filepath.Join(paths.HostFloorDirUnder(h.home), "refresh", "pi.stamp")
}

// refreshLaunchResult is one `yolo host [flags] -- pi`.
type refreshLaunchResult struct {
	rc        int
	out, errs string
	// execEnv is what the exec was handed, nil when the launch never reached it.
	execEnv map[string]string
}

// launch runs `yolo host [flags] -- pi` with the exec replaced by one that logs "exec".
func (h *refreshHost) launch(t *testing.T, flags ...string) refreshLaunchResult {
	t.Helper()
	var r refreshLaunchResult
	orig := hostSyscallExec
	hostSyscallExec = func(_ string, _, env []string) error {
		f, err := os.OpenFile(h.log, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err == nil {
			_, _ = f.WriteString("exec\n")
			f.Close()
		}
		r.execEnv = map[string]string{}
		for _, kv := range env {
			if k, v, ok := strings.Cut(kv, "="); ok {
				r.execEnv[k] = v
			}
		}
		return nil
	}
	defer func() { hostSyscallExec = orig }()
	var out, errw bytes.Buffer
	r.rc = hostMain(append(append([]string{}, flags...), "--", "pi"), &out, &errw, false, nil)
	r.out, r.errs = out.String(), errw.String()
	return r
}

// boundRefreshes gives every floor this test builds a refresh bound of d: the launch's seam.
func boundRefreshes(t *testing.T, d time.Duration) {
	t.Helper()
	orig := newHostFloor
	newHostFloor = func(out io.Writer, progs []hostfloor.Program) *hostfloor.Floor {
		f := orig(out, progs)
		f.PollTimeout = d
		return f
	}
	t.Cleanup(func() { newHostFloor = orig })
}

const refreshingPi = "yolo host: Refreshing pi (update --extensions)...\n"

func TestHostPiRefreshesItsExtensionsOnceBeforeItsExec(t *testing.T) {
	h := newRefreshHost(t, piOnItsPathCopy, "")
	r := h.launch(t)
	if r.rc != 0 || r.execEnv == nil {
		t.Fatalf("yolo host -- pi: rc=%d, exec reached %v\n%s", r.rc, r.execEnv != nil, r.errs)
	}
	got := h.entries(t)
	if len(got) != 2 || !strings.HasPrefix(got[0], "refresh update --extensions|") || got[1] != "exec" {
		t.Fatalf("the log is %q, want pi's declared refresh once and then the exec", got)
	}
	if !strings.Contains(r.errs, refreshingPi) {
		t.Errorf("the refresh did not say it was running:\n%s", r.errs)
	}
	// Its output is the launch's stderr, never its stdout: an agent's stdout is routinely parsed.
	for _, line := range []string{"refresh-on-stdout", "refresh-on-stderr"} {
		if !strings.Contains(r.errs, line) {
			t.Errorf("the refresh's %q did not reach the launch's stderr:\n%s", line, r.errs)
		}
		if strings.Contains(r.out, line) {
			t.Errorf("the refresh's %q reached the launch's stdout: %q", line, r.out)
		}
	}
	if strings.Index(r.errs, refreshingPi) > strings.Index(r.errs, "yolo host: starting pi") {
		t.Errorf("the refresh came after the starting line:\n%s", r.errs)
	}

	// Inside the interval, the next launch runs none.
	if r := h.launch(t); r.rc != 0 || h.refreshes(t) != 1 || strings.Contains(r.errs, refreshingPi) {
		t.Errorf("a second launch inside the interval: rc=%d, %d refreshes\n%s", r.rc, h.refreshes(t), r.errs)
	}
	// Past it, one more.
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(h.stamp(), old, old); err != nil {
		t.Fatalf("the refresh left no stamp under the floor: %v", err)
	}
	if r := h.launch(t); r.rc != 0 || h.refreshes(t) != 2 {
		t.Errorf("a launch past the interval: rc=%d, %d refreshes, want 2\n%s", r.rc, h.refreshes(t), r.errs)
	}
}

func TestHostPiRefreshesInsideTheIntervalWhenItsSettingsChange(t *testing.T) {
	h := newRefreshHost(t, piOnItsPathCopy, "")
	h.launch(t)
	writeFile(t, filepath.Join(h.home, ".pi", "agent", "settings.json"), `{"packages":["npm:pi-fixture"]}`)
	if r := h.launch(t); r.rc != 0 || h.refreshes(t) != 2 {
		t.Errorf("a changed settings.json inside the interval: rc=%d, %d refreshes, want 2\n%s",
			r.rc, h.refreshes(t), r.errs)
	}
	if h.launch(t); h.refreshes(t) != 2 {
		t.Errorf("the same settings again ran the refresh again")
	}
}

func TestHostPiRefreshFollowsAgentUpdates(t *testing.T) {
	for name, cfg := range map[string]string{
		"agent_updates false":     `{"packs": ["pi"], "host_floor": {"pi": false}, "agent_updates": false}`,
		"agent_updates pi: false": `{"packs": ["pi"], "host_floor": {"pi": false}, "agent_updates": {"pi": false}}`,
	} {
		t.Run(name, func(t *testing.T) {
			h := newRefreshHost(t, cfg, "")
			r := h.launch(t)
			if r.rc != 0 || r.execEnv == nil {
				t.Fatalf("rc=%d\n%s", r.rc, r.errs)
			}
			if h.refreshes(t) != 0 || strings.Contains(r.errs, "Refreshing pi") {
				t.Errorf("%s still refreshed pi:\n%s", name, r.errs)
			}
		})
	}
}

func TestHostPiRefreshFailureIsALineAndPiStillStarts(t *testing.T) {
	h := newRefreshHost(t, piOnItsPathCopy, "  exit 7")
	r := h.launch(t)
	if r.rc != 0 || r.execEnv == nil {
		t.Fatalf("a failed refresh stopped the launch: rc=%d\n%s", r.rc, r.errs)
	}
	if !strings.Contains(r.errs, "yolo host: pi: the pre-launch refresh failed (status 7) — running what is installed\n") {
		t.Errorf("the failure was not said in the launcher's words:\n%s", r.errs)
	}
	seen := filepath.Join(paths.HostFloorDirUnder(h.home), "refresh", "pi.seen")
	if entries, _ := os.ReadDir(seen); len(entries) != 0 {
		t.Errorf("a failed refresh recorded its settings as seen: %v", entries)
	}
	// So the next launch retries it, inside the interval.
	if h.launch(t); h.refreshes(t) != 2 {
		t.Errorf("the launch after a failed refresh did not retry it: %d refreshes", h.refreshes(t))
	}
}

func TestHostPiRefreshThatHangsIsBoundedAndPiStillStarts(t *testing.T) {
	boundRefreshes(t, 300*time.Millisecond)
	h := newRefreshHost(t, piOnItsPathCopy, "  exec sleep 30")
	start := time.Now()
	r := h.launch(t)
	if r.rc != 0 || r.execEnv == nil {
		t.Fatalf("a hung refresh stopped the launch: rc=%d\n%s", r.rc, r.errs)
	}
	if took := time.Since(start); took > hostfloor.RefreshKillAfter+10*time.Second {
		t.Errorf("the launch waited %s on a refresh bounded at 300ms", took)
	}
	if !strings.Contains(r.errs, "yolo host: pi: the pre-launch refresh timed out after 300ms — running what is installed\n") {
		t.Errorf("the timeout was not said:\n%s", r.errs)
	}
}

func TestHostPiRefreshSkipsAHeldLockWithOneLine(t *testing.T) {
	h := newRefreshHost(t, piOnItsPathCopy, "")
	h.launch(t)
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(h.stamp(), old, old); err != nil {
		t.Fatal(err)
	}
	lock := filepath.Join(paths.HostFloorDirUnder(h.home), "refresh", "pi.lock")
	lk, err := pidlock.Acquire(lock, pidlock.NoWait, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer lk.Release()
	r := h.launch(t)
	if r.rc != 0 || r.execEnv == nil || h.refreshes(t) != 1 {
		t.Fatalf("a held lock: rc=%d, %d refreshes, want pi started and no second refresh\n%s",
			r.rc, h.refreshes(t), r.errs)
	}
	if n := strings.Count(r.errs, "another refresh holds"); n != 1 {
		t.Errorf("a held lock said so %d times, want once:\n%s", n, r.errs)
	}
}

// THE REFRESH CARRIES NO CREDENTIAL THE COMPOSITION SCOPES TO PI: the jail's launcher runs it
// before it sources pi's own env file, which is where the gate puts what it scopes to pi alone.
func TestHostPiRefreshRunsWithoutTheCredentialsComposedForPi(t *testing.T) {
	h := newRefreshHost(t, `{"packs": ["pi", "zai"], "host_floor": {"pi": false}, `+
		`"env_sources": [{"ZAI_API_KEY": "tok-host"}]}`, "")
	r := h.launch(t, "-p", "zai")
	if r.rc != 0 || r.execEnv == nil {
		t.Fatalf("yolo host -p zai -- pi: rc=%d\n%s", r.rc, r.errs)
	}
	if r.execEnv["ZAI_API_KEY"] != "tok-host" {
		t.Fatalf("setup: the exec was not handed pi's zai key (got %q)\n%s", r.execEnv["ZAI_API_KEY"], r.errs)
	}
	// The invoking shell's own value (hostGateHome blanks it) passes through, as it does to pi;
	// the value yolo composed for pi does not.
	got := h.entries(t)
	if len(got) == 0 || !strings.HasPrefix(got[0], "refresh ") || strings.Contains(got[0], "tok-host") {
		t.Errorf("the refresh did not run, or saw pi's credential: %q", got)
	}
	// And the child's PATH, the one pi is exec'd with, floor bin/ last.
	if len(got) > 0 && !strings.Contains(got[0], "|PATH="+filepath.Dir(h.stub)+string(os.PathListSeparator)) {
		t.Errorf("the refresh did not run on the child's PATH: %q", got[0])
	}
}

// THE REFRESH RUNS IN WHAT EVERY PROCESS RECEIVES, AND IN NOTHING SCOPED TO PI. In a jail the
// refresh inherits the shared yolo-user-env.sh, which every process there gets: the ungated pack
// env (pi's own PI_TELEMETRY=0) and the env_sources values no provider claims (a proxy, a CA
// bundle, a registry), which a refresh behind a proxy cannot do without. Only what the credential
// gate scopes to pi is in pi's own file, which its launcher sources after the refresh. And every
// removal applies, an env_sources null taking the invoking shell's value out, so the refresh never
// holds a name pi would not.
func TestHostPiRefreshRunsInTheCompositionEveryProcessReceives(t *testing.T) {
	h := newRefreshHost(t, `{"packs": ["pi", "zai"], "host_floor": {"pi": false}, "env_sources": [`+
		`{"ZAI_API_KEY": "tok-host", "HTTPS_PROXY": "http://proxy.example:3128", "REVIEW_SECRET": null}]}`, "")
	h.setStub(t, `  echo "env PI_TELEMETRY=${PI_TELEMETRY-unset}|HTTPS_PROXY=${HTTPS_PROXY-unset}|`+
		`REVIEW_SECRET=${REVIEW_SECRET-unset}|ZAI_API_KEY=${ZAI_API_KEY-unset}" >> '`+h.log+`'`)
	t.Setenv("REVIEW_SECRET", "from-shell")
	// None may come from the shell this test runs in (a jail exports PI_TELEMETRY itself), so an
	// unset one in the refresh is one yolo did not set.
	for _, k := range []string{"PI_TELEMETRY", "HTTPS_PROXY", "ZAI_API_KEY"} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
	r := h.launch(t, "-p", "zai")
	if r.rc != 0 || r.execEnv == nil {
		t.Fatalf("yolo host -p zai -- pi: rc=%d\n%s", r.rc, r.errs)
	}
	var got string
	for _, e := range h.entries(t) {
		if strings.HasPrefix(e, "env ") {
			got = e
		}
	}
	want := "env PI_TELEMETRY=0|HTTPS_PROXY=http://proxy.example:3128|REVIEW_SECRET=unset|ZAI_API_KEY=unset"
	if got != want {
		t.Errorf("the refresh ran in\n  %q\nwant\n  %q", got, want)
	}
	// The same composition hands pi all of it, its zai key included, and the null's removal.
	if r.execEnv["PI_TELEMETRY"] != "0" || r.execEnv["HTTPS_PROXY"] != "http://proxy.example:3128" ||
		r.execEnv["ZAI_API_KEY"] != "tok-host" {
		t.Errorf("setup: the exec was not handed the composition: %v", r.execEnv)
	}
	if v, ok := r.execEnv["REVIEW_SECRET"]; ok {
		t.Errorf("setup: the env_sources null left pi the shell's value %q", v)
	}
}

func TestHostPiInAJailLeavesTheRefreshToTheJailsLauncher(t *testing.T) {
	h := newRefreshHost(t, piOnItsPathCopy, "")
	t.Setenv("YOLO_VERSION", "0.0.0-test")
	r := h.launch(t)
	if r.rc != 0 || r.execEnv == nil {
		t.Fatalf("in a jail: rc=%d\n%s", r.rc, r.errs)
	}
	if h.refreshes(t) != 0 || strings.Contains(r.errs, "Refreshing pi") {
		t.Errorf("in a jail yolo host refreshed pi itself:\n%s", r.errs)
	}
	if _, err := os.Stat(filepath.Join(paths.HostFloorDirUnder(h.home), "refresh")); !os.IsNotExist(err) {
		t.Errorf("an in-jail launch made the host's refresh state: %v", err)
	}
}

// A SIGTERM OR SIGHUP THAT STOPS THE REFRESH ENDS THE LAUNCH, as the jail's launcher ends: the
// stub signals its parent, which is this test process because the floor execs it directly.
func TestHostPiRefreshStoppedByATerminateEndsTheLaunch(t *testing.T) {
	h := newRefreshHost(t, piOnItsPathCopy, "  kill -TERM $PPID; exec sleep 10")
	r := h.launch(t)
	if r.execEnv != nil {
		t.Fatalf("a SIGTERM during the refresh still started pi\n%s", r.errs)
	}
	if r.rc != 128+15 {
		t.Errorf("rc=%d, want 143 (SIGTERM)\n%s", r.rc, r.errs)
	}
	if !strings.Contains(r.errs, "stopped by SIGTERM") || !strings.Contains(r.errs, "run it again to start pi") {
		t.Errorf("the stop was not said with its next step:\n%s", r.errs)
	}
	if pidlock.Held(filepath.Join(paths.HostFloorDirUnder(h.home), "refresh", "pi.lock")) {
		t.Error("the stopped refresh left its lock held")
	}
	// A Ctrl-C ends the refresh and pi still starts.
	h.setStub(t, "  kill -INT $PPID; exec sleep 10")
	if err := os.Remove(h.stamp()); err != nil {
		t.Fatal(err)
	}
	r = h.launch(t)
	if r.rc != 0 || r.execEnv == nil {
		t.Fatalf("a Ctrl-C during the refresh stopped the launch: rc=%d\n%s", r.rc, r.errs)
	}
	if !strings.Contains(r.errs, "the pre-launch refresh was interrupted (Ctrl-C) — running what is installed") {
		t.Errorf("the interrupt was not said:\n%s", r.errs)
	}
}

// A REFRESH THAT HANDLES THE SIGTERM STILL ENDS THE LAUNCH: what decides is the signal yolo host
// received, not the status the refresh chose on its way out (a shell trap here, a node
// process.on handler followed by process.exit(143) in the wild), as the jail launcher's _shielded
// trap ends the launcher whatever the program exits with. The stub's shape is
// testsupport.UntilInterrupted's, which macOS's /bin/sh needs: nothing in the foreground, the
// trap set once the first wait has returned.
func TestHostPiRefreshThatHandlesASIGTERMStillEndsTheLaunch(t *testing.T) {
	h := newRefreshHost(t, piOnItsPathCopy, "  i=0; t=0; while [ $i -le 200 ]; do sleep $t >/dev/null 2>&1 & wait $!; "+
		"if [ $i -eq 0 ]; then trap 'exit 143' TERM; kill -TERM $PPID; t=0.05; fi; i=$((i+1)); done")
	r := h.launch(t)
	if r.execEnv != nil {
		t.Fatalf("a SIGTERM the refresh handled still started pi\n%s", r.errs)
	}
	if r.rc != 128+15 {
		t.Errorf("rc=%d, want 143 (SIGTERM)\n%s", r.rc, r.errs)
	}
	if strings.Contains(r.errs, "the pre-launch refresh failed") || !strings.Contains(r.errs, "stopped by SIGTERM") {
		t.Errorf("the stop was said as a failure, or not at all:\n%s", r.errs)
	}
}

// THE FLOOR'S OWN COPY IS REFRESHED TOO: a fixture pack's npm program, installed into the floor
// from the fake registry, declares a refresh; the launch runs it through bin/<bin>, whose fake Node
// prints the argv it was handed — to the launch's stderr, never its stdout — and then execs that
// same launcher.
func TestHostRefreshRunsAgainstTheFloorsCopy(t *testing.T) {
	floorHostFixtureWith(t, `{"kind":"program","bin":"floorcli","via":"npm","package":"floorcli-pkg",`+
		`"refresh":{"argv":["sync","--add-ons"],"lock":".floorcli-store/.yolo-update.lock"}}`, "")
	got := captureHostExec(t)
	var out, errw bytes.Buffer
	if rc := hostMain([]string{"--", "floorcli"}, &out, &errw, false, nil); rc != 0 || !got.execed {
		t.Fatalf("yolo host -- floorcli: rc=%d, exec reached %v\n%s", rc, got.execed, errw.String())
	}
	launcher := filepath.Join(paths.HostFloorDir(), "bin", "floorcli")
	if got.target != launcher {
		t.Fatalf("setup: the launch exec'd %s, want the floor's copy %s\n%s", got.target, launcher, errw.String())
	}
	errs := errw.String()
	if !strings.Contains(errs, "yolo host: Refreshing floorcli (sync --add-ons)...\n") {
		t.Errorf("the floor's copy was not refreshed:\n%s", errs)
	}
	if !strings.Contains(errs, "node:") || !strings.Contains(errs, " sync --add-ons\n") {
		t.Errorf("the refresh did not run the floor's launcher with the declared argv:\n%s", errs)
	}
	if strings.Contains(out.String(), "node:") {
		t.Errorf("the refresh's output reached the launch's stdout: %q", out.String())
	}
}
