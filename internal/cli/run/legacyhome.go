package run

// legacyhome.go moves a project's legacy home, <ws>/.yolo/home, to the home of the setup that
// owns it, <ws>/.yolo/setups/<name>/home (docs/design/named-setups.md §9, OQ-SU5;
// named-setups-plan.md SUP-D9, SUP-D20).
//
// The move is silent: no prompt, one informational line. It runs only once no jail of the
// project runs, and "could not ask" waits for a later launch, as every reaper here declines
// when it cannot ask. It moves only a real directory (lstat, never a link) owned by the invoking
// user, with a rename that never replaces a destination. Anything else stays where it is, with a
// line giving the `mv` to run. Nothing is ever deleted, and the move never refuses a launch.
//
// ⚠ NO PRODUCTION CALLER YET (SUP-D19). The call belongs after resolveSetup and before home
// preparation, and it lands with the switch that makes a launch bind SetupHomeState: wired any
// earlier, a launch would move .yolo/home away and then start on a fresh, empty .yolo/home. The
// test that fails if that call site is deleted lands with the switch.

import (
	"errors"
	"fmt"

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
	// is no signal.
	AppleContainerLaid func(home string) bool
}

type legacyHomeResult int

const (
	// legacyHomeAbsent: there is no legacy home. Nothing to say.
	legacyHomeAbsent legacyHomeResult = iota
	// legacyHomeNotOurs: the selected setup's technology never read .yolo/home (a host setup),
	// so the home is left for the setup that did, and this launch says nothing.
	legacyHomeNotOurs
	// legacyHomeDeferred: a jail of the project runs, or the runtime could not be asked; the
	// home moves at a later launch.
	legacyHomeDeferred
	// legacyHomeMoved: the home is now the setup's home.
	legacyHomeMoved
	// legacyHomeLeft: the home cannot be moved by yolo and stays, with the mv to run.
	legacyHomeLeft
)

func (r legacyHomeResult) String() string {
	return [...]string{"absent", "not ours", "deferred", "moved", "left"}[r]
}

// legacyHomeOwner is the setup the legacy home belongs to (SUP-D20), or "" when the selected
// setup's technology never read one. Every container and Seatbelt backend used <ws>/.yolo/home
// as its home (Apple Container and Seatbelt with one layout since slice 12a), so the selected
// setup owns it, except that the Apple Container layout signal gives it to Apple Container only.
// A host setup never read one.
func legacyHomeOwner(r legacyHomeRequest, home string) string {
	switch r.Setup.Confinement {
	case setup.ConfinementPodman, setup.ConfinementAppleContainer, setup.ConfinementSeatbelt:
		if r.Setup.Confinement != setup.ConfinementAppleContainer && r.AppleContainerLaid != nil && r.AppleContainerLaid(home) {
			return "apple-container-self"
		}
		return r.Setup.Name
	}
	return ""
}

// adoptLegacyHome moves the legacy home to its setup's home when SUP-D9 allows, and returns the
// one line the launch prints ("" when there is nothing to say).
func adoptLegacyHome(r legacyHomeRequest) (legacyHomeResult, string) {
	stateDir := paths.WorkspaceStateDir(r.Workspace)
	src := paths.WorkspaceHomeState(r.Workspace)

	yolo, err := unix.Open(stateDir, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		if errors.Is(err, unix.ENOENT) {
			return legacyHomeAbsent, ""
		}
		// A linked or unreadable .yolo: whatever is beneath it is not this move's to touch, and
		// the launch's own state writes refuse it with their own message.
		return legacyHomeAbsent, ""
	}
	defer unix.Close(yolo)

	var st unix.Stat_t
	if err := unix.Fstatat(yolo, "home", &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return legacyHomeAbsent, ""
	}

	owner := legacyHomeOwner(r, src)
	if owner == "" {
		return legacyHomeNotOurs, ""
	}
	dest := paths.SetupHomeState(r.Workspace, owner)
	mv := shquote.JoinDisplay([]string{"mv", src, dest})

	if r.Jails != projectJailsStopped {
		why := "a jail of this project is running"
		if r.Jails == projectJailsUnknown {
			why = "yolo could not ask the container runtime whether a jail of this project is running"
		}
		return legacyHomeDeferred, fmt.Sprintf("This project's home %s moves to %s, the %s setup's own home, "+
			"once no jail of the project runs; %s, so it stays for now. Run `yolo stop` and launch again to move it.",
			src, dest, owner, why)
	}

	left := func(why string) (legacyHomeResult, string) {
		return legacyHomeLeft, fmt.Sprintf("This project's home %s was left in place: %s. "+
			"To give it to the %s setup, run `%s` while no jail of the project runs.", src, why, owner, mv)
	}
	if owner != r.Setup.Name {
		return left(fmt.Sprintf("its layout is the %s setup's, not %s's", owner, r.Setup.Name))
	}
	switch {
	case st.Mode&unix.S_IFMT == unix.S_IFLNK:
		return left("it is a symbolic link, and yolo moves only a real directory")
	case st.Mode&unix.S_IFMT != unix.S_IFDIR:
		return left("it is not a directory")
	case int(st.Uid) != r.UID:
		return left(fmt.Sprintf("it belongs to uid %d, not to you (uid %d)", st.Uid, r.UID))
	}

	setupDir, err := paths.EnsureSetupStateDir(r.Workspace, owner)
	if err != nil {
		return left(fmt.Sprintf("its setup's directory could not be made (%v)", err))
	}
	defer setupDir.Close()

	switch err := renameNoReplaceBetween(yolo, "home", int(setupDir.Fd()), "home"); {
	case err == nil:
		return legacyHomeMoved, fmt.Sprintf("Moved this project's home %s to %s: each setup keeps its own home now.", src, dest)
	case errors.Is(err, unix.ENOENT):
		return legacyHomeAbsent, ""
	case errors.Is(err, unix.EEXIST), errors.Is(err, unix.ENOTEMPTY):
		return left(fmt.Sprintf("%s already exists; move that aside first", dest))
	default:
		return left(fmt.Sprintf("the move failed (%v)", err))
	}
}
