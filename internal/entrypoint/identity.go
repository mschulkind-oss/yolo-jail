package entrypoint

import (
	"os"
	"os/exec"
)

// configureGit set git name, email, and global
// gitignore from the host-forwarded YOLO_GIT_* env vars. Best-effort — no-op if
// git isn't on PATH. Each subprocess uses capture_output=True (stdout+stderr
// discarded).
func configureGit(e *Env) {
	if _, err := exec.LookPath("git"); err != nil {
		return
	}
	if name := e.Getenv("YOLO_GIT_NAME"); name != "" {
		runQuiet("git", "config", "--global", "user.name", name)
	}
	if email := e.Getenv("YOLO_GIT_EMAIL"); email != "" {
		runQuiet("git", "config", "--global", "user.email", email)
	}
	gitignore := e.Getenv("YOLO_GLOBAL_GITIGNORE")
	if gitignore != "" {
		if fi, err := os.Stat(gitignore); err == nil && fi.Mode().IsRegular() {
			runQuiet("git", "config", "--global", "core.excludesFile", gitignore)
		}
	}
}

// runQuiet runs argv with stdout/stderr discarded. Errors are swallowed (the
// identity setter is best-effort and never aborts boot).
func runQuiet(name string, args ...string) {
	cmd := exec.Command(name, args...)
	cmd.Stdout = nil
	cmd.Stderr = nil
	_ = cmd.Run()
}
