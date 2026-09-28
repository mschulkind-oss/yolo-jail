package prune

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/image"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// liveimageroots.go maps the image a running container is on to the GC root that
// pins its closure (BUILD_DIR/roots/<sha16>), for the two readers that need it:
// the GC-root reaper, which since OQ-LS4 never reaps the root of an image a
// container is running on, and the store GC's refusal (UnrootedRunningImages).
//
// A running ref comes from `<runtime> ps --format {{.Image}}` — the name the
// container was started from, which on a normal launch is one of two spellings:
//
//   - `<repo>:<sha16>`, the content-addressed ref (image.JailImageRef). The tag
//     IS the root's basename.
//   - `<repo>:stock-<64 hex>`, the stock ref a matched launch runs
//     (image.StockImageRef). The tag carries the flake's identity, not the store
//     path, so it maps through the record the load that wrote the tag left
//     (image.RecordedStockStorePath) and then through image.ImageStoreKey.
//
// Anything else is UNMAPPABLE. Two kinds, because the two readers treat them
// differently: a ref in the jail repository (the legacy `latest`, a stock tag
// with no record) or with no tag at all (a bare ID, which could be anything) is a
// jail whose root yolo cannot name; a ref in any other repository is FOREIGN — a
// container yolo did not start, which needs no root of yolo's.

// runningRoot is one running ref's answer.
type runningRoot struct {
	ref string
	// key is the roots/<key> basename, "" when unmappable.
	key string
	// storePath is the store path the root must point at, when the mapping
	// knows it (stock refs); "" for a content ref, whose tag is a hash of it.
	storePath string
	// foreign marks a ref outside the jail repository.
	foreign bool
	// why is the unmappable reason, in the user's words.
	why string
}

// isJailRepository reports whether repo (a ref's part before the tag) is the
// jail image's, in either runtime's spelling.
func isJailRepository(repo string) bool {
	return repo == paths.JailImageRepo || repo == paths.JailImageRepoShort
}

// isContentTag reports whether tag has the shape of image.ImageStoreKey: 16
// lowercase hex characters.
func isContentTag(tag string) bool {
	return len(tag) == 16 && strings.TrimLeft(tag, "0123456789abcdef") == ""
}

// mapRunningRef classifies one running ref against buildDir's stock records.
func mapRunningRef(buildDir, ref string) runningRoot {
	i := strings.LastIndex(ref, ":")
	// A colon inside a registry host:port is followed by a `/`, so it is not a tag.
	if i < 0 || strings.Contains(ref[i+1:], "/") {
		return runningRoot{ref: ref, why: "no tag; cannot tell which image it is"}
	}
	repo, tag := ref[:i], ref[i+1:]
	if !isJailRepository(repo) {
		return runningRoot{ref: ref, foreign: true, why: "not a yolo jail image; yolo holds no root for it"}
	}
	if isContentTag(tag) {
		return runningRoot{ref: ref, key: tag}
	}
	if hex, ok := image.StockTagHex(tag); ok {
		sp, recorded := image.RecordedStockStorePath(buildDir, hex)
		if !recorded {
			return runningRoot{ref: ref, why: "a stock image this host has no build record for"}
		}
		return runningRoot{ref: ref, key: image.ImageStoreKey(sp), storePath: sp}
	}
	return runningRoot{ref: ref, why: "a tag that names no store path"}
}

// LiveImageRootKeys asks the runtime which images have running containers and
// returns the roots/<sha16> basenames that pin their closures — the set the
// GC-root reaper must never reap (OQ-LS4).
//
// known=false means the reaper must reap NOTHING this pass, and why says which
// evidence was missing. Two causes: the runtime could not be asked, or a running
// JAIL's image cannot be mapped to its root — then any root could be that jail's,
// and reaping is the dangerous direction. A FOREIGN container is ignored: it is
// not a yolo jail and runs on no root of yolo's.
func LiveImageRootKeys(rt, buildDir string, run RunFunc) (keys map[string]bool, known bool, why string) {
	refs, ok := RunningImageRefs(rt, run)
	if !ok {
		return nil, false, fmt.Sprintf("could not list running container images (%s)", rt)
	}
	keys = map[string]bool{}
	var unmappable []string
	for _, ref := range refs {
		r := mapRunningRef(buildDir, ref)
		switch {
		case r.key != "":
			keys[r.key] = true
		case !r.foreign:
			unmappable = append(unmappable, r.ref+" ("+r.why+")")
		}
	}
	if len(unmappable) > 0 {
		sort.Strings(unmappable)
		return nil, false, "a running jail's image cannot be mapped to its GC root: " +
			strings.Join(unmappable, ", ")
	}
	return keys, true, ""
}

// UnrootedRunningImages is what is left of the store GC's refusal after OQ-LS4,
// and it is deliberately small.
//
// Since the GC-root reaper holds every running image's root by liveness, a root
// that EXISTS for a running image is safe from both reapers, so the refusal no
// longer re-proves what the reaper guarantees. It keeps the two cases the reaper
// cannot give:
//
//   - a running image yolo CANNOT MAP to a root at all — no tag, a foreign image,
//     the legacy `latest`, a stock tag with no build record. Unknown is not
//     permission (P3): yolo cannot say its closure is pinned, so it will not
//     collect the store under it.
//   - a mapped image whose root does NOT EXIST — liveness can hold a root, never
//     create one. A registration that failed at launch, or a jail started before
//     the stock record existed whose root has since aged out, is running on an
//     unpinned closure, and `nix store gc` would delete it.
//
// The sentinel-based half this used to carry (UnrootedProtectedPaths over the
// last ten loads) is GONE: it refused the GC for a closure no container runs,
// which is a cache the age reaper is entitled to give up, and every closure a
// container DOES run is covered here without it.
//
// Each entry names the ref and why; sorted for a deterministic report.
func UnrootedRunningImages(buildDir string, refs []string) []string {
	rootsDir := filepath.Join(buildDir, "roots")
	unrooted := []string{}
	seen := map[string]bool{}
	for _, ref := range refs {
		if seen[ref] {
			continue
		}
		seen[ref] = true
		r := mapRunningRef(buildDir, ref)
		if r.key == "" {
			unrooted = append(unrooted, r.ref+" ("+r.why+"; cannot confirm its closure is rooted)")
			continue
		}
		target, err := os.Readlink(filepath.Join(rootsDir, r.key))
		if err != nil || (r.storePath != "" && target != r.storePath) {
			unrooted = append(unrooted, r.ref+" (no durable GC root at build/roots/"+r.key+")")
		}
	}
	sort.Strings(unrooted)
	return unrooted
}
