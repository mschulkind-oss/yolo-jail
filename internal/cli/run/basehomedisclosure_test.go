// basehomedisclosure_test.go has TWO halves and they protect different things.
//
// The behavioural half drives o.ensureStorage() over a fixture home and asserts the line
// lands on o.Stderr — which also pins that the disclosure goes through the Options stream
// (and therefore the launch-log tee) rather than the process's os.Stderr, the defect the
// package-level ensureStorage could not avoid.
//
// The AST half pins the CALL SITE. Deleting `o.ensureStorage()` from Run stops the
// eviction disclosure AND the v2 layout migration, and until this file existed both were
// silent: internal/storage's own test calls MigrateStorageLayout directly with
// insideJail=true, so both production call sites could be deleted with the whole unit
// gate green — the exact shape AGENTS.md rules out.
package run

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fixtureGlobalHome points HOME at a resolved temp dir and plants legacy runtime state in
// the base home paths.GlobalHome() resolves to.
//
// HOME is the only seam: paths.GlobalHome -> GlobalStorage -> home() reads os.LookupEnv
// directly, so Options.Getenv cannot redirect it. t.Setenv therefore also means this test
// can never call t.Parallel().
//
// EvalSymlinks at the mint point, per AGENTS.md's darwin rule.
func fixtureGlobalHome(t *testing.T) string {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve temp home: %v", err)
	}
	t.Setenv("HOME", home)
	// YOLO_VERSION is SET inside this jail, and both the layout migration and the
	// base-home walk are host-only. A test that forgets this passes vacuously.
	t.Setenv("YOLO_VERSION", "")
	globalHome := filepath.Join(home, ".local", "share", "yolo-jail", "home")
	for _, rel := range []string{
		".claude/projects/-home-someone-code-thing/transcript.jsonl",
		".copilot/session-state/events.jsonl",
	} {
		p := filepath.Join(globalHome, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(p, []byte(strings.Repeat("x", 4096)), 0o600); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	return globalHome
}

func TestEnsureStorageDisclosesLegacyBaseHomeState(t *testing.T) {
	globalHome := fixtureGlobalHome(t)
	var errBuf bytes.Buffer
	o := goldenOptions("/ws", t.TempDir())
	o.Stderr = &errBuf
	o.Stdout = &bytes.Buffer{}

	if err := o.ensureStorage(); err != nil {
		t.Fatalf("ensureStorage: %v", err)
	}

	got := errBuf.String()
	for _, want := range []string{".claude", ".copilot", "Nothing has been moved"} {
		if !strings.Contains(got, want) {
			t.Errorf("stderr = %q, want it to name %q", got, want)
		}
	}
	// §5.8's redaction rule, asserted where the bytes actually reach a terminal.
	for _, forbidden := range []string{globalHome, "-home-someone-code-thing", "transcript.jsonl"} {
		if strings.Contains(got, forbidden) {
			t.Errorf("stderr = %q\n must not contain %q", got, forbidden)
		}
	}
	if strings.Contains(got, "[dim]") {
		t.Errorf("stderr = %q: the richtext tags must be rendered or stripped, not printed", got)
	}
}

// TestBaseHomeDisclosureIsHostOnly: in a jail, paths.GlobalHome() resolves under the
// jail's own disposable home while /home/agent is the :ro bind of the host's — so an
// in-jail walk either measures nothing or measures host bytes it cannot move.
func TestBaseHomeDisclosureIsHostOnly(t *testing.T) {
	fixtureGlobalHome(t)
	var errBuf bytes.Buffer
	o := goldenOptions("/ws", t.TempDir())
	o.Stderr = &errBuf
	o.Stdout = &bytes.Buffer{}
	o.Getenv = func(k string) string {
		if k == "YOLO_VERSION" {
			return "9.9.9"
		}
		return ""
	}

	o.noteLegacyBaseHome()
	if got := errBuf.String(); got != "" {
		t.Fatalf("in-jail disclosure = %q, want silence", got)
	}
}

// TestBaseHomeDisclosureIsNotSuppressible pins OQ-RO3 for this line: a disclosure is
// never gated by a report tier, and YOLO_NO_BANNER is documented as not silencing one.
func TestBaseHomeDisclosureIsNotSuppressible(t *testing.T) {
	for _, env := range []string{"YOLO_NO_BANNER", "YOLO_QUIET", "NO_COLOR"} {
		t.Run(env, func(t *testing.T) {
			fixtureGlobalHome(t)
			var errBuf bytes.Buffer
			o := goldenOptions("/ws", t.TempDir())
			o.Stderr = &errBuf
			o.Stdout = &bytes.Buffer{}
			o.Getenv = func(k string) string {
				if k == env {
					return "1"
				}
				return ""
			}
			o.noteLegacyBaseHome()
			if !strings.Contains(errBuf.String(), ".claude") {
				t.Fatalf("%s silenced the disclosure: stderr = %q", env, errBuf.String())
			}
		})
	}
}

// TestRunCallsEnsureStorage pins the call site.
//
// ⚠ IF THIS FAILS BECAUSE THE CALL MOVED rather than because it is gone, MOVE THIS CHECK
// WITH IT — do not delete it. What it protects has no other witness: deleting
// `o.ensureStorage()` from Run stops the base-home disclosure (which then looks exactly
// like a clean host) and stops the v2 layout migration (which then looks like nothing at
// all), and neither has a behavioural test that reaches the launch path.
func TestRunCallsEnsureStorage(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "run.go", nil, 0)
	if err != nil {
		t.Fatalf("parse run.go: %v", err)
	}
	var found bool
	for _, decl := range f.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Name.Name != "Run" {
			continue
		}
		ast.Inspect(fd, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "ensureStorage" {
				found = true
			}
			return true
		})
	}
	if !found {
		t.Fatal("Run no longer calls o.ensureStorage(): the base-home disclosure and the v2 " +
			"layout migration both stop running, silently. If the call MOVED, move this pin with it.")
	}
}

// TestEnsureStorageDetectsTheBaseHome pins the inner call, for the same reason one level
// down: ensureStorage's own body is what turns the launch into a detection site, and the
// behavioural test above cannot tell "the call was removed" from "the fixture produced no
// candidates" if the fixture ever changes.
func TestEnsureStorageDetectsTheBaseHome(t *testing.T) {
	fd := methodDecl(t, "run.go", "ensureStorage")
	var found bool
	ast.Inspect(fd, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "noteLegacyBaseHome" {
			found = true
		}
		return true
	})
	if !found {
		t.Fatal("ensureStorage no longer calls o.noteLegacyBaseHome(). Detection is always-on by " +
			"ruling (design §5.7) and must NOT be moved inside storage.MigrateStorageLayout, whose " +
			"layout-version marker early-return would make it marker-gated.")
	}
}
