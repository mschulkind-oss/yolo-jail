package entrypoint

// prelaunchrefresh_test.go RUNS the pre-launch refresh (prelaunchrefresh.go) — the execution
// and concurrency tiers of docs/design/pi-extension-lifecycle.md, §3.2 and §3.3.
//
// Every cell drives a FAKE program: a shell script at the launcher's REAL_BIN that logs its
// argv and, when run with the refresh argv, can fail, block, or tamper with the lock on
// request. No agent is started and nothing reaches a network (AGENTS.md, "No agent tests");
// the one shipped-pack cell runs the REAL generated pi launcher against a fake pi.
//
// Each cell is written to go red for a named deletion: the call that runs the refresh, the
// splice that bakes it, the projection that carries it, the manifest line that declares it, the
// lock, the throttle, the policy gate, the bound, and the two redirections.

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// probeLockRel is the probe's lock, in a store that is one workspace's own state, as pi's
// `.pi` is since XB-D14 (docs/design/pi-extension-store-builds.md); a cell that wants a store
// two jails share links it (newSharedStorePair).
const (
	probeLockRel = ".tool/.yolo-update.lock"
	probeOwner   = ".yolo-lock-owner"
)

// fakeRefreshProgram is the stand-in vendor binary. It logs one line per argument
// ("ARG:<word>") after a header naming the mode, so a quoting defect cannot hide inside a
// space-joined "$*". Run with "update" first, it is the REFRESH and honors:
//
//	FAKE_REFRESH_RC=<n>      exit n
//	FAKE_REFRESH_WAIT=<f>    create <f>.started, then block until <f> exists (bounded)
//	FAKE_REFRESH_STEAL=1     overwrite the lock's owner token, as a stale-breaker would
//
// It also records whether the refresh LOCK was held while it ran, and whether anything
// arrived on stdin — the two facts a caller cannot see from the outside.
func fakeRefreshProgram(log, lock string) string {
	q := shellQuoteForTest
	return `#!/bin/bash
LOG=` + q(log) + `
LOCK=` + q(lock) + `
if [ "${1:-}" = "update" ]; then
    echo "REFRESH" >> "$LOG"
    for a in "$@"; do printf 'ARG:%s\n' "$a" >> "$LOG"; done
    [ -d "$LOCK" ] && echo "LOCKED" >> "$LOG"
    if IFS= read -r line; then echo "STDIN:$line" >> "$LOG"; fi
    echo "REFRESH-STDOUT"
    if [ -n "${FAKE_REFRESH_WAIT:-}" ]; then
        : > "$FAKE_REFRESH_WAIT.started"
        for _ in $(seq 1 400); do [ -e "$FAKE_REFRESH_WAIT" ] && break; sleep 0.05; done
    fi
    if [ -n "${FAKE_REFRESH_STEAL:-}" ]; then
        printf 'someone-else\n' > "$LOCK/` + probeOwner + `"
    fi
    exit "${FAKE_REFRESH_RC:-0}"
fi
echo "LAUNCH:$*" >> "$LOG"
echo "RAN $*"
if [ -n "${FAKE_LAUNCH_WAIT:-}" ]; then
    : > "$FAKE_LAUNCH_WAIT.started"
    for _ in $(seq 1 400); do [ -e "$FAKE_LAUNCH_WAIT" ] && break; sleep 0.05; done
fi
`
}

// prelaunchProbe is one generated launcher, the fake program it manages, and the store its
// refresh locks, which also holds the refresh's stamp and seen markers.
type prelaunchProbe struct {
	home, log, stamps, script, realBin, store string
	native                                    bool
	updates                                   bool
	refresh                                   *packdecl.Refresh
	// heartbeat, when set, replaces the baked REFRESH_HEARTBEAT interval (seconds) in the
	// rendered launcher, so a cell can watch the heartbeat without waiting a real minute.
	heartbeat string
	// bodyPatch replaces baked literals in the rendered launcher (old → new), for a cell that
	// must shorten a bound it cannot otherwise wait out.
	bodyPatch map[string]string
	// stderrWatch, when set, also receives the launcher's stderr AS IT IS WRITTEN, so a cell
	// can act on what the launcher says at the moment it says it rather than after a sleep.
	stderrWatch io.Writer
	// path, when set, is the launcher's PATH in place of this process's, so a cell can say
	// which yolo, if any, _bounded finds.
	path string
	// timing is the rendered Install's RefreshTiming: "" and "launch" run the refresh before the
	// exec, "next-launch" in the background (prelaunchrefreshtiming_test.go).
	timing string
	// fork renders a fork's SOURCE launcher instead (forklauncher.go); its program sits at the
	// native path, so a fork probe is made with native set.
	fork bool
	// gate and probe are the program's baked tree gate (Install.Gate) and probe arguments
	// (Install.ProbeArgs), for the cells of launchersteps_test.go.
	gate  string
	probe []string
	// servers is the baked MCP server set; empty, so no server refresh, unless a cell sets it.
	servers launcherServers
}

// newPrelaunchProbe seeds a fake program at REAL_BIN (so the launch path, not the cold-install
// arm, is under test) and the store the lock lives in. The PROGRAM's own update stamp is
// touched fresh, so the only thing due is the refresh — the two must not be confused.
func newPrelaunchProbe(t *testing.T, native bool) *prelaunchProbe {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not found")
	}
	home := t.TempDir()
	p := &prelaunchProbe{
		home:    home,
		log:     filepath.Join(home, "argv.log"),
		stamps:  filepath.Join(home, ".cache", "yolo-agent-stamps"),
		script:  filepath.Join(home, "launch-tool"),
		store:   filepath.Join(home, ".tool"),
		native:  native,
		updates: true,
		refresh: &packdecl.Refresh{Argv: []string{"update", "--extensions"}, Lock: probeLockRel},
	}
	p.realBin = filepath.Join(home, ".npm-global", "bin", "tool")
	if native {
		p.realBin = filepath.Join(home, ".local", "bin", "tool")
	}
	for _, d := range []string{filepath.Dir(p.realBin), p.stamps, p.store} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(p.realBin, []byte(fakeRefreshProgram(p.log, p.lockPath())), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p.stamps, "tool.stamp"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func (p *prelaunchProbe) lockPath() string { return filepath.Join(p.home, probeLockRel) }

// stampPath is the refresh's stamp where production names it, beside the lock (XB-D14).
func (p *prelaunchProbe) stampPath() string {
	return filepath.Join(p.home, filepath.FromSlash(RefreshStampRel(probeLockRel, "tool")))
}

// write renders the launcher through the production generator.
func (p *prelaunchProbe) write(t *testing.T) {
	t.Helper()
	var body string
	if p.fork {
		// A FORK's source launcher (forklauncher.go), with this home already holding the key's
		// build at the native path, so the launch path, not a materialize, is under test.
		keyDir := filepath.Join(p.home, ".local", "state", "yolo", "fork-keys")
		if err := os.MkdirAll(keyDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(keyDir, "tool"), []byte("k\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		body = strings.Join(sourceAgentLauncherSegments(&packdecl.Install{Kind: packdecl.InstallKindSource,
			Bin: "tool", ForkedBy: "forkpack", Produces: []string{".local/bin/tool"},
			Refresh: p.refresh, RefreshTiming: p.timing},
			ForkDelivery{Key: "k"}, p.stamps, keyDir, filepath.Join(p.home, "ws", ".yolo", "receipts.jsonl"),
			"", p.updates, launcherServers{}, nil), "")
	} else if p.native {
		body = nativeAgentLauncher("probe",
			&packdecl.Install{Kind: "native", Bin: "tool",
				InstallerURL: "https://example.invalid/never-fetched.sh", Refresh: p.refresh, RefreshTiming: p.timing,
				Gate: p.gate, ProbeArgs: p.probe},
			p.stamps, filepath.Join(p.home, "ws", ".yolo", "receipts.jsonl"), "",
			p.updates, p.servers, nil)
	} else {
		body = npmAgentLauncher("probe",
			&packdecl.Install{Kind: "npm", Bin: "tool", Package: "tool", Refresh: p.refresh, RefreshTiming: p.timing,
				Gate: p.gate, ProbeArgs: p.probe},
			p.stamps, filepath.Join(p.home, "ws", ".yolo", "receipts.jsonl"),
			p.updates, p.servers, nil)
	}
	if p.heartbeat != "" {
		const baked = "\nREFRESH_HEARTBEAT=60 "
		if !strings.Contains(body, baked) {
			t.Fatalf("the launcher no longer bakes %q, so this cell cannot shorten it", baked)
		}
		body = strings.Replace(body, baked, "\nREFRESH_HEARTBEAT="+p.heartbeat+" ", 1)
	}
	for from, to := range p.bodyPatch {
		if !strings.Contains(body, from) {
			t.Fatalf("the launcher no longer bakes %q, so this cell cannot patch it", from)
		}
		body = strings.Replace(body, from, to, 1)
	}
	if err := os.WriteFile(p.script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

// run executes the launcher with a line of "typed" input on stdin, and returns stdout and
// stderr SEPARATELY — the refresh's output must land on the second, never the first.
func (p *prelaunchProbe) run(t *testing.T, pathPrefix string, env ...string) (stdout, stderr string) {
	t.Helper()
	p.write(t)
	cmd := exec.Command(p.script)
	cmd.Dir = p.home
	path := os.Getenv("PATH")
	if p.path != "" {
		path = p.path
	}
	if pathPrefix != "" {
		path = pathPrefix + ":" + path
	}
	cmd.Env = append([]string{"HOME=" + p.home, "PATH=" + path}, env...)
	cmd.Stdin = strings.NewReader("typed-by-the-user\n")
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if p.stderrWatch != nil {
		cmd.Stderr = io.MultiWriter(&errb, p.stderrWatch)
	}
	if err := cmd.Run(); err != nil {
		t.Fatalf("launcher failed: %v\nstdout:\n%s\nstderr:\n%s", err, out.String(), errb.String())
	}
	return out.String(), errb.String()
}

func (p *prelaunchProbe) logLines(t *testing.T) []string { return logLines(t, p.log) }

func countLine(lines []string, want string) int {
	n := 0
	for _, l := range lines {
		if l == want {
			n++
		}
	}
	return n
}

// assertLaunched: every outcome of the refresh ends in the program being exec'd (§4.1
// invariant 2) — the refresh happens on the way to running it, never instead of it.
func assertProgramLaunched(t *testing.T, log []string, stdout string) {
	t.Helper()
	if countLine(log, "LAUNCH:") != 1 || !strings.Contains(stdout, "RAN") {
		t.Errorf("the launcher must still exec the program:\nlog=%v\nstdout=%q", log, stdout)
	}
}

func backdatePath(t *testing.T, path string, age time.Duration) {
	t.Helper()
	old := time.Now().Add(-age)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
}

// TestPrelaunchRefreshRunsBeforeTheLaunchUnderTheLock is the tier's whole claim, in both
// templates: the declared argv reaches the program, BEFORE the exec, while the lock is held;
// the lock is released afterwards; the refresh's stamp is written beside its lock and nothing
// is written to the machine-global stamp dir; the refresh cannot read the user's terminal; and
// none of its output reaches the launch's stdout.
func TestPrelaunchRefreshRunsBeforeTheLaunchUnderTheLock(t *testing.T) {
	for _, native := range []bool{false, true} {
		t.Run(map[bool]string{false: "npm", true: "native"}[native], func(t *testing.T) {
			p := newPrelaunchProbe(t, native)
			start := time.Now()
			stdout, stderr := p.run(t, "")
			// The lock's heartbeat (and the sleep it leaves behind when stopped) must hold
			// nothing of the launch's: with the output pipe inherited, a piped launch would
			// not see EOF until that sleep ended, a full REFRESH_HEARTBEAT later.
			if elapsed := time.Since(start); elapsed > 30*time.Second {
				t.Errorf("the launch took %s: something the refresh left behind held its output open", elapsed)
			}
			log := p.logLines(t)

			want := []string{"REFRESH", "ARG:update", "ARG:--extensions", "LOCKED", "LAUNCH:"}
			if strings.Join(log, "\n") != strings.Join(want, "\n") {
				t.Fatalf("want the refresh, under the lock, THEN the launch:\n got %q\nwant %q\nstderr:\n%s",
					log, want, stderr)
			}
			if _, err := os.Stat(p.lockPath()); !os.IsNotExist(err) {
				t.Errorf("the lock must be released after the refresh (stat err=%v)", err)
			}
			if _, err := os.Stat(p.stampPath()); err != nil {
				t.Errorf("the refresh must write its own stamp: %v", err)
			}
			if _, err := os.Lstat(filepath.Join(p.stamps, "refresh")); !os.IsNotExist(err) {
				t.Errorf("the refresh wrote into the machine-global stamp dir (err=%v); its "+
					"throttle has its lock's scope (XB-D14)", err)
			}
			if strings.Contains(stdout, "REFRESH-STDOUT") || !strings.Contains(stderr, "REFRESH-STDOUT") {
				t.Errorf("the refresh's stdout must be sent to stderr, so a piped launch "+
					"receives only the program's output:\nstdout=%q\nstderr=%q", stdout, stderr)
			}
			if !strings.Contains(stdout, "RAN") {
				t.Errorf("the launch's own stdout must be untouched: %q", stdout)
			}
		})
	}
}

// TestPrelaunchRefreshIsThrottledByItsStamp: at most once per UPDATE_INTERVAL, and the stamp
// is the refresh's own — a fresh PROGRAM stamp (seeded by the probe) did not suppress it
// above, and a fresh refresh stamp suppresses it here.
func TestPrelaunchRefreshIsThrottledByItsStamp(t *testing.T) {
	p := newPrelaunchProbe(t, false)
	p.run(t, "")
	if countLine(p.logLines(t), "REFRESH") != 1 {
		t.Fatalf("first launch should refresh: %v", p.logLines(t))
	}
	p.run(t, "")
	if n := countLine(p.logLines(t), "REFRESH"); n != 1 {
		t.Errorf("a second launch inside UPDATE_INTERVAL must not refresh again (ran %d times)", n)
	}
	backdatePath(t, p.stampPath(), 2*time.Hour)
	p.run(t, "")
	if n := countLine(p.logLines(t), "REFRESH"); n != 2 {
		t.Errorf("a launch past UPDATE_INTERVAL must refresh again (ran %d times)", n)
	}
}

// TestPrelaunchRefreshFrozenByPolicy: `agent_updates` off bakes UPDATES_ENABLED=0, and the
// refresh consults it first (§3.2 step 3) — not even the stamp is asked.
func TestPrelaunchRefreshFrozenByPolicy(t *testing.T) {
	p := newPrelaunchProbe(t, false)
	p.updates = false
	stdout, _ := p.run(t, "")
	log := p.logLines(t)
	if countLine(log, "REFRESH") != 0 {
		t.Errorf("a pack frozen by agent_updates must not refresh: %v", log)
	}
	if _, err := os.Stat(p.stampPath()); !os.IsNotExist(err) {
		t.Errorf("a frozen launcher must not touch the refresh stamp (err=%v)", err)
	}
	assertProgramLaunched(t, log, stdout)
}

// TestPrelaunchRefreshSkipsWhileAnotherHoldsTheLock is §3.3's non-blocking rule: a fresh lock
// means another jail is writing the store, so this launch says so and proceeds at once — no
// wait, no refresh, and no stamp (the holder writes it when it finishes).
func TestPrelaunchRefreshSkipsWhileAnotherHoldsTheLock(t *testing.T) {
	p := newPrelaunchProbe(t, false)
	if err := os.Mkdir(p.lockPath(), 0o755); err != nil {
		t.Fatal(err)
	}
	stdout, stderr := p.run(t, "")
	log := p.logLines(t)
	if countLine(log, "REFRESH") != 0 {
		t.Errorf("a held lock must suppress the refresh: %v", log)
	}
	if !strings.Contains(stderr, "another refresh holds") {
		t.Errorf("the skip must be said, naming why:\n%s", stderr)
	}
	if _, err := os.Stat(p.lockPath()); err != nil {
		t.Errorf("another holder's lock must be left alone: %v", err)
	}
	if _, err := os.Stat(p.stampPath()); !os.IsNotExist(err) {
		t.Errorf("a skipped refresh must not stamp — that is the holder's job (err=%v)", err)
	}
	assertProgramLaunched(t, log, stdout)
}

// TestPrelaunchRefreshBreaksAStaleLock: a lock older than STALE_LOCK belongs to a holder that
// died (§4.2, "Stale Lock (Crashed Jail)"), so it is removed and the refresh proceeds.
func TestPrelaunchRefreshBreaksAStaleLock(t *testing.T) {
	p := newPrelaunchProbe(t, false)
	if err := os.Mkdir(p.lockPath(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p.lockPath(), probeOwner), []byte("dead-jail\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	backdatePath(t, p.lockPath(), 20*time.Minute)
	stdout, stderr := p.run(t, "")
	log := p.logLines(t)
	if countLine(log, "REFRESH") != 1 || countLine(log, "LOCKED") != 1 {
		t.Errorf("a stale lock must be broken and the refresh run under a new one:\n%v\n%s", log, stderr)
	}
	if _, err := os.Stat(p.lockPath()); !os.IsNotExist(err) {
		t.Errorf("the re-taken lock must be released afterwards (err=%v)", err)
	}
	assertProgramLaunched(t, log, stdout)
}

// TestPrelaunchRefreshReportsAMissingStoreAsSuch is the fail-CLOSED half, and the reason the
// lock returns three answers rather than two: with no store there is no lock to take, the
// refresh must not run unguarded (flock.go's error path fails open; a write must not), the
// store must not be invented in a per-workspace home, and the message must not claim another
// jail is refreshing — the user has a mount to fix, not a wait to sit out.
//
// The report is said at EVERY launch, and that changed with XB-D14: the stamp that used to
// hold it to once an hour lives in the store now, so there is nowhere to write it, and
// writing it anywhere else is the machine-wide throttle that let one jail's failed mount
// silence another's refresh. Writing the stamp must not create the store either.
func TestPrelaunchRefreshReportsAMissingStoreAsSuch(t *testing.T) {
	p := newPrelaunchProbe(t, false)
	if err := os.RemoveAll(p.store); err != nil {
		t.Fatal(err)
	}
	for launch := 1; launch <= 2; launch++ {
		stdout, stderr := p.run(t, "")
		log := p.logLines(t)
		if countLine(log, "REFRESH") != 0 {
			t.Errorf("launch %d: with no store the refresh must not run at all: %v", launch, log)
		}
		if !strings.Contains(stderr, "cannot take the refresh lock") || strings.Contains(stderr, "another refresh holds") {
			t.Errorf("launch %d: a missing store must be reported as that, not as contention:\n%s", launch, stderr)
		}
		// Every stop names the next step: a store the jail mounts is missing because the mount
		// did not happen, and restarting the jail is what mounts it again.
		for _, want := range []string{p.store + " is missing", "restart the jail", "yolo stop"} {
			if !strings.Contains(stderr, want) {
				t.Errorf("launch %d: the missing-store line does not say %q:\n%s", launch, want, stderr)
			}
		}
		if _, err := os.Stat(p.store); !os.IsNotExist(err) {
			t.Errorf("launch %d: the launcher must never create the store itself (err=%v)", launch, err)
		}
		if countLine(log, "LAUNCH:") != launch {
			t.Errorf("launch %d: the launcher must still exec the program:\n%v\nstdout=%q", launch, log, stdout)
		}
	}
	if _, err := os.Lstat(filepath.Join(p.stamps, "refresh")); !os.IsNotExist(err) {
		t.Errorf("the report was throttled in the machine-global stamp dir (err=%v)", err)
	}
}

// TestPrelaunchRefreshNamesWhatToCheckWhenTheStoreIsThereButTheLockIsNot: the store exists, but
// something that is not a directory sits at the lock's path (or the store refuses the write,
// which a root test process cannot produce), so the lock cannot be taken. That is not a missing
// mount, and the line names the command that shows which of the two it is, its paths quoted for
// a home with a space in it.
func TestPrelaunchRefreshNamesWhatToCheckWhenTheStoreIsThereButTheLockIsNot(t *testing.T) {
	for _, native := range []bool{false, true} {
		p := newPrelaunchProbe(t, native)
		if err := os.WriteFile(p.lockPath(), []byte("not a lock\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		stdout, stderr := p.run(t, "")
		log := p.logLines(t)
		if countLine(log, "REFRESH") != 0 {
			t.Errorf("native=%v: with the lock's path occupied the refresh must not run: %v", native, log)
		}
		q, err := exec.Command("bash", "-c", `printf '%q %q' "$1" "$2"`, "_", p.store, p.lockPath()).Output()
		if err != nil {
			t.Fatal(err)
		}
		want := "ls -ld " + string(q)
		if !strings.Contains(stderr, "cannot take the refresh lock") || !strings.Contains(stderr, want) ||
			strings.Contains(stderr, "is missing") {
			t.Errorf("native=%v: an occupied lock path must name %q, and never a missing store:\n%s",
				native, want, stderr)
		}
		assertProgramLaunched(t, log, stdout)
	}
}

// TestPrelaunchRefreshFailureStillLaunches: a non-zero refresh (§4.2, "npm Registry Outage")
// is reported, stamped so the next launch does not retry at once, releases the lock, and the
// program still runs.
func TestPrelaunchRefreshFailureStillLaunches(t *testing.T) {
	p := newPrelaunchProbe(t, false)
	stdout, stderr := p.run(t, "", "FAKE_REFRESH_RC=3")
	log := p.logLines(t)
	if !strings.Contains(stderr, "pre-launch refresh failed (status 3)") {
		t.Errorf("the failure must be reported with its status:\n%s", stderr)
	}
	if _, err := os.Stat(p.stampPath()); err != nil {
		t.Errorf("a failed refresh must still stamp (§4.1 invariant 3): %v", err)
	}
	if _, err := os.Stat(p.lockPath()); !os.IsNotExist(err) {
		t.Errorf("a failed refresh must still release the lock (err=%v)", err)
	}
	assertProgramLaunched(t, log, stdout)
}

// TestPrelaunchRefreshIsBoundedByUpdateTimeout: the refresh is an update act, run through the
// launcher's _bounded (updatebound.go, §3.2 step 4), and an expiry — status 124 — is said as a
// timeout. DETACHED, yolo's bounded no-terminal verb (the stand-in runs the real one) ends a
// refresh that would block for twenty seconds once the shortened bound has passed. With no yolo,
// the FALLBACK is GNU timeout with the same grace, in the terminal's foreground group: a fake
// timeout records its argv, then either runs the command or reports expiry.
func TestPrelaunchRefreshIsBoundedByUpdateTimeout(t *testing.T) {
	t.Run("detached", func(t *testing.T) {
		p := newPrelaunchProbe(t, false)
		p.path = yoloStandIn(t) + ":" + pathWithout(t, "yolo")
		p.bodyPatch = map[string]string{"\nUPDATE_TIMEOUT=60 ": "\nUPDATE_TIMEOUT=1 ", "\nUPDATE_GRACE=5\n": "\nUPDATE_GRACE=1\n"}
		begun := time.Now()
		stdout, stderr := p.run(t, "", "FAKE_REFRESH_WAIT="+filepath.Join(p.home, "never"))
		if took := time.Since(begun); took > 12*time.Second {
			t.Errorf("the refresh outlived its 1s bound by %s", took)
		}
		if !strings.Contains(stderr, "timed out after 1s") {
			t.Errorf("an expired bound must be reported as a timeout:\n%s", stderr)
		}
		if _, err := os.Stat(p.lockPath()); !os.IsNotExist(err) {
			t.Errorf("a timed-out refresh must release the lock (err=%v)", err)
		}
		assertProgramLaunched(t, p.logLines(t), stdout)
	})
	t.Run("timeout-fallback", func(t *testing.T) {
		p := newPrelaunchProbe(t, false)
		p.path = pathWithout(t, "yolo")
		fake := filepath.Join(p.home, "fakebin")
		if err := os.MkdirAll(fake, 0o755); err != nil {
			t.Fatal(err)
		}
		tlog := filepath.Join(p.home, "timeout.log")
		body := "#!/bin/bash\nprintf '%s\\n' \"$*\" >> " + shellQuoteForTest(tlog) + "\n" +
			"if [ -n \"${FAKE_TIMEOUT_EXPIRE:-}\" ]; then exit 124; fi\n" +
			"while [ \"${1#-}\" != \"$1\" ]; do [ \"$1\" = -k ] && shift; shift; done\nshift\nexec \"$@\"\n"
		if err := os.WriteFile(filepath.Join(fake, "timeout"), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
		_, stderr := p.run(t, fake)
		got, _ := os.ReadFile(tlog)
		if want := "--foreground -k 5 60 " + p.realBin + " update --extensions"; !strings.Contains(string(got), want) {
			t.Errorf("the refresh must run under `timeout --foreground -k 5 60`:\n got %q\nwant it to contain %q", got, want)
		}
		if !strings.Contains(stderr, "yolo cannot detach tool's update from this terminal here") {
			t.Errorf("the fallback must say it could not detach the refresh:\n%s", stderr)
		}

		backdatePath(t, p.stampPath(), 2*time.Hour)
		if err := os.Remove(p.log); err != nil {
			t.Fatal(err)
		}
		stdout, stderr := p.run(t, fake, "FAKE_TIMEOUT_EXPIRE=1")
		if !strings.Contains(stderr, "timed out after 60s") {
			t.Errorf("an expired bound must be reported as a timeout:\n%s", stderr)
		}
		if _, err := os.Stat(p.lockPath()); !os.IsNotExist(err) {
			t.Errorf("a timed-out refresh must release the lock (err=%v)", err)
		}
		assertProgramLaunched(t, p.logLines(t), stdout)
	})
}

// TestPrelaunchRefreshLeavesAStolenLockAlone: when another launcher broke this one's lock as
// stale and re-took it mid-refresh, finishing must not delete the new holder's lock — the
// owner token is what tells the two apart.
func TestPrelaunchRefreshLeavesAStolenLockAlone(t *testing.T) {
	p := newPrelaunchProbe(t, false)
	p.run(t, "", "FAKE_REFRESH_STEAL=1")
	got, err := os.ReadFile(filepath.Join(p.lockPath(), probeOwner))
	if err != nil {
		t.Fatalf("the new holder's lock was deleted by the old one: %v", err)
	}
	if strings.TrimSpace(string(got)) != "someone-else" {
		t.Errorf("the new holder's token was overwritten: %q", got)
	}
}

// newSharedStorePair is two jails with their OWN homes, one shared store (a symlink here, a
// bind mount in a real jail) and one machine-global stamp dir; both fakes log to A's file.
// It is the shape of a pack whose refresh lock sits in a machine-scope store, which no shipped
// pack declares since XB-D14: the throttle then has that store's scope too.
func newSharedStorePair(t *testing.T) (a, b *prelaunchProbe) {
	t.Helper()
	return newProbePair(t, true)
}

// newWorkspacePair is two workspaces on one machine: their OWN homes and stores, as pi's `.pi`
// is per workspace, and one machine-global stamp dir (the machine's ~/.cache, which every jail
// mounts); both fakes log to A's file.
func newWorkspacePair(t *testing.T) (a, b *prelaunchProbe) {
	t.Helper()
	return newProbePair(t, false)
}

// newProbePair links B's machine stamp dir to A's, and its store too when shareStore is set.
func newProbePair(t *testing.T, shareStore bool) (a, b *prelaunchProbe) {
	t.Helper()
	a = newPrelaunchProbe(t, false)
	b = newPrelaunchProbe(t, false)
	links := []struct{ from, to string }{{b.stamps, a.stamps}}
	if shareStore {
		links = append(links, struct{ from, to string }{b.store, a.store})
	}
	for _, link := range links {
		if err := os.RemoveAll(link.from); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(link.to, link.from); err != nil {
			t.Fatal(err)
		}
	}
	b.stamps, b.log = a.stamps, a.log
	if err := os.WriteFile(b.realBin, []byte(fakeRefreshProgram(a.log, b.lockPath())), 0o755); err != nil {
		t.Fatal(err)
	}
	return a, b
}

// heldRefresh is a launcher whose refresh is blocked mid-run, holding the lock.
type heldRefresh struct {
	cmd     *exec.Cmd
	out     *bytes.Buffer
	release string
}

// startHeldRefresh starts p's launcher with a refresh that blocks until release is called,
// and returns once the refresh is running — so the lock is held from then on.
func startHeldRefresh(t *testing.T, p *prelaunchProbe) *heldRefresh {
	t.Helper()
	h := &heldRefresh{out: &bytes.Buffer{}, release: filepath.Join(p.home, "release")}
	p.write(t)
	h.cmd = exec.Command(p.script)
	h.cmd.Dir = p.home
	h.cmd.Env = []string{"HOME=" + p.home, "PATH=" + os.Getenv("PATH"), "FAKE_REFRESH_WAIT=" + h.release}
	h.cmd.Stdout, h.cmd.Stderr = h.out, h.out
	if err := h.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.WriteFile(h.release, nil, 0o644); _ = h.cmd.Wait() })
	deadline := time.Now().Add(20 * time.Second)
	for {
		if _, err := os.Stat(h.release + ".started"); err == nil {
			return h
		}
		if time.Now().After(deadline) {
			t.Fatalf("the holder never started its refresh:\n%s", h.out.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// finish lets the held refresh complete and waits for the launcher to exit.
func (h *heldRefresh) finish(t *testing.T) {
	t.Helper()
	if err := os.WriteFile(h.release, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := h.cmd.Wait(); err != nil {
		t.Fatalf("the holder's launcher: %v\n%s", err, h.out.String())
	}
}

// lockAge is how old the lock directory's mtime is — the only thing the stale break reads.
func lockAge(t *testing.T, lock string) time.Duration {
	t.Helper()
	fi, err := os.Stat(lock)
	if err != nil {
		t.Fatalf("the lock is gone: %v", err)
	}
	return time.Since(fi.ModTime())
}

// TestConcurrentJailsRefreshTheSharedStoreOnce is §3.3's scenario end to end: two jails with
// their OWN homes, one shared store (a symlink here, a bind mount in a real jail) and one
// machine-global stamp dir. While jail A's refresh is running, jail B launches: it must not
// refresh, must not wait, and must say why.
func TestConcurrentJailsRefreshTheSharedStoreOnce(t *testing.T) {
	a, b := newSharedStorePair(t)
	held := startHeldRefresh(t, a)

	start := time.Now()
	stdoutB, stderrB := b.run(t, "")
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Errorf("jail B waited %s on A's refresh — a held lock must never block a launch", elapsed)
	}
	if !strings.Contains(stderrB, "another refresh holds") {
		t.Errorf("jail B must say another refresh holds the lock:\n%s", stderrB)
	}
	if !strings.Contains(stdoutB, "RAN") {
		t.Errorf("jail B must launch while A refreshes: %q", stdoutB)
	}

	held.finish(t)
	log := logLines(t, a.log)
	if n := countLine(log, "REFRESH"); n != 1 {
		t.Errorf("the shared store must be refreshed ONCE across both jails, got %d:\n%v", n, log)
	}
	if n := countLine(log, "LAUNCH:"); n != 2 {
		t.Errorf("both jails must launch, got %d:\n%v", n, log)
	}
	// And now the stamp A wrote throttles B: the next launch in B does not refresh.
	b.run(t, "")
	if n := countLine(logLines(t, a.log), "REFRESH"); n != 1 {
		t.Errorf("A's stamp lives in the store the two jails share, so it must throttle B "+
			"too (refreshes=%d)", n)
	}
}

// TestARefreshThrottlesOnlyItsOwnWorkspace is XB-D14's second half
// (docs/design/pi-extension-store-builds.md §6.3): a refresh's stamp and its seen-content
// markers live beside its lock, so for a store that is one workspace's own state they throttle
// that workspace alone. Two workspaces on one machine, the machine's ~/.cache shared and the
// stores not: B refreshes within the hour after A, even for the very content A refreshed with,
// and then B's own stamp throttles B. With the throttle machine-wide, B skipped its refresh
// and left the program's own startup to install what B's content names, unlocked.
func TestARefreshThrottlesOnlyItsOwnWorkspace(t *testing.T) {
	for _, due := range []bool{false, true} {
		t.Run(map[bool]string{false: "hourly stamp", true: "seen content"}[due], func(t *testing.T) {
			a, b := newWorkspacePair(t)
			if due {
				for _, p := range []*prelaunchProbe{a, b} {
					p.refresh.DueOnChange = []string{dueRel}
					p.setWatched(t, `{"packages":["npm:same"]}`)
				}
				// B's own hourly stamp is fresh, so the only thing that can make B due is
				// content B never refreshed with: this cell isolates where the seen
				// markers live from where the stamp does.
				if err := os.MkdirAll(filepath.Dir(b.stampPath()), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(b.stampPath(), nil, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			a.run(t, "")
			if n := countLine(logLines(t, a.log), "REFRESH"); n != 1 {
				t.Fatalf("A's first launch should refresh (refreshes=%d)", n)
			}
			_, stderr := b.run(t, "")
			if n := countLine(logLines(t, a.log), "REFRESH"); n != 2 {
				t.Errorf("workspace B's refresh was throttled by workspace A's (refreshes=%d):\n%s", n, stderr)
			}
			b.run(t, "")
			if n := countLine(logLines(t, a.log), "REFRESH"); n != 2 {
				t.Errorf("B's own stamp must still throttle B within the hour (refreshes=%d)", n)
			}
			for _, p := range []*prelaunchProbe{a, b} {
				if _, err := os.Stat(p.stampPath()); err != nil {
					t.Errorf("no stamp beside %s's lock: %v", filepath.Base(p.home), err)
				}
			}
		})
	}
}

// probeBeat is the heartbeat interval the two heartbeat cells bake in place of
// REFRESH_HEARTBEAT's minute, and probeBeatSeconds is the same interval spelled for sleep(1),
// which takes a fractional operand on GNU coreutils and on macOS alike. The cells wait in
// multiples of it, so each wait still spans a fixed number of beats however short a beat is.
const (
	probeBeat        = 200 * time.Millisecond
	probeBeatSeconds = "0.2"
)

// TestALiveRefreshKeepsItsLockFresh: the stale break reads only the lock's age, so a LIVE
// refresh that runs longer than STALE_LOCK must keep its lock young, or a second launcher
// breaks it and two refreshes write one store. `_bounded` keeps every refresh far shorter than
// STALE_LOCK wherever yolo's bounded verb or timeout(1) is on PATH; a launcher with neither runs
// it unbounded, and a refresh stalled on a slow registry (npm retries a fetch for minutes) then
// outlives it. So the holder heartbeats the lock. Backdating A's lock past STALE_LOCK stands in for the ten
// minutes; the heartbeat must bring it back, and B must then see it HELD, not stale.
func TestALiveRefreshKeepsItsLockFresh(t *testing.T) {
	a, b := newSharedStorePair(t)
	a.heartbeat, b.heartbeat = probeBeatSeconds, probeBeatSeconds
	held := startHeldRefresh(t, a)

	backdatePath(t, a.lockPath(), 11*time.Minute)
	deadline := time.Now().Add(10 * time.Second)
	for lockAge(t, a.lockPath()) > time.Minute {
		if time.Now().After(deadline) {
			t.Fatalf("a live holder's lock stayed %s old: nothing keeps it younger than "+
				"STALE_LOCK, so the next launcher breaks it mid-refresh", lockAge(t, a.lockPath()))
		}
		time.Sleep(100 * time.Millisecond)
	}
	_, stderrB := b.run(t, "")
	if !strings.Contains(stderrB, "another refresh holds") {
		t.Errorf("B must see a live holder's lock as HELD:\n%s", stderrB)
	}
	held.finish(t)
	if n := countLine(logLines(t, a.log), "REFRESH"); n != 1 {
		t.Errorf("two refreshes wrote the store at once (refreshes=%d):\n%v", n, logLines(t, a.log))
	}

	// The heartbeat stops with the refresh. It must not outlive the release and touch the
	// lock path afterwards: a plain `touch` there would CREATE a file, and a file at the
	// lock path reads as "cannot take the lock" to every launch from then on.
	// Five beats, in each of which a heartbeat that outlived the release would touch it.
	time.Sleep(5 * probeBeat)
	if _, err := os.Lstat(a.lockPath()); !os.IsNotExist(err) {
		t.Errorf("something recreated the lock path after its release (err=%v)", err)
	}
}

// TestADeadLaunchersLockStillAges is the other side of the heartbeat: it lives only as long
// as the launcher that took the lock. A launcher killed mid-refresh (SIGKILL, a closed
// terminal) must leave a lock that AGES, or the stale break never fires and no jail on the
// machine refreshes again. The fake refresh is left running on purpose — a killed launcher
// can orphan its child, and the lock belongs to the launcher, not to the child.
func TestADeadLaunchersLockStillAges(t *testing.T) {
	a := newPrelaunchProbe(t, false)
	a.heartbeat = probeBeatSeconds
	held := startHeldRefresh(t, a)
	if err := held.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	// Reap the LAUNCHER only (kill -0 succeeds on an unreaped zombie, which would be this
	// test's artifact, not the heartbeat's). cmd.Wait would also wait for the orphaned fake,
	// which still holds the output pipe; the cleanup releases it.
	if _, err := held.cmd.Process.Wait(); err != nil {
		t.Fatal(err)
	}
	// Five beats for the heartbeat to notice its launcher is gone, then age the lock and
	// give it five more beats in which it must NOT be touched.
	time.Sleep(5 * probeBeat)
	backdatePath(t, a.lockPath(), 11*time.Minute)
	time.Sleep(5 * probeBeat)
	if age := lockAge(t, a.lockPath()); age < 10*time.Minute {
		t.Errorf("a dead launcher's lock was kept fresh (age %s), so it can never go stale", age)
	}
}

// TestNoRefreshDeclaredRendersNoRefresh: a program declaring no refresh must never enter the
// function, whatever the store and stamps look like.
//
// "Never enters" is asserted on what the user SEES, not only on what ran: with the HAS_REFRESH
// gate gone, every program declaring no refresh (every shipped program but pi) takes the lock
// at "$HOME/" — which exists, so it reads as HELD — and prints a false "another refresh holds"
// on every launch while running nothing and stamping nothing. Both halves of the switch are
// pinned: the splice bakes it off, and the function obeys it.
func TestNoRefreshDeclaredRendersNoRefresh(t *testing.T) {
	for _, native := range []bool{false, true} {
		t.Run(map[bool]string{false: "npm", true: "native"}[native], func(t *testing.T) {
			p := newPrelaunchProbe(t, native)
			p.refresh = nil
			stdout, stderr := p.run(t, "")
			log := p.logLines(t)
			if countLine(log, "REFRESH") != 0 {
				t.Errorf("an undeclared refresh ran: %v", log)
			}
			if _, err := os.Stat(p.stampPath()); !os.IsNotExist(err) {
				t.Errorf("an undeclared refresh must write no stamp (err=%v)", err)
			}
			if strings.Contains(strings.ToLower(stderr), "refresh") {
				t.Errorf("a program declaring no refresh must say nothing about one:\n%s", stderr)
			}
			body, err := os.ReadFile(p.script)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(body), "\nHAS_REFRESH=0\n") {
				t.Errorf("a program declaring no refresh must bake the switch off")
			}
			assertProgramLaunched(t, log, stdout)
		})
	}
}

// TestPrelaunchRefreshPassesHostileValuesAsData: the argv words and the lock path are pack
// values, spliced under the contract every other sentinel obeys. A hostile word must reach the
// program as ONE argument, the lock must be taken at the literal path, and neither may run.
func TestPrelaunchRefreshPassesHostileValuesAsData(t *testing.T) {
	p := newPrelaunchProbe(t, false)
	v := hostileValue("-refresh")
	hostileStore := filepath.Join(p.home, "st-"+strings.ReplaceAll(v, "/", "_"))
	if err := os.MkdirAll(hostileStore, 0o755); err != nil {
		t.Fatal(err)
	}
	rel := filepath.Base(hostileStore) + "/lock"
	p.refresh = &packdecl.Refresh{Argv: []string{"update", v}, Lock: rel}
	if err := os.WriteFile(p.realBin,
		[]byte(fakeRefreshProgram(p.log, filepath.Join(p.home, rel))), 0o755); err != nil {
		t.Fatal(err)
	}
	stdout, stderr := p.run(t, "")
	assertNoWitness(t, p.home, "-refresh", "refresh argv / lock")
	log := p.logLines(t)
	if !hasExactArg(log, "ARG:"+v) {
		t.Errorf("the hostile argv word did not arrive as one argument:\n%v\n%s", log, stderr)
	}
	if countLine(log, "LOCKED") != 1 {
		t.Errorf("the lock was not taken at the literal hostile path:\n%v\n%s", log, stderr)
	}
	assertProgramLaunched(t, log, stdout)
}

// TestShippedPiLauncherRefreshesItsExtensions is the CALL-SITE cell: the SHIPPED pi manifest,
// through the loader, the projection and GenerateAgentLaunchers — boot.go's own call — to the
// launcher on disk, run against a fake pi. It goes red if packs/pi stops declaring the refresh,
// if InstallContributions stops carrying it, if the generator stops splicing it, or if the
// template stops calling it. It also pins that the refresh runs under the RESOLVED NODE (pi
// declares a node_floor), since `pi` is a `#!/usr/bin/env node` script the workspace's own
// mise pin would otherwise choose the interpreter for.
func TestShippedPiLauncherRefreshesItsExtensions(t *testing.T) {
	launcher, home, log := shippedPiLauncher(t)
	// A raw entry in pi's settings, so the refresh is worth running (XB-D23).
	writePiSettings(t, home, `{"packages":["npm:pi-web-access","~/.pi/agent/yolo-ext/x"]}`)
	// --help, not --version: a version probe runs no update step (XB-D24).
	cmd := exec.Command(launcher, "--help")
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "PATH=" + os.Getenv("PATH"), "TMPDIR=" + t.TempDir()}
	cmd.Stdin = strings.NewReader("")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("the shipped pi launcher failed: %v\n%s", err, out)
	}
	got := logLines(t, log)
	want := []string{"NODE", "REFRESH", "ARG:update", "ARG:--extensions", "LOCKED", "NODE", "LAUNCH:--help"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("the shipped pi launcher must refresh its extensions under the node it runs "+
			"under and its workspace's lock, then launch:\n got %q\nwant %q\n%s", got, want, out)
	}
	const piLock = ".pi/.yolo-update.lock"
	if _, err := os.Stat(filepath.Join(home, filepath.FromSlash(RefreshStampRel(piLock, "pi")))); err != nil {
		t.Errorf("the refresh stamp is not in the workspace's .pi beside its lock: %v", err)
	}
	if ents, err := os.ReadDir(filepath.Join(home, filepath.FromSlash(RefreshSeenRel(piLock, "pi")))); err != nil || len(ents) != 1 {
		t.Errorf("the refresh's seen marker is not in the workspace's .pi beside its lock "+
			"(%d entries, err=%v)", len(ents), err)
	}
	if _, err := os.Lstat(filepath.Join(home, ".cache", "yolo-agent-stamps", "refresh")); !os.IsNotExist(err) {
		t.Errorf("the refresh throttled itself machine-wide, in ~/.cache (err=%v)", err)
	}
}

// writePiSettings writes pi's user settings in home.
func writePiSettings(t *testing.T, home, body string) {
	t.Helper()
	p := filepath.Join(home, ".pi", "agent", "settings.json")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// THE SHIPPED PI LAUNCHER SKIPS THE REFRESH WHEN NOTHING IS RAW (XB-D23), and A VERSION PROBE RUNS
// NO UPDATE STEP (XB-D24): settings naming only local paths, as a workspace whose every extension
// is a tree yolo built has, refresh nothing, and neither does `pi --version` with a raw entry, while
// a project's own `.pi/settings.json` naming one makes it worth running. Red if packs/pi stops
// declaring `only_if` or `probe_args`, or the templates stop reading either.
func TestTheShippedPiLauncherRefreshesOnlyForRawEntriesAndNeverForAProbe(t *testing.T) {
	for _, tc := range []struct {
		name, settings, project, arg string
		refresh                      bool
	}{
		{"only local paths", `{"packages":["~/.pi/agent/yolo-ext/x"],"httpIdleTimeoutMs":1}`, "", "--help", false},
		{"no settings at all", "", "", "--help", false},
		{"a raw git entry", `{"packages":["git:github.com/o/r"]}`, "", "--help", true},
		{"a raw entry in the project's settings", `{"packages":[]}`, `{"packages":["https://x/y.git"]}`, "--help", true},
		{"a version probe", `{"packages":["npm:a"]}`, "", "--version", false},
		{"the short version probe", `{"packages":["npm:a"]}`, "", "-v", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			launcher, home, log := shippedPiLauncher(t)
			if tc.settings != "" {
				writePiSettings(t, home, tc.settings)
			}
			work := filepath.Join(home, "proj")
			if err := os.MkdirAll(filepath.Join(work, ".pi"), 0o755); err != nil {
				t.Fatal(err)
			}
			if tc.project != "" {
				if err := os.WriteFile(filepath.Join(work, ".pi", "settings.json"), []byte(tc.project), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			// The program's own update is due too: a probe must not run it either.
			backdatePath(t, filepath.Join(home, ".cache", "yolo-agent-stamps", "pi.stamp"), 2*time.Hour)
			fakeNpm := filepath.Join(home, "fakebin")
			if err := os.MkdirAll(fakeNpm, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(fakeNpm, "npm"), []byte("#!/bin/sh\necho NPM \"$@\" >> "+
				shellQuoteForTest(log)+"\necho 0.0.1\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(launcher, tc.arg)
			cmd.Dir = work
			cmd.Env = []string{"HOME=" + home, "PATH=" + fakeNpm + ":" + os.Getenv("PATH"), "TMPDIR=" + t.TempDir()}
			cmd.Stdin = strings.NewReader("")
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("the shipped pi launcher failed: %v\n%s", err, out)
			}
			lines := logLines(t, log)
			if got := countLine(lines, "REFRESH") == 1; got != tc.refresh {
				t.Errorf("refreshed = %v, want %v: %q\n%s", got, tc.refresh, lines, out)
			}
			if countLine(lines, "LAUNCH:"+tc.arg) != 1 {
				t.Errorf("the program was not launched with %s: %q", tc.arg, lines)
			}
			probe := strings.HasPrefix(tc.arg, "-v") || tc.arg == "--version"
			if npm := strings.Contains(strings.Join(lines, "\n"), "NPM "); probe && npm {
				t.Errorf("a version probe asked npm about an update: %q", lines)
			}
		})
	}
}

// shippedPiLauncher generates the SHIPPED pi launcher through GenerateAgentLaunchers, boot.go's
// own call, over a fake pi at its place and a fake node at the resolved floor, with pi's own update
// stamp fresh. It returns the launcher, the home, and the log the fakes write.
func shippedPiLauncher(t *testing.T) (launcher, home, log string) {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not found")
	}
	orig := imageProbeBase
	imageProbeBase = t.TempDir()
	t.Cleanup(func() { imageProbeBase = orig })
	stubImageNode(t, "")
	home = t.TempDir()
	log = filepath.Join(home, "argv.log")
	nodeStore := t.TempDir()
	nodeBin := filepath.Join(nodeStore, "24.0.0", "bin")
	if err := os.MkdirAll(nodeBin, 0o755); err != nil {
		t.Fatal(err)
	}
	fakeNode := "#!/bin/bash\necho \"NODE\" >> " + shellQuoteForTest(log) +
		"\nprintf '%s\\n' \"${NODE_COMPILE_CACHE:-}\" > " + shellQuoteForTest(log+".ncc") + "\nexec \"$@\"\n"
	if err := os.WriteFile(filepath.Join(nodeBin, "node"), []byte(fakeNode), 0o755); err != nil {
		t.Fatal(err)
	}
	oldStore := miseNodeStore
	miseNodeStore = nodeStore
	t.Cleanup(func() { miseNodeStore = oldStore })

	e := NewEnv(map[string]string{"JAIL_HOME": home, "YOLO_PACK_ROOT": stageShippedPacks(t)})
	if err := GenerateAgentLaunchers(e); err != nil {
		t.Fatalf("GenerateAgentLaunchers over the shipped packs: %v", err)
	}
	launcher = filepath.Join(e.LaunchDir(), "pi")
	body, err := os.ReadFile(launcher)
	if err != nil {
		t.Fatalf("no pi launcher: %v", err)
	}
	if !strings.Contains(string(body), "REFRESH_ARGV=(update --extensions)") {
		t.Fatalf("the shipped pi launcher does not bake `update --extensions`:\n%s", body)
	}
	// The first-install race's trigger (pi-git-extension-caching.md): pi's settings name its
	// extensions, so a change there must route their install through the locked refresh.
	if !strings.Contains(string(body), "\nREFRESH_DUE_ON_CHANGE=(.pi/agent/settings.json)\n") ||
		!strings.Contains(string(body), "\nHAS_REFRESH_DUE=1\n") {
		t.Fatalf("the shipped pi launcher does not watch .pi/agent/settings.json")
	}

	// The real pi's place, with pi's own update stamp fresh so the program is not updated. The
	// lock, and the stamp beside it, are in the WORKSPACE's own `.pi` since XB-D14
	// (docs/design/pi-extension-store-builds.md §6.3): pi's npm prefix is per workspace, so this
	// workspace's launches are the only ones its refresh has to exclude, or to throttle.
	realBin := filepath.Join(home, ".npm-global", "bin", "pi")
	lock := filepath.Join(home, ".pi", ".yolo-update.lock")
	for _, d := range []string{filepath.Dir(realBin), filepath.Join(home, ".pi"),
		filepath.Join(home, ".cache", "yolo-agent-stamps")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(realBin, []byte(fakeRefreshProgram(log, lock)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".cache", "yolo-agent-stamps", "pi.stamp"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "\nPROBE_ARGS=(--version -v)\n") ||
		!strings.Contains(string(body), "\nHAS_REFRESH_ONLY_IF=1\n") {
		t.Fatalf("the shipped pi launcher does not bake its probe arguments and its worth-running test")
	}
	return launcher, home, log
}

// TestPrelaunchRefreshSkipsUpdateModeAndReentry: the two launcher paths that never reach the
// exec below the refresh must never reach the refresh either. `yolo pack update`
// (YOLO_PACK_UPDATE=1) refreshes the PROGRAM and exits, and a refresh there would be a second,
// unrequested act; a re-entered launcher (a bare-name call from inside the program's own
// update) must exec at once, or the refresh could recurse into itself.
func TestPrelaunchRefreshSkipsUpdateModeAndReentry(t *testing.T) {
	for _, tc := range []struct{ name, env string }{
		{"update mode", "YOLO_PACK_UPDATE=1"},
		{"re-entry", "_YOLO_LAUNCHER_ACTIVE=:tool"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := newPrelaunchProbe(t, false)
			// A fake npm answering "already current", so update mode has nothing to install.
			fake := filepath.Join(p.home, "fakebin")
			if err := os.MkdirAll(fake, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(fake, "npm"), []byte("#!/bin/sh\necho 0\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			p.run(t, fake, tc.env)
			if n := countLine(p.logLines(t), "REFRESH"); n != 0 {
				t.Errorf("%s must not run the pre-launch refresh (ran %d times): %v", tc.name, n, p.logLines(t))
			}
		})
	}
}
