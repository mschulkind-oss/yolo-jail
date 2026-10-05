package cli

// hostblockers_test.go pins HE-D11 (docs/design/host-launch-environment.md) at its call site,
// hostLaunch, through hostExec: the blocked tools a jail would generate are put first on the PATH
// of the program `yolo host --` starts, the shims work there (the guardrails message and 127 for a
// blocked use, the real tool for the rest and under YOLO_BYPASS_SHIMS=1), and nothing changes for
// a launch with nothing to block. Delete the hostBlockedChildPath line in hostLaunch and the first
// test fails: the child's PATH no longer starts with a block dir.

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// blockerHostFixture is a hermetic `yolo host` home with cfg as the user config, a working
// directory of its own, and a PATH of a bin dir holding the fake agent and the given fake
// binaries, then a folder holding links to the real sh, grep and find and nothing else — so
// whether rg or fd is "on PATH" is the fixture's choice, not the machine's (this repo's own jail
// bakes both into /bin).
func blockerHostFixture(t *testing.T, cfg string, fakes ...string) (home, bin string) {
	t.Helper()
	resolved := func() string {
		d, err := filepath.EvalSymlinks(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	home = resolved()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("YOLO_BYPASS_SHIMS", "")
	t.Chdir(resolved())
	userCfg(t, home, cfg)
	orig := prepareOpenAIAuthHost
	prepareOpenAIAuthHost = func(hostPrelaunch, io.Writer) (managedOpenAIHostLaunch, error) { return nil, nil }
	t.Cleanup(func() { prepareOpenAIAuthHost = orig })
	bin = filepath.Join(resolved(), "bin")
	for _, name := range append([]string{"yolo-fake-agent"}, fakes...) {
		writeFile(t, filepath.Join(bin, name), "#!/bin/sh\nexit 0\n")
		if err := os.Chmod(filepath.Join(bin, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	sys := filepath.Join(resolved(), "sys")
	if err := os.MkdirAll(sys, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"sh", "grep", "find"} {
		real := ""
		for _, dir := range []string{"/usr/bin", "/bin"} {
			if fi, err := os.Stat(filepath.Join(dir, name)); err == nil && fi.Mode()&0o111 != 0 {
				real = filepath.Join(dir, name)
				break
			}
		}
		if real == "" {
			t.Skipf("no %s in /usr/bin or /bin", name)
		}
		if err := os.Symlink(real, filepath.Join(sys, name)); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+sys)
	return home, bin
}

// launchCapturing runs `yolo host -- yolo-fake-agent` and returns what the exec was handed.
func launchCapturing(t *testing.T) (*execCapture, string) {
	t.Helper()
	got := captureHostExec(t)
	var errw bytes.Buffer
	if rc := hostExec(nil, []string{"yolo-fake-agent"}, io.Discard, &errw, nil); rc != 0 || !got.execed {
		t.Fatalf("hostExec rc=%d execed=%v:\n%s", rc, got.execed, errw.String())
	}
	return got, errw.String()
}

// runWithEnv runs sh -c script in dir with env, and returns its exit code and combined output.
func runWithEnv(t *testing.T, env []string, dir, script string) (int, string) {
	t.Helper()
	cmd := exec.Command("/bin/sh", "-c", script)
	cmd.Env, cmd.Dir = env, dir
	out, err := cmd.CombinedOutput()
	if ee, ok := err.(*exec.ExitError); ok {
		return ee.ExitCode(), string(out)
	}
	if err != nil {
		t.Fatalf("sh -c %q: %v", script, err)
	}
	return 0, string(out)
}

func TestHostLaunchPutsTheGuardrailsBlockersFirstOnTheChildsPath(t *testing.T) {
	_, bin := blockerHostFixture(t, `{"packs": ["guardrails"]}`, "rg", "fd")
	got, errs := launchCapturing(t)

	childPath := envValue(got.env, "PATH")
	entries := filepath.SplitList(childPath)
	if len(entries) == 0 || filepath.Dir(entries[0]) != paths.HostBlockDir() {
		t.Fatalf("the child's PATH does not start with a block dir under %s: %s",
			paths.HostBlockDir(), childPath)
	}
	blockDir := entries[0]
	for _, name := range []string{"grep", "find"} {
		if fi, err := os.Stat(filepath.Join(blockDir, name)); err != nil || fi.Mode().Perm()&0o111 == 0 {
			t.Errorf("no executable %s shim in %s (%v)", name, blockDir, err)
		}
	}
	// The rest of the PATH is OQ-HE10's, untouched: the caller's, then the floor's bin/.
	if rest := strings.Join(entries[1:], string(os.PathListSeparator)); rest !=
		hostChildPath(hostLaunchPath(), hostFloorBinDir()) {
		t.Errorf("the PATH after the block dir = %s, want the unblocked child PATH %s", rest,
			hostChildPath(hostLaunchPath(), hostFloorBinDir()))
	}
	if !strings.HasPrefix(entries[1], bin) {
		t.Errorf("the caller's PATH does not follow the block dir: %s", childPath)
	}
	for _, want := range []string{"blocking grep, find (the guardrails pack) for this launch",
		"YOLO_BYPASS_SHIMS=1", "path_helper", "/etc/profile", "Claude Code's Bash tool"} {
		if !strings.Contains(errs, want) {
			t.Errorf("the launch did not say %q:\n%s", want, errs)
		}
	}

	// The shims work in the environment the child is handed.
	work := t.TempDir()
	writeFile(t, filepath.Join(work, "hay.txt"), "the needle is here\n")
	rc, out := runWithEnv(t, got.env, work, "grep -r needle .")
	if rc != 127 || !strings.Contains(out, "grep's recursive mode is blocked") {
		t.Errorf("grep -r: rc=%d, want 127 and the guardrails message:\n%s", rc, out)
	}
	rc, out = runWithEnv(t, got.env, work, "find .")
	if rc != 127 || !strings.Contains(out, "find is blocked") {
		t.Errorf("find: rc=%d, want 127 and the guardrails message:\n%s", rc, out)
	}
	rc, out = runWithEnv(t, got.env, work, "grep needle hay.txt")
	if rc != 0 || !strings.Contains(out, "the needle is here") {
		t.Errorf("a single-file grep did not reach the real grep: rc=%d\n%s", rc, out)
	}
	rc, out = runWithEnv(t, append(append([]string{}, got.env...), "YOLO_BYPASS_SHIMS=1"), work,
		"grep -r needle .")
	if rc != 0 || !strings.Contains(out, "the needle is here") {
		t.Errorf("YOLO_BYPASS_SHIMS=1 grep -r did not reach the real grep: rc=%d\n%s", rc, out)
	}
}

// Two launches with the same blockers share one directory: its name is its content.
func TestHostLaunchReusesTheBlockDirOfTheSameBlockers(t *testing.T) {
	blockerHostFixture(t, `{"packs": ["guardrails"]}`, "rg", "fd")
	first, _ := launchCapturing(t)
	second, _ := launchCapturing(t)
	a := filepath.SplitList(envValue(first.env, "PATH"))[0]
	b := filepath.SplitList(envValue(second.env, "PATH"))[0]
	if a != b {
		t.Errorf("two launches with one set of blockers used two dirs: %s and %s", a, b)
	}
	if left, _ := filepath.Glob(filepath.Join(paths.HostBlockDir(), ".tmp-*")); len(left) > 0 {
		t.Errorf("a write left its temporary dir behind: %v", left)
	}
}

// Nothing blocked: the child's PATH is exactly OQ-HE10's, and nothing is said or written.
func TestHostLaunchWithNothingBlockedLeavesThePathAsItWas(t *testing.T) {
	blockerHostFixture(t, `{"packs": []}`, "rg", "fd")
	got, errs := launchCapturing(t)
	if p, want := envValue(got.env, "PATH"), hostChildPath(hostLaunchPath(), hostFloorBinDir()); p != want {
		t.Errorf("child PATH = %s, want %s", p, want)
	}
	if strings.Contains(errs, "blocking") {
		t.Errorf("a launch with nothing to block said it blocks something:\n%s", errs)
	}
	if _, err := os.Stat(paths.HostBlockDir()); err == nil {
		t.Errorf("a launch with nothing to block wrote %s", paths.HostBlockDir())
	}
}

// A replacement missing from the PATH leaves its tool unblocked, as in a jail, and says so with
// the host's next step.
func TestHostLaunchDoesNotBlockAToolWhoseReplacementIsMissing(t *testing.T) {
	blockerHostFixture(t, `{"packs": ["guardrails"]}`, "fd")
	got, errs := launchCapturing(t)
	if !strings.Contains(errs, "not blocking grep: its replacement rg is not on this launch's PATH") ||
		!strings.Contains(errs, "`host_path`") {
		t.Errorf("no line named the missing replacement and the fix:\n%s", errs)
	}
	blockDir := filepath.SplitList(envValue(got.env, "PATH"))[0]
	if _, err := os.Stat(filepath.Join(blockDir, "grep")); err == nil {
		t.Errorf("grep was blocked although rg is missing: %s", blockDir)
	}
	if _, err := os.Stat(filepath.Join(blockDir, "find")); err != nil {
		t.Errorf("find was not blocked although fd is present: %v", err)
	}
}

// The user scope's own list is honored; a WORKSPACE's yolo-jail.jsonc is never read at the host.
func TestHostLaunchBlocksTheUserScopeListAndNeverTheWorkspaces(t *testing.T) {
	blockerHostFixture(t, `{"packs": [], "security": {"blocked_tools": ["curl"]}}`)
	got, errs := launchCapturing(t)
	blockDir := filepath.SplitList(envValue(got.env, "PATH"))[0]
	if _, err := os.Stat(filepath.Join(blockDir, "curl")); err != nil {
		t.Fatalf("the user scope's curl block is not on the child's PATH (%v):\n%s", err, errs)
	}
	if !strings.Contains(errs, "curl (your security.blocked_tools)") {
		t.Errorf("the disclosure does not name the user's own list:\n%s", errs)
	}

	blockerHostFixture(t, `{"packs": []}`)
	writeFile(t, "yolo-jail.jsonc", `{"security": {"blocked_tools": ["curl"]}}`)
	got, errs = launchCapturing(t)
	if p := envValue(got.env, "PATH"); strings.Contains(p, paths.HostBlockDir()) {
		t.Errorf("a workspace's blocked_tools reached the host launch: %s\n%s", p, errs)
	}
}

// A block dir an outer launch handed down is taken out, and this launch's own goes first.
func TestHostLaunchStripsAnotherLaunchsBlockDir(t *testing.T) {
	_, bin := blockerHostFixture(t, `{"packs": ["guardrails"]}`, "rg", "fd")
	stale := filepath.Join(paths.HostBlockDir(), "0123456789abcdef")
	if err := os.MkdirAll(stale, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", stale+string(os.PathListSeparator)+os.Getenv("PATH"))
	got, _ := launchCapturing(t)
	entries := filepath.SplitList(envValue(got.env, "PATH"))
	n := 0
	for _, e := range entries {
		if filepath.Dir(e) == paths.HostBlockDir() {
			n++
			if e == stale {
				t.Errorf("the outer launch's block dir %s is still on the child's PATH", stale)
			}
		}
	}
	if n != 1 || filepath.Dir(entries[0]) != paths.HostBlockDir() || !strings.HasPrefix(entries[1], bin) {
		t.Errorf("want exactly one block dir, first, then the caller's PATH: %v", entries)
	}
}

// In a jail `yolo host` writes no host block dir: the jail's own are already on the PATH it hands
// down unchanged. The same call, on the same PATH (the fixture's, holding rg and fd), does write
// one outside a jail, so the guard is what makes the difference.
func TestHostLaunchInAJailWritesNoBlockDir(t *testing.T) {
	blockerHostFixture(t, `{"packs": ["guardrails"]}`, "rg", "fd")
	launch := composeHostLaunch("yolo-fake-agent", "", nil, func(string) {})
	childPath := os.Getenv("PATH")
	if b := composeHostBlockers(launch.cfg, launch.packs, childPath); b.dir == "" ||
		!strings.HasPrefix(b.path, b.dir+string(os.PathListSeparator)) {
		t.Fatalf("setup: outside a jail the guardrails blockers wrote no dir: %+v", b)
	}
	t.Setenv("YOLO_VERSION", "test")
	if got := composeHostBlockers(launch.cfg, launch.packs, childPath); got.dir != "" || got.path != childPath {
		t.Errorf("in a jail the blockers were written: %+v", got)
	}
}

// THE HATCH RUNS THE REAL PROGRAM BEHIND EVERY BLOCK at the host (HE-D12), not grep and find
// alone: the disclosure says YOLO_BYPASS_SHIMS=1 lets a command through, and a user's own entry
// under the hatch used to skip its refusal and run nothing, exiting 0, so `YOLO_BYPASS_SHIMS=1
// curl …` in an agent's script "succeeded" without a byte fetched. A blocked name with nothing
// behind it says so under the hatch and exits 127.
func TestHostLaunchsHatchRunsTheProgramBehindEveryBlock(t *testing.T) {
	_, bin := blockerHostFixture(t, `{"packs": [], "security": {"blocked_tools": `+
		`["yolo-fixture-tool", "yolo-absent-tool"]}}`)
	writeFile(t, filepath.Join(bin, "yolo-fixture-tool"), "#!/bin/sh\necho \"real tool ran: $*\"\n")
	if err := os.Chmod(filepath.Join(bin, "yolo-fixture-tool"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, errs := launchCapturing(t)
	work := t.TempDir()
	rc, out := runWithEnv(t, got.env, work, "yolo-fixture-tool a b")
	if rc != 127 || !strings.Contains(out, "yolo-fixture-tool is blocked") {
		t.Fatalf("the user's block did not refuse: rc=%d %q\n%s", rc, out, errs)
	}
	bypass := append(append([]string{}, got.env...), "YOLO_BYPASS_SHIMS=1")
	rc, out = runWithEnv(t, bypass, work, "yolo-fixture-tool a b")
	if rc != 0 || out != "real tool ran: a b\n" {
		t.Errorf("YOLO_BYPASS_SHIMS=1 did not run the real program behind the block: rc=%d %q", rc, out)
	}
	rc, out = runWithEnv(t, bypass, work, "yolo-absent-tool")
	if rc != 127 || !strings.Contains(out, "no yolo-absent-tool is installed behind this block") {
		t.Errorf("the hatch with nothing behind the block: rc=%d %q, want 127 and the line saying so", rc, out)
	}
}

// A real grep or find the shims could pass calls on to is not on the child's PATH: that tool is
// left unblocked, and the launch says so, as for a missing replacement.
func TestHostLaunchDoesNotBlockAToolItCannotFind(t *testing.T) {
	blockerHostFixture(t, `{"packs": ["guardrails"]}`, "rg", "fd")
	sys := filepath.SplitList(os.Getenv("PATH"))[1]
	if err := os.Remove(filepath.Join(sys, "grep")); err != nil {
		t.Fatal(err)
	}
	got, errs := launchCapturing(t)
	if !strings.Contains(errs, "not blocking grep: there is no grep on this launch's PATH to block") {
		t.Errorf("no line named the missing grep:\n%s", errs)
	}
	blockDir := filepath.SplitList(envValue(got.env, "PATH"))[0]
	if _, err := os.Lstat(filepath.Join(blockDir, "grep")); err == nil {
		t.Errorf("grep was blocked although there is no grep behind the block: %s", blockDir)
	}
	if _, err := os.Stat(filepath.Join(blockDir, "find")); err != nil {
		t.Errorf("find was not blocked although its real binary and fd are present: %v", err)
	}
}

// TestHostLaunchBlocksAcrossARealExec is the property at PROCESS level: a child test process runs
// hostExec for real — syscall.Exec and all — into `sh -c 'grep -r …'`, and the shell that replaced
// it meets the shim.
func TestHostLaunchBlocksAcrossARealExec(t *testing.T) {
	if os.Getenv(hostBlockHelperEnv) != "" {
		t.Skip("helper process")
	}
	dir := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^TestHostBlockHelperProcess$", "-test.count=1")
	cmd.Env = append(os.Environ(), hostBlockHelperEnv+"="+dir, "TMPDIR="+dir)
	out, err := cmd.CombinedOutput()
	ee, ok := err.(*exec.ExitError)
	if !ok || ee.ExitCode() != 127 {
		t.Fatalf("the exec'd shell's grep -r: err=%v, want exit 127:\n%s", err, out)
	}
	if !strings.Contains(string(out), "grep's recursive mode is blocked") {
		t.Errorf("the exec'd shell did not print the guardrails message:\n%s", out)
	}
}

const hostBlockHelperEnv = "YOLO_TEST_HOST_BLOCK_HELPER_DIR"

// TestHostBlockHelperProcess is TestHostLaunchBlocksAcrossARealExec's child. On success it never
// returns: the exec replaces it with the shell.
func TestHostBlockHelperProcess(t *testing.T) {
	dir := os.Getenv(hostBlockHelperEnv)
	if dir == "" {
		t.Skip("only runs as a helper process")
	}
	blockerHostFixture(t, `{"packs": ["guardrails"]}`, "rg", "fd")
	writeFile(t, "hay.txt", "needle\n")
	var errw bytes.Buffer
	rc := hostExec(nil, []string{"sh", "-c", "grep -r needle ."}, io.Discard, &errw, nil)
	t.Fatalf("hostExec returned %d instead of exec'ing:\n%s", rc, errw.String())
}
