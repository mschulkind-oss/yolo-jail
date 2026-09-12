package cli

// applyhostdepgate_test.go pins docs/design/report-tiers.md §4.9's dependency gate: on
// `--assert` a missing declared dependency STOPS the run, at the prompt, with nothing written.
//
// EVERY TEST HERE GOES THROUGH applyHost, not through gateHostDeps. That is the rule this
// repo's test discipline turns on — "does it fail if I delete the CALL SITE?" — and it is
// especially load-bearing for a gate: the gate's whole claim is about WHEN it runs relative to
// the render, so a unit test of the function would pass with the call removed from apply.go and
// every writing apply sailing past a missing binary.
//
// The `--assert` fixtures assert on the HOME as well as the report, because "refused" and
// "nothing was written" are two claims and only one of them is in the text.

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// depGateFixture builds a throwaway $HOME whose only pack declares the given contributions plus
// a briefing, and returns the home with the path that briefing would write.
//
// THE BRIEFING IS THE INSTRUMENT. A refusal that says "nothing was written" is checkable only
// against a run that would otherwise have written something, so every fixture here has one
// destination whose presence or absence answers the question the report merely claims.
//
// PATH holds `apt` and nothing else the deps resolve to, so the remedy in the report is the
// detected manager's and the declared binaries are deterministically missing — on this machine
// and on CI (see hostdepstub_test.go for why that sentence has to be true of both).
func depGateFixture(t *testing.T, contributions ...string) (home, briefing, binDir string) {
	t.Helper()
	binDir = fakeBinDir(t, "apt")
	home = t.TempDir()
	packDir := filepath.Join(t.TempDir(), "gatepack")
	writeFile(t, filepath.Join(packDir, "pack.json"),
		`{"name":"gatepack","description":"g","contributes":[`+
			strings.Join(contributions, ",")+`,`+
			`{"kind":"briefing","from":"AGENTS.md","into":".gate/AGENTS.md"}]}`)
	writeFile(t, filepath.Join(packDir, "AGENTS.md"), "Gate prose.\n")
	writeFile(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"),
		`{"packs":[{"source":"file://`+packDir+`","name":"gatepack"}]}`)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	return home, filepath.Join(home, ".gate", "AGENTS.md"), binDir
}

// watchInstalls replaces the install runner with one that records what it was asked to run and
// does whatever `then` says. The default runner is already disarmed for the package
// (hostdepstub_test.go); this is how a test says what should happen INSTEAD.
func watchInstalls(t *testing.T, then func(cmd string) error) *[]string {
	t.Helper()
	var ran []string
	prev := depInstallRun
	t.Cleanup(func() { depInstallRun = prev })
	depInstallRun = func(cmd string, _ io.Writer) error {
		ran = append(ran, cmd)
		if then == nil {
			return nil
		}
		return then(cmd)
	}
	return &ran
}

// A DECLINED INSTALL IS FATAL, AT THE PROMPT, WITH NOTHING WRITTEN — §4.9's `--assert` row, and
// the reversal of env-manager plan OQ-9's "a decline is non-fatal" for this verb (§6 protects
// the reversal; the reason is that an --assert's promise is a READY environment).
//
// The briefing is the measurement: the gate runs before the first render, so a `n` leaves a home
// the apply never touched. An end-of-run refusal would pass every assertion about the exit code
// and fail this one.
func TestApplyHostAssertRefusesADeclinedInstall(t *testing.T) {
	home, briefing, _ := depGateFixture(t,
		`{"kind":"program","bin":"gatebin","via":"npm","package":"gatebin"}`)
	ran := watchInstalls(t, nil)

	var out, errw bytes.Buffer
	rc := applyHost(&out, &errw, false, true, strings.NewReader("n\n"))
	report := out.String() + errw.String()

	if rc != 1 {
		t.Fatalf("a declined install must refuse the run; rc=%d\n%s", rc, report)
	}
	if len(*ran) != 0 {
		t.Errorf("a NO ran the install anyway: %v\n%s", *ran, report)
	}
	if !strings.Contains(report, "refused") || !strings.Contains(report, "declined") {
		t.Errorf("the refusal must say it was declined:\n%s", report)
	}
	if !strings.Contains(report, "Nothing was written") {
		t.Errorf("the refusal must state that nothing was written:\n%s", report)
	}
	// The command is printed ABOVE the prompt, whatever the answer — §4.9 point 4's whole
	// protection, since promptYesNo has no terminal check by contract.
	if !strings.Contains(report, "npm install -g gatebin") {
		t.Errorf("the exact install command must be shown before the prompt:\n%s", report)
	}
	if _, err := os.Stat(briefing); !os.IsNotExist(err) {
		t.Errorf("%s exists — the run wrote before it refused, so \"nothing was written\" is "+
			"not what the refusal means", briefing)
	}
	if _, err := os.Stat(filepath.Join(home, ".gate")); !os.IsNotExist(err) {
		t.Errorf("the apply created a destination directory before refusing")
	}
}

// SILENCE IS NO (§4.9 point 4). promptYesNo reads a nil stdin as NO, so an unattended --assert
// — CI, a script, a cron — refuses rather than installing a package on a machine nobody is
// watching.
func TestApplyHostAssertRefusesAnUnattendedInstall(t *testing.T) {
	_, briefing, _ := depGateFixture(t,
		`{"kind":"program","bin":"gatebin","via":"npm","package":"gatebin"}`)
	ran := watchInstalls(t, nil)

	var out, errw bytes.Buffer
	if rc := applyHost(&out, &errw, false, true, nil); rc != 1 {
		t.Fatalf("an unattended --assert must refuse; rc=%d\n%s%s", rc, out.String(), errw.String())
	}
	if len(*ran) != 0 {
		t.Errorf("nobody answered and yolo installed anyway: %v", *ran)
	}
	if _, err := os.Stat(briefing); !os.IsNotExist(err) {
		t.Error("an unattended refusal wrote into the home")
	}
}

// A MISSING `requires` IS FATAL AND IS NEVER OFFERED AN INSTALL — OQ-RO7, the one place the two
// dep kinds are told apart.
//
// The stdin is `y`, deliberately: there must be no prompt for that answer to be read by. A gate
// that offered the install would consume it and run something, and the kind's definition says
// yolo never installs a `requires`. The remedy is still NAMED — the user is the actor.
func TestApplyHostAssertRefusesAMissingRequiresWithoutOfferingAnInstall(t *testing.T) {
	_, briefing, _ := depGateFixture(t,
		`{"kind":"requires","bin":"reqbin","install_hints":{"apt":"reqbin-pkg"}}`)
	ran := watchInstalls(t, nil)

	var out, errw bytes.Buffer
	rc := applyHost(&out, &errw, false, true, strings.NewReader("y\n"))
	report := out.String() + errw.String()

	if rc != 1 {
		t.Fatalf("a missing `requires` must refuse the run; rc=%d\n%s", rc, report)
	}
	if len(*ran) != 0 {
		t.Errorf("yolo offered to install a `requires`, which contradicts the kind: %v\n%s",
			*ran, report)
	}
	if strings.Contains(report, "[y/N]") {
		t.Errorf("there must be no install prompt for a `requires`:\n%s", report)
	}
	if !strings.Contains(report, "reqbin") || !strings.Contains(report, "Nothing was written") {
		t.Errorf("the refusal must name the binary and say nothing was written:\n%s", report)
	}
	// The remedy is still stated — the blocker has a fix, it is just not yolo's to run.
	if !strings.Contains(report, "sudo apt install -y reqbin-pkg") {
		t.Errorf("a `requires` blocker must still name its remedy:\n%s", report)
	}
	if _, err := os.Stat(briefing); !os.IsNotExist(err) {
		t.Error("the refused run wrote into the home")
	}
}

// A MISSING `program` IS OFFERED, AND A YES INSTALLS IT AND CONTINUES — the other half of
// OQ-RO7, and §4.3's "Installed `rg`; applied: …" row.
//
// The stdin is a PIPE, which is the point of §4.9 point 4: promptYesNo has no terminal check by
// contract and §4.9 rules that none is added here, so a scripted `y` installs. That is the
// ruling being pinned, not an accident of the fixture.
func TestApplyHostAssertInstallsAnOfferedProgramAndCarriesOn(t *testing.T) {
	home, briefing, binDir := depGateFixture(t,
		`{"kind":"program","bin":"gatebin","via":"npm","package":"gatebin"}`)
	// The stub "install" writes into the fixture's own PATH dir: the re-probe has to find the
	// binary through the same PATH the run is using, which is what makes this a test of the
	// re-probe rather than of the seam.
	ran := watchInstalls(t, func(string) error {
		return os.WriteFile(filepath.Join(binDir, "gatebin"), []byte("#!/bin/sh\n"), 0o755)
	})

	var out, errw bytes.Buffer
	rc := applyHost(&out, &errw, false, true, strings.NewReader("y\n"))
	report := out.String() + errw.String()

	if rc != 0 {
		t.Fatalf("an accepted install must let the apply continue; rc=%d\n%s", rc, report)
	}
	if len(*ran) != 1 || (*ran)[0] != "npm install -g gatebin" {
		t.Fatalf("the pack's own declared command is what runs; got %v\n%s", *ran, report)
	}
	// The VERDICT leads with it: an apply that changed the machine's toolchain said something
	// no count below it can express (§4.3).
	if !strings.Contains(report, "Installed `gatebin`") {
		t.Errorf("the verdict must name what it installed:\n%s", report)
	}
	// And the run CONTINUED — the render happened, which is the half a refusal shares no
	// output with.
	if _, err := os.Stat(briefing); err != nil {
		t.Errorf("the apply did not write after installing: %v\n%s", err, report)
	}
	// The re-probe's answer reaches the per-contribution LINE and the COUNTS, not only the
	// verdict — a report still calling the binary missing after installing it would contradict
	// the sentence it ends with. Exactly ONE `MISSING` survives, and it is the blocker the
	// prompt was about, printed before the install ran.
	if !strings.Contains(report, "present at "+filepath.Join(binDir, "gatebin")) {
		t.Errorf("the installed dep must report present, with its resolved path:\n%s", report)
	}
	if n := strings.Count(report, "MISSING"); n != 1 {
		t.Errorf("`MISSING` appears %d times, want the blocker line only:\n%s", n, report)
	}
	if !strings.Contains(report, "1 declared dependency present") ||
		strings.Contains(report, "missing (") {
		t.Errorf("the counts must say present, with no missing clause:\n%s", report)
	}
	_ = home
}

// AN INSTALL THAT RUNS AND LEAVES THE BINARY MISSING IS A DECLINE (§4.9 point 5). The command's
// exit code is not the evidence — an installer that exits 0 and delivers nothing leaves the
// environment exactly as unready as one that failed loudly — so the RE-PROBE decides.
func TestApplyHostAssertRefusesWhenTheInstallProducesNothing(t *testing.T) {
	_, briefing, _ := depGateFixture(t,
		`{"kind":"program","bin":"gatebin","via":"npm","package":"gatebin"}`)
	ran := watchInstalls(t, func(string) error { return nil }) // "succeeds", installs nothing

	var out, errw bytes.Buffer
	rc := applyHost(&out, &errw, false, true, strings.NewReader("y\n"))
	report := out.String() + errw.String()

	if rc != 1 {
		t.Fatalf("an install that produced nothing must refuse the run; rc=%d\n%s", rc, report)
	}
	if len(*ran) != 1 {
		t.Errorf("the install should have been attempted exactly once; got %v", *ran)
	}
	if !strings.Contains(report, "did not produce it") {
		t.Errorf("the refusal must say the install did not produce the binary:\n%s", report)
	}
	if _, err := os.Stat(briefing); !os.IsNotExist(err) {
		t.Error("the refused run wrote into the home")
	}
}

// THE DRY RUN IS UNCHANGED: it reports the same blocker, never prompts, never installs, and
// exits 0 (OQ-RO5, whose pin §4.9 keeps deliberately). Its whole output IS the finding.
func TestApplyHostDryRunReportsAMissingDepAndNeverPrompts(t *testing.T) {
	_, briefing, _ := depGateFixture(t,
		`{"kind":"program","bin":"gatebin","via":"npm","package":"gatebin"}`)
	ran := watchInstalls(t, nil)

	var out, errw bytes.Buffer
	// A `y` on stdin that nothing may read: the dry run writes nothing, so it has nothing to
	// confirm and nothing to offer.
	rc := applyHost(&out, &errw, false, false, strings.NewReader("y\n"))
	report := out.String() + errw.String()

	if rc != 0 {
		t.Fatalf("the dry run must stay exit 0 over a missing dep; rc=%d\n%s", rc, report)
	}
	if len(*ran) != 0 {
		t.Errorf("the dry run installed something: %v\n%s", *ran, report)
	}
	if strings.Contains(report, "[y/N]") {
		t.Errorf("the dry run must not prompt:\n%s", report)
	}
	if !strings.Contains(report, "An --assert would NOT complete") {
		t.Errorf("the verdict must say an --assert would not complete:\n%s", report)
	}
	if !strings.Contains(report, "npm install -g gatebin") {
		t.Errorf("the dry run must still show the remedy:\n%s", report)
	}
	if _, err := os.Stat(briefing); !os.IsNotExist(err) {
		t.Error("the dry run wrote into the home")
	}
}

// THE RUNNER RUNS A COMMAND, NOT AN ARGV. A pack's install hint is a line a user would type —
// depcheck turns an `installer` program into `curl -fsSL <url> | sh` — so splitting it on spaces
// would mangle exactly the remedy the pack wrote down.
//
// This is the one test that calls the real runner (the seam is disarmed package-wide, see
// hostdepstub_test.go), and it stays inside a temp dir: the commands here write a file and
// return a status, which is what the shell property needs and all it needs.
func TestRunDepInstallCommandGoesThroughAShell(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "out")

	var log bytes.Buffer
	// A PIPE and a REDIRECT: both are shell syntax, and both are shapes real hints have.
	if err := runDepInstallCommand("echo installed | tr a-z A-Z > "+out, &log); err != nil {
		t.Fatalf("a well-formed command must succeed: %v (%s)", err, log.String())
	}
	body, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("the command did not run through a shell: %v", err)
	}
	if strings.TrimSpace(string(body)) != "INSTALLED" {
		t.Errorf("shell output = %q, want the piped form", body)
	}

	// A failing command is an ERROR, and its output reaches the writer — which is how the
	// gate's refusal gets to say what went wrong rather than only that something did.
	log.Reset()
	if err := runDepInstallCommand("echo boom >&2; exit 3", &log); err == nil {
		t.Error("a non-zero exit must be reported as an error")
	}
	if !strings.Contains(log.String(), "boom") {
		t.Errorf("the command's own output must reach the report, got %q", log.String())
	}
}
