package flakebundle

// install_pin_test.go is the CROSS-LANGUAGE call-site pin for this package.
//
// Everything flakebundle guarantees is worthless if the one caller stops calling
// it, and that caller is a `just` recipe: no compiler, no import graph, nothing
// that fails when the Go side changes. The behavioural tests in this package all
// pass against a Justfile that went back to staging over the live path — which is
// precisely the shape AGENTS.md names ("a test that pins the CALLEE while the
// CALL SITE is unpinned is not a test").

import (
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"
)

func justfileSource(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := goruntime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller gave no source path — cannot locate the repo root")
	}
	root := filepath.Join(filepath.Dir(thisFile), "..", "..")
	body, err := os.ReadFile(filepath.Join(root, "Justfile"))
	if err != nil {
		t.Fatalf("read Justfile at the resolved repo root %s: %v", root, err)
	}
	return string(body)
}

// TestInstallStagesIntoAGenerationAndActivatesIt pins both halves of the install
// contract, because either one alone reintroduces the bug: staging without
// activating leaves the stable path on the OLD generation (the install silently
// does nothing), and activating without staging into a fresh dir means something
// wrote over a directory a running jail is mounting.
func TestInstallStagesIntoAGenerationAndActivatesIt(t *testing.T) {
	body := justfileSource(t)

	for _, want := range []string{"bundle-dir --stage", "bundle-dir --activate"} {
		if !strings.Contains(body, want) {
			t.Errorf("the `just install` recipe no longer runs `yolo internal %s`.\n"+
				"An install that writes over the live bundle path deletes pid1 out from "+
				"under every RUNNING jail — a bind mount pins an inode, not a path, so the "+
				"container is left mounted on an emptied directory and cannot be repaired. "+
				"That is the failure internal/flakebundle exists to prevent; if the staging "+
				"moved, move this pin with it.", want)
		}
	}
	// The old spelling, which the staging script `rm -rf`s. Its return would be
	// the regression, whatever else the recipe gained.
	if strings.Contains(body, `stage-source-bundle.sh "$BUNDLE_DIR"`) {
		t.Error("`just install` stages into the stable bundle path again. " +
			"scripts/stage-source-bundle.sh leads with `rm -rf $DEST`, so that path is the " +
			"one directory it must never be handed.")
	}
}
