package entrypoint

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/progress"
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
	// A WAIT THAT ENDS AT ONCE PRINTS NOTHING (docs/reference/report-tiers.md, progress lines:
	// a step that ends within two seconds prints nothing). The wait used to announce itself and
	// the daemon's log path on every launch, ready or not.
	if stderr.String() != "" {
		t.Errorf("a readiness wait that ended at once printed:\n%s", stderr.String())
	}
}

// A READINESS FAILURE NAMES THE DAEMON'S LOG, the one diagnostic a refused boot leaves behind
// (wire-bridge.md, where the post-mortem lives): the path is in the refusal itself, because a
// fast failure prints no progress line to carry it.
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
	err := startJailDaemonSupervisor(e)
	if err == nil || !strings.Contains(err.Error(), "no-route") {
		t.Fatal("startJailDaemonSupervisor() succeeded after negative readiness")
	}
	wantLog := filepath.Join(e.Home, ".local", "state", "yolo-jail-daemons", "wire-bridge.log")
	if !strings.Contains(err.Error(), wantLog) {
		t.Errorf("the readiness refusal does not name the daemon's log %s:\n%v", wantLog, err)
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
	err := startJailDaemonSupervisor(e)
	if err == nil {
		t.Fatal("startJailDaemonSupervisor() succeeded after supervisor exited before readiness")
	}
	if !strings.Contains(err.Error(), "wire-bridge.log") {
		t.Errorf("the refusal does not name the daemon's log:\n%v", err)
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

// The readiness wait has no bound of its own, so it runs under a progress line that names
// the services it waits for, counts them as they report and closes with the verdict; a
// failed one closes naming the daemon's log, a ready one does not. Immediate shows it
// although the fake supervisor answers at once.
func TestJailDaemonReadinessWaitIsNarrated(t *testing.T) {
	const logPath = "/home/agent/.local/state/yolo-jail-daemons/wire-bridge.log"
	for _, tc := range []struct {
		name, body, want string
		names            bool
	}{
		{"ready", `printf 'ready wire-bridge\n' >&3`,
			"Waiting for in-jail services (wire-bridge): done — 1 of 1 ready (", false},
		{"failed", `printf 'failed wire-bridge no-route\n' >&3`,
			"Waiting for in-jail services (wire-bridge): failed — 0 of 1 ready; daemon log: " + logPath + " (", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bin := writeSupervisor(t, tc.body)
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
			e.progressCfg = progress.Config{Immediate: true}
			_ = startJailDaemonSupervisor(e)
			got := stderr.String()
			if !strings.Contains(got, "Waiting for in-jail services (wire-bridge)…\n") ||
				!strings.Contains(got, tc.want) {
				t.Errorf("want the wait narrated and closed with %q, got:\n%s", tc.want, got)
			}
			if strings.Contains(got, logPath) != tc.names {
				t.Errorf("names the daemon log = %v, want %v:\n%s", !tc.names, tc.names, got)
			}
			if strings.Contains(got, "Daemon diagnostics") || strings.Contains(got, "waiting for required") {
				t.Errorf("the wait printed a line of its own beside its progress line:\n%s", got)
			}
		})
	}
}

// A SLOW WAIT NAMES THE LOG WHILE IT WAITS: once the progress line shows (past its grace), its
// detail carries the daemon's log path, so a wait that hangs points at the one file that says
// why. Heartbeats are forced every tick here; the supervisor answers after a few of them.
func TestASlowReadinessWaitNamesTheDaemonLog(t *testing.T) {
	const logPath = "/home/agent/.local/state/yolo-jail-daemons/wire-bridge.log"
	bin := writeSupervisor(t, `sleep 0.3; printf 'ready wire-bridge\n' >&3`)
	// The real PATH after the stand-in's dir, for its `sleep`.
	t.Setenv("PATH", filepath.Dir(bin)+string(os.PathListSeparator)+os.Getenv("PATH"))
	oldPIDFile := supervisorPIDFile
	supervisorPIDFile = filepath.Join(t.TempDir(), "yolo-jaild.pid")
	t.Cleanup(func() { supervisorPIDFile = oldPIDFile })

	tick := make(chan time.Time)
	stop := make(chan struct{})
	ticking := make(chan struct{})
	go func() {
		defer close(ticking)
		for {
			select {
			case tick <- time.Now():
			case <-stop:
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()

	var stderr syncBuffer
	e := NewEnv(map[string]string{
		"YOLO_JAIL_DAEMONS":           "present",
		paths.JailDaemonReadyNamesEnv: "wire-bridge",
	})
	e.Stderr = &stderr
	e.progressCfg = progress.Config{Grace: time.Nanosecond, Heartbeat: time.Nanosecond, Tick: tick}
	err := startJailDaemonSupervisor(e)
	close(stop)
	<-ticking
	if err != nil {
		t.Fatalf("startJailDaemonSupervisor() = %v", err)
	}
	got := stderr.String()
	if !strings.Contains(got, "0 of 1 ready; daemon log: "+logPath) {
		t.Errorf("a slow wait never named the daemon's log:\n%s", got)
	}
	if !strings.Contains(got, "Waiting for in-jail services (wire-bridge): done — 1 of 1 ready (") {
		t.Errorf("a slow wait did not close with its verdict alone:\n%s", got)
	}
}
