package entrypoint

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// resetAnchorDir prepares a generated-script directory for a fresh boot: create it if
// absent, then clear its CONTENTS — never the dir itself.
//
// Both generated dirs (~/.yolo/bin/block and ~/.yolo/bin/launch) live under ONE bind-mount
// ANCHOR, mounted from <ws>/.yolo/home/yolo-bin while their parent /home/agent
// is mounted read-only. os.RemoveAll(dir) tries to unlink the anchor top-down, fails EROFS
// on the read-only parent, and leaves every stale child in place — so unblocking a tool
// (dropping curl from blocked_tools) or dropping a pack never took effect on the next
// fresh launch. ClearContents recurses into the children so stale entries ARE removed,
// matching the mount-anchor invariant codified in fsx.go. MkdirAll first so a first-ever
// run (no dir yet, e.g. macos-user) still gets an empty dir.
func resetAnchorDir(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return ClearContents(dir)
}

// GenerateShims clears the contents of SHIM_DIR and writes one
// blocking/filtering shim per entry in YOLO_BLOCK_CONFIG.
// An absent/empty/unparseable config leaves an empty SHIM_DIR.
// The shim body is the frozen argv-filter contract: message/suggestion text +
// exit code 127. See ShimContent for the exact grammar.
//
// BlockDir (~/.yolo/bin/block) is the BLOCKER dir and is ordered FIRST on PATH. The lazy
// installers live in ~/.yolo/bin/launch, ordered LAST — see Env.LaunchDir.
func GenerateShims(e *Env) error {
	if err := resetAnchorDir(e.BlockDir()); err != nil {
		return err
	}

	blockJSON := e.Getenv("YOLO_BLOCK_CONFIG")
	if blockJSON == "" {
		return nil
	}
	decoded, err := jsonx.Decode([]byte(blockJSON))
	if err != nil {
		// Unparseable config -> no shims.
		return nil
	}
	config, ok := decoded.([]any)
	if !ok {
		// A non-list config never occurs in real YOLO_BLOCK_CONFIG (always a
		// JSON array), so we decline to act on it.
		return nil
	}

	for _, item := range config {
		cfg, ok := item.(*jsonx.OrderedMap)
		if !ok {
			// Non-object entry: real configs are arrays of objects; skip
			// defensively.
			continue
		}
		name, ok := stringValue(cfg, "name")
		if !ok || name == "" {
			continue // a nameless entry has no shim to write
		}
		if !packdecl.ValidBinName(name) {
			// The shim is FILED at filepath.Join(BlockDir, name), and this list arrives
			// from the assembled config whose workspace half is agent-editable — a name
			// carrying ".." would write an executable outside the anchor into the jail's
			// persistent home. ValidateConfig refuses it upstream; this is the
			// writer-side half.
			continue
		}
		// Default message when the entry supplies none.
		msg := "Error: tool " + name + " is blocked in this project."
		if v, present := cfg.Get("message"); present {
			if s, isStr := v.(string); isStr {
				msg = s
			}
		}
		sug := ""
		if v, present := cfg.Get("suggestion"); present {
			if s, isStr := v.(string); isStr {
				sug = s
			}
		}
		// A BLOCKER WHOSE REPLACEMENT IS ABSENT IS JUST BREAKAGE. The default
		// entries block `grep -r` and `find` and tell the agent to use `rg` and
		// `fd` — which is sound advice on the container backends, where the image
		// BAKES both, and false on macos-user, which bakes nothing: there the tool
		// comes from `packages:` or not at all. Measured 2026-09-04 on a real Mac
		// launch whose `packages:` held only `just` and `fzf`: the shims were
		// generated, `grep -r` exited 127, and the suggestion named a binary that
		// did not exist. That is worse than not blocking — the agent loses the
		// capability AND is sent somewhere empty.
		//
		// So a blocker declaring a `replacement` is generated only when that binary
		// is on the PATH the agent will actually have. An entry with no
		// `replacement` always generates, which is every custom entry a user has
		// ever written: the gate is opt-in by declaration, so no existing config
		// changes behaviour.
		if repl, present := stringValue(cfg, "replacement"); present && repl != "" &&
			lookPathIn(agentPath(e), repl) == "" {
			e.warn("not blocking " + name + ": its replacement " + repl +
				" is not on PATH in this jail, and a block with no working alternative " +
				"removes the capability instead of redirecting it (add " + repl +
				" to `packages` to restore the block)")
			continue
		}
		realBin := ""
		if name == "grep" || name == "find" {
			realBin = e.ShimBinPath() + "/" + name
		}
		blockFlags := stringList(cfg, "block_flags")
		allowFlags := stringList(cfg, "allow_flags")

		content := ShimContent(msg, sug, realBin, blockFlags, allowFlags)
		shimPath := filepath.Join(e.BlockDir(), name)
		if err := writeExecutable(shimPath, content); err != nil {
			return err
		}
	}
	return nil
}

// ShimContent renders the shim script body. Two flavors:
//   - Filter shim (blockFlags non-empty AND realBin set): inspect argv against
//     the glob patterns and only exit 127 when one matches, else exec the real
//     binary. Long-option exact matches (--foo) come first, then a `--*` skip
//     so unrelated long options pass, then the short patterns.
//   - Unconditional block: exit 127 with the message (and exec realBin after,
//     only if realBin is set).
//
// msg/sug are embedded verbatim inside `echo "..."` — no shell escaping (the
// frozen contract).
func ShimContent(msg, sug, realBin string, blockFlags, allowFlags []string) string {
	var lines []string
	// ALLOW WINS OVER BLOCK, and it is scanned FIRST because a single allowed flag
	// exempts the whole invocation — `find -maxdepth 1 …` is one command, not a
	// sequence of args each judged alone. Scanning for it up front also means the
	// block loop below never has to know about exceptions.
	//
	// EXTENSION POINT, WIRED AND UNUSED (2026-09-04). No shipped entry sets
	// allow_flags; it exists because the shape of the next rule is already visible —
	// "block find UNLESS it carries a depth limit" — and `block_flags` alone cannot
	// express it, since it matches on PRESENCE and there is no negated form. Adding
	// the mechanism now and changing no behaviour is deliberate: the refactor that
	// moved these rules into a pack is not the place to also change which rules there
	// are (extension-point-principle.md — design the extension point when you build
	// the first instance, wire the one edge you need).
	allowScan := func() []string {
		if len(allowFlags) == 0 {
			return nil
		}
		return []string{
			`  for arg in "$@"; do`,
			`    case "$arg" in`,
			"      " + strings.Join(allowFlags, "|") + ") exec " + realBin + ` "$@" ;;`,
			"    esac",
			"  done",
		}
	}
	if len(blockFlags) > 0 && realBin != "" {
		var longExact, shortPatterns []string
		for _, p := range blockFlags {
			if strings.HasPrefix(p, "--") {
				longExact = append(longExact, p)
			} else {
				shortPatterns = append(shortPatterns, p)
			}
		}
		lines = append(lines, "#!/bin/sh")
		lines = append(lines, `if [ -z "$YOLO_BYPASS_SHIMS" ]; then`)
		lines = append(lines, allowScan()...)
		lines = append(lines, `  for arg in "$@"; do`)
		lines = append(lines, `    case "$arg" in`)
		if len(longExact) > 0 {
			lines = append(lines, "      "+strings.Join(longExact, "|")+")")
			lines = append(lines, `        echo "`+msg+`" >&2`)
			if sug != "" {
				lines = append(lines, `        echo "Suggestion: `+sug+`" >&2`)
			}
			lines = append(lines, "        exit 127")
			lines = append(lines, "        ;;")
		}
		lines = append(lines, "      --*)")
		lines = append(lines, "        : ;;")
		if len(shortPatterns) > 0 {
			lines = append(lines, "      "+strings.Join(shortPatterns, "|")+")")
			lines = append(lines, `        echo "`+msg+`" >&2`)
			if sug != "" {
				lines = append(lines, `        echo "Suggestion: `+sug+`" >&2`)
			}
			lines = append(lines, "        exit 127")
			lines = append(lines, "        ;;")
		}
		lines = append(lines, "    esac")
		lines = append(lines, "  done")
		lines = append(lines, "fi")
		lines = append(lines, "exec "+realBin+` "$@"`)
		lines = append(lines, "")
	} else {
		lines = append(lines, "#!/bin/sh")
		lines = append(lines, `if [ -z "$YOLO_BYPASS_SHIMS" ]; then`)
		if realBin != "" {
			// An always-block entry with exceptions: "refuse, unless the invocation
			// carries one of these". This is the arm `find` would use to become
			// "blocked unless depth-limited" — see OQ-GR-1.
			lines = append(lines, allowScan()...)
		}
		lines = append(lines, `  echo "`+msg+`" >&2`)
		if sug != "" {
			lines = append(lines, `  echo "Suggestion: `+sug+`" >&2`)
		}
		lines = append(lines, "  exit 127")
		lines = append(lines, "fi")
		if realBin != "" {
			lines = append(lines, "exec "+realBin+` "$@"`)
		}
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}

// GenerateAgentLaunchers writes one lazy-install launcher per pack `program`
// contribution into the LAUNCH dir (~/.yolo/bin/launch), which is ordered LAST on PATH.
// npm vs native launcher body is driven by the pack's install declaration.
//
// EVERY program contribution, not the first: a pack declaring `shellcheck` and `shfmt`
// gets two launchers. The loop is nested (packs × their installs) because
// InstallContributions is plural — it used to return on the first match, so the second
// binary of any two-tool pack silently never installed while the host path reported both.
//
// It no longer skips a name a blocked-tool shim owns, and that is the point of the split
// rather than an omission. The two dirs cannot collide, so a tool that is BOTH blocked and
// declared as a pack `program` gets a blocker in ~/.yolo/bin/block (first on PATH) and a
// launcher in ~/.yolo/bin/launch (last) — and the blocker wins by position, which is the
// correct outcome. Previously the launcher was simply never written, so a config that later
// unblocked the tool left it with no installer until the next boot.
//
// This is also the FIRST generator to write into the launcher dir, so it owns the
// contents-only reset (the same contract GenerateShims has for the shim dir); the
// package-manager launchers below are additive and must run after it.
func GenerateAgentLaunchers(e *Env) error {
	launcherDir := e.LaunchDir()
	if err := resetAnchorDir(launcherDir); err != nil {
		return err
	}
	stampDir := filepath.Join(e.Home, ".cache", "yolo-agent-stamps")

	packs, err := LoadJailPacks(e)
	if err != nil {
		return err
	}
	for _, p := range packs {
		// HonoredInstalls applies the ORIGIN gate PER CONTRIBUTION: a fetched pack cannot
		// introduce a curl-piped installer, because that would let a git ref run arbitrary
		// code in the jail — but an npm install beside it is not gated, so the decision
		// cannot be made once for the whole pack. The refusals were already reported at
		// staging time on the host.
		installs, _ := p.HonoredInstalls()
		for i := range installs {
			inst := &installs[i]
			if !packdecl.ValidBinName(inst.Bin) {
				// The launcher is FILED at filepath.Join(LaunchDir, bin); a traversal
				// bin would write outside the anchor into the jail's persistent home.
				// LoadJailPacks makes the manifest refusal fatal before this can run —
				// this is defense-in-depth for a caller that bypasses the loader.
				continue
			}
			launcherPath := filepath.Join(launcherDir, inst.Bin)
			if pathExists(launcherPath) {
				// Two packs claiming one bin name. The footprint check refuses this on the
				// host (`program` is CombineExclusive by bin); first-writer-wins keeps the
				// jail deterministic if it ever gets here anyway. It also covers a pack
				// that repeats one bin across two of its OWN contributions.
				continue
			}
			var launcher string
			switch inst.Kind {
			case "npm":
				launcher = npmAgentLauncher(inst, stampDir, receiptsFile(e),
					agentUpdatesAllows(e, p.Name))
			case "native":
				launcher = nativeAgentLauncher(inst, stampDir, receiptsFile(e), capturesDir(e),
					agentUpdatesAllows(e, p.Name))
			default:
				// UNREACHABLE from the boot path: LoadJailPacks reads manifests tolerantly,
				// and DecodeTolerant drops a `program` whose `via` this build does not know
				// before it can reach here (program-delivery.md §6.2). This branch is
				// defense-in-depth for a hand-built Install value, and it warns rather than
				// dropping silently — a declared binary with no launcher and no message is
				// the failure mode the plural-installs bug already cost once.
				e.warn(fmt.Sprintf(
					"yolo-entrypoint: pack %s: no launcher for %q — unknown install kind %q",
					p.Name, inst.Bin, inst.Kind))
				continue
			}
			if err := writeExecutable(launcherPath, launcher); err != nil {
				return err
			}
		}
	}
	return nil
}

// npmAgentLauncher renders the npm lazy-install launcher for one `program via npm`
// contribution.
//
// The declared package string is split here rather than in the shell, because the template
// needs both halves in DIFFERENT places (see npmspec.go): the name indexes node_modules
// and is the only thing `npm view` accepts, while the install spec is the name plus
// whatever selector the pack asked for. Splitting it in bash would mean re-deriving npm's
// scoped-package rule in a launcher that has to keep working when the pack author gets it
// slightly wrong.
//
// EVERY VALUE IS shquote'd — see the splice contract on npmLauncherTemplate. `flags` takes
// Join rather than Quote because it is a LIST that must reach npm as several argv words;
// Join quotes each word and leaves only the separating spaces splittable, which is the one
// place in these templates where word splitting is intended rather than tolerated. The
// trailing space is the template's, not the value's: the sentinel is glued to
// `--prefer-online`, so an empty list must render nothing at all.
//
// __YOLO_PINNED__ IS THE ONE QUOTING CALL NO TEST CAN KILL, and that is a property of the
// value rather than a gap: `pinned` is the string "0" or "1", decided three lines up from a
// bool, so Quote is the identity on every input it can ever receive. It is quoted anyway so
// the contract has no exemptions for a reader to memorize; a mutation run will report it as
// a survivor, and that report is correct.
func npmAgentLauncher(inst *packdecl.Install, stampDir, receiptsPath string,
	updates bool) string {
	binName := inst.Bin
	pkgName, pkgVersion := splitNpmSpec(inst.Package)
	pinned := "0"
	if npmSpecIsPinned(pkgVersion) {
		pinned = "1"
	}
	extraFlags := shquote.Join(inst.Flags)
	if extraFlags != "" {
		extraFlags += " "
	}
	r := strings.NewReplacer(
		"__YOLO_BIN__", shquote.Quote(binName),
		"__YOLO_PKG__", shquote.Quote(pkgName),
		"__YOLO_SPEC__", shquote.Quote(npmInstallSpec(pkgName, pkgVersion)),
		"__YOLO_PINNED__", shquote.Quote(pinned),
		"__YOLO_STAMP_DIR__", shquote.Quote(stampDir),
		"__YOLO_EXTRA__", extraFlags,
		"__YOLO_UPDATES_ENABLED__", shquote.Quote(boolFlag(updates)),
		"__YOLO_HAS_UPDATE_VERB__", shquote.Quote(boolFlag(len(inst.UpdateVerb) > 0)),
		// Join for the same reason `flags` above takes it: a LIST that must reach the
		// program as several argv words, landing in the bare `UPDATE_VERB=(…)`.
		"__YOLO_UPDATE_VERB__", shquote.Join(inst.UpdateVerb),
		"__YOLO_RECEIPTS_FILE__", shquote.Quote(receiptsPath),
		"__YOLO_RECEIPT_HEAD__", shquote.Quote(receiptPrefix("npm", binName, inst.Package)),
	)
	return r.Replace(npmLauncherTemplate)
}

// nativeAgentLauncher renders the installer-URL launcher for one `program via native`
// contribution. Same splice contract as npmAgentLauncher: every value is shquote'd and
// lands in a bare position.
//
// THE URL IS THE SHARPEST ONE. It used to be spliced raw onto curl's argv
// (`curl -fsSL __YOLO_URL__ -o "$script"`), so an `installerUrl` carrying a space, a `;` or
// a `$(…)` was shell source rather than an argument — the values are post-approval (a human
// accepted the pack), which made this hardening rather than a live hole, and is not a reason
// to leave one generator quoting and another not.
// capturesPath is the in-jail install-capture store, EMPTY when this jail has none.
//
// BAKED INTO THE LAUNCHER, never read from the environment by the generated script, for
// exactly receiptsFile's reason turned around: the value IS a container env var here (the
// run pipeline emits `-e YOLO_CAPTURES_DIR=/ctx/captures` beside the `:ro` bind), but
// macos-user execs its launchers under `env -i`, so a `${YOLO_CAPTURES_DIR:-}` in the
// template would read empty in one backend and populated in another for reasons the script
// cannot see. Reading it HERE, once, at generation time, makes the launcher's copy the same
// fact the boot had.
//
// A non-absolute value is dropped with a warning rather than spliced. It can only arrive
// from a host that emitted a malformed `-e`, and a relative store path would make the
// launcher's `[ -d "$CAPTURES_DIR" ]` test resolve against whatever directory the agent
// happened to be in.
func capturesDir(e *Env) string {
	dir := e.Getenv(CapturesDirEnv)
	if dir == "" {
		return ""
	}
	if !filepath.IsAbs(dir) {
		e.warn(fmt.Sprintf("yolo-entrypoint: %s=%q is not an absolute path — no launcher "+
			"will materialize from the capture store", CapturesDirEnv, dir))
		return ""
	}
	return dir
}

func nativeAgentLauncher(inst *packdecl.Install, stampDir, receiptsPath, capturesPath string,
	updates bool) string {
	binName := inst.Bin
	installerURL := inst.InstallerURL
	r := strings.NewReplacer(
		"__YOLO_BIN__", shquote.Quote(binName),
		"__YOLO_URL__", shquote.Quote(installerURL),
		"__YOLO_STAMP_DIR__", shquote.Quote(stampDir),
		"__YOLO_RECEIPTS_FILE__", shquote.Quote(receiptsPath),
		"__YOLO_CAPTURES_DIR__", shquote.Quote(capturesPath),
		"__YOLO_UPDATES_ENABLED__", shquote.Quote(boolFlag(updates)),
		"__YOLO_HAS_UPDATE_VERB__", shquote.Quote(boolFlag(len(inst.UpdateVerb) > 0)),
		// Join, not Quote: the verb is a LIST that must reach the program as several argv
		// words, and Join quotes each word so only the separating spaces stay splittable —
		// the same one place in these templates where word splitting is intended, and the
		// same treatment npmAgentLauncher gives `flags`. It lands inside `UPDATE_VERB=(…)`,
		// which is a bare position: nothing here is inside quotes.
		"__YOLO_UPDATE_VERB__", shquote.Join(inst.UpdateVerb),
		"__YOLO_RECEIPT_HEAD__", shquote.Quote(receiptPrefix("installer", binName, installerURL)),
	)
	return r.Replace(nativeLauncherTemplate)
}

// boolFlag renders a Go bool as the "1"/"0" a generated launcher tests with `[ "$X" = "1" ]`.
//
// Deliberately not `true`/`false`: the templates already spell every other switch that way
// (PINNED, YOLO_PACK_UPDATE, YOLO_INSTALL_ONLY), and one variable answering a different
// vocabulary is how a `[ "$X" = "1" ]` comes to silently mean "off" for the value someone
// reached for to mean "on".
func boolFlag(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

// GeneratePackageManagerLaunchers writes lazy npm launchers for package managers not
// pre-installed via mise (pnpm) into the LAUNCHER dir. Every spliced value is shlex.quote'd
// (see the splice contract on npmLauncherTemplate) so a $HOME with shell metacharacters
// doesn't break the launcher. This generator quoted its stamp dir — through a
// `__YOLO_STAMP_DIR_LIT__` sentinel whose `_LIT_` suffix marked it as the exception — while
// the other two spliced the same value raw. The suffix is gone with the exception it named.
//
// It writes a gap receipt for the same reason the agent launchers do: this is an install
// yolo itself runs, and "every install yolo runs leaves one line" (§10 step one) is a claim
// about the SET. pnpm was the one member missing from it — a program installed at first use,
// from the registry, with no record anywhere that it happened.
//
// MkdirAll-only (no clear): GenerateAgentLaunchers owns the dir reset and runs first, so
// clearing here would delete the launchers it just wrote.
func GeneratePackageManagerLaunchers(e *Env) error {
	launcherDir := e.LaunchDir()
	if err := os.MkdirAll(launcherDir, 0o755); err != nil {
		return err
	}
	stampDir := filepath.Join(e.Home, ".cache", "yolo-package-manager-stamps")

	// The only lazily-installed package manager is pnpm. The package string goes through
	// the same split as a pack's, so `pnpm` still renders `pnpm@latest` byte-for-byte
	// while a future entry that names a version would be honoured instead of corrupted —
	// this list is the second site that hardcoded `@latest`, and leaving one behind is how
	// the next reader concludes the rule is inconsistent rather than fixed.
	for _, pm := range []struct{ bin, pkg string }{{"pnpm", "pnpm"}} {
		launcherPath := filepath.Join(launcherDir, pm.bin)
		if pathExists(launcherPath) {
			continue // a pack already claimed this bin name
		}
		body := pkgManagerLauncher(pm.bin, pm.pkg, stampDir, receiptsFile(e))
		if err := writeExecutable(launcherPath, body); err != nil {
			return err
		}
	}
	return nil
}

// pkgManagerLauncher renders one package-manager launcher body.
//
// It is split out of GeneratePackageManagerLaunchers for the same reason npmAgentLauncher
// and nativeAgentLauncher are separate from GenerateAgentLaunchers, and for one more: the
// bin and package above are a HARDCODED list, so `shquote.Quote("pnpm")` is byte-identical
// to no quoting at all and a test driving the generator could never tell the two apart. A
// seam that takes bin and pkg as arguments is what makes the splice contract measurable on
// those two sentinels — see TestPkgManagerLauncherQuotesItsBinAndSpec. The production call
// site stays pinned by every test that reads the emitted pnpm launcher.
func pkgManagerLauncher(bin, pkg, stampDir, receiptsPath string) string {
	pkgName, pkgVersion := splitNpmSpec(pkg)
	r := strings.NewReplacer(
		"__YOLO_BIN__", shquote.Quote(bin),
		"__YOLO_SPEC__", shquote.Quote(npmInstallSpec(pkgName, pkgVersion)),
		"__YOLO_STAMP_DIR__", shquote.Quote(stampDir),
		"__YOLO_RECEIPTS_FILE__", shquote.Quote(receiptsPath),
		// kind "npm" because the RESOLVER is npm — the receipt names the mechanism that
		// did the install, not the declaration's origin. pnpm is declared by that list
		// rather than by a pack, and a reader comparing receipts to bytes has no use for
		// that distinction; a reader looking for "which resolver do I ask about this
		// package" has every use for the kind.
		"__YOLO_RECEIPT_HEAD__", shquote.Quote(receiptPrefix("npm", bin, pkg)),
	)
	return r.Replace(pkgManagerLauncherTemplate)
}

// stampMtimeFn is the `_stamp_mtime` helper every launcher template embeds, and it exists
// because `stat -c %Y` is GNU-only.
//
// THE BUG IT FIXES WAS NOT A MISSING NUMBER, IT WAS A DEFEATED THROTTLE. macos-user runs
// these generated launchers NATIVELY on the host (RunDarwinBootstrap calls the same three
// generators the container's boot path does), where BSD stat rejects `-c` and the old
// `|| echo 0` swallowed it. The stamp's mtime then read as epoch 0, so every
// `$(date +%s) - 0` age was ~56 years — permanently `-gt UPDATE_INTERVAL` and
// permanently `-lt RETRY_INTERVAL`. Every launch polled the registry (npm agent), ran a
// self-update (native agent), or re-attempted a failed install (package manager): the
// exact per-invocation network traffic all three throttles exist to prevent, silently, on
// the one backend with no image to hide it.
//
// GNU first, then BSD, then 0. The order matters: the container is Linux and is the common
// case, so it takes the first branch and pays no extra process. `date -r` is deliberately
// NOT used as the BSD arm — GNU `date -r` means "read the format from a file" rather than
// "use the file's mtime", so a coreutils host would answer a different question rather than
// fail over. The final `echo 0` is kept for a genuinely absent stamp, which is the one case
// where "infinitely old" is the right answer; every CALLER guards `-f "$STAMP"` first, so it
// no longer doubles as an error path.
const stampMtimeFn = `
# Portable stamp mtime in epoch seconds: GNU stat, then BSD stat, then 0.
_stamp_mtime() {
    stat -c %Y "$1" 2>/dev/null || stat -f %m "$1" 2>/dev/null || echo 0
}
`

// receiptsFile is where every install yolo itself runs appends its gap receipt
// (docs/design/program-delivery.md §10 step one, OQ-PD1's "yolo-written receipts only
// where no native lock exists"). One JSON line per install: the npm and installer agent
// launchers, the pnpm package-manager launcher, and the bootstrap's MCP-preset and LSP arms.
//
// THE WORKSPACE OWNS THE REALIZATION, NOT THE DECLARATION, and the file is filed with the
// former on purpose. Packs are USER scope by ruling (OQ-PD1: "ecosystem-native lockfiles at
// the declaration's home … user for `packs`"), so the workspace under which an install ran
// is not where its declaration lives and cannot be read as the pin. What IS per-workspace is
// the thing the receipt describes: `<ws>/.yolo/home/{npm-global,local,go}` are the binds the
// npm prefix, ~/.local/bin and $GOBIN resolve to, so the BYTES a receipt names exist in this
// workspace and in no other. That makes this a workspace-scope observation log beside the
// realization (§10 step one, verbatim), and the user-scope pin OQ-PD1 names —
// `packs.lock.json`, which already exists and is empty — arrives with the fifth step, where
// obeying starts. Nothing here is consulted by an install.
//
// THAT ATTRIBUTION IS KNOWN-COARSE ON macos-user, and the divergence is the backend's rather
// than a bug to fix here: there is one sandbox home for the whole machine
// (`macosuser.SandboxHome()` = /Users/_yolojail), so ~/.npm-global and ~/.local/bin are
// shared by every workspace that jail ever runs. A receipt filed under the workspace that
// happened to trigger the install still names real bytes, but a SECOND workspace's launcher
// will find that program already installed, write nothing, and leave its own file silent
// about a program it uses. Read the macos-user files as "this workspace caused it", never as
// "this workspace has it".
//
// IT IS BAKED AT GENERATION TIME, not read from the environment by the generated script,
// and neither runtime leaves that a choice: YOLO_WORKSPACE is a HOST-side launcher input
// that is absent inside a live container, and macos-user execs its launchers under
// `env -i`. A `${YOLO_WORKSPACE:-/workspace}` in the template would therefore have written
// every macos-user jail's receipts to a container path that does not exist there, silently,
// which is the same class of defect the stat -c bug in stampMtimeFn was.
func receiptsFile(e *Env) string {
	return filepath.Join(e.WorkspaceDir(), ".yolo", "receipts.jsonl")
}

// receiptPrefix renders the head of a receipt line: the fields a GENERATOR already knows —
// schema version, resolver kind, binary name, and the pack's declaration verbatim.
//
// encoding/json does the escaping, at generation time, so a declaration carrying a quote or
// a backslash cannot produce a line no reader can parse. The shell downstream interpolates
// only constrained values (a spec, a version, a hex digest, an integer, an act, a date).
//
// bin and declared are omitted when empty, which is the LSP bootstrap's case: it reads its
// package list out of the environment at run time, so it renders the constant half here and
// appends the other two from the shell (_yolo_head) under the same scrubbing.
//
// NO `path` FIELD FROM THE BOOTSTRAP'S ARMS (lsp-npm, lsp-go, mcp-npm), and that is what the
// kind is for. Those three install a LIST — the whole of YOLO_LSP_NPM_INSTALL, of
// YOLO_LSP_GO_INSTALL, of the enabled MCP presets — through one resolver each, so every
// entry lands in the one directory that resolver owns: $NPM_CONFIG_PREFIX/lib/node_modules
// (+ its bin/) for the two npm kinds, $GOBIN for lsp-go. The kind therefore IMPLIES the
// prefix, and spelling it per line would repeat one constant across every receipt a boot
// writes while adding nothing a reader could not derive. The three launcher funnels are the
// opposite case and DO carry it: each installs one program and has $REAL_BIN in hand, and
// for the installer kind the landing path is the only identity there is (§6.3).
func receiptPrefix(kind, bin, declared string) string {
	var b strings.Builder
	b.WriteString(`{"schema":1,"kind":`)
	b.WriteString(jsonStringLiteral(kind))
	if bin != "" {
		b.WriteString(`,"bin":`)
		b.WriteString(jsonStringLiteral(bin))
	}
	if declared != "" {
		b.WriteString(`,"declared":`)
		b.WriteString(jsonStringLiteral(declared))
	}
	return b.String()
}

// jsonStringLiteral renders s as a JSON string, quotes included.
func jsonStringLiteral(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		return `""`
	}
	return string(b)
}

// receiptShellFns is the receipt writer every generated installer embeds, spliced the same
// way stampMtimeFn is. A SHARED CONST rather than a sourced ~/.yolo-receipts.sh: a new
// generated file needs its own mount surface, its own anchor reset and its own golden
// entry, none of which a helper three templates share has any business costing.
//
// THE APPEND CANNOT FAIL ITS CALLER, and that is a requirement rather than caution. Two of
// the three templates run under `set -euo pipefail`, and the npm launcher's _do_install
// STATUS is load-bearing at three call sites — it is the only signal `yolo pack update`
// gets. So the whole body is a group on the left of `|| true` (which suspends errexit for
// everything inside it) and the function ends in an UNCONDITIONAL `return 0`. A receipt
// records; it never gates (program-delivery.md §9 R1) — it has no outcome of its own to
// report, and nothing of anyone else's to relay either.
//
// It used to end `return "$_yr_rc"`, with `_yr_rc` read from `$?` on the first line, and
// that read the wrong thing: at function entry `$?` is not the caller's status but whatever
// the last command substitution in THIS call's own arguments left behind — and every call
// site passes at least one (`"$(_installed_version)"`, `"$(_yolo_head …)"`). The
// cannot-fail property therefore held only because those helpers happen to end in
// `return 0`; a resolver added later that reported "I could not tell" with a non-zero
// status would have killed its caller under errexit, two frames from anything that mentions
// receipts. `return 0` makes the property structural instead of incidental.
//
// It mkdir -p's its own parent because macos-user stages no <ws>/.yolo for it.
//
// GNU/BSD portability is the same trap stampMtimeFn documents, and the digest is where it
// bites: `sha256sum` is GNU coreutils, `shasum -a 256` is the BSD/perl spelling, `openssl
// dgst` is the last resort — and the three print the digest in three different columns
// (bare, bare, after an "SHA2-256(path)=" prefix). The hex is therefore FOUND by shape, not
// indexed by field, or a macOS run would record a filename as a digest.
const receiptShellFns = `
# --- gap receipts (docs/design/program-delivery.md §10) ---------------------------
_yolo_scrub() {
    printf '%s' "${1:-}" | tr -cd '[:print:]' | tr -d '\\"' | cut -c 1-200
}

_yolo_sha256() {
    local out tok
    out=$(sha256sum "$1" 2>/dev/null) ||
        out=$(shasum -a 256 "$1" 2>/dev/null) ||
        out=$(openssl dgst -sha256 "$1" 2>/dev/null) ||
        return 0
    for tok in $out; do
        if [ ${#tok} -eq 64 ] && [ -z "${tok//[0-9a-f]/}" ]; then
            printf '%s\n' "$tok"
            return 0
        fi
    done
    return 0
}

# BSD wc pads its count with leading spaces; GNU does not.
_yolo_bytes() {
    local n
    n=$(wc -c < "$1" 2>/dev/null) || return 0
    n="${n//[!0-9]/}"
    if [ -n "$n" ]; then printf '%s\n' "$n"; fi
    return 0
}

# _yolo_head appends the bin/declared pair for a receipt whose identity is only known at
# run time. $1 is the constant half, rendered in Go.
_yolo_head() {
    local h="$1"
    if [ -n "${2:-}" ]; then h="$h,\"bin\":\"$(_yolo_scrub "$2")\""; fi
    if [ -n "${3:-}" ]; then h="$h,\"declared\":\"$(_yolo_scrub "$3")\""; fi
    printf '%s' "$h"
}

# _yolo_receipt <head> <spec> <resolved> <file-to-digest> <act> [landing-path]
# Every argument but <head> may be empty; an empty one omits its field. <landing-path> is
# §6's fourth tuple member and is passed only by the three launcher funnels, which have the
# path in hand; see receiptPrefix for why the bootstrap's arms leave it out.
_yolo_receipt() {
    {
        local line dig sz
        line="$1"
        if [ -n "${2:-}" ]; then line="$line,\"spec\":\"$(_yolo_scrub "$2")\""; fi
        if [ -n "${3:-}" ]; then line="$line,\"resolved\":\"$(_yolo_scrub "$3")\""; fi
        if [ -n "${4:-}" ] && [ -f "$4" ]; then
            dig=$(_yolo_sha256 "$4")
            sz=$(_yolo_bytes "$4")
            if [ -n "$dig" ]; then line="$line,\"sha256\":\"$dig\""; fi
            if [ -n "$sz" ]; then line="$line,\"bytes\":$sz"; fi
        fi
        if [ -n "${6:-}" ]; then line="$line,\"path\":\"$(_yolo_scrub "$6")\""; fi
        line="$line,\"act\":\"$(_yolo_scrub "${5:-install}")\""
        line="$line,\"time\":\"$(date -u +%Y-%m-%dT%H:%M:%SZ)\"}"
        # ONE printf, ONE line: the whole receipt reaches the file as a single write(2) on a
        # descriptor opened O_APPEND, and the kernel serializes the implied seek-to-end and
        # the write against every other appender on the same inode — which is what keeps two
        # launchers installing at once from interleaving half-lines. (An earlier note here
        # credited PIPE_BUF. PIPE_BUF bounds atomic writes to a PIPE and says nothing about a
        # regular file; the line-length budget the schema keeps is a readability bound, not
        # the thing that makes this append atomic.)
        mkdir -p "${_YOLO_RECEIPTS%/*}"
        printf '%s\n' "$line" >> "$_YOLO_RECEIPTS"
    } >/dev/null 2>&1 || true
    return 0
}
`

// npmLauncherTemplate is the npm agent launcher body, with the per-agent
// fields replaced by __YOLO_*__ sentinels.
//
// THE SPLICE CONTRACT, which all three templates in this file obey and which the three
// generators above implement:
//
//	Every __YOLO_*__ sentinel that carries a value is a SHELL LITERAL — shquote'd in Go
//	(Quote, or Join for a list) and landing in a BARE, unquoted position: the right-hand
//	side of an assignment, or a whole argv word. No sentinel appears inside quotes, no
//	sentinel appears mid-word, and no sentinel appears in a `#` comment.
//
// The bare position is what makes the quoting load-bearing rather than decorative: a value
// spliced into `X="__SENTINEL__"` cannot be Quote'd (the emitted single quotes would land
// INSIDE the double quotes and become data), which is precisely why the raw form was chosen
// there in the first place and precisely why it could not be fixed in place. A value needed
// inside a string is therefore assigned ONCE at the top and referenced as "$VAR" — that is
// why BIN, PKG, SPEC and URL are shell variables and why the header comment no longer names
// the program (a bin name carrying a newline would have ended the comment and turned its
// tail into shell source; the file's own name in ~/.yolo/bin/launch/ says it instead).
//
// The values arrive from a pack manifest a human already approved, so this is hardening, not
// a live exploit. There are 18 value splices across the three replacers (8 + 5 + 5, counted
// 2026-09-03), and 7 of them were ALREADY correct before this contract was written: both
// receipt fields in all three templates, plus the package-manager stamp dir. That is what
// made the other 11 legible as a mistake rather than as a decision.
//
// THE LAUNCHER RESOLVES A NEW VERSION FOR AN UNPINNED PACKAGE, at most once per
// UPDATE_INTERVAL, when the jail's agent_updates policy allows it (program-delivery.md
// §3.5, OQ-PD12). It used to only REPORT: trust-paths.md §1 row 1 ("no magical evergreen
// npm packages", 2026-08-18) took the hourly reinstall away, and OQ-PD3 was narrowed on
// 2026-09-03 to say that ruling covers PROJECT dependencies and not the agent class.
// A PINNED package is untouched by any of it — a declared selector already IS the answer
// to "which version", so there is nothing to resolve.
const npmLauncherTemplate = `#!/bin/bash
# Lazy-install launcher — installs on first use, not at boot, and refreshes an UNPINNED
# package at most once per UPDATE_INTERVAL. BIN below names the program.
set -euo pipefail
export NPM_CONFIG_PREFIX="${NPM_CONFIG_PREFIX:-$HOME/.npm-global}"
export NPM_CONFIG_CACHE="${NPM_CONFIG_CACHE:-$HOME/.cache/npm}"
BIN=__YOLO_BIN__
STAMP_DIR=__YOLO_STAMP_DIR__
STAMP="$STAMP_DIR/$BIN.stamp"
SPEC_FILE="$STAMP_DIR/$BIN.spec"
REAL_BIN="$NPM_CONFIG_PREFIX/bin/$BIN"
# PKG is the package NAME alone; SPEC is what "npm install" is handed. They differ whenever
# the pack declared a version, and the two are NOT interchangeable: only PKG may index
# node_modules or be passed to "npm view", and only SPEC may be installed.
PKG=__YOLO_PKG__
SPEC=__YOLO_SPEC__
PINNED=__YOLO_PINNED__   # 1 when the declaration carried a version selector
UPDATE_INTERVAL=3600  # seconds between updates of an UNPINNED package
UPDATE_TIMEOUT=60     # a hung updater must not hang the command the user typed
STALE_LOCK=600        # a lock nobody released must not freeze updates forever
# 1 when this jail's agent_updates policy lets this pack move. BAKED, so a launcher
# generated under a frozen policy carries no update branch at all.
UPDATES_ENABLED=__YOLO_UPDATES_ENABLED__
# The pack's declared update verb (OQ-PD14): the program's own argv, bin omitted. No
# SHIPPED npm pack declares one — for those the registry IS the vendor's channel, so
# "npm install -g" below is the fallback and it is the right answer. The branch exists
# because the projection carries the verb for every via, and a value the manifest
# accepts and the launcher ignores is a declaration that silently does nothing.
# HAS_UPDATE_VERB gates every expansion of the array: bash before 4.4 treats "${arr[@]}"
# on an EMPTY array as unbound under "set -u", and macos-user runs these launchers
# against a stock /bin/bash 3.2.
HAS_UPDATE_VERB=__YOLO_HAS_UPDATE_VERB__
UPDATE_VERB=(__YOLO_UPDATE_VERB__)
# ONE lock per INSTALL PREFIX: two npm installs into one $NPM_CONFIG_PREFIX at the same
# moment is what §3.5's contention rule forbids. Per-workspace binds make this unreachable
# on the container backends; macos-user shares one home across every workspace and session.
LOCK_DIR="$NPM_CONFIG_PREFIX/.yolo-update.lock"
# Baked, never read from the environment: see receiptsFile.
_YOLO_RECEIPTS=__YOLO_RECEIPTS_FILE__

# --- re-entry ----------------------------------------------------------------------
# B2 PUT THE LAUNCH DIR AHEAD OF THE INSTALL PREFIXES, so a BARE-NAME call of this program
# from inside its own update — an npm postinstall that runs the tool, a vendor updater that
# shells out to itself — now resolves back to this script. The launcher's own calls go
# through an absolute $REAL_BIN and are safe; nothing else is. Without this guard the first
# invocation after the reorder is a fork bomb.
#
# ONE variable holding a delimited SET, rather than one variable per bin: a bin name may
# carry characters no shell identifier may (- and .).
case ":${_YOLO_LAUNCHER_ACTIVE:-}:" in
    *":$BIN:"*)
        if [ -x "$REAL_BIN" ]; then exec "$REAL_BIN" "$@"; fi
        echo "  ⚠ $BIN not available" >&2
        exit 1
        ;;
esac
export _YOLO_LAUNCHER_ACTIVE="${_YOLO_LAUNCHER_ACTIVE:-}:$BIN"

mkdir -p "$STAMP_DIR"
mkdir -p "$NPM_CONFIG_PREFIX"
` + stampMtimeFn + receiptShellFns + `
_installed_version() {
    jq -r '.version' "$NPM_CONFIG_PREFIX/lib/node_modules/$PKG/package.json" 2>/dev/null || echo "0"
}

# _resolved_version answers the RECEIPT's question — "what did npm actually put on disk?" —
# and answers NOTHING when it cannot tell.
#
# _installed_version cannot be reused for it, and the difference is the whole point of the
# split: its "|| echo 0" is a POLL sentinel, picked so that a package which is absent or
# unreadable compares unequal to any registry answer and the "an update is available" line
# still fires. Copied into a receipt that same 0 becomes a FORGERY — a run with no jq on
# PATH (macos-user executes these launchers natively, under env -i, against whatever the
# host has) would record "resolved":"0" for every install it ever made, and a reconcile
# reading the file back cannot tell that from a package genuinely at version 0. An omitted
# field says "unknown", which is the truth, and _yolo_receipt drops an empty one. Same shape
# as the bootstrap's _yolo_lsp_npm_version, for the same reason.
_resolved_version() {
    local v
    v=$(jq -r '.version' "$NPM_CONFIG_PREFIX/lib/node_modules/$PKG/package.json" 2>/dev/null) || return 0
    # jq prints "null" for an absent key, which is not a version.
    if [ "$v" != "null" ]; then printf '%s\n' "$v"; fi
    return 0
}

# _do_install RETURNS THE INSTALL'S STATUS, and that return value is load-bearing for
# exactly one caller. Update mode has no other way to find out that the program it was
# asked to refresh is not there: it exits instead of exec'ing, so the "is $REAL_BIN
# missing?" test at the bottom of this script — the launch path's verdict, and a truer
# question there, since a failed UPGRADE still leaves a working binary to run — never
# executes. Without a status, "yolo pack update" reported success for a refresh that
# installed nothing, which is the silent no-op the whole split exists to avoid.
#
# The launch path therefore drops it explicitly ("_do_install || true") rather than by
# accident: dropping it is the correct behaviour there, and saying so keeps the two
# readings from being confused for one.
_do_install() {
    local rc=0
    echo "  Installing $SPEC..." >&2
    # Clean stale npm temp dirs that cause ENOTEMPTY
    rm -rf "$NPM_CONFIG_PREFIX"/lib/node_modules/${PKG%%/*}/.${PKG##*/}-* 2>/dev/null
    if YOLO_BYPASS_SHIMS=1 npm install -g __YOLO_EXTRA__--prefer-online "$SPEC" 2>&1; then
        # Record what we ASKED for, and ONLY once npm agreed to it. It lets a later run tell
        # "the DECLARATION moved" from "the registry moved" with a local file read and no
        # network — the only question a pinned package still has to answer.
        #
        # Recording it on FAILURE too looks like the same one-attempt-per-event throttle the
        # stamp is, and it is not: it WEDGES the jail. An upgrade leaves REAL_BIN present —
        # the PREVIOUS version — so the "not installed" branch never fires, and a spec file
        # already advanced to the new declaration silences the pinned branch as well. One
        # failed "npm install" during an offline hour would pin the jail to the old binary
        # for the life of the home: no hourly retry, no retry at boot, no message. The
        # unpinned path has no such hole, because its next poll still sees the two versions
        # differ and tries again. Leaving the PREVIOUS spec in place instead keeps the record
        # true (it names what is actually on disk) and makes the mismatch self-healing.
        printf '%s\n' "$SPEC" > "$SPEC_FILE"
        # THE RECEIPT FUNNEL for this program. Every path that changes the installed
        # bytes — a cold install, a moved pin, an explicit update — arrives here, and
        # only after npm agreed, so one hook covers all three and none of them records
        # an install that did not happen.
        #
        # The act is decided by the SAME predicate the dispatch at the bottom of this script
        # uses — "= 1", not "is set". They disagreed once: YOLO_PACK_UPDATE=0 took the cold
        # install path and then recorded itself as an update, so the one field that says
        # whether a human asked for this was wrong for exactly the value a caller reaches for
        # to mean "no".
        local act=install
        if [ "${YOLO_PACK_UPDATE:-}" = "1" ]; then act=update; fi
        _yolo_receipt __YOLO_RECEIPT_HEAD__ "$SPEC" "$(_resolved_version)" "" "$act" "$REAL_BIN"
    else
        rc=1
    fi
    touch "$STAMP"
    return "$rc"
}

# _bounded runs its argv under a wall-clock bound where the platform has one. timeout(1)
# is GNU coreutils: the image bakes it, a stock macOS does not, and running unbounded is a
# better answer there than not updating at all.
_bounded() {
    if command -v timeout >/dev/null 2>&1; then
        YOLO_BYPASS_SHIMS=1 timeout "$UPDATE_TIMEOUT" "$@"
    else
        YOLO_BYPASS_SHIMS=1 "$@"
    fi
}

# _take_lock is a NON-BLOCKING mkdir, and both halves of that are §3.5's ruling rather than
# an implementation shortcut: there is no flock in the image and none on a stock macOS, and
# an invocation that cannot take the lock must PROCEED WITHOUT UPDATING and say so.
_take_lock() {
    if mkdir "$LOCK_DIR" 2>/dev/null; then return 0; fi
    local age
    age=$(( $(date +%s) - $(_stamp_mtime "$LOCK_DIR") ))
    if [ "$age" -gt "$STALE_LOCK" ]; then
        rmdir "$LOCK_DIR" 2>/dev/null || true
        if mkdir "$LOCK_DIR" 2>/dev/null; then return 0; fi
    fi
    return 1
}

_drop_lock() { rmdir "$LOCK_DIR" 2>/dev/null || true; }

# _update_due is the throttle. UPDATES_ENABLED is checked FIRST because it is the one gate
# that is not about time: a jail whose agent_updates policy freezes this pack must not even
# ask what the stamp says.
_update_due() {
    [ "$UPDATES_ENABLED" = "1" ] || return 1
    [ -f "$STAMP" ] || return 0
    [ "$(( $(date +%s) - $(_stamp_mtime "$STAMP") ))" -gt "$UPDATE_INTERVAL" ]
}

# _locked_update holds the install-prefix lock across the whole act, and is the only caller
# on the launch path.
_locked_update() {
    local rc=0
    if ! _take_lock; then
        echo "  $BIN: another update is in progress — running the installed version." >&2
        return 0
    fi
    _update || rc=$?
    _drop_lock
    return "$rc"
}

# _update is the ONLY path in this script that resolves a new version, and the only way
# into it is YOLO_PACK_UPDATE=1. That is the install/update split, implemented at the one
# place that knows how to talk to npm rather than copied into Go beside it.
#
# IT REPORTS FAILURE THROUGH ITS EXIT STATUS, because it is the only thing that can. An
# update runs with nobody reading the scrollback — "yolo pack update" walks every
# npm-declared program in turn — and its caller has no other signal: it cannot inspect
# $REAL_BIN (that is this jail's npm prefix, not the CLI's), and npm's own "npm ERR!"
# lines are indistinguishable from the noise a SUCCESSFUL install prints. So every way
# this function can fail to leave the declared program installed returns non-zero:
# a failed "npm install", and a registry that did not answer.
_update() {
    if [ "$PINNED" = "1" ]; then
        # A declared selector already IS the answer to "which version", so there is
        # nothing for an update to resolve — asking the registry here would either
        # override the declaration or waste the round-trip. Converging on the declaration
        # is still this act's job: an update that left the jail behind its own pack would
        # report success while running the old binary.
        if [ ! -x "$REAL_BIN" ] || [ "$(cat "$SPEC_FILE" 2>/dev/null || true)" != "$SPEC" ]; then
            _do_install
        else
            echo "  $BIN is pinned to $SPEC by its pack — nothing to resolve." >&2
        fi
        return
    fi
    if [ ! -x "$REAL_BIN" ]; then
        _do_install
        return
    fi
    if [ "$HAS_UPDATE_VERB" = "1" ]; then
        # NO RECEIPT FOR THE VERB BRANCH, deliberately, and for the reason the native
        # template states at length: the verb is the VENDOR's own updater, so what moved,
        # to what, and where it landed are all its decisions and the launcher observes
        # none of them. A receipt here would be a guess with a timestamp on it.
        echo "  Updating $BIN..." >&2
        local vrc=0
        _bounded "$REAL_BIN" "${UPDATE_VERB[@]}" >&2 || vrc=$?
        touch "$STAMP"
        return "$vrc"
    fi
    INSTALLED=$(_installed_version)
    # A registry that did not answer is NOT "already current", and the two must not share
    # a branch. _poll_and_report may collapse them — it substitutes $INSTALLED for a failed
    # "npm view" on purpose, because an informational poll that cannot reach the registry
    # has nothing to say and must not delay a launch. Here the user ASKED, so the same
    # substitution would answer a question that was never put to the registry: it printed
    # "<version> is already current" and exited 0 for an offline jail, which is worse than
    # the reinstall it replaced — a user told their CLI is current stops looking.
    #
    # An empty answer is treated the same as a failed one: "npm view" has more than one way
    # to come back with nothing useful, and every one of them means "unknown", never "same".
    LATEST=$(YOLO_BYPASS_SHIMS=1 npm view "$PKG" version 2>/dev/null || true)
    if [ -z "$LATEST" ]; then
        echo "  ⚠ $BIN: could not ask the npm registry for a newer version — leaving $INSTALLED in place." >&2
        return 1
    fi
    if [ "$INSTALLED" = "$LATEST" ]; then
        echo "  $BIN $INSTALLED is already current." >&2
        touch "$STAMP"
        return 0
    fi
    echo "  Updating $BIN $INSTALLED → $LATEST..." >&2
    _do_install
}

if [ "${YOLO_PACK_UPDATE:-}" = "1" ]; then
    # Update mode EXITS instead of exec'ing the real binary: "yolo pack update" walks
    # every npm-declared program in turn and must refresh them, not launch them.
    #
    # It exits with _update's STATUS, not 0. The "|| _rc=$?" capture is what makes that
    # readable under "set -e": running _update on the left of a "||" suspends errexit for
    # the whole of it, so a failed "npm install" inside comes back as a return value
    # instead of killing the script two frames down — and the script's own exit code stays
    # the single place this outcome is decided.
    _rc=0
    _update || _rc=$?
    exit "$_rc"
fi

if [ ! -x "$REAL_BIN" ]; then
    # Cold home: the FIRST install is not a poll, and the no-evergreen ruling does not
    # touch it. There is no version here to keep — without this branch a fresh jail would
    # simply have no agent CLI at all.
    #
    # "|| true": on the LAUNCH path a failed install is not the verdict. The -x "$REAL_BIN"
    # test at the bottom is, because it answers the question this path actually has — is
    # there something to exec? — and it answers it correctly for the upgrade case too,
    # where the install failed and the previous version is still perfectly runnable.
    _do_install || true
elif [ "$PINNED" = "1" ]; then
    # A pinned package has nothing to poll for. A "npm view $PKG version" call answers "what is
    # the registry's latest?", which against a declared selector is either ignored (the
    # round-trip was pure cost) or honoured (the declaration was a lie) — and for a tag or
    # a range it never compares equal, so polling would reinstall every hour forever. The
    # one thing that can legitimately move this binary is the DECLARATION, so that is what
    # we compare, offline.
    if [ "$(cat "$SPEC_FILE" 2>/dev/null || true)" != "$SPEC" ]; then
        _do_install || true
    fi
elif _update_due; then
    # EVERGREEN (§3.5, OQ-PD12). This branch used to be _poll_and_report: an hourly
    # "npm view" that printed "x -> y is available. Run yolo pack update" and installed
    # nothing. The ruling it implemented (trust-paths.md §1 row 1, "no magical evergreen
    # npm packages") is NARROWED by OQ-PD3/PD11: it holds for PROJECT dependencies and
    # does not reach the agent class, whose members want to be current and have no pin to
    # be reproducible against.
    #
    # The objection the report answered — "the binary changed between two invocations with
    # nobody present" — is answered differently now rather than ignored: the trigger is the
    # user's OWN invocation of the agent, so there is always somebody present, and it is
    # the person who just typed the program's name. A failure here is scoped to that
    # command and never to the jail.
    _locked_update || true
fi

if [ -x "$REAL_BIN" ]; then
    exec "$REAL_BIN" "$@"
else
    echo "  ⚠ $BIN not available" >&2
    exit 1
fi
`

// InstallOnlyEnv makes a native launcher INSTALL AND STOP, without running the program.
//
// It exists for `yolo capture` (program-delivery.md §6.3), which drives this very launcher
// inside a throwaway jail so that what a capture records is exactly what a launch would have
// installed — the alternative being a second implementation of "download the installer and
// run it", which is the drift this repo spends its comments avoiding.
//
// The exec has to be suppressed rather than tolerated. The capture surfaces are `~/.local`,
// `~/.npm-global` and `~/go`, and a tool run once writes its FIRST-RUN state into them:
// config, machine identifiers, telemetry opt-ins. Captured, that state is content-addressed
// into an entry and hardlinked into every workspace on the machine — §6.3's "an installer
// that personalizes at install time … defeats the sharing", arrived at by accident.
//
// NATIVE LAUNCHERS ONLY. The npm and package-manager launchers ignore it, because capture is
// the INSTALLER resolver's mechanism and nothing else has a reason to install-without-running
// (npm's refresh path already has YOLO_PACK_UPDATE, which is a different question).
const InstallOnlyEnv = "YOLO_INSTALL_ONLY"

// CapturesDirEnv names the in-jail path of the machine's INSTALL-CAPTURE STORE, emitted by
// the run pipeline beside the `:ro` bind that puts it there (`internal/cli/run/assemble.go`).
//
// It is a host↔jail contract in the same class as YOLO_PACK_ROOT: the host decides where the
// store lands (a `/ctx` path under podman, the host path itself under Apple Container, which
// cannot nest the bind) and tells the jail, because the jail cannot derive it — paths.CapturesDir()
// inside a jail resolves to the JAIL's home, which is a per-workspace bind and is not the
// machine-wide store at all.
//
// ABSENT MEANS "NO STORE", and three separate things produce that: a host yolo older than this
// variable, the macos-user backend (which has no mount to make and no capture support yet), and
// the CAPTURE JAIL ITSELF — `yolo capture` suppresses the mount on purpose, so that the installer
// it runs cannot be satisfied by the very store it is filling (see internal/cli/capturehost.go).
const CapturesDirEnv = "YOLO_CAPTURES_DIR"

// nativeLauncherTemplate is the native agent launcher body. Same splice contract as
// npmLauncherTemplate: every sentinel is a shquote'd literal in a bare position, and the
// values that a string needs (BIN, URL) are shell variables assigned once at the top.
//
// IT IS AN UPDATER NOW, not only an installer (program-delivery.md §3.5, OQ-PD12/PD14).
// It used to run `"$REAL_BIN" install` on an hourly stamp — one hardcoded verb, with no
// status, no timeout, no lock and no policy — which is a no-op for every vendor whose verb
// is spelled something else. What replaces it is the pack's DECLARED verb (UPDATE_VERB),
// with the four properties §3.5 requires of an update: bounded (UPDATE_TIMEOUT),
// serialized against the other writers of this install prefix (LOCK_DIR), scoped to the
// invocation rather than to the jail, and not run at all when the jail's `agent_updates`
// policy says so (UPDATES_ENABLED). A7's V-axis prune rides the same success paths
// (_prune_versions), because the act that creates a version is the one that knows to
// delete the one it superseded.
const nativeLauncherTemplate = `#!/bin/bash
# Lazy-update launcher — installs/updates on first use, not at boot. BIN names the program.
set -euo pipefail
BIN=__YOLO_BIN__
URL=__YOLO_URL__
STAMP_DIR=__YOLO_STAMP_DIR__
STAMP="$STAMP_DIR/$BIN.stamp"
REAL_BIN="$HOME/.local/bin/$BIN"
UPDATE_INTERVAL=3600
# A hung vendor updater must not hang the command the user actually typed (§3.5): after
# this many seconds the launcher proceeds with whatever is already installed.
UPDATE_TIMEOUT=60
# A lock nobody released — a killed shell, a jail torn down mid-update — must not freeze
# updates for the life of the home. Ten times the bound on a single attempt.
STALE_LOCK=600
# A7's keep-newest-K over the VENDOR's own version directory (agent-cli-copies.md §5.1):
# the live build plus one rollback target. This K is per workspace and over VERSIONS; it
# is NOT the capture store's K (OQ-PD17: machine-wide, 1), and neither is the N this
# corpus uses for the workspace count.
KEEP_VERSIONS=2
# 1 when this jail's agent_updates policy lets this pack move. BAKED, so a launcher
# generated under a frozen policy carries no update branch at all.
UPDATES_ENABLED=__YOLO_UPDATES_ENABLED__
# The pack's declared update verb (OQ-PD14): the program's own argv, bin omitted.
# HAS_UPDATE_VERB gates every expansion of the array — bash before 4.4 treats
# "${arr[@]}" on an EMPTY array as an unbound variable under "set -u", and macos-user runs
# these launchers against a stock /bin/bash 3.2.
HAS_UPDATE_VERB=__YOLO_HAS_UPDATE_VERB__
UPDATE_VERB=(__YOLO_UPDATE_VERB__)
# Baked, never read from the environment: see receiptsFile.
_YOLO_RECEIPTS=__YOLO_RECEIPTS_FILE__
# The machine's install-capture store, as this jail sees it. Empty when there is none —
# baked at generation time for the same reason as the line above; see capturesDir.
CAPTURES_DIR=__YOLO_CAPTURES_DIR__
# ONE lock per INSTALL PREFIX, not per program: §3.5's contention rule is about who may
# write into $HOME/.local, and two vendor updaters running there at once is what it
# forbids. On the container backends the prefix is a per-workspace bind and nothing can
# contend; macos-user shares one home across every workspace and session, which is the
# case this exists for.
LOCK_DIR="$HOME/.local/.yolo-update.lock"
# What a receipt calls the act that wrote the bytes. The install paths leave it alone; the
# update path flips it, so a reconcile can tell a cold install from an evergreen refresh.
_ACT=install

# --- re-entry ----------------------------------------------------------------------
# B2 PUT THE LAUNCH DIR AHEAD OF THE INSTALL PREFIXES, so a BARE-NAME call of this program
# from inside its own update — a vendor installer that runs "claude", an npm postinstall
# that runs "copilot" — now resolves back to this script. The launcher's own calls go
# through an absolute $REAL_BIN and are safe; the vendor's are not, and yolo does not
# control them. Without this guard the first invocation after the reorder is a fork bomb,
# not a subtle wrong answer.
#
# ONE variable holding a delimited SET, rather than one variable per bin: a bin name may
# carry characters no shell identifier may (- and .), so "YOLO_LAUNCHER_ACTIVE_$BIN" is
# not always a name that can be assigned at all.
case ":${_YOLO_LAUNCHER_ACTIVE:-}:" in
    *":$BIN:"*)
        if [ -x "$REAL_BIN" ]; then exec "$REAL_BIN" "$@"; fi
        echo "  ⚠ $BIN not available" >&2
        exit 1
        ;;
esac
export _YOLO_LAUNCHER_ACTIVE="${_YOLO_LAUNCHER_ACTIVE:-}:$BIN"

mkdir -p "$STAMP_DIR"
mkdir -p "$HOME/.local"
` + stampMtimeFn + receiptShellFns + `
# _bounded runs its argv under a wall-clock bound where the platform has one. timeout(1)
# is GNU coreutils: the image bakes it, a stock macOS does not, and running unbounded is a
# better answer there than not updating at all.
_bounded() {
    if command -v timeout >/dev/null 2>&1; then
        YOLO_BYPASS_SHIMS=1 timeout "$UPDATE_TIMEOUT" "$@"
    else
        YOLO_BYPASS_SHIMS=1 "$@"
    fi
}

# _take_lock is a NON-BLOCKING mkdir, and both halves of that are the ruling rather than an
# implementation shortcut. There is no flock in the image and none on a stock macOS; and an
# invocation that cannot take the lock must PROCEED WITHOUT UPDATING and say so — the user
# typed the agent's name, so making them wait, or refusing, would be worse than running the
# version already on disk (§3.5).
_take_lock() {
    if mkdir "$LOCK_DIR" 2>/dev/null; then return 0; fi
    local age
    age=$(( $(date +%s) - $(_stamp_mtime "$LOCK_DIR") ))
    if [ "$age" -gt "$STALE_LOCK" ]; then
        rmdir "$LOCK_DIR" 2>/dev/null || true
        if mkdir "$LOCK_DIR" 2>/dev/null; then return 0; fi
    fi
    return 1
}

_drop_lock() { rmdir "$LOCK_DIR" 2>/dev/null || true; }

# _prune_versions is A7, the V-axis prune (agent-cli-copies.md §5.1): keep-newest-K over
# the vendor's own version directory, run by the act that created the new version, in this
# workspace, immediately, on success.
#
# IT NEEDS NO STORE, NO ORACLE AND NO ENUMERATION, and that is a property of the tree
# rather than a policy: the referrer set for ~/.local/share/<bin>/versions/* is ONE symlink,
# ~/.local/bin/<bin>, in the same per-workspace tree, so everything else there is
# unreferenced BY CONSTRUCTION for this workspace. Measured 2026-09-04 in this development
# jail: five claude builds totalling 1223.4 MiB, of which 1018.6 MiB — 83.3 % — were
# unreferenced. It needs no filesystem support either, so it behaves the same on ext4 and
# btrfs, which capture does not.
#
# THE SYMLINK IS ALSO THE GUARD. When $REAL_BIN is not a symlink INTO that directory this
# does nothing at all: the referrer set is then unknown, and a prune that cannot name the
# live version has no business deleting anything. That is what makes it safe to call for
# every native program, including the ones that keep no version directory.
_prune_versions() {
    local vdir="$HOME/.local/share/$BIN/versions"
    [ -d "$vdir" ] || return 0
    local live
    live=$(readlink "$REAL_BIN" 2>/dev/null) || return 0
    [ -n "$live" ] || return 0
    case "$live" in
        /*) ;;
        *) live="${REAL_BIN%/*}/$live" ;;
    esac
    case "$live" in "$vdir"/*) ;; *) return 0 ;; esac
    # THE LIVE ENTRY IS THE DIRECTORY ENTRY, NOT THE SYMLINK'S TARGET, and conflating the
    # two deletes the running version. claude's builds are single FILES directly under
    # versions/ (measured 2026-09-04), so there $live IS the entry; a vendor that keeps a
    # directory per version has the binary one level deeper, and comparing the whole target
    # path against the entry then never matches — the guard silently stops guarding, in
    # exactly the rollback shape where it is the only thing standing between "keep newest K"
    # and an unusable launcher.
    local rest live_entry
    rest=${live#"$vdir"/}
    live_entry="$vdir/${rest%%/*}"
    local entry victims
    victims=$(
        for entry in "$vdir"/*; do
            [ -e "$entry" ] || continue
            printf '%s\t%s\n' "$(_stamp_mtime "$entry")" "$entry"
        done | sort -rn | tail -n "+$((KEEP_VERSIONS + 1))" | cut -f2-
    )
    [ -n "$victims" ] || return 0
    while IFS= read -r entry; do
        if [ -n "$entry" ] && [ "$entry" != "$live_entry" ]; then
            rm -rf -- "$entry"
            echo "  $BIN: removed superseded version ${entry##*/}" >&2
        fi
    done <<YOLO_PRUNE_EOF
$victims
YOLO_PRUNE_EOF
    return 0
}


# _try_materialize puts an already-captured install into this home instead of downloading it
# (docs/design/program-delivery.md §6.3's *materialize*). Returns 0 only when $REAL_BIN
# exists afterwards; every other outcome falls through to the vendor installer below.
#
# THE FALLBACK IS NOT OPTIONAL. install-capture.md's Blockers say making a capture MANDATORY
# for this class is a behaviour change nobody has ruled on, and it would make a first run on
# a machine with no capture FAIL. So every failure here — no store, no entry for this
# bin+platform, a torn entry, a store on a filesystem that cannot even copy — is a miss, and
# a miss is silent about everything except what the materializer itself chose to say.
#
# IT IS REACHED FROM THE COLD-INSTALL ARM ONLY, which is OQ-CP4's ruling rather than an
# oversight: the store serves the FIRST install of each workspace and nothing after it, so
# materializing from the update arm would put a captured — by then superseded — build back
# over a newer one.
#
# It is a whole subprocess rather than shell that walks the tree, because the mechanism is a
# reflink ioctl: the fallback chain (reflink -> hardlink -> copy) has no shell spelling, and
# "cp --reflink=auto" is GNU-only and would silently take the copy arm on the BSD userland
# macos-user runs under.
_try_materialize() {
    [ -n "$CAPTURES_DIR" ] || return 1
    [ -d "$CAPTURES_DIR" ] || return 1
    command -v yolo >/dev/null 2>&1 || return 1
    YOLO_BYPASS_SHIMS=1 yolo internal capture-materialize \
        --store="$CAPTURES_DIR" --home="$HOME" --bin="$BIN" \
        --declared="$URL" --receipts="$_YOLO_RECEIPTS" || return 1
    [ -x "$REAL_BIN" ]
}

# _run_installer downloads the vendor's script and runs it. IT RETURNS A STATUS, and that
# status is load-bearing for update mode alone — the same split the npm template carries,
# for the same reason: update mode exits instead of exec'ing, so the "-x $REAL_BIN" test at
# the bottom of this script never runs there, and without a status "yolo pack update"
# reports success for a refresh that installed nothing.
#
# The launch path drops it explicitly ("|| true") rather than by accident: there a failed
# install is not the verdict, because a failed UPGRADE still leaves a working binary to run.
_run_installer() {
    if [ "$_ACT" = update ]; then
        echo "  Updating $BIN (re-running its installer — the pack declares no verb)..." >&2
    else
        echo "  Installing $BIN..." >&2
    fi
    # Download to a file BEFORE running it, rather than curl | bash. A stale or moved
    # installer endpoint usually keeps answering 200 with a web page, and piping that
    # straight into bash reports the HTML as a bash syntax error plus a curl broken-pipe
    # error — three messages, none naming the wrong URL. Landing it first lets us say so.
    local script
    script="$(mktemp -t "$BIN-install-XXXXXX.sh")"
    # "$URL", quoted at the point of use as well as at the splice: the Go side hands this
    # script a shell literal, and the variable it lands in must still reach curl as ONE
    # argv word rather than as however many the value's spaces suggest.
    if ! YOLO_BYPASS_SHIMS=1 curl -fsSL "$URL" -o "$script"; then
        echo "  ⚠ $BIN installer download failed: $URL" >&2
        echo "    (no network, or the endpoint moved — nothing was changed on disk.)" >&2
        rm -f "$script"
        touch "$STAMP"
        return 1
    fi
    # Pure-bash markup sniff: no grep, because grep is a SHIMMED tool in the jail and a
    # launcher must not depend on the block config staying compatible with these flags.
    local head_line
    IFS= read -r head_line < "$script" || true
    shopt -s nocasematch
    if [[ "$head_line" =~ ^[[:space:]]*\<(\!doctype|html|\?xml) ]]; then
        shopt -u nocasematch
        echo "  ⚠ $BIN installer URL is not a shell script — it served a web page." >&2
        echo "    $URL" >&2
        echo "    The pack's install.installerUrl is probably stale; check the tool's docs" >&2
        echo "    for its current install command." >&2
        rm -f "$script"
        touch "$STAMP"
        return 1
    fi
    shopt -u nocasematch
    YOLO_BYPASS_SHIMS=1 bash "$script" 2>&1 || true
    rm -f "$script"
    touch "$STAMP"
    # A receipt only for a run that LEFT SOMETHING, and the same test is the status. An
    # installer can fail loudly (exit 1, nothing downloaded) or quietly (exit 0, the binary
    # under a prefix this launcher never looks at); the LANDING PATH is what separates both
    # from a real install.
    if [ ! -x "$REAL_BIN" ]; then
        return 1
    fi
    # $REAL_BIN twice, and the two arguments are different questions: the fourth is the
    # file to DIGEST (the resolved identity), the sixth is the LANDING PATH (§6's tuple).
    # They coincide here because an installer's only observable output is the binary it
    # left; at the npm funnels they do not. An installer publishes no lockable artifact, so
    # what it LEFT is the only resolved identity there is (§6.3).
    _yolo_receipt __YOLO_RECEIPT_HEAD__ "" "" "$REAL_BIN" "$_ACT" "$REAL_BIN"
    return 0
}

# _do_install is the COLD path: nothing at $REAL_BIN, so try the capture store first and
# fall through to the vendor's installer.
_do_install() {
    local rc=0
    # THE CAPTURE COMES FIRST, before the download and before the stamp. This is the branch
    # the whole subsystem exists for: the second workspace on a machine stops refetching an
    # agent CLI that the first one already paid for (1.2 GB for claude, measured 2026-09-03,
    # and ~/.local is a per-workspace bind).
    if _try_materialize; then
        touch "$STAMP"
    else
        _run_installer || rc=$?
    fi
    if [ "$rc" = 0 ]; then _prune_versions; fi
    return "$rc"
}

# _update is the evergreen act (§3.5): run the pack's DECLARED verb against the installed
# program, or — absent a verb — re-run the vendor's installer, which is OQ-PD14's fallback
# for via: installer.
#
# NO RECEIPT FOR THE VERB BRANCH, deliberately. The verb is the vendor's own updater: it
# decides whether anything moved, to what, and where it put it, and the launcher can observe
# none of that — a receipt written here would be a guess with a timestamp on it. The drift
# it leaves is the RECONCILE's to report, against the bytes on disk rather than against a
# claim (program-delivery.md §6.3, §10 step two). The installer fallback DOES write one,
# because there yolo ran the install itself.
#
# It returns the update's status, for _run_installer's reason, and touches the stamp on
# every outcome — that is the throttle, and a failed attempt must not be retried on the
# next invocation a second later.
_update() {
    local rc=0
    if [ "$HAS_UPDATE_VERB" = "1" ]; then
        echo "  Updating $BIN..." >&2
        _bounded "$REAL_BIN" "${UPDATE_VERB[@]}" >&2 || rc=$?
        touch "$STAMP"
    else
        _ACT=update
        _run_installer || rc=$?
    fi
    if [ "$rc" = 0 ]; then
        _prune_versions
    else
        echo "  ⚠ $BIN: update failed (status $rc) — running the installed version." >&2
    fi
    return "$rc"
}

# _update_due is the throttle, and UPDATES_ENABLED is checked FIRST because it is the one
# gate that is not about time: a jail whose agent_updates policy freezes this pack must
# not even ask what the stamp says.
_update_due() {
    [ "$UPDATES_ENABLED" = "1" ] || return 1
    [ -f "$STAMP" ] || return 0
    [ "$(( $(date +%s) - $(_stamp_mtime "$STAMP") ))" -gt "$UPDATE_INTERVAL" ]
}

# _locked_update holds the install-prefix lock across the whole act, and is the only caller
# on the launch path. Both the "cannot take it" message and the drop live here so the two
# entry points cannot come to disagree about them.
_locked_update() {
    local rc=0
    if ! _take_lock; then
        echo "  $BIN: another update is in progress — running the installed version." >&2
        return 0
    fi
    _update || rc=$?
    _drop_lock
    return "$rc"
}

if [ "${YOLO_PACK_UPDATE:-}" = "1" ]; then
    # Update mode EXITS instead of exec'ing: "yolo pack update" walks every declared
    # program in turn and must refresh them, not launch them. It ignores the stamp AND the
    # policy — a human asked for this one, now, which is the job §3.5 leaves the verb: it
    # is the only way to refresh a pack whose agent_updates is false.
    _rc=0
    if [ ! -x "$REAL_BIN" ]; then
        _do_install || _rc=$?
    elif _take_lock; then
        _update || _rc=$?
        _drop_lock
    else
        echo "  ⚠ $BIN: another update holds the install-prefix lock — nothing refreshed." >&2
        _rc=1
    fi
    exit "$_rc"
fi

if [ ! -x "$REAL_BIN" ]; then
    # Cold home: install, and do not let a failure be the verdict — the -x test at the
    # bottom is, because it answers the question this path actually has (is there something
    # to exec?).
    _do_install || true
elif _update_due; then
    _locked_update || true
fi

# INSTALL AND STOP. See InstallOnlyEnv: yolo capture needs the install this launcher
# performs and must not have the program RUN afterwards, because a first run writes the
# tool own state into the very directories the capture is about to record.
if [ "${` + InstallOnlyEnv + `:-}" = "1" ]; then
    if [ -x "$REAL_BIN" ]; then
        exit 0
    fi
    echo "  ⚠ $BIN not available" >&2
    exit 1
fi

if [ -x "$REAL_BIN" ]; then
    exec "$REAL_BIN" "$@"
else
    echo "  ⚠ $BIN not available" >&2
    exit 1
fi
`

// pkgManagerLauncherTemplate is the per-manager package-manager launcher body. Same splice
// contract as npmLauncherTemplate; this is the template the contract was READ OFF, since its
// stamp dir was the one value in the file already spliced as a quoted literal.
const pkgManagerLauncherTemplate = `#!/bin/bash
set -euo pipefail
export NPM_CONFIG_PREFIX="${NPM_CONFIG_PREFIX:-$HOME/.npm-global}"
export NPM_CONFIG_CACHE="${NPM_CONFIG_CACHE:-$HOME/.cache/npm}"
BIN=__YOLO_BIN__
STAMP_DIR=__YOLO_STAMP_DIR__
STAMP="$STAMP_DIR/$BIN.stamp"
REAL_BIN="$NPM_CONFIG_PREFIX/bin/$BIN"
# Only the install SPEC, deliberately: unlike the agent launcher this body never indexes
# node_modules and never calls "npm view", which are the only two things the bare package
# NAME is good for. Carrying an unread PKG beside it would read as "the name matters here
# too" and invite the next edit to use it in a place only the spec belongs.
SPEC=__YOLO_SPEC__  # what npm install is handed: name@<selector>
RETRY_INTERVAL=3600  # seconds before retrying a failed install
# Baked, never read from the environment: see receiptsFile.
_YOLO_RECEIPTS=__YOLO_RECEIPTS_FILE__

mkdir -p "$STAMP_DIR"
` + stampMtimeFn + receiptShellFns + `
if [ ! -x "$REAL_BIN" ]; then
    # Throttle repeated install attempts after a failure — without this, every
    # invocation would re-hit npm registry when offline / install is broken.
    SHOULD_INSTALL=1
    if [ -f "$STAMP" ]; then
        STAMP_AGE=$(( $(date +%s) - $(_stamp_mtime "$STAMP") ))
        if [ "$STAMP_AGE" -lt "$RETRY_INTERVAL" ]; then
            SHOULD_INSTALL=0
        fi
    fi
    if [ "$SHOULD_INSTALL" = "1" ]; then
        echo "  Installing $SPEC..." >&2
        # The status is CAPTURED, not dropped with "|| true". This install is one of the
        # "every install yolo itself runs" set (§10 step one) and it is the one that fails
        # most often — the RETRY_INTERVAL throttle above exists because offline attempts are
        # routine — so a receipt appended unconditionally would record an install that never
        # happened, on exactly the boots where the record matters. The launch path's verdict
        # is still the -x test below, unchanged: this captures the status to decide whether
        # to RECORD, never whether to proceed.
        pm_rc=0
        YOLO_BYPASS_SHIMS=1 npm install -g --prefer-online "$SPEC" 2>&1 || pm_rc=$?
        if [ "$pm_rc" = 0 ]; then
            # No "resolved": reading the installed version means indexing node_modules by
            # package NAME, and this body deliberately carries only the spec (see above).
            # The omission says "unknown" rather than inventing a version — the same rule
            # _resolved_version follows in the agent launcher.
            _yolo_receipt __YOLO_RECEIPT_HEAD__ "$SPEC" "" "" install "$REAL_BIN"
        fi
        touch "$STAMP"
    fi
fi

if [ -x "$REAL_BIN" ]; then
    exec "$REAL_BIN" "$@"
else
    echo "  ⚠ $BIN not available" >&2
    exit 1
fi
`
