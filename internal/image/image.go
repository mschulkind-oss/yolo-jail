// Package image provides the container-image build/load pipeline — the command
// builders, the nix-stderr summarizer, the byte-progress formatter, the
// per-runtime load sentinel (LRU of store paths), the sha256-keyed cache path,
// and the /etc/nix/machines stream-command resolution.
package image

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// ImageLoadCmd returns the command to load a container image from a tar
// archive. Mirrors _image_load_cmd.
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

// JailImage returns the jail image name appropriate for the runtime: the short
// (unqualified) name for Apple Container, the fully-qualified ref otherwise.
// Mirrors _jail_image.
func JailImage(runtime string) string {
	if runtime == "container" {
		return paths.JailImageShort
	}
	return paths.JailImage
}

var (
	reCopyingPath = regexp.MustCompile(`copying path '/nix/store/[a-z0-9]+-(.+?)'`)
	reBuildingDrv = regexp.MustCompile(`building '/nix/store/[a-z0-9]+-(.+?)\.drv'`)
	reProgress    = regexp.MustCompile(`^\[[\d/]+ (?:built|copied|fetched).*\]`)
)

// SummarizeNixLine extracts a short human-readable summary from a nix build
// stderr line, or "" if none applies. Mirrors _summarize_nix_line exactly,
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

// FormatProgress formats byte progress with an optional percentage. Mirrors
// _format_progress: MB shown as "%.0f MB" below 1024, else "%.1f GB"; the
// percentage is capped at 99 until done. Go's %.0f/%.1f use round-half-to-even,
// matching Python's format spec.
func FormatProgress(current, estimate int64) string {
	mb := float64(current) / (1024 * 1024)
	var curStr string
	if mb >= 1024 {
		curStr = fmt.Sprintf("%.1f GB", mb/1024)
	} else {
		curStr = fmt.Sprintf("%.0f MB", mb)
	}
	if estimate > 0 {
		pct := int(current * 100 / estimate)
		if pct > 99 {
			pct = 99 // cap at 99% until done
		}
		return fmt.Sprintf("%s (%d%%)", curStr, pct)
	}
	return curStr
}

// FormatImageSize formats a materialized-image byte count the way auto_load_image
// prints it: "%.0f MB" below 1 GB, else "%.1f GB". Mirrors the size_str block.
func FormatImageSize(totalBytes int64) string {
	mb := float64(totalBytes) / (1024 * 1024)
	if mb >= 1024 {
		return fmt.Sprintf("%.1f GB", mb/1024)
	}
	return fmt.Sprintf("%.0f MB", mb)
}

// ReadLoadedPaths reads the set of store paths loaded into a runtime from its
// sentinel file. Missing file → empty. Mirrors _read_loaded_paths (blank lines
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

// AddLoadedPath appends storePath to the sentinel as the most-recent entry,
// de-duplicating (move-to-end) and capping at the 10 most recent. Written as
// "\n".join(paths) + "\n". Mirrors _add_loaded_path.
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

// ImageCachePath returns the cached tar file path for a nix store path, keyed by
// the first 16 hex chars of sha256(storePath), under GLOBAL_CACHE/images/.
// Mirrors _image_cache_path (including the mkdir -p of the images dir).
func ImageCachePath(storePath string) (string, error) {
	cacheDir := filepath.Join(paths.GlobalCache(), "images")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(storePath))
	pathHash := hex.EncodeToString(sum[:])[:16]
	return filepath.Join(cacheDir, pathHash+".tar"), nil
}

// SizeFileForSentinel derives the size-estimate file path from a sentinel path
// the way _estimate_image_size does: sentinel.parent / f"{sentinel.name}-size".
//
// PRESERVED QUIRK: _materialize_image calls this with sentinel =
// BUILD_DIR/"last-load-size", so the READ path is BUILD_DIR/"last-load-size-size"
// — but the WRITER at the end of _materialize_image writes to
// BUILD_DIR/"last-load-size" (no doubled suffix). The two never meet: the size
// estimate reads a file the pipeline never writes, so it always falls through to
// the nix closure-size probe. This is faithfully reproduced, not fixed (see the
// go-port divergence policy — surprising Python behavior is preserved).
func SizeFileForSentinel(sentinel string) string {
	return filepath.Join(filepath.Dir(sentinel), filepath.Base(sentinel)+"-size")
}

// ReadEstimatedSizeFile reads the cached size estimate from the given size file
// (see SizeFileForSentinel), returning (0, false) when absent or unparseable —
// the callsite then falls back to the nix closure-size probe. Mirrors the
// size_file branch of _estimate_image_size.
func ReadEstimatedSizeFile(sizeFile string) (int64, bool) {
	data, err := os.ReadFile(sizeFile)
	if err != nil {
		return 0, false
	}
	n, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

// SizeSentinelPath is the sentinel _materialize_image passes to
// _estimate_image_size (BUILD_DIR/last-load-size). The WRITER also targets this
// path; the reader targets SizeFileForSentinel(SizeSentinelPath) — the quirk
// documented on SizeFileForSentinel.
func SizeSentinelPath() string {
	return filepath.Join(paths.BuildDir(), "last-load-size")
}
