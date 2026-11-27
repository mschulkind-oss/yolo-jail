package unifiedhome

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

const modulePath = "github.com/mschulkind-oss/yolo-jail/"
const unifiedhomePkg = modulePath + "internal/unifiedhome"

// importsOf is the import paths of the given Go files, parsed without type checking.
func importsOf(t *testing.T, files []string) []string {
	t.Helper()
	seen := map[string]bool{}
	for _, f := range files {
		af, err := parser.ParseFile(token.NewFileSet(), f, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", f, err)
		}
		for _, imp := range af.Imports {
			p, _ := strconv.Unquote(imp.Path.Value)
			seen[p] = true
		}
	}
	var out []string
	for p := range seen {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// sourceFiles is the non-test Go files directly in dir.
func sourceFiles(t *testing.T, dir string) []string {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	var out []string
	for _, e := range ents {
		if n := e.Name(); !e.IsDir() && strings.HasSuffix(n, ".go") && !strings.HasSuffix(n, "_test.go") {
			out = append(out, filepath.Join(dir, n))
		}
	}
	if len(out) == 0 {
		t.Fatalf("%s holds no Go source: the guard would pass over nothing", dir)
	}
	return out
}

// TestHostRendererDoesNotImportTheComposition (unified-workspace-home.md §10, UHP U7): host and jail
// never share a home, so the code that renders a HOST (internal/render, which every notch's render
// goes through, and the files of `yolo host apply`) must not reach the composition that links a
// workspace's sessions into a unified home. internal/render is followed through every package it
// imports inside this module; the host-apply files are checked at their own imports, because
// internal/cli as a package also holds the launch verbs that rightly reach it.
func TestHostRendererDoesNotImportTheComposition(t *testing.T) {
	root := filepath.Join("..", "..")
	seen := map[string]bool{}
	var walk func(chain []string, pkg string)
	walk = func(chain []string, pkg string) {
		if seen[pkg] {
			return
		}
		seen[pkg] = true
		chain = append(chain, pkg)
		for _, imp := range importsOf(t, sourceFiles(t, filepath.Join(root, strings.TrimPrefix(pkg, modulePath)))) {
			if imp == unifiedhomePkg {
				t.Errorf("internal/render reaches internal/unifiedhome: %s", strings.Join(append(chain, imp), " -> "))
			}
			if strings.HasPrefix(imp, modulePath) {
				walk(chain, imp)
			}
		}
	}
	walk(nil, modulePath+"internal/render")

	var apply []string
	for _, f := range sourceFiles(t, filepath.Join(root, "internal", "cli")) {
		if base := filepath.Base(f); base == "apply.go" || strings.HasPrefix(base, "applyhost") {
			apply = append(apply, f)
		}
	}
	if len(apply) < 2 {
		t.Fatalf("found %d host-apply files under internal/cli; the guard would pass over nothing", len(apply))
	}
	for _, f := range apply {
		for _, imp := range importsOf(t, []string{f}) {
			if imp == unifiedhomePkg {
				t.Errorf("%s imports internal/unifiedhome: the host render must not compose a unified home", f)
			}
		}
	}
}
