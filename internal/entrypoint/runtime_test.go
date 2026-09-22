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

// TestOrphanedJailDaemonRefusesAndNamesThePID pins OQ-PC3's ruling: the detection stays,
// the SIGKILL is gone, and the boot refuses carrying the PID.
//
// It asserts the REFUSAL rather than the absence of a kill, because "no kill happened" is
// also true of a build where the whole detection was deleted — and the detection is the
// half the ruling kept.
func TestOrphanedJailDaemonRefusesAndNamesThePID(t *testing.T) {
	oldFind := findJailDaemonOrphans
	findJailDaemonOrphans = func(string) ([]orphanedJailDaemon, error) {
		return []orphanedJailDaemon{{Name: "wire-bridge", PID: 8214}}, nil
	}
	t.Cleanup(func() { findJailDaemonOrphans = oldFind })

	var stderr bytes.Buffer
	e := NewEnv(map[string]string{"YOLO_JAIL_DAEMONS": "present"})
	e.Stderr = &stderr

	err := refuseOnOrphanedJailDaemons(e)
	if err == nil {
		t.Fatal("an orphaned daemon must REFUSE the boot; got nil — the ruling replaced the " +
			"SIGKILL with a refusal, not with silence")
	}
	if !strings.Contains(err.Error(), "8214") || !strings.Contains(err.Error(), "wire-bridge") {
		t.Errorf("the refusal must name the daemon and its PID — that is the one fact that makes "+
			"it actionable; got: %v", err)
	}
	if !strings.Contains(err.Error(), "kill <pid>") {
		t.Errorf("the refusal must hand the operator the command yolo declined to run itself; got: %v", err)
	}
	if got := stderr.String(); !strings.Contains(got, "wire-bridge is running as pid 8214") {
		t.Errorf("the orphan must be reported per-daemon on stderr, got:\n%s", got)
	}
}

// TestStartSupervisorRefusesOnAnOrphan pins the CALL SITE, not just the callee.
//
// This repo has shipped the other shape repeatedly: a test that exercises a helper
// directly stays green when the production caller is deleted, so the guard can be switched
// off wholesale with the unit gate passing. Delete the refuseOnOrphanedJailDaemons call
// from startJailDaemonSupervisor and this test goes red; the two tests above do not.
func TestStartSupervisorRefusesOnAnOrphan(t *testing.T) {
	oldPIDFile, oldLegacy, oldFind := supervisorPIDFile, legacySupervisorPIDFiles, findJailDaemonOrphans
	dir := t.TempDir()
	supervisorPIDFile = filepath.Join(dir, "absent-yolo-jaild.pid")
	legacySupervisorPIDFiles = []string{filepath.Join(dir, "absent-legacy.pid")}
	findJailDaemonOrphans = func(string) ([]orphanedJailDaemon, error) {
		return []orphanedJailDaemon{{Name: "wire-bridge", PID: 8214}}, nil
	}
	t.Cleanup(func() {
		supervisorPIDFile, legacySupervisorPIDFiles, findJailDaemonOrphans = oldPIDFile, oldLegacy, oldFind
	})

	var stderr bytes.Buffer
	e := NewEnv(map[string]string{"YOLO_JAIL_DAEMONS": `[{"name":"wire-bridge","cmd":["yolo-jaild","wire-bridge"]}]`})
	e.Stderr = &stderr

	err := startJailDaemonSupervisor(e)
	if err == nil {
		t.Fatal("startJailDaemonSupervisor must propagate the orphan refusal; got nil — either the " +
			"call was removed or its error is being swallowed")
	}
	if !strings.Contains(err.Error(), "8214") {
		t.Errorf("the refusal must reach the caller intact, PID included; got: %v", err)
	}
}

// TestNoOrphansIsNotARefusal keeps the common path honest: the refusal fires on a FINDING,
// never on the check having run.
func TestNoOrphansIsNotARefusal(t *testing.T) {
	oldFind := findJailDaemonOrphans
	findJailDaemonOrphans = func(string) ([]orphanedJailDaemon, error) { return nil, nil }
	t.Cleanup(func() { findJailDaemonOrphans = oldFind })

	var stderr bytes.Buffer
	e := NewEnv(map[string]string{"YOLO_JAIL_DAEMONS": "present"})
	e.Stderr = &stderr
	if err := refuseOnOrphanedJailDaemons(e); err != nil {
		t.Fatalf("no orphans must not refuse: %v", err)
	}
	if stderr.String() != "" {
		t.Errorf("no orphans must say nothing, got:\n%s", stderr.String())
	}
}
