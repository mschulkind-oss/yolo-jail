package run

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// THE EMPTY PODMAN PLACEHOLDER (issue #66). A podman launch that binds a single file `:ro` into
// the jail's home makes an empty file at the bind's target in the workspace's sidecar
// (<workspace>/.yolo/home), and that file stays after the jail exits: 0 bytes, mode 0700. The
// inherited user scope was delivered that way, so a workspace that ever ran under podman can hold
// one at ~/.config/yolo-jail/config.jsonc (or config.toml). A later macos-user launch delivers its
// user scope at that same path, and the overwrite rule refused the file because the home-overlay
// installer cannot prove it made it.
//
// The rule stays: a file yolo cannot prove it delivered is never overwritten. But an empty,
// unshared regular file owned by the launching user holds no data to lose, so the launch moves it
// aside itself, to <name>.podman-placeholder, and says so. Anything else at the path keeps the
// refusal, which names the `mv` that clears it.
const podmanPlaceholderSuffix = ".podman-placeholder"

// placeholderBeforeRename is nil in production; a test sets it to change the file between the
// check and the rename, the race movePlaceholderAside's after-the-fact check answers.
var placeholderBeforeRename func(path string)

// podmanPlaceholderNames bounds the free-name search: <name>.podman-placeholder, then -2 .. -9.
const podmanPlaceholderNames = 9

// isEmptyPlaceholder reports whether info is a regular file with no bytes, no other hard link,
// owned by uid: a file whose move cannot take data from anyone.
func isEmptyPlaceholder(info os.FileInfo, uid int) bool {
	if info == nil || !info.Mode().IsRegular() || info.Size() != 0 {
		return false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Nlink == 1 && inheritedFileOwnedBy(info, uid)
}

// clearPodmanPlaceholders moves aside every empty placeholder that occupies the inherited
// preflight scope's destination family in wsState, printing one line for each. A dry run moves
// nothing: it says what a launch will move and returns those destinations (storage-relative) so
// the plan selects as though they were gone. A placeholder the launch cannot move is named and
// left in place, where the overwrite refusal then names the command that clears it.
func (o *Options) clearPodmanPlaceholders(wsState string) map[string]bool {
	if _, err := os.Lstat(wsState); err != nil {
		return nil
	}
	out := o.pr(o.Stderr)
	uid := os.Geteuid()
	legacy, toml := inheritScopeFamily(config.InheritPreflight)
	var pending map[string]bool
	for _, rel := range []string{legacy, strings.TrimSuffix(legacy, ".jsonc") + ".json", toml} {
		storageRel := filepath.FromSlash(strings.TrimPrefix(rel, "."))
		info, exists, err := lstatHomeFile(wsState, storageRel)
		if err != nil || !exists || !isEmptyPlaceholder(info, uid) {
			continue // the selector meets the same object and refuses or keeps it as before
		}
		path := filepath.Join(wsState, storageRel)
		if o.DryRun {
			if pending == nil {
				pending = map[string]bool{}
			}
			pending[storageRel] = true
			out.print("A launch will move the empty file at " + path + " aside to " + path + podmanPlaceholderSuffix +
				": it holds no data (a podman launch's single-file bind leaves one) and stands where the jail's user config goes.")
			continue
		}
		dest, err := movePlaceholderAside(wsState, storageRel, uid)
		if err != nil {
			out.print("[yellow]Warning: the empty file at " + path + " was not moved aside: " + err.Error() + "[/yellow]")
			continue
		}
		out.print("Moved the empty file at " + path + " aside to " + dest +
			": it held no data (a podman launch's single-file bind leaves one) and stood where the jail's user config goes.")
	}
	return pending
}

// movePlaceholderAside renames the empty placeholder at storageRel beneath wsState to the first
// free <name>.podman-placeholder[-N], returning its new path.
//
// BENEATH THE SIDECAR'S ROOT, never by path: every directory is opened refusing a link, as
// priorInheritedDelivery opens the staging tree, because the sandbox writes the sidecar. The
// rename never replaces (renameNoReplaceAt), so an earlier placeholder or anything else at a
// candidate name keeps its place. And it is CHECKED AFTER THE FACT against the descriptor opened
// before it: a file written to, linked or swapped in between the check and the rename is renamed
// back, so the only file this ever leaves moved is the empty one it proved.
func movePlaceholderAside(wsState, storageRel string, uid int) (string, error) {
	root, err := paths.OpenStateDirRoot(wsState)
	if err != nil {
		return "", err
	}
	defer root.Close()
	parts := strings.Split(filepath.Clean(storageRel), string(filepath.Separator))
	parent := root
	for i, part := range parts[:len(parts)-1] {
		next, err := paths.OpenStateSubdirRoot(parent, part, filepath.Join(wsState, filepath.Join(parts[:i+1]...)))
		if err != nil {
			return "", err
		}
		defer next.Close()
		parent = next
	}
	dir := filepath.Join(wsState, filepath.Join(parts[:len(parts)-1]...))
	name := parts[len(parts)-1]
	checked, err := parent.Lstat(name)
	if err != nil {
		return "", err
	}
	if !isEmptyPlaceholder(checked, uid) {
		return "", errors.New("it is no longer an empty, unshared file of yours")
	}
	held, err := parent.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return "", err
	}
	defer held.Close()
	if info, err := held.Stat(); err != nil || !os.SameFile(checked, info) || !isEmptyPlaceholder(info, uid) {
		return "", errors.New("it changed while it was being checked")
	}
	dirFile, err := parent.Open(".")
	if err != nil {
		return "", err
	}
	defer dirFile.Close()
	dirfd := int(dirFile.Fd())
	if placeholderBeforeRename != nil {
		placeholderBeforeRename(filepath.Join(dir, name))
	}
	moved := ""
	for i := 1; i <= podmanPlaceholderNames && moved == ""; i++ {
		candidate := name + podmanPlaceholderSuffix
		if i > 1 {
			candidate += "-" + strconv.Itoa(i)
		}
		switch err := renameNoReplaceAt(dirfd, name, candidate); {
		case err == nil:
			moved = candidate
		case errors.Is(err, syscall.EEXIST):
			continue
		default:
			return "", err
		}
	}
	if moved == "" {
		base := filepath.Join(dir, name+podmanPlaceholderSuffix)
		return "", fmt.Errorf("%s and every name up to %s-%d are taken", base, base, podmanPlaceholderNames)
	}
	after, err := parent.Lstat(moved)
	heldNow, heldErr := held.Stat()
	if err == nil && heldErr == nil && os.SameFile(after, heldNow) && isEmptyPlaceholder(heldNow, uid) {
		return filepath.Join(dir, moved), nil
	}
	// Not the file that was proved, or no longer empty: put it back, never over anything.
	if backErr := renameNoReplaceAt(dirfd, moved, name); backErr != nil {
		return "", fmt.Errorf("it changed while being moved, and it now sits at %s (moving it back failed: %v); review it and move it where it belongs", filepath.Join(dir, moved), backErr)
	}
	return "", errors.New("it changed while being moved, so it was put back")
}
