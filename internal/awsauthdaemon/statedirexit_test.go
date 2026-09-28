package awsauthdaemon

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/awsauth"
	"github.com/mschulkind-oss/yolo-jail/internal/hostservice"
)

// THE CALL SITE: the real Main, serving, returns once its state dir is removed, and the dir
// stays removed. Delete Main's hostservice.WatchStateDir call and Main serves on past the
// deadline. See internal/hostservice/statedir.go for why a host-wide daemon outlives its
// state dir at all.
//
// The only `aws` this runs is a stub answering `--version`, the one invocation Main makes at
// spawn; --no-background-refresh keeps the proactive minter from asking for more.
func TestMainExitsWhenItsStateDirIsRemoved(t *testing.T) {
	prev := hostservice.StateDirPollInterval
	hostservice.StateDirPollInterval = 20 * time.Millisecond
	t.Cleanup(func() { hostservice.StateDirPollInterval = prev })
	t.Setenv("HOME", t.TempDir()) // reportStartup reads ~/.aws/config

	stub := filepath.Join(t.TempDir(), "aws")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\necho aws-cli/2.99.0\n"), 0o755); err != nil {
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
	if st, err := os.Stat(stateDir); err != nil || !st.IsDir() {
		t.Fatalf("Main did not create its state dir at startup (%v)", err)
	}

	if err := os.RemoveAll(stateDir); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-rc:
		if got != 0 {
			t.Errorf("Main exited %d for a removed state dir, want 0 (a clean shutdown)", got)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Main is still serving 10s after its state dir was removed")
	}
	if _, err := os.Lstat(stateDir); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the state dir came back after the daemon exited (%v)", err)
	}
}
