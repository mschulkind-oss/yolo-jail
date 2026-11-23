package entrypoint

// forklauncher.go is the jail's half of a FORK's delivery (docs/design/forked-programs-as-packs.md
// FP-D8, the plan's step 6): the SOURCE LAUNCHER, generated for a program a fork builds, which
// materializes the store entry the host decided on and execs it.
//
// # The host decides, the jail materializes
//
// Which entry answers a fork is a question about the fork LOCK — the pinned commit — and the lock
// lives in the user config directory, which no jail can read. So the host answers it (run's
// forkDeliveriesFor, after building on a miss) and hands the jail, per bin, the store KEY to
// materialize or the REASON there is none, in one env pair (ForkBuildsEnv), read once at boot and
// baked into the launcher as every other launcher fact is.
//
// # What the launcher never does
//
//   - Fall back to the base's delivery. The installer route's download fallback is not this
//     route's: a fork whose build failed is one missing tool (§9), and running the base's upstream
//     program under the fork's name would be the wrong program looking like the right one.
//   - Run a build it was not handed. A key is the host's answer for THIS launch; a home that still
//     holds an older build of the fork is re-materialized from the new key, and with no key the
//     launcher refuses rather than running the stale revision (§9: never serve a near-miss).
//   - Update itself. A fork moves when its pin does (`yolo pack update` on the host); in update
//     mode the launcher says so and changes nothing.

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/patchrecovery"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// ForkBuildsEnv names the host's per-bin fork decisions in the jail environment: a JSON object,
// bin → ForkDelivery. A host↔jail contract in YOLO_PACK_ROOT's class, which version.SourceSkew
// covers (it moves only with internal/).
const ForkBuildsEnv = "YOLO_FORK_BUILDS"

// ForkDelivery is the host's answer for one fork's program this launch: the store key to
// materialize, or why there is none.
type ForkDelivery struct {
	// FreshFailure is host-only explicit request failure, independent of serving bytes and hatches.
	FreshFailure string `json:"-"`
	// FreshError retains the typed host-only admission refusal.
	FreshError error `json:"-"`
	// Key is the capture store entry holding the pinned build, "" when there is none.
	Key string `json:"key,omitempty"`
	// Reason is why there is no key, naming what to do; "" when Key is set.
	Reason string `json:"reason,omitempty"`
	// Cause is the build's cause in plain words, when its act found one (BuildCause): what the
	// launch's refusal of a missing patched fork says (PF-D77). nil leaves Reason to say it.
	Cause *BuildCause `json:"cause,omitempty"`
	// PatchFailure is host-only failure evidence; the jail wire remains unchanged.
	PatchFailure *packsrc.PatchFailure `json:"-"`
	// Runs names the intact admitted build Key holds when PatchFailure is set, for the launch's error
	// block (PF-D83); CachedGood names the older recorded good build cached-good recovery would run
	// when Key is "" (PF-D82). Host-only, as PatchFailure is.
	Runs       string `json:"-"`
	CachedGood string `json:"-"`
	// Recovery is the hatch list the advance decided would get this launch running
	// (patchrecovery.Hatch), which the launch's refusal prints as is. Host-only.
	Recovery []patchrecovery.Hatch `json:"-"`
	// Unsaid says the host's build act said nothing of Reason, leaving it to the launch's refusal or
	// warning (internal/cli/run's missingbuilds.go), which then says it once. The host's alone: never
	// on the wire.
	Unsaid bool `json:"-"`
	// Pack is the pack that declares the program, set in the jail by sourceDeliveryFor: what a
	// PACK-TREE program's launcher names as the source of its bytes, and its receipt's declared
	// source (packdecl.PackTreeBuildSource). Never on the wire: the jail reads it from its own packs.
	Pack string `json:"-"`
	// Reused says the host's slot answered Key from a build the store already held, with no build
	// this launch: what a checkout build's disclosure says (run's noteCheckoutDeliveries). The host's
	// alone: never on the wire.
	Reused bool `json:"-"`
}

// ForkBuildsWire renders the decisions for the environment, "" for none.
func ForkBuildsWire(d map[string]ForkDelivery) string {
	if len(d) == 0 {
		return ""
	}
	b, err := json.Marshal(d)
	if err != nil {
		return ""
	}
	return string(b)
}

// forkDeliveries reads the host's decisions from the environment. An unreadable value decides
// nothing, and says so: every fork's launcher then reports that the host handed it no build.
func forkDeliveries(e *Env) map[string]ForkDelivery {
	raw := e.Getenv(ForkBuildsEnv)
	if raw == "" {
		return nil
	}
	var d map[string]ForkDelivery
	if err := json.Unmarshal([]byte(raw), &d); err != nil {
		e.warn(fmt.Sprintf("yolo-entrypoint: %s is not a fork delivery this build reads (%v) — no "+
			"source-built program is delivered in this jail", ForkBuildsEnv, err))
		return nil
	}
	return d
}

// forkDeliveryFor is bin's decision, or the reason a launcher with none reports.
func forkDeliveryFor(d map[string]ForkDelivery, bin string) ForkDelivery {
	if got, ok := d[bin]; ok && (got.Key != "" || got.Reason != "") {
		return got
	}
	return ForkDelivery{Reason: "the launch that started this jail built no " + bin +
		" (a launcher older than the fork route, or a jail with no capture store)"}
}

// sourceDeliveryFor is forkDeliveryFor for a source launcher's program: a PACK-TREE program's
// (`via: "pack"`) reason with no key names its pack's tree, never a fork route the launch did not
// take, since its build is keyed by that tree's digest and no fork pin exists for it. pack is the
// pack that declares the program.
func sourceDeliveryFor(d map[string]ForkDelivery, inst *packdecl.Install, pack string) ForkDelivery {
	if got, ok := d[inst.Bin]; ok && (got.Key != "" || got.Reason != "") {
		got.Pack = pack
		return got
	}
	if inst.IsPackTree() {
		return ForkDelivery{Pack: pack, Reason: "the launch that started this jail built no " + inst.Bin + " from pack " +
			pack + "'s own tree (" + inst.PackFrom + ")"}
	}
	got := forkDeliveryFor(d, inst.Bin)
	got.Pack = pack
	return got
}

// sourceBuiltBy is who built a source launcher's program, as its lines name it after "is not
// available: ": a fork pack's build, or a PACK-TREE program's build from its own pack's tree, which
// no fork pack declares (so never "fork 's build").
func sourceBuiltBy(inst *packdecl.Install, d ForkDelivery) string {
	if inst.IsPackTree() {
		if d.Pack == "" {
			return "the build of " + inst.Bin + " from its pack's tree (" + inst.PackFrom + ")"
		}
		return "pack " + d.Pack + "'s build of " + inst.Bin + " from " + inst.PackFrom
	}
	return "fork " + inst.ForkedBy + "'s build"
}

// sourceOwner is the pack that supplies a source launcher's bytes: the fork pack, or the pack that
// declares a pack-tree program.
func sourceOwner(inst *packdecl.Install, d ForkDelivery) string {
	if inst.IsPackTree() {
		return d.Pack
	}
	return inst.ForkedBy
}

// sourceDeclared is the declared source a source launcher's materialize records in its receipt: a
// fork's pinned source address, or a pack-tree program's build source, "pack:<pack>/<from>", the
// string its build receipt carries (packdecl.PackTreeBuildSource). A pack-tree program handed no pack
// still declares "pack:/<from>": the prefix is what tells a materialize refusal to name `yolo pack
// build`, and a pack-tree program has no fork source address to fall back to.
func sourceDeclared(inst *packdecl.Install, d ForkDelivery) string {
	if inst.IsPackTree() {
		return packdecl.PackTreeBuildSource(d.Pack, inst.PackFrom)
	}
	return inst.Source
}

// forkKeyDir is where a home records which fork build key it holds, one file per bin: IN THE HOME'S
// OWN STORAGE, beside the materialized program, never in ~/.cache. The launchers' other stamps live
// in ~/.cache, which every jail on the machine binds from one shared directory — right for "when did
// anything on this machine last poll", and wrong for "which build is in THIS home": a key recorded
// there by one workspace's materialize would let another workspace's launcher exec whatever its own
// home holds at the program path — an older pin's build, or the base's upstream program from before
// the fork was selected — as the build the host asked for. ~/.local is per workspace, like the
// program surfaces the build's files land in.
func forkKeyDir(e *Env) string { return filepath.Join(e.Home, ".local", "state", "yolo", "fork-keys") }

// sourceAgentLauncherSegments renders the source launcher for one program a fork builds, split at
// every exec-prefix position (npmAgentLauncherSegments' reason: a declared node_floor that nothing
// meets at generation is joined in by the provisioning stage). keyDir is forkKeyDir.
func sourceAgentLauncherSegments(inst *packdecl.Install, d ForkDelivery, stampDir, keyDir, receiptsPath,
	capturesPath string, updates bool, servers launcherServers, flags *packload.LaunchInjection) []string {
	return sourceAgentLauncherSegmentsWithTmpdir(inst, d, stampDir, keyDir, receiptsPath,
		capturesPath, updates, servers, flags, "", false)
}

func sourceAgentLauncherSegmentsWithTmpdir(inst *packdecl.Install, d ForkDelivery, stampDir, keyDir, receiptsPath,
	capturesPath string, updates bool, servers launcherServers, flags *packload.LaunchInjection,
	sandboxContext string, sandboxNative bool) []string {
	token := execPrefixToken()
	r := strings.NewReplacer(append([]string{
		"__YOLO_BIN__", shquote.Quote(inst.Bin),
		"__YOLO_PROGRAM_PATH__", shquote.Quote(inst.ProgramPath()),
		"__YOLO_FORK_KEY_DIR__", shquote.Quote(keyDir),
		"__YOLO_FORK_KEY__", shquote.Quote(d.Key),
		"__YOLO_FORK_REASON__", shquote.Quote(forkReasonSaid(d)),
		"__YOLO_BUILT_BY__", shquote.Quote(sourceBuiltBy(inst, d)),
		"__YOLO_SOURCE__", shquote.Quote(sourceDeclared(inst, d)),
		"__YOLO_FORK_UPDATE_NOTE__", shquote.Quote(forkUpdateNote(inst)),
		"__YOLO_PRODUCES__", shquote.Join(inst.Produces),
		"__YOLO_STAMP_DIR__", shquote.Quote(stampDir),
		"__YOLO_RECEIPTS_FILE__", shquote.Quote(receiptsPath),
		"__YOLO_CAPTURES_DIR__", shquote.Quote(capturesPath),
		"__YOLO_UPDATES_ENABLED__", shquote.Quote(boolFlag(updates)),
		"__YOLO_SERVERS_ENABLED__", shquote.Quote(boolFlag(!servers.empty())),
		"__YOLO_SERVERS_NPM__", shquote.Quote(servers.npm),
		"__YOLO_EXEC_PREFIX__", token,
		// The gate a patched extension this agent loads puts on it (patchedtrees.go, PPX-D18).
		"__YOLO_TREE_GATE__", shquote.Quote(inst.Gate),
	}, append(append(append(launchFlagSplices(flags), useInstalledSplices(inst.Bin, sourceOwner(inst, d))...), refreshSplices(inst.Refresh, inst.RefreshTiming)...), startupSplicesWithTmpdir(inst, true, sandboxContext, sandboxNative)...)...)...)
	return strings.Split(r.Replace(sourceLauncherTemplate), token)
}

// forkReasonSaid is what a fork's launcher with no key says after "is not available in this jail: ":
// the host's reason and, when its act found one, the cause under it (causeSaid), as an extension's
// gate says it. A not-started build's reason is generic once a cause carries what stopped it. The
// reason's own next step (" — the next fresh launch tries again"), which causeSaid leaves for the
// gate's last line to say, is this launcher's last line, since it has no other.
func forkReasonSaid(d ForkDelivery) string {
	lines := causeSaid(d.Reason, d.Cause, "    ")
	if _, next, ok := CutReason(d.Reason); ok && len(lines) > 1 && next != "" {
		lines = append(lines, "    "+strings.ToUpper(next[:1])+next[1:]+".")
	}
	return strings.Join(lines, "\n")
}

// forkUpdateNote is what a source launcher says in update mode after "<bin>: ": whose build the
// program is and what moves it — a fork's pin, a fresh launch on the host for a PATCHED fork
// (docs/reference/patched-forks.md §7), or a change to its pack's tree for a PACK-TREE program,
// which has no fork pack and no pin to name.
func forkUpdateNote(inst *packdecl.Install) string {
	if inst.IsPackTree() {
		return "built from its pack's own tree (" + inst.PackFrom + ") — a change to that tree moves it, " +
			"at the next launch from the host"
	}
	by := "built from source by fork pack " + inst.ForkedBy + " — "
	if inst.IsPatchedFork() {
		return by + "a patched fork: a fresh launch on the host checks its upstream and builds what moved " +
			"('yolo pack update' there checks now), and the jail it starts runs that build"
	}
	return by + "its pin moves it (run 'yolo pack update' on the host, then launch again)"
}

// sourceLauncherTemplate is the source launcher body. Same splice contract as npmLauncherTemplate:
// every sentinel is a shquote'd literal in a bare position.
const sourceLauncherTemplate = `#!/bin/bash
# Source launcher — a program a FORK builds (forked-programs-as-packs.md), or one a pack builds
# from its own tree (pack-buildable-programs.md): materialized from the capture store entry the
# host built at the fork's pinned commit or the tree's digest, never installed any other way.
set -euo pipefail
BIN=__YOLO_BIN__
PROGRAM=__YOLO_PROGRAM_PATH__
REAL_BIN="$HOME/"__YOLO_PROGRAM_PATH__
# The host's decision for this launch (ForkBuildsEnv): the store key, or why there is none.
KEY=__YOLO_FORK_KEY__
REASON=__YOLO_FORK_REASON__
# Whose build this is, as the lines below name it: a fork pack's, or the pack's own for a program
# built from its tree (sourceBuiltBy).
BUILT_BY=__YOLO_BUILT_BY__
SOURCE=__YOLO_SOURCE__
# The home-relative paths the build leaves (the fork's produces, or a pack-tree program's link and
# its whole ~/.local/lib/yolo-programs/<bin> directory): cleared before a different key is
# materialized, so no file of an older build outlives its pin or its tree (BG-D12).
PRODUCES=(__YOLO_PRODUCES__)
STAMP_DIR=__YOLO_STAMP_DIR__
# Which key THIS home holds: in the home's own storage, never the machine-shared ~/.cache that
# STAMP_DIR is in, or one workspace's materialize would vouch for another workspace's files.
KEY_DIR=__YOLO_FORK_KEY_DIR__
KEY_STAMP="$KEY_DIR/$BIN"
_YOLO_RECEIPTS=__YOLO_RECEIPTS_FILE__
CAPTURES_DIR=__YOLO_CAPTURES_DIR__
# The pre-launch refresh's bounds, the other launchers' own numbers. Its due rule is the jail
# start's, not a clock (prelaunchrefresh.go).
UPDATE_TIMEOUT=60
STALE_LOCK=600
UPDATES_ENABLED=__YOLO_UPDATES_ENABLED__
SERVERS_ENABLED=__YOLO_SERVERS_ENABLED__
SERVERS_NPM=__YOLO_SERVERS_NPM__
HAS_LAUNCH_FLAGS=__YOLO_HAS_LAUNCH_FLAGS__
LAUNCH_FLAGS=(__YOLO_LAUNCH_FLAGS__)
` + refreshDeclShell + probeArgsDeclShell + launchFlagsShellFn + `
case ":${_YOLO_LAUNCHER_ACTIVE:-}:" in
    *":$BIN:"*)
        if [ -x "$REAL_BIN" ]; then
            _yolo_launch_argv "$@"
            exec __YOLO_EXEC_PREFIX__"$REAL_BIN" ${YOLO_ARGV[@]+"${YOLO_ARGV[@]}"}
        fi
        echo "  ⚠ $BIN not available" >&2
        exit 1
        ;;
esac
export _YOLO_LAUNCHER_ACTIVE="${_YOLO_LAUNCHER_ACTIVE:-}:$BIN"

mkdir -p "$STAMP_DIR"
` + stampMtimeFn + updateBoundShellFn + prelaunchRefreshShellFn + `
# A FORK HAS NO UPDATE MODE: its pin moves it, on the host, and the next launch builds the new
# revision — or, for a patched fork, a fresh launch on the host checks its upstream and builds it.
# "yolo pack update" reaches this and is told so.
if [ "${YOLO_PACK_UPDATE:-}" = "1" ]; then
    echo "  $BIN: "__YOLO_FORK_UPDATE_NOTE__ >&2
    exit 0
fi
` + treeGateShell + `
# NO KEY, NO PROGRAM. The base's delivery is not a fallback, and an older build still in this
# home is not this launch's: the pin it was built at is not the one the host asked for.
if [ -z "$KEY" ]; then
    echo "  ⚠ $BIN is not available in this jail: $REASON" >&2
    exit 1
fi

# Verify a warm home's declared outputs against the admitted capture. The key stamp identifies
# what was requested; it does not prove the files still match that artifact.
_yolo_verify_fork_tree() {
    local -a args=(internal capture-materialize --verify-installed --store="$CAPTURES_DIR" \
        --home="$HOME" --bin="$BIN" --program="$PROGRAM" --key="$KEY")
    for p in "${PRODUCES[@]}"; do args+=(--produces="$p"); done
    YOLO_BYPASS_SHIMS=1 yolo "${args[@]}" >/dev/null 2>&1
}

# THE HOST'S BUILD, materialized once per key: a cold home, or one holding another pin's build.
_NEEDS_MATERIALIZE=0
if [ ! -x "$REAL_BIN" ] || [ "$(cat "$KEY_STAMP" 2>/dev/null || true)" != "$KEY" ]; then
    _NEEDS_MATERIALIZE=1
elif [ -z "$CAPTURES_DIR" ] || [ ! -d "$CAPTURES_DIR" ] || ! command -v yolo >/dev/null 2>&1; then
    _NEEDS_MATERIALIZE=1
elif ! _yolo_verify_fork_tree; then
    _NEEDS_MATERIALIZE=1
fi
if [ "$_NEEDS_MATERIALIZE" = "1" ]; then
    if [ -z "$CAPTURES_DIR" ] || [ ! -d "$CAPTURES_DIR" ] || ! command -v yolo >/dev/null 2>&1; then
        echo "  ⚠ $BIN is not available: this jail has no capture store to materialize $BUILT_BY from" >&2
        exit 1
    fi
    for p in "${PRODUCES[@]}"; do
        rm -rf -- "$HOME/$p"
    done
    if ! YOLO_BYPASS_SHIMS=1 yolo internal capture-materialize --store="$CAPTURES_DIR" --home="$HOME" \
        --bin="$BIN" --key="$KEY" --declared="$SOURCE" --receipts="$_YOLO_RECEIPTS"; then
        echo "  ⚠ $BIN is not available: $BUILT_BY ($KEY) could not be put in place" >&2
        exit 1
    fi
    if [ ! -x "$REAL_BIN" ] || ! _yolo_verify_fork_tree; then
        echo "  ⚠ $BIN is not available: $BUILT_BY ($KEY) did not materialize as the admitted output tree" >&2
        exit 1
    fi
    mkdir -p "$KEY_DIR"
    printf '%s\n' "$KEY" > "$KEY_STAMP"
fi

# INSTALL AND STOP (InstallOnlyEnv): the readiness act materializes the host's build above and
# runs nothing.
if [ "${` + InstallOnlyEnv + `:-}" = "1" ]; then
    if [ -x "$REAL_BIN" ]; then
        exit 0
    fi
    echo "  ⚠ $BIN not available: $BUILT_BY ($KEY) holds no runnable $REAL_BIN" >&2
    exit 1
fi

_refresh_servers() {
    local rc=0
    command -v yolo >/dev/null 2>&1 || return 0
    YOLO_BYPASS_SHIMS=1 yolo internal refresh-servers \
        --home="$HOME" --npm="$SERVERS_NPM" \
        --updates="$UPDATES_ENABLED" --bin="$BIN" >&2 || rc=$?
    # UPDATE_REFUSED is a server whose NEWER version did not install (PD-NF1): the subcommand has
    # said the refusal, and the command stops. Any other status is advisory, as above.
    if [ "$rc" = "$UPDATE_REFUSED" ]; then exit "$UPDATE_REFUSED"; fi
    return 0
}

if [ "$SERVERS_ENABLED" = "1" ] && [ "$_YOLO_PROBE" != "1" ]; then
    _refresh_servers
fi
` + prelaunchRefreshCallShell + `
` + agentEnvShellFn + agentAuthPrelaunchShellFn + compileCacheShellFn + sandboxTmpdirEnvShellFn + `
if [ -x "$REAL_BIN" ]; then
    _yolo_launch_argv "$@"
    exec __YOLO_EXEC_PREFIX__"$REAL_BIN" ${YOLO_ARGV[@]+"${YOLO_ARGV[@]}"}
else
    echo "  ⚠ $BIN not available" >&2
    exit 1
fi
`
