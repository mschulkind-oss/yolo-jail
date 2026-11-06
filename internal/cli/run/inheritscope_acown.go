package run

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/configdoc"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// APPLE CONTAINER'S OWN PREVIOUS COPY (the Mac run of 2026-11-06, §2). Apple Container cannot
// bind a single file, so a launch COPIES the generated user scope into the jail's home
// (userConfigMountArgsForHome), beside the staged file it copied it from, and that copy is still
// there at the next launch. The overwrite refusal is for a file the USER put at the destination;
// it refused the previous launch's copy too, so no workspace with an inherited user scope could
// relaunch on this backend.
//
// The rule stays: a file yolo cannot show is its own is never overwritten. As with the empty
// podman placeholder (clearPodmanPlaceholders), the test is that replacing the file can take no
// data from anyone: a regular file of the launching user's with no other hard link, whose bytes
// are exactly the staged file a previous launch copied there (inheritStageName, read beneath the
// home and never through a link). An edit, a hard link, another owner or a missing stage keeps
// the refusal, which names the `mv` that clears it.

// inheritStageName is the name, directly in the jail's home state, of the staged copy of the
// scope file a launch delivers at rel.
func inheritStageName(scope config.InheritScope, rel string) string {
	return "inherit-" + scope.String() + filepath.Ext(rel)
}

// appleContainerOwnCopies returns the storage-relative destinations of scope that hold a previous
// Apple Container launch's unchanged copy. A destination it cannot judge is left out, for the
// refusal to meet and name.
func (o *Options) appleContainerOwnCopies(wsState string, mapDestination func(string) (string, error), scope config.InheritScope) (map[string]bool, error) {
	own := map[string]bool{}
	for _, rel := range inheritScopeDestinations(scope) {
		storageRel, err := mapDestination(rel)
		if err != nil {
			return nil, err
		}
		if isAppleContainerOwnCopy(wsState, storageRel, inheritStageName(scope, rel)) {
			own[storageRel] = true
		}
	}
	return own, nil
}

// clearAppleContainerOwnCopies removes each own copy (appleContainerOwnCopies) that is not at the
// destination this launch delivers format to — a copy of the other format, which would otherwise
// stand beside the new one — and leaves the one at that destination for the delivery to replace.
// A dry run removes nothing and says what a launch will remove.
func (o *Options) clearAppleContainerOwnCopies(wsState string, mapDestination func(string) (string, error), scope config.InheritScope, format configdoc.Format, own map[string]bool) error {
	selected, err := mapDestination(InheritedScopeRel(scope, format))
	if err != nil {
		return err
	}
	out := o.pr(o.Stderr)
	for _, rel := range inheritScopeDestinations(scope) {
		storageRel, err := mapDestination(rel)
		if err != nil {
			return err
		}
		if !own[storageRel] || storageRel == selected {
			continue
		}
		path := filepath.Join(wsState, storageRel)
		if o.DryRun {
			out.print("A launch will remove " + path + ": it is an earlier launch's copy of the jail's user config, unchanged, " +
				"and this launch delivers it at ~/" + InheritedScopeRel(scope, format) + " instead.")
			continue
		}
		if err := removeAppleContainerOwnCopy(wsState, storageRel, inheritStageName(scope, rel)); err != nil {
			mv := shquote.JoinDisplay([]string{"mv", path, path + ".moved-aside"})
			return fmt.Errorf("refusing Apple Container inherited %s scope: the earlier launch's copy of the user config at %s "+
				"could not be removed (%v), and it would stand beside this launch's ~/%s. Move it aside (%s) and relaunch",
				scope, path, err, InheritedScopeRel(scope, format), mv)
		}
		out.print("Removed " + path + ": it was an earlier launch's copy of the jail's user config, unchanged, " +
			"and this launch delivers it at ~/" + InheritedScopeRel(scope, format) + " instead.")
	}
	return nil
}

// removeAppleContainerOwnCopy removes storageRel beneath wsState after proving it again.
func removeAppleContainerOwnCopy(wsState, storageRel, stageName string) error {
	if !isAppleContainerOwnCopy(wsState, storageRel, stageName) {
		return fmt.Errorf("it changed while it was being checked")
	}
	root, err := openStateRoot(wsState)
	if err != nil {
		return err
	}
	defer root.Close()
	return root.Remove(storageRel)
}

// isAppleContainerOwnCopy reports whether the destination storageRel beneath wsState is a regular,
// unshared file of this user's whose bytes are exactly the staged file stageName's. Nothing is
// followed: lstatHomeFile refuses a link at any component, and each open is O_NOFOLLOW and checked
// against what was judged.
func isAppleContainerOwnCopy(wsState, storageRel, stageName string) bool {
	info, exists, err := lstatHomeFile(wsState, storageRel)
	if err != nil || !exists || !info.Mode().IsRegular() || !inheritedFileOwnedBy(info, os.Geteuid()) {
		return false
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); !ok || stat.Nlink != 1 {
		return false
	}
	root, err := openStateRoot(wsState)
	if err != nil {
		return false
	}
	defer root.Close()
	dest, ok := readRegularNoFollow(root, storageRel, info)
	if !ok || len(dest) == 0 {
		return false
	}
	stage, ok := readRegularNoFollow(root, stageName, nil)
	return ok && bytes.Equal(dest, stage)
}

// readRegularNoFollow reads the regular file rel beneath root without following a link at it;
// when want is set, the file opened must be that one.
func readRegularNoFollow(root *os.Root, rel string, want os.FileInfo) ([]byte, bool) {
	f, err := root.OpenFile(rel, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || (want != nil && !os.SameFile(want, info)) {
		return nil, false
	}
	body, err := io.ReadAll(f)
	return body, err == nil
}
