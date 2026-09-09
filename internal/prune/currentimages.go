package prune

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/image"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// currentimages.go is image retention after OQ-LS3
// (docs/design/the-load-sentinel-is-not-a-liveness-oracle.md §6.2, ruled
// 2026-09-08 and sharpened 2026-09-09): ONE POINTER PER WORKSPACE at the image
// that workspace last launched against, and nothing else.
//
// # WHY THIS REPLACES A COUNT RATHER THAN RETUNING ONE
//
// `--keep-images N` sorted every image row by CreatedAt and kept the newest N,
// with no notion of a workspace or a configuration — so on a machine with four
// workspaces two images were evicted per pass however recently each had been
// used, and CreatedAt is when the ARCHIVE WAS STREAMED, so the longest-running
// jail sorted oldest and went first. The ruling: **the unit is the
// CONFIGURATION, and there is no undo buffer** — *"I don't know that I've ever
// rolled back, only evolved forward."* Keep each configuration's current image;
// never hold a superseded copy to go back to. That leaves a global count with no
// depth to bound, which is why the flag went away instead of moving to a
// different number.
//
// # THE POINTER IS NOT A CONFIG IDENTITY, and confusing the two cost a day
//
// Grouping images BY configuration looks like it needs a value that is equal
// across every image of one config, and nothing in this tree records one:
// image.ImageStoreKey is per IMAGE, and the flake's `imageIdentity` is
// deliberately invariant across `packages:` lists. Neither can group. That dead
// end is real and it is the wrong problem — **a grouping key is only needed when
// a group has more than one member.** With zero superseded copies, "each
// config's current image" is a SET OF POINTERS, one per workspace, and a
// workspace's current image is something the launcher already knows: it is the
// store path it just wrote to the load sentinel.
//
// # WHY THE POINTERS LIVE UNDER BuildDir() AND NOT IN THE WORKSPACE
//
// §6.2 names <workspace>/.yolo as the pointer's natural home, and for a single
// pointer read by its own workspace it would be. THE REAPER NEEDS THE UNION,
// and it has no way to enumerate workspaces that is not itself a registry: the
// only two candidates are the runtime's container list (which `yolo prune`'s own
// stale-container sweep removes rows from, in the same run, minutes before this
// section) and the per-container tracking files (which runtime
// .PruneStaleTrackingFiles deletes for every container that is not RUNNING). A
// retention rule that forgets a workspace the moment its jail is not up is the
// defect this replaces, one mechanism over.
//
// So the pointers are host state keyed by the deterministic container name, one
// small file per workspace — exactly the shape paths.ApprovalsDir already argues
// for ("that is already this repo's answer to one small piece of host state per
// workspace"), and a sibling of the load sentinel and the GC roots under
// BuildDir() because it is read on the same pass they are. The WORKSPACE PATH is
// recorded INSIDE the file, which is what makes a pointer verifiable rather than
// merely present: see CurrentImageTags.
//
// # ONE SPELLING, IN THE PACKAGE THAT ACTS ON IT
//
// The writer is the launch path (internal/cli/run/currentimage.go) and the
// reader is this package's reaper. Both go through the functions here rather
// than joining a path themselves, because a divergence between the two would be
// silent in exactly one direction: the reaper would find no pointers, decline
// (fail-safe), and stop reclaiming anything — the shape of defect that sat true
// for 24 days and 404 GiB before OQ-DF3.

// currentImagesDirName is BuildDir()'s directory of per-workspace current-image
// pointers. Deliberately a directory of its own rather than more lines in a
// shared file: the unit is one workspace, and a per-workspace file is what makes
// a launch's write and a sweep's read independent of each other with no locking
// protocol of their own (last-writer-wins on one file per workspace, and no
// workspace can lose another's entry — which is precisely what
// image.AddLoadedPath's shared, unlocked sentinel cannot promise).
const currentImagesDirName = "current-images"

// CurrentImagesDir is that directory under an explicit BuildDir.
func CurrentImagesDir(buildDir string) string {
	return filepath.Join(buildDir, currentImagesDirName)
}

// CurrentImagePointer is one workspace's recorded current image.
type CurrentImagePointer struct {
	// Container is the deterministic container name the pointer is keyed by
	// (runtime.FromWorkspace's value) — the file's own name.
	Container string
	// Workspace is the resolved workspace path the pointer was written for. It is
	// recorded so a pointer can be CHECKED rather than merely counted; see
	// CurrentImageTags for what a vanished workspace means.
	Workspace string
	// StorePath is the nix store path of the image that launch ran.
	StorePath string
}

// RecordCurrentImage writes one workspace's pointer, replacing whatever it held.
//
// REPLACING IS THE WHOLE MECHANISM. There is no history here and no generation
// count: the previous value is exactly the superseded copy the ruling says not to
// keep, so the write that supersedes an image is also the write that stops
// protecting it.
//
// Content is two lines — the store path, then the resolved workspace — and both
// are required by the reader. A truncated or half-written file therefore reads as
// "no pointer for this workspace" rather than as a pointer at a path nobody
// asked for; os.WriteFile truncates before it writes, so that is a state a full
// disk can produce.
func RecordCurrentImage(buildDir, container, workspace, storePath string) error {
	if container == "" || workspace == "" || storePath == "" {
		// Not an error the caller can act on and not a state to record: a pointer
		// missing any of its three parts cannot be read back (see
		// ReadCurrentImagePointers), so writing one would only occupy the name a
		// real pointer needs.
		return nil
	}
	dir := CurrentImagesDir(buildDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, container),
		[]byte(storePath+"\n"+workspace+"\n"), 0o644)
}

// ReadCurrentImagePointers reads every workspace's pointer.
//
// dirKnown reports whether the DIRECTORY could be read at all, which is the only
// tri-state this level owes its caller: a single unreadable or malformed pointer
// is one workspace's evidence missing and shrinks the protected set, while an
// absent directory means no launch has ever recorded one and there is no
// evidence of any kind (CurrentImageTags is where that becomes a decline).
func ReadCurrentImagePointers(buildDir string) (ptrs []CurrentImagePointer, dirKnown bool) {
	entries, err := os.ReadDir(CurrentImagesDir(buildDir))
	if err != nil {
		return nil, false
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		data, rerr := os.ReadFile(filepath.Join(CurrentImagesDir(buildDir), e.Name()))
		if rerr != nil {
			continue
		}
		var lines []string
		for _, line := range strings.Split(string(data), "\n") {
			if s := strings.TrimSpace(line); s != "" {
				lines = append(lines, s)
			}
		}
		if len(lines) < 2 {
			continue // truncated / half-written: not a pointer
		}
		ptrs = append(ptrs, CurrentImagePointer{
			Container: e.Name(), Workspace: lines[1], StorePath: lines[0],
		})
	}
	return ptrs, true
}

// CurrentImageTags is the retention set PruneOldImages vetoes with: the content
// tag of every live workspace's current image (image.ImageStoreKey — the same 16
// hex chars JailImageRef puts after the colon), plus the legacy `latest` tag.
//
// TAGS RATHER THAN REFS, for the reason the veto it replaces already gave: the
// runtimes spell the repository differently (podman's rows carry the localhost/
// prefix, Apple Container's do not) while the query has already narrowed every
// row to the jail repository, so comparing the half that is stable is what keeps
// a prefix mismatch from silently reading as "not protected".
//
// THE LEGACY TAG IS UNCONDITIONAL, and it does not make the set known. It exists
// for the one branch that has no store path to hash: AutoLoadImage's degraded
// fallback (SkipBuild, or a build failure the operator opted past) can only ask
// whether *an* image is present under the name the flake bakes, and it records
// no pointer. Protecting `latest` keeps an offline launch that worked yesterday
// working today.
//
// A POINTER WHOSE WORKSPACE IS GONE PROTECTS NOTHING, deliberately. The ruling's
// unit is a configuration, and a deleted workspace has none — so its image is
// superseded in the only sense that matters. The pointer FILE is left in place
// rather than reaped: it is ~100 bytes, a workspace can be temporarily absent (a
// detached volume, an unmounted share), and an image that gets removed under a
// returning workspace costs one re-stream from a store closure that OQ-LS1's
// GC-root policy still holds for a week. Deleting the file would cost the same
// re-stream and buy nothing back.
//
// known=false means NO pointer was honoured — no directory, an empty one, or one
// holding nothing but pointers to workspaces that no longer exist — and the
// caller must then reap nothing. Same tri-state polarity as every other reaper in
// this package: unknown is not permission (P3). It is also what makes the day
// this ships safe by construction, since the directory does not exist yet.
func CurrentImageTags(buildDir string) (tags map[string]struct{}, known bool) {
	tags = map[string]struct{}{tagOf(paths.JailImage): {}}
	ptrs, dirKnown := ReadCurrentImagePointers(buildDir)
	if !dirKnown {
		return tags, false
	}
	for _, p := range ptrs {
		if _, err := os.Stat(p.Workspace); err != nil {
			continue // the workspace is gone: no configuration, nothing to keep
		}
		tags[image.ImageStoreKey(p.StorePath)] = struct{}{}
		known = true
	}
	return tags, known
}
