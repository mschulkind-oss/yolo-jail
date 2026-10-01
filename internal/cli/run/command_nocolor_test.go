package run

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// command_nocolor_test.go pins the one color decision the generated container script has:
// scriptColor, the NO_COLOR half of the gate over the launch environment, made by
// provisionStage and sessionCmd for every line the script prints.

// TestTheContainerScriptHonorsNoColor RUNS what a launch builds for its first session —
// provisionStage and sessionCmd, the launch's own entries — against a failing bootstrap, so all
// three of its lines print: the provisioning line, the red provisioning failure, and the
// "⚡ Executing" hand-over. With NO_COLOR unset they are colored (the control); with
// NO_COLOR=1 in the launch environment none carries an escape, and the words and the
// outcome are unchanged.
func TestTheContainerScriptHonorsNoColor(t *testing.T) {
	for _, tc := range []struct {
		noColor  string
		wantANSI bool
	}{{"", true}, {"1", false}} {
		f := newStageFixture(t, 1)
		o := goldenOptions("/ws", t.TempDir())
		o.Getenv = func(k string) string {
			if k == "NO_COLOR" {
				return tc.noColor
			}
			return ""
		}
		rc, stdout, stderr := f.runFirstSession(t, o.provisionStage, o.sessionCmd(targetCmdForTest))
		if rc != 0 || !strings.Contains(stdout, targetMarker) {
			t.Fatalf("NO_COLOR=%q: the launch did not reach its target (rc %d):\n%s\n%s",
				tc.noColor, rc, stdout, stderr)
		}
		for _, words := range []string{"📦 Provisioning tools...", "✗ Provisioning failed (exit 1)",
			"⚡ Executing: " + targetCmdForTest} {
			if !strings.Contains(stderr, words) {
				t.Errorf("NO_COLOR=%q: stderr lost %q:\n%q", tc.noColor, words, stderr)
			}
		}
		if hasANSI := strings.Contains(stderr, "\x1b["); hasANSI != tc.wantANSI {
			t.Errorf("NO_COLOR=%q: the script's lines carry ANSI = %v, want %v:\n%q",
				tc.noColor, hasANSI, tc.wantANSI, stderr)
		}
	}
}

// TestFinalInternalCmdIsTheGoldenWhenColorIsOn: with NO_COLOR unset, the launch's first-session
// bytes are byte-for-byte the golden — honoring NO_COLOR moved no frozen byte for anyone who
// has not set it — and with it set, the plain bytes differ from the golden only by escapes.
func TestFinalInternalCmdIsTheGoldenWhenColorIsOn(t *testing.T) {
	want, err := os.ReadFile(filepath.Join("testdata", "final_cmd_bash.txt"))
	if err != nil {
		t.Fatal(err)
	}
	o := goldenOptions("/ws", t.TempDir())
	firstSession := func() string { return o.provisionStage() + "; " + o.sessionCmd("bash") }
	if got := firstSession(); got != string(want) {
		t.Errorf("NO_COLOR unset: the launch's command is not the golden\n got: %q\nwant: %q", got, want)
	}
	o.Getenv = func(k string) string {
		if k == "NO_COLOR" {
			return "1"
		}
		return ""
	}
	plain := firstSession()
	if strings.Contains(plain, `\033`) {
		t.Errorf("NO_COLOR=1: the launch's command still carries an escape:\n%s", plain)
	}
	stripped := string(want)
	for _, esc := range []string{`\033[1;36m`, `\033[1;31m`, `\033[2m`, `\033[0m`} {
		stripped = strings.ReplaceAll(stripped, esc, "")
	}
	if plain != stripped {
		t.Errorf("NO_COLOR=1 changed more than the escapes\n got: %q\nwant: %q", plain, stripped)
	}
}

// TestTheLaunchBuildsItsCommandsThroughTheColorDecision is the call-site pin. The launch
// cannot be driven through its container here, so this reads the package: the ONLY
// production caller of buildProvisionStage is provisionStage and of buildSessionCmd is
// sessionCmd (which make the color decision), and something in the package calls each of
// those. A launch that went back to calling a builder with its own color argument fails the
// first half; one that stopped handing the stage or the session command over fails the
// second.
func TestTheLaunchBuildsItsCommandsThroughTheColorDecision(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	builders := map[string][]string{}
	used := map[string]bool{}
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", file, err)
		}
		for _, decl := range f.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			ast.Inspect(fd.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				switch fn := call.Fun.(type) {
				case *ast.Ident:
					if fn.Name == "buildProvisionStage" || fn.Name == "buildSessionCmd" {
						builders[fn.Name] = append(builders[fn.Name], file+":"+fd.Name.Name)
					}
				case *ast.SelectorExpr:
					if fn.Sel.Name == "provisionStage" || fn.Sel.Name == "sessionCmd" {
						used[fn.Sel.Name] = true
					}
				}
				return true
			})
		}
	}
	for builder, wrapper := range map[string]string{"buildProvisionStage": "provisionStage",
		"buildSessionCmd": "sessionCmd"} {
		if got := builders[builder]; len(got) != 1 || !strings.HasSuffix(got[0], ":"+wrapper) {
			t.Errorf("%s's production callers are %v, want only %s: any other caller decides the "+
				"script's color itself, and can decide it without NO_COLOR", builder, got, wrapper)
		}
		if !used[wrapper] {
			t.Errorf("nothing in the package calls %s, so no launch hands it over through the "+
				"color decision", wrapper)
		}
	}
}
