package integration

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// I/O PRIORITY BUILD STEP 5'S MEASUREMENT: DOES A PROCESS I/O POLICY SET ON THE LAUNCHER SURVIVE
// THE macos-user LAUNCH ARGV? (docs/design/io-priority.md §5.5 and IO-D7;
// docs/design/io-priority-plan.md, step 5.)
//
// ANSWERED: the scheduled run of 2026-10-03 (GitHub Actions run 37121866798) logged IOPOL
// VERDICT: SURVIVES, and step 5 is built on it — TestMacosUserIOPriorityIsApplied below checks
// the launcher's own set. This experiment stays as the inheritance regression underneath: its
// workspace declares no resources.io, so the launcher sets nothing and the wrapper's policy is
// the only one in play.
//
// THE QUESTION. Step 5 would have the macos-user launcher call
// setiopolicy_np(IOPOL_TYPE_DISK, IOPOL_SCOPE_PROCESS, …) and then run macosuser.LaunchArgv:
// `sudo --user=_yolojail /usr/bin/env -i … /usr/bin/sandbox-exec -f <profile> -- …`. The man
// page says only that a new process inherits its parent's policy. Whether the policy survives a
// setuid `sudo` and `sandbox-exec` is the fact IO-D7 ships on, and only a Mac can produce it.
//
// HOW IT IS SET. A wrapper sets IOPOL_THROTTLE on ITSELF, at process scope, and then execs the
// built `yolo` (withLauncherPrefix). That is the state step 5 would put the launcher in before
// LaunchArgv runs, without building step 5. The wrapper and the reader are Python's ctypes over
// libSystem's own setiopolicy_np/getiopolicy_np, so the measurement uses the documented API and
// no syscall number: the host's `python3` sets, and the sandbox's (the floor's) reads.
//
// FOUR READINGS, each one `IOPOL process=<n> thread=<n>`, where IOPOL_THROTTLE is 3:
//
//   - CONTROL: the wrapper execs the reader directly. It must read 3, or the instrument itself
//     does not work and NOTHING was measured. That is the one way this test fails.
//   - SUDO: the wrapper execs `sudo -n --user=_yolojail /usr/bin/env -i <reader>`, the launch's
//     own first hop, alone.
//   - SANDBOX-EXEC: the wrapper execs `sandbox-exec -p '(version 1)(allow default)' <reader>`,
//     the other hop, alone and as the invoking user.
//   - LAUNCH: the wrapper execs `yolo run -- bash -lc '<reader>'` on macos-user, the whole argv.
//
// # Every answer passes. Only an experiment not conducted is red
//
// TestAppleContainerReachesHostLoopback's rule, which macosuserspawnlock_test.go keeps for
// OQ-HD10. The answer is one `IOPOL VERDICT:` line in the log and the step summary, with the two
// single-hop readings beside it so a lost policy names the hop that lost it. A single-hop reading
// that could not be taken (the host python unreadable by the sandbox account, say) is recorded
// as n/a, never failed: the launch reading is the answer, and the hops only explain it.
func TestMacosUserIOPolicyAcrossTheLaunchArgv(t *testing.T) {
	requireMacosUser(t)
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal("no `python3` on this host's PATH, so the wrapper that sets the launcher's I/O " +
			"policy cannot run and NOTHING was measured. A GitHub-hosted macOS runner has one; a " +
			"Mac without one needs it installed for this experiment")
	}
	setter := []string{py, "-c", ioPolicySetterPy}
	reader := []string{py, "-c", ioPolicyReaderPy}

	control, controlOut := ioPolicyHostReading(append(append([]string{}, setter...), reader...))
	if !ioPolicyThrottled(control) {
		t.Fatalf("CONTROL: the wrapper set IOPOL_THROTTLE (%d) and exec'd the reader directly, "+
			"and the reader did not see it, so the instrument does not work on this Mac and "+
			"NOTHING was measured.\n%s", ioPolicyThrottle, controlOut)
	}
	viaSudo, sudoOut := ioPolicyHostReading(append(append(append([]string{}, setter...),
		"sudo", "-n", "--set-home", "--user="+macosuser.SandboxUser, "/usr/bin/env", "-i"), reader...))
	viaSandbox, sandboxOut := ioPolicyHostReading(append(append(append([]string{}, setter...),
		"/usr/bin/sandbox-exec", "-p", "(version 1)(allow default)"), reader...))

	ws := macosUserWorkspace(t, `{}`)
	r := runMacosUser(t, ws, strings.Join([]string{
		`echo "=== IOPOL ==="`,
		`echo "reader $(command -v python3 || echo NONE)"`,
		`python3 -c ` + shquote.Quote(ioPolicyReaderPy) + ` 2>&1`,
		`echo "=== END ==="`,
	}, "\n"), withLauncherPrefix(setter...))
	if !strings.Contains(r.stderr, ioPolicySetterMark) {
		t.Fatalf("the launch's stderr lacks the wrapper's %q line, so yolo did not run under the "+
			"wrapper and the sandbox's reading would describe a launcher with NO policy set: "+
			"NOTHING was measured (withLauncherPrefix, runCommand's launchCommand call).\n"+
			"stdout:\n%s\nstderr:\n%s", ioPolicySetterMark, r.stdout, r.stderr)
	}
	body := section(r.stdout, "=== IOPOL ===", "=== END ===")
	launch := parseIOPolicyLine(body)
	if r.rc != 0 || launch == nil {
		t.Fatalf("the macos-user launch under the I/O-policy wrapper did not report a reading "+
			"(rc %d), so NOTHING was measured about the launch argv. A wrapper failure prints "+
			"setiopolicy_np's errno; a sandbox with no python3 prints `reader NONE`.\n"+
			"stdout:\n%s\nstderr:\n%s", r.rc, r.stdout, r.stderr)
	}

	verdict := ioPolicyVerdict(launch)
	stepSummary(t,
		"### I/O priority step 5: does IOPOL_THROTTLE set on the launcher survive the macos-user argv?",
		"",
		"- "+verdict,
		"- control (wrapper execs the reader): "+ioPolicyDescribe(control, controlOut),
		"- `sudo -n --user="+macosuser.SandboxUser+" /usr/bin/env -i` alone: "+ioPolicyDescribe(viaSudo, sudoOut),
		"- `sandbox-exec` alone, as the invoking user: "+ioPolicyDescribe(viaSandbox, sandboxOut),
		"- the whole launch (the sandbox's "+ioPolicyReaderLine(body)+"): "+ioPolicyDescribe(launch, body),
		"- record it in docs/design/io-priority.md (IO-D7) and io-priority-plan.md step 5",
		"")
}

// TestMacosUserIOPriorityIsApplied is build step 5 itself, on a Mac (io-priority.md §5.5,
// IO-D7): with NO wrapper around the launcher, a declared resources.io reaches the sandboxed
// shell as its process disk policy, because the macos-user launcher sets it on itself before
// the bootstrap. "idle" reads IOPOL_THROTTLE, "low" IOPOL_UTILITY, and an empty object — which
// declares nothing — reads whatever the launcher's own policy is, taken here as the baseline
// rather than assumed to be IOPOL_DEFAULT. TestMacosUserIOPolicyAcrossTheLaunchArgv stays as
// the inheritance regression underneath it.
func TestMacosUserIOPriorityIsApplied(t *testing.T) {
	requireMacosUser(t)
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal("no `python3` on this host's PATH, so the baseline policy cannot be read")
	}
	baseline, baselineOut := ioPolicyHostReading([]string{py, "-c", ioPolicyReaderPy})
	if baseline == nil {
		t.Fatalf("the host could not read its own disk policy, so nothing below compares:\n%s", baselineOut)
	}
	for _, tc := range []struct {
		name, cfg, want string
	}{
		{"idle", `{"resources": {"io": "idle"}}`, fmt.Sprint(ioPolicyThrottle)},
		{"low", `{"resources": {"io": {"priority": "low"}}}`, fmt.Sprint(ioPolicyUtility)},
		{"undeclared", `{"resources": {"io": {}}}`, baseline["process"]},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ws := macosUserWorkspace(t, tc.cfg)
			r := runMacosUser(t, ws, strings.Join([]string{
				`echo "=== IOPOL ==="`,
				`echo "reader $(command -v python3 || echo NONE)"`,
				`python3 -c ` + shquote.Quote(ioPolicyReaderPy) + ` 2>&1`,
				`echo "=== END ==="`,
			}, "\n"))
			body := section(r.stdout, "=== IOPOL ===", "=== END ===")
			got := parseIOPolicyLine(body)
			if r.rc != 0 || got == nil {
				t.Fatalf("the launch did not report a reading (rc %d).\nstdout:\n%s\nstderr:\n%s",
					r.rc, r.stdout, r.stderr)
			}
			if got["process"] != tc.want {
				t.Errorf("resources.io %s: the sandboxed shell reads process=%s, want %s (the "+
					"launcher's own baseline is %s).\nlaunch output:\n%s",
					tc.cfg, got["process"], tc.want, baseline["process"], r.combined())
			}
			if strings.Contains(r.combined(), "was not applied on macos-user") {
				t.Errorf("the launch warned that the policy was not applied:\n%s", r.combined())
			}
			stepSummary(t, "- resources.io "+tc.cfg+": sandbox reads "+ioPolicyDescribe(got, body)+
				" (baseline "+ioPolicyDescribe(baseline, baselineOut)+")")
		})
	}
}

// The policy numbers this file names, from <sys/resource.h>. The reader prints whatever it
// gets; THROTTLE and UTILITY are the two a declaration asserts.
const (
	ioPolicyThrottle = 3 // IOPOL_THROTTLE, "idle"
	ioPolicyUtility  = 4 // IOPOL_UTILITY, "low"
)

// ioPolicySetterMark is the line the setter writes to stderr once the policy is set, just
// before it execs its arguments. The launch reading checks for it, so a launch that did not run
// under the wrapper fails as not measured instead of reading as a lost policy.
const ioPolicySetterMark = "iopol-setter: IOPOL_THROTTLE set, exec'ing the wrapped command"

// ioPolicySetterPy sets IOPOL_THROTTLE on its own process (IOPOL_TYPE_DISK = 0,
// IOPOL_SCOPE_PROCESS = 0), says so on stderr (ioPolicySetterMark) and execs its arguments, or
// exits non-zero naming errno.
const ioPolicySetterPy = `import ctypes, os, sys
try:
    lib = ctypes.CDLL(None, use_errno=True)
    lib.setiopolicy_np
except (OSError, AttributeError):
    lib = ctypes.CDLL("/usr/lib/libSystem.B.dylib", use_errno=True)
if lib.setiopolicy_np(0, 0, 3) != 0:
    sys.exit("setiopolicy_np(IOPOL_TYPE_DISK, IOPOL_SCOPE_PROCESS, IOPOL_THROTTLE) failed: errno %d" % ctypes.get_errno())
sys.stderr.write("` + ioPolicySetterMark + `\n")
sys.stderr.flush()
os.execvp(sys.argv[1], sys.argv[1:])`

// ioPolicyReaderPy prints the disk policy at process scope (0) and thread scope (1) as one
// `IOPOL process=<n> thread=<n>` line; a failed call prints ERR<errno> in its place.
const ioPolicyReaderPy = `import ctypes
try:
    lib = ctypes.CDLL(None, use_errno=True)
    lib.getiopolicy_np
except (OSError, AttributeError):
    lib = ctypes.CDLL("/usr/lib/libSystem.B.dylib", use_errno=True)
def get(scope):
    r = lib.getiopolicy_np(0, scope)
    return str(r) if r >= 0 else "ERR%d" % ctypes.get_errno()
print("IOPOL process=" + get(0) + " thread=" + get(1))`

// ioPolicyHostReading runs argv on the host and parses its reading. The output is returned for
// the record whatever happened; a nil reading means none was printed.
func ioPolicyHostReading(argv []string) (map[string]string, string) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, argv[0], argv[1:]...).CombinedOutput()
	text := strings.TrimSpace(string(out))
	if err != nil {
		text += fmt.Sprintf(" (%v)", err)
	}
	return parseIOPolicyLine(text), text
}

// parseIOPolicyLine finds the reader's `IOPOL process=<n> thread=<n>` line in out and returns
// its fields, or nil when there is none.
func parseIOPolicyLine(out string) map[string]string {
	for _, line := range strings.Split(out, "\n") {
		rest, ok := strings.CutPrefix(strings.TrimSpace(line), "IOPOL ")
		if !ok {
			continue
		}
		got := map[string]string{}
		for _, f := range strings.Fields(rest) {
			if k, v, ok := strings.Cut(f, "="); ok {
				got[k] = v
			}
		}
		if got["process"] != "" {
			return got
		}
	}
	return nil
}

// ioPolicyReaderLine is the probe's `reader <path>` line, the python3 the sandbox resolved.
func ioPolicyReaderLine(body string) string {
	for _, line := range strings.Split(body, "\n") {
		if l := strings.TrimSpace(line); strings.HasPrefix(l, "reader ") {
			return l
		}
	}
	return "reader unknown"
}

// ioPolicyThrottled reports whether a reading saw IOPOL_THROTTLE at process scope.
func ioPolicyThrottled(r map[string]string) bool {
	return r != nil && r["process"] == fmt.Sprint(ioPolicyThrottle)
}

// ioPolicyVerdict is the one line the experiment records about the whole launch.
func ioPolicyVerdict(launch map[string]string) string {
	if ioPolicyThrottled(launch) {
		return "IOPOL VERDICT: SURVIVES — a process-scope IOPOL_THROTTLE set on the launcher reached " +
			"the sandboxed shell through sudo, env -i and sandbox-exec, so step 5 can set it in " +
			"the launcher before LaunchArgv"
	}
	return "IOPOL VERDICT: LOST — the sandboxed shell read process=" + launch["process"] +
		", not IOPOL_THROTTLE (3), so a policy set in the launcher does not reach the agent and " +
		"step 5 needs another place to set it (the single-hop readings say which hop drops it)"
}

// ioPolicyDescribe renders one reading for the summary, or n/a with what the hop printed.
func ioPolicyDescribe(r map[string]string, out string) string {
	if r == nil {
		return "n/a (" + lastLines(out, 3) + ")"
	}
	return "process=" + r["process"] + " thread=" + r["thread"]
}

// TestMacosUserIOPolicyReadingsParse is the -short check of the parser and the verdict, on the
// shapes the reader prints and the ones a failed hop leaves.
func TestMacosUserIOPolicyReadingsParse(t *testing.T) {
	cases := []struct {
		out       string
		process   string
		throttled bool
	}{
		{"IOPOL process=3 thread=0", "3", true},
		{"reader /nix/store/x/bin/python3\nIOPOL process=0 thread=0\n", "0", false},
		{"IOPOL process=ERR1 thread=ERR1", "ERR1", false},
		{"sudo: a password is required (exit status 1)", "", false},
		{"", "", false},
	}
	for _, tc := range cases {
		got := parseIOPolicyLine(tc.out)
		if got["process"] != tc.process {
			t.Errorf("parseIOPolicyLine(%q) process = %q, want %q", tc.out, got["process"], tc.process)
		}
		if ioPolicyThrottled(got) != tc.throttled {
			t.Errorf("ioPolicyThrottled(%q) = %v, want %v", tc.out, !tc.throttled, tc.throttled)
		}
	}
	if v := ioPolicyVerdict(parseIOPolicyLine("IOPOL process=3 thread=0")); !strings.Contains(v, "SURVIVES") {
		t.Errorf("a throttled launch reading is not reported as surviving: %s", v)
	}
	if v := ioPolicyVerdict(parseIOPolicyLine("IOPOL process=0 thread=0")); !strings.Contains(v, "LOST") ||
		!strings.Contains(v, "process=0") {
		t.Errorf("an unthrottled launch reading is not reported as lost with its value: %s", v)
	}
	if d := ioPolicyDescribe(nil, "sudo: a password is required"); !strings.HasPrefix(d, "n/a (") {
		t.Errorf("a hop with no reading is not described as n/a: %s", d)
	}
}

// TestMacosUserIOPolicyLauncherPrefixWrapsTheBinary pins the harness half the experiment rests
// on, THROUGH runCommand rather than launchCommand alone: with withLauncherPrefix the wrapper
// runs first and execs yolo with its arguments, which is what the setter's
// `os.execvp(sys.argv[1], sys.argv[1:])` expects; and with no prefix yolo runs itself. A test of
// launchCommand alone passed with runCommand's call to it deleted, and the experiment then
// recorded a launcher with no policy set as a LOST verdict. yoloBin is a stub script here, and
// HOME a temp dir, because runCommand's cleanup takes this HOME's keeper locks.
func TestMacosUserIOPolicyLauncherPrefixWrapsTheBinary(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh on PATH")
	}
	t.Setenv("HOME", resolvedTempDir(t))
	dir := resolvedTempDir(t)
	stub := filepath.Join(dir, "yolo")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\necho \"YOLO-STUB $*\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	saved := yoloBin
	yoloBin = stub
	t.Cleanup(func() { yoloBin = saved })

	r := runCommand(t, dir, []string{"run", "--", "true"},
		withLauncherPrefix(sh, "-c", `echo "WRAPPER $#"; exec "$@"`, "wrapper"))
	if r.rc != 0 || r.stdout != "WRAPPER 4\nYOLO-STUB run -- true\n" {
		t.Errorf("runCommand with a launcher prefix printed %q (rc %d), want the wrapper's line "+
			"and then yolo's with its own arguments: the prefix did not wrap the binary", r.stdout, r.rc)
	}
	r = runCommand(t, dir, []string{"run"})
	if r.rc != 0 || r.stdout != "YOLO-STUB run\n" {
		t.Errorf("runCommand with no prefix printed %q (rc %d), want yolo's line alone", r.stdout, r.rc)
	}
}
