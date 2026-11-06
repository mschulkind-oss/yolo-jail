package run

// legacyhome.go moves a project's legacy home, <ws>/.yolo/home, to the home of the setup that
// owns it, <ws>/.yolo/setups/<name>/home (docs/design/named-setups.md §9, OQ-SU5;
// named-setups-plan.md SUP-D9, SUP-D20, SUP-D21).
//
// The move is silent: no prompt, one informational line. It moves only a real directory (lstat,
// never a link) owned by the invoking user and laid by the selected setup's technology, and only
// once no jail of the project runs; "could not ask" waits for a later launch, as every reaper
// here declines when it cannot ask. The rename never replaces anything but an empty directory.
// Anything else stays where it is, with a line naming why and the one step that clears it.
// Nothing is ever deleted, and the move never refuses a launch.
//
// SUP-D21: until the legacy home has moved, a launch of the setup it belongs to keeps using
// .yolo/home and creates no .yolo/setups/<name>/home, so a deferred or left home is never
// stranded behind a fresh one, and a printed manual move never lands inside one.
//
// ⚠ NO PRODUCTION CALLER YET (SUP-D19). The call belongs after resolveSetup and before home
// preparation, and it lands with the switch that makes a launch bind SetupHomeState: wired any
// earlier, a launch would move .yolo/home away and then start on a fresh, empty .yolo/home. The
// test that fails if that call site is deleted lands with the switch.

import (
	"errors"
	"fmt"
	"io/fs"
	"os"

	"golang.org/x/sys/unix"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
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
	// UID is the invoking user: the only owner whose home the move takes.
	UID int
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
)

func (r legacyHomeResult) String() string {
	return [...]string{"absent", "not ours", "deferred", "moved", "left"}[r]
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
	src := paths.WorkspaceHomeState(r.Workspace)
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
	case int(st.Uid) != r.UID:
		return left("This project's home %s belongs to uid %d, not to you (uid %d), so it was "+
			"left in place. Give it to you and launch again with no jail of the project "+
			"running: `%s`", src, st.Uid, r.UID,
			shquote.JoinDisplay([]string{"sudo", "chown", "-R", fmt.Sprint(r.UID), src}))
	}
	// Minor-5 of the 6a-i review: the owner is named as "an Apple Container setup", since a user's
	// own Apple Container setup may carry any name; the example names the built-in.
	if r.Setup.Confinement != setup.ConfinementAppleContainer && r.AppleContainerLaid != nil && r.AppleContainerLaid(src) {
		return left("This project's home %s was laid by an Apple Container setup, so the %s setup "+
			"starts with its own home and leaves that one in place. To move it to its owner, "+
			"launch an Apple Container setup with no jail of the project running, for example "+
			"`yolo run --setup apple-container-self`", src, r.Setup.Name)
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
