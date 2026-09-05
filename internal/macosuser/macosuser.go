// Package macosuser is the native macOS backend that isolates an agent in a
// dedicated hidden macOS user hardened with an Apple Seatbelt (sandbox-exec)
// profile: no Linux container, no VM, no arch switch. Based on SandVault's
// design (github.com/webcoyote/sandvault).
// Every artifact producer here is a pure data-returning function (command
// lists, ACL ACE strings, the SBPL profile, launch argv, the in-process
// entrypoint bootstrap), so the security properties are fully unit-testable on
// Linux CI without a Mac. Only RunMacosUser and the macos-* command bodies
// shell out, guarded to macOS.
package macosuser

import (
	"path/filepath"
	"strconv"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/pytext"
)

// Dedicated account constants. Frozen contract (must not drift — the run-path
// argv builders, ACLs, and teardown all key off these exact names/paths).
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
	mvBin    = "/bin/mv"
	rmBin    = "/bin/rm"

	// packsLeaf is the state-dir subdir holding each session's staged pack tree.
	packsLeaf = "packs"
	// homeOverlayLeaf is the state-dir subdir holding each session's staged CONTENT
	// tree — skills and briefings, laid out at their home-relative destinations.
	homeOverlayLeaf = "home-overlay"
)

// SandboxHome is /Users/_yolojail.
func SandboxHome() string { return "/Users/" + SandboxUser }

// SharedRootDefault is the neutral shared-workspace root (/Users/Shared/yolo).
// A NEUTRAL directory outside every user's home — the crux of the model's
// "clear semantics".
func SharedRootDefault() string { return "/Users/Shared/yolo" }

// ---------------------------------------------------------------------------
// Account provisioning — command lists (pure; executed by the orchestrator)
// ---------------------------------------------------------------------------
// CreateUserCommands returns the dscl/dseditgroup argv to create the hidden
// sandbox account.
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
// files.
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
// (setgid), plus the inheriting ACL ACEs applied to the root itself.
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
// Staging the yolo binary into the root-owned state dir
// ---------------------------------------------------------------------------
// StagedYoloPath returns where the running yolo binary is staged for the sandbox
// user to self-exec (root-owned so the sandbox can't rewrite the launch binary;
// world-readable+executable so it can run).
func StagedYoloPath(sd string) string {
	if sd == "" {
		sd = stateDir
	}
	return filepath.Join(sd, "yolo")
}

// StageBinaryCommands returns the sudo argv that stage the running yolo binary
// (selfExe = os.Executable()) into the root-owned state dir for the sandbox user
// to self-exec as `yolo internal darwin-bootstrap` (J2 §3). This replaces the
// old StageEntrypointCommands, which copied the deleted src/entrypoint tree.
//
// Staging goes copy-to-temp then atomic mv, guaranteeing a FRESH INODE: macOS
// caches Mach-O code signatures per vnode, so overwriting a previously staged
// binary in place gets the next exec SIGKILLed (invalid signature). A rename
// over the old path drops the old vnode. The staged copy is chmod a+rX so the
// sandbox uid can read+exec it, and the host checkout (which may be unreadable
// to the sandbox uid) is never on the launch path — self-staging serves Track D
// too (an installed-only Mac has no checkout).
func StageBinaryCommands(selfExe, sd string) [][]string {
	if sd == "" {
		sd = stateDir
	}
	dst := StagedYoloPath(sd)
	tmp := dst + ".new"
	return [][]string{
		{mkdirBin, "-p", sd},
		{cpBin, "-f", selfExe, tmp},
		{chmodBin, "a+rX", tmp},
		{mvBin, "-f", tmp, dst}, // atomic rename → fresh inode, drops the cached-signature vnode
	}
}

// ---------------------------------------------------------------------------
// Staging the pack trees into the root-owned state dir
// ---------------------------------------------------------------------------
// StagedPackRoot returns where this session's pack trees are staged for the
// sandbox user to read: <stateDir>/packs/<cname>. This is the macos-user analogue
// of the container's `:ro` /ctx/packs mount, and it is root-owned for the same
// reason that mount is read-only — a pack manifest is an INPUT to composition, so
// an agent able to rewrite one could grant its own pack a host file on the next
// launch.
//
// It is a COPY under /var rather than the host-side staging tree itself, which
// lives under the invoking user's ~/.local/share/yolo-jail. Two reasons, both
// structural: the sandbox uid has no business traversing the admin user's home
// (that home is what this backend isolates the agent FROM, and the same state dir
// holds the agent credential store), and it could not reliably do so anyway —
// a macOS home is not required to be world-traversable, so pointing the sandbox at
// one is a permission failure waiting to read as "packs silently did nothing",
// which is the exact defect this whole path exists to end.
func StagedPackRoot(cname, sd string) string {
	if sd == "" {
		sd = stateDir
	}
	return filepath.Join(sd, packsLeaf, cname)
}

// StagePackCommands returns the sudo argv that copy the host-side staged pack tree
// (stagePacks' root) into the root-owned state dir, world-readable, for the
// bootstrap to render from. Empty when there is no host tree to copy — a launch
// with no packs stages nothing rather than an empty directory.
//
// Replace-by-rename, like StageBinaryCommands and for a related reason: the tree
// must flip atomically from the previous launch's pack set to this one, and a `cp`
// over a live directory would leave a union of the two — a pack the user dropped
// from `packs` would keep rendering, which is precisely the bug pruneDroppedPackStaging
// exists to prevent on the host side. The destination is removed BEFORE the rename
// because `mv src dst` moves src INSIDE dst when dst is an existing directory; that
// one is not a nicety, it is the difference between replacing the tree and nesting
// it one level deeper every launch.
func StagePackCommands(hostPackRoot, cname, sd string) [][]string {
	if hostPackRoot == "" {
		return nil
	}
	if sd == "" {
		sd = stateDir
	}
	dst := StagedPackRoot(cname, sd)
	tmp := dst + ".new"
	return [][]string{
		{mkdirBin, "-p", filepath.Join(sd, packsLeaf)},
		{rmBin, "-rf", tmp},
		{cpBin, "-R", hostPackRoot, tmp},
		{chmodBin, "-R", "a+rX", tmp},
		{rmBin, "-rf", dst},
		{mvBin, "-f", tmp, dst},
	}
}

// StagedHomeOverlay is where a session's composed CONTENT tree lands, root-owned and
// world-readable so the sandbox uid can read it and cannot rewrite it.
//
// Beside the packs and staged the same way, for the same reason: both are host-composed
// input the sandbox must READ and must not be able to edit into something the next
// launch trusts. The overlay is the stronger case of the two — it carries skills and
// briefing prose the agent then FOLLOWS, so a sandbox-writable copy would let a jail
// rewrite its own instructions between launches.
func StagedHomeOverlay(cname, sd string) string {
	if sd == "" {
		sd = stateDir
	}
	return filepath.Join(sd, homeOverlayLeaf, cname)
}

// StageHomeOverlayCommands returns the sudo argv that stage the composed content tree
// (skills + briefings, already laid out at their home-relative paths) for this session.
// Empty hostOverlay → no commands, so a jail with no content to deliver pays nothing.
//
// Same rm-then-mv shape as the packs: the destination is REPLACED rather than merged
// into, because a destination that left the config must stop being delivered and a
// merge would keep serving it forever.
func StageHomeOverlayCommands(hostOverlay, cname, sd string) [][]string {
	if hostOverlay == "" {
		return nil
	}
	if sd == "" {
		sd = stateDir
	}
	dst := StagedHomeOverlay(cname, sd)
	tmp := dst + ".new"
	return [][]string{
		{mkdirBin, "-p", filepath.Join(sd, homeOverlayLeaf)},
		{rmBin, "-rf", tmp},
		{cpBin, "-R", hostOverlay, tmp},
		{chmodBin, "-R", "a+rX", tmp},
		{rmBin, "-rf", dst},
		{mvBin, "-f", tmp, dst},
	}
}

// ---------------------------------------------------------------------------
// Workspace location — must be neutral ground, never inside a home
// ---------------------------------------------------------------------------
// HomeContaining returns the user-home dir that contains `workspace`, or ""
// when the workspace is on neutral ground. A "home" is a direct
// child of /Users other than /Users/Shared. Pure and path-only. The bool is
// false when no home contains the workspace.
func HomeContaining(workspace, usersRoot string) (string, bool) {
	if usersRoot == "" {
		usersRoot = "/Users"
	}
	// Check the workspace itself, then each ancestor up to the root.
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
// file).
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
// hot path).
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

// WorkspaceGrantedScript returns a bash test that exits 0 when `dir` carries an
// ACE granting the sandbox group, and non-zero when it does not.
//
// WHY A WORKSPACE CAN LACK THE GRANT. Two causes, both measured 2026-09-03; the
// second is the one that actually bit and it is not the one anybody expected.
//
//  1. CREATION ORDER. macOS applies inheritable ACEs at CREATE time and never
//     retroactively, and SharedRootProvisionCommands sets `chmod +a` on the shared
//     ROOT only (no -R, deliberately — a per-run walk is what the inheriting ACE
//     exists to avoid). So a directory that already existed when the ACEs were
//     added never receives them. "Projects created under it are shared
//     automatically" is true, and says nothing about projects created BEFORE.
//
//  2. A RECREATED ACCOUNT. ACLs store a principal's UUID, not its name — so an ACE
//     survives renaming an account and does NOT survive deleting and recreating
//     one. On the maintainer's Mac EVERY directory under the shared root carried
//     correctly-inherited ACEs naming uuid 0E7B72D7-…, which resolves to no user
//     and no group: the _yolojail account had been recreated with a fresh
//     GeneratedUID (the live group is a different uuid). The grants looked right in
//     `ls` and granted nothing. A teardown+setup cycle is enough to cause this.
//
// What that cost: the launch spent a sudo prompt, staged, ran the bootstrap, and
// died six generators deep with `mkdir <ws>/.yolo/prism: permission denied` — a
// message naming neither ACLs nor `yolo macos-fix-permissions`, the command that
// already existed to fix it.
//
// WHAT DOES AND DOES NOT INHERIT, measured on macOS 26.5 rather than assumed.
// Everything that CREATES a new object inside the dir inherits: mkdir, touch, cp,
// `cp -p`, `cp -a`, `cp -R`, ditto, `rsync -a`, `tar -x`, `git init`/`clone`. The
// only creator that does NOT is `mv` within a volume, because rename(2) creates
// nothing — the inode keeps whatever ACL it already had. (84c55268 names "rename /
// cp -p" as the miss; the `cp -p` half is wrong on 26.5 — it inherits, and when the
// SOURCE carries its own ACL the destination ends up with both.)
//
// Inheritance is NOT a function of the creating user. It is driven entirely by the
// parent's inheritable ACEs: the creator decides the new object's owner, and the
// setgid bit its group, but the ACL comes from the parent whoever writes it.
//
// WHAT THIS DOES AND DOES NOT PROVE. It detects the KNOWN cause — the ACE yolo
// itself applies is absent — and it is exact for that. It is not a general
// writability oracle: only the sandbox uid can answer that, and probing as it
// would cost a sudo prompt of its own before the one the launch already spends.
// So the bootstrap's own failure remains the backstop for everything else, and
// this exists to convert the common case from a dead end into one command.
func WorkspaceGrantedScript(dir, group string) string {
	if group == "" {
		group = SandboxGroup
	}
	// `(inherited )?` is LOAD-BEARING and was missing in the first cut. `ls -lde`
	// renders a directly-applied ACE as "group:g allow …" and an INHERITED one as
	// "group:g inherited allow …" — so a literal match on the first spelling
	// false-negatives every workspace that inherited correctly, which is the
	// common good case this check exists to wave through. Measured 2026-09-03
	// against real `ls` output; the first cut's unit test stubbed RunBash and so
	// never ran the grep against anything.
	//
	// Matching the NAME rather than a uuid is also what makes this catch a stale
	// grant: an ACE naming a principal that no longer exists renders as a bare
	// uuid (ls cannot resolve it), so it matches nothing here — which is correct,
	// because it grants the current sandbox account nothing.
	return "/bin/ls -lde " + shQuote(dir) +
		" | /usr/bin/grep -qE " + shQuote("group:"+group+" (inherited )?allow") + "\n"
}

// UngrantedChildrenScript lists the immediate children of `root` that the sandbox
// group has no grant on, printing each and exiting 0 when there is at least one
// (1 when every child is already shared).
//
// WHY CHILDREN, AND WHY AT SETUP. An earlier cut of this detected a STALE grant
// specifically — an ace naming a uuid that no longer resolves, which is what a
// teardown+setup cycle leaves behind. That was over-clever twice over. It parsed
// `ls -le` output shape to recognise an unresolvable principal, and it answered a
// narrower question than the one setup actually has: after provisioning, ANY
// pre-existing child may be unshared, and the reason does not matter. A workspace
// that predates setup has no ace at all; one moved in with `mv` has whatever it
// brought; one left by a recreated account has a dead ace. All three are "not
// granted", all three have the same fix, and WorkspaceGrantedScript already
// answers all three — so there is nothing for a second, more fragile detector to
// add.
//
// It is O(number of workspaces) — one `ls` each, ~10ms for a handful — because the
// shared root's children ARE the workspaces. That is what makes it affordable at
// setup where a full walk is not: the check is bounded by how many projects you
// have, and only the REPAIR is bounded by how big they are.
//
// Reporting by name matters: "2 of 3 workspaces are not shared" plus their names
// is actionable, where "something under the root needs fixing" sends the user to
// walk it themselves.
func UngrantedChildrenScript(root, group string) string {
	if group == "" {
		group = SandboxGroup
	}
	pat := shQuote("group:" + group + " (inherited )?allow")
	return "set -u\n" +
		"found=1\n" +
		"for d in " + shQuote(root) + "/*/; do\n" +
		"  [ -d \"$d\" ] || continue\n" +
		"  if ! /bin/ls -lde \"${d%/}\" | /usr/bin/grep -qE " + pat + "; then\n" +
		"    echo \"  • ${d%/}\"\n" +
		"    found=0\n" +
		"  fi\n" +
		"done\n" +
		"exit $found\n"
}

// WorkspaceACLStripScript returns the find-based bash script that removes ALL
// ACLs from the workspace (chmod -h -N).
func WorkspaceACLStripScript(workspace string) string {
	return "set -euo pipefail\n" +
		"ws=" + shQuote(workspace) + "\n" +
		"find \"$ws\" -exec chmod -h -N {} +\n"
}

// ---------------------------------------------------------------------------
// Launch — sudo -u + env -i + sandbox-exec, SandVault-style
// ---------------------------------------------------------------------------
// SandboxPath returns the PATH for the sandboxed agent — the two generated dirs, then its
// own install prefixes, then the `prefix` (darwin store bin dirs), then system.
//
// THE THIRD COPY OF entrypoint.BootPath's ORDER, and it moves with it. B2
// (program-delivery.md §3.5, OQ-PD12a) puts ~/.yolo/bin/launch SECOND, ahead of the
// install prefixes: a launcher ordered after what it installs is unreachable from its own
// second invocation onward, so the update arm it carries never runs. ~/.yolo/bin/block
// stays first — interception must win over installation.
//
// macos-user is the backend where this matters most and hides least: it bakes no image, so
// the only thing a launcher could shadow here is a `packages:` store entry or a system
// binary, and the generation-time collision check (entrypoint's launchercollision.go)
// reads exactly this string, minus the per-home prefixes, to decide.
func SandboxPath(home string, prefix []string) string {
	if home == "" {
		home = SandboxHome()
	}
	parts := []string{
		home + "/.yolo/bin/block",
		home + "/.yolo/bin/launch",
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
// order, and the workspace-centric `cd … && exec …` inner shell).
func LaunchArgv(agentArgv []string, profilePath string, sandboxEnv *jsonx.OrderedMap, workspace, user, home string, pathPrefix []string) []string {
	if user == "" {
		user = SandboxUser
	}
	if home == "" {
		home = SandboxHome()
	}
	envPairs := sandboxEnvPairs(home, user, SandboxPath(home, pathPrefix), sandboxEnv)
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

// sandboxEnvPairs renders the `env -i` K=V list for a process run AS the sandbox user: the
// HOME/USER/SHELL/PATH quartet this backend owns, then everything the caller composed.
//
// The quartet is PROTECTED — a caller's own HOME or PATH is dropped, never merged — because
// these four are what make the process the sandbox user's rather than a copy of whatever the
// invoking shell had. Shared by the agent launch (LaunchArgv) and the install-capture driver
// (CaptureDriverArgv), which differ in what they exec and in nothing about the environment they
// exec it in; two spellings of that would be two ways for a capture to stop resembling a launch.
func sandboxEnvPairs(home, user, pathValue string, sandboxEnv *jsonx.OrderedMap) []string {
	protected := map[string]struct{}{"HOME": {}, "USER": {}, "SHELL": {}, "PATH": {}}
	envPairs := []string{
		"HOME=" + home,
		"USER=" + user,
		"SHELL=/bin/zsh",
		"PATH=" + pathValue,
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
	return envPairs
}

// ---------------------------------------------------------------------------
// Loopholes on the native backend
// ---------------------------------------------------------------------------
// (scoped), full (passthrough).
var macosLogModes = map[string]struct{}{"off": {}, "user": {}, "full": {}}

// endpointReadRights is the ACE right-set a host service's published endpoint
// file needs, and nothing more. READ, never write: a Unix socket needed write to
// connect(2), a file needs only read, and the sandbox has no reason to rewrite
// its own endpoint (loophole-transport.md OQ-T5 — it gains nothing by doing so,
// since the file already holds its own token).
const endpointReadRights = "read,readattr,readextattr,readsecurity"

// EndpointGrantCommands returns the `chmod +a` argv letting the sandbox USER read
// one published endpoint file.
//
// Why a grant is needed at all: GuestProfileMacOS() carries PrimSeparateUser and
// macos-user runs the sandbox as SandboxUser, so the process that must READ the
// endpoint file is a different uid from the one that WROTE it. The file is 0600
// and its directory 0700 — deliberately, because the file carries this jail's
// bearer token and internal/svcendpoint refuses to publish into a directory that
// is group- or world-accessible. Without an explicit grant the sandbox cannot
// reach it, which is the one place PrimSeparateUser costs something.
//
// Two ACEs, and the shape is the point:
//
//   - read on the FILE (endpointReadRights).
//   - search — traverse, not list — on the file's own directory, which is the
//     ONLY ancestor that blocks the sandbox: yolo creates that one 0700 and every
//     ancestor above it (/private/tmp at 1777, /private, /) is already
//     world-searchable, so walking further would modify ACLs on shared system
//     directories to no effect.
//
// A `user:` ACE, not a `group:` one: SandboxGroup contains the host user
// (SharedRootProvisionCommands adds them), so a group ACE would widen the grant
// past the single account that needs it.
//
// This replaces BrokerSocketGrantCommands, which had zero call sites and no test
// and was never executed. With its only plausible argument
// (/tmp/yolo-claude-oauth-broker.sock) it emitted `chgrp _yolojail /tmp` plus
// `chmod 0750 /tmp` — group-owning the machine's /tmp and stripping its sticky
// bit. Its chgrp+chmod-the-parent shape is exactly what an ACE avoids here: under
// loopback-tls the parent of one credential is a directory full of OTHER jails'
// credentials, so widening it is a credential-boundary regression on the one
// backend whose entire point is that boundary.
//
// NOT EXECUTABLE ON LINUX — `chmod +a` is a macOS ACL extension. This builds the
// argv and is unit-tested on the emitted strings; only a Mac can run it. It has
// no call site yet: macos-user does not start host services at all today (the
// broker is unwired there — Thread B), so this is the piece that has to exist
// before that wiring can, not a behaviour change.
func EndpointGrantCommands(endpointPath, user string) [][]string {
	if user == "" {
		user = SandboxUser
	}
	return [][]string{
		{chmodBin, "+a", "user:" + user + " allow " + endpointReadRights, endpointPath},
		{chmodBin, "+a", "user:" + user + " allow search", pathParent(endpointPath)},
	}
}

// MacosLogWrapperScript returns a yolo-log helper wrapping Apple's `log`.
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
func SessionProfilePath(cname, sd string) string {
	if sd == "" {
		sd = stateDir
	}
	return filepath.Join(sd, "profile-"+cname+".sb")
}

// shQuote single-quotes a string for safe bash embedding: it ALWAYS wraps in
// single quotes (an empty string becomes an empty quoted pair), escaping any
// embedded quote by closing, adding an escaped quote, and reopening. The
// unconditional wrapping is deliberate — the SBPL/argv builders depend on it.
func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

// sbplStr quotes a path as an SBPL double-quoted string literal: escape
// backslash then double-quote.
func sbplStr(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\"", "\\\"")
	return "\"" + s + "\""
}

// NextFreeID returns the first integer >= floor not in `existing` (SandVault's
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

// reprStr renders a string as a repr()-style quoted literal (used by the
// git-identity dict repr in the dry-run plan).
func reprStr(s string) string { return pytext.Repr(s) }

// itoa formats an int in base 10.
func itoa(n int) string { return strconv.Itoa(n) }

// --- path helpers (path-only, purely lexical) ---
// The /Users/<name> membership check always runs on already-resolved absolute
// paths, so a clean-based split is faithful and HomeContaining stays path-only.
// pathParent returns the parent of p: everything up to the last slash, or "/" /
// p itself for roots.
func pathParent(p string) string { return filepath.Dir(p) }

// pathName returns the final component of p.
func pathName(p string) string { return filepath.Base(p) }

// resolvePathAbs makes absolute, then resolves symlinks best-effort.
// filepath.EvalSymlinks errors on non-existent paths, so fall back to the
// lexical abs (a non-existent workspace must still resolve for the plan).
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

// pathParents returns p's ancestor chain: parent, grandparent, … up to the
// root, in that order.
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
