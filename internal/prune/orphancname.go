package prune

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// orphancname.go is the sweep of PER-CONTAINER-NAME HOST STATE: the small files yolo's global
// storage holds for one container name (runtime.FromWorkspace's deterministic name, "cname"
// below), which every launch writes and nothing removed once the name stopped being used.
//
// # WHY IT EXISTS
//
// A cname is a hash of a workspace path, so every distinct workspace mints a new one, and the
// integration suite, the npm-resolve jails and the fork-build jails each launch from a fresh
// temporary workspace. Each such launch left, for ever:
//
//   - locks/<cname>.lock, .keeper, .sessions, .arrival, .host-daemons.lock and the
//     .session-records directory (internal/cli/run: flock.go, keeperstate.go, sessionlock.go,
//     hostdaemonperjail.go). A .keeper is removed only when it names the right PID, so a crash
//     leaves it.
//   - build/current-images/<cname> (currentimages.go), which CurrentImageTags skips once its
//     workspace is gone but never deletes.
//   - approvals/<cname>.json, .scope.json, .scope-sources.json, .loophole-grants.json (internal/config).
//   - logs/jail-keeper-<cname>.log and <cname>-socat.log, each capped but one per name, and the
//     <cname>-cgd.log the retired per-jail cgroup-delegate daemon wrote (no current writer; the
//     files remain on machines that ran it).
//   - owners/<cname>, removed only on a clean exit (internal/cli/run/lifecycle.go).
//
// In a development jail the lock files alone had reached six figures when this was written,
// almost all of them the integration suite's and the nested build jails'.
//
// # WHEN A NAME IS AN ORPHAN — every term is required
//
//  1. The container runtime lists NO container of that name, in any state. A runtime that cannot
//     be asked declines the whole sweep (the caller passes knownOK=false): unknown is not
//     permission, as for every reaper in this package.
//  2. No tracking file names it under containers/, and no live host-services session's key
//     (paths.JailShortHash) is its hash — a macos-user session has no container, and its keeper
//     and session locks are keyed by its cname.
//  3. EVERY file of the name is older than OrphanCnameAgeFloor. The floor is per NAME, not per
//     file: one recent file (a launch lock a launch truncated, a keeper log a keeper wrote) keeps
//     the whole name, so the sweep never splits one jail's state into kept and removed halves.
//     Fourteen days is far past any launch, keeper or capture this code runs, and short enough
//     that a CI-heavy machine sheds the integration suite's names within a fortnight.
//  4. For the current-image pointer and the approval record only: the WORKSPACE PATH IS GONE.
//     The pointer is the one record of a cname's workspace (it holds the resolved path, and the
//     name is a one-way hash), so a name with no readable pointer, or one whose recorded path
//     does not hash back to the name, keeps both. A deleted approval record makes the next launch
//     of that workspace prompt again (BB-D30: a missing part diffs against none and asks), which
//     is safe but is a cost a returning workspace should never pay, so the record goes only when
//     the workspace provably cannot return at that path. The pointer is kept by the same test
//     because it is the evidence the approval decision reads: it is deleted after the approvals,
//     in the same pass, and only when every approval part of the name went. A part that stayed
//     (its recheck failed, or its removal did) keeps the pointer, so the next sweep can still
//     prove the workspace gone and finish the record.
//
//     The approval records keep the project key while each jail's pointer has the jail's name
//     (named-setups-plan.md SUP-D39), so a project key's records are judged by the pointer of any
//     of its jails, and such a pointer stays while any of those records does.
//
//     An unmounted workspace (a detached volume, an unmounted share) reads as gone: past the
//     floor its approvals go and its next launch prompts again. That is the accepted limit.
//
// # LOCK FILES ARE REMOVED ONLY WHILE HELD
//
// Unlinking a lock file another process is about to flock splits the lock: the late opener locks
// the unlinked inode while the next opener creates and locks a new one, and both believe they
// hold it. So a lock file is removed only after flock(LOCK_EX|LOCK_NB) on it succeeds, and only
// when the path still names the inode that was locked (os.SameFile on the held descriptor and a
// fresh Lstat): a busy lock or a replaced file is skipped and counted. The residual window — a
// process that opened the old inode before the unlink and flocks it after — needs a launch of a
// workspace that has been idle for the age floor to start in the microseconds of the unlink, and
// the per-name launch lock holders do not re-check their inode after acquiring.
//
// # WHAT IS NEVER TOUCHED
//
// Only names that parse as a cname ("yolo-<sanitized>-<8 hex>", runtime.FromResolved's shape)
// followed by one of the per-name suffixes below are candidates. The machine-wide files in
// locks/ — image-copy.lock (internal/image), housekeeping.lock and housekeeping-pass.lock
// (internal/cli/run), host-apply.lock (internal/cli), capture-<program>.lock and
// fork-build-<id>.lock (internal/cli), macos-user-sessions, macos-user-pack-trees and
// macos-user-home (internal/macosuser, internal/cli/run) — never parse, so they are never
// candidates. The host-service-*.log, launch-service-*.log, launches.log and crossings.log in
// logs/ do not parse either.

// OrphanCnameAgeFloor is the age every file of a name must have passed before the name is
// swept (term 3 above).
const OrphanCnameAgeFloor = 14 * 24 * time.Hour

// cnamePattern is a jail name: runtime.FromResolved's output ("yolo-", the sanitized basename,
// "-", eight hex), the legacy name, optionally followed by "-" and a setup's name (runtime.JailName,
// setup.ValidName's shape; named-setups-plan.md SUP-D2).
const cnamePattern = `yolo-[a-z0-9-]+-[0-9a-f]{8}(?:-[a-z0-9](?:[a-z0-9-]{0,30}[a-z0-9])?)?`

// notchPattern is the optional ".<notch>" a macos-user keeper key adds to a cname
// (internal/cli/run keeperKey: "macos-user" and "guest").
const notchPattern = `(?:\.(?:macos-user|guest))?`

// The classes of per-name state, by directory.
const (
	OrphanClassLocks         = "locks"
	OrphanClassLogs          = "logs"
	OrphanClassOwners        = "owners"
	OrphanClassCurrentImages = "current-images"
	OrphanClassApprovals     = "approvals"
)

var (
	orphanLockRe     = regexp.MustCompile(`^(` + cnamePattern + `)` + notchPattern + `(\.host-daemons\.lock|\.lock|\.keeper|\.sessions|\.arrival|\.session-records)$`)
	orphanKeeperLog  = regexp.MustCompile(`^jail-keeper-(` + cnamePattern + `)` + notchPattern + `\.log(\.1|\.lock)?$`)
	orphanSidecarLog = regexp.MustCompile(`^(` + cnamePattern + `)-(?:socat|cgd)\.log(\.1|\.lock)?$`)
	orphanBareRe     = regexp.MustCompile(`^(` + cnamePattern + `)$`)
	orphanApprovalRe = regexp.MustCompile(`^(` + cnamePattern + `)(\.loophole-grants\.json|\.scope-sources\.json|\.scope\.json|\.json)$`)
)

// OrphanCnameOf parses one directory entry's base name in class's directory: the cname it
// belongs to, and whether it is a per-name file this sweep may consider at all. Exported so the
// writers' package can pin every spelling it writes against it (a writer whose file this does not
// parse is a leak again, silently).
func OrphanCnameOf(class, base string) (cname string, ok bool) {
	var m []string
	switch class {
	case OrphanClassLocks:
		m = orphanLockRe.FindStringSubmatch(base)
	case OrphanClassLogs:
		if m = orphanKeeperLog.FindStringSubmatch(base); m == nil {
			m = orphanSidecarLog.FindStringSubmatch(base)
		}
	case OrphanClassOwners, OrphanClassCurrentImages:
		m = orphanBareRe.FindStringSubmatch(base)
	case OrphanClassApprovals:
		m = orphanApprovalRe.FindStringSubmatch(base)
	}
	if m == nil {
		return "", false
	}
	return m[1], true
}

// OrphanCnameDirs are the directories the sweep reads, each explicit so a test and both callers
// name the same ones.
type OrphanCnameDirs struct {
	Locks, Logs, Owners, CurrentImages, Approvals string
	// Containers is the tracking-file directory (paths.ContainerDir), re-read before each deletion.
	Containers string
}

// OrphanCnameDirsUnder is the sweep's directories under one global storage root and build dir.
func OrphanCnameDirsUnder(globalStorage, buildDir, containerDir string) OrphanCnameDirs {
	return OrphanCnameDirs{
		Locks:         filepath.Join(globalStorage, "locks"),
		Logs:          filepath.Join(globalStorage, "logs"),
		Owners:        filepath.Join(globalStorage, "owners"),
		CurrentImages: CurrentImagesDir(buildDir),
		Approvals:     filepath.Join(globalStorage, "approvals"),
		Containers:    containerDir,
	}
}

// OrphanCnameReport is what one sweep did, or would do on a dry run.
type OrphanCnameReport struct {
	// Declined is set when the sweep did nothing because liveness was unknown.
	Declined bool
	// Names is every cname with at least one file removed (would be, on a dry run), sorted.
	Names []string
	// Files and Bytes count the removed entries; ByClass splits Files by class.
	Files   int
	Bytes   int64
	ByClass map[string]int
	// Busy counts lock files skipped because another process held them, or because the path
	// named another inode once the lock was taken.
	Busy int
}

type orphanEntry struct {
	class string
	path  string
	lock  bool // remove only while holding its flock
	dir   bool // a directory of lock files (.session-records)
}

// orphanLockFlock is flock(2), a var so a test can reach the error path.
var orphanLockFlock = syscall.Flock

// orphanAfterLock runs after a lock file's flock is taken and before its inode is re-checked.
// Nothing but a test sets it (to replace the file in that window).
var orphanAfterLock func(path string)

// AllYoloContainerNames lists every yolo-* container the runtime knows, in ANY state — the
// sweep's term 1 is "no container of that name", and a stopped container still owns its name.
// Known=false when the runtime cannot be asked.
func AllYoloContainerNames(rt string, run RunFunc) runtime.LiveSet {
	if rt == "" {
		return runtime.LiveSet{Known: false}
	}
	if rt == "container" {
		res := run([]string{"container", "ls", "--all"}, psTimeout)
		if !res.Ran || res.RC != 0 {
			return runtime.LiveSet{Known: false}
		}
		return runtime.LiveSet{Known: true, Names: runtime.ParseContainerLsLive(res.Stdout)}
	}
	res := run([]string{rt, "ps", "-a", "--format", "{{.Names}}"}, psTimeout)
	if !res.Ran || res.RC != 0 {
		return runtime.LiveSet{Known: false}
	}
	names := map[string]struct{}{}
	for _, line := range strings.Split(res.Stdout, "\n") {
		if t := strings.TrimSpace(line); strings.HasPrefix(t, "yolo-") {
			names[t] = struct{}{}
		}
	}
	return runtime.LiveSet{Known: true, Names: names}
}

// macosUserBesideRuntimes are the container runtimes a macos-user sweep looks for on PATH: a
// container jail of the same Mac owns its names whatever backend is asking.
var macosUserBesideRuntimes = []string{"podman", "container"}

// InstalledContainerRuntimes is every container runtime in macosUserBesideRuntimes that has
// reports installed. The macos-user housekeeping slot and `yolo prune` on that backend both read
// it, so the two known sets cannot drift apart.
func InstalledContainerRuntimes(has func(string) bool) []string {
	var out []string
	for _, rt := range macosUserBesideRuntimes {
		if has(rt) {
			out = append(out, rt)
		}
	}
	return out
}

// AllYoloContainerNamesOf is AllYoloContainerNames over several runtimes, merged. ok=false, with
// failed naming the runtime, when any of them cannot be asked: the caller declines.
func AllYoloContainerNamesOf(runtimes []string, run RunFunc) (names map[string]struct{}, failed string, ok bool) {
	names = map[string]struct{}{}
	for _, rt := range runtimes {
		all := AllYoloContainerNames(rt, run)
		if !all.Known {
			return nil, rt, false
		}
		for n := range all.Names {
			names[n] = struct{}{}
		}
	}
	return names, "", true
}

// LiveSessionKeys is the workspace key (paths.JailShortHash of the cname) of every host-services
// session under base that is live or whose liveness cannot be read. ok=false when the sessions
// cannot be listed, and the caller then declines.
func LiveSessionKeys(base string) (keys map[string]bool, ok bool) {
	sessions, ok := runtime.ListSessions(base)
	if !ok {
		return nil, false
	}
	keys = map[string]bool{}
	for _, s := range sessions {
		if s.Liveness != runtime.SessionGone && s.Key != "" {
			keys[s.Key] = true
		}
	}
	return keys, true
}

// PruneOrphanCnameStateGuarded sweeps the per-name state of every orphaned cname (the rules are
// the file header's). known is the runtime's names (any state) plus every name the caller must
// keep (tracked, the launching jail); sessionKeys is LiveSessionKeys; knownOK=false declines.
// Each deletion goes through guard with a recheck that the name is still untracked and the entry
// still past the floor; a nil guard deletes directly (`yolo prune`).
func PruneOrphanCnameStateGuarded(dirs OrphanCnameDirs, known map[string]struct{}, sessionKeys map[string]bool,
	knownOK, apply bool, now time.Time, guard Guard) OrphanCnameReport {
	rep := OrphanCnameReport{ByClass: map[string]int{}, Names: []string{}}
	if !knownOK {
		rep.Declined = true
		return rep
	}
	byName := map[string][]orphanEntry{}
	collect := func(class, dir string) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, e := range entries {
			cname, ok := OrphanCnameOf(class, e.Name())
			if !ok {
				continue
			}
			ent := orphanEntry{class: class, path: filepath.Join(dir, e.Name())}
			if class == OrphanClassLocks {
				ent.lock = true
				ent.dir = strings.HasSuffix(e.Name(), ".session-records")
			}
			if class == OrphanClassLogs && strings.HasSuffix(e.Name(), ".lock") {
				ent.lock = true // internal/logcap's trim-and-append sidecar
			}
			byName[cname] = append(byName[cname], ent)
		}
	}
	collect(OrphanClassLocks, dirs.Locks)
	collect(OrphanClassLogs, dirs.Logs)
	collect(OrphanClassOwners, dirs.Owners)
	collect(OrphanClassCurrentImages, dirs.CurrentImages)
	collect(OrphanClassApprovals, dirs.Approvals)

	names := make([]string, 0, len(byName))
	for n := range byName {
		names = append(names, n)
	}
	sort.Strings(names)

	// The approval records keep the PROJECT key (runtime.FromWorkspace, named-setups-plan.md
	// SUP-D39) while a launch names its pointer for its jail (runtime.JailName), so a project's
	// approvals usually have no pointer of their own name. A pointer of any of the project's
	// jails is the same evidence: it names the workspace, which hashes back to the project key.
	projectGone := map[string]bool{}
	for _, ent := range byName {
		for _, e := range ent {
			if e.class != OrphanClassCurrentImages {
				continue
			}
			name := filepath.Base(e.path)
			if ws, ok := pointerWorkspace(dirs.CurrentImages, name); ok && workspaceAbsent(ws) {
				projectGone[runtime.FromResolved(ws)] = true
			}
		}
	}
	// approvalsStay reports whether any approval record of project is still on disk. Names sort
	// the project key before each of its jails' names, so by the time a jail's pointer is weighed
	// the project's records have had their own turn. A dry run counts what it predicted removing
	// as removed, so it predicts what the apply does.
	predicted := map[string]bool{}
	approvalsStay := func(project string) bool {
		for _, e := range byName[project] {
			if e.class != OrphanClassApprovals || predicted[e.path] {
				continue
			}
			if _, err := os.Lstat(e.path); err == nil || !errors.Is(err, fs.ErrNotExist) {
				return true
			}
		}
		return false
	}

	for _, cname := range names {
		if _, keep := known[cname]; keep {
			continue
		}
		if sessionKeys[paths.JailShortHash(cname)] {
			continue
		}
		if tracked(dirs.Containers, cname) {
			continue
		}
		ents := byName[cname]
		if !allOlderThan(ents, now, OrphanCnameAgeFloor) {
			continue
		}
		wsGone := workspaceGone(dirs.CurrentImages, cname) || projectGone[cname]
		// Approvals before the pointer: the pointer is the evidence the approval decision read.
		sort.SliceStable(ents, func(i, j int) bool { return orphanOrder(ents[i].class) < orphanOrder(ents[j].class) })
		removedAny, approvalKept := false, false
		for _, ent := range ents {
			if (ent.class == OrphanClassApprovals || ent.class == OrphanClassCurrentImages) && !wsGone {
				continue
			}
			if ent.class == OrphanClassCurrentImages && approvalKept {
				continue // the evidence for the approval part that stayed
			}
			if ent.class == OrphanClassCurrentImages {
				// The evidence for the project's approval records, kept while any of them stays.
				if ws, ok := pointerWorkspace(dirs.CurrentImages, cname); ok && approvalsStay(runtime.FromResolved(ws)) {
					continue
				}
			}
			size := entrySize(ent.path)
			if !apply {
				predicted[ent.path] = true
				rep.Files++
				rep.Bytes += size
				rep.ByClass[ent.class]++
				removedAny = true
				continue
			}
			removed, busy := false, false
			guard.Do(func() bool {
				return !tracked(dirs.Containers, cname) && allOlderThan([]orphanEntry{ent}, now, OrphanCnameAgeFloor)
			}, func() { removed, busy = removeOrphanEntry(ent) })
			if busy {
				rep.Busy++
			}
			if removed {
				rep.Files++
				rep.Bytes += size
				rep.ByClass[ent.class]++
				removedAny = true
			} else if ent.class == OrphanClassApprovals {
				approvalKept = true
			}
		}
		if removedAny {
			rep.Names = append(rep.Names, cname)
		}
	}
	return rep
}

func orphanOrder(class string) int {
	switch class {
	case OrphanClassApprovals:
		return 0
	case OrphanClassCurrentImages:
		return 2
	default:
		return 1
	}
}

func tracked(containerDir, cname string) bool {
	if containerDir == "" {
		return false
	}
	_, err := os.Lstat(filepath.Join(containerDir, cname))
	return err == nil
}

// allOlderThan is term 3: every entry, and every child of a lock directory, last modified before
// now-floor. An entry that cannot be read counts as recent (keep).
func allOlderThan(ents []orphanEntry, now time.Time, floor time.Duration) bool {
	for _, ent := range ents {
		st, err := os.Lstat(ent.path)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue // already gone: nothing to protect
			}
			return false
		}
		if now.Sub(st.ModTime()) < floor {
			return false
		}
		if ent.dir {
			children, err := os.ReadDir(ent.path)
			if err != nil {
				return false
			}
			for _, c := range children {
				ci, err := c.Info()
				if err != nil || now.Sub(ci.ModTime()) < floor {
					return false
				}
			}
		}
	}
	return true
}

// workspaceGone is term 4: the cname's current-image pointer names a workspace that hashes back
// to the cname (its legacy name, or a setup's: runtime.IsJailOfResolved), and nothing exists at that path. Any doubt (no pointer, a malformed one, a path
// that does not hash back, an Lstat error other than not-exist) is "not gone".
func workspaceGone(currentImagesDir, cname string) bool {
	ws, ok := pointerWorkspace(currentImagesDir, cname)
	return ok && workspaceAbsent(ws)
}

// pointerWorkspace is the workspace cname's current-image pointer names, when the pointer is
// well formed and that workspace hashes back to cname; ok=false on any doubt.
func pointerWorkspace(currentImagesDir, cname string) (string, bool) {
	data, err := os.ReadFile(filepath.Join(currentImagesDir, cname))
	if err != nil {
		return "", false
	}
	var lines []string
	for _, line := range strings.Split(string(data), "\n") {
		if s := strings.TrimSpace(line); s != "" {
			lines = append(lines, s)
		}
	}
	if len(lines) < 2 {
		return "", false
	}
	ws := lines[1]
	if !filepath.IsAbs(ws) || !runtime.IsJailOfResolved(ws, cname) {
		return "", false
	}
	return ws, true
}

// workspaceAbsent is true only when nothing exists at ws; an Lstat error other than not-exist
// counts as present.
func workspaceAbsent(ws string) bool {
	_, err := os.Lstat(ws)
	return errors.Is(err, fs.ErrNotExist)
}

func entrySize(path string) int64 {
	st, err := os.Lstat(path)
	if err != nil {
		return 0
	}
	if st.IsDir() {
		return dirSizeBytes(path)
	}
	return st.Size()
}

// removeOrphanEntry removes one entry: a lock file only while held, a lock directory by removing
// each free child and then the directory if that emptied it, anything else directly. Symlinks and
// unexpected types are left alone.
func removeOrphanEntry(ent orphanEntry) (removed, busy bool) {
	st, err := os.Lstat(ent.path)
	if err != nil {
		return false, false
	}
	switch {
	case ent.dir:
		if !st.IsDir() {
			return false, false
		}
		children, err := os.ReadDir(ent.path)
		if err != nil {
			return false, false
		}
		for _, c := range children {
			if _, b := removeLockFileWhileHeld(filepath.Join(ent.path, c.Name())); b {
				busy = true
			}
		}
		return os.Remove(ent.path) == nil, busy
	case !st.Mode().IsRegular():
		return false, false
	case ent.lock:
		return removeLockFileWhileHeld(ent.path)
	default:
		return os.Remove(ent.path) == nil, false
	}
}

// removeLockFileWhileHeld unlinks path only while holding an exclusive flock on the inode it
// names, and only if the path still names that inode after the lock is taken (the file header's
// split-lock reason). busy reports a held lock or a replaced file.
func removeLockFileWhileHeld(path string) (removed, busy bool) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return false, false
	}
	defer func() { _ = f.Close() }()
	fd := int(f.Fd())
	if err := orphanLockFlock(fd, syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return false, true
	}
	defer func() { _ = orphanLockFlock(fd, syscall.LOCK_UN) }()
	if orphanAfterLock != nil {
		orphanAfterLock(path)
	}
	held, err := f.Stat()
	if err != nil {
		return false, false
	}
	cur, err := os.Lstat(path)
	if err != nil || !cur.Mode().IsRegular() || !os.SameFile(held, cur) {
		return false, true
	}
	return os.Remove(path) == nil, false
}
