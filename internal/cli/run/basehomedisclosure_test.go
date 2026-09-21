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

func TestEnsureStorageRefusesOnLegacyBaseHomeState(t *testing.T) {
	globalHome := fixtureGlobalHome(t)
	var errBuf bytes.Buffer
	o := goldenOptions("/ws", t.TempDir())
	o.Stderr = &errBuf
	o.Stdout = &bytes.Buffer{}

	err := o.ensureStorage()
	if err == nil {
		t.Fatal("ensureStorage returned nil: legacy bytes in the base home must REFUSE the " +
			"launch, not warn past it — a warning on every launch is one people scroll by, " +
			"and the bytes are readable by every jail while it sits there")
	}
	if !strings.Contains(err.Error(), legacyBaseHomeHatch) {
		t.Errorf("the refusal must name its hatch; got %q", err.Error())
	}

	got := errBuf.String()
	// The body is on STDERR (so it reaches the launch-log tee), not in the error string.
	for _, want := range []string{".claude", ".copilot", "mkdir -p", "mv ", "recreated empty"} {
		if !strings.Contains(got, want) {
			t.Errorf("stderr = %q, want it to name %q", got, want)
		}
	}
	// THE REDACTION RULE, NARROWED ON PURPOSE when this became a refusal (2026-09-21).
	// §5.8 forbids the line naming WORKSPACES and ENTRY paths, and both stay forbidden
	// below: the base home's largest tree is `.claude/projects/<mangled workspace path>`,
	// so printing entries would print the user's own directory names to every terminal.
	//
	// paths.GlobalHome() is neither, and it is now REQUIRED rather than forbidden: a
	// refusal whose whole purpose is to hand the user a command they can paste has to name
	// the path that command operates on. Asserted positively so that relaxation is a
	// decision this test records rather than one a future edit makes silently.
	if !strings.Contains(got, globalHome) {
		t.Errorf("stderr = %q, want the mv to name the real base home %q", got, globalHome)
	}
	for _, forbidden := range []string{"-home-someone-code-thing", "transcript.jsonl"} {
		if strings.Contains(got, forbidden) {
			t.Errorf("stderr = %q\n must not contain %q", got, forbidden)
		}
	}
	if strings.Contains(got, "[dim]") || strings.Contains(got, "[bold") {
		t.Errorf("stderr = %q: the richtext tags must be rendered or stripped, not printed", got)
	}
}

// TestTheHatchDowngradesTheRefusal: the repo's pattern is that a fatal names an escape
// hatch and the hatch is honoured. Without this, the string in the refusal is a promise
// nothing keeps.
func TestTheHatchDowngradesTheRefusal(t *testing.T) {
	fixtureGlobalHome(t)
	var errBuf bytes.Buffer
	o := goldenOptions("/ws", t.TempDir())
	o.Stderr = &errBuf
	o.Stdout = &bytes.Buffer{}
	o.Getenv = func(k string) string {
		if k == legacyBaseHomeHatch {
			return "1"
		}
		return ""
	}

	if err := o.ensureStorage(); err != nil {
		t.Fatalf("%s did not downgrade the refusal: %v", legacyBaseHomeHatch, err)
	}
	if !strings.Contains(errBuf.String(), "launching anyway") {
		t.Errorf("the hatch must still SAY it fired; stderr = %q", errBuf.String())
	}
}

// TestAnEmptyBaseHomeDoesNotRefuse is the one that keeps this from bricking every host.
//
// Empty directories are the steady state on every machine that never ran the old
// shared-writable home — measured on a real host, five candidate entries and zero bytes —
// so a gate keyed on "are there candidates" rather than "are there bytes" would refuse
// every launch, everywhere, for a condition with nothing to fix.
func TestAnEmptyBaseHomeDoesNotRefuse(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "")
	for _, rel := range []string{".claude/projects", ".copilot/session-state"} {
		if err := os.MkdirAll(filepath.Join(home, ".local", "share", "yolo-jail", "home", rel), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	var errBuf bytes.Buffer
	o := goldenOptions("/ws", t.TempDir())
	o.Stderr = &errBuf
	o.Stdout = &bytes.Buffer{}

	if err := o.ensureStorage(); err != nil {
		t.Fatalf("an empty base home must not refuse a launch: %v", err)
	}
	if got := errBuf.String(); strings.Contains(got, "mv ") {
		t.Errorf("stderr = %q, want no move instructions when there is nothing to move", got)
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

	_ = o.noteLegacyBaseHome()
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
			_ = o.noteLegacyBaseHome()
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

// TestAnUndeclaredRootWithBytesReportsButDoesNotRefuse pins the defect this gate shipped
// with for one commit.
//
// THE CASE IS REAL, NOT HYPOTHETICAL. Measured on a maintainer host 2026-09-21:
// `.claude/bin` in the base home is a `files` destination of a LOCAL pack (matt-fzf), and
// basehome.ShippedDecls() reads only packload.Embedded() — so the walk classified a user's
// own pack content as RUNTIME. With the gate keyed on Bytes() that is a launch refused with
// instructions to archive the user's own files, which is worse than the leak it prevents.
//
// The rule: an undeclared root is REPORTED (we cannot say what is in it) and never REFUSED
// (we cannot say it is wrong).
func TestAnUndeclaredRootWithBytesReportsButDoesNotRefuse(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "")
	globalHome := filepath.Join(home, ".local", "share", "yolo-jail", "home")
	// A root no shipped pack declares, holding real bytes — a local pack's files tree.
	p := filepath.Join(globalHome, ".localpackdir", "delivered-tool")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(strings.Repeat("x", 4096)), 0o600); err != nil {
		t.Fatal(err)
	}

	var errBuf bytes.Buffer
	o := goldenOptions("/ws", t.TempDir())
	o.Stderr = &errBuf
	o.Stdout = &bytes.Buffer{}

	if err := o.ensureStorage(); err != nil {
		t.Fatalf("an UNDECLARED root's bytes must not refuse a launch — they may be a local "+
			"pack's own delivered files, which this cannot tell apart: %v", err)
	}
	got := errBuf.String()
	if !strings.Contains(got, ".localpackdir") {
		t.Errorf("it must still be REPORTED; stderr = %q", got)
	}
	if strings.Contains(got, "mv ") {
		t.Errorf("stderr = %q: must not offer to move a root whose declarations were never read", got)
	}
}
