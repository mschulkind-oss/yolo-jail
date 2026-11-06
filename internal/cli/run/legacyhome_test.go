package run

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/setup"
)

// legacyHomeWorkspace is a workspace holding a legacy <ws>/.yolo/home with one transcript in it.
func legacyHomeWorkspace(t *testing.T) (ws, src string) {
	t.Helper()
	ws = t.TempDir()
	src = paths.WorkspaceHomeState(ws)
	writeTranscript(t, src)
	return ws, src
}

func writeTranscript(t *testing.T, home string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".claude", "transcript"), []byte("kept"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func legacyRequest(ws, name string, jails projectJails) legacyHomeRequest {
	return legacyHomeRequest{
		Workspace: ws,
		Setup:     setup.MustBuiltIn(name),
		UID:       os.Geteuid(),
		Jails:     jails,
	}
}

func assertHoldsTranscript(t *testing.T, home string) {
	t.Helper()
	if b, err := os.ReadFile(filepath.Join(home, ".claude", "transcript")); err != nil || string(b) != "kept" {
		t.Errorf("%s does not hold the transcript: %q, %v", home, b, err)
	}
}

func assertLegacyHomeStays(t *testing.T, src string) {
	t.Helper()
	fi, err := os.Lstat(src)
	if err != nil {
		t.Fatalf("legacy home is gone: %v", err)
	}
	if fi.IsDir() {
		assertHoldsTranscript(t, src)
	}
}

func assertOneLine(t *testing.T, line string) {
	t.Helper()
	if line == "" || strings.Contains(strings.TrimRight(line, "\n"), "\n") {
		t.Errorf("want exactly one line, got %q", line)
	}
	if strings.Contains(line, "?") || strings.Contains(strings.ToLower(line), "[y/n]") {
		t.Errorf("the move never asks: %q", line)
	}
}

var backticked = regexp.MustCompile("`([^`]+)`")

// remedy is the line's last backticked command: the one step it names.
func remedy(t *testing.T, line string) string {
	t.Helper()
	m := backticked.FindAllStringSubmatch(line, -1)
	if len(m) == 0 {
		t.Fatalf("the line names no command: %q", line)
	}
	return m[len(m)-1][1]
}

// runRemedy runs the command a line names, the way the user pastes it.
func runRemedy(t *testing.T, line string) {
	t.Helper()
	cmd := remedy(t, line)
	if out, err := exec.Command("sh", "-c", cmd).CombinedOutput(); err != nil {
		t.Fatalf("the printed command %q failed: %v\n%s", cmd, err, out)
	}
}

// TestLegacyHomeMovesSilentlyOnceNoJailRuns is SUP-D9's main case: a quiesced launch moves the
// legacy home to its setup's home, with no prompt and one informational line naming both paths.
func TestLegacyHomeMovesSilentlyOnceNoJailRuns(t *testing.T) {
	ws, src := legacyHomeWorkspace(t)
	got, line := adoptLegacyHome(legacyRequest(ws, "podman-self", projectJailsStopped))
	if got != legacyHomeMoved {
		t.Fatalf("result = %v (%s), want moved", got, line)
	}
	dest := paths.SetupHomeState(ws, "podman-self")
	assertHoldsTranscript(t, dest)
	if _, err := os.Lstat(src); !os.IsNotExist(err) {
		t.Errorf("legacy home still there after the move: %v", err)
	}
	assertOneLine(t, line)
	if !strings.Contains(line, src) || !strings.Contains(line, dest) {
		t.Errorf("the line must name both paths: %q", line)
	}
}

// TestLegacyHomeReplacesAnEmptyDestination (SUP-D21): an empty directory at the setup's home
// holds nothing to lose, so it does not turn a move into a home left behind.
func TestLegacyHomeReplacesAnEmptyDestination(t *testing.T) {
	ws, src := legacyHomeWorkspace(t)
	dest := paths.SetupHomeState(ws, "podman-self")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	if got, line := adoptLegacyHome(legacyRequest(ws, "podman-self", projectJailsStopped)); got != legacyHomeMoved {
		t.Fatalf("result = %v (%s), want moved", got, line)
	}
	assertHoldsTranscript(t, dest)
	if _, err := os.Lstat(src); !os.IsNotExist(err) {
		t.Errorf("legacy home still there after the move: %v", err)
	}
}

// TestLegacyHomeWaitsWhileAJailRuns: a running jail of the project, or a runtime that could not be
// asked, leaves a movable legacy home in place, writes nothing, and says this setup keeps using it
// until a later launch moves it (SUP-D21).
func TestLegacyHomeWaitsWhileAJailRuns(t *testing.T) {
	for _, jails := range []projectJails{projectJailsRunning, projectJailsUnknown} {
		t.Run(jails.String(), func(t *testing.T) {
			ws, src := legacyHomeWorkspace(t)
			got, line := adoptLegacyHome(legacyRequest(ws, "podman-self", jails))
			if got != legacyHomeDeferred {
				t.Fatalf("result = %v (%s), want deferred", got, line)
			}
			assertLegacyHomeStays(t, src)
			if _, err := os.Lstat(paths.SetupsDir(ws)); !os.IsNotExist(err) {
				t.Errorf("a deferred move created %s: %v", paths.SetupsDir(ws), err)
			}
			assertOneLine(t, line)
			if !strings.Contains(line, "yolo stop") || !strings.Contains(line, "keeps using") {
				t.Errorf("the line must say what this launch uses and name the next step: %q", line)
			}
		})
	}
}

// TestLegacyHomeDefersOnlyWhatItWouldMove: a source a quiesced launch would leave is reported as
// left even while a jail runs, so no line promises a move that will not happen.
func TestLegacyHomeDefersOnlyWhatItWouldMove(t *testing.T) {
	cases := map[string]func(t *testing.T, ws, src string, r *legacyHomeRequest){
		"symbolic link": func(t *testing.T, ws, src string, r *legacyHomeRequest) { relink(t, ws, src) },
		"foreign owned": func(t *testing.T, ws, src string, r *legacyHomeRequest) { r.UID = os.Geteuid() + 1 },
		"another setup's": func(t *testing.T, ws, src string, r *legacyHomeRequest) {
			r.AppleContainerLaid = func(string) bool { return true }
		},
	}
	for name, arrange := range cases {
		t.Run(name, func(t *testing.T) {
			ws, src := legacyHomeWorkspace(t)
			r := legacyRequest(ws, "podman-self", projectJailsRunning)
			arrange(t, ws, src, &r)
			if got, line := adoptLegacyHome(r); got != legacyHomeLeft {
				t.Errorf("result = %v (%s), want left", got, line)
			}
		})
	}
}

// relink moves src aside and leaves a link to it at src.
func relink(t *testing.T, ws, src string) string {
	t.Helper()
	real := filepath.Join(ws, "elsewhere")
	if err := os.Rename(src, real); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, src); err != nil {
		t.Fatal(err)
	}
	return real
}

// TestLegacyHomeLeavesWhatItCannotMove: whatever yolo cannot move stays in place, with one line
// whose command, run as printed, clears the way; where one was run, the next launch moves the home
// or starts fresh as the case says, and nothing is lost.
func TestLegacyHomeLeavesWhatItCannotMove(t *testing.T) {
	for _, c := range []struct {
		name    string
		arrange func(t *testing.T, ws, src string, r *legacyHomeRequest)
		// run: the printed command is runnable here; then is the next launch's result.
		run  bool
		then legacyHomeResult
		mark string // a word the line must hold
	}{
		{name: "symbolic link", run: true, then: legacyHomeAbsent, mark: "symbolic link",
			arrange: func(t *testing.T, ws, src string, r *legacyHomeRequest) {
				real := relink(t, ws, src)
				t.Cleanup(func() { assertHoldsTranscript(t, real) })
			}},
		{name: "dangling symbolic link", run: true, then: legacyHomeAbsent, mark: "symbolic link",
			arrange: func(t *testing.T, ws, src string, r *legacyHomeRequest) {
				if err := os.RemoveAll(src); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(ws, "absent"), src); err != nil {
					t.Fatal(err)
				}
			}},
		{name: "regular file", run: true, then: legacyHomeAbsent, mark: "not a directory",
			arrange: func(t *testing.T, ws, src string, r *legacyHomeRequest) {
				if err := os.RemoveAll(src); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(src, []byte("x"), 0o600); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if b, err := os.ReadFile(src + ".not-a-home"); err != nil || string(b) != "x" {
						t.Errorf("the file was not kept aside: %q, %v", b, err)
					}
				})
			}},
		// Another account's directory: the request's invoking user is not its owner.
		{name: "foreign owned", mark: "chown",
			arrange: func(t *testing.T, ws, src string, r *legacyHomeRequest) { r.UID = os.Geteuid() + 1 }},
		{name: "another setup's layout", mark: "Apple Container",
			arrange: func(t *testing.T, ws, src string, r *legacyHomeRequest) {
				r.AppleContainerLaid = func(string) bool { return true }
			}},
		{name: "destination holds a home", run: true, then: legacyHomeMoved, mark: "already",
			arrange: func(t *testing.T, ws, src string, r *legacyHomeRequest) {
				dest := paths.SetupHomeState(ws, "podman-self")
				writeTranscript(t, dest)
				t.Cleanup(func() { assertHoldsTranscript(t, dest+".aside") })
			}},
		{name: "destination is a dangling link", run: true, then: legacyHomeMoved, mark: "already",
			arrange: func(t *testing.T, ws, src string, r *legacyHomeRequest) {
				dest := paths.SetupHomeState(ws, "podman-self")
				if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(ws, "absent"), dest); err != nil {
					t.Fatal(err)
				}
			}},
	} {
		t.Run(c.name, func(t *testing.T) {
			ws, src := legacyHomeWorkspace(t)
			r := legacyRequest(ws, "podman-self", projectJailsStopped)
			c.arrange(t, ws, src, &r)
			got, line := adoptLegacyHome(r)
			if got != legacyHomeLeft {
				t.Fatalf("result = %v (%s), want left in place", got, line)
			}
			assertLegacyHomeStays(t, src)
			assertOneLine(t, line)
			if !strings.Contains(line, src) || !strings.Contains(line, c.mark) {
				t.Errorf("the line must name %s and why (%q): %q", src, c.mark, line)
			}
			if !c.run {
				return
			}
			runRemedy(t, line)
			r = legacyRequest(ws, "podman-self", projectJailsStopped)
			if got, line := adoptLegacyHome(r); got != c.then {
				t.Fatalf("after the printed command, result = %v (%s), want %v", got, line, c.then)
			}
			if c.then == legacyHomeMoved {
				assertHoldsTranscript(t, paths.SetupHomeState(ws, "podman-self"))
			}
		})
	}
}

// TestLegacyHomeManualMoveLands: where the filesystem cannot do a rename that refuses to replace,
// or the home is its own mount, the line names that plainly and gives a manual move which, run as
// printed, lands the home at the setup's home.
func TestLegacyHomeManualMoveLands(t *testing.T) {
	for _, c := range []struct {
		errno unix.Errno
		mark  string
	}{
		{unix.EINVAL, "filesystem"},
		{unix.ENOTSUP, "filesystem"},
		{unix.EXDEV, "mount"},
	} {
		t.Run(c.errno.Error(), func(t *testing.T) {
			orig := legacyHomeRename
			legacyHomeRename = func(int, string, int, string) error { return c.errno }
			t.Cleanup(func() { legacyHomeRename = orig })
			ws, src := legacyHomeWorkspace(t)
			got, line := adoptLegacyHome(legacyRequest(ws, "podman-self", projectJailsStopped))
			if got != legacyHomeLeft {
				t.Fatalf("result = %v (%s), want left", got, line)
			}
			assertLegacyHomeStays(t, src)
			assertOneLine(t, line)
			if !strings.Contains(line, c.mark) {
				t.Errorf("the line must name the %s: %q", c.mark, line)
			}
			runRemedy(t, line)
			assertHoldsTranscript(t, paths.SetupHomeState(ws, "podman-self"))
		})
	}
}

// TestLegacyHomeRefusesLinkedState: a link the last jail left at .yolo or .yolo/setups is never
// followed; the home stays, nothing lands in the link's target, and the line says so.
func TestLegacyHomeRefusesLinkedState(t *testing.T) {
	for _, c := range []struct {
		name     string
		at       func(ws string) string
		dangling bool
	}{
		{"setups", paths.SetupsDir, false},
		{"setups dangling", paths.SetupsDir, true},
		{".yolo", paths.WorkspaceStateDir, false},
		{".yolo dangling", paths.WorkspaceStateDir, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			ws, src := legacyHomeWorkspace(t)
			link := c.at(ws)
			target := t.TempDir()
			if c.at(ws) == paths.WorkspaceStateDir(ws) {
				// The legacy home sits in the directory the link names.
				if err := os.Rename(link, filepath.Join(target, "yolo")); err != nil {
					t.Fatal(err)
				}
				target = filepath.Join(target, "yolo")
				src = filepath.Join(target, "home")
			}
			if c.dangling {
				target = filepath.Join(t.TempDir(), "absent")
			}
			if err := os.Symlink(target, link); err != nil {
				t.Fatal(err)
			}
			got, line := adoptLegacyHome(legacyRequest(ws, "podman-self", projectJailsStopped))
			if got != legacyHomeLeft {
				t.Fatalf("result = %v (%s), want left in place", got, line)
			}
			assertOneLine(t, line)
			if !strings.Contains(line, link) || !strings.Contains(line, "symbolic link") {
				t.Errorf("the line must name the link %s: %q", link, line)
			}
			if !c.dangling {
				if c.name == ".yolo" {
					assertLegacyHomeStays(t, src)
				} else if entries, _ := os.ReadDir(target); len(entries) != 0 {
					t.Errorf("wrote %d entries through the link", len(entries))
				}
			}
		})
	}
}

func TestLegacyHomeAbsentSaysNothing(t *testing.T) {
	ws := t.TempDir()
	for _, jails := range []projectJails{projectJailsStopped, projectJailsRunning, projectJailsUnknown} {
		if got, line := adoptLegacyHome(legacyRequest(ws, "podman-self", jails)); got != legacyHomeAbsent || line != "" {
			t.Errorf("%v: result = %v, line %q; want absent and silent", jails, got, line)
		}
	}
	if _, err := os.Lstat(paths.WorkspaceStateDir(ws)); !os.IsNotExist(err) {
		t.Errorf("an absent legacy home created state: %v", err)
	}
}

// TestLegacyHomeReadsNoLayoutThroughALink: the Apple Container layout signal reads files beneath
// the home, so it is asked only about a real directory of the user's.
func TestLegacyHomeReadsNoLayoutThroughALink(t *testing.T) {
	ws, src := legacyHomeWorkspace(t)
	relink(t, ws, src)
	r := legacyRequest(ws, "seatbelt-self", projectJailsStopped)
	r.AppleContainerLaid = func(string) bool {
		t.Error("the layout signal was read through a symbolic link")
		return false
	}
	adoptLegacyHome(r)
}

// TestLegacyHomeAttribution is SUP-D20: a home moves only to a setup whose technology could have
// laid it, and the Apple Container layout signal keeps an Apple Container home from Seatbelt.
func TestLegacyHomeAttribution(t *testing.T) {
	acLaid := func(string) bool { return true }
	for _, c := range []struct {
		name, setup string
		signal      func(string) bool
		want        legacyHomeResult
	}{
		{name: "host never adopts", setup: "host-self", want: legacyHomeNotOurs},
		{name: "yolo-account host never adopts", setup: "host-yolo-account", want: legacyHomeNotOurs},
		{name: "podman adopts", setup: "podman-self", want: legacyHomeMoved},
		{name: "seatbelt adopts the shared layout", setup: "seatbelt-yolo-account", want: legacyHomeMoved},
		{name: "apple container adopts the shared layout", setup: "apple-container-self", want: legacyHomeMoved},
		{name: "apple container adopts its own copy", setup: "apple-container-self", signal: acLaid, want: legacyHomeMoved},
		{name: "seatbelt leaves an apple container home", setup: "seatbelt-self", signal: acLaid, want: legacyHomeLeft},
		{name: "podman leaves an apple container home", setup: "podman-self", signal: acLaid, want: legacyHomeLeft},
	} {
		t.Run(c.name, func(t *testing.T) {
			ws, src := legacyHomeWorkspace(t)
			r := legacyRequest(ws, c.setup, projectJailsStopped)
			r.AppleContainerLaid = c.signal
			got, line := adoptLegacyHome(r)
			if got != c.want {
				t.Fatalf("result = %v (%q), want %v", got, line, c.want)
			}
			switch got {
			case legacyHomeNotOurs:
				if line != "" {
					t.Errorf("a setup that never read .yolo/home says nothing, got %q", line)
				}
				assertLegacyHomeStays(t, src)
			case legacyHomeMoved:
				assertHoldsTranscript(t, paths.SetupHomeState(ws, c.setup))
			case legacyHomeLeft:
				assertLegacyHomeStays(t, src)
				if !strings.Contains(line, "an Apple Container setup") {
					t.Errorf("the line must name an Apple Container setup: %q", line)
				}
			}
		})
	}
}
