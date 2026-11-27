package check

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	yoloruntime "github.com/mschulkind-oss/yolo-jail/internal/runtime"
	officialpacks "github.com/mschulkind-oss/yolo-jail/packs"
)

// treeListing is every path beneath dir with its mode, for a before/after comparison.
func treeListing(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		out = append(out, p+" "+info.Mode().String()+" "+info.ModTime().String())
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// TestCheckReportsWhatWaitsToMerge: a workspace whose other setup's home holds sessions, with a
// jail of the project running, gets ONE line naming that setup and `yolo stop`, and the check
// writes nothing (docs/design/unified-workspace-home.md §9.4: a launch is the only thing that merges).
func TestCheckReportsWhatWaitsToMerge(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	var out, errBuf bytes.Buffer
	o := baseOptions(t, &out)
	o.Stderr = &errBuf
	ws := o.Workspace

	stranded := paths.SetupHomeState(ws, "apple-container-self")
	session := filepath.Join(stranded, ".claude", "projects", "-workspace", "c.jsonl")
	if err := os.MkdirAll(filepath.Dir(session), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(session, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(paths.SetupStateDir(ws, "podman-self"), 0o755); err != nil {
		t.Fatal(err)
	}

	loaded, problems := packload.MaterializeEmbedded(officialpacks.FS, t.TempDir())
	if len(problems) != 0 {
		t.Fatalf("materializing official packs: %v", problems)
	}
	var claude []*packload.Pack
	for _, p := range loaded {
		if p.Name == "claude" {
			claude = append(claude, p)
		}
	}
	if len(claude) != 1 {
		t.Fatalf("the claude pack was not found among %d loaded", len(loaded))
	}
	o.unifiedHomePacks = func() ([]*packload.Pack, bool) { return claude, true }

	running := yoloruntime.JailName(ws, "podman-self")
	o.LookPath = func(name string) (string, bool) { return "/bin/" + name, name == "podman" }
	o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
		if slices.Equal(argv, []string{"podman", "ps", "-a", "-q", "--filter", "name=^/" + running + "$"}) {
			return ExecResult{Ran: true, Stdout: "0123abcd\n"}
		}
		return ExecResult{Ran: true}
	}

	before := treeListing(t, ws)
	o.noteUnifiedHomeWaiting()
	after := treeListing(t, ws)

	if !slices.Equal(before, after) {
		t.Errorf("the check wrote into the workspace:\nbefore %v\nafter  %v", before, after)
	}
	if out.Len() != 0 {
		t.Errorf("the report stream received %q; a disclosure is not part of the report", out.String())
	}
	lines := strings.Split(strings.TrimSpace(errBuf.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("want ONE line, got %d:\n%s", len(lines), errBuf.String())
	}
	for _, want := range []string{"apple-container-self", "yolo stop"} {
		if !strings.Contains(lines[0], want) {
			t.Errorf("the line is missing %q: %s", want, lines[0])
		}
	}

	// Nothing waits once the home holds nothing a launch would move.
	if err := os.Remove(session); err != nil {
		t.Fatal(err)
	}
	errBuf.Reset()
	o.noteUnifiedHomeWaiting()
	if errBuf.Len() != 0 {
		t.Errorf("an empty home was reported as waiting: %s", errBuf.String())
	}
}

// TestCheckCallsTheUnifiedHomeReport pins the call site in Check, the shape of
// TestCheckCallsTheBaseHomeDisclosure: baseOptions skips the ensure-storage block, so no
// behavioural test reaches the call, and deleting it silences the only place a waiting merge shows.
func TestCheckCallsTheUnifiedHomeReport(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "check.go", nil, 0)
	if err != nil {
		t.Fatalf("parse check.go: %v", err)
	}
	var found bool
	for _, decl := range f.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Name.Name != "Check" {
			continue
		}
		ast.Inspect(fd, func(n ast.Node) bool {
			if call, ok := n.(*ast.CallExpr); ok {
				if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "noteUnifiedHomeWaiting" {
					found = true
				}
			}
			return true
		})
	}
	if !found {
		t.Fatal("Check no longer calls o.noteUnifiedHomeWaiting(). It is the only place a home waiting " +
			"to merge into the workspace's shared home is shown (a launch merges; there is no verb, OQ-UH2).")
	}
}
