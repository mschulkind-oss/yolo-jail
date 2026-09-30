package check

import (
	"bytes"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/containerbuilder"
)

// The extra-platforms footgun WARNING must name this host's own linux double, not a
// hardcoded `aarch64-linux`. The detector matches any `<arch>-linux`, so on an Intel Mac it
// fires for x86_64-linux and used to tell the user to remove a line that is not in their
// nix.conf — a real problem with an unfollowable remedy. BACKLOG E8's bug class, and it
// survived because nothing tested this remedy string at all.
func TestExtraPlatformsRemedyNamesThisHostsSystem(t *testing.T) {
	var out bytes.Buffer
	o := &Options{
		Stdout:      &out,
		IsTTYStdout: func() bool { return false },
	}
	fillDefaults(o)
	o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
		if slices.Equal(argv, nixCmdArgv("config", "show")) {
			return ExecResult{Ran: true, RC: 0, Stdout: "extra-platforms = x86_64-linux aarch64-linux\n"}
		}
		return ExecResult{Ran: false}
	}
	r := newReporter(&out, false)
	o.nixExtraPlatformsAndBuilder(r)

	want := containerbuilder.BuilderSystem()
	if !strings.Contains(out.String(), "Remove '"+want+"'") {
		t.Errorf("the remedy must name THIS host's linux double (%q), or it asks the user to "+
			"delete a line they do not have:\n%s", want, out.String())
	}
}

// enablesNixCommand reports whether argv turns the `nix-command` experimental
// feature on for itself.
func enablesNixCommand(argv []string) bool {
	i := slices.Index(argv, "--extra-experimental-features")
	return i >= 0 && i+1 < len(argv) && slices.Contains(strings.Fields(argv[i+1]), "nix-command")
}

// officialInstallerNix is an Exec seam behaving like the nix the OFFICIAL
// installer leaves on a Mac: `nix-command` is off, so any `nix <subcommand>`
// that does not turn it on for itself fails with rc 1 and nix's own refusal,
// while `nix --version` (no subcommand) still works. seen records every argv.
func officialInstallerNix(seen *[][]string) func([]string, string, []string, time.Duration) ExecResult {
	return func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
		*seen = append(*seen, argv)
		if len(argv) == 0 || argv[0] != "nix" {
			return ExecResult{Ran: false}
		}
		if slices.Equal(argv, []string{"nix", "--version"}) {
			return ExecResult{Ran: true, RC: 0, Stdout: "nix (Nix) 2.31.2\n"}
		}
		if !enablesNixCommand(argv) {
			return ExecResult{Ran: true, RC: 1,
				Stderr: "error: experimental Nix feature 'nix-command' is disabled; " +
					"add '--extra-experimental-features nix-command' to enable it\n"}
		}
		switch {
		case slices.Contains(argv, "store") && slices.Contains(argv, "info"):
			return ExecResult{Ran: true, RC: 0,
				Stdout: "Store URL: daemon\nVersion: 2.31.2\nTrusted: 1\n"}
		case slices.Contains(argv, "config") && slices.Contains(argv, "show"):
			return ExecResult{Ran: true, RC: 0, Stdout: "min-free = 1073741824\n"}
		}
		return ExecResult{Ran: false}
	}
}

// TestNixSectionsWorkWithNixCommandOff: on a Mac with the official Nix
// installer, where the `nix-command` feature is OFF by default, the Nix section
// reported "Nix daemon: connection failed" — a false [FAIL] that stopped `yolo
// check` on a working machine — because `nix store info` and `nix config show`
// ran without enabling the feature they live behind.
//
// It drives the SECTIONS (sectionNix on macOS, which reaches the daemon check,
// the extra-platforms probe and hasLinuxBuilder; and sectionAutoGC) through a
// fake nix that refuses any subcommand missing the flag, so reverting any one
// call site to a bare `nix store info` / `nix config show` fails it, as a FAIL
// or as a recorded argv without the flag.
func TestNixSectionsWorkWithNixCommandOff(t *testing.T) {
	var out bytes.Buffer
	var seen [][]string
	o := &Options{IsMacOS: true, Stdout: &out, IsTTYStdout: func() bool { return false }}
	fillDefaults(o)
	o.LookPath = func(name string) (string, bool) { return "/usr/local/bin/" + name, name == "nix" }
	o.Exec = officialInstallerNix(&seen)
	r := newReporter(&out, false)
	o.sectionNix(r)
	o.sectionAutoGC(r)

	if r.failed != 0 {
		t.Errorf("a working nix with nix-command off must not FAIL; got %d:\n%s", r.failed, out.String())
	}
	if !strings.Contains(out.String(), "Nix daemon: connected, user is trusted") {
		t.Errorf("the daemon check did not reach its PASS line:\n%s", out.String())
	}
	var storeInfo, configShow int
	for _, argv := range seen {
		if slices.Equal(argv, []string{"nix", "--version"}) {
			continue
		}
		if !enablesNixCommand(argv) {
			t.Errorf("nix subcommand run without enabling nix-command: %v", argv)
		}
		if slices.Contains(argv, "store") {
			storeInfo++
		}
		if slices.Contains(argv, "config") {
			configShow++
		}
	}
	// Non-vacuous: the daemon check ran once, and `nix config show` ran for the
	// extra-platforms probe, hasLinuxBuilder, and the auto-GC section.
	if storeInfo != 1 || configShow != 3 {
		t.Errorf("expected 1 `store info` and 3 `config show` probes, got %d and %d: %v",
			storeInfo, configShow, seen)
	}
}

// TestNixVersionProbeNamesWhatWentWrong: the version probe said "probe failed" for a
// timeout and an exec failure alike, and passed a nix that exited nonzero. Each outcome
// now has its own line, and the probe gets the daemon check's 15 s budget.
func TestNixVersionProbeNamesWhatWentWrong(t *testing.T) {
	cases := []struct {
		name string
		res  ExecResult
		want string
		pass bool
	}{
		{"timeout", ExecResult{Ran: true, Timeout: true}, "did not answer within 15s", false},
		{"not run", ExecResult{Ran: false}, "could not be run: /usr/local/bin/nix", false},
		{"nonzero", ExecResult{Ran: true, RC: 1, Stderr: "boom"}, "`nix --version` exited 1", false},
		{"works", ExecResult{Ran: true, Stdout: "nix (Nix) 2.31.2\n"}, "nix: nix (Nix) 2.31.2", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			o := &Options{Stdout: &out, IsTTYStdout: func() bool { return false }}
			fillDefaults(o)
			o.LookPath = func(name string) (string, bool) { return "/usr/local/bin/" + name, name == "nix" }
			var budget time.Duration
			o.Exec = func(argv []string, _ string, _ []string, d time.Duration) ExecResult {
				if slices.Equal(argv, []string{"nix", "--version"}) {
					budget = d
					return tc.res
				}
				return ExecResult{Ran: false}
			}
			r := newReporter(&out, false)
			o.sectionNix(r)
			if !strings.Contains(out.String(), tc.want) {
				t.Errorf("output lacks %q:\n%s", tc.want, out.String())
			}
			if (r.failed == 0) != tc.pass {
				t.Errorf("failed=%d, want pass=%v:\n%s", r.failed, tc.pass, out.String())
			}
			if budget != 15*time.Second {
				t.Errorf("nix --version ran with a %v budget, want 15s", budget)
			}
		})
	}
}

// linuxNix is an Exec seam for a Linux nix whose `nix --version` works and whose
// `nix store info` answers with store. seen records every argv.
func linuxNix(seen *[][]string, store ExecResult) func([]string, string, []string, time.Duration) ExecResult {
	return func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
		*seen = append(*seen, argv)
		switch {
		case slices.Equal(argv, []string{"nix", "--version"}):
			return ExecResult{Ran: true, RC: 0, Stdout: "nix (Nix) 2.31.2\n"}
		case slices.Equal(argv, nixCmdArgv("store", "info")):
			return store
		}
		return ExecResult{Ran: false}
	}
}

// runLinuxNixSection drives sectionNix as a Linux host with nix on the PATH, and systemctl on it
// too when systemctl is true.
func runLinuxNixSection(t *testing.T, store ExecResult, systemctl bool) (*reporter, [][]string, string) {
	t.Helper()
	var out bytes.Buffer
	var seen [][]string
	o := &Options{IsMacOS: false, Stdout: &out, IsTTYStdout: func() bool { return false }}
	fillDefaults(o)
	o.LookPath = func(name string) (string, bool) {
		return "/usr/bin/" + name, name == "nix" || (systemctl && name == "systemctl")
	}
	o.Exec = linuxNix(&seen, store)
	r := newReporter(&out, false)
	o.sectionNix(r)
	return r, seen, out.String()
}

// findingFor returns the recorded finding whose message is msg.
func findingFor(r *reporter, msg string) (Finding, bool) {
	for _, f := range r.findings {
		if f.Message == msg {
			return f, true
		}
	}
	return Finding{}, false
}

// PS-D5 (docs/design/provisioner-sets.md): the daemon connectivity probe runs on every OS where
// nix is found. It was gated on macOS, so a Linux host whose daemon was down got no line at all
// before the launch that builds its image through that daemon. The trusted-user verdict and the
// extra-platforms and builder block stay macOS-only: a Linux daemon answering "Trusted: 0" is
// connected, and no `nix config show` is asked for.
func TestTheNixDaemonProbeRunsOffMacOS(t *testing.T) {
	r, seen, out := runLinuxNixSection(t, ExecResult{Ran: true, RC: 0,
		Stdout: "Store URL: daemon\nVersion: 2.31.2\nTrusted: 0\n"}, true)
	if f, ok := findingFor(r, "Nix daemon: connected"); !ok || f.Status != "pass" {
		t.Errorf("a Linux host with a working daemon must get the connected PASS line:\n%s", out)
	}
	if strings.Contains(out, "NOT trusted") {
		t.Errorf("the trusted-user verdict is macOS-only (it diagnoses the builder offload):\n%s", out)
	}
	var storeInfo int
	for _, argv := range seen {
		if slices.Equal(argv, nixCmdArgv("store", "info")) {
			storeInfo++
		}
		if slices.Contains(argv, "config") {
			t.Errorf("the extra-platforms and builder block must stay macOS-only, but ran %v", argv)
		}
	}
	if storeInfo != 1 {
		t.Errorf("`nix store info` ran %d times on Linux, want once: %v", storeInfo, seen)
	}
}

// PS-D5's remedy half: off macOS the timeout and the failure each name the restart for the
// host's service manager, systemd's where systemctl is on the PATH and a sentence naming the
// service where it is not (a jail, whose daemon is the host's).
func TestTheLinuxNixDaemonFaultsNameTheRestart(t *testing.T) {
	const systemd = "sudo systemctl restart nix-daemon"
	const hung = "Nix daemon: store operation timed out (daemon may be hung)"
	cases := []struct {
		name      string
		store     ExecResult
		systemctl bool
		msg, want string
	}{
		{"timeout, systemd", ExecResult{Ran: true, Timeout: true}, true, hung, systemd},
		{"timeout, no systemctl", ExecResult{Ran: true, Timeout: true}, false, hung,
			"there is no systemctl here"},
		{"refused, systemd", ExecResult{Ran: true, RC: 1, Stderr: "error: cannot connect to socket at " +
			"'/nix/var/nix/daemon-socket/socket': Connection refused\n"},
			true, "Nix daemon: connection failed", "Connection refused — Restart the Nix daemon: " + systemd},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, _, out := runLinuxNixSection(t, tc.store, tc.systemctl)
			f, ok := findingFor(r, tc.msg)
			if !ok {
				t.Fatalf("no %q finding:\n%s", tc.msg, out)
			}
			if f.Status != "fail" {
				t.Errorf("%q graded %s, want fail", tc.msg, f.Status)
			}
			if !strings.Contains(f.Note, tc.want) {
				t.Errorf("the remedy lacks %q: %q", tc.want, f.Note)
			}
			if strings.Contains(f.Note, "launchctl") {
				t.Errorf("a Linux remedy names launchd: %q", f.Note)
			}
		})
	}
}
