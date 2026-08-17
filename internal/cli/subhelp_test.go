package cli

// subhelp_test.go is the enforcement half of docs/design/self-documenting-cli.md
// item 1: every command answers `--help` on demand, to stdout, exit 0, with no
// side effect.
//
// It is written to be EXHAUSTIVE BY CONSTRUCTION: it walks dispatch.go's
// `registry` — the single source of truth for the CLI surface — rather than a
// list of command names someone must remember to extend. A tenth command added
// to the registry with no help fails this test the moment it is registered,
// which is the only way a rule like this stays true.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// helpProbeDeadline bounds a single `<sub> --help` dispatch.
//
// It exists because of what a REGRESSION here would otherwise do to `just
// test-fast`: if a handler stops answering help, this test invokes it for real,
// and "for real" means `check` runs a nix image build and `prune` walks the
// disk. The deadline turns that into a fast, legible failure instead of a unit
// suite that appears to hang. The handler's goroutine is left running — it dies
// with the test binary — because there is no way to cancel a handler that was
// never given a context, and a leaked goroutine on an already-failing path is
// the cheaper of the two problems.
const helpProbeDeadline = 10 * time.Second

// TestEveryRegisteredCommandAnswersHelp walks the dispatch registry and asserts
// that `yolo <sub> --help` (and `-h`) prints that command's registered usage to
// stdout, exits 0, and changes nothing on disk.
//
// The no-side-effect half is asserted, not assumed: each probe runs with the cwd
// and $HOME pointed at empty temp trees, and both trees must still be empty
// afterwards. That is exactly what `yolo init --help` (which scaffolded
// yolo-jail.jsonc + .gitignore into the cwd) and `yolo init-user-config --help`
// (which wrote ~/.config/yolo-jail/config.jsonc) would fail on.
func TestEveryRegisteredCommandAnswersHelp(t *testing.T) {
	for _, sub := range slices.Sorted(maps.Keys(registry)) {
		t.Run(sub, func(t *testing.T) {
			spec, ok := subcommandUsage[sub]
			if !ok || strings.TrimSpace(spec.text) == "" {
				t.Fatalf("%q is in the dispatch registry but has no usage text in "+
					"subcommandUsage — it cannot answer --help", sub)
			}
			for _, flag := range []string{"--help", "-h"} {
				// A fresh sandbox per probe: cwd and HOME both point at empty temp
				// trees, so any write a help request performs lands where we can see it
				// (and not in the user's real workspace or home while the suite runs).
				cwd, home := t.TempDir(), t.TempDir()
				t.Setenv("HOME", home)
				t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
				t.Chdir(cwd)

				rc, stdout, stderr := probeHelp(t, sub, flag)

				if rc != 0 {
					t.Errorf("`yolo %s %s` exit = %d, want 0 — help is a request, not an "+
						"error\nstderr: %s", sub, flag, rc, stderr)
				}
				if got, want := strings.TrimSpace(stdout), strings.TrimSpace(spec.text); got != want {
					t.Errorf("`yolo %s %s` printed something other than its registered "+
						"usage\n--- got ---\n%s\n--- want ---\n%s", sub, flag, got, want)
				}
				if strings.TrimSpace(stderr) != "" {
					t.Errorf("`yolo %s %s` wrote to stderr; help belongs on stdout:\n%s",
						sub, flag, stderr)
				}
				if strings.Contains(stdout, "\x1b[") {
					t.Errorf("`yolo %s %s` emitted ANSI off a non-TTY; usage text must be "+
						"plain and byte-stable", sub, flag)
				}
				// The side-effect assertion — the reason this test exists at all.
				assertTreeEmpty(t, "cwd", cwd)
				assertTreeEmpty(t, "$HOME", home)
			}
		})
	}
}

// TestSubcommandUsageHasNoStaleEntries is the other direction of the same sync:
// a usage text registered under a name the registry does not know is a rename
// that left help behind, and it would never be reachable.
func TestSubcommandUsageHasNoStaleEntries(t *testing.T) {
	for _, name := range slices.Sorted(maps.Keys(subcommandUsage)) {
		if _, ok := registry[name]; !ok {
			t.Errorf("subcommandUsage registers help for %q, which is not a dispatch "+
				"registry key", name)
		}
	}
}

// TestHelpRequestedStopsAtDashDash pins the two forms helpRequested must NOT
// claim: an inner command's own flags, and a value-taking flag's value.
func TestHelpRequestedStopsAtDashDash(t *testing.T) {
	cases := []struct {
		name       string
		args       []string
		valueFlags []string
		want       bool
	}{
		{"plain", []string{"prune", "--help"}, nil, true},
		{"short", []string{"ps", "-h"}, nil, true},
		{"word", []string{"loopholes", "help"}, nil, true},
		{"none", []string{"prune", "--apply"}, nil, false},
		{"after dash-dash", []string{"broker", "--", "--help"}, nil, false},
		{"flag value", []string{"init", "-m", "--help"}, []string{"--mount", "-m"}, false},
		{"flag value then help", []string{"init", "-m", "x", "--help"}, []string{"--mount", "-m"}, true},
		{"unlisted flag value", []string{"init", "-m", "--help"}, nil, true},
	}
	for _, tc := range cases {
		if got := helpRequested(tc.args, tc.valueFlags...); got != tc.want {
			t.Errorf("%s: helpRequested(%q, %q) = %v, want %v",
				tc.name, tc.args, tc.valueFlags, got, tc.want)
		}
	}
}

// TestRunHelpIsNotClaimedByTheSharedScan guards the seam between the shared scan
// and run's stricter one: `yolo -- claude --help` must reach the inner command,
// which is why `run` is not routed through answerHelp (see subhelp.go's header).
func TestRunHelpIsNotClaimedByTheSharedScan(t *testing.T) {
	if runHelpRequested([]string{"run", "--", "claude", "--help"}) {
		t.Error("`yolo -- claude --help` must pass --help to the inner command")
	}
	if runHelpRequested([]string{"run", "foo", "--help"}) {
		t.Error("`yolo run foo --help` runs `foo --help` in the jail")
	}
}

// TestUsageListsEveryParsedFlag is the content half of the standard
// (self-documenting-cli item 2: help must enumerate the command's flags), and it
// is derived rather than listed: it PARSES this package's own source, reads the
// name→handler mapping straight out of dispatch.go's `registry` composite
// literal, and collects every `--flag` string literal in each handler's body.
// Each one must appear in that command's registered usage text.
//
// So a flag added to a handler's switch is documented or the build's own tests
// fail — the drift that left `prune`'s twelve flags and `init`'s `--mount`
// undocumented for as long as they existed cannot recur silently.
//
// Its blind spot, stated so nobody reads more into a pass than is there: a
// handler that DELEGATES its parsing to another function (runApply → applyMain,
// runRun → parseRunArgs) has no flag literals in its own body and passes
// vacuously. run is covered by TestRunUsageListsEveryRunFlag, which pins the same
// property through the `runFlags` list its parser and its usage share.
func TestUsageListsEveryParsedFlag(t *testing.T) {
	handlers, funcs := parseCLISource(t)
	for _, sub := range slices.Sorted(maps.Keys(registry)) {
		fnName, ok := handlers[sub]
		if !ok {
			t.Errorf("could not find %q in the parsed `registry` literal — the source "+
				"scan is out of step with dispatch.go", sub)
			continue
		}
		fn, ok := funcs[fnName]
		if !ok {
			t.Errorf("registry maps %q to %s, which this package does not declare",
				sub, fnName)
			continue
		}
		usage := subcommandUsage[sub].text
		for _, flag := range longFlagLiterals(fn) {
			if !strings.Contains(usage, flag) {
				t.Errorf("`yolo %s` parses %s but its help never mentions it "+
					"(handler %s)", sub, flag, fnName)
			}
		}
	}
}

// parseCLISource parses the non-test sources of package cli and returns the
// registry's subcommand→handler-name mapping plus every function declaration by
// name. Reading the mapping out of the AST (rather than restating it) is what
// keeps the flag audit honest through a rename.
func parseCLISource(t *testing.T) (handlers map[string]string, funcs map[string]*ast.FuncDecl) {
	t.Helper()
	handlers = map[string]string{}
	funcs = map[string]*ast.FuncDecl{}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	fset := token.NewFileSet()
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, decl := range f.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				if d.Recv == nil {
					funcs[d.Name.Name] = d
				}
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					vs, ok := spec.(*ast.ValueSpec)
					if !ok || len(vs.Names) != 1 || vs.Names[0].Name != "registry" || len(vs.Values) != 1 {
						continue
					}
					lit, ok := vs.Values[0].(*ast.CompositeLit)
					if !ok {
						continue
					}
					for _, elt := range lit.Elts {
						kv, ok := elt.(*ast.KeyValueExpr)
						if !ok {
							continue
						}
						key, ok := kv.Key.(*ast.BasicLit)
						if !ok || key.Kind != token.STRING {
							continue
						}
						fn, ok := kv.Value.(*ast.Ident)
						if !ok {
							continue
						}
						handlers[strings.Trim(key.Value, `"`)] = fn.Name
					}
				}
			}
		}
	}
	if len(handlers) == 0 {
		t.Fatal("parsed no entries from the `registry` literal")
	}
	return handlers, funcs
}

// longFlagLiterals returns the distinct `--flag` string literals in fn's body.
//
// LONG flags only: a short flag is a one-or-two-character literal that collides
// with ordinary strings, and every long flag has a short form documented beside
// it anyway. A trailing `=` is trimmed so the `--flag=value` form checks against
// the same text as `--flag`, and the bare `--` separator is excluded by length.
func longFlagLiterals(fn *ast.FuncDecl) []string {
	seen := map[string]bool{}
	var out []string
	ast.Inspect(fn, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		v := strings.Trim(lit.Value, `"`)
		if !strings.HasPrefix(v, "--") || len(v) <= 2 {
			return true
		}
		v = strings.TrimSuffix(v, "=")
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
		return true
	})
	slices.Sort(out)
	return out
}

// probeHelp dispatches `<sub> <flag>` through dispatchNative with os.Stdout and
// os.Stderr redirected to temp files, under helpProbeDeadline.
//
// Temp FILES rather than a pipe: a handler that regressed into doing real work
// can write more than a pipe buffer holds, and a blocked writer would hang the
// probe instead of tripping the deadline.
func probeHelp(t *testing.T, sub, flag string) (rc int, stdout, stderr string) {
	t.Helper()
	dir := t.TempDir()
	outF, err := os.Create(filepath.Join(dir, "stdout"))
	if err != nil {
		t.Fatalf("create stdout capture: %v", err)
	}
	errF, err := os.Create(filepath.Join(dir, "stderr"))
	if err != nil {
		t.Fatalf("create stderr capture: %v", err)
	}
	oldOut, oldErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = outF, errF
	restore := func() { os.Stdout, os.Stderr = oldOut, oldErr }

	done := make(chan int, 1)
	go func() { done <- dispatchNative(sub, []string{sub, flag}) }()
	select {
	case rc = <-done:
		restore()
	case <-time.After(helpProbeDeadline):
		restore()
		t.Fatalf("`yolo %s %s` did not return within %s — it is doing the command's "+
			"real work instead of answering help", sub, flag, helpProbeDeadline)
	}
	_ = outF.Close()
	_ = errF.Close()
	ob, _ := os.ReadFile(filepath.Join(dir, "stdout"))
	eb, _ := os.ReadFile(filepath.Join(dir, "stderr"))
	return rc, string(ob), string(eb)
}

// assertTreeEmpty fails if anything exists under root. label names the tree in
// the failure so the message says WHICH interrogation-time write happened.
func assertTreeEmpty(t *testing.T, label, root string) {
	t.Helper()
	var found []string
	err := filepath.WalkDir(root, func(path string, _ os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path != root {
			rel, _ := filepath.Rel(root, path)
			found = append(found, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	if len(found) > 0 {
		t.Errorf("--help wrote into %s: %v — interrogating a command must not change "+
			"the machine", label, found)
	}
}
