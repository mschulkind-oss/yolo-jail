package run

import "strings"

// setupScript is the provisioning core (store prune, mise install, bootstrap,
// venv-precreate) run under `YOLO_BYPASS_SHIMS=1 sh -c '…'`.
//
// ONE thing binds THESE bytes: testdata/final_cmd_bash.txt, which
// TestBuildFinalInternalCmdBashGolden (command_test.go) compares for exact equality
// against buildFinalInternalCmd's output — and that output composes this constant, so
// any drift here is a golden diff. The in-jail entrypoint parses none of it.
//
// This comment used to claim the literal "PROVISIONING FAILED" as a second binder of
// this string. It is not in these bytes at all: provisionScript below emits it, and
// its two out-of-package readers are named there.
// Tools resolve on install only; a workspace mise.lock, when present, governs
// resolution (mise honors it by default), and upgrades happen only through an
// explicit act — docs/design/program-delivery.md OQ-PD3.
const setupScript = "YOLO_BYPASS_SHIMS=1 sh -c '" +
	`if [ "${YOLO_STORE_PRUNE_OK:-0}" = "1" ]; then ` +
	`for _p in "$MISE_DATA_DIR"/installs/*/*; do ` +
	`if [ -L "$_p" ] && [ ! -e "$_p" ]; then ` +
	`rm -f -- "$_p" && echo "  ↳ pruned dangling store symlink: $_p" >&2; ` +
	"fi; done; fi && " +
	`echo "  ↳ mise install" >&2 && ` +
	"mise install --quiet && " +
	`echo "  ↳ bootstrap" >&2 && ` +
	"~/.yolo-bootstrap.sh >&2 && " +
	"~/.yolo-venv-precreate.sh >&2'"

// startupLog is the in-jail provisioning log path.
const startupLog = "/workspace/.yolo/startup.log"

// miseActivate is the one-time mise activation + blocker-dir re-prepend that runs
// after provisioning. Bound by the same single thing setupScript is:
// buildFinalInternalCmd composes it, and TestBuildFinalInternalCmdBashGolden pins
// that composed output byte-for-byte against testdata/final_cmd_bash.txt. Nothing
// else reads these bytes — a change here is legible as a golden diff, and is only a
// contract to the extent the golden is re-blessed deliberately.
const miseActivate = `. "$HOME/.config/yolo-user-env.sh" 2>/dev/null; ` +
	`eval "$(mise env -s bash)" 2>/dev/null; export PATH="$HOME/.yolo/bin/block:$PATH"`

// provisionScript wraps setupScript with the tee-to-log + PROVISIONING FAILED
// banner + continue/abort prompt.
//
// Two different contracts bind two different parts of it:
//
//   - the WHOLE string is composed into buildFinalInternalCmd's output, pinned
//     byte-for-byte by TestBuildFinalInternalCmdBashGolden against
//     testdata/final_cmd_bash.txt — so drift is a golden diff;
//   - the LITERAL "PROVISIONING FAILED" is a CROSS-PROCESS contract with two readers
//     outside this package, neither visible to the golden — which would be re-blessed
//     around a rename without complaint. One is code:
//     jailcontent.ReadProvisioningFailed (internal/jailcontent/write.go:80-88) greps
//     startup.log for it to decide whether the briefing shows its banner. The other is
//     PROSE SHIPPED TO AGENTS: the built-in diagnosing-the-jail skill tells them to
//     look for exactly this string in /workspace/.yolo/startup.log
//     (internal/jailcontent/builtinskills/diagnosing-the-jail/SKILL.md §2), so a
//     rename also silently invalidates the instructions every jail carries.
//     Either way a failed provision becomes a jail that reports itself healthy.
var provisionScript = "" +
	`printf "=== yolo provisioning %s ===\n" "$(date "+%Y-%m-%dT%H:%M:%S%z")" ` +
	">" + startupLog + "; " +
	"(" + setupScript + ") 2>&1 | tee -a " + startupLog + " >&2; " +
	`_prc="${PIPESTATUS[0]}"; ` +
	`if [ "$_prc" -ne 0 ]; then ` +
	`printf "PROVISIONING FAILED (exit %s)\n" "$_prc" >>` + startupLog + "; " +
	`printf "\033[1;31m✗ Provisioning failed (exit %s) — log: ` +
	startupLog + `\033[0m\n" "$_prc" >&2; ` +
	`if [ -t 0 ] && [ "${YOLO_PROVISION_PROMPT:-1}" != "0" ]; then ` +
	`printf "Provisioning failed — continue anyway? [Y/n] " >&2; ` +
	`read -r _ans; case "$_ans" in [nN]*) exit "$_prc";; esac; ` +
	"fi; fi"

// buildFinalInternalCmd assembles the final_internal_cmd:
// the provisioning message → provision_script → mise activate → executing
// message → target command. displayCmd is target_cmd with single quotes escaped
// as '\”. profile wraps each phase with timing (the profile branch).
//
// THIS is where the "frozen bytes" claim the three constants above make actually
// lives: TestBuildFinalInternalCmdBashGolden pins this function's non-profile output
// against testdata/final_cmd_bash.txt, and that output closes over setupScript,
// provisionScript and miseActivate — so the golden is the single binder for all four.
// The profile branch has NO golden, and the two other tests here are property checks
// rather than byte pins: TestBuildFinalInternalCmdQuotingEscapesDisplay (non-profile
// only, display escaping) and TestFinalInternalCmdNeverUpgrades (both branches, the
// one OQ-PD3 property). So a change confined to the profile branch ships green.
func buildFinalInternalCmd(targetCmd string, profile bool) string {
	displayCmd := strings.ReplaceAll(targetCmd, "'", `'\''`)
	if profile {
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
