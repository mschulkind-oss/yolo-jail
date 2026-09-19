package entrypoint

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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

	var stderr bytes.Buffer
	e := NewEnv(map[string]string{
		"YOLO_JAIL_DAEMONS":           "present",
		paths.JailDaemonReadyNamesEnv: "wire-bridge",
	})
	e.Stderr = &stderr
	if err := startJailDaemonSupervisor(e); err != nil {
		t.Fatalf("startJailDaemonSupervisor() = %v, want readiness acknowledgement", err)
	}
	if !strings.Contains(stderr.String(), "waiting for required in-jail service readiness: wire-bridge") ||
		!strings.Contains(stderr.String(), "wire-bridge.log") {
		t.Errorf("readiness wait must name the service and its diagnostics, got:\n%s", stderr.String())
	}
}

func TestJailDaemonSupervisorRefusesNegativeReadiness(t *testing.T) {
	bin := writeSupervisor(t, `printf 'failed wire-bridge no-route\n' >&3`)
	t.Setenv("PATH", filepath.Dir(bin))
	oldPIDFile := supervisorPIDFile
	supervisorPIDFile = filepath.Join(t.TempDir(), "yolo-jaild.pid")
	t.Cleanup(func() { supervisorPIDFile = oldPIDFile })

	e := NewEnv(map[string]string{
		"YOLO_JAIL_DAEMONS":           "present",
		paths.JailDaemonReadyNamesEnv: "wire-bridge",
	})
	if err := startJailDaemonSupervisor(e); err == nil || !strings.Contains(err.Error(), "no-route") {
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

func TestJailDaemonSupervisorReusesLegacySupervisor(t *testing.T) {
	bin := writeSupervisor(t, `exit 99`)
	t.Setenv("PATH", filepath.Dir(bin))
	oldPIDFile, oldLegacy := supervisorPIDFile, legacySupervisorPIDFiles
	supervisorPIDFile = filepath.Join(t.TempDir(), "yolo-jaild.pid")
	legacyPIDFile := filepath.Join(t.TempDir(), "yolo-jail-supervisor.pid")
	legacySupervisorPIDFiles = []string{legacyPIDFile}
	t.Cleanup(func() {
		supervisorPIDFile, legacySupervisorPIDFiles = oldPIDFile, oldLegacy
	})
	if err := os.WriteFile(legacyPIDFile, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	e := NewEnv(map[string]string{
		"YOLO_JAIL_DAEMONS":           "present",
		paths.JailDaemonReadyNamesEnv: "wire-bridge",
	})
	if err := startJailDaemonSupervisor(e); err != nil {
		t.Fatalf("startJailDaemonSupervisor() = %v, want reuse of the live legacy supervisor", err)
	}
}
