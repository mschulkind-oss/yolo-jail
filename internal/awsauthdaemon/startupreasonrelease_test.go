package awsauthdaemon

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/awsauth"
	"github.com/mschulkind-oss/yolo-jail/internal/hostservice"
)

// THE STARTUP-REASON CHANNEL DOES NOT OUTLIVE STARTUP. The real Main, handed a channel shaped like
// the one its owner passes, runs `aws --version` without the child inheriting the channel's fd,
// and once serving has closed that fd and unset the three variables, so the owner reads no record
// and nothing the daemon starts later sees them. Delete Main's ProtectStartupReason or
// ReleaseStartupReason call and this fails.
func TestMainKeepsTheStartupReasonChannelFromItsChildrenAndReleasesItOnceServing(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the stub inspects /proc/self/fd")
	}
	prev := hostservice.StateDirPollInterval
	hostservice.StateDirPollInterval = 20 * time.Millisecond
	t.Cleanup(func() { hostservice.StateDirPollInterval = prev })
	t.Setenv("HOME", t.TempDir())

	parent, child, attempt, err := hostservice.NewStartupReasonChannel()
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	fd, err := syscall.Dup(int(child.Fd()))
	_ = child.Close()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(hostservice.StartupReasonFDEnv, strconv.Itoa(fd))
	t.Setenv(hostservice.StartupReasonAttemptEnv, attempt)
	t.Setenv(hostservice.StartupReasonServiceEnv, LoopholeName)

	marker := filepath.Join(t.TempDir(), "child-saw")
	stub := filepath.Join(t.TempDir(), "aws")
	script := "#!/bin/sh\nif [ -e /proc/self/fd/" + strconv.Itoa(fd) + " ]; then echo fd >> " + marker + "; fi\n" +
		"echo aws-cli/2.99.0\n"
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	settings := settingsFileWith(t, `{"profile":"bedrock","unnarrowed":true}`)
	sock := filepath.Join(shortTempDir(t), "d.sock")
	stateDir := filepath.Join(t.TempDir(), "state", LoopholeName)
	rc := make(chan int, 1)
	go func() {
		rc <- Main([]string{"--socket", sock, "--settings", settings, "--aws-binary", stub,
			"--no-background-refresh", "--state-file", filepath.Join(stateDir, awsauth.StateFileName)})
	}()
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if _, err := os.Stat(sock); err == nil {
			break
		}
		select {
		case got := <-rc:
			t.Fatalf("Main returned %d before serving", got)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("Main never bound its socket")
		}
	}
	if raw, err := os.ReadFile(marker); err == nil && strings.Contains(string(raw), "fd") {
		t.Error("the `aws --version` child inherited the startup-reason fd")
	}
	for _, name := range []string{hostservice.StartupReasonFDEnv, hostservice.StartupReasonAttemptEnv,
		hostservice.StartupReasonServiceEnv} {
		if v, set := os.LookupEnv(name); set {
			t.Errorf("%s is still set (%q) after the daemon began serving", name, v)
		}
	}
	got := hostservice.ReadStartupReasonOutcome(nil, parent, LoopholeName, attempt, time.Now().Add(2*time.Second))
	if got.Kind != hostservice.StartupReasonReadNoRecord {
		t.Errorf("owner read = %+v, want no record (the serving daemon closed its end)", got)
	}
	if err := os.RemoveAll(stateDir); err != nil {
		t.Fatal(err)
	}
	select {
	case <-rc:
	case <-time.After(10 * time.Second):
		t.Fatal("Main is still serving 10s after its state dir was removed")
	}
}
