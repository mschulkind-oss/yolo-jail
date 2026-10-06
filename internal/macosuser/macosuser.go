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
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/pytext"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
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
	// ctxLeaf is the state-dir subdir holding each session's staged CONTEXT tree —
	// the HOST BYTES a `/ctx` mount carries on every other backend.
	ctxLeaf = "ctx"
	// capturesLeaf is the state-dir subdir holding the root-owned copy of the install-capture
	// store's entries this backend's launches materialize from (StagedCapturesRoot). One per
	// MACHINE, not per session: its entries are content-addressed, so a copy made for one
	// workspace serves every other.
	capturesLeaf = "captures"
)

// SandboxHome is /Users/_yolojail.
func SandboxHome() string { return "/Users/" + SandboxUser }

// SandboxMiseData is the mise data dir for a sandbox home: <home>/.yolo/mise.
//
// IT IS THE MACHINE TIER, AND IT HAS TO BE NAMED TO STAY THERE. mise's own default is
// $HOME/.local/share/mise (entrypoint.NewEnv resolves the same fallback), and under the
// home-tier layout ~/.local is a symlink into <workspace>/.yolo/home — so leaving the
// default in place would put the tool store in the PER-WORKSPACE tier, where no other
// backend keeps it: the container mounts one machine-wide store at /mise
// (internal/cli/run/assemble_parts.go) for every workspace on the host.
// docs/design/macos-user-home-tiers.md §5 states this as a precondition of the layout, and
// macos-user-provisioning.md's OQ-P3 takes its answer from it.
//
// Under ~/.yolo because that is yolo's own namespace in the home. Its sibling ~/.yolo/bin
// IS a layout symlink (the generated-script anchor is per-workspace on every backend), so
// the two tiers do meet inside that directory — which is the layout's rule working, not an
// exception to it: a path is workspace tier when the layout links it and machine tier
// otherwise.
func SandboxMiseData(home string) string {
	if home == "" {
		home = SandboxHome()
	}
	return filepath.Join(home, ".yolo", "mise")
}

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
	cmds := [][]string{
		// Group
		{"dscl", ".", "-create", "/Groups/" + group},
		{"dscl", ".", "-create", "/Groups/" + group, "PrimaryGroupID", itoa(gid)},
		{"dscl", ".", "-create", "/Groups/" + group, "RealName", "YOLO Jail"},
		// User
		{"dscl", ".", "-create", "/Users/" + user},
		{"dscl", ".", "-create", "/Users/" + user, "UniqueID", itoa(uid)},
		{"dscl", ".", "-create", "/Users/" + user, "PrimaryGroupID", itoa(gid)},
		{"dscl", ".", "-create", "/Users/" + user, "RealName", "YOLO Jail"},
		{"dscl", ".", "-create", "/Users/" + user, "NFSHomeDirectory", SandboxHome()},
		{"dscl", ".", "-create", "/Users/" + user, "UserShell", "/bin/zsh"},
		// Hidden from the login window
		{"dscl", ".", "-create", "/Users/" + user, "IsHidden", "1"},
		// Not a real login user: strip from staff
		{"dseditgroup", "-o", "edit", "-d", user, "-t", "user", "staff"},
		// Shared group membership (host user + sandbox user) for the ACL
		{"dseditgroup", "-o", "edit", "-a", user, "-t", "user", group},
		{"dseditgroup", "-o", "edit", "-a", hostUser, "-t", "user", group},
	}
	// Provision the home dir with correct ownership + 0750.
	return append(cmds, ProvisionHomeCommands()...)
}

// ProvisionHomeCommands returns the argv that make the account home exist with the
// ownership and mode the backend needs: `createhomedir`, then `chown -R` + `chmod 750`.
// Run under sudo.
//
// SEPARATE FROM CreateUserCommands (which ends with exactly these) because the ACCOUNT and
// its HOME are two facts that can be true independently, and only one of them was ever
// checked. `sudo rm -rf /Users/_yolojail` leaves the dscl record and takes the directory —
// which is what the runbook prescribes for an account predating the home-tier layout — and
// the sandbox uid cannot repair that itself, since /Users is root-owned 0755. So setup
// calls this list on its own for a home that went missing under an account that did not,
// and CreateUserCommands appends it rather than spelling it a second time.
func ProvisionHomeCommands() [][]string {
	home := SandboxHome()
	return [][]string{
		{"createhomedir", "-c", "-u", SandboxUser},
		{"chown", "-R", SandboxUser + ":" + SandboxGroup, home},
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
//
// IN GuestBinDir, beside the darwin in-jail binaries (jaildaemon.go), since OQ-DP8: the
// guest's one prefix of yolo's own binaries, the counterpart of a container's
// /opt/yolo-jail/bin. It sat at the state dir's root until then; the move changes no PATH
// ORDER, because SandboxPath derives its one entry from this path's directory.
func StagedYoloPath(sd string) string {
	return GuestBinaryPath("yolo", sd)
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
		{mkdirBin, "-p", filepath.Dir(dst)},
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
// from `packs` would keep rendering, which is precisely the bug a fresh pack tree per launch
// rules out on the host side (docs/reference/pack-system.md#oq-pk2). The destination is removed BEFORE the rename
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
// Staging the /ctx CONTEXT TREE into the root-owned state dir
// ---------------------------------------------------------------------------
// StagedCtxRoot returns where this session's CONTEXT TREE is staged for the
// sandbox user to read: <stateDir>/ctx/<cname>. It is what $YOLO_CTX_ROOT names,
// and it is the macos-user analogue of the container's /ctx mounts — one leaf over
// from the packs and the home overlay, staged the same way and for the same reasons.
//
// WHY NOT /ctx ITSELF. A new TOP-LEVEL directory on macOS needs an /etc/synthetic.conf
// entry and a reboot; the repo already knows this for /nix (check.checkNixStore's hint).
// Per-machine root-dir creation is off the table, so the jail is TOLD where the tree
// landed (entrypoint's YOLO_CTX_ROOT) rather than assuming a constant — the same rule
// YOLO_PACK_ROOT and entrypoint.CapturesDirEnv already follow.
//
// WHY NOT THE HOME TIERS. The per-workspace tier resolves to <workspace>/.yolo/home
// (docs/design/macos-user-home-tiers.md), which is inside the profile's `(subpath ws)`
// and therefore AGENT-WRITABLE. A context tree the agent can rewrite is not a context
// tree: it is an input to config composition, so an agent able to edit one composes its
// own next launch. Under /var it is root-owned AND outside the profile's enumerated
// writable set, which is where the read-only half comes from for free.
//
// THE `:ro` HALF COSTS NO SBPL, AND THAT IS MEASURED. SeatbeltProfile is
// `(deny file-write* (subpath "/"))` followed by an enumerated allow — workspace,
// sandbox home, /tmp, /private/tmp, /var/folders, /private/var/folders, /dev — and
// /var/yolo-jail is in none of them, while reads land on `(allow default)`. Measured on
// hardware 2026-09-13 (macOS 26.5, arm64): under a real session profile over this tree,
// `head -c 4 /var/yolo-jail/yolo` succeeded and `touch /var/yolo-jail/canary` gave
// `Operation not permitted`, where the same touch UNSANDBOXED gives `Permission denied`.
// EPERM vs EACCES is what proves the MAC half is doing the work rather than the
// directory's owner — which is stronger than the container backends' `host_files
// readonly`, whose own reference text concedes "0444 is DAC, not kernel enforcement".
func StagedCtxRoot(cname, sd string) string {
	if sd == "" {
		sd = stateDir
	}
	return filepath.Join(sd, ctxLeaf, cname)
}

// StageCtxCommands returns the sudo argv that copy the host-side composed context tree
// into the root-owned state dir, readable by the sandbox account and by no other, for the
// bootstrap to compose from.
// Empty hostCtxTree → no commands, so a launch with no host bytes to carry pays nothing.
//
// Same rm-then-mv shape as the packs and the home overlay, and here the reason is the
// sharpest of the three: a grant the user REVOKED — a pack dropped from `packs`, a
// `host_files` entry deleted from their config — must stop being delivered, and a `cp`
// over a live directory would leave the union of both launches. A stale settings.json
// that keeps composing into the agent's config is exactly the "config file that looks
// correct and is missing the user's own settings" OQ-CO10 refuses.
//
// NOT A SYMLINK FARM, and Seatbelt was never the reason. Measured 2026-09-13, probe 1:
// a profile denying reads under one directory also denies a `cat` of a symlink in an
// ALLOWED directory pointing into it, for an absolute link and a relative one alike —
// so Seatbelt evaluates the TARGET, and a link into the invoking user's home would need
// a per-source allow. That half is fixable; the DAC half is not fixable by any profile.
// The sandbox runs as a foreign uid (SandboxUser, via LaunchArgv's `sudo -u`) and a
// macOS home is not required to be world-traversable, so the link would fail at the
// POSIX layer before the profile was ever consulted — StagedPackRoot's doc comment
// already ruled this exact question for the neighbouring feature.
func StageCtxCommands(hostCtxTree, cname, sd string) [][]string {
	if hostCtxTree == "" {
		return nil
	}
	if sd == "" {
		sd = stateDir
	}
	dst := StagedCtxRoot(cname, sd)
	tmp := dst + ".new"
	return [][]string{
		{mkdirBin, "-p", filepath.Join(sd, ctxLeaf)},
		{rmBin, "-rf", tmp},
		{cpBin, "-R", hostCtxTree, tmp},
		{chmodBin, "-R", "a+rX", tmp},
		// THE TREE'S ROOT IS CLOSED TO EVERY OTHER ACCOUNT, and opened to the sandbox's alone:
		// these are the user's own files — a directory host_files entry is routinely ~/.aws — and
		// `a+rX` alone left every one readable by any local account under /var. The contents keep
		// `a+rX`, which the sandbox account's reads need; nobody else can reach them past a 0700
		// root. A `user:` ACE for sandboxFileReadAce's reason (SandboxGroup holds the host user),
		// with list as well as search, because the bootstrap and the agent walk this directory
		// (SandboxEnvDirCommands grants search alone: a file there is opened by name).
		// UNMEASURED on a Mac (context-mounts.md CX-D24): both rights are spelled as in the
		// shared root's provisioning ACE (dirRights), but no Mac has yet run a launch through a
		// root closed this way, and if the ACE does not admit the sandbox nothing here arrives.
		{chmodBin, "0700", tmp},
		{chmodBin, "+a", "user:" + SandboxUser + " allow list,search", tmp},
		{rmBin, "-rf", dst},
		{mvBin, "-f", tmp, dst},
	}
}

// StageEmptyCtxCommands stages an EMPTY root-owned directory at StagedCtxRoot, for a launch
// whose host CLI composed no context tree: $YOLO_CONTEXT_DIR is exported on every launch
// (docs/design/context-mounts.md CX-D4), so the directory it names must exist even when
// nothing is in it — an empty one says "no context mounts", which is true. Same rm-then-mv
// shape as StageCtxCommands, so a previous launch's tree never survives into this one.
func StageEmptyCtxCommands(cname, sd string) [][]string {
	if sd == "" {
		sd = stateDir
	}
	dst := StagedCtxRoot(cname, sd)
	tmp := dst + ".new"
	return [][]string{
		{mkdirBin, "-p", filepath.Join(sd, ctxLeaf)},
		{rmBin, "-rf", tmp},
		{mkdirBin, tmp},
		{chmodBin, "755", tmp},
		{rmBin, "-rf", dst},
		{mvBin, "-f", tmp, dst},
	}
}

// ---------------------------------------------------------------------------
// Staging the INSTALL-CAPTURE STORE's entries into the root-owned state dir
// ---------------------------------------------------------------------------

// CaptureEntry is one install-capture store entry (internal/capture) a launch stages for the
// sandbox, so the generated launcher of the program it holds materializes it instead of running
// the vendor's installer (docs/design/program-delivery.md §6.3). The HOST CLI picks it, through
// the resolver the launcher's own `capture-materialize` asks (internal/cli's resolveCaptureFor),
// at this backend's platform; this package only stages what it is handed, for HostContext's
// reason: finding it reads the invoking user's state dir.
type CaptureEntry struct {
	// Bin is the program the entry was captured for, for the dry run.
	Bin string
	// Key is the entry's store key: its directory name under the store's entries/.
	Key string
	// Source is the entry directory in the user's store (<CapturesDir>/entries/<key>), read by
	// root when it is copied and never by the sandbox.
	Source string
}

// StagedCapturesRoot is the root-owned copy of the install-capture store a macos-user sandbox
// reads: <stateDir>/captures, laid out as the store is (entries/<key>), so the launcher's
// `capture-materialize --store=` takes it unchanged. It is what entrypoint.CapturesDirEnv names
// to the bootstrap, which bakes it into every generated launcher.
//
// docs/plans/install-capture.md hand-off H4, answered (b): a neutral machine store outside every
// home, root-owned and read-only to the sandbox — an implementation decision under the
// maintainer's 2026-10-04 delegation, reversible. It changes no byte of the session Seatbelt
// profile: reads outside /Users, /Volumes and the keychains fall on its `(allow default)`, and
// reads under /var/yolo-jail were MEASURED on hardware 2026-09-13 (StagedCtxRoot). It is the
// pattern StagedPackRoot and StageCtxCommands follow, for their reason: the user's store sits
// under the invoking user's home, which the sandbox account may not traverse.
//
// A COPY, never a link of the store's inodes. An entry a macos-user capture made holds files the
// sandbox account owns, under the shared-group ACL its staging tree carries (CaptureStagingCommands),
// so a hardlink of one would let the sandbox rewrite bytes every workspace on the machine runs
// (INFERRED from that ownership; no Mac has tried it). Copied by root, every byte here is root's.
// The store is not a confidentiality boundary, here or on a container, where every jail reads it
// whole through a `:ro` bind (internal/cli/run's captures.go).
func StagedCapturesRoot(sd string) string {
	if sd == "" {
		sd = stateDir
	}
	return filepath.Join(sd, capturesLeaf)
}

// capturesEntriesLeaf and capturesStagingLeaf are the staged store's two children, the user
// store's own names (internal/capture's store.go): entries/ is all a reader scans, and staging/
// holds a copy in flight, where no reader looks.
const (
	capturesEntriesLeaf = "entries"
	capturesStagingLeaf = "staging"
)

// stageCaptureScriptName and pruneCapturesScriptName are the $0 of the two root scripts below,
// so `ps` and a failure message name which one ran.
const (
	stageCaptureScriptName  = "yolo-stage-capture"
	pruneCapturesScriptName = "yolo-prune-captures"
)

// stageCaptureScript copies ONE entry into the staged store unless it is already there: $1 the
// staged root, $2 the key, $3 the entry in the user's store. Run by root (sudo), so the copy is
// root's; `cp -R` copies a link as a link and follows none, on BSD and GNU alike. The copy is
// made under staging/ and renamed into entries/ last, so a reader scanning entries/ never meets
// a half-copied entry: a `<key>.new` beside the entries would be scanned, and would win
// selection's tie-break over the entry it copies.
//
// "ALREADY THERE" IS A FACT ABOUT THE KEY, and a key is a content address: an entry staged under
// it holds the bytes it names, so it is copied once per machine. Decided when the command runs,
// as root, rather than by the host CLI reading /var/yolo-jail first, so a plan stays a pure
// function of its inputs and no check can be stale by the time the copy would have run.
//
// A COPY THAT FAILS REMOVES WHAT IT MADE and exits non-zero. Any step can fail — a full disk
// partway through a 1.2 GB plain copy, an I/O error, a source the user's store reaped after the
// host picked it — and the half-made tree goes at once rather than at the next launch's prune, so
// a launch on a full disk does not keep it for the rest of its run. The launch runs this
// best-effort (RunPlan.CaptureStageCommands), so the failure costs the program its copy and
// nothing else: its launcher misses and downloads.
//
// NO ACL CROSSES, and nothing here strips one. An entry a macos-user capture made carries the
// shared group's inherited ACE (`group:_yolojail allow read,write,…,writesecurity,chown`, set on
// its staging tree by CaptureStagingCommands; the store's renames keep it, and freezeTree drops
// mode bits only), and an ACE grants what the mode bits deny. Plain `cp -R` does not carry it:
// Apple's cp copies a file's ACL only under -p (file_cmds cp/utils.c, `if (pflag &&
// fcopyfile(…, COPYFILE_ACL)`) and a directory's only under -p (cp.c, preserve_dir_acls). The
// extended attributes it copies without -p are what flistxattr lists (copyfile.c's
// copyfile_xattr), and that the kernel leaves the ACL out of that list is INFERRED, not read. Nor
// can a copy inherit one: yolo sets no inheritable ACE on the state dir or under it (the context
// tree's root, the env-file dir and the files in it carry `user:` ACEs with no inherit flag).
//
// `chmod -R -N` IS NOT ADDED AS A SECOND GUARD, on purpose: under -R, Apple's chmod clears an ACL
// through chmodx_np, which follows a symlink (chmod/chmod.c asks it to follow for every -R entry,
// and chmod_acl.c's clear branch calls chmodx_np), so run as root it would clear the ACL of
// whatever a captured link names outside this tree, and fail on a dangling link. A Mac checks the
// outcome instead (integration/macosusercapture_test.go: no ACE on the staged tree, and the
// sandbox cannot open a staged file for append).
var stageCaptureScript = strings.Join([]string{
	"set -eu",
	`dst="$1/` + capturesEntriesLeaf + `/$2"`,
	`if [ -d "$dst" ]; then exit 0; fi`,
	`tmp="$1/` + capturesStagingLeaf + `/$2"`,
	rmBin + ` -rf "$tmp"`,
	// One `&&` chain inside an `if`, where `set -e` does not end the script, so a failure of any
	// step reaches the cleanup below it.
	"if " + strings.Join([]string{
		cpBin + ` -R "$3" "$tmp"`,
		// Readable and searchable by every account, writable by no account but root, which
		// owns every byte: the store already froze its files (internal/capture's freezeTree),
		// and the sandbox could change neither a root-owned file's bytes nor its mode.
		chmodBin + ` -R a+rX,go-w "$tmp"`,
		mvBin + ` "$tmp" "$dst"`,
	}, " && ") + "; then exit 0; fi",
	rmBin + ` -rf "$tmp"`,
	"exit 1",
}, "; ")

// pruneCapturesScript removes every staged entry the user's store no longer selects: $1 the
// staged root, every later argument a key to keep. A superseded capture and one reaped from the
// user's store (`yolo prune`) go at the next launch, so internal/prune needs no second root.
// Anything left under staging/ is a copy a killed launch did not finish, and goes too.
var pruneCapturesScript = strings.Join([]string{
	"set -eu",
	"root=$1",
	"shift",
	`for d in "$root"/` + capturesEntriesLeaf + `/*; do ` + strings.Join([]string{
		`if [ ! -e "$d" ] && [ ! -L "$d" ]; then continue; fi`,
		`k=${d##*/}`,
		"keep=no",
		`for want in "$@"; do if [ "$k" = "$want" ]; then keep=yes; fi; done`,
		`if [ "$keep" = no ]; then ` + rmBin + ` -rf "$d"; fi`,
	}, "; ") + "; done",
	rmBin + ` -rf "$root"/` + capturesStagingLeaf + `/*`,
}, "; ")

// stageCaptureArgv is the one command that stages entry e under root.
func stageCaptureArgv(root string, e CaptureEntry) []string {
	return []string{"/bin/sh", "-c", stageCaptureScript, stageCaptureScriptName, root, e.Key, e.Source}
}

// stageableCaptures is the subset of entries a root script may be handed: a key that is one
// plain store key and a source that is an absolute path. Anything else came from a store yolo
// did not write and is dropped rather than spliced into a path root writes under.
func stageableCaptures(entries []CaptureEntry) []CaptureEntry {
	var out []CaptureEntry
	for _, e := range entries {
		if captureKeyOK(e.Key) && filepath.IsAbs(e.Source) {
			out = append(out, e)
		}
	}
	return out
}

// captureKeyOK reports whether k is a store key's shape: lowercase hex (internal/capture's Key).
func captureKeyOK(k string) bool {
	if k == "" {
		return false
	}
	for _, c := range k {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// StageCaptureCommands returns the sudo argv that bring the staged store up to date for this
// launch: make it, drop every staged entry outside entries' and kept's keys, then copy in each of
// entries that is not there yet (stageCaptureScript). Empty when entries is, so a launch that
// stages no capture runs nothing and leaves the store as the last launch that did left it.
//
// They are RunPlan.CaptureStageCommands, never StageCommands, and the launch runs them
// BEST-EFFORT (orchestrator.go's stageCaptures): a miss is the launcher's fallback to its
// download, and making a capture mandatory for the installer class is a change nobody has ruled
// (install-capture.md's Blockers), so no failure here may refuse a launch that would have started
// without the store. The first three are the store's own and the rest one entry each, in that
// order; stageCaptures keys on it.
//
// `kept` are the keys of the user store's OTHER current entries at this backend's platform — a
// program another workspace selects — so alternating between two workspaces' pack sets copies
// nothing twice. A key in neither list is superseded or gone from the user store.
func StageCaptureCommands(entries []CaptureEntry, kept []string, sd string) [][]string {
	entries = stageableCaptures(entries)
	if len(entries) == 0 {
		return nil
	}
	root := StagedCapturesRoot(sd)
	keep := make([]string, 0, len(entries)+len(kept))
	seen := map[string]bool{}
	for _, e := range entries {
		if !seen[e.Key] {
			seen[e.Key] = true
			keep = append(keep, e.Key)
		}
	}
	for _, k := range kept {
		if captureKeyOK(k) && !seen[k] {
			seen[k] = true
			keep = append(keep, k)
		}
	}
	cmds := [][]string{
		{mkdirBin, "-p", filepath.Join(root, capturesEntriesLeaf), filepath.Join(root, capturesStagingLeaf)},
		// Explicit, rather than whatever umask sudo runs under: the sandbox has to search both to
		// reach an entry, and a launcher that cannot reach one downloads instead, saying nothing.
		{chmodBin, "755", root, filepath.Join(root, capturesEntriesLeaf)},
		append([]string{"/bin/sh", "-c", pruneCapturesScript, pruneCapturesScriptName, root}, keep...),
	}
	for _, e := range entries {
		cmds = append(cmds, stageCaptureArgv(root, e))
	}
	return cmds
}

// ---------------------------------------------------------------------------
// Workspace location — must be neutral ground, never inside a home
// ---------------------------------------------------------------------------
// HomeContaining returns the user-home dir that contains `workspace`, or ""
// when the workspace is on neutral ground. A "home" is a direct
// child of /Users other than /Users/Shared, under any spelling of /Users a
// default macOS install resolves to that directory (macOSHomes). Pure and
// path-only, and the same on every OS: a dry-run on Linux prints the macOS plan.
// The bool is false when no home contains the workspace; the home is returned
// in the workspace's own spelling, so a refusal names a path the reader typed.
func HomeContaining(workspace string) (string, bool) {
	return macOSHomes.containing(workspace)
}

// homeLayout is what the neutral-ground check needs to know about the filesystem the homes live
// on. The check stays LEXICAL — a workspace need not exist yet, and the plan is built on Linux
// for a dry-run — so the facts that make two spellings one directory are inputs rather than
// syscalls, which is also what lets a Linux test state each fact on its own.
type homeLayout struct {
	// usersRoot is the directory whose direct children are homes.
	usersRoot string
	// aliases are other paths that ARE usersRoot: the far end of a firmlink, which
	// filepath.EvalSymlinks does not resolve because a firmlink is not a symlink.
	aliases []string
	// foldCase says the volume compares names case-insensitively, so `/USERS` is usersRoot.
	foldCase bool
}

// macOSHomes is the layout a default macOS install has (docs/design/configurable-workspace-root.md
// §5). On a Mac, homespelling_darwin_test.go checks the firmlink against the running machine and
// only logs whether its volume folds case, folding being the assumption below.
//
//   - /Users is a firmlink to /System/Volumes/Data/Users (macOS 10.15 split the boot volume into a
//     read-only system volume and a Data volume, and firmlinks join the two).
//   - APFS is case-insensitive by default. Folding is ASSUMED rather than probed because the
//     error only runs one way: on a case-sensitive volume a folded match can only refuse MORE (a
//     path like `/USERS/x`, which is no home there), never accept a path a byte comparison with
//     usersRoot refused.
//
// The exemption for Shared is NOT folded: see containing.
var macOSHomes = homeLayout{
	usersRoot: "/Users",
	aliases:   []string{"/System/Volumes/Data/Users"},
	foldCase:  true,
}

// sharedHomeName is the one child of the users root that is not a home.
const sharedHomeName = "Shared"

// containing is HomeContaining against layout l. It checks the workspace itself, then each
// ancestor up to the root.
//
// The Shared exemption is matched BYTE FOR BYTE even when l folds case, so this check can only
// ever refuse more than a byte comparison with usersRoot did, never less: `/Users/SHARED` stays a
// refused home. Folding it would accept a spelling the rest of the plan does not treat as the
// shared root (ancestorLiterals keys on "/Users/Shared/"), and on a case-sensitive volume a real
// account named "shared".
func (l homeLayout) containing(workspace string) (string, bool) {
	for _, p := range append([]string{workspace}, pathParents(workspace)...) {
		if l.isUsersRoot(pathParent(p)) && pathName(p) != sharedHomeName {
			return p, true
		}
	}
	return "", false
}

// isUsersRoot reports whether dir is a spelling of l's users root.
func (l homeLayout) isUsersRoot(dir string) bool {
	for _, root := range append([]string{l.usersRoot}, l.aliases...) {
		if dir == root || (l.foldCase && strings.EqualFold(dir, root)) {
			return true
		}
	}
	return false
}

// inHomeWorkspaceRefusal is the launch's refusal for a workspace under `home`, and it names
// the fix that reaches the path: moving the project to neutral ground. It must NEVER send the
// reader to `yolo macos-fix-permissions` for the path it refuses — that command declines
// every path under a home — which is the loop this message exists to end. The fix-permissions
// line it does print names the DESTINATION, because `mv` is the one way into the shared root
// that inherits no sharing (WorkspaceGrantedScript's measurement).
func inHomeWorkspaceRefusal(workspace, home string) string {
	msg := "[bold red]" + workspace + " is inside the home folder " + home + ".[/bold red]\n" +
		"The sandbox user cannot work inside a user's home, so this backend runs only on " +
		"neutral ground,\nand no command can share a home folder with it: " +
		"`yolo macos-fix-permissions` refuses one on purpose.\n\n"
	if workspace == home {
		// Never suggest moving a whole home.
		return msg + "Run `yolo` from a project folder under [bold]" + SharedRootDefault() +
			"[/bold] instead."
	}
	plan := inHomeMovePlan(workspace)
	msg += "Move the project under " + sharedRootForMove() + ", then share it, " +
		"because a move carries no sharing with it:\n"
	if plan.note != "" {
		msg += plan.note + "\n"
	}
	for _, step := range plan.steps {
		msg += "  [bold]" + step + "[/bold]\n"
	}
	return msg + "  [bold]" + plan.share + "[/bold]\n" +
		"[dim]A copy or a fresh clone made there (cp -R, git clone) is shared as it is " +
		"created, so\nit needs no `macos-fix-permissions`.[/dim]"
}

// inHomeWorkspaceFix is the same remedy in plain text, for `yolo check`'s note.
func inHomeWorkspaceFix(workspace, home string) string {
	if workspace == home {
		return "Run `yolo` from a project folder under " + SharedRootDefault() + " instead; " +
			"`yolo macos-fix-permissions` refuses every path in a home."
	}
	plan := inHomeMovePlan(workspace)
	msg := "Move it under " + sharedRootForMove() + ", then share it; a move carries no " +
		"sharing (a copy or a clone made there is shared already):\n"
	if plan.note != "" {
		msg += plan.note + "\n"
	}
	for _, step := range plan.steps {
		msg += "  " + step + "\n"
	}
	return msg + "  " + plan.share
}

// sharedRootForMove is where the in-home remedy moves a project: SharedRootDefault. A var only
// so a test can point the destination check (inHomeMovePlan) at a tree it built and run the
// printed commands there.
var sharedRootForMove = SharedRootDefault

// inHomeMove is the remedy both spellings of the in-home refusal print: the shell commands that
// move the project to neutral ground, in order; the share of where it LANDS; and, when the
// obvious destination is taken, the sentence that says why the commands are not the plain move.
type inHomeMove struct {
	steps []string
	share string
	note  string
}

// inHomeMovePlan decides the move by looking at the destination first, because a plain
// `mv <project> <root>/<name>` is right only when nothing is there.
//
//   - A SYMBOLIC LINK TO THE PROJECT is what the natural workaround for the neutral-ground rule
//     leaves (`ln -s ~/code/proj /Users/Shared/yolo/proj`, then launching from the link): the
//     launch resolves the workspace to the home and refuses it, and mv follows a link to a
//     directory, so it tried to move the project into itself and failed. The link is removed
//     first — `rm` on a link removes the link, never what it points at — and the project moved
//     to where it was.
//   - ANYTHING ELSE THERE (a real directory, a file, a link to something else) would take the
//     project INSIDE it, or fail, and the share would then name the wrong level. The move takes
//     the first free `<name>-2`, `<name>-3`, … instead, and the note says so.
//
// The share always names where the project lands, the property the in-home remedy exists for:
// a remedy that cannot reach the path it names is the loop this refusal replaced.
func inHomeMovePlan(workspace string) inHomeMove {
	root := sharedRootForMove()
	name := filepath.Base(workspace)
	dest := filepath.Join(root, name)
	plan := func(to, note string, first ...string) inHomeMove {
		return inHomeMove{
			steps: append(first, "mv "+shquote.Quote(workspace)+" "+shquote.Quote(to)),
			share: "yolo macos-fix-permissions " + shquote.Quote(to),
			note:  note,
		}
	}
	fi, err := os.Lstat(dest)
	switch {
	case os.IsNotExist(err):
		return plan(dest, "")
	case err != nil:
		return plan(dest, "(Could not check whether "+dest+" already exists: "+err.Error()+
			". If it does, move the project to a name that is free there instead.)")
	case fi.Mode()&os.ModeSymlink != 0 && linksToWorkspace(dest, workspace):
		return plan(dest, dest+" is a symbolic link to this project, and mv would follow it and "+
			"try to move the project into itself.\nRemove the link first (rm removes the link, "+
			"never the project):", "rm "+shquote.Quote(dest))
	}
	free := freeSharedName(root, name)
	return plan(free, dest+" already exists, and mv would put the project inside it, so this "+
		"moves it to "+free+" instead\n(or pick another free name there):")
}

// linksToWorkspace reports whether the link at dest resolves to the workspace, compared resolved
// on both sides, as the launch resolves the workspace it judges.
func linksToWorkspace(dest, workspace string) bool {
	got, err := filepath.EvalSymlinks(dest)
	if err != nil {
		return false
	}
	if ws, err := filepath.EvalSymlinks(workspace); err == nil {
		workspace = ws
	}
	return got == workspace
}

// freeSharedName returns the first of <root>/<name>-2, -3, … that does not exist, or a
// placeholder the reader has to replace when a hundred are taken.
func freeSharedName(root, name string) string {
	for n := 2; n <= 100; n++ {
		cand := filepath.Join(root, name+"-"+strconv.Itoa(n))
		if _, err := os.Lstat(cand); os.IsNotExist(err) {
			return cand
		}
	}
	return filepath.Join(root, "NEW-NAME")
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
// ITS HEAD IS entrypoint.BootPath's HEAD, TAKEN FROM IT RATHER THAN COPIED (HomePathDirs):
// blockers, launchers, the npm prefix, the mise shims, $GOPATH/bin, ~/.local/bin — the
// container's order entry for entry (program-delivery.md, OQ-PD27). This was a third,
// hand-written copy, and it had drifted: it put ~/.local/bin THIRD, so a tool a vendor
// installer or pipx left there outranked the mise shim of the same name on this backend
// alone, while BootPath ranks the shim first. B2 (program-delivery.md §3.5, OQ-PD12a) is why
// ~/.yolo/bin/launch is second, ahead of the install prefixes: a launcher ordered after what
// it installs is unreachable from its own second invocation onward, so the update arm it
// carries never runs. ~/.yolo/bin/block stays first — interception must win over installation.
//
// THE TAIL IS THIS PLATFORM'S OWN, deliberately: the darwin store prefix where the container
// has its store farm, the staged yolo's directory where the container has /bin/<name> links,
// and macOS's system dirs in the order its own /etc/paths lists them (/usr/bin before /bin,
// then the two sbin dirs). Only the head is a cross-backend rule.
//
// macos-user is the backend where this matters most and hides least: it bakes no image, so
// the only thing a launcher could shadow here is a `packages:` store entry or a system
// binary, and the generation-time collision check (entrypoint's launchercollision.go)
// reads exactly this string, minus the per-home prefixes, to decide.
//
// # THE STAGED yolo IS ON IT, and that is what makes `yolo` a resolvable NAME in here
//
// docs/design/declaration-parity.md DP-B8 / DP-L4. Every generated launcher carries two
// functions that open `command -v yolo || return` — `_refresh_servers` (the MCP/LSP server
// refresh) and `_try_materialize` (program-delivery.md §6.3's "use an already-captured
// install instead of downloading it") — and on this backend both returned at that line on
// every invocation, because the only yolo the sandbox can reach is the root-owned copy
// StageBinaryCommands stages at StagedYoloPath, which was on no PATH at all. Armed,
// generated, baked with SERVERS_ENABLED=1, and unreachable: neither failed, both no-oped.
//
// PATH RATHER THAN TEACHING THE TWO FUNCTIONS AN ABSOLUTE PATH, which was the other half
// of DP-L4's remedy. The functions are generated by internal/entrypoint for BOTH backends
// off one template; giving them a macOS-only literal would put a second, backend-specific
// answer to "where is yolo?" into a generator whose whole value is having one. PATH is
// also the wider fix — every other consumer in the sandbox (a `requires` probe, a hook, an
// agent shelling out) gets the same resolution — and it costs no new trust: the binary is
// root-owned and `a+rX` precisely so the sandbox uid may exec and not rewrite it.
//
// WHERE IT SITS mirrors the container, where `yolo` is a `/bin/<name>` symlink into the
// mounted prefix and `/bin` is the LAST entry (AGENTS.md, PATH order): after the store
// prefix `packages:` materializes into, ahead of the system dirs. So nothing already on
// this PATH moves relative to anything else, and a `packages:` entry still outranks it.
func SandboxPath(home string, prefix []string) string {
	if home == "" {
		home = SandboxHome()
	}
	// The Env the bootstrap generates against resolves these the same way: its MISE_DATA_DIR
	// is SandboxMiseData (buildBootstrapEnv), and the npm prefix and GOPATH are the home's
	// defaults on both sides.
	parts := entrypoint.HomePathDirs(entrypoint.NewEnv(map[string]string{
		"JAIL_HOME":     home,
		"MISE_DATA_DIR": SandboxMiseData(home),
	}))
	parts = append(parts, prefix...)
	// Derived from StagedYoloPath rather than spelled, so the directory holding the
	// staged binary and the directory on PATH cannot become two different answers.
	parts = append(parts, filepath.Dir(StagedYoloPath("")))
	parts = append(parts, "/usr/bin", "/bin", "/usr/sbin", "/sbin")
	return strings.Join(parts, ":")
}

// LaunchArgv builds the `sudo -u … env -i … sandbox-exec -f … -- <agent>` argv.
//
// `envFile` is the session env file (SandboxEnvFile) carrying everything the launch
// composed — git identity, TERM, the profile/provider channel, the hydrated env_sources.
// IT IS A PATH, NOT THE VALUES: this builder never receives the composed environment, so
// it cannot put a credential on a command line even by accident (envfile.go states why).
// "" means nothing was composed and the argv is unwrapped.
//
// What is left on the argv is the identity quartet, which is not a secret and which the
// sandbox must have before the file is read (sandboxEnvPairs, and the workspace-centric
// `cd … && exec …` inner shell).
func LaunchArgv(agentArgv []string, profilePath, envFile string, workspace, user, home string, pathPrefix []string) []string {
	return LaunchArgvWithGuard(agentArgv, profilePath, envFile, workspace, user, home, pathPrefix, SessionGuard{}, "")
}

// LaunchArgvWithGuard is LaunchArgv with the session guard (sessionguard.go) placed between
// the env-file reader and the inner shell when `guard` is Enabled:
//
//	… sandbox-exec -f <profile> -- /bin/sh -c <reader> yolo-sandbox-env <env file> \
//	    <staged yolo> internal session-guard --memory <bytes> -- /bin/zsh -c 'cd … && exec …'
//
// THERE, AND NOWHERE ELSE. After the reader, so the guard runs with the session's composed
// environment and inside the sandbox, as the account whose processes it reads; before the
// shell, so everything the agent starts is its descendant. stagedYolo is the root-owned copy
// the sandbox already execs for its bootstrap (StagedYoloPath), never the host's own binary,
// which the sandbox account cannot read.
//
// A guard that is not Enabled adds no word, so a launch that declares no resources.memory
// gets the argv byte for byte as before it existed.
func LaunchArgvWithGuard(agentArgv []string, profilePath, envFile string, workspace, user, home string, pathPrefix []string, guard SessionGuard, stagedYolo string) []string {
	if user == "" {
		user = SandboxUser
	}
	if home == "" {
		home = SandboxHome()
	}
	envPairs := sandboxEnvPairs(home, user, SandboxPath(home, pathPrefix), envFile)
	// Run the agent from the workspace: a zsh cd's in, then execs the agent so it inherits
	// the TTY and PID.
	quotedAgent := make([]string, len(agentArgv))
	for i, a := range agentArgv {
		quotedAgent[i] = shQuote(a)
	}
	inner := "cd " + shQuote(workspace) + " && exec " + strings.Join(quotedAgent, " ")
	// ⚠ NO `--login`, FOR THE SAME CORRECTNESS REASON THE STAGE ARGV HAS NEVER HAD IT
	// (provision.go). `sudo -i` does not execve its argv: per sudo(8) it concatenates the
	// command and args, backslash-escaping every character EXCEPT alphanumerics,
	// underscores, hyphens and DOLLAR SIGNS, and hands the string to a login shell. So a
	// newline arrived as a `\`-continuation and was removed, and every `$var` was expanded
	// by that intermediate shell against an empty environment. Neither is an error: the
	// wrong command runs, exits 0, and prints plausible output.
	//
	// The flag was kept here until 2026-09-12 on the belief that the login rc files it runs
	// are what re-prepend PATH after macOS path_helper (OQ-1, the acceptance bar). They are
	// not: the very next word is `/usr/bin/env -i`, which wipes the environment that login
	// shell just built. The agent's PATH is the explicit `PATH=` below, and the re-prepend
	// that OQ-1 measures happens in the user's OWN login shell downstream — `yolo -- bash
	// -lc …` reads /etc/profile (path_helper) and then WriteLoginRC's rc file, inside the
	// sandbox. Removing the flag leaves both untouched and lets sudo execve the argv.
	//
	// MEASURED BOTH WAYS ON HARDWARE 2026-09-12 (macOS 26.5, arm64): with the flag, a
	// nine-binary floor probe printed nine BLANK lines because `$b` was eaten — item 6 could
	// not be measured at all until the probe was rewritten without variables. Without it,
	// the same probe resolves all nine into the store profile and item 3's `fzf` still beats
	// Homebrew's. PlanInvariants pins the absence on both argvs.
	out := []string{
		"sudo",
		"--set-home",
		"--user=" + user,
		"/usr/bin/env",
		"-i",
	}
	out = append(out, envPairs...)
	out = append(out, "/usr/bin/sandbox-exec", "-f", profilePath, "--")
	session := append(guard.Argv(stagedYolo), "/bin/zsh", "-c", inner)
	out = append(out, ExecWithEnvFile(envFile, session)...)
	return out
}

// sandboxEnvPairs renders the `env -i` K=V list for a process run AS the sandbox user, and
// it is now a CLOSED LIST: the HOME/USER/SHELL/PATH quartet this backend owns, the mise
// store and login PATH that travel with it, and one word naming the session env file.
//
// EVERYTHING A CALLER COMPOSED CROSSES IN THE FILE INSTEAD (envfile.go). This function used
// to append the caller's whole env, which is how `env_sources` credentials and the profile
// channel's provider tokens came to ride three command lines in cleartext. The composed map
// is no longer a parameter, so the leak cannot come back by someone re-adding a loop.
//
// The quartet stays PROTECTED on both crossings — dropped from the file by
// SandboxEnvFileContent and absent from this list by construction — because these are what
// make the process the sandbox user's rather than a copy of whatever the invoking shell had.
//
// Shared by the agent launch (LaunchArgv), the provisioning stage (ProvisionArgv) and the
// install-capture driver (CaptureDriverArgv), which differ in what they exec and in nothing
// about the environment they exec it in; two spellings of that would be two ways for a
// capture to stop resembling a launch.
func sandboxEnvPairs(home, user, pathValue, envFile string) []string {
	envPairs := []string{
		"HOME=" + home,
		"USER=" + user,
		"SHELL=/bin/zsh",
		"PATH=" + pathValue,
		// MISE_DATA_DIR is emitted WITH the PATH because they are one fact: the shims dir on
		// that PATH is <this>/shims (SandboxPath), so a process that resolved the shims from
		// one value and the store from another would run a shim whose install is elsewhere.
		// Protected for the same reason the quartet is — the tier a store belongs to is this
		// backend's to decide, not a caller's, and the default it overrides lands in the
		// per-workspace tier (SandboxMiseData).
		"MISE_DATA_DIR=" + SandboxMiseData(home),
		// The same PATH again, under its own name, because a LOGIN shell does not keep the
		// one above: macOS path_helper reorders PATH in /etc/zprofile, and the rc files
		// entrypoint.WriteLoginRC generates re-prepend from this variable rather than from a
		// literal — those files sit at the root of a home every workspace shares, so a baked
		// value is one workspace's store dirs in another's login shell.
		entrypoint.DarwinLoginPathEnv + "=" + pathValue,
	}
	// The file's PATH, so a human reading `ps` or a dry run can find the environment the
	// command line no longer shows. The reader that consumes it is ExecWithEnvFile's `sh -c`
	// wrapper, which is handed the same path as an argument.
	if envFile != "" {
		envPairs = append(envPairs, SandboxEnvFileEnv+"="+envFile)
	}
	return envPairs
}

// ---------------------------------------------------------------------------
// Loopholes on the native backend
// ---------------------------------------------------------------------------
// macosLogModes is the `macos_log` vocabulary as a lookup — off (a stub naming the
// remedy), user (scoped), full (passthrough) — DERIVED from config.MacosLogModes rather
// than restated here.
//
// The two lists have to be one list. config.validateMacosLog is what the pre-flight
// judges a user's config against, and MacosLogWrapperScript below silently rewrites
// anything outside this map to "off": a mode accepted there and missing here is a dial
// the user set, yolo accepted, and the jail then ignored with no message on any surface.
// This var used to hold its own literal, which is that drift waiting to happen — and the
// key spent its whole life so far unreachable for the mirror-image reason (F1: the
// generator knew three modes and the schema knew none).
var macosLogModes = func() map[string]struct{} {
	m := make(map[string]struct{}, len(config.MacosLogModes))
	for _, mode := range config.MacosLogModes {
		m[mode] = struct{}{}
	}
	return m
}()

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
//   - read on the FILE (sandboxFileReadAce, shared with the session env file — READ, never
//     write: a Unix socket needed write to connect(2), a file needs only read, and the
//     sandbox has no reason to rewrite its own endpoint, which already holds its own token —
//     loophole-transport.md OQ-T5).
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
// argv and is unit-tested on the emitted strings; only a Mac can run it.
//
// WIRED 2026-09-17. It was written ahead of its caller ("no call site yet: macos-user
// does not start host services at all today"); that arm now starts every admitted
// loophole, and runplan.go derives a grant for EVERY `YOLO_SERVICE_*_ENDPOINT` in the
// rendered env rather than the one hardcoded broker name. PlanInvariants refuses a plan
// carrying an endpoint with no matching grant, so the two cannot drift apart silently.
func EndpointGrantCommands(endpointPath, user string) [][]string {
	if user == "" {
		user = SandboxUser
	}
	return [][]string{
		sandboxFileReadAce(endpointPath, user),
		{chmodBin, "+a", "user:" + user + " allow search", pathParent(endpointPath)},
	}
}

// MacosLogWrapperScript returns a yolo-log helper wrapping Apple's `log`.
//
// ⚠ THE SWITCH'S DEFAULT IS "user", NOT "off", so a mode added to the vocabulary without a
// case of its own is served the SCOPED helper rather than the stub — it hands out more
// access than the new word asked for, quietly. The unrecognised-mode rewrite above is what
// keeps that unreachable for anything config.MacosLogModes does not list.
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
// SessionProfilePath returns the root-owned per-session Seatbelt profile path:
// <stateDir>/profile-<key>.sb. A launch passes its session key (SessionKey: <cname>.<session id>,
// sessionfiles.go), so a second terminal in the workspace cannot rewrite the profile a running
// sandbox was started under, and removes the one it wrote when it ends; an install capture passes
// its own staging tree's cname.
func SessionProfilePath(key, sd string) string {
	if sd == "" {
		sd = stateDir
	}
	return filepath.Join(sd, "profile-"+key+".sb")
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
