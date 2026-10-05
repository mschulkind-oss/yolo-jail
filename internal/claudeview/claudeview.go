// Package claudeview holds the pieces of the Claude CREDENTIAL VIEW that more than one side of the
// jail boundary needs: the switch that selects it for a launch, where a workspace's view lives on
// the host, what a view carries, and the confined read and write every host-side writer goes
// through.
//
// A credential view (docs/design/claude-login-without-interception.md §2) is a per-workspace
// `.credentials.json` the host broker writes for Claude: the current access token and its real
// expiry, and NO refresh token. Claude refreshes only when its stored credential holds a refresh
// token (F4 there), so a jail reading a view never contacts the token endpoint, and the host
// broker is the one refresher of the machine's login. The broker's half (the canonical login,
// the refresh, the registrations and the enrollment of a jail's /login) is internal/oauthbroker;
// this package is the leaf the launcher, the loophole argv and the in-jail entrypoint can all
// import without importing the broker.
//
// # THE SWITCH IS TEMPORARY, AND IT IS THE ONE SECOND PATH THIS CONCERN IS ALLOWED
//
// OQ-CL1 ruled that the view REPLACES the interception (the /etc/hosts entry, the CA, the
// terminator) at every notch, deleted rather than switched, in an order: build the view, pass
// §7's measures on a real Claude, run it a day on a real rootless host, then delete the
// interception in the same release. Until the measures pass the interception is the only PROVEN
// path, so the view ships behind SwitchEnv. The switch, and every reader of it, is deleted with
// the interception (CL-D7, CL-D10); nothing here is meant to outlive that release.
package claudeview

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// SwitchEnv is the dial that selects the credential view for a launch
// (CL-D10, docs/design/claude-login-without-interception.md).
//
//	podman, macos-user, container (Apple): off unless the host environment sets it to 1
//	host (`yolo host -- claude`):          off unless the invoking shell sets it to 1 (CL-D27)
//
// The launcher resolves it once and hands the RESOLVED value to the jail (1 or 0), so the
// in-jail entrypoint never re-derives a default for a runtime it cannot see. At the host there is
// no jail to hand it to: `yolo host` resolves it for runtime "host" and, when it is on, points the
// launched Claude's store at a directory yolo manages (HostLocation) instead of `~/.claude`, which
// is the user's own login and stays untouched (OQ-NC7).
const SwitchEnv = "YOLO_CLAUDE_CREDENTIAL_VIEW"

// HostRuntime is the runtime name the host notch resolves the switch for and registers its view
// under, so `yolo claude-auth status` names the launch that registered it.
const HostRuntime = "host"

// DefaultOn reports the switch's default for a runtime (CL-D11): OFF ON EVERY BACKEND, so `=1`
// is the opt-in everywhere until the measures pass. OQ-CL1's order is measure first.
//
//   - podman: the interception is the proven path there.
//   - Apple Container: the interception never ran there, and Claude holds its own refresh token
//     and refreshes itself, racing but working. With the view on and M1 unmeasured, a Claude
//     that did not adopt the rewritten file would lose its login when its eight-hour access
//     token expired, which is worse than the race.
//   - macos-user: Claude on macOS keeps its login in the Keychain first and the file only as a
//     fallback, and which one a sandbox account's Claude reads is unmeasured; the sandbox's
//     machine-tier shared file is in the sandbox account's home, not the host user's store the
//     broker migrates from; and this backend's launch path has never run on hardware (M11).
//   - host: Claude keeps the user's own login by OQ-NC7's ruling, and the view at the host is an
//     opt-in like everywhere else (CL-D27); whether Claude on macOS reads the file or a Keychain
//     entry under the moved store is unmeasured.
//
// Kept as a function of the runtime, though it answers false for all of them, because turning
// one backend on after its measures pass is a change to this one line.
func DefaultOn(runtime string) bool {
	return false
}

// Selected resolves SwitchEnv for one launch on runtime. A value is read as on for 1/true/yes/on
// and off for 0/false/no/off, case-insensitively; anything else, and absence, takes the runtime's
// default.
func Selected(runtime string, getenv func(string) string) bool {
	switch strings.ToLower(strings.TrimSpace(getenv(SwitchEnv))) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	}
	return DefaultOn(runtime)
}

// ResolvedValue is the value the launcher hands the jail for a decision: "1" or "0".
func ResolvedValue(on bool) string {
	if on {
		return "1"
	}
	return "0"
}

// ViewRel is the view's path relative to the agent's home: where Claude already looks
// (`~/.claude/.credentials.json`, CL-D8), so no Claude environment variable changes.
const ViewRel = ".claude/.credentials.json"

// ViewFile is the view's leaf name inside the `.claude` directory.
const ViewFile = ".credentials.json"

// LegacyLinkTarget is the relative symlink the claude pack's `shared_credentials` hook writes at
// ViewRel (entrypoint.linkIntoSharedDir: filepath.Rel from the link's directory to the shared
// file). A launch that selects the view removes exactly this link, which yolo itself made, and
// no other (RemoveLegacyLink).
var LegacyLinkTarget = filepath.Join("..", ".claude-shared-credentials", ViewFile)

// SecureStorageEnv is the variable that moves Claude's credential store: Claude opens
// `CLAUDE_SECURESTORAGE_CONFIG_DIR ?? CLAUDE_CONFIG_DIR ?? ~/.claude` + `.credentials.json`, and
// keeps its refresh lock (`.oauth_refresh.lock`) and its write lock (`.storage-write.lock`) in
// that same directory. MEASURED in the 2.1.284 binary (search `CLAUDE_SECURESTORAGE_CONFIG_DIR;`):
// an empty value falls back to `~/.claude`, and `~` is not expanded, so the value is an absolute
// path. On macOS the Keychain service name gains a suffix hashed from the same directory once
// the variable is set (search `Claude Code${`), so a login Claude kept in a Keychain rather than
// in this file would not be found under the new name.
//
// THE BRIDGE (CL-D22, docs/design/claude-login-without-interception.md). Until the view replaces
// the interception, a launch that links Claude's credential into the machine-scope shared
// directory also sets this variable to that directory, so Claude reads and writes the real file
// there and no symlink sits in its credential path. Without it, Claude's first save after a
// refresh (a temp file renamed over the path) replaced the link with a private file, the next
// boot discarded that file in favor of the shared one, and the shared one held the refresh token
// the save had already spent: a login on every launch after the first refresh, on the two
// backends no broker serves. A VIEW launch never sets it, since the view is a file at ViewRel
// that Claude must read instead. Deleted with the view's switch and the shared directory (CL-D7).
const SecureStorageEnv = "CLAUDE_SECURESTORAGE_CONFIG_DIR"

// HostSubdir is the name of the workspace overlay directory, under <workspace>/.yolo/home, that
// holds a runtime's `~/.claude`: podman binds each overlay entry with its leading dot stripped,
// macos-user links the account home's `~/.claude` to the same stripped name, and Apple Container
// binds the whole overlay at the home, so there the entry keeps its dot
// (docs/reference/jail-home.md; AGENTS.md, "Agent logs").
func HostSubdir(runtime string) string {
	if runtime == "container" {
		return ".claude"
	}
	return "claude"
}

// viewFields is what a view carries of the canonical credential, in this order (CL-D1). An
// allowlist rather than "everything but refreshToken", so a field the vendor adds later (a
// second secret, say) does not reach a jail until someone decides it should.
var viewFields = []string{"accessToken", "expiresAt", "scopes", "subscriptionType", "rateLimitTier"}

// Project returns the view of a canonical claudeAiOauth object: viewFields copied when present,
// and never a refreshToken. A nil or empty input, or one with no accessToken, projects to nil:
// there is nothing a view could carry.
func Project(oauth *jsonx.OrderedMap) *jsonx.OrderedMap {
	if oauth == nil {
		return nil
	}
	if at, _ := oauth.Get("accessToken"); at == nil || at == "" {
		return nil
	}
	out := jsonx.NewOrderedMap()
	for _, k := range viewFields {
		if v, ok := oauth.Get(k); ok {
			out.Set(k, v)
		}
	}
	return out
}

// ErrViewIsLink refuses a view path, or the directory above it, that is a symbolic link. The
// directory is jail-writable, so a link there could aim a host write at any host file.
var ErrViewIsLink = errors.New("it is a symbolic link, and the jail can write the directory " +
	"it is in, so the broker does not follow it")

// maxViewBytes bounds a view read. The file is jail-written, and the broker parses it on the host.
const maxViewBytes = 1 << 20

// Location is one view on the host. A jail's is one workspace's:
// <Workspace>/.yolo/home/<Subdir>/.credentials.json. The host notch's is a directory yolo
// manages, Dir, absolute and outside every workspace: <Dir>/.credentials.json (HostLocation).
// Exactly one of the two shapes is set; a registration written before Dir existed has Workspace
// and Subdir alone, and reads exactly as it did.
type Location struct {
	Workspace string `json:"workspace,omitempty"`
	Subdir    string `json:"subdir,omitempty"`
	// Dir is the absolute directory of a host-notch view, which Claude reads through
	// CLAUDE_SECURESTORAGE_CONFIG_DIR. Empty for a workspace's view.
	Dir string `json:"dir,omitempty"`
}

// HostLocation is the view `yolo host -- <program>` registers for the pack that delivers it:
// paths.HostAgentStoreDir(pack), a directory under yolo's own state dir that no jail mounts.
func HostLocation(pack string) Location {
	return Location{Dir: paths.HostAgentStoreDir(pack)}
}

// IsHost reports whether l is a host-notch view (Dir set) rather than a workspace's.
func (l Location) IsHost() bool { return l.Dir != "" }

// Valid reports whether l has one of its two shapes: a Dir that is absolute, or a Workspace and
// a Subdir. A registration file that is neither is skipped by the broker.
func (l Location) Valid() bool {
	if l.Dir != "" {
		return filepath.IsAbs(l.Dir) && l.Workspace == "" && l.Subdir == ""
	}
	return l.Workspace != "" && l.Subdir != ""
}

// Path is the view's host path, for messages only. Nothing opens by it.
func (l Location) Path() string {
	if l.IsHost() {
		return filepath.Join(l.Dir, ViewFile)
	}
	return filepath.Join(paths.WorkspaceHomeState(l.Workspace), l.Subdir, ViewFile)
}

// openDir opens the view's directory beneath <workspace>/.yolo/home, refusing a symbolic link at
// `.yolo`, at `home` and at the subdir: every one of the three is jail-writable (the workspace is
// bound whole on both container backends, and macos-user's sandbox writes the workspace). It
// creates nothing; a missing directory is ErrDirGone, which is how a registration for a
// workspace that has gone is recognized.
//
// A host view's directory is opened refusing a link at Dir itself (paths.OpenStateDirRoot), and
// the same ErrDirGone when it is missing. Nothing but yolo writes there, but the refusal costs
// nothing and keeps one rule for every view.
func (l Location) openDir() (*os.Root, error) {
	if l.IsHost() {
		if !l.Valid() {
			return nil, &fs.PathError{Op: "open", Path: l.Dir, Err: fs.ErrInvalid}
		}
		r, err := paths.OpenStateDirRoot(l.Dir)
		if err != nil {
			return nil, l.dirError(err)
		}
		return r, nil
	}
	if l.Subdir == "" || filepath.Base(l.Subdir) != l.Subdir || l.Subdir == "." || l.Subdir == ".." {
		return nil, &fs.PathError{Op: "open", Path: l.Subdir, Err: fs.ErrInvalid}
	}
	home, err := paths.OpenWorkspaceStateSubdir(l.Workspace, "home")
	if err != nil {
		return nil, l.dirError(err)
	}
	defer home.Close()
	r, err := paths.OpenStateSubdirRoot(home, l.Subdir, filepath.Join(paths.WorkspaceHomeState(l.Workspace), l.Subdir))
	if err != nil {
		return nil, l.dirError(err)
	}
	return r, nil
}

func (l Location) dirError(err error) error {
	var linked *paths.LinkedStateDirError
	if errors.As(err, &linked) {
		return &fs.PathError{Op: "open", Path: linked.Path, Err: ErrViewIsLink}
	}
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("%w: %s", ErrDirGone, filepath.Dir(l.Path()))
	}
	return err
}

// ErrDirGone is a view whose directory, or the workspace overlay above it, does not exist. The
// broker reads it as "that workspace has gone" and drops the registration; it never creates
// the directory back (only EnsureDir, at a launch, creates it).
var ErrDirGone = errors.New("the view's directory does not exist")

// EnsureDir creates the view's directory, and the workspace overlay above it, when missing: the
// launch's half, before it registers the view, since on macos-user the directory is otherwise
// made only later by the sandbox's bootstrap. <workspace>/.yolo goes through
// paths.EnsureWorkspaceStateDir, the one chokepoint that creates it; nothing below it is
// created through a link.
//
// A host view's Dir is created 0700, with its parent under the state dir, and is then opened
// refusing a link, so a link standing where the directory should be fails the launch's
// registration rather than being written through.
func (l Location) EnsureDir() error {
	if l.IsHost() {
		if !l.Valid() {
			return &fs.PathError{Op: "mkdir", Path: l.Dir, Err: fs.ErrInvalid}
		}
		if err := os.MkdirAll(filepath.Dir(l.Dir), 0o700); err != nil {
			return err
		}
		if err := os.Mkdir(l.Dir, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
			return err
		}
		r, err := paths.OpenStateDirRoot(l.Dir)
		if err != nil {
			return l.dirError(err)
		}
		return r.Close()
	}
	if _, err := paths.EnsureWorkspaceStateDir(l.Workspace); err != nil {
		return err
	}
	state, err := paths.OpenStateDirRoot(paths.WorkspaceStateDir(l.Workspace))
	if err != nil {
		return l.dirError(err)
	}
	defer state.Close()
	if err := state.Mkdir("home", 0o755); err != nil && !errors.Is(err, fs.ErrExist) {
		return err
	}
	home, err := paths.OpenStateSubdirRoot(state, "home", paths.WorkspaceHomeState(l.Workspace))
	if err != nil {
		return l.dirError(err)
	}
	defer home.Close()
	if err := home.Mkdir(l.Subdir, 0o755); err != nil && !errors.Is(err, fs.ErrExist) {
		return err
	}
	return nil
}

// Read returns the view's bytes. A symbolic link at the view, or above it, is refused with
// ErrViewIsLink and never followed; a missing view is an fs.ErrNotExist.
func (l Location) Read() ([]byte, error) {
	r, err := l.openDir()
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return l.readIn(r)
}

func (l Location) readIn(r *os.Root) ([]byte, error) {
	if fi, err := r.Lstat(ViewFile); err == nil && fi.Mode()&fs.ModeSymlink != 0 {
		return nil, &fs.PathError{Op: "read", Path: l.Path(), Err: ErrViewIsLink}
	}
	f, err := paths.OpenRegularFileBeneath(r, ViewFile)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxViewBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxViewBytes {
		return nil, fmt.Errorf("%s: larger than %d bytes, not read", l.Path(), maxViewBytes)
	}
	return data, nil
}

// StorageLockDir is Claude's own credential write lock, a directory beside the file. MEASURED
// in the 2.1.284 binary (search `.storage-write`): every read-modify-write of the credential
// store runs under proper-lockfile's lock(join(configDir, ".storage-write"), {realpath: false,
// retries: {retries: 10, minTimeout: 100, maxTimeout: 1000}, stale: 15000}), and proper-lockfile
// takes a lock by mkdir of `${file}.lock` (its `lockfilePath || \`${e}.lock\“), treats one whose
// modification time is older than `stale` as abandoned, and releases by removing it.
//
// The broker takes the same lock around its own read-modify-write of a view (CL-D13), because
// the file also holds Claude's MCP servers' OAuth (`mcpOAuth`, claude-code#45551): without it a
// Claude saving an MCP token from a stale read could write back the access token the broker
// just replaced, which a refresh has already revoked upstream (CL-D12).
const StorageLockDir = ".storage-write.lock"

// storageLockStale is proper-lockfile's `stale` for that lock, 15 s (MEASURED, above).
const storageLockStale = 15 * time.Second

// StorageLockWait bounds how long a view write waits for Claude's lock before writing without
// it. Claude holds it for the length of one file write, and a view left stale is an outage
// once the refresh has revoked the old token, so the wait is short and the write goes ahead.
var StorageLockWait = 2 * time.Second

// onStaleLockBroken, when set, runs each time acquireStorageLock has removed a stale lock and
// before it tries again: a test's window onto a jail planting the lock back in that gap.
var onStaleLockBroken func()

// acquireStorageLock takes Claude's storage lock beneath r, waiting up to StorageLockWait and
// breaking a lock older than proper-lockfile's own stale limit, as proper-lockfile would. It
// returns the release, and whether the lock was held: false means the write goes ahead
// without it (the wait ran out, or something other than a directory sits at the name, which is
// left alone).
//
// EVERY WAY ROUND THE LOOP ENDS AT THE DEADLINE, because the directory is jail-writable and the
// broker calls this holding refresh.lock (every view write, and since CL-D25 the shared file's
// on every podman refresh, store.go's writeLegacy): a wait with no bound is every jail on the
// machine unable to refresh. So a stale lock that cannot be removed (the jail put something
// inside it, and rmdir fails) is waited on like a held one, and one the jail plants again each
// time it is broken is retried only until the deadline. Nothing inside a lock is ever removed.
func acquireStorageLock(r *os.Root) (func(), bool) {
	deadline := time.Now().Add(StorageLockWait)
	for {
		err := r.Mkdir(StorageLockDir, 0o755)
		if err == nil {
			return func() { _ = r.Remove(StorageLockDir) }, true
		}
		if !errors.Is(err, fs.ErrExist) {
			return func() {}, false
		}
		fi, lerr := r.Lstat(StorageLockDir)
		if lerr == nil && !fi.IsDir() {
			return func() {}, false
		}
		if time.Now().After(deadline) {
			return func() {}, false
		}
		if lerr == nil && time.Since(fi.ModTime()) > storageLockStale && r.Remove(StorageLockDir) == nil {
			if onStaleLockBroken != nil {
				onStaleLockBroken()
			}
			continue
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// Update read-modify-writes the view under Claude's storage lock (acquireStorageLock). mutate
// gets the current bytes, nil when there is no file, and returns what to write; nil, or the
// same bytes, writes nothing. The write is confined: the directory is opened refusing a link
// (openDir), a symbolic link at the view is refused with ErrViewIsLink rather than replaced or
// followed, and the bytes go to an O_EXCL temp file in the same directory that is then renamed
// over the view, so a reader sees the old file or the new one and never a torn one (the
// credential-file precedent, oauthbroker.WriteTokens). The file is 0600.
//
// It reports whether it wrote, and whether Claude's lock was held for it.
func (l Location) Update(mutate func(current []byte) ([]byte, error)) (wrote, locked bool, err error) {
	r, err := l.openDir()
	if err != nil {
		return false, false, err
	}
	defer r.Close()
	return updateBeneath(r, ViewFile, l.readIn, mutate)
}

// UpdateSharedFile is Update for the machine store's shared credentials file, name in dir, which
// CL-D22's bridge makes Claude's own store file: Claude reads it and writes it there, under the
// same `.storage-write.lock` beside it, so a host writer takes that lock too and replaces only
// what it owns (the broker, CL-D13's reasons applied to this file). Two differences from a view,
// both the whole-file write's behavior kept: a symbolic link at name is neither read nor
// followed but REPLACED by the rename (the jail can plant one, and the rename never writes
// through it), and nothing else in dir is touched. dir itself is opened refusing a link
// (paths.OpenStateDirRoot); a missing dir is an fs.ErrNotExist.
func UpdateSharedFile(dir, name string, mutate func(current []byte) ([]byte, error)) (wrote, locked bool, err error) {
	if name == "" || filepath.Base(name) != name || name == "." || name == ".." {
		return false, false, &fs.PathError{Op: "open", Path: name, Err: fs.ErrInvalid}
	}
	r, err := paths.OpenStateDirRoot(dir)
	if err != nil {
		return false, false, err
	}
	defer r.Close()
	read := func(r *os.Root) ([]byte, error) {
		if fi, err := r.Lstat(name); err == nil && !fi.Mode().IsRegular() {
			return nil, fs.ErrNotExist
		}
		f, err := paths.OpenRegularFileBeneath(r, name)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		data, err := io.ReadAll(io.LimitReader(f, maxViewBytes+1))
		if err != nil {
			return nil, err
		}
		if len(data) > maxViewBytes {
			return nil, fmt.Errorf("%s: larger than %d bytes, not read", filepath.Join(dir, name), maxViewBytes)
		}
		return data, nil
	}
	return updateBeneath(r, name, read, mutate)
}

// updateBeneath is the body Update and UpdateSharedFile share: Claude's storage lock beneath r,
// read, mutate, and an O_EXCL temp file renamed over name.
func updateBeneath(r *os.Root, name string, read func(*os.Root) ([]byte, error),
	mutate func(current []byte) ([]byte, error)) (wrote, locked bool, err error) {
	release, locked := acquireStorageLock(r)
	defer release()
	current, err := read(r)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, locked, err
	}
	next, err := mutate(current)
	if err != nil || next == nil || bytes.Equal(next, current) {
		return false, locked, err
	}
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return false, locked, err
	}
	tmp := name + ".tmp." + hex.EncodeToString(suffix[:])
	f, err := r.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return false, locked, err
	}
	if _, err := f.Write(next); err != nil {
		f.Close()
		_ = r.Remove(tmp)
		return false, locked, err
	}
	if err := f.Close(); err != nil {
		_ = r.Remove(tmp)
		return false, locked, err
	}
	if err := r.Rename(tmp, name); err != nil {
		_ = r.Remove(tmp)
		return false, locked, err
	}
	return true, locked, nil
}

// Write replaces the view with data, on Update's terms.
func (l Location) Write(data []byte) error {
	_, _, err := l.Update(func([]byte) ([]byte, error) { return data, nil })
	return err
}

// RemoveLegacyLink removes the `shared_credentials` hook's own link at the view, the one a launch
// before the view left there, and reports whether it did. Only a link whose target is exactly
// LegacyLinkTarget is removed; a link to anywhere else is left for Update to refuse, because
// yolo did not make it. Removing a link never follows it.
//
// A host view has no such link to remove: no hook ever ran in yolo's own directory.
func (l Location) RemoveLegacyLink() (bool, error) {
	if l.IsHost() {
		return false, nil
	}
	r, err := l.openDir()
	if err != nil {
		return false, err
	}
	defer r.Close()
	fi, err := r.Lstat(ViewFile)
	if err != nil || fi.Mode()&fs.ModeSymlink == 0 {
		return false, nil
	}
	target, err := r.Readlink(ViewFile)
	if err != nil || target != LegacyLinkTarget {
		return false, nil
	}
	if err := r.Remove(ViewFile); err != nil {
		return false, err
	}
	return true, nil
}
