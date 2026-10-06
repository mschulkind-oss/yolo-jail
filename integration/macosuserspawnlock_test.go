package integration

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// OQ-HD10'S MEASUREMENT: TWO macos-user LAUNCHES OF ONE WORKSPACE, AT ONCE
// (docs/design/host-daemon-ownership.md#OQ-HD10).
//
// THE QUESTION. Retiring `host_daemon.scope: "host"` (HD-R1) removes the spawn flock,
// paths.HostSingletonLock, along with the singleton it guards. On macos-user there is no
// container, and per-jail identity is per-WORKSPACE (the cname hashes the resolved workspace
// path), so two simultaneous launches of one workspace are one jail as far as every per-jail
// path is concerned. The leaning is "measure two concurrent launches before removing
// anything"; this is that measurement. It asserts nothing about the answer.
//
// WHAT IT RECORDS, one `HD10 …` line each, so one grep of the job log recovers the record:
//
//   - SPAWN: how many claude-oauth-broker processes existed while both launches ran — sampled
//     from the host by pgrep every 100 ms, so a count of one is a LOWER bound on spawns and a
//     count of two is proof of a second spawn. It is only an answer if no broker was alive
//     when the pair started (a live one short-circuits BrokerSpawn before the flock matters).
//     So a live broker is stopped first with `yolo host-daemon stop`, and the line says
//     whether that worked — but ONLY in a run that declared itself the macos-user job
//     (YOLO_TEST_MACOS_USER). Anywhere else the live broker is a developer's real one, and
//     the test refuses rather than kill it (hd10PreBrokerPlan; awsauth_test.go's precedent).
//     Whatever the answer, the broker the pair spawned is stopped at cleanup, because it runs
//     under this test's temp HOME but answers on the machine-wide socket.
//   - WORKSPACE-LOCK: whether either launch printed a lock's waiting notice. READ FROM CODE: a
//     launch first takes its key's ARRIVAL LOCK (internal/cli/run's keeperspawn.go), which the
//     launch that spawns the workspace's keeper hands it and the keeper lets go once its host
//     services are up, so the second launch waits there for the first one's keeper and then joins
//     it. The workspace launch lock comes after, before the content staging (refreshJailBriefings),
//     and is held until the orchestrator releases it before the agent. Both are courtesy locks
//     that warn and continue when they cannot be taken, which is why neither answers OQ-HD10 alone.
//   - ENDPOINT DURING: what each session's claude-oauth-broker endpoint variable named, and
//     whether the file was readable and its host:port dialable while both sessions were up.
//   - ENDPOINT AFTER: the same probe in the LONGER session after the shorter one's `yolo`
//     process had exited. This one is ASSERTED, below. The second run (scheduled macos-user run
//     36319436117, f937d0fd) measured it GONE: both sessions published into ONE per-workspace
//     host-services dir, the second session's front replaced the first's endpoint file, and the
//     shorter session's teardown removed the dir under the survivor. Since the keeper
//     (docs/design/jail-lifetime-last-session-wins.md §9.9) the workspace's macos-user host
//     services are ONE KEEPER'S, for every session of the workspace: so the survivor's endpoint
//     must still answer, the keeper's front, which ends with the last session.
//   - KEEPER: ASSERTED too (hd10KeeperFailure). One launch spawned the keeper and said so, the
//     other joined it and said so, both sandboxes were told the one endpoint file and its token
//     (§8 item 14's pointers and tokens, compared without an agent turn), and after the last
//     session nothing of the keeper is left: its process, and its host-services dir.
//   - SESSION FILES: which session env file each session read ($YOLO_DARWIN_ENV_FILE), and
//     whether the longer session's was still there after the shorter one's `yolo` had exited.
//     ASSERTED too (hd10SessionFileFailure). Until each session named its files by a session id
//     of its own (internal/macosuser/sessionfiles.go) both read <cname>.env, and the shorter
//     session's teardown removed it under the survivor
//     (docs/design/jail-lifetime-last-session-wins.md §9.9.10, row 6).
//
// # Every answer about SPAWN passes. Only an experiment not conducted is red, and the survivor
//
// TestAppleContainerReachesHostLoopback's rule, for the spawn question OQ-HD10 still asks. What
// fails: neither launch running its probe (then nothing about concurrency was observed — the
// single-launch tests are the control and say why), or the two sessions never being up at the
// same time (then there was no concurrency to observe). Two failures are about the MACHINE, not
// the answer: the refusal to run beside an undeclared live broker, and a broker the pair spawned
// still alive after the cleanup stopped it. And one is a DEFECT, the teardown defect the second
// run measured, which is not OQ-HD10's to rule and is fixed: a survivor whose endpoint does not
// answer after the other session exited, or a run that could not observe it (hd10SurvivorFailure).
//
// ONE LAUNCH PAIR, and the pair's sessions coordinate through marker files in the workspace
// (which both the sandbox account and this process can write), so the overlap is arranged
// rather than hoped for: each session waits for the other's `up` marker, the shorter one waits
// until the longer has probed, and the longer waits for this process to say the shorter's
// `yolo` has exited before probing again.
func TestMacosUserTwoConcurrentLaunchesOfOneWorkspace(t *testing.T) {
	requireMacosUser(t)
	// The claude pack is the subject: its claude-oauth-broker is the host-scoped singleton
	// whose spawn the flock serializes.
	packHome(t, `{"packs": ["claude"]}`)
	ws := macosUserWorkspace(t, `{}`)
	syncDir := filepath.Join(ws, "hd10-sync")
	if err := os.Mkdir(syncDir, 0o777); err != nil {
		t.Fatal(err)
	}
	// Mkdir's mode is filtered by the umask; both sessions run as the sandbox account and
	// write here, so the mode is set outright.
	if err := os.Chmod(syncDir, 0o777); err != nil {
		t.Fatal(err)
	}

	pre := hd10BrokerPIDs()
	stopFirst, refusal := hd10PreBrokerPlan(pre, os.Getenv(macosUserDeclareEnv) != "")
	if refusal != "" {
		t.Fatal(refusal)
	}
	// The pair's broker is spawned under this test's isolated HOME — its default credentials
	// path is derived from os.UserHomeDir — yet it answers on the MACHINE-WIDE /tmp socket
	// (paths.HostSingletonSocket, keyed by loophole name alone). Left running, it outlives the
	// temp HOME, and every later launch on this machine adopts a broker whose credentials file
	// has been deleted. Registered after requireMacosUser's HOME isolation, so it runs FIRST,
	// while the HOME it was spawned under still exists.
	t.Cleanup(func() {
		if r := runYoloCLI(t, ws, "host-daemon", "stop", hd10Broker); r.rc != 0 {
			t.Logf("stopping the broker this test's launches spawned: rc=%d\n%s", r.rc, r.combined())
		}
		if left := hd10BrokerPIDs(); len(left) > 0 {
			t.Errorf("a %s is still alive after this test stopped it (pids %v); it was spawned "+
				"under a temp HOME that is about to be deleted, so stop it by hand with "+
				"`yolo host-daemon stop %s` before the next launch adopts it", hd10Broker, left, hd10Broker)
		}
	})
	stopNote := "no broker was alive beforehand"
	if stopFirst {
		r := runCommand(t, ws, []string{"host-daemon", "stop", hd10Broker})
		post := hd10BrokerPIDs()
		stopNote = fmt.Sprintf("a broker was alive beforehand (pids %v); `yolo host-daemon stop %s` "+
			"rc=%d; alive after it: %v", pre, hd10Broker, r.rc, post)
		pre = post
	}
	exercised := len(pre) == 0

	envVar := macosUserServiceEnvVar(hd10Broker)
	scriptA := hd10Script(syncDir, envVar, "A", "B", true)
	scriptB := hd10Script(syncDir, envVar, "B", "A", false)

	// The sampler runs from before the first launch until both have returned.
	samples := &hd10Sampler{seen: map[int]bool{}}
	stopSampling := make(chan struct{})
	var samplerDone sync.WaitGroup
	samplerDone.Add(1)
	go func() {
		defer samplerDone.Done()
		for {
			samples.observe(hd10BrokerPIDs())
			select {
			case <-stopSampling:
				return
			case <-time.After(100 * time.Millisecond):
			}
		}
	}()

	env := hd10LaunchEnv()
	t0 := time.Now()
	a := hd10Launch(t, ws, scriptA, env)
	b := hd10Launch(t, ws, scriptB, env)
	// Each `<X>-exited` marker is written by THIS process once that launch's whole `yolo` —
	// its deferred teardown included — has returned, never by a session. The longer session
	// probes again on B-exited, and every wait in either script also stops on the OTHER
	// session's marker, so a launch that dies early costs its partner nothing but a record.
	var rA, rB hd10Result
	var bExited time.Duration
	for a != nil || b != nil {
		select {
		case rA = <-a:
			a = nil
			_ = os.WriteFile(filepath.Join(syncDir, "A-exited"), []byte("exited\n"), 0o644)
		case rB = <-b:
			b = nil
			bExited = time.Since(t0)
			_ = os.WriteFile(filepath.Join(syncDir, "B-exited"), []byte("exited\n"), 0o644)
		}
	}
	close(stopSampling)
	samplerDone.Wait()
	final := hd10BrokerPIDs()

	ranA := strings.Contains(rA.stdout, "=== END ===")
	ranB := strings.Contains(rB.stdout, "=== END ===")
	if !ranA && !ranB {
		t.Fatalf("HD10: NEITHER launch ran its probe (A rc=%d, B rc=%d), so nothing about "+
			"concurrent launches was observed — this is not an answer to OQ-HD10. The "+
			"single-launch macos-user tests are the control: if they are red too, the backend "+
			"is broken, not concurrent.\nA stdout:\n%s\nA stderr:\n%s\nB stdout:\n%s\nB stderr:\n%s",
			rA.rc, rB.rc, lastLines(rA.stdout, 40), lastLines(rA.stderr, 40),
			lastLines(rB.stdout, 40), lastLines(rB.stderr, 40))
	}
	fa := hd10Fields(section(rA.stdout, "=== A ===", "=== END ==="))
	fb := hd10Fields(section(rB.stdout, "=== B ===", "=== END ==="))
	if fa["A_OVERLAP"] != "yes" && fb["B_OVERLAP"] != "yes" {
		t.Fatalf("HD10: the two sessions were never up at the same time (A saw B: %q, B saw A: %q), "+
			"so there was no concurrency to observe. A launch that refused or failed before its "+
			"session is recorded below; read it — a refusal of the second launch IS an answer, "+
			"and this test should then be taught to record it rather than fail.\n"+
			"A rc=%d:\n%s\nB rc=%d:\n%s", fa["A_OVERLAP"], fb["B_OVERLAP"],
			rA.rc, lastLines(rA.combined(), 40), rB.rc, lastLines(rB.combined(), 40))
	}

	waited := func(r hd10Result) bool {
		return strings.Contains(r.combined(), "Waiting for concurrent jail launch in workspace")
	}
	pidFile, _ := os.ReadFile(paths.HostSingletonPIDFile(hd10Broker))
	spawn := hd10SpawnVerdict(exercised, samples.distinct(), samples.max)

	t.Logf("HD10 LAUNCHES: A rc=%d ran=%v, B rc=%d ran=%v (B's yolo exited %s after the pair started)",
		rA.rc, ranA, rB.rc, ranB, bExited.Round(time.Second))
	t.Logf("HD10 SPAWN: %s. Pre-state: %s. Broker pids seen during the pair: %v (at most %d at "+
		"once); alive after both exited: %v; pid file now: %q",
		spawn, stopNote, samples.distinct(), samples.max, final, strings.TrimSpace(string(pidFile)))
	t.Logf("HD10 WORKSPACE-LOCK: A printed the waiting notice: %v; B printed it: %v "+
		"(a key's arrival lock and the workspace launch lock both print it: a wait means one launch "+
		"waited for the other's keeper to be ready, or for its staging and bootstrap)", waited(rA), waited(rB))
	t.Logf("HD10 ENDPOINT DURING: A %s | B %s | one file for both: %v", hd10Probe(fa, "A_DURING"),
		hd10Probe(fb, "B_DURING"), fa["A_DURING_VAR"] != "" && fa["A_DURING_VAR"] == fb["B_DURING_VAR"])
	t.Logf("HD10 ENDPOINT AFTER B EXITED: A saw the exit marker: %s; A %s",
		fa["A_SAW_B_EXIT"], hd10Probe(fa, "A_AFTER"))
	t.Logf("HD10 WARNINGS: A:\n%s\nB:\n%s", hd10Warnings(rA.combined()), hd10Warnings(rB.combined()))
	t.Logf("HD10 VERDICT: %s; the survivor's endpoint after the other session ended: %s. "+
		"Record both in docs/design/host-daemon-ownership.md#OQ-HD10.",
		spawn, hd10AfterVerdict(fa))
	if failure := hd10SurvivorFailure(fa); failure != "" {
		t.Errorf("%s\nA rc=%d:\n%s\nB rc=%d:\n%s", failure,
			rA.rc, lastLines(rA.combined(), 40), rB.rc, lastLines(rB.combined(), 40))
	}
	t.Logf("HD10 SESSION FILES: A read %s (there: %s) | B read %s (there: %s) | A's after B exited: "+
		"%s (there: %s)", orNone(fa["A_DURING_ENVFILE"]), orNone(fa["A_DURING_ENVFILE_EXISTS"]),
		orNone(fb["B_DURING_ENVFILE"]), orNone(fb["B_DURING_ENVFILE_EXISTS"]),
		orNone(fa["A_AFTER_ENVFILE"]), orNone(fa["A_AFTER_ENVFILE_EXISTS"]))
	if failure := hd10SessionFileFailure(fa, fb); failure != "" {
		t.Errorf("%s\nA rc=%d:\n%s\nB rc=%d:\n%s", failure,
			rA.rc, lastLines(rA.combined(), 40), rB.rc, lastLines(rB.combined(), 40))
	}
	// ONE KEEPER FOR BOTH, AND NOTHING OF IT AFTER THE LAST (JL-D37 to JL-D41).
	t.Logf("HD10 KEEPER: A %s | B %s | endpoint A %s B %s | token A %s B %s",
		hd10KeeperRole(rA.combined()), hd10KeeperRole(rB.combined()), orNone(fa["A_DURING_VAR"]),
		orNone(fb["B_DURING_VAR"]), orNone(fa["A_DURING_TOKENHASH"]), orNone(fb["B_DURING_TOKENHASH"]))
	if failure := hd10KeeperFailure(rA.combined(), rB.combined(), fa, fb); failure != "" {
		t.Errorf("%s\nA rc=%d:\n%s\nB rc=%d:\n%s", failure,
			rA.rc, lastLines(rA.combined(), 40), rB.rc, lastLines(rB.combined(), 40))
	}
	if ep := fa["A_DURING_VAR"]; ep != "" && ep != "UNSET" {
		if _, err := os.Stat(filepath.Dir(ep)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("HD10: the keeper's host-services dir %s outlived the last session (%v)", filepath.Dir(ep), err)
		}
	}
	for _, out := range []string{rA.combined(), rB.combined()} {
		if pid := hd10KeeperPID(out); pid > 0 && processAlive(pid) {
			t.Errorf("HD10: the keeper (pid %d) is still running after the last session", pid)
		}
	}
}

// hd10KeeperRole is what one launch's output says it did about the workspace's keeper: "spawned",
// "joined", or "none".
func hd10KeeperRole(out string) string {
	switch {
	case strings.Contains(out, "keeper: yolo internal daemon jail-keeper will hold this workspace's macos-user host services"):
		return "spawned"
	case strings.Contains(out, "keeper: joined yolo internal daemon jail-keeper (pid "):
		return "joined"
	}
	return "none"
}

// hd10KeeperPID is the pid the spawning launch's "keeper: started, pid N" line names, 0 for none.
func hd10KeeperPID(out string) int {
	const lead = "keeper: started, pid "
	i := strings.Index(out, lead)
	if i < 0 {
		return 0
	}
	n, _ := strconv.Atoi(strings.Fields(out[i+len(lead):] + " ")[0])
	return n
}

// processAlive reports whether pid names a live process.
func processAlive(pid int) bool {
	return exec.Command("kill", "-0", strconv.Itoa(pid)).Run() == nil
}

// hd10KeeperFailure is the experiment's assertion about the keeper: "" when one launch spawned it
// and the other joined it, and both sessions were told the one endpoint file and the one token, and
// otherwise why that is a failure. PURE, and pinned with the verdicts.
func hd10KeeperFailure(outA, outB string, fa, fb map[string]string) string {
	roles := hd10KeeperRole(outA) + "+" + hd10KeeperRole(outB)
	if roles != "spawned+joined" && roles != "joined+spawned" {
		return "HD10: the two launches did not share one keeper (A " + hd10KeeperRole(outA) + ", B " +
			hd10KeeperRole(outB) + "): one must spawn the workspace's macos-user keeper and the other join it " +
			"(docs/design/jail-lifetime-last-session-wins.md JL-D41)"
	}
	a, b := fa["A_DURING_VAR"], fb["B_DURING_VAR"]
	if a == "" || a == "UNSET" || a != b {
		return fmt.Sprintf("HD10: the two sandboxes were told different endpoint files (A %q, B %q); every "+
			"session of a workspace is told its keeper's (JL-D38)", a, b)
	}
	ta, tb := fa["A_DURING_TOKENHASH"], fb["B_DURING_TOKENHASH"]
	if ta == "" || ta != tb {
		return fmt.Sprintf("HD10: the two sandboxes read different tokens from the endpoint file (A %q, B %q)", ta, tb)
	}
	return ""
}

// hd10SessionFileFailure is the experiment's assertion about the per-session files: "" when the
// two sessions read two different env files and the longer session's was still there after the
// shorter one's `yolo` had exited, and otherwise why that is a failure. PURE, and pinned with the
// verdicts. NOT OBSERVED fails, for hd10SurvivorFailure's reason.
func hd10SessionFileFailure(fa, fb map[string]string) string {
	a, b := fa["A_DURING_ENVFILE"], fb["B_DURING_ENVFILE"]
	switch {
	case a == "" || a == "UNSET" || b == "" || b == "UNSET":
		return fmt.Sprintf("HD10: NOT OBSERVED — a session did not name its env file (A %q, B %q), "+
			"so whether two sessions share one was not shown", a, b)
	case a == b:
		return "HD10: both sessions read ONE env file, " + a + ". Each macos-user session must name " +
			"its env file, daemons env file and Seatbelt profile by a session id of its own, or one " +
			"session's launch hands the other's sandbox its environment and its exit removes the " +
			"file under it (internal/macosuser/sessionfiles.go)"
	case fa["A_SAW_B_EXIT"] != "yes":
		return "HD10: NOT OBSERVED — the longer session never saw the shorter one exit, so whether " +
			"its env file survived that exit was not shown"
	case fa["A_AFTER_ENVFILE"] != a:
		return fmt.Sprintf("HD10: the longer session's env file changed during its own session "+
			"(%q, then %q)", a, fa["A_AFTER_ENVFILE"])
	case fa["A_AFTER_ENVFILE_EXISTS"] != "yes":
		return "HD10: the shorter session's exit removed the longer session's env file " + a +
			"; a session's teardown must remove only its own files (internal/macosuser/sessionfiles.go)"
	}
	return ""
}

// hd10SurvivorFailure is the experiment's one assertion about its answer: "" when the longer
// session's endpoint still answered after the shorter session's `yolo` had exited, teardown and
// all, and otherwise why that is a failure. PURE, and pinned with the verdicts.
//
// NOT OBSERVED fails too. It means the longer session never probed after the other's exit, so a
// run that ends there has not shown the survivor keeps a working endpoint, which is the claim.
func hd10SurvivorFailure(fa map[string]string) string {
	verdict := hd10AfterVerdict(fa)
	if strings.HasPrefix(verdict, "STILL WORKS") {
		return ""
	}
	return "HD10: after the shorter session exited, the longer session's " + hd10Broker +
		" endpoint was " + verdict + ". One macos-user session's exit must never remove or " +
		"invalidate an endpoint another live session of the same workspace uses: the workspace's " +
		"keeper holds them until its last session leaves " +
		"(docs/design/jail-lifetime-last-session-wins.md §9.9; docs/design/host-daemon-ownership.md#OQ-HD10)"
}

// hd10Broker is the host-scoped loophole whose spawn this measures.
const hd10Broker = "claude-oauth-broker"

// hd10WaitSeconds bounds each wait in a session's script. The per-launch deadline
// (macosUserTimeout) is the outer bound; this one only has to outlast the partner's
// provisioning, which on a cold runner includes whatever of the floor the first launch left.
const hd10WaitSeconds = 900

// hd10Script is one session's probe. self and other name the two sessions; the longer
// session (A) probes a second time after the shorter one has exited.
//
// Only the endpoint's host:port field is printed — the file's last field is a bearer token.
func hd10Script(dir, envVar, self, other string, longer bool) string {
	// Up to hd10WaitSeconds, and never past the other launch's exit: the other session's
	// provisioning may queue behind this one's on the workspace lock, so the bound is
	// generous, and the exit marker is what keeps a dead partner from costing all of it.
	wait := func(marker string) string {
		return fmt.Sprintf(`w=0; while [ ! -e %[1]s/%[2]s ] && [ ! -e %[1]s/%[3]s-exited ] && `+
			`[ $w -lt %[4]d ]; do sleep 1; w=$((w+1)); done`, dir, marker, other, hd10WaitSeconds)
	}
	lines := []string{
		`probe() {`,
		`  ef="${YOLO_DARWIN_ENV_FILE-}"; echo "$1_ENVFILE=${ef:-UNSET}"`,
		`  if [ -n "$ef" ] && [ -e "$ef" ]; then echo "$1_ENVFILE_EXISTS=yes"; else echo "$1_ENVFILE_EXISTS=no"; fi`,
		`  ep="${` + envVar + `-}"`,
		`  echo "$1_VAR=${ep:-UNSET}"`,
		`  if [ -z "$ep" ]; then return; fi`,
		`  if [ ! -e "$ep" ]; then echo "$1_FILE=ABSENT"; return; fi`,
		`  if ! head -c 1 "$ep" >/dev/null 2>&1; then echo "$1_FILE=UNREADABLE"; return; fi`,
		`  echo "$1_FILE=READABLE"`,
		`  hp="$(cut -d' ' -f1 "$ep")"; echo "$1_HOSTPORT=$hp"`,
		// The token is the file's last field; only a checksum of it is printed.
		`  th="$(awk '{print $NF}' "$ep" | cksum | cut -d' ' -f1)"; echo "$1_TOKENHASH=$th"`,
		`  if [ -n "$hp" ] && (exec 3<>"/dev/tcp/${hp%:*}/${hp##*:}") 2>/dev/null; then echo "$1_DIAL=OK"; else echo "$1_DIAL=FAILED"; fi`,
		`}`,
		fmt.Sprintf(`touch %s/%s-up`, dir, self),
		wait(other + "-up"),
		`echo "=== ` + self + ` ==="`,
		fmt.Sprintf(`[ -e %s/%s-up ] && echo "%s_OVERLAP=yes" || echo "%s_OVERLAP=no"`, dir, other, self, self),
		`probe ` + self + `_DURING`,
		fmt.Sprintf(`touch %s/%s-probed`, dir, self),
	}
	if longer {
		lines = append(lines,
			wait(other+"-exited"),
			fmt.Sprintf(`[ -e %s/%s-exited ] && echo "%s_SAW_B_EXIT=yes" || echo "%s_SAW_B_EXIT=no"`, dir, other, self, self),
			`probe `+self+`_AFTER`,
		)
	} else {
		// The shorter session outlives the longer one's first probe, so both "during" probes
		// really are taken while the other session is up.
		lines = append(lines, wait(other+"-probed"))
	}
	return strings.Join(append(lines, `echo "=== END ==="`), "\n")
}

// hd10Result is one launch's outcome. runCommand cannot be used from a second goroutine (it
// calls t.Fatalf), so the pair is started here with the same environment it builds.
type hd10Result struct {
	rc             int
	stdout, stderr string
	startErr       error
}

func (r hd10Result) combined() string { return r.stdout + r.stderr }

// hd10LaunchEnv is runCommand's launcher environment plus macosUserRunEnv's runtime
// selection, computed once on the test goroutine.
func hd10LaunchEnv() []string {
	env := append(os.Environ(), "TERM=dumb")
	env = append(env, childRepoRootEnv()...)
	env = append(env, autoCaptureEnvForSuite()...)
	return append(env, "YOLO_RUNTIME=macos-user")
}

// hd10Launch starts one macos-user launch of script in dir and delivers its result. A launch
// that overruns macosUserTimeout is killed and reported as rc -1: a wedged pair is a finding,
// and it must not leave the other launch unobserved.
//
// The detached-writer cleanup is registered HERE, on the test goroutine, and runs when the
// test ends — after BOTH launches of the pair, which share one HOME: stopping that HOME's
// broker when the first launch returned would kill it under the second
// (detachedwriters_test.go).
func hd10Launch(t *testing.T, dir, script string, env []string) <-chan hd10Result {
	t.Helper()
	ch := make(chan hd10Result, 1)
	args := append(jailRunArgs(), "--", "bash", "-lc", script)
	awaitDetachedWriters(t, dir, launchHome(env))
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), macosUserTimeout())
		defer cancel()
		cmd := exec.CommandContext(ctx, yoloBin, args...)
		cmd.Dir = dir
		cmd.Env = env
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		err := cmd.Run()
		r := hd10Result{stdout: stdout.String(), stderr: stderr.String()}
		var ee *exec.ExitError
		switch {
		case err == nil:
		case ctx.Err() != nil:
			r.rc = -1
			r.stderr += fmt.Sprintf("\n[HD10: killed after %s]", macosUserTimeout())
		case errors.As(err, &ee):
			r.rc = ee.ExitCode()
		default:
			r.rc, r.startErr = -1, err
			r.stderr += "\n[HD10: did not start: " + err.Error() + "]"
		}
		ch <- r
	}()
	return ch
}

// hd10PreBrokerPlan decides what the test may do about claude-oauth-brokers alive before the
// pair starts: stop them first, or refuse to run. PURE, and pinned under -short
// (TestMacosUserHD10VerdictsNameEachCase).
//
// The broker is a machine-wide singleton — its pid, lock and socket files are /tmp paths keyed
// by loophole name alone (paths.HostSingletonPIDFile and siblings) — so a live one on a
// developer's Mac is the one that developer's real jails refresh Claude OAuth through. Stopping
// it would hand those jails, until someone noticed, a replacement spawned under this test's
// temp HOME. Only a run that DECLARED itself the macos-user job (declared) is on a machine
// whose broker is disposable; everywhere else the answer is to refuse and say how to proceed,
// as awsauth_test.go's fixture does for aws-auth.
func hd10PreBrokerPlan(pre []int, declared bool) (stopFirst bool, refusal string) {
	switch {
	case len(pre) == 0:
		return false, ""
	case declared:
		return true, ""
	}
	return false, fmt.Sprintf("a %[1]s is already running on this machine (pids %[2]v), and this "+
		"experiment needs none alive when its launch pair starts. It is machine-wide (%[3]s), so it "+
		"is almost certainly the one your real jails use, and this test will not kill it for you. "+
		"Stop it with `yolo host-daemon stop %[1]s` (the next launch that wants it starts it again) "+
		"and rerun; or set %[4]s=1 on a disposable machine to let the test stop it itself.",
		hd10Broker, pre, paths.HostSingletonSocket(hd10Broker), macosUserDeclareEnv)
}

// hd10BrokerPIDs lists the live claude-oauth-broker daemons on this host. pgrep exits 1 on
// no match, which is the empty answer rather than a fault.
func hd10BrokerPIDs() []int {
	out, _ := exec.Command("pgrep", "-f", "internal daemon "+hd10Broker).Output()
	return hd10ParsePIDs(string(out))
}

// hd10ParsePIDs reads pgrep's one-pid-per-line output, sorted.
func hd10ParsePIDs(out string) []int {
	var pids []int
	for _, f := range strings.Fields(out) {
		if n, err := strconv.Atoi(f); err == nil {
			pids = append(pids, n)
		}
	}
	sort.Ints(pids)
	return pids
}

// hd10Sampler accumulates what the pgrep samples saw.
type hd10Sampler struct {
	mu   sync.Mutex
	seen map[int]bool
	max  int
}

func (s *hd10Sampler) observe(pids []int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range pids {
		s.seen[p] = true
	}
	if len(pids) > s.max {
		s.max = len(pids)
	}
}

func (s *hd10Sampler) distinct() []int {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []int
	for p := range s.seen {
		out = append(out, p)
	}
	sort.Ints(out)
	return out
}

// hd10SpawnVerdict names what the samples say about spawn. PURE, and pinned under -short
// (TestMacosUserHD10VerdictsNameEachCase).
func hd10SpawnVerdict(exercised bool, distinct []int, maxAtOnce int) string {
	switch {
	case !exercised:
		return "NOT EXERCISED — a broker was still alive when the pair started, so both " +
			"launches found it and neither reached the spawn under the flock"
	case len(distinct) == 0:
		return "NO BROKER — neither launch left a claude-oauth-broker running long enough to " +
			"sample, so the spawn itself failed or was skipped; read the WARNINGS line"
	case len(distinct) == 1:
		return "ONE BROKER — exactly one daemon was ever seen; the workspace's macos-user keeper " +
			"starts the host services for every session of the workspace, and a launch that joins it " +
			"starts none (docs/design/jail-lifetime-last-session-wins.md §9.9), so the pair needed " +
			"one spawn, which the spawn flock (paths.HostSingletonLock) still guards"
	case maxAtOnce >= 2:
		return fmt.Sprintf("TWO SPAWNS AT ONCE — %d brokers were alive together: the spawn was "+
			"NOT serialized, and two daemons share one single-use refresh token", maxAtOnce)
	default:
		return fmt.Sprintf("%d BROKERS IN SEQUENCE — never two at once, so one replaced the "+
			"other: a spawn after a kill, or a daemon that died and was respawned", len(distinct))
	}
}

// hd10AfterVerdict names what the longer session saw once the shorter had exited. PURE, and
// pinned with hd10SpawnVerdict.
func hd10AfterVerdict(fa map[string]string) string {
	switch {
	case fa["A_SAW_B_EXIT"] != "yes":
		return "NOT OBSERVED (the longer session never saw the shorter one exit)"
	case fa["A_AFTER_FILE"] == "ABSENT":
		return "GONE — the shorter session's teardown removed the endpoint file the longer " +
			"session is using (what the second run measured, when both sessions shared one " +
			"per-workspace host-services dir)"
	case fa["A_AFTER_DIAL"] == "OK":
		return "STILL WORKS — the file is there and its host:port answers"
	case fa["A_AFTER_FILE"] == "READABLE":
		return "STALE — the file is there and its host:port no longer answers"
	case fa["A_AFTER_VAR"] == "UNSET" || fa["A_AFTER_VAR"] == "":
		return "NO VARIABLE — the longer session was never told the endpoint"
	default:
		return "UNREADABLE — the file is there and the sandbox account cannot read it"
	}
}

// hd10Fields parses the probe's KEY=VALUE lines.
func hd10Fields(s string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(s, "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(line), "="); ok && k != "" && !strings.ContainsAny(k, " \t") {
			out[k] = v
		}
	}
	return out
}

// hd10Probe renders one probe's fields on one line.
func hd10Probe(f map[string]string, prefix string) string {
	return fmt.Sprintf("var=%s file=%s hostport=%s dial=%s",
		orNone(f[prefix+"_VAR"]), orNone(f[prefix+"_FILE"]), orNone(f[prefix+"_HOSTPORT"]), orNone(f[prefix+"_DIAL"]))
}

func orNone(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// hd10Warnings keeps the launch lines that could carry a collision: warnings, refusals and
// the lock notices.
func hd10Warnings(out string) string {
	var keep []string
	for _, line := range strings.Split(out, "\n") {
		l := strings.ToLower(line)
		if strings.Contains(l, "warning") || strings.Contains(l, "refus") ||
			strings.Contains(l, "waiting for concurrent") || strings.Contains(l, "could not") ||
			strings.Contains(l, hd10Broker) {
			keep = append(keep, "  "+strings.TrimSpace(line))
		}
	}
	if len(keep) == 0 {
		return "  (none)"
	}
	return strings.Join(keep, "\n")
}

// TestMacosUserHD10VerdictsNameEachCase pins the experiment's classifiers from Linux, under
// -short. They decide what the recorded line SAYS, so a case that reads as another would put a
// wrong answer into OQ-HD10's evidence a night later. Named TestMacosUser… so the job that runs
// the experiment also checks them; not behind requireMacosUser.
func TestMacosUserHD10VerdictsNameEachCase(t *testing.T) {
	for _, tc := range []struct {
		name      string
		exercised bool
		distinct  []int
		max       int
		want      string
	}{
		{"a live broker beforehand", false, []int{7}, 1, "NOT EXERCISED"},
		{"nothing ever ran", true, nil, 0, "NO BROKER"},
		{"one daemon", true, []int{7}, 1, "ONE BROKER"},
		{"two at once", true, []int{7, 9}, 2, "TWO SPAWNS AT ONCE"},
		{"a replacement", true, []int{7, 9}, 1, "2 BROKERS IN SEQUENCE"},
	} {
		if got := hd10SpawnVerdict(tc.exercised, tc.distinct, tc.max); !strings.HasPrefix(got, tc.want) {
			t.Errorf("%s: hd10SpawnVerdict = %q, want it to start %q", tc.name, got, tc.want)
		}
	}

	for _, tc := range []struct {
		name   string
		fields string
		want   string
	}{
		{"never saw the exit", "A_SAW_B_EXIT=no", "NOT OBSERVED"},
		{"the file is gone", "A_SAW_B_EXIT=yes\nA_AFTER_VAR=/tmp/x\nA_AFTER_FILE=ABSENT", "GONE"},
		{"still answers", "A_SAW_B_EXIT=yes\nA_AFTER_FILE=READABLE\nA_AFTER_DIAL=OK", "STILL WORKS"},
		{"stale", "A_SAW_B_EXIT=yes\nA_AFTER_FILE=READABLE\nA_AFTER_DIAL=FAILED", "STALE"},
		{"no variable", "A_SAW_B_EXIT=yes\nA_AFTER_VAR=UNSET", "NO VARIABLE"},
		{"unreadable", "A_SAW_B_EXIT=yes\nA_AFTER_VAR=/tmp/x\nA_AFTER_FILE=UNREADABLE", "UNREADABLE"},
	} {
		if got := hd10AfterVerdict(hd10Fields(tc.fields)); !strings.HasPrefix(got, tc.want) {
			t.Errorf("%s: hd10AfterVerdict = %q, want it to start %q", tc.name, got, tc.want)
		}
		// The survivor assertion passes on a working endpoint alone, and every other verdict,
		// NOT OBSERVED included, is a failure that names it.
		failure := hd10SurvivorFailure(hd10Fields(tc.fields))
		switch {
		case tc.want == "STILL WORKS" && failure != "":
			t.Errorf("%s: hd10SurvivorFailure = %q, want \"\" for a survivor whose endpoint answers", tc.name, failure)
		case tc.want != "STILL WORKS" && !strings.Contains(failure, tc.want):
			t.Errorf("%s: hd10SurvivorFailure = %q, want a failure naming %q", tc.name, failure, tc.want)
		}
	}

	// The pre-state rule: a developer's live broker is refused, never killed; only a declared
	// CI run may stop one; and nothing alive means nothing to decide.
	if stop, why := hd10PreBrokerPlan(nil, false); stop || why != "" {
		t.Errorf("no live broker: hd10PreBrokerPlan = (%v, %q), want (false, \"\")", stop, why)
	}
	if stop, why := hd10PreBrokerPlan([]int{7}, false); stop || !strings.Contains(why, "host-daemon stop "+hd10Broker) ||
		!strings.Contains(why, macosUserDeclareEnv) {
		t.Errorf("an undeclared run beside a live broker: hd10PreBrokerPlan = (%v, %q), want a "+
			"refusal naming the stop command and %s, and no stop", stop, why, macosUserDeclareEnv)
	}
	if stop, why := hd10PreBrokerPlan([]int{7}, true); !stop || why != "" {
		t.Errorf("a declared run beside a live broker: hd10PreBrokerPlan = (%v, %q), want (true, \"\")", stop, why)
	}

	for _, tc := range []struct {
		name, a, b string
		want       string
	}{
		{"one file for both", "A_DURING_ENVFILE=/e/x.env\nA_SAW_B_EXIT=yes\nA_AFTER_ENVFILE=/e/x.env\nA_AFTER_ENVFILE_EXISTS=yes",
			"B_DURING_ENVFILE=/e/x.env", "ONE env file"},
		{"removed under the survivor", "A_DURING_ENVFILE=/e/a.env\nA_SAW_B_EXIT=yes\nA_AFTER_ENVFILE=/e/a.env\nA_AFTER_ENVFILE_EXISTS=no",
			"B_DURING_ENVFILE=/e/b.env", "removed the longer session's env file"},
		{"unnamed", "A_DURING_ENVFILE=UNSET", "B_DURING_ENVFILE=/e/b.env", "NOT OBSERVED"},
		{"exit unseen", "A_DURING_ENVFILE=/e/a.env\nA_SAW_B_EXIT=no", "B_DURING_ENVFILE=/e/b.env", "NOT OBSERVED"},
		{"each its own, and kept", "A_DURING_ENVFILE=/e/a.env\nA_SAW_B_EXIT=yes\nA_AFTER_ENVFILE=/e/a.env\nA_AFTER_ENVFILE_EXISTS=yes",
			"B_DURING_ENVFILE=/e/b.env", ""},
	} {
		got := hd10SessionFileFailure(hd10Fields(tc.a), hd10Fields(tc.b))
		if (tc.want == "") != (got == "") || !strings.Contains(got, tc.want) {
			t.Errorf("%s: hd10SessionFileFailure = %q, want %q", tc.name, got, tc.want)
		}
	}
	if s := hd10Script("/s", "V", "A", "B", true); !strings.Contains(s, "_ENVFILE=${ef:-UNSET}") ||
		!strings.Contains(s, "_ENVFILE_EXISTS=") {
		t.Errorf("the probe does not record the session env file:\n%s", s)
	}

	// The keeper assertion: one spawns and one joins, and the two are told one file and one token.
	spawned := "keeper: yolo internal daemon jail-keeper will hold this workspace's macos-user host services (x)"
	joined := "keeper: joined yolo internal daemon jail-keeper (pid 7), which holds"
	same := map[string]string{"A_DURING_VAR": "/tmp/s/x.endpoint", "A_DURING_TOKENHASH": "11"}
	sameB := map[string]string{"B_DURING_VAR": "/tmp/s/x.endpoint", "B_DURING_TOKENHASH": "11"}
	for _, tc := range []struct {
		name, a, b string
		fb         map[string]string
		want       string
	}{
		{"one keeper, one file", spawned, joined, sameB, ""},
		{"two keepers", spawned, spawned, sameB, "did not share one keeper"},
		{"no keeper", "", "", sameB, "did not share one keeper"},
		{"two files", joined, spawned, map[string]string{"B_DURING_VAR": "/tmp/t/x.endpoint", "B_DURING_TOKENHASH": "11"}, "different endpoint files"},
		{"two tokens", spawned, joined, map[string]string{"B_DURING_VAR": "/tmp/s/x.endpoint", "B_DURING_TOKENHASH": "22"}, "different tokens"},
	} {
		got := hd10KeeperFailure(tc.a, tc.b, same, tc.fb)
		if (tc.want == "") != (got == "") || !strings.Contains(got, tc.want) {
			t.Errorf("%s: hd10KeeperFailure = %q, want %q", tc.name, got, tc.want)
		}
	}
	if pid := hd10KeeperPID("x\nkeeper: started, pid 4242\ny"); pid != 4242 {
		t.Errorf("hd10KeeperPID = %d, want 4242", pid)
	}
	if s := hd10Script("/s", "V", "A", "B", true); !strings.Contains(s, "_TOKENHASH=") {
		t.Errorf("the probe does not record the endpoint's token:\n%s", s)
	}

	if got := hd10ParsePIDs("123\n45\n\n"); len(got) != 2 || got[0] != 45 || got[1] != 123 {
		t.Errorf("hd10ParsePIDs = %v, want [45 123]", got)
	}
	// The sessions' probe lines must be what the parser reads: the script's own markers.
	script := hd10Script("/s", "YOLO_SERVICE_X_ENDPOINT", "A", "B", true)
	for _, want := range []string{"=== A ===", "A_OVERLAP", "probe A_DURING", "B-exited", "probe A_AFTER", "=== END ==="} {
		if !strings.Contains(script, want) {
			t.Errorf("the longer session's script lacks %q:\n%s", want, script)
		}
	}
	if s := hd10Script("/s", "V", "B", "A", false); strings.Contains(s, "_AFTER") || !strings.Contains(s, "A-probed") {
		t.Errorf("the shorter session's script must wait for the other's probe and never probe after:\n%s", s)
	}
	// Every wait stops on the partner's exit marker, or a launch that dies early holds the
	// other for the whole bound.
	for _, s := range []string{script, hd10Script("/s", "V", "B", "A", false)} {
		for _, line := range strings.Split(s, "\n") {
			if strings.HasPrefix(line, "w=0;") && !strings.Contains(line, "-exited ]") {
				t.Errorf("a wait does not stop on the partner's exit marker: %s", line)
			}
		}
	}
}

// TestMacosUserHD10GuardsTheMachineWideBroker is the CALL-SITE half of hd10PreBrokerPlan, read
// from the source for warmupgate_test.go's reason: the experiment runs only on a macos-user
// host, so no -short test can drive it, and the rule's own cases pass whether or not the
// experiment consults it. It fails if the experiment stops asking the rule, if it regains an
// unconditional stop of whatever broker is alive, or if it stops cleaning up the broker its
// own pair spawned under a temp HOME.
func TestMacosUserHD10GuardsTheMachineWideBroker(t *testing.T) {
	src, err := os.ReadFile("macosuserspawnlock_test.go")
	if err != nil {
		t.Fatalf("reading macosuserspawnlock_test.go: %v", err)
	}
	body := string(src)
	i := strings.Index(body, "\nfunc TestMacosUserTwoConcurrentLaunchesOfOneWorkspace(")
	if i < 0 {
		t.Fatal("TestMacosUserTwoConcurrentLaunchesOfOneWorkspace is gone; this test has lost its subject")
	}
	j := strings.Index(body[i+1:], "\n}\n")
	if j < 0 {
		t.Fatal("could not find the end of TestMacosUserTwoConcurrentLaunchesOfOneWorkspace")
	}
	fn := body[i : i+1+j]
	if !strings.Contains(fn, "hd10PreBrokerPlan(pre, os.Getenv(macosUserDeclareEnv) != \"\")") {
		t.Error("the experiment no longer asks hd10PreBrokerPlan, with the run's declaration, " +
			"what to do about a live broker — on a developer's Mac it would kill the real one")
	}
	if strings.Contains(fn, "if len(pre) > 0 {") {
		t.Error("the experiment stops a live broker on the bare fact that one is alive again; " +
			"only a declared run may (hd10PreBrokerPlan)")
	}
	// The cleanup BLOCK, not the rest of the function: the pre-state stop spells the same
	// argv, and a search past the block's end would find that one instead.
	cleanup := ""
	if k := strings.Index(fn, "t.Cleanup(func() {"); k >= 0 {
		if e := strings.Index(fn[k:], "\n\t})\n"); e >= 0 {
			cleanup = fn[k : k+e]
		}
	}
	if !strings.Contains(cleanup, `"host-daemon", "stop", hd10Broker`) {
		t.Error("the experiment no longer stops, at cleanup, the broker its launch pair spawned; " +
			"that broker outlives the temp HOME it was spawned under and every later launch on " +
			"the machine adopts it")
	}
	// The survivor ASSERTION, not just its verdict line: hd10SurvivorFailure's cases pass whether
	// or not the experiment consults it, and the teardown fix it checks can only be seen on a Mac.
	if !strings.Contains(fn, "if failure := hd10SurvivorFailure(fa); failure != \"\" {\n\t\tt.Errorf(") {
		t.Error("the experiment no longer fails when the surviving session's endpoint stops " +
			"answering after the other session exits (hd10SurvivorFailure); it would go back to " +
			"recording the macos-user teardown defect instead of catching it")
	}
}

// TestMacosUserSweepsAKilledSessionsFiles: A SESSION KILLED BEFORE ITS TEARDOWN LEAVES ITS
// ROOT-OWNED FILES AND ITS LIVENESS RECORD, AND THE NEXT LAUNCH SWEEPS BOTH
// (internal/macosuser/sessionfiles.go). The first launch writes the env file it was handed into
// the workspace and sleeps; this process SIGKILLs its `yolo`, so no deferred teardown runs, and
// checks, as root, that the session's env file and profile are still there (the control: a kill
// that left nothing shows nothing). A second launch of the same workspace must then remove every
// one of them and the record, while the first session's sandbox, stopped here too, no longer
// holds anything.
func TestMacosUserSweepsAKilledSessionsFiles(t *testing.T) {
	requireMacosUser(t)
	ws := macosUserWorkspace(t, `{}`)
	marker := filepath.Join(ws, "killed-session-envfile")
	// A sleep no other process on the runner runs, so the cleanup's pkill stops only this one.
	const nap = "sleep 3607"
	script := `printf '%s\n' "$YOLO_DARWIN_ENV_FILE" > ` + shquote.Quote(marker) + `; ` + nap
	ctx, cancel := context.WithTimeout(context.Background(), macosUserTimeout())
	defer cancel()
	cmd := exec.CommandContext(ctx, yoloBin, append(jailRunArgs(), "--", "bash", "-lc", script)...)
	cmd.Dir = ws
	cmd.Env = hd10LaunchEnv()
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	awaitDetachedWriters(t, ws, launchHome(cmd.Env))
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting the first launch: %v", err)
	}
	exited := make(chan struct{})
	go func() { _ = cmd.Wait(); close(exited) }()
	stopSandbox := func() {
		_ = runQuiet(time.Minute, "sudo", "-n", "/usr/bin/pkill", "-u", macosuser.SandboxUser, "-f", nap)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		<-exited
		stopSandbox()
	})

	envFile := ""
	for envFile == "" {
		if b, err := os.ReadFile(marker); err == nil && strings.TrimSpace(string(b)) != "" {
			envFile = strings.TrimSpace(string(b))
			break
		}
		select {
		case <-exited:
			t.Fatalf("the first launch exited before its session wrote %s:\nstdout:\n%s\nstderr:\n%s",
				marker, lastLines(stdout.String(), 40), lastLines(stderr.String(), 40))
		case <-ctx.Done():
			t.Fatalf("the first launch did not reach its session within %s", macosUserTimeout())
		case <-time.After(500 * time.Millisecond):
		}
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("SIGKILL of the first launch: %v", err)
	}
	<-exited
	stopSandbox()

	key := strings.TrimSuffix(filepath.Base(envFile), ".env")
	files := macosuser.SessionFilePaths(key, "")
	record := filepath.Join(macosuser.SessionRecordsDir(), key+".lock")
	present := func(p string) bool { return runQuiet(time.Minute, "sudo", "-n", "/bin/test", "-e", p) }
	if !present(envFile) || !present(macosuser.SessionProfilePath(key, "")) {
		t.Fatalf("NOT OBSERVED: the killed session left no env file (%s: %v) or profile (%v), so "+
			"there was nothing for the next launch to sweep", envFile, present(envFile),
			present(macosuser.SessionProfilePath(key, "")))
	}
	if _, err := os.Lstat(record); err != nil {
		t.Fatalf("the killed session left no liveness record at %s (%v), so nothing could tell the "+
			"next launch that its files are an ended session's", record, err)
	}

	r := runMacosUser(t, ws, "true")
	if r.rc != 0 {
		t.Fatalf("the second launch failed (rc %d):\n%s", r.rc, lastLines(r.combined(), 40))
	}
	for _, f := range files {
		if present(f) {
			t.Errorf("the second launch did not sweep the killed session's %s", f)
		}
	}
	if _, err := os.Lstat(record); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the killed session's record %s survived the next launch's sweep (%v)", record, err)
	}
}
