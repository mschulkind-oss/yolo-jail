package builder

import (
	"bytes"
	"strings"
	"testing"
)

// baseDeps returns Deps for a clean macOS host: set up + reachable + key
// installed. Callers override per test.
func baseDeps(rec *[]string) Deps {
	confHas := "builders = ssh-ng://builder@linux-builder aarch64-linux /etc/nix/builder_ed25519 4 - - - -\n"
	return Deps{
		IsMacOS:   func() bool { return true },
		Reachable: func() bool { return true },
		FileIsFile: func(p string) bool {
			// ssh config + key + nix conf all present.
			return p == "/etc/ssh/ssh_config.d/100-linux-builder.conf" ||
				p == "/etc/nix/builder_ed25519" ||
				p == "/etc/nix/nix.conf"
		},
		ReadFileText:          func(string) (string, bool) { return confHas, true },
		NixCustomConfIncluded: func() (bool, bool) { return false, true },
		CurrentTrustedUsers:   func() []string { return []string{"root"} },
		DetectNixDaemonLabel:  func() (string, bool) { return "", false },
		HostUser:              func() string { return "matt" },
		RunSetupScript: func(s string) (int, bool) {
			if rec != nil {
				*rec = append(*rec, "setup-script")
			}
			return 0, true
		},
		StartVMForeground: func() error {
			if rec != nil {
				*rec = append(*rec, "vm-foreground")
			}
			return nil
		},
		StartVMDetached: func() (Proc, error) {
			if rec != nil {
				*rec = append(*rec, "vm-detached")
			}
			return nil, nil
		},
		ReadBuilderPID: func() (int, bool) { return 0, false },
		PIDIsLive:      func(int) bool { return false },
		StopVM: func() (bool, string) {
			if rec != nil {
				*rec = append(*rec, "stop-vm")
			}
			return true, ""
		},
		Sleep:   func(float64) {},
		Now:     func() float64 { return 0 },
		Confirm: func(string) bool { return true },
	}
}

func TestRequireMacosShortCircuits(t *testing.T) {
	for _, sub := range []string{"status", "start", "stop", "setup"} {
		var rec []string
		d := baseDeps(&rec)
		d.IsMacOS = func() bool { return false }
		var buf bytes.Buffer
		d.Out = &buf
		if rc := RunBuilder(d, sub, nil); rc != 0 {
			t.Errorf("%s off-macOS rc = %d, want 0", sub, rc)
		}
		if len(rec) != 0 {
			t.Errorf("%s off-macOS must not act: %v", sub, rec)
		}
		if !strings.Contains(buf.String(), "macOS-only concept") {
			t.Errorf("%s off-macOS notice missing", sub)
		}
	}
}

func TestStatusSetUpAndRunning(t *testing.T) {
	d := baseDeps(nil)
	var buf bytes.Buffer
	d.Out = &buf
	rc := BuilderStatusCmd(d)
	if rc != 0 {
		t.Errorf("rc = %d", rc)
	}
	s := buf.String()
	for _, want := range []string{"set up:       yes", "nix.conf:   yes  (/etc/nix/nix.conf)",
		"reachable:    yes  (port 31022)", "Builder set up and running."} {
		if !strings.Contains(s, want) {
			t.Errorf("status missing %q\n%s", want, s)
		}
	}
}

func TestStatusNotSetUp(t *testing.T) {
	d := baseDeps(nil)
	d.FileIsFile = func(string) bool { return false }
	d.ReadFileText = func(string) (string, bool) { return "", false }
	var buf bytes.Buffer
	d.Out = &buf
	rc := BuilderStatusCmd(d)
	if rc != 1 {
		t.Errorf("not-set-up rc = %d, want 1", rc)
	}
	if !strings.Contains(buf.String(), "Not set up.") {
		t.Error("missing not-set-up notice")
	}
}

func TestStatusCustomConfPath(t *testing.T) {
	d := baseDeps(nil)
	d.NixCustomConfIncluded = func() (bool, bool) { return true, true }
	var buf bytes.Buffer
	d.Out = &buf
	BuilderStatusCmd(d)
	if !strings.Contains(buf.String(), "(/etc/nix/nix.custom.conf)") {
		t.Errorf("custom conf path not used:\n%s", buf.String())
	}
}

func TestStartAlreadyRunning(t *testing.T) {
	var rec []string
	d := baseDeps(&rec)
	var buf bytes.Buffer
	d.Out = &buf
	if rc := BuilderStartCmd(d); rc != 0 {
		t.Errorf("rc = %d", rc)
	}
	if len(rec) != 0 {
		t.Errorf("already-running must not spawn: %v", rec)
	}
	if !strings.Contains(buf.String(), "already running") {
		t.Error("missing already-running notice")
	}
}

func TestStartNotSetUp(t *testing.T) {
	d := baseDeps(nil)
	d.Reachable = func() bool { return false }
	d.FileIsFile = func(string) bool { return false }
	d.ReadFileText = func(string) (string, bool) { return "", false }
	var buf bytes.Buffer
	d.Out = &buf
	if rc := BuilderStartCmd(d); rc != 1 {
		t.Errorf("rc = %d, want 1", rc)
	}
	if !strings.Contains(buf.String(), "Builder not set up.") {
		t.Error("missing not-set-up")
	}
}

func TestStartFirstBoot(t *testing.T) {
	var rec []string
	d := baseDeps(&rec)
	d.Reachable = func() bool { return false }
	// set up (ssh config present, nix conf has builder) but key NOT present.
	d.FileIsFile = func(p string) bool {
		return p == "/etc/ssh/ssh_config.d/100-linux-builder.conf" || p == "/etc/nix/nix.conf"
	}
	// After first boot, make it reachable so we exit 0 on the fast path.
	firstBooted := false
	d.StartVMForeground = func() error { firstBooted = true; rec = append(rec, "vm-foreground"); return nil }
	reachAfter := func() bool { return firstBooted }
	d.Reachable = reachAfter
	var buf bytes.Buffer
	d.Out = &buf
	if rc := BuilderStartCmd(d); rc != 0 {
		t.Errorf("rc = %d, want 0", rc)
	}
	if !contains(rec, "vm-foreground") {
		t.Errorf("first boot not run: %v", rec)
	}
	if !strings.Contains(buf.String(), "First boot:") {
		t.Error("missing first-boot banner")
	}
}

func TestStopRunning(t *testing.T) {
	var rec []string
	d := baseDeps(&rec)
	var buf bytes.Buffer
	d.Out = &buf
	if rc := BuilderStopCmd(d); rc != 0 {
		t.Errorf("rc = %d", rc)
	}
	if !contains(rec, "stop-vm") {
		t.Error("stop not called")
	}
	if !strings.Contains(buf.String(), "Builder stopped.") {
		t.Error("missing stopped notice")
	}
}

func TestStopNotRunning(t *testing.T) {
	var rec []string
	d := baseDeps(&rec)
	d.Reachable = func() bool { return false }
	var buf bytes.Buffer
	d.Out = &buf
	if rc := BuilderStopCmd(d); rc != 0 {
		t.Errorf("rc = %d", rc)
	}
	if len(rec) != 0 {
		t.Errorf("not-running must not stop: %v", rec)
	}
}

func TestSetupAlreadyDone(t *testing.T) {
	var rec []string
	d := baseDeps(&rec)
	var buf bytes.Buffer
	d.Out = &buf
	if rc := BuilderSetupCmd(d, SetupFlags{MaxJobs: 4}); rc != 0 {
		t.Errorf("rc = %d", rc)
	}
	if len(rec) != 0 {
		t.Errorf("already-set-up must not run script: %v", rec)
	}
	if !strings.Contains(buf.String(), "Builder already set up.") {
		t.Error("missing already-set-up")
	}
}

func TestSetupShowExitsBeforeRun(t *testing.T) {
	var rec []string
	d := baseDeps(&rec)
	// Not set up yet.
	d.FileIsFile = func(string) bool { return false }
	d.ReadFileText = func(string) (string, bool) { return "", false }
	var buf bytes.Buffer
	d.Out = &buf
	if rc := BuilderSetupCmd(d, SetupFlags{MaxJobs: 4, Show: true}); rc != 0 {
		t.Errorf("rc = %d", rc)
	}
	if contains(rec, "setup-script") {
		t.Error("--show must not run the script")
	}
	s := buf.String()
	if !strings.Contains(s, "The exact root script:") || !strings.Contains(s, "builders = ssh-ng://builder@linux-builder") {
		t.Errorf("script not previewed:\n%s", s)
	}
}

func TestSetupRunsScriptWithYes(t *testing.T) {
	var rec []string
	d := baseDeps(&rec)
	d.FileIsFile = func(string) bool { return false }
	d.ReadFileText = func(string) (string, bool) { return "", false }
	var buf bytes.Buffer
	d.Out = &buf
	if rc := BuilderSetupCmd(d, SetupFlags{MaxJobs: 8, Yes: true}); rc != 0 {
		t.Errorf("rc = %d", rc)
	}
	if !contains(rec, "setup-script") {
		t.Error("setup script not run")
	}
	if !strings.Contains(buf.String(), "Builder wired up.") {
		t.Error("missing wired-up notice")
	}
}

func TestSetupAbortsOnDecline(t *testing.T) {
	var rec []string
	d := baseDeps(&rec)
	d.FileIsFile = func(string) bool { return false }
	d.ReadFileText = func(string) (string, bool) { return "", false }
	d.Confirm = func(string) bool { return false }
	var buf bytes.Buffer
	d.Out = &buf
	if rc := BuilderSetupCmd(d, SetupFlags{MaxJobs: 4}); rc != 1 {
		t.Errorf("declined rc = %d, want 1", rc)
	}
	if contains(rec, "setup-script") {
		t.Error("decline must not run script")
	}
	if !strings.Contains(buf.String(), "Aborted.") {
		t.Error("missing abort notice")
	}
}

func TestParseSetupFlags(t *testing.T) {
	f := parseSetupFlags([]string{"--max-jobs", "8", "--show", "-y"})
	if f.MaxJobs != 8 || !f.Show || !f.Yes {
		t.Errorf("= %+v", f)
	}
	f = parseSetupFlags([]string{"--max-jobs=2"})
	if f.MaxJobs != 2 {
		t.Errorf("= %+v", f)
	}
	f = parseSetupFlags(nil)
	if f.MaxJobs != 4 {
		t.Errorf("default = %+v", f)
	}
}

func TestRunBuilderUnknownSub(t *testing.T) {
	d := baseDeps(nil)
	var buf bytes.Buffer
	d.Out = &buf
	if rc := RunBuilder(d, "bogus", nil); rc != 2 {
		t.Errorf("unknown rc = %d, want 2", rc)
	}
}

func TestEnsureBuilderReasons(t *testing.T) {
	// not macOS
	d := baseDeps(nil)
	d.IsMacOS = func() bool { return false }
	if ok, r := EnsureBuilder(d, nil); ok || r != "not macOS" {
		t.Errorf("= %v %q", ok, r)
	}
	// reachable
	d = baseDeps(nil)
	if ok, r := EnsureBuilder(d, nil); !ok || r != "" {
		t.Errorf("reachable = %v %q", ok, r)
	}
	// not set up
	d = baseDeps(nil)
	d.Reachable = func() bool { return false }
	d.FileIsFile = func(string) bool { return false }
	d.ReadFileText = func(string) (string, bool) { return "", false }
	if ok, r := EnsureBuilder(d, nil); ok || r != "not set up" {
		t.Errorf("not-set-up = %v %q", ok, r)
	}
	// needs first-boot (set up but no key)
	d = baseDeps(nil)
	d.Reachable = func() bool { return false }
	d.FileIsFile = func(p string) bool {
		return p == "/etc/ssh/ssh_config.d/100-linux-builder.conf" || p == "/etc/nix/nix.conf"
	}
	if ok, r := EnsureBuilder(d, nil); ok || r != "needs first-boot" {
		t.Errorf("first-boot = %v %q", ok, r)
	}
}

func contains(sl []string, x string) bool {
	for _, v := range sl {
		if v == x {
			return true
		}
	}
	return false
}
