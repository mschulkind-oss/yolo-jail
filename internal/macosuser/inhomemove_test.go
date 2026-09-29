package macosuser

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

// inhomemove_test.go RUNS the in-home remedy. The refusal exists to end "a remedy that cannot
// reach the path it names", so what is pinned is not its wording but what its commands do: taken
// out of the message as printed and handed to a shell, in order, they must leave the project at
// the one path the printed `macos-fix-permissions` line names, and touch nothing else there.

// moveFixture builds a project under a home and an empty shared root, points the remedy at that
// root, and returns the project, its home and the root.
func moveFixture(t *testing.T) (ws, home, root string) {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home = filepath.Join(base, "Users", "matt")
	ws = filepath.Join(home, "code", "proj")
	root = filepath.Join(base, "Users", "Shared", "yolo")
	for _, d := range []string{ws, root} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(ws, "marker"), []byte("the project"), 0o644); err != nil {
		t.Fatal(err)
	}
	prev := sharedRootForMove
	sharedRootForMove = func() string { return root }
	t.Cleanup(func() { sharedRootForMove = prev })
	return ws, home, root
}

// runRemedy runs every `rm`/`mv` command line of msg in a shell, in order, and returns the
// directory the `macos-fix-permissions` line names, unquoted by the same shell.
func runRemedy(t *testing.T, msg string) (shared string) {
	t.Helper()
	for _, line := range strings.Split(msg, "\n") {
		cmd := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(cmd, "rm "), strings.HasPrefix(cmd, "mv "):
			if out, err := exec.Command("/bin/sh", "-c", cmd).CombinedOutput(); err != nil {
				t.Fatalf("the printed command %q fails: %v\n%s\nthe remedy was:\n%s", cmd, err, out, msg)
			}
		case strings.HasPrefix(cmd, "yolo macos-fix-permissions "):
			arg := strings.TrimPrefix(cmd, "yolo macos-fix-permissions ")
			out, err := exec.Command("/bin/sh", "-c", "printf %s "+arg).Output()
			if err != nil {
				t.Fatal(err)
			}
			shared = string(out)
		}
	}
	if shared == "" {
		t.Fatalf("the remedy names no macos-fix-permissions:\n%s", msg)
	}
	return shared
}

// Four destinations, each through both spellings (the launch's refusal and yolo check's fix):
// nothing there; a link to the project, the natural workaround for the neutral-ground rule
// (mv followed it and tried to move the project into itself); a real directory of that name
// (the project landed inside it, and the share named the level above); and a link to
// something else. It fails if the remedy goes back to a plain mv to <root>/<name>, and if
// either spelling stops printing the plan's commands.
func TestTheInHomeRemedyMovesTheProjectWhereItsShareCommandPoints(t *testing.T) {
	cases := map[string]func(t *testing.T, ws, root string){
		"nothing there": nil,
		"a link to the project": func(t *testing.T, ws, root string) {
			if err := os.Symlink(ws, filepath.Join(root, "proj")); err != nil {
				t.Fatal(err)
			}
		},
		"a directory of that name": func(t *testing.T, ws, root string) {
			if err := os.MkdirAll(filepath.Join(root, "proj"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "proj", "theirs"), []byte("someone else's"), 0o644); err != nil {
				t.Fatal(err)
			}
		},
		"a link to something else": func(t *testing.T, ws, root string) {
			elsewhere := filepath.Join(root, "elsewhere")
			if err := os.MkdirAll(elsewhere, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(elsewhere, filepath.Join(root, "proj")); err != nil {
				t.Fatal(err)
			}
		},
	}
	spellings := map[string]func(ws, home string) string{
		"the launch's refusal": func(ws, home string) string {
			return richtext.Render(inHomeWorkspaceRefusal(ws, home), false)
		},
		"yolo check's fix": inHomeWorkspaceFix,
	}
	for name, plant := range cases {
		for spelling, render := range spellings {
			t.Run(name+"/"+spelling, func(t *testing.T) {
				ws, home, root := moveFixture(t)
				if plant != nil {
					plant(t, ws, root)
				}
				var before []string
				_ = filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
					if err == nil && p != root {
						before = append(before, p)
					}
					return nil
				})

				msg := render(ws, home)
				shared := runRemedy(t, msg)

				if got, err := os.ReadFile(filepath.Join(shared, "marker")); err != nil || string(got) != "the project" {
					t.Errorf("after the printed commands the project is not at %s, the path the "+
						"share names (%v)\nthe remedy was:\n%s", shared, err, msg)
				}
				if filepath.Dir(shared) != root {
					t.Errorf("the project landed at %s, not directly under the shared root %s", shared, root)
				}
				if _, err := os.Lstat(ws); !os.IsNotExist(err) {
					t.Errorf("the project is still at %s after the move (%v)", ws, err)
				}
				// Nothing already there was touched, except a link to the project itself.
				for _, p := range before {
					if name == "a link to the project" && p == filepath.Join(root, "proj") {
						continue
					}
					if _, err := os.Lstat(p); err != nil {
						t.Errorf("the remedy removed %s, which was there before it ran", p)
					}
				}
				if name == "a directory of that name" {
					if b, err := os.ReadFile(filepath.Join(root, "proj", "theirs")); err != nil || string(b) != "someone else's" {
						t.Errorf("the directory already at the destination was changed (%v)", err)
					}
				}
			})
		}
	}
}
