package image

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/containerbuilder"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// AutoLoadOptions carries the injectable seams for AutoLoadImage so the load
// pipeline is testable without a real nix/podman. Zero fields get real
// implementations.
type AutoLoadOptions struct {
	// Runtime is "podman" or "container".
	Runtime string
	// RepoRoot is the nix build cwd.
	RepoRoot string
	// ExtraPackages is the config `packages` list (JSON-encoded into
	// YOLO_EXTRA_PACKAGES). nil/empty → unset.
	ExtraPackages []any
	// Out receives the human progress/status lines (rich markup already
	// stripped by the caller's printer; here we write plain text). nil =>
	// io.Discard.
	Out io.Writer
	// ProgressTTY reports whether Out is a real terminal. When true, the
	// image-caching byte progress redraws IN PLACE (carriage return, like
	// Python's rich status spinner) instead of one line per chunk — otherwise
	// a multi-GB stream spams hundreds of "Caching image… 98%" lines. When
	// false (piped/redirected), progress is suppressed to a single start line.
	ProgressTTY bool
	// IsMacOS overrides the platform for the build-offload branch.
	IsMacOS bool
	// Getpid names the PID-unique out-link. nil => os.Getpid.
	Getpid func() int
	// BuildStorePath runs the nix build and returns (storePath, stderrTail).
	// nil => the real nix build. Injected for tests.
	BuildStorePath func(repoRoot string, extra []any, outLink string) (string, []string)
	// BuildOffload attempts the macOS container-builder offload after a plain
	// build fails: it starts a Linux builder container and retries the nix build
	// with a --builders line pointing at it. Returns (storePath, stderrTail);
	// "" if the offload is unavailable or also failed. nil => the real offload
	// (containerbuilder session); a nil-returning stub disables it (Linux, tests).
	BuildOffload func(repoRoot string, extra []any, outLink string) (string, []string)
	// Run runs a subprocess (image inspect / load), returning (rc, ran). nil =>
	// real exec. Used for the runtime-side probes only.
	Run func(argv []string) (rc int, ran bool)
	// Materialize streams the nix image to cacheFile, returning byte count (0 on
	// failure). nil => real streaming.
	Materialize func(storePath, cacheFile string) int64
	// DiagnoseFailure maps a nix stderr tail to (title, remedy). nil => a plain
	// join (the caller normally passes nixdiag.DiagnoseNixBuildFailure bound
	// with the resolved remedy).
	DiagnoseFailure func(stderrTail []string) (title, remedy string)
	// LoadAppleContainer converts+loads a tar into Apple Container. nil => real.
	LoadAppleContainer func(tarPath string) bool
}

func (o *AutoLoadOptions) fill() {
	if o.Out == nil {
		o.Out = io.Discard
	}
	if o.Getpid == nil {
		o.Getpid = os.Getpid
	}
	if o.BuildStorePath == nil {
		o.BuildStorePath = func(repoRoot string, extra []any, outLink string) (string, []string) {
			return buildImageStorePath(repoRoot, extra, outLink, o.Out)
		}
	}
	if o.BuildOffload == nil {
		o.BuildOffload = func(repoRoot string, extra []any, outLink string) (string, []string) {
			return buildImageWithContainerBuilder(o.Runtime, repoRoot, extra, outLink, o.Out)
		}
	}
	if o.Run == nil {
		o.Run = func(argv []string) (int, bool) {
			cmd := exec.Command(argv[0], argv[1:]...)
			if err := cmd.Run(); err != nil {
				if _, ok := err.(*exec.ExitError); ok {
					return cmd.ProcessState.ExitCode(), true
				}
				return 0, false
			}
			return 0, true
		}
	}
	if o.Materialize == nil {
		o.Materialize = func(storePath, cacheFile string) int64 {
			return materializeImage(storePath, cacheFile, o.IsMacOS, o.Out, o.ProgressTTY)
		}
	}
	if o.DiagnoseFailure == nil {
		o.DiagnoseFailure = func(tail []string) (string, string) {
			if len(tail) == 0 {
				return "nix build failed", ""
			}
			t := tail
			if len(t) > 10 {
				t = t[len(t)-10:]
			}
			return "nix build failed", strings.Join(t, "\n")
		}
	}
	if o.LoadAppleContainer == nil {
		o.LoadAppleContainer = func(tarPath string) bool {
			return loadImageForAppleContainer(tarPath, o.Out)
		}
	}
}

// AutoLoadImage ports auto_load_image: ensure the nix jail image is built +
// loaded into the container runtime. Returns true when an image is ready to run
// (freshly loaded, already loaded, or a cached/existing image is usable), false
// when none could be made available (the caller MUST NOT launch the jail on
// false — the actionable reason was already printed).
//
// The macOS from-source build-offload is wired (J3): when the plain build fails
// on macOS, BuildOffload starts a Linux builder container and retries the build
// over ssh-ng before falling back to a cached tar / failure diagnosis. On Linux
// the offload is never consulted. The behavioral end-to-end (real container +
// remote build) is the mac-ac-container-builder runbook (Track M).
func AutoLoadImage(opts AutoLoadOptions) bool {
	opts.fill()
	o := &opts
	out := o.Out

	sentinel := filepath.Join(paths.BuildDir(), "last-load-"+o.Runtime)
	outLink := filepath.Join(paths.BuildDir(), fmt.Sprintf("run-result-%d", o.Getpid()))
	pkgJSON := ""
	if len(o.ExtraPackages) > 0 {
		if s, err := jsonx.DumpsCompact(o.ExtraPackages); err == nil {
			pkgJSON = s
		}
	}

	currentPath, buildTail := o.BuildStorePath(o.RepoRoot, o.ExtraPackages, outLink)

	// macOS build-offload (J3): a from-source `packages:` build needs Linux. If
	// the plain build failed on macOS, start a container builder and retry the
	// build over ssh-ng before falling back to a stale cache. On Linux (or when
	// the offload is disabled) BuildOffload is a nil-returning stub.
	if currentPath == "" && o.IsMacOS {
		if off, offTail := o.BuildOffload(o.RepoRoot, o.ExtraPackages, outLink); off != "" {
			currentPath, buildTail = off, offTail
		} else if len(offTail) > 0 {
			buildTail = offTail
		}
	}

	if currentPath == "" {
		// Build failed. If the image already exists in the runtime, proceed.
		imageName := JailImage(o.Runtime)
		if rc, ran := o.Run(ImageInspectCmd(o.Runtime, imageName)); ran && rc == 0 {
			fmt.Fprintln(out, "Using existing "+imageName+" image.")
			return true
		}
		// No image in runtime — try the most recent cached tar.
		cacheDir := filepath.Join(paths.GlobalCache(), "images")
		for _, tarFile := range newestTars(cacheDir) {
			fmt.Fprintln(out, "Loading image from cache: "+filepath.Base(tarFile))
			if o.Runtime == "container" {
				if o.LoadAppleContainer(tarFile) {
					fmt.Fprintln(out, "Done: loaded image from cache")
					return true
				}
			} else {
				if rc, ran := o.Run(ImageLoadCmd(o.Runtime, tarFile)); ran && rc == 0 {
					fmt.Fprintln(out, "Done: loaded image from cache")
					return true
				}
			}
		}
		// Genuinely no image and can't build one.
		title, remedy := o.DiagnoseFailure(buildTail)
		fmt.Fprintln(out, "Cannot start jail: "+title+".")
		if remedy != "" {
			fmt.Fprintln(out, remedy)
		}
		return false
	}

	// Check if this store path has already been loaded into the runtime.
	loadedPaths := ReadLoadedPaths(sentinel)
	imageName := JailImage(o.Runtime)
	rc, ran := o.Run(ImageInspectCmd(o.Runtime, imageName))
	imagePresent := ran && rc == 0

	_, alreadyLoaded := loadedPaths[currentPath]
	if !alreadyLoaded || !imagePresent {
		switch {
		case !imagePresent && alreadyLoaded:
			fmt.Fprintln(out, "Image load needed: sentinel claims loaded, but "+imageName+
				" is missing from "+o.Runtime+" (storage reset / pruned?)")
		case len(loadedPaths) == 0:
			fmt.Fprintln(out, "Image load needed: first run (no images loaded into "+o.Runtime+" yet)")
		default:
			fmt.Fprintln(out, "Image load needed: nix store path changed")
			fmt.Fprintln(out, "  new: "+currentPath)
			if pkgJSON != "" {
				fmt.Fprintln(out, "  packages: "+pkgJSON)
			}
		}
		cacheFile, err := ImageCachePath(currentPath)
		if err != nil {
			fmt.Fprintln(out, "Error preparing image cache: "+err.Error())
			_ = os.Remove(outLink)
			return false
		}
		if !fileExists(cacheFile) {
			totalBytes := o.Materialize(currentPath, cacheFile)
			if totalBytes == 0 {
				fmt.Fprintln(out, "Error streaming image to cache.")
				_ = os.Remove(outLink)
				return false
			}
			fmt.Fprintln(out, "  Cached image: "+FormatImageSize(totalBytes))
		}
		var loadOK bool
		if o.Runtime == "container" {
			loadOK = o.LoadAppleContainer(cacheFile)
		} else {
			rc, ran := o.Run(ImageLoadCmd(o.Runtime, cacheFile))
			loadOK = ran && rc == 0
		}
		if !loadOK {
			if o.Runtime != "container" {
				fmt.Fprintln(out, "Error loading image into "+o.Runtime+".")
			}
			_ = os.Remove(outLink)
			return false
		}
		_ = AddLoadedPath(sentinel, currentPath)
		fmt.Fprintln(out, "Done: loaded image")
	}

	_ = os.Remove(outLink)
	return true
}

// buildImageStorePath ports _build_image_store_path for the run path: run
// `nix build .#ociImage --impure --out-link <outLink> --print-build-logs` in
// repoRoot, streaming a summary and retaining the last 30 stderr lines. Returns
// (resolvedStorePath, stderrTail); storePath "" on failure.
func buildImageStorePath(repoRoot string, extra []any, outLink string, out io.Writer) (string, []string) {
	return buildImageStorePathArgs(repoRoot, extra, outLink, out, nil, nil)
}

// buildImageStorePathArgs is buildImageStorePath with extra nix args
// (e.g. --builders "…") and extra env (e.g. NIX_SSHOPTS) appended — the seam the
// macOS container-builder offload uses to retry the build against a remote
// builder. extraArgs/extraEnv nil => the plain build.
func buildImageStorePathArgs(repoRoot string, extra []any, outLink string, out io.Writer, extraArgs, extraEnv []string) (string, []string) {
	buildEnv := os.Environ()
	if len(extra) > 0 {
		if pkgJSON, err := jsonx.DumpsCompact(extra); err == nil {
			buildEnv = append(buildEnv, "YOLO_EXTRA_PACKAGES="+pkgJSON)
		}
	}
	buildEnv = append(buildEnv, extraEnv...)
	argv := []string{
		"nix", "--extra-experimental-features", "nix-command flakes",
		"build", ".#ociImage", "--impure",
		"--out-link", outLink, "--print-build-logs",
	}
	argv = append(argv, extraArgs...)
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
		if summary := SummarizeNixLine(clean); summary != "" {
			fmt.Fprintln(out, summary)
		}
	}
	_ = cmd.Wait()
	if cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != 0 {
		return "", tail
	}
	if resolved, err := os.Readlink(outLink); err == nil {
		return resolved, tail
	}
	return outLink, tail
}

// buildImageWithContainerBuilder is the macOS build-offload (J3): start a Linux
// builder container and retry the nix build with a --builders line pointing at
// it over ssh-ng. Returns (storePath, stderrTail); "" if the builder couldn't be
// started or the offloaded build failed. The builder is stopped before return.
//
// The ssh key management (generate the ed25519 keypair under BuilderKeyDir,
// authorize the .pub in the container via the RunArgv pubkey env) and the actual
// remote build are behaviorally verified by the mac-ac-container-builder runbook
// (Track M); here the lifecycle is driven through the containerbuilder.Session
// seams so the decision + argv construction are exercised in unit tests.
func buildImageWithContainerBuilder(runtime, repoRoot string, extra []any, outLink string, out io.Writer) (string, []string) {
	pubkey, err := ensureBuilderKey()
	if err != nil {
		return "", []string{"container builder: " + err.Error()}
	}
	sess := &containerbuilder.Session{
		Runtime: runtime,
		Pubkey:  pubkey,
		Deps:    realSessionDeps(out),
	}
	fmt.Fprintln(out, "Starting the Linux builder container for the from-source build…")
	host, port, ok := sess.Start()
	if !ok {
		return "", []string{"container builder did not start"}
	}
	defer sess.Stop()

	buildersLine := sess.BuildersLine(host, port, 4)
	extraArgs := []string{"--builders", buildersLine, "--max-jobs", "0"}
	extraEnv := []string{"NIX_SSHOPTS=" + containerbuilder.NixSSHOpts()}
	return buildImageStorePathArgs(repoRoot, extra, outLink, out, extraArgs, extraEnv)
}

// materializeImage ports _materialize_image: stream the nix image to cacheFile
// (via a temp + rename), returning the byte count (0 on failure).
func materializeImage(storePath, cacheFile string, isMacOS bool, out io.Writer, progressTTY bool) int64 {
	streamCmd := streamImageCommand(storePath, isMacOS)
	cmd := exec.Command(streamCmd[0], streamCmd[1:]...)
	cmd.Stderr = nil
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return 0
	}
	if err := cmd.Start(); err != nil {
		return 0
	}
	tmpFile := strings.TrimSuffix(cacheFile, ".tar") + ".tmp"
	f, err := os.Create(tmpFile)
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return 0
	}
	var total int64
	buf := make([]byte, 1024*1024)
	sentinel := SizeSentinelPath()
	estimated := estimateImageSize(storePath, sentinel)
	// Progress rendering (rich status.update — a SINGLE line
	// that redraws): on a TTY, redraw in place with \r (throttled to whole-
	// percent changes so a multi-GB stream doesn't emit hundreds of near-
	// identical updates); off a TTY, emit nothing per-chunk (a redirected log
	// must not accumulate 500 "98% 98% 99%" lines). A final newline closes the
	// redrawn line so the next message starts cleanly.
	prog := newProgressLine(out, progressTTY)
	for {
		n, rerr := stdout.Read(buf)
		if n > 0 {
			if _, werr := f.Write(buf[:n]); werr != nil {
				prog.done()
				f.Close()
				_ = os.Remove(tmpFile)
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
				return 0
			}
			total += int64(n)
			prog.update(total, estimated)
		}
		if rerr != nil {
			break
		}
	}
	prog.done()
	f.Close()
	_ = cmd.Wait()
	if cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != 0 {
		_ = os.Remove(tmpFile)
		return 0
	}
	if err := os.Rename(tmpFile, cacheFile); err != nil {
		_ = os.Remove(tmpFile)
		return 0
	}
	// Save size for future estimates (the writer path — see the doubled-suffix
	// quirk on SizeFileForSentinel).
	_ = os.WriteFile(sentinel, []byte(strconv.FormatInt(total, 10)), 0o644)
	return total
}

// progressLine renders the image-caching byte progress as a single, in-place
// updating line on a TTY (carriage return, like Python's rich status spinner),
// and as nothing per-chunk when piped (so a redirected log doesn't accumulate
// hundreds of near-identical "Caching image… 98%" lines). Updates are throttled
// to when the RENDERED string changes (whole-percent or MB/GB rollover), so a
// multi-GB stream produces ~100 redraws, not one per 1 MB chunk.
type progressLine struct {
	out    io.Writer
	tty    bool
	last   string
	shown  bool
	prefix string
}

func newProgressLine(out io.Writer, tty bool) *progressLine {
	return &progressLine{out: out, tty: tty, prefix: "Caching image... "}
}

func (p *progressLine) update(current, estimate int64) {
	if !p.tty {
		return // no per-chunk spam on a pipe/redirect
	}
	msg := p.prefix + FormatProgress(current, estimate)
	if msg == p.last {
		return // throttle: nothing visibly changed
	}
	p.last = msg
	p.shown = true
	// \r returns to column 0; trailing spaces clear any shorter previous line.
	fmt.Fprintf(p.out, "\r%s   ", msg)
}

// done closes the in-place line with a newline so the next message starts on a
// fresh line (only when something was drawn).
func (p *progressLine) done() {
	if p.tty && p.shown {
		fmt.Fprintln(p.out)
	}
}

// estimateImageSize ports _estimate_image_size: the cached size file (read via
// the doubled-suffix quirk path, which never exists), else the nix closure-size
// probe.
func estimateImageSize(storePath, sentinel string) int64 {
	if n, ok := ReadEstimatedSizeFile(SizeFileForSentinel(sentinel)); ok {
		return n
	}
	cmd := exec.Command("nix", "--extra-experimental-features", "nix-command flakes",
		"path-info", "--closure-size", storePath)
	data, err := cmd.Output()
	if err != nil {
		return 0
	}
	fields := strings.Fields(strings.TrimSpace(string(data)))
	for i := len(fields) - 1; i >= 0; i-- {
		if n, err := strconv.ParseInt(fields[i], 10, 64); err == nil {
			return n
		}
	}
	return 0
}

// streamImageCommand ports _stream_image_command: on Linux the store path IS
// the executable (its shebang streams the tar); the macOS remote-builder ssh
// path is a documented narrowing (falls back to local execution).
func streamImageCommand(storePath string, isMacOS bool) []string {
	if !isMacOS {
		return []string{storePath}
	}
	machines := "/etc/nix/machines"
	data, err := os.ReadFile(machines)
	if err != nil {
		return []string{storePath}
	}
	if _, sshHost, ok := LinuxBuilderFromMachines(string(data)); ok {
		// nix copy the closure to the builder, then run the script over ssh.
		copyCmd := exec.Command("nix", "copy", "--to", "ssh-ng://"+sshHost, storePath)
		if err := copyCmd.Run(); err != nil {
			return []string{storePath}
		}
		return []string{"ssh", sshHost, storePath}
	}
	return []string{storePath}
}

// loadImageForAppleContainer ports _load_image_for_apple_container: convert the
// nix V2 tar to OCI via skopeo (preferred) or podman, then load into Apple
// Container.
func loadImageForAppleContainer(tarPath string, out io.Writer) bool {
	if _, err := exec.LookPath("skopeo"); err == nil {
		return convertViaSkopeo(tarPath, out)
	}
	if _, err := exec.LookPath("podman"); err == nil {
		return convertViaDaemon("podman", tarPath, out)
	}
	fmt.Fprintln(out, "Cannot convert Nix image to OCI format for Apple Container.")
	fmt.Fprintln(out, "Install one of: skopeo (recommended, no daemon needed) or podman.")
	return false
}

func convertViaSkopeo(tarPath string, out io.Writer) bool {
	ociDir, err := os.MkdirTemp("", "yolo-oci-")
	if err != nil {
		return false
	}
	defer os.RemoveAll(ociDir)
	if err := exec.Command("skopeo", "copy",
		"docker-archive:"+tarPath, "oci:"+ociDir+":"+paths.JailImageShort).Run(); err != nil {
		fmt.Fprintln(out, "skopeo conversion to OCI failed.")
		return false
	}
	ociTar := tarPath + ".oci.tar"
	if err := exec.Command("tar", "cf", ociTar, "-C", ociDir, ".").Run(); err != nil {
		fmt.Fprintln(out, "Failed to create OCI tar.")
		return false
	}
	loadErr := exec.Command("container", "image", "load", "-i", ociTar).Run()
	_ = os.Remove(ociTar)
	if loadErr != nil {
		fmt.Fprintln(out, "Failed to load OCI image into Apple Container.")
		return false
	}
	return true
}

func convertViaDaemon(daemon, tarPath string, out io.Writer) bool {
	if err := exec.Command(daemon, "load", "-i", tarPath).Run(); err != nil {
		fmt.Fprintln(out, "Failed to load image into "+daemon+" for conversion.")
		return false
	}
	ociTar := tarPath + ".oci.tar"
	if err := exec.Command(daemon, "save", "--format", "oci-archive", "-o", ociTar, paths.JailImage).Run(); err != nil {
		fmt.Fprintln(out, "Failed to export OCI image from "+daemon+".")
		return false
	}
	loadErr := exec.Command("container", "image", "load", "-i", ociTar).Run()
	_ = os.Remove(ociTar)
	if loadErr != nil {
		fmt.Fprintln(out, "Failed to load OCI image into Apple Container.")
		return false
	}
	return true
}

// newestTars returns *.tar files in dir sorted newest-first by mtime. Empty when
// dir is missing.
func newestTars(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	type tf struct {
		path  string
		mtime int64
	}
	var tars []tf
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".tar") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		tars = append(tars, tf{filepath.Join(dir, e.Name()), info.ModTime().UnixNano()})
	}
	// newest first
	for i := 0; i < len(tars); i++ {
		for j := i + 1; j < len(tars); j++ {
			if tars[j].mtime > tars[i].mtime {
				tars[i], tars[j] = tars[j], tars[i]
			}
		}
	}
	out := make([]string, len(tars))
	for i, t := range tars {
		out[i] = t.path
	}
	return out
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
