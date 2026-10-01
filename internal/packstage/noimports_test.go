package packstage

// noimports_test.go pins loopholeowners.go's "No internal imports" rule for the whole package:
// every path crosses as a parameter, so the launch path, `yolo prune` and the tests can each
// point it somewhere different, and nothing here can grow an opinion about where storage lives.
// The rule was stated in prose alone, and a constant imported for one suffix broke it.

import (
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"strings"
	"testing"
)

func TestPackstageTakesNoInternalImports(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	const module = "github.com/mschulkind-oss/yolo-jail/"
	parsed := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		parsed++
		for _, imp := range f.Imports {
			path, _ := strconv.Unquote(imp.Path.Value)
			if strings.HasPrefix(path, module) {
				t.Errorf("%s imports %s; packstage takes its paths and names as data "+
					"(loopholeowners.go, \"No internal imports\") — mirror the value and pin it "+
					"equal in a test instead", name, path)
			}
		}
	}
	if parsed == 0 {
		t.Fatal("no package source files found; the test is not reading the package")
	}
}
