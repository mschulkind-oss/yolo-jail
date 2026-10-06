//go:build linux

package capture

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

// landlock_linux_test.go measures the host capture's confinement (landlock_linux.go) on this
// machine's own kernel: this test binary re-execs itself under ExecConfined (the confine helper)
// and, confined, tries what an installer might (the probe helper), printing each outcome. Nothing
// here is a stand-in for the kernel; a kernel without Landlock skips, and YOLO_TEST_REQUIRE_LANDLOCK=1
// makes that skip a failure, for a runner that is meant to have it.

const (
	landlockConfineArg   = "-capture-landlock-confine"
	landlockProbeArg     = "-capture-landlock-probe"
	landlockSuperviseArg = "-capture-landlock-supervise"
)

func TestMain(m *testing.M) {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case landlockSuperviseArg:
			// Supervise the rest of the argv, this binary again, as `yolo internal landlock-exec
			// --supervise` does, and end with its status.
			self, err := os.Executable()
			if err != nil {
				fmt.Fprintln(os.Stderr, "supervise:", err)
				os.Exit(3)
			}
			cmd := exec.Command(self, os.Args[2:]...)
			cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
			err = Supervise(cmd)
			var ee *exec.ExitError
			if errors.As(err, &ee) {
				os.Exit(ee.ExitCode())
			}
			if err != nil {
				fmt.Fprintln(os.Stderr, "supervise:", err)
				os.Exit(4)
			}
			os.Exit(0)
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
	code := 0
	for _, op := range ops {
		verb, rest, _ := strings.Cut(op, ":")
		a, b, _ := strings.Cut(rest, ":")
		var err error
		switch verb {
		case "exitcode":
			code, err = strconv.Atoi(a)
		case "sleep":
			time.Sleep(time.Minute)
		case "linger", "lingerchild":
			// linger starts lingerchild in a session of its own and waits for it; lingerchild starts a
			// sleeper in a session of its own, records its pid in a, and exits at once. So the sleeper
			// is double-forked out of the probe's session, and orphaned before the probe ends — the
			// shape of `setsid nohup <daemon> &` in a vendor's installer.
			self, serr := os.Executable()
			if serr != nil {
				err = serr
				break
			}
			next := []string{landlockProbeArg, "lingerchild:" + a}
			if verb == "lingerchild" {
				next = []string{landlockProbeArg, "sleep:"}
			}
			c := exec.Command(self, next...)
			c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
			if err = c.Start(); err != nil {
				break
			}
			if verb == "linger" {
				err = c.Wait()
			} else {
				err = os.WriteFile(a, []byte(strconv.Itoa(c.Process.Pid)), 0o644)
			}
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
		case "openread":
			// One read, never blocking and never taking a terminal as this process's own: a terminal's
			// read would otherwise wait for a line, and a device for an end it never reaches.
			var fd int
			if fd, err = unix.Open(a, unix.O_RDONLY|unix.O_NONBLOCK|unix.O_NOCTTY|unix.O_CLOEXEC, 0); err == nil {
				buf := make([]byte, 64)
				if _, rerr := unix.Read(fd, buf); rerr != nil && rerr != unix.EAGAIN {
					err = rerr
				}
				unix.Close(fd)
			}
		case "openwrite":
			var fd int
			if fd, err = unix.Open(a, unix.O_WRONLY|unix.O_NOCTTY|unix.O_CLOEXEC, 0); err == nil {
				unix.Close(fd)
			}
		case "ioctl":
			// A terminal's own ioctl (TCGETS, reading its termios), on a device opened for reading.
			var fd int
			if fd, err = unix.Open(a, unix.O_RDONLY|unix.O_NONBLOCK|unix.O_NOCTTY|unix.O_CLOEXEC, 0); err == nil {
				_, err = unix.IoctlGetTermios(fd, unix.TCGETS)
				unix.Close(fd)
			}
		case "signalparent":
			// Signal 0 asks the kernel's permission without sending anything: the parent is the test,
			// outside the confinement's domain.
			err = unix.Kill(unix.Getppid(), 0)
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
	return code
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
// the home; it can neither read nor write beneath the real /tmp, where yolo keeps owner-only files
// holding a service's address and token, nor open the user's terminals under /dev/pts; it can still
// use /dev's stand-in devices and its own descriptors through /dev/fd, read a CA file it was granted
// under that home and run a granted program; it can create no AF_UNIX socket and set up no io_uring,
// while socketpair and an inet socket stay usable; and from ABI 6 it can signal no process outside its
// domain, its parent among them.
func TestTheHostCapturesConfinementHoldsOnThisKernel(t *testing.T) {
	abi := requireLandlock(t)
	root := resolvedTempDir(t)
	home := filepath.Join(root, "home")
	staging := filepath.Join(home, ".local", "share", "yolo-jail", "captures", "staging", "tool")
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
	// YOLO'S OWN SECRET-BEARING /tmp STATE, as a `yolo host` session keeps it: an endpoint file, 0600 in
	// a 0700 directory directly under the real /tmp (paths.DefaultHostSingletonDir), whatever TMPDIR
	// this test runs under. Only those permissions protect it from a process of the same user.
	tmpState, err := os.MkdirTemp("/tmp", "yolo-host-services-landlock-test-")
	if err != nil {
		t.Fatalf("the real /tmp is not writable here: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(tmpState) })
	endpoint := filepath.Join(tmpState, "claude-oauth-broker.endpoint")
	if err := os.WriteFile(endpoint, []byte("127.0.0.1:40000 CERT SECRET-TOKEN\n"), 0o600); err != nil {
		t.Fatal(err)
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
	type probe struct{ op, want string }
	probes := []probe{
		{"write:" + filepath.Join(staging, "new"), "ok"},
		{"mkdir:" + filepath.Join(staging, "dir"), "ok"},
		{"rename:" + filepath.Join(staging, "moving") + ":" + filepath.Join(staging, "dir", "moved"), "ok"},
		{"write:" + filepath.Join(outside, "stray"), "EACCES"},
		{"write:" + filepath.Join(home, ".ssh", "authorized_keys"), "EACCES"},
		// A rename out of the write set is refused as EXDEV (Landlock's answer for a move it cannot vouch
		// for across its domain) or EACCES, either of which leaves the file where it was.
		{"rename:" + filepath.Join(staging, "new") + ":" + filepath.Join(outside, "escaped"), "EXDEV|EACCES"},
		{"read:" + secret, "EACCES"},
		{"readdir:" + home, "EACCES"},
		{"read:" + endpoint, "EACCES"},
		{"readdir:" + tmpState, "EACCES"},
		{"write:" + filepath.Join(tmpState, "planted"), "EACCES"},
		{"openwrite:/dev/null", "ok"},
		{"openread:/dev/urandom", "ok"},
		{"openwrite:/dev/fd/1", "ok"},
		{"readdir:/dev", "EACCES"},
		{"read:" + ca, "ok"},
		{"exec:" + self + ":-test.run=^$", "ok"},
		{"unixsocket:", "EACCES"},
		{"iouring:", "ENOSYS"},
		{"socketpair:", "ok"},
		{"inetsocket:", "ok"},
	}
	if abi >= 3 {
		probes = append(probes, probe{"truncate:" + victim, "EACCES"})
	}
	if abi >= 6 {
		probes = append(probes, probe{"signalparent:", "EPERM"})
	}
	// THE USER'S TERMINAL: a pty with a line typed into it, as another terminal of the user's would have.
	if pts, ok := openTypedPty(t, "typed-password\n"); ok {
		probes = append(probes, probe{"openread:" + pts, "EACCES"}, probe{"openwrite:" + pts, "EACCES"})
	}
	args := append(append([]string{landlockConfineArg}, p.Args()...), "--", self, landlockProbeArg)
	for _, pr := range probes {
		args = append(args, pr.op)
	}
	out, err := exec.Command(self, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("the confined probe failed: %v\n%s", err, out)
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
	for _, pr := range probes {
		if g := got[pr.op]; !slices.Contains(strings.Split(pr.want, "|"), g) {
			t.Errorf("%s: %s, want %s", pr.op, g, pr.want)
		}
	}
	if strings.Contains(string(out), "SECRET-TOKEN") || strings.Contains(string(out), "typed-password") {
		t.Errorf("the confined probe printed what it should not have read:\n%s", out)
	}
	if b, _ := os.ReadFile(victim); abi >= 3 && string(b) != "content" {
		t.Errorf("a file outside the write set was truncated to %q", b)
	}
	for _, f := range []string{filepath.Join(outside, "stray"), filepath.Join(outside, "escaped"),
		filepath.Join(home, ".ssh", "authorized_keys"), filepath.Join(tmpState, "planted")} {
		if _, err := os.Lstat(f); err == nil {
			t.Errorf("%s exists: a confined write landed outside the staging tree", f)
		}
	}
}

// openTypedPty opens a pty pair, types line into its master, and returns the slave's path, as one of
// the user's own terminals with a password half typed would be; false where this machine has no ptys.
func openTypedPty(t *testing.T, line string) (string, bool) {
	t.Helper()
	m, err := unix.Open("/dev/ptmx", unix.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Logf("no /dev/ptmx here, so no terminal probe: %v", err)
		return "", false
	}
	var unlock int
	if _, _, e := unix.Syscall(unix.SYS_IOCTL, uintptr(m), unix.TIOCSPTLCK, uintptr(unsafe.Pointer(&unlock))); e != 0 {
		unix.Close(m)
		t.Logf("unlockpt: %v, so no terminal probe", e)
		return "", false
	}
	n, err := unix.IoctlGetUint32(m, unix.TIOCGPTN)
	if err != nil {
		unix.Close(m)
		t.Logf("ptsname: %v, so no terminal probe", err)
		return "", false
	}
	pts := fmt.Sprintf("/dev/pts/%d", n)
	// The slave held open here too, so what is typed waits in its input queue.
	s, err := unix.Open(pts, unix.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC, 0)
	if err != nil {
		unix.Close(m)
		t.Logf("open %s: %v, so no terminal probe", pts, err)
		return "", false
	}
	t.Cleanup(func() { unix.Close(s); unix.Close(m) })
	if _, err := unix.Write(m, []byte(line)); err != nil {
		t.Fatalf("typing into the pty: %v", err)
	}
	// The test itself can read it: what the confined probe is refused is the user's, not the kernel's.
	if fd, err := unix.Open(pts, unix.O_RDONLY|unix.O_NONBLOCK|unix.O_NOCTTY|unix.O_CLOEXEC, 0); err == nil {
		unix.Close(fd)
	} else {
		t.Fatalf("the unconfined test cannot open %s: %v", pts, err)
	}
	return pts, true
}

// A DEVICE'S IOCTLS NEED THEIR OWN GRANT from ABI 5 (LANDLOCK_ACCESS_FS_IOCTL_DEV): a terminal granted
// for reading and executing alone opens, and its termios cannot be read or set through it; /dev/null, a stand-in
// device the policy grants whole, answers its ioctl as any non-terminal does.
func TestADevicesIoctlsNeedTheirOwnGrant(t *testing.T) {
	if abi := requireLandlock(t); abi < 5 {
		t.Skipf("this kernel's Landlock is ABI %d, and device ioctls are a right from ABI 5", abi)
	}
	pts, ok := openTypedPty(t, "")
	if !ok {
		t.Skip("no pty to probe")
	}
	root := resolvedTempDir(t)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	// Everything readable (the terminal included), nothing writable but the test's tree and /dev/null.
	p := LandlockPolicy{ReadWrite: []string{root, "/dev/null"}, ReadExec: []string{"/"}}
	ops := []string{"openread:" + pts, "ioctl:" + pts, "ioctl:/dev/null"}
	args := append(append(append([]string{landlockConfineArg}, p.Args()...), "--", self, landlockProbeArg), ops...)
	out, err := exec.Command(self, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("the confined probe failed: %v\n%s", err, out)
	}
	for op, want := range map[string]string{ops[0]: "ok", ops[1]: "EACCES", ops[2]: "ENOTTY"} {
		verb, rest, _ := strings.Cut(op, ":")
		if !strings.Contains(string(out), verb+" "+rest+": "+want+"\n") {
			t.Errorf("%s: want %s, got\n%s", op, want, out)
		}
	}
}

// A SUPERVISED CONFINEMENT LEAVES NOTHING RUNNING (Supervise, HP-D18's lifetime containment): the
// confined command double-forks a sleeper out of its session and exits; once the supervisor returns,
// with the command's own status, the sleeper is gone — killed and reaped, not left to init.
func TestASupervisedConfinementLeavesNothingRunning(t *testing.T) {
	requireLandlock(t)
	root := resolvedTempDir(t)
	home := filepath.Join(root, "home")
	staging := filepath.Join(home, "staging")
	if err := os.MkdirAll(staging, 0o755); err != nil {
		t.Fatal(err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	p, err := CapturePolicy(CapturePolicyOptions{Staging: staging, Home: home, Exec: []string{self}})
	if err != nil {
		t.Fatal(err)
	}
	pidFile := filepath.Join(staging, "lingering.pid")
	args := append(append([]string{landlockSuperviseArg, landlockConfineArg}, p.Args()...), "--", self,
		landlockProbeArg, "linger:"+pidFile, "exitcode:7")
	out, err := exec.Command(self, args...).CombinedOutput()
	var ee *exec.ExitError
	if !errors.As(err, &ee) || ee.ExitCode() != 7 {
		t.Fatalf("the supervised command's status did not come back: %v\n%s", err, out)
	}
	b, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("the confined command started no sleeper: %v\n%s", err, out)
	}
	pid, err := strconv.Atoi(string(b))
	if err != nil {
		t.Fatal(err)
	}
	if !procGone(pid) {
		_ = unix.Kill(pid, unix.SIGKILL)
		t.Fatalf("pid %d, double-forked out of the confined command's session, outlived its supervisor\n%s", pid, out)
	}
}

// procGone says whether pid has exited: no /proc entry, or a zombie its new parent has not reaped.
func procGone(pid int) bool {
	_, state, ok := procParent(pid)
	return !ok || state == 'Z'
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
// out the same way; /tmp is granted for nothing at all, since yolo's own owner-only state is there;
// /dev is granted only as its stand-in devices and /dev/shm, its terminals never, and /dev/shm around
// an excluded branch under it; and the staging tree, the granted program and the granted CA file are
// each named.
func TestTheCapturePolicyGrantsEverythingButTheExcludedBranches(t *testing.T) {
	root := resolvedTempDir(t)
	for _, d := range []string{"usr/bin", "etc", "home/me/.ssh", "home/other", "run/user/1000", "run/systemd",
		"tmp/build", "tmp/me-home", "dev/shm/me-runtime", "dev/shm/other", "dev/pts", "var/lib"} {
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
		Exclude: []string{filepath.Join(root, "run", "user", "1000"), filepath.Join(root, "tmp", "me-home"),
			filepath.Join(root, "dev", "shm", "me-runtime")},
		Exec: []string{filepath.Join(home, "go", "bin", "yolo")}, Read: []string{filepath.Join(home, "ca.crt")}})
	if err != nil {
		t.Fatal(err)
	}
	j := func(rel ...string) string { return filepath.Join(append([]string{root}, rel...)...) }
	wantRX := []string{j("etc"), j("file"), j("home", "other"), j("run", "systemd"), j("usr"), j("var"),
		filepath.Join(home, "go", "bin", "yolo")}
	if !slices.Equal(p.ReadExec, wantRX) {
		t.Errorf("read+exec =\n  %q\nwant\n  %q", p.ReadExec, wantRX)
	}
	wantRW := []string{staging, j("dev", "null"), j("dev", "zero"), j("dev", "full"), j("dev", "random"),
		j("dev", "urandom"), j("dev", "tty"), j("dev", "shm", "other")}
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
