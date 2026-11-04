package integration

import (
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	goruntime "runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	naming "github.com/mschulkind-oss/yolo-jail/internal/runtime"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

const readyWindowRecordProcessEnv = "YOLO_TEST_READY_WINDOW_RECORD_PROCESS"

// TestKeeperReadyWindowDoesNotStopALaunchRecordWriter drives holdAtReadyWithAnother itself.
// Its process stand-ins use the production launch-record writer. A FIFO holds the first writer
// inside that writer's flock; delaying the started notice until the lock is held selects the
// schedule that the real launch's spawned callback can reach before the fixture reads its output.
// Only after SIGSTOP does the FIFO gain a reader. An attach then cannot record its launch if the
// stopped process owns the flock, and prints its header but never its provisioning-wait marker.
func TestKeeperReadyWindowDoesNotStopALaunchRecordWriter(t *testing.T) {
	if os.Getenv(readyWindowRecordProcessEnv) != "" {
		readyWindowRecordProcess(t)
		return
	}
	if goruntime.GOOS != "linux" {
		t.Skip("observes the stand-in launch's SIGSTOP through /proc")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("YOLO_TEST_JAIL_TIMEOUT", "2")
	t.Setenv(readyWindowRecordProcessEnv, "1")
	pidPath := filepath.Join(home, "first.pid")
	logPath := run.MachineLaunchLogPath()
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(logPath, 0o600); err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	launcher := filepath.Join(home, "launcher")
	body := "#!/bin/sh\nexec " + shquote.Join([]string{exe,
		"-test.run=^TestKeeperReadyWindowDoesNotStopALaunchRecordWriter$", "-test.short", "--"}) + " \"$@\"\n"
	if err := os.WriteFile(launcher, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	oldBin := yoloBin
	yoloBin = launcher
	t.Cleanup(func() { yoloBin = oldBin })
	// writeProject's container cleanup has no real container in this process-only regression.
	rt := filepath.Join(home, "runtime")
	if err := os.WriteFile(rt, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	harnessRuntimeOverride = rt
	t.Cleanup(func() { harnessRuntimeOverride = "" })

	readerDone, readerExited := make(chan struct{}), make(chan struct{})
	readerReady := make(chan error, 1)
	go func() {
		defer close(readerExited)
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(time.Millisecond) {
			raw, _ := os.ReadFile(pidPath)
			pid, _ := strconv.Atoi(strings.TrimSpace(string(raw)))
			stat, _ := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
			if end := strings.LastIndexByte(string(stat), ')'); end >= 0 && strings.HasPrefix(string(stat[end+1:]), " T ") {
				f, err := os.OpenFile(logPath, os.O_RDONLY|syscall.O_NONBLOCK, 0)
				readerReady <- err
				if err == nil {
					defer f.Close()
					<-readerDone
				}
				return
			}
			select {
			case <-readerDone:
				return
			default:
			}
		}
		readerReady <- errors.New("first stand-in was never stopped")
	}()
	t.Cleanup(func() { close(readerDone); <-readerExited })

	w := holdAtReadyWithAnother(t, 1, "KEEPER-READY-RECORD-FIRST")
	if err := <-readerReady; err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(w.other.combined(), "first session to begin provisioning") {
		t.Fatalf("the fixture returned without the attach's marker:\n%s", w.other.combined())
	}
	// The first process is still stopped, so this is not success bought by resuming the window.
	stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", w.first.pid))
	if err != nil || !strings.Contains(string(stat), ") T ") {
		t.Fatalf("the fixture did not leave the first launch stopped: %s (%v)", stat, err)
	}
	t.Log("the second session recorded its launch and reached its marker while the first remained SIGSTOPped")
}

// readyWindowRecordProcess is selected only in this regression's private child environment.
// It supplies the keeper/entrypoint markers, not a runtime, while recording through production.
func readyWindowRecordProcess(t *testing.T) {
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(os.Args, " "), "KEEPER-READY-RECORD-FIRST") {
		if err := os.WriteFile(filepath.Join(os.Getenv("HOME"), "first.pid"), []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
			t.Fatal(err)
		}
		go run.StartHostLaunchRecord(dir).Started()
		// Either the fixture's barrier owns the lock (the safe schedule), or this writer owns it
		// while opening the FIFO (the failing schedule). No timing assumption chooses between them.
		locked := false
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(time.Millisecond) {
			f, err := os.OpenFile(run.MachineLaunchLogPath()+".lock", os.O_WRONLY, 0)
			if err != nil {
				continue
			}
			err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
			if err == nil {
				_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
			}
			_ = f.Close()
			if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
				locked = true
				break
			}
		}
		if !locked {
			t.Fatal("the production record writer never reached its lock")
		}
		cname := naming.FromWorkspace(dir)
		keeperLog := filepath.Join(paths.GlobalStorage(), "logs", "jail-keeper-"+cname+".log")
		if err := os.WriteFile(keeperLog, []byte("holds "+cname+" until its last session leaves\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		fmt.Printf("keeper: started, pid %d\n", os.Getpid())
	} else {
		fmt.Println("Attaching to existing jail")
		run.StartHostLaunchRecord(dir).Started()
		fmt.Println("waiting for this jail's first session to begin provisioning")
	}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM)
	defer signal.Stop(signals)
	<-signals // the fixture owns and reaps both stand-ins through its ordinary launch cleanups
}
