package entrypoint

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// programreconcile_test.go covers program-delivery.md §10 step two: the offline comparison
// between what the receipts say a jail GOT and what is on disk. It reports; it installs
// nothing and removes nothing (A4 as ruled, §5.4).
//
// It is a separate file from reconcile_test.go, which is about the prism's managed-table
// render and shares only the word.

// reconcileHome stages a jail home plus a one-pack tree and returns (home, workspace,
// packRoot). The pack tree exists because the reconcile is YOLO_PACK_ROOT-gated for
// catalog.go's reason; its contents are irrelevant to every comparison here.
func reconcileHome(t *testing.T) (home, ws, packRoot string) {
	t.Helper()
	home = t.TempDir()
	ws = filepath.Join(home, "ws")
	packRoot = t.TempDir()
	packDir := filepath.Join(packRoot, "toolpack")
	if err := os.MkdirAll(packDir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name":"toolpack","contributes":[` +
		`{"kind":"program","bin":"declared-npm","via":"npm","package":"@scope/declared@1.2.3"}` +
		`]}`
	if err := os.WriteFile(filepath.Join(packDir, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	return home, ws, packRoot
}

// writeReceipts writes the given raw lines to <ws>/.yolo/receipts.jsonl.
//
// Raw LINES rather than marshalled structs, because the reader's contract is with the bytes a
// shell wrote — including the malformed ones a struct could not express. The round-trip test
// below is what keeps these lines honest against the real writer.
func writeReceipts(t *testing.T, ws string, lines ...string) {
	t.Helper()
	dir := filepath.Join(ws, ".yolo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := ""
	for _, l := range lines {
		body += l + "\n"
	}
	if err := os.WriteFile(filepath.Join(dir, "receipts.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// seedNpmVersion materializes a globally installed package with a package.json version.
func seedNpmVersion(t *testing.T, home, name, version string) {
	t.Helper()
	dir := filepath.Join(home, ".npm-global", "lib", "node_modules", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(map[string]string{"name": name, "version": version})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "package.json"), body, 0o644); err != nil {
		t.Fatal(err)
	}
}

// runReconcile drives the production entry point and returns (terminal output, log-only
// output). The split is load-bearing: the empty-file note goes to the LOG and must never
// reach a terminal (Env.LogOnly, env.go:60-70).
func runReconcile(t *testing.T, vars map[string]string) (string, string) {
	t.Helper()
	var term, logOnly strings.Builder
	e := NewEnv(vars)
	e.Stderr = &term
	e.LogOnly = &logOnly
	ReconcileInstalledPrograms(e)
	return term.String(), logOnly.String()
}

// --- the npm comparison -------------------------------------------------------------

// TestReconcileNamesAnNpmVersionMismatch is the first of the three comparisons: the receipt
// says one version, the installed package.json says another. Offline, no registry.
func TestReconcileNamesAnNpmVersionMismatch(t *testing.T) {
	home, ws, packRoot := reconcileHome(t)
	seedNpmVersion(t, home, "@scope/tool", "3.0.0")
	writeReceipts(t, ws, `{"schema":1,"kind":"npm","bin":"tool","declared":"@scope/tool@2.0.0",`+
		`"spec":"@scope/tool@2.0.0","resolved":"2.0.0","path":"/x/tool","act":"install",`+
		`"time":"2026-08-24T10:00:00Z"}`)

	term, _ := runReconcile(t, map[string]string{
		"JAIL_HOME": home, "YOLO_WORKSPACE": ws, "YOLO_PACK_ROOT": packRoot,
	})
	if !strings.Contains(term, "@scope/tool") {
		t.Fatalf("the drifted package must be named:\n%s", term)
	}
	for _, want := range []string{"2.0.0", "3.0.0", reconcilePrefix} {
		if !strings.Contains(term, want) {
			t.Errorf("the report must state %q — a mismatch nobody can read the two sides of "+
				"is not a report:\n%s", want, term)
		}
	}
	// The ACT, which is the half of OQ-PD8 this venue inherits. The launcher's dead poll
	// named `yolo pack update` and said why: "a report the reader cannot act on is worse than
	// the reinstall it replaces". The boot is where that sentence has to be said now.
	if !strings.Contains(term, "yolo pack update") {
		t.Errorf("the finding must name the act that resolves it:\n%s", term)
	}
	// ...and it must NOT claim the registry question. This comparison is offline by ruling
	// (A4, §5.4: "a launch must not depend on a registry being reachable"), so "a newer
	// version is available upstream" is the update verb's answer, never this one's.
	if strings.Contains(term, "available") {
		t.Errorf("an offline comparison cannot speak about what the registry has:\n%s", term)
	}
}

// TestReconcileIsQuietWhenTheNpmVersionAgrees is the negative the mismatch test cannot see:
// a comparison that reported unconditionally would satisfy the assertion above just as well,
// and would then put a line on every launch of every jail.
func TestReconcileIsQuietWhenTheNpmVersionAgrees(t *testing.T) {
	home, ws, packRoot := reconcileHome(t)
	seedNpmVersion(t, home, "@scope/tool", "2.0.0")
	writeReceipts(t, ws, `{"schema":1,"kind":"npm","bin":"tool","declared":"@scope/tool@2.0.0",`+
		`"resolved":"2.0.0","act":"install","time":"2026-08-24T10:00:00Z"}`)

	if term, _ := runReconcile(t, map[string]string{
		"JAIL_HOME": home, "YOLO_WORKSPACE": ws, "YOLO_PACK_ROOT": packRoot,
	}); term != "" {
		t.Errorf("agreement is the common case and must be silent, got:\n%s", term)
	}
}

// TestReconcileTreatsAnAbsentResolvedAsUnknownNotZero is the FORGERY, closed from the reader's
// end.
//
// shims.go:592-610 is explicit that `_installed_version`'s `|| echo 0` is a POLL sentinel and
// that copying it into a receipt would be a forgery — so the writer OMITS the field when it
// could not read a version (a run with no jq on PATH; macos-user execs these launchers under
// env -i). A reader that defaulted the absent field to "0" would commit the same forgery from
// the other side: every such install would be reported as drifting from 0 to its real
// version, on every boot, and the reports that mattered would be lost in them.
func TestReconcileTreatsAnAbsentResolvedAsUnknownNotZero(t *testing.T) {
	home, ws, packRoot := reconcileHome(t)
	seedNpmVersion(t, home, "tool", "1.5.0")
	writeReceipts(t, ws, `{"schema":1,"kind":"npm","bin":"tool","declared":"tool",`+
		`"spec":"tool@latest","act":"install","time":"2026-08-24T10:00:00Z"}`)

	term, _ := runReconcile(t, map[string]string{
		"JAIL_HOME": home, "YOLO_WORKSPACE": ws, "YOLO_PACK_ROOT": packRoot,
	})
	if term != "" {
		t.Errorf("an omitted `resolved` means UNKNOWN, never 0 — it is not comparable and "+
			"must produce no finding:\n%s", term)
	}
	// And the parse itself must say so, so the property is pinned at the reader too.
	r, err := parseReceiptLine(`{"schema":1,"kind":"npm","bin":"tool","act":"install"}`)
	if err != nil {
		t.Fatal(err)
	}
	if r.HasResolved() {
		t.Errorf("HasResolved must be false for an omitted field: %+v", r)
	}
}

// TestReconcileUsesTheNameHalfOfAPinnedDeclaration: node_modules is indexed by NAME, so a
// declaration carrying a selector (`foo@1.2.3`) names a directory that does not exist. The
// same split the launcher makes, for the same reason (npmspec.go).
func TestReconcileUsesTheNameHalfOfAPinnedDeclaration(t *testing.T) {
	home, ws, packRoot := reconcileHome(t)
	seedNpmVersion(t, home, "@scope/tool", "9.9.9")
	writeReceipts(t, ws, `{"schema":1,"kind":"npm","bin":"tool","declared":"@scope/tool@2.0.0",`+
		`"resolved":"2.0.0","act":"install","time":"2026-08-24T10:00:00Z"}`)

	term, _ := runReconcile(t, map[string]string{
		"JAIL_HOME": home, "YOLO_WORKSPACE": ws, "YOLO_PACK_ROOT": packRoot,
	})
	if !strings.Contains(term, "9.9.9") {
		t.Errorf("a pinned declaration must still resolve to the installed directory — "+
			"otherwise every pinned package is silently uncomparable:\n%s", term)
	}
	// And the report names the PACKAGE, not the spec: `@scope/tool@2.0.0` is not a thing on
	// disk anyone can go look at.
	if strings.Contains(term, "@scope/tool@2.0.0:") {
		t.Errorf("the finding must be keyed by the package name:\n%s", term)
	}
}

// TestReconcileComparesTheLatestReceiptForAProgram: the log is append-only, so an update
// writes a SECOND line for a program that already has one. Comparing against the first would
// report the version the jail was provisioned with as drift from the version it deliberately
// updated to — a false finding on exactly the jails that used `yolo pack update`.
func TestReconcileComparesTheLatestReceiptForAProgram(t *testing.T) {
	home, ws, packRoot := reconcileHome(t)
	seedNpmVersion(t, home, "tool", "2.0.0")
	writeReceipts(t, ws,
		`{"schema":1,"kind":"npm","bin":"tool","declared":"tool","resolved":"1.0.0",`+
			`"act":"install","time":"2026-08-24T10:00:00Z"}`,
		`{"schema":1,"kind":"npm","bin":"tool","declared":"tool","resolved":"2.0.0",`+
			`"act":"update","time":"2026-08-24T10:00:00Z"}`)

	if term, _ := runReconcile(t, map[string]string{
		"JAIL_HOME": home, "YOLO_WORKSPACE": ws, "YOLO_PACK_ROOT": packRoot,
	}); term != "" {
		t.Errorf("the newest receipt for a program is the one that describes the bytes; an "+
			"update must not read as drift:\n%s", term)
	}

	// ...and the reduction itself, at the level it is stated. Same second in the `time`
	// field on purpose: the stamp has one-second resolution, so FILE ORDER is what decides.
	got := latestReceipts([]receipt{
		{Kind: "npm", Bin: "tool", Resolved: "1.0.0"},
		{Kind: "npm", Bin: "tool", Resolved: "2.0.0"},
		{Kind: "lsp-go", Bin: "tool", Resolved: "v1.0.0"},
	})
	if len(got) != 2 {
		t.Fatalf("kind is part of the key — an npm `tool` and an LSP go `tool` are different "+
			"bytes in different directories: %v", got)
	}
	if r := got[receiptKey{Kind: "npm", Bin: "tool"}]; r.Resolved != "2.0.0" {
		t.Errorf("last append wins, got %q", r.Resolved)
	}
}

// --- the installer digest comparison -------------------------------------------------

// installerReceipt renders an installer receipt line for a real file on disk.
func installerReceipt(t *testing.T, path, sha string, bytes int64) string {
	t.Helper()
	line := map[string]any{
		"schema": 1, "kind": "installer", "bin": filepath.Base(path),
		"declared": "https://x.invalid/i.sh", "sha256": sha, "bytes": bytes,
		"path": path, "act": "install", "time": "2026-08-24T10:00:00Z",
	}
	body, err := json.Marshal(line)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

// TestReconcileNamesInstallerDigestDrift is the gap the launcher deliberately leaves: the two
// vendor self-update branches emit NO receipt, because what moved, to what and where are all
// the vendor's decisions and a receipt written there would be a guess with a timestamp on it.
// shims.go says the drift is "the RECONCILE's to report, against the bytes on disk rather
// than against a claim" (§6.3) — this is that report.
func TestReconcileNamesInstallerDigestDrift(t *testing.T) {
	home, ws, packRoot := reconcileHome(t)
	bin := filepath.Join(home, ".local", "bin", "probetool")
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, []byte("VERSION-TWO"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A receipt whose digest is of DIFFERENT bytes of the SAME length: the size test passes,
	// so only the hash can find this — which is the case the hash exists for.
	oldSum, err := fileSHA256Of(t, []byte("VERSION-ONE"))
	if err != nil {
		t.Fatal(err)
	}
	writeReceipts(t, ws, installerReceipt(t, bin, oldSum, int64(len("VERSION-ONE"))))

	term, _ := runReconcile(t, map[string]string{
		"JAIL_HOME": home, "YOLO_WORKSPACE": ws, "YOLO_PACK_ROOT": packRoot,
	})
	if !strings.Contains(term, bin) {
		t.Fatalf("the drifted binary must be named by its landing path:\n%s", term)
	}
	if !strings.Contains(term, "different bytes") {
		t.Errorf("the finding must say WHAT differs — a digest pair on its own is unreadable:"+
			"\n%s", term)
	}
	if !strings.Contains(term, shortDigest(oldSum)) {
		t.Errorf("the report must state the digest the receipt recorded:\n%s", term)
	}
}

// TestReconcileStatsBeforeItDigests is the boot-path cost, made OBSERVABLE rather than
// merely commented.
//
// ~/.local/bin/agy is 181 MB in this jail and claude keeps just over 1 GB of builds per
// workspace (§5.3). This runs before the exec that hands control to the agent, so hashing
// every installer-managed binary on every launch would be a per-boot cost paid to answer a
// question a stat already answers whenever the answer is "different". A size change IS drift
// and needs no hash to establish it.
//
// THE FIXTURE IS DELIBERATELY SELF-INCONSISTENT, and that is the only way to see the order
// from the outside: the receipt's `sha256` is the digest of the bytes that ARE on disk while
// its `bytes` is a different number. A size-first implementation compares the sizes, finds
// them different, and reports. A digest-first one compares the hashes, finds them EQUAL, and
// reports nothing — so the two orderings are distinguishable by output instead of by timing,
// on any machine, including one running as root where an unreadable-file probe is impossible.
func TestReconcileStatsBeforeItDigests(t *testing.T) {
	home, ws, packRoot := reconcileHome(t)
	bin := filepath.Join(home, ".local", "bin", "bigtool")
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	body := []byte("much longer contents than before")
	if err := os.WriteFile(bin, body, 0o755); err != nil {
		t.Fatal(err)
	}
	matchingSum, err := fileSHA256Of(t, body)
	if err != nil {
		t.Fatal(err)
	}
	writeReceipts(t, ws, installerReceipt(t, bin, matchingSum, int64(len(body))-7))

	term, _ := runReconcile(t, map[string]string{
		"JAIL_HOME": home, "YOLO_WORKSPACE": ws, "YOLO_PACK_ROOT": packRoot,
	})
	if !strings.Contains(term, "bytes") {
		t.Fatalf("a size change is already drift and must be reported from the stat alone — "+
			"digesting a 181 MB binary on every boot to learn what st_size already said is a "+
			"per-boot cost for nothing:\n%s", term)
	}
	if strings.Contains(term, "different bytes") {
		t.Errorf("a size difference must not be reported as a same-size hash difference:\n%s", term)
	}
	// Both numbers, so a reader can see which way it moved.
	for _, want := range []string{itoa(int64(len(body))), itoa(int64(len(body)) - 7)} {
		if !strings.Contains(term, want) {
			t.Errorf("the finding must state both sizes (missing %s):\n%s", want, term)
		}
	}
}

// TestReconcileIsQuietWhenTheInstallerBytesAreUnchanged: the warm, ordinary case.
func TestReconcileIsQuietWhenTheInstallerBytesAreUnchanged(t *testing.T) {
	home, ws, packRoot := reconcileHome(t)
	bin := filepath.Join(home, ".local", "bin", "probetool")
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	body := []byte("VERSION-ONE")
	if err := os.WriteFile(bin, body, 0o755); err != nil {
		t.Fatal(err)
	}
	sum, err := fileSHA256Of(t, body)
	if err != nil {
		t.Fatal(err)
	}
	writeReceipts(t, ws, installerReceipt(t, bin, sum, int64(len(body))))

	if term, _ := runReconcile(t, map[string]string{
		"JAIL_HOME": home, "YOLO_WORKSPACE": ws, "YOLO_PACK_ROOT": packRoot,
	}); term != "" {
		t.Errorf("unchanged bytes must be silent, got:\n%s", term)
	}
}

// fileSHA256Of digests body by writing it and calling the production digest, so the test's
// expectation and the implementation cannot disagree about the encoding.
func fileSHA256Of(t *testing.T, body []byte) (string, error) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(p, body, 0o644); err != nil {
		return "", err
	}
	return fileSHA256(p)
}

// --- the LSP sentinel generalisation ---------------------------------------------------

// TestReconcileGeneralisesTheLSPSentinel is §10 step two's HEADLINE CLAIM and a live defect
// in this jail.
//
// The sentinel is "the only install/uninstall reconciliation loop in the system, and it is
// one field short of being a receipt" (§4.3). What it cannot do is notice that its own record
// and the disk disagree — and MEASURED 2026-09-02, ~/.yolo-installed-lsps here is ONE BYTE (a
// newline) while pyright, typescript and typescript-language-server are all installed. The
// uninstall loop is keyed on that record, so those three can never be removed by it.
//
// Both directions, because they are different defects with the same cause:
//
//	recorded, not installed  → the uninstall loop would remove nothing
//	installed + declared, not recorded → dropping the declaration will not uninstall it
func TestReconcileGeneralisesTheLSPSentinel(t *testing.T) {
	home, ws, packRoot := reconcileHome(t)
	// Installed and declared, but the sentinel lost its record — the live shape.
	seedNpmVersion(t, home, "pyright", "1.1.0")
	// Recorded but gone: nothing under node_modules, nothing in $GOBIN.
	if err := os.WriteFile(filepath.Join(home, ".yolo-installed-lsps"),
		[]byte("npm:bash-language-server\ngo:github.com/x/vanished@v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	term, _ := runReconcile(t, map[string]string{
		"JAIL_HOME": home, "YOLO_WORKSPACE": ws, "YOLO_PACK_ROOT": packRoot,
		"YOLO_LSP_NPM_INSTALL": "pyright\n",
	})

	if !strings.Contains(term, "npm:pyright") || !strings.Contains(term, "absent from the LSP") {
		t.Errorf("an installed, declared, unrecorded LSP package must be reported — this is "+
			"the record-and-bytes divergence the whole design exists to close:\n%s", term)
	}
	for _, gone := range []string{"npm:bash-language-server", "go:github.com/x/vanished@v1"} {
		if !strings.Contains(term, gone) {
			t.Errorf("a sentinel entry with nothing on disk must be reported (%s):\n%s", gone, term)
		}
	}
}

// TestReconcileSentinelIsQuietWhenTheRecordMatchesTheDisk is the negative: a healthy sentinel
// is the common case, and a report that fired anyway would put three lines on every launch of
// every jail with LSP servers configured.
func TestReconcileSentinelIsQuietWhenTheRecordMatchesTheDisk(t *testing.T) {
	home, ws, packRoot := reconcileHome(t)
	seedNpmVersion(t, home, "pyright", "1.1.0")
	gobin := filepath.Join(home, "go", "bin")
	if err := os.MkdirAll(gobin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gobin, "gopls"), []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".yolo-installed-lsps"),
		[]byte("npm:pyright\ngo:golang.org/x/tools/gopls@latest\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if term, _ := runReconcile(t, map[string]string{
		"JAIL_HOME": home, "YOLO_WORKSPACE": ws, "YOLO_PACK_ROOT": packRoot,
		"YOLO_LSP_NPM_INSTALL": "pyright\n",
		"YOLO_LSP_GO_INSTALL":  "golang.org/x/tools/gopls@latest\n",
	}); term != "" {
		t.Errorf("a sentinel that agrees with the disk must be silent, got:\n%s", term)
	}
}

// TestReconcileSentinelSparesAnUninstalledDeclaration: a server this boot is about to
// install — declared, unrecorded and ABSENT — is the cold path, not a divergence. The
// bootstrap two steps from here installs it and writes the record.
func TestReconcileSentinelSparesAnUninstalledDeclaration(t *testing.T) {
	home, ws, packRoot := reconcileHome(t)

	if term, _ := runReconcile(t, map[string]string{
		"JAIL_HOME": home, "YOLO_WORKSPACE": ws, "YOLO_PACK_ROOT": packRoot,
		"YOLO_LSP_NPM_INSTALL": "pyright\n",
		"YOLO_LSP_GO_INSTALL":  "golang.org/x/tools/gopls@latest\n",
	}); term != "" {
		t.Errorf("a cold home about to install its declared servers is not a divergence:\n%s",
			term)
	}
}

// TestReconcileSentinelToleratesAnUnknownKind: a future sentinel kind this build does not know
// is version skew, and reporting "not installed" for it would turn a newer host's record into
// a wrong finding on every boot — the same tolerance `via` was given in §6.2.
func TestReconcileSentinelToleratesAnUnknownKind(t *testing.T) {
	home, ws, packRoot := reconcileHome(t)
	if err := os.WriteFile(filepath.Join(home, ".yolo-installed-lsps"),
		[]byte("uv:some-tool\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if term, _ := runReconcile(t, map[string]string{
		"JAIL_HOME": home, "YOLO_WORKSPACE": ws, "YOLO_PACK_ROOT": packRoot,
	}); term != "" {
		t.Errorf("a kind this build cannot probe must produce no finding:\n%s", term)
	}
}

// --- silence, gating and the malformed line ------------------------------------------

// TestReconcileIsSilentWithoutAStagedPackTree inherits catalog.go's gate and its reason:
// without a staged pack tree every declared-set input reads empty for a reason that has
// nothing to do with what is installed. It is also what keeps this out of macos-user's boot
// by construction (catalog.go:16-21), where there is no pack tree at all.
func TestReconcileIsSilentWithoutAStagedPackTree(t *testing.T) {
	home, ws, _ := reconcileHome(t)
	seedNpmVersion(t, home, "tool", "9.9.9")
	writeReceipts(t, ws, `{"schema":1,"kind":"npm","bin":"tool","declared":"tool",`+
		`"resolved":"1.0.0","act":"install","time":"2026-08-24T10:00:00Z"}`)
	if err := os.WriteFile(filepath.Join(home, ".yolo-installed-lsps"),
		[]byte("npm:vanished\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	term, logOnly := runReconcile(t, map[string]string{"JAIL_HOME": home, "YOLO_WORKSPACE": ws})
	if term != "" || logOnly != "" {
		t.Errorf("no pack root means no declared set and therefore no report, got:\n%s\n%s",
			term, logOnly)
	}
}

// TestReconcileIsSilentWithoutAReceiptsFile: an ABSENT receipts file is the NORMAL state.
// MEASURED 2026-09-02: no receipts.jsonl exists anywhere on this machine, because every
// install site sits behind a cold `if [ ! -x "$REAL_BIN" ]` branch and every home here is
// warm. A line on every launch of every existing jail is noise on exactly the surface
// catalogSize's comment warns about.
//
// An EMPTY file gets a boot-log note and nothing else: "the file exists and says nothing" is
// a fact a reader of the log wants and a user watching a healthy launch does not.
func TestReconcileIsSilentWithoutAReceiptsFile(t *testing.T) {
	home, ws, packRoot := reconcileHome(t)
	vars := map[string]string{
		"JAIL_HOME": home, "YOLO_WORKSPACE": ws, "YOLO_PACK_ROOT": packRoot,
	}

	term, logOnly := runReconcile(t, vars)
	if term != "" || logOnly != "" {
		t.Errorf("an absent receipts file is the normal state and must produce nothing at "+
			"all, got terminal:\n%s\nlog:\n%s", term, logOnly)
	}

	// Now an empty one: log only.
	writeReceipts(t, ws)
	term, logOnly = runReconcile(t, vars)
	if term != "" {
		t.Errorf("an empty receipts log must not reach the terminal:\n%s", term)
	}
	if !strings.Contains(logOnly, "no usable entries") {
		t.Errorf("an empty receipts log is worth a boot-log note:\n%s", logOnly)
	}
}

// TestReconcileSkipsAndNamesAMalformedLine: a jail must not refuse to boot over a malformed
// observation log. The file is appended to by shell from four concurrent launchers, it lives
// in the user's workspace where anything may edit it, and nothing downstream of it gates — so
// a parse error is a finding ABOUT the log, reported the way a finding about a package is.
//
// And the surviving lines must still be compared: a reader that gave up at the first bad line
// would lose every receipt after it, silently.
func TestReconcileSkipsAndNamesAMalformedLine(t *testing.T) {
	home, ws, packRoot := reconcileHome(t)
	seedNpmVersion(t, home, "tool", "3.0.0")
	writeReceipts(t, ws,
		`{"schema":1,"kind":"npm","bin":"trunc"`, // a half-written line
		`not json at all`,
		`{"schema":1,"nokind":"x"}`, // parses, but is not a receipt
		`{"schema":1,"kind":"npm","bin":"tool","declared":"tool","resolved":"1.0.0",`+
			`"act":"install","time":"2026-08-24T10:00:00Z"}`)

	term, _ := runReconcile(t, map[string]string{
		"JAIL_HOME": home, "YOLO_WORKSPACE": ws, "YOLO_PACK_ROOT": packRoot,
	})

	skips := 0
	for _, line := range strings.Split(strings.TrimSpace(term), "\n") {
		if strings.Contains(line, "unparseable receipt line") {
			skips++
			if !strings.Contains(line, "receipts.jsonl line ") {
				t.Errorf("a skipped line must be NAMED so a reader can go find it: %q", line)
			}
		}
	}
	if skips != 3 {
		t.Errorf("want one report per unparseable line, got %d:\n%s", skips, term)
	}
	// The good line after them was still compared.
	if !strings.Contains(term, "1.0.0") || !strings.Contains(term, "3.0.0") {
		t.Errorf("a bad line must not stop the reader: the receipts after it still describe "+
			"real bytes:\n%s", term)
	}
}

// TestReconcileLinesReadAsAReport: these land in the boot log beside `requires` warnings,
// pack-skew notices and the catalog's own lines, so a reader has to be able to tell at a
// glance that they are one report rather than a boot problem.
func TestReconcileLinesReadAsAReport(t *testing.T) {
	home, ws, packRoot := reconcileHome(t)
	seedNpmVersion(t, home, "tool", "3.0.0")
	writeReceipts(t, ws, `{"schema":1,"kind":"npm","bin":"tool","declared":"tool",`+
		`"resolved":"1.0.0","act":"install","time":"2026-08-24T10:00:00Z"}`)

	term, _ := runReconcile(t, map[string]string{
		"JAIL_HOME": home, "YOLO_WORKSPACE": ws, "YOLO_PACK_ROOT": packRoot,
	})
	lines := strings.Split(strings.TrimSpace(term), "\n")
	if len(lines) != 1 {
		t.Fatalf("want one line per finding, got %d:\n%s", len(lines), term)
	}
	for _, line := range lines {
		if !strings.HasPrefix(line, reconcilePrefix) {
			t.Errorf("every line must be prefixed so the report reads as one thing: %q", line)
		}
	}
	// The prefix must not be the catalog's: the two say different things about the same
	// bytes ("nothing declares this" vs "the record is wrong about this").
	if strings.Contains(term, catalogPrefix) {
		t.Errorf("the reconcile must not wear the catalog's prefix:\n%s", term)
	}
}

// TestReconcileTouchesNothing makes the report-only ruling STRUCTURAL rather than documented.
//
// "Reconcile reports; it does not install" (A4 as ruled, §5.4) and OQ-PD7's "report first;
// gate later" are both claims about what this must NOT do, and a reconcile that quietly
// rewrote a sentinel, refreshed a receipt or pruned a package would be indistinguishable from
// a working one until the day it removed something a user wanted. Same shape as
// TestCatalogTouchesNothing, which is the precedent this is modelled on.
func TestReconcileTouchesNothing(t *testing.T) {
	home, ws, packRoot := reconcileHome(t)
	seedNpmVersion(t, home, "tool", "3.0.0")
	seedNpmVersion(t, home, "pyright", "1.1.0")
	bin := filepath.Join(home, ".local", "bin", "probetool")
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, []byte("VERSION-TWO"), 0o755); err != nil {
		t.Fatal(err)
	}
	sum, err := fileSHA256Of(t, []byte("VERSION-ONE"))
	if err != nil {
		t.Fatal(err)
	}
	writeReceipts(t, ws,
		`{"schema":1,"kind":"npm","bin":"tool","declared":"tool","resolved":"1.0.0",`+
			`"act":"install","time":"2026-08-24T10:00:00Z"}`,
		`bad line`,
		installerReceipt(t, bin, sum, int64(len("VERSION-ONE"))))
	if err := os.WriteFile(filepath.Join(home, ".yolo-installed-lsps"),
		[]byte("npm:vanished\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// The workspace holds the receipts log, so both trees are snapshotted.
	beforeHome, beforeWS := treeSnapshot(t, home), treeSnapshot(t, ws)
	term, _ := runReconcile(t, map[string]string{
		"JAIL_HOME": home, "YOLO_WORKSPACE": ws, "YOLO_PACK_ROOT": packRoot,
	})
	if term == "" {
		t.Fatal("this fixture must produce findings, or the snapshot proves nothing about a " +
			"reconcile that ran")
	}
	if after := treeSnapshot(t, home); after != beforeHome {
		t.Errorf("the reconcile changed the home tree.\nbefore:\n%s\nafter:\n%s", beforeHome, after)
	}
	if after := treeSnapshot(t, ws); after != beforeWS {
		t.Errorf("the reconcile changed the workspace tree — the receipts log is an "+
			"APPEND-ONLY observation log and a reader must never rewrite it.\nbefore:\n%s\n"+
			"after:\n%s", beforeWS, after)
	}
}

// --- the writer/reader round trip -----------------------------------------------------

// TestReceiptRoundTripThroughAGeneratedLauncher is what keeps the reader from drifting away
// from the writer.
//
// "Two implementations of one client is the drift the transport unification exists to end"
// (AGENTS.md, about the generated in-jail clients) — and a receipts schema with a Go writer
// and a Go reader is the same exposure. A hand-written fixture would pin this file against
// itself: the reader would keep parsing whatever the test author remembered the writer doing.
//
// So this GENERATES a launcher through GenerateAgentLaunchers (the production call site, so
// the receipts path is the one the boot bakes), RUNS it against fakes the way
// receipts_test.go's npmProbe does, and parses the file THAT launcher wrote.
func TestReceiptRoundTripThroughAGeneratedLauncher(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not found")
	}
	home := t.TempDir()
	ws := filepath.Join(home, "ws")
	packRoot := t.TempDir()
	packDir := filepath.Join(packRoot, "toolpack")
	if err := os.MkdirAll(packDir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name":"toolpack","contributes":[` +
		`{"kind":"program","bin":"tool","via":"npm","package":"@scope/tool@2.0.0"}` +
		`]}`
	if err := os.WriteFile(filepath.Join(packDir, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}

	// The fakes: an `npm install` that materializes the package.json the launcher reads, and
	// a five-line stand-in for `jq -r .version`. Same shape as newNpmProbe's.
	fakeBin := filepath.Join(home, "fakebin")
	if err := os.MkdirAll(fakeBin, 0o755); err != nil {
		t.Fatal(err)
	}
	npm := `#!/bin/bash
case "${1:-}" in
install)
    spec="${@: -1}"
    name="${spec%@*}"
    mkdir -p "$NPM_CONFIG_PREFIX/bin" "$NPM_CONFIG_PREFIX/lib/node_modules/$name"
    printf '{"version":"2.0.0"}\n' > "$NPM_CONFIG_PREFIX/lib/node_modules/$name/package.json"
    printf '#!/bin/sh\necho RAN\n' > "$NPM_CONFIG_PREFIX/bin/tool"
    chmod +x "$NPM_CONFIG_PREFIX/bin/tool"
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
	for name, body := range map[string]string{"npm": npm, "jq": jq} {
		if err := os.WriteFile(filepath.Join(fakeBin, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	vars := map[string]string{
		"JAIL_HOME": home, "YOLO_WORKSPACE": ws, "YOLO_PACK_ROOT": packRoot,
	}
	e := NewEnv(vars)
	if err := GenerateAgentLaunchers(e); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(filepath.Join(e.LaunchDir(), "tool"))
	cmd.Env = []string{"HOME=" + home, "PATH=" + fakeBin + ":/bin:/usr/bin"}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("the generated launcher must install and exec (%v):\n%s", err, out)
	}

	// The READER, against the bytes that launcher wrote — at the path the reader derives on
	// its own, which also pins that the two agree about WHERE the log is.
	recs, malformed, present := readReceiptLog(NewEnv(vars))
	if !present {
		t.Fatalf("the reader did not find the file the launcher wrote at %s",
			receiptsPathFor(NewEnv(vars)))
	}
	if len(malformed) != 0 {
		t.Fatalf("the writer's own output must parse: %v", malformed)
	}
	if len(recs) != 1 {
		t.Fatalf("want one receipt, got %d: %+v", len(recs), recs)
	}
	r := recs[0]
	for _, want := range []struct{ name, got, want string }{
		{"kind", r.Kind, "npm"},
		{"bin", r.Bin, "tool"},
		{"declared", r.Declared, "@scope/tool@2.0.0"},
		{"spec", r.Spec, "@scope/tool@2.0.0"},
		{"resolved", r.Resolved, "2.0.0"},
		{"act", r.Act, "install"},
		{"path", r.Path, filepath.Join(home, ".npm-global", "bin", "tool")},
	} {
		if want.got != want.want {
			t.Errorf("%s = %q, want %q — the reader must mirror the writer field for field",
				want.name, want.got, want.want)
		}
	}
	if r.Schema != 1 {
		t.Errorf("schema = %d, want 1", r.Schema)
	}
	if !strings.HasSuffix(r.Time, "Z") || len(r.Time) != 20 {
		t.Errorf("time = %q, want the writer's 20-char UTC stamp", r.Time)
	}
	// An npm receipt carries no digest, and the reader must say ABSENT rather than 0.
	if r.Bytes != -1 || r.SHA256 != "" {
		t.Errorf("an npm receipt has no digest; bytes must read as absent (-1), got %d/%q",
			r.Bytes, r.SHA256)
	}
	if !r.HasResolved() {
		t.Errorf("this receipt DID measure a version: %+v", r)
	}

	// ...and the reconcile built on it reports nothing, because the package on disk is the
	// version the launcher recorded. That is the round trip closed end to end: writer →
	// reader → comparison.
	if term, _ := runReconcile(t, vars); term != "" {
		t.Errorf("a jail whose disk matches its own receipt must be silent:\n%s", term)
	}
}

// TestReceiptReaderAndWriterAgreeOnThePath: the writer BAKES receiptsFile(e) into every
// generated installer, so a reader that spelled the path a second way would read an empty
// file forever and report no drift on a jail full of it.
func TestReceiptReaderAndWriterAgreeOnThePath(t *testing.T) {
	e := NewEnv(map[string]string{
		"JAIL_HOME": t.TempDir(), "YOLO_WORKSPACE": "/Users/matt/code/thing",
	})
	if got, want := receiptsPathFor(e), receiptsFile(e); got != want {
		t.Errorf("reader path %q != writer path %q", got, want)
	}
	// And the writer actually bakes that value: the reader's path is only right if it is the
	// one the generated installers append to.
	if !strings.Contains(BootstrapScript(e), "_YOLO_RECEIPTS="+receiptsPathFor(e)) {
		t.Errorf("the bootstrap must bake the path the reader reads (%s):\n%s",
			receiptsPathFor(e), BootstrapScript(e))
	}
}

// --- the CALL SITE --------------------------------------------------------------------

// TestBootReconcilesBesideTheCatalog is THE call-site test.
//
// Main cannot be called from a test — it ends in execBash, which replaces the process — so
// nothing else in this file can observe whether the boot path uses any of it. Every test
// above would pass in full against a boot.go that never calls the reconcile, which is the
// exact shape this repo has shipped five times: the callee pinned, the call site unpinned,
// the feature switchable off with the unit gate green.
//
// Pinned by reading the source, the way catalog_test.go pins the catalog's own wiring. Three
// properties, each of which a one-line move would break invisibly:
//
//   - it is called at all;
//   - it is NOT a genStep — a drifted version is not a broken generator, and genStep is FATAL
//     (A12), so routing it there would mean a jail whose vendor CLI self-updated refuses to
//     START. OQ-PD7 rules that this reports before it gates, and a fatal on day one would be
//     the gate granted without the measurement meant to justify it;
//   - it sits AFTER the catalog and BEFORE the exec. The catalog's question is "what has no
//     owner at all" and this one's is "what does the record get wrong about the things that
//     do", so the coarser finding comes first — and both must read the PREVIOUS boot's state,
//     which means above the exec that hands control away.
//
// Every landmark is located with callIndex, not strings.Index: this file's prose names the
// function and boot.go's comments do too, so a plain substring search is satisfied by a
// COMMENT — including the comment that would be left behind if the call itself were removed.
func TestBootReconcilesBesideTheCatalog(t *testing.T) {
	src, err := os.ReadFile(filepath.Join(repoRoot(t), "internal", "entrypoint", "boot.go"))
	if err != nil {
		t.Fatalf("reading boot.go: %v", err)
	}
	got := string(src)

	call := callIndex(got, "ReconcileInstalledPrograms(e)")
	if call < 0 {
		t.Fatal("boot.go never calls ReconcileInstalledPrograms — the reconcile is " +
			"unreachable, and every test in this file passes anyway")
	}
	if strings.Contains(got, `genStep(e, "reconcile`) {
		t.Error("the reconcile must not be a genStep: it generates nothing, and a fatal there " +
			"would mean a jail whose vendor CLI self-updated refuses to START")
	}
	catalog := callIndex(got, "CatalogInstalledOrphans(e)")
	execCall := callIndex(got, "return execBash(e, command)")
	if catalog < 0 || execCall < 0 {
		t.Fatalf("boot.go no longer contains the landmarks this ordering is about "+
			"(catalog=%d, exec=%d)", catalog, execCall)
	}
	if call < catalog {
		t.Error("the reconcile must run after the catalog — \"nothing owns this\" is the " +
			"coarser finding and comes before \"the record is wrong about this\"")
	}
	if call > execCall {
		t.Error("the reconcile must run before the exec that replaces this process: the state " +
			"it reads is the PREVIOUS launch's, which is the only state a receipt describes")
	}
}

// TestReconcileIsNotWiredIntoTheDarwinBootstrap: catalog.go:16-21 states the argument and it
// applies unchanged. macos-user stages no pack tree and passes no YOLO_LSP_*_INSTALL (its
// launcher builds no LSP env at all), so every declared-set input would read empty there —
// and an empty declared set turns a report into a boot that calls everything a divergence.
// A backend that cannot state what it declared must not be asked what diverged.
func TestReconcileIsNotWiredIntoTheDarwinBootstrap(t *testing.T) {
	src, err := os.ReadFile(filepath.Join(repoRoot(t), "internal", "entrypoint", "darwin.go"))
	if err != nil {
		t.Fatalf("reading darwin.go: %v", err)
	}
	if callIndex(string(src), "ReconcileInstalledPrograms(e)") >= 0 {
		t.Error("RunDarwinBootstrap must not reconcile: it stages no pack tree, so the gate " +
			"is the only thing standing between it and reporting every installed program as " +
			"a divergence (catalog.go:16-21)")
	}
}
