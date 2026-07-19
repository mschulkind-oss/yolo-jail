package runcmd

import (
	"os"
	"path/filepath"
	"testing"
)

// TestDaemonLauncherSeam2 covers the go-port launcher swap
// (_daemon_launcher): a console-script daemon named in YOLO_GO_DAEMONS resolves
// to the Go binary at $YOLO_GO_BIN_DIR when it exists+executable; otherwise it
// falls back to the console-script name on PATH. This is the decision that the
// external-service spawn path uses so the full-Go run launches the Go
// yolo-host-processes daemon instead of the Python console script (which isn't
// on the host PATH under the Go front door).
func TestDaemonLauncherSeam2(t *testing.T) {
	binDir := t.TempDir()
	goBin := filepath.Join(binDir, "yolo-host-processes")
	if err := os.WriteFile(goBin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	env := map[string]string{}
	o := &Options{
		Getenv:   func(k string) string { return env[k] },
		LookPath: func(name string) (string, bool) { return "/usr/bin/" + name, true },
	}

	// Not gated → console-script name (PATH fallback), NOT the Go binary.
	env["YOLO_GO_DAEMONS"] = ""
	env["YOLO_GO_BIN_DIR"] = binDir
	if got := o.daemonLauncher("yolo-host-processes"); len(got) != 1 || got[0] != "yolo-host-processes" {
		t.Errorf("ungated: got %v, want [yolo-host-processes] (PATH fallback)", got)
	}

	// Gated + binary present → the Go binary path.
	env["YOLO_GO_DAEMONS"] = "yolo-host-processes,yolo-claude-oauth-broker-host"
	if got := o.daemonLauncher("yolo-host-processes"); len(got) != 1 || got[0] != goBin {
		t.Errorf("gated+present: got %v, want [%s]", got, goBin)
	}

	// Gated but binary missing → falls back to the console-script name.
	env["YOLO_GO_BIN_DIR"] = filepath.Join(binDir, "nonexistent")
	if got := o.daemonLauncher("yolo-host-processes"); len(got) != 1 || got[0] != "yolo-host-processes" {
		t.Errorf("gated+missing: got %v, want [yolo-host-processes] (fallback)", got)
	}

	// Gated, binary missing, AND not on PATH → nil (nothing to launch).
	o.LookPath = func(string) (string, bool) { return "", false }
	if got := o.daemonLauncher("yolo-host-processes"); got != nil {
		t.Errorf("gated+missing+not-on-PATH: got %v, want nil", got)
	}
}

// TestExternalServiceLauncherSwap asserts the swap condition used inside
// startExternalService: a swapped launcher (len!=1 or differing token) replaces
// only cmd[0], keeping the substituted tail. This matches the // `if launcher != [cmd[0]]: cmd = [*launcher, *cmd[1:]]` guard.
func TestExternalServiceLauncherSwap(t *testing.T) {
	binDir := t.TempDir()
	goBin := filepath.Join(binDir, "yolo-host-processes")
	if err := os.WriteFile(goBin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{
		"YOLO_GO_DAEMONS": "yolo-host-processes",
		"YOLO_GO_BIN_DIR": binDir,
	}
	o := &Options{
		Getenv:   func(k string) string { return env[k] },
		LookPath: func(name string) (string, bool) { return "/usr/bin/" + name, true },
	}
	cmdArgs := []string{"yolo-host-processes", "--socket", "/tmp/x.sock"}
	launcher := o.daemonLauncher(cmdArgs[0])
	swapped := len(launcher) != 1 || launcher[0] != cmdArgs[0]
	if !swapped {
		t.Fatalf("expected swap for gated present binary; launcher=%v", launcher)
	}
	got := append(append([]string{}, launcher...), cmdArgs[1:]...)
	want := []string{goBin, "--socket", "/tmp/x.sock"}
	if len(got) != len(want) {
		t.Fatalf("swapped argv len %d, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("argv[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
