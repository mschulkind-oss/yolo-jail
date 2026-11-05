package run

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
)

func TestMCPAuthReadinessWaitCoversBoundedNativeLockInterleaving(t *testing.T) {
	old := mcpAuthReadinessBudget
	mcpAuthReadinessBudget = 180 * time.Millisecond
	defer func() { mcpAuthReadinessBudget = old }()
	hd := &loopholes.HostDaemon{Detach: true, LaunchInput: "mcp-auth-share"}
	o := &Options{ServiceReadyTimeout: 30 * time.Millisecond}
	if got := o.serviceReadyTimeoutFor(hd); got != 180*time.Millisecond {
		t.Fatalf("MCP readiness = %s; daemon's native lock allowance exceeds it", got)
	}
	if got := o.serviceReadyTimeoutFor(&loopholes.HostDaemon{}); got != 30*time.Millisecond {
		t.Fatalf("other broker readiness changed: %s", got)
	}
	for _, tc := range []struct {
		name  string
		hold  time.Duration
		ready bool
	}{
		{"released within startup budget", 65 * time.Millisecond, true},
		{"beyond actual readiness deadline", 230 * time.Millisecond, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, err := os.MkdirTemp("/tmp", "yj-mcp-ready-")
			if err != nil {
				t.Fatal(err)
			}
			defer os.RemoveAll(root)
			lockFile, err := os.OpenFile(filepath.Join(root, "native.lock"), os.O_CREATE|os.O_RDWR, 0o600)
			if err != nil {
				t.Fatal(err)
			}
			defer lockFile.Close()
			if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(root, "ready.sock")
			done := make(chan struct{})
			go func() {
				defer close(done)
				f, err := os.OpenFile(lockFile.Name(), os.O_RDWR, 0)
				if err != nil {
					return
				}
				defer f.Close()
				deadline := time.Now().Add(150 * time.Millisecond)
				for time.Now().Before(deadline) {
					if syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) == nil {
						defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
						ln, err := net.Listen("unix", path)
						if err != nil {
							return
						}
						defer ln.Close()
						<-time.After(55 * time.Millisecond)
						return
					}
					time.Sleep(time.Millisecond)
				}
			}()
			released := make(chan struct{})
			time.AfterFunc(tc.hold, func() { _ = syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN); close(released) })
			failure := o.waitServiceReadyUntilWithTimeout(time.Now().Add(o.serviceReadyTimeoutFor(hd)), func() bool { return socketConnectable(path, time.Millisecond) }, make(chan struct{}), nil, &exec.Cmd{}, o.serviceReadyTimeoutFor(hd))
			<-done
			<-released
			if tc.ready && failure != "" {
				t.Fatalf("within allowance refused: %s", failure)
			}
			if !tc.ready && failure == "" {
				t.Fatal("late lock release started service beyond startup deadline")
			}
		})
	}
}
