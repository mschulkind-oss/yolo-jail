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
//
// ⚠ ON macos-user THIS RUNS OUTSIDE SEATBELT, as the sandbox account, which can write every
// workspace under the shared root while the agent can write only its own. So neither the git
// it runs nor the file that git writes may be the agent's choice: the git is found outside the
// sandbox home (gitForConfig), and the file is the one ~/.gitconfig reaches through the home
// layout's own links and no other (gitGlobalConfigFile).
func configureGit(e *Env) {
	git := gitForConfig(e)
	if git == "" {
		// A genuinely absent tool, not a failure — there is no identity to set if
		// there is no git. Recorded so "the jail has no git identity" is answerable
		// from the log without guessing which of the two causes it was.
		e.note("git identity: skipped, no git on PATH")
		return
	}
	global, err := gitGlobalConfigFile(e)
	if err != nil {
		e.warn("Warning: no git identity and no safe.directory entry were written: " + err.Error() +
			"\nUntil they are, git in this jail refuses to commit, and refuses the workspace with " +
			"\"detected dubious ownership\" (exit 128).")
		return
	}
	set := func(key, val string) {
		if err := runGitConfig(e, git, global, "--global", key, val); err != nil {
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
	trustWorkspace(e, git, global)
}

// gitForConfig is the git configureGit runs: the first on the agent's PATH that lies OUTSIDE
// the sandbox home, which leaves the floor's git in the nix store and the system's.
//
// WHY THE AGENT'S PATH. The macos-user launch runs the bootstrap under `env -i` and names no
// PATH (macosuser.DarwinBootstrapArgv), so exec.LookPath finds nothing, and this step used to
// note "no git on PATH" and return — on the one backend it runs on, it wrote no identity and no
// safe.directory at all. The sandbox's real PATH rides in as $YOLO_DARWIN_LOGIN_PATH instead,
// and the floor puts git on it.
//
// ⚠ WHY NOT ALL OF IT. The bootstrap runs OUTSIDE Seatbelt, as the sandbox account, which is
// tolerable only because it runs nothing the agent can change (P4 in
// docs/reference/macos-user-provisioning.md). The login path's first entries are INSIDE the
// sandbox home — ~/.yolo/bin/block, ~/.yolo/bin/launch, ~/.local/bin, ~/.npm-global/bin, the
// mise shims and ~/go/bin (macosuser.SandboxPath) — and the profile lets the agent write the
// whole home. A `git` the agent left in ~/.local/bin would run here unconfined at the next
// launch, able to read and write every other workspace under the shared root; and since the
// mise store is machine-wide, a shim one workspace's agent planted would run in another
// workspace's bootstrap. So the lookup is imageProbePath's: the same login path with every
// directory under the jail home removed, the filter the launcher-collision check already
// applies for the same reason (what the launch provides, never what an install wrote there).
// Without a login path — every backend but this one — that is the image's /bin:/usr/bin.
//
// The process's own PATH is not consulted at all: on this backend it is empty, and a lookup
// there would be a second PATH that nothing filters.
func gitForConfig(e *Env) string {
	return lookPathIn(imageProbePath(e), "git")
}

// gitGlobalConfigFile is the file every `git config --global` write here lands in: ~/.gitconfig
// followed through the home layout's own links to its PHYSICAL path
// (DarwinHomeLayout.homeFileThroughLayout), or an error naming the link that is not the
// layout's.
//
// WHY. On macos-user ~/.gitconfig is the layout's redirect to .config/git/config, and ~/.config
// is its link into the workspace sidecar, which the agent can write. git follows every link it
// meets on the way to the config file, the file itself included, so a link the agent planted at
// <sidecar>/config/git had this unconfined write land in another workspace's .git/config: its
// [user] and [safe] sections were rewritten (reproduced; the content is yolo's, but the place is
// the agent's choice). Handing git the checked physical path leaves nothing on the way for it to
// follow.
//
// THE LAYOUT IS DERIVED WITHOUT THE PACKS, because nothing on this path is a pack's: the
// ~/.gitconfig redirect and the ~/.config link it resolves through are core, laid for every
// launch whatever it selects (DeriveDarwinHomeLayout). A pack-declared link elsewhere — a
// planted link at <sidecar>/claude, say — is the layout step's to refuse and does not stop this
// write, which cannot reach it.
//
// Where the launch named no sidecar — an install capture's staging home, and every test Env —
// the layout has no links, so the rule is only that nothing on the way to ~/.gitconfig is one.
func gitGlobalConfigFile(e *Env) (string, error) {
	layout, _ := darwinHomeLayoutFor(e, nil)
	return layout.homeFileThroughLayout(".gitconfig")
}

// trustWorkspace adds the workspace to git's safe.directory list, in the global config file
// `global` (gitGlobalConfigFile).
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
// gits resolve both sides, older ones resolve only the repository's — so with an older git a
// workspace reached through a symlink (/tmp is /private/tmp on a Mac) would never match. The
// launcher resolves the workspace already; this does not rely on it.
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
func trustWorkspace(e *Env, git, global string) {
	ws := e.WorkspaceDir()
	if resolved, err := filepath.EvalSymlinks(ws); err == nil {
		ws = resolved
	}
	if err := runGitConfig(e, git, global, "--global", "--replace-all", "--fixed-value", "safe.directory", ws, ws); err != nil {
		e.warn("Warning: could not mark " + ws + " as a git safe.directory: " + err.Error() +
			"; the workspace belongs to another account, so git here will refuse it with " +
			"\"detected dubious ownership\" (exit 128) until it is set: " +
			"git config --global --add safe.directory " + ws)
	}
}

// runGitConfig runs `git config <args>` against the global config file `global`, with stdout
// and stderr discarded, and RETURNS the error: whether a failure is worth a line is the
// caller's decision. (It replaced runQuiet, which once swallowed the error and so made
// every caller's "best-effort" indistinguishable from "did not happen".)
//
// HOME is the Env's, and GIT_CONFIG_GLOBAL names the file outright: with HOME alone, a caller
// whose XDG_CONFIG_HOME names another tree would still have git write THAT tree's git/config
// whenever <home>/.gitconfig does not exist yet, and on macos-user git would resolve
// <home>/.gitconfig through the sidecar's links again, after gitGlobalConfigFile checked them.
func runGitConfig(e *Env, git, global string, args ...string) error {
	cmd := exec.Command(git, append([]string{"config"}, args...)...)
	// Appended last, so they win over any inherited value (os/exec keeps the LAST
	// duplicate of a key).
	cmd.Env = append(os.Environ(),
		"HOME="+e.Home,
		"GIT_CONFIG_GLOBAL="+global)
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Run()
}
