package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/hostfloor/floortest"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// hostlaunchpath_test.go pins the LAUNCH PATH (docs/design/host-launch-environment.md §2.2: the
// PATH yolo was started with, then `host_path`'s folders not already on it) at every host call
// site that reads it — the launch gate's dependency survey, `yolo host apply`'s pre-flight and its
// install, `yolo check-deps`, and the exec's target lookup — and the miss line (HE-D2) each prints.
// Every fixture is a temp HOME and a fake PATH; no agent CLI runs, and no test reads the real home.
// Each test says which deleted call site fails it.

const pathSep = string(os.PathListSeparator)

// launchPathFixture is a temp HOME whose user config selects one pack, needpack, declaring the
// given contributions, with extra appended to the config object. The PATH is a fake one holding
// only `apt` (the detected manager), so a binary is present only where the fixture puts it.
func launchPathFixture(t *testing.T, extra string, contributions ...string) (home, pathDir string) {
	t.Helper()
	home = floortest.ResolvedTemp(t)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("YOLO_VERSION", "")
	t.Chdir(floortest.ResolvedTemp(t))
	pack := filepath.Join(floortest.ResolvedTemp(t), "needpack")
	writeFile(t, filepath.Join(pack, "pack.json"),
		`{"name":"needpack","contributes":[`+strings.Join(contributions, ",")+`]}`)
	writeFile(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"),
		`{"packs":[{"source":"file://`+pack+`","name":"needpack"}]`+extra+`}`)
	return home, fakeBinDir(t, "apt")
}

// putExe writes an executable stub named name into dir and returns its path.
func putExe(t *testing.T, dir, name string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	writeFile(t, p, "#!/bin/sh\n")
	if err := os.Chmod(p, 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

const hpTool = `{"kind":"requires","bin":"yolo-hp-tool","install_hints":{"apt":"yolo-hp-tool-apt"}}`

// runCheckDepsT runs `yolo check-deps --no-manifest` and returns its rc and output.
func runCheckDepsT(t *testing.T) (int, string) {
	t.Helper()
	var out, errw bytes.Buffer
	rc := checkDepsMain([]string{"--no-manifest"}, &out, &errw, false)
	return rc, out.String() + errw.String()
}

// runHostApplyDry runs `yolo host apply` (the dry run) and returns its output.
func runHostApplyDry(t *testing.T) string {
	t.Helper()
	var out, errw bytes.Buffer
	if rc := applyHost(&out, &errw, false, false, nil); rc != 0 {
		t.Fatalf("host apply dry run rc=%d\n%s%s", rc, out.String(), errw.String())
	}
	return out.String() + errw.String()
}

// runGate runs the launch gate for a launch of bin and returns what it printed.
func runGate(t *testing.T, bin string) string {
	t.Helper()
	setGateTTY(t, false)
	var errw bytes.Buffer
	if !hostApplyGate(&errw, nil, bin) {
		t.Fatalf("the gate refused the launch of %s:\n%s", bin, errw.String())
	}
	return errw.String()
}

// TestEveryHostCheckReadsHostPath is the design's test 2 — one PATH for every check. A `requires`
// tool the PATH yolo was started with lacks, in a `host_path` folder: `yolo host apply`'s pre-flight,
// `yolo check-deps` and the launch gate's survey all read it present, at that folder. Dropped from
// `host_path` and left in ~/.cargo/bin, a hint folder: every one reads it missing — the hint never
// makes a check pass — and prints the miss line naming the PATH searched, the pack, the key and the
// hint folder; `host apply --format json` carries the same line. Route any of the three probes back
// to a bare exec.LookPath, or delete its miss line, and this fails.
func TestEveryHostCheckReadsHostPath(t *testing.T) {
	home, pathDir := launchPathFixture(t, `,"host_path":["~/tools/bin"],"host_apply_on_launch":true`, hpTool)
	inHostPath := putExe(t, filepath.Join(home, "tools", "bin"), "yolo-hp-tool")

	verboseReport(t)
	if report := runHostApplyDry(t); !strings.Contains(report, "present at "+inHostPath) ||
		strings.Contains(report, "MISSING") {
		t.Errorf("host apply did not find the host_path copy:\n%s", report)
	}
	if rc, report := runCheckDepsT(t); rc != 0 || !strings.Contains(report, inHostPath) {
		t.Errorf("check-deps rc=%d did not find the host_path copy:\n%s", rc, report)
	}
	if got := runGate(t, "someagent"); strings.Contains(got, "yolo-hp-tool") {
		t.Errorf("the gate reported a tool host_path holds:\n%s", got)
	}

	// Without host_path, the tool only in a hint folder — and in the DEFAULT view: the miss line is
	// a disclosure no verbosity decides.
	t.Setenv(paths.VerboseEnv, "")
	if err := os.Remove(inHostPath); err != nil {
		t.Fatal(err)
	}
	putExe(t, filepath.Join(home, ".cargo", "bin"), "yolo-hp-tool")
	writeFile(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"), strings.Replace(
		readFileT(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc")),
		`,"host_path":["~/tools/bin"]`, "", 1))
	tail := `is not on %s, ` + pathDir + `, the PATH yolo was started with. If yolo-hp-tool is ` +
		`installed, add its folder to "host_path" in ~/.config/yolo-jail/config.jsonc; ~/.cargo/bin has one.`
	checking := "yolo-hp-tool (required by the needpack pack) " + strings.Replace(tail, "%s", "the PATH yolo searched", 1)
	launching := "yolo host: yolo-hp-tool (required by the needpack pack) " + strings.Replace(tail, "%s", "this launch's PATH", 1)

	if report := runHostApplyDry(t); !strings.Contains(report, "MISSING") || !strings.Contains(report, checking) {
		t.Errorf("host apply's dependency blocker lacks the miss line %q:\n%s", checking, report)
	}
	if rc, report := runCheckDepsT(t); rc != 1 || !strings.Contains(report, checking) {
		t.Errorf("check-deps rc=%d lacks the miss line %q:\n%s", rc, checking, report)
	}
	if got := runGate(t, "someagent"); !strings.Contains(got, launching) {
		t.Errorf("the gate lacks the miss line %q:\n%s", launching, got)
	}
	// Once per missing program: the program this launch starts is left to the exec's own line.
	if got := runGate(t, "yolo-hp-tool"); strings.Contains(got, "is not on this launch's PATH") {
		t.Errorf("the gate printed the target's miss line, which the exec prints:\n%s", got)
	}

	survey := &hostApplySurvey{}
	var sink bytes.Buffer
	applyHostSurveyed(&sink, &sink, false, false, nil, survey)
	doc, err := json.Marshal(buildHostApplyDoc(survey))
	if err != nil {
		t.Fatal(err)
	}
	if want, _ := json.Marshal(checking); !strings.Contains(string(doc), `"miss":`+string(want)) {
		t.Errorf("the JSON document's dependency group lacks the miss line:\n%s", doc)
	}
}

// otherOS is a platform this test is not running on, for a program whose vendor publishes no build
// for this host.
func otherOS() string {
	if runtime.GOOS == "darwin" {
		return "linux"
	}
	return "darwin"
}

// TestAProgramWithNoBuildHereStillPrintsTheMissLine: a selected pack's program whose vendor publishes
// no build for this host has no floor entry, so `yolo host -- <it>` looks it up on the launch PATH
// (OQ-HE11 (a)) — and a check that looked it up there and found nothing prints the miss line too
// (HE-D2: it prints on every miss), in `check-deps`, in `yolo host apply`'s default view and in the
// launch gate. It is still not MISSING: nothing could install it, so check-deps exits 0 and host
// apply counts it apart. Gate the miss line on "missing" again at any of the three, and this fails.
func TestAProgramWithNoBuildHereStillPrintsTheMissLine(t *testing.T) {
	home, pathDir := launchPathFixture(t, `,"host_apply_on_launch":true`,
		`{"kind":"program","bin":"yolo-hp-uptool","via":"npm","package":"yolo-hp-uptool-pkg","platforms":["`+otherOS()+`"]}`)
	withTestFloor(t) // the production floor, so the program has no entry for the platform alone
	putExe(t, filepath.Join(home, ".cargo", "bin"), "yolo-hp-uptool")
	tail := `is not on %s, ` + pathDir + `, the PATH yolo was started with. If yolo-hp-uptool is ` +
		`installed, add its folder to "host_path" in ~/.config/yolo-jail/config.jsonc; ~/.cargo/bin has one.`
	checking := "yolo-hp-uptool (a program of the needpack pack) " + strings.Replace(tail, "%s", "the PATH yolo searched", 1)
	launching := "yolo host: yolo-hp-uptool (a program of the needpack pack) " + strings.Replace(tail, "%s", "this launch's PATH", 1)

	rc, report := runCheckDepsT(t)
	if rc != 0 || !strings.Contains(report, "no build for this host") {
		t.Fatalf("check-deps rc=%d, want 0 and the no-build line: nothing could install it\n%s", rc, report)
	}
	if !strings.Contains(report, checking) {
		t.Errorf("check-deps lacks the miss line %q:\n%s", checking, report)
	}
	if report := runHostApplyDry(t); !strings.Contains(report, checking) {
		t.Errorf("host apply's default view lacks the miss line %q:\n%s", checking, report)
	} else if strings.Contains(report, "MISSING") {
		t.Errorf("host apply called a program with no build here MISSING:\n%s", report)
	}
	if got := runGate(t, "someagent"); !strings.Contains(got, launching) {
		t.Errorf("the gate lacks the miss line %q:\n%s", launching, got)
	}
}

// TestARefusedHostPathEntryIsNamedInTheMissLine: a `host_path` entry validation refuses reaches no
// PATH, and no host verb validates the config first, so a miss in the folder the user meant must say
// the entry is why — or the line tells them to add the folder they already listed. check-deps and the
// exec both name it, with the fix. Drop the refused entries from the resolver, or from the line, and
// this fails.
func TestARefusedHostPathEntryIsNamedInTheMissLine(t *testing.T) {
	home, pathDir := launchPathFixture(t, `,"host_path":["$HOME/.cargo/bin","~/tools/bin"]`, hpTool)
	putExe(t, filepath.Join(home, ".cargo", "bin"), "yolo-hp-tool")
	ignored := `. host_path's entry "$HOME/.cargo/bin" is ignored: host_path expands no variable, so write it ` +
		`"~/.cargo/bin". If yolo-hp-tool is installed, add its folder to "host_path" in ~/.config/yolo-jail/config.jsonc; ` +
		`~/.cargo/bin has one.`
	if rc, report := runCheckDepsT(t); rc != 1 || !strings.Contains(report, "is not on the PATH yolo searched, "+pathDir+
		", the PATH yolo was started with, then host_path's ~/tools/bin"+ignored) {
		t.Errorf("check-deps rc=%d does not name the refused entry:\n%s", rc, report)
	}

	orig := prepareOpenAIAuthHost
	prepareOpenAIAuthHost = func(hostPrelaunch, io.Writer) (managedOpenAIHostLaunch, error) { return nil, nil }
	t.Cleanup(func() { prepareOpenAIAuthHost = orig })
	captureHostExec(t)
	var errw bytes.Buffer
	if rc := hostExec(nil, []string{"yolo-hp-tool"}, io.Discard, &errw, nil); rc != 127 {
		t.Fatalf("rc=%d, want 127\n%s", rc, errw.String())
	}
	if !strings.Contains(errw.String(), ignored) {
		t.Errorf("the exec's miss line does not name the refused entry:\n%s", errw.String())
	}
}

// readFileT reads path or fails the test.
func readFileT(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestTheExecReadsHostPathAndMissesWithTheMissLine is the design's test 1 for `host_path` and test 8
// for the exec. A program no pack declares with a copy on the PATH yolo was started with and another
// in a `host_path` folder runs the first; one only in the `host_path` folder runs from there; the
// child's PATH is the caller's, then `host_path`'s new folder, then the floor's bin/. A program found
// only in a hint folder exits 127 with the miss line in place of the lookup's own error, and never
// runs. Hand hostExec a lookup without host_path, or delete the miss line, and this fails.
func TestTheExecReadsHostPathAndMissesWithTheMissLine(t *testing.T) {
	floorHostFixture(t, `,"host_path":["~/tools/bin"]`)
	home := paths.Home()
	tools := filepath.Join(home, "tools", "bin")
	ambient := floortest.ResolvedTemp(t)
	putExe(t, ambient, "other")
	t.Setenv("PATH", strings.Join([]string{ambient, "/usr/bin", "/bin"}, pathSep))
	putExe(t, tools, "other")
	extra := putExe(t, tools, "extra")

	got := captureHostExec(t)
	var errw bytes.Buffer
	if rc := hostExec(nil, []string{"other"}, io.Discard, &errw, nil); rc != 0 || got.target != filepath.Join(ambient, "other") {
		t.Fatalf("rc=%d target=%s, want the ambient copy\n%s", rc, got.target, errw.String())
	}
	floorBin := filepath.Join(paths.HostFloorDir(), "bin")
	childPath := envValue(got.env, "PATH")
	if want := os.Getenv("PATH") + pathSep + tools + pathSep + floorBin; childPath != want {
		t.Errorf("child PATH =\n  %s\nwant the caller's, then host_path's folder, then the floor's bin/:\n  %s",
			childPath, want)
	}
	errw.Reset()
	if rc := hostExec(nil, []string{"extra"}, io.Discard, &errw, nil); rc != 0 || got.target != extra {
		t.Fatalf("rc=%d target=%s, want the host_path copy %s\n%s", rc, got.target, extra, errw.String())
	}

	// From a bare PATH, a program only in a hint folder: 127 and the miss line, nothing run.
	bare := floortest.ResolvedTemp(t)
	t.Setenv("PATH", bare)
	putExe(t, filepath.Join(home, ".cargo", "bin"), "notes-sync")
	got.execed = false
	errw.Reset()
	if rc := hostExec(nil, []string{"notes-sync"}, io.Discard, &errw, nil); rc != 127 || got.execed {
		t.Fatalf("rc=%d execed=%v, want 127 and no exec: a hint folder never resolves anything\n%s",
			rc, got.execed, errw.String())
	}
	want := "yolo host: notes-sync is not on this launch's PATH, " + bare + ", the PATH yolo was started " +
		`with, then host_path's ~/tools/bin. If notes-sync is installed, add its folder to "host_path" in ` +
		"~/.config/yolo-jail/config.jsonc; ~/.cargo/bin has one.\n"
	if !strings.Contains(errw.String(), want) {
		t.Errorf("the exec's miss lacks the miss line\n  %s\ngot:\n%s", want, errw.String())
	}
	if strings.Contains(errw.String(), "not found in PATH (searched") {
		t.Errorf("the lookup's own error printed beside the miss line:\n%s", errw.String())
	}

	// Started with no PATH at all: the child searches the stand-in system folders, then host_path's,
	// and the line says so (HE-D4, HP-D12).
	t.Setenv("PATH", "")
	errw.Reset()
	if rc := hostExec(nil, []string{"notes-sync"}, io.Discard, &errw, nil); rc != 127 {
		t.Fatalf("rc=%d, want 127\n%s", rc, errw.String())
	}
	if !strings.Contains(errw.String(), "the system folders yolo uses when it is started with no PATH, "+
		"then host_path's ~/tools/bin.") {
		t.Errorf("with no PATH the miss line does not say so:\n%s", errw.String())
	}
}

// TestTheStartingLineSaysWhichPartOfThePathTheTargetCameFrom: the hand-over line names where the
// binary it starts was found — the PATH yolo was started with ("from your PATH"), a `host_path`
// folder, or, for a launch started with no PATH at all, the system folders its child searches in
// its place (HP-D12). Hard-code "from your PATH" again, and the second and third cases fail.
func TestTheStartingLineSaysWhichPartOfThePathTheTargetCameFrom(t *testing.T) {
	floorHostFixture(t, `,"host_path":["~/tools/bin"]`)
	ambient := floortest.ResolvedTemp(t)
	fromCaller := putExe(t, ambient, "other")
	putExe(t, filepath.Join(paths.Home(), "tools", "bin"), "extra")
	t.Setenv("PATH", ambient)
	captureHostExec(t)
	for _, c := range []struct{ bin, want string }{
		{"other", "yolo host: starting other (from your PATH, " + fromCaller + ")\n"},
		{"extra", "yolo host: starting extra (from host_path, ~/tools/bin/extra)\n"},
	} {
		var errw bytes.Buffer
		if rc := hostExec(nil, []string{c.bin}, io.Discard, &errw, nil); rc != 0 {
			t.Fatalf("%s: rc=%d\n%s", c.bin, rc, errw.String())
		}
		if !strings.Contains(errw.String(), c.want) {
			t.Errorf("%s: want the starting line %q\ngot:\n%s", c.bin, c.want, errw.String())
		}
	}

	// Started with no PATH: `sh` is found in the stand-in system folders, and the line says so.
	t.Setenv("PATH", "")
	var errw bytes.Buffer
	if rc := hostExec(nil, []string{"sh"}, io.Discard, &errw, nil); rc != 0 {
		t.Fatalf("sh: rc=%d\n%s", rc, errw.String())
	}
	if want := "yolo host: starting sh (from the system folders yolo uses when it is started with no PATH, "; !strings.Contains(errw.String(), want) {
		t.Errorf("want the starting line to begin %q\ngot:\n%s", want, errw.String())
	}
}

// TestWithNoPathTheGateDoesNotCallHostPathAloneThisLaunchsPath: started with no PATH, the checks
// search host_path's folders alone (HE-D4) while the launch's child searches the system folders ahead
// of them (HP-D12), so the gate's line names what the check searched as "the PATH yolo searched" —
// calling it "this launch's PATH" would describe a PATH the agent does not get.
func TestWithNoPathTheGateDoesNotCallHostPathAloneThisLaunchsPath(t *testing.T) {
	launchPathFixture(t, `,"host_path":["~/tools/bin"],"host_apply_on_launch":true`, hpTool)
	t.Setenv("PATH", "")
	got := runGate(t, "someagent")
	if want := "yolo host: yolo-hp-tool (required by the needpack pack) is not on the PATH yolo searched, " +
		"~/tools/bin, host_path's folders alone: yolo was started with no PATH."; !strings.Contains(got, want) {
		t.Errorf("the gate's no-PATH line lacks %q:\n%s", want, got)
	}
}

// TestAProgramTheFloorCannotHoldIsFoundInHostPath: a selected pack's program `host_floor` leaves out
// is looked up on the child's PATH (OQ-HE11 (a)) — which includes `host_path`'s folders — and, with
// no copy anywhere, exits 127 with the miss line naming the pack it belongs to.
func TestAProgramTheFloorCannotHoldIsFoundInHostPath(t *testing.T) {
	floorHostFixture(t, `,"host_floor":{"floorpack":false},"host_path":["~/tools/bin"]`)
	t.Setenv("PATH", floortest.ResolvedTemp(t))
	inHostPath := putExe(t, filepath.Join(paths.Home(), "tools", "bin"), "floorcli")
	got := captureHostExec(t)
	var errw bytes.Buffer
	if rc := hostExec(nil, []string{"floorcli"}, io.Discard, &errw, nil); rc != 0 || got.target != inHostPath {
		t.Fatalf("rc=%d target=%s, want the host_path copy %s\n%s", rc, got.target, inHostPath, errw.String())
	}
	if !strings.Contains(errw.String(), "yolo has no copy of floorcli") {
		t.Errorf("the no-copy line is missing:\n%s", errw.String())
	}
	if err := os.Remove(inHostPath); err != nil {
		t.Fatal(err)
	}
	errw.Reset()
	if rc := hostExec(nil, []string{"floorcli"}, io.Discard, &errw, nil); rc != 127 {
		t.Fatalf("rc=%d, want 127\n%s", rc, errw.String())
	}
	if !strings.Contains(errw.String(), "yolo host: floorcli (a program of the floorpack pack) is not on "+
		"this launch's PATH, ") {
		t.Errorf("the miss line does not name the program's pack:\n%s", errw.String())
	}
}

// TestTheInstallRunsWithTheLaunchPathAndTheReprobeReadsIt is the design's test 3 (HE-D6): an
// accepted install that lands in a `host_path` folder the PATH yolo was started with lacks is found
// by the re-probe, so the --assert completes, and the installer was handed PATH equal to the launch
// PATH exactly — no floor bin/. Hand the install the inherited environ, or re-probe with a bare
// exec.LookPath, and this fails: the run refuses with "did not produce it".
func TestTheInstallRunsWithTheLaunchPathAndTheReprobeReadsIt(t *testing.T) {
	home, _, _, binDir := depGateFixtureWithConfig(t,
		`{"kind":"program","bin":"gatebin","via":"npm","package":"gatebin"}`)
	cfg := filepath.Join(home, ".config", "yolo-jail", "config.jsonc")
	writeFile(t, cfg, strings.Replace(readFileT(t, cfg), `}]}`, `}],"host_path":["~/tools/bin"]}`, 1))
	tools := filepath.Join(home, "tools", "bin")
	var handed []string
	prev := depInstallRun
	t.Cleanup(func() { depInstallRun = prev })
	depInstallRun = func(cmd string, env []string, _ io.Writer, _ bool) error {
		handed = env
		putExe(t, tools, "gatebin")
		return nil
	}
	var out, errw bytes.Buffer
	rc := applyHost(&out, &errw, false, true, strings.NewReader("y\n"))
	report := out.String() + errw.String()
	if rc != 0 {
		t.Fatalf("rc=%d, want the --assert to complete: the install landed in a host_path folder\n%s", rc, report)
	}
	if got, want := envValue(handed, "PATH"), binDir+pathSep+tools; got != want {
		t.Errorf("the installer was handed PATH=%q, want the launch PATH exactly, %q", got, want)
	}
	if !strings.Contains(report, filepath.Join(tools, "gatebin")) {
		t.Errorf("the re-probe did not name the installed copy:\n%s", report)
	}
}

// TestTheManagerGuessReadsHostPath is the design's test 4: a package manager present only in a
// `host_path` folder names the remedy; with the folder dropped, no manager is found on the PATH and
// the tool has no remedy for this host. Restore the bare exec.LookPath in the manager guess, and the
// first half fails.
func TestTheManagerGuessReadsHostPath(t *testing.T) {
	home, _ := launchPathFixture(t, `,"host_path":["~/mgr/bin"]`,
		`{"kind":"requires","bin":"yolo-hp-tool","install_hints":{"pacman":"yolo-hp-pacman"}}`)
	fakeBinDir(t) // a PATH with no manager at all
	putExe(t, filepath.Join(home, "mgr", "bin"), "pacman")
	if _, report := runCheckDepsT(t); !strings.Contains(report, "sudo pacman -S --noconfirm yolo-hp-pacman") {
		t.Errorf("check-deps did not name pacman's remedy, found only in host_path:\n%s", report)
	}
	cfg := filepath.Join(home, ".config", "yolo-jail", "config.jsonc")
	writeFile(t, cfg, strings.Replace(readFileT(t, cfg), `,"host_path":["~/mgr/bin"]`, "", 1))
	if _, report := runCheckDepsT(t); !strings.Contains(report, "no install hint for this host") {
		t.Errorf("with no manager on the launch PATH, the remedy should be absent:\n%s", report)
	}
}

// TestAWrapperNeverReadsAsItsProgram is the design's test 5 (HE-D5): the wrap dir first on the PATH
// yolo was started with, holding a wrapper for a required tool and no other copy. check-deps reads
// it missing, naming the skipped folder, and the exec exits 127 with the miss line rather than
// running the wrapper — which would exec `yolo host` again.
func TestAWrapperNeverReadsAsItsProgram(t *testing.T) {
	launchPathFixture(t, "", `{"kind":"requires","bin":"yolo-hp-wrapped","install_hints":{"apt":"x"}}`)
	wrap := paths.WrapDir()
	putExe(t, wrap, "yolo-hp-wrapped")
	t.Setenv("PATH", wrap+pathSep+os.Getenv("PATH"))
	rc, report := runCheckDepsT(t)
	if rc != 1 || !strings.Contains(report, "MISSING") ||
		!strings.Contains(report, "(skipping yolo's own ~/.local/share/yolo-jail/bin/wrap)") {
		t.Errorf("check-deps rc=%d read the wrapper as the program:\n%s", rc, report)
	}
	if strings.Contains(report, filepath.Join(wrap, "yolo-hp-wrapped")+"\n") {
		t.Errorf("check-deps named the wrapper as the program's path:\n%s", report)
	}

	orig := prepareOpenAIAuthHost
	prepareOpenAIAuthHost = func(hostPrelaunch, io.Writer) (managedOpenAIHostLaunch, error) { return nil, nil }
	t.Cleanup(func() { prepareOpenAIAuthHost = orig })
	got := captureHostExec(t)
	var errw bytes.Buffer
	if rc := hostExec(nil, []string{"yolo-hp-wrapped"}, io.Discard, &errw, nil); rc != 127 || got.execed {
		t.Fatalf("rc=%d execed=%v (target %s), want 127: the exec ran the wrapper\n%s",
			rc, got.execed, got.target, errw.String())
	}
	if !strings.Contains(errw.String(), "yolo host: yolo-hp-wrapped (required by the needpack pack) is not on "+
		"this launch's PATH, ") {
		t.Errorf("the exec's miss lacks the miss line:\n%s", errw.String())
	}
}
