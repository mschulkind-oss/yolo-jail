// Package integration holds yolo-jail's end-to-end tests: they drive the real
// `yolo` CLI against a real container runtime. Every test that touches a
// container calls requireJail(t) as its first line, which skips under
// `go test -short` (pre-commit, `just test-fast`, the check-go CI job); the
// full suite runs under `just test` and the CI integration job.
//
// The package is deliberately test-only (all files are *_test.go), so it stays
// outside the flake's goSrc fileset — editing a test never invalidates the jail
// image derivation — while still living inside the Go module, so it is covered
// by `go test`/`go vet`/staticcheck/gofmt and can import internal/runtime for
// the real container-name algorithm instead of a Python mirror that could drift.
//
// No test in this package calls t.Parallel(): container tests run serially,
// reproducing the Python suite's deliberate serial integration discipline (the
// session image load must not race across parallel workers).
package integration

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	naming "github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

const jailImage = "yolo-jail:latest"

// yoloBin is the `yolo` binary built once by TestMain; repoRoot is the module
// root. Both stay empty under `go test -short`, where no container test runs and
// nothing needs them.
var (
	yoloBin  string
	repoRoot string
)

// TestMain builds the CLI under test once and, when running inside a nested
// jail, ensures the image is loaded — then runs the suite. Under -short it does
// none of that (only the non-container fast tests run).
func TestMain(m *testing.M) {
	flag.Parse()
	if testing.Short() {
		os.Exit(m.Run())
	}

	ensureNixInPath()

	root, err := moduleRoot()
	if err != nil {
		log.Fatalf("integration: locating module root: %v", err)
	}
	repoRoot = root

	binDir, err := os.MkdirTemp("", "yolo-integration-")
	if err != nil {
		log.Fatalf("integration: creating temp bin dir: %v", err)
	}
	yoloBin = filepath.Join(binDir, "yolo")
	build := exec.Command("go", "build", "-o", yoloBin, "./cmd/yolo")
	build.Dir = repoRoot
	if out, err := build.CombinedOutput(); err != nil {
		os.RemoveAll(binDir)
		log.Fatalf("integration: building yolo CLI under test: %v\n%s", err, out)
	}

	ensureJailImage()

	code := m.Run()
	os.RemoveAll(binDir)
	os.Exit(code)
}

// moduleRoot returns the repository root — the parent of this file's directory
// (integration/) — via runtime.Caller so it is independent of the working
// directory the test binary is launched from.
func moduleRoot() (string, error) {
	_, thisFile, _, ok := goruntime.Caller(0)
	if !ok {
		return "", fmt.Errorf("runtime.Caller(0) failed")
	}
	return filepath.Dir(filepath.Dir(thisFile)), nil
}

// ensureNixInPath ports conftest _ensure_nix_in_path: on darwin, prepend the
// default nix profile bin to PATH (only if it exists and is not already there)
// so subprocesses that shell out to `nix build` can find it.
func ensureNixInPath() {
	if goruntime.GOOS != "darwin" {
		return
	}
	const nixBin = "/nix/var/nix/profiles/default/bin"
	path := os.Getenv("PATH")
	if strings.Contains(path, nixBin) {
		return
	}
	if _, err := os.Stat(nixBin); err != nil {
		return
	}
	os.Setenv("PATH", nixBin+string(os.PathListSeparator)+path)
}

// inContainer reports whether the process is running inside a container, using
// the same markers as the CLI and conftest.
func inContainer() bool {
	for _, marker := range []string{"/run/.containerenv", "/.dockerenv"} {
		if _, err := os.Stat(marker); err == nil {
			return true
		}
	}
	return false
}

// detectRuntime resolves the container runtime: $YOLO_RUNTIME wins; otherwise
// prefer `container` on darwin, then fall back to podman/container by PATH.
// Returns "" when none is available.
//
// This merges conftest's two runtime helpers (_detect_runtime, used for the
// image load, and _force_remove_container's env/platform selection) into one.
// Inside a jail both resolved to podman, so behavior is unchanged; honoring
// $YOLO_RUNTIME on the image-load path is a strict improvement.
func detectRuntime() string {
	if rt := os.Getenv("YOLO_RUNTIME"); rt != "" {
		return rt
	}
	if goruntime.GOOS == "darwin" {
		if _, err := exec.LookPath("container"); err == nil {
			return "container"
		}
	}
	for _, rt := range []string{"podman", "container"} {
		if _, err := exec.LookPath(rt); err == nil {
			return rt
		}
	}
	return ""
}

// imageExists probes for the jail image under both its bare and localhost/ tags.
func imageExists(rt string) bool {
	for _, name := range []string{jailImage, "localhost/" + jailImage} {
		if exec.Command(rt, "image", "inspect", name).Run() == nil {
			return true
		}
	}
	return false
}

// ensureJailImage ports conftest ensure_jail_image: when the suite runs inside a
// Linux container (the nested-jail case), the inner runtime has its own image
// store that cannot see the host's, so build .#ociImage and load it. It is a
// no-op on darwin, outside a container, or when the image is already present.
// A build failure is fatal (tests cannot run); a load failure is a warning
// (tests may skip).
func ensureJailImage() {
	if goruntime.GOOS == "darwin" || !inContainer() {
		return
	}
	rt := detectRuntime()
	if rt == "" {
		log.Println("[integration] no container runtime (podman/container) found; skipping image load")
		return
	}
	if imageExists(rt) {
		return
	}
	if err := exec.Command(rt, "info", "--format", "{{.Store.GraphRoot}}").Run(); err != nil {
		log.Println("[integration] container runtime storage unavailable (read-only filesystem?) — integration tests may be skipped")
		return
	}

	log.Printf("[integration] loading %s into inner %s (this may take a minute)...", jailImage, rt)
	outLink := filepath.Join(repoRoot, ".run-result")
	build := exec.Command("nix", "--extra-experimental-features", "nix-command flakes",
		"build", ".#ociImage", "--impure", "--out-link", outLink)
	build.Dir = repoRoot
	if out, err := build.CombinedOutput(); err != nil {
		log.Fatalf("integration: nix build failed inside jail — cannot load %s: %v\n%s\n"+
			"Ensure the host nix daemon socket is mounted (/nix/var/nix/daemon-socket) "+
			"and NIX_REMOTE=daemon is set.", jailImage, err, out)
	}
	defer os.Remove(outLink)

	resolved, err := filepath.EvalSymlinks(outLink)
	if err != nil {
		log.Printf("[integration] cannot resolve %s: %v — image not loaded", outLink, err)
		return
	}

	// The out-link is a script that streams a docker-archive to stdout; pipe it
	// into `<runtime> load` (mirrors conftest's Popen pipe, no shell needed).
	stream := exec.Command(resolved)
	load := exec.Command(rt, "load")
	pipe, err := stream.StdoutPipe()
	if err != nil {
		log.Printf("[integration] wiring image stream pipe failed: %v", err)
		return
	}
	load.Stdin = pipe
	var loadOut bytes.Buffer
	load.Stdout = &loadOut
	load.Stderr = &loadOut
	if err := stream.Start(); err != nil {
		log.Printf("[integration] starting image stream failed: %v", err)
		return
	}
	if err := load.Start(); err != nil {
		log.Printf("[integration] starting %s load failed: %v", rt, err)
		_ = stream.Process.Kill()
		_ = stream.Wait()
		return
	}
	loadErr := load.Wait()
	streamErr := stream.Wait()
	if streamErr != nil || loadErr != nil {
		log.Printf("[integration] %s load failed (integration tests may be skipped): stream=%v load=%v\n%s",
			rt, streamErr, loadErr, strings.TrimSpace(loadOut.String()))
		return
	}
	log.Printf("[integration] %s", strings.TrimSpace(loadOut.String()))
}

// defaultJailTimeoutSeconds is the per-invocation deadline for a single
// `yolo -- <cmd>` call. Cold start on a fresh runner (image pull, container
// create, mise install, loophole spawn, entrypoint config generation) runs well
// over two minutes; 300s gives headroom while still catching a genuinely hung
// container. YOLO_TEST_JAIL_TIMEOUT overrides it for slow environments (the
// macOS nightly sets 1200).
const defaultJailTimeoutSeconds = 300

// jailTimeout returns the per-command deadline from YOLO_TEST_JAIL_TIMEOUT
// (integer seconds) or defaultJailTimeoutSeconds.
func jailTimeout() time.Duration {
	if v := os.Getenv("YOLO_TEST_JAIL_TIMEOUT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return time.Duration(n) * time.Second
		}
	}
	return defaultJailTimeoutSeconds * time.Second
}

// childRepoRootEnv tells the spawned CLI where the yolo-jail repo is.
//
// The CLI needs the repo root for nix image builds, and resolves it by walking
// UP from its working directory looking for a dir with both flake.nix and
// go.mod. This harness deliberately defeats that walk: the binary is built into
// an os.MkdirTemp dir and every test runs it with cmd.Dir set to a t.TempDir()
// workspace, so the walk finds nothing and the CLI dies with "Cannot find
// yolo-jail repo root" — which is what took out the entire Linux integration
// job (not just the nix-building tests: `yolo check` reports it as a failed
// check too). The Python suite never hit this because it invoked the CLI from
// the repo.
//
// TestMain already knows the answer — moduleRoot() derives it from
// runtime.Caller, independent of any cwd — so hand it to the child. A real
// YOLO_REPO_ROOT in the environment (set inside jails and by CI) wins: it is
// the CLI's own first-choice source and may legitimately differ from this
// checkout, e.g. the /opt/yolo-jail bind in a nested jail.
func childRepoRootEnv() []string {
	if repoRoot == "" || os.Getenv("YOLO_REPO_ROOT") != "" {
		return nil
	}
	return []string{"YOLO_REPO_ROOT=" + repoRoot}
}

// result is the outcome of a yolo invocation.
type result struct {
	rc     int
	stdout string
	stderr string
}

func (r result) combined() string { return r.stdout + r.stderr }

type runConfig struct{ timeout time.Duration }

type runOption func(*runConfig)

// withTimeout overrides the default per-command deadline (e.g. the 600s
// mise-venv activation case).
func withTimeout(d time.Duration) runOption {
	return func(c *runConfig) { c.timeout = d }
}

// runCommand runs the built yolo binary with the given args in dir, capturing
// stdout and stderr separately. The run is bounded by jailTimeout() (overridable
// via withTimeout); on deadline expiry it force-removes the workspace's
// container before failing the test, so a hung run leaves no orphan (ports
// run_yolo's TimeoutExpired handler).
func runCommand(t *testing.T, dir string, args []string, opts ...runOption) result {
	t.Helper()
	cfg := runConfig{timeout: jailTimeout()}
	for _, o := range opts {
		o(&cfg)
	}

	ctx, cancel := context.WithTimeout(context.Background(), cfg.timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, yoloBin, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "TERM=dumb")
	cmd.Env = append(cmd.Env, childRepoRootEnv()...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		forceRemoveContainer(dir)
		t.Fatalf("yolo timed out after %s: yolo %s", cfg.timeout, strings.Join(args, " "))
	}

	rc := 0
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			rc = exitErr.ExitCode()
		} else {
			t.Fatalf("yolo failed to start (yolo %s): %v", strings.Join(args, " "), err)
		}
	}
	return result{rc: rc, stdout: stdout.String(), stderr: stderr.String()}
}

// runYolo runs a shell script inside the jail via a login shell:
// `yolo run -- bash -lc <script>`.
func runYolo(t *testing.T, dir, script string, opts ...runOption) result {
	t.Helper()
	return runCommand(t, dir, []string{"run", "--", "bash", "-lc", script}, opts...)
}

// runYoloDirect runs a command directly (`yolo run -- <args...>`), NOT wrapped
// in bash -lc — exercising the non-login-shell PATH setup, the path that once
// broke `yolo -- copilot` with "command not found".
func runYoloDirect(t *testing.T, dir string, args ...string) result {
	t.Helper()
	return runCommand(t, dir, append([]string{"run", "--"}, args...))
}

// runYoloCLI runs a host-side yolo subcommand directly (e.g. `yolo check
// --no-build`), without entering a jail.
func runYoloCLI(t *testing.T, dir string, args ...string) result {
	t.Helper()
	return runCommand(t, dir, args)
}

// forceRemoveContainer removes the jail container for a workspace dir, deriving
// the name from the real algorithm (internal/runtime.FromWorkspace) rather than
// a mirror. Errors are ignored; the legacy hash-only name is not tried (it named
// pre-rename Python-era containers only).
func forceRemoveContainer(dir string) {
	rt := detectRuntime()
	if rt == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = exec.CommandContext(ctx, rt, "rm", "-f", naming.FromWorkspace(dir)).Run()
}

// writeProject creates a temp workspace containing yolo-jail.jsonc with the
// given JSONC body and registers container cleanup, returning the workspace dir.
func writeProject(t *testing.T, configJSON string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "yolo-jail.jsonc"), []byte(configJSON), 0o644); err != nil {
		t.Fatalf("writing yolo-jail.jsonc: %v", err)
	}
	t.Cleanup(func() { forceRemoveContainer(dir) })
	return dir
}

// tempProjectConfig is the standard fixture config (ported from conftest's
// temp_project): all three legacy agents selected explicitly (the library-model
// default is claude-only, but many tests assert copilot/gemini configs), a curl
// block plus a custom-message grep block, and bridge networking.
const tempProjectConfig = `{
  "agents": ["copilot", "gemini", "claude"],
  "security": {
    "blocked_tools": [
      "curl",
      {"name": "grep", "message": "NO GREP ALLOWED", "suggestion": "use rg"}
    ]
  },
  "network": {"mode": "bridge"}
}`

// tempProject creates a workspace with the standard fixture config.
func tempProject(t *testing.T) string {
	t.Helper()
	return writeProject(t, tempProjectConfig)
}

// section returns the slice of s strictly between the first occurrence of start
// and the next occurrence of end (end=="" means "to the end of s"). Empty if
// start is absent. Used by the merged multi-probe integration tests: one jail
// launch runs several fenced probes (each preceded by `echo "=== NAME ==="`),
// and section splits the combined stdout back into per-probe chunks so each
// assertion stays independent.
func section(s, start, end string) string {
	i := strings.Index(s, start)
	if i < 0 {
		return ""
	}
	i += len(start)
	if end == "" {
		return s[i:]
	}
	j := strings.Index(s[i:], end)
	if j < 0 {
		return s[i:]
	}
	return s[i : i+j]
}

// requireJail skips the calling test under `go test -short`. Every test that
// creates a container must call it first.
func requireJail(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping container integration test (-short)")
	}
}

// skipIfCgroupReadonly skips when cgroup v2 is absent or read-only (e.g. a
// nested jail), probing with an mkdir/rmdir under /sys/fs/cgroup.
func skipIfCgroupReadonly(t *testing.T) {
	t.Helper()
	const cgroupRoot = "/sys/fs/cgroup"
	if _, err := os.Stat(cgroupRoot); err != nil {
		t.Skip("cgroup v2 not available")
	}
	probe := filepath.Join(cgroupRoot, ".yolo-test-probe")
	if err := os.Mkdir(probe, 0o755); err != nil {
		t.Skip("cgroup filesystem is read-only (nested jail?)")
	}
	_ = os.Remove(probe)
}

// skipIfInContainer skips tests that deadlock under podman-in-podman (the mise
// re-entrant shim case).
func skipIfInContainer(t *testing.T) {
	t.Helper()
	if inContainer() {
		t.Skip("mise has a re-entrant shim deadlock in nested containers (podman-in-podman)")
	}
}
