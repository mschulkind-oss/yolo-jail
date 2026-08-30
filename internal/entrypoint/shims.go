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
		realBin := ""
		if name == "grep" || name == "find" {
			realBin = e.ShimBinPath() + "/" + name
		}
		blockFlags := stringList(cfg, "block_flags")

		content := ShimContent(msg, sug, realBin, blockFlags)
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
func ShimContent(msg, sug, realBin string, blockFlags []string) string {
	var lines []string
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
				launcher = npmAgentLauncher(inst, stampDir, receiptsFile(e))
			case "native":
				launcher = nativeAgentLauncher(inst, stampDir, receiptsFile(e))
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
func npmAgentLauncher(inst *packdecl.Install, stampDir, receiptsPath string) string {
	binName := inst.Bin
	pkgName, pkgVersion := splitNpmSpec(inst.Package)
	pinned := "0"
	if npmSpecIsPinned(pkgVersion) {
		pinned = "1"
	}
	extraFlags := strings.Join(inst.Flags, " ")
	if extraFlags != "" {
		extraFlags += " "
	}
	r := strings.NewReplacer(
		"__YOLO_BIN__", binName,
		"__YOLO_PKG__", pkgName,
		"__YOLO_SPEC__", npmInstallSpec(pkgName, pkgVersion),
		"__YOLO_PINNED__", pinned,
		"__YOLO_STAMP_DIR__", stampDir,
		"__YOLO_EXTRA__", extraFlags,
		"__YOLO_RECEIPTS_FILE__", shquote.Quote(receiptsPath),
		"__YOLO_RECEIPT_HEAD__", shquote.Quote(receiptPrefix("npm", binName, inst.Package)),
	)
	return r.Replace(npmLauncherTemplate)
}

func nativeAgentLauncher(inst *packdecl.Install, stampDir, receiptsPath string) string {
	binName := inst.Bin
	installerURL := inst.InstallerURL
	r := strings.NewReplacer(
		"__YOLO_BIN__", binName,
		"__YOLO_URL__", installerURL,
		"__YOLO_STAMP_DIR__", stampDir,
		"__YOLO_RECEIPTS_FILE__", shquote.Quote(receiptsPath),
		"__YOLO_RECEIPT_HEAD__", shquote.Quote(receiptPrefix("installer", binName, installerURL)),
	)
	return r.Replace(nativeLauncherTemplate)
}

// GeneratePackageManagerLaunchers writes lazy npm launchers for package managers not
// pre-installed via mise (pnpm) into the LAUNCHER dir. The stamp dir path is shlex.quote'd
// so a $HOME with shell metacharacters doesn't break the launcher.
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
	stampDirLiteral := shquote.Quote(stampDir)

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
		pkgName, pkgVersion := splitNpmSpec(pm.pkg)
		r := strings.NewReplacer(
			"__YOLO_BIN__", pm.bin,
			"__YOLO_SPEC__", npmInstallSpec(pkgName, pkgVersion),
			"__YOLO_STAMP_DIR_LIT__", stampDirLiteral,
			"__YOLO_RECEIPTS_FILE__", shquote.Quote(receiptsFile(e)),
			// kind "npm" because the RESOLVER is npm — the receipt names the mechanism that
			// did the install, not the declaration's origin. pnpm is declared by this list
			// rather than by a pack, and a reader comparing receipts to bytes has no use for
			// that distinction; a reader looking for "which resolver do I ask about this
			// package" has every use for the kind.
			"__YOLO_RECEIPT_HEAD__", shquote.Quote(receiptPrefix("npm", pm.bin, pm.pkg)),
		)
		if err := writeExecutable(launcherPath, r.Replace(pkgManagerLauncherTemplate)); err != nil {
			return err
		}
	}
	return nil
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
// THE LAUNCHER NEVER RESOLVES A NEW VERSION ON ITS OWN (docs/design/trust-paths.md §1
// row 1, ruled 2026-08-18: "no magical evergreen npm packages"). It installs on first
// use, it reports when the registry has moved, and the ONE input that makes it resolve
// anything after that is YOLO_PACK_UPDATE=1 — which `yolo pack update` sets and nothing
// else does. See _poll_and_report and _update below for the whole of the rule.
const npmLauncherTemplate = `#!/bin/bash
# Lazy-install launcher for __YOLO_BIN__ — installs on first use, not at boot, and never
# resolves a new version unless YOLO_PACK_UPDATE=1 asks it to.
set -euo pipefail
export NPM_CONFIG_PREFIX="${NPM_CONFIG_PREFIX:-$HOME/.npm-global}"
export NPM_CONFIG_CACHE="${NPM_CONFIG_CACHE:-$HOME/.cache/npm}"
STAMP_DIR="__YOLO_STAMP_DIR__"
STAMP="$STAMP_DIR/__YOLO_BIN__.stamp"
SPEC_FILE="$STAMP_DIR/__YOLO_BIN__.spec"
REAL_BIN="$NPM_CONFIG_PREFIX/bin/__YOLO_BIN__"
# PKG is the package NAME alone; SPEC is what "npm install" is handed. They differ whenever
# the pack declared a version, and the two are NOT interchangeable: only PKG may index
# node_modules or be passed to "npm view", and only SPEC may be installed.
PKG="__YOLO_PKG__"
SPEC="__YOLO_SPEC__"
PINNED="__YOLO_PINNED__"   # 1 when the declaration carried a version selector
UPDATE_INTERVAL=3600  # seconds between update CHECKS — a check reports, it never installs
# Baked, never read from the environment: see receiptsFile.
_YOLO_RECEIPTS=__YOLO_RECEIPTS_FILE__

mkdir -p "$STAMP_DIR"
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

# _poll_and_report is the hourly registry check, and it is INFORMATIONAL. It asks the
# registry what its "latest" dist-tag is and PRINTS the answer. It does not install.
#
# It used to be _poll_and_update: same "npm view", then a full "npm install -g" whenever
# the registry had moved. That reinstall is the mechanism docs/design/trust-paths.md §1
# row 1 deletes, and the objection is not that updating is bad — it is that the binary
# changed between two invocations WITH NOBODY PRESENT. A silent change has no act to pin
# to, so no pin, lockfile field or approval prompt can ever cover it; the only fix that
# works is for the timer to stop being an installer. "yolo pack update" is the act that
# replaces it, and the message below names it because a report the reader cannot act on
# is worse than the reinstall it replaces.
#
# The stamp is touched on EVERY check, hit or miss — that is the throttle, and it has to
# be unconditional now that the "hit" branch no longer installs (and so no longer touches
# the stamp via _do_install). Without it the check would fire on every single invocation
# once the registry moved, which is both a network round-trip per launch and the exact
# noise that gets a real notice skimmed past.
_poll_and_report() {
    INSTALLED=$(_installed_version)
    LATEST=$(YOLO_BYPASS_SHIMS=1 npm view "$PKG" version 2>/dev/null || echo "$INSTALLED")
    if [ "$INSTALLED" != "$LATEST" ]; then
        echo "  __YOLO_BIN__ $INSTALLED → $LATEST is available. Run 'yolo pack update' to install it." >&2
    fi
    touch "$STAMP"
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
            echo "  __YOLO_BIN__ is pinned to $SPEC by its pack — nothing to resolve." >&2
        fi
        return
    fi
    if [ ! -x "$REAL_BIN" ]; then
        _do_install
        return
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
        echo "  ⚠ __YOLO_BIN__: could not ask the npm registry for a newer version — leaving $INSTALLED in place." >&2
        return 1
    fi
    if [ "$INSTALLED" = "$LATEST" ]; then
        echo "  __YOLO_BIN__ $INSTALLED is already current." >&2
        touch "$STAMP"
        return 0
    fi
    echo "  Updating __YOLO_BIN__ $INSTALLED → $LATEST..." >&2
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
elif [ ! -f "$STAMP" ]; then
    # First run since jail boot — say whether a newer version exists.
    _poll_and_report
else
    # Check if stamp is stale (older than UPDATE_INTERVAL)
    STAMP_AGE=$(( $(date +%s) - $(_stamp_mtime "$STAMP") ))
    if [ "$STAMP_AGE" -gt "$UPDATE_INTERVAL" ]; then
        _poll_and_report
    fi
fi

if [ -x "$REAL_BIN" ]; then
    exec "$REAL_BIN" "$@"
else
    echo "  ⚠ __YOLO_BIN__ not available" >&2
    exit 1
fi
`

// nativeLauncherTemplate is the native agent launcher body.
const nativeLauncherTemplate = `#!/bin/bash
# Lazy-update launcher for __YOLO_BIN__ — installs/updates on first use, not at boot.
set -euo pipefail
STAMP_DIR="__YOLO_STAMP_DIR__"
STAMP="$STAMP_DIR/__YOLO_BIN__.stamp"
REAL_BIN="$HOME/.local/bin/__YOLO_BIN__"
UPDATE_INTERVAL=3600
# Baked, never read from the environment: see receiptsFile.
_YOLO_RECEIPTS=__YOLO_RECEIPTS_FILE__

mkdir -p "$STAMP_DIR"
` + stampMtimeFn + receiptShellFns + `
_do_install() {
    echo "  Installing __YOLO_BIN__..." >&2
    # Download to a file BEFORE running it, rather than curl | bash. A stale or moved
    # installer endpoint usually keeps answering 200 with a web page, and piping that
    # straight into bash reports the HTML as a bash syntax error plus a curl broken-pipe
    # error — three messages, none naming the wrong URL. Landing it first lets us say so.
    local script
    script="$(mktemp -t __YOLO_BIN__-install-XXXXXX.sh)"
    if ! YOLO_BYPASS_SHIMS=1 curl -fsSL __YOLO_URL__ -o "$script"; then
        echo "  ⚠ __YOLO_BIN__ installer download failed: __YOLO_URL__" >&2
        rm -f "$script"
        touch "$STAMP"
        return
    fi
    # Pure-bash markup sniff: no grep, because grep is a SHIMMED tool in the jail and a
    # launcher must not depend on the block config staying compatible with these flags.
    local head_line
    IFS= read -r head_line < "$script" || true
    shopt -s nocasematch
    if [[ "$head_line" =~ ^[[:space:]]*\<(\!doctype|html|\?xml) ]]; then
        shopt -u nocasematch
        echo "  ⚠ __YOLO_BIN__ installer URL is not a shell script — it served a web page." >&2
        echo "    __YOLO_URL__" >&2
        echo "    The pack's install.installerUrl is probably stale; check the tool's docs" >&2
        echo "    for its current install command." >&2
        rm -f "$script"
        touch "$STAMP"
        return
    fi
    shopt -u nocasematch
    YOLO_BYPASS_SHIMS=1 bash "$script" 2>&1 || true
    # A receipt only for a run that LEFT SOMETHING. _do_install returns 0 down every
    # failure path above — a served web page, a failed download — because on the launch
    # path a failed install is not the verdict, so a receipt written unconditionally here
    # would record an install for exactly the two shapes that installed nothing. The
    # digest is of the binary the vendor produced: an installer publishes no lockable
    # artifact, so what it LEFT is the only resolved identity there is (§6.3).
    if [ -x "$REAL_BIN" ]; then
        # $REAL_BIN twice, and the two arguments are different questions: the fourth is the
        # file to DIGEST (the resolved identity), the sixth is the LANDING PATH (§6's tuple).
        # They coincide here because an installer's only observable output is the binary it
        # left; at the npm funnels they do not.
        _yolo_receipt __YOLO_RECEIPT_HEAD__ "" "" "$REAL_BIN" install "$REAL_BIN"
    fi
    rm -f "$script"
    touch "$STAMP"
}

# THE TWO VENDOR SELF-UPDATES BELOW EMIT NO RECEIPT, deliberately. "$REAL_BIN install"
# is the vendor's own updater: it decides whether anything moved, to what, and where it
# put it, and the launcher cannot observe any of that — a receipt written here would be a
# guess with a timestamp on it. The drift it leaves is the RECONCILE's to report, against
# the bytes on disk rather than against a claim (docs/design/program-delivery.md §6.3,
# §10 step two).
if [ ! -x "$REAL_BIN" ]; then
    _do_install
elif [ ! -f "$STAMP" ]; then
    # First run since boot — try a quick update
    YOLO_BYPASS_SHIMS=1 "$REAL_BIN" install 2>&1 || true
    touch "$STAMP"
else
    STAMP_AGE=$(( $(date +%s) - $(_stamp_mtime "$STAMP") ))
    if [ "$STAMP_AGE" -gt "$UPDATE_INTERVAL" ]; then
        YOLO_BYPASS_SHIMS=1 "$REAL_BIN" install 2>&1 || true
        touch "$STAMP"
    fi
fi

if [ -x "$REAL_BIN" ]; then
    exec "$REAL_BIN" "$@"
else
    echo "  ⚠ __YOLO_BIN__ not available" >&2
    exit 1
fi
`

// pkgManagerLauncherTemplate is the per-manager package-manager launcher body.
const pkgManagerLauncherTemplate = `#!/bin/bash
set -euo pipefail
export NPM_CONFIG_PREFIX="${NPM_CONFIG_PREFIX:-$HOME/.npm-global}"
export NPM_CONFIG_CACHE="${NPM_CONFIG_CACHE:-$HOME/.cache/npm}"
STAMP_DIR=__YOLO_STAMP_DIR_LIT__
STAMP="$STAMP_DIR/__YOLO_BIN__.stamp"
REAL_BIN="$NPM_CONFIG_PREFIX/bin/__YOLO_BIN__"
# Only the install SPEC, deliberately: unlike the agent launcher this body never indexes
# node_modules and never calls "npm view", which are the only two things the bare package
# NAME is good for. Carrying an unread PKG beside it would read as "the name matters here
# too" and invite the next edit to use it in a place only the spec belongs.
SPEC="__YOLO_SPEC__"  # what npm install is handed: name@<selector>
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
    echo "  ⚠ __YOLO_BIN__ not available" >&2
    exit 1
fi
`
