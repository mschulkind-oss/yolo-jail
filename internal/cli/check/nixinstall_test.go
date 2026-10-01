package check

// nixinstall_test.go pins the next step `yolo check` gives for a machine with no Nix, or one whose
// nix will not start. Both used to stop short: "nix not found" pointed at https://nixos.org/download/,
// a page that leaves the choice of installer to the reader, and "nix found but could not be run"
// had no note at all (docs/reference/happy-path-principle.md, rules 1 and 3). The install each
// now prints is the getting-started guide's for this machine.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/storage"
)

// TestNixInstallHintsMatchTheGuide: every command the hints print is a line of
// userguide/getting-started.md, the guide that recommends it, so the two cannot drift apart.
func TestNixInstallHintsMatchTheGuide(t *testing.T) {
	guide, err := os.ReadFile(filepath.Join("..", "..", "..", "userguide", "getting-started.md"))
	if err != nil {
		t.Fatal(err)
	}
	lines := map[string]bool{}
	for _, l := range strings.Split(string(guide), "\n") {
		lines[strings.TrimSpace(l)] = true
	}
	for _, intel := range []bool{false, true} {
		_, cmds := storage.NixInstall(intel)
		for _, c := range cmds {
			if !lines[c] {
				t.Errorf("no line of userguide/getting-started.md is %q (intel Mac: %v)", c, intel)
			}
		}
	}
	for _, want := range []string{storage.NixInstallerUninstall, storage.NixUninstallManual} {
		if !strings.Contains(string(guide), want) {
			t.Errorf("userguide/getting-started.md does not name %q", want)
		}
	}
}

// nixSection runs the Nix section with nix at nixPath ("" for none) and `nix --version` failing
// to start, and returns its output.
func nixSection(t *testing.T, mod func(*Options), nixPath string) string {
	t.Helper()
	var out bytes.Buffer
	o := &Options{Stdout: &out, IsTTYStdout: func() bool { return false },
		Getenv: func(string) string { return "" }}
	o.LookPath = func(name string) (string, bool) { return nixPath, name == "nix" && nixPath != "" }
	o.Exec = func([]string, string, []string, time.Duration) ExecResult { return ExecResult{Ran: false} }
	o.PathExists = func(string) bool { return false }
	mod(o)
	fillDefaults(o)
	o.sectionNix(newReporter(&out, false))
	return stripANSI(out.String())
}

// TestNixNotFoundNamesThisMachinesInstall: the note is the guide's install for this machine, then
// the re-check in a new terminal, where nix is on the PATH. Inside a container jail the nix is the
// image's, so the step is a relaunch.
func TestNixNotFoundNamesThisMachinesInstall(t *testing.T) {
	const recheck = "then, in a new terminal: yolo check"
	for _, tc := range []struct {
		name string
		mod  func(*Options)
		want []string
	}{
		{"linux", func(o *Options) { o.Machine = "x86_64" }, []string{storage.NixInstallerCommand, recheck}},
		{"apple silicon", func(o *Options) { o.IsMacOS, o.Machine = true, "arm64" },
			[]string{storage.NixInstallerCommand, recheck}},
		{"intel mac", func(o *Options) { o.IsMacOS, o.Machine = true, "x86_64" },
			append(append([]string{}, storage.NixIntelMacCommands...), recheck)},
		{"container jail", func(o *Options) {
			o.Getenv = func(k string) string { return map[string]string{"YOLO_VERSION": "9.9.9-test"}[k] }
		}, []string{"comes from the jail's image", "relaunch the jail, then: yolo check", "is a yolo bug"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := nixSection(t, tc.mod, "")
			if !strings.Contains(got, "[FAIL] nix not found") {
				t.Fatalf("no [FAIL] for a machine with no nix:\n%s", got)
			}
			for _, want := range tc.want {
				if !strings.Contains(got, want) {
					t.Errorf("the note lacks %q:\n%s", want, got)
				}
			}
			if strings.Contains(got, "nixos.org/download") {
				t.Errorf("the note still sends the reader to choose an installer:\n%s", got)
			}
		})
	}
}

// TestANixThatWillNotStartNamesItsErrorAndTheReinstall: the [FAIL] had no note. It now names the
// command that shows nix's own error, quoted for a shell, and the reinstall: the installer's own
// uninstall where its receipt is, else where the steps for the nixos.org script's install are.
func TestANixThatWillNotStartNamesItsErrorAndTheReinstall(t *testing.T) {
	const nixPath = "/opt/my nix/bin/nix"
	for _, tc := range []struct {
		name    string
		receipt bool
		want    string
	}{
		{"installer receipt", true, storage.NixInstallerUninstall},
		{"no receipt", false, storage.NixUninstallManual},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := nixSection(t, func(o *Options) {
				o.Machine = "x86_64"
				o.PathExists = func(p string) bool { return tc.receipt && p == storage.NixInstallerReceipt }
			}, nixPath)
			for _, want := range []string{
				"[FAIL] nix found but could not be run: " + nixPath,
				"'/opt/my nix/bin/nix' --version",
				tc.want,
				storage.NixInstallerCommand,
				"then, in a new terminal: yolo check",
			} {
				if !strings.Contains(got, want) {
					t.Errorf("the finding lacks %q:\n%s", want, got)
				}
			}
		})
	}
}

// TestANixThatExitsNonzeroNamesItsErrorAndTheReinstall: a nix that started and exited non-zero on
// `nix --version` got nix's own stderr as its note and nothing else, or an empty note when nix
// printed nothing. It keeps that stderr and now adds the reinstall a nix that will not start gets
// (nixBrokenNote). The command that shows nix's own error comes only when nix printed none: with
// the error already on the line above, "run it yourself to see nix's own error" sent the reader
// to fetch what yolo had just shown them (rule 7). In a container jail the step is the relaunch,
// since the jail's nix is the image's.
func TestANixThatExitsNonzeroNamesItsErrorAndTheReinstall(t *testing.T) {
	const nixPath = "/opt/my nix/bin/nix"
	const see = "'/opt/my nix/bin/nix' --version"
	exits := func(stderr string) func(*Options) {
		return func(o *Options) {
			o.Exec = func([]string, string, []string, time.Duration) ExecResult {
				return ExecResult{Ran: true, RC: 1, Stderr: stderr}
			}
		}
	}
	for _, tc := range []struct {
		name    string
		mod     func(*Options)
		stderr  string
		want    []string
		without []string
	}{
		{"with an error", func(o *Options) { o.Machine = "x86_64" }, "error: libstore is broken\n",
			[]string{"error: libstore is broken", storage.NixUninstallManual, storage.NixInstallerCommand,
				"then, in a new terminal: yolo check"},
			[]string{see, "Run it yourself"}},
		{"silent", func(o *Options) { o.Machine = "x86_64" }, "",
			[]string{"Run it yourself to see nix's own error", see, storage.NixInstallerCommand}, nil},
		{"container jail", func(o *Options) {
			o.Getenv = func(k string) string { return map[string]string{"YOLO_VERSION": "9.9.9-test"}[k] }
		}, "error: boom\n", []string{"error: boom", "relaunch the jail, then: yolo check"},
			[]string{see, "Run it yourself"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := nixSection(t, func(o *Options) { tc.mod(o); exits(tc.stderr)(o) }, nixPath)
			if !strings.Contains(got, "[FAIL] nix found but `nix --version` exited 1") {
				t.Fatalf("no [FAIL] for a nix that exited 1:\n%s", got)
			}
			for _, want := range tc.want {
				if !strings.Contains(got, want) {
					t.Errorf("the finding lacks %q:\n%s", want, got)
				}
			}
			for _, not := range tc.without {
				if strings.Contains(got, not) {
					t.Errorf("the finding still sends the reader to see an error it printed (%q):\n%s", not, got)
				}
			}
		})
	}
}

// TestANixInAMacosUserJailNamesTheHost: a macos-user jail runs the host's own nix, which its
// launch puts on the sandbox's PATH when it can and otherwise names why ("nix is not available
// inside the sandbox: …"). The sandbox account can neither install Nix for the host nor repair
// it, so the step is the host's. The notes used to give this account the host's install, or the
// uninstall of the host's nix, as if the jail were the host.
func TestANixInAMacosUserJailNamesTheHost(t *testing.T) {
	jail := func(o *Options) {
		o.IsMacOS, o.Machine = true, "arm64"
		o.Getenv = func(k string) string { return map[string]string{"YOLO_VERSION": "9.9.9-test"}[k] }
		o.PathExists = func(p string) bool { return p == storage.NixInstallerReceipt }
	}
	for _, tc := range []struct {
		name, nixPath string
		want          []string
	}{
		{"not found", "", []string{"[FAIL] nix not found", "nix is not available inside the sandbox",
			"run `yolo check` on the host", "then relaunch the jail"}},
		{"will not start", "/nix/store/x-nix/bin/nix", []string{"[FAIL] nix found but could not be run",
			"/nix/store/x-nix/bin/nix --version", "run `yolo check` on the host", "then relaunch the jail"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := nixSection(t, jail, tc.nixPath)
			for _, want := range tc.want {
				if !strings.Contains(got, want) {
					t.Errorf("the finding lacks %q:\n%s", want, got)
				}
			}
			for _, host := range []string{storage.NixInstallerCommand, storage.NixInstallerUninstall} {
				if strings.Contains(got, host) {
					t.Errorf("the jail's account is told to run the host's %q:\n%s", host, got)
				}
			}
		})
	}
	// The note quotes the launch's own line; it must still be the line a launch prints.
	src, err := os.ReadFile(filepath.Join("..", "..", "macosuser", "orchestrator.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), "nix is not available inside the sandbox:") {
		t.Error("the macos-user launch no longer prints \"nix is not available inside the sandbox:\", " +
			"which the note quotes")
	}
}
