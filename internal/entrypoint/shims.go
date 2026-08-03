package entrypoint

import (
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
// Both generated dirs (~/.yolo-shims and ~/.yolo-launchers) are bind-mount ANCHORS,
// mounted from <ws>/.yolo/home/{yolo-shims,yolo-launchers} while their parent /home/agent
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
// SHIM_DIR (~/.yolo-shims) is the BLOCKER dir and is ordered FIRST on PATH. The lazy
// installers live in ~/.yolo-launchers, ordered LAST — see Env.LauncherDir.
func GenerateShims(e *Env) error {
	if err := resetAnchorDir(e.ShimDir()); err != nil {
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
		shimPath := filepath.Join(e.ShimDir(), name)
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
// contribution into the LAUNCHER dir (~/.yolo-launchers), which is ordered LAST on PATH.
// npm vs native launcher body is driven by the pack's install declaration.
//
// It no longer skips a name a blocked-tool shim owns, and that is the point of the split
// rather than an omission. The two dirs cannot collide, so a tool that is BOTH blocked and
// declared as a pack `program` gets a blocker in ~/.yolo-shims (first on PATH) and a
// launcher in ~/.yolo-launchers (last) — and the blocker wins by position, which is the
// correct outcome. Previously the launcher was simply never written, so a config that later
// unblocked the tool left it with no installer until the next boot.
//
// This is also the FIRST generator to write into the launcher dir, so it owns the
// contents-only reset (the same contract GenerateShims has for the shim dir); the
// package-manager launchers below are additive and must run after it.
func GenerateAgentLaunchers(e *Env) error {
	launcherDir := e.LauncherDir()
	if err := resetAnchorDir(launcherDir); err != nil {
		return err
	}
	stampDir := filepath.Join(e.Home, ".cache", "yolo-agent-stamps")

	packs, err := LoadJailPacks(e)
	if err != nil {
		return err
	}
	for _, p := range packs {
		// HonoredInstall applies the ORIGIN gate: a fetched pack cannot introduce a
		// curl-piped installer, because that would let a git ref run arbitrary code in the
		// jail. The refusal was already reported at staging time on the host.
		inst, _ := p.HonoredInstall()
		if inst == nil {
			continue
		}
		launcherPath := filepath.Join(launcherDir, inst.Bin)
		if pathExists(launcherPath) {
			// Two packs claiming one bin name. The footprint check refuses this on the
			// host (`program` is CombineExclusive by bin); first-writer-wins keeps the
			// jail deterministic if it ever gets here anyway.
			continue
		}
		var launcher string
		switch inst.Kind {
		case "npm":
			launcher = npmAgentLauncher(inst, stampDir)
		case "native":
			launcher = nativeAgentLauncher(inst, stampDir)
		default:
			continue
		}
		if err := writeExecutable(launcherPath, launcher); err != nil {
			return err
		}
	}
	return nil
}

func npmAgentLauncher(inst *packdecl.Install, stampDir string) string {
	binName := inst.Bin
	pkgName := inst.Package
	extraFlags := strings.Join(inst.Flags, " ")
	if extraFlags != "" {
		extraFlags += " "
	}
	r := strings.NewReplacer(
		"__YOLO_BIN__", binName,
		"__YOLO_PKG__", pkgName,
		"__YOLO_STAMP_DIR__", stampDir,
		"__YOLO_EXTRA__", extraFlags,
	)
	return r.Replace(npmLauncherTemplate)
}

func nativeAgentLauncher(inst *packdecl.Install, stampDir string) string {
	binName := inst.Bin
	installerURL := inst.InstallerURL
	r := strings.NewReplacer(
		"__YOLO_BIN__", binName,
		"__YOLO_URL__", installerURL,
		"__YOLO_STAMP_DIR__", stampDir,
	)
	return r.Replace(nativeLauncherTemplate)
}

// GeneratePackageManagerLaunchers writes lazy npm launchers for package managers not
// pre-installed via mise (pnpm) into the LAUNCHER dir. The stamp dir path is shlex.quote'd
// so a $HOME with shell metacharacters doesn't break the launcher.
//
// MkdirAll-only (no clear): GenerateAgentLaunchers owns the dir reset and runs first, so
// clearing here would delete the launchers it just wrote.
func GeneratePackageManagerLaunchers(e *Env) error {
	launcherDir := e.LauncherDir()
	if err := os.MkdirAll(launcherDir, 0o755); err != nil {
		return err
	}
	stampDir := filepath.Join(e.Home, ".cache", "yolo-package-manager-stamps")
	stampDirLiteral := shquote.Quote(stampDir)

	// The only lazily-installed package manager is pnpm.
	for _, pm := range []struct{ bin, pkg string }{{"pnpm", "pnpm"}} {
		launcherPath := filepath.Join(launcherDir, pm.bin)
		if pathExists(launcherPath) {
			continue // a pack already claimed this bin name
		}
		r := strings.NewReplacer(
			"__YOLO_BIN__", pm.bin,
			"__YOLO_PKG__", pm.pkg,
			"__YOLO_STAMP_DIR_LIT__", stampDirLiteral,
		)
		if err := writeExecutable(launcherPath, r.Replace(pkgManagerLauncherTemplate)); err != nil {
			return err
		}
	}
	return nil
}

// npmLauncherTemplate is the npm agent launcher body, with the per-agent
// fields replaced by __YOLO_*__ sentinels.
const npmLauncherTemplate = `#!/bin/bash
# Lazy-update launcher for __YOLO_BIN__ — installs/updates on first use, not at boot.
set -euo pipefail
export NPM_CONFIG_PREFIX="${NPM_CONFIG_PREFIX:-$HOME/.npm-global}"
export NPM_CONFIG_CACHE="${NPM_CONFIG_CACHE:-$HOME/.cache/npm}"
STAMP_DIR="__YOLO_STAMP_DIR__"
STAMP="$STAMP_DIR/__YOLO_BIN__.stamp"
REAL_BIN="$NPM_CONFIG_PREFIX/bin/__YOLO_BIN__"
PKG="__YOLO_PKG__"
UPDATE_INTERVAL=3600  # seconds between update checks

mkdir -p "$STAMP_DIR"

_do_install() {
    echo "  Installing $PKG..." >&2
    # Clean stale npm temp dirs that cause ENOTEMPTY
    rm -rf "$NPM_CONFIG_PREFIX"/lib/node_modules/${PKG%%/*}/.${PKG##*/}-* 2>/dev/null
    YOLO_BYPASS_SHIMS=1 npm install -g __YOLO_EXTRA__--prefer-online "$PKG@latest" 2>&1 || true
    touch "$STAMP"
}

if [ ! -x "$REAL_BIN" ]; then
    _do_install
elif [ ! -f "$STAMP" ]; then
    # First run since jail boot — check if update needed
    INSTALLED=$(jq -r '.version' "$NPM_CONFIG_PREFIX/lib/node_modules/$PKG/package.json" 2>/dev/null || echo "0")
    LATEST=$(YOLO_BYPASS_SHIMS=1 npm view "$PKG" version 2>/dev/null || echo "$INSTALLED")
    if [ "$INSTALLED" != "$LATEST" ]; then
        echo "  Updating __YOLO_BIN__ $INSTALLED → $LATEST..." >&2
        _do_install
    else
        touch "$STAMP"
    fi
else
    # Check if stamp is stale (older than UPDATE_INTERVAL)
    STAMP_AGE=$(( $(date +%s) - $(stat -c %Y "$STAMP" 2>/dev/null || echo 0) ))
    if [ "$STAMP_AGE" -gt "$UPDATE_INTERVAL" ]; then
        INSTALLED=$(jq -r '.version' "$NPM_CONFIG_PREFIX/lib/node_modules/$PKG/package.json" 2>/dev/null || echo "0")
        LATEST=$(YOLO_BYPASS_SHIMS=1 npm view "$PKG" version 2>/dev/null || echo "$INSTALLED")
        if [ "$INSTALLED" != "$LATEST" ]; then
            echo "  Updating __YOLO_BIN__ $INSTALLED → $LATEST..." >&2
            _do_install
        else
            touch "$STAMP"
        fi
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

mkdir -p "$STAMP_DIR"

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
    rm -f "$script"
    touch "$STAMP"
}

if [ ! -x "$REAL_BIN" ]; then
    _do_install
elif [ ! -f "$STAMP" ]; then
    # First run since boot — try a quick update
    YOLO_BYPASS_SHIMS=1 "$REAL_BIN" install 2>&1 || true
    touch "$STAMP"
else
    STAMP_AGE=$(( $(date +%s) - $(stat -c %Y "$STAMP" 2>/dev/null || echo 0) ))
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
PKG="__YOLO_PKG__"
RETRY_INTERVAL=3600  # seconds before retrying a failed install

mkdir -p "$STAMP_DIR"

if [ ! -x "$REAL_BIN" ]; then
    # Throttle repeated install attempts after a failure — without this, every
    # invocation would re-hit npm registry when offline / install is broken.
    SHOULD_INSTALL=1
    if [ -f "$STAMP" ]; then
        STAMP_AGE=$(( $(date +%s) - $(stat -c %Y "$STAMP" 2>/dev/null || echo 0) ))
        if [ "$STAMP_AGE" -lt "$RETRY_INTERVAL" ]; then
            SHOULD_INSTALL=0
        fi
    fi
    if [ "$SHOULD_INSTALL" = "1" ]; then
        echo "  Installing $PKG..." >&2
        YOLO_BYPASS_SHIMS=1 npm install -g --prefer-online "$PKG@latest" 2>&1 || true
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
