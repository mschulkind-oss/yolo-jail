package entrypoint

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// launchersplice_test.go guards the SPLICE CONTRACT written on npmLauncherTemplate: every
// __YOLO_*__ sentinel in the three launcher templates is a shquote'd shell literal landing
// in a BARE position (an assignment's right-hand side, or a whole argv word).
//
// It exists because 11 of the 18 value splices were RAW until 2026-09-03 — the installer URL
// straight onto curl's argv, the package name and install spec inside double quotes, the
// stamp dir in two of the three templates — while the 7 beside them (both receipt fields in
// all three templates, plus the package-manager stamp dir) were correctly quoted. The values
// come from a pack manifest a human approved, so that was hardening rather than a live
// exploit; it is also exactly the shape that stops being hardening the day a value stops
// being approved. There are 19 splices as of 2026-09-04: slice 4 of install-capture added
// __YOLO_CAPTURES_DIR__ to the native template.
//
// THE TESTS BELOW ARE WRITTEN TO FAIL IF A shquote CALL IS DELETED, not merely to describe
// the current output (AGENTS.md, "a test that pins the CALLEE while the CALL SITE is
// unpinned is not a test"). Each hostile value carries two BOOBY TRAPS — a `$(…)` command
// substitution and a `;`-separated command, each of which creates a witness file — so a
// regenerated raw splice does not merely mangle a string, it RUNS those two commands, and
// the witness assertions catch it even in the cases where bash would still parse the script.
//
// MEASURED, by deleting each of the 18 calls in turn: 17 go red. The one survivor is
// __YOLO_PINNED__, where Quote is the identity on the only two inputs that reach it ("0" and
// "1", derived from a bool in the generator) — see npmAgentLauncher's docstring. Re-run that
// check if you add a sentinel. Re-run for the 19th (__YOLO_CAPTURES_DIR__, 2026-09-04): it goes
// RED, so the tally is 18 of 19.

// Witness basenames. Relative, not absolute, because one of the values fed through this
// harness is a BIN NAME, and ValidBinName refuses a "/" — the payload therefore has to be
// spellable without a path, and every launcher below is run with its cwd set to the temp
// home so a fired trap lands somewhere the test can see it.
const (
	witnessCmdSub = "PWNED_BY_COMMAND_SUBSTITUTION"
	witnessSemi   = "PWNED_BY_SEMICOLON"
)

// hostileValue returns a value carrying every metacharacter class that can escape a shell
// context — a space, a single quote, a double quote, a `$`, a backtick and a `;` — plus the
// two witness-creating traps. tag keeps two sites in one test from sharing a witness, so a
// failure names WHICH splice fired.
//
// It is deliberately not a valid npm package name or URL. The question these tests ask is
// never "does the tool accept this?" but "does the SHELL treat it as data?", and a value the
// shell mangles is the only one that can answer it.
func hostileValue(tag string) string {
	return "a b$(touch " + witnessCmdSub + tag + ");touch " + witnessSemi + tag +
		";'sq\"dq`echo no`$HOME"
}

// assertNoWitness fails when either trap in hostileValue fired, i.e. when the value reached
// the shell as CODE. dir is the cwd the launcher ran in.
func assertNoWitness(t *testing.T, dir, tag, what string) {
	t.Helper()
	for _, w := range []string{witnessCmdSub + tag, witnessSemi + tag} {
		if _, err := os.Stat(filepath.Join(dir, w)); err == nil {
			t.Errorf("%s: the spliced value EXECUTED — %s exists, so the value was shell "+
				"source rather than data", what, w)
		}
	}
}

// hostileReceiptsPath is a receipts destination whose WORKSPACE component carries the
// payload. It is one path component (hostileValue spells no "/"), and its parents do not
// exist — the receipt writer mkdir -p's its own parent, so the file's arrival at this exact
// path is the round-trip evidence for __YOLO_RECEIPTS_FILE__.
func hostileReceiptsPath(home, tag string) string {
	return filepath.Join(home, "ws-"+hostileValue(tag), ".yolo", "receipts.jsonl")
}

// assertReceiptLanded is the behavioural half of the __YOLO_RECEIPTS_FILE__ guard, and it is
// needed because assertParses alone does NOT catch a raw splice there: the payload's lone
// double quote finds a closing partner further down the template often enough that `bash -n`
// still accepts the file. Measured — the npm template survived exactly that mutation while
// the other two died on it. _yolo_receipt swallows every error by design (`|| true`, output
// to /dev/null), so an absent file is the only signal a broken path leaves.
func assertReceiptLanded(t *testing.T, path, what string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Errorf("%s: no receipt at the path the generator baked in (%q): %v", what, path, err)
	}
}

// assertParses runs `bash -n` over a generated launcher. A raw splice of a value carrying an
// odd number of quotes usually cannot even be parsed, so this is the cheap half of the guard
// and the only one available for a code path a unit test cannot reach. It is NOT sufficient
// on its own — see assertReceiptLanded for the case it lets through.
//
// It also refuses a LEFTOVER sentinel. That is its own defect class rather than pedantry: the
// `__YOLO_STAMP_DIR_LIT__` → `__YOLO_STAMP_DIR__` rename made in the same commit as this file
// would otherwise have shipped a template with an unspliced placeholder, which bash reads as
// a bare word and `set -u` never sees.
func assertParses(t *testing.T, body, what string) {
	t.Helper()
	if strings.Contains(body, "__YOLO_") {
		t.Errorf("%s: an unspliced __YOLO_*__ sentinel survived into the script:\n%s", what, body)
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not found")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "script")
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("bash", "-n", path).CombinedOutput()
	if err != nil {
		t.Errorf("%s: generated script is not valid bash: %v\n%s\n--- script ---\n%s",
			what, err, out, body)
	}
}

// TestLauncherTemplatesParseWithHostileValues feeds the hostile value through EVERY
// generator input at once and asserts the emitted script is still bash. Cheap, total
// coverage of the sentinel set; the behavioural tests below are what prove the value also
// arrives intact.
func TestLauncherTemplatesParseWithHostileValues(t *testing.T) {
	v := hostileValue("-parse")
	assertParses(t, npmAgentLauncher(
		&packdecl.Install{Kind: "npm", Bin: v, Package: v, Flags: []string{v, "--plain"}},
		v, v), "npm launcher")
	assertParses(t, nativeAgentLauncher(
		&packdecl.Install{Kind: "native", Bin: v, InstallerURL: v},
		v, v, v), "native launcher")

	// The package-manager launcher takes no per-pack input at all: its bin and package are
	// a hardcoded list, so the only value that can be hostile is the stamp dir, which is
	// derived from $HOME.
	home := filepath.Join(t.TempDir(), v)
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	e := NewEnv(map[string]string{"JAIL_HOME": home, "YOLO_WORKSPACE": home})
	if err := GeneratePackageManagerLaunchers(e); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(e.LaunchDir(), "pnpm"))
	if err != nil {
		t.Fatal(err)
	}
	assertParses(t, string(body), "package-manager launcher")
}

// argvLogger writes a fake tool that appends ONE LINE PER ARGUMENT to logPath, then runs
// extra. One line per argument is the whole point: the existing npmProbe logs "$*", which
// joins argv with spaces and therefore cannot tell one hostile word from three ordinary
// ones — the exact distinction a quoting test has to make.
func argvLogger(t *testing.T, dir, name, logPath, extra string) {
	t.Helper()
	body := "#!/bin/bash\n" +
		"for a in \"$@\"; do printf '%s\\n' \"$a\" >> " + shellSingleQuote(logPath) + "; done\n" +
		extra + "\nexit 0\n"
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

// shellSingleQuote is a two-line stand-in for shquote.Quote, used only to build the FAKES.
// The production quoting is what these tests measure, so the harness must not depend on it —
// a bug in shquote.Quote would otherwise break the fake and the assertion together, and the
// test would go green by cancelling itself out.
func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}

// logLines returns the fake tool's per-argument log.
func logLines(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	trimmed := strings.TrimRight(string(data), "\n")
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "\n")
}

func hasExactArg(log []string, want string) bool {
	for _, got := range log {
		if got == want {
			return true
		}
	}
	return false
}

// runLauncher writes body to <home>/<name>, runs it with cwd=home under a controlled PATH,
// and returns its combined output plus exit code. cwd is home so a fired witness lands where
// assertNoWitness looks for it.
func runLauncher(t *testing.T, home, name, body, fakeBin string) (string, int) {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not found")
	}
	script := filepath.Join(home, name)
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(script)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "PATH=" + fakeBin + ":" + os.Getenv("PATH")}
	out, err := cmd.CombinedOutput()
	rc := 0
	if ee, ok := err.(*exec.ExitError); ok {
		rc = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("launcher could not be run at all: %v\n%s", err, out)
	}
	return string(out), rc
}

// TestNativeLauncherQuotesTheInstallerURL is the headline defect, measured: the URL used to
// be `curl -fsSL __YOLO_URL__ -o "$script"`, raw on an argv.
//
// The assertion is that curl receives the URL as ONE argument byte-identical to the
// declaration. A word-count assertion would not be enough — three words is also what a
// correctly quoted URL containing two spaces would produce if it were quoted at the splice
// and then left unquoted at the point of use.
func TestNativeLauncherQuotesTheInstallerURL(t *testing.T) {
	home := t.TempDir()
	fakeBin := filepath.Join(home, "fakebin")
	if err := os.MkdirAll(fakeBin, 0o755); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(home, "curl.log")
	// The fake curl honours -o by landing a real installer, so the launcher proceeds down
	// its success path and the test also covers the bytes AFTER the download.
	argvLogger(t, fakeBin, "curl", logPath,
		`out=""
prev=""
for a in "$@"; do
    if [ "$prev" = "-o" ]; then out="$a"; fi
    prev="$a"
done
if [ -n "$out" ]; then
    printf '#!/bin/bash\nmkdir -p "$HOME/.local/bin"\nprintf "#!/bin/sh\\necho LAUNCHED\\n" > "$HOME/.local/bin/probetool"\nchmod +x "$HOME/.local/bin/probetool"\n' > "$out"
fi`)

	url := "https://example.invalid/install.sh?" + hostileValue("-url")
	receipts := hostileReceiptsPath(home, "-nativereceipts")
	body := nativeAgentLauncher(
		&packdecl.Install{Kind: "native", Bin: "probetool", InstallerURL: url},
		filepath.Join(home, "stamps"), receipts, "")

	out, rc := runLauncher(t, home, "probetool", body, fakeBin)
	if rc != 0 {
		t.Errorf("launcher failed (rc=%d) — a hostile URL must be data, not code:\n%s", rc, out)
	}
	assertNoWitness(t, home, "-url", "native installer URL")
	assertNoWitness(t, home, "-nativereceipts", "native receipts path")
	assertReceiptLanded(t, receipts, "native launcher")
	log := logLines(t, logPath)
	if !hasExactArg(log, url) {
		t.Errorf("curl did not receive the URL as one intact argument.\nwant: %q\ngot argv:\n%s",
			url, strings.Join(log, "\n"))
	}
	if !strings.Contains(out, "LAUNCHED") {
		t.Errorf("launcher did not exec the installed binary:\n%s", out)
	}
}

// TestNpmLauncherQuotesTheInstallSpecAndFlags covers the two npm-side sentinels that reach a
// tool's argv: __YOLO_SPEC__ (raw inside double quotes until 2026-09-03) and __YOLO_EXTRA__,
// which is a LIST and therefore takes shquote.Join rather than Quote.
//
// The flags assertion is the one that pins Join specifically: `strings.Join` on
// []string{"--flag with space"} yields three argv words, and only per-argument logging can
// tell that from one.
func TestNpmLauncherQuotesTheInstallSpecAndFlags(t *testing.T) {
	home := t.TempDir()
	fakeBin := filepath.Join(home, "fakebin")
	if err := os.MkdirAll(fakeBin, 0o755); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(home, "npm.log")
	// `npm install` materializes the binary the launcher execs; `npm view` never runs on
	// this path (the declaration is pinned) but answers anyway so a regression that starts
	// polling shows up as an argv line rather than a hang.
	argvLogger(t, fakeBin, "npm", logPath,
		`if [ "${1:-}" = install ]; then
    mkdir -p "$NPM_CONFIG_PREFIX/bin"
    printf '#!/bin/sh\necho LAUNCHED\n' > "$NPM_CONFIG_PREFIX/bin/tool"
    chmod +x "$NPM_CONFIG_PREFIX/bin/tool"
fi
if [ "${1:-}" = view ]; then echo 9.9.9; fi`)

	// A pinned selector carrying the payload: splitNpmSpec returns it verbatim, so SPEC is
	// `tool@<hostile>` while PKG stays the bare name.
	spec := "tool@1.0.0-" + hostileValue("-spec")
	flag := "--flag=" + hostileValue("-flag")
	receipts := hostileReceiptsPath(home, "-npmreceipts")
	body := npmAgentLauncher(
		&packdecl.Install{Kind: "npm", Bin: "tool", Package: spec, Flags: []string{flag, "--plain"}},
		filepath.Join(home, "stamps"), receipts)

	out, rc := runLauncher(t, home, "tool", body, fakeBin)
	if rc != 0 {
		t.Errorf("launcher failed (rc=%d) — a hostile spec must be data, not code:\n%s", rc, out)
	}
	assertNoWitness(t, home, "-spec", "npm install spec")
	assertNoWitness(t, home, "-flag", "npm install flags")
	assertNoWitness(t, home, "-npmreceipts", "npm receipts path")
	assertReceiptLanded(t, receipts, "npm launcher")

	log := logLines(t, logPath)
	if !hasExactArg(log, spec) {
		t.Errorf("npm did not receive the install spec as one intact argument.\nwant: %q\ngot argv:\n%s",
			spec, strings.Join(log, "\n"))
	}
	// Both flags, and as separate words: Join must quote each element without welding the
	// list into one argument.
	for _, want := range []string{flag, "--plain"} {
		if !hasExactArg(log, want) {
			t.Errorf("npm did not receive flag %q as one intact argument.\ngot argv:\n%s",
				want, strings.Join(log, "\n"))
		}
	}
	if !strings.Contains(out, "LAUNCHED") {
		t.Errorf("launcher did not exec the installed binary:\n%s", out)
	}
}

// TestNpmLauncherQuotesThePackageName covers __YOLO_PKG__, which the spec test above cannot
// reach: splitNpmSpec puts everything after the first non-zero-index `@` in the SPEC, so a
// payload carried in the version selector leaves the NAME half clean. A declaration with no
// selector at all is what makes PKG hostile — and PKG has exactly one place it reaches a
// tool, `npm view "$PKG" version`, so the test drives the hourly poll to get there
// (installed binary present, no selector, no stamp).
func TestNpmLauncherQuotesThePackageName(t *testing.T) {
	home := t.TempDir()
	fakeBin := filepath.Join(home, "fakebin")
	if err := os.MkdirAll(fakeBin, 0o755); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(home, "npm.log")
	argvLogger(t, fakeBin, "npm", logPath, `if [ "${1:-}" = view ]; then echo 9.9.9; fi`)
	// jq is what _installed_version asks for the local version; a stub keeps the answer
	// (and therefore the "an update is available" line) independent of the host's jq.
	if err := os.WriteFile(filepath.Join(fakeBin, "jq"),
		[]byte("#!/bin/sh\necho 1.0.0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	// No `@` anywhere, so splitNpmSpec returns the whole string as the NAME and PINNED=0 —
	// the only shape that both makes PKG hostile and leaves the poll enabled.
	pkg := "pkg-" + hostileValue("-pkgname")
	seedInstalled(t, filepath.Join(home, ".npm-global", "bin"), "tool")

	body := npmAgentLauncher(
		&packdecl.Install{Kind: "npm", Bin: "tool", Package: pkg},
		filepath.Join(home, "stamps"),
		filepath.Join(home, "ws", ".yolo", "receipts.jsonl"))
	out, rc := runLauncher(t, home, "tool", body, fakeBin)
	if rc != 0 {
		t.Errorf("launcher failed (rc=%d) — a hostile package name must be data:\n%s", rc, out)
	}
	assertNoWitness(t, home, "-pkgname", "npm package name")

	log := logLines(t, logPath)
	if !hasExactArg(log, "view") {
		t.Fatalf("the registry poll did not run, so PKG never reached a tool — the test "+
			"measures nothing.\nargv:\n%s\noutput:\n%s", strings.Join(log, "\n"), out)
	}
	if !hasExactArg(log, pkg) {
		t.Errorf("npm view did not receive the package name as one intact argument.\n"+
			"want: %q\ngot argv:\n%s", pkg, strings.Join(log, "\n"))
	}
}

// TestPkgManagerLauncherQuotesItsBinAndSpec drives the pkgManagerLauncher seam directly.
//
// It has to: GeneratePackageManagerLaunchers' bin and package come from a hardcoded
// {"pnpm","pnpm"} list, and shquote.Quote("pnpm") is byte-identical to no quoting, so a test
// that went through the generator could not distinguish the two spellings no matter what it
// asserted. The generator's own call of this function is pinned by the pnpm assertions in
// TestNpmLauncherBodyCarriesNameAndSpecSeparately and by the package-manager subtest below.
func TestPkgManagerLauncherQuotesItsBinAndSpec(t *testing.T) {
	home := t.TempDir()
	fakeBin := filepath.Join(home, "fakebin")
	if err := os.MkdirAll(fakeBin, 0o755); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(home, "npm.log")
	bin := hostileValue("-pmbin")
	argvLogger(t, fakeBin, "npm", logPath,
		`if [ "${1:-}" = install ]; then
    mkdir -p "$NPM_CONFIG_PREFIX/bin"
    printf '#!/bin/sh\necho LAUNCHED\n' > "$NPM_CONFIG_PREFIX/bin/"`+shellSingleQuote(bin)+`
    chmod +x "$NPM_CONFIG_PREFIX/bin/"`+shellSingleQuote(bin)+`
fi`)

	pkg := "mgr@1.0.0-" + hostileValue("-pmspec")
	receipts := hostileReceiptsPath(home, "-pmreceipts")
	body := pkgManagerLauncher(bin, pkg, filepath.Join(home, "stamps"), receipts)
	assertParses(t, body, "package-manager launcher")

	out, rc := runLauncher(t, home, "pm-run", body, fakeBin)
	if rc != 0 {
		t.Errorf("launcher failed (rc=%d) — a hostile bin/spec must be data:\n%s", rc, out)
	}
	assertNoWitness(t, home, "-pmbin", "package-manager BIN")
	assertNoWitness(t, home, "-pmspec", "package-manager SPEC")
	assertNoWitness(t, home, "-pmreceipts", "package-manager receipts path")
	assertReceiptLanded(t, receipts, "package-manager launcher")

	log := logLines(t, logPath)
	if !hasExactArg(log, pkg) {
		t.Errorf("npm did not receive the install spec as one intact argument.\nwant: %q\n"+
			"got argv:\n%s", pkg, strings.Join(log, "\n"))
	}
	// BIN never reaches a tool's argv here; the round trip is the exec, exactly as in
	// TestLaunchersQuoteTheBinNameAndStampDir.
	if !strings.Contains(out, "LAUNCHED") {
		t.Errorf("launcher did not exec $NPM_CONFIG_PREFIX/bin/<bin> — BIN did not survive "+
			"the shell:\n%s", out)
	}
}

// TestLaunchersQuoteTheBinNameAndStampDir covers the two sentinels that never reach a tool's
// argv and so cannot be measured by a fake: BIN and STAMP_DIR.
//
// The measurement is a ROUND TRIP through the shell. A binary is pre-created at exactly the
// path the template composes from BIN, and a fresh stamp is pre-created at exactly the path
// it composes from STAMP_DIR — so the launcher takes its "already installed, nothing to
// poll" branch and execs the binary only if BOTH values survived byte-identically. Any
// mangling turns the exec into the "not available" arm; any raw splice fires a witness.
//
// ValidBinName is the reason this is not merely theoretical: it refuses only "", ".", ".."
// and names containing "/" or ":", so a space, a quote, a `$` and a `;` are all accepted bin
// names today, and the launcher's own filename in ~/.yolo/bin/launch/ is that same string.
func TestLaunchersQuoteTheBinNameAndStampDir(t *testing.T) {
	bin := hostileValue("-bin")
	if !packdecl.ValidBinName(bin) {
		t.Fatalf("the payload must be a bin name the schema ACCEPTS, or this test proves "+
			"nothing about a reachable state: %q", bin)
	}

	t.Run("npm", func(t *testing.T) {
		home := t.TempDir()
		stamps := filepath.Join(home, hostileValue("-stampdir"))
		npmBin := filepath.Join(home, ".npm-global", "bin")
		seedInstalled(t, npmBin, bin)
		seedFreshStamp(t, stamps, bin)

		body := npmAgentLauncher(
			&packdecl.Install{Kind: "npm", Bin: bin, Package: "pkg@1.0.0"},
			stamps, filepath.Join(home, "ws", ".yolo", "receipts.jsonl"))
		out, rc := runLauncher(t, home, "launcher", body, filepath.Join(home, "nonexistent-bin"))
		if rc != 0 || !strings.Contains(out, "LAUNCHED") {
			t.Errorf("npm launcher did not exec $NPM_CONFIG_PREFIX/bin/<bin> for a hostile "+
				"bin name (rc=%d) — BIN or STAMP_DIR did not survive the shell:\n%s", rc, out)
		}
		assertNoWitness(t, home, "-bin", "npm launcher BIN")
		assertNoWitness(t, home, "-stampdir", "npm launcher STAMP_DIR")
	})

	t.Run("native", func(t *testing.T) {
		home := t.TempDir()
		stamps := filepath.Join(home, hostileValue("-stampdir"))
		seedInstalled(t, filepath.Join(home, ".local", "bin"), bin)
		seedFreshStamp(t, stamps, bin)

		body := nativeAgentLauncher(
			&packdecl.Install{Kind: "native", Bin: bin, InstallerURL: "https://example.invalid/i.sh"},
			stamps, filepath.Join(home, "ws", ".yolo", "receipts.jsonl"), "")
		out, rc := runLauncher(t, home, "launcher", body, filepath.Join(home, "nonexistent-bin"))
		if rc != 0 || !strings.Contains(out, "LAUNCHED") {
			t.Errorf("native launcher did not exec $HOME/.local/bin/<bin> for a hostile bin "+
				"name (rc=%d) — BIN or STAMP_DIR did not survive the shell:\n%s", rc, out)
		}
		assertNoWitness(t, home, "-bin", "native launcher BIN")
		assertNoWitness(t, home, "-stampdir", "native launcher STAMP_DIR")
	})

	// The package-manager launcher's bin is hardcoded, so its stamp dir is the whole
	// exposure — and it is $HOME-derived, which is the case its docstring already claimed
	// to cover. Here the claim is executed: HOME itself carries the payload.
	t.Run("package-manager", func(t *testing.T) {
		home := filepath.Join(t.TempDir(), hostileValue("-pmhome"))
		if err := os.MkdirAll(home, 0o755); err != nil {
			t.Fatal(err)
		}
		seedInstalled(t, filepath.Join(home, ".npm-global", "bin"), "pnpm")

		e := NewEnv(map[string]string{"JAIL_HOME": home, "YOLO_WORKSPACE": home})
		if err := GeneratePackageManagerLaunchers(e); err != nil {
			t.Fatal(err)
		}
		body, err := os.ReadFile(filepath.Join(e.LaunchDir(), "pnpm"))
		if err != nil {
			t.Fatal(err)
		}
		out, rc := runLauncher(t, home, "pnpm-run", string(body),
			filepath.Join(home, "nonexistent-bin"))
		if rc != 0 || !strings.Contains(out, "LAUNCHED") {
			t.Errorf("pnpm launcher did not exec its real binary from a hostile $HOME "+
				"(rc=%d):\n%s", rc, out)
		}
		assertNoWitness(t, home, "-pmhome", "package-manager STAMP_DIR")
		// mkdir -p "$STAMP_DIR" runs unconditionally, so the dir's existence at the exact
		// composed path is the direct evidence that the value round-tripped.
		want := filepath.Join(home, ".cache", "yolo-package-manager-stamps")
		if st, err := os.Stat(want); err != nil || !st.IsDir() {
			t.Errorf("STAMP_DIR was not created at the path the generator named (%q): %v",
				want, err)
		}
	})
}

// seedInstalled pre-creates the "already installed" binary the launcher should exec.
func seedInstalled(t *testing.T, dir, bin string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, bin), []byte("#!/bin/sh\necho LAUNCHED\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

// seedFreshStamp pre-creates a stamp young enough to suppress the update poll, so the run
// needs no network fake and the only thing under test is whether the paths composed.
func seedFreshStamp(t *testing.T, stampDir, bin string) {
	t.Helper()
	if err := os.MkdirAll(stampDir, 0o755); err != nil {
		t.Fatal(err)
	}
	stamp := filepath.Join(stampDir, bin+".stamp")
	if err := os.WriteFile(stamp, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := os.Chtimes(stamp, now, now); err != nil {
		t.Fatal(err)
	}
}
