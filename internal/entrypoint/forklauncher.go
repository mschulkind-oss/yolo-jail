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
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// ForkBuildsEnv names the host's per-bin fork decisions in the jail environment: a JSON object,
// bin → ForkDelivery. A host↔jail contract in YOLO_PACK_ROOT's class, which version.SourceSkew
// covers (it moves only with internal/).
const ForkBuildsEnv = "YOLO_FORK_BUILDS"

// ForkDelivery is the host's answer for one fork's program this launch: the store key to
// materialize, or why there is none.
type ForkDelivery struct {
	// Key is the capture store entry holding the pinned build, "" when there is none.
	Key string `json:"key,omitempty"`
	// Reason is why there is no key, naming what to do; "" when Key is set.
	Reason string `json:"reason,omitempty"`
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

// sourceAgentLauncherSegments renders the source launcher for one program a fork builds, split at
// every exec-prefix position (npmAgentLauncherSegments' reason: a declared node_floor that nothing
// meets at generation is joined in by the provisioning stage).
func sourceAgentLauncherSegments(inst *packdecl.Install, d ForkDelivery, stampDir, receiptsPath,
	capturesPath string, updates bool, servers launcherServers, flags *packload.LaunchInjection) []string {
	token := execPrefixToken()
	var produces []string
	for _, p := range inst.Produces {
		produces = append(produces, p)
	}
	r := strings.NewReplacer(append([]string{
		"__YOLO_BIN__", shquote.Quote(inst.Bin),
		"__YOLO_PROGRAM_PATH__", shquote.Quote(inst.ProgramPath()),
		"__YOLO_FORK_KEY__", shquote.Quote(d.Key),
		"__YOLO_FORK_REASON__", shquote.Quote(d.Reason),
		"__YOLO_FORKED_BY__", shquote.Quote(inst.ForkedBy),
		"__YOLO_SOURCE__", shquote.Quote(inst.Source),
		"__YOLO_PRODUCES__", shquote.Join(produces),
		"__YOLO_STAMP_DIR__", shquote.Quote(stampDir),
		"__YOLO_RECEIPTS_FILE__", shquote.Quote(receiptsPath),
		"__YOLO_CAPTURES_DIR__", shquote.Quote(capturesPath),
		"__YOLO_UPDATES_ENABLED__", shquote.Quote(boolFlag(updates)),
		"__YOLO_SERVERS_ENABLED__", shquote.Quote(boolFlag(!servers.empty())),
		"__YOLO_SERVERS_NPM__", shquote.Quote(servers.npm),
		"__YOLO_EXEC_PREFIX__", token,
	}, append(launchFlagSplices(flags), refreshSplices(inst.Refresh)...)...)...)
	return strings.Split(r.Replace(sourceLauncherTemplate), token)
}

// sourceLauncherTemplate is the source launcher body. Same splice contract as npmLauncherTemplate:
// every sentinel is a shquote'd literal in a bare position.
const sourceLauncherTemplate = `#!/bin/bash
# Source launcher — a program a FORK builds (forked-programs-as-packs.md): materialized from the
# capture store entry the host built at the fork's pinned commit, never installed any other way.
set -euo pipefail
BIN=__YOLO_BIN__
REAL_BIN="$HOME/"__YOLO_PROGRAM_PATH__
# The host's decision for this launch (ForkBuildsEnv): the store key, or why there is none.
KEY=__YOLO_FORK_KEY__
REASON=__YOLO_FORK_REASON__
FORKED_BY=__YOLO_FORKED_BY__
SOURCE=__YOLO_SOURCE__
# The home-relative paths the build leaves (the fork's produces): cleared before a different
# key is materialized, so an older build's files do not outlive its pin.
PRODUCES=(__YOLO_PRODUCES__)
STAMP_DIR=__YOLO_STAMP_DIR__
KEY_STAMP="$STAMP_DIR/$BIN.fork-key"
_YOLO_RECEIPTS=__YOLO_RECEIPTS_FILE__
CAPTURES_DIR=__YOLO_CAPTURES_DIR__
# The pre-launch refresh's throttle and bounds, the other launchers' own numbers.
UPDATE_INTERVAL=3600
UPDATE_TIMEOUT=60
STALE_LOCK=600
UPDATES_ENABLED=__YOLO_UPDATES_ENABLED__
SERVERS_ENABLED=__YOLO_SERVERS_ENABLED__
SERVERS_NPM=__YOLO_SERVERS_NPM__
HAS_LAUNCH_FLAGS=__YOLO_HAS_LAUNCH_FLAGS__
LAUNCH_FLAGS=(__YOLO_LAUNCH_FLAGS__)
` + refreshDeclShell + launchFlagsShellFn + `
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
` + stampMtimeFn + `
_bounded() {
    if command -v timeout >/dev/null 2>&1; then
        YOLO_BYPASS_SHIMS=1 timeout "$UPDATE_TIMEOUT" "$@"
    else
        YOLO_BYPASS_SHIMS=1 "$@"
    fi
}

# A FORK HAS NO UPDATE MODE: its pin moves it, on the host, and the next launch builds the new
# revision. "yolo pack update" reaches this and is told so.
if [ "${YOLO_PACK_UPDATE:-}" = "1" ]; then
    echo "  $BIN: built from source by fork pack $FORKED_BY — its pin moves it (run 'yolo pack update' on the host, then launch again)" >&2
    exit 0
fi

# NO KEY, NO PROGRAM. The base's delivery is not a fallback, and an older build still in this
# home is not this launch's: the pin it was built at is not the one the host asked for.
if [ -z "$KEY" ]; then
    echo "  ⚠ $BIN is not available in this jail: $REASON" >&2
    exit 1
fi

# THE HOST'S BUILD, materialized once per key: a cold home, or one holding another pin's build.
if [ ! -x "$REAL_BIN" ] || [ "$(cat "$KEY_STAMP" 2>/dev/null || true)" != "$KEY" ]; then
    if [ -z "$CAPTURES_DIR" ] || [ ! -d "$CAPTURES_DIR" ] || ! command -v yolo >/dev/null 2>&1; then
        echo "  ⚠ $BIN is not available: this jail has no capture store to materialize fork $FORKED_BY's build from" >&2
        exit 1
    fi
    for p in "${PRODUCES[@]}"; do
        rm -rf -- "$HOME/$p"
    done
    if ! YOLO_BYPASS_SHIMS=1 yolo internal capture-materialize --store="$CAPTURES_DIR" --home="$HOME" \
        --bin="$BIN" --key="$KEY" --declared="$SOURCE" --receipts="$_YOLO_RECEIPTS"; then
        echo "  ⚠ $BIN is not available: fork $FORKED_BY's build ($KEY) could not be put in place" >&2
        exit 1
    fi
    printf '%s\n' "$KEY" > "$KEY_STAMP"
fi

_refresh_servers() {
    command -v yolo >/dev/null 2>&1 || return 0
    YOLO_BYPASS_SHIMS=1 yolo internal refresh-servers \
        --home="$HOME" --npm="$SERVERS_NPM" \
        --updates="$UPDATES_ENABLED" >&2 || true
}

if [ "$SERVERS_ENABLED" = "1" ]; then
    _refresh_servers
fi
` + prelaunchRefreshShellFn + `
` + agentEnvShellFn + agentAuthPrelaunchShellFn + `
if [ -x "$REAL_BIN" ]; then
    _yolo_launch_argv "$@"
    exec __YOLO_EXEC_PREFIX__"$REAL_BIN" ${YOLO_ARGV[@]+"${YOLO_ARGV[@]}"}
else
    echo "  ⚠ $BIN not available" >&2
    exit 1
fi
`
