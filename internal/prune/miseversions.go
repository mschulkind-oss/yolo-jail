package prune

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/miseuse"
	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// miseversions.go reclaims the shared mise tool store's UNUSED VERSIONS: the installed versions no
// jail on this machine has used for 30 days, once they total 1 GiB, offered at a launch and
// automatic once the user says yes — docs/design/minimal-disk-footprint.md OQ-DF4, ruled
// 2026-10-05 (option A). The store is <state>/mise on the host, bound at /mise in every jail, and
// `yolo stores` listed it as reclaimed by nothing.
//
// "USED" IS MACHINE-WIDE, AND THAT IS THE WHOLE DIFFICULTY. Every workspace's jail shares the one
// store, but mise's own record of what is in use is per workspace, so `mise prune` in any one jail
// removes versions other workspaces still run. The evidence here is the MISE USE RECORD
// (internal/miseuse): every jail writes, into the store, what its workspace uses, refreshed daily
// while it runs, and this file joins them all. A version is reclaimable only when no record in
// force names it AND its own directory is older than the window, so a version installed in the
// last 30 days — by a jail whose next record has not been written yet — is kept by its own age.
//
// A RECORD IS IN FORCE while it is younger than the window, or while its workspace's jail is
// running, whatever its age — except that a record saying its jail could not tell what it uses is
// SUPERSEDED by any newer record of the same workspace, since every jail life writes a record of
// its own and the earlier life's is never replaced. And the pass DECLINES, reclaiming nothing, whenever the records
// cannot answer — the tri-state rule every reaper here keeps:
//
//   - the runtime cannot be asked which jails are running;
//   - a running jail has no record at all (one a yolo older than the record started, or one whose
//     main process has not written its first yet);
//   - a record in force, the newest of its workspace, says its jail could not tell what it uses,
//     or a recent record cannot be read.
//
// AND IT JUDGES NOTHING UNTIL THE RECORDING IS A WINDOW OLD (MiseSweep.Waiting, which is not a
// failure). Before that, a version a workspace used without recording it — under the yolo that
// preceded the record — would read as unused, and that is the cross-workspace mistake the record
// exists to prevent.
//
// HOST-ONLY, at every caller. A jail sees the store, but not the host's running jails, so it can
// never tell an unrecorded running jail from no jail at all.

// MiseVersionsWindow is how long a version must go unused by every jail before it is reclaimable:
// the record's own window, which is the ruling's 30 days.
const MiseVersionsWindow = miseuse.Window

// miseReclaimPrefix names a version directory a removal has taken out of mise's sight but not yet
// finished deleting: hidden, so mise no longer lists it, and finished by the next removal pass,
// whatever the records say — it needs no judgement, since no mise can run it.
const miseReclaimPrefix = ".yolo-reclaim-"

// MiseVersion is one installed version directory of the shared tool store.
type MiseVersion struct {
	// Rel is "<tool dir>/<version>" beneath the store's installs/.
	Rel   string `json:"rel"`
	Bytes int64  `json:"bytes"`
	Files int    `json:"files"`
	// Changed is the later of its directory's modification and change times: when it was
	// installed, near enough, or last changed since.
	Changed time.Time `json:"changed"`
	// LastUsed is the newest record naming it, in force or not; zero when no record ever has.
	LastUsed time.Time `json:"last_used,omitempty"`
	// Err is why a removal failed.
	Err string `json:"error,omitempty"`
	// leftover marks a directory an interrupted removal left hidden: no judging, only finishing.
	leftover bool
}

// Display is the version as a person reads it: "python 3.11.14".
func (v MiseVersion) Display() string { return strings.Replace(v.Rel, "/", " ", 1) }

// MiseSweep is one pass over the store: what it found, and what it did.
type MiseSweep struct {
	Store string
	// Installed is how many version directories the store holds.
	Installed int
	// Since is when the store got its first use record; zero when it never has.
	Since time.Time
	// Waiting, when set, is why nothing can be judged YET: the recording is younger than the
	// window. Not a failure, and nothing is removed.
	Waiting string
	// Declined, when set, is why the records cannot answer NOW. A failure: nothing is removed,
	// and `yolo prune` exits non-zero.
	Declined string
	// Remedy is the next step a decline names: what makes the records answer.
	Remedy string
	// Candidates is every version no jail has used for the window, largest first.
	Candidates []MiseVersion
	// Bytes is their total; a lower bound when Partial.
	Bytes   int64
	Partial bool
	Removed []MiseVersion
	// RemovedBytes is what Removed held.
	RemovedBytes int64
	// Failed is every candidate whose removal failed, with its Err.
	Failed []MiseVersion
	// LeftoverBytes is what the directories an interrupted removal left still hold: counted apart
	// from Bytes, since they are no version's, and finished by the next removal; a lower bound
	// when Partial.
	LeftoverBytes int64
	// leftovers are directories an interrupted removal left, which the next removal finishes.
	leftovers []MiseVersion
	live      runtime.LiveSet
}

// Leftovers is how many directories an interrupted removal left for the next removal to finish.
func (s MiseSweep) Leftovers() int { return len(s.leftovers) }

// FindUnusedMiseVersions judges the store at store (paths.GlobalMise() on the host): every version
// no jail has used for the window, sized within budget. live is the runtime's running jails, and
// an unknown set declines. It deletes nothing.
func FindUnusedMiseVersions(store string, live runtime.LiveSet, now time.Time, budget time.Duration) MiseSweep {
	s := MiseSweep{Store: store, live: live}
	root, err := os.OpenRoot(store)
	if errors.Is(err, fs.ErrNotExist) {
		return s
	}
	if err != nil {
		s.Declined = "the tool store cannot be opened: " + err.Error()
		s.Remedy = "check that " + store + " is readable by you, then run `yolo prune` again"
		return s
	}
	defer root.Close()
	versions, leftovers, err := listMiseVersions(root)
	if err != nil {
		s.Declined = "the tool store's installs cannot be listed: " + err.Error()
		s.Remedy = "check that " + store + "/installs is readable by you, then run `yolo prune` again"
		return s
	}
	if budget <= 0 {
		budget = 24 * time.Hour // unbounded, for a caller that asked for no budget
	}
	deadline := time.Now().Add(budget)
	s.Installed, s.leftovers = len(versions), leftovers
	// Sized before any decline or wait: a leftover needs no judgement, and a removal finishes it
	// whatever the records say, so every report counts it.
	for i := range s.leftovers {
		if time.Now().After(deadline) || !sizeMiseVersion(root, &s.leftovers[i], deadline) {
			s.Partial = true
		}
		s.LeftoverBytes += s.leftovers[i].Bytes
	}
	if len(versions) == 0 {
		return s
	}
	census, err := miseuse.ReadAll(store)
	if err != nil {
		s.Declined = "the use records cannot be read: " + err.Error()
		s.Remedy = "check that " + store + "/" + miseuse.DirName + " is a directory you can read, then run `yolo prune` again"
		return s
	}
	s.Since = census.Since
	if census.SinceErr != nil {
		s.Waiting = fmt.Sprintf("the record's start time, %s/%s/%s, cannot be read (%v); "+
			"the next launch on this host replaces it and starts the clock again",
			store, miseuse.DirName, miseuse.SinceName, census.SinceErr)
		return s
	}
	if census.Since.IsZero() {
		s.Waiting = "no launch on this host has run a jail that records the tool versions it uses yet; " +
			"the next launch on this host starts the clock"
		return s
	}
	if judgeable := census.Since.Add(MiseVersionsWindow); now.Before(judgeable) {
		s.Waiting = fmt.Sprintf("jails have recorded the tool versions they use only since %s, "+
			"so the first versions can be judged unused on %s",
			census.Since.Local().Format("2006-01-02"), judgeable.Local().Format("2006-01-02"))
		return s
	}
	j := judgeMiseUse(census, live, now)
	if j.declined != "" {
		s.Declined, s.Remedy = j.declined, j.remedy
		return s
	}
	for _, v := range versions {
		if _, used := j.inForce[v.Rel]; used {
			continue
		}
		if now.Sub(v.Changed) < MiseVersionsWindow {
			continue // installed within the window: kept by its own age
		}
		v.LastUsed = j.lastSeen[v.Rel]
		if time.Now().After(deadline) {
			s.Partial = true
		} else if !sizeMiseVersion(root, &v, deadline) {
			s.Partial = true
		}
		s.Candidates = append(s.Candidates, v)
		s.Bytes += v.Bytes
	}
	sort.SliceStable(s.Candidates, func(a, b int) bool {
		if s.Candidates[a].Bytes != s.Candidates[b].Bytes {
			return s.Candidates[a].Bytes > s.Candidates[b].Bytes
		}
		return s.Candidates[a].Rel < s.Candidates[b].Rel
	})
	return s
}

// PruneUnusedMiseVersions is FindUnusedMiseVersions, then — with apply — the removal of every
// candidate: `yolo prune`'s form. Each removal still rechecks the records first.
func PruneUnusedMiseVersions(store string, live runtime.LiveSet, apply bool, now time.Time, budget time.Duration) MiseSweep {
	s := FindUnusedMiseVersions(store, live, now, budget)
	if !apply {
		return s
	}
	return PruneUnusedMiseVersionsGuarded(s, now, nil)
}

// PruneUnusedMiseVersionsGuarded removes s's candidates, each under guard (guard.go), whose
// recheck reads the records and the version's directory again right before the removal: a jail
// that recorded the version since the pass judged it, or installed it again, keeps it. A nil
// guard still rechecks, without a lock. A sweep that waited or declined removes no version.
//
// A removal first RENAMES the version out of mise's sight, then deletes it, so an interrupted or
// failed delete leaves a hidden directory no mise lists rather than a half-deleted version mise
// would run; the next call finishes it, before anything else and even when the sweep waited or
// declined, since what no mise can run needs no judgement. Alias links mise keeps beside the version ("22 ->
// ./22.20.0") go with it, since they would dangle.
func PruneUnusedMiseVersionsGuarded(s MiseSweep, now time.Time, guard Guard) MiseSweep {
	if len(s.Candidates) == 0 && len(s.leftovers) == 0 {
		return s
	}
	if guard == nil {
		guard = func(recheck func() bool, del func()) bool {
			if recheck != nil && !recheck() {
				return false
			}
			del()
			return true
		}
	}
	root, err := os.OpenRoot(s.Store)
	if err != nil {
		for _, v := range s.Candidates {
			v.Err = err.Error()
			s.Failed = append(s.Failed, v)
		}
		return s
	}
	defer root.Close()
	for _, v := range s.leftovers {
		var rmErr error
		guard.Do(func() bool { return true }, func() { rmErr = removeTreeBeneath(root, path.Join("installs", v.Rel)) })
		if rmErr == nil {
			s.Removed = append(s.Removed, v)
			s.RemovedBytes += v.Bytes
		}
	}
	if s.Declined != "" || s.Waiting != "" {
		return s
	}
	for _, v := range s.Candidates {
		var rmErr error
		ran := guard.Do(
			func() bool { return miseVersionStillUnused(root, s.Store, v.Rel, s.live, now) },
			func() { rmErr = removeMiseVersion(root, v.Rel, now) })
		switch {
		case !ran:
			// Kept: in use now, or the lock could not be taken. The next pass judges it again.
		case rmErr != nil:
			v.Err = rmErr.Error()
			s.Failed = append(s.Failed, v)
		default:
			s.Removed = append(s.Removed, v)
			s.RemovedBytes += v.Bytes
		}
	}
	return s
}

// miseJudgement is what the records say about the store.
type miseJudgement struct {
	// inForce is every install a record in force names.
	inForce map[string]bool
	// lastSeen is, per install, the newest record naming it, in force or not.
	lastSeen map[string]time.Time
	// declined is why the records cannot answer, and remedy what would make them.
	declined, remedy string
}

// judgeMiseUse reads a census against the running jails. The rules are the file comment's.
func judgeMiseUse(c miseuse.Census, live runtime.LiveSet, now time.Time) miseJudgement {
	j := miseJudgement{inForce: map[string]bool{}, lastSeen: map[string]time.Time{}}
	if !live.Known {
		j.declined = "the container runtime could not be asked which jails are running"
		j.remedy = "check that the runtime answers (`podman ps`), then run `yolo prune` again"
		return j
	}
	cnames := map[string]string{}
	cnameOf := func(ws string) string {
		if ws == "" {
			return ""
		}
		if n, ok := cnames[ws]; ok {
			return n
		}
		n := runtime.FromWorkspace(ws)
		cnames[ws] = n
		return n
	}
	// A workspace's NEWEST record is what it says now. Each jail life writes under a name of its
	// own, so the record an earlier life left is never replaced: without this, an earlier life's
	// "could not tell" would outlive the fix and the relaunch its remedy asks for.
	newest := map[string]time.Time{}
	for _, r := range c.Records {
		if ws := r.Record.Workspace; r.Err == nil && ws != "" && r.Record.Recorded.After(newest[ws]) {
			newest[ws] = r.Record.Recorded
		}
	}
	recorded := map[string]bool{}
	for _, r := range c.Records {
		if r.Err != nil {
			if now.Sub(r.Modified) < MiseVersionsWindow {
				j.declined = fmt.Sprintf("the use record %s cannot be read (%v)", r.File, r.Err)
				j.remedy = fmt.Sprintf("it stops counting on %s; delete it from the store's %s "+
					"directory to judge without it now", r.Modified.Add(MiseVersionsWindow).Local().Format("2006-01-02"),
					miseuse.DirName)
				return j
			}
			continue
		}
		rec := r.Record
		name := cnameOf(rec.Workspace)
		_, running := live.Names[name]
		running = running && name != ""
		if name != "" {
			recorded[name] = true
		}
		for _, rel := range rec.Installs {
			if rec.Recorded.After(j.lastSeen[rel]) {
				j.lastSeen[rel] = rec.Recorded
			}
		}
		if !running && now.Sub(rec.Recorded) >= MiseVersionsWindow {
			continue
		}
		if rec.Unknown != "" && rec.Workspace != "" && rec.Recorded.Before(newest[rec.Workspace]) {
			continue // superseded: a later record of the same workspace says what it uses
		}
		if rec.Unknown != "" {
			j.declined = fmt.Sprintf("a jail of %s could not say which tool versions it uses "+
				"(recorded %s: %s)", rec.Workspace, rec.Recorded.Local().Format("2006-01-02"), rec.Unknown)
			if running {
				j.remedy = "fix what stops `mise ls` in that jail (its mise config, usually); the jail records again within a day, and at its next start"
			} else {
				j.remedy = fmt.Sprintf("fix what stops `mise ls` in that workspace (its mise config, usually) "+
					"and launch it again, or wait: the record stops counting on %s",
					rec.Recorded.Add(MiseVersionsWindow).Local().Format("2006-01-02"))
			}
			return j
		}
		for _, rel := range rec.Installs {
			j.inForce[rel] = true
		}
	}
	running := make([]string, 0, len(live.Names))
	for name := range live.Names {
		running = append(running, name)
	}
	sort.Strings(running)
	for _, name := range running {
		if !recorded[name] {
			j.declined = fmt.Sprintf("the running jail %s has not recorded which tool versions it uses", name)
			j.remedy = "a jail records within seconds of starting; one a yolo older than the record " +
				"started never does, so stop it (`yolo stop` in its workspace) or wait for it to end, " +
				"then run `yolo prune` again"
			return j
		}
	}
	return j
}

// listMiseVersions lists every version directory beneath installs/: a real directory in a real
// tool directory, its name not hidden. A link is never a version (mise's aliases, a `mise link`
// to a directory outside the store); a hidden reclaim directory is a leftover.
func listMiseVersions(root *os.Root) (versions, leftovers []MiseVersion, err error) {
	tools, err := readDirBeneathRoot(root, "installs")
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	for _, t := range tools {
		tdir := path.Join("installs", t)
		fi, err := root.Lstat(tdir)
		if err != nil || !fi.IsDir() || strings.HasPrefix(t, ".") {
			continue
		}
		entries, err := readDirBeneathRoot(root, tdir)
		if err != nil {
			return nil, nil, err
		}
		for _, name := range entries {
			fi, err := root.Lstat(path.Join(tdir, name))
			if err != nil || !fi.IsDir() {
				continue
			}
			v := MiseVersion{Rel: t + "/" + name, Changed: changedTime(fi)}
			switch {
			case strings.HasPrefix(name, miseReclaimPrefix):
				v.leftover = true
				leftovers = append(leftovers, v)
			case strings.HasPrefix(name, "."):
			default:
				versions = append(versions, v)
			}
		}
	}
	return versions, leftovers, nil
}

func readDirBeneathRoot(root *os.Root, rel string) ([]string, error) {
	f, err := root.Open(rel)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	names, err := f.Readdirnames(-1)
	if err != nil {
		return nil, err
	}
	sort.Strings(names)
	return names, nil
}

// changedTime is the later of fi's modification and change times. Either moving forward makes a
// version look younger, so it is kept longer: the side an age guard errs on.
func changedTime(fi os.FileInfo) time.Time {
	t := fi.ModTime()
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		if c := statChangeTime(st); c.After(t) {
			t = c
		}
	}
	return t
}

// sizeMiseVersion sums v's regular files' apparent sizes, the measure `yolo stores` uses. False
// when the deadline cut the walk short, leaving a lower bound.
func sizeMiseVersion(root *os.Root, v *MiseVersion, deadline time.Time) bool {
	complete := true
	_ = fs.WalkDir(root.FS(), path.Join("installs", v.Rel), func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if time.Now().After(deadline) {
			complete = false
			return fs.SkipAll
		}
		if d.Type().IsRegular() {
			if info, ierr := d.Info(); ierr == nil {
				v.Bytes += info.Size()
				v.Files++
			}
		}
		return nil
	})
	return complete
}

// miseVersionStillUnused is the removal's recheck: the version is still a real directory older
// than the window, and the records, read again, still answer and name it nowhere.
func miseVersionStillUnused(root *os.Root, store, rel string, live runtime.LiveSet, now time.Time) bool {
	fi, err := root.Lstat(path.Join("installs", rel))
	if err != nil || !fi.IsDir() || now.Sub(changedTime(fi)) < MiseVersionsWindow {
		return false
	}
	census, err := miseuse.ReadAll(store)
	if err != nil {
		return false
	}
	j := judgeMiseUse(census, live, now)
	return j.declined == "" && !j.inForce[rel]
}

// removeMiseVersion takes rel out of mise's sight by renaming it hidden, removes the alias links
// that named it, then deletes it. Beneath root throughout: the store is jail-writable, and a link
// a jail left inside it resolves within the store or not at all.
func removeMiseVersion(root *os.Root, rel string, now time.Time) error {
	tool, version := path.Split(rel)
	tdir := path.Join("installs", tool)
	hidden := path.Join(tdir, fmt.Sprintf("%s%s.%d", miseReclaimPrefix, version, now.UnixNano()))
	if err := root.Rename(path.Join(tdir, version), hidden); err != nil {
		return err
	}
	if names, err := readDirBeneathRoot(root, tdir); err == nil {
		for _, name := range names {
			link := path.Join(tdir, name)
			fi, err := root.Lstat(link)
			if err != nil || fi.Mode()&fs.ModeSymlink == 0 {
				continue
			}
			if target, err := root.Readlink(link); err == nil && path.Clean(target) == version {
				_ = root.Remove(link)
			}
		}
	}
	return removeTreeBeneath(root, hidden)
}

// removeTreeBeneath removes rel and everything under it, and if that fails once, makes every
// directory in it writable and tries again: an install can carry a read-only directory, which
// stops a plain recursive removal half way.
func removeTreeBeneath(root *os.Root, rel string) error {
	if err := root.RemoveAll(rel); err == nil {
		return nil
	}
	_ = fs.WalkDir(root.FS(), rel, func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			_ = root.Chmod(p, 0o755)
		}
		return nil
	})
	return root.RemoveAll(rel)
}
