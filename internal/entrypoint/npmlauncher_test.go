package entrypoint

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// npmlauncher_test.go pins the npm `program` launcher's VERSION handling.
//
// The defect it exists for: the template appended a literal `@latest` to the declared
// package string, so `foo@1.2.3` was installed as `foo@1.2.3@latest` — npm resolves
// nothing for that, and a version was therefore inexpressible rather than merely
// unpinned (docs/design/trust-paths.md §1, top row).
//
// Two halves, deliberately separate: a table over the PARSER (npm's scoped-package rule is
// the whole subtlety), and behavioural runs of the generated launcher against a fake `npm`
// that logs its argv. The second half is what makes the "pinned packages do not poll the
// registry" claim measurable instead of a comment — a text assertion on the template would
// pass just as happily if the poll ran anyway.

func TestSplitNpmSpec(t *testing.T) {
	cases := []struct {
		spec        string
		wantName    string
		wantVersion string
		wantInstall string
	}{
		// Unversioned: both spellings, scoped and bare. These are what every shipped pack
		// declares today and they must keep resolving to latest, hourly poll included.
		{"name", "name", "", "name@latest"},
		{"@scope/name", "@scope/name", "", "@scope/name@latest"},
		// Versioned. The scoped form is the one the old code could not represent at all.
		{"name@1.2.3", "name", "1.2.3", "name@1.2.3"},
		{"@scope/name@1.2.3", "@scope/name", "1.2.3", "@scope/name@1.2.3"},
		// A selector is not always an exact version: npm takes a dist-tag or a range in
		// the same position, and both are legitimate declarations.
		{"name@next", "name", "next", "name@next"},
		{"name@^1.0.0", "name", "^1.0.0", "name@^1.0.0"},
		{"@scope/name@next", "@scope/name", "next", "@scope/name@next"},
		{"name@>=1.0.0 <2.0.0", "name", ">=1.0.0 <2.0.0", "name@>=1.0.0 <2.0.0"},
		// An alias/URL selector keeps its own @ — we split once and pass the rest through.
		{"name@npm:other@1.2.3", "name", "npm:other@1.2.3", "name@npm:other@1.2.3"},
		// Degenerate spellings. A trailing @ is npm-invalid, so read it as "no version".
		{"name@", "name", "", "name@latest"},
		{"", "", "", "@latest"},
		{"  name@1.2.3  ", "name", "1.2.3", "name@1.2.3"},
		// The real shipped packages, so the table says what production looks like.
		{"@anthropic-ai/claude-code", "@anthropic-ai/claude-code", "", "@anthropic-ai/claude-code@latest"},
		{"opencode-ai", "opencode-ai", "", "opencode-ai@latest"},
	}
	for _, tc := range cases {
		name, version := splitNpmSpec(tc.spec)
		if name != tc.wantName || version != tc.wantVersion {
			t.Errorf("splitNpmSpec(%q) = (%q, %q), want (%q, %q)",
				tc.spec, name, version, tc.wantName, tc.wantVersion)
		}
		if got := npmInstallSpec(name, version); got != tc.wantInstall {
			t.Errorf("npmInstallSpec for %q = %q, want %q", tc.spec, got, tc.wantInstall)
		}
		if got, want := npmSpecIsPinned(version), tc.wantVersion != ""; got != want {
			t.Errorf("npmSpecIsPinned for %q = %v, want %v", tc.spec, got, want)
		}
	}
}

// TestNpmLauncherBodyCarriesNameAndSpecSeparately pins the structural half: the rendered
// launcher must hold the NAME and the INSTALL SPEC as two different variables. Collapsing
// them is exactly the old bug, and it is invisible from the outside until npm is called.
//
// The assertions are whole LINES rather than substrings, and the expected right-hand side
// runs through shquote.Quote — which is the splice contract on npmLauncherTemplate stated as
// a test. It used to assert `PKG="<value>"`, the raw-inside-double-quotes form; a substring
// check on `PKG=<value>` would still pass against that spelling, so pinning the whole line
// is what makes this assertion strictly stronger rather than merely different.
func TestNpmLauncherBodyCarriesNameAndSpecSeparately(t *testing.T) {
	cases := []struct{ pkg, wantPKG, wantSPEC, wantPINNED string }{
		{"foo", "foo", "foo@latest", "0"},
		{"foo@1.2.3", "foo", "foo@1.2.3", "1"},
		{"@scope/foo", "@scope/foo", "@scope/foo@latest", "0"},
		{"@scope/foo@2.0.0", "@scope/foo", "@scope/foo@2.0.0", "1"},
		// A selector npm accepts and the shell does not: a range carries a space and a
		// `<`, so raw-in-double-quotes rendered `SPEC="name@>=1.0.0 <2.0.0"` and quoting
		// is the only reason the launcher still parses. This row is here to make the
		// contract load-bearing on a value a real pack may write.
		{"foo@>=1.0.0 <2.0.0", "foo", "foo@>=1.0.0 <2.0.0", "1"},
	}
	for _, tc := range cases {
		body := npmAgentLauncher(&packdecl.Install{Kind: "npm", Bin: "foo", Package: tc.pkg},
			"/stamps", filepath.Join(t.TempDir(), "receipts.jsonl"), true)
		for _, want := range []string{
			"\nPKG=" + shquote.Quote(tc.wantPKG) + "\n",
			"\nSPEC=" + shquote.Quote(tc.wantSPEC) + "\n",
			"\nPINNED=" + shquote.Quote(tc.wantPINNED) + " ",
		} {
			if !strings.Contains(body, want) {
				t.Errorf("package %q: launcher missing %q\n%s", tc.pkg, want, body)
			}
		}
		// The literal that caused the defect must not survive anywhere in a versioned
		// launcher: `$PKG@latest` is what turned foo@1.2.3 into foo@1.2.3@latest.
		if strings.Contains(body, "$PKG@latest") {
			t.Errorf("package %q: launcher still appends @latest to the package string\n%s", tc.pkg, body)
		}
	}
	// The package-manager launcher is the second site that hardcoded it.
	e := NewEnv(map[string]string{"JAIL_HOME": t.TempDir()})
	if err := GeneratePackageManagerLaunchers(e); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(e.LaunchDir(), "pnpm"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "$PKG@latest") {
		t.Errorf("pnpm launcher still hardcodes @latest:\n%s", body)
	}
	// ...and its behaviour is unchanged: pnpm carries no version, so it still installs
	// pnpm@latest byte-for-byte.
	if !strings.Contains(string(body), "\nSPEC=pnpm@latest ") {
		t.Errorf("pnpm must still resolve to pnpm@latest:\n%s", body)
	}
}

// --- behavioural half -------------------------------------------------------------

// npmProbe is one temp jail-home wired with a fake `npm` and a fake `jq`, so the generated
// launcher can be RUN and its registry traffic observed.
type npmProbe struct {
	home         string
	fakeBin      string
	logPath      string
	receiptsPath string
	// pathDir, when set, is the WHOLE $PATH the launcher runs with, replacing
	// "fakeBin:$PATH". Set by hideJQ; empty for every other caller.
	pathDir string
	// updates is this jail's agent_updates verdict for the pack, baked into the launcher
	// at generation time. TRUE for every probe unless a cell says otherwise, because open
	// is the DEFAULT the design rules (§3.5) and a harness defaulting the other way would
	// quietly exercise the frozen path everywhere.
	updates bool
	// verb is the pack's declared update verb (OQ-PD14). Empty for every probe but the
	// one cell that measures it: no shipped npm pack declares one.
	verb []string
}

// newNpmProbe writes the fakes. `npm install` materializes the binary and the
// node_modules package.json the launcher reads; `npm view` answers with
// $FAKE_LATEST. Every invocation is appended to a log, which is the measurement.
//
// $FAKE_INSTALL_FAIL makes `npm install` fail the way a real one does — non-zero, nothing
// written — which is the only way to reach the branch that decides whether a pinned
// launcher may record a spec it never got.
//
// $FAKE_VIEW_FAIL is the same injection for the other half of the network: `npm view`
// exits non-zero and prints nothing, which is what an offline jail, a proxy refusing
// CONNECT and a registry outage all look like from in here. It has to be separately
// injectable because the launcher's two modes must answer it DIFFERENTLY — the
// informational poll swallows it, the explicit update must not.
func newNpmProbe(t *testing.T, bin string) *npmProbe {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not found")
	}
	home := t.TempDir()
	fakeBin := filepath.Join(home, "fakebin")
	if err := os.MkdirAll(fakeBin, 0o755); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(home, "npm.log")

	npm := `#!/bin/bash
printf '%s\n' "$*" >> "` + logPath + `"
case "${1:-}" in
install)
    if [ -n "${FAKE_INSTALL_FAIL:-}" ]; then
        echo "npm ERR! network unreachable" >&2
        exit 1
    fi
    spec="${@: -1}"
    name="${spec%@*}"
    ver="${spec##*@}"
    ver="${FAKE_INSTALLED_VERSION:-$ver}"
    mkdir -p "$NPM_CONFIG_PREFIX/bin" "$NPM_CONFIG_PREFIX/lib/node_modules/$name"
    printf '{"version":"%s"}\n' "$ver" > "$NPM_CONFIG_PREFIX/lib/node_modules/$name/package.json"
    # "echo RAN \$*" rather than a bare "echo RAN": the launcher's declared-verb branch
    # (OQ-PD14) runs this same binary with the vendor's argv, and without the argv in the
    # output "the verb ran" and "the program was launched" are the same observation.
    printf '#!/bin/sh\necho RAN $*\n' > "$NPM_CONFIG_PREFIX/bin/` + bin + `"
    chmod +x "$NPM_CONFIG_PREFIX/bin/` + bin + `"
    ;;
view)
    if [ -n "${FAKE_VIEW_FAIL:-}" ]; then
        echo "npm ERR! code ENOTFOUND" >&2
        exit 1
    fi
    echo "${FAKE_LATEST:-0}"
    ;;
esac
exit 0
`
	// A 5-line stand-in for `jq -r .version <file>`: the launcher only ever asks that one
	// question, and depending on the developer's jq being present would make this test
	// skip silently on the machines that matter least.
	jq := `#!/bin/bash
f="${@: -1}"
[ -f "$f" ] || exit 1
line=$(tr -d ' \t' < "$f")
line="${line#*\"version\":\"}"
printf '%s\n' "${line%%\"*}"
`
	for name, body := range map[string]string{"npm": npm, "jq": jq} {
		if err := os.WriteFile(filepath.Join(fakeBin, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return &npmProbe{
		home:    home,
		fakeBin: fakeBin,
		logPath: logPath,
		updates: true,
		// Deliberately a path whose PARENT does not exist: the receipt writer has to
		// create it itself, because macos-user stages no <ws>/.yolo.
		receiptsPath: filepath.Join(home, "ws", ".yolo", "receipts.jsonl"),
	}
}

// hideJQ re-points the probe at a $PATH with NO jq on it, which is a state a real launch can
// genuinely be in: macos-user runs these same generated launchers natively on the host, under
// `env -i`, against whatever that Mac has — and jq is not a macOS builtin. The container
// bakes one, so this is the one backend where the difference shows.
//
// It is a real absence rather than a jq that fails, because the two are not the same
// measurement: a stub that exits non-zero would still be found by `command -v` and would
// still let a helper that shells out to it "work". The dir holds only the fake npm plus
// symlinks to the coreutils the launcher and the receipt writer actually invoke; anything
// missing from that list turns into a visible failure of the test, not a silent pass.
func (p *npmProbe) hideJQ(t *testing.T) {
	t.Helper()
	dir := filepath.Join(p.home, "nojqbin")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, tool := range []string{
		"mkdir", "rm", "touch", "date", "tr", "cut", "chmod", "cat", "stat", "wc",
		"sha256sum", "sed", "env",
	} {
		real, err := exec.LookPath(tool)
		if err != nil {
			continue // not every one is needed on every path; a missing one that IS shows up
		}
		if err := os.Symlink(real, filepath.Join(dir, tool)); err != nil {
			t.Fatal(err)
		}
	}
	npm, err := os.ReadFile(filepath.Join(p.fakeBin, "npm"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "npm"), npm, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "jq")); !os.IsNotExist(err) {
		t.Fatalf("the point of this PATH is that jq is not on it (err=%v)", err)
	}
	p.pathDir = dir
}

// receipts returns the receipt lines the launcher appended, or nil when it wrote none.
func (p *npmProbe) receipts(t *testing.T) []map[string]any {
	t.Helper()
	return readReceipts(t, p.receiptsPath)
}

// run renders the launcher for pkg, executes it, and returns the fake npm's argv log.
func (p *npmProbe) run(t *testing.T, bin, pkg string, env ...string) []string {
	t.Helper()
	log, _ := p.runOut(t, bin, pkg, env...)
	return log
}

// runOut is run plus the launcher's own combined output. The second value is what makes
// two things measurable that the npm argv log cannot see: the informational "an update is
// available" line (which by design touches npm only for the `view`), and whether the
// launcher exec'd the real binary — the fake one prints RAN, so its absence is the proof
// that update mode refreshed instead of launching.
//
// It FAILS the test on a non-zero exit, which is right for every caller that expects the
// launcher to succeed. A caller measuring the exit code itself wants runStatus.
func (p *npmProbe) runOut(t *testing.T, bin, pkg string, env ...string) ([]string, string) {
	t.Helper()
	log, out, rc := p.runStatus(t, bin, pkg, env...)
	if rc != 0 {
		t.Fatalf("launcher failed: exit %d\n%s", rc, out)
	}
	return log, out
}

// runStatus is runOut without the verdict: it hands back the launcher's EXIT CODE instead
// of failing on it.
//
// That code is not a detail of the harness — it is the launcher's only channel to
// `yolo pack update`, which walks a list of programs and cannot see into any of them
// (it does not know this jail's npm prefix, and npm's own "npm ERR!" lines are
// indistinguishable from the chatter a SUCCESSFUL install prints). A test that could only
// assert on stdout would pass just as happily against a launcher that reported every
// outcome as success.
func (p *npmProbe) runStatus(t *testing.T, bin, pkg string, env ...string) ([]string, string, int) {
	t.Helper()
	body := npmAgentLauncher(
		&packdecl.Install{Kind: "npm", Bin: bin, Package: pkg, UpdateVerb: p.verb},
		filepath.Join(p.home, "stamps"),
		// The receipts path is BAKED at generation time, so a harness that let it
		// default would append to the developer's real /workspace/.yolo on every run
		// of this file. It is pointed at the probe's temp home instead, which is also
		// what makes the receipt assertions below readable.
		p.receiptsPath,
		p.updates,
	)
	script := filepath.Join(p.home, bin)
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	launchPath := p.fakeBin + ":" + os.Getenv("PATH")
	if p.pathDir != "" {
		launchPath = p.pathDir
	}
	cmd := exec.Command(script)
	cmd.Env = append([]string{
		"HOME=" + p.home,
		"PATH=" + launchPath,
	}, env...)
	out, err := cmd.CombinedOutput()
	rc := 0
	if ee, ok := err.(*exec.ExitError); ok {
		rc = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("launcher could not be run at all: %v\n%s", err, out)
	}
	return p.log(t), string(out), rc
}

func (p *npmProbe) log(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile(p.logPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	trimmed := strings.TrimRight(string(data), "\n")
	if trimmed == "" {
		return nil // Split would hand back one empty line and read as "npm ran"
	}
	return strings.Split(trimmed, "\n")
}

func (p *npmProbe) truncateLog(t *testing.T) {
	t.Helper()
	if err := os.WriteFile(p.logPath, nil, 0o644); err != nil {
		t.Fatal(err)
	}
}

// agePastInterval backdates the launcher's stamp beyond UPDATE_INTERVAL (3600s), which is
// the trigger for the hourly registry poll.
func (p *npmProbe) agePastInterval(t *testing.T, bin string) {
	t.Helper()
	stamp := filepath.Join(p.home, "stamps", bin+".stamp")
	old := time.Now().Add(-3 * time.Hour)
	if err := os.Chtimes(stamp, old, old); err != nil {
		t.Fatal(err)
	}
}

func hasArgv(log []string, substr string) bool {
	for _, line := range log {
		if strings.Contains(line, substr) {
			return true
		}
	}
	return false
}

// TestNpmLauncherInstallsTheDeclaredVersion is the defect, reduced: a scoped package with a
// version must reach npm intact.
func TestNpmLauncherInstallsTheDeclaredVersion(t *testing.T) {
	p := newNpmProbe(t, "tool")
	log := p.run(t, "tool", "@scope/tool@2.0.0")

	if !hasArgv(log, "install -g --prefer-online @scope/tool@2.0.0") {
		t.Errorf("declared version did not reach npm:\n%s", strings.Join(log, "\n"))
	}
	if hasArgv(log, "@latest") {
		t.Errorf("a declared version must not be re-suffixed with @latest:\n%s", strings.Join(log, "\n"))
	}
}

// TestUnpinnedNpmLauncherUpdatesPastItsStamp is the evergreen ruling, reduced to its one
// observable claim (program-delivery.md §3.5, OQ-PD12, 2026-09-03).
//
// THIS CELL HAS BEEN INVERTED TWICE, and the file keeps the record rather than deleting it,
// because which behaviour is defended has changed twice and each turn was deliberate:
//
//	until 2026-08-17  the hourly check REINSTALLED whenever the registry had moved
//	2026-08-18        trust-paths.md §1 row 1 made it report only — a binary that changes
//	                  between two invocations with nobody present has no act to pin to
//	2026-09-03        OQ-PD3 was NARROWED: that ruling covers PROJECT dependencies, and an
//	                  agent CLI is not one. It wants to be current, and there is no pin for
//	                  it to be reproducible against
//
// The 2026-08-18 objection is answered rather than dropped. The trigger is the user's own
// invocation of the agent, so somebody IS present — the person who just typed the program's
// name — and the failure is scoped to that one command.
//
// The first install is untouched by any of it: a fresh jail home has no version to keep.
func TestUnpinnedNpmLauncherUpdatesPastItsStamp(t *testing.T) {
	p := newNpmProbe(t, "tool")

	log := p.run(t, "tool", "tool", "FAKE_INSTALLED_VERSION=1.0.0")
	if !hasArgv(log, "install -g --prefer-online tool@latest") {
		t.Fatalf("an unversioned package must still install @latest on FIRST use:\n%s",
			strings.Join(log, "\n"))
	}

	// Stamp older than the interval, and the registry has moved. The launcher must ask,
	// must install, and must say which two versions it moved between.
	p.agePastInterval(t, "tool")
	p.truncateLog(t)
	log, out := p.runOut(t, "tool", "tool", "FAKE_LATEST=9.9.9")
	if !hasArgv(log, "view tool version") {
		t.Errorf("the launcher must ask the registry what is current:\n%s", strings.Join(log, "\n"))
	}
	if !hasArgv(log, "install -g --prefer-online tool@latest") {
		t.Errorf("an agent dependency past its stamp must be UPDATED, not merely reported "+
			"on — the report was measured never to be acted upon (claude.stamp unmoved for "+
			"nine days, OQ-PD8):\n%s", strings.Join(log, "\n"))
	}
	if !strings.Contains(out, "1.0.0 → 9.9.9") {
		t.Errorf("the update must say which versions it moved between:\n%s", out)
	}
	// And it still launches: an update is something that happens on the way to running
	// the program, never instead of it.
	if !strings.Contains(out, "RAN") {
		t.Errorf("the launcher must exec the tool after updating it:\n%s", out)
	}
}

// TestUnpinnedNpmLauncherFrozenByPolicyNeverTouchesTheRegistry: `agent_updates` off means
// the emitted launcher carries no update branch at all — no `npm view`, no install, no
// network. Baked at generation time, so nothing in the jail can talk it into one.
func TestUnpinnedNpmLauncherFrozenByPolicyNeverTouchesTheRegistry(t *testing.T) {
	p := newNpmProbe(t, "tool")
	p.run(t, "tool", "tool", "FAKE_INSTALLED_VERSION=1.0.0")

	p.updates = false
	p.agePastInterval(t, "tool")
	p.truncateLog(t)
	log, out := p.runOut(t, "tool", "tool", "FAKE_LATEST=9.9.9")
	if len(log) != 0 {
		t.Errorf("a frozen pack must keep the launcher off the registry entirely, got:\n%s",
			strings.Join(log, "\n"))
	}
	if !strings.Contains(out, "RAN") {
		t.Errorf("...and must still launch the program:\n%s", out)
	}
}

// TestUnpinnedNpmLauncherRunsADeclaredVerbInsteadOfNpm: no shipped npm pack declares an
// update verb, but the projection carries it for every `via`, so the launcher has to honour
// it — a field the manifest accepts and the launcher ignores is a declaration that silently
// does nothing, which is the defect OQ-PD14 exists to close.
func TestUnpinnedNpmLauncherRunsADeclaredVerbInsteadOfNpm(t *testing.T) {
	p := newNpmProbe(t, "tool")
	p.run(t, "tool", "tool", "FAKE_INSTALLED_VERSION=1.0.0")

	p.verb = []string{"self-update"}
	p.agePastInterval(t, "tool")
	p.truncateLog(t)
	log, out := p.runOut(t, "tool", "tool", "FAKE_LATEST=9.9.9")
	if hasArgv(log, "view") || hasArgv(log, "install") {
		t.Errorf("a declared verb REPLACES the npm refresh — the vendor's updater decides "+
			"what to fetch, not this launcher:\n%s", strings.Join(log, "\n"))
	}
	if !strings.Contains(out, "RAN self-update") {
		t.Errorf("the declared verb must reach the program:\n%s", out)
	}
}

// TestNpmLauncherIsANoOpWhenReEntered is trap 4 on the npm side, and the symptom it
// prevents is a fork bomb rather than a wrong answer: B2 puts the launch dir ahead of
// $NPM_CONFIG_PREFIX/bin, so a bare-name call of the program from inside its own update —
// an npm postinstall that runs the tool — resolves back to this script.
//
// Driven from the outside, with the guard variable already naming this bin, which is
// exactly the environment such a child would see.
func TestNpmLauncherIsANoOpWhenReEntered(t *testing.T) {
	p := newNpmProbe(t, "tool")
	p.run(t, "tool", "tool", "FAKE_INSTALLED_VERSION=1.0.0")

	p.agePastInterval(t, "tool")
	p.truncateLog(t)
	log, out := p.runOut(t, "tool", "tool", "FAKE_LATEST=9.9.9",
		"_YOLO_LAUNCHER_ACTIVE=:tool")
	if len(log) != 0 {
		t.Errorf("a re-entered launcher must run NO update logic, got:\n%s",
			strings.Join(log, "\n"))
	}
	if !strings.Contains(out, "RAN") {
		t.Errorf("...and must exec the program:\n%s", out)
	}

	// A DIFFERENT bin in the guard must not suppress this one — the variable is a set, not
	// a boolean, or the first launcher to run would freeze every other.
	q := newNpmProbe(t, "tool")
	q.run(t, "tool", "tool", "FAKE_INSTALLED_VERSION=1.0.0")
	q.agePastInterval(t, "tool")
	q.truncateLog(t)
	if log := q.run(t, "tool", "tool", "FAKE_LATEST=9.9.9",
		"_YOLO_LAUNCHER_ACTIVE=:someotherbin"); len(log) == 0 {
		t.Error("another bin in the guard set must not suppress this one's update")
	}
}

// TestNpmLauncherProceedsWithoutUpdatingWhenTheLockIsHeld is §3.5's contention rule on the
// npm prefix: cannot take the lock => run what is installed, say so, never wait, never fail.
func TestNpmLauncherProceedsWithoutUpdatingWhenTheLockIsHeld(t *testing.T) {
	p := newNpmProbe(t, "tool")
	p.run(t, "tool", "tool", "FAKE_INSTALLED_VERSION=1.0.0")
	if err := os.MkdirAll(filepath.Join(p.home, ".npm-global", ".yolo-update.lock"), 0o755); err != nil {
		t.Fatal(err)
	}

	p.agePastInterval(t, "tool")
	p.truncateLog(t)
	log, out := p.runOut(t, "tool", "tool", "FAKE_LATEST=9.9.9")
	if len(log) != 0 {
		t.Errorf("a held lock means no npm traffic at all, got:\n%s", strings.Join(log, "\n"))
	}
	if !strings.Contains(out, "another update is in progress") {
		t.Errorf("...and the launcher must say so:\n%s", out)
	}
	if !strings.Contains(out, "RAN") {
		t.Errorf("...and must still launch the installed version:\n%s", out)
	}
}

// TestUnpinnedNpmLauncherTimeline walks the whole life of one jail home in the order a
// user meets it, because each phase is only meaningful against the others: "install ran"
// is correct cold and a defect warm, and a test that checked either alone would pass while
// the launcher did the wrong thing in the other.
//
// The phases and the exact npm traffic each is allowed:
//
//	cold        (no binary)                  install, no view — the first install is not an update
//	warm        (binary, fresh stamp)        NOTHING — the throttle, and the common case
//	stamp-aged  (binary, stamp > interval)   view AND install — evergreen
//	warm again  (stamp refreshed by the update) NOTHING — the update re-armed the throttle
func TestUnpinnedNpmLauncherTimeline(t *testing.T) {
	p := newNpmProbe(t, "tool")

	// COLD.
	log, out := p.runOut(t, "tool", "tool", "FAKE_INSTALLED_VERSION=1.0.0")
	if !hasArgv(log, "install -g --prefer-online tool@latest") {
		t.Fatalf("cold: the first invocation in a fresh jail home must install:\n%s",
			strings.Join(log, "\n"))
	}
	if hasArgv(log, "view") {
		t.Errorf("cold: there is nothing to compare against yet, so nothing to ask the "+
			"registry:\n%s", strings.Join(log, "\n"))
	}
	if !strings.Contains(out, "RAN") {
		t.Errorf("cold: the launcher must still exec the tool it just installed:\n%s", out)
	}

	// WARM: same home, stamp fresh, registry moved. The throttle means silence.
	p.truncateLog(t)
	if log := p.run(t, "tool", "tool", "FAKE_LATEST=9.9.9"); len(log) != 0 {
		t.Errorf("warm: a fresh stamp must keep the launcher off the network entirely — "+
			"got:\n%s", strings.Join(log, "\n"))
	}

	// STAMP-AGED: the update fires, asks once, and installs what it found.
	p.agePastInterval(t, "tool")
	p.truncateLog(t)
	log = p.run(t, "tool", "tool", "FAKE_LATEST=9.9.9")
	if !hasArgv(log, "view tool version") {
		t.Errorf("stamp-aged: the update must ask:\n%s", strings.Join(log, "\n"))
	}
	if !hasArgv(log, "install -g --prefer-online tool@latest") {
		t.Errorf("stamp-aged: the update must install what it found:\n%s", strings.Join(log, "\n"))
	}

	// WARM AGAIN: the update touched the stamp, so the next invocation is silent. Without
	// that the launcher would go to the registry on every single invocation for as long as
	// the fake stayed ahead of the fake installed version.
	p.truncateLog(t)
	if log := p.run(t, "tool", "tool", "FAKE_LATEST=9.9.9"); len(log) != 0 {
		t.Errorf("warm again: an update must re-arm the throttle:\n%s", strings.Join(log, "\n"))
	}
}

// TestNpmLauncherUpdateModeRefreshesWithoutLaunching pins what update mode still is, now
// that it is no longer the ONLY resolver (which is what this cell was called until §3.5's
// evergreen ruling gave the launch path an update branch of its own).
//
// Its remaining job is the one the launcher cannot do: refresh NOW, ignoring the stamp and
// ignoring `agent_updates`, and EXIT rather than exec — `yolo pack update` walks a list of
// programs and must not start any of them.
func TestNpmLauncherUpdateModeRefreshesWithoutLaunching(t *testing.T) {
	p := newNpmProbe(t, "tool")
	p.run(t, "tool", "tool", "FAKE_INSTALLED_VERSION=1.0.0")

	// Stamp deliberately left FRESH: an explicit update is not throttled. The user asked.
	p.truncateLog(t)
	log, out := p.runOut(t, "tool", "tool", "YOLO_PACK_UPDATE=1",
		"FAKE_LATEST=9.9.9", "FAKE_INSTALLED_VERSION=9.9.9")

	if !hasArgv(log, "view tool version") {
		t.Errorf("update mode must resolve what the registry has:\n%s", strings.Join(log, "\n"))
	}
	if !hasArgv(log, "install -g --prefer-online tool@latest") {
		t.Errorf("update mode must install it — it is the ONLY act that may:\n%s",
			strings.Join(log, "\n"))
	}
	if strings.Contains(out, "RAN") {
		t.Errorf("update mode must not exec the tool: `yolo pack update` is refreshing a "+
			"list of programs, not starting one:\n%s", out)
	}

	// Already current: still no install, and still no launch.
	p.truncateLog(t)
	log, out = p.runOut(t, "tool", "tool", "YOLO_PACK_UPDATE=1", "FAKE_LATEST=9.9.9")
	if hasArgv(log, "install") {
		t.Errorf("update mode must not reinstall a version that is already current:\n%s",
			strings.Join(log, "\n"))
	}
	if !strings.Contains(out, "already current") {
		t.Errorf("update mode must say it found nothing to do:\n%s", out)
	}
}

// TestNpmLauncherUpdateModeReportsAFailedInstall is the split's silent no-op, closed.
//
// Update mode exits instead of exec'ing, so it never reaches the `-x "$REAL_BIN"` test at
// the bottom of the script — the launch path's verdict, and the only place the launcher
// used to decide anything about failure. With an unconditional `exit 0` after `_update`,
// every failure inside it came back as SUCCESS: `yolo pack update` printed npm's error and
// returned 0, so a scripted `yolo pack update && …` proceeded and a user was told nothing
// had gone wrong while the agent CLI was still the old one — or, on a cold home, absent.
//
// That is the failure this whole mechanism was built to prevent, arriving through the act
// meant to replace it, and it is invisible from inside Go: `internal/cli`'s seam test
// stubs the refresh out, so the plumbing carrying a non-zero was pinned while the launcher
// could not produce one. It only shows up by RUNNING the script against a failing npm.
//
// Both shapes are covered because they reach `_do_install` down different branches — the
// unpinned one after a registry comparison, the pinned one after a spec-file comparison —
// and a `return` that drops the status on either is the same defect.
func TestNpmLauncherUpdateModeReportsAFailedInstall(t *testing.T) {
	t.Run("unpinned", func(t *testing.T) {
		p := newNpmProbe(t, "tool")
		p.run(t, "tool", "tool", "FAKE_INSTALLED_VERSION=1.0.0")
		p.truncateLog(t)

		log, out, rc := p.runStatus(t, "tool", "tool", "YOLO_PACK_UPDATE=1",
			"FAKE_LATEST=9.9.9", "FAKE_INSTALL_FAIL=1")
		if !hasArgv(log, "install -g --prefer-online tool@latest") {
			t.Fatalf("the update must at least attempt the install:\n%s", strings.Join(log, "\n"))
		}
		if rc == 0 {
			t.Errorf("an update whose `npm install` failed must exit non-zero — it is the "+
				"only signal `yolo pack update` gets, and 0 here means a user is told the "+
				"refresh worked while the old binary is still in place:\n%s", out)
		}
	})

	t.Run("pinned, declaration moved", func(t *testing.T) {
		p := newNpmProbe(t, "tool")
		p.run(t, "tool", "tool@1.2.3")
		p.truncateLog(t)

		log, out, rc := p.runStatus(t, "tool", "tool@1.3.0", "YOLO_PACK_UPDATE=1",
			"FAKE_INSTALL_FAIL=1")
		if !hasArgv(log, "install -g --prefer-online tool@1.3.0") {
			t.Fatalf("a moved pin must still be attempted:\n%s", strings.Join(log, "\n"))
		}
		if rc == 0 {
			t.Errorf("a pinned update that could not converge on its declaration must say so "+
				"in its exit code:\n%s", out)
		}
	})

	// The LAUNCH path is deliberately the other way round and must stay that way: a failed
	// install there is not the verdict, because the question that path has is "is there
	// something to exec?" — and after a failed UPGRADE there still is.
	t.Run("launch path keeps its own verdict", func(t *testing.T) {
		p := newNpmProbe(t, "tool")
		p.run(t, "tool", "tool", "FAKE_INSTALLED_VERSION=1.0.0")
		p.truncateLog(t)

		// A failed pinned upgrade: the previous binary is still there, so the launch
		// succeeds and runs it.
		_, out, rc := p.runStatus(t, "tool", "tool@2.0.0", "FAKE_INSTALL_FAIL=1")
		if rc != 0 || !strings.Contains(out, "RAN") {
			t.Errorf("a failed upgrade must still launch the version that IS installed "+
				"(rc=%d):\n%s", rc, out)
		}

		// A COLD home whose install failed has nothing to exec, and that is the one case
		// the launch path fails on — with the message that names the tool.
		q := newNpmProbe(t, "tool")
		_, out, rc = q.runStatus(t, "tool", "tool", "FAKE_INSTALL_FAIL=1")
		if rc == 0 {
			t.Errorf("a cold home whose install failed has no CLI, and the launcher must "+
				"not report success:\n%s", out)
		}
		if !strings.Contains(out, "not available") {
			t.Errorf("and it must say which tool is missing:\n%s", out)
		}
	})
}

// TestNpmLauncherUpdateModeDoesNotClaimCurrentWhenTheRegistryIsSilent: "I could not ask"
// and "the answer was the same" are different facts, and only one of them is good news.
//
// `_update` resolved LATEST with `npm view … || echo "$INSTALLED"`, borrowed verbatim from
// the informational poll. In the poll that substitution is right — a check nobody asked for
// that cannot reach the registry has nothing to say and must not delay a launch. In an
// update it makes the failure indistinguishable from success by construction: the two
// versions compare equal, so an offline jail was told "<version> is already current" and
// given exit 0. That is worse than the silent reinstall this mechanism replaced, because a
// user who has been told their CLI is current stops looking.
func TestNpmLauncherUpdateModeDoesNotClaimCurrentWhenTheRegistryIsSilent(t *testing.T) {
	p := newNpmProbe(t, "tool")
	p.run(t, "tool", "tool", "FAKE_INSTALLED_VERSION=1.0.0")
	p.truncateLog(t)

	log, out, rc := p.runStatus(t, "tool", "tool", "YOLO_PACK_UPDATE=1", "FAKE_VIEW_FAIL=1")
	if !hasArgv(log, "view tool version") {
		t.Fatalf("the update must have asked:\n%s", strings.Join(log, "\n"))
	}
	if strings.Contains(out, "already current") {
		t.Errorf("an unanswered registry must never be reported as an up-to-date one:\n%s", out)
	}
	if rc == 0 {
		t.Errorf("the user asked for an update and did not get one; that is not exit 0:\n%s", out)
	}
	// And it must not paper over the failure by reinstalling blind: a guess is not a
	// resolution, and this is the act the no-evergreen ruling reserves for real answers.
	if hasArgv(log, "install") {
		t.Errorf("a registry that did not answer is not a licence to reinstall:\n%s",
			strings.Join(log, "\n"))
	}

	// The POLL keeps the opposite behaviour, and the contrast is the point: nobody asked,
	// so an unreachable registry is silent, harmless and must not stop the launch.
	p.agePastInterval(t, "tool")
	p.truncateLog(t)
	log, out, rc = p.runStatus(t, "tool", "tool", "FAKE_VIEW_FAIL=1")
	if !hasArgv(log, "view tool version") {
		t.Errorf("the poll must still try:\n%s", strings.Join(log, "\n"))
	}
	if rc != 0 || !strings.Contains(out, "RAN") {
		t.Errorf("but a poll that could not reach the registry must never stop a launch "+
			"(rc=%d):\n%s", rc, out)
	}
	if strings.Contains(out, "is available") {
		t.Errorf("nor invent a report out of an answer it never got:\n%s", out)
	}
}

// TestNpmLauncherUpdateModeHonoursAPin: a declared selector IS the answer to "which
// version", so an explicit update must not ask the registry for one — asking would either
// override the declaration or waste the round-trip, which is the same reasoning npmspec.go
// gives for the pinned poll being skipped.
func TestNpmLauncherUpdateModeHonoursAPin(t *testing.T) {
	p := newNpmProbe(t, "tool")
	p.run(t, "tool", "tool@1.2.3")

	p.truncateLog(t)
	log, out := p.runOut(t, "tool", "tool@1.2.3", "YOLO_PACK_UPDATE=1", "FAKE_LATEST=9.9.9")
	if len(log) != 0 {
		t.Errorf("an explicit update on a PINNED package must touch npm not at all:\n%s",
			strings.Join(log, "\n"))
	}
	if !strings.Contains(out, "pinned") {
		t.Errorf("and it must say why it did nothing:\n%s", out)
	}

	// A pin the jail has not caught up with is still this act's job: update converges on
	// the declaration, offline. Without it an update would report success while the old
	// binary kept running.
	p.truncateLog(t)
	log = p.run(t, "tool", "tool@1.3.0", "YOLO_PACK_UPDATE=1", "FAKE_LATEST=9.9.9")
	if !hasArgv(log, "install -g --prefer-online tool@1.3.0") {
		t.Errorf("update must converge on a moved declaration:\n%s", strings.Join(log, "\n"))
	}
	if hasArgv(log, "view") {
		t.Errorf("and must do it offline — the declaration already names the version:\n%s",
			strings.Join(log, "\n"))
	}
}

// TestUnpinnedNpmLauncherDoesNotReinstallWhenCurrent is the half of "unchanged" the
// poll's own refactor put at risk: the comparison it exists to make.
//
// The hourly poll reads the INSTALLED version out of
// `$NPM_CONFIG_PREFIX/lib/node_modules/$PKG/package.json` and compares it with the
// registry's. Both halves moved when the branch was extracted into
// `_poll_and_report` / `_installed_version`, and if either stops matching — the
// lookup handed $SPEC instead of $PKG is the near miss, since the two are now
// different strings — the comparison never comes out equal and the launcher runs a
// full `npm install -g` on the first invocation after EVERY interval, forever. That
// is precisely the reinstall storm the pinned branch was written to avoid, arriving
// on the unpinned path where every shipped pack lives.
//
// A test that only ever asserts "an update did happen" cannot see it: an unconditional
// reinstall satisfies that assertion too. So this one pins the negative.
func TestUnpinnedNpmLauncherDoesNotReinstallWhenCurrent(t *testing.T) {
	p := newNpmProbe(t, "tool")
	p.run(t, "tool", "tool", "FAKE_INSTALLED_VERSION=1.0.0")

	p.agePastInterval(t, "tool")
	p.truncateLog(t)
	log := p.run(t, "tool", "tool", "FAKE_LATEST=1.0.0")

	if !hasArgv(log, "view tool version") {
		t.Fatalf("the update must still ask for an unpinned package:\n%s", strings.Join(log, "\n"))
	}
	if hasArgv(log, "install") {
		t.Errorf("the installed version already IS the registry's latest; reinstalling here "+
			"is an unconditional hourly `npm install -g` for every jail:\n%s", strings.Join(log, "\n"))
	}
}

// TestPinnedNpmLauncherNeverPollsTheRegistry: the poll asks for the registry's `latest`,
// which is not an answer to any question a pinned package has. Left running, it also never
// compares equal for a tag or a range — so it would reinstall hourly, forever.
func TestPinnedNpmLauncherNeverPollsTheRegistry(t *testing.T) {
	for _, pkg := range []string{"tool@1.2.3", "tool@next", "tool@^1.0.0"} {
		p := newNpmProbe(t, "tool")
		p.run(t, "tool", pkg, "FAKE_INSTALLED_VERSION=1.2.3")

		p.agePastInterval(t, "tool")
		p.truncateLog(t)
		log := p.run(t, "tool", pkg, "FAKE_LATEST=9.9.9", "FAKE_INSTALLED_VERSION=1.2.3")

		if len(log) != 0 {
			t.Errorf("%s: a pinned package must touch npm exactly once — got:\n%s",
				pkg, strings.Join(log, "\n"))
		}
	}
}

// TestPinnedNpmLauncherFollowsTheDeclaration is what makes a pin usable rather than
// merely expressible: with a binary already installed, the pinned branch skips the
// registry — so if it did not also notice the DECLARATION moving, changing a pack from
// 1.2.3 to 1.3.0 would silently keep running 1.2.3 for the life of the jail home.
func TestPinnedNpmLauncherFollowsTheDeclaration(t *testing.T) {
	p := newNpmProbe(t, "tool")
	p.run(t, "tool", "tool@1.2.3")
	p.truncateLog(t)

	// Same jail home, new declaration, stamp still fresh.
	log := p.run(t, "tool", "tool@1.3.0")
	if !hasArgv(log, "install -g --prefer-online tool@1.3.0") {
		t.Errorf("a moved declaration must be installed:\n%s", strings.Join(log, "\n"))
	}
	if hasArgv(log, "view") {
		t.Errorf("and it must be noticed offline, without a registry poll:\n%s",
			strings.Join(log, "\n"))
	}

	// Re-running with the SAME declaration must then be inert.
	p.truncateLog(t)
	if log := p.run(t, "tool", "tool@1.3.0"); len(log) != 0 {
		t.Errorf("an unchanged pin must not reinstall:\n%s", strings.Join(log, "\n"))
	}
}

// TestPinnedNpmLauncherRetriesAFailedUpgrade is the hole the recorded-spec mechanism opens
// if the spec is recorded unconditionally, and it is worse than the bug the mechanism fixes.
//
// An UPGRADE is not the cold case: REAL_BIN is already there holding the PREVIOUS version,
// so a failed `npm install` does not fall into the "not installed, retry unconditionally"
// branch. Record the new spec anyway and the pinned branch goes quiet too — both exits are
// shut, and one offline minute pins the jail to the old binary for the life of the home,
// with no hourly retry, no retry at boot and no message. The unpinned path cannot wedge
// this way: its next poll still sees the two versions differ and tries again. This test is
// the asymmetry, closed.
func TestPinnedNpmLauncherRetriesAFailedUpgrade(t *testing.T) {
	p := newNpmProbe(t, "tool")
	p.run(t, "tool", "tool@1.2.3") // installs, records tool@1.2.3
	p.truncateLog(t)

	// The declaration moves while the registry is unreachable. The attempt is made and
	// fails; the binary on disk is still 1.2.3.
	log := p.run(t, "tool", "tool@1.3.0", "FAKE_INSTALL_FAIL=1")
	if !hasArgv(log, "install -g --prefer-online tool@1.3.0") {
		t.Fatalf("the moved declaration must still be attempted:\n%s", strings.Join(log, "\n"))
	}
	p.truncateLog(t)

	// Registry back. Nothing about the DECLARATION changed, but 1.3.0 was never installed,
	// so the launcher must try again rather than treat the failure as done.
	log = p.run(t, "tool", "tool@1.3.0")
	if !hasArgv(log, "install -g --prefer-online tool@1.3.0") {
		t.Errorf("a failed pinned upgrade must be retried, not recorded as installed:\n%s",
			strings.Join(log, "\n"))
	}
	// ...and it must still be noticed offline — a retry is not a licence to poll.
	if hasArgv(log, "view") {
		t.Errorf("the retry must not reach for the registry's latest:\n%s", strings.Join(log, "\n"))
	}

	// Once it succeeds the launcher goes quiet again, so the retry is bounded by the
	// failure and not a permanent reinstall loop.
	p.truncateLog(t)
	if log := p.run(t, "tool", "tool@1.3.0"); len(log) != 0 {
		t.Errorf("a pin that finally installed must stop reinstalling:\n%s", strings.Join(log, "\n"))
	}
}
