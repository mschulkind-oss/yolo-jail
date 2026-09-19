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

func TestFindOrphanedJailDaemonsMatchesOnlyDeclaredCommand(t *testing.T) {
	oldProcRoot := procRoot
	procRoot = t.TempDir()
	t.Cleanup(func() { procRoot = oldProcRoot })
	writeProcCmdline := func(pid, cmdline string) {
		t.Helper()
		dir := filepath.Join(procRoot, pid)
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "cmdline"), []byte(cmdline), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeProcCmdline("101", "/opt/yolo-jail/bin/yolo-jaild\x00wire-bridge\x00")
	writeProcCmdline("102", "yolo-jaild\x00a-different-daemon\x00")
	writeProcCmdline("103", "unrelated\x00wire-bridge\x00")

	orphans, err := findOrphanedJailDaemons(`[{"name":"wire-bridge","cmd":["yolo-jaild","wire-bridge"]}]`)
	if err != nil {
		t.Fatal(err)
	}
	if len(orphans) != 1 || orphans[0] != (orphanedJailDaemon{Name: "wire-bridge", PID: 101}) {
		t.Fatalf("findOrphanedJailDaemons() = %#v, want only wire-bridge pid 101", orphans)
	}
}

func TestReclaimOrphanedJailDaemonsReportsAndKillsExactOrphan(t *testing.T) {
	oldFind, oldKill := findJailDaemonOrphans, killJailDaemonOrphan
	findJailDaemonOrphans = func(string) ([]orphanedJailDaemon, error) {
		return []orphanedJailDaemon{{Name: "wire-bridge", PID: 8214}}, nil
	}
	var killed []int
	killJailDaemonOrphan = func(pid int) error {
		killed = append(killed, pid)
		return nil
	}
	t.Cleanup(func() {
		findJailDaemonOrphans, killJailDaemonOrphan = oldFind, oldKill
	})

	var stderr bytes.Buffer
	e := NewEnv(map[string]string{"YOLO_JAIL_DAEMONS": "present"})
	e.Stderr = &stderr
	if err := reclaimOrphanedJailDaemons(e); err != nil {
		t.Fatal(err)
	}
	if len(killed) != 1 || killed[0] != 8214 {
		t.Fatalf("killed = %v, want only pid 8214", killed)
	}
	if got := stderr.String(); !strings.Contains(got, "reclaiming orphaned in-jail daemon wire-bridge (pid 8214)") || !strings.Contains(got, "reclaimed orphaned in-jail daemon wire-bridge (pid 8214)") {
		t.Fatalf("reclaim must report its action directly, got:\n%s", got)
	}
}
