package entrypoint

import (
	"os"
	"os/exec"
)

// configureGit set git name, email, and global
// gitignore from the host-forwarded YOLO_GIT_* env vars. No-op if git isn't on
// PATH. Each subprocess uses capture_output=True (stdout+stderr discarded).
//
// EACH FAILURE IS REPORTED, and the symptom is why. `git config --global` writes
// ~/.gitconfig; the container backends compose that file on the HOST and mount it
// :ro, so on a skewed or misconfigured launch these writes fail with EROFS — and the
// jail then boots with NO git identity at all. What the user meets is not a boot
// error but `git commit` refusing with "Please tell me who you are", which reads as
// yolo never having been told the identity rather than as yolo having been unable to
// record it. Naming the setting and the reason at boot is the whole difference.
//
// Still never fatal: a jail with no git identity is usable for everything else, and
// this runs on the one backend (macos-user) where the identity is written rather than
// mounted, so a refusal here would gate a whole launch on a git config.
func configureGit(e *Env) {
	if _, err := exec.LookPath("git"); err != nil {
		// A genuinely absent tool, not a failure — there is no identity to set if
		// there is no git. Recorded so "the jail has no git identity" is answerable
		// from the log without guessing which of the two causes it was.
		e.note("git identity: skipped, no git on PATH")
		return
	}
	set := func(key, val string) {
		if err := runQuiet("git", "config", "--global", key, val); err != nil {
			e.warn("Warning: could not set git " + key + ": " + err.Error() +
				"; this jail has no " + key + " and git will refuse to commit until one " +
				"is set (~/.gitconfig is mounted read-only on the container backends)")
		}
	}
	if name := e.Getenv("YOLO_GIT_NAME"); name != "" {
		set("user.name", name)
	}
	if email := e.Getenv("YOLO_GIT_EMAIL"); email != "" {
		set("user.email", email)
	}
	gitignore := e.Getenv("YOLO_GLOBAL_GITIGNORE")
	if gitignore != "" {
		if fi, err := os.Stat(gitignore); err == nil && fi.Mode().IsRegular() {
			set("core.excludesFile", gitignore)
		}
	}
}

// runQuiet runs argv with stdout/stderr discarded and RETURNS the error. It used to
// swallow it, which made every caller's "best-effort" indistinguishable from "did
// not happen": the discard was in the runner, so no call site could choose to report
// even when the failure was the interesting part. Deciding that is now the caller's.
func runQuiet(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Run()
}
