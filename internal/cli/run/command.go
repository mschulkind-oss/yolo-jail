package run

import (
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/provision"
)

// jailWorkspace is where every container backend binds the workspace. The stage's log
// path is derived from it rather than written out, so the container's spelling and
// macos-user's come from one function (provision.StartupLog).
const jailWorkspace = "/workspace"

// setupScript is the provisioning core (store prune, mise install, bootstrap,
// venv-precreate) run under `YOLO_BYPASS_SHIMS=1 sh -c '…'`.
//
// THE CONTAINER TAKES ALL SIX STEPS, which is the thing to notice about this list: it is
// a SUBSET selection, and the other backend running a stage takes four of them
// (macosuser.ProvisionSetup). The steps themselves live in internal/provision because
// internal/macosuser cannot import this package — see that package's comment.
//
// ONE thing binds THESE bytes: testdata/final_cmd_bash.txt, which
// TestBuildFinalInternalCmdBashGolden (command_test.go) compares for exact equality
// against buildFinalInternalCmd's output — and that output composes this variable, so
// any drift here is a golden diff. The in-jail entrypoint parses none of it.
//
// This comment used to claim the literal "PROVISIONING FAILED" as a second binder of
// this string. It is not in these bytes at all: provisionScript below emits it, and its
// readers are named at provision.FailedMarker.
// Tools resolve on install only; a workspace mise.lock, when present, governs
// resolution (mise honors it by default), and upgrades happen only through an
// explicit act — docs/design/program-delivery.md OQ-PD3.
var setupScript = provision.SetupBypassingShims(
	provision.StepPruneStore,
	provision.StepAnnounceMiseInstall,
	provision.StepMiseInstall,
	provision.StepAnnounceBootstrap,
	provision.StepRunBootstrap,
	provision.StepRunVenvPrecreate,
)

// startupLog is the in-jail provisioning log path — the workspace bind's .yolo sidecar,
// which is the same file jailcontent.ReadProvisioningFailed reads from the HOST side.
var startupLog = provision.StartupLog(jailWorkspace)

// miseActivate is the one-time mise activation + blocker-dir re-prepend that runs
// after provisioning. Bound by the same single thing setupScript is:
// buildFinalInternalCmd composes it, and TestBuildFinalInternalCmdBashGolden pins
// that composed output byte-for-byte against testdata/final_cmd_bash.txt. Nothing
// else reads these bytes — a change here is legible as a golden diff, and is only a
// contract to the extent the golden is re-blessed deliberately.
const miseActivate = `. "$HOME/.config/yolo-user-env.sh" 2>/dev/null; ` +
	`eval "$(mise env -s bash)" 2>/dev/null; export PATH="$HOME/.yolo/bin/block:$PATH"`

// provisionScript wraps setupScript with the tee-to-log + PROVISIONING FAILED
// banner + continue/abort prompt. The wrapper is provision.Script, shared with the
// macos-user stage; what is container-specific here is only the log path and which
// steps the body carries.
//
// The WHOLE string is composed into buildFinalInternalCmd's output, pinned byte-for-byte
// by TestBuildFinalInternalCmdBashGolden against testdata/final_cmd_bash.txt — so drift
// is a golden diff. The cross-process contract the literal carries is documented where
// the literal now lives (provision.FailedMarker), which is also where its readers are
// named; the golden would be re-blessed around a rename without complaint, and only a
// single definition makes the rename a compile error instead.
var provisionScript = provision.Script(startupLog, setupScript)

// buildFinalInternalCmd assembles the final_internal_cmd:
// the provisioning message → provision_script → mise activate → executing
// message → target command. displayCmd is target_cmd with single quotes escaped
// as '\”. timing wraps each phase in timers (the timing branch; --timing since
// docs/reference/providers.md OQ-PT5 — the in-jail report it prints still says
// "YOLO Jail Profile", which is a name this step did not own).
//
// THIS is where the "frozen bytes" claim the three constants above make actually
// lives: TestBuildFinalInternalCmdBashGolden pins this function's non-timing output
// against testdata/final_cmd_bash.txt, and that output closes over setupScript,
// provisionScript and miseActivate — so the golden is the single binder for all four.
// The timing branch has NO golden, and the two other tests here are property checks
// rather than byte pins: TestBuildFinalInternalCmdQuotingEscapesDisplay (non-timing
// only, display escaping) and TestFinalInternalCmdNeverUpgrades (both branches, the
// one OQ-PD3 property). So a change confined to the timing branch ships green.
func buildFinalInternalCmd(targetCmd string, timing bool) string {
	displayCmd := strings.ReplaceAll(targetCmd, "'", `'\''`)
	if timing {
		return "" +
			"exec 3>&2; " +
			`printf '\033[2m📦 Provisioning tools...\033[0m\n' >&2; ` +
			"_t0=$(date +%s%N); " + provisionScript + "; " +
			"_t1=$(date +%s%N); " +
			miseActivate + "; " +
			"_t2=$(date +%s%N); " +
			`printf '\033[1;36m⚡ Executing: ` + displayCmd + `\033[0m\n' >&2; ` +
			targetCmd + "; _rc=$?; " +
			"_t3=$(date +%s%N); " +
			"echo '' >&3; echo '=== YOLO Jail Profile ===' >&3; " +
			"echo '' >&3; echo '--- Entrypoint (config generation) ---' >&3; " +
			`awk '/^=== YOLO/{buf=""} {buf=buf $0 "\n"} END{printf "%s", buf}' ~/.yolo-perf.log >&3 2>/dev/null; ` +
			"echo '' >&3; echo '--- Container setup ---' >&3; " +
			`printf '  mise install + bootstrap: %s\n' "$(( (_t1 - _t0) / 1000000 ))ms" >&3; ` +
			`printf '  mise hook-env:            %s\n' "$(( (_t2 - _t1) / 1000000 ))ms" >&3; ` +
			`printf '  command execution:        %s\n' "$(( (_t3 - _t2) / 1000000 ))ms" >&3; ` +
			`printf '  total in-container:       %s\n' "$(( (_t3 - _t0) / 1000000 ))ms" >&3; ` +
			"echo '' >&3; " +
			"echo '--- Node path comparison ---' >&3; " +
			"_n0=$(date +%s%N); /bin/node --version >/dev/null 2>&1; _n1=$(date +%s%N); " +
			`printf '  /bin/node:        %sms\n' "$(( (_n1 - _n0) / 1000000 ))" >&3; ` +
			`_n2=$(date +%s%N); "$MISE_DATA_DIR/shims/node" --version >/dev/null 2>&1; _n3=$(date +%s%N); ` +
			`printf '  mise shim node:   %sms\n' "$(( (_n3 - _n2) / 1000000 ))" >&3; ` +
			"echo '' >&3; " +
			"exit $_rc"
	}
	return "" +
		`printf '\033[2m📦 Provisioning tools...\033[0m\n' >&2; ` +
		provisionScript + "; " +
		miseActivate + "; " +
		`printf '\033[1;36m⚡ Executing: ` + displayCmd + `\033[0m\n' >&2; ` +
		targetCmd
}
