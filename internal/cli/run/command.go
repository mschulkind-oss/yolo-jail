package run

import "strings"

// setupScript is the provisioning core (mise trust/prune/install/upgrade,
// bootstrap, venv-precreate) run under `YOLO_BYPASS_SHIMS=1 sh -c '…'`. Frozen
// contract (must not drift — the in-jail entrypoint depends on the exact bytes).
const setupScript = "YOLO_BYPASS_SHIMS=1 sh -c '" +
	"(mise trust --all --quiet 2>/dev/null || true) && " +
	`if [ "${YOLO_STORE_PRUNE_OK:-0}" = "1" ]; then ` +
	`for _p in "$MISE_DATA_DIR"/installs/*/*; do ` +
	`if [ -L "$_p" ] && [ ! -e "$_p" ]; then ` +
	`rm -f -- "$_p" && echo "  ↳ pruned dangling store symlink: $_p" >&2; ` +
	"fi; done; fi && " +
	`echo "  ↳ mise install" >&2 && ` +
	"mise install --quiet && " +
	`echo "  ↳ mise upgrade" >&2 && ` +
	"{ mise upgrade --yes >/tmp/yolo-mise-upgrade.out 2>&1; _urc=$?; " +
	`grep -v "^mise WARN" /tmp/yolo-mise-upgrade.out | sed "s/^/    /" >&2; ` +
	`[ "$_urc" -eq 0 ]; } && ` +
	`echo "  ↳ bootstrap" >&2 && ` +
	"~/.yolo-bootstrap.sh >&2 && " +
	"~/.yolo-venv-precreate.sh >&2'"

// startupLog is the in-jail provisioning log path.
const startupLog = "/workspace/.yolo/startup.log"

// miseActivate is the one-time mise activation + yolo-shims re-prepend that runs
// after provisioning. Frozen contract (must not drift — the exact bytes matter).
const miseActivate = `. "$HOME/.config/yolo-user-env.sh" 2>/dev/null; ` +
	`eval "$(mise env -s bash)" 2>/dev/null; export PATH="$HOME/.yolo-shims:$PATH"`

// provisionScript wraps setupScript with the tee-to-log + PROVISIONING FAILED
// banner + continue/abort prompt. Frozen contract (must not drift — the exact bytes matter).
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
// as '\”. profile wraps each phase with timing (the profile branch). Frozen
// contract (must not drift — the exact bytes matter).
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
