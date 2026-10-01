package check

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/reporoot"
	"github.com/mschulkind-oss/yolo-jail/internal/storage"
)

// macMachine is a Mac a macos-user launch would accept, as the section's seams see it, plus a
// record of every subprocess the section asked for. Each test breaks one fact of it.
type macMachine struct {
	opts  Options
	out   *bytes.Buffer
	execs *[]string
}

func newMacMachine(t *testing.T) macMachine {
	t.Helper()
	var out bytes.Buffer
	var execs []string
	repo := t.TempDir()
	must(t, os.WriteFile(filepath.Join(repo, "flake.lock"), []byte("{}"), 0o644))
	o := Options{
		IsMacOS:     true,
		Stdout:      &out,
		IsTTYStdout: func() bool { return false },
		Workspace:   "/Users/Shared/yolo/proj",
		Getenv:      func(string) string { return "" }, // on the host, not in a jail
		Geteuid:     func() int { return 501 },
		PathIsDir:   func(string) bool { return true },
		LookPath: func(name string) (string, bool) {
			switch name {
			case "sandbox-exec", "nix":
				return "/usr/bin/" + name, true
			}
			return "", false
		},
		Exec: func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
			execs = append(execs, strings.Join(argv, " "))
			return ExecResult{Ran: true, RC: 0} // `id _yolojail` and the ACL probe both pass
		},
		RepoRoot: func() (reporoot.Resolution, bool) {
			return reporoot.Resolution{Root: repo, Source: reporoot.FromEnv}, true
		},
	}
	fillDefaults(&o)
	return macMachine{opts: o, out: &out, execs: &execs}
}

func (m macMachine) run() string {
	r := newReporter(m.opts.Stdout, false)
	m.opts.checkMacosUserBackend(r)
	return m.out.String()
}

// failExec makes the subprocess whose argv contains `match` exit 1.
func (m *macMachine) failExec(match string) {
	prev := m.opts.Exec
	m.opts.Exec = func(argv []string, dir string, env []string, d time.Duration) ExecResult {
		res := prev(argv, dir, env, d)
		if strings.Contains(strings.Join(argv, " "), match) {
			res.RC = 1
		}
		return res
	}
}

// A machine the launch accepts gets a PASS for every one of the launch's preconditions, read
// off the launch's own list — so deleting the loop that renders them, or a precondition the
// section stops asking, fails this. And the section no longer calls itself experimental.
func TestTheMacosUserSectionPassesEveryLaunchPrecondition(t *testing.T) {
	m := newMacMachine(t)
	got := m.run()
	for _, c := range macosuser.LaunchPreconditions() {
		if want := "[PASS] " + c.Ready("/Users/Shared/yolo/proj"); !strings.Contains(got, want) {
			t.Errorf("no %q row:\n%s", want, got)
		}
	}
	for _, want := range []string{"[PASS] nix available", "[PASS] flake.lock present"} {
		if !strings.Contains(got, want) {
			t.Errorf("no %q row:\n%s", want, got)
		}
	}
	for _, gone := range []string{"[FAIL]", "[WARN]", "[SKIP]", "xperimental", "NOT verified"} {
		if strings.Contains(got, gone) {
			t.Errorf("a machine the launch accepts reports %q:\n%s", gone, got)
		}
	}
}

// ONE ROW PER REFUSAL, each a FAIL naming its fix, and each row the launch's own words for the
// same condition. The cases are keyed by precondition, and a precondition the launch gains
// without a case here fails the guard at the end.
func TestTheMacosUserSectionFailsEachConditionTheLaunchRefuses(t *testing.T) {
	const ws = "/Users/Shared/yolo/proj"
	cases := map[string]struct {
		breakIt   func(m *macMachine)
		workspace string
		fix       []string // the remedy the FAIL row's note must name
		skipped   []string // rows that must be SKIPs, not passes, as a result
	}{
		macosuser.PreconditionMacOS: {
			breakIt: func(m *macMachine) { m.opts.IsMacOS = false },
			fix:     []string{"use 'podman' or 'container' on this host"},
			skipped: []string{"the sandbox user", "the workspace's sharing"},
		},
		macosuser.PreconditionNotRoot: {
			breakIt: func(m *macMachine) { m.opts.Geteuid = func() int { return 0 } },
			fix:     []string{"Run yolo as your normal user"},
		},
		macosuser.PreconditionSeatbelt: {
			breakIt: func(m *macMachine) {
				m.opts.LookPath = func(n string) (string, bool) { return "/usr/bin/nix", n == "nix" }
			},
			fix: []string{"/usr/bin/sandbox-exec"},
		},
		macosuser.PreconditionSandboxUser: {
			breakIt: func(m *macMachine) { m.failExec("id " + macosuser.SandboxUser) },
			fix:     []string{"yolo macos-setup"},
			skipped: []string{"the sandbox home", "the workspace's sharing"},
		},
		macosuser.PreconditionSandboxHome: {
			breakIt: func(m *macMachine) {
				m.opts.PathIsDir = func(p string) bool { return p != macosuser.SandboxHome() }
			},
			fix: []string{"yolo macos-setup"},
		},
		macosuser.PreconditionNeutralGround: {
			breakIt:   func(*macMachine) {},
			workspace: "/Users/matt/code/proj",
			fix: []string{"mv /Users/matt/code/proj /Users/Shared/yolo/proj",
				"yolo macos-fix-permissions /Users/Shared/yolo/proj"},
			skipped: []string{"the workspace's sharing"},
		},
		macosuser.PreconditionWorkspaceShared: {
			breakIt: func(m *macMachine) { m.failExec("ls -lde") },
			fix:     []string{"yolo macos-fix-permissions " + ws},
		},
	}
	for _, c := range macosuser.LaunchPreconditions() {
		tc, ok := cases[c.ID]
		if !ok {
			t.Errorf("the launch refuses on %q and no case here shows yolo check reporting it", c.ID)
			continue
		}
		t.Run(c.ID, func(t *testing.T) {
			m := newMacMachine(t)
			w := ws
			if tc.workspace != "" {
				w = tc.workspace
				m.opts.Workspace = w
			}
			tc.breakIt(&m)
			got := m.run()
			if want := "[FAIL] " + c.Unmet(w); !strings.Contains(got, want) {
				t.Errorf("no %q row:\n%s", want, got)
			}
			for _, fix := range tc.fix {
				if !strings.Contains(got, fix) {
					t.Errorf("the FAIL row does not name its fix %q:\n%s", fix, got)
				}
			}
			if strings.Count(got, "[FAIL]") != 1 {
				t.Errorf("one broken condition, %d FAIL rows:\n%s", strings.Count(got, "[FAIL]"), got)
			}
			for _, name := range tc.skipped {
				if !strings.Contains(got, "[SKIP] Not checked: "+name) {
					t.Errorf("%q is not reported as unchecked:\n%s", name, got)
				}
			}
		})
	}
}

// The ACL probe's remedy refuses a path under a home, so for such a workspace the probe must
// not even run: its row is a SKIP pointing at the location row, whose fix is the move.
func TestTheMacosUserSectionDoesNotProbeTheSharingOfAWorkspaceInAHome(t *testing.T) {
	m := newMacMachine(t)
	m.opts.Workspace = "/Users/matt/code/proj"
	got := m.run()
	for _, argv := range *m.execs {
		if strings.Contains(argv, "ls -lde") {
			t.Errorf("probed the sharing of a workspace under a home (%q):\n%s", argv, got)
		}
	}
	if strings.Contains(got, "macos-fix-permissions /Users/matt") {
		t.Errorf("the report sends the user to macos-fix-permissions for a path under a home:\n%s", got)
	}
}

// The build step: every launch builds the sandbox's tools with the host's nix and refuses
// without it. The fix is the getting-started guide's install for this Mac's chip, then the
// re-check; it used to be a link to https://nixos.org/download, which leaves the choice of
// installer to the reader.
func TestTheMacosUserSectionFailsAMachineWithNoNix(t *testing.T) {
	for _, tc := range []struct {
		machine string
		want    string
	}{
		{"arm64", storage.NixInstallerCommand},
		{"x86_64", storage.NixIntelMacCommands[0]},
	} {
		t.Run(tc.machine, func(t *testing.T) {
			m := newMacMachine(t)
			m.opts.Machine = tc.machine
			m.opts.LookPath = func(n string) (string, bool) { return "/usr/bin/" + n, n == "sandbox-exec" }
			got := m.run()
			for _, want := range []string{"[FAIL] nix not found", tc.want, "then, in a new terminal: yolo check"} {
				if !strings.Contains(got, want) {
					t.Errorf("the nix FAIL row lacks %q:\n%s", want, got)
				}
			}
		})
	}
}

// Off a Mac, nothing past the platform row is a finding, and the build rows are not printed.
func TestTheMacosUserSectionOffAMacReportsThePlatformAndStops(t *testing.T) {
	m := newMacMachine(t)
	m.opts.IsMacOS = false
	got := m.run()
	if strings.Contains(got, "nix") || strings.Contains(got, "[PASS]") {
		t.Errorf("reported past the platform off a Mac:\n%s", got)
	}
	if len(*m.execs) != 0 {
		t.Errorf("ran %q off a Mac", *m.execs)
	}
}

// THE CALL SITE: a whole `yolo check` on a Mac whose macos-user workspace sits in the user's
// home fails, on that row, with the move as its fix — where it used to pass with an
// "experimental" warning while every launch from there refused. It fails if Check stops
// calling the section, and if the section stops asking the launch's list.
func TestYoloCheckFailsAMacWhoseWorkspaceTheLaunchRefuses(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	var out bytes.Buffer
	opts := baseOptions(t, &out)
	opts.IsMacOS = true
	opts.Machine = "arm64"
	opts.Workspace = "/Users/matt/code/proj"
	repo := t.TempDir()
	must(t, os.WriteFile(filepath.Join(repo, "flake.nix"), []byte("{}"), 0o644))
	must(t, os.WriteFile(filepath.Join(repo, "flake.lock"), []byte("{}"), 0o644))
	opts.RepoRoot = func() (reporoot.Resolution, bool) {
		return reporoot.Resolution{Root: repo, Source: reporoot.FromEnv}, true
	}
	opts.Getenv = func(k string) string {
		if k == "YOLO_RUNTIME" {
			return "macos-user"
		}
		return "" // on the host
	}
	opts.PathExists = func(p string) bool {
		if p == "/nix" || strings.HasPrefix(p, "/nix/") {
			return true // stubbed: a CI runner has no /nix
		}
		_, err := os.Stat(p)
		return err == nil
	}
	opts.LookPath = func(name string) (string, bool) {
		return "/usr/bin/" + name, name == "nix" || name == "sandbox-exec"
	}
	opts.Geteuid = func() int { return 501 }
	opts.PathIsDir = func(string) bool { return true }
	opts.Exec = fakeExec(map[string]ExecResult{
		"nix --version":               {Stdout: "nix (Nix) 2.30.0", Ran: true, RC: 0},
		"id " + macosuser.SandboxUser: {Ran: true, RC: 0},
		"bash -c":                     {Ran: true, RC: 0},
	})

	rc := Check(opts)
	got := stripANSI(out.String())
	if !strings.Contains(got, "macOS-user backend") {
		t.Fatalf("yolo check on a macos-user Mac has no macOS-user section:\n%s", got)
	}
	for _, want := range []string{
		"[FAIL] Workspace /Users/matt/code/proj is inside the home folder /Users/matt",
		"mv /Users/matt/code/proj /Users/Shared/yolo/proj",
		"[PASS] Sandbox user '" + macosuser.SandboxUser + "' exists",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("no %q in the report:\n%s", want, got)
		}
	}
	if rc != 1 {
		t.Errorf("yolo check exited %d on a machine every launch from this workspace refuses, want 1", rc)
	}
}

// In a jail the section is one SKIP: the backend runs on the host, and the jail cannot see it.
func TestTheMacosUserSectionSkipsInsideAJail(t *testing.T) {
	m := newMacMachine(t)
	m.opts.Getenv = func(k string) string {
		if k == "YOLO_VERSION" {
			return "9.9.9-test"
		}
		return ""
	}
	got := m.run()
	if !strings.Contains(got, "[SKIP] Inside jail") || strings.Contains(got, "[PASS]") || strings.Contains(got, "[FAIL]") {
		t.Errorf("in a jail the section must be a single skip:\n%s", got)
	}
}
