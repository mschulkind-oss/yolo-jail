package entrypoint

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// nativelauncherjailstart_test.go pins the NATIVE launcher's update rule: an unfrozen program is
// checked ONCE PER JAIL START, at its first invocation, and never by a clock
// (docs/design/launcher-update-per-jail-start.md, LU-D1..D10, slice 2). It is the native twin of
// npmlauncherjailstart_test.go and takes the same table of cases over the pack's update verb and
// the vendor installer re-run, the two acts a native launcher has.
//
// The check is a CLAIM on <state>/launch-checks/<bin>.<id>, <id> being the record the boot wrote.
// Each test writes the record itself (newJailStart), drives the real generated launcher against a
// fake vendor program that logs its argv, and waits on events only: the race and signal tests
// block the fake updater on named pipes. No sleep is a pass condition and nothing reaches a network.

const nativeUpdateLine = "RAN:update"

// newJailStart writes a fresh jail-start record, as the boot's write_jail_start does, and returns
// its id: the next launcher invocation is the first of a new jail start.
func (p *updateProbe) newJailStart(t *testing.T) string {
	t.Helper()
	p.starts++
	id := "start" + strconv.Itoa(p.starts)
	dir := filepath.Join(p.home, ".local", "state", "yolo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "jail-start"), []byte(id+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return id
}

// markers lists the check markers of the probe's program under the launch-checks dir.
func (p *updateProbe) markers(t *testing.T) []string {
	t.Helper()
	got, err := filepath.Glob(filepath.Join(p.home, filepath.FromSlash(LaunchChecksDirRel), "probetool.*"))
	if err != nil {
		t.Fatal(err)
	}
	for i := range got {
		got[i] = filepath.Base(got[i])
	}
	return got
}

func (p *updateProbe) truncateLog(t *testing.T) {
	t.Helper()
	if err := os.WriteFile(p.log, nil, 0o644); err != nil {
		t.Fatal(err)
	}
}

// relaunch regenerates the probe's launcher with mutate applied to its install declaration.
func (p *updateProbe) relaunch(t *testing.T, mutate func(*packdecl.Install)) {
	t.Helper()
	inst := &packdecl.Install{Kind: "native", Bin: "probetool", InstallerURL: "https://example.invalid/never-fetched.sh", UpdateVerb: []string{"update"}}
	mutate(inst)
	body := nativeAgentLauncher("probe", inst, p.stamps,
		filepath.Join(p.home, "ws", ".yolo", "receipts.jsonl"), "", true, launcherServers{}, nil)
	if err := os.WriteFile(p.script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

// failingUpdate re-points the fake program at one whose update verb exits 7.
func (p *updateProbe) failingUpdate(t *testing.T) {
	t.Helper()
	body := "#!/bin/bash\nprintf '%s\\n' \"RAN:$*\" >> " + shellQuoteForTest(p.log) + "\n" +
		"if [ \"${1:-}\" = update ]; then exit 7; fi\n"
	if err := os.WriteFile(p.realBin, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

func countLines(log []string, want string) int {
	n := 0
	for _, l := range log {
		if l == want {
			n++
		}
	}
	return n
}

// The evergreen act, once per start: the first invocation of a start runs the update and then the
// program; the second runs the program alone; a NEW jail start updates again.
func TestNativeLauncherChecksOncePerJailStart(t *testing.T) {
	p := newUpdateProbe(t, []string{"update"}, true, true)
	id := p.newJailStart(t)

	out, rc := p.run(t)
	if got := p.argvLog(t); rc != 0 || len(got) != 2 || got[0] != nativeUpdateLine || got[1] != "RAN:" {
		t.Fatalf("the first invocation of a start updates, then launches (rc=%d), got %v\n%s", rc, got, out)
	}
	if got := p.markers(t); len(got) != 1 || got[0] != "probetool."+id {
		t.Errorf("the check leaves one marker named for the start id, got %v", got)
	}

	p.truncateLog(t)
	if _, rc := p.run(t); rc != 0 {
		t.Fatalf("second run rc=%d", rc)
	}
	if got := p.argvLog(t); len(got) != 1 || got[0] != "RAN:" {
		t.Errorf("a second invocation of the same start launches and nothing else, got %v", got)
	}

	p.newJailStart(t)
	p.truncateLog(t)
	if _, rc := p.run(t); rc != 0 {
		t.Fatalf("third run rc=%d", rc)
	}
	if got := p.argvLog(t); countLines(got, nativeUpdateLine) != 1 {
		t.Errorf("a NEW jail start checks again, got %v", got)
	}
}

// The same rule over the vendor installer re-run (a pack that declares no verb).
func TestNativeLauncherInstallerReRunIsOncePerJailStart(t *testing.T) {
	url := installerURL(t, "#!/bin/bash\nexit 0\n")
	p := newUpdateProbeAt(t, nil, true, true, url)
	id := p.newJailStart(t)

	out, rc := p.run(t)
	if rc != 0 || !strings.Contains(out, "re-running its installer") {
		t.Fatalf("the first invocation re-runs the installer (rc=%d):\n%s", rc, out)
	}
	if got := p.markers(t); len(got) != 1 || got[0] != "probetool."+id {
		t.Errorf("one marker named for the start id, got %v", got)
	}
	out, _ = p.run(t)
	if strings.Contains(out, "re-running its installer") {
		t.Errorf("the second invocation of the start must not re-run the installer:\n%s", out)
	}
	p.newJailStart(t)
	if out, _ = p.run(t); !strings.Contains(out, "re-running its installer") {
		t.Errorf("a new start re-runs it:\n%s", out)
	}
}

// Nothing answered (the verb failed with its upstream unreachable, or the installer could not be
// downloaded) KEEPS the marker (LU-D2): the installed version runs and nothing retries this start.
func TestNativeLauncherKeepsTheMarkerWhenNothingAnswers(t *testing.T) {
	t.Run("verb, upstream silent", func(t *testing.T) {
		p := newUpdateProbeAt(t, []string{"update"}, true, true, silentHost(t))
		p.failingUpdate(t)
		p.newJailStart(t)
		out, rc := p.run(t)
		if rc != 0 || !strings.Contains(out, "could not be reached to check for a newer version") {
			t.Fatalf("the installed version runs, saying so (rc=%d):\n%s", rc, out)
		}
		if m := p.markers(t); len(m) != 1 {
			t.Errorf("an unanswered check keeps its marker, got %v", m)
		}
		p.truncateLog(t)
		p.run(t)
		if got := p.argvLog(t); len(got) != 1 || got[0] != "RAN:" {
			t.Errorf("no retry until the next start, got %v", got)
		}
	})
	t.Run("installer, download failed", func(t *testing.T) {
		p := newUpdateProbeAt(t, nil, true, true, silentHost(t))
		p.newJailStart(t)
		out, rc := p.run(t)
		for _, want := range []string{"could not download its installer to check for a newer version — running the installed version",
			"the next time this jail starts", "yolo pack update"} {
			if rc != 0 || !strings.Contains(out, want) {
				t.Errorf("output lacks %q (rc=%d):\n%s", want, rc, out)
			}
		}
		if m := p.markers(t); len(m) != 1 {
			t.Errorf("an unanswered check keeps its marker, got %v", m)
		}
	})
}

// A newer version whose update failed while its upstream answered REFUSES (exit 75, PD-NF1) and
// RELEASES the marker, so the next invocation retries (LU-D10).
func TestNativeLauncherReleasesTheMarkerOnARefusal(t *testing.T) {
	p := newUpdateProbeAt(t, []string{"update"}, true, true, answeringHost(t))
	p.failingUpdate(t)
	p.newJailStart(t)

	out, rc := p.run(t)
	if rc != UpdateRefusedStatus {
		t.Fatalf("exit %d, want the refusal's %d:\n%s", rc, UpdateRefusedStatus, out)
	}
	if m := p.markers(t); len(m) != 0 {
		t.Errorf("a refusal releases the marker, got %v", m)
	}
	p.truncateLog(t)
	if _, rc := p.run(t); rc != UpdateRefusedStatus || countLines(p.argvLog(t), nativeUpdateLine) != 1 {
		t.Errorf("the next invocation retries the update (rc=%d), got %v", rc, p.argvLog(t))
	}
}

// A held install lock releases the marker: the launcher ran the installed version without
// checking, so the next invocation of the start may.
func TestNativeLauncherReleasesTheMarkerWhenTheLockIsHeld(t *testing.T) {
	p := newUpdateProbe(t, []string{"update"}, true, true)
	p.newJailStart(t)
	lock := filepath.Join(p.home, ".local", ".yolo-update.lock")
	if err := os.MkdirAll(lock, 0o755); err != nil {
		t.Fatal(err)
	}
	out, rc := p.run(t)
	if got := p.argvLog(t); rc != 0 || len(got) != 1 || got[0] != "RAN:" || !strings.Contains(out, "another update is in progress") {
		t.Fatalf("a held lock means launch without updating, and says so (rc=%d) %v\n%s", rc, got, out)
	}
	if m := p.markers(t); len(m) != 0 {
		t.Errorf("a held lock releases the marker, got %v", m)
	}
	if err := os.Remove(lock); err != nil {
		t.Fatal(err)
	}
	p.truncateLog(t)
	p.run(t)
	if countLines(p.argvLog(t), nativeUpdateLine) != 1 {
		t.Errorf("once the lock is free the same start checks, got %v", p.argvLog(t))
	}
}

// A SIGTERM mid-act releases the marker (the shield's cleanup); the update is the fake verb,
// blocked on a named pipe until the launcher has been signalled.
func TestNativeLauncherReleasesTheMarkerWhenSignalled(t *testing.T) {
	p := newUpdateProbe(t, []string{"update"}, true, true)
	p.newJailStart(t)
	started, release := p.fifos(t)
	p.blockingUpdate(t, started, release)

	cmd := p.command(t)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	waitFifo(t, started, done)
	if m := p.markers(t); len(m) != 1 {
		t.Fatalf("the in-flight check holds the claim, got %v", m)
	}
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	// bash runs a trap once the foreground command it is waiting on returns, so the fake updater is
	// let go AFTER the signal is queued: the trap, and the claim's release, then run.
	openRelease(t, release)
	<-done
	if m := p.markers(t); len(m) != 0 {
		t.Errorf("a SIGTERM mid-act releases the marker, got %v", m)
	}
}

// A frozen agent_updates, a version probe and an install-only run CLAIM NOTHING: no marker, no
// update, whatever the record says.
func TestNativeLauncherClaimsNothingOutsideAnUnfrozenLaunch(t *testing.T) {
	cases := []struct {
		name    string
		updates bool
		probe   bool
		env     []string
		args    []string
	}{
		{name: "agent_updates held", updates: false},
		{name: "version probe", updates: true, probe: true, args: []string{"--version"}},
		{name: "install only", updates: true, env: []string{InstallOnlyEnv + "=1"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := newUpdateProbe(t, []string{"update"}, tc.updates, true)
			if tc.probe {
				p.relaunch(t, func(i *packdecl.Install) { i.ProbeArgs = []string{"--version"} })
			}
			p.newJailStart(t)
			cmd := p.command(t, tc.args...)
			cmd.Env = append(cmd.Env, tc.env...)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("launcher failed: %v\n%s", err, out)
			}
			if countLines(p.argvLog(t), nativeUpdateLine) != 0 {
				t.Errorf("no update, got %v", p.argvLog(t))
			}
			if m := p.markers(t); len(m) != 0 {
				t.Errorf("claimed nothing, yet markers %v", m)
			}
			if strings.Contains(string(out), "no start record") {
				t.Errorf("a record exists, so the no-record line must not appear:\n%s", out)
			}
		})
	}
}

// No jail-start record: the launcher checks NOTHING, runs the installed version, and says so with
// its next step (LU-D6). It never mints a record itself.
func TestNativeLauncherWithNoStartRecordChecksNothingAndSaysSo(t *testing.T) {
	p := newUpdateProbe(t, []string{"update"}, true, true)
	record := filepath.Join(p.home, filepath.FromSlash(JailStartRel))
	if err := os.Remove(record); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	out, rc := p.run(t)
	if got := p.argvLog(t); rc != 0 || len(got) != 1 || got[0] != "RAN:" {
		t.Fatalf("no record, no check (rc=%d), got %v\n%s", rc, got, out)
	}
	for _, want := range []string{"this jail has no start record, so it checked nothing for updates — running the installed version",
		"Relaunch the jail"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if _, err := os.Stat(record); !os.IsNotExist(err) {
		t.Errorf("a launcher must never write the record (one writer: the boot), stat err=%v", err)
	}
}

// THE FAN-OUT: two launchers of one jail start, the first blocked inside its update on named
// pipes. The second finds the claim, launches at once, and does not update; the update ran once.
func TestNativeLauncherSecondInvocationDoesNotCheckWhileTheFirstIsInFlight(t *testing.T) {
	p := newUpdateProbe(t, []string{"update"}, true, true)
	p.newJailStart(t)
	started, release := p.fifos(t)
	p.blockingUpdate(t, started, release)

	first := p.command(t)
	var firstOut strings.Builder
	first.Stdout, first.Stderr = &firstOut, &firstOut
	if err := first.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- first.Wait() }()
	waitFifo(t, started, done)

	out, err := p.command(t).CombinedOutput()
	if err != nil {
		t.Fatalf("the second launcher failed: %v\n%s", err, out)
	}
	log := p.argvLog(t)
	if countLines(log, nativeUpdateLine) != 1 || countLines(log, "RAN:") != 1 {
		t.Errorf("the update ran once (the first, still blocked) and the second launched, got %v", log)
	}
	openRelease(t, release)
	if err := <-done; err != nil {
		t.Fatalf("the first launcher failed: %v\n%s", err, firstOut.String())
	}
	if got := p.argvLog(t); countLines(got, "RAN:") != 2 {
		t.Errorf("the first launcher launches after its update, got %v", got)
	}
}

func (p *updateProbe) command(t *testing.T, args ...string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(p.script, args...)
	cmd.Dir = p.home
	cmd.Env = []string{"HOME=" + p.home, "PATH=" + os.Getenv("PATH")}
	return cmd
}

func (p *updateProbe) fifos(t *testing.T) (started, release string) {
	t.Helper()
	started, release = filepath.Join(p.home, "update-started"), filepath.Join(p.home, "update-release")
	for _, f := range []string{started, release} {
		if err := syscall.Mkfifo(f, 0o600); err != nil {
			t.Skipf("no named pipes here: %v", err)
		}
	}
	return started, release
}

// blockingUpdate makes the fake program's update verb announce itself on started and wait on
// release before it finishes.
func (p *updateProbe) blockingUpdate(t *testing.T, started, release string) {
	t.Helper()
	body := "#!/bin/bash\nprintf '%s\\n' \"RAN:$*\" >> " + shellQuoteForTest(p.log) + "\n" +
		"if [ \"${1:-}\" = update ]; then echo up > " + shellQuoteForTest(started) + "; read -r _ < " + shellQuoteForTest(release) + "; fi\n"
	if err := os.WriteFile(p.realBin, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

// waitFifo blocks until the fake updater writes to started. The timeout only turns a hang into a
// red test.
func waitFifo(t *testing.T, started string, done <-chan error) {
	t.Helper()
	opened := make(chan struct{})
	go func() {
		if f, err := os.Open(started); err == nil {
			buf := make([]byte, 8)
			_, _ = f.Read(buf)
			f.Close()
		}
		close(opened)
	}()
	select {
	case <-opened:
	case err := <-done:
		t.Fatalf("the launcher ended before its update started: %v", err)
	case <-time.After(60 * time.Second):
		t.Fatal("the launcher never reached its update")
	}
}

func openRelease(t *testing.T, release string) {
	t.Helper()
	f, err := os.OpenFile(release, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString("go\n")
	f.Close()
}

// A successful COLD install installed the latest, so it records this start's marker: the second
// invocation of the same start runs no updater, and a new start updates.
func TestNativeLauncherColdInstallRecordsTheStartsMarker(t *testing.T) {
	url := installerURL(t, "#!/bin/bash\nmkdir -p \"$HOME/.local/bin\"\nprintf '#!/bin/bash\\necho \"RAN:$*\" >> "+"\"$HOME/argv.log\"\\n' > \"$HOME/.local/bin/probetool\"\nchmod +x \"$HOME/.local/bin/probetool\"\n")
	p := newUpdateProbeAt(t, []string{"update"}, true, false, url)
	id := p.newJailStart(t)
	p.log = filepath.Join(p.home, "argv.log")

	out, rc := p.run(t)
	if rc != 0 {
		t.Fatalf("cold launch rc=%d:\n%s", rc, out)
	}
	if m := p.markers(t); len(m) != 1 || m[0] != "probetool."+id {
		t.Errorf("a cold install records the start's marker, got %v\n%s", m, out)
	}
	p.truncateLog(t)
	if _, rc := p.run(t); rc != 0 {
		t.Fatalf("second run rc=%d", rc)
	}
	if got := p.argvLog(t); len(got) != 1 || got[0] != "RAN:" {
		t.Errorf("the second invocation of the start launches and does not update, got %v", got)
	}
	p.newJailStart(t)
	p.truncateLog(t)
	p.run(t)
	if countLines(p.argvLog(t), nativeUpdateLine) != 1 {
		t.Errorf("a new start updates, got %v", p.argvLog(t))
	}
}
