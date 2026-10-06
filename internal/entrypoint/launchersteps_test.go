package entrypoint

// launchersteps_test.go RUNS the launcher fixes of docs/design/pi-extension-store-builds.md §14
// step 1 in every launcher template: the refresh skipped when nothing is raw (XB-D23), every update
// step skipped for a version probe while a cold install still runs (XB-D24), and the tree gate
// first, before any install, update or refresh (XB-D25). Same fake program as
// prelaunchrefresh_test.go: nothing reaches a network and no agent starts.

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// cmd is the written launcher run with args, HOME and PATH as run sets them, in the home.
func (p *prelaunchProbe) cmd(pathPrefix string, args ...string) *exec.Cmd {
	cmd := exec.Command(p.script, args...)
	cmd.Dir = p.home
	path := os.Getenv("PATH")
	if pathPrefix != "" {
		path = pathPrefix + ":" + path
	}
	cmd.Env = []string{"HOME=" + p.home, "PATH=" + path}
	cmd.Stdin = strings.NewReader("")
	return cmd
}

// runArgs runs the written launcher with args and returns its two streams, failing on a failure.
func (p *prelaunchProbe) runArgs(t *testing.T, pathPrefix string, args ...string) (string, string) {
	t.Helper()
	cmd := p.cmd(pathPrefix, args...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		t.Fatalf("launcher failed: %v\n%s\n%s", err, out.String(), errb.String())
	}
	return out.String(), errb.String()
}

// fakeNpmLogging is an npm that logs each call into log and answers "0.0.1" to a view.
func fakeNpmLogging(t *testing.T, dir, log string) string {
	t.Helper()
	bin := filepath.Join(dir, "fakenpm")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "#!/bin/sh\necho \"NPM $*\" >> " + shellQuoteForTest(log) + "\necho 0.0.1\n"
	if err := os.WriteFile(filepath.Join(bin, "npm"), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

// A VERSION PROBE runs no hourly update and no pre-launch refresh, in both templates; any other
// first argument runs both. Red if a template stops reading _YOLO_PROBE at either step, or the
// generator stops baking Install.Probe.
func TestAVersionProbeRunsNoUpdateStep(t *testing.T) {
	for _, native := range []bool{false, true} {
		for _, arg := range []string{"--version", "chat"} {
			p := newPrelaunchProbe(t, native)
			p.probe = []string{"--version", "-v"}
			backdatePath(t, filepath.Join(p.stamps, "tool.stamp"), 2*time.Hour) // the program's update is due
			npm := fakeNpmLogging(t, p.home, p.log)
			p.write(t)
			stdout, stderr := p.runArgs(t, npm, arg)
			lines := p.logLines(t)
			refreshed := countLine(lines, "REFRESH") == 1
			updated := strings.Contains(strings.Join(lines, "\n"), "NPM view") || strings.Contains(stderr, "Updating")
			if native {
				updated = strings.Contains(stderr, "Updating") || strings.Contains(stderr, "never-fetched")
			}
			probe := arg == "--version"
			if refreshed == probe || updated == probe {
				t.Errorf("native=%v %s: refreshed %v, updated %v, want both %v\nlog=%q\nstderr=%s", native, arg,
					refreshed, updated, !probe, lines, stderr)
			}
			if countLine(lines, "LAUNCH:"+arg) != 1 || !strings.Contains(stdout, "RAN") {
				t.Errorf("native=%v %s: the program was not launched: %q", native, arg, lines)
			}
		}
	}
}

// loggingYolo is a yolo that logs its argv into log and answers refresh-servers with success; any
// other call with a `--` runs what follows it, as the no-terminal verb does, and one the source
// launcher's materialize makes puts a program at ~/.local/bin/<bin> that logs as fakeRefreshProgram.
func loggingYolo(t *testing.T, dir, log, bin string) string {
	t.Helper()
	d := filepath.Join(dir, "loggingyolo")
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `#!/bin/bash
printf 'YOLO %s\n' "$*" >> ` + shellQuoteForTest(log) + `
case "${2:-}" in
    refresh-servers) exit 0 ;;
    capture-materialize)
        mkdir -p "$HOME/.local/bin"
        printf '#!/bin/bash\necho "LAUNCH:$*" >> %s\necho RAN\n' ` + shellQuoteForTest(shellQuoteForTest(log)) + ` > "$HOME/.local/bin/` + bin + `"
        chmod +x "$HOME/.local/bin/` + bin + `"
        exit 0 ;;
esac
while [ "$#" -gt 0 ] && [ "$1" != "--" ]; do shift; done
[ "$#" -gt 0 ] || exit 0
shift
exec "$@"
`
	if err := os.WriteFile(filepath.Join(d, "yolo"), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return d
}

// A VERSION PROBE RUNS NO MCP SERVER REFRESH (XB-D24), in all three templates, and any other first
// argument runs it. Red if a template drops `[ "$_YOLO_PROBE" != 1 ]` from its SERVERS_ENABLED
// guard.
func TestAVersionProbeRunsNoServerRefresh(t *testing.T) {
	servers := launcherServers{npm: "some-mcp-server"}
	for _, arg := range []string{"--version", "chat"} {
		want := arg != "--version"
		for _, native := range []bool{false, true} {
			p := newPrelaunchProbe(t, native)
			p.probe = []string{"--version", "-v"}
			p.servers = servers
			p.write(t)
			if out, err := p.cmd(loggingYolo(t, p.home, p.log, "tool"), arg).CombinedOutput(); err != nil {
				t.Fatalf("native=%v %s: launcher failed: %v\n%s", native, arg, err, out)
			}
			log := strings.Join(p.logLines(t), "\n")
			if got := strings.Contains(log, "YOLO internal refresh-servers"); got != want {
				t.Errorf("native=%v %s: refreshed the servers %v, want %v:\n%s", native, arg, got, want, log)
			}
			if !strings.Contains(log, "LAUNCH:"+arg) {
				t.Errorf("native=%v %s: the program was not launched:\n%s", native, arg, log)
			}
		}
		// The source launcher, a fork's build materialized from the capture store.
		home := t.TempDir()
		log := filepath.Join(home, "argv.log")
		inst := &packdecl.Install{Kind: packdecl.InstallKindSource, Bin: "tool", ForkedBy: "fork", Source: "git+https://example.invalid/x?ref=main",
			Produces: []string{".local/bin/tool"}, Probe: []string{"--version", "-v"}}
		body := strings.Join(sourceAgentLauncherSegments(inst, ForkDelivery{Key: "k1"}, filepath.Join(home, "stamps"),
			filepath.Join(home, "keys"), filepath.Join(home, "receipts.jsonl"), t.TempDir(), true, servers, nil), "")
		script := filepath.Join(home, "tool-launcher")
		if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(script, arg)
		cmd.Dir = home
		cmd.Env = []string{"HOME=" + home, "PATH=" + loggingYolo(t, home, log, "tool") + ":" + os.Getenv("PATH")}
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("source %s: launcher failed: %v\n%s", arg, err, out)
		}
		data, _ := os.ReadFile(log)
		if got := strings.Contains(string(data), "YOLO internal refresh-servers"); got != want {
			t.Errorf("source %s: refreshed the servers %v, want %v:\n%s", arg, got, want, data)
		}
		if !strings.Contains(string(data), "LAUNCH:"+arg) {
			t.Errorf("source %s: the program was not launched:\n%s", arg, data)
		}
	}
}

// A COLD INSTALL STILL RUNS FOR A PROBE: with nothing installed, nothing would answer it.
func TestAVersionProbeStillInstallsAColdHome(t *testing.T) {
	p := newPrelaunchProbe(t, false)
	p.probe = []string{"--version"}
	if err := os.Remove(p.realBin); err != nil {
		t.Fatal(err)
	}
	npm := fakeNpmLogging(t, p.home, p.log)
	p.write(t)
	cmd := p.cmd(npm, "--version")
	_ = cmd.Run()
	if !strings.Contains(strings.Join(p.logLines(t), "\n"), "NPM install -g") {
		t.Errorf("a version probe in a cold home did not install: %q", p.logLines(t))
	}
}

// THE REFRESH RUNS ONLY WHEN WORTH IT (XB-D23), in both templates: no listed file holding a listed
// string skips it; a home file or a project file, relative to where the program starts, holding
// one runs it. Red if a template stops calling _refresh_worth, or the generator stops baking OnlyIf.
func TestTheRefreshRunsOnlyWhenAListedFileHoldsAListedString(t *testing.T) {
	for _, native := range []bool{false, true} {
		for _, tc := range []struct {
			name, home, project string
			want                bool
		}{
			{"nothing listed exists", "", "", false},
			{"a home file without the string", `{"packages":["~/x"]}`, "", false},
			{"a home file with it", `{"packages":["npm:x"]}`, "", true},
			{"a project file with it", "", `{"packages":["npm:x"]}`, true},
		} {
			p := newPrelaunchProbe(t, native)
			p.refresh.OnlyIf = &packdecl.RefreshOnlyIf{Files: []string{".tool/settings.json"},
				ProjectFiles: []string{".tool/settings.json"}, Contains: []string{`"npm:`, `"git:`}}
			work := filepath.Join(p.home, "proj")
			for dir, body := range map[string]string{p.home: tc.home, work: tc.project} {
				if body == "" {
					continue
				}
				if err := os.MkdirAll(filepath.Join(dir, ".tool"), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, ".tool", "settings.json"), []byte(body), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.MkdirAll(work, 0o755); err != nil {
				t.Fatal(err)
			}
			p.write(t)
			cmd := p.cmd("", "chat")
			cmd.Dir = work
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("launcher failed: %v\n%s", err, out)
			}
			if got := countLine(p.logLines(t), "REFRESH") == 1; got != tc.want {
				t.Errorf("native=%v %s: refreshed %v, want %v", native, tc.name, got, tc.want)
			}
		}
	}
}

// THE WORTH TEST READS WITH THE SHELL ALONE (XB-D41): a `cat` the user blocked
// (security.blocked_tools, which ~/.yolo/bin/block puts first on PATH) is a shim that exits 127,
// and reading the file through it would answer "no listed string" and skip every refresh for
// good, and reading the lock's owner token through it would leave every lock taken. Red if
// _refresh_file_holds or _refresh_lock_owner reads a file through any program on PATH.
func TestTheRefreshWorthTestNeedsNoProgramOnPath(t *testing.T) {
	for _, native := range []bool{false, true} {
		p := newPrelaunchProbe(t, native)
		p.refresh.OnlyIf = &packdecl.RefreshOnlyIf{Files: []string{".tool/settings.json"}, Contains: []string{`"npm:`}}
		if err := os.WriteFile(filepath.Join(p.store, "settings.json"), []byte(`{"packages":["npm:x"]}`), 0o644); err != nil {
			t.Fatal(err)
		}
		block := filepath.Join(p.home, "block")
		if err := os.MkdirAll(block, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(block, "cat"),
			[]byte("#!/bin/sh\necho 'cat is blocked' >&2\nexit 127\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		p.write(t)
		if out, err := p.cmd(block, "chat").CombinedOutput(); err != nil {
			t.Fatalf("native=%v: launcher failed: %v\n%s", native, err, out)
		}
		if countLine(p.logLines(t), "REFRESH") != 1 {
			t.Errorf("native=%v: with cat blocked the refresh did not run, though the settings name npm:x: %q",
				native, p.logLines(t))
		}
		// And the lock it took is released: the owner-token check reads with the shell too.
		if _, err := os.Lstat(p.lockPath()); !os.IsNotExist(err) {
			t.Errorf("native=%v: with cat blocked the refresh left its lock behind (err=%v)", native, err)
		}
	}
}

// THE TREE GATE RUNS FIRST (XB-D25), in every template: a launch it stops runs no install, no
// update and no refresh — here a cold home, a due update and a due refresh all at once — and a
// version probe is not stopped. Red if the gate's splice moves back below the install.
func TestTheTreeGateStopsBeforeAnyInstallUpdateOrRefresh(t *testing.T) {
	for _, native := range []bool{false, true} {
		p := newPrelaunchProbe(t, native)
		p.gate = "  ⚠ extension x/y has no build in this jail"
		if err := os.Remove(p.realBin); err != nil {
			t.Fatal(err)
		}
		npm := fakeNpmLogging(t, p.home, p.log)
		p.write(t)
		cmd := p.cmd(npm, "chat")
		out, err := cmd.CombinedOutput()
		if err == nil || !strings.Contains(string(out), "extension x/y has no build in this jail") {
			t.Fatalf("native=%v: the gate did not stop the launch (err %v):\n%s", native, err, out)
		}
		if lines := p.logLines(t); len(lines) != 0 || strings.Contains(string(out), "Installing") ||
			strings.Contains(string(out), "never-fetched") {
			t.Errorf("native=%v: the stopped launch paid for an install or a refresh first: %q\n%s", native, lines, out)
		}
	}
	// The source launcher: the gate stops before the fork's build is materialized.
	home, launcher, fakeBin, argvLog := forkLauncherWithTrees(t, `{"pi":{"key":"k1"}}`, stoppedTree)
	if _, rc := runForkLauncher(t, home, launcher, fakeBin); rc == 0 {
		t.Fatal("the source launcher was not stopped")
	}
	if data, _ := os.ReadFile(argvLog); strings.Contains(string(data), "capture-materialize") {
		t.Errorf("the source launcher materialized the fork's build before its gate stopped it:\n%s", data)
	}
	// Every template carries the gate above its first install step.
	for name, body := range map[string]string{
		"npm":    npmAgentLauncher("p", &packdecl.Install{Kind: "npm", Bin: "t", Package: "t"}, "/s", "/r", true, launcherServers{}, nil),
		"native": nativeAgentLauncher("p", &packdecl.Install{Kind: "native", Bin: "t", InstallerURL: "https://x/i.sh"}, "/s", "/r", "", true, launcherServers{}, nil),
		"source": strings.Join(sourceAgentLauncherSegments(&packdecl.Install{Bin: "t"}, ForkDelivery{Key: "k"}, "/s", "/k", "/r", "", true, launcherServers{}, nil), ""),
	} {
		gate := strings.Index(body, "\nTREE_GATE=")
		first := strings.Index(body, "\n    # Cold home:")
		if name == "source" {
			first = strings.Index(body, "# NO KEY, NO PROGRAM.")
		}
		if gate < 0 || first < 0 || gate > first || gate > strings.Index(body, "_prelaunch_refresh || true") {
			t.Errorf("%s: the tree gate (at %d) is not above the first install step (at %d)", name, gate, first)
		}
	}
}
