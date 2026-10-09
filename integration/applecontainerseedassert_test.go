package integration

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type acLoginSeedProbe struct {
	jailSeed string
	dirs     map[string]string
}

func parseACLoginSeedProbe(stdout string) (acLoginSeedProbe, error) {
	const (
		seedMarker = "=== SEED ==="
		dirsMarker = "=== DIRS ==="
		endMarker  = "=== END ==="
	)
	seedAt := strings.Index(stdout, seedMarker)
	dirsAt := strings.Index(stdout, dirsMarker)
	endAt := strings.Index(stdout, endMarker)
	if seedAt < 0 || dirsAt < 0 || endAt < 0 || seedAt >= dirsAt || dirsAt >= endAt {
		return acLoginSeedProbe{}, errors.New("probe markers are missing or out of order")
	}
	seed := strings.TrimSpace(stdout[seedAt+len(seedMarker) : dirsAt])
	dirs := make(map[string]string, 3)
	for _, line := range strings.Split(stdout[dirsAt+len(dirsMarker):endAt], "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		name, state, ok := strings.Cut(line, "|")
		if !ok || name == "" || (state != "DIR" && state != "ABSENT") {
			return acLoginSeedProbe{}, fmt.Errorf("malformed directory fact %q", line)
		}
		if _, exists := dirs[name]; exists {
			return acLoginSeedProbe{}, fmt.Errorf("duplicate directory fact %q", name)
		}
		dirs[name] = state
	}
	for _, name := range []string{".claude", "claude", "npm-global"} {
		if _, ok := dirs[name]; !ok {
			return acLoginSeedProbe{}, fmt.Errorf("probe did not report the %q directory", name)
		}
	}
	return acLoginSeedProbe{jailSeed: seed, dirs: dirs}, nil
}

func acLoginSeedFailures(email string, probe acLoginSeedProbe, hostCopy string, hostCopyErr error) error {
	var wrong []string
	if email == "" {
		wrong = append(wrong, "the fake login email is empty")
	} else {
		if !strings.Contains(probe.jailSeed, email) {
			wrong = append(wrong, "the jail's ~/.claude.json lacks the seed's login")
		}
		if hostCopyErr != nil {
			wrong = append(wrong, "reading the host workspace's .claude.json failed: "+hostCopyErr.Error())
		} else if !strings.Contains(hostCopy, email) {
			wrong = append(wrong, "the host workspace's .claude.json lacks the seed's login")
		}
	}
	for dir, want := range map[string]string{".claude": "DIR", "claude": "ABSENT", "npm-global": "ABSENT"} {
		if got := probe.dirs[dir]; got != want {
			wrong = append(wrong, fmt.Sprintf("~/%s is %s, want %s", dir, got, want))
		}
	}
	if len(wrong) != 0 {
		return errors.New(strings.Join(wrong, "; "))
	}
	return nil
}

func TestACLoginSeedProbeRejectsDuplicateDirectoryFacts(t *testing.T) {
	const email = "yolo-it-test@example.invalid"
	base := "=== SEED ===\n" + email + "\n=== DIRS ===\n"
	ending := "claude|ABSENT\nnpm-global|ABSENT\n=== END ==="
	for _, tc := range []struct {
		name, duplicate string
	}{
		{name: "conflicting wrong then expected", duplicate: ".claude|ABSENT\n.claude|DIR\n"},
		{name: "identical facts", duplicate: ".claude|DIR\n.claude|DIR\n"},
		{name: "conflicting expected then wrong", duplicate: ".claude|DIR\n.claude|ABSENT\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stdout := base + tc.duplicate + ending
			probe, err := parseACLoginSeedProbe(stdout)
			if err == nil {
				if verdictErr := acLoginSeedFailures(email, probe, email, nil); verdictErr == nil {
					t.Fatalf("duplicate facts were parsed into a false HOLDS verdict: %q", tc.duplicate)
				}
				t.Fatalf("duplicate facts should be malformed, but the verdict only rejected the overwritten value: %q", tc.duplicate)
			}
		})
	}
}

func TestACLoginSeedProbeParserRejectsMalformedAndMissingFacts(t *testing.T) {
	for _, tc := range []struct {
		name   string
		stdout string
	}{
		{name: "missing markers", stdout: "no probe output"},
		{name: "malformed directory fact", stdout: "=== SEED ===\nseed\n=== DIRS ===\nbad fact\n=== END ==="},
		{name: "no directory facts", stdout: "=== SEED ===\nseed\n=== DIRS ===\n=== END ==="},
		{name: "incomplete directory facts", stdout: "=== SEED ===\nseed\n=== DIRS ===\n.claude|DIR\n=== END ==="},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := parseACLoginSeedProbe(tc.stdout); err == nil {
				t.Fatal("malformed or incomplete probe was accepted as measured")
			}
		})
	}
}

func TestACLoginSeedVerdictControls(t *testing.T) {
	const email = "yolo-it-test@example.invalid"
	goodProbe, err := parseACLoginSeedProbe("=== SEED ===\n" + email + "\n=== DIRS ===\n" +
		".claude|DIR\nclaude|ABSENT\nnpm-global|ABSENT\n=== END ===")
	if err != nil {
		t.Fatal(err)
	}
	if err := acLoginSeedFailures(email, goodProbe, email, nil); err != nil {
		t.Fatalf("known-good observation rejected: %v", err)
	}

	t.Run("missing email", func(t *testing.T) {
		missingEmail, err := parseACLoginSeedProbe("=== SEED ===\nseed without the email\n=== DIRS ===\n" +
			".claude|DIR\nclaude|ABSENT\nnpm-global|ABSENT\n=== END ===")
		if err != nil {
			t.Fatal(err)
		}
		if err := acLoginSeedFailures(email, missingEmail, email, nil); err == nil ||
			!strings.Contains(err.Error(), "jail's ~/.claude.json") {
			t.Fatalf("missing jail email did not fail specifically: %v", err)
		}
	})
	for _, tc := range []struct {
		dir, got, want string
	}{
		{dir: ".claude", got: "ABSENT", want: "DIR"},
		{dir: "claude", got: "DIR", want: "ABSENT"},
		{dir: "npm-global", got: "DIR", want: "ABSENT"},
	} {
		t.Run("wrong "+tc.dir, func(t *testing.T) {
			probe := acLoginSeedProbe{jailSeed: email, dirs: map[string]string{
				".claude": "DIR", "claude": "ABSENT", "npm-global": "ABSENT",
			}}
			probe.dirs[tc.dir] = tc.got
			if err := acLoginSeedFailures(email, probe, email, nil); err == nil ||
				!strings.Contains(err.Error(), fmt.Sprintf("~/%s is %s, want %s", tc.dir, tc.got, tc.want)) {
				t.Fatalf("wrong directory fact did not fail specifically: %v", err)
			}
		})
	}
	t.Run("host-copy read error", func(t *testing.T) {
		readErr := errors.New("fixture read denied")
		if err := acLoginSeedFailures(email, goodProbe, "", readErr); err == nil ||
			!strings.Contains(err.Error(), "fixture read denied") {
			t.Fatalf("host-copy read error was silently accepted: %v", err)
		}
	})
}

// Pin the native boot experiment's assertion boundary. The pure verdict tests above run on
// every host; this AST check makes them useless as a substitute if the real boot caller stops
// checking that verdict or stops failing the test on a bad answer.
func TestACLoginSeedBootCallerKeepsTheHardAssertion(t *testing.T) {
	_, here, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate the integration test source")
	}
	filename := filepath.Join(filepath.Dir(here), "applecontainerhome_test.go")
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filename, nil, 0)
	if err != nil {
		t.Fatalf("parse native boot test: %v", err)
	}
	var boot *ast.FuncDecl
	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.Name == "TestAppleContainerFreshWorkspaceBootsWithTheLoginSeed" {
			boot = fn
			break
		}
	}
	if boot == nil {
		t.Fatal("native login-seed boot test is missing")
	}
	assertionBoundary := false
	ast.Inspect(boot.Body, func(node ast.Node) bool {
		if stmt, ok := node.(*ast.IfStmt); ok {
			if !astHasCall(stmt.Init, "acLoginSeedFailures") || !isErrNotNil(stmt.Cond, "err") {
				return true
			}
			ast.Inspect(stmt.Body, func(bodyNode ast.Node) bool {
				if call, ok := bodyNode.(*ast.CallExpr); ok && isTestingFatalf(call) {
					assertionBoundary = true
				}
				return true
			})
		}
		return true
	})
	if !assertionBoundary {
		t.Fatal("native login-seed boot test must fail when acLoginSeedFailures returns an error")
	}
}

func astHasCall(node ast.Node, name string) bool {
	if node == nil {
		return false
	}
	found := false
	ast.Inspect(node, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		if ident, ok := call.Fun.(*ast.Ident); ok && ident.Name == name {
			found = true
		}
		return true
	})
	return found
}

func isErrNotNil(expr ast.Expr, name string) bool {
	binary, ok := expr.(*ast.BinaryExpr)
	if !ok || binary.Op != token.NEQ {
		return false
	}
	left, leftOK := binary.X.(*ast.Ident)
	right, rightOK := binary.Y.(*ast.Ident)
	return leftOK && left.Name == name && rightOK && right.Name == "nil"
}

func isTestingFatalf(call *ast.CallExpr) bool {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "Fatalf" {
		return false
	}
	ident, ok := selector.X.(*ast.Ident)
	return ok && ident.Name == "t"
}
