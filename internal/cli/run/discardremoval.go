package run

// discardremoval.go deletes a gone jail's per-launch directories AFTER the teardown that the
// user's prompt waits on has finished, the way scratchremoval.go deletes its scratch volumes.
//
// THE BUG IT FIXES. `shutdown.clear_tracking` took 19.4 s at a quit on the maintainer's host
// (2026-10-11, load average 93, ten other jails), where it had taken 0.04-0.13 s for a week. The
// span is forgetGone, and nothing in it waits but one bounded `podman ps` (10 s) and three
// os.RemoveAll calls, run inline: the home skeleton, the pack tree, and the tree's per-launch
// copies of its patched extensions (patchedtrees.go). The copies are what moved the number: a
// launch with eight patched pi extensions copies about 6,350 files (58 MB) per launch, and
// deleting them one unlinkat at a time on a disk ten jails share is what the session's
// `session.keeper_teardown` then waited out.
//
// THE SHAPE. On the same known-gone evidence as before, forgetGone RENAMES each directory into
// AGENTS_DIR/<cname>/.discard/<batch> (paths.DiscardRoot): one rename per directory within one
// filesystem, whatever the tree holds, so the path a reader or a later launch could find is gone
// at once, exactly as the inline delete left it. It then starts ONE detached process, `yolo
// internal discard-rm -- <cname>`, in its own session with stdio on /dev/null, which empties the
// whole discard root at low priority and exits; nobody waits on it. Whatever it never reaches (a
// remover killed, a host rebooted) is emptied by the next teardown's remover, or goes with the
// whole AGENTS_DIR/<cname> through prune.PruneOrphanAgentStaging.
//
// EVERY FAILURE FALLS BACK TO THE INLINE DELETE: a directory that does not rename is removed where
// it is, and a batch whose remover does not start is removed here, so a failure costs only the
// time the teardown always paid, never a leaked tree.
//
// The remover only ever REMOVES: it creates nothing, not even the discard root, so a caller
// deleting the whole state directory under it (the test suite's t.TempDir cleanup) races only
// another removal.

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// DiscardRemoverVerb is the hidden `yolo internal` verb the discard remover runs as.
const DiscardRemoverVerb = "discard-rm"

// discardRemoverArgv is the remover's command line, "yolo" substituted by the caller.
func discardRemoverArgv(cname string) []string {
	return []string{"yolo", "internal", DiscardRemoverVerb, "--", cname}
}

// discardGoneTrees disposes of the per-launch directories of cname's container, which the caller
// has established is known gone and holds the workspace lock for (forgetGone): the skeleton this
// launch built and the pack tree it handed the container, with the tree's live-tree record, its
// delivery record and its per-launch copies. Each argument is "" for none, and each is taken only
// under the path guard its inline removal had (discardUnheldSkeleton, discardPackTree): a direct
// child of this cname's root, never anything else.
//
// The records are removed here, inline, because they are single files a reader consults
// (runningJailPackTree, HandedForkKeys); the directories are moved out of the way and handed to the
// detached remover (the file header).
func (o *Options) discardGoneTrees(cname, skeleton, tree string) {
	var dirs []string
	if skeleton != "" && filepath.Dir(skeleton) == paths.HomeSkeletonRoot(cname) {
		dirs = append(dirs, skeleton)
	}
	if tree != "" {
		forgetLivePackTree(cname, tree)
		if filepath.Dir(tree) == paths.PackTreeRoot(cname) {
			_ = os.Remove(handedForksPath(tree))
			dirs = append(dirs, tree)
			if copies := patchedCopiesDir(tree); copies != "" {
				dirs = append(dirs, copies)
			}
		}
	}
	batch, left := moveToDiscard(cname, dirs)
	for _, d := range left {
		_ = os.RemoveAll(d)
	}
	if batch == "" {
		return
	}
	argv, err := o.selfExecArgv(discardRemoverArgv(cname))
	if err == nil {
		err = o.StartDetached(argv, nil)
	} else {
		_ = o.reportPinnedSpawnRefused("the discard remover", err)
	}
	if err != nil {
		o.Perf.Mark("shutdown.clear_tracking.discard_rm_not_started")
		_ = os.RemoveAll(batch)
		return
	}
	o.Perf.Mark("shutdown.clear_tracking.discard_rm_started")
}

// moveToDiscard renames each existing directory of dirs into a new batch directory under cname's
// discard root, and returns the batch ("" when nothing moved) and the directories that exist but
// did not move, which the caller removes in place. A directory that is not there is neither.
func moveToDiscard(cname string, dirs []string) (batch string, left []string) {
	var present []string
	for _, d := range dirs {
		if _, err := os.Lstat(d); err == nil {
			present = append(present, d)
		}
	}
	if len(present) == 0 {
		return "", nil
	}
	root := paths.DiscardRoot(cname)
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", present
	}
	batch, err := os.MkdirTemp(root, "gone-")
	if err != nil {
		return "", present
	}
	moved := 0
	for i, d := range present {
		if err := os.Rename(d, filepath.Join(batch, fmt.Sprintf("%d-%s", i, filepath.Base(d)))); err != nil {
			left = append(left, d)
			continue
		}
		moved++
	}
	if moved == 0 {
		_ = os.Remove(batch)
		return "", left
	}
	return batch, left
}

// DiscardRemoverMain is `yolo internal discard-rm -- <cname>`: the detached remover. It empties
// paths.DiscardRoot(cname), every batch in it and not only the one its teardown moved there, and
// then removes the root when nothing new has arrived. It has no terminal, so it reports through
// its exit status alone: 0 when the root is empty or gone, 1 when something stayed.
func DiscardRemoverMain(args []string) int {
	if len(args) != 2 || args[0] != "--" || !validDiscardCname(args[1]) {
		fmt.Fprintf(os.Stderr, "usage: yolo internal %s -- <container-name>\n", DiscardRemoverVerb)
		return 2
	}
	// Below the user's interactive work: bulk metadata IO nobody waits for. Best-effort.
	_ = syscall.Setpriority(syscall.PRIO_PROCESS, 0, 10)
	if !emptyDiscardRoot(args[1]) {
		return 1
	}
	return 0
}

// validDiscardCname reports whether cname is one path element, so the root it names is a direct
// child of AGENTS_DIR's entry for it and never anything above.
func validDiscardCname(cname string) bool {
	return cname != "" && cname != "." && cname != ".." && filepath.Base(cname) == cname &&
		filepath.Clean(cname) == cname
}

// emptyDiscardRoot removes everything under cname's discard root, then the root itself when it is
// empty, and reports whether nothing is left. It creates nothing.
func emptyDiscardRoot(cname string) bool {
	root := paths.DiscardRoot(cname)
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return true
	}
	if err != nil {
		return false
	}
	ok := true
	for _, e := range entries {
		if err := os.RemoveAll(filepath.Join(root, e.Name())); err != nil {
			ok = false
		}
	}
	// Fails, harmlessly, when a teardown has moved a new batch in since the listing: that batch is
	// its own remover's.
	_ = os.Remove(root)
	return ok
}
