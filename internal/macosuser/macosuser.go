// Package macosuser is the Go port (AS-IS, per go-port plan §13/Stage 16b) of
// src/cli/macos_user.py — the native macОS backend that isolates an agent in a
// dedicated hidden macOS user hardened with an Apple Seatbelt (sandbox-exec)
// profile: no Linux container, no VM, no arch switch. It is yolo-jail's port of
// SandVault's design (github.com/webcoyote/sandvault).
//
// Design mirror: every artifact producer here is a PURE data-returning function
// (command lists, ACL ACE strings, the SBPL profile, launch argv, the in-process
// entrypoint bootstrap), so its SandVault-parity security properties are fully
// unit-testable on Linux CI without a Mac. Only RunMacosUser and the macos-*
// command bodies shell out, guarded to macOS.
//
// Ported AS-IS: current behavior is reproduced faithfully, including
// known-broken/unfinished paths (real-hardware verification stays a Mac-runbook
// step; see the Stage 16b handoff for the list of deliberately-reproduced
// behaviors). No macOS bug fixes live in the port commits.
//
// Source of truth: src/cli/macos_user.py. Port from that file, never from
// memory or summaries.
package macosuser

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/pytext"
)

// Dedicated account constants (byte-identical to macos_user.py).
const (
	// SandboxUser is the hidden service account (`_` prefix + IsHidden) so it
	// never shows on the login window, mirroring SandVault's hidden user.
	SandboxUser  = "_yolojail"
	SandboxGroup = "_yolojail"

	// sandboxMinID: UID/GID floor for the auto-picked free id (SandVault uses
	// 600; macOS hides sub-500 accounts, 500+ service accounts + IsHidden is
	// the safe, collision-free range).
	sandboxMinID = 600

	// stateDir is the root-owned, 0444 state dir holding the per-session
	// Seatbelt profile, the entrypoint bootstrap, and a root-owned copy of the
	// stdlib-only `entrypoint` package.
	stateDir = "/var/yolo-jail"

	// Absolute paths to the system tools the run path invokes under sudo —
	// pinned so the argv is deterministic regardless of the caller's PATH.
	mkdirBin = "/bin/mkdir"
	teeBin   = "/usr/bin/tee"
	chmodBin = "/bin/chmod"
	cpBin    = "/bin/cp"
)

// SandboxHome is /Users/_yolojail.
func SandboxHome() string { return "/Users/" + SandboxUser }

// SharedRootDefault is the neutral shared-workspace root (/Users/Shared/yolo).
// A NEUTRAL directory outside every user's home — the crux of the model's
// "clear semantics".
func SharedRootDefault() string { return "/Users/Shared/yolo" }

// pythonCandidates are the ordered python3 interpreters for the sandbox user,
// best first. The bare /usr/bin/python3 is LAST and only a fallback: with the
// Command Line Tools absent it is the xcode-select stub, which triggers a GUI
// install flow instead of running Python.
var pythonCandidates = []string{
	"/opt/homebrew/bin/python3", // arm64 Homebrew (0755, world-runnable)
	"/usr/local/bin/python3",    // Intel Homebrew / python.org
	"/usr/bin/python3",          // CLT/system — LAST (may be the xcode-select stub)
}

// ---------------------------------------------------------------------------
// Account provisioning — command lists (pure; executed by the orchestrator)
// ---------------------------------------------------------------------------

// CreateUserCommands returns the dscl/dseditgroup argv to create the hidden
// sandbox account. Mirrors create_user_commands. The random password is set
// separately (never a literal argv — it would show in `ps`), so it is
// intentionally NOT in this list.
func CreateUserCommands(uid, gid int, hostUser string) [][]string {
	user := SandboxUser
	group := SandboxGroup
	home := SandboxHome()
	return [][]string{
		// Group
		{"dscl", ".", "-create", "/Groups/" + group},
		{"dscl", ".", "-create", "/Groups/" + group, "PrimaryGroupID", itoa(gid)},
		{"dscl", ".", "-create", "/Groups/" + group, "RealName", "YOLO Jail"},
		// User
		{"dscl", ".", "-create", "/Users/" + user},
		{"dscl", ".", "-create", "/Users/" + user, "UniqueID", itoa(uid)},
		{"dscl", ".", "-create", "/Users/" + user, "PrimaryGroupID", itoa(gid)},
		{"dscl", ".", "-create", "/Users/" + user, "RealName", "YOLO Jail"},
		{"dscl", ".", "-create", "/Users/" + user, "NFSHomeDirectory", home},
		{"dscl", ".", "-create", "/Users/" + user, "UserShell", "/bin/zsh"},
		// Hidden from the login window
		{"dscl", ".", "-create", "/Users/" + user, "IsHidden", "1"},
		// Not a real login user: strip from staff
		{"dseditgroup", "-o", "edit", "-d", user, "-t", "user", "staff"},
		// Shared group membership (host user + sandbox user) for the ACL
		{"dseditgroup", "-o", "edit", "-a", user, "-t", "user", group},
		{"dseditgroup", "-o", "edit", "-a", hostUser, "-t", "user", group},
		// Provision the home dir with correct ownership + 0750.
		{"createhomedir", "-c", "-u", user},
		{"chown", "-R", user + ":" + group, home},
		{"chmod", "750", home},
	}
}

// DeleteUserCommands returns the dscl argv to tear the sandbox account down.
// Home removal is last so a failed earlier step doesn't orphan a live session's
// files. Mirrors delete_user_commands.
func DeleteUserCommands(hostUser string) [][]string {
	user := SandboxUser
	group := SandboxGroup
	home := SandboxHome()
	return [][]string{
		{"dseditgroup", "-o", "edit", "-d", hostUser, "-t", "user", group},
		{"dscl", ".", "-delete", "/Users/" + user},
		{"dscl", ".", "-delete", "/Groups/" + group},
		{"rm", "-rf", home},
	}
}

// SharedRootProvisionCommands returns the mkdir/chown/chmod argv to provision
// the neutral shared root — owned by the host user, group _yolojail, mode 2770
// (setgid), plus the inheriting ACL ACEs applied to the root itself. Mirrors
// shared_root_provision_commands.
func SharedRootProvisionCommands(root, hostUser string) [][]string {
	if root == "" {
		root = SharedRootDefault()
	}
	group := SandboxGroup
	aces := WorkspaceACLAces(group)
	return [][]string{
		{"mkdir", "-p", root},
		{"chown", hostUser + ":" + group, root},
		{"chmod", "2770", root},
		{"chmod", "+a", aces["dir"], root},
		{"chmod", "+a", aces["file_inherit"], root},
	}
}

// ---------------------------------------------------------------------------
// Interpreter resolution — never blindly trust /usr/bin/python3
// ---------------------------------------------------------------------------

// PythonCandidates returns the ordered python3 candidates, best first (a copy).
func PythonCandidates() []string {
	return append([]string(nil), pythonCandidates...)
}

// ResolvePython returns the first existing candidate interpreter, or "" if none
// exist. `exists` is injectable so a Linux test can assert the ordering; pass
// nil to use os.Stat. Mirrors resolve_python (Python's None → "").
func ResolvePython(exists func(string) bool) (string, bool) {
	if exists == nil {
		exists = func(p string) bool { _, err := os.Stat(p); return err == nil }
	}
	for _, cand := range PythonCandidates() {
		if exists(cand) {
			return cand, true
		}
	}
	return "", false
}

// ---------------------------------------------------------------------------
// Staging the entrypoint package into the root-owned state dir
// ---------------------------------------------------------------------------

// StagedEntrypointDir returns where the stdlib-only entrypoint package is
// staged (importable). Mirrors staged_entrypoint_dir.
func StagedEntrypointDir(sd string) string {
	if sd == "" {
		sd = stateDir
	}
	return filepath.Join(sd, "entrypoint")
}

// StageEntrypointCommands returns the sudo argv copying `entrypoint` into the
// root-owned state dir (root-owned so the sandbox can't rewrite its own
// profile; world-readable so it can import). Mirrors stage_entrypoint_commands.
func StageEntrypointCommands(repoSrc, sd string) [][]string {
	if sd == "" {
		sd = stateDir
	}
	// Python: src = repo_src / "entrypoint"; then f"{src}/." — path-join then
	// append "/." literally (NOT filepath.Clean, which would drop the trailing
	// "/."). Reproduce the literal.
	src := filepath.Join(repoSrc, "entrypoint")
	dst := StagedEntrypointDir(sd)
	return [][]string{
		{mkdirBin, "-p", sd},
		{cpBin, "-R", src + "/.", dst},
		{chmodBin, "-R", "a+rX", dst},
	}
}

// ---------------------------------------------------------------------------
// Workspace location — must be neutral ground, never inside a home
// ---------------------------------------------------------------------------

// HomeContaining returns the user-home dir that contains `workspace`, or ""
// (Python None) when the workspace is on neutral ground. A "home" is a direct
// child of /Users other than /Users/Shared. Pure and path-only. Mirrors
// home_containing. The bool is false when no home contains the workspace.
func HomeContaining(workspace, usersRoot string) (string, bool) {
	if usersRoot == "" {
		usersRoot = "/Users"
	}
	// candidates = [workspace, *workspace.parents]
	for _, p := range append([]string{workspace}, pathParents(workspace)...) {
		parent := pathParent(p)
		if parent == usersRoot && pathName(p) != "Shared" {
			return p, true
		}
	}
	return "", false
}

// ---------------------------------------------------------------------------
// Workspace ACL — SandVault's dir/file-split inheriting ACEs
// ---------------------------------------------------------------------------

const (
	dirRights = "read,write,append,delete,delete_child,readattr,writeattr,readextattr," +
		"writeextattr,readsecurity,writesecurity,chown,search,list,directory_inherit"
	fileInheritRights = "read,write,append,delete,delete_child,readattr,writeattr,readextattr," +
		"writeextattr,readsecurity,writesecurity,chown," +
		"file_inherit,directory_inherit,only_inherit"
	fileRights = "read,write,append,delete,delete_child,readattr,writeattr,readextattr," +
		"writeextattr,readsecurity,writesecurity,chown"
)

// WorkspaceACLAces returns the three chmod +a ACE strings (dir / file-inherit /
// file). Mirrors workspace_acl_aces.
func WorkspaceACLAces(group string) map[string]string {
	if group == "" {
		group = SandboxGroup
	}
	return map[string]string{
		"dir":          "group:" + group + " allow " + dirRights,
		"file_inherit": "group:" + group + " allow " + fileInheritRights,
		"file":         "group:" + group + " allow " + fileRights,
	}
}

// FixPermissionsScript returns the find-based bash script that (re)applies the
// split ACEs to a tree (the on-demand macos-fix-permissions retrofit, NOT the
// hot path). Mirrors fix_permissions_script byte-for-byte.
func FixPermissionsScript(root, group string) string {
	aces := WorkspaceACLAces(group)
	r := shQuote(root)
	return "set -euo pipefail\n" +
		"root=" + r + "\n" +
		"echo \"Applying shared-group ACLs under $root (this can take a moment on a large tree)…\"\n" +
		"find \"$root\" -type d -exec chmod -h +a " + shQuote(aces["dir"]) + " {} +\n" +
		"find \"$root\" -type d -exec chmod -h +a " + shQuote(aces["file_inherit"]) + " {} +\n" +
		"find \"$root\" ! -type d -exec chmod -h +a " + shQuote(aces["file"]) + " {} +\n" +
		"echo \"Done.\"\n"
}

// WorkspaceACLStripScript returns the find-based bash script that removes ALL
// ACLs from the workspace (chmod -h -N). Mirrors workspace_acl_strip_script.
func WorkspaceACLStripScript(workspace string) string {
	return "set -euo pipefail\n" +
		"ws=" + shQuote(workspace) + "\n" +
		"find \"$ws\" -exec chmod -h -N {} +\n"
}

// ---------------------------------------------------------------------------
// Launch — sudo -u + env -i + sandbox-exec, SandVault-style
// ---------------------------------------------------------------------------

// SandboxPath returns the PATH for the sandboxed agent — its own bin dirs
// first, then the `prefix` (darwin store bin dirs), then system. Mirrors
// sandbox_path.
func SandboxPath(home string, prefix []string) string {
	if home == "" {
		home = SandboxHome()
	}
	parts := []string{
		home + "/.yolo-shims",
		home + "/.local/bin",
		home + "/.npm-global/bin",
		home + "/.local/share/mise/shims",
		home + "/go/bin",
	}
	parts = append(parts, prefix...)
	parts = append(parts, "/usr/bin", "/bin", "/usr/sbin", "/sbin")
	return strings.Join(parts, ":")
}

// LaunchArgv builds the `sudo -u … env -i … sandbox-exec -f … -- <agent>` argv.
// `sandboxEnv` is the fully-resolved launch env as an ordered map (git identity
// + TERM + provider keys); the HOME/USER/SHELL/PATH quartet is not
// caller-overridable. Mirrors launch_argv (incl. env -i ordering, PATH join
// order, and the workspace-centric `cd … && exec …` inner shell).
func LaunchArgv(agentArgv []string, profilePath string, sandboxEnv *jsonx.OrderedMap, workspace, user, home string, pathPrefix []string) []string {
	if user == "" {
		user = SandboxUser
	}
	if home == "" {
		home = SandboxHome()
	}
	protected := map[string]struct{}{"HOME": {}, "USER": {}, "SHELL": {}, "PATH": {}}
	envPairs := []string{
		"HOME=" + home,
		"USER=" + user,
		"SHELL=/bin/zsh",
		"PATH=" + SandboxPath(home, pathPrefix),
	}
	if sandboxEnv != nil {
		for _, k := range sandboxEnv.Keys() {
			if _, ok := protected[k]; ok {
				continue // never let a caller override the identity/PATH quartet
			}
			v, _ := sandboxEnv.Get(k)
			envPairs = append(envPairs, k+"="+asStr(v))
		}
	}
	// Run the agent from the workspace. A login zsh cd's in, then execs the
	// agent so it inherits the TTY and PID.
	quotedAgent := make([]string, len(agentArgv))
	for i, a := range agentArgv {
		quotedAgent[i] = shQuote(a)
	}
	inner := "cd " + shQuote(workspace) + " && exec " + strings.Join(quotedAgent, " ")
	out := []string{
		"sudo",
		"--login",
		"--set-home",
		"--user=" + user,
		"/usr/bin/env",
		"-i",
	}
	out = append(out, envPairs...)
	out = append(out,
		"/usr/bin/sandbox-exec",
		"-f",
		profilePath,
		"--",
		"/bin/zsh",
		"-c",
		inner,
	)
	return out
}

// ---------------------------------------------------------------------------
// Loopholes on the native backend
// ---------------------------------------------------------------------------

// macosLogModes mirrors journal's modes: off (no helper, default), user
// (scoped), full (passthrough).
var macosLogModes = map[string]struct{}{"off": {}, "user": {}, "full": {}}

// BrokerSocketGrantCommands returns the chmod/chgrp argv letting the sandbox
// group reach the broker socket. Mirrors broker_socket_grant_commands.
func BrokerSocketGrantCommands(socketPath, group string) [][]string {
	if group == "" {
		group = SandboxGroup
	}
	parent := pathParent(socketPath)
	return [][]string{
		{"chgrp", group, parent},
		{"chmod", "0750", parent},
		{"chgrp", group, socketPath},
		{"chmod", "0660", socketPath},
	}
}

// MacosLogWrapperScript returns a yolo-log helper wrapping Apple's `log`.
// Mirrors macos_log_wrapper_script byte-for-byte.
func MacosLogWrapperScript(mode string) string {
	if _, ok := macosLogModes[mode]; !ok {
		mode = "off"
	}
	var body string
	switch mode {
	case "off":
		body = "echo \"yolo-log: macOS log access is disabled.\" >&2\n" +
			"echo \"  Enable it by setting \\\"macos_log\\\": \\\"user\\\" (or \\\"full\\\") in yolo-jail.jsonc, then restart.\" >&2\n" +
			"exit 1\n"
	case "full":
		body = "exec /usr/bin/log \"$@\"\n"
	default: // "user"
		body = "if [ \"$#\" -eq 0 ]; then\n" +
			"  exec /usr/bin/log show --last 5m --style compact\n" +
			"fi\n" +
			"case \"$1\" in\n" +
			"  show|stream|collect|config|help)\n" +
			"    exec /usr/bin/log \"$@\" ;;\n" +
			"  *)\n" +
			"    exec /usr/bin/log show \"$@\" ;;\n" +
			"esac\n"
	}
	return "#!/bin/bash\nset -euo pipefail\n" + body
}

// ---------------------------------------------------------------------------
// Helpers (small; pure)
// ---------------------------------------------------------------------------

// SessionProfilePath returns the root-owned per-session Seatbelt profile path.
// Mirrors session_profile_path.
func SessionProfilePath(cname, sd string) string {
	if sd == "" {
		sd = stateDir
	}
	return filepath.Join(sd, "profile-"+cname+".sb")
}

// shQuote single-quotes a string for safe bash embedding. Mirrors _sh_quote
// EXACTLY: "'" + s.replace("'", "'\”") + "'" — this is NOT shlex.quote (it
// always wraps, and empty → "”").
func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

// sbplStr quotes a path as an SBPL double-quoted string literal. Mirrors
// _sbpl_str: escape backslash then double-quote.
func sbplStr(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\"", "\\\"")
	return "\"" + s + "\""
}

// NextFreeID returns the first integer >= floor not in `existing` (SandVault's
// picker). Mirrors next_free_id. Pass floor<=0 to use the default sandboxMinID.
func NextFreeID(existing map[int]struct{}, floor int) int {
	if floor <= 0 {
		floor = sandboxMinID
	}
	uid := floor
	for {
		if _, ok := existing[uid]; !ok {
			return uid
		}
		uid++
	}
}

// asStr renders an OrderedMap value as a string (values in the launch/git-
// identity maps are always strings; a non-string degrades to "").
func asStr(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// sortedKeys returns the sorted keys of a string map (for the bootstrap's
// `sorted(baked.items())`).
func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// reprStr is Python repr() for a string (used by the bootstrap generator).
func reprStr(s string) string { return pytext.Repr(s) }

// itoa formats an int in base 10.
func itoa(n int) string { return strconv.Itoa(n) }

// --- pathlib.PurePath helpers (path-only, matching Python semantics) ---
//
// Python's PurePosixPath treats trailing slashes and repeated slashes
// distinctly from filepath.Clean in some edge cases, but for the /Users/<name>
// membership check the inputs are always already-resolved absolute paths, so a
// clean-based split is faithful. HomeContaining is documented as "path-only".

// pathParent returns the parent of p (PurePath.parent): everything up to the
// last slash, or "/" / p itself for roots. Uses filepath.Dir which matches
// PurePosixPath.parent for absolute inputs.
func pathParent(p string) string { return filepath.Dir(p) }

// pathName returns the final component of p (PurePath.name).
func pathName(p string) string { return filepath.Base(p) }

// resolvePathAbs mirrors str(Path(p).resolve()): make absolute, then resolve
// symlinks best-effort (filepath.EvalSymlinks errors on non-existent paths;
// Python's resolve(strict=False) does not, so fall back to the lexical abs).
func resolvePathAbs(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		abs = p
	}
	if evaled, err := filepath.EvalSymlinks(abs); err == nil {
		return evaled
	}
	return abs
}

// pathParents returns p's ancestor chain (PurePath.parents): parent, grandparent,
// … up to the root, in that order.
func pathParents(p string) []string {
	var out []string
	cur := p
	for {
		parent := filepath.Dir(cur)
		if parent == cur {
			break
		}
		out = append(out, parent)
		cur = parent
	}
	return out
}
