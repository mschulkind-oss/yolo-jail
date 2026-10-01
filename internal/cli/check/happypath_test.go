package check

// happypath_test.go pins the next step `yolo check` prints at each of its dead ends — a stop
// that reported a problem and left the user to work out the fix
// (docs/reference/happy-path-principle.md, which coins both terms). Each test reads the
// message the way the user sees it, through the section or the whole report, and where the
// note names a command, follows it.

import (
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/outfmt"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/reporoot"
)

// fakeJails is a podman that holds running yolo containers and answers the four questions
// the Running Jails section asks: ps, inspect (the workspace), top (stuck?) and rm -f.
type fakeJails struct {
	running map[string]string // name -> the workspace its YOLO_HOST_DIR names
	removed [][]string        // every `rm -f` argv, in order
}

func (f *fakeJails) exec(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
	if len(argv) < 2 {
		return ExecResult{Ran: false}
	}
	switch argv[1] {
	case "ps":
		names := make([]string, 0, len(f.running))
		for n := range f.running {
			names = append(names, n)
		}
		sort.Strings(names)
		var b strings.Builder
		for _, n := range names {
			b.WriteString(n + "\t2 hours ago\n")
		}
		return ExecResult{Ran: true, Stdout: b.String()}
	case "inspect":
		return ExecResult{Ran: true, Stdout: "YOLO_HOST_DIR=" + f.running[argv[2]] + "\n"}
	case "top":
		return ExecResult{Ran: true, RC: 1}
	case "rm":
		if len(argv) > 2 && argv[2] == "-f" {
			f.removed = append(f.removed, append([]string(nil), argv...))
			for _, n := range argv[3:] {
				delete(f.running, n)
			}
			return ExecResult{Ran: true}
		}
	}
	return ExecResult{Ran: false}
}

// twoOrphans is a fakeJails with two running jails whose workspaces are gone.
func twoOrphans(t *testing.T) *fakeJails {
	gone := filepath.Join(t.TempDir(), "gone")
	return &fakeJails{running: map[string]string{
		"yolo-api-3f2a91c0": gone + "-api",
		"yolo-web-9c1d47e2": gone + "-web",
	}}
}

// fixLine returns the command on the report's "fix:" line, split into words, or nil.
func fixLine(report string) []string {
	for _, l := range strings.Split(report, "\n") {
		if _, rest, ok := strings.Cut(l, "fix:"); ok {
			return strings.Fields(rest)
		}
	}
	return nil
}

// A pipe cannot answer the terminal's [y/N], so it gets the command instead — and it used to
// get `yolo prune --apply`, which removes only STOPPED containers, while the orphans this
// section lists are running. Following the printed command must remove exactly what the
// terminal's `y` removes, and the re-check it names must then find nothing left.
func TestPipedOrphanHintRemovesWhatTheTerminalsYesRemoves(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	// What `y` at a terminal removes.
	yes := twoOrphans(t)
	tty := &Options{IsTTYStdout: func() bool { return true }, Stdin: strings.NewReader("y\n"), Exec: yes.exec}
	tty.sectionRunningJails(newReporter(&bytes.Buffer{}, false), "podman")
	var yesRemoved []string
	for _, argv := range yes.removed {
		yesRemoved = append(yesRemoved, argv[3:]...)
	}
	sort.Strings(yesRemoved)
	if len(yesRemoved) != 2 {
		t.Fatalf("the terminal's yes removed %v, want both orphans", yesRemoved)
	}

	// What a pipe is told.
	jails := twoOrphans(t)
	var out bytes.Buffer
	pipe := &Options{IsTTYStdout: func() bool { return false }, Stdin: strings.NewReader("y\n"), Exec: jails.exec}
	pipe.sectionRunningJails(newReporter(&out, false), "podman")
	got := out.String()
	if len(jails.removed) != 0 {
		t.Fatalf("a pipe removed jails nobody was asked about: %v", jails.removed)
	}
	if strings.Contains(got, "yolo prune") {
		t.Errorf("the pipe is still sent to `yolo prune`, which removes only stopped containers:\n%s", got)
	}
	if !strings.Contains(got, "then: yolo check") {
		t.Errorf("the hint names no re-check:\n%s", got)
	}
	fix := fixLine(got)
	if len(fix) < 4 || strings.Join(fix[:3], " ") != "podman rm -f" {
		t.Fatalf("the pipe's fix line is %q, want `podman rm -f <names>`:\n%s", fix, got)
	}
	named := append([]string(nil), fix[3:]...)
	sort.Strings(named)
	if strings.Join(named, " ") != strings.Join(yesRemoved, " ") {
		t.Errorf("the hint names %v, the terminal's yes removes %v", named, yesRemoved)
	}

	// Follow it, then the re-check it names.
	jails.exec(fix, "", nil, 0)
	var again bytes.Buffer
	pipe.sectionRunningJails(newReporter(&again, false), "podman")
	if len(jails.running) != 0 || !strings.Contains(again.String(), "No jails currently running") {
		t.Errorf("after the printed command the re-check still finds jails (%v):\n%s", jails.running, again.String())
	}
}

// The finding's NOTE carries the command, because `--format json` discards the report's prose
// and keeps only the findings: a script or an agent reading the document must get the same
// next step a terminal reader does.
func TestOrphanFindingNoteCarriesTheFix(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	jails := twoOrphans(t)
	r := newReporter(&bytes.Buffer{}, false)
	(&Options{IsTTYStdout: func() bool { return false }, Exec: jails.exec}).sectionRunningJails(r, "podman")
	var note string
	for _, f := range r.findings {
		if f.Status == "warn" && strings.Contains(f.Message, "orphaned jail") {
			note = f.Note
		}
	}
	if !strings.Contains(note, "podman rm -f yolo-api-3f2a91c0 yolo-web-9c1d47e2") ||
		!strings.Contains(note, "then: yolo check") {
		t.Errorf("the orphan finding's note is %q, want the removal and the re-check", note)
	}
}

// `--format json` at a terminal discards the report, the question included, so asking would
// block on an answer to a prompt nobody can see. It must take the pipe's path instead.
func TestJSONCheckAtATerminalNeverAsksAboutOrphans(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	jails := twoOrphans(t)
	o := &Options{IsTTYStdout: func() bool { return true }, Stdin: strings.NewReader("y\n"),
		Exec: jails.exec, Format: outfmt.JSON}
	o.sectionRunningJails(newReporter(&bytes.Buffer{}, false), "podman")
	if len(jails.removed) != 0 {
		t.Errorf("a JSON run read an answer to a prompt it never showed and removed %v", jails.removed)
	}
}

// The launch refuses the same fault with its fix (run.go: "Cannot find yolo-jail repo root"),
// and `yolo check` printed the finding alone.
func TestRepoRootFailureNamesTheLaunchsFix(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	var out bytes.Buffer
	opts := baseOptions(t, &out)
	Check(opts)
	got := stripANSI(out.String())
	_, after, ok := strings.Cut(got, "[FAIL] Could not resolve the yolo-jail repo root\n")
	if !ok {
		t.Fatalf("no repo-root finding:\n%s", got)
	}
	for _, want := range []string{"just install", "YOLO_REPO_ROOT=~/code/yolo-jail yolo check"} {
		if !strings.Contains(after, want) {
			t.Errorf("the repo-root finding does not name %q:\n%s", want, got)
		}
	}
}

// A root YOLO_REPO_ROOT names but that holds no flake.nix (reporoot accepts a go.mod) is told
// to point the variable at a checkout.
func TestFlakeMissingAtTheRootNamesTheFix(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	var out bytes.Buffer
	opts := baseOptions(t, &out)
	opts.RepoRoot = func() (reporoot.Resolution, bool) {
		return reporoot.Resolution{Root: "/src/not-yolo", Source: reporoot.FromEnv}, true
	}
	Check(opts)
	got := stripANSI(out.String())
	_, after, ok := strings.Cut(got, "[WARN] flake.nix not found at /src/not-yolo/flake.nix")
	if !ok {
		t.Fatalf("no flake.nix finding:\n%s", got)
	}
	if !strings.Contains(after, "YOLO_REPO_ROOT=~/code/yolo-jail yolo check") {
		t.Errorf("the flake.nix finding names no fix:\n%s", got)
	}
}

// missingRuntimeNote runs the Container Runtime section on a host with no runtime, whose PATH
// holds only bins, and returns the report.
func missingRuntimeNote(t *testing.T, mac bool, machine, swVers string, bins ...string) string {
	t.Helper()
	var out bytes.Buffer
	opts := baseOptions(t, &out)
	opts.IsMacOS, opts.Machine = mac, machine
	opts.LookPath = func(name string) (string, bool) {
		for _, b := range bins {
			if b == name {
				return "/usr/bin/" + name, true
			}
		}
		return "", false
	}
	opts.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
		if strings.Join(argv, " ") == "sw_vers -productVersion" && swVers != "" {
			return ExecResult{Ran: true, Stdout: swVers + "\n"}
		}
		return ExecResult{Ran: false}
	}
	opts.sectionContainerRuntime(newReporter(&out, false))
	got := stripANSI(out.String())
	if !strings.Contains(got, "[FAIL] No container runtime installed") {
		t.Fatalf("no missing-runtime finding:\n%s", got)
	}
	return got
}

// The missing-runtime note named "your package manager, e.g. `sudo apt install podman`" on
// every host: a task rather than a step, and the wrong command on Fedora, Arch or a Mac. It
// names THIS host's command now — internal/depcheck's line for the manager it finds, or the
// exact instructions for this platform where no manager line applies.
func TestMissingRuntimeNamesThisHostsInstallCommand(t *testing.T) {
	for _, tc := range []struct {
		name          string
		mac           bool
		machine, vers string
		bins          []string
		want, notWant []string
	}{
		{name: "debian", machine: "x86_64", bins: []string{"apt"},
			want:    []string{"sudo apt install -y podman passt slirp4netns uidmap", "then: yolo check"},
			notWant: []string{"your package manager", "brew", "dnf"}},
		{name: "fedora", machine: "x86_64", bins: []string{"dnf"},
			want:    []string{"sudo dnf install -y podman", "then: yolo check"},
			notWant: []string{"apt", "brew"}},
		{name: "arch", machine: "x86_64", bins: []string{"pacman"},
			want:    []string{"sudo pacman -S --noconfirm podman", "then: yolo check"},
			notWant: []string{"apt", "brew"}},
		{name: "no known manager", machine: "x86_64", bins: []string{"nix"},
			want:    []string{"https://podman.io/docs/installation", "then: yolo check"},
			notWant: []string{"nix profile install", "sudo apt"}},
		{name: "apple silicon on macOS 26", mac: true, machine: "arm64", vers: "26.1", bins: []string{"brew"},
			want:    []string{"brew install container", "container system start", "then: yolo check"},
			notWant: []string{"apt", "podman"}},
		{name: "apple silicon before macOS 26", mac: true, machine: "arm64", vers: "15.7.1", bins: []string{"brew"},
			want: []string{"brew install podman", "podman machine init --cpus 4 --memory 8192 --disk-size 50 " +
				"-v /Users:/Users -v /private:/private -v /var/folders:/var/folders", "podman machine start",
				"then: yolo check"},
			notWant: []string{"apt", "brew install container"}},
		{name: "intel mac", mac: true, machine: "x86_64", vers: "15.7.1", bins: []string{"brew"},
			want: []string{"podman-installer-macos-amd64.pkg", "https://github.com/containers/podman/releases",
				"podman machine start", "then: yolo check"},
			notWant: []string{"apt", "brew install podman", "brew install container"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			got := missingRuntimeNote(t, tc.mac, tc.machine, tc.vers, tc.bins...)
			for _, w := range tc.want {
				if !strings.Contains(got, w) {
					t.Errorf("missing %q:\n%s", w, got)
				}
			}
			for _, w := range tc.notWant {
				if strings.Contains(got, w) {
					t.Errorf("names %q, which is not this host's:\n%s", w, got)
				}
			}
		})
	}
}

// NixOS has no package-manager line for podman: it is a system option. A host whose only
// manager is nix and that says it is NixOS gets that option.
func TestMissingRuntimeOnNixOSNamesTheSystemOption(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	var out bytes.Buffer
	opts := baseOptions(t, &out)
	opts.LookPath = func(name string) (string, bool) { return "/run/current-system/sw/bin/nix", name == "nix" }
	opts.PathExists = func(p string) bool { return p == "/etc/NIXOS" }
	opts.sectionContainerRuntime(newReporter(&out, false))
	got := stripANSI(out.String())
	for _, want := range []string{"virtualisation.podman.enable = true;", "sudo nixos-rebuild switch", "yolo check"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q:\n%s", want, got)
		}
	}
}

// Every podman package name the missing-runtime note can print comes from the getting-started
// guide's own install lines (rule 3 of the principle: a platform-specific name records where it
// came from). This reads the guide, so a renamed package there fails here instead of drifting.
func TestPodmanInstallHintsMatchTheGuide(t *testing.T) {
	guide, err := os.ReadFile(filepath.Join("..", "..", "..", "userguide", "getting-started.md"))
	if err != nil {
		t.Fatal(err)
	}
	for mgr, pkgs := range podmanInstallHints {
		found := false
		for _, l := range strings.Split(string(guide), "\n") {
			if !strings.Contains(l, mgr) {
				continue
			}
			all := true
			for _, p := range strings.Fields(pkgs) {
				if !strings.Contains(" "+l+" ", " "+p+" ") {
					all = false
				}
			}
			if all {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("no line of userguide/getting-started.md installs %q with %s", pkgs, mgr)
		}
	}
}

// A runtime that IS installed but would not run reached the same "nothing works" branch as one
// that is absent, so a second [FAIL] said "No container runtime installed" and its note told an
// apt host that none of apt, dnf or pacman was on its PATH, and a Mac to install the runtime it
// already has. The runtime's own [FAIL] names the command that shows why it does not run, and is
// the one row for that cause (HE-D2, docs/reference/host-agent-environment.md).
func TestBrokenRuntimeIsNotToldToInstallOne(t *testing.T) {
	for _, tc := range []struct {
		name    string
		mac     bool
		bins    []string
		notWant []string
	}{
		{name: "linux", bins: []string{"podman", "apt"},
			notWant: []string{"None of apt, dnf or pacman", "sudo apt install"}},
		{name: "mac", mac: true, bins: []string{"podman", "brew"},
			notWant: []string{"brew install podman", "brew install container"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			var out bytes.Buffer
			opts := baseOptions(t, &out)
			opts.IsMacOS, opts.Machine = tc.mac, "arm64"
			opts.LookPath = func(name string) (string, bool) {
				for _, b := range tc.bins {
					if b == name {
						return "/usr/bin/" + name, true
					}
				}
				return "", false
			}
			opts.Exec = func([]string, string, []string, time.Duration) ExecResult { return ExecResult{Ran: false} }
			opts.sectionContainerRuntime(newReporter(&out, false))
			got := stripANSI(out.String())
			if !strings.Contains(got, "[FAIL] podman found but not working: exec failed") {
				t.Fatalf("no finding for the podman that does not run:\n%s", got)
			}
			for _, w := range tc.notWant {
				if strings.Contains(got, w) {
					t.Errorf("an installed podman is told %q:\n%s", w, got)
				}
			}
			if strings.Contains(got, "No container runtime installed") {
				t.Errorf("an installed podman is reported as no runtime installed:\n%s", got)
			}
			if n := strings.Count(got, "[FAIL]"); n != 1 {
				t.Errorf("one cause, %d [FAIL] rows:\n%s", n, got)
			}
		})
	}
}

// The terminal's yes printed "Stopped <name>" whatever the removal returned, so a removal the
// runtime refused read as done: the false green rule 5 forbids. A failed removal says so, names
// the command to retry, and leaves the jail's tracking file, since the jail is still there.
func TestOrphanRemovalThatFailsIsNotReportedAsStopped(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	jails := twoOrphans(t)
	refuse := func(argv []string, d string, e []string, to time.Duration) ExecResult {
		if len(argv) > 1 && argv[1] == "rm" {
			return ExecResult{Ran: true, RC: 125, Stderr: "Error: cannot remove container: device busy\n"}
		}
		return jails.exec(argv, d, e, to)
	}
	// Each jail's tracking file, which the removal of a jail that is gone clears.
	if err := os.MkdirAll(paths.ContainerDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, ws := range jails.running {
		if err := os.WriteFile(filepath.Join(paths.ContainerDir(), name), []byte(ws+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	var out bytes.Buffer
	r := newReporter(&out, false)
	(&Options{IsTTYStdout: func() bool { return true }, Stdin: strings.NewReader("y\n"), Exec: refuse}).
		sectionRunningJails(r, "podman")
	got := stripANSI(out.String())
	if strings.Contains(got, "Stopped yolo-") {
		t.Errorf("a refused removal is reported as stopped:\n%s", got)
	}
	for name := range jails.running {
		if _, err := os.Stat(filepath.Join(paths.ContainerDir(), name)); err != nil {
			t.Errorf("the tracking of %s, still running, was removed: %v", name, err)
		}
	}
	for _, want := range []string{"Could not stop yolo-api-3f2a91c0", "device busy",
		"podman rm -f yolo-api-3f2a91c0", "then: yolo check"} {
		if !strings.Contains(got, want) {
			t.Errorf("the failed removal does not say %q:\n%s", want, got)
		}
	}
}
