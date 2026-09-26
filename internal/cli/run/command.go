package run

import (
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
	provision.StepRunVenvPrecreate,
	provision.StepAnnounceBootstrap,
	provision.StepRunBootstrap,
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
// steps the body carries. `color` is scriptColor's decision, and colors only the
// console line.
//
// It is also what keeps a REFUSED stage from reaching the target: provision.Script ends in
// `exit` on provision.RefusedStatus, and because provisionScript is spliced into
// buildFinalInternalCmd's top-level `bash -c` rather than a subshell, that exit ends the
// container's command before miseActivate, the Executing banner and the target.
//
// The WHOLE string is composed into buildFinalInternalCmd's output, pinned byte-for-byte
// (its color=true form) by TestBuildFinalInternalCmdBashGolden against
// testdata/final_cmd_bash.txt — so drift is a golden diff. The cross-process contract the literal carries is documented where
// the literal now lives (provision.FailedMarker), which is also where its readers are
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

// provisioningLine is the "📦 Provisioning tools..." line both branches of
// buildFinalInternalCmd print first.
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

// finalInternalCmd is buildFinalInternalCmd with this launch's two decisions made:
// the timing report (timingReporting, the PRINT gate) and the script's color. The
// launch calls THIS, never buildFinalInternalCmd directly —
// TestTheLaunchBuildsItsCommandThroughFinalInternalCmd pins that, so the color decision
// cannot be dropped at the call site while the function below stays green.
func (o *Options) finalInternalCmd(targetCmd string) string {
	return buildFinalInternalCmd(targetCmd, o.timingReporting(), o.scriptColor())
}

// executingBanner is the "⚡ Executing: <target>" line both branches of
// buildFinalInternalCmd print just before the target runs.
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

// buildFinalInternalCmd assembles the final_internal_cmd:
// the provisioning message → provision_script → mise activate → executing
// message (executingBanner) → target command. timing wraps each phase in timers (the
// timing branch; --timing since docs/reference/providers.md OQ-PT5 — the in-jail report
// it prints still says "YOLO Jail Profile", which is a name this step did not own).
//
// THIS is where the "frozen bytes" claim the three constants above make actually
// lives: TestBuildFinalInternalCmdBashGolden pins this function's non-timing output
// against testdata/final_cmd_bash.txt, and that output closes over setupScript,
// provisionScript and miseActivate — so the golden is the single binder for all four.
// The timing branch has NO golden. The other tests here are property checks rather than
// byte pins, and three of them cover BOTH branches: TestExecutingBannerPrintsTheTargetVerbatim
// (the banner, run through bash), TestARefusedStageNeverReachesTheTarget (the composed
// command run with a refusing bootstrap) and TestFinalInternalCmdNeverUpgrades (the one
// OQ-PD3 property). Anything else confined to the timing branch still ships green.
//
// `color` is scriptColor's decision, made by finalInternalCmd. The golden pins color=true;
// color=false is exactly those bytes minus their escapes, which
// TestFinalInternalCmdIsTheGoldenWhenColorIsOn asserts, so NO_COLOR moved no frozen byte for
// a launch that does not set it.
func buildFinalInternalCmd(targetCmd string, timing, color bool) string {
	if timing {
		return "" +
			"exec 3>&2; " +
			provisioningLine(color) +
			"_t0=$(date +%s%N); " + provisionScript(color) + "; " +
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
		provisioningLine(color) +
		provisionScript(color) + "; " +
		miseActivate + "; " +
		executingBanner(targetCmd, color) + "; " +
		targetCmd
}
