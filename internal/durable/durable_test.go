package durable

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// workspaceDir is a workspace under a resolved temp dir, so a darwin /var → /private/var
// link cannot make a comparison disagree (AGENTS.md, the darwin PATH-RESOLUTION class).
func workspaceDir(t *testing.T) string {
	t.Helper()
	d, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ws := filepath.Join(d, "ws")
	if err := os.Mkdir(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	return ws
}

func TestEnsureCreatesTheDirOnceAndKeepsWhatIsThere(t *testing.T) {
	ws := workspaceDir(t)
	got, err := Ensure(ws)
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if got != HostPath(ws) || got != filepath.Join(ws, ".yolo", "durable") {
		t.Fatalf("Ensure returned %q", got)
	}
	// .yolo came through the chokepoint: its ignore file hides the durable dir from git.
	if b, err := os.ReadFile(filepath.Join(ws, ".yolo", paths.WorkspaceStateIgnoreName)); err != nil ||
		!strings.Contains(string(b), "\n*\n") {
		t.Fatalf(".yolo has no ignore file: %v %q", err, b)
	}
	if err := os.WriteFile(filepath.Join(got, "note"), []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Ensure(ws); err != nil {
		t.Fatalf("second Ensure: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(got, "note")); string(b) != "mine" {
		t.Fatal("Ensure disturbed the directory's contents")
	}
}

// A link the jail left at `.yolo` or at `durable` is refused, never followed: the launcher
// would otherwise create the directory wherever the link points.
func TestEnsureRefusesALinkAndSaysWhich(t *testing.T) {
	for _, tc := range []struct{ name, link, want string }{
		{"linked .yolo", ".yolo", "`.yolo` is a symbolic link"},
		{"linked durable", ".yolo/durable", "`.yolo/durable` is a symbolic link"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ws := workspaceDir(t)
			target := t.TempDir()
			if tc.link == ".yolo/durable" {
				if err := os.Mkdir(filepath.Join(ws, ".yolo"), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.Symlink(target, filepath.Join(ws, tc.link)); err != nil {
				t.Fatal(err)
			}
			_, err := Ensure(ws)
			if err == nil {
				t.Fatal("Ensure followed a link")
			}
			if got := Reason(err, ws); got != tc.want {
				t.Errorf("Reason = %q, want %q", got, tc.want)
			}
			if entries, _ := os.ReadDir(target); len(entries) != 0 {
				t.Errorf("Ensure wrote through the link: %v", entries)
			}
		})
	}
}

func TestEnsureRefusesAFileAtTheName(t *testing.T) {
	ws := workspaceDir(t)
	if err := os.MkdirAll(filepath.Join(ws, ".yolo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, ".yolo", "durable"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Ensure(ws)
	if err == nil || Reason(err, ws) != "`.yolo/durable` is not a directory" {
		t.Fatalf("err %v, reason %q", err, Reason(err, ws))
	}
}
