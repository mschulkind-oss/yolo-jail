package macosuser

import (
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/provision"
)

// provision.go is macos-user's PROVISIONING STAGE — half two of
// docs/design/macos-user-provisioning.md: the imperative step every other backend runs
// inside its jail, run here as a third privileged step between the bootstrap and the
// agent (orchestrator.go).
//
// ⚠ IT IS A NEW CONFINED STEP, NOT A PORT, and the two halves of that sentence are the
// two things to check when reading this file.
//
// NEW: the step did not exist. macos-user ran the darwin bootstrap (yolo's own
// generators, installing nothing) and then the agent, so `mise_tools` and `lsp_servers`
// rendered config and installed nothing at all.
//
// CONFINED: the bootstrap runs OUTSIDE Seatbelt — measured 2026-09-11, its argv is
// `sudo --user=… /usr/bin/env -i … darwin-bootstrap` with no sandbox-exec — and that is
// tolerable only because it executes yolo's own code against a root-owned staged tree
// (the design's principle P4). THE STAGE RUNS VENDOR CODE: `npm install` postinstall
// hooks and mise plugins. The container runs that inside the jail, so running it
// unconfined here would be a regression the container never had. It therefore runs under
// `sandbox-exec -f <profile>` with the SAME session profile the agent gets — already
// installed before the bootstrap (orchestrator.go), so nothing new has to exist for the
// stage to be confined, and a separately-launched sandbox-exec is not the nested-profile
// case Seatbelt refuses.
//
// ⚠ WHAT "CONFINED" DOES AND DOES NOT BUY, measured on hardware 2026-09-11 and worth
// stating where the stage is built rather than only in the design: two vendor installers
// run under this very profile still reached into yolo's generated files (one appended a
// PATH export to .bashrc/.zshrc/.zprofile/.bash_profile; another prompted on /dev/tty and
// waited for a human). Seatbelt was working — the sandbox home is supposed to be writable
// and the tty is the launch's own. Confinement bounds what the stage can reach OUTSIDE
// the sandbox; it is not a promise about the sandbox home.

// ProvisionSetup returns the stage body for this backend: FOUR of provision's six steps.
//
// What is dropped, and why each is a decision rather than an omission:
//
//   - StepPruneStore is gated on YOLO_STORE_PRUNE_OK, which the container's launcher sets
//     only after proving no other jail is live. This backend computes no such proof, so
//     the step would be permanently inert — carrying it would be a line that reads like a
//     feature and is one only on the other backend.
//   - StepRunVenvPrecreate is Linux-absolute (/workspace, /bin/python3), so on a Mac it
//     would exit 0 having done nothing, every launch. Nothing generates that script here
//     either (entrypoint.GenerateDarwinBootstrapScript states the same reason).
//
// `bootstrapScript` is the ABSOLUTE path of the generated script — this backend has no
// bind to reach it through at ~/.yolo-bootstrap.sh, and deliberately writes it into the
// workspace sidecar instead (entrypoint.DarwinBootstrapScriptPath).
func ProvisionSetup(bootstrapScript string) string {
	return provision.Setup(
		provision.StepAnnounceMiseInstall,
		provision.StepMiseInstall,
		provision.StepAnnounceBootstrap,
		provision.StepRunBootstrapAt(bootstrapScript),
	)
}

// ProvisionScript wraps ProvisionSetup in the shared tee-to-log + PROVISIONING FAILED
// wrapper, rooted at the REAL workspace's .yolo sidecar.
//
// That rebinding is the whole of step 9. The log path was a constant rooted at the
// container's fixed /workspace bind; the reader that decides whether the agent's briefing
// shows a "provisioning failed" banner (jailcontent.ReadProvisioningFailed) has always
// resolved it from the host's workspace instead. On the container the bind makes those
// two paths one file; here there is no bind, so the emitter has to name the real path or
// the record lands somewhere nothing reads. Both now come from provision.StartupLog.
func ProvisionScript(workspace, bootstrapScript string) string {
	return provision.Script(provision.StartupLog(workspace), ProvisionSetup(bootstrapScript))
}

// ProvisionArgv builds the stage's privileged argv:
//
//	sudo --user=<sb> /usr/bin/env -i YOLO_BYPASS_SHIMS=1 <env…> \
//	    /usr/bin/sandbox-exec -f <profile> -- /bin/bash -c <script>
//
// ⚠ NO `--login`, AND THAT IS A CORRECTNESS REQUIREMENT RATHER THAN A PREFERENCE.
// `sudo -i` does not execve the argv it is given: per sudo(8) it CONCATENATES the command
// and its arguments, backslash-escaping every character except alphanumerics, underscores,
// hyphens and DOLLAR SIGNS, and hands the result to a login shell. Measured on hardware
// 2026-09-11 through the launch argv, which does carry the flag:
//
//	bash -lc $'echo A\necho B'          printed  Aecho B    (the newline became a
//	                                              `\`-continuation and was removed)
//	bash -lc 'X=inner; echo got=$X'     printed  got=       ($X was unescaped, so the
//	                                              intermediate login shell expanded it)
//
// Neither failure is an error: the wrong command runs and exits 0. This script is dense
// with `$` — "$_prc", "${PIPESTATUS[0]}", "$(date …)", "$MISE_DATA_DIR" — so forwarding it
// through `--login` would have the outer shell expand every one of them against an empty
// environment before the inner shell ever saw the text. The stage would report success
// having provisioned nothing, which is the exact failure mode the startup log exists to
// make impossible.
//
// ROUTED AROUND, NOT FIXED. The launch argv keeps `--login`, because it is load-bearing
// there for a different measured reason (the login rc files re-prepend PATH after macOS
// path_helper reorders it — the acceptance bar OQ-1 passed on 2026-09-10), and changing
// it is its own change with its own test. The stage instead takes the BOOTSTRAP argv's
// shape, which has never had the flag: `sudo --user=… /usr/bin/env -i …`. That costs
// nothing here, because the login shell's rc work is discarded by the very next word in
// either argv — `/usr/bin/env -i` wipes the environment it just built — and the PATH the
// stage actually runs with is the explicit `PATH=` in the env list below, the same value
// SandboxPath gives the agent. PlanInvariants pins the absence.
//
// YOLO_BYPASS_SHIMS is set in the ENVIRONMENT rather than inside an `sh -c '…'` prefix the
// way the container spells it. Two things follow: the whole stage process bypasses the
// blocked-tool shims — which it must, since the bootstrap script uses `find` and `grep`
// and the guardrails pack refuses both with exit 127 — while the agent, a separate
// process, does not; and the script is free to embed an absolute path without nesting
// quotes inside a single-quoted `sh -c`.
func ProvisionArgv(script, profilePath string, sandboxEnv *jsonx.OrderedMap,
	workspace, user, home string, pathPrefix []string) []string {
	if user == "" {
		user = SandboxUser
	}
	if home == "" {
		home = SandboxHome()
	}
	out := []string{
		"sudo",
		"--user=" + user,
		"/usr/bin/env",
		"-i",
		"YOLO_BYPASS_SHIMS=1",
	}
	// The AGENT'S OWN environment, from the same function the launch argv uses. The stage
	// installs into the prefixes the agent resolves through (~/.npm-global, ~/.local, the
	// mise store named by MISE_DATA_DIR), so a stage that composed its environment
	// separately could install into a prefix the agent never looks in — and the two
	// spellings would be free to drift apart one key at a time.
	out = append(out, sandboxEnvPairs(home, user, SandboxPath(home, pathPrefix), sandboxEnv)...)
	out = append(out,
		"/usr/bin/sandbox-exec",
		"-f",
		profilePath,
		"--",
		"/bin/bash",
		"-c",
		script,
	)
	return out
}

// ProvisionBootstrapScript is where this workspace's generated bootstrap script lives —
// the value both the generator (entrypoint.GenerateDarwinBootstrapScript, through the
// sidecar it is handed) and the stage argv resolve to.
func ProvisionBootstrapScript(workspace string) string {
	return entrypoint.DarwinBootstrapScriptPath(paths.WorkspaceHomeState(workspace))
}

// ProvisionNeeded reports whether this config gives the stage anything to do.
//
// THE SKIP RULE, and its point is that `yolo -- bash` in a workspace that declares no
// tools pays nothing: no third privileged step, no sudo, no `mise install` against an
// empty config. The two keys are exactly the two imperative surfaces this backend can now
// deliver.
//
// `mcp_presets` is NOT one of them, and that is the design's list minus one entry rather
// than an oversight: the preset WRAPPERS are not generated on this backend at all
// (entrypoint.RunDarwinBootstrap warns and says why), so the npm packages behind them
// have nothing to be spawned by — Env.SkipMCPPresets empties that arm of the generated
// script too. Counting presets here would start a stage whose only work is a download
// nothing can exec.
//
// ⚠ ONE STRANDED CASE, stated rather than closed. The generated script's UNINSTALL loop
// reads a sentinel of what the last run installed, so removing the final entry from
// `lsp_servers` flips this to false and that loop never runs: the package stays in the
// workspace's npm prefix. Closing it needs a filesystem probe, and this is a pure
// function of the config by deliberate choice — the dry-run plan has to be able to
// describe the launch without touching the disk. The leak is bounded to the workspace's
// own prefix, and re-adding the key then removing it with a launch in between collects it.
func ProvisionNeeded(cfg *jsonx.OrderedMap) bool {
	if mise := config.MergeMiseTools(cfg); mise != nil && mise.Len() > 0 {
		return true
	}
	if lsp, ok := getSectionOrEmptyMap(cfg, "lsp_servers").(*jsonx.OrderedMap); ok && lsp.Len() > 0 {
		return true
	}
	return false
}
