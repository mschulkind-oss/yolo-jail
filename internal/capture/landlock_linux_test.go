//go:build linux

package capture

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"unsafe"

	"golang.org/x/sys/unix"
)

// landlock_linux_test.go measures the host capture's confinement (landlock_linux.go) on this
// machine's own kernel: this test binary re-execs itself under ExecConfined (the confine helper)
// and, confined, tries what an installer might (the probe helper), printing each outcome. Nothing
// here is a stand-in for the kernel; a kernel without Landlock skips, and YOLO_TEST_REQUIRE_LANDLOCK=1
// makes that skip a failure, for a runner that is meant to have it.

const (
	landlockConfineArg = "-capture-landlock-confine"
	landlockProbeArg   = "-capture-landlock-probe"
)

func TestMain(m *testing.M) {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case landlockConfineArg:
			p, argv, err := ParseLandlockArgs(os.Args[2:])
			if err == nil {
				err = ExecConfined(p, argv, os.Environ())
			}
			fmt.Fprintln(os.Stderr, "confine:", err)
			os.Exit(3)
		case landlockProbeArg:
			os.Exit(landlockProbe(os.Args[2:]))
		}
	}
	os.Exit(m.Run())
}

// landlockProbe runs each "op:path[:path]" it is handed and prints "op path: ok" or the errno's name.
func landlockProbe(ops []string) int {
	for _, op := range ops {
		verb, rest, _ := strings.Cut(op, ":")
		a, b, _ := strings.Cut(rest, ":")
		var err error
		switch verb {
		case "write":
			err = os.WriteFile(a, []byte("x"), 0o644)
		case "mkdir":
			err = os.Mkdir(a, 0o755)
		case "rename":
			err = os.Rename(a, b)
		case "truncate":
			err = os.Truncate(a, 0)
		case "read":
			_, err = os.ReadFile(a)
		case "readdir":
			_, err = os.ReadDir(a)
		case "exec":
			err = exec.Command(a, b).Run()
		case "unixsocket":
			var fd int
			if fd, err = unix.Socket(unix.AF_UNIX, unix.SOCK_STREAM, 0); err == nil {
				unix.Close(fd)
			}
		case "socketpair":
			var fds [2]int
			if fds, err = unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM, 0); err == nil {
				unix.Close(fds[0])
				unix.Close(fds[1])
			}
		case "inetsocket":
			var fd int
			if fd, err = unix.Socket(unix.AF_INET, unix.SOCK_STREAM, 0); err == nil {
				unix.Close(fd)
			}
		case "iouring":
			params := make([]byte, 120) // struct io_uring_params, zeroed
			fd, _, errno := unix.Syscall(unix.SYS_IO_URING_SETUP, 1, uintptr(unsafe.Pointer(&params[0])), 0)
			if errno != 0 {
				err = errno
			} else {
				unix.Close(int(fd))
			}
		default:
			err = fmt.Errorf("unknown op %q", verb)
		}
		fmt.Printf("%s %s: %s\n", verb, rest, outcome(err))
	}
	return 0
}

func outcome(err error) string {
	if err == nil {
		return "ok"
	}
	var errno unix.Errno
	if errors.As(err, &errno) {
		return unix.ErrnoName(errno)
	}
	return err.Error()
}

// requireLandlock skips a test on a kernel that cannot confine a host capture, unless the runner is
// meant to have one.
func requireLandlock(t *testing.T) int {
	t.Helper()
	abi, err := HostConfinementABI()
	if err == nil {
		return abi
	}
	if os.Getenv("YOLO_TEST_REQUIRE_LANDLOCK") == "1" {
		t.Fatalf("YOLO_TEST_REQUIRE_LANDLOCK=1, and this kernel cannot confine a host capture: %v", err)
	}
	if errors.Is(err, ErrLandlockUnavailable) || abi > 0 {
		t.Skipf("no host confinement on this kernel: %v", err)
	}
	t.Fatalf("probing Landlock: %v", err)
	return 0
}

// TestTheHostCapturesConfinementHoldsOnThisKernel is HP-D18's confinement, measured: confined to a
// staging tree under the home, the process writes, makes directories and renames inside it; it can
// neither write, rename out of it, nor (from ABI 3) truncate anything elsewhere in the home, nor read
// the home; it can
// still read a CA file it was granted under that home and run a granted program; and it can create
// no AF_UNIX socket and set up no io_uring, while socketpair and an inet socket stay usable.
func TestTheHostCapturesConfinementHoldsOnThisKernel(t *testing.T) {
	abi := requireLandlock(t)
	root := resolvedTempDir(t)
	home := filepath.Join(root, "home")
	staging := filepath.Join(home, ".local", "share", "yolo-jail", "captures", "staging", "tool")
	// Outside the write set is the rest of the home: a test's whole tree is under /tmp, which the
	// installer may write, as any program the user runs may.
	outside := filepath.Join(home, "project")
	for _, d := range []string{filepath.Join(home, ".ssh"), staging, outside} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	secret := filepath.Join(home, ".ssh", "id_ed25519")
	ca := filepath.Join(home, ".ca-bundle.crt")
	victim := filepath.Join(outside, "victim")
	for _, f := range []string{secret, ca, victim, filepath.Join(staging, "moving")} {
		if err := os.WriteFile(f, []byte("content"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	p, err := CapturePolicy(CapturePolicyOptions{Staging: staging, Home: home, Exec: []string{self},
		Read: []string{ca}})
	if err != nil {
		t.Fatal(err)
	}
	ops := []string{
		"write:" + filepath.Join(staging, "new"),
		"mkdir:" + filepath.Join(staging, "dir"),
		"rename:" + filepath.Join(staging, "moving") + ":" + filepath.Join(staging, "dir", "moved"),
		"write:" + filepath.Join(outside, "stray"),
		"write:" + filepath.Join(home, ".ssh", "authorized_keys"),
		"rename:" + filepath.Join(staging, "new") + ":" + filepath.Join(outside, "escaped"),
		"truncate:" + victim,
		"read:" + secret,
		"readdir:" + home,
		"read:" + ca,
		"exec:" + self + ":-test.run=^$",
		"unixsocket:",
		"iouring:",
		"socketpair:",
		"inetsocket:",
	}
	args := append(append([]string{landlockConfineArg}, p.Args()...), "--", self, landlockProbeArg)
	cmd := exec.Command(self, append(args, ops...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("the confined probe failed: %v\n%s", err, out)
	}
	want := map[string]string{
		ops[0]: "ok", ops[1]: "ok", ops[2]: "ok",
		ops[3]: "EACCES", ops[4]: "EACCES", ops[5]: "EXDEV",
		ops[7]: "EACCES", ops[8]: "EACCES",
		ops[9]: "ok", ops[10]: "ok",
		ops[11]: "EACCES", ops[12]: "ENOSYS", ops[13]: "ok", ops[14]: "ok",
	}
	if abi >= 3 {
		want[ops[6]] = "EACCES"
	}
	got := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		head, result, ok := strings.Cut(line, ": ")
		if !ok {
			continue
		}
		verb, rest, _ := strings.Cut(head, " ")
		got[verb+":"+rest] = result
	}
	for op, w := range want {
		if g := got[op]; g != w && !(op == ops[5] && g == "EACCES") {
			// A rename out of the write set is refused as EXDEV (Landlock's answer for a move it
			// cannot vouch for across its domain) or EACCES, either of which leaves the file where it was.
			t.Errorf("%s: %s, want %s", op, g, w)
		}
	}
	if b, _ := os.ReadFile(victim); abi >= 3 && string(b) != "content" {
		t.Errorf("a file outside the write set was truncated to %q", b)
	}
	for _, f := range []string{filepath.Join(outside, "stray"), filepath.Join(outside, "escaped"),
		filepath.Join(home, ".ssh", "authorized_keys")} {
		if _, err := os.Lstat(f); err == nil {
			t.Errorf("%s exists: a confined write landed outside the staging tree", f)
		}
	}
}

// A confinement that cannot be built runs nothing: a policy naming a path the kernel cannot grant
// stops before the exec, and the command never starts.
func TestAConfinementThatCannotBeBuiltRunsNothing(t *testing.T) {
	requireLandlock(t)
	root := resolvedTempDir(t)
	marker := filepath.Join(root, "ran")
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	// A link to itself cannot be opened for a rule (ELOOP), which is no grant the kernel can make.
	loop := filepath.Join(root, "loop")
	if err := os.Symlink(loop, loop); err != nil {
		t.Fatal(err)
	}
	bad := LandlockPolicy{ReadWrite: []string{root}, ReadExec: []string{loop}}
	args := append(append([]string{landlockConfineArg}, bad.Args()...), "--", self, landlockProbeArg,
		"write:"+marker)
	out, err := exec.Command(self, args...).CombinedOutput()
	if err == nil || !strings.Contains(string(out), "confine:") {
		t.Fatalf("a bad policy ran its command: %v\n%s", err, out)
	}
	if _, err := os.Lstat(marker); err == nil {
		t.Error("the command ran although its confinement could not be built")
	}
}

// THE POLICY'S SHAPE, on a tree of the test's own: the root's directories are granted except the
// branch holding the home, which is descended into and granted around; a symbolic link is never
// granted (what it names is, or is not, where it really is); the excluded runtime directory is left
// out the same way; /tmp is granted for writing around a home under it; and the staging tree, the
// granted program and the granted CA file are each named.
func TestTheCapturePolicyGrantsEverythingButTheExcludedBranches(t *testing.T) {
	root := resolvedTempDir(t)
	for _, d := range []string{"usr/bin", "etc", "home/me/.ssh", "home/other", "run/user/1000", "run/systemd",
		"tmp/build", "tmp/me-home", "dev/shm", "var/lib"} {
		if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(d)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(root, "home", "me"), filepath.Join(root, "mylink")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "file"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(root, "home", "me")
	staging := filepath.Join(home, ".local", "share", "yolo-jail", "captures", "staging", "tool")
	p, err := CapturePolicy(CapturePolicyOptions{Root: root, Staging: staging, Home: home,
		Exclude: []string{filepath.Join(root, "run", "user", "1000"), filepath.Join(root, "tmp", "me-home")},
		Exec:    []string{filepath.Join(home, "go", "bin", "yolo")}, Read: []string{filepath.Join(home, "ca.crt")}})
	if err != nil {
		t.Fatal(err)
	}
	j := func(rel ...string) string { return filepath.Join(append([]string{root}, rel...)...) }
	wantRX := []string{j("dev"), j("etc"), j("file"), j("home", "other"), j("run", "systemd"), j("usr"), j("var"),
		filepath.Join(home, "go", "bin", "yolo")}
	if !slices.Equal(p.ReadExec, wantRX) {
		t.Errorf("read+exec =\n  %q\nwant\n  %q", p.ReadExec, wantRX)
	}
	wantRW := []string{staging, j("tmp", "build"), j("dev", "null"), j("dev", "zero"), j("dev", "full"),
		j("dev", "random"), j("dev", "urandom"), j("dev", "tty"), j("dev", "shm")}
	if !slices.Equal(p.ReadWrite, wantRW) {
		t.Errorf("read+write =\n  %q\nwant\n  %q", p.ReadWrite, wantRW)
	}
	if !slices.Equal(p.Read, []string{filepath.Join(home, "ca.crt")}) {
		t.Errorf("read = %q, want the CA file", p.Read)
	}
	for _, granted := range append(append([]string{}, p.ReadExec...), p.ReadWrite...) {
		if granted == j("mylink") {
			t.Errorf("the symbolic link %s was granted: it names the home", granted)
		}
	}

	// No home, or a home at the root, leaves nothing to exclude: refused, never a policy granting all.
	for _, h := range []string{"", root} {
		if _, err := CapturePolicy(CapturePolicyOptions{Root: root, Staging: staging, Home: h}); err == nil {
			t.Errorf("a home of %q built a policy", h)
		}
	}
}

// The policy crosses the re-exec as flags and comes back whole; a relative path or a missing `--` is
// refused, so the confining process never guesses at a grant.
func TestALandlockPolicyRoundTripsThroughItsFlags(t *testing.T) {
	p := LandlockPolicy{ReadWrite: []string{"/a", "/b"}, ReadExec: []string{"/usr"}, Read: []string{"/etc/ca"}}
	got, argv, err := ParseLandlockArgs(append(p.Args(), "--", "/bin/true", "x"))
	if err != nil || !slices.Equal(got.ReadWrite, p.ReadWrite) || !slices.Equal(got.ReadExec, p.ReadExec) ||
		!slices.Equal(got.Read, p.Read) || !slices.Equal(argv, []string{"/bin/true", "x"}) {
		t.Fatalf("round trip = %+v %q %v", got, argv, err)
	}
	for _, bad := range [][]string{{"--rw=relative", "--", "/bin/true"}, {"--rw=/a"}, {"--rw=/a", "--"},
		{"--what=/a", "--", "/bin/true"}} {
		if _, _, err := ParseLandlockArgs(bad); err == nil {
			t.Errorf("ParseLandlockArgs(%q) accepted it", bad)
		}
	}
}
