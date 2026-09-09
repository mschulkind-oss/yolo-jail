// Package image provides the container-image build/delivery pipeline — the
// command builders, the nix-stderr summarizer, the per-runtime load sentinel
// (LRU of store paths), the sha256-keyed content ref and cache path, and the
// layer-aware copy that delivers an image into a runtime (layercopy.go).
//
// Architecture and invariants: docs/reference/image-staging-vs-baking.md
package image

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// ImageLoadCmd returns the command to load a container image from a tar
// archive — a FILE on disk.
//
// SINCE C9 IT HAS ONE CALLER: the build-failure / SkipBuild fallback that loads
// whatever LEGACY tar `newestTars` found. The normal path on every backend is a
// `skopeo copy` from the nix store (internal/image/layercopy.go) and writes no
// tar at all — the stdin form this file used to also carry (`podman load` with
// no -i, C3's pipe) is gone with the stream that fed it.
//
// It still branches per runtime, and that is why the degraded loop can be one
// arm instead of two: `container image load -i` and `podman load -i` differ only
// here.
func ImageLoadCmd(runtime, tarPath string) []string {
	if runtime == "container" {
		return []string{"container", "image", "load", "-i", tarPath}
	}
	return []string{runtime, "load", "-i", tarPath}
}

// ImageInspectCmd returns the command to inspect a container image. Mirrors
// _image_inspect_cmd.
func ImageInspectCmd(runtime, image string) []string {
	return []string{runtime, "image", "inspect", image}
}

// ImageTagCmd returns the command that gives an already-loaded image a SECOND
// name. C2 uses it to point the legacy `:latest` tag at the image just streamed
// under its content-addressed name — the DOWNSTREAM direction, so a failure
// costs a stale `podman images` listing and nothing else.
//
// The other direction (tag the content ref FROM :latest, after the load) is what
// this used to do, and it is a defect: see ContainersStorageDest
// (internal/image/layercopy.go) for the race that made a wrong binding
// permanent, and for why C9 makes it unrepresentable rather than merely
// avoided.
//
// Apple Container never reaches this: the copy names the image inside the OCI
// archive it writes, so the content ref is applied going in — which also avoids
// inventing an argv for a `container image tag` subcommand nothing here can
// verify.
func ImageTagCmd(runtime, src, dst string) []string {
	return []string{runtime, "tag", src, dst}
}

// JailImage returns the LEGACY :latest jail image name appropriate for the
// runtime: the short (unqualified) name for Apple Container, the fully-qualified
// ref otherwise.
//
// This is NOT the ref a jail runs (see JailImageRef). It survives for two jobs:
// the degraded fallback branch in AutoLoadImage — SkipBuild, or a failed build
// the operator opted past, where "is *an* image present" is the only question
// askable and there is no store path to hash — and as the DESTINATION of the
// best-effort tag that keeps `:latest` pointing at the newest load, so a human
// running `podman images` still sees one.
func JailImage(runtime string) string {
	if runtime == "container" {
		return paths.JailImageShort
	}
	return paths.JailImage
}

// JailImageRepository returns the repository half of the jail image ref for a
// runtime — the part that is invariant across every image this machine ever
// loads, and therefore the right filter for "any jail image" questions
// (`podman images <repo>`, as internal/prune already does).
func JailImageRepository(runtime string) string {
	if runtime == "container" {
		return paths.JailImageRepoShort
	}
	return paths.JailImageRepo
}

// JailImageRef is C2's content-addressed image ref: `<repo>:<sha16-of-storePath>`.
//
// The tag is keyFor(storePath) — the SAME 16 hex chars that name the store
// path's cache tar (cache/images/<key>.tar) and its durable GC root
// (build/roots/<key>). One hash function for all three, so a reaper can
// correlate a loaded image, its tar and its root without a reverse lookup, and
// so the three can never drift apart (docs/reference/image-staging-vs-baking.md,
// "The content-addressed image ref").
//
// This is what makes "is the image for THIS config loaded?" answerable at all.
// While one `:latest` tag named every image, the question could only be
// approximated — see the load decision in AutoLoadImage for the ambiguity that
// approximation left behind, and why content addressing dissolves it.
func JailImageRef(runtime, storePath string) string {
	return JailImageRepository(runtime) + ":" + keyFor(storePath)
}

var (
	reCopyingPath = regexp.MustCompile(`copying path '/nix/store/[a-z0-9]+-(.+?)'`)
	reBuildingDrv = regexp.MustCompile(`building '/nix/store/[a-z0-9]+-(.+?)\.drv'`)
	reProgress    = regexp.MustCompile(`^\[[\d/]+ (?:built|copied|fetched).*\]`)
)

// SummarizeNixLine extracts a short human-readable summary from a nix build
// stderr line, or "" if none applies.
// including precedence: copying → building → evaluating → progress-counter.
func SummarizeNixLine(line string) string {
	if m := reCopyingPath.FindStringSubmatch(line); m != nil {
		return "Fetching " + m[1]
	}
	if m := reBuildingDrv.FindStringSubmatch(line); m != nil {
		return "Building " + m[1]
	}
	if strings.Contains(strings.ToLower(line), "evaluating") {
		return "Evaluating flake..."
	}
	// re.match anchors at the start of the STRIPPED line.
	if reProgress.MatchString(strings.TrimSpace(line)) {
		return strings.TrimSpace(line)
	}
	return ""
}

// FormatImageSize formats a materialized-image byte count the way auto_load_image
// prints it: "%.0f MB" below 1 GB, else "%.1f GB".
func FormatImageSize(totalBytes int64) string {
	mb := float64(totalBytes) / (1024 * 1024)
	if mb >= 1024 {
		return fmt.Sprintf("%.1f GB", mb/1024)
	}
	return fmt.Sprintf("%.0f MB", mb)
}

// ReadLoadedPaths reads the set of store paths loaded into a runtime from its
// sentinel file. Missing file → empty.
// dropped, each line stripped).
func ReadLoadedPaths(sentinel string) map[string]struct{} {
	out := map[string]struct{}{}
	data, err := os.ReadFile(sentinel)
	if err != nil {
		return out
	}
	for _, line := range strings.Split(string(data), "\n") {
		if s := strings.TrimSpace(line); s != "" {
			out[s] = struct{}{}
		}
	}
	return out
}

// CurrentLoadedPath returns the MOST-RECENT store path recorded in a runtime's
// load sentinel — the image a jail launched against that runtime runs right now
// (AddLoadedPath appends the newest entry last). ok=false when the sentinel is
// missing or empty. Distinct from ReadLoadedPaths, which returns the whole LRU
// as an unordered set: the storage §3 store-GC rooting confirmation needs the
// single current image per runtime, not its history.
func CurrentLoadedPath(sentinel string) (string, bool) {
	data, err := os.ReadFile(sentinel)
	if err != nil {
		return "", false
	}
	current := ""
	for _, line := range strings.Split(string(data), "\n") {
		if s := strings.TrimSpace(line); s != "" {
			current = s // keep overwriting → the last non-empty line wins
		}
	}
	if current == "" {
		return "", false
	}
	return current, true
}

// AddLoadedPath appends storePath to the sentinel as the most-recent entry,
// de-duplicating (move-to-end) and capping at the 10 most recent. Written as
// "\n".join(paths) + "\n".
//
// Since C2 the caller records a path on every successful launch, not only when a
// load happened, so the LRU means "recently USED" — several images stay loaded
// at once now, and a jail can legitimately run one whose load was many launches
// ago. That is the property prune's protected set needs; see the call site in
// AutoLoadImage for what leaving it on the load path would cost.
func AddLoadedPath(sentinel, storePath string) error {
	var pathsList []string
	if data, err := os.ReadFile(sentinel); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			if s := strings.TrimSpace(line); s != "" && s != storePath {
				pathsList = append(pathsList, s)
			}
		}
	}
	pathsList = append(pathsList, storePath)
	if len(pathsList) > 10 {
		pathsList = pathsList[len(pathsList)-10:]
	}
	return os.WriteFile(sentinel, []byte(strings.Join(pathsList, "\n")+"\n"), 0o644)
}

// keyFor is the first 16 hex chars of sha256(storePath) — the shared key that
// correlates a store path's cache tar (cache/images/<key>.tar) with its durable
// GC root (build/roots/<key>, see gcroot.go). Both derive from this one helper
// so the two can never drift apart.
func keyFor(storePath string) string {
	sum := sha256.Sum256([]byte(storePath))
	return hex.EncodeToString(sum[:])[:16]
}
