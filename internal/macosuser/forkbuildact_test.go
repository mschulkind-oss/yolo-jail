package macosuser

// forkbuildact_test.go pins RunForkBuildAct's call sites that capture_test.go's plan tests do not
// reach (docs/design/forked-programs-as-packs.md FP-D24): the TLS trust a launch composes, the two
// refusals of its toolchain step, and the stop a signal sent to yolo alone makes while that step's
// nix runs. Each test fails if its call site is deleted.

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/nixchildren"
)

// forkBuildActRun is a Deps.Run that records like captureDeps' and, for the build's driver, leaves
// a proto-entry in the staging tree of opts, as a build would.
func forkBuildActRun(t *testing.T, d Deps, opts ForkBuildOptions) func([]string) int {
	t.Helper()
	realRun := d.Run
	root := ForkBuildStagingRoot(opts.CaptureRoot, opts.BuildID)
	return func(argv []string) int {
		rc := realRun(argv)
		if strings.Contains(strings.Join(argv, " "), "capture-run") {
			if err := os.MkdirAll(filepath.Join(root, "out", "tree"), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		return rc
	}
}

// forkBuildActOptions is testForkBuildOptions staged under a temp capture root, with no darwin
// floor yet: the act builds it.
func forkBuildActOptions(t *testing.T) (ForkBuildOptions, string) {
	t.Helper()
	tmp, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	opts := testForkBuildOptions()
	opts.Darwin = nil
	opts.CaptureRoot = filepath.Join(tmp, "captures")
	return opts, tmp
}

// THE BUILD RUNS UNDER THE TRUST A LAUNCH COMPOSES from the toolchain it built: the floor's roots and
// this Mac's System-keychain CAs, written as the two CA files the session env file names, and said.
// A build behind a corporate CA fetches its dependencies through it.
func TestAForkBuildActComposesTheLaunchsTrust(t *testing.T) {
	f := newKeychainFixture(t)
	c := &captureDeps{}
	d := c.deps()
	d.ReadFile = func(p string) (string, bool) {
		if p == testProfile+"/"+profileCABundleRel {
			return testRoots, true
		}
		return "", true
	}
	d.ReadSystemKeychain = func() (string, error) { return f.export, nil }
	d.VerifyCA = verifyOnly(f.trusted, f.anyPurpose)
	d.MaterializeDarwin = func(string, []any) (*Darwin, bool, error) {
		return &Darwin{PathPrefix: []string{testProfile + "/bin"}, System: "aarch64-darwin", ProfilePath: testProfile}, true, nil
	}
	opts, tmp := forkBuildActOptions(t)
	written := map[string]string{}
	install := d.InstallRootFile
	d.InstallRootFile = func(path, content, mode string) bool {
		written[path] = content
		return install(path, content, mode)
	}
	d.Run = forkBuildActRun(t, d, opts)
	if rc := RunForkBuildAct(d, opts, filepath.Join(tmp, "store", "out"), filepath.Join(tmp, "store", "toolchain"), false); rc != 0 {
		t.Fatalf("RunForkBuildAct = %d\n%s", rc, c.out.String())
	}
	cname := BuildForkBuildPlan(opts).Cname
	bundle := CABundleFile(cname, "")
	for _, p := range []string{bundle, CAExtrasFile(cname, "")} {
		if !slices.Contains(c.files, p) {
			t.Errorf("the build did not write %s: %v", p, c.files)
		}
	}
	env := ""
	for p, content := range written {
		if strings.HasSuffix(p, ".env") || strings.Contains(content, "SSL_CERT_FILE") {
			env += content
		}
	}
	if !strings.Contains(env, "SSL_CERT_FILE") || !strings.Contains(env, bundle) {
		t.Errorf("the build's session env does not point SSL_CERT_FILE at %s:\n%s", bundle, env)
	}
	if want := "Trusting 2 certificate authorities from this Mac's System keychain"; !strings.Contains(c.out.String(), want) {
		t.Errorf("the build does not say %q:\n%s", want, c.out.String())
	}
}

// NO FLAKE, NO TOOLCHAIN: a build with no repo root refuses before any nix runs, naming where a
// launch's flake is read, rather than have nix evaluate whatever flake the cwd holds.
func TestAForkBuildActWithNoFlakeRefusesBeforeItsToolchain(t *testing.T) {
	c := &captureDeps{}
	d := c.deps()
	d.MaterializeDarwin = func(string, []any) (*Darwin, bool, error) {
		t.Error("the darwin floor was built with no flake to build it from")
		return nil, false, nil
	}
	opts := testForkBuildOptions()
	opts.RepoRoot = ""
	if rc := RunForkBuildAct(d, opts, "/nonexistent/out", "/nonexistent/toolchain", false); rc != 1 ||
		!strings.Contains(c.out.String(), "No yolo-jail flake was found") || !strings.Contains(c.out.String(), "yolo check") {
		t.Errorf("rc = %d, want a refusal naming `yolo check`:\n%s", rc, c.out.String())
	}
	if len(c.ran) != 0 {
		t.Errorf("the act ran steps with no toolchain: %v", c.ran)
	}
}

// A TOOLCHAIN WITH NO TOOL DIRECTORY IS NO TOOLCHAIN: a floor build that reports success and names
// no PATH entry refuses, as a yolo bug, and the build line never runs on the bare system PATH.
func TestAForkBuildActWhoseToolchainHasNoPathRefuses(t *testing.T) {
	c := &captureDeps{}
	d := c.deps()
	d.MaterializeDarwin = func(string, []any) (*Darwin, bool, error) {
		return &Darwin{ProfilePath: "/nix/store/floor", System: "aarch64-darwin"}, true, nil
	}
	if rc := RunForkBuildAct(d, testForkBuildOptions(), "/nonexistent/out", "/nonexistent/toolchain", false); rc != 1 ||
		!strings.Contains(c.out.String(), "produced no tool directory") {
		t.Errorf("rc = %d, want the no-tool-directory refusal:\n%s", rc, c.out.String())
	}
	if strings.Contains(strings.Join(c.ran, "\n"), "capture-run") {
		t.Error("the build line ran with no toolchain on its PATH")
	}
}

// A SIGNAL SENT TO YOLO ALONE WHILE THE BUILD'S TOOLCHAIN NIX RUNS STOPS THAT NIX (nixchildren): with
// no launch arm (Deps.Ending nil, as `yolo capture` and the host floor call the act), a SIGTERM
// interrupts it and ends the process 143, and the build line never runs. Without the stop, the
// signal's default action ended yolo and left a build of up to half an hour running with no parent.
func TestASignalWhileTheForkBuildsToolchainNixRunsStopsIt(t *testing.T) {
	s := nixchildren.Isolate(t)
	catchSignal(t, syscall.SIGTERM)
	marks := t.TempDir()
	c := &captureDeps{}
	d := c.deps()
	var buf bytes.Buffer
	d.Out = &buf
	d.MaterializeDarwin = func(string, []any) (*Darwin, bool, error) {
		return nil, false, trackedStandInNix(marks)
	}
	returned := make(chan int, 1)
	go func() {
		returned <- RunForkBuildAct(d, testForkBuildOptions(), "/nonexistent/out", "/nonexistent/toolchain", false)
	}()
	for deadline := time.Now().Add(10 * time.Second); s.Running() != 1; time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("the toolchain's nix never ran\n%s", buf.String())
		}
	}
	awaitStarted(t, marks)
	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case code := <-s.Exited():
		if code != 128+int(syscall.SIGTERM) {
			t.Errorf("the signaled build ended %d, want %d", code, 128+int(syscall.SIGTERM))
		}
	case <-time.After(15 * time.Second):
		t.Fatal("a SIGTERM sent to yolo alone during the toolchain build did not end it")
	}
	if _, err := os.Stat(filepath.Join(marks, "interrupted")); err != nil {
		t.Error("the build ended without interrupting its toolchain's nix")
	}
	select {
	case <-returned:
	case <-time.After(15 * time.Second):
		t.Fatal("RunForkBuildAct never returned after its faked exit")
	}
	if strings.Contains(strings.Join(c.ran, "\n"), "capture-run") {
		t.Error("the build line ran after the signal ended the build")
	}
}

// The stop covers the toolchain's nix and nothing after it: from the privileged steps on, a signal
// is the steps' own to take, and an arm left armed would end yolo under a running sudo build.
func TestTheForkBuildsNixArmIsGoneBeforeTheBuildLine(t *testing.T) {
	s := nixchildren.Isolate(t)
	catchSignal(t, syscall.SIGTERM)
	c := &captureDeps{}
	d := c.deps()
	d.MaterializeDarwin = func(string, []any) (*Darwin, bool, error) {
		return &Darwin{PathPrefix: []string{"/nix/store/floor/bin"}, ProfilePath: "/nix/store/floor"}, true, nil
	}
	opts, tmp := forkBuildActOptions(t)
	run := forkBuildActRun(t, d, opts)
	d.Run = func(argv []string) int {
		if strings.Contains(strings.Join(argv, " "), "capture-run") {
			if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
				t.Error(err)
			}
			select {
			case code := <-s.Exited():
				t.Errorf("the toolchain's nix arm was still armed at the build line and ended it %d", code)
			case <-time.After(300 * time.Millisecond):
			}
		}
		return run(argv)
	}
	if rc := RunForkBuildAct(d, opts, filepath.Join(tmp, "store", "out"), filepath.Join(tmp, "store", "toolchain"), false); rc != 0 {
		t.Fatalf("RunForkBuildAct = %d\n%s", rc, c.out.String())
	}
}
