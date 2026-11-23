package entrypoint

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// npmlauncherjailstart_test.go pins the npm launcher's update rule: an UNPINNED program is
// checked ONCE PER JAIL START, at its first invocation, and never by a clock
// (docs/design/launcher-update-per-jail-start.md, LU-D1..D10).
//
// The check is a CLAIM: the launcher creates <state>/launch-checks/<bin>.<id> with noclobber
// (O_EXCL), where <id> is the jail-start record the boot wrote. The creator checks; every other
// invocation of the same start finds the marker and runs the installed version at once. These
// tests drive the generated launcher against a fake `npm` that logs each call, write the record
// themselves (newJailStart), and wait on events only: the race test blocks `npm view` on a named
// pipe, so no pass condition is a sleep or an elapsed time.
//
// History of this cell, kept because the defended behaviour has been inverted before: the hourly
// check REINSTALLED (until 2026-08-17), then only REPORTED (2026-08-18, trust-paths.md §1), then
// UPDATED again once OQ-PD3 was narrowed to project dependencies (2026-09-03, OQ-PD12). The hour
// itself was dropped on 2026-11-23 (LU-D1). The first install is untouched by any of it.

const (
	installNewer = "install -g --prefer-online tool@latest"
	viewTool     = "view tool version"
)

// The evergreen act, once per start: the first invocation of a jail start asks the registry and
// installs the newer version, saying which two versions it moved between; the second asks
// nothing and runs the installed one; a NEW jail start asks again.
func TestUnpinnedNpmLauncherChecksOncePerJailStart(t *testing.T) {
	p := newNpmProbe(t, "tool")
	log := p.run(t, "tool", "tool", "FAKE_INSTALLED_VERSION=1.0.0")
	if !hasArgv(log, installNewer) {
		t.Fatalf("an unversioned package must still install @latest on FIRST use:\n%s", strings.Join(log, "\n"))
	}
	if m := p.markers(t, "tool"); len(m) != 0 {
		t.Errorf("a jail with no start record has no marker to record, got %v", m)
	}

	id := p.newJailStart(t)
	p.truncateLog(t)
	log, out := p.runOut(t, "tool", "tool", "FAKE_LATEST=9.9.9")
	if n := countArgv(log, viewTool); n != 1 {
		t.Errorf("the first invocation of a start asks the registry once, asked %d times:\n%s", n, strings.Join(log, "\n"))
	}
	if !hasArgv(log, installNewer) {
		t.Errorf("a newer version must be INSTALLED, not merely reported on:\n%s", strings.Join(log, "\n"))
	}
	if !strings.Contains(out, "1.0.0 → 9.9.9") {
		t.Errorf("the update must say which versions it moved between:\n%s", out)
	}
	if !strings.Contains(out, "RAN") {
		t.Errorf("the launcher must exec the tool after updating it:\n%s", out)
	}
	if got := p.markers(t, "tool"); len(got) != 1 || got[0] != "tool."+id {
		t.Errorf("the check leaves one marker named for the start id, got %v", got)
	}

	p.truncateLog(t)
	log, out = p.runOut(t, "tool", "tool", "FAKE_LATEST=10.0.0")
	if len(log) != 0 {
		t.Errorf("a second invocation of the same start must not touch npm, got:\n%s", strings.Join(log, "\n"))
	}
	if !strings.Contains(out, "RAN") {
		t.Errorf("...and must still run the installed version:\n%s", out)
	}

	p.newJailStart(t)
	p.truncateLog(t)
	log = p.run(t, "tool", "tool", "FAKE_LATEST=10.0.0")
	if n := countArgv(log, viewTool); n != 1 || !hasArgv(log, installNewer) {
		t.Errorf("a NEW jail start checks again:\n%s", strings.Join(log, "\n"))
	}
}

// A registry that did not answer keeps the marker (LU-D2): the installed version runs, the line
// ends with the next step, and nothing asks again until the next start.
func TestNpmLauncherKeepsTheMarkerWhenTheRegistryDoesNotAnswer(t *testing.T) {
	p := newNpmProbe(t, "tool")
	p.run(t, "tool", "tool", "FAKE_INSTALLED_VERSION=1.0.0")
	p.newJailStart(t)
	p.truncateLog(t)

	log, out := p.runOut(t, "tool", "tool", "FAKE_VIEW_FAIL=1")
	if n := countArgv(log, viewTool); n != 1 || hasArgv(log, "install") {
		t.Errorf("an unreachable registry is asked once and installs nothing:\n%s", strings.Join(log, "\n"))
	}
	for _, want := range []string{
		"could not ask the npm registry for a newer version — running the installed 1.0.0",
		"the next time this jail starts", "yolo pack update", "RAN",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if m := p.markers(t, "tool"); len(m) != 1 {
		t.Errorf("an unanswered check keeps its marker, got %v", m)
	}
	p.truncateLog(t)
	if log := p.run(t, "tool", "tool", "FAKE_LATEST=9.9.9"); len(log) != 0 {
		t.Errorf("no retry until the next start, got:\n%s", strings.Join(log, "\n"))
	}
}

// A newer version whose install failed REFUSES (exit 75, PD-NF1) and RELEASES the marker, so the
// next invocation of the same start retries (LU-D10).
func TestNpmLauncherReleasesTheMarkerWhenTheNewerInstallFails(t *testing.T) {
	p := newNpmProbe(t, "tool")
	p.run(t, "tool", "tool", "FAKE_INSTALLED_VERSION=1.0.0")
	p.newJailStart(t)
	p.truncateLog(t)

	_, out, rc := p.runStatus(t, "tool", "tool", "FAKE_LATEST=9.9.9", "FAKE_INSTALL_FAIL=1")
	if rc != UpdateRefusedStatus {
		t.Fatalf("exit %d, want the refusal's %d:\n%s", rc, UpdateRefusedStatus, out)
	}
	if m := p.markers(t, "tool"); len(m) != 0 {
		t.Errorf("a refusal releases the marker, got %v", m)
	}
	p.truncateLog(t)
	log := p.run(t, "tool", "tool", "FAKE_LATEST=9.9.9")
	if !hasArgv(log, viewTool) || !hasArgv(log, installNewer) {
		t.Errorf("the next invocation retries the update:\n%s", strings.Join(log, "\n"))
	}
}

// A held install lock releases the marker: the launcher ran the installed version without
// checking, so the next invocation of the start may.
func TestNpmLauncherReleasesTheMarkerWhenTheLockIsHeld(t *testing.T) {
	p := newNpmProbe(t, "tool")
	p.run(t, "tool", "tool", "FAKE_INSTALLED_VERSION=1.0.0")
	p.newJailStart(t)
	lock := filepath.Join(p.home, ".npm-global", ".yolo-update.lock")
	if err := os.MkdirAll(lock, 0o755); err != nil {
		t.Fatal(err)
	}
	p.truncateLog(t)

	log, out := p.runOut(t, "tool", "tool", "FAKE_LATEST=9.9.9")
	if len(log) != 0 || !strings.Contains(out, "another update is in progress") {
		t.Errorf("a held lock means no npm traffic and says so:\n%s\n%s", strings.Join(log, "\n"), out)
	}
	if m := p.markers(t, "tool"); len(m) != 0 {
		t.Errorf("a held lock releases the marker, got %v", m)
	}
	if err := os.Remove(lock); err != nil {
		t.Fatal(err)
	}
	if log := p.run(t, "tool", "tool", "FAKE_LATEST=9.9.9"); !hasArgv(log, viewTool) {
		t.Errorf("once the lock is free the same start checks:\n%s", strings.Join(log, "\n"))
	}
}

// A pinned program, a held agent_updates, a version probe and an install-only run CLAIM NOTHING:
// no marker, no registry traffic, whatever the record says.
func TestNpmLauncherClaimsNothingOutsideAnUnpinnedLaunch(t *testing.T) {
	cases := []struct {
		name   string
		pkg    string
		setup  func(p *npmProbe)
		env    []string
		wantRC int
	}{
		{name: "pinned", pkg: "tool@1.0.0"},
		{name: "agent_updates held", pkg: "tool", setup: func(p *npmProbe) { p.updates = false }},
		{name: "version probe", pkg: "tool", setup: func(p *npmProbe) {
			p.probeArgs = []string{"--version"}
			p.args = []string{"--version"}
		}},
		{name: "install only", pkg: "tool", env: []string{InstallOnlyEnv + "=1"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := newNpmProbe(t, "tool")
			p.run(t, "tool", tc.pkg, "FAKE_INSTALLED_VERSION=1.0.0")
			if tc.setup != nil {
				tc.setup(p)
			}
			p.newJailStart(t)
			p.truncateLog(t)
			log, out := p.runOut(t, "tool", tc.pkg, append([]string{"FAKE_LATEST=9.9.9"}, tc.env...)...)
			if hasArgv(log, "view") || hasArgv(log, "install") {
				t.Errorf("no registry traffic, got:\n%s\n%s", strings.Join(log, "\n"), out)
			}
			if m := p.markers(t, "tool"); len(m) != 0 {
				t.Errorf("claimed nothing, yet markers %v", m)
			}
			if strings.Contains(out, "no start record") {
				t.Errorf("a record exists, so the no-record line must not appear:\n%s", out)
			}
		})
	}
}

// No jail-start record (an older boot, a failed write): the launcher checks NOTHING, runs the
// installed version, and says so with its next step (LU-D6). It never mints a record itself.
func TestNpmLauncherWithNoStartRecordChecksNothingAndSaysSo(t *testing.T) {
	p := newNpmProbe(t, "tool")
	p.run(t, "tool", "tool", "FAKE_INSTALLED_VERSION=1.0.0")
	p.truncateLog(t)

	log, out := p.runOut(t, "tool", "tool", "FAKE_LATEST=9.9.9")
	if len(log) != 0 {
		t.Errorf("no record, no check, got:\n%s", strings.Join(log, "\n"))
	}
	for _, want := range []string{
		"this jail has no start record, so it checked nothing for updates — running the installed 1.0.0",
		"Relaunch the jail", "RAN",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if _, err := os.Stat(filepath.Join(p.home, ".local", "state", "yolo", "jail-start")); !os.IsNotExist(err) {
		t.Errorf("a launcher must never write the record (one writer: the boot), stat err=%v", err)
	}
}

// THE FAN-OUT: two launchers of one jail start, the first blocked inside `npm view` on a named
// pipe. The second finds the claim, returns at once having run the program, and made no npm call;
// the registry is asked once in all. Waits on the pipes (events), never on time.
func TestNpmLauncherSecondInvocationDoesNotCheckWhileTheFirstIsInFlight(t *testing.T) {
	p := newNpmProbe(t, "tool")
	p.run(t, "tool", "tool", "FAKE_INSTALLED_VERSION=1.0.0")
	p.newJailStart(t)
	p.truncateLog(t)

	started := filepath.Join(p.home, "view-started")
	release := filepath.Join(p.home, "view-release")
	for _, f := range []string{started, release} {
		if err := syscall.Mkfifo(f, 0o600); err != nil {
			t.Skipf("no named pipes here: %v", err)
		}
	}

	first := p.command(t, "tool", "tool", "FAKE_LATEST=1.0.0", "FAKE_VIEW_STARTED="+started, "FAKE_VIEW_BLOCK="+release)
	done := make(chan error, 1)
	var firstOut strings.Builder
	first.Stdout, first.Stderr = &firstOut, &firstOut
	if err := first.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { done <- first.Wait() }()

	// The event: the first launcher's `npm view` is in flight. The timeout only turns a hang
	// into a red test.
	opened := make(chan struct{})
	go func() {
		f, err := os.Open(started)
		if err == nil {
			buf := make([]byte, 8)
			_, _ = f.Read(buf)
			f.Close()
		}
		close(opened)
	}()
	select {
	case <-opened:
	case err := <-done:
		t.Fatalf("the first launcher ended before its npm view started: %v\n%s", err, firstOut.String())
	case <-time.After(60 * time.Second):
		t.Fatal("the first launcher never reached npm view")
	}

	log, out := p.runOut(t, "tool", "tool", "FAKE_LATEST=1.0.0")
	if n := countArgv(log, viewTool); n != 1 {
		t.Errorf("the registry is asked once in all (the first, still blocked), asked %d times:\n%s", n, strings.Join(log, "\n"))
	}
	if hasArgv(log, "install") {
		t.Errorf("the second launcher must not install:\n%s", strings.Join(log, "\n"))
	}
	if !strings.Contains(out, "RAN") {
		t.Errorf("the second launcher runs the installed version at once:\n%s", out)
	}

	rel, err := os.OpenFile(release, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = rel.WriteString("go\n")
	rel.Close()
	if err := <-done; err != nil {
		t.Fatalf("the first launcher failed: %v\n%s", err, firstOut.String())
	}
	if !strings.Contains(firstOut.String(), "is already current") {
		t.Errorf("the first launcher finishes its check:\n%s", firstOut.String())
	}
}

// A successful COLD install installed the latest, so it records this start's marker: the second
// invocation of the same start makes no registry call, and a new start checks again.
func TestNpmLauncherColdInstallRecordsTheStartsMarker(t *testing.T) {
	p := newNpmProbe(t, "tool")
	id := p.newJailStart(t)
	log := p.run(t, "tool", "tool", "FAKE_INSTALLED_VERSION=1.0.0")
	if !hasArgv(log, installNewer) {
		t.Fatalf("the cold launch installs:\n%s", strings.Join(log, "\n"))
	}
	if m := p.markers(t, "tool"); len(m) != 1 || m[0] != "tool."+id {
		t.Errorf("a cold install records the start's marker, got %v", m)
	}
	p.truncateLog(t)
	if log := p.run(t, "tool", "tool", "FAKE_LATEST=9.9.9"); len(log) != 0 {
		t.Errorf("the second invocation of the start must not touch npm, got:\n%s", strings.Join(log, "\n"))
	}
	p.newJailStart(t)
	p.truncateLog(t)
	if log := p.run(t, "tool", "tool", "FAKE_LATEST=9.9.9"); !hasArgv(log, viewTool) {
		t.Errorf("a new start checks again:\n%s", strings.Join(log, "\n"))
	}
}
