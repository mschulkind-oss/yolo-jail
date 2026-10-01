package check

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// runAutoGCSection wires a minimal Options for sectionAutoGC alone: nix present
// (unless hasNix=false), and an Exec stub returning the given `nix config show`
// result for the config probe.
func runAutoGCSection(t *testing.T, hasNix bool, probe ExecResult) string {
	t.Helper()
	var out bytes.Buffer
	o := &Options{
		LookPath:    func(string) (string, bool) { return "/bin/nix", hasNix },
		Exec:        func([]string, string, []string, time.Duration) ExecResult { return probe },
		Stdout:      &out,
		IsTTYStdout: func() bool { return false },
		nixHostRoot: t.TempDir(),
	}
	fillDefaults(o)
	o.Getenv = func(string) string { return "" } // on the host, wherever the test runs
	o.LookPath = func(string) (string, bool) { return "/bin/nix", hasNix }
	o.Exec = func([]string, string, []string, time.Duration) ExecResult { return probe }
	r := newReporter(&out, false)
	o.sectionAutoGC(r)
	return out.String()
}

// autoGCSection runs the auto-GC section on m.
func (m nixMachine) autoGCSection(t *testing.T) (*reporter, string) {
	t.Helper()
	var out bytes.Buffer
	o := m.options(t, &out)
	r := newReporter(&out, false)
	o.sectionAutoGC(r)
	return r, out.String()
}

const minFreeOffRow = "nix min-free = 0 — the daemon's automatic GC is OFF, so the store grows unbounded"

// determinate-nixd runs its own disk-based garbage collection whatever min-free says, so on
// Determinate Nix a min-free of 0 is not a missing net. The section said the daemon's automatic
// GC was OFF there, and sent the user to edit the nix.conf determinate-nixd replaces.
func TestAutoGCOnDeterminateNixIsNotReportedOff(t *testing.T) {
	for _, mac := range []bool{true, false} {
		r, out := nixMachine{mac: mac, version: determinateNixVersion, config: "min-free = 0\n",
			files: map[string]string{nixConfFile: determinateManagedNixConf, determinatePlist: ""}}.autoGCSection(t)
		if r.warned != 0 || strings.Contains(out, "automatic GC is OFF") {
			t.Errorf("mac=%v: Determinate Nix's GC reported off:\n%s", mac, out)
		}
		if !strings.Contains(out, "determinate-nixd") {
			t.Errorf("mac=%v: the section must say determinate-nixd collects on its own:\n%s", mac, out)
		}
		if strings.Contains(out, "/etc/nix/nix.conf") {
			t.Errorf("mac=%v: a Determinate Nix user must never be sent to nix.conf:\n%s", mac, out)
		}
	}
}

// On upstream Nix the min-free remedy names the one file the lines take effect in on this
// install, and the restart for this OS's daemon, instead of "nix.conf (or nix.custom.conf)" and
// "restart the nix daemon".
func TestAutoGCRemedyNamesThisNixsFileAndRestart(t *testing.T) {
	cases := []struct {
		name          string
		mac           bool
		nixConf       string
		file, restart string
	}{
		{"official installer, Linux", false, officialInstallerNixConf,
			"/etc/nix/nix.conf", "sudo systemctl restart nix-daemon"},
		{"Determinate installer, Mac", true, determinateInstallerNixConf,
			"/etc/nix/nix.custom.conf", "sudo launchctl kickstart -k system/org.nixos.nix-daemon"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, out := nixMachine{mac: tc.mac, version: upstreamNixVersion, config: "min-free = 0\n",
				files: map[string]string{nixConfFile: tc.nixConf, upstreamPlist: ""}}.autoGCSection(t)
			f, ok := findingFor(r, minFreeOffRow)
			if !ok || f.Status != "warn" {
				t.Fatalf("no min-free WARN:\n%s", out)
			}
			if !strings.Contains(f.Note, tc.file) || !strings.Contains(f.Note, tc.restart) {
				t.Errorf("the remedy must name %s and `%s`:\n%s", tc.file, tc.restart, f.Note)
			}
			other := "/etc/nix/nix.custom.conf"
			if tc.file == other {
				other = "/etc/nix/nix.conf"
			}
			if strings.Contains(f.Note, other) {
				t.Errorf("the remedy names %s too; one file is the fix:\n%s", other, f.Note)
			}
		})
	}
}

// Inside a jail `nix config show` reads the JAIL's own nix config, not the host daemon's.
// MEASURED 2026-10-01 in a jail on a Determinate Nix host: NIX_CONFIG="min-free = 123" in the
// jail's environment changed the min-free it reported, which a reading of the daemon could not
// see, and `nix --version` there names the jail's own nix too. So a min-free of 0 there says
// nothing about the host's GC: it is a host fact, never a WARN that the host's GC is off.
func TestAutoGCInsideAJailIsAHostFact(t *testing.T) {
	r, out := nixMachine{inJail: true, version: upstreamNixVersion, config: "min-free = 0\n"}.autoGCSection(t)
	if r.warned != 0 || strings.Contains(out, "automatic GC is OFF") {
		t.Errorf("a jail's own min-free reported as the host's GC:\n%s", out)
	}
	if r.skipped != 1 || !strings.Contains(out, "host fact") || !strings.Contains(out, "yolo check") {
		t.Errorf("the section must be one host-fact SKIP naming where to check:\n%s", out)
	}
}

func TestAutoGCSectionMinFreeZeroWarns(t *testing.T) {
	got := runAutoGCSection(t, true, ExecResult{Ran: true, RC: 0,
		Stdout: "max-free = 9223372036854775807\nmin-free = 0\n"})
	if !strings.Contains(got, "Nix auto-GC") {
		t.Fatalf("expected section header, got:\n%s", got)
	}
	if !strings.Contains(got, "min-free = 0") || !strings.Contains(got, "automatic GC is OFF") {
		t.Errorf("expected the min-free=0 WARN, got:\n%s", got)
	}
	if !strings.Contains(got, "storage §1") {
		t.Errorf("expected the remedy to note §1 rooting makes this safe, got:\n%s", got)
	}
}

func TestAutoGCSectionMinFreeSetPasses(t *testing.T) {
	got := runAutoGCSection(t, true, ExecResult{Ran: true, RC: 0,
		Stdout: "min-free = 53687091200\nmax-free = 214748364800\n"})
	if !strings.Contains(got, "min-free is set") || !strings.Contains(got, "50.0 GiB") {
		t.Errorf("expected the configured-floor PASS with a human size, got:\n%s", got)
	}
}

func TestAutoGCSectionSkippedNoNix(t *testing.T) {
	got := runAutoGCSection(t, false, ExecResult{Ran: false})
	if strings.Contains(got, "Nix auto-GC") {
		t.Errorf("section must be skipped when nix is absent, got:\n%s", got)
	}
}

func TestAutoGCSectionSkippedOnUnreadableConfig(t *testing.T) {
	got := runAutoGCSection(t, true, ExecResult{Ran: true, RC: 1})
	if strings.Contains(got, "Nix auto-GC") {
		t.Errorf("section must stay silent when the config can't be read, got:\n%s", got)
	}
}

func TestAutoGCSectionSkippedWhenKeyAbsent(t *testing.T) {
	got := runAutoGCSection(t, true, ExecResult{Ran: true, RC: 0, Stdout: "max-jobs = auto\n"})
	if strings.Contains(got, "Nix auto-GC") {
		t.Errorf("section must stay silent when min-free is absent, got:\n%s", got)
	}
}
