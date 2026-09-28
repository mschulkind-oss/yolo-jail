package packload

// processtree.go is the PROCESS PACK TREE (a term coined here, 2026-09-28): one leased
// directory per process that holds the configured packs a verb STAGED for its own lifetime,
// the host notch's counterpart of the tree a launch stages for its jail.
//
// It exists because the host notch used to read a configured pack's files IN PLACE — the
// user's directory, the pack store's checkout — while every launch reads the copy
// packstage.Stage left. So an entry's `exclude` removed a skill from every jail and still
// delivered it to the real home, and a local pack's escaping symlink was refused by the launch
// and rendered by `yolo host apply` (docs/plans/notch-convergence.md rows B3 and B6). Staging at
// the host needs somewhere a Pack.Root can keep naming for as long as the verb reads it, with no
// owner to hand a cleanup func to — exactly the embedded tree's problem, so it takes the
// embedded tree's answer. Only a FILTERED entry is staged here (config.ResolvePackForProcess): an
// unfiltered one is read in place, since its copy would hold the same files and would die with a
// process that a host-scope daemon spawned from it outlives.
//
//   - It is released with the embedded packs (ReleaseEmbedded), at the exits that already
//     release those — cli.Main's defer, `yolo host`'s exec, every ttyproxy signal arm — so
//     nothing new has to remember to call anything.
//   - It carries a lease (embeddedlease.go) and the embedded FALLBACK tree's name prefix, so a
//     tree whose owner was SIGKILLed is reaped by the next fallback's sweep or by `yolo prune`,
//     under the same liveness rule, without either learning a second name. The prefix names
//     "a per-process leased pack tree", which the fallback tree also is.
//   - A go-test binary under cmd/go makes it inside $WORK, as it makes its embedded tree, so
//     the unit suite neither leaks into TMPDIR nor depends on a test's TMPDIR outliving it.
//
// Nothing is created until the first caller asks, so `yolo --version` still writes nothing.

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

var (
	// processTreeRoot is this process's tree, "" before the first ProcessPackDir call or after a
	// release. Guarded by embeddedMu, because ReleaseEmbedded releases both.
	processTreeRoot  string
	processTreeLease *os.File
	// processTreeSeq numbers the directories handed out, so two stagings of one pack in one
	// process never share a directory: a caller may hold the first Pack while staging the second.
	processTreeSeq int
)

// ProcessPackDir returns a NEW, EMPTY directory whose base name is name, inside this process's
// pack tree, for a caller that stages a pack and must keep reading the staged copy until the
// process exits. Every call returns a different directory. The base name is the caller's so
// that packload.Pack.StagedSlug, which is the staged directory's base name, reads the same at
// the host as in a launch's tree.
//
// The tree is deleted by ReleaseEmbedded; a Pack loaded from a directory handed out here must
// not be read after it.
func ProcessPackDir(name string) (string, error) {
	embeddedMu.Lock()
	defer embeddedMu.Unlock()
	if processTreeRoot != "" {
		if _, err := os.Stat(processTreeRoot); err != nil {
			// Gone underneath us (a test's TMPDIR cleaned up, a manual rm): start a new tree
			// rather than hand out a directory under a root nothing leases.
			releaseProcessTreeLocked()
		}
	}
	if processTreeRoot == "" {
		if err := createProcessTreeLocked(); err != nil {
			return "", err
		}
	}
	processTreeSeq++
	dir := filepath.Join(processTreeRoot, strconv.Itoa(processTreeSeq), name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

// ProcessTreeLocation reports this process's pack tree, "" before the first ProcessPackDir call
// or after a release. For the pins that the tree is released and leased.
func ProcessTreeLocation() string {
	embeddedMu.Lock()
	defer embeddedMu.Unlock()
	return processTreeRoot
}

// createProcessTreeLocked makes and leases the tree. Caller holds embeddedMu.
func createProcessTreeLocked() error {
	parent := os.TempDir()
	if dir, isTest := testBinaryCacheDir(); isTest && dir != "" {
		// Beside the embedded base inside cmd/go's $WORK: that directory's parent is the
		// test binary's own b<N> dir, which exists for as long as the binary runs.
		parent = filepath.Dir(dir)
	} else {
		sweepDeadFallbacks(parent, time.Now())
	}
	dir, err := os.MkdirTemp(parent, EmbeddedFallbackPrefix)
	if err != nil {
		return err
	}
	// Resolved where it is minted (AGENTS.md's darwin PATH-RESOLUTION class): on macOS TMPDIR
	// is under /var, a symlink to /private/var, and a Pack.Root compared against a resolved
	// path must already be resolved.
	if resolved, rerr := filepath.EvalSymlinks(dir); rerr == nil {
		dir = resolved
	}
	lease, err := createLease(filepath.Join(dir, EmbeddedLeaseName))
	if err != nil {
		_ = os.RemoveAll(dir)
		return errors.Join(errors.New("process pack tree: lease"), err)
	}
	processTreeRoot, processTreeLease, processTreeSeq = dir, lease, 0
	return nil
}

// releaseProcessTreeLocked deletes the tree and closes its lease. Caller holds embeddedMu.
func releaseProcessTreeLocked() {
	if processTreeRoot != "" {
		_ = os.RemoveAll(processTreeRoot)
	}
	if processTreeLease != nil {
		_ = processTreeLease.Close()
	}
	processTreeRoot, processTreeLease, processTreeSeq = "", nil, 0
}
