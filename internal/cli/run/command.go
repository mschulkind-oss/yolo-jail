package run

import (
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/provision"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
	"github.com/mschulkind-oss/yolo-jail/internal/tty"
)

// jailWorkspace is where every container backend binds the workspace. The stage's log
// path is derived from it rather than written out, so the container's spelling and
// macos-user's come from one function (provision.StartupLog).
const jailWorkspace = "/workspace"

// setupScript is the provisioning core (store prune, mise install, venv-precreate,
// bootstrap) run under `YOLO_BYPASS_SHIMS=1 sh -c '…'`.
//
// THE CONTAINER TAKES ALL SIX STEPS, which is the thing to notice about this list: it is
// a SUBSET selection, and the other backend running a stage takes four of them
// (macosuser.ProvisionSetup). The steps themselves live in internal/provision because
// internal/macosuser cannot import this package — see that package's comment.
//
// ⚠ THE BOOTSTRAP IS LAST, AND THE ORDER IS LOAD-BEARING. It is the one step that can exit
// provision.RefusedStatus (a declared Node floor nothing satisfies,
// docs/reference/agent-program-runtimes.md OQ-AR3), and the `&&` join skips whatever follows a
// failing step. The venv step used to follow it, so a refused floor also skipped venv
// creation, which needs only `mise install` and has nothing to do with Node.
// TestARefusedFloorSkipsNoUnrelatedStep (command_refusal_test.go) runs the composed bytes and
// fails if the venv step moves back behind it.
//
// AND IT RUNS WHATEVER THE STEPS BEFORE IT DID (provision.Stage, AR-L4): a failed `mise
// install` used to skip it through the `&&` join, so no Node floor was checked. The four steps
// above it are still joined with `&&` among themselves, so a failed `mise install` still skips
// the venv step. TestAFailedMiseInstallStillRunsTheBootstrap runs the composed bytes for that.
//
// ONE thing binds THESE bytes: testdata/final_cmd_bash.txt, which
// TestFirstSessionBytesAreTheGolden (command_test.go) compares for exact equality against the
// provisioning stage and the first session's command joined as the first session runs them —
// and the stage composes this variable, so any drift here is a golden diff. The in-jail
// entrypoint runs the stage and parses none of it.
//
// This comment used to claim the literal "PROVISIONING FAILED" as a second binder of
// this string. It is not in these bytes at all: provisionScript below emits it, and its
// readers are named at provision.FailedMarker.
// Tools resolve on install only; a workspace mise.lock, when present, governs
// resolution (mise honors it by default), and upgrades happen only through an
// explicit act — docs/design/program-delivery.md OQ-PD3.
var setupScript = provision.StageBypassingShims(
	[]string{
		provision.StepPruneStore,
		provision.StepAnnounceMiseInstall,
		provision.StepMiseInstall,
		provision.StepRunVenvPrecreate,
	},
	[]string{
		provision.StepAnnounceBootstrap,
		provision.StepRunBootstrap,
	},
)

// startupLog is the in-jail provisioning log path — the workspace bind's .yolo sidecar,
// which is the same file jailcontent.ReadProvisioningFailed reads from the HOST side.
var startupLog = provision.StartupLog(jailWorkspace)

// miseActivate is the one-time mise activation + blocker-dir re-prepend that runs
// before the first session's command. Bound by the same single thing setupScript is:
// buildSessionCmd composes it, and TestFirstSessionBytesAreTheGolden pins that composed
// output byte-for-byte against testdata/final_cmd_bash.txt. Nothing else reads these
// bytes — a change here is legible as a golden diff, and is only a contract to the extent
// the golden is re-blessed deliberately.
const miseActivate = `. "$HOME/.config/yolo-user-env.sh" 2>/dev/null; ` +
	`eval "$(mise env -s bash)" 2>/dev/null; export PATH="$HOME/.yolo/bin/block:$PATH"`

// provisionScript wraps setupScript with the tee-to-log + PROVISIONING FAILED
// banner + continue/abort prompt. The wrapper is provision.Script, shared with the
// macos-user stage; what is container-specific here is only the log path and which
// steps the body carries. `color` is scriptColor's decision, and colors only the
// console line.
//
// It is also what keeps a REFUSED stage from reaching the target: provision.Script ends in
// `exit` on provision.RefusedStatus (and on a declined prompt), which ends the stage's own
// shell with that status. The entrypoint runs the stage as its own shell on the first
// session's terminal and records a non-zero status as the jail's refusal
// (entrypoint/jailmain.go), so the session's command — miseActivate, the Executing banner
// and the target — never runs, and every other session is refused with the same status.
//
// The WHOLE string is composed into buildProvisionStage's output, pinned byte-for-byte (its
// color=true form) by TestFirstSessionBytesAreTheGolden against testdata/final_cmd_bash.txt —
// so drift is a golden diff. The cross-process contract the literal carries is documented
// where the literal now lives (provision.FailedMarker), which is also where its readers are
// named; the golden would be re-blessed around a rename without complaint, and only a
// single definition makes the rename a compile error instead.
func provisionScript(color bool) string { return provision.Script(startupLog, setupScript, color) }

// scriptSGR is the escape pair one generated printf line is wrapped in, spelled as
// printf's own `\033` escape (the bytes the golden pins), or two empty strings when
// color is off — so a plain line differs from a colored one by exactly its escapes.
func scriptSGR(color bool, code string) (open, reset string) {
	if !color {
		return "", ""
	}
	return `\033[` + code + `m`, `\033[0m`
}

// provisioningLine is the "📦 Provisioning tools..." line the stage prints first.
func provisioningLine(color bool) string {
	dim, reset := scriptSGR(color, "2")
	return `printf '` + dim + `📦 Provisioning tools...` + reset + `\n' >&2; `
}

// scriptColor is the color decision for every line the GENERATED container script
// prints — the provisioning line, a provisioning failure, the "⚡ Executing" hand-over.
//
// It is the NO_COLOR half of the one gate (tty.NoColor) and nothing else, because the
// terminal half has no subject here: these lines print from inside the container, whose
// stream this process never probes, and they were never terminal-gated. The environment
// is o.Getenv — the launch environment every other decision reads, and the one
// noColorEnvArgs hands the jail — so the script and the jail it runs in agree.
func (o *Options) scriptColor() bool { return !tty.NoColor(o.Getenv) }

// provisionStage is buildProvisionStage with this launch's color decision made. The launch
// hands it to the container's main process (entrypoint.HoldMainArg), which records it for the
// first session. The launch calls THIS, never buildProvisionStage directly —
// TestTheLaunchBuildsItsCommandsThroughTheColorDecision pins that, so the color decision
// cannot be dropped at the call site while the function below stays green.
func (o *Options) provisionStage() string { return buildProvisionStage(o.scriptColor()) }

// sessionCmd is buildSessionCmd with this launch's two decisions made: the timing report
// (timingReporting, the PRINT gate) and the script's color. It is the first session's command
// (firstSessionExecCmd); an attach's command is its bare target, which the entrypoint
// announces itself.
func (o *Options) sessionCmd(targetCmd string) string {
	return buildSessionCmd(targetCmd, o.timingReporting(), o.scriptColor())
}

// executingBanner is the "⚡ Executing: <target>" line both branches of buildSessionCmd
// print just before the target runs.
//
// THE TARGET IS printf's ARGUMENT, NEVER ITS FORMAT. It used to be spliced into the format
// string (with only its single quotes escaped), so every `%` and `\` in a command was a
// printf directive: `stat -c "%u:%g %a" f` was displayed as `stat -c "0:0 0x0p+0" f`, which
// shows the user a command they did not type. As an argument to `%s` it is printed
// byte-for-byte, and shquote.Quote makes it one word whatever it contains.
func executingBanner(targetCmd string, color bool) string {
	cyan, reset := scriptSGR(color, "1;36")
	return `printf '` + cyan + `⚡ Executing: %s` + reset + `\n' ` + shquote.Quote(targetCmd) + ` >&2`
}

// buildProvisionStage is the provisioning STAGE a launch runs once per container: the
// provisioning message, then provisionScript. It runs as its own shell on the FIRST
// session's terminal (entrypoint/jailmain.go), after that session's boot pass and before its
// command. Until the container's main process became a hold it was the first clause of the
// container's own command, and the bytes did not change when it moved: the stage and
// buildSessionCmd's command, joined by "; ", are exactly that old command
// (TestFirstSessionBytesAreTheGolden).
func buildProvisionStage(color bool) string {
	return provisioningLine(color) + provisionScript(color)
}

// buildSessionCmd assembles the first session's command: mise activate → executing
// message (executingBanner) → target command. timing wraps each phase in timers (the
// timing branch; --timing since docs/reference/providers.md OQ-PT5 — the in-jail report
// it prints still says "YOLO Jail Profile", which is a name this step did not own). The
// stage ran before this command in a shell of its own, so the timing branch reads its
// duration from entrypoint.ProvisionMillisEnv, which the entrypoint sets for the session.
//
// THIS is where the "frozen bytes" claim the constants above make actually lives, with
// buildProvisionStage: TestFirstSessionBytesAreTheGolden pins the two joined against
// testdata/final_cmd_bash.txt, and they close over setupScript, provisionScript and
// miseActivate — so the golden is the single binder for all of them. The timing branch has
// NO golden. The other tests here are property checks rather than byte pins, and three of
// them cover BOTH branches: TestExecutingBannerPrintsTheTargetVerbatim (the banner, run
// through bash), TestARefusedStageNeverReachesTheTarget (the stage run with a refusing
// bootstrap, then the command only if the stage succeeded, as the entrypoint does) and
// TestFinalInternalCmdNeverUpgrades (the one OQ-PD3 property). Anything else confined to
// the timing branch still ships green.
//
// `color` is scriptColor's decision, made by sessionCmd. The golden pins color=true;
// color=false is exactly those bytes minus their escapes, which
// TestFinalInternalCmdIsTheGoldenWhenColorIsOn asserts, so NO_COLOR moved no frozen byte for
// a launch that does not set it.
func buildSessionCmd(targetCmd string, timing, color bool) string {
	if timing {
		return "" +
			"exec 3>&2; " +
			`_p="${` + entrypoint.ProvisionMillisEnv + `:-0}"; ` +
			"_t1=$(date +%s%N); " +
			miseActivate + "; " +
			"_t2=$(date +%s%N); " +
			executingBanner(targetCmd, color) + "; " +
			targetCmd + "; _rc=$?; " +
			"_t3=$(date +%s%N); " +
			"echo '' >&3; echo '=== YOLO Jail Profile ===' >&3; " +
			"echo '' >&3; echo '--- Entrypoint (config generation) ---' >&3; " +
			`awk '/^=== YOLO/{buf=""} {buf=buf $0 "\n"} END{printf "%s", buf}' ~/.yolo-perf.log >&3 2>/dev/null; ` +
			"echo '' >&3; echo '--- Container setup ---' >&3; " +
			`printf '  mise install + bootstrap: %s\n' "${_p}ms" >&3; ` +
			`printf '  mise hook-env:            %s\n' "$(( (_t2 - _t1) / 1000000 ))ms" >&3; ` +
			`printf '  command execution:        %s\n' "$(( (_t3 - _t2) / 1000000 ))ms" >&3; ` +
			`printf '  total in-container:       %s\n' "$(( _p + (_t3 - _t1) / 1000000 ))ms" >&3; ` +
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
		miseActivate + "; " +
		executingBanner(targetCmd, color) + "; " +
		targetCmd
}
