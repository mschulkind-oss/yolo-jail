package image

import (
	"bufio"
	"os"
	"os/exec"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// BuildOCIImage runs the side-effecting core of _build_image_store_path for the
// `yolo check` preflight: run `nix build .#ociImage --impure` and return the
// resulting store path on success, plus the retained stderr tail (last 30
// lines) for failure diagnosis via DiagnoseNixBuildFailure. storePath is "" on
// any failure (non-zero exit, missing nix). extraPackages, when non-empty, is
// JSON-encoded into YOLO_EXTRA_PACKAGES the way the Python does.
//
// The rich live-status spinner and the --builders offload path stay in the run
// slice; check's callsite passes no builders and consumes only (storePath,
// stderrTail).
func BuildOCIImage(repoRoot string, extraPackages []any) (string, []string) {
	outLink, err := os.CreateTemp("", "yolo-check-*")
	if err != nil {
		return "", []string{"could not create out-link temp: " + err.Error()}
	}
	outPath := outLink.Name()
	_ = outLink.Close()
	_ = os.Remove(outPath) // nix creates the symlink itself
	// Removing the out-link (and thus its GC root) is safe HERE and only here:
	// the `yolo check` preflight builds the image to prove it *can* build, but
	// never loads or runs it — there is no running closure to protect, so an
	// unrooted result is correct. Do NOT copy this pattern into a load path: the
	// run path (autoload.go) MUST retain a durable root for the image it runs
	// against (storage-lifecycle §1; see image.RegisterImageRoot).
	defer os.Remove(outPath)

	buildEnv := os.Environ()
	if len(extraPackages) > 0 {
		if pkgJSON, err := jsonx.DumpsCompact(extraPackages); err == nil {
			buildEnv = append(buildEnv, "YOLO_EXTRA_PACKAGES="+pkgJSON)
		}
	}

	argv := []string{
		"nix",
		"--extra-experimental-features", "nix-command flakes",
		"build", ".#ociImage", "--impure",
		"--out-link", outPath,
		"--print-build-logs",
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = repoRoot
	cmd.Env = buildEnv
	cmd.Stdout = nil
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return "", []string{"could not pipe nix stderr: " + err.Error()}
	}
	if err := cmd.Start(); err != nil {
		return "", []string{"nix command not found"}
	}

	var tail []string
	scanner := bufio.NewScanner(stderr)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		clean := strings.TrimRight(scanner.Text(), " \t\r\n")
		if clean == "" {
			continue
		}
		tail = append(tail, clean)
		if len(tail) > 30 {
			tail = tail[1:]
		}
	}
	_ = cmd.Wait()
	if cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != 0 {
		return "", tail
	}
	// Resolve the out-link the way str(out_link.resolve()) does.
	if resolved, err := os.Readlink(outPath); err == nil {
		return resolved, tail
	}
	return outPath, tail
}
