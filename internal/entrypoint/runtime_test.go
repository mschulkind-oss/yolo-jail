package entrypoint

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// writeSupervisor creates the smallest possible yolo-jaild stand-in. The
// entrypoint owns the first exec in the readiness chain, so this test pins that
// it waits for an actual acknowledgement rather than merely a successful spawn.
func writeSupervisor(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "yolo-jaild")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestJailDaemonSupervisorWaitsForReadinessAcknowledgement(t *testing.T) {
	bin := writeSupervisor(t, `printf 'ready wire-bridge\n' >&3`)
	t.Setenv("PATH", filepath.Dir(bin))
	oldPIDFile := supervisorPIDFile
	supervisorPIDFile = filepath.Join(t.TempDir(), "yolo-jaild.pid")
	t.Cleanup(func() { supervisorPIDFile = oldPIDFile })

	e := NewEnv(map[string]string{
		"YOLO_JAIL_DAEMONS":           "present",
		paths.JailDaemonReadyNamesEnv: "wire-bridge",
	})
	if err := startJailDaemonSupervisor(e); err != nil {
		t.Fatalf("startJailDaemonSupervisor() = %v, want readiness acknowledgement", err)
	}
}

func TestJailDaemonSupervisorRefusesNegativeReadiness(t *testing.T) {
	bin := writeSupervisor(t, `printf 'failed wire-bridge\n' >&3`)
	t.Setenv("PATH", filepath.Dir(bin))
	oldPIDFile := supervisorPIDFile
	supervisorPIDFile = filepath.Join(t.TempDir(), "yolo-jaild.pid")
	t.Cleanup(func() { supervisorPIDFile = oldPIDFile })

	e := NewEnv(map[string]string{
		"YOLO_JAIL_DAEMONS":           "present",
		paths.JailDaemonReadyNamesEnv: "wire-bridge",
	})
	if err := startJailDaemonSupervisor(e); err == nil {
		t.Fatal("startJailDaemonSupervisor() succeeded after negative readiness")
	}
}

func TestJailDaemonSupervisorRefusesExitBeforeReadiness(t *testing.T) {
	bin := writeSupervisor(t, `exit 0`)
	t.Setenv("PATH", filepath.Dir(bin))
	oldPIDFile := supervisorPIDFile
	supervisorPIDFile = filepath.Join(t.TempDir(), "yolo-jaild.pid")
	t.Cleanup(func() { supervisorPIDFile = oldPIDFile })

	e := NewEnv(map[string]string{
		"YOLO_JAIL_DAEMONS":           "present",
		paths.JailDaemonReadyNamesEnv: "wire-bridge",
	})
	if err := startJailDaemonSupervisor(e); err == nil {
		t.Fatal("startJailDaemonSupervisor() succeeded after supervisor exited before readiness")
	}
}
