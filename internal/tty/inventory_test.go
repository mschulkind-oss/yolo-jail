package tty

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// colorEmitters is EVERY non-test Go file in cmd/ and internal/ whose string literals hold an
// ANSI escape (a CSI, `ESC [`), each with the decision that governs it. It is the inventory
// behind docs/plans/cli-visual-polish.md's invariant: a new entry is a new place yolo can add
// color, and adding one here is the moment to route it through Color (or NoColor, for text
// another process prints) — the audit that found `prune` and `ttyproxy` still carrying
// private copies of the terminal probe is the reason the list is enforced rather than kept.
var colorEmitters = map[string]string{
	"internal/richtext/richtext.go": "the renderer; every caller passes a color resolved " +
		"through Color (colorForWriter, the engines' gates)",
	"internal/cli/check/reporter.go": "check's private palette (background badges); " +
		"newReporter's color is check's Color gate",
	"internal/cli/markup.go": "init's renderMarkup; its color is colorForWriter's",
	"internal/cli/run/command.go": "the generated container script's lines; scriptColor, " +
		"NoColor over the launch environment",
	"internal/provision/provision.go": "the provisioning stage's failure line; the caller's " +
		"NoColor decision (Script's color parameter)",
	"internal/entrypoint/boot.go": "the exec-into hand-over line; NoColor over the jail's " +
		"environment",
	"internal/entrypoint/shell.go": "the jail's .bashrc prompt; gated in bash on NO_COLOR " +
		"at shell start",
	"internal/ttyproxy/ttyproxy.go": "termReset: terminal RESTORATION after a child, which " +
		"clears attributes rather than adding color, so no gate applies",
}

// TestEveryColorEmitterIsInventoried fails when a file starts emitting ANSI without being
// listed above (so without anyone deciding which gate governs it), and when a listed file
// stops, so the inventory cannot outlive what it describes.
func TestEveryColorEmitterIsInventoried(t *testing.T) {
	root := filepath.Join("..", "..")
	found := map[string]bool{}
	for _, dir := range []string{"cmd", "internal"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			if emitsANSI(t, path) {
				rel, _ := filepath.Rel(root, path)
				found[filepath.ToSlash(rel)] = true
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	var unlisted, stale []string
	for f := range found {
		if _, ok := colorEmitters[f]; !ok {
			unlisted = append(unlisted, f)
		}
	}
	for f := range colorEmitters {
		if !found[f] {
			stale = append(stale, f)
		}
	}
	sort.Strings(unlisted)
	sort.Strings(stale)
	if len(unlisted) > 0 {
		t.Errorf("these files emit ANSI escapes and are not in colorEmitters: %v. Decide which "+
			"gate governs each — tty.Color for a stream this process writes, tty.NoColor for "+
			"text another process prints — and list it with that decision.", unlisted)
	}
	if len(stale) > 0 {
		t.Errorf("colorEmitters lists files that no longer emit ANSI: %v — remove them.", stale)
	}
}

// charDeviceReaders are the files allowed to ask os.ModeCharDevice a question, with why it
// is not a terminal probe.
var charDeviceReaders = map[string]string{
	"internal/footer/footer.go": "ReadJSON declines to read a character-device stdin (a " +
		"terminal or /dev/null — neither carries the agent's JSON); it decides no color",
}

// TestNoPrivateTerminalProbe keeps the terminal probe in this package: outside it, no
// non-test file may call IoctlGetTermios only for its error (the TCGETS/TIOCGETA probe
// shape — ttyproxy reads termios for their VALUE, which is not a probe), nor test
// os.ModeCharDevice except where charDeviceReaders says why. A private copy is how a color
// gate ends up consulting a probe the next reader cannot find — prune and ttyproxy each
// carried one after cli-color-audit.md recorded the probe as unified.
func TestNoPrivateTerminalProbe(t *testing.T) {
	root := filepath.Join("..", "..")
	var probes, charDev []string
	for _, dir := range []string{"cmd", "internal"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			rel, _ := filepath.Rel(root, path)
			rel = filepath.ToSlash(rel)
			if strings.HasPrefix(rel, "internal/tty/") {
				return nil
			}
			f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
			if err != nil {
				t.Fatalf("parse %s: %v", path, err)
			}
			ast.Inspect(f, func(n ast.Node) bool {
				switch n := n.(type) {
				case *ast.AssignStmt:
					if len(n.Lhs) == 2 && len(n.Rhs) == 1 {
						if id, ok := n.Lhs[0].(*ast.Ident); ok && id.Name == "_" && callsNamed(n.Rhs[0], "IoctlGetTermios") {
							probes = append(probes, rel)
						}
					}
				case *ast.SelectorExpr:
					if n.Sel.Name == "ModeCharDevice" {
						if _, ok := charDeviceReaders[rel]; !ok {
							charDev = append(charDev, rel)
						}
					}
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(probes) > 0 {
		t.Errorf("private terminal probes outside internal/tty: %v — call tty.IsTerminal / "+
			"tty.IsTerminalFile instead", probes)
	}
	if len(charDev) > 0 {
		t.Errorf("os.ModeCharDevice tested outside internal/tty: %v — it false-positives on "+
			"/dev/null and the container's -t, so it is not a terminal probe; use tty.IsTerminal, "+
			"or list the file in charDeviceReaders with why it asks something else", charDev)
	}
}

func callsNamed(e ast.Expr, name string) bool {
	call, ok := e.(*ast.CallExpr)
	if !ok {
		return false
	}
	switch fn := call.Fun.(type) {
	case *ast.SelectorExpr:
		return fn.Sel.Name == name
	case *ast.Ident:
		return fn.Name == name
	}
	return false
}

// emitsANSI reports whether any string literal in the file holds an escape sequence: the
// ESC byte itself followed by `[` (Go's "\x1b[", "\033[", "\u001b["), or the TEXT a shell
// turns into one (`\033[`, `\e[`, `\x1b[` inside a raw string handed to printf or echo -e).
func emitsANSI(t *testing.T, path string) bool {
	t.Helper()
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	f, err := parser.ParseFile(token.NewFileSet(), path, src, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	hit := false
	ast.Inspect(f, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING || hit {
			return !hit
		}
		v, err := strconv.Unquote(lit.Value)
		if err != nil {
			return true
		}
		for _, seq := range []string{"\x1b[", `\033[`, `\e[`, `\x1b[`} {
			if strings.Contains(v, seq) {
				hit = true
			}
		}
		return !hit
	})
	return hit
}
