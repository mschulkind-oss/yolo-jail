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

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
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
//   - WORKSPACE-LOCK: whether either launch printed the per-workspace lock's waiting notice.
//     READ FROM CODE, and the reason this line is not the answer: that lock is taken inside
//     the macos-user orchestrator (macosuser.Run's LockWorkspace), AFTER run.go's native arm
//     has already started the host daemons through startLoopholesDisclosed. So it cannot be
//     what serializes spawn, whatever it prints.
//   - ENDPOINT DURING: what each session's claude-oauth-broker endpoint variable named, and
//     whether the file was readable and its host:port dialable while both sessions were up.
//   - ENDPOINT AFTER: the same probe in the LONGER session after the shorter one's `yolo`
//     process had exited. READ FROM CODE, the prediction: both sessions publish into ONE
//     per-workspace host-services dir (paths.HostServicesDir of one cname), startHostSingleton
//     unlinks the endpoint file before publishing its own, and the macos-user arm's deferred
//     stopLoopholes passes no cname, so it removes that whole dir unconditionally. If so, the
//     survivor's endpoint is gone — a collision over per-workspace state that is not the spawn
//     flock's and that the flock does not prevent.
//
// # Every answer PASSES. Only an experiment not conducted is red
//
// TestAppleContainerReachesHostLoopback's rule. What fails: neither launch running its probe
// (then nothing about concurrency was observed — the single-launch tests are the control and
// say why), or the two sessions never being up at the same time (then there was no
// concurrency to observe). Two failures are about the MACHINE, not the answer: the refusal to
// run beside an undeclared live broker, and a broker the pair spawned still alive after the
// cleanup stopped it.
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
	a := hd10Launch(ws, scriptA, env)
	b := hd10Launch(ws, scriptB, env)
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
		"(taken after the host daemons start, so it is not what serializes spawn)", waited(rA), waited(rB))
	t.Logf("HD10 ENDPOINT DURING: A %s | B %s", hd10Probe(fa, "A_DURING"), hd10Probe(fb, "B_DURING"))
	t.Logf("HD10 ENDPOINT AFTER B EXITED: A saw the exit marker: %s; A %s",
		fa["A_SAW_B_EXIT"], hd10Probe(fa, "A_AFTER"))
	t.Logf("HD10 WARNINGS: A:\n%s\nB:\n%s", hd10Warnings(rA.combined()), hd10Warnings(rB.combined()))
	t.Logf("HD10 VERDICT: %s; the survivor's endpoint after the other session ended: %s. "+
		"Record both in docs/design/host-daemon-ownership.md#OQ-HD10.",
		spawn, hd10AfterVerdict(fa))
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
		`  ep="${` + envVar + `-}"`,
		`  echo "$1_VAR=${ep:-UNSET}"`,
		`  if [ -z "$ep" ]; then return; fi`,
		`  if [ ! -e "$ep" ]; then echo "$1_FILE=ABSENT"; return; fi`,
		`  if ! head -c 1 "$ep" >/dev/null 2>&1; then echo "$1_FILE=UNREADABLE"; return; fi`,
		`  echo "$1_FILE=READABLE"`,
		`  hp="$(cut -d' ' -f1 "$ep")"; echo "$1_HOSTPORT=$hp"`,
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
func hd10Launch(dir, script string, env []string) <-chan hd10Result {
	ch := make(chan hd10Result, 1)
	args := append(jailRunArgs(), "--", "bash", "-lc", script)
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
		return "ONE BROKER — the pair raced to spawn and exactly one daemon was ever seen; " +
			"on this backend the spawn flock is the only lock around that code (the " +
			"workspace lock is taken later), so it or the liveness re-check inside it did the work"
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
			"session is using (one per-workspace host-services dir, removed unconditionally)"
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
}
