package entrypoint

import (
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// packAliases writes a shell alias for each pack whose install binary has launchFlags,
// so an interactive shell gets the same flags a `yolo -- <bin>` invocation does.
//
// DERIVED rather than declared. It used to be an AgentSpec.Alias string holding a whole
// command line ("copilot --yolo --no-auto-update"), which duplicated the launchFlags the
// same spec already carried — two places to change, and a pack shipping only one of them
// would get a shell alias silently disagreeing with the launcher.
func packAliases(e *Env) string {
	packs, err := LoadJailPacks(e)
	if err != nil {
		return ""
	}
	flagsByBin := packload.LaunchFlagsFor(packs, true)
	var lines []string
	for _, p := range packs {
		// Every honored install, not the first: a pack declaring two programs with
		// launchFlags for both needs two aliases, for the same reason it needs two
		// launchers (shims.go).
		installs, _ := p.HonoredInstalls()
		for _, inst := range installs {
			flags := flagsByBin[inst.Bin]
			if len(flags) == 0 {
				continue
			}
			lines = append(lines, "alias "+inst.Bin+"='"+inst.Bin+" "+strings.Join(flags, " ")+"'")
		}
	}
	return strings.Join(lines, "\n")
}

// YOLO_HOST_DIR (default "unknown"); mise_shims is the MISE_SHIMS path.
func Bashrc(e *Env) string {
	// An absent YOLO_HOST_DIR defaults to "unknown"; an explicit (even empty)
	// value is used verbatim.
	hostDir, ok := e.Lookup("YOLO_HOST_DIR")
	if !ok {
		hostDir = "unknown"
	}
	miseShims := e.MiseShims()
	aliases := packAliases(e)

	var b strings.Builder
	b.WriteString(bashrcPart1)
	b.WriteString(hostDir)
	b.WriteString(bashrcPart2)
	b.WriteString(miseShims)
	b.WriteString(bashrcPart3)
	if aliases != "" {
		b.WriteString(aliases + "\n")
	}
	b.WriteString(bashrcPart4)
	return b.String()
}

// GenerateBashrc writes the .bashrc (truncate-in-place for the bind mount).
func GenerateBashrc(e *Env) error {
	return writeInPlaceString(e.BashrcPath(), Bashrc(e))
}

// The bashrc template is split at the two interpolation points (host_dir and
// mise_shims) and the conditional agent-aliases block.

const bashrcPart1 = `# YOLO Jail Prompt
YELLOW='\[\033[1;33m\]'
RED='\[\033[1;31m\]'
GREEN='\[\033[1;32m\]'
BLUE='\[\033[1;34m\]'
MAGENTA='\[\033[1;35m\]'
CYAN='\[\033[1;36m\]'
NC='\[\033[0m\]'

JAIL_BANNER="${RED}🔒 YOLO-JAIL${NC}"
HOST_INFO="${CYAN}(host: `

const bashrcPart2 = `)${NC}"

export PS1="\n${JAIL_BANNER} ${HOST_INFO}\n${GREEN}jail${NC}:${BLUE}\w${NC}\$ "

# Set terminal/tmux title (only when inside tmux to avoid literal "JAIL" output)
export PROMPT_COMMAND='[ -n "$TMUX" ] && printf "\033]0;JAIL\033\\"'

# Agent-friendly defaults (no pagers, no line numbers)
export PAGER=cat
export BAT_PAGER=""
export BAT_STYLE="plain"
export GIT_PAGER=cat
# EDITOR=cat prevents agents from getting stuck in interactive editors (e.g. git commit).
# VISUAL=nvim is used by interactive tools like Copilot's ctrl-g (edit prompt in editor).
# Standard Unix convention: programs check VISUAL first for full-screen terminals, EDITOR as fallback.
export EDITOR=cat
export VISUAL=nvim
# Disable pi (pi.dev coding agent) install/usage telemetry inside the jail.
export PI_TELEMETRY=0

# Combined CA bundle — baseline Nix cacert + every loophole CA.
# Point every standard TLS trust-store env var at one file so Python
# (ssl, requests, httpx), curl, and git all verify the same roots the
# in-jail broker leafs are signed by.  NODE_EXTRA_CA_CERTS is set by
# the container launcher to just the extras (Node adds them to its own
# bundled roots).  See GenerateCABundle in system.go.
if [ -f "$HOME/.yolo-ca-bundle.crt" ]; then
    export SSL_CERT_FILE="$HOME/.yolo-ca-bundle.crt"
    export REQUESTS_CA_BUNDLE="$HOME/.yolo-ca-bundle.crt"
    export CURL_CA_BUNDLE="$HOME/.yolo-ca-bundle.crt"
    export GIT_SSL_CAINFO="$HOME/.yolo-ca-bundle.crt"
fi

# Source user-defined env vars from config (defaults, overridable by .env).
# Loaded early so mise activation can override with .env values.
[ -f "$HOME/.config/yolo-user-env.sh" ] && . "$HOME/.config/yolo-user-env.sh"

# PATH with npm-global and go binaries
export NPM_CONFIG_PREFIX="${NPM_CONFIG_PREFIX:-$HOME/.npm-global}"
export NPM_CONFIG_CACHE="${NPM_CONFIG_CACHE:-$HOME/.cache/npm}"
export GOPATH="${GOPATH:-$HOME/go}"
# Two generated dirs, at OPPOSITE ends of PATH, because they are different mechanisms:
#   BLOCK_DIR    — blocked-tool blockers (grep, find). Interception is the whole job, so
#                  they must PRECEDE the real binary.
#   LAUNCH_DIR   — lazy installers (claude, pnpm). They only need to run when nothing
#                  else provides the name, so they go LAST, after /bin. That is what
#                  makes a pack declaring "program fzf" unable to shadow /bin/fzf.
BLOCK_DIR="${HOME}/.yolo/bin/block"
LAUNCH_DIR="${HOME}/.yolo/bin/launch"
export PATH="$BLOCK_DIR:$HOME/.local/bin:$NPM_CONFIG_PREFIX/bin:`

const bashrcPart3 = `:$GOPATH/bin:/bin:/usr/bin:$LAUNCH_DIR"

# Activate mise with shell hooks (interactive shells only).
# Non-interactive shells (bash -lc) skip activation to avoid a deadlock:
# mise hook-env holds a lock then spawns uv via the mise shim (which IS mise),
# re-entering mise locking. The caller's eval "$(mise env ...)" already set up
# the environment before spawning this shell.
if [[ $- == *i* ]]; then
    eval "$(mise activate bash)"
fi
# NO mise-trust call here. MISE_TRUSTED_CONFIG_PATHS=/workspace already trusts every config
# under the workspace, on its own, with no on-disk mark — see boot.go's "Workspace mise
# trust — REMOVED" note for why the mark was worse than redundant.

# Aliases
alias ls='ls --color=auto'
alias ll='ls -alF'
`

const bashrcPart4 = `# Agent YOLO flags: copilot gets a --yolo alias above (when selected);
# claude gets --dangerously-skip-permissions injected by the CLI (with
# IS_SANDBOX=1 to bypass the root check); opencode/pi auto-approve via their
# own config files.
alias vi='nvim'
alias vim='nvim'
alias bat='bat --style=plain --paging=never'
`

// GenerateBootstrapScript writes ~/.yolo-bootstrap.sh (chmod |= S_IEXEC).
func GenerateBootstrapScript(e *Env) error {
	return writeExecutable(bootstrapPath(e), BootstrapScript(e))
}

func bootstrapPath(e *Env) string { return e.Home + "/.yolo-bootstrap.sh" }

// interpolation is the mise_shims path in the PATH export line, the preset-gated MCP npm
// package list, and the receipt sentinels (a baked path plus the three constant JSON heads —
// the MCP and LSP loops' bin/declared come from the environment at run time, so only the
// kind can be rendered here; see receiptPrefix).
func BootstrapScript(e *Env) string {
	r := strings.NewReplacer(
		"__YOLO_MISE_SHIMS__", e.MiseShims(),
		"__YOLO_MCP_NPM_PACKAGES__", mcpPresetNpmPackages(e),
		"__YOLO_RECEIPTS_FILE__", shquote.Quote(receiptsFile(e)),
		"__YOLO_RECEIPT_LSP_NPM__", shquote.Quote(receiptPrefix("lsp-npm", "", "")),
		"__YOLO_RECEIPT_LSP_GO__", shquote.Quote(receiptPrefix("lsp-go", "", "")),
		"__YOLO_RECEIPT_MCP_NPM__", shquote.Quote(receiptPrefix("mcp-npm", "", "")),
	)
	return r.Replace(bootstrapTemplate)
}

// mcpPresetNpmPackages returns the npm packages the ENABLED MCP presets need, space
// separated, or "" when none are enabled (D6).
//
// This used to be an unconditional `npm install -g chrome-devtools-mcp
// @modelcontextprotocol/server-sequential-thinking`, which ran in EVERY jail whether
// anything wanted those servers or not. Probed on an empty-agent jail: 112 npm
// packages installed for zero agents and zero configured presets.
//
// Gating on the presets that actually asked for them is the whole fix: an MCP preset
// is already config data (YOLO_MCP_PRESETS), so the install should follow the same
// declaration the server table does rather than being hardcoded beside it. That also
// makes the eventual move to a pack contribution a change of SOURCE rather than a
// change of mechanism.
func mcpPresetNpmPackages(e *Env) string {
	var pkgs []string
	for _, preset := range e.LoadMCPPresetNames() {
		switch preset {
		case "chrome-devtools":
			pkgs = append(pkgs, "chrome-devtools-mcp")
		case "sequential-thinking":
			pkgs = append(pkgs, "@modelcontextprotocol/server-sequential-thinking")
		}
	}
	return strings.Join(pkgs, " ")
}

// bootstrapTemplate is the body of the bootstrap script.
const bootstrapTemplate = `#!/bin/bash
export NPM_CONFIG_PREFIX="${NPM_CONFIG_PREFIX:-$HOME/.npm-global}"
export NPM_CONFIG_CACHE="${NPM_CONFIG_CACHE:-$HOME/.cache/npm}"
export GOPATH="${GOPATH:-$HOME/go}"
export GOBIN="$GOPATH/bin"
export PATH="$HOME/.local/bin:$NPM_CONFIG_PREFIX/bin:__YOLO_MISE_SHIMS__:$GOBIN:$PATH"
# Baked, never read from the environment: see receiptsFile.
_YOLO_RECEIPTS=__YOLO_RECEIPTS_FILE__
` + receiptShellFns + `
# The npm/go resolvers' "resolved identity" readers. Both may come back empty — a missing jq,
# a binary built without module info — and an empty answer omits the field rather than
# inventing one. _yolo_lsp_npm_version keeps its name and serves the MCP arm too: both are
# the same question ("what version of <pkg> is in the global prefix?") asked of the same
# resolver, and a second copy under a second name is how the two answers drift apart.
_yolo_lsp_npm_version() {
    local v
    v=$(jq -r '.version' "$NPM_CONFIG_PREFIX/lib/node_modules/$1/package.json" 2>/dev/null) || return 0
    # jq prints "null" for an absent key, which is not a version.
    if [ "$v" != "null" ]; then printf '%s\n' "$v"; fi
    return 0
}

# ` + "`" + `go version -m <bin>` + "`" + ` prints one tab-indented ` + "`" + `mod` + "`" + ` row: the literal "mod", the
# module path, the version, then the checksum. The leading tab does NOT produce an empty
# first field — tab is IFS whitespace, so read strips the leading run.
_yolo_go_module_version() {
    local out tag modpath ver rest
    out=$(YOLO_BYPASS_SHIMS=1 go version -m "$1" 2>/dev/null) || return 0
    while IFS=$'\t' read -r tag modpath ver rest; do
        if [ "$tag" = "mod" ]; then printf '%s\n' "$ver"; return 0; fi
    done <<< "$out"
    return 0
}

# Initialize font cache (once, not on every shell session)
fc-cache -f >/dev/null 2>&1

# Agent CLIs (copilot, claude, codex) are NOT installed here.
# Lazy-install launchers in ~/.yolo/bin/launch/ install them on first use, keeping boot
# fast.  They no longer update themselves on a timer — "yolo pack update" is the act that
# resolves a new version.  Only MCP/LSP tools that agents depend on are installed here.

# --- MCP preset tools (gated on the ENABLED presets, D6) ----------------
# Empty when no preset needs an npm package, so a jail that wants none installs
# nothing.  This was previously unconditional: 112 npm packages in every jail,
# including one with no agents and no presets at all.
YOLO_MCP_NPM="__YOLO_MCP_NPM_PACKAGES__"
if [ -n "$YOLO_MCP_NPM" ]; then
    # Reinstall only when something is actually missing, so a warm jail is fast.
    missing=""
    for pkg in $YOLO_MCP_NPM; do
        case "$pkg" in
            chrome-devtools-mcp) bin=chrome-devtools-mcp ;;
            *server-sequential-thinking) bin=mcp-server-sequential-thinking ;;
            *) bin="" ;;
        esac
        if [ -n "$bin" ] && ! command -v "$bin" >/dev/null; then missing="yes"; fi
    done
    if [ -n "$missing" ]; then
        echo "  Installing MCP tools..." >&2
        # Clean stale npm temp directories that cause ENOTEMPTY on rename.
        # maxdepth 2 catches both top-level and scoped (@org/) packages.
        find "$NPM_CONFIG_PREFIX/lib/node_modules" -maxdepth 2 -name '.*' -type d 2>/dev/null | xargs rm -rf
        # The status is CAPTURED, never dropped with "|| true": this is one of the installs
        # §10 step one's "every install yolo itself runs" covers, and a receipt appended
        # after an unconditional success records installs that never happened — an offline
        # boot fails here routinely and simply retries next launch.
        mcp_rc=0
        YOLO_BYPASS_SHIMS=1 npm install -g $YOLO_MCP_NPM || mcp_rc=$?
        if [ "$mcp_rc" = 0 ]; then
            # ONE LINE PER PACKAGE, not one per npm invocation. The install is a single
            # command over the whole list because that is what npm is good at, but the
            # receipt's unit is a package: a reader asking "where did
            # @modelcontextprotocol/server-sequential-thinking come from" must find a line
            # naming it, not a line naming a set it happens to be in.
            for pkg in $YOLO_MCP_NPM; do
                _yolo_receipt "$(_yolo_head __YOLO_RECEIPT_MCP_NPM__ '' "$pkg")" \
                    "" "$(_yolo_lsp_npm_version "$pkg")" "" install
            done
        fi
    fi
fi

# --- LSP installs (gated on workspace config) ---------------------------
# Sentinel records what we installed last boot, so we can uninstall on
# removal.  Format: one ` + "``" + `kind:identifier` + "``" + ` per line, e.g.
# ` + "``" + `npm:pyright` + "``" + ` / ` + "``" + `go:github.com/isaacphi/mcp-language-server` + "``" + `.
SENTINEL="$HOME/.yolo-installed-lsps"
prev=""
[ -f "$SENTINEL" ] && prev=$(cat "$SENTINEL")
desired=""
for pkg in $(printf '%s\n' "${YOLO_LSP_NPM_INSTALL:-}" | sed '/^$/d'); do
    desired="${desired}npm:${pkg}\n"
done
for pkg in $(printf '%s\n' "${YOLO_LSP_GO_INSTALL:-}" | sed '/^$/d'); do
    desired="${desired}go:${pkg}\n"
done
desired=$(printf "$desired")

# Install anything in desired that isn't already installed.
echo "$desired" | while IFS= read -r entry; do
    [ -z "$entry" ] && continue
    kind=${entry%%:*}
    pkg=${entry#*:}
    case "$kind" in
        npm)
            # Probe via npm ls -g; faster than ` + "`" + `command -v` + "`" + ` when the bin name doesn't match the pkg name.
            if ! YOLO_BYPASS_SHIMS=1 npm ls -g --depth=0 "$pkg" >/dev/null 2>&1; then
                echo "  Installing npm: $pkg" >&2
                # The status is CAPTURED, not dropped with "|| true". A receipt appended
                # after an unconditional success records an install that may never have
                # happened, and this is the one loop whose failures are routine (an
                # offline boot retries the whole set next launch).
                lsp_rc=0
                YOLO_BYPASS_SHIMS=1 npm install -g --prefer-online "$pkg" 2>&1 || lsp_rc=$?
                if [ "$lsp_rc" = 0 ]; then
                    # Appended INSIDE the arm: this loop reads from a pipe, so it runs in
                    # a subshell and no state it accumulates would survive the "done".
                    _yolo_receipt "$(_yolo_head __YOLO_RECEIPT_LSP_NPM__ '' "$pkg")" \
                        "" "$(_yolo_lsp_npm_version "$pkg")" "" install
                fi
            fi
            ;;
        go)
            # ` + "``" + `go install pkg@ver` + "``" + ` is idempotent but slow; skip if the bin already exists.
            # Strip ` + "``" + `@version` + "``" + ` to derive the binary name from the last path segment.
            base=${pkg%@*}
            bin=${base##*/}
            if [ ! -f "$GOBIN/$bin" ]; then
                if command -v go >/dev/null; then
                    echo "  Installing go: $pkg" >&2
                    mkdir -p "$GOBIN"
                    lsp_rc=0
                    YOLO_BYPASS_SHIMS=1 go install "$pkg" 2>&1 || lsp_rc=$?
                    if [ "$lsp_rc" = 0 ]; then
                        _yolo_receipt "$(_yolo_head __YOLO_RECEIPT_LSP_GO__ "$bin" "$pkg")" \
                            "" "$(_yolo_go_module_version "$GOBIN/$bin")" "" install
                    fi
                else
                    echo "  ⚠ go not found, skipping $pkg" >&2
                fi
            fi
            ;;
    esac
done

# Uninstall anything in prev that's no longer in desired (workspace
# dropped an LSP between boots).
echo "$prev" | while IFS= read -r entry; do
    [ -z "$entry" ] && continue
    if ! printf '%s\n' "$desired" | grep -qxF "$entry"; then
        kind=${entry%%:*}
        pkg=${entry#*:}
        case "$kind" in
            npm)
                echo "  Uninstalling npm: $pkg (no longer configured)" >&2
                YOLO_BYPASS_SHIMS=1 npm uninstall -g "$pkg" 2>&1 || true
                ;;
            go)
                base=${pkg%@*}
                bin=${base##*/}
                if [ -f "$GOBIN/$bin" ]; then
                    echo "  Removing go binary: $bin (no longer configured)" >&2
                    rm -f "$GOBIN/$bin"
                fi
                ;;
        esac
    fi
done

# Persist the new sentinel.
printf '%s\n' "$desired" > "$SENTINEL"

# NOTE: an unconditional 'pip install showboat' used to live here. It is GONE, deliberately —
# do not add another ungated tool install to this script. Every other install above is
# config-gated (mcp presets, lsp_servers) or pack-declared, probes for what it needs, and
# tolerates failure; showboat was the only one that did none of that, and being the LAST
# command it turned a missing 'pip' into "PROVISIONING FAILED" on every boot (PR #29).
# Nothing in the repo consumed it. If a tool is wanted in the image, the mechanisms are
# 'packages:' (baked) or a pack's 'requires'/'program' contribution — not this file.
`

// GenerateVenvPrecreateScript writes ~/.yolo-venv-precreate.sh (chmod |= S_IEXEC).
func GenerateVenvPrecreateScript(e *Env) error {
	return writeExecutable(e.Home+"/.yolo-venv-precreate.sh", venvPrecreateScript)
}

// venvPrecreateScript is the venv-precreate script body (no interpolation).
const venvPrecreateScript = `#!/bin/bash
# Pre-create python venvs to avoid a mise shim deadlock.
# When _.python.venv={create:true} is configured, mise hook-env spawns
# uv via the mise shim (which IS /bin/mise), re-entering mise's flock
# and deadlocking.  Creating the venv beforehand with the real uv binary
# means mise finds it already exists and skips the uv call.

[ -f /workspace/mise.toml ] || [ -f /workspace/.mise.toml ] || \
    [ -f /workspace/mise.jail.toml ] || [ -f /workspace/.mise.jail.toml ] || exit 0

# Get real binary paths (not shims) — requires mise install to have run
_uv=$(mise which uv 2>/dev/null) || exit 0
_py=$(mise which python 2>/dev/null) || exit 0
[ -n "$_uv" ] && [ -n "$_py" ] || exit 0

# Parse the venv path from mise config.  Every jail exports
# MISE_ENV=jail, so the jail pair (mise.jail.toml/.mise.jail.toml)
# overrides the base pair; within each pair the dotted file wins (it
# loads later).  Read highest-priority first, first hit wins.
_vp=$(/bin/python3 -c "
import re, sys, tomllib

def venv_value(path):
    try:
        with open(path, 'rb') as f:
            v = tomllib.load(f).get('env')
    except Exception:
        return None
    for key in ('_', 'python', 'venv'):
        if not isinstance(v, dict):
            return None
        v = v.get(key)
    return v

root = sys.argv[1]
v = None
for name in ('.mise.jail.toml', 'mise.jail.toml', '.mise.toml', 'mise.toml'):
    v = venv_value(root + '/' + name)
    if v is not None:
        break
if isinstance(v, dict):
    if not v.get('create', False):
        sys.exit(1)
    v = v.get('path', '.venv')
if not isinstance(v, str):
    sys.exit(1)
# Resolve the one tera template we can (config_root == /workspace);
# any other template is unresolvable here — skip pre-creation.
v = re.sub(r'^\{\{\s*config_root\s*\}\}/', '', v)
if '{{' in v or '{%' in v:
    sys.exit(1)
print(v)
" /workspace 2>/dev/null) || exit 0

# The per-side venv shadow mount materializes an empty dir, and a pre-split
# venv may point at an interpreter path that no longer resolves — a bare -d
# test would wrongly skip both.  Only a pyvenv.cfg whose 'home =' dir still
# exists counts as a live venv; anything else is (re)created.  --clear is
# what makes the heal work: without it uv refuses to reuse an existing
# venv dir.  It empties the dir in place (same inode), which is the only
# safe move when /workspace/<path> is the shadow mountpoint itself.
if [ -f "/workspace/$_vp/pyvenv.cfg" ]; then
    _home=$(sed -n 's/^home *= *//p' "/workspace/$_vp/pyvenv.cfg" | head -n 1)
    [ -n "$_home" ] && [ -d "$_home" ] && exit 0
fi
# stderr kept: creation failures must reach the startup log.
"$_uv" venv --clear "/workspace/$_vp" --python "$_py" || true
`
