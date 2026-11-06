package run

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/setup"
)

// legacyHomeWorkspace is a workspace holding a legacy <ws>/.yolo/home with one transcript in it.
func legacyHomeWorkspace(t *testing.T) (ws, src string) {
	t.Helper()
	ws = t.TempDir()
	src = paths.WorkspaceHomeState(ws)
	if err := os.MkdirAll(filepath.Join(src, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, ".claude", "transcript"), []byte("kept"), 0o600); err != nil {
		t.Fatal(err)
	}
	return ws, src
}

func legacyRequest(ws, name string, jails projectJails) legacyHomeRequest {
	return legacyHomeRequest{
		Workspace: ws,
		Setup:     setup.MustBuiltIn(name),
		UID:       os.Geteuid(),
		Jails:     jails,
	}
}

func assertLegacyHomeStays(t *testing.T, ws, src string) {
	t.Helper()
	fi, err := os.Lstat(src)
	if err != nil {
		t.Fatalf("legacy home is gone: %v", err)
	}
	if fi.IsDir() {
		if b, err := os.ReadFile(filepath.Join(src, ".claude", "transcript")); err != nil || string(b) != "kept" {
			t.Errorf("legacy home's contents changed: %q, %v", b, err)
		}
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

// TestLegacyHomeMovesSilentlyOnceNoJailRuns is SUP-D9's main case: a quiesced launch moves the
// legacy home to its setup's home, with no prompt and one informational line naming both paths.
func TestLegacyHomeMovesSilentlyOnceNoJailRuns(t *testing.T) {
	ws, src := legacyHomeWorkspace(t)
	got, line := adoptLegacyHome(legacyRequest(ws, "podman-self", projectJailsStopped))
	if got != legacyHomeMoved {
		t.Fatalf("result = %v (%s), want moved", got, line)
	}
	dest := paths.SetupHomeState(ws, "podman-self")
	if b, err := os.ReadFile(filepath.Join(dest, ".claude", "transcript")); err != nil || string(b) != "kept" {
		t.Fatalf("the setup's home does not hold the legacy home: %q, %v", b, err)
	}
	if _, err := os.Lstat(src); !os.IsNotExist(err) {
		t.Errorf("legacy home still there after the move: %v", err)
	}
	assertOneLine(t, line)
	if !strings.Contains(line, src) || !strings.Contains(line, dest) {
		t.Errorf("the line must name both paths: %q", line)
	}
}

// TestLegacyHomeWaitsWhileAJailRuns: a running jail of the project, or a runtime that could not be
// asked, leaves the legacy home in place, writes nothing, and says it moves at a later launch.
func TestLegacyHomeWaitsWhileAJailRuns(t *testing.T) {
	for _, jails := range []projectJails{projectJailsRunning, projectJailsUnknown} {
		t.Run(jails.String(), func(t *testing.T) {
			ws, src := legacyHomeWorkspace(t)
			got, line := adoptLegacyHome(legacyRequest(ws, "podman-self", jails))
			if got != legacyHomeDeferred {
				t.Fatalf("result = %v (%s), want deferred", got, line)
			}
			assertLegacyHomeStays(t, ws, src)
			if _, err := os.Lstat(paths.SetupsDir(ws)); !os.IsNotExist(err) {
				t.Errorf("a deferred move created %s: %v", paths.SetupsDir(ws), err)
			}
			assertOneLine(t, line)
			if !strings.Contains(line, "yolo stop") {
				t.Errorf("the line must name the next step: %q", line)
			}
		})
	}
}

// TestLegacyHomeLeavesWhatItCannotMove: a source that is not a real directory owned by the
// invoking user, or a destination already there, stays in place with the printed mv.
func TestLegacyHomeLeavesWhatItCannotMove(t *testing.T) {
	cases := map[string]func(t *testing.T, ws, src string, r *legacyHomeRequest){
		"symbolic link": func(t *testing.T, ws, src string, r *legacyHomeRequest) {
			real := filepath.Join(ws, "elsewhere")
			if err := os.Rename(src, real); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(real, src); err != nil {
				t.Fatal(err)
			}
		},
		"regular file": func(t *testing.T, ws, src string, r *legacyHomeRequest) {
			if err := os.RemoveAll(src); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(src, []byte("x"), 0o600); err != nil {
				t.Fatal(err)
			}
		},
		// Another account's directory: the request's invoking user is not its owner.
		"foreign owned": func(t *testing.T, ws, src string, r *legacyHomeRequest) {
			r.UID = os.Geteuid() + 1
		},
		"destination exists": func(t *testing.T, ws, src string, r *legacyHomeRequest) {
			if err := os.MkdirAll(paths.SetupHomeState(ws, "podman-self"), 0o755); err != nil {
				t.Fatal(err)
			}
		},
	}
	for name, arrange := range cases {
		t.Run(name, func(t *testing.T) {
			ws, src := legacyHomeWorkspace(t)
			r := legacyRequest(ws, "podman-self", projectJailsStopped)
			arrange(t, ws, src, &r)
			got, line := adoptLegacyHome(r)
			if got != legacyHomeLeft {
				t.Fatalf("result = %v (%s), want left in place", got, line)
			}
			assertLegacyHomeStays(t, ws, src)
			assertOneLine(t, line)
			if !strings.Contains(line, "mv ") || !strings.Contains(line, src) {
				t.Errorf("the line must give the mv: %q", line)
			}
		})
	}
}

// TestLegacyHomeRefusesALinkedSetupsDir: a link the last jail left at .yolo/setups is never
// followed; the home stays and nothing lands in the link's target.
func TestLegacyHomeRefusesALinkedSetupsDir(t *testing.T) {
	ws, src := legacyHomeWorkspace(t)
	target := t.TempDir()
	if err := os.Symlink(target, paths.SetupsDir(ws)); err != nil {
		t.Fatal(err)
	}
	got, line := adoptLegacyHome(legacyRequest(ws, "podman-self", projectJailsStopped))
	if got != legacyHomeLeft {
		t.Fatalf("result = %v (%s), want left in place", got, line)
	}
	assertLegacyHomeStays(t, ws, src)
	if entries, _ := os.ReadDir(target); len(entries) != 0 {
		t.Errorf("wrote %d entries through the link", len(entries))
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

// TestLegacyHomeAttribution is SUP-D20: a home moves only to a setup whose technology could have
// laid it, and the Apple Container layout signal keeps an Apple Container home from Seatbelt.
func TestLegacyHomeAttribution(t *testing.T) {
	acLaid := func(string) bool { return true }
	for _, c := range []struct {
		name, setup string
		signal      func(string) bool
		want        legacyHomeResult
		mvTo        string
	}{
		{name: "host never adopts", setup: "host-self", want: legacyHomeNotOurs},
		{name: "yolo-account host never adopts", setup: "host-yolo-account", want: legacyHomeNotOurs},
		{name: "podman adopts", setup: "podman-self", want: legacyHomeMoved},
		{name: "seatbelt adopts the shared layout", setup: "seatbelt-yolo-account", want: legacyHomeMoved},
		{name: "apple container adopts the shared layout", setup: "apple-container-self", want: legacyHomeMoved},
		{name: "apple container adopts its own copy", setup: "apple-container-self", signal: acLaid, want: legacyHomeMoved},
		{name: "seatbelt leaves an apple container home", setup: "seatbelt-self", signal: acLaid, want: legacyHomeLeft, mvTo: "apple-container-self"},
		{name: "podman leaves an apple container home", setup: "podman-self", signal: acLaid, want: legacyHomeLeft, mvTo: "apple-container-self"},
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
				assertLegacyHomeStays(t, ws, src)
			case legacyHomeMoved:
				if _, err := os.Lstat(paths.SetupHomeState(ws, c.setup)); err != nil {
					t.Errorf("not moved to %s's home: %v", c.setup, err)
				}
			case legacyHomeLeft:
				assertLegacyHomeStays(t, ws, src)
				if !strings.Contains(line, paths.SetupHomeState(ws, c.mvTo)) {
					t.Errorf("the mv must name %s's home: %q", c.mvTo, line)
				}
			}
		})
	}
}
