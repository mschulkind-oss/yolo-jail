package entrypoint

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// receipts_test.go covers program-delivery.md §10's first step: every install yolo itself
// runs leaves ONE line saying what it got.
//
// The tests RUN the generated scripts rather than asserting on their text, and that is the
// only shape that can measure this. A text assertion cannot tell a receipt that is written
// from a receipt statement that is present: the whole hazard here is a hook that is spelled
// correctly and never reached (the native launcher's failure paths all return 0), or one
// that is reached and silently kills its caller (the npm launcher runs under `set -e` and
// its _do_install status is the only signal `yolo pack update` gets).

// readReceipts parses a receipts.jsonl, or returns nil when the file was never written.
//
// It also enforces the two properties of the FILE rather than of any one line: every line is
// parseable JSON on its own, and every line stays inside a fixed budget. The budget is a
// READABILITY bound — one install, one line a human can take in, `path` and `declared` both
// scrubbed to 200 chars — not an atomicity one. (It was documented as PIPE_BUF, which bounds
// atomic writes to a PIPE and says nothing about a regular file; what makes the append
// atomic is that it is one write(2) under O_APPEND. See _yolo_receipt.)
func readReceipts(t *testing.T, path string) []map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		if line == "" {
			continue
		}
		if len(line) > 2048 {
			t.Errorf("a receipt line is %d bytes; one install must stay one readable line, "+
				"which is what bounds every field the shell interpolates:\n%s", len(line), line)
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("receipt line is not JSON (%v):\n%s", err, line)
		}
		out = append(out, m)
	}
	return out
}

// str reads a string field, failing when it is absent or of the wrong type.
func str(t *testing.T, r map[string]any, key string) string {
	t.Helper()
	v, ok := r[key]
	if !ok {
		t.Fatalf("receipt has no %q field: %v", key, r)
	}
	s, ok := v.(string)
	if !ok {
		t.Fatalf("receipt field %q is %T, want string: %v", key, v, r)
	}
	return s
}

// requireOne fails unless exactly one receipt was written, which is the schema's unit: one
// line per install, never a line per attempt and never two for one.
func requireOne(t *testing.T, got []map[string]any) map[string]any {
	t.Helper()
	if len(got) != 1 {
		t.Fatalf("want exactly 1 receipt, got %d: %v", len(got), got)
	}
	return got[0]
}

// --- npm ---------------------------------------------------------------------------

// TestNpmInstallLeavesAReceipt is the schema, measured end to end on the cold path.
func TestNpmInstallLeavesAReceipt(t *testing.T) {
	p := newNpmProbe(t, "tool")
	p.run(t, "tool", "@scope/tool@2.0.0", "FAKE_INSTALLED_VERSION=2.0.0")

	r := requireOne(t, p.receipts(t))
	if got := r["schema"]; got != float64(1) {
		t.Errorf("schema = %v, want 1", got)
	}
	for _, want := range []struct{ key, val string }{
		{"kind", "npm"},
		{"bin", "tool"},
		// The DECLARATION verbatim, not the derived install spec: the two differ the
		// moment a pack names a version, and telling "the declaration moved" from "the
		// registry moved" is the one question a pinned package still has.
		{"declared", "@scope/tool@2.0.0"},
		{"spec", "@scope/tool@2.0.0"},
		{"resolved", "2.0.0"},
		{"act", "install"},
	} {
		if got := str(t, r, want.key); got != want.val {
			t.Errorf("%s = %q, want %q", want.key, got, want.val)
		}
	}
	// An npm package has a resolved version, so it needs no digest — and carrying one
	// would mean hashing a directory tree at every install.
	for _, absent := range []string{"sha256", "bytes"} {
		if _, ok := r[absent]; ok {
			t.Errorf("an npm receipt must not carry %q: %v", absent, r)
		}
	}
	// The LANDING PATH — §6's tuple is (declaration, resolver, resolved identity, landing
	// path, scope, time), and this is the member the schema used to drop. It is not
	// derivable from the rest of the line: the npm prefix is a per-jail path, and on
	// macos-user it is a machine-wide one shared by every workspace.
	if want := filepath.Join(p.home, ".npm-global", "bin", "tool"); str(t, r, "path") != want {
		t.Errorf("path = %q, want the binary this install landed at %q", r["path"], want)
	}
	if ts := str(t, r, "time"); !strings.HasSuffix(ts, "Z") || len(ts) != 20 {
		t.Errorf("time = %q, want a 20-char UTC ISO stamp ending in Z", ts)
	}
}

// TestNpmReceiptOmitsAResolvedVersionItCouldNotRead is the forgery, closed.
//
// The receipt used to interpolate `_installed_version`, whose `|| echo "0"` is a POLL
// sentinel — chosen so an unreadable package compares unequal to any registry answer. With
// no jq on PATH every install therefore recorded `"resolved":"0"`, a version nobody
// measured, in the one field a reconcile is going to compare against the disk. Omitting it
// says "unknown", which is true; and the rest of the line is still worth having.
func TestNpmReceiptOmitsAResolvedVersionItCouldNotRead(t *testing.T) {
	p := newNpmProbe(t, "tool")
	p.hideJQ(t)

	_, out := p.runOut(t, "tool", "@scope/tool@2.0.0")
	if !strings.Contains(out, "RAN") {
		t.Fatalf("the install must still happen and still exec the tool:\n%s", out)
	}

	r := requireOne(t, p.receipts(t))
	if v, present := r["resolved"]; present {
		t.Errorf("resolved = %v: with no jq the launcher cannot read a version, and a poll "+
			"sentinel is not a measurement — the field must be omitted: %v", v, r)
	}
	// The line is still a receipt: what it DID know is recorded.
	for _, want := range []struct{ key, val string }{
		{"kind", "npm"}, {"bin", "tool"}, {"declared", "@scope/tool@2.0.0"},
		{"spec", "@scope/tool@2.0.0"}, {"act", "install"},
	} {
		if got := str(t, r, want.key); got != want.val {
			t.Errorf("%s = %q, want %q", want.key, got, want.val)
		}
	}
}

// TestNpmReceiptRecordsTheRealVersionWhenItCanReadOne is the other half of the same
// property: omitting is for the case that cannot be measured, never a blanket retreat.
func TestNpmReceiptRecordsTheRealVersionWhenItCanReadOne(t *testing.T) {
	p := newNpmProbe(t, "tool")
	p.run(t, "tool", "tool@1.2.3", "FAKE_INSTALLED_VERSION=1.2.3")

	if v := str(t, requireOne(t, p.receipts(t)), "resolved"); v != "1.2.3" {
		t.Errorf("resolved = %q, want the version jq read out of the installed "+
			"package.json", v)
	}
}

// TestNpmReceiptActMatchesTheDispatchPredicate: the receipt and the dispatch have to agree
// about what update mode IS.
//
// The dispatch at the bottom of the launcher tests `= "1"`; the receipt tested `-n`. So
// YOLO_PACK_UPDATE=0 — the spelling a caller reaches for to mean "no" — took the ordinary
// launch path and then wrote itself down as an update, corrupting the one field that says
// whether a human asked for this.
func TestNpmReceiptActMatchesTheDispatchPredicate(t *testing.T) {
	p := newNpmProbe(t, "tool")
	_, out := p.runOut(t, "tool", "tool@1.0.0", "YOLO_PACK_UPDATE=0",
		"FAKE_INSTALLED_VERSION=1.0.0")

	// It exec'd the tool, so this was the launch path — not update mode, which exits.
	if !strings.Contains(out, "RAN") {
		t.Fatalf("YOLO_PACK_UPDATE=0 is not update mode; the launcher must launch:\n%s", out)
	}
	if act := str(t, requireOne(t, p.receipts(t)), "act"); act != "install" {
		t.Errorf("act = %q, want install: nobody asked for an update, so the receipt must "+
			"not claim one", act)
	}
}

// TestNpmReceiptFunnelCoversEveryPathThatChangesTheBytes is why the hook sits at the end of
// _do_install rather than at the three places that call it. Cold install, a moved pin and
// an explicit update reach npm down three different branches, and a receipt on one of them
// is a record with two silent holes in it.
func TestNpmReceiptFunnelCoversEveryPathThatChangesTheBytes(t *testing.T) {
	p := newNpmProbe(t, "tool")

	// 1. cold.
	p.run(t, "tool", "tool@1.2.3")
	// 2. the DECLARATION moved — no registry involved.
	p.run(t, "tool", "tool@1.3.0")
	// 3. an explicit update on an unpinned declaration.
	p.runOut(t, "tool", "tool", "YOLO_PACK_UPDATE=1", "FAKE_LATEST=9.9.9",
		"FAKE_INSTALLED_VERSION=9.9.9")

	got := p.receipts(t)
	if len(got) != 3 {
		t.Fatalf("want one receipt per install, got %d: %v", len(got), got)
	}
	for i, want := range []string{"tool@1.2.3", "tool@1.3.0", "tool@latest"} {
		if s := str(t, got[i], "spec"); s != want {
			t.Errorf("receipt %d: spec = %q, want %q", i, s, want)
		}
	}
	// The act is the difference between "this jail was provisioned" and "someone asked for
	// a new version", and it is the only field that says which.
	for i, want := range []string{"install", "install", "update"} {
		if s := str(t, got[i], "act"); s != want {
			t.Errorf("receipt %d: act = %q, want %q", i, s, want)
		}
	}
}

// TestNpmReceiptIsWrittenOnlyWhenNpmAgreed: the hook is inside the success arm, beside the
// spec file, so a failed install records nothing. A receipt for an install that did not
// happen is worse than no receipt at all — it is the exact claim the reconcile will later
// compare the disk against.
func TestNpmReceiptIsWrittenOnlyWhenNpmAgreed(t *testing.T) {
	p := newNpmProbe(t, "tool")
	_, out, rc := p.runStatus(t, "tool", "tool", "FAKE_INSTALL_FAIL=1")
	if rc == 0 {
		t.Fatalf("a cold home whose install failed must not report success:\n%s", out)
	}
	if got := p.receipts(t); len(got) != 0 {
		t.Errorf("a failed install must leave no receipt, got: %v", got)
	}
}

// TestNpmReceiptCannotChangeTheLauncherOutcome is constraint 3, measured.
//
// The templates run under `set -euo pipefail` and _do_install's return value is read at
// three call sites — it is the ONLY signal `yolo pack update` gets, and the split exists
// because a refresh that silently installed nothing used to report success. So a receipt
// that cannot be written must be indistinguishable, from every caller, from no receipt at
// all: not a failed launch, not a failed update, not a lost exec.
//
// The unwritable path is a receipts file whose PARENT is a regular file, so both the
// mkdir -p and the append fail. That is not contrived — a jail whose <ws>/.yolo the user
// made a file, or a read-only workspace mount, produces it.
func TestNpmReceiptCannotChangeTheLauncherOutcome(t *testing.T) {
	p := newNpmProbe(t, "tool")
	blocker := filepath.Join(p.home, "not-a-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	p.receiptsPath = filepath.Join(blocker, ".yolo", "receipts.jsonl")

	// Launch path: the install still happens and the tool is still exec'd.
	_, out, rc := p.runStatus(t, "tool", "tool", "FAKE_INSTALLED_VERSION=1.0.0")
	if rc != 0 || !strings.Contains(out, "RAN") {
		t.Fatalf("an unwritable receipts path must not stop a launch (rc=%d):\n%s", rc, out)
	}
	// Update path: the status _do_install returns must still be its own.
	p.truncateLog(t)
	log, out, rc := p.runStatus(t, "tool", "tool", "YOLO_PACK_UPDATE=1", "FAKE_LATEST=9.9.9",
		"FAKE_INSTALLED_VERSION=9.9.9")
	if !hasArgv(log, "install -g --prefer-online tool@latest") {
		t.Fatalf("the update must still have run:\n%s", strings.Join(log, "\n"))
	}
	if rc != 0 {
		t.Errorf("a successful update whose receipt could not be written must still exit 0 "+
			"(rc=%d):\n%s", rc, out)
	}
}

// TestReceiptCannotFailACallerUnderErrexit makes the cannot-fail-the-caller property
// STRUCTURAL rather than incidental, at the level the property is stated: _yolo_receipt
// itself, called under `set -euo pipefail` the way all three templates call it.
//
// The function used to end `return "$_yr_rc"`, with _yr_rc read from `$?` on its first line —
// which at function entry is NOT the caller's status but whatever the last command
// substitution in this call's own arguments left behind. Every call site passes at least one
// (`"$(_resolved_version)"`, `"$(_yolo_head …)"`), so the property held only because those
// helpers happen to end in `return 0`. A resolver added later that reports "I could not
// tell" with a non-zero status — the most natural way to spell it — would have killed its
// caller two frames from anything mentioning receipts.
func TestReceiptCannotFailACallerUnderErrexit(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not found")
	}
	home := t.TempDir()
	receipts := filepath.Join(home, "ws", ".yolo", "receipts.jsonl")

	script := filepath.Join(home, "probe.sh")
	body := `#!/bin/bash
set -euo pipefail
_YOLO_RECEIPTS=` + shquote.Quote(receipts) + `
` + receiptShellFns + `
# A resolver that cannot answer: it prints nothing and says so with a non-zero status.
# "jq is not installed", "this binary has no module info" and "the registry did not answer"
# are all spelled exactly like this.
_cannot_tell() { return 3; }

_yolo_receipt '{"schema":1,"kind":"probe"' "" "$(_cannot_tell)" "" install
echo CALLER_PROCEEDED
`
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(script).CombinedOutput()
	if err != nil {
		t.Fatalf("a receipt whose argument could not be resolved killed its caller "+
			"(%v):\n%s", err, out)
	}
	if !strings.Contains(string(out), "CALLER_PROCEEDED") {
		t.Errorf("the caller must run on past the receipt:\n%s", out)
	}
	// ...and the receipt is still written, minus the field nobody could measure.
	r := requireOne(t, readReceipts(t, receipts))
	if _, present := r["resolved"]; present {
		t.Errorf("an unresolvable version must be omitted, not invented: %v", r)
	}
	if act := str(t, r, "act"); act != "install" {
		t.Errorf("act = %q, want install", act)
	}
}

// --- package managers (pnpm) ---------------------------------------------------------

// pnpmLauncherRun generates the pnpm launcher through GeneratePackageManagerLaunchers — the
// call site, so the receipts path is the one the boot path would bake — runs it against a
// fake npm, and returns (combined output, exit code, receipts).
func pnpmLauncherRun(t *testing.T, env ...string) (string, int, []map[string]any) {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not found")
	}
	home := t.TempDir()
	ws := filepath.Join(home, "ws")
	fakeBin := filepath.Join(home, "fakebin")
	if err := os.MkdirAll(fakeBin, 0o755); err != nil {
		t.Fatal(err)
	}
	npm := `#!/bin/bash
case "${1:-}" in
install)
    if [ -n "${FAKE_INSTALL_FAIL:-}" ]; then echo "npm ERR! network unreachable" >&2; exit 1; fi
    mkdir -p "$NPM_CONFIG_PREFIX/bin"
    printf '#!/bin/sh\necho PNPM_RAN\n' > "$NPM_CONFIG_PREFIX/bin/pnpm"
    chmod +x "$NPM_CONFIG_PREFIX/bin/pnpm"
    ;;
esac
exit 0
`
	if err := os.WriteFile(filepath.Join(fakeBin, "npm"), []byte(npm), 0o755); err != nil {
		t.Fatal(err)
	}

	e := NewEnv(map[string]string{"JAIL_HOME": home, "YOLO_WORKSPACE": ws})
	if err := GeneratePackageManagerLaunchers(e); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(filepath.Join(e.LauncherDir(), "pnpm"))
	cmd.Env = append([]string{
		"HOME=" + home,
		"PATH=" + fakeBin + ":" + os.Getenv("PATH"),
	}, env...)
	out, err := cmd.CombinedOutput()
	rc := 0
	if ee, ok := err.(*exec.ExitError); ok {
		rc = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("pnpm launcher could not be run at all: %v\n%s", err, out)
	}
	return string(out), rc, readReceipts(t, filepath.Join(ws, ".yolo", "receipts.jsonl"))
}

// TestPackageManagerInstallLeavesAReceipt closes the fourth install site. "Every install
// yolo itself runs leaves one line" (§10 step one) is a claim about the SET, and pnpm was
// the member missing from it: a program fetched from the registry at first use, with no
// record anywhere that it happened.
func TestPackageManagerInstallLeavesAReceipt(t *testing.T) {
	out, rc, got := pnpmLauncherRun(t)
	if rc != 0 || !strings.Contains(out, "PNPM_RAN") {
		t.Fatalf("the launcher must install and then exec pnpm (rc=%d):\n%s", rc, out)
	}

	r := requireOne(t, got)
	for _, want := range []struct{ key, val string }{
		// The RESOLVER is npm, which is what the kind names — not the declaration's
		// origin, which for pnpm is a hardcoded list rather than a pack.
		{"kind", "npm"},
		{"bin", "pnpm"},
		{"declared", "pnpm"},
		{"spec", "pnpm@latest"},
		{"act", "install"},
	} {
		if got := str(t, r, want.key); got != want.val {
			t.Errorf("%s = %q, want %q", want.key, got, want.val)
		}
	}
	if path := str(t, r, "path"); !strings.HasSuffix(path, "/.npm-global/bin/pnpm") {
		t.Errorf("path = %q, want the binary this install landed at", path)
	}
}

// TestPackageManagerReceiptIsNotWrittenAfterAFailedInstall is the "|| true" trap in the one
// launcher that still had it. This install fails ROUTINELY — the RETRY_INTERVAL throttle
// above it exists because offline attempts are ordinary — so a receipt appended after an
// unconditional success would be wrong on exactly the boots the record is for.
func TestPackageManagerReceiptIsNotWrittenAfterAFailedInstall(t *testing.T) {
	out, rc, got := pnpmLauncherRun(t, "FAKE_INSTALL_FAIL=1")
	if len(got) != 0 {
		t.Errorf("a failed install must leave no receipt, got %v\n%s", got, out)
	}
	// The launch path's verdict is unchanged by the status capture: nothing to exec.
	if rc == 0 || !strings.Contains(out, "not available") {
		t.Errorf("a failed install must still end in the not-available verdict (rc=%d):\n%s",
			rc, out)
	}
}

// --- native / installer ------------------------------------------------------------

// TestNativeInstallLeavesAReceiptWithADigest: a vendor installer publishes no lockable
// artifact, so what it LEFT is the only resolved identity there is (§6.3). The digest and
// the size are that identity.
func TestNativeInstallLeavesAReceiptWithADigest(t *testing.T) {
	url := serveBody(t, 200, "application/x-sh", strings.Join([]string{
		"#!/bin/bash",
		"set -eu",
		`mkdir -p "$HOME/.local/bin"`,
		`printf '#!/bin/bash\necho PROBETOOL_RAN\n' > "$HOME/.local/bin/probetool"`,
		`chmod +x "$HOME/.local/bin/probetool"`,
	}, "\n")+"\n")

	rc, out, got := runNativeLauncherWithReceipts(t, url)
	if rc != 0 {
		t.Fatalf("a real installer must succeed, rc=%d\n%s", rc, out)
	}
	r := requireOne(t, got)
	if k := str(t, r, "kind"); k != "installer" {
		t.Errorf("kind = %q, want installer", k)
	}
	if d := str(t, r, "declared"); d != url {
		t.Errorf("declared = %q, want the installer URL %q", d, url)
	}
	dig := str(t, r, "sha256")
	if len(dig) != 64 || strings.Trim(dig, "0123456789abcdef") != "" {
		t.Errorf("sha256 = %q, want 64 bare hex chars — the three digest tools print it in "+
			"three different columns and only the normalized form is comparable", dig)
	}
	n, ok := r["bytes"].(float64)
	if !ok || n <= 0 {
		t.Errorf("bytes = %v, want a positive JSON number: %v", r["bytes"], r)
	}
	// An installer's own version is the vendor's business and unobservable from here, so
	// the field is omitted rather than guessed.
	if _, present := r["resolved"]; present {
		t.Errorf("an installer receipt must not invent a resolved version: %v", r)
	}
	// The landing path (§6's tuple), which for this kind is the only identity there is
	// besides the digest — and the two are of the same file here, deliberately.
	if path := str(t, r, "path"); !strings.HasSuffix(path, "/.local/bin/probetool") {
		t.Errorf("path = %q, want the binary the installer left in ~/.local/bin", path)
	}
}

// TestNativeFailurePathsLeaveNoReceipt is the guard's whole reason to exist. _do_install
// returns 0 down both failure branches — on the launch path a failed install is not the
// verdict — so an unguarded hook would record an install for exactly the two shapes that
// installed nothing, and both are the SHIPPED diagnosis for a stale installer URL.
func TestNativeFailurePathsLeaveNoReceipt(t *testing.T) {
	t.Run("served a web page", func(t *testing.T) {
		url := serveBody(t, 200, "text/html; charset=utf-8",
			`<!doctype html><html lang="en-US"><head></head></html>`)
		_, out, got := runNativeLauncherWithReceipts(t, url)
		if len(got) != 0 {
			t.Errorf("a served web page installed nothing and must leave no receipt: %v\n%s",
				got, out)
		}
	})
	t.Run("download failed", func(t *testing.T) {
		url := serveBody(t, 404, "text/html", "not found")
		_, out, got := runNativeLauncherWithReceipts(t, url)
		if len(got) != 0 {
			t.Errorf("a failed download must leave no receipt: %v\n%s", got, out)
		}
	})
}

// TestNativeLauncherVendorSelfUpdateEmitsNoReceipt pins the deliberate GAP, because a gap
// nothing asserts is indistinguishable from a hook someone forgot. `"$REAL_BIN" install`
// is the vendor's own updater: what moved, to what, and where it landed are all its
// decisions, and the launcher observes none of them. That drift is the reconcile's to
// report (§6.3), against the bytes rather than against a claim.
func TestNativeLauncherVendorSelfUpdateEmitsNoReceipt(t *testing.T) {
	body := nativeAgentLauncher(
		&packdecl.Install{Kind: "native", Bin: "probetool", InstallerURL: "https://x.invalid/i.sh"},
		"/stamps", "/tmp/receipts.jsonl",
	)
	for _, line := range strings.Split(body, "\n") {
		if !strings.Contains(line, `YOLO_BYPASS_SHIMS=1 "$REAL_BIN" install`) {
			continue
		}
		if strings.Contains(line, "_yolo_receipt") {
			t.Errorf("the vendor self-update must emit no receipt:\n%s", line)
		}
	}
	// And the reason has to survive in the file, or the next reader adds the hook back.
	if !strings.Contains(body, "EMIT NO RECEIPT") {
		t.Error("the two self-update branches must say WHY they record nothing")
	}
}

// --- LSP bootstrap -------------------------------------------------------------------

// bootstrapProbe is a temp jail home wired with fake npm/jq/go, so ~/.yolo-bootstrap.sh's
// LSP install loop can be RUN. That loop reads its package list from a PIPE, so it executes
// in a subshell — the one detail that makes "assert on the script text" useless here: a
// receipt accumulated in a variable and written after `done` is lost, silently, and looks
// perfectly correct in a diff.
type bootstrapProbe struct {
	home    string
	fakeBin string
	// presets is YOLO_MCP_PRESETS, which is a GENERATION-time input rather than a run-time
	// one: the package list is baked into the script by mcpPresetNpmPackages, so a test
	// that set it in the run env would exercise nothing.
	presets      string
	receiptsPath string
	sentinel     string
	gobin        string
}

func newBootstrapProbe(t *testing.T) *bootstrapProbe {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not found")
	}
	home := t.TempDir()
	fakeBin := filepath.Join(home, "fakebin")
	if err := os.MkdirAll(fakeBin, 0o755); err != nil {
		t.Fatal(err)
	}

	// `npm ls -g` always reports "not installed", so the install arm always runs.
	npm := `#!/bin/bash
case "${1:-}" in
ls) exit 1 ;;
install)
    if [ -n "${FAKE_NPM_FAIL:-}" ]; then echo "npm ERR! network unreachable" >&2; exit 1; fi
    pkg="${@: -1}"
    mkdir -p "$NPM_CONFIG_PREFIX/lib/node_modules/$pkg"
    printf '{"version":"3.2.1"}\n' > "$NPM_CONFIG_PREFIX/lib/node_modules/$pkg/package.json"
    ;;
esac
exit 0
`
	// `go install <pkg@ver>` lands a binary in $GOBIN; `go version -m <bin>` answers with
	// the tab-separated mod row the real one prints (leading tab included).
	goFake := `#!/bin/bash
case "${1:-}" in
install)
    if [ -n "${FAKE_GO_FAIL:-}" ]; then echo "go: cannot download" >&2; exit 1; fi
    pkg="${2:-}"
    base="${pkg%@*}"
    bin="${base##*/}"
    mkdir -p "$GOBIN"
    printf '#!/bin/sh\necho GO_RAN\n' > "$GOBIN/$bin"
    chmod +x "$GOBIN/$bin"
    ;;
version)
    if [ -n "${FAKE_GO_NO_MODINFO:-}" ]; then echo "go: no module info" >&2; exit 1; fi
    printf '%s: go1.26.0\n' "${3:-}"
    printf '\tpath\tgithub.com/example/tool\n'
    printf '\tmod\tgithub.com/example/tool\tv1.4.2\th1:deadbeef=\n'
    ;;
esac
exit 0
`
	jq := `#!/bin/bash
f="${@: -1}"
[ -f "$f" ] || exit 1
line=$(tr -d ' \t' < "$f")
line="${line#*\"version\":\"}"
printf '%s\n' "${line%%\"*}"
`
	for name, body := range map[string]string{"npm": npm, "go": goFake, "jq": jq} {
		if err := os.WriteFile(filepath.Join(fakeBin, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return &bootstrapProbe{
		home:    home,
		fakeBin: fakeBin,
		// A workspace whose .yolo does not exist: the receipt writer must create it.
		receiptsPath: filepath.Join(home, "ws", ".yolo", "receipts.jsonl"),
		sentinel:     filepath.Join(home, ".yolo-installed-lsps"),
		gobin:        filepath.Join(home, "go", "bin"),
	}
}

// run generates and executes ~/.yolo-bootstrap.sh with the given extra environment.
func (b *bootstrapProbe) run(t *testing.T, env ...string) string {
	t.Helper()
	e := NewEnv(map[string]string{
		"JAIL_HOME":        b.home,
		"YOLO_WORKSPACE":   filepath.Join(b.home, "ws"),
		"YOLO_MCP_PRESETS": b.presets,
	})
	if err := GenerateBootstrapScript(e); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bootstrapPath(e))
	// The fakes plus the system dirs, NEVER the developer's own $PATH. The MCP block
	// decides whether to install by probing `command -v mcp-server-sequential-thinking`,
	// and this jail's real ~/.npm-global/bin is on the inherited PATH — so inheriting it
	// made the block skip its install and the test pass or fail on whether the machine
	// running it happens to use that MCP server.
	cmd.Env = append([]string{
		"HOME=" + b.home,
		"PATH=" + b.fakeBin + ":/bin:/usr/bin",
	}, env...)
	out, err := cmd.CombinedOutput()
	if _, isExit := err.(*exec.ExitError); err != nil && !isExit {
		t.Fatalf("bootstrap could not be run at all: %v\n%s", err, out)
	}
	return string(out)
}

func (b *bootstrapProbe) receipts(t *testing.T) []map[string]any {
	t.Helper()
	return readReceipts(t, b.receiptsPath)
}

// pick returns the one receipt of the given kind, or fails.
func pick(t *testing.T, got []map[string]any, kind string) map[string]any {
	t.Helper()
	var found []map[string]any
	for _, r := range got {
		if r["kind"] == kind {
			found = append(found, r)
		}
	}
	if len(found) != 1 {
		t.Fatalf("want exactly 1 %q receipt, got %d of them in %v", kind, len(found), got)
	}
	return found[0]
}

// TestLSPInstallsLeaveReceipts covers the third install site: the bootstrap's LSP loop.
func TestLSPInstallsLeaveReceipts(t *testing.T) {
	b := newBootstrapProbe(t)
	out := b.run(t,
		"YOLO_LSP_NPM_INSTALL=pyright",
		"YOLO_LSP_GO_INSTALL=github.com/example/tool@v1.4.2")

	got := b.receipts(t)
	if len(got) != 2 {
		t.Fatalf("want one receipt per LSP install, got %d: %v\n%s", len(got), got, out)
	}

	npm := pick(t, got, "lsp-npm")
	if d := str(t, npm, "declared"); d != "pyright" {
		t.Errorf("lsp-npm declared = %q, want pyright", d)
	}
	if v := str(t, npm, "resolved"); v != "3.2.1" {
		t.Errorf("lsp-npm resolved = %q, want the version from the installed package.json", v)
	}

	goR := pick(t, got, "lsp-go")
	if d := str(t, goR, "declared"); d != "github.com/example/tool@v1.4.2" {
		t.Errorf("lsp-go declared = %q, want the declared module@version", d)
	}
	// The bin name the uninstall loop derives, so a later reconcile can find the file.
	if bin := str(t, goR, "bin"); bin != "tool" {
		t.Errorf("lsp-go bin = %q, want tool", bin)
	}
	// From `go version -m`, not from the declaration: a declaration may name a branch or
	// a pseudo-version and the binary knows what it actually is.
	if v := str(t, goR, "resolved"); v != "v1.4.2" {
		t.Errorf("lsp-go resolved = %q, want the mod line's version", v)
	}
}

// TestMCPPresetInstallLeavesAReceiptPerPackage covers the install site the "every install
// yolo runs" claim was missing on the bootstrap side.
//
// The MCP block installs the whole preset list in ONE npm invocation, because that is what
// npm is good at — but the receipt's unit is a package, not an invocation: a reader asking
// where a given server came from has to find a line naming it.
func TestMCPPresetInstallLeavesAReceiptPerPackage(t *testing.T) {
	b := newBootstrapProbe(t)
	b.presets = `["sequential-thinking"]`
	out := b.run(t)

	r := pick(t, b.receipts(t), "mcp-npm")
	if d := str(t, r, "declared"); d != "@modelcontextprotocol/server-sequential-thinking" {
		t.Errorf("mcp-npm declared = %q, want the package the enabled preset needs\n%s", d, out)
	}
	if v := str(t, r, "resolved"); v != "3.2.1" {
		t.Errorf("mcp-npm resolved = %q, want the version from the installed package.json", v)
	}
	if act := str(t, r, "act"); act != "install" {
		t.Errorf("mcp-npm act = %q, want install", act)
	}
	// No landing path on this arm: the kind names the resolver, and the resolver owns one
	// prefix — see receiptPrefix. A per-line copy of one constant is not information.
	if p, present := r["path"]; present {
		t.Errorf("the mcp-npm arm installs a LIST into the prefix its kind implies; a path "+
			"field there is a repeated constant: %v", p)
	}
}

// TestMCPPresetReceiptIsNotWrittenAfterAFailedInstall: the arm captures npm's status rather
// than running under "|| true", for the same reason the LSP arms do — an offline boot fails
// here routinely and simply retries next launch, and a receipt is a claim about bytes.
func TestMCPPresetReceiptIsNotWrittenAfterAFailedInstall(t *testing.T) {
	b := newBootstrapProbe(t)
	b.presets = `["sequential-thinking"]`
	out := b.run(t, "FAKE_NPM_FAIL=1")

	for _, r := range b.receipts(t) {
		if r["kind"] == "mcp-npm" {
			t.Errorf("a failed MCP install must leave no receipt: %v\n%s", r, out)
		}
	}
}

// TestMCPPresetWithNoEnabledPresetInstallsNothingAndRecordsNothing: the receipt loop must be
// inside the gate, not beside it. D6 made the install preset-gated after measuring 112 npm
// packages in a jail with no agents and no presets; a receipt written for a jail that
// installed nothing would re-introduce exactly that claim in the record.
func TestMCPPresetWithNoEnabledPresetInstallsNothingAndRecordsNothing(t *testing.T) {
	b := newBootstrapProbe(t)
	out := b.run(t) // no presets

	for _, r := range b.receipts(t) {
		if r["kind"] == "mcp-npm" {
			t.Errorf("no preset asked for anything, so nothing was installed: %v\n%s", r, out)
		}
	}
}

// TestLSPReceiptsAreNotWrittenAfterAFailedInstall is the "|| true" trap, closed.
//
// Both arms used to end in `|| true`, which discards the status: appending a receipt after
// one records every attempt as a success, including the offline boot that installed
// nothing and will retry next launch.
func TestLSPReceiptsAreNotWrittenAfterAFailedInstall(t *testing.T) {
	b := newBootstrapProbe(t)
	out := b.run(t,
		"YOLO_LSP_NPM_INSTALL=pyright",
		"YOLO_LSP_GO_INSTALL=github.com/example/tool@v1.4.2",
		"FAKE_NPM_FAIL=1")

	got := b.receipts(t)
	for _, r := range got {
		if r["kind"] == "lsp-npm" {
			t.Errorf("a failed npm install must leave no receipt: %v\n%s", r, out)
		}
	}
	// ...and the failure must not take the sibling arm down with it: the loop still has
	// to install the go server, and still has to record it.
	pick(t, got, "lsp-go")
}

// TestLSPGoReceiptOmitsAnUnreadableVersion: `go version -m` fails on a binary built
// without module info, and the answer to "what version is this?" is then nothing. Omitting
// the field says that; a placeholder would be a fact nobody measured.
func TestLSPGoReceiptOmitsAnUnreadableVersion(t *testing.T) {
	b := newBootstrapProbe(t)
	b.run(t, "YOLO_LSP_GO_INSTALL=github.com/example/tool@v1.4.2", "FAKE_GO_NO_MODINFO=1")

	r := pick(t, b.receipts(t), "lsp-go")
	if _, present := r["resolved"]; present {
		t.Errorf("an unparseable `go version -m` must omit resolved, not invent it: %v", r)
	}
	// The rest of the receipt is still true and still worth having.
	if d := str(t, r, "declared"); d != "github.com/example/tool@v1.4.2" {
		t.Errorf("declared = %q", d)
	}
}

// TestLSPSentinelBytesAreUnchangedByTheReceiptHook is the constraint the receipt work had
// to not break, pinned so a later edit cannot.
//
// ~/.yolo-installed-lsps is read back by the UNINSTALL loop with an exact-line match
// (`grep -qxF`), so any change to its format — a trailing field, a different separator, a
// reordering — orphans every entry a previous boot wrote: the loop would find no line
// matching and uninstall the user's whole configured LSP set on the next launch.
func TestLSPSentinelBytesAreUnchangedByTheReceiptHook(t *testing.T) {
	b := newBootstrapProbe(t)
	b.run(t,
		"YOLO_LSP_NPM_INSTALL=pyright",
		"YOLO_LSP_GO_INSTALL=github.com/example/tool@v1.4.2")

	data, err := os.ReadFile(b.sentinel)
	if err != nil {
		t.Fatal(err)
	}
	const want = "npm:pyright\ngo:github.com/example/tool@v1.4.2\n"
	if string(data) != want {
		t.Errorf("sentinel bytes changed.\n got: %q\nwant: %q\n\nThe uninstall loop matches "+
			"these lines EXACTLY, so a format change silently uninstalls every server a "+
			"previous boot installed.", data, want)
	}
}

// TestBootstrapReceiptPathIsBakedNotRead is constraint 1 for the third site.
//
// YOLO_WORKSPACE is a HOST-side launcher input: it does not exist inside a live container,
// and macos-user launches under `env -i`. A template that read it would have written every
// jail's receipts to the container default, silently, on the one backend with no image to
// hide it — the same shape as the stat -c bug stampMtimeFn documents.
func TestBootstrapReceiptPathIsBakedNotRead(t *testing.T) {
	e := NewEnv(map[string]string{
		"JAIL_HOME":      t.TempDir(),
		"YOLO_WORKSPACE": "/Users/matt/code/thing",
	})
	script := BootstrapScript(e)
	if !strings.Contains(script, "_YOLO_RECEIPTS=/Users/matt/code/thing/.yolo/receipts.jsonl") {
		t.Errorf("the receipts path must be baked from WorkspaceDir at generation time:\n%s",
			script)
	}
	if strings.Contains(script, "${YOLO_WORKSPACE") {
		t.Error("the generated script must not read YOLO_WORKSPACE — it is absent in a live " +
			"container and under macos-user's env -i")
	}

	// A workspace whose path needs quoting must still produce a runnable script.
	e = NewEnv(map[string]string{
		"JAIL_HOME":      t.TempDir(),
		"YOLO_WORKSPACE": "/tmp/a b'c",
	})
	if !strings.Contains(BootstrapScript(e), `_YOLO_RECEIPTS='/tmp/a b'"'"'c/.yolo/receipts.jsonl'`) {
		t.Errorf("a workspace path with shell metacharacters must be shell-quoted:\n%s",
			BootstrapScript(e))
	}
}

// TestLauncherReceiptPathIsBakedNotRead is the same property for the two launcher
// templates, which is where it matters most: they are the scripts macos-user runs
// natively, under env -i.
func TestLauncherReceiptPathIsBakedNotRead(t *testing.T) {
	for name, body := range map[string]string{
		"npm": npmAgentLauncher(&packdecl.Install{Kind: "npm", Bin: "t", Package: "t"},
			"/stamps", "/ws/.yolo/receipts.jsonl"),
		"native": nativeAgentLauncher(&packdecl.Install{Kind: "native", Bin: "t", InstallerURL: "u"},
			"/stamps", "/ws/.yolo/receipts.jsonl"),
	} {
		if !strings.Contains(body, "_YOLO_RECEIPTS=/ws/.yolo/receipts.jsonl") {
			t.Errorf("%s launcher does not bake the receipts path:\n%s", name, body)
		}
		if strings.Contains(body, "YOLO_WORKSPACE") {
			t.Errorf("%s launcher reads YOLO_WORKSPACE, which is absent in a live container "+
				"and under macos-user's env -i:\n%s", name, body)
		}
	}
}

// TestGenerateAgentLaunchersBakesTheWorkspaceReceiptsPath is the CALL SITE, which the two
// tests above cannot reach: they hand the renderers a path, so they would both pass against
// a GenerateAgentLaunchers that resolved the wrong workspace — or none.
func TestGenerateAgentLaunchersBakesTheWorkspaceReceiptsPath(t *testing.T) {
	home := t.TempDir()
	packRoot := t.TempDir()
	packDir := filepath.Join(packRoot, "toolpack")
	if err := os.MkdirAll(packDir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name":"toolpack","contributes":[` +
		`{"kind":"program","bin":"npmtool","via":"npm","package":"npmtool"},` +
		`{"kind":"program","bin":"nativetool","via":"installer","url":"https://x.invalid/i.sh"}` +
		`]}`
	if err := os.WriteFile(filepath.Join(packDir, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}

	ws := filepath.Join(home, "myworkspace")
	e := NewEnv(map[string]string{
		"JAIL_HOME":      home,
		"YOLO_PACK_ROOT": packRoot,
		"YOLO_WORKSPACE": ws,
	})
	if err := GenerateAgentLaunchers(e); err != nil {
		t.Fatal(err)
	}
	for _, bin := range []string{"npmtool", "nativetool"} {
		data, err := os.ReadFile(filepath.Join(e.LauncherDir(), bin))
		if err != nil {
			t.Fatal(err)
		}
		want := "_YOLO_RECEIPTS=" + filepath.Join(ws, ".yolo", "receipts.jsonl")
		if !strings.Contains(string(data), want) {
			t.Errorf("%s launcher must bake %q:\n%s", bin, want, data)
		}
	}
}

// TestReceiptPrefixEscapesTheDeclaration: the head is rendered in Go with encoding/json for
// one reason — a declaration is a pack author's string, and one quote in it would otherwise
// produce a line no reader can parse, for the life of the file.
func TestReceiptPrefixEscapesTheDeclaration(t *testing.T) {
	head := receiptPrefix("npm", `bi"n`, `pkg\"@1`)
	var m map[string]any
	if err := json.Unmarshal([]byte(head+"}"), &m); err != nil {
		t.Fatalf("head is not parseable JSON (%v): %s", err, head)
	}
	if m["bin"] != `bi"n` || m["declared"] != `pkg\"@1` {
		t.Errorf("round-trip lost the original strings: %v", m)
	}
	// The LSP loop renders only the constant half; bin/declared come from the shell.
	if got := receiptPrefix("lsp-npm", "", ""); got != `{"schema":1,"kind":"lsp-npm"` {
		t.Errorf("empty bin/declared must be omitted, got %q", got)
	}
}
