package entrypoint

// reconcile.go is program-delivery.md §10 STEP TWO, and only that step: "generalise the LSP
// sentinel into a reconcile. It already does install *and* uninstall against a declared set;
// what it lacks is the resolved version and a caller for anything but LSP servers. Reconcile
// compares, offline, and reports — and it inherits the 'newer version available' channel that
// OQ-PD8 found dead in the launcher. It installs nothing and removes nothing."
//
// The ruling it implements is A4's, verbatim (§5.4): "rejected as literal reinstall; adopted
// as reconcile. A launch must not depend on a registry being reachable, and an install is not
// free. But the *comparison* is free and offline — 'what the receipt says vs. what is on
// disk' — and the LSP sentinel already proves the loop can be written. Reconcile reports; it
// does not install." OQ-PD7 bounds it from the other side: "report first; gate later only if
// the reports justify it".
//
// MODELLED ON catalog.go, which is the shipped precedent for "reads state and prints what it
// found", and it inherits three of that file's decisions unchanged because the arguments
// transfer without modification:
//
//   - IT IS GATED ON YOLO_PACK_ROOT, for catalog.go's stated reason: without a staged pack
//     tree the declared-set inputs read empty for a reason that has nothing to do with what is
//     installed (an older host launcher, a backend that stages nothing), and a comparison
//     against an empty declaration is not a report — it is a list of everything.
//   - IT OBSERVES THE PREVIOUS BOOT'S STATE, deliberately. Main runs before
//     ~/.yolo-bootstrap.sh and before any lazy launcher, so what is on disk here is what the
//     LAST launch installed. That is the state a receipt describes.
//   - IT IS NOT WIRED INTO RunDarwinBootstrap. catalog.go:16-21 states the argument and it
//     applies unchanged: macos-user stages no pack tree and passes no YOLO_LSP_*_INSTALL, so
//     every declared-set input would read as empty there.
//
// AND IT IS NOT A genStep, for catalog.go's reason plus one of its own. A drifted version is
// not a broken generator: nothing was half-written, and genStep is FATAL (A12) — routing this
// through it would mean a jail whose claude self-updated refuses to START. The one of its own
// is R1/OQ-PD7: this is the report that has to earn a gate before it becomes one, and a fatal
// on day one would be the gate, granted without the measurement that was supposed to justify
// it.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// reconcilePrefix heads every line, the way catalogPrefix does and for the same reason: these
// land in the boot log beside `requires` warnings and pack-skew notices, so a reader has to be
// able to tell at a glance that they are one report rather than a boot problem.
const reconcilePrefix = "boot reconcile: "

// ReconcileInstalledPrograms compares, offline, what the receipts say a jail got against what
// is on disk, and reports the differences. It installs nothing, removes nothing, and writes
// nothing.
//
// Three comparisons, which are the three the record can actually make today:
//
//  1. an npm receipt's `resolved` version against the version in the installed package's
//     package.json;
//  2. an installer receipt's (sha256, bytes) against a re-measure of its landing path — the
//     drift a VENDOR SELF-UPDATER leaves, which the launcher deliberately records nothing
//     about ("$REAL_BIN install" emits no receipt: what moved, to what and where are all the
//     vendor's decisions, "and the drift it leaves is the RECONCILE's to report, against the
//     bytes on disk rather than against a claim" — shims.go's own comment, §6.3);
//  3. the LSP SENTINEL against what is actually on disk. This is the step's headline claim —
//     the sentinel is "the only install/uninstall reconciliation loop in the system, and it is
//     one field short of being a receipt" (§4.3) — and it is a LIVE DEFECT in this jail:
//     MEASURED 2026-09-02, ~/.yolo-installed-lsps is one byte (a newline) while three npm LSP
//     packages from a since-unconfigured `lsp_servers` are still installed, so the uninstall
//     loop will never remove them (§10 step four found the same three as orphans, "their
//     sentinel record lost").
//
// SILENCE IS THE COMMON CASE and is deliberate on both surfaces. An absent receipts file
// produces nothing at all — that is the NORMAL state, since every install site sits behind a
// cold `if [ ! -x "$REAL_BIN" ]` branch and a warm home writes no receipt. An EMPTY one gets a
// boot-log note (e.note: log only, never the terminal), because "the file exists and says
// nothing" is a fact a reader of the log wants and a user watching a healthy launch does not.
// A line on every launch of every existing jail would be noise on exactly the surface
// catalogSize's comment warns about: a column of identical lines is what trains a reader to
// skim the one that matters.
func ReconcileInstalledPrograms(e *Env) {
	r := ReconcileInstalled(e)
	for _, m := range r.Malformed {
		// warnOnce, not warn: the receipts log is read once today, but env.go:119 makes the
		// dedupe a property of the SINK precisely so a second reader cannot reintroduce the
		// five-identical-warnings shape the pack tree produced.
		e.warnOnce(reconcilePrefix + "skipped an unparseable receipt line — " + m)
	}
	if r.ReceiptsPresent && r.Receipts == 0 {
		e.note(reconcilePrefix + "receipts log has no usable entries; nothing to compare")
	}
	for _, f := range r.Findings {
		e.warnOnce(reconcilePrefix + f)
	}
}

// ReconcileReport is one reconcile, as data. It exists because the comparison has a SECOND
// caller now — `yolo programs ls`, the on-demand spelling of this same offline read — and the
// boot's e.warn/e.note sinks are not available to a CLI that renders through richtext and has
// to answer with an exit code. Two implementations of "what does the record get wrong" is
// the drift this repo keeps deleting; one function and two renderers is not.
type ReconcileReport struct {
	// Findings are the differences, already sorted, without the boot's prefix.
	Findings []string
	// Malformed names each receipt line that would not parse. A finding ABOUT the log
	// rather than about a program, which is why it is a separate field: the CLI reports it
	// in its own section and the boot warns it above the rest.
	Malformed []string
	// ReceiptsPresent is whether the receipts file exists at all. FALSE IS THE NORMAL
	// STATE — every install site sits behind a cold branch — and it is distinct from a
	// file that exists and says nothing, which is the only one of the two worth a note.
	ReceiptsPresent bool
	// Receipts is how many lines parsed.
	Receipts int
}

// ReconcileInstalled runs the comparison and returns it. It reads files and nothing else: no
// subprocess, no registry, no network. See the file header for the three comparisons and for
// why an unknown value is never compared.
func ReconcileInstalled(e *Env) ReconcileReport {
	if e.Getenv("YOLO_PACK_ROOT") == "" {
		return ReconcileReport{}
	}
	recs, malformed, present := readReceiptLog(e)
	out := ReconcileReport{Malformed: malformed, ReceiptsPresent: present, Receipts: len(recs)}
	out.Findings = append(out.Findings, reconcileReceiptFindings(e, latestReceipts(recs))...)
	out.Findings = append(out.Findings, reconcileSentinelFindings(e)...)
	return out
}

// reconcileReceiptFindings walks the latest receipt per program and returns one line per
// difference it can state, sorted so the report is stable across boots.
//
// A receipt with nothing comparable produces NOTHING, and that is the OQ-PD8 half of the
// step: an omitted `resolved` is the writer saying it could not measure the version (a run
// with no jq on PATH — see receipt.HasResolved), so treating it as a value would manufacture
// a mismatch out of the writer's honesty. shims.go:592-610 is explicit that copying
// `_installed_version`'s `|| echo 0` into a receipt would be a forgery; reading an absent
// field as "0" is the same forgery from the other end.
func reconcileReceiptFindings(e *Env, latest map[receiptKey]receipt) []string {
	var out []string
	for _, r := range latest {
		switch r.Kind {
		case "npm", "lsp-npm", "mcp-npm":
			if f := reconcileNpmVersion(e, r); f != "" {
				out = append(out, f)
			}
		case "installer":
			if f := reconcileInstallerDigest(r); f != "" {
				out = append(out, f)
			}
		}
	}
	sort.Strings(out)
	return out
}

// reconcileNpmVersion compares the version a receipt recorded against the one in the
// installed package's package.json, and returns "" when the two agree or when either side is
// unknown.
//
// The package NAME is what indexes node_modules, which is why the receipt's `declared` is
// split the way the launcher splits it: a declaration carrying a selector (`foo@1.2.3`) names
// a directory that does not exist. Same split, same reason as catalogNpmOrphans.
//
// A package that is GONE is not reported here. That is the catalog's opposite question and
// `requires` already answers the missing-declared-binary one; a reconcile finding of "the
// receipt names a package that is not installed" on a jail whose workspace dropped the pack
// would be a third voice saying what those two say better.
//
// IT NAMES THE ACT, which is the half of OQ-PD8 this venue can actually inherit. The
// launcher's own informational poll ends "Run 'yolo pack update' to install it" and says why:
// "a report the reader cannot act on is worse than the reinstall it replaces"
// (npmLauncherTemplate). That poll is dead in steady state — the stamp is machine-global and
// nineteen days of launches never moved it — so the boot is where the sentence has to be said
// now. What does NOT move here is the poll's registry question: this comparison is offline by
// ruling, so "a newer version exists upstream" remains the update verb's to answer, and this
// line claims only what a local file read can support.
func reconcileNpmVersion(e *Env, r receipt) string {
	if !r.HasResolved() {
		return ""
	}
	name, _ := splitNpmSpec(receiptPackageName(r))
	if name == "" {
		return ""
	}
	onDisk, ok := installedNpmVersion(e, name)
	if !ok || onDisk == r.Resolved {
		return ""
	}
	return name + ": receipt says " + r.Resolved + ", installed is " + onDisk +
		" (recorded " + r.Time + ") — run 'yolo pack update' to reassert the declaration"
}

// receiptPackageName is the npm identity a receipt carries. `declared` is the pack's
// declaration verbatim and is what the two LIST arms write; the launcher funnels write it too
// and also carry `spec`. Preferring `declared` keeps one rule for all three kinds.
func receiptPackageName(r receipt) string {
	if r.Declared != "" {
		return r.Declared
	}
	return r.Spec
}

// installedNpmVersion reads `version` out of a globally installed package's package.json.
//
// encoding/json, NEVER a shell-out to `jq`. The shell side uses jq because it is a shell; the
// entrypoint is Go, and spawning jq here would import the exact hazard
// _resolved_version's comment documents — macos-user execs its launchers under `env -i`
// against whatever the host has, and jq is not a macOS builtin. In the reader that failure is
// worse than in the writer: the writer omits a field, this would silently report no drift.
func installedNpmVersion(e *Env, name string) (string, bool) {
	data, err := os.ReadFile(filepath.Join(e.NpmPrefix, "lib", "node_modules", name, "package.json"))
	if err != nil {
		return "", false
	}
	var pkg struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil || pkg.Version == "" {
		return "", false
	}
	return pkg.Version, true
}

// reconcileInstallerDigest re-measures an installer receipt's landing path and reports when
// the bytes are not the ones the receipt names.
//
// SIZE FIRST, DIGEST ONLY IF THE SIZE AGREES, and the reason is measured: ~/.local/bin/agy is
// 181 MB in this jail and claude keeps just over 1 GB of builds per workspace (§5.3). This
// runs on the boot path, before the exec that hands control to the agent, so hashing every
// installer-managed binary on every launch would be a per-boot cost paid to answer a question
// a stat already answers whenever the answer is "different". A size change IS drift and needs
// no hash to establish it; only equal sizes leave the question open.
//
// A receipt with no digest produces nothing: `sha256` is written only when one of the three
// digest spellings answered, and an absent one is unknown rather than zero — the same rule
// `resolved` follows.
func reconcileInstallerDigest(r receipt) string {
	if r.Path == "" || r.SHA256 == "" {
		return ""
	}
	fi, err := os.Stat(r.Path)
	if err != nil || !fi.Mode().IsRegular() {
		// Absent or not a file: not this report's finding. An installer's program going
		// missing is the catalog's and `requires`' question, and a self-updater that
		// replaced the file with a symlink is a shape this comparison cannot speak about.
		return ""
	}
	if r.Bytes >= 0 && fi.Size() != r.Bytes {
		return r.Path + ": receipt records " + itoa(r.Bytes) + " bytes, on disk is " +
			itoa(fi.Size()) + " — the vendor's own updater leaves no receipt (recorded " +
			r.Time + ")"
	}
	sum, err := fileSHA256(r.Path)
	if err != nil || sum == r.SHA256 {
		return ""
	}
	return r.Path + ": receipt records sha256 " + shortDigest(r.SHA256) + ", on disk is " +
		shortDigest(sum) + " — same size, different bytes (recorded " + r.Time + ")"
}

// fileSHA256 digests a file in a streaming read, so a 181 MB binary does not become 181 MB of
// resident memory on the boot path.
func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// shortDigest renders the first 12 hex chars, which is what a reader compares by eye. The
// full digest is in the receipt for anyone who needs it, and two 64-char strings on one line
// is a line nobody reads.
func shortDigest(d string) string {
	if len(d) > 12 {
		return d[:12] + "…"
	}
	return d
}

// itoa is strconv.Itoa for an int64 without the import, kept trivial because it is only ever
// handed a file size.
func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// reconcileSentinelFindings is the GENERALISATION §10 step two names: the LSP sentinel already
// does install and uninstall against a declared set, so the thing it cannot do is notice that
// its own record and the disk disagree.
//
// It compares ~/.yolo-installed-lsps — what the LAST bootstrap says it installed — against
// what is actually there, both ways:
//
//   - a sentinel entry with nothing on disk: the record claims an install that is gone, so the
//     uninstall loop's `npm uninstall -g` / `rm -f "$GOBIN/$bin"` would be a no-op and nothing
//     would say why;
//   - something on disk that THIS LAUNCH declares and the sentinel does not: the record is
//     short, so when the declaration is dropped the uninstall loop will not see the entry and
//     will leave the package installed forever. THAT IS THE LIVE DEFECT IN THIS JAIL — the
//     sentinel is one byte while pyright, typescript and typescript-language-server are
//     installed (§10 step four measured the same three from the other side, as orphans).
//
// The catalog names those three as ORPHANS, which is a different sentence about the same
// bytes: the catalog says "nothing declares this", and this says "the record that is supposed
// to be able to remove it does not know about it". A jail where `lsp_servers` still declares
// them gets the second finding and not the first, which is exactly the case the catalog cannot
// see.
func reconcileSentinelFindings(e *Env) []string {
	recorded := map[string]struct{}{}
	for _, entry := range readLSPSentinel(e) {
		recorded[entry] = struct{}{}
	}

	var out []string
	// Half one: the record claims something that is not there.
	for entry := range recorded {
		kind, id, ok := strings.Cut(entry, ":")
		if !ok || id == "" {
			continue
		}
		if !lspEntryOnDisk(e, kind, id) {
			out = append(out, "LSP sentinel records "+entry+" but it is not installed — the "+
				"uninstall loop keyed on this record would remove nothing")
		}
	}
	// Half two: it is there, this launch declares it, and the record is silent — so a later
	// launch that drops the declaration cannot uninstall it.
	for _, entry := range declaredLSPEntries(e) {
		if _, ok := recorded[entry]; ok {
			continue
		}
		kind, id, _ := strings.Cut(entry, ":")
		if !lspEntryOnDisk(e, kind, id) {
			// Declared, unrecorded and absent: this boot's bootstrap is about to install it
			// and write the record. Nothing to report.
			continue
		}
		out = append(out, entry+" is installed and declared but absent from the LSP "+
			"sentinel — dropping the declaration will not uninstall it")
	}
	sort.Strings(out)
	return out
}

// declaredLSPEntries renders THIS launch's YOLO_LSP_*_INSTALL lists in the sentinel's own
// `kind:identifier` vocabulary, so the two are comparable line for line.
//
// The spelling is the bootstrap's (`npm:${pkg}`, `go:${pkg}`, shell.go's desired-set loop),
// and it has to stay the bootstrap's: the uninstall loop matches those lines EXACTLY
// (`grep -qxF`), so a reconcile that normalized them would report drift against a record that
// is perfectly self-consistent.
func declaredLSPEntries(e *Env) []string {
	var out []string
	for _, pkg := range splitLSPInstallList(e.Getenv("YOLO_LSP_NPM_INSTALL")) {
		out = append(out, "npm:"+pkg)
	}
	for _, pkg := range splitLSPInstallList(e.Getenv("YOLO_LSP_GO_INSTALL")) {
		out = append(out, "go:"+pkg)
	}
	return out
}

// lspEntryOnDisk answers, offline, whether a sentinel entry's program is present — probed the
// way the BOOTSTRAP probes it, because a disagreement between the two would make this report
// about the probe rather than about the jail.
//
// npm: the package DIRECTORY under the global prefix. The bootstrap uses `npm ls -g
// --depth=0 "$pkg"`, which is a subprocess this must not spawn (the boot path is not a place
// to fork npm, and macos-user has no guaranteed npm at all) — but both questions reduce to
// "does node_modules/<name> exist", which is also what installedNpmPackages walks.
//
// go: `$GOBIN/<bin>`, with the bin name derived from the module path exactly as shell.go
// derives it (`base=${pkg%@*}; bin=${base##*/}`, goModuleBinName) — the same reduction the
// uninstall loop's `rm -f "$GOBIN/$bin"` uses, so "on disk" here means the file that loop
// would remove.
//
// An unknown kind reads as PRESENT, which is the quiet answer: a future sentinel kind this
// build does not know is version skew, and reporting "not installed" for it would turn a newer
// host's record into a wrong finding on every boot.
func lspEntryOnDisk(e *Env, kind, id string) bool {
	switch kind {
	case "npm":
		name, _ := splitNpmSpec(id)
		if name == "" {
			return true
		}
		fi, err := os.Stat(filepath.Join(e.NpmPrefix, "lib", "node_modules", name))
		return err == nil && fi.IsDir()
	case "go":
		bin := goModuleBinName(id)
		if bin == "" {
			return true
		}
		_, err := os.Stat(filepath.Join(e.GoBin(), bin))
		return err == nil
	default:
		return true
	}
}
