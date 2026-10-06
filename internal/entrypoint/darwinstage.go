package entrypoint

import (
	"path/filepath"
)

// darwinstage.go is step 8 of docs/design/macos-user-provisioning.md half two: the
// macos-user bootstrap starts GENERATING the script the provisioning stage runs, so that
// there is something for the stage to exec.
//
// ⚠ IT IS WRITTEN INTO THE WORKSPACE SIDECAR, NOT THE ACCOUNT HOME, and that is the one
// decision in this file. The container's copy lives at ~/.yolo-bootstrap.sh, which is a
// BIND of <workspace>/.yolo/home/yolo-bootstrap.sh (run.podmanBaseMounts) — the home path
// is the bind's appearance and the sidecar path is where the bytes are. macos-user has no
// binds and one account home shared by every workspace, so writing it home-rooted here
// would put one workspace's generated script at a path the next workspace's launch
// overwrites: the cross-workspace write-write race docs/design/macos-user-home-tiers.md
// exists to end, re-introduced by half two rather than inherited from it.
//
// Naming the sidecar path directly is therefore not a second dialect — it is the SAME
// path the container's mount table names host-side, reached without a mount. Nothing on
// this backend needs ~/.yolo-bootstrap.sh to exist, because the only thing that execs the
// script is the stage argv, which yolo composes itself
// (macosuser.ProvisionArgv → provision.StepRunBootstrapAt).

// DarwinBootstrapScriptPath returns the generated bootstrap script's path inside a
// workspace sidecar. ONE spelling, shared by the generator here and by the macos-user
// stage that execs it — a plan invariant asserts the argv names this exact path, which is
// only meaningful while both come from this function.
func DarwinBootstrapScriptPath(sidecar string) string {
	return filepath.Join(sidecar, "yolo-bootstrap.sh")
}

// GenerateDarwinBootstrapScript writes the bootstrap script into this Env's workspace
// sidecar. No sidecar → no script, and that is correct rather than degraded: the only
// caller without one is the install capture, whose throwaway staging home runs no stage.
//
// ⚠ THE VENV-PRECREATE SCRIPT IS DELIBERATELY NOT WRITTEN. Its body is Linux-absolute —
// it tests /workspace/mise.toml and shells out to /bin/python3 — so on a Mac it would
// find neither and exit 0 on every launch: a step that reports success having never run,
// which is the silent skip OQ-P1 ruled against. The stage's step list omits it for the
// same reason (provision.StepRunVenvPrecreate names it), so a Mac workspace that
// configures `_.python.venv` gets no pre-created venv here. Recorded, not papered over.
func GenerateDarwinBootstrapScript(e *Env) error {
	sidecar := e.DarwinSidecar()
	if sidecar == "" {
		return nil
	}
	return writeExecutable(DarwinBootstrapScriptPath(sidecar), BootstrapScript(e))
}
