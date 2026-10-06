package integration

import (
	"strconv"
	"strings"
	"testing"
)

// RESOURCES ON macos-user, ON A MAC: the sampled memory guard (internal/macosuser/sessionguard.go)
// and the pids_limit evidence the open question needs.
//
// The guard runs as the sandbox account inside the agent's Seatbelt sandbox, reading the process
// table with /bin/ps. Three facts only a Mac can settle, each a section of one launch:
//
//   - PS: `ps -o rss=` reads a descendant's size from inside the sandbox — the profile denies
//     process-info-pidinfo outside the sandbox and allows it for the same sandbox, and the guard
//     lives on the second half of that;
//   - HOG: a process that allocates past resources.memory is stopped, with the guard's line
//     naming the key, within a bound well under the hog's own sleep;
//   - UNDER: a session under the limit runs to its own exit status, 0.
//
// LIMITS prints kern.maxproc, kern.maxprocperuid and the sandbox's `ulimit -u`: the per-user
// process cap that makes RLIMIT_NPROC collide across sessions on one account, which is the
// reason resources.pids_limit is still read and ignored (DP-D1) and the evidence its open
// question rests on. Printed, never asserted.

// memoryGuardHogBound is how long the hog may live, timed inside the sandbox so launch
// overhead is not in it: two samples (2 s each), the 5 s grace and a slow runner's slack,
// well under the 60 s the hog sleeps if nothing stops it.
const memoryGuardHogBound = 45

func TestMacosUserMemoryGuard(t *testing.T) {
	requireMacosUser(t)
	ws := macosUserWorkspace(t, `{"resources": {"memory": "256m"}}`)

	r := runMacosUser(t, ws, strings.Join([]string{
		`echo "=== PS ==="`,
		`sleep 30 & kid=$!`,
		`echo "RSS=$(/bin/ps -o rss= -p "$kid" | tr -d ' ')"`,
		`echo "ROWS=$(/bin/ps -ax -o pid=,ppid=,rss= | wc -l | tr -d ' ')"`,
		`echo "DASHES=$(/bin/ps -ax -o rss= | grep -c -- '-' || true)"`,
		`kill "$kid"`,
		`echo "=== LIMITS ==="`,
		`sysctl kern.maxproc kern.maxprocperuid 2>&1`,
		`echo "ULIMIT_U=$(ulimit -u)"`,
		`echo "=== END ==="`,
	}, "\n"))
	out := r.combined()
	if r.rc != 0 || !strings.Contains(r.stdout, "=== END ===") {
		t.Fatalf("UNDER: a session well under resources.memory did not run to its own exit "+
			"status 0 (rc %d), so the guard in the launch chain broke an ordinary launch.\n%s", r.rc, out)
	}
	if !strings.Contains(out, "resources.memory (256m) is guarded by sampling") {
		t.Errorf("the launch did not disclose the sampled guard:\n%s", out)
	}
	if strings.Contains(out, "resources are NOT enforced") {
		t.Errorf("memory is guarded, yet the launch still calls it ignored:\n%s", out)
	}
	ps := section(r.stdout, "=== PS ===", "=== LIMITS ===")
	rss := strings.TrimSpace(strings.TrimPrefix(firstLineWith(ps, "RSS="), "RSS="))
	if n, err := strconv.Atoi(rss); err != nil || n <= 0 {
		t.Errorf("PS: `ps -o rss=` of a descendant read %q from inside the sandbox; the guard "+
			"sums exactly this column, so it is blind here.\n%s", rss, ps)
	}
	limits := section(r.stdout, "=== LIMITS ===", "=== END ===")
	stepSummary(t, "### macos-user resources: the memory guard and the pids_limit evidence", "",
		"- PS (inside the sandbox): "+strings.Join(strings.Fields(ps), " "),
		"- LIMITS (pids_limit evidence): "+strings.Join(strings.Fields(limits), " "), "")

	r = runMacosUser(t, ws, strings.Join([]string{
		`echo "=== HOG ==="`,
		`start=$(date +%s)`,
		`perl -e '$s = "x" x (768 * 1024 * 1024); print "HOG-ALLOCATED\n"; sleep 60; print "HOG-SURVIVED\n"'`,
		`rc=$?`,
		`echo "HOG-SECONDS=$(( $(date +%s) - start ))"`,
		`echo "HOG-RC=$rc"`,
		`echo "=== END ==="`,
		`exit $rc`,
	}, "\n"))
	out = r.combined()
	if strings.Contains(out, "HOG-SURVIVED") {
		t.Fatalf("HOG: a process holding 768m in a 256m session slept its full minute: nothing "+
			"stopped it.\n%s", out)
	}
	if !strings.Contains(out, "over resources.memory (256m)") {
		t.Errorf("HOG: no guard line naming resources.memory:\n%s", out)
	}
	if r.rc == 0 || !strings.Contains(r.stdout, "HOG-RC=") || strings.Contains(r.stdout, "HOG-RC=0") {
		t.Errorf("HOG: the session exited %d with the hog's status %q; a stopped hog must leave a "+
			"non-zero status.\n%s", r.rc, firstLineWith(r.stdout, "HOG-RC="), out)
	}
	secs := strings.TrimPrefix(firstLineWith(r.stdout, "HOG-SECONDS="), "HOG-SECONDS=")
	if n, err := strconv.Atoi(secs); err != nil || n > memoryGuardHogBound {
		t.Errorf("HOG: the hog lived %q seconds; a stopped hog should be gone within %ds",
			secs, memoryGuardHogBound)
	}
	stepSummary(t, "- HOG: rc "+strconv.Itoa(r.rc)+" after "+secs+"s; guard line: "+
		firstLineWith(out, "over resources.memory"), "")
}

// firstLineWith returns the first line of s containing marker, trimmed, or "".
func firstLineWith(s, marker string) string {
	for _, l := range strings.Split(s, "\n") {
		if strings.Contains(l, marker) {
			return strings.TrimSpace(l)
		}
	}
	return ""
}
