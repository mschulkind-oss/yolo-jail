package entrypoint

import (
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/provision"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// packAliases writes a shell alias for each pack whose install binary has launchFlags,
// so an interactive shell gets the same flags a `yolo -- <bin>` invocation does.
//
// ONE PRODUCER, THREE MECHANISMS. The alias body is launchFlagsFor — this package's single
// call into packload.InjectLaunchFlags, over the bare argv `<bin>`, which is literally the
// call the host makes on `yolo -- <bin>` rather than a second fold of the same table beside
// it. The generated launchers (launchflags.go, launchwrapper.go) are the third mechanism and
// come from the same call, so the alias and the script PATH resolves cannot disagree about
// which flags exist. It used to read LaunchFlagsFor and re-assemble
// the argv here, which agreed with the injector only for as long as nobody changed one of
// them; the skip rules in particular (a flag the user already typed) lived in the injector
// alone and were re-implemented nowhere, because the alias's argv is always bare. The two
// mechanisms cannot be collapsed further — the host rewrite happens before the container
// exists and the alias only exists inside it — so what is shared is the producer and its
// record. See docs/design/declaration-parity.md §5.6.
//
// NO profile table. It took one until OQ-PT8 shrank the kind: a kind:profile body could
// carry launch flags, and a selected variant's contribution had to reach the alias or the
// two spellings of one launch would disagree — the variant's flags on the alias, gone from
// the direct invocation. The shrink moved that body to the `profile:` modifier, and a
// launch-kind contribution gated by it has no consumer yet (see LaunchFlagsFor), so the
// two spellings now agree by construction rather than by both folding the same table.
//
// DERIVED rather than declared. It used to be an AgentSpec.Alias string holding a whole
// command line, which duplicated the launchFlags the same spec already carried — two places
// to change, and a pack shipping only one of them would get a shell alias silently
// disagreeing with the launcher.
//
// THE AUTONOMOUS POSTURE IS THE NOTCH, not a default: an interactive shell only exists
// inside the jail, so it is the right one to fold, and a pack's permission-bypass flag
// (claude's --dangerously-skip-permissions, copilot's --yolo) reaches the alias for the same
// reason it reaches `yolo -- <bin>`. The injector hardcodes that posture, which is what
// makes it the right call here and would make it the WRONG one at the host notch: nothing
// in this file may grow a host spelling.
func packAliases(e *Env) string {
	packs, err := LoadJailPacks(e)
	if err != nil {
		return ""
	}
	var lines []string
	var rewrites []*packload.LaunchInjection
	for _, p := range packs {
		// Every honored install, not the first: a pack declaring two programs with
		// launchFlags for both needs two aliases, for the same reason it needs two
		// launchers (shims.go).
		installs, _ := p.HonoredInstalls()
		for _, inst := range installs {
			// Nil means the injector added nothing — this binary has no declared flags,
			// the same skip the old `len(flags) == 0` made. A pack whose install declares
			// a bin another pack gives the flags to is still aliased here, and the record
			// names THAT pack: the merge is "later pack wins", so the pack that installs a
			// binary and the pack that claims its flags need not be the same one.
			inj := launchFlagsFor(packs, inst.Bin)
			if inj == nil {
				continue
			}
			argv := inj.After
			// Quoted, not interpolated into a '…' pair. The alias line is shell source the
			// jail sources on every interactive shell, so a pack-declared flag is code this
			// file emits; a flag carrying a quote used to terminate the enclosing quotes
			// and render a .bashrc bash refused to parse. Quote(Join(argv)) is a single
			// shell word that expands back to exactly the argv the direct invocation
			// passes — byte-identical to the old rendering for the common all-safe case,
			// where Join quotes nothing and Quote wraps the spaces.
			lines = append(lines, "alias "+inst.Bin+"="+shquote.Quote(shquote.Join(argv)))
			rewrites = append(rewrites, inj)
		}
	}
	discloseShellAliases(e, rewrites)
	return strings.Join(lines, "\n")
}

// discloseShellAliases states what the aliases above change, on the launch terminal.
//
// THE DISCLOSURE THE ALIAS PATH DID NOT HAVE. A launch that rewrites the argv you typed at
// yolo says so (run.noteLaunchFlagInjection: "yolo CHANGED the command you asked for"), and
// until this function the OTHER half of the same declaration — the alias that changes what
// `copilot` means at the jail prompt — was written in silence. The boundary is the one
// packhostgrants.go names: "the boundary today is DISCLOSURE, not consent", and copilot's
// `--yolo` is `--allow-all-tools --allow-all-paths --allow-all-urls` in copilot's own help.
// A user who types `copilot` and gets that has a right to read the sentence, and `type
// copilot` is not a sentence anyone reads unprompted.
//
// HERE, AND NOT ON THE HOST'S LAUNCH STREAM. The host could fold the same table — it has the
// staged packs — but it would be describing a file it has not written yet, from a process
// that on the attach path does not rebuild it at all. The writer is the only thing that
// knows, so the writer says so; that is the same argument packload.LaunchInjection makes for
// returning the record rather than letting the print site re-derive it.
//
// NOT IN THE BRIEFING, which is the other surface that suggested itself, because the
// briefing's reader is the one party this mechanism cannot reach. An agent spawns `copilot`
// through `bash -c`, which is non-interactive: bash reads no rcfile and expands no aliases
// there (it is why `yolo -- copilot` needs the host rewrite at all). A briefing section
// would describe, to the only reader it is written for, a thing that never happens to them.
//
// COMPRESSED, NEVER SUPPRESSED (OQ-RO3). The entrypoint runs on attach as well as on a
// fresh boot, so these lines print on every entry into the jail; that is the cadence every
// other boot disclosure has, and the compression — one header carrying the inspect-and-
// bypass hint, one line per changed command — is the density control a launch stream gets
// instead of a quiet flag. Silent when no pack declared a flag, which is most jails: a
// disclosure that prints "nothing" is how a disclosure surface becomes wallpaper (OQ-BP-3).
func discloseShellAliases(e *Env, rewrites []*packload.LaunchInjection) {
	if len(rewrites) == 0 {
		return
	}
	if e.Vars[DarwinLoginPathEnv] != "" {
		// macos-user: the aliases are written and NOT delivered. This backend's account
		// shell is zsh (macosuser.go's dscl UserShell, and the launch's own `/bin/zsh -l`
		// for a bare `yolo`), and zsh reads none of the bash rc files. WriteLoginRC is the
		// existing proof: the PATH half of this same .bashrc had to be re-emitted into
		// .zprofile/.zshrc for exactly this reason, and the alias half was never ported.
		//
		// WHAT CHANGED IS THE CONSEQUENCE, NOT THE FACT (DP-B43, closed with DP-B44 rather
		// than by porting the aliases to a zsh rc). The flags now ride on the LAUNCHER —
		// an installer for a name a pack installs, a wrapper for one it does not — and
		// ~/.yolo/bin/launch is second on macosuser.SandboxPath, which WriteLoginRC
		// re-prepends into .zprofile and .zshrc. So a name typed at this backend's zsh
		// prompt resolves through the launch dir and DOES carry its flags; the undelivered
		// aliases are a redundant second carrier rather than a hole. Saying "run WITHOUT
		// them" here would now be the false sentence.
		//
		// It is still said out loud rather than dropped: a file yolo writes and nothing
		// reads is a fact about this environment a reader is entitled to, and "absent and
		// loud" beats "absent and silent" (docs/reference/macos-user-nix-and-features.md).
		var bins []string
		for _, inj := range rewrites {
			bins = append(bins, inj.Before[0])
		}
		e.warn("pack launch flags are written as bash aliases in " + e.BashrcPath() +
			", and this account's login shell is zsh, which does not read it. They reach " +
			strings.Join(bins, ", ") + " at this prompt anyway, through " + e.LaunchDir() +
			"/<name>, which is on this account's PATH — the alias is the redundant carrier " +
			"here, not the delivering one.")
		return
	}
	// `\<name>` is NOT the escape any more, and naming it would be the one false sentence
	// in this block: since DP-B44 the launcher in ~/.yolo/bin/launch injects the same flags
	// for every spelling, so bypassing the alias reaches a carrier that adds them back.
	// NoLaunchFlagsEnv is the escape that survives both.
	e.warn("yolo CHANGED what these commands mean in this jail's interactive shell " +
		"(`type <name>` prints the alias; " + NoLaunchFlagsEnv + "=1 runs one without the flags):")
	for _, inj := range rewrites {
		// shquote.Join on the AFTER side for the reason the host's block quotes both of
		// its argvs: the line is meant to be copy-pasteable, and an argument containing a
		// space must not be able to masquerade as two.
		e.warn("  you type: " + inj.Before[0] + "  →  bash runs: " + shquote.Join(inj.After) +
			"  (added by pack " + inj.Pack + ")")
	}
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
# Two generated dirs, ADJACENT AND AT THE FRONT, because they are different mechanisms
# whose relative order is what carries the meaning (B2, program-delivery.md §3.5):
#   BLOCK_DIR    — blocked-tool blockers (grep, find). Interception is the whole job, so
#                  they must PRECEDE everything, the launchers included.
#   LAUNCH_DIR   — lazy installers/updaters (claude, pnpm). AHEAD of the install prefixes,
#                  because a launcher that comes after what it installs is unreachable
#                  from its own second invocation onward — which is why the hourly update
#                  it carries had not run in nine days. A launcher is kept from shadowing
#                  a baked binary by a GENERATION-TIME check now (launchercollision.go),
#                  not by this position.
#
# /run/yolo/packages/bin, one step ahead of /bin, is the store-delivered package farm
# (C4/C5). It is empty — in fact absent — unless this launch opted into store delivery,
# and its position is chosen so that opting in changes no precedence relation: the tools
# it holds are the ones that would otherwise be baked into /bin.
#
# THIS STRING IS A SECOND, INDEPENDENTLY-WRITTEN COPY of BootPath (boot.go), which is the
# authority. They disagreed about $HOME/.local/bin for months — second here, fifth there —
# because the only test comparing them asserted "block first, launch last" and nothing
# about the middle. They are now identical, and launcherdir_test.go compares them entry by
# entry.
BLOCK_DIR="${HOME}/.yolo/bin/block"
LAUNCH_DIR="${HOME}/.yolo/bin/launch"
export PATH="$BLOCK_DIR:$LAUNCH_DIR:$NPM_CONFIG_PREFIX/bin:`

const bashrcPart3 = `:$GOPATH/bin:$HOME/.local/bin:/run/yolo/packages/bin:/bin:/usr/bin"

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

const bashrcPart4 = `# Agent YOLO flags: the aliases above carry each selected pack's
# autonomous-posture launch flags — copilot's --yolo, claude's
# --dangerously-skip-permissions (the CLI adds IS_SANDBOX=1 to bypass the root
# check) — and "yolo -- <agent>" injects the same ones; opencode/pi auto-approve
# via their own config files.
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
// package list, and the receipt sentinels (a baked path plus the constant JSON head of the
// MCP loop's receipts — its declared package comes from the loop at run time, so only the
// kind can be rendered here; see receiptPrefix).
//
// It installs NO language server. The LSP install loop that used to follow the MCP one — fed
// by YOLO_LSP_NPM_INSTALL / YOLO_LSP_GO_INSTALL from a three-entry recipe table, tracked by
// the ~/.yolo-installed-lsps sentinel — is deleted with the table
// (docs/reference/mcp-configuration.md#oq-lsp1): a configured server's `command` must already
// resolve on PATH. What it installed before the deletion is left in place, and the boot
// catalog names it as an orphan (catalog.go) for `yolo programs remove` to collect.
func BootstrapScript(e *Env) string {
	r := strings.NewReplacer(
		"__YOLO_MISE_SHIMS__", e.MiseShims(),
		"__YOLO_MCP_NPM_PACKAGES__", mcpPresetNpmPackages(e),
		// One check per distinct Node floor the selected packs' programs declare, each naming
		// the programs and packs that declare it. Baked for macos-user's `env -i`, per the
		// comment at the consuming site.
		"__YOLO_NODE_FLOOR_CHECKS__", nodeFloorChecks(e),
		// The one status the stage's wrapper passes through as a refusal. Spelled by
		// provision, the package that tests for it, and never here.
		"__YOLO_REFUSED_STATUS__", strconv.Itoa(provision.RefusedStatus),
		"__YOLO_RECEIPTS_FILE__", shquote.Quote(receiptsFile(e)),
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
	// An environment that does not generate the preset WRAPPERS installs nothing for
	// them either (Env.SkipMCPPresets). The wrapper is the executable an MCP client
	// spawns; the npm package is only what it spawns INTO. Installing the second
	// without the first is a download nothing can ever exec, and on macos-user it would
	// also make the launch's own "mcp_presets are not delivered" warning a half-truth.
	if e.SkipMCPPresets {
		return ""
	}
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
# The npm resolver's "resolved identity" reader. It may come back empty — a missing jq, an
# unreadable package.json — and an empty answer omits the field rather than inventing one.
_yolo_npm_version() {
    local v
    v=$(jq -r '.version' "$NPM_CONFIG_PREFIX/lib/node_modules/$1/package.json" 2>/dev/null) || return 0
    # jq prints "null" for an absent key, which is not a version.
    if [ "$v" != "null" ]; then printf '%s\n' "$v"; fi
    return 0
}

# Initialize font cache (once, not on every shell session)
fc-cache -f >/dev/null 2>&1

# Agent CLIs (copilot, claude, codex) are NOT installed here.
# Lazy-install launchers in ~/.yolo/bin/launch/ install them on first use, keeping boot
# fast.  They no longer update themselves on a timer — "yolo pack update" is the act that
# resolves a new version.  Only the MCP preset tools agents depend on are installed here —
# never a language server: a configured lsp_servers command must already be on PATH.

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
                    "" "$(_yolo_npm_version "$pkg")" "" install
            done
        fi
    fi
fi

# --- Node floors: install what a declared program needs, then REFUSE if it is absent ----
# OQ-AR2's eager half and OQ-AR3's refusal (docs/reference/agent-program-runtimes.md).
#
# THIS is the eager slot, and the reason is ordering: launcher generation runs before
# mise install AND host-side under yolo check, so the generator may resolve but must not
# install.  This script runs in the provisioning stage — after the CA bundle, after
# mise install, in the one place that already installs over the network.
#
# The checks are BAKED, not read from the environment: macos-user runs the stage under
# env -i, so an inherited variable would be a silent no-op there.  Each baked call names
# one distinct floor and, as its second argument, every program and pack declaring it —
# the two things the refusal must name that only the generator knows.
#
# LAST IN THE SCRIPT, AND THE REFUSAL IS AN EXIT STATUS, NOT AN EARLY EXIT.  A floor
# nothing satisfies must stop the launch, but must not cost the installs above, which
# are for other programs.  The status is the one the stage's wrapper passes through
# unconditionally (provision.RefusedStatus): every other failure in this stage degrades,
# and until this status existed the refusal degraded with them, so the target ran anyway.
#
# ⚠ mise install node@<floor> is the right call here even though a mise SELECTOR IS A
# PREFIX rather than a floor.  That asymmetry is the whole point: a prefix is wrong for
# ACCEPTING an installed version (it would fetch 22.19.0 while 22.23.2 sits there) and
# exactly right for INSTALLING one, because what it fetches satisfies >= floor.
_yolo_floor_refused=""
_yolo_node_floor() {
    if yolo internal node-floor-satisfied "$1" >/dev/null 2>&1; then
        return 0
    fi
    echo "  ↳ installing node@$1 (for $2)" >&2
    mise install "node@$1" >&2 || true
    # Exit 1 means "not satisfied" and prints what IS available on stdout; any other
    # non-zero means the predicate itself could not answer, and saying "none" then would
    # be a claim nobody measured.
    local _avail _rc=0
    _avail=$(yolo internal node-floor-satisfied "$1" 2>/dev/null) || _rc=$?
    if [ "$_rc" = 0 ]; then
        return 0
    fi
    if [ "$_rc" != 1 ]; then
        _avail="unknown (yolo internal node-floor-satisfied exited $_rc)"
    fi
    # REFUSAL, not a warning: a jail that cannot run a program its own config selected is
    # not a ready environment, and reporting success while leaving it unready states a
    # result that was not achieved.
    echo "yolo: REFUSING to start this jail: a selected pack needs a Node that is not here." >&2
    echo "      Needed by: $2" >&2
    echo "      Floor:     Node >=$1 (installing node@$1 failed)" >&2
    echo "      Available: ${_avail:-none}" >&2
    echo "      Make a Node >=$1 available (installing one needs the network), or drop" >&2
    echo "      the pack from your packs list." >&2
    _yolo_floor_refused=1
}
__YOLO_NODE_FLOOR_CHECKS__
if [ -n "$_yolo_floor_refused" ]; then
    exit __YOLO_REFUSED_STATUS__
fi

# NOTE: an unconditional 'pip install showboat' used to live here. It is GONE, deliberately —
# do not add another ungated tool install to this script. Every other install above is
# config-gated (mcp presets) or pack-declared, probes for what it needs, and either
# tolerates failure or — the Node floor alone — refuses the launch by exit status;
# showboat was the only one that did none of that, and being the LAST
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

// nodeFloorDecl is one DISTINCT Node floor and every program that declares it, as
// "program <bin> (pack <name>)" — the words OQ-AR3's refusal must say, in the order the packs
// were loaded.
type nodeFloorDecl struct {
	Floor      string
	DeclaredBy []string
}

// declaredNodeFloors is the set of DISTINCT Node floors the selected packs' `program`
// contributions declare, each with the programs and packs declaring it, for the bootstrap's
// eager install and its refusal.
//
// Distinct, and sorted by floor, for the reason every other baked list here is: two packs
// declaring 22.19 is one install, and a stable order keeps the generated script byte-stable
// across boots so a diff of two bootstrap scripts means something. The declarers are NOT merged
// away with the duplicate floor: a refusal naming only the first of two programs that need it
// would send the user to drop a pack and meet the same refusal again.
//
// A pack whose installs cannot be resolved contributes nothing rather than failing generation —
// its own problems are reported on their own path, and a pack that cannot say what it installs
// cannot be shown to need an interpreter.
func declaredNodeFloors(e *Env) []nodeFloorDecl {
	// LoadJailPacks, the same source every other generator in this package reads. An error
	// contributes nothing: a boot that cannot load packs has a louder problem than a missing
	// interpreter, and it is reported on its own path.
	packs, err := LoadJailPacks(e)
	if err != nil {
		return nil
	}
	byFloor := map[string]*nodeFloorDecl{}
	for _, p := range packs {
		if p == nil {
			continue
		}
		installs, _ := p.HonoredInstalls()
		for _, in := range installs {
			if in.NodeFloor == "" {
				continue
			}
			d := byFloor[in.NodeFloor]
			if d == nil {
				d = &nodeFloorDecl{Floor: in.NodeFloor}
				byFloor[in.NodeFloor] = d
			}
			who := "program " + in.Bin + " (pack " + p.Name + ")"
			if !slices.Contains(d.DeclaredBy, who) {
				d.DeclaredBy = append(d.DeclaredBy, who)
			}
		}
	}
	out := make([]nodeFloorDecl, 0, len(byFloor))
	for _, d := range byFloor {
		out = append(out, *d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Floor < out[j].Floor })
	return out
}

// nodeFloorChecks renders declaredNodeFloors as the bootstrap's `_yolo_node_floor <floor> <who>`
// calls, one per line, or "" when no selected pack declares a floor.
//
// Every value is shquote'd into a bare word: the floor is validated (packdecl.ValidNodeFloor) but
// the bin and the pack name are pack-supplied strings, and this is shell source.
func nodeFloorChecks(e *Env) string {
	var lines []string
	for _, d := range declaredNodeFloors(e) {
		lines = append(lines, "_yolo_node_floor "+shquote.Quote(d.Floor)+" "+
			shquote.Quote(strings.Join(d.DeclaredBy, " and ")))
	}
	return strings.Join(lines, "\n")
}
