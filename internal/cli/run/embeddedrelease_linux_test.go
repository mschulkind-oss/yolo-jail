//go:build linux

package run

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

const signalArmHelperEnv = "YOLO_TEST_SIGNAL_ARM_HELPER"

// TestSignalArmReleasesTheFallbackTree drives the REAL terminate arm — runWithProxy over a
// pty, SIGTERM to the launcher process — in a child process, and looks at the child's
// fallback embedded tree after it has exited. The child passes NO onTerminate, as the attach
// arm and the macos-user seam do, so the release it observes is runWithProxy's own. The
// control case (that release stubbed out) proves the arm skips cli.Main's defer, i.e. that
// runWithProxy's release is what removes the tree, not something downstream.
func TestSignalArmReleasesTheFallbackTree(t *testing.T) {
	if os.Getenv(signalArmHelperEnv) != "" {
		t.Skip("helper process")
	}
	for _, tc := range []struct {
		mode     string
		wantGone bool
	}{
		{mode: "release", wantGone: true},
		{mode: "control", wantGone: false},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			dir := t.TempDir()
			cmd := exec.Command(os.Args[0], "-test.run=^TestSignalArmHelperProcess$", "-test.count=1")
			cmd.Env = append(os.Environ(), signalArmHelperEnv+"="+tc.mode+":"+dir)
			out, err := cmd.CombinedOutput()
			var ee *exec.ExitError
			if !errors.As(err, &ee) || ee.ExitCode() != 128+int(syscall.SIGTERM) {
				if strings.Contains(string(out), "SKIP-NO-PTY") {
					t.Skip("no pty available")
				}
				t.Fatalf("helper exit = %v, want %d from the signal arm\n%s", err, 128+int(syscall.SIGTERM), out)
			}
			rootB, rerr := os.ReadFile(filepath.Join(dir, "root"))
			if rerr != nil {
				t.Fatalf("helper never recorded its tree: %v\n%s", rerr, out)
			}
			_, serr := os.Stat(string(rootB))
			gone := os.IsNotExist(serr)
			if gone != tc.wantGone {
				t.Errorf("%s: fallback tree gone=%v after the signal arm, want %v", tc.mode, gone, tc.wantGone)
			}
		})
	}
}

// TestSignalArmHelperProcess is the child: a fallback tree, a pty as stdin (the proxy only
// installs its signal arm on a TTY), runWithProxy, then SIGTERM to itself. It exits through
// ttyproxy's os.Exit(143) and never returns.
func TestSignalArmHelperProcess(t *testing.T) {
	spec := os.Getenv(signalArmHelperEnv)
	if spec == "" {
		t.Skip("only runs as a helper process")
	}
	mode, dir, _ := strings.Cut(spec, ":")
	// No t.TempDir / t.Setenv cleanups: this process leaves through os.Exit.
	tmp := filepath.Join(dir, "tmp")
	_ = os.MkdirAll(tmp, 0o755)
	os.Setenv("TMPDIR", tmp)
	packload.OverrideEmbeddedCacheDir("")
	if len(packload.Embedded()) == 0 {
		t.Fatalf("Embedded() is empty: %v", packload.EmbeddedProblems())
	}
	root, fallback := packload.EmbeddedLocation()
	if !fallback {
		t.Fatal("setup: not a fallback tree")
	}
	if err := os.WriteFile(filepath.Join(dir, "root"), []byte(root), 0o644); err != nil {
		t.Fatal(err)
	}

	master, slave, err := helperPty()
	if err != nil {
		os.Stdout.WriteString("SKIP-NO-PTY\n")
		os.Exit(0)
	}
	defer unix.Close(master)
	os.Stdin = os.NewFile(uintptr(slave), "pty-slave")

	// No onTerminate, as the attach arm passes none: the release must come from runWithProxy
	// itself. The control stubs that release out. The child is not killed by the arm: a
	// short sleep outlives it and then exits on its own, so runWithProxy cannot return on
	// the main goroutine and race the arm's os.Exit.
	if mode == "control" {
		terminateRelease = func() {}
	}
	go func() {
		time.Sleep(300 * time.Millisecond)
		_ = syscall.Kill(os.Getpid(), syscall.SIGTERM)
	}()
	_, _ = runWithProxy([]string{"sleep", "3"}, nil, nil, &Options{})
	t.Fatal("runWithProxy returned; the signal arm did not fire")
}

func helperPty() (int, int, error) {
	master, err := unix.Open("/dev/ptmx", unix.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		return -1, -1, err
	}
	if err := unix.IoctlSetPointerInt(master, unix.TIOCSPTLCK, 0); err != nil {
		unix.Close(master)
		return -1, -1, err
	}
	n, err := unix.IoctlGetUint32(master, unix.TIOCGPTN)
	if err != nil {
		unix.Close(master)
		return -1, -1, err
	}
	slave, err := unix.Open("/dev/pts/"+itoa(int(n)), unix.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		unix.Close(master)
		return -1, -1, err
	}
	return master, slave, nil
}
