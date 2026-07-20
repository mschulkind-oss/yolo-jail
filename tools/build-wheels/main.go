// Command build-wheels builds per-platform PyPI wheels that wrap the Go host
// binaries. Byte-faithful Go port of scripts/build_wheels.py (itself derived
// from Simon Willison's go-to-wheel, Apache-2.0 — same wheel layout and
// platform tags). The yolo-jail package ships TWO console scripts (yolo and
// yolo-ps), so this cannot use go-to-wheel directly.
//
// Windows is deliberately absent: the Go tree uses unix-only syscalls and the
// tool has no Windows story.
//
// Usage (CI: .github/workflows/publish.yml; also runnable locally):
//
//	go run ./tools/build-wheels --version 0.7.0 [--output-dir dist]
//	    [--platforms linux-amd64,darwin-arm64]
//
// This lives under tools/ (not cmd/) on purpose: cmd/* binaries are symlinked
// into the jail image, built by scripts/build-go.sh, and enumerated by the
// flake build loop. tools/ leaks into none of those while staying in-module
// (so go vet / staticcheck / gofmt cover it).
package main

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/csv"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	packageName = "yolo-jail"
	importName  = "yolo_jail"
	versionPkg  = "github.com/mschulkind-oss/yolo-jail/internal/version"

	// creatorUnix is the ZIP "version made by" host-system byte (3 == Unix).
	// Go's zip writer preserves the high byte and stamps the low byte with the
	// spec version (20), yielding the same 0x0314 value CPython's zipfile emits
	// on a POSIX host — so zipinfo interprets external_attr as Unix modes.
	creatorUnix = 3

	// execMode is S_IFREG | 0o755 (rwxr-xr-x). The S_IFREG file-type bit is
	// load-bearing: without it some installers treat external_attr as garbage
	// and extract the binaries non-executable.
	execMode = 0o100755

	// dataMode mirrors CPython zipfile.writestr()'s default external_attr for a
	// non-directory string arcname: 0o600 << 16 (no file-type bits). Setting it
	// explicitly keeps the zipinfo perms column identical to the Python builder.
	dataMode = 0o600
)

// binary pairs a console-script name with its wrapper function. Order matters:
// the first entry is the primary binary and backs both `main` and __main__.py.
type binary struct {
	script string
	fn     string
}

var binaries = []binary{
	{"yolo", "main"},
	{"yolo-ps", "ps"},
}

type platform struct {
	goos, goarch, tag string
}

// platformKeys is the ordered key list (drives the default --platforms value
// and its display); platforms maps each key to (GOOS, GOARCH, wheel tag). Same
// keys/tags as go-to-wheel. glibc and musl share one CGO_ENABLED=0 static
// binary; the two tags exist so pip resolves on both libc families.
var platformKeys = []string{
	"linux-amd64",
	"linux-arm64",
	"linux-amd64-musl",
	"linux-arm64-musl",
	"darwin-amd64",
	"darwin-arm64",
}

var platforms = map[string]platform{
	"linux-amd64":      {"linux", "amd64", "manylinux_2_17_x86_64"},
	"linux-arm64":      {"linux", "arm64", "manylinux_2_17_aarch64"},
	"linux-amd64-musl": {"linux", "amd64", "musllinux_1_2_x86_64"},
	"linux-arm64-musl": {"linux", "arm64", "musllinux_1_2_aarch64"},
	"darwin-amd64":     {"darwin", "amd64", "macosx_10_9_x86_64"},
	"darwin-arm64":     {"darwin", "arm64", "macosx_11_0_arm64"},
}

// metadataFields is ordered to match the METADATA_FIELDS dict insertion order.
var metadataFields = []struct{ k, v string }{
	{"Summary", "Secure container jail for AI coding agents — run Claude Code, Copilot, Gemini, opencode, or pi in YOLO mode safely"},
	{"Author", "Matt Schulkind"},
	{"License", "Apache-2.0"},
	{"Home-page", "https://github.com/mschulkind-oss/yolo-jail"},
}

// fileEntry is one wheel member. The slice preserves insertion order, which is
// the RECORD order (Python relied on dict insertion order for the same thing).
type fileEntry struct {
	path    string
	content []byte
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	version := flag.String("version", "", "wheel version, no leading v (required)")
	outputDir := flag.String("output-dir", "dist", "output directory")
	platformsFlag := flag.String("platforms", strings.Join(platformKeys, ","),
		"comma-separated subset of: "+strings.Join(platformKeys, ", "))
	goBinary := flag.String("go-binary", "go", "go binary to use")
	flag.Parse()

	if *version == "" {
		return fmt.Errorf("--version is required")
	}

	var requested []string
	for _, p := range strings.Split(*platformsFlag, ",") {
		if p != "" {
			requested = append(requested, p)
		}
	}
	var unknown []string
	for _, p := range requested {
		if _, ok := platforms[p]; !ok {
			unknown = append(unknown, p)
		}
	}
	if len(unknown) > 0 {
		return fmt.Errorf("unknown platforms: %s", strings.Join(unknown, ", "))
	}

	root := repoRoot()
	commit := gitCommit(root)

	tmp, err := os.MkdirTemp("", "yolo-wheels-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)

	// glibc/musl pairs share a build — compile once per (goos, goarch).
	compiled := map[string]map[string][]byte{}
	for _, key := range requested {
		pf := platforms[key]
		ck := pf.goos + "/" + pf.goarch
		if _, ok := compiled[ck]; !ok {
			bins, err := compileBinaries(*version, pf.goos, pf.goarch, *goBinary, commit, tmp, root)
			if err != nil {
				return err
			}
			compiled[ck] = bins
		}
		wheel, err := buildWheel(compiled[ck], *version, pf.tag, *outputDir, root)
		if err != nil {
			return err
		}
		fmt.Printf("built %s\n", wheel)
	}
	return nil
}

// repoRoot locates the module root. Preferring `git rev-parse --show-toplevel`
// lets the tool run from a subdirectory; it falls back to the working
// directory (which is the repo root in every current caller).
func repoRoot() string {
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err == nil {
		if root := strings.TrimSpace(string(out)); root != "" {
			return root
		}
	}
	wd, _ := os.Getwd()
	return wd
}

// gitCommit returns the short HEAD hash, or "" if git is unavailable (the
// Python builder likewise tolerated a missing commit and dropped the -X flag).
func gitCommit(root string) string {
	cmd := exec.Command("git", "rev-parse", "--short", "HEAD")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// compileBinaries cross-compiles every host binary for one platform and returns
// script-name -> bytes. The ldflags set is copied verbatim from the Python
// builder so the embedded binaries stay byte-identical (no -trimpath added).
func compileBinaries(version, goos, goarch, goBinary, commit, tmpDir, root string) (map[string][]byte, error) {
	ldflags := "-s -w -X " + versionPkg + ".buildVersion=" + version
	if commit != "" {
		ldflags += " -X " + versionPkg + ".GitCommit=" + commit
	}

	out := map[string][]byte{}
	for _, b := range binaries {
		target := filepath.Join(tmpDir, b.script+"_"+goos+"_"+goarch)
		cmd := exec.Command(goBinary, "build", "-ldflags="+ldflags, "-o", target, "./cmd/"+b.script)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "GOOS="+goos, "GOARCH="+goarch, "CGO_ENABLED=0")
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			return nil, fmt.Errorf("go build failed for %s (%s/%s):\n%s", b.script, goos, goarch, stderr.String())
		}
		data, err := os.ReadFile(target)
		if err != nil {
			return nil, err
		}
		out[b.script] = data
	}
	return out, nil
}

// generateInitPy renders yolo_jail/__init__.py. The template is copied
// byte-for-byte from build_wheels.py's f-string (Python source shipped inside
// the wheel — it must match exactly so the parity diff passes).
func generateInitPy(version string) string {
	var wrappers []string
	for _, b := range binaries {
		wrappers = append(wrappers, fmt.Sprintf("def %s():\n    _run(%q)", b.fn, b.script))
	}
	return fmt.Sprintf(`"""yolo-jail Go binaries packaged as a Python wheel."""

import os
import stat
import sys

__version__ = "%s"


def _run(name):
    binary = os.path.join(os.path.dirname(__file__), "bin", name)
    # Some installers drop the exec bit; restore it before exec.
    mode = os.stat(binary).st_mode
    if not (mode & stat.S_IXUSR):
        os.chmod(binary, mode | stat.S_IXUSR | stat.S_IXGRP | stat.S_IXOTH)
    os.execvp(binary, [binary] + sys.argv[1:])


%s
`, version, strings.Join(wrappers, "\n\n"))
}

// generateEntryPoints renders dist-info/entry_points.txt (console scripts, not
// .data/scripts/).
func generateEntryPoints() string {
	var lines []string
	for _, b := range binaries {
		lines = append(lines, fmt.Sprintf("%s = %s:%s", b.script, importName, b.fn))
	}
	return "[console_scripts]\n" + strings.Join(lines, "\n") + "\n"
}

// generateMetadata renders dist-info/METADATA, with the full README as the
// body after a blank line.
func generateMetadata(version, readme string) string {
	lines := []string{
		"Metadata-Version: 2.1",
		"Name: " + packageName,
		"Version: " + version,
	}
	for _, f := range metadataFields {
		lines = append(lines, f.k+": "+f.v)
	}
	lines = append(lines,
		"Requires-Python: >=3.10",
		"Description-Content-Type: text/markdown",
		"",
		readme,
	)
	return strings.Join(lines, "\n") + "\n"
}

// generateWheelMetadata renders dist-info/WHEEL. The Generator line is kept
// verbatim from the Python builder so the byte-diff validation passes.
func generateWheelMetadata(platformTag string) string {
	return "Wheel-Version: 1.0\n" +
		"Generator: yolo-jail build_wheels (go-to-wheel-derived)\n" +
		"Root-Is-Purelib: false\n" +
		"Tag: py3-none-" + platformTag + "\n"
}

// recordHash returns the `sha256=<urlsafe-b64, padding stripped>` digest used
// in RECORD.
func recordHash(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256=" + base64.RawURLEncoding.EncodeToString(sum[:])
}

// generateRecord renders dist-info/RECORD. UseCRLF is required: Python's csv
// module defaults to \r\n line terminators, Go's csv writer defaults to \n.
// RECORD lists itself last with empty hash/size (path,,).
func generateRecord(files []fileEntry) string {
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	w.UseCRLF = true
	for _, f := range files {
		if strings.HasSuffix(f.path, "RECORD") {
			_ = w.Write([]string{f.path, "", ""})
		} else {
			_ = w.Write([]string{f.path, recordHash(f.content), strconv.Itoa(len(f.content))})
		}
	}
	w.Flush()
	return buf.String()
}

// buildWheel assembles one wheel. File insertion order == RECORD order; RECORD
// is generated over the full list (including its own placeholder entry) and its
// content is then filled in, exactly as build_wheels.py did.
func buildWheel(bins map[string][]byte, version, platformTag, outputDir, root string) (string, error) {
	readme, err := os.ReadFile(filepath.Join(root, "README.md"))
	if err != nil {
		return "", err
	}

	var files []fileEntry
	files = append(files, fileEntry{importName + "/__init__.py", []byte(generateInitPy(version))})
	files = append(files, fileEntry{importName + "/__main__.py", []byte("from . import main\nmain()\n")})
	for _, b := range binaries {
		files = append(files, fileEntry{importName + "/bin/" + b.script, bins[b.script]})
	}

	distInfo := importName + "-" + version + ".dist-info"
	files = append(files, fileEntry{distInfo + "/METADATA", []byte(generateMetadata(version, string(readme)))})
	files = append(files, fileEntry{distInfo + "/WHEEL", []byte(generateWheelMetadata(platformTag))})
	files = append(files, fileEntry{distInfo + "/entry_points.txt", []byte(generateEntryPoints())})
	// Apache-2.0 §4 redistribution obligations: ship LICENSE + NOTICE inside the
	// wheel under dist-info/licenses/ (same layout `uv build` produced before).
	for _, name := range []string{"LICENSE", "NOTICE"} {
		if data, err := os.ReadFile(filepath.Join(root, name)); err == nil {
			files = append(files, fileEntry{distInfo + "/licenses/" + name, data})
		}
	}

	files = append(files, fileEntry{distInfo + "/RECORD", nil})
	files[len(files)-1].content = []byte(generateRecord(files))

	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return "", err
	}
	wheelPath := filepath.Join(outputDir, importName+"-"+version+"-py3-none-"+platformTag+".whl")
	if err := writeWheel(files, wheelPath); err != nil {
		return "", err
	}
	return wheelPath, nil
}

// writeWheel zips the ordered file list. Each entry is stamped with the Unix
// host byte; entries under bin/ carry an executable mode (0o755 + S_IFREG) and
// are STORED uncompressed, all others carry 0o600 and are Deflate-compressed —
// exactly matching build_wheels.py, which passed a bare ZipInfo (default
// compress_type ZIP_STORED) for the binaries and a plain arcname (inheriting
// the archive's ZIP_DEFLATED) for everything else. Storing the multi-MB
// binaries also keeps the archive's aggregate size byte-for-byte comparable.
func writeWheel(files []fileEntry, wheelPath string) error {
	f, err := os.Create(wheelPath)
	if err != nil {
		return err
	}
	defer f.Close()

	zw := zip.NewWriter(f)
	for _, fe := range files {
		hdr := &zip.FileHeader{Name: fe.path, Method: zip.Deflate}
		hdr.CreatorVersion = creatorUnix << 8
		if strings.Contains(fe.path, "/bin/") {
			hdr.Method = zip.Store
			hdr.ExternalAttrs = uint32(execMode) << 16
		} else {
			hdr.ExternalAttrs = uint32(dataMode) << 16
		}
		w, err := zw.CreateHeader(hdr)
		if err != nil {
			return err
		}
		if _, err := w.Write(fe.content); err != nil {
			return err
		}
	}
	if err := zw.Close(); err != nil {
		return err
	}
	return f.Close()
}
