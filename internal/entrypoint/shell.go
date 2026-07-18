package entrypoint

import (
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/agents"
)

// agentAliases mirrors shell._agent_aliases: bashrc `alias <bin>='<rhs>'` lines
// for the SELECTED agents that declare an alias (gemini, copilot today).
func agentAliases(e *Env) string {
	var lines []string
	for _, name := range LoadAgents(e) {
		spec, ok := agents.Get(name)
		if ok && spec.Alias != "" {
			lines = append(lines, "alias "+spec.Install.Bin+"='"+spec.Alias+"'")
		}
	}
	return strings.Join(lines, "\n")
}

// Bashrc mirrors shell.generate_bashrc's content assembly. host_dir comes from
// YOLO_HOST_DIR (default "unknown"); mise_shims is the MISE_SHIMS path.
func Bashrc(e *Env) string {
	// Python: os.environ.get("YOLO_HOST_DIR", "unknown") — absent key defaults
	// to "unknown"; an explicit (even empty) value is used verbatim.
	hostDir, ok := e.Lookup("YOLO_HOST_DIR")
	if !ok {
		hostDir = "unknown"
	}
	miseShims := e.MiseShims()
	aliases := agentAliases(e)

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
// mise_shims) and the conditional agent-aliases block, byte-exact with
// shell.generate_bashrc.

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
# bundled roots).  See generate_ca_bundle() in entrypoint.py.
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
SHIM_DIR="${HOME}/.yolo-shims"
export PATH="$SHIM_DIR:$HOME/.local/bin:$NPM_CONFIG_PREFIX/bin:`

const bashrcPart3 = `:$GOPATH/bin:/bin:/usr/bin"

# Activate mise with shell hooks (interactive shells only).
# Non-interactive shells (bash -lc) skip activation to avoid a deadlock:
# mise hook-env holds a lock then spawns uv via the mise shim (which IS mise),
# re-entering mise locking. The caller's eval "$(mise env ...)" already set up
# the environment before spawning this shell.
if [[ $- == *i* ]]; then
    eval "$(mise activate bash)"
fi
# Trust workspace mise configs from any cwd.  mise trust is dir-scoped and
# --all covers cwd+parents only; MISE_TRUSTED_CONFIG_PATHS=/workspace is the
# blanket mechanism — this hook is belt-and-suspenders for configs written
# after boot.
(cd /workspace 2>/dev/null && mise trust --all --quiet 2>/dev/null) || true

# Aliases
alias ls='ls --color=auto'
alias ll='ls -alF'
`

const bashrcPart4 = `# Agent YOLO flags: gemini/copilot get a --yolo alias above (when selected);
# claude gets --dangerously-skip-permissions injected by cli.py (with
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

// BootstrapScript mirrors shell.generate_bootstrap_script. The only
// interpolation is the mise_shims path in the PATH export line.
func BootstrapScript(e *Env) string {
	return strings.Replace(bootstrapTemplate, "__YOLO_MISE_SHIMS__", e.MiseShims(), 1)
}

// bootstrapTemplate is the byte-exact body of shell.generate_bootstrap_script
// (an rf"""...""" string). Python-doubled braces {{ }} become literal { } here.
const bootstrapTemplate = `#!/bin/bash
export NPM_CONFIG_PREFIX="${NPM_CONFIG_PREFIX:-$HOME/.npm-global}"
export NPM_CONFIG_CACHE="${NPM_CONFIG_CACHE:-$HOME/.cache/npm}"
export GOPATH="${GOPATH:-$HOME/go}"
export GOBIN="$GOPATH/bin"
export PATH="$HOME/.local/bin:$NPM_CONFIG_PREFIX/bin:__YOLO_MISE_SHIMS__:$GOBIN:$PATH"

# Initialize font cache (once, not on every shell session)
fc-cache -f >/dev/null 2>&1

# Agent CLIs (gemini, copilot, claude) are NOT updated here.
# Lazy-update launchers in ~/.yolo-shims/ handle install/update on first use,
# keeping boot fast.  Only MCP/LSP tools that agents depend on are installed here.

# --- Always-on MCP tools (chrome-devtools-mcp, sequential-thinking) -----
if ! command -v chrome-devtools-mcp >/dev/null; then
    echo "  Installing MCP tools..." >&2
    # Clean stale npm temp directories that cause ENOTEMPTY on rename.
    # maxdepth 2 catches both top-level and scoped (@org/) packages.
    find "$NPM_CONFIG_PREFIX/lib/node_modules" -maxdepth 2 -name '.*' -type d 2>/dev/null | xargs rm -rf
    YOLO_BYPASS_SHIMS=1 npm install -g chrome-devtools-mcp @modelcontextprotocol/server-sequential-thinking
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
                YOLO_BYPASS_SHIMS=1 npm install -g --prefer-online "$pkg" 2>&1 || true
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
                    YOLO_BYPASS_SHIMS=1 go install "$pkg" 2>&1 || true
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

# --- showboat (unconditional; tiny dep, useful for debugging) -----------
if ! command -v showboat >/dev/null; then
    echo "  Installing showboat..." >&2
    YOLO_BYPASS_SHIMS=1 pip install showboat
fi
`

// GenerateVenvPrecreateScript writes ~/.yolo-venv-precreate.sh (chmod |= S_IEXEC).
func GenerateVenvPrecreateScript(e *Env) error {
	return writeExecutable(e.Home+"/.yolo-venv-precreate.sh", venvPrecreateScript)
}

// venvPrecreateScript is the byte-exact body of shell.generate_venv_precreate_script
// (a plain r"""...""" string — no interpolation).
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
