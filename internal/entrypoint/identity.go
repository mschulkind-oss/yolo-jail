package entrypoint

import (
	"os"
	"os/exec"
	"path/filepath"
)

// configureGit sets git name, email and global gitignore from the host-forwarded
// YOLO_GIT_* env vars, and marks the workspace as a safe.directory. No-op if git isn't on
// PATH. Each subprocess runs with stdout+stderr discarded.
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
//
// IT WRITES THE HOME IT IS HANDED, not the process's. Every other generator here is a
// function of *Env, and this one used to be the exception: `git config --global` resolved
// ~/.gitconfig from the process's $HOME. The bootstrap sets the two to the same directory
// (runDarwinBootstrap rebinds HOME before building the Env), so production is unchanged,
// but a test booting a fake home wrote into the real one — and since safe.directory is
// written unconditionally, every test that runs the native bootstrap would have.
func configureGit(e *Env) {
	if _, err := exec.LookPath("git"); err != nil {
		// A genuinely absent tool, not a failure — there is no identity to set if
		// there is no git. Recorded so "the jail has no git identity" is answerable
		// from the log without guessing which of the two causes it was.
		e.note("git identity: skipped, no git on PATH")
		return
	}
	set := func(key, val string) {
		if err := runGitConfig(e, "--global", key, val); err != nil {
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
	trustWorkspace(e)
}

// trustWorkspace adds the workspace to git's safe.directory list.
//
// WHY. On macos-user the agent runs as the sandbox account and the workspace belongs to
// the human who launched it, which is exactly the case git's ownership check (CVE-2022-24765)
// refuses: the agent's first `git status` fails with "detected dubious ownership in
// repository", exit 128, and so does every tool that shells out to git. It is the first
// thing a new macos-user launch runs into.
//
// WHAT IT TRUSTS: the workspace path, EXACTLY. Never `*`, which would trust every
// repository any user owns, and never the `<path>/*` prefix form: a repository nested under
// the workspace is not the one the human pointed yolo at, and extending trust to it is a
// decision for its owner. Nothing is given away by the one entry. The check protects the
// account running git from a repository configured by someone else, and the agent already
// runs whatever that repository's code says; its config (core.fsmonitor, hooks) asks for
// nothing more.
//
// THE RESOLVED PATH. git compares the entry against the repository's physical path — newer
// gits resolve both sides, older ones (Apple's included) compare against getcwd's answer —
// so a workspace reached through a symlink (/tmp is /private/tmp on a Mac) would otherwise
// never match. The launcher resolves the workspace already; this does not rely on it.
//
// IDEMPOTENT, AND IT KEEPS THE USER'S OWN ENTRIES. `--replace-all --fixed-value` rewrites
// only lines equal to this path and adds one when there is none: a plain `git config
// safe.directory X` would refuse outright ("cannot overwrite multiple values") the moment
// the user had added a second entry of their own, and `--add` would grow a duplicate at
// every launch into a file that persists per workspace.
//
// EVERY BACKEND THAT RUNS THIS WRITES IT — there is no backend branch here, and none is
// wanted: where the account owns the workspace, git never consults the list, so the entry is
// inert. Today that is one backend. The container boot does not run configureGit
// (bootsteps.go: its global config is composed on the host and mounted read-only), and in a
// rootless podman jail the jail's uid 0 IS the workspace's owner through the user
// namespace, so git passes the ownership check there with no entry at all.
func trustWorkspace(e *Env) {
	ws := e.WorkspaceDir()
	if resolved, err := filepath.EvalSymlinks(ws); err == nil {
		ws = resolved
	}
	if err := runGitConfig(e, "--global", "--replace-all", "--fixed-value", "safe.directory", ws, ws); err != nil {
		e.warn("Warning: could not mark " + ws + " as a git safe.directory: " + err.Error() +
			"; the workspace belongs to another account, so git here will refuse it with " +
			"\"detected dubious ownership\" (exit 128) until it is set: " +
			"git config --global --add safe.directory " + ws)
	}
}

// runGitConfig runs `git config <args>` against e.Home's global config, with stdout and
// stderr discarded, and RETURNS the error: whether a failure is worth a line is the
// caller's decision. (It replaced runQuiet, which once swallowed the error and so made
// every caller's "best-effort" indistinguishable from "did not happen".)
//
// HOME is the Env's, and so is GIT_CONFIG_GLOBAL: with HOME alone, a caller whose
// XDG_CONFIG_HOME names another tree would still have git write THAT tree's git/config
// whenever <home>/.gitconfig does not exist yet. On macos-user <home>/.gitconfig is the home
// layout's link into the workspace sidecar (paths.HomeFileRedirects), which git writes
// through, so naming it changes nothing there.
func runGitConfig(e *Env, args ...string) error {
	cmd := exec.Command("git", append([]string{"config"}, args...)...)
	// Appended last, so they win over any inherited value (os/exec keeps the LAST
	// duplicate of a key).
	cmd.Env = append(os.Environ(),
		"HOME="+e.Home,
		"GIT_CONFIG_GLOBAL="+filepath.Join(e.Home, ".gitconfig"))
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Run()
}
