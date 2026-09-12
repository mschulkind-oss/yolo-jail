// Package provision produces the jail's PROVISIONING STAGE and owns the one literal
// that reports its failure.
//
// THE STAGE is the imperative provisioning step a launch runs INSIDE the jail, after the
// package floor exists and before the agent starts: the store prune, `mise install`, and
// the generated bootstrap script that npm-installs LSP servers and MCP tools. The word is
// docs/design/macos-user-provisioning.md's; it is not a config key and it names nothing a
// user types.
//
// WHY IT IS A PACKAGE OF ITS OWN. Two backends run the stage and they cannot see each
// other: the container composes it into the `bash -c` its argv hands podman
// (internal/cli/run), and macos-user runs it as a separate confined process
// (internal/macosuser), which imports internal/cli/run in neither direction and must not.
// A second copy of these bytes is a second answer to "what does provisioning do" — and the
// half of it that matters most, the FailedMarker below, already has a reader in a third
// package.
//
// ⚠ THE MARKER IS A CROSS-PROCESS CONTRACT WITH THREE READERS, only one of which is Go.
// jailcontent.ReadProvisioningFailed greps the startup log for it to decide whether the
// briefing shows its banner; the built-in diagnosing-the-jail skill tells every agent to
// look for exactly this string; and a human reads it in the log. It is defined HERE, once,
// beside the producer that writes it, so a rename is a compile error in the reader rather
// than a jail that reports itself healthy after a failed provision.
package provision

import (
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// FailedMarker is the literal the stage writes to the startup log when provisioning
// exits non-zero, and the literal jailcontent.ReadProvisioningFailed greps for. See the
// package comment for the three readers.
const FailedMarker = "PROVISIONING FAILED"

// StartupLog is the provisioning log for one workspace: <workspace>/.yolo/startup.log.
//
// ONE function, and the parameter is why it is one. The container's jail sees the
// workspace at the fixed /workspace bind and this was a constant rooted there; macos-user
// has no bind and the real path is the only path there is. The reader
// (jailcontent.ReadProvisioningFailed) has always resolved it from the HOST's workspace,
// so the constant and the reader agreed only because the container's bind made the two
// paths name one file. Spelling it as a function of the workspace is what makes that
// agreement structural instead of coincidental.
func StartupLog(workspace string) string {
	return filepath.Join(workspace, ".yolo", "startup.log")
}

// The STEPS of the stage, each a complete shell command. Setup joins them with `&&`, so
// each is also the point at which the stage stops: a failing step skips the rest and the
// wrapper records it.
//
// They are constants rather than a rendered script because the two backends take
// DIFFERENT SUBSETS, and a subset is only legible if the members have names. What each
// backend takes, and why, is stated at its own composition site.
const (
	// StepPruneStore removes mise `installs/<tool>/<alias>` symlinks left dangling by a
	// nix GC. Gated on YOLO_STORE_PRUNE_OK, which the container's launcher sets only
	// when it has proved no other jail is live (run.storePruneEnv) — so the step is
	// inert without that proof, which is the state of every backend that cannot produce
	// one.
	StepPruneStore = `if [ "${YOLO_STORE_PRUNE_OK:-0}" = "1" ]; then ` +
		`for _p in "$MISE_DATA_DIR"/installs/*/*; do ` +
		`if [ -L "$_p" ] && [ ! -e "$_p" ]; then ` +
		`rm -f -- "$_p" && echo "  ↳ pruned dangling store symlink: $_p" >&2; ` +
		`fi; done; fi`
	// StepAnnounceMiseInstall / StepAnnounceBootstrap are the progress lines. They are
	// steps rather than decoration because they are also what a tee'd startup.log shows
	// a reader as the last thing that started.
	StepAnnounceMiseInstall = `echo "  ↳ mise install" >&2`
	StepMiseInstall         = "mise install --quiet"
	StepAnnounceBootstrap   = `echo "  ↳ bootstrap" >&2`
	// StepRunBootstrap execs the generated bootstrap script at the path the container
	// BINDS it to. macos-user has no bind and names the real file instead — see
	// StepRunBootstrapAt.
	StepRunBootstrap = "~/.yolo-bootstrap.sh >&2"
	// StepRunVenvPrecreate is CONTAINER-ONLY, and not by omission: its body is
	// Linux-absolute (it tests /workspace/mise.toml and shells out to /bin/python3,
	// entrypoint.venvPrecreateScript), so on a Mac it would find neither and exit 0 on
	// every launch — a step that reports success without ever having run.
	StepRunVenvPrecreate = "~/.yolo-venv-precreate.sh >&2"
)

// StepRunBootstrapAt is StepRunBootstrap for a backend that names the script by absolute
// path instead of reaching it through a bind at ~/.yolo-bootstrap.sh.
func StepRunBootstrapAt(path string) string {
	return shquote.Quote(path) + " >&2"
}

// Setup joins steps with `&&` — the stage body, without any shim bypass.
func Setup(steps ...string) string { return strings.Join(steps, " && ") }

// SetupBypassingShims wraps Setup in `YOLO_BYPASS_SHIMS=1 sh -c '…'`.
//
// The bypass is not optional: the bootstrap script uses `find` and `grep`, and a jail that
// selects the guardrails pack blocks both with a shim that exits 127. The container needs
// the wrapper because its stage is one clause of a much longer `bash -c` whose other
// clauses (the agent!) must NOT bypass anything. A backend that runs the stage as its own
// process sets the variable in that process's environment instead and calls Setup — which
// is also what lets it embed an absolute path without nesting quotes inside `sh -c '…'`.
func SetupBypassingShims(steps ...string) string {
	return "YOLO_BYPASS_SHIMS=1 sh -c '" + Setup(steps...) + "'"
}

// Script wraps a setup body with the tee-to-log, the FailedMarker record, the red console
// line and the continue/abort prompt. `logPath` is StartupLog for the workspace being
// provisioned; `setup` is a Setup/SetupBypassingShims result.
//
// It is bash, not sh: ${PIPESTATUS[0]} is how the exit status of the stage survives the
// pipe into tee, and a plain `sh` would report tee's.
//
// The exit status is the CALLER'S SIGNAL, and it says exactly one thing: whether the human
// asked to stop. A failed stage that was not vetoed — including every non-interactive one,
// where there is nobody to ask — completes with status 0, because a jail whose tools did
// not install is still a jail the user asked for and the record is in the log. Only the
// `n` answer propagates.
func Script(logPath, setup string) string {
	log := shquote.Quote(logPath)
	return "" +
		`printf "=== yolo provisioning %s ===\n" "$(date "+%Y-%m-%dT%H:%M:%S%z")" ` +
		">" + log + "; " +
		"(" + setup + ") 2>&1 | tee -a " + log + " >&2; " +
		`_prc="${PIPESTATUS[0]}"; ` +
		`if [ "$_prc" -ne 0 ]; then ` +
		`printf "` + FailedMarker + ` (exit %s)\n" "$_prc" >>` + log + "; " +
		`printf "\033[1;31m✗ Provisioning failed (exit %s) — log: ` +
		dquoteEscape(logPath) + `\033[0m\n" "$_prc" >&2; ` +
		`if [ -t 0 ] && [ "${YOLO_PROVISION_PROMPT:-1}" != "0" ]; then ` +
		`printf "Provisioning failed — continue anyway? [Y/n] " >&2; ` +
		`read -r _ans; case "$_ans" in [nN]*) exit "$_prc";; esac; ` +
		"fi; fi"
}

// dquoteEscape escapes the four characters that are still live inside a DOUBLE-quoted
// shell string, so a path can be embedded in one.
//
// It exists for the console line in Script, which carries the log path inside printf's
// FORMAT STRING rather than as an argument — the shape the container has emitted since
// this script was written, and the shape its golden pins. A workspace path is arbitrary
// on macos-user (the container's is the fixed /workspace bind), so a `$` in it would be
// expanded and a `"` would end the string early. Every safe path — /workspace's included
// — passes through unchanged, which is what lets the container's bytes stay identical.
func dquoteEscape(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "$", `\$`, "`", "\\`")
	return r.Replace(s)
}
