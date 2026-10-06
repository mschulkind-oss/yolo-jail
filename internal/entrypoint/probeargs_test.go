package entrypoint

// probeargs_test.go RUNS the version probe (probeargs.go; pi-extension-store-builds.md XB-D24,
// XB-D51) through the production generators, in all three templates: an invocation whose first
// argument the pack declares as a probe runs none of the launcher's steps before the exec, and
// waits for no lock.
//
// Every cell drives fakes: the program is prelaunchrefresh_test.go's fake (it logs its argv, and
// "REFRESH" when run as the refresh), `npm` and `yolo` are scripts that log every call. No agent
// is started and nothing reaches a network (AGENTS.md, "No agent tests"). None needs a terminal,
// so macOS's check runs them under its /bin/bash 3.2.
//
// Each cell pairs the probe with an invocation that is not one, in the same home, and asserts the
// steps DO run there: a probe cell that saw nothing would otherwise pass against a launcher whose
// steps had all been deleted.

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

// probeHarness is one generated launcher whose every pre-exec step is due, and the fakes that
// record which of them ran.
type probeHarness struct {
	home, script, realBin, fakeBin string
	progLog, yoloLog, npmLog       string
	template                       string // "npm", "native" or "fork"
	inst                           packdecl.Install
	// patch replaces baked literals in the rendered launcher (old → new).
	patch map[string]string
}

// newProbeHarness seeds a fake program at REAL_BIN with no update stamp (so the program's own
// update is due), the refresh's store with no refresh stamp (so the refresh is due), a fake npm
// and a fake yolo (so the MCP server refresh, the authentication step and the model menu are
// observable), and declares `--version` and `-v` as the program's probe.
func newProbeHarness(t *testing.T, template string) *probeHarness {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not found")
	}
	home := t.TempDir()
	h := &probeHarness{
		home:     home,
		script:   filepath.Join(home, "launch-tool"),
		fakeBin:  filepath.Join(home, "fakebin"),
		progLog:  filepath.Join(home, "prog.log"),
		yoloLog:  filepath.Join(home, "yolo.log"),
		npmLog:   filepath.Join(home, "npm.log"),
		template: template,
		inst: packdecl.Install{Bin: "tool", ProbeArgs: []string{"--version", "-v"},
			Refresh: &packdecl.Refresh{Argv: []string{"update", "--extensions"}, Lock: probeLockRel}},
	}
	switch template {
	case "npm":
		h.inst.Kind, h.inst.Package = "npm", "tool"
		h.inst.ModelMenu = &packdecl.ModelMenu{}
		h.realBin = filepath.Join(home, ".npm-global", "bin", "tool")
	case "native":
		h.inst.Kind, h.inst.InstallerURL = "native", "https://example.invalid/never-fetched.sh"
		h.inst.UpdateVerb = []string{"self-update"}
		h.inst.ModelMenu = &packdecl.ModelMenu{}
		h.realBin = filepath.Join(home, ".local", "bin", "tool")
	case "fork":
		h.inst.Kind, h.inst.ForkedBy = packdecl.InstallKindSource, "forkpack"
		h.inst.Produces = []string{".local/bin/tool"}
		h.realBin = filepath.Join(home, ".local", "bin", "tool")
		// This home already holds the key's build, so the launch path, not a materialize, runs.
		keyDir := filepath.Join(home, ".local", "state", "yolo", "fork-keys")
		mustMkdir(t, keyDir)
		mustWrite(t, filepath.Join(keyDir, "tool"), "k\n", 0o644)
	}
	mustMkdir(t, filepath.Dir(h.realBin))
	mustMkdir(t, filepath.Join(home, ".pi-shared-npm"))
	mustMkdir(t, h.fakeBin)
	mustWrite(t, h.realBin, fakeRefreshProgram(h.progLog, filepath.Join(home, probeLockRel)), 0o755)
	// `yolo internal no-terminal … -- CMD` runs CMD, as the real verb does once it has detached it;
	// every other call is logged and answered with nothing.
	mustWrite(t, filepath.Join(h.fakeBin, "yolo"), `#!/bin/bash
if [ "${1:-} ${2:-}" = "internal no-terminal" ]; then
    while [ "$#" -gt 0 ] && [ "$1" != "--" ]; do shift; done
    shift
    exec "$@"
fi
printf '%s\n' "$*" >> `+shellQuoteForTest(h.yoloLog)+`
exit 0
`, 0o755)
	mustWrite(t, filepath.Join(h.fakeBin, "npm"), `#!/bin/bash
printf '%s\n' "$*" >> `+shellQuoteForTest(h.npmLog)+`
if [ "${1:-}" = "view" ]; then echo 9.9.9; fi
exit 0
`, 0o755)
	return h
}

func mustMkdir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
}

func mustWrite(t *testing.T, path, body string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
}

// write renders the launcher through the production generator for h's template.
func (h *probeHarness) write(t *testing.T) {
	t.Helper()
	stamps := filepath.Join(h.home, ".cache", "yolo-agent-stamps")
	receipts := filepath.Join(h.home, "ws", ".yolo", "receipts.jsonl")
	servers := launcherServers{npm: "some-mcp-server"}
	var body string
	switch h.template {
	case "npm":
		body = npmAgentLauncher("probe", &h.inst, stamps, receipts, true, servers, nil)
	case "native":
		body = nativeAgentLauncher("probe", &h.inst, stamps, receipts, "", true, servers, nil)
	case "fork":
		body = strings.Join(sourceAgentLauncherSegments(&h.inst, ForkDelivery{Key: "k"}, stamps,
			filepath.Join(h.home, ".local", "state", "yolo", "fork-keys"), receipts, "", true, servers, nil), "")
	}
	for from, to := range h.patch {
		if !strings.Contains(body, from) {
			t.Fatalf("the launcher no longer bakes %q, so this cell cannot patch it", from)
		}
		body = strings.Replace(body, from, to, 1)
	}
	mustWrite(t, h.script, body, 0o755)
}

// run executes the launcher with args, the fakes first on PATH, and the authentication step
// switched on for the program (YOLO_AUTH_PRELAUNCH_TOOL_LOGIN, the login-only form).
func (h *probeHarness) run(t *testing.T, args ...string) (string, int) {
	t.Helper()
	h.write(t)
	cmd := exec.Command(h.script, args...)
	cmd.Dir = h.home
	// A closed environment: an inherited NPM_CONFIG_PREFIX would point the launcher at the real
	// install prefix of the jail running the test.
	cmd.Env = []string{"HOME=" + h.home, "TMPDIR=" + t.TempDir(),
		"PATH=" + h.fakeBin + ":" + os.Getenv("PATH"), "YOLO_AUTH_PRELAUNCH_TOOL_LOGIN=1"}
	cmd.Stdin = strings.NewReader("")
	out, err := cmd.CombinedOutput()
	rc := 0
	if ee, ok := err.(*exec.ExitError); ok {
		rc = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("the launcher could not be run: %v\n%s", err, out)
	}
	return string(out), rc
}

// yoloVerbs are the `yolo internal` verbs a launch's steps call, each a step a probe skips.
var yoloVerbs = map[string]string{
	"internal refresh-servers":          "the MCP server refresh",
	"internal openai-auth-client token": "the authentication step",
	"internal model-menu":               "the model menu",
}

func (h *probeHarness) calledVerbs(t *testing.T) map[string]bool {
	t.Helper()
	got := map[string]bool{}
	for _, l := range logLines(t, h.yoloLog) {
		for verb := range yoloVerbs {
			if strings.HasPrefix(l, verb) {
				got[verb] = true
			}
		}
	}
	return got
}

// TestAVersionProbeRunsNoUpdateStep is the probe's whole claim, in all three templates: `tool
// --version` and `tool -v` run the program and nothing before it — no npm call, no update verb,
// no MCP server refresh, no authentication, no model menu, no pre-launch refresh — and leave no
// stamp, lock or refresh record behind. Then the same home, invoked with `-p --version`, runs
// every one of them: a probe is the FIRST argument only, and the cell would be vacuous without it.
func TestAVersionProbeRunsNoUpdateStep(t *testing.T) {
	for _, template := range []string{"npm", "native", "fork"} {
		t.Run(template, func(t *testing.T) {
			h := newProbeHarness(t, template)
			for _, probe := range []string{"--version", "-v"} {
				out, rc := h.run(t, probe)
				if rc != 0 {
					t.Fatalf("%s: the probe exited %d:\n%s", probe, rc, out)
				}
				if got := logLines(t, h.progLog); len(got) != 1 || got[0] != "LAUNCH:"+probe {
					t.Errorf("%s: the program must run once, with the probe and nothing before it: %q\n%s",
						probe, got, out)
				}
				if got := logLines(t, h.npmLog); len(got) != 0 {
					t.Errorf("%s: a version probe called npm: %q", probe, got)
				}
				if got := logLines(t, h.yoloLog); len(got) != 0 {
					t.Errorf("%s: a version probe called yolo (a refresh, an update or the "+
						"authentication step): %q", probe, got)
				}
				for _, p := range []string{
					filepath.Join(h.home, ".cache", "yolo-agent-stamps", "refresh", "tool.stamp"),
					filepath.Join(h.home, ".cache", "yolo-agent-stamps", "tool.stamp"),
					filepath.Join(h.home, probeLockRel),
				} {
					if _, err := os.Stat(p); !os.IsNotExist(err) {
						t.Errorf("%s: a version probe left %s behind (err=%v)", probe, p, err)
					}
				}
				if err := os.Remove(h.progLog); err != nil {
					t.Fatal(err)
				}
			}

			out, rc := h.run(t, "-p", "--version")
			if rc != 0 {
				t.Fatalf("the launch exited %d:\n%s", rc, out)
			}
			prog := logLines(t, h.progLog)
			if countLine(prog, "REFRESH") != 1 || prog[len(prog)-1] != "LAUNCH:-p --version" {
				t.Errorf("an invocation that is not a probe must refresh, then launch: %q\n%s", prog, out)
			}
			called := h.calledVerbs(t)
			for verb, step := range yoloVerbs {
				if verb == "internal model-menu" && template == "fork" {
					continue // the source template carries no model menu
				}
				if !called[verb] {
					t.Errorf("an invocation that is not a probe did not run %s (no %q call): %q\n%s",
						step, verb, logLines(t, h.yoloLog), out)
				}
			}
			switch template {
			case "npm":
				if npm := strings.Join(logLines(t, h.npmLog), "\n"); !strings.Contains(npm, "view tool version") ||
					!strings.Contains(npm, "install -g") {
					t.Errorf("an invocation that is not a probe must run the due update: npm %q\n%s", npm, out)
				}
			case "native":
				if !hasExactArg(prog, "LAUNCH:self-update") {
					t.Errorf("an invocation that is not a probe must run the due update verb: %q\n%s", prog, out)
				}
			}
		})
	}
}

// TestAVersionProbeStillInstallsAColdHome: with nothing at REAL_BIN there is nothing to answer,
// so the cold install runs for a probe too (npm's install, here), and the probe then runs.
func TestAVersionProbeStillInstallsAColdHome(t *testing.T) {
	h := newProbeHarness(t, "npm")
	if err := os.Remove(h.realBin); err != nil {
		t.Fatal(err)
	}
	// The fake npm puts a program in place on install, as the real one does.
	mustWrite(t, filepath.Join(h.fakeBin, "npm"), `#!/bin/bash
printf '%s\n' "$*" >> `+shellQuoteForTest(h.npmLog)+`
if [ "${1:-}" = "install" ]; then
    mkdir -p "$NPM_CONFIG_PREFIX/bin"
    printf '#!/bin/bash\necho "LAUNCH:$*" >> %s\n' `+shellQuoteForTest(shellQuoteForTest(h.progLog))+` > "$NPM_CONFIG_PREFIX/bin/tool"
    chmod +x "$NPM_CONFIG_PREFIX/bin/tool"
fi
exit 0
`, 0o755)
	out, rc := h.run(t, "--version")
	if rc != 0 {
		t.Fatalf("the probe exited %d:\n%s", rc, out)
	}
	if npm := strings.Join(logLines(t, h.npmLog), "\n"); !strings.Contains(npm, "install -g") || strings.Contains(npm, "view") {
		t.Errorf("a probe in a cold home must install the program, and only that: npm %q\n%s", npm, out)
	}
	if got := logLines(t, h.progLog); len(got) != 1 || got[0] != "LAUNCH:--version" {
		t.Errorf("the freshly installed program must answer the probe: %q\n%s", got, out)
	}
	if got := logLines(t, h.yoloLog); len(got) != 0 {
		t.Errorf("a probe in a cold home still runs no other step: %q", got)
	}
}

// TestAVersionProbeNeverWaitsForTheRefreshLock: another jail holds the refresh's lock and this
// workspace's watched content is new, the one case a launch waits for the holder (up to
// UPDATE_TIMEOUT). A probe does not wait and does not say it waits; the control, with the bound
// cut to one second, does both.
func TestAVersionProbeNeverWaitsForTheRefreshLock(t *testing.T) {
	h := newProbeHarness(t, "npm")
	h.inst.Refresh.DueOnChange = []string{".pi/agent/settings.json"}
	h.patch = map[string]string{"UPDATE_TIMEOUT=60": "UPDATE_TIMEOUT=1"}
	mustMkdir(t, filepath.Join(h.home, ".pi", "agent"))
	mustWrite(t, filepath.Join(h.home, ".pi", "agent", "settings.json"), `{"packages":["npm:x"]}`, 0o644)
	mustMkdir(t, filepath.Join(h.home, probeLockRel)) // held, and fresh: not stale

	start := time.Now()
	out, rc := h.run(t, "--version")
	if rc != 0 {
		t.Fatalf("the probe exited %d:\n%s", rc, out)
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second || strings.Contains(out, "waiting for it") {
		t.Errorf("a version probe waited for the refresh lock (%s):\n%s", elapsed, out)
	}

	out, rc = h.run(t, "-p")
	if rc != 0 {
		t.Fatalf("the launch exited %d:\n%s", rc, out)
	}
	if !strings.Contains(out, "waiting for it") {
		t.Errorf("the control must wait for the held lock, or the probe's cell proves nothing:\n%s", out)
	}
}

// TestAVersionProbePassesTheTreeGate: a patched extension the program loads has no build, so a
// launch stops before the exec (PPX-D18); a version probe, which loads no extension, is answered.
func TestAVersionProbePassesTheTreeGate(t *testing.T) {
	for _, template := range []string{"npm", "native", "fork"} {
		t.Run(template, func(t *testing.T) {
			h := newProbeHarness(t, template)
			h.inst.Gate = "  ⚠ extension acme/ext has no build in this jail"
			out, rc := h.run(t, "--version")
			if rc != 0 || !hasExactArg(logLines(t, h.progLog), "LAUNCH:--version") {
				t.Errorf("the gate stopped a version probe (rc=%d):\n%s", rc, out)
			}
			out, rc = h.run(t, "-p")
			if rc == 0 || !strings.Contains(out, "has no build in this jail") {
				t.Errorf("the gate must still stop a launch (rc=%d):\n%s", rc, out)
			}
		})
	}
}

// TestAProgramWithNoProbeArgsHasNoProbe: a program that declares none treats `--version` as any
// other invocation, so the probe is the pack's declaration and never core's guess about a flag.
func TestAProgramWithNoProbeArgsHasNoProbe(t *testing.T) {
	h := newProbeHarness(t, "npm")
	h.inst.ProbeArgs = nil
	out, rc := h.run(t, "--version")
	if rc != 0 {
		t.Fatalf("the launch exited %d:\n%s", rc, out)
	}
	if countLine(logLines(t, h.progLog), "REFRESH") != 1 || len(logLines(t, h.npmLog)) == 0 {
		t.Errorf("with no probe declared, --version must run the launch's steps:\n%s", out)
	}
	h.write(t)
	body, err := os.ReadFile(h.script)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "\nHAS_PROBE_ARGS=0\nPROBE_ARGS=()\n") {
		t.Errorf("a program with no probe must bake an empty, switched-off probe")
	}
}

// TestTheProbeIsBakedIntoEveryTemplate pins the generated text: the declaration, quoted word by
// word, and each gate the probe sets, in the three templates the boot writes.
func TestTheProbeIsBakedIntoEveryTemplate(t *testing.T) {
	inst := packdecl.Install{Bin: "tool", ProbeArgs: []string{"--version", "-v", "it's"}}
	gates := []string{
		`elif [ "$_YOLO_PROBE" = "1" ]; then`,                 // npm's update chain
		`elif [ "$_YOLO_PROBE" != "1" ] && _update_due; then`, // native's
		`if [ "$SERVERS_ENABLED" = "1" ] && [ "$_YOLO_PROBE" != "1" ]; then`,
		`[ "${_YOLO_PROBE:-}" = "1" ] || _prelaunch_refresh || true`,
		`[ "${_YOLO_PROBE:-}" = "1" ] || _refresh_agent_auth`,
		`[ "$_YOLO_PROBE" = "1" ] || _yolo_model_menu`,
		`[ "${_YOLO_PROBE:-}" != "1" ] || TREE_GATE=""`,
	}
	for _, tc := range []struct {
		template string
		body     string
		want     []int
	}{
		{"npm", func() string {
			i := inst
			i.Kind, i.Package = "npm", "tool"
			return npmAgentLauncher("p", &i, "/s", "/r", true, launcherServers{npm: "x"}, nil)
		}(), []int{0, 2, 3, 4, 5, 6}},
		{"native", func() string {
			i := inst
			i.Kind, i.InstallerURL = "native", "https://example.invalid/i.sh"
			return nativeAgentLauncher("p", &i, "/s", "/r", "", true, launcherServers{npm: "x"}, nil)
		}(), []int{1, 2, 3, 4, 5, 6}},
		{"fork", func() string {
			i := inst
			i.Kind = packdecl.InstallKindSource
			return strings.Join(sourceAgentLauncherSegments(&i, ForkDelivery{Key: "k"}, "/s", "/k", "/r", "",
				true, launcherServers{npm: "x"}, nil), "")
		}(), []int{2, 3, 4, 6}},
	} {
		if !strings.Contains(tc.body, "\nHAS_PROBE_ARGS=1\nPROBE_ARGS=(--version -v 'it'\"'\"'s')\n") {
			t.Errorf("%s: the probe arguments are not baked word by word", tc.template)
		}
		for _, i := range tc.want {
			if !strings.Contains(tc.body, gates[i]) {
				t.Errorf("%s: no %q", tc.template, gates[i])
			}
		}
		if strings.Contains(tc.body, "__YOLO_") {
			t.Errorf("%s: a sentinel was left unspliced", tc.template)
		}
	}
}

// TestShippedPiVersionProbeRunsNoUpdateStep is the CALL-SITE cell for the manifest: the SHIPPED pi
// pack, through the loader, the projection and GenerateAgentLaunchers, declares `--version` and
// `-v` as its probe, and `pi --version` with its refresh due runs pi and nothing else. It goes red
// if packs/pi stops declaring the probe, InstallContributions stops carrying it, or the generator
// stops splicing it.
func TestShippedPiVersionProbeRunsNoUpdateStep(t *testing.T) {
	home, launcher, log := shippedPiLauncher(t)
	body, err := os.ReadFile(launcher)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "\nPROBE_ARGS=(--version -v)\n") {
		t.Fatalf("the shipped pi launcher does not declare --version and -v as its probe")
	}
	// pi's own hourly update is due too (no stamp), and npm is a fake that logs every call.
	if err := os.Remove(filepath.Join(home, ".cache", "yolo-agent-stamps", "pi.stamp")); err != nil {
		t.Fatal(err)
	}
	fakeBin, npmLog := filepath.Join(home, "fakebin"), filepath.Join(home, "npm.log")
	mustMkdir(t, fakeBin)
	mustWrite(t, filepath.Join(fakeBin, "npm"), "#!/bin/bash\nprintf '%s\\n' \"$*\" >> "+
		shellQuoteForTest(npmLog)+"\necho 9.9.9\n", 0o755)
	cmd := exec.Command(launcher, "--version")
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "PATH=" + fakeBin + ":" + os.Getenv("PATH"), "TMPDIR=" + t.TempDir()}
	cmd.Stdin = strings.NewReader("")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("the shipped pi launcher failed: %v\n%s", err, out)
	}
	if got := logLines(t, log); strings.Join(got, "\n") != "NODE\nLAUNCH:--version" {
		t.Errorf("pi --version must run pi under its node and nothing before it: %q\n%s", got, out)
	}
	if got := logLines(t, npmLog); len(got) != 0 {
		t.Errorf("pi --version with pi's update due called npm: %q\n%s", got, out)
	}
}

// TestEveryShippedAgentDeclaresItsVersionProbe: every program a shipped pack installs declares
// `--version`, the word the per-pack install matrix runs (integration/agents_test.go), so the probe
// of every agent sharing these launchers answers at once. Red if a manifest drops the line.
func TestEveryShippedAgentDeclaresItsVersionProbe(t *testing.T) {
	home := t.TempDir()
	e := NewEnv(map[string]string{"JAIL_HOME": home, "YOLO_PACK_ROOT": stageShippedPacks(t)})
	e.Stderr = &bytes.Buffer{}
	packs, err := LoadJailPacks(e)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, p := range packs {
		for _, in := range p.Decl.InstallContributions() {
			n++
			found := false
			for _, a := range in.ProbeArgs {
				found = found || a == "--version"
			}
			if !found {
				t.Errorf("pack %s: program %s declares no --version probe (probe_args %q)", p.Name, in.Bin, in.ProbeArgs)
			}
		}
	}
	if n == 0 {
		t.Fatal("the shipped packs install no program")
	}
}

// shippedPiLauncher generates the launchers over the shipped packs, with a fake pi at pi's place
// (pi's own update stamp fresh, its refresh due) and a fake node that logs "NODE" and its
// NODE_COMPILE_CACHE to log before running pi. It returns the home, pi's launcher and the log.
func shippedPiLauncher(t *testing.T) (home, launcher, log string) {
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
	mustMkdir(t, nodeBin)
	mustWrite(t, filepath.Join(nodeBin, "node"), "#!/bin/bash\necho \"NODE\" >> "+shellQuoteForTest(log)+
		"\nprintf '%s\\n' \"${NODE_COMPILE_CACHE:-}\" > "+shellQuoteForTest(log+".ncc")+"\nexec \"$@\"\n", 0o755)
	oldStore := miseNodeStore
	miseNodeStore = nodeStore
	t.Cleanup(func() { miseNodeStore = oldStore })

	e := NewEnv(map[string]string{"JAIL_HOME": home, "YOLO_PACK_ROOT": stageShippedPacks(t)})
	e.Stderr = &bytes.Buffer{}
	if err := GenerateAgentLaunchers(e); err != nil {
		t.Fatalf("GenerateAgentLaunchers over the shipped packs: %v", err)
	}
	launcher = filepath.Join(e.LaunchDir(), "pi")
	realBin := filepath.Join(home, ".npm-global", "bin", "pi")
	stamps := filepath.Join(home, ".cache", "yolo-agent-stamps")
	for _, d := range []string{filepath.Dir(realBin), filepath.Join(home, ".pi-shared-npm"), stamps} {
		mustMkdir(t, d)
	}
	mustWrite(t, realBin, fakeRefreshProgram(log, filepath.Join(home, ".pi-shared-npm", ".yolo-update.lock")), 0o755)
	mustWrite(t, filepath.Join(stamps, "pi.stamp"), "", 0o644)
	return home, launcher, log
}
