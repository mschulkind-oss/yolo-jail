package main

import (
	"strings"
	"testing"
)

// `uvx yolo-jail …` runs the console script named after the package, so the
// wheel must provide one; `yolo` stays the first entry. Both name the wrapper
// that execs bin/yolo, so the program sees the same argv either way.
func TestEntryPointsProvideThePackageNamedScript(t *testing.T) {
	got := generateEntryPoints()
	want := "[console_scripts]\n" +
		"yolo = yolo_jail:main\n" +
		"yolo-jail = yolo_jail:main\n"
	if got != want {
		t.Fatalf("entry_points.txt:\n%s\nwant:\n%s", got, want)
	}
	if !strings.Contains(generateInitPy("0.0.0"), "def main():\n    _run(\"yolo\")") {
		t.Fatalf("main() no longer runs bin/yolo")
	}
}
