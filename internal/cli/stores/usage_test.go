package stores

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestUsageListsEveryFlagThisPackageParses is the content half of the
// self-documenting-CLI standard, carried across a package boundary.
//
// internal/cli has TestUsageListsEveryParsedFlag, which reads a HANDLER's body
// and one hop of same-package delegation — and `yolo stores`'s parse lives in
// ANOTHER package, so that scan passes VACUOUSLY for this command. Without this
// test a flag added to ParseArgs would be documented nowhere and the suite would
// stay green, which is the drift that left prune's twelve flags undocumented for
// as long as they existed.
//
// It reads the flag literals out of ParseArgs itself rather than out of the whole
// package: this file's first cut scanned every source file and demanded that
// Usage document podman's --format.
func TestUsageListsEveryFlagThisPackageParses(t *testing.T) {
	var parseArgs *ast.FuncDecl
	for _, f := range parsePackageFiles(t, ".") {
		for _, decl := range f.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.Name == "ParseArgs" {
				parseArgs = fn
			}
		}
	}
	if parseArgs == nil {
		t.Fatal("no ParseArgs in this package — the scan has stopped testing anything")
	}

	var flags []string
	ast.Inspect(parseArgs, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		if v, err := strconv.Unquote(lit.Value); err == nil && strings.HasPrefix(v, "--") {
			flags = append(flags, v)
		}
		return true
	})
	if len(flags) == 0 {
		t.Fatal("ParseArgs holds no flag literals — the scan has stopped testing anything")
	}
	for _, flag := range flags {
		if !strings.Contains(Usage, flag) {
			t.Errorf("ParseArgs parses %s but Usage never mentions it", flag)
		}
	}
}

// TestUsageStatesTheLedgerContract. The one exception to "this command never
// mutates a store" is the ledger, and a user has to be able to find that out
// from the command itself: what is written, where, how it is bounded, and how to
// turn it off.
func TestUsageStatesTheLedgerContract(t *testing.T) {
	for _, want := range []string{"--no-record", "30", ".samples", "read-only", "READ-ONLY"} {
		if strings.Contains(strings.ToLower(Usage), strings.ToLower(want)) {
			continue
		}
		t.Errorf("Usage never mentions %q; the ledger exception has to be discoverable from --help", want)
	}
}

// TestUsageHasNoBacktick guards the trap the previous commit on this surface
// tripped over: Usage is a raw string literal, and a backtick inside one ends it.
func TestUsageHasNoBacktick(t *testing.T) {
	if strings.Contains(Usage, "`") {
		t.Error("Usage contains a backtick — inside a raw string literal that ends the literal")
	}
}

// parsePackageFiles parses every non-test .go file in dir. It is ParseFile per
// file rather than ParseDir because ParseDir is deprecated (staticcheck SA1019),
// and it is the same shape internal/cli's own source scans use.
func parsePackageFiles(t *testing.T, dir string) []*ast.File {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	fset := token.NewFileSet()
	var out []*ast.File
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		out = append(out, f)
	}
	return out
}
