package config

// testmainrelease_test.go pins that this package's test binary gives back the pack trees it
// staged. A test that resolves a filtered pack stages it into the process pack tree
// (packload.ProcessPackDir), which nothing releases at a test binary's exit unless TestMain
// does. Run outside cmd/go's $WORK, as scripts/completion-census.py runs each test, that tree
// is a yolo-embedded-lease-<n> directory left in TMPDIR.

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const releaseHelperEnv = "YOLO_TEST_CONFIG_RELEASE_HELPER"

// TestATestBinaryOutsideWorkLeavesNoProcessPackTree runs a copy of this test binary, outside
// $WORK, on one test that stages a pack, and requires TMPDIR to hold no leased pack tree after
// it exits. MUTATION: delete the packload.ReleaseEmbedded call from TestMain and this fails.
func TestATestBinaryOutsideWorkLeavesNoProcessPackTree(t *testing.T) {
	if os.Getenv(releaseHelperEnv) != "" {
		t.Skip("the helper's own run")
	}
	scratch, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(scratch, "bin", "config.test")
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	in, err := os.Open(exe)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	out, err := os.OpenFile(bin, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
	tmp := filepath.Join(scratch, "tmp")
	if err := os.Mkdir(tmp, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "-test.short", "-test.count=1", "-test.run=^TestResolvePackStagesALocalClonesWorkingTree$")
	cmd.Env = append(os.Environ(), "TMPDIR="+tmp, releaseHelperEnv+"=1")
	if o, err := cmd.CombinedOutput(); err != nil || !strings.Contains(string(o), "PASS") {
		t.Fatalf("helper run: %v\n%s", err, o)
	}
	es, err := os.ReadDir(tmp)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range es {
		if strings.HasPrefix(e.Name(), "yolo-embedded-lease-") {
			t.Errorf("a test binary that exited normally left %s in TMPDIR", e.Name())
		}
	}
}
