package run

// legacyhome.go moves a project's legacy home, <ws>/.yolo/home, to the home of the setup that
// owns it, <ws>/.yolo/setups/<name>/home (docs/design/named-setups.md §9, OQ-SU5;
// named-setups-plan.md SUP-D9, SUP-D20, SUP-D21).
//
// The move is silent: no prompt, one informational line. It moves only a real directory (lstat,
// never a link) owned by the invoking user (or, for seatbelt-yolo-account, by the yolo account,
// SUP-D31) and laid by the selected setup's technology, and only
// once no jail of the project runs; "could not ask" waits for a later launch, as every reaper
// here declines when it cannot ask. The rename never replaces anything but an empty directory.
// Anything else stays where it is, with a line naming why and the one step that clears it.
// Nothing is ever deleted, and the move never refuses a launch.
//
// SUP-D21: until the legacy home has moved, a launch of the setup it belongs to keeps using
// .yolo/home and creates no .yolo/setups/<name>/home, so a deferred or left home is never
// stranded behind a fresh one, and a printed manual move never lands inside one.
//
// THE CALL SITE is Run, right after resolveSetup and before anything reads or prepares a home
// (selectHome below; slice 6a-ii). selectHome also decides which home the launch binds, and every
// reader of this launch's home asks o.homeDir() rather than spelling a path, so the move and the
// switch cannot drift apart: a launch that moved .yolo/home and still bound it would start on an
// empty home. TestLaunchMovesTheLegacyHomeAndBindsTheSetupsHome fails if the call is deleted.

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"

	"golang.org/x/sys/unix"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
	yoloruntime "github.com/mschulkind-oss/yolo-jail/internal/runtime"
	"github.com/mschulkind-oss/yolo-jail/internal/setup"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// projectJails is what the runtime can say about every jail of a project: the legacy name and
// each setup's JailName. Three answers, because "could not ask" is not "none runs".
type projectJails int

const (
	projectJailsStopped projectJails = iota
	projectJailsRunning
	projectJailsUnknown
)

func (j projectJails) String() string {
	switch j {
	case projectJailsStopped:
		return "stopped"
	case projectJailsRunning:
		return "running"
	}
	return "unknown"
}

// legacyHomeRequest is what the move needs to know about the launch.
type legacyHomeRequest struct {
	Workspace string
	// Setup is the setup this launch selected.
	Setup setup.Setup
	// UID is the invoking user, an owner whose home the move takes.
	UID int
	// AccountUID is the yolo account's uid when the selected setup runs as it under Seatbelt
	// (seatbelt-yolo-account, which lays its home as that account), and nil otherwise: the one
	// other owner whose home the move takes (SUP-D31).
	AccountUID *int
	// Jails is the answer about every jail of the project.
	Jails projectJails
	// AppleContainerLaid is the Apple Container layout signal (isAppleContainerInheritedCopy on a
	// generated user-config copy in the home): true attributes the home to Apple Container. nil
	// is no signal. It reads files beneath home, so it is asked only about a real directory of
	// the invoking user's.
	AppleContainerLaid func(home string) bool
}

type legacyHomeResult int

const (
	// legacyHomeAbsent: there is no legacy home. Nothing to say.
	legacyHomeAbsent legacyHomeResult = iota
	// legacyHomeNotOurs: the selected setup's technology never read .yolo/home (a host setup),
	// so the home is left for the setup that did, and this launch says nothing.
	legacyHomeNotOurs
	// legacyHomeDeferred: the home would move, but a jail of the project runs or the runtime
	// could not be asked; this launch keeps using it (SUP-D21) and a later one moves it.
	legacyHomeDeferred
	// legacyHomeMoved: the home is now the setup's home.
	legacyHomeMoved
	// legacyHomeLeft: yolo cannot move the home; it stays, with the step that clears the way.
	legacyHomeLeft
	// legacyHomeOthers: the home stays because it is not the selected setup's to take: an Apple
	// Container setup laid it and this setup is not one. This setup starts its own home, and the
	// line names the setup that moves it.
	legacyHomeOthers
)

func (r legacyHomeResult) String() string {
	return [...]string{"absent", "not ours", "deferred", "moved", "left", "another setup's"}[r]
}

// legacyHomeRename is the destination-exclusive rename; a test swaps it to reach the failures
// only another filesystem gives.
var legacyHomeRename = renameNoReplaceBetween

// readsLegacyHome reports whether the selected setup's technology ever used <ws>/.yolo/home as
// its home: every container and Seatbelt backend did (Apple Container and Seatbelt with one
// layout since slice 12a); a host setup never did.
func readsLegacyHome(s setup.Setup) bool {
	switch s.Confinement {
	case setup.ConfinementPodman, setup.ConfinementAppleContainer, setup.ConfinementSeatbelt:
		return true
	}
	return false
}

// adoptLegacyHome moves the legacy home to its setup's home when SUP-D9 allows, and returns the
// one line the launch prints ("" when there is nothing to say).
func adoptLegacyHome(r legacyHomeRequest) (legacyHomeResult, string) {
	if !readsLegacyHome(r.Setup) {
		return legacyHomeNotOurs, ""
	}
	stateDir := paths.WorkspaceStateDir(r.Workspace)
	src := paths.LegacyHomeState(r.Workspace)
	dest := paths.SetupHomeState(r.Workspace, r.Setup.Name)
	left := func(format string, a ...any) (legacyHomeResult, string) {
		return legacyHomeLeft, fmt.Sprintf(format, a...)
	}
	manual := shquote.JoinDisplay([]string{"mkdir", "-p", paths.SetupStateDir(r.Workspace, r.Setup.Name)}) +
		" && " + shquote.JoinDisplay([]string{"mv", "-n", src, dest})

	yolo, err := unix.Open(stateDir, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		if errors.Is(err, unix.ENOENT) {
			return legacyHomeAbsent, ""
		}
		if fi, lerr := os.Lstat(stateDir); lerr == nil && fi.Mode()&fs.ModeSymlink != 0 {
			return left("This project's state directory %s is a symbolic link, which yolo never "+
				"moves anything beneath, so any home of this project's in it was left in place. "+
				"Replace the link with the directory it points at and launch again.", stateDir)
		}
		return left("This project's state directory %s could not be opened (%v), so any home of "+
			"this project's in it was left in place. Fix that and launch again.", stateDir, err)
	}
	defer unix.Close(yolo)

	var st unix.Stat_t
	if err := unix.Fstatat(yolo, "home", &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		if errors.Is(err, unix.ENOENT) {
			return legacyHomeAbsent, ""
		}
		return left("This project's home %s could not be examined (%v), so it was left in place. "+
			"Fix that and launch again with no jail of the project running.", src, err)
	}

	// What a quiesced launch would leave is reported as left whatever runs, so no line promises a
	// move that will not happen; and the layout signal reads only a real directory of the user's.
	switch {
	case st.Mode&unix.S_IFMT == unix.S_IFLNK:
		return left("This project's home %s is a symbolic link, and yolo moves only a real "+
			"directory, so it was left in place. If it points at this project's home, put that "+
			"directory here in its place and launch again; otherwise remove the link, which "+
			"leaves what it points at untouched: `%s`", src, shquote.JoinDisplay([]string{"rm", src}))
	case st.Mode&unix.S_IFMT != unix.S_IFDIR:
		return left("This project's home %s is not a directory, so it was left in place. Move it "+
			"aside and the next launch starts a fresh home: `%s`", src,
			shquote.JoinDisplay([]string{"mv", "-n", src, src + ".not-a-home"}))
	case int(st.Uid) != r.UID && (r.AccountUID == nil || int(st.Uid) != *r.AccountUID):
		return left("This project's home %s belongs to uid %d, not to you (uid %d), so it was "+
			"left in place. Give it to you and launch again with no jail of the project "+
			"running: `%s`", src, st.Uid, r.UID,
			shquote.JoinDisplay([]string{"sudo", "chown", "-R", fmt.Sprint(r.UID), src}))
	}
	// Minor-5 of the 6a-i review: the owner is named as "an Apple Container setup", since a user's
	// own Apple Container setup may carry any name; the example names the built-in.
	if r.Setup.Confinement != setup.ConfinementAppleContainer && r.AppleContainerLaid != nil && r.AppleContainerLaid(src) {
		return legacyHomeOthers, fmt.Sprintf("This project's home %s was laid by an Apple Container "+
			"setup, so the %s setup starts with its own home and leaves that one in place. To move "+
			"it to its owner, launch an Apple Container setup with no jail of the project running, "+
			"for example `yolo run --setup apple-container-self`", src, r.Setup.Name)
	}

	if r.Jails != projectJailsStopped {
		why := "a jail of this project is running"
		if r.Jails == projectJailsUnknown {
			why = "yolo could not ask the container runtime whether a jail of this project is running"
		}
		return legacyHomeDeferred, fmt.Sprintf("This project's home %s moves to %s once no jail of "+
			"the project runs; %s, so the %s setup keeps using it until then. Run `yolo stop` "+
			"and launch again to move it.", src, dest, why, r.Setup.Name)
	}

	setupDir, err := paths.EnsureSetupStateDir(r.Workspace, r.Setup.Name)
	if err != nil {
		var linked *paths.LinkedStateDirError
		if errors.As(err, &linked) {
			return left("%s is a symbolic link, which yolo never writes beneath, so this "+
				"project's home %s was left in place. Remove the link and launch again with no "+
				"jail of the project running: `%s`", linked.Path, src,
				shquote.JoinDisplay([]string{"rm", linked.Path}))
		}
		return left("The %s setup's directory could not be made (%v), so this project's home %s "+
			"was left in place. Fix that and launch again with no jail of the project running.",
			r.Setup.Name, err, src)
	}
	defer setupDir.Close()
	dirfd := int(setupDir.Fd())

	// An empty directory at the destination holds nothing to lose, and the kernel checks the
	// emptiness: rmdir fails on anything else, a link included.
	destTaken := func() (legacyHomeResult, string) {
		return left("%s already holds something, so this project's home %s was left in place. "+
			"To give the %s setup the old home, move what is there aside and launch again with "+
			"no jail of the project running: `%s`", dest, src, r.Setup.Name,
			shquote.JoinDisplay([]string{"mv", "-n", dest, dest + ".aside"}))
	}
	switch err := unix.Unlinkat(dirfd, "home", unix.AT_REMOVEDIR); {
	case err == nil, errors.Is(err, unix.ENOENT):
	case errors.Is(err, unix.ENOTEMPTY), errors.Is(err, unix.EEXIST), errors.Is(err, unix.ENOTDIR):
		return destTaken()
	default:
		return left("%s could not be cleared (%v), so this project's home %s was left in place. "+
			"Move it yourself while no jail of the project runs: `%s`", dest, err, src, manual)
	}

	switch err := legacyHomeRename(yolo, "home", dirfd, "home"); {
	case err == nil:
		// The record of the mountpoints yolo made in that home lives beside it (preparePackFiles,
		// filepath.Dir of the home), so it goes with it: without it the setup's launches could not
		// retire scaffolding the legacy launches left (named-setups-plan.md SUP-D33). Best effort, beneath the same two
		// descriptors, never replacing; a record that stays behind only leaves those unowned.
		_ = renameNoReplaceBetween(yolo, packFilesMountpointManifestName, dirfd, packFilesMountpointManifestName)
		// The capture store (the prism baseline) belongs to the home it was rendered into, so it
		// goes with it too (SUP-D35): left behind, the setup's first boot would diff the moved
		// home against no baseline and lose the overlay the user built. Same terms.
		_ = renameNoReplaceBetween(yolo, "prism", dirfd, "prism")
		return legacyHomeMoved, fmt.Sprintf("Moved this project's home %s to %s: each setup keeps its own home now.", src, dest)
	case errors.Is(err, unix.ENOENT):
		return legacyHomeAbsent, ""
	case errors.Is(err, unix.EEXIST), errors.Is(err, unix.ENOTEMPTY):
		return destTaken()
	case errors.Is(err, unix.EXDEV):
		return left("This project's home %s is its own mount, so yolo cannot rename it and left "+
			"it in place. Move it yourself while no jail of the project runs: `%s`", src, manual)
	case errors.Is(err, unix.EINVAL), errors.Is(err, unix.ENOTSUP), errors.Is(err, unix.EOPNOTSUPP):
		return left("The filesystem under %s cannot do a rename that refuses to replace, so yolo "+
			"left this project's home %s in place. Move it yourself while no jail of the project "+
			"runs: `%s`", stateDir, src, manual)
	default:
		return left("Moving this project's home %s to %s failed (%v), so it was left in place. "+
			"Move it yourself while no jail of the project runs: `%s`", src, dest, err, manual)
	}
}

// legacyHomeAccountUID is the yolo account's uid (macosuser.SandboxUID), which owns the home a
// seatbelt-yolo-account launch ran in (SUP-D31). A var so a test off macOS can stand it in.
var legacyHomeAccountUID = macosuser.SandboxUID

// selectHome is the launch's half of SUP-D9 and SUP-D21: it moves the legacy home to the selected
// setup's home when the move is allowed, prints the move's one line, and records which home this
// launch binds (o.homeState). Run calls it once every launch gate has passed, before anything reads or
// prepares a home. It never refuses the launch.
//
// WHILE A LEGACY HOME IS IN PLACE it decides under the PROJECT lock (holdHomeLock), the launch
// lock file of the project key that every jail of the workspace was keyed by before
// setup-qualified names (named-setups-plan.md SUP-D36). The launch holds it, beside its own jail's
// launch lock, until its jail is up and answers the probe (releaseHomeLock), so a launch of another
// setup the legacy home is attributed to cannot move it between this launch's decision and its
// bind (6a-ii review m2): without it, the first would recreate an empty .yolo/home and start on
// it. Once the home has moved this takes no lock, so only the launches that might move it wait.
func (o *Options) selectHome() {
	o.homeState = ""
	legacy := paths.LegacyHomeState(o.Workspace)
	if _, err := os.Lstat(legacy); err == nil && readsLegacyHome(o.Setup) {
		o.holdHomeLock()
		r := legacyHomeRequest{
			Workspace:          o.Workspace,
			Setup:              o.Setup,
			UID:                os.Geteuid(),
			Jails:              o.projectJails(),
			AppleContainerLaid: appleContainerLaidHome,
		}
		if o.Setup.Confinement == setup.ConfinementSeatbelt && o.Setup.RunAs != setup.RunAsSelf {
			if uid, err := legacyHomeAccountUID(); err == nil {
				account := int(uid)
				r.AccountUID = &account
			}
		}
		if _, line := adoptLegacyHome(r); line != "" {
			o.pr(o.Stderr).print(richtext.Escape(line))
		}
	}
	o.homeState = o.currentHome()
}

// homeDir is the host directory this launch binds as its home: the one selectHome chose, or, for a
// caller that resolved a setup without Run (`yolo config apply`'s preflight), the one a launch of
// that setup would bind now (currentHome). An Options with no setup at all, which only a unit test
// builds, names the legacy home.
func (o *Options) homeDir() string {
	switch {
	case o.homeState != "":
		return o.homeState
	case o.Setup.Name == "":
		return paths.LegacyHomeState(o.Workspace)
	}
	return o.currentHome()
}

// currentHome is the home a launch of the selected setup binds now, which is what the move left
// (SUP-D21): the legacy home while it is still in place as a real directory and attributed to this
// setup, the setup's own home otherwise. It moves nothing and creates nothing.
func (o *Options) currentHome() string {
	legacy := paths.LegacyHomeState(o.Workspace)
	if readsLegacyHome(o.Setup) {
		if fi, err := os.Lstat(legacy); err == nil && fi.IsDir() &&
			(o.Setup.Confinement == setup.ConfinementAppleContainer || !appleContainerLaidHome(legacy)) {
			return legacy
		}
	}
	return paths.SetupHomeState(o.Workspace, o.Setup.Name)
}

// homeRel is homeDir relative to <workspace>/.yolo ("home" or "setups/<setup>/home").
func (o *Options) homeRel() string {
	if rel, ok := paths.HomeStateRel(o.Workspace, o.homeDir()); ok {
		return rel
	}
	return "home"
}

// HomeFor is the home a launch of s in workspace binds now (currentHome), for a reader outside the
// launch that must look where the launch put its state: `yolo config`'s jail target. It moves
// nothing and creates nothing.
func HomeFor(workspace string, s setup.Setup) string {
	o := Options{Workspace: workspace, Setup: s}
	return o.currentHome()
}

// projectJails asks whether any jail of the workspace runs (jailRuns, for each of its names): the
// legacy name an older yolo keyed every setup by, and the jail name of every setup with state under
// .yolo/setups, the selected one's included (projectJailNames). Any one running is running; any one
// that could not be asked is unknown, which the move reads as running.
func (o *Options) projectJails() projectJails {
	answer := projectJailsStopped
	for _, cname := range o.projectJailNames() {
		switch j, _ := o.jailRuns(cname); j {
		case projectJailsRunning:
			return projectJailsRunning
		case projectJailsUnknown:
			answer = projectJailsUnknown
		}
	}
	return answer
}

// projectJailNames is every name a jail of the workspace can run under: the legacy name, the
// selected setup's, and that of every setup with a directory under .yolo/setups. A launch makes
// its setup's directory before its jail starts (ensureSetupState), so a jail this yolo started
// always has one; the legacy name covers every jail an older yolo started. A directory whose name
// is not a valid setup name is no setup's and names no jail.
func (o *Options) projectJailNames() []string {
	names := []string{o.legacyJailName()}
	seen := map[string]bool{names[0]: true}
	add := func(setupName string) {
		if !setup.ValidName(setupName) {
			return
		}
		if n := yoloruntime.JailName(o.Workspace, setupName); !seen[n] {
			seen[n] = true
			names = append(names, n)
		}
	}
	add(o.Setup.Name)
	for _, name := range paths.SetupStateNames(o.Workspace) {
		add(name)
	}
	return names
}

// jailRuns asks whether a jail runs under cname, three ways, because a jail can run with any one
// of them silent (6a-ii review M1):
//
//   - the keeper of its container, and of its macos-user and guest sessions (probeKeeper);
//   - a live session record under any of those keys, kept or not (liveKeyedSessions): a keeperless
//     macos-user session (JL-D42) holds no keeper lock, only its record;
//   - a container of the name in each container runtime on PATH (probeExistingContainer,
//     SUP-D10's probe): a jail whose keeper died, or one a yolo from before keepers started, has
//     neither of the others.
//
// how names what answered running (a runtime's name, "keeper" or "session"), for a refusal to name
// the stop. A runtime not on PATH runs no jail, so it is not asked.
func (o *Options) jailRuns(cname string) (answer projectJails, how string) {
	answer = projectJailsStopped
	for _, key := range []string{cname, keeperKey(cname, keeperNotchMacosUser), keeperKey(cname, keeperNotchGuest)} {
		switch probeKeeper(key) {
		case keeperAlive:
			return projectJailsRunning, "keeper"
		case keeperUnknown:
			answer = projectJailsUnknown
		}
		if len(liveKeyedSessions(key)) > 0 {
			return projectJailsRunning, "session"
		}
	}
	for _, rt := range projectContainerRuntimes {
		if _, ok := o.LookPath(rt); !ok {
			continue
		}
		switch id, known := o.probeExistingContainer(cname, rt, trackingProbeTimeout); {
		case id != "":
			return projectJailsRunning, rt
		case !known:
			answer = projectJailsUnknown
		}
	}
	return answer, ""
}

// JailRunState is jailRuns over the real seams, for a reader outside a launch: `yolo setup list`'s
// running column (named-setups-plan.md slice 6b). running is true when the jail named cname runs;
// known is false when no way of asking said running and one could not answer, so it may run.
func JailRunState(cname string) (running, known bool) {
	o := NewDefaultOptions()
	o.Exec = realExec
	o.LookPath = func(name string) (string, bool) {
		p, err := exec.LookPath(name)
		return p, err == nil
	}
	switch j, _ := o.jailRuns(cname); j {
	case projectJailsRunning:
		return true, true
	case projectJailsUnknown:
		return false, false
	}
	return false, true
}

// projectContainerRuntimes are the container runtimes a jail of a project can run in: podman's
// and Apple Container's `container`.
var projectContainerRuntimes = []string{"podman", "container"}

// appleContainerLaidHome is the Apple Container layout signal (SUP-D20) for home, one of a
// workspace's homes: a generated user-config copy of the shape an Apple Container launch leaves
// (isAppleContainerInheritedCopy) at any inherited destination, where that backend keeps it (home is
// HOME there, so a home-relative destination is its path beneath home). It reads beneath a root on
// home that refuses a link at `.yolo` and below (paths.OpenWorkspaceHomeRoot), so it reads nothing a
// jail pointed elsewhere.
func appleContainerLaidHome(home string) bool {
	r, err := paths.OpenWorkspaceHomeRoot(filepath.Dir(filepath.Dir(home)), home)
	if err != nil {
		return false
	}
	defer r.Close()
	uid := os.Geteuid()
	for _, scope := range []config.InheritScope{config.InheritPreflight, config.InheritNested} {
		for _, rel := range inheritScopeDestinations(scope) {
			if isAppleContainerInheritedCopyBeneath(r, filepath.FromSlash(rel), uid) {
				return true
			}
		}
	}
	return false
}
