package entrypoint

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/agents"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// GenerateShims mirrors shims.generate_shims: it rmtree's SHIM_DIR, recreates
// it, and writes one blocking/filtering shim per entry in YOLO_BLOCK_CONFIG.
// An absent/empty/unparseable config leaves an empty SHIM_DIR (matching the
// Python early returns).
//
// The shim body is the frozen argv-filter contract: message/suggestion text +
// exit code 127. See ShimContent for the exact grammar.
func GenerateShims(e *Env) error {
	shimDir := e.ShimDir()
	// Use RemoveAll to handle races when multiple jails start concurrently and
	// both try to rmtree the same shared home directory (Python: shutil.rmtree
	// with ignore_errors=True).
	_ = os.RemoveAll(shimDir)
	if err := os.MkdirAll(shimDir, 0o755); err != nil {
		return err
	}

	blockJSON := e.Getenv("YOLO_BLOCK_CONFIG")
	if blockJSON == "" {
		return nil
	}
	decoded, err := jsonx.Decode([]byte(blockJSON))
	if err != nil {
		// json.JSONDecodeError / TypeError -> return (no shims).
		return nil
	}
	config, ok := decoded.([]any)
	if !ok {
		// Python would iterate a dict's keys and then crash on .get; a non-list
		// config never occurs in real YOLO_BLOCK_CONFIG (always a JSON array),
		// so we decline to act rather than reproduce a crash.
		return nil
	}

	for _, item := range config {
		cfg, ok := item.(*jsonx.OrderedMap)
		if !ok {
			// Non-object entry: Python's tool_cfg.get would AttributeError.
			// Real configs are arrays of objects; skip defensively.
			continue
		}
		name, ok := stringValue(cfg, "name")
		if !ok || name == "" {
			continue // Python: `if not name: continue`
		}
		// Python default: f"Error: tool {name} is blocked in this project."
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
			realBin = "/bin/" + name
		}
		blockFlags := stringList(cfg, "block_flags")

		content := ShimContent(msg, sug, realBin, blockFlags)
		shimPath := filepath.Join(shimDir, name)
		if err := writeExecutable(shimPath, content); err != nil {
			return err
		}
	}
	return nil
}

// ShimContent renders the shim script body byte-for-byte as shims.generate_shims
// does. Two flavors:
//
//   - Filter shim (blockFlags non-empty AND realBin set): inspect argv against
//     the glob patterns and only exit 127 when one matches, else exec the real
//     binary. Long-option exact matches (--foo) come first, then a `--*` skip
//     so unrelated long options pass, then the short patterns.
//   - Unconditional block: exit 127 with the message (and exec realBin after,
//     only if realBin is set).
//
// msg/sug are embedded verbatim inside `echo "..."` — no shell escaping, exactly
// as Python's f-strings do (the frozen contract).
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

// GenerateAgentLaunchers mirrors shims.generate_agent_launchers: lazy-update
// wrappers for the SELECTED agents (YOLO_AGENTS). Skips writing when a shim of
// the same name already exists (a blocked-tool shim from GenerateShims, which
// runs first). npm vs native launcher body is driven by the agent's InstallSpec.
func GenerateAgentLaunchers(e *Env) error {
	shimDir := e.ShimDir()
	if err := os.MkdirAll(shimDir, 0o755); err != nil {
		return err
	}
	stampDir := filepath.Join(e.Home, ".cache", "yolo-agent-stamps")

	for _, name := range LoadAgents(e) {
		spec, ok := agents.Get(name)
		if !ok {
			continue
		}
		binName := spec.Install.Bin
		shimPath := filepath.Join(shimDir, binName)
		if pathExists(shimPath) {
			continue // don't overwrite a blocked-tool shim
		}
		var launcher string
		switch spec.Install.Kind {
		case "npm":
			launcher = npmAgentLauncher(spec, stampDir)
		case "native":
			launcher = nativeAgentLauncher(spec, stampDir)
		default:
			continue
		}
		if err := writeExecutable(shimPath, launcher); err != nil {
			return err
		}
	}
	return nil
}

// npmAgentLauncher mirrors shims._npm_agent_launcher.
func npmAgentLauncher(spec agents.AgentSpec, stampDir string) string {
	binName := spec.Install.Bin
	pkgName := spec.Install.Package
	extraFlags := strings.Join(spec.Install.InstallFlags, " ")
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

// nativeAgentLauncher mirrors shims._native_agent_launcher.
func nativeAgentLauncher(spec agents.AgentSpec, stampDir string) string {
	binName := spec.Install.Bin
	installerURL := spec.Install.InstallerURL
	r := strings.NewReplacer(
		"__YOLO_BIN__", binName,
		"__YOLO_URL__", installerURL,
		"__YOLO_STAMP_DIR__", stampDir,
	)
	return r.Replace(nativeLauncherTemplate)
}

// GeneratePackageManagerLaunchers mirrors shims.generate_package_manager_launchers:
// lazy npm launchers for package managers not pre-installed via mise (pnpm).
// The stamp dir path is shlex.quote'd so a $HOME with shell metacharacters
// doesn't break the launcher.
func GeneratePackageManagerLaunchers(e *Env) error {
	shimDir := e.ShimDir()
	if err := os.MkdirAll(shimDir, 0o755); err != nil {
		return err
	}
	stampDir := filepath.Join(e.Home, ".cache", "yolo-package-manager-stamps")
	stampDirLiteral := shquote.Quote(stampDir)

	// Python: npm_package_managers = {"pnpm": "pnpm"} (single entry).
	for _, pm := range []struct{ bin, pkg string }{{"pnpm", "pnpm"}} {
		shimPath := filepath.Join(shimDir, pm.bin)
		if pathExists(shimPath) {
			continue
		}
		r := strings.NewReplacer(
			"__YOLO_BIN__", pm.bin,
			"__YOLO_PKG__", pm.pkg,
			"__YOLO_STAMP_DIR_LIT__", stampDirLiteral,
		)
		if err := writeExecutable(shimPath, r.Replace(pkgManagerLauncherTemplate)); err != nil {
			return err
		}
	}
	return nil
}

// npmLauncherTemplate is the byte-exact body of shims._npm_agent_launcher with
// the f-string fields replaced by __YOLO_*__ sentinels.
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

// nativeLauncherTemplate is the byte-exact body of shims._native_agent_launcher.
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
    YOLO_BYPASS_SHIMS=1 curl -fsSL __YOLO_URL__ | bash 2>&1 || true
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

// pkgManagerLauncherTemplate is the byte-exact body of
// shims.generate_package_manager_launchers' per-manager launcher.
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
