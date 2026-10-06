package entrypoint

// readiness_test.go RUNS the jail's readiness act (readiness.go; docs/design/jail-notch-readiness.md,
// OQ-JR1): the bootstrap a boot generates, over the launchers the same boot wrote, against a fake
// `npm`. Running it is the point, as for the Node floor beside it: a text assertion on the template
// passes for a script whose refusal never reaches the stage's status.
//
// Every case here goes through the production producers — GenerateAgentLaunchers and
// BootstrapScript over one Env — so deleting the readiness calls from the bootstrap, the
// install-only branch from a launcher, or the refusal's exit fails one of them.

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/provision"
)

const (
	readyPack = "readyfix"
	readyBin  = "readyprobe"
)

// readyManifest is one pack declaring one npm program.
const readyManifest = `{"name": "` + readyPack + `", "contributes": [{"kind": "program", "bin": "` +
	readyBin + `", "via": "npm", "package": "readyprobe-pkg"}]}`

// readyHome is a temp jail home whose launchers and bootstrap one boot generated, with a fake
// npm that installs (or, under FAKE_NPM_OFFLINE, fails as npm does with no network) and logs
// every call to log. The program it installs records that it RAN in the same log, so "the
// readiness act never runs the program" is an observation rather than an absence.
type readyHome struct {
	e                     *Env
	fakeBin, log, scriptP string
}

func newReadyHome(t *testing.T, manifests map[string]string, extra map[string]string) readyHome {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("no bash on PATH; the bootstrap is bash")
	}
	home := t.TempDir()
	vars := map[string]string{
		"JAIL_HOME": home,
		// A temp workspace, never the default: launchers write receipts under <workspace>/.yolo.
		"YOLO_WORKSPACE": filepath.Join(home, "ws"),
	}
	if manifests != nil {
		vars["YOLO_PACK_ROOT"] = stageFloorPacks(t, manifests)
	}
	for k, v := range extra {
		vars[k] = v
	}
	e := NewEnv(vars)
	var stderr bytes.Buffer
	e.Stderr = &stderr
	if err := GenerateAgentLaunchers(e); err != nil {
		t.Fatal(err)
	}
	h := readyHome{e: e, fakeBin: filepath.Join(home, "fake-bin"), log: filepath.Join(home, "calls.log"),
		scriptP: filepath.Join(home, "bootstrap.sh")}
	writeTestFile(t, h.scriptP, BootstrapScript(e))
	if err := os.Chmod(h.scriptP, 0o755); err != nil {
		t.Fatal(err)
	}
	fakes := map[string]string{
		"npm": `#!/bin/bash
echo "npm $*" >> "$FAKE_LOG"
[ "$1" = install ] || exit 0
if [ -n "${FAKE_NPM_KILLED:-}" ]; then
    # What a killed install leaves: bash's job line, and no word a pattern would call an error.
    echo "Terminated                 npm install -g readyprobe-pkg@latest" >&2
    exit 143
fi
if [ -n "${FAKE_NPM_OFFLINE:-}" ]; then
    # npm's own words with no network, as the nested-jail check recorded them.
    echo "npm error code ENOTFOUND" >&2
    echo "npm error syscall getaddrinfo" >&2
    echo "npm error errno ENOTFOUND" >&2
    echo "npm error network request to https://registry.npmjs.org/readyprobe-pkg failed, reason: getaddrinfo ENOTFOUND registry.npmjs.org" >&2
    echo "npm error network This is a problem related to network connectivity." >&2
    echo "npm error A complete log of this run can be found in: /nowhere/_logs/debug-0.log" >&2
    exit 1
fi
if [ -n "${FAKE_NPM_404:-}" ]; then
    echo "npm error code E404" >&2
    echo "npm error 404 Not Found - GET https://registry.npmjs.org/readyprobe-pkg - Not found" >&2
    echo "npm error 404" >&2
    echo "npm error 404  'readyprobe-pkg@latest' is not in this registry." >&2
    exit 1
fi
mkdir -p "$NPM_CONFIG_PREFIX/bin"
printf '#!/bin/bash\necho "PROGRAM RAN $*" >> "%s"\n' "$FAKE_LOG" > "$NPM_CONFIG_PREFIX/bin/` + readyBin + `"
chmod +x "$NPM_CONFIG_PREFIX/bin/` + readyBin + `"
echo "added 1 package in 0s"
`,
		"fc-cache": "#!/bin/sh\nexit 0\n",
	}
	for name, body := range fakes {
		writeTestFile(t, filepath.Join(h.fakeBin, name), body)
		if err := os.Chmod(filepath.Join(h.fakeBin, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return h
}

// realBin is where the npm launcher looks for the program.
func (h readyHome) realBin() string { return filepath.Join(h.e.NpmBin(), readyBin) }

// run executes the bootstrap with the fakes and the system dirs on PATH — never the caller's
// PATH, whose real npm would change what is installed.
func (h readyHome) run(t *testing.T, env ...string) (rc int, out, calls string) {
	t.Helper()
	cmd := exec.Command(h.scriptP)
	cmd.Env = append([]string{
		"HOME=" + h.e.Home,
		"PATH=" + h.fakeBin + ":/bin:/usr/bin",
		"FAKE_LOG=" + h.log,
		"TMPDIR=" + os.TempDir(),
	}, env...)
	o, err := cmd.CombinedOutput()
	if ee, ok := err.(*exec.ExitError); ok {
		rc = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("the bootstrap could not be run: %v\n%s", err, o)
	}
	c, _ := os.ReadFile(h.log)
	return rc, string(o), string(c)
}

// THE ACT: a cold home's declared program is installed by the stage, through its own launcher,
// and is never run there.
func TestReadinessInstallsADeclaredProgramBeforeTheCommandRuns(t *testing.T) {
	h := newReadyHome(t, map[string]string{readyPack: readyManifest}, nil)
	rc, out, calls := h.run(t)
	if rc != 0 {
		t.Fatalf("rc = %d, want 0 for an install that worked:\n%s", rc, out)
	}
	if !isExecutableFile(h.realBin()) {
		t.Errorf("the stage left %s uninstalled — the command would start without the program "+
			"its pack declares:\n%s", h.realBin(), out)
	}
	if !strings.Contains(calls, "npm install -g --prefer-online readyprobe-pkg@latest") {
		t.Errorf("the program was not installed through its launcher's npm install:\n%s", calls)
	}
	if strings.Contains(calls, "PROGRAM RAN") {
		t.Errorf("the readiness act RAN the program it installed:\n%s", calls)
	}
	if strings.Contains(out, "REFUSING") {
		t.Errorf("a launch whose program installed was refused:\n%s", out)
	}
}

// A second boot of the same home installs nothing and says nothing (the design's §7 item 2).
func TestAWarmHomeInstallsNothingAndSaysNothing(t *testing.T) {
	h := newReadyHome(t, map[string]string{readyPack: readyManifest}, nil)
	if rc, out, _ := h.run(t); rc != 0 {
		t.Fatalf("the cold run failed: rc=%d\n%s", rc, out)
	}
	if err := os.Remove(h.log); err != nil {
		t.Fatal(err)
	}
	rc, out, calls := h.run(t, "FAKE_NPM_OFFLINE=1")
	if rc != 0 {
		t.Errorf("rc = %d on a warm home (offline, which a warm home must not notice):\n%s", rc, out)
	}
	if strings.Contains(calls, "npm install") {
		t.Errorf("a warm home installed again, which is a refresh the act must never do:\n%s", calls)
	}
	if strings.Contains(out, readyBin) || strings.Contains(out, "Installing") {
		t.Errorf("a warm home's readiness act said something:\n%s", out)
	}
}

// THE RULING: an install that fails STOPS the launch, offline included, naming the pack, the
// program and the install's error, and offering the hatch by name. The status is the one the
// stage's wrapper passes through as a refusal.
func TestReadinessRefusesAProgramItCannotInstallOfflineIncluded(t *testing.T) {
	h := newReadyHome(t, map[string]string{readyPack: readyManifest}, nil)
	rc, out, _ := h.run(t, "FAKE_NPM_OFFLINE=1")
	if rc != provision.RefusedStatus {
		t.Errorf("rc = %d, want provision.RefusedStatus (%d) — any other status is degraded by the "+
			"stage's wrapper and the command runs without the program", rc, provision.RefusedStatus)
	}
	for what, want := range map[string]string{
		"the refusal": "REFUSING to start this jail",
		"the program": "program " + readyBin + " (pack " + readyPack + ")",
		"the error":   "getaddrinfo ENOTFOUND registry.npmjs.org",
		"the status":  "its install exited 1",
		"the hatch":   paths.AllowMissingProgramsEnv + "=1 yolo <your command>",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the refusal does not name %s (%q):\n%s", what, want, out)
		}
	}
}

// The error the refusal quotes is the install's own, not the launcher's closing line, which
// names nothing.
func TestTheRefusalQuotesTheInstallsErrorNotTheLaunchersClosingLine(t *testing.T) {
	h := newReadyHome(t, map[string]string{readyPack: readyManifest}, nil)
	_, out, _ := h.run(t, "FAKE_NPM_OFFLINE=1")
	summary := out[strings.Index(out, "REFUSING"):]
	if strings.Contains(summary, "its install failed, above") {
		t.Errorf("the refusal quotes the launcher's closing line instead of the error:\n%s", summary)
	}
	// The line saying what failed and why, which is npm's fourth: its first three name only the
	// code, the syscall and the errno.
	if !strings.Contains(summary, "          npm error network request to https://registry.npmjs.org/readyprobe-pkg "+
		"failed, reason: getaddrinfo ENOTFOUND registry.npmjs.org\n") {
		t.Errorf("the refusal does not quote the line saying what failed and why, indented under the program:\n%s", summary)
	}
	if strings.Contains(summary, "npm error syscall") {
		t.Errorf("the refusal quotes npm's code block although a line says what failed:\n%s", summary)
	}

	// An install that names its error and no failure (npm's 404) is quoted by its error lines.
	_, out, _ = h.run(t, "FAKE_NPM_404=1")
	summary = out[strings.Index(out, "REFUSING"):]
	if !strings.Contains(summary, "          npm error code E404\n          npm error 404 Not Found") {
		t.Errorf("an install naming only its error must be quoted by its error lines:\n%s", summary)
	}

	// An install whose output names no error (a killed npm, found by the nested-jail check) is
	// quoted by its own last line — still never the launcher's closing one.
	_, out, _ = h.run(t, "FAKE_NPM_KILLED=1")
	summary = out[strings.Index(out, "REFUSING"):]
	if strings.Contains(summary, "its install failed, above") || !strings.Contains(summary, "          Terminated") {
		t.Errorf("an install with no error line must be quoted by its own last line:\n%s", summary)
	}
}

// The refusal survives the stage's wrapper with nobody at a terminal, so the command after the
// stage never runs — the shape the entrypoint runs the stage in (jailmain.go).
func TestARefusedReadinessNeverReachesTheCommand(t *testing.T) {
	h := newReadyHome(t, map[string]string{readyPack: readyManifest}, nil)
	logPath := filepath.Join(h.e.Home, "startup.log")
	stage := provision.Script(logPath, provision.Stage([]string{"true"}, []string{provision.StepRunBootstrapAt(h.scriptP)}), false)
	cmd := exec.Command("bash", "-c", stage+"\necho THE-COMMAND-RAN")
	cmd.Env = []string{"HOME=" + h.e.Home, "PATH=" + h.fakeBin + ":/bin:/usr/bin", "FAKE_LOG=" + h.log,
		"FAKE_NPM_OFFLINE=1", "TMPDIR=" + os.TempDir()}
	cmd.Stdin = nil
	o, err := cmd.CombinedOutput()
	rc := 0
	if ee, ok := err.(*exec.ExitError); ok {
		rc = ee.ExitCode()
	}
	if rc != provision.RefusedStatus || strings.Contains(string(o), "THE-COMMAND-RAN") {
		t.Errorf("rc = %d: a refused readiness act must end the stage with %d before the command:\n%s",
			rc, provision.RefusedStatus, o)
	}
}

// THE HATCH: the launch's own AllowMissingProgramsEnv, baked by the boot, starts the jail and
// lists each program it could not install, with its error.
func TestTheHatchStartsTheJailAndListsWhatItCouldNotInstall(t *testing.T) {
	h := newReadyHome(t, map[string]string{readyPack: readyManifest},
		map[string]string{paths.AllowMissingProgramsEnv: "1"})
	rc, out, _ := h.run(t, "FAKE_NPM_OFFLINE=1")
	if rc != 0 {
		t.Errorf("rc = %d: %s must start the jail:\n%s", rc, paths.AllowMissingProgramsEnv, out)
	}
	if strings.Contains(out, "REFUSING") {
		t.Errorf("the hatch still refused:\n%s", out)
	}
	for _, want := range []string{
		paths.AllowMissingProgramsEnv + " is set, so this jail starts WITHOUT",
		"program " + readyBin + " (pack " + readyPack + ")",
		"getaddrinfo ENOTFOUND",
		"installs the first time it is run",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the hatch's list does not say %q:\n%s", want, out)
		}
	}
	// And the hatch is the BAKED value, not one the stage's own environment can supply: a
	// jail that never asked for it is refused even if the variable turns up in the stage.
	plain := newReadyHome(t, map[string]string{readyPack: readyManifest}, nil)
	if rc, out, _ := plain.run(t, "FAKE_NPM_OFFLINE=1", paths.AllowMissingProgramsEnv+"=1"); rc != provision.RefusedStatus {
		t.Errorf("rc = %d: the stage read the hatch from its own environment rather than the boot's:\n%s", rc, out)
	}
}

// The suite's off-switch: nothing is installed ahead of the command, and the stage says what it
// left. Not the hatch: it does not even try.
func TestNoProgramReadinessInstallsNothingAndSaysWhatItLeft(t *testing.T) {
	h := newReadyHome(t, map[string]string{readyPack: readyManifest},
		map[string]string{paths.NoProgramReadinessEnv: "1"})
	rc, out, calls := h.run(t)
	if rc != 0 {
		t.Errorf("rc = %d, want 0:\n%s", rc, out)
	}
	if strings.Contains(calls, "npm install") || isExecutableFile(h.realBin()) {
		t.Errorf("%s still installed the program:\n%s", paths.NoProgramReadinessEnv, calls)
	}
	if !strings.Contains(out, paths.NoProgramReadinessEnv) || !strings.Contains(out, "program "+readyBin+" (pack "+readyPack+")") {
		t.Errorf("the stage did not say what it left uninstalled:\n%s", out)
	}
}

// A jail whose packs declare no program runs no readiness act and prints nothing for one (the
// design's §7 item 3).
func TestAJailWithNoProgramsHasNoReadinessAct(t *testing.T) {
	h := newReadyHome(t, map[string]string{"plain": `{"name": "plain", "contributes": []}`}, nil)
	if got := readinessChecks(h.e); got != "" {
		t.Errorf("readinessChecks = %q for a jail with no program", got)
	}
	rc, out, calls := h.run(t)
	if rc != 0 || out != "" || calls != "" {
		t.Errorf("rc=%d, output %q, calls %q — want a silent, successful stage", rc, out, calls)
	}
}

// THE SET IS THE BOOT'S LAUNCHERS. Readiness asks for exactly the programs GenerateAgentLaunchers
// wrote a launcher for: not a name the image provides (it is ready already, and its launch-dir
// entry, if any, is a flag wrapper with nothing to install), not a program whose vendor publishes
// nothing for this platform (launchercollision.go keeps that a line, not a refusal), and a bin two
// packs declare once.
func TestReadinessAsksForExactlyTheLaunchersTheBootWrote(t *testing.T) {
	image := t.TempDir()
	writeTestFile(t, filepath.Join(image, "baked"), "#!/bin/sh\n")
	if err := os.Chmod(filepath.Join(image, "baked"), 0o755); err != nil {
		t.Fatal(err)
	}
	defer OverrideImageProbeBase(image)()
	h := newReadyHome(t, map[string]string{
		"first": `{"name": "first", "contributes": [
			{"kind": "program", "bin": "alpha", "via": "npm", "package": "alpha"},
			{"kind": "program", "bin": "beta", "via": "installer", "url": "https://example.invalid/i.sh"},
			{"kind": "program", "bin": "baked", "via": "npm", "package": "baked"},
			{"kind": "program", "bin": "gamma", "via": "npm", "package": "gamma", "platforms": ["plan9/amd64"]}]}`,
		"second": `{"name": "second", "contributes": [
			{"kind": "program", "bin": "alpha", "via": "npm", "package": "alpha-again"}]}`,
	}, nil)
	entries, err := os.ReadDir(h.e.LaunchDir())
	if err != nil {
		t.Fatal(err)
	}
	var launchers []string
	for _, en := range entries {
		launchers = append(launchers, en.Name())
	}
	var ready []string
	for _, p := range declaredReadyPrograms(h.e) {
		ready = append(ready, p.Bin)
	}
	sort.Strings(launchers)
	sort.Strings(ready)
	if strings.Join(ready, ",") != strings.Join(launchers, ",") || strings.Join(ready, ",") != "alpha,beta" {
		t.Errorf("readiness asks for %v, the boot wrote launchers %v; want both to be [alpha beta]", ready, launchers)
	}
	// And the generated bootstrap carries exactly those calls.
	script, _ := os.ReadFile(h.scriptP)
	if got := strings.Count(string(script), "\n_yolo_ready "); got != 2 {
		t.Errorf("the bootstrap carries %d readiness calls, want 2:\n%s", got, readinessChecks(h.e))
	}
}

// --- the launchers' install-only mode -------------------------------------------------------

// The npm launcher installs an absent program and runs nothing; a present one costs no npm call.
func TestTheNpmLaunchersInstallOnlyModeInstallsAndRunsNothing(t *testing.T) {
	h := newReadyHome(t, map[string]string{readyPack: readyManifest}, nil)
	pathEnv := h.fakeBin + ":/bin:/usr/bin"
	out, rc := runGeneratedLauncher(t, h.e, pathEnv, readyBin, installOnlyEnv, "FAKE_LOG="+h.log)
	if rc != 0 || !isExecutableFile(h.realBin()) {
		t.Fatalf("rc=%d, installed=%v: install-only must install an absent program:\n%s",
			rc, isExecutableFile(h.realBin()), out)
	}
	calls, _ := os.ReadFile(h.log)
	if strings.Contains(string(calls), "PROGRAM RAN") {
		t.Errorf("install-only ran the program:\n%s", calls)
	}
	if err := os.Remove(h.log); err != nil {
		t.Fatal(err)
	}
	if out, rc := runGeneratedLauncher(t, h.e, pathEnv, readyBin, installOnlyEnv, "FAKE_LOG="+h.log); rc != 0 {
		t.Errorf("rc=%d for a present program:\n%s", rc, out)
	}
	if calls, _ := os.ReadFile(h.log); len(calls) != 0 {
		t.Errorf("install-only did work for a present program (it must refresh nothing):\n%s", calls)
	}
	// A failed install says so and fails, with no advice to run the program again.
	cold := newReadyHome(t, map[string]string{readyPack: readyManifest}, nil)
	out, rc = runGeneratedLauncher(t, cold.e, cold.fakeBin+":/bin:/usr/bin", readyBin, installOnlyEnv,
		"FAKE_LOG="+cold.log, "FAKE_NPM_OFFLINE=1")
	if rc == 0 || !strings.HasSuffix(out, "  ⚠ "+readyBin+" not available: its install failed, above.\n") {
		t.Errorf("rc=%d: a failed install-only run must fail and say so last:\n%s", rc, out)
	}
}

// The installer launcher's install-only mode never refreshes a present program, even when its
// update is due: the readiness act asks for presence alone (OQ-PD12a keeps currency at the
// invocation). The control, without the mode, shows the update was due.
func TestTheInstallerLaunchersInstallOnlyModeNeverRefreshes(t *testing.T) {
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("curl not found")
	}
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte("#!/bin/bash\nexit 0\n"))
	}))
	t.Cleanup(srv.Close)
	e, pathEnv, _ := misplacedFixture(t,
		`{"kind":"program","bin":"readynative","via":"installer","url":"`+srv.URL+`/install.sh"}`)
	realBin := filepath.Join(e.Home, ".local", "bin", "readynative")
	writeTestFile(t, realBin, "#!/bin/bash\necho READYNATIVE_RAN\n")
	if err := os.Chmod(realBin, 0o755); err != nil {
		t.Fatal(err)
	}
	// No stamp: an update is due on the next invocation.
	out, rc := runGeneratedLauncher(t, e, pathEnv, "readynative", installOnlyEnv)
	if rc != 0 || hits.Load() != 0 || strings.Contains(out, "READYNATIVE_RAN") {
		t.Errorf("rc=%d, installer fetched %d time(s): install-only must leave a present program "+
			"alone and run nothing:\n%s", rc, hits.Load(), out)
	}
	if _, rc := runGeneratedLauncher(t, e, pathEnv, "readynative"); rc != 0 || hits.Load() == 0 {
		t.Errorf("rc=%d, fetched %d time(s): the control launch did not run the due update, so this "+
			"case proves nothing", rc, hits.Load())
	}
}

// A fork's source launcher materializes the host's build and runs nothing; with no build from the
// host it fails naming why, which is the error a readiness refusal quotes.
func TestTheSourceLaunchersInstallOnlyModeMaterializesAndRunsNothing(t *testing.T) {
	home, launcher, fakeBin, argvLog := forkLauncher(t, `{"pi":{"key":"k1"}}`)
	cmd := exec.Command(launcher)
	cmd.Env = []string{"HOME=" + home, "PATH=" + fakeBin + ":" + os.Getenv("PATH"), installOnlyEnv}
	out, err := cmd.CombinedOutput()
	if err != nil || strings.Contains(string(out), "FORK_BUILD_RAN") {
		t.Errorf("err=%v: install-only must materialize the build and run nothing:\n%s", err, out)
	}
	if calls, _ := os.ReadFile(argvLog); !strings.Contains(string(calls), "--key=k1") {
		t.Errorf("the build was not materialized: %q", calls)
	}

	home, launcher, fakeBin, _ = forkLauncher(t, `{"pi":{"reason":"the build of commit abc failed"}}`)
	cmd = exec.Command(launcher)
	cmd.Env = []string{"HOME=" + home, "PATH=" + fakeBin + ":" + os.Getenv("PATH"), installOnlyEnv}
	out, err = cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "the build of commit abc failed") {
		t.Errorf("err=%v: a fork with no build must fail install-only, naming the host's reason:\n%s", err, out)
	}
}

// --- macos-user (JR-D2) -----------------------------------------------------------------------

// That backend's stage does not run the act yet, so its bootstrap carries none of it, and its
// boot names each declared program it finds absent — and stops naming it once it is installed.
func TestMacosUserCarriesNoReadinessAndNamesWhatIsAbsent(t *testing.T) {
	home := t.TempDir()
	vars := map[string]string{
		// HOME as the sandbox account's process has it, so every prefix the Env derives is
		// under the temp home — never the real one this test runs in.
		"HOME":                  home,
		"YOLO_PACK_ROOT":        stageFloorPacks(t, map[string]string{readyPack: readyManifest}),
		"YOLO_DARWIN_WORKSPACE": filepath.Join(home, "ws"),
		DarwinHomeSidecarEnv:    filepath.Join(home, "sidecar"),
	}
	e := DarwinEnvFrom(vars, home)
	if !strings.HasPrefix(e.NpmBin(), home) {
		t.Fatalf("the fixture's npm prefix %s is outside its temp home", e.NpmBin())
	}
	var stderr bytes.Buffer
	e.Stderr = &stderr
	if err := GenerateDarwinBootstrapScript(e); err != nil {
		t.Fatal(err)
	}
	script, err := os.ReadFile(DarwinBootstrapScriptPath(e.DarwinSidecar()))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(script), "\n_yolo_ready ") {
		t.Errorf("the macos-user bootstrap carries readiness calls its stage does not run (JR-D2)")
	}
	for _, want := range []string{"macos-user does not install", "program " + readyBin + " (pack " + readyPack + ")"} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("the macos-user boot does not say %q:\n%s", want, stderr.String())
		}
	}
	writeTestFile(t, filepath.Join(e.NpmBin(), readyBin), "#!/bin/sh\n")
	if err := os.Chmod(filepath.Join(e.NpmBin(), readyBin), 0o755); err != nil {
		t.Fatal(err)
	}
	stderr.Reset()
	if err := GenerateDarwinBootstrapScript(e); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stderr.String(), "macos-user does not install") {
		t.Errorf("the boot names a program that is installed:\n%s", stderr.String())
	}
}
