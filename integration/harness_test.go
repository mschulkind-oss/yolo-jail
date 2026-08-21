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

	"github.com/mschulkind-oss/yolo-jail/internal/config"
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
	warmJail()

	code := m.Run()
	os.RemoveAll(binDir)
	os.Exit(code)
}

// warmJail pays the suite's ONE-TIME container costs here, where nothing is being
// timed, instead of inside whichever test happens to run first.
//
// The defect it exists for (docs/design/agent-install-in-ci.md §4.1, measured on CI run
// 32419507352): the first container test absorbs podman's first-container create, the
// entrypoint's mise install/upgrade, and bootstrap's MCP npm downloads — and is then judged
// against YOLO_TEST_JAIL_TIMEOUT, a PER-COMMAND cap sized for steady-state work.
// TestAgentToolsAvailable sorts first, so on x64 it cost 124.5s for two installs that cost
// ~12s each anywhere later in the same run; on the macOS nightly it cost 1033s of a 1200s
// cap on a healthy night, and blew the cap the first time the runner ran 1.6x slow.
//
// This is a MEASUREMENT fix, not a cost fix: the suite's wall clock barely moves, because
// the warmup still costs whatever it costs. What changes is that no single test's budget
// contains it, so the cap measures what it was sized for and per-test durations become
// comparable to each other. Widening the cap instead was considered and rejected — it
// preserves the misattribution and has to be re-widened every time warmup grows.
//
// It runs under its OWN redirected HOME (seedPackHome, shared with packHome) carrying an
// EMPTY user config, and that is not optional. The first version used the ambient HOME and
// died instantly on a developer machine: the real config selected a local pack at
// /home/matt/.dotfiles/..., a HOST path that does not exist inside the jail. CI would never
// have caught it — a fresh runner's HOME has no yolo config at all — so this is the rare
// defect that is invisible in CI and fatal locally. An empty config also means no packs are
// selected, so the warmup installs no agent CLI; the npm HTTP cache IS shared
// (paths.GlobalCache()), so bootstrap's own downloads still land warm for every later test.
//
// FAILURE IS NON-FATAL AND MUST STAY THAT WAY. A warmup is an attribution fix, not a gate:
// if it cannot launch, every test still runs and still reports its own diagnosis against its
// own assertions. Making this fatal would convert one unexplained environment problem into a
// suite that refuses to say anything at all.
func warmJail() {
	if detectRuntime() == "" {
		return // ensureJailImage already reported the absence
	}
	dir, err := os.MkdirTemp("", "yolo-warmup-")
	if err != nil {
		degraded("warmup: creating temp workspace: %v — the first container test will absorb "+
			"the suite's one-time costs", err)
		return
	}
	defer os.RemoveAll(dir)
	if err := os.WriteFile(filepath.Join(dir, "yolo-jail.jsonc"), []byte("{}"), 0o644); err != nil {
		degraded("warmup: writing workspace config: %v", err)
		return
	}
	home := filepath.Join(dir, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		degraded("warmup: creating temp home: %v", err)
		return
	}
	if err := seedPackHome(home, os.Getenv("HOME"), `{}`); err != nil {
		degraded("warmup: seeding temp home: %v", err)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), jailTimeout())
	defer cancel()
	args := append(jailRunArgs(), "--", "bash", "-lc", "true")
	cmd := exec.CommandContext(ctx, yoloBin, args...)
	cmd.Dir = dir
	// HOME goes to the SUBPROCESS only — never os.Setenv — because this runs before
	// m.Run() and a process-wide HOME would silently redirect every test that expects the
	// ambient one.
	cmd.Env = append(os.Environ(), "TERM=dumb", "HOME="+home)
	cmd.Env = append(cmd.Env, childRepoRootEnv()...)

	start := time.Now()
	out, err := cmd.CombinedOutput()
	elapsed := time.Since(start).Round(time.Second)
	forceRemoveContainer(dir)

	if err != nil {
		degraded("warmup jail failed after %s (%v) — tests still run, but the first "+
			"container test will absorb the suite's one-time costs:\n%s", elapsed, err, out)
		return
	}
	// Printed unconditionally: this number is the whole point of the change, and the only
	// way to tell from a CI log whether warmup is actually absorbing anything.
	log.Printf("[integration] warmed the jail in %s — one-time container costs are paid "+
		"here, outside any test's timing", elapsed)
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

// imageExists probes for the jail image under both its bare and localhost/ tags,
// returning the tag that answered ("" when neither did) — callers need the name
// to probe the image itself (see checkImageSkew), not just a yes/no.
func imageExists(rt string) string {
	for _, name := range []string{jailImage, "localhost/" + jailImage} {
		if exec.Command(rt, "image", "inspect", name).Run() == nil {
			return name
		}
	}
	return ""
}

// ensureJailImage makes a jail image available AND verifies it matches the source
// tree under test.
//
// Inside a Linux container (the nested-jail case) the inner runtime has its own
// image store that cannot see the host's, so an absent image is built from
// .#ociImage and loaded. A build failure is fatal (tests cannot run); a load
// failure is degraded (tests may skip).
//
// The image-already-present short-circuit STAYS — a nix image build is minutes
// and the suite has to stay usable — but it is no longer blind: whatever image we
// end up with is handed to checkImageSkew, which compares it against the source
// tree and by default aborts the suite rather than let a stale image masquerade as
// a regression in new code (see imageskew_test.go for the mechanism). Set
// YOLO_TEST_REBUILD_IMAGE=1 to force a rebuild+reload instead of short-circuiting.
//
// Every early return reports through degraded(): a harness that silently gives up
// on loading or checking the image is exactly how stale-image debugging starts.
func ensureJailImage() {
	rt := detectRuntime()
	if rt == "" {
		degraded("no container runtime (podman/container) found — no image load, no " +
			"staleness check, and every container test will fail or skip")
		return
	}

	// Outside a container (a real host, incl. darwin) the image is the host's own,
	// managed by `just load` / a CI load step — the suite must not build over it.
	// Checking it for skew is still both possible and worthwhile.
	if !inContainer() {
		if name := imageExists(rt); name != "" {
			checkImageSkew(rt, name)
		} else {
			degraded("no %s image in %s and not inside a container (so the suite will "+
				"not build one) — container tests will fail; run `just load`", jailImage, rt)
		}
		return
	}

	forceRebuild := os.Getenv(rebuildEnv) != ""
	if name := imageExists(rt); name != "" && !forceRebuild {
		checkImageSkew(rt, name)
		return
	}
	if err := exec.Command(rt, "info", "--format", "{{.Store.GraphRoot}}").Run(); err != nil {
		degraded("%s storage unavailable (read-only filesystem?) — cannot load an "+
			"image; integration tests may be skipped", rt)
		return
	}
	if forceRebuild {
		log.Printf("[integration] %s set — rebuilding and reloading %s", rebuildEnv, jailImage)
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
		degraded("cannot resolve %s: %v — image not loaded", outLink, err)
		return
	}

	// The out-link is a script that streams a docker-archive to stdout; pipe it
	// into `<runtime> load` (mirrors conftest's Popen pipe, no shell needed).
	stream := exec.Command(resolved)
	load := exec.Command(rt, "load")
	pipe, err := stream.StdoutPipe()
	if err != nil {
		degraded("wiring image stream pipe failed: %v", err)
		return
	}
	load.Stdin = pipe
	var loadOut bytes.Buffer
	load.Stdout = &loadOut
	load.Stderr = &loadOut
	if err := stream.Start(); err != nil {
		degraded("starting image stream failed: %v", err)
		return
	}
	if err := load.Start(); err != nil {
		degraded("starting %s load failed: %v", rt, err)
		_ = stream.Process.Kill()
		_ = stream.Wait()
		return
	}
	loadErr := load.Wait()
	streamErr := stream.Wait()
	if streamErr != nil || loadErr != nil {
		degraded("%s load failed (integration tests may be skipped): stream=%v load=%v\n%s",
			rt, streamErr, loadErr, strings.TrimSpace(loadOut.String()))
		return
	}
	log.Printf("[integration] %s", strings.TrimSpace(loadOut.String()))

	// Verify what we just loaded. A build+load that succeeded can still leave a
	// mismatched image — most commonly because nix only sees git-TRACKED files, so
	// a brand-new untracked file is absent from the image the suite is about to
	// test. Checking after the load is what makes that case loud instead of a
	// puzzling test failure.
	if name := imageExists(rt); name != "" {
		checkImageSkew(rt, name)
	} else {
		degraded("%s reported a successful load but no %s image is present", rt, jailImage)
	}
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
// YOLO_REPO_ROOT in the environment (set by CI) wins: it is the CLI's own
// first-choice source and may legitimately differ from this checkout.
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
	res := result{rc: rc, stdout: stdout.String(), stderr: stderr.String()}
	// Attribute a failed image build to the image build, right here, before the
	// caller asserts anything about a jail that was never built from this source
	// tree (see imagebuildfailure_test.go for why this cannot live in TestMain's
	// skew check). Every run* helper funnels through here, so no test opts in.
	failIfImageBuildFailed(t, args, res)
	return res
}

// runYolo runs a shell script inside the jail via a login shell:
// `yolo run -- bash -lc <script>`.
func runYolo(t *testing.T, dir, script string, opts ...runOption) result {
	t.Helper()
	return runCommand(t, dir, append(jailRunArgs(), "--", "bash", "-lc", script), opts...)
}

// jailRunArgs is the argv prefix every jail-launching helper shares:
// `run --accept-config-changes`.
//
// The flag is not incidental. Since docs/design/config-safety.md OQ-D2, a launch
// with a CHANGED config and no terminal to approve it on is REFUSED, and this
// harness is that launch by definition — `cmd.Stdin` is never a tty. Several tests
// rewrite yolo-jail.jsonc between two launches of the same workspace on purpose
// (TestShimPersistence is the canonical one), and without the flag the second
// launch would refuse instead of applying the edit. Passing it is the scripted
// caller doing exactly what the ruling asks: saying out loud, per launch, that it
// means the new config.
func jailRunArgs() []string {
	return []string{"run", config.AcceptConfigChangesFlag}
}

// runYoloDirect runs a command directly (`yolo run -- <args...>`), NOT wrapped
// in bash -lc — exercising the non-login-shell PATH setup, the path that once
// broke `yolo -- copilot` with "command not found".
func runYoloDirect(t *testing.T, dir string, args ...string) result {
	t.Helper()
	return runCommand(t, dir, append(append(jailRunArgs(), "--"), args...))
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
	if strings.Contains(configJSON, `"packs"`) {
		t.Fatalf("writeProject got a `packs` key, which is USER SCOPE ONLY — a workspace "+
			"config naming one is a `yolo check` error. Use writeProjectWithPacks so the "+
			"key lands in the user config where it is read:\n%s", configJSON)
	}
	t.Cleanup(func() { forceRemoveContainer(dir) })
	return dir
}

// writeProjectWithPacks is writeProject for a test that needs specific packs active.
//
// The SPLIT is forced by scope, not preference: `packs` is read from
// ~/.config/yolo-jail/config.jsonc directly and never from a workspace config, because a
// workspace config travels with the repo and is agent-editable — so it must not decide what
// content an agent then follows. A fixture that wrote `packs` into yolo-jail.jsonc would
// fail `yolo check` rather than select anything, and writeProject now says so outright.
//
// packNames are BARE embedded-pack names ("claude", "codex"), which is how the packs yolo
// ships are selected; nothing is active by default.
func writeProjectWithPacks(t *testing.T, workspaceConfig string, packNames ...string) string {
	t.Helper()
	quoted := make([]string, len(packNames))
	for i, n := range packNames {
		quoted[i] = `"` + n + `"`
	}
	packHome(t, `{"packs": [`+strings.Join(quoted, ", ")+`]}`)
	return writeProject(t, workspaceConfig)
}

// tempProjectConfig is the standard fixture WORKSPACE config (ported from conftest's
// temp_project): a curl block plus a custom-message grep block, and bridge networking.
//
// The pack selection is NOT here — it moved to the user config (tempProjectPacks), because
// `packs` is user-scope only. See writeProjectWithPacks.
const tempProjectConfig = `{
  "security": {
    "blocked_tools": [
      "curl",
      {"name": "grep", "message": "NO GREP ALLOWED", "suggestion": "use rg"}
    ]
  },
  "network": {"mode": "bridge"}
}`

// tempProjectPacks is the standard fixture's pack selection: the three tools whose configs
// the suite asserts. Nothing is active by default, so a fixture that needs a tool's config
// file to exist has to name its pack.
var tempProjectPacks = []string{"copilot", "codex", "claude"}

// tempProject creates a workspace with the standard fixture config and pack selection.
func tempProject(t *testing.T) string {
	t.Helper()
	return writeProjectWithPacks(t, tempProjectConfig, tempProjectPacks...)
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
