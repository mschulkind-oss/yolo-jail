package integration

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/supervisor"
)

// TestMacosUserJailDaemonRunsConfinedInTheGuest is steps 3 and 4 of
// docs/design/jail-daemon-on-macos-user-plan.md on the hardware, built on OQ-DP8 and OQ-DP9 of
// docs/design/declaration-parity.md: a bare `"packs": ["claude"]` selects the OpenAI refresh
// adapter, and the macos-user launch must stage a DARWIN yolo-jaild into the sandbox's own
// prefix, start `yolo-jaild supervise` under the session's Seatbelt profile as the sandbox
// account, and have the adapter bind — then leave no supervisor behind when the command exits.
//
// The subject is the OpenAI adapter rather than the plan's hello-daemon: hello-daemon's argv
// names the container's loophole mount, which the guest declines by name (loopholes'
// guestrun.go), and an embedded pack's files are 0444 anyway (OQ-BP5).
//
// WHAT ONLY THIS TEST CAN SEE, every item of it unexecuted before: that the Go-built darwin
// yolo-jaild is signed well enough for the kernel to exec from /var/yolo-jail/bin; that
// sandbox-exec admits the supervisor and the adapter it execs; that the adapter can READ its
// root-owned 0600 daemon env file through the `user:` ACE (its log says "serving on", not
// "idling, serving nothing"); that it can write its log under the sandbox home; and that the
// stop's SIGTERM, relayed by `sudo -n`, ends the supervisor.
//
// THE SUPERVISOR IS LOOKED FOR FROM THE HOST, never from inside the session. The first Mac run
// (36575801495) listed processes from the agent's own shell and found no supervisor, with
// every other assertion green: yolo-jaild resolved to the staged prefix, the launch disclosed
// the daemon, and the adapter's log — which only the supervisor opens, and which lives in this
// run's fresh per-workspace home — said "serving on". Two faults in the PROBE made that
// listing blind:
//
//   - The supervisor is confined (OQ-DP9) by its OWN sandbox-exec, so to the agent's sandbox
//     it is another process, and the profile hides another process from the agent
//     (seatbelt.go's cross-process-procargs-deny and cross-process-pidinfo-deny). The only
//     yolo-jaild line the agent's `ps` returned was the probe's OWN bash, whose argv quotes
//     the script — the same-sandbox case the profile re-allows. The supervisor, the adapter
//     and the root sudo above them are all older than that bash, and none of them appeared
//     ahead of it. That they are hidden rather than merely sorted later is inferred from
//     BSD ps's pid order, not measured; the host-side check below does not depend on it.
//   - That bash line quoted the whole script, "=== END ===" included, so section() cut the
//     listing at the marker INSIDE the argv. parseJailDaemonProbe now refuses a marker that
//     appears twice rather than returning a silently shortened section.
//
// So the host polls its own `ps` for the supervisor while the probe waits, and hands what it
// saw back through a file in the workspace; the probe echoes it, and the probe's own output
// never contains a process listing.
func TestMacosUserJailDaemonRunsConfinedInTheGuest(t *testing.T) {
	requireMacosUser(t)
	packHome(t, `{"packs": ["claude"]}`)
	ws := macosUserWorkspace(t, `{}`)
	seen := filepath.Join(ws, ".yolo-it-jaild-seen")
	uid := sandboxUID(t)

	watch := watchForGuestSupervisor(uid, seen)
	t.Cleanup(func() { watch.stop() }) // a Fatalf below must not leave the poll running
	r := macosUserRunProbe(t, "jail-daemon", ws, jailDaemonProbeScript(seen))
	hostSaw, lastListing := watch.stop()

	logDir := filepath.Join(ws, ".yolo", "home", "local", "state", "yolo-jail-daemons")
	diag := func() string {
		return fmt.Sprintf("\n--- the supervisor's logs: %s (in the sandbox: ~/.local/state/yolo-jail-daemons; "+
			"supervisor.log is the supervisor's own stdout and stderr, and the launch quotes "+
			"sudo's or sandbox-exec's refusal itself)\n%s"+
			"\n--- the host's last listing of %s's processes and of any sudo naming %s:\n%s"+
			"\n--- launch stdout:\n%s\n--- launch stderr:\n%s",
			logDir, dumpDir(logDir), macosuser.SandboxUser, macosuser.JaildName, lastListing,
			r.stdout, r.stderr)
	}

	probe, err := parseJailDaemonProbe(r.stdout)
	if err != nil {
		t.Fatalf("the probe's output does not parse: %v%s", err, diag())
	}
	if want := "JAILD=" + macosuser.GuestBinaryPath(macosuser.JaildName, ""); !strings.Contains(probe.probe, want) {
		t.Errorf("the sandbox does not resolve yolo-jaild to the staged guest prefix (want %q):\n%s%s",
			want, probe.probe, diag())
	}
	if !strings.Contains(r.combined(), "Started openai-auth-broker inside the sandbox") {
		t.Errorf("the launch did not disclose the guest's jail daemon%s", diag())
	}
	// JD-8: the guest wrote its own supervisor.log as the sandbox account, the host user can read
	// it, and it carries the readiness line the launch waited for before saying "Started".
	if b, err := os.ReadFile(macosuser.SupervisorLogPath(ws)); err != nil {
		t.Errorf("the host cannot read the supervisor's log: %v%s", err, diag())
	} else if !strings.Contains(string(b), supervisor.StartedLinePrefix+"openai-auth-broker") {
		t.Errorf("the supervisor's log has no readiness line for the adapter:\n%s%s", b, diag())
	}
	if strings.Contains(probe.log, "spawn failed:") || !strings.Contains(probe.log, "serving on") {
		t.Errorf("the OpenAI adapter did not start and serve in the guest (spawn failed, or no "+
			"caller token read from the daemon env file):\n%s%s", probe.log, diag())
	}
	// The host saw it while the session ran, as the SANDBOX account, with the declared argv.
	if hostSaw == "" {
		t.Errorf("the host saw no `%s supervise` run as %s (uid %s) during the session. The "+
			"probe waited for the host's answer and got: %q%s",
			macosuser.GuestBinaryPath(macosuser.JaildName, ""), macosuser.SandboxUser, uid,
			strings.TrimSpace(probe.seen), diag())
	} else if !strings.Contains(probe.seen, macosuser.GuestBinaryPath(macosuser.JaildName, "")+" supervise") {
		t.Errorf("the host saw the supervisor (%s) but the probe finished before the answer "+
			"reached it (it read %q), so the session's own timing is off%s",
			hostSaw, strings.TrimSpace(probe.seen), diag())
	}

	// Nothing survives the session: the stop is SIGTERM to the supervisor's process group.
	deadline := time.Now().Add(20 * time.Second)
	for {
		left, _ := exec.Command("pgrep", "-u", macosuser.SandboxUser, "-f", "yolo-jaild").Output()
		if strings.TrimSpace(string(left)) == "" {
			break
		}
		if time.Now().After(deadline) {
			t.Errorf("a yolo-jaild of %s survives the session (pids %s)%s", macosuser.SandboxUser,
				strings.TrimSpace(string(left)), diag())
			break
		}
		time.Sleep(500 * time.Millisecond)
	}

	// And the in-jail binary is the SANDBOX's, never on the host's PATH (OQ-DP8).
	if p, err := exec.LookPath("yolo-jaild"); err == nil {
		t.Errorf("yolo-jaild resolves on the HOST's PATH (%s); the host ship set is {yolo}", p)
	}
}

// The probe's section markers, in the order it prints them.
const (
	jdProbeMarker = "=== PROBE ==="
	jdLogMarker   = "=== LOG ==="
	jdSeenMarker  = "=== SEEN ==="
	jdEndMarker   = "=== END ==="
)

// jailDaemonProbeScript is the session's script. It waits for the adapter's log to say
// "serving on", then for the host's answer at seen, and prints both. It prints NO process
// listing: a listing from inside the sandbox cannot see the confined supervisor, and would
// quote this script — markers and all — back into the output.
func jailDaemonProbeScript(seen string) string {
	log := "$HOME/.local/state/yolo-jail-daemons/openai-auth-broker.log"
	return strings.Join([]string{
		`echo "` + jdProbeMarker + `"`,
		`echo "JAILD=$(command -v yolo-jaild || echo NONE)"`,
		`for i in $(seq 1 50); do grep -q 'serving on' "` + log + `" 2>/dev/null && break; sleep 0.2; done`,
		// The host's poll runs every 250 ms; 30 s is for a slow first exec of a freshly
		// staged binary, whose signature the kernel checks once.
		`for i in $(seq 1 150); do [ -s "` + seen + `" ] && break; sleep 0.2; done`,
		`echo "` + jdLogMarker + `"`,
		`cat "` + log + `" 2>&1 || echo "NO LOG"`,
		`echo "` + jdSeenMarker + `"`,
		`cat "` + seen + `" 2>/dev/null || echo "THE HOST SAW NO SUPERVISOR"`,
		`echo "` + jdEndMarker + `"`,
	}, "\n")
}

// jailDaemonProbe is the probe's output, one field per section.
type jailDaemonProbe struct{ probe, log, seen string }

// parseJailDaemonProbe splits the probe's stdout into its sections, and REFUSES output in
// which a marker appears more than once: section() takes the first occurrence of each, so a
// section whose body quotes a marker — as a process listing of the probe's own shell does —
// would be cut short there and read as a result.
func parseJailDaemonProbe(stdout string) (jailDaemonProbe, error) {
	for _, m := range []string{jdProbeMarker, jdLogMarker, jdSeenMarker, jdEndMarker} {
		switch n := strings.Count(stdout, m); {
		case n == 0:
			return jailDaemonProbe{}, fmt.Errorf("no %q in the output", m)
		case n > 1:
			return jailDaemonProbe{}, fmt.Errorf("%q appears %d times, so a section quotes "+
				"a marker and would be cut short at it", m, n)
		}
	}
	return jailDaemonProbe{
		probe: section(stdout, jdProbeMarker, jdLogMarker),
		log:   section(stdout, jdLogMarker, jdSeenMarker),
		seen:  section(stdout, jdSeenMarker, jdEndMarker),
	}, nil
}

// guestSupervisorLine returns the line of a host listing (`ps -axww -o uid=,pid=,command=`)
// that is the guest's supervisor: run by uid, its command beginning with the staged
// yolo-jaild and `supervise`. Beginning with, so the root `sudo` that started it — whose
// argv names the same words further along — is never mistaken for it; the `env`,
// `sandbox-exec` and env-file reader between them exec in place and leave no line.
func guestSupervisorLine(listing, uid string) string {
	want := macosuser.GuestBinaryPath(macosuser.JaildName, "") + " supervise"
	for _, line := range strings.Split(listing, "\n") {
		f := strings.Fields(line)
		if len(f) < 3 || f[0] != uid {
			continue
		}
		cmd := strings.Join(f[2:], " ")
		if cmd == want || strings.HasPrefix(cmd, want+" ") {
			return strings.TrimSpace(line)
		}
	}
	return ""
}

// relevantListing is the part of a host listing worth printing on failure: every process of
// uid, and every line naming yolo-jaild (the root sudo above the supervisor included).
func relevantListing(listing, uid string) string {
	var out []string
	for _, line := range strings.Split(listing, "\n") {
		f := strings.Fields(line)
		if len(f) >= 3 && (f[0] == uid || strings.Contains(line, macosuser.JaildName)) {
			out = append(out, strings.TrimSpace(line))
		}
	}
	if len(out) == 0 {
		return "(none)"
	}
	return strings.Join(out, "\n")
}

// supervisorWatch polls the host's process table during a session.
type supervisorWatch struct {
	quit chan struct{}
	done chan struct{}
	once sync.Once
	mu   sync.Mutex
	saw  string
	last string
}

// watchForGuestSupervisor polls until it sees the supervisor, then writes its line to seen,
// where the session's probe is waiting for it.
//
// `sudo -n ps` first: the sandbox account is another uid, and whether a non-root `ps` may
// read another uid's command line is not something this suite has measured on macOS. The
// macos-user gate has already proved `sudo -n` does not prompt; plain `ps` is the fallback.
func watchForGuestSupervisor(uid, seen string) *supervisorWatch {
	w := &supervisorWatch{quit: make(chan struct{}), done: make(chan struct{})}
	go func() {
		defer close(w.done)
		tick := time.NewTicker(250 * time.Millisecond)
		defer tick.Stop()
		for {
			listing := hostProcessListing()
			line := guestSupervisorLine(listing, uid)
			w.mu.Lock()
			w.last = relevantListing(listing, uid)
			w.mu.Unlock()
			if line != "" {
				_ = os.WriteFile(seen, []byte(line+"\n"), 0o644)
				w.mu.Lock()
				w.saw = line
				w.mu.Unlock()
				return
			}
			select {
			case <-w.quit:
				return
			case <-tick.C:
			}
		}
	}()
	return w
}

// stop ends the poll and returns the supervisor line it saw ("" for none) and its last
// listing of what was relevant.
func (w *supervisorWatch) stop() (saw, last string) {
	w.once.Do(func() { close(w.quit) })
	<-w.done
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.saw, w.last
}

func hostProcessListing() string {
	args := []string{"-axww", "-o", "uid=,pid=,command="}
	if out, err := exec.Command("sudo", append([]string{"-n", "/bin/ps"}, args...)...).Output(); err == nil {
		return string(out)
	}
	out, _ := exec.Command("/bin/ps", args...).Output()
	return string(out)
}

// sandboxUID is the sandbox account's numeric uid, which a `ps` listing prints unabridged
// (BSD ps may truncate a user NAME column).
func sandboxUID(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("id", "-u", macosuser.SandboxUser).Output()
	if err != nil {
		t.Fatalf("id -u %s: %v — the gate admitted a host with no sandbox account", macosuser.SandboxUser, err)
	}
	return strings.TrimSpace(string(out))
}

// dumpDir lists dir's files with their contents, for a failure message.
func dumpDir(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Sprintf("(unreadable: %v)\n", err)
	}
	var b strings.Builder
	for _, e := range entries {
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			fmt.Fprintf(&b, "== %s (unreadable: %v)\n", e.Name(), err)
			continue
		}
		fmt.Fprintf(&b, "== %s\n%s\n", e.Name(), data)
	}
	if b.Len() == 0 {
		return "(empty)\n"
	}
	return b.String()
}
