package entrypoint

// updatebound_test.go is the platform-neutral half of the UPDATE BOUND's tests (program-delivery.md
// §3.5): the stand-in `yolo` the launchers' update path calls, and the probe every cell drives. The
// cells that need a terminal are in updatebound_linux_test.go.
//
// Every cell drives a FAKE program: a shell script at the launcher's REAL_BIN whose update verb
// (or pre-launch refresh) behaves in one of the ways a vendor updater has, and whose bare run
// prints AGENT_RAN. No agent is started and nothing reaches a network (AGENTS.md, "No agent
// tests").

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/notty"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// asYoloEnv makes this test binary, re-executed through yoloStandIn's script, act as the jail's
// `yolo` for the one verb the update path calls, `internal no-terminal`, which it runs through
// notty.Main exactly as the real binary's dispatch (internal/cli) does. So the cells below run the
// real detach, bound and signal forwarding, not a stub of them.
const asYoloEnv = "YOLO_ENTRYPOINT_TEST_AS_YOLO"

// runAsYolo is the stand-in's whole CLI; any other verb is misuse.
func runAsYolo(args []string) int {
	if len(args) >= 2 && args[0] == "internal" && args[1] == NoTerminalVerb {
		return notty.Main(NoTerminalVerb, args[2:])
	}
	fmt.Fprintf(os.Stderr, "test yolo: no verb %q\n", strings.Join(args, " "))
	return 2
}

// yoloStandIn returns a directory holding a `yolo` that is this test binary in asYoloEnv mode.
func yoloStandIn(t *testing.T) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	body := "#!/bin/sh\n" + asYoloEnv + "=1 exec " + shellQuoteForTest(exe) + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(dir, "yolo"), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// updateBehavior is how a fake vendor program updates: a PRELUDE, run before the program says it
// has started, and an ACTION after it. The shapes the cells use are in updatebound_linux_test.go.
type updateBehavior struct{ prelude, action string }

// boundProbe is one generated launcher and the fake program it manages.
type boundProbe struct {
	home, realBin, script, pidFile, started string
	timeout, grace                          int
}

// boundProbeOpts says which launcher to render and how the fake program updates.
type boundProbeOpts struct {
	npm bool // the npm template; the native one otherwise
	// verb is the update verb; nil declares none.
	verb []string
	// refresh, when set, is a pre-launch refresh, and the program's own update is made not due.
	refresh *packdecl.Refresh
	behave  updateBehavior
	// timeout and grace replace UPDATE_TIMEOUT and UPDATE_GRACE in the rendered launcher.
	timeout, grace int
	// installerURL is the native launcher's installer, which an update re-runs when no verb is
	// declared; empty is a URL nothing fetches.
	installerURL string
}

// newBoundProbe renders the launcher through the production generator and seeds REAL_BIN, so the
// update branch (not the cold install) is what runs.
func newBoundProbe(t *testing.T, o boundProbeOpts) *boundProbe {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not found")
	}
	home := t.TempDir()
	p := &boundProbe{
		home:    home,
		script:  filepath.Join(home, "launch-probetool"),
		pidFile: filepath.Join(home, "update.pid"),
		started: filepath.Join(home, "update.started"),
		timeout: o.timeout,
	}
	stamps := filepath.Join(home, ".cache", "yolo-agent-stamps")
	receipts := filepath.Join(home, "ws", ".yolo", "receipts.jsonl")
	inst := &packdecl.Install{Bin: "probetool", UpdateVerb: o.verb, Refresh: o.refresh}
	var body string
	if o.npm {
		inst.Kind, inst.Package = "npm", "probetool"
		p.realBin = filepath.Join(home, ".npm-global", "bin", "probetool")
		body = npmAgentLauncher(inst, stamps, receipts, true, launcherServers{}, nil)
	} else {
		inst.Kind, inst.InstallerURL = "native", o.installerURL
		if inst.InstallerURL == "" {
			inst.InstallerURL = "https://example.invalid/never-fetched.sh"
		}
		p.realBin = filepath.Join(home, ".local", "bin", "probetool")
		body = nativeAgentLauncher(inst, stamps, receipts, "", true, launcherServers{}, nil)
	}
	if !strings.Contains(body, "\nUPDATE_TIMEOUT=60") {
		t.Fatal("the launcher no longer bakes UPDATE_TIMEOUT=60, so this cell cannot shorten it")
	}
	body = strings.Replace(body, "\nUPDATE_TIMEOUT=60", "\nUPDATE_TIMEOUT="+strconv.Itoa(o.timeout), 1)
	// The grace is patched where the launcher has one; before it had one, there was none.
	if strings.Contains(body, "\nUPDATE_GRACE=5") {
		body = strings.Replace(body, "\nUPDATE_GRACE=5", "\nUPDATE_GRACE="+strconv.Itoa(o.grace), 1)
		p.grace = o.grace
	}
	if err := os.WriteFile(p.script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	q := shellQuoteForTest
	prog := "#!/bin/bash\ncase \"${1:-}\" in\ninstall|refresh)\n" +
		o.behave.prelude + "\n" +
		"    echo \"$$\" > " + q(p.pidFile) + "\n" +
		"    : > " + q(p.started) + "\n" +
		o.behave.action + "\n" +
		"    echo UPDATE_FINISHED >&2\n    exit 0 ;;\nesac\necho AGENT_RAN\n"
	if err := os.MkdirAll(filepath.Dir(p.realBin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.realBin, []byte(prog), 0o755); err != nil {
		t.Fatal(err)
	}
	if o.refresh != nil {
		// Only the refresh is due: the program's own stamp is fresh, and the store exists.
		seedFreshStamp(t, stamps, "probetool")
		if err := os.MkdirAll(filepath.Dir(filepath.Join(home, o.refresh.Lock)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(p.killUpdate)
	return p
}

// killUpdate kills a fake update a failing cell left running, so it does not outlive the test.
func (p *boundProbe) killUpdate() {
	b, err := os.ReadFile(p.pidFile)
	if err != nil {
		return
	}
	if pid, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil && pid > 1 {
		if proc, err := os.FindProcess(pid); err == nil {
			_ = proc.Kill()
		}
	}
}

// EVERY TEMPLATE CARRIES THE ONE BOUND. The three templates each had their own _bounded, the fork's
// a copy of the others', and a fix landing in two of three is the drift this pins: each rendered
// launcher defines _bounded exactly once, and it is updateBoundShellFn's, with its grace.
func TestEveryLauncherTemplateCarriesTheOneUpdateBound(t *testing.T) {
	refresh := &packdecl.Refresh{Argv: []string{"refresh"}, Lock: ".store/.lock"}
	rendered := map[string]string{
		"npm": npmAgentLauncher(&packdecl.Install{Kind: "npm", Bin: "tool", Package: "tool", Refresh: refresh},
			"/stamps", "/receipts.jsonl", true, launcherServers{}, nil),
		"native": nativeAgentLauncher(&packdecl.Install{Kind: "native", Bin: "tool",
			InstallerURL: "https://x.invalid/i.sh", Refresh: refresh},
			"/stamps", "/receipts.jsonl", "", true, launcherServers{}, nil),
		"source": strings.Join(sourceAgentLauncherSegments(&packdecl.Install{Kind: "source", Bin: "tool",
			Refresh: refresh}, ForkDelivery{Key: "k"}, "/stamps", "/keys", "/receipts.jsonl", "", true,
			launcherServers{}, nil), ""),
	}
	for name, body := range rendered {
		if n := strings.Count(body, "\n_bounded() {"); n != 1 {
			t.Errorf("the %s launcher defines _bounded %d times, want once", name, n)
		}
		if !strings.Contains(body, updateBoundShellFn) {
			t.Errorf("the %s launcher does not carry updateBoundShellFn whole", name)
		}
		if !strings.Contains(body, `_shielded '_stop_refresh_heartbeat; _drop_refresh_lock'`) {
			t.Errorf("the %s launcher's pre-launch refresh is not _shielded", name)
		}
	}
}

// WITH NEITHER yolo's VERB NOR timeout(1), the update still runs, with no stdin, and the launcher
// says it is unbounded rather than leaving a hang unexplained (a stock macOS with no yolo on PATH).
func TestAnUpdateWithNoBoundAvailableRunsAndSaysSo(t *testing.T) {
	p := newBoundProbe(t, boundProbeOpts{verb: []string{"install"},
		behave: updateBehavior{"", "if read -r _x; then echo STDIN_READ >&2; fi"}, timeout: 60, grace: 5})
	cmd := exec.Command(p.script)
	cmd.Dir = p.home
	cmd.Env = []string{"HOME=" + p.home, "PATH=" + pathWithout(t, "yolo", "timeout")}
	cmd.Stdin = strings.NewReader("typed-by-the-user\n")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("launcher: %v\n%s", err, out)
	}
	for _, want := range []string{"it runs with no stdin and no time limit", "UPDATE_FINISHED", "AGENT_RAN"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(string(out), "STDIN_READ") {
		t.Errorf("the update read the launcher's stdin:\n%s", out)
	}
}
