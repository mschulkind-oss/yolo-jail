package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The guardrails pack's mv and rm guards reach the program `yolo host --` starts, protecting the
// directory the launch was started in: delete the workspace argument hostBlockedChildPath passes
// and no guard is written.
func TestHostLaunchGuardsTheLaunchDirectorysDotYolo(t *testing.T) {
	blockerHostFixture(t, `{"packs": ["guardrails"]}`, "rg", "fd", "mv", "rm")
	ws, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	got, _ := launchCapturing(t)
	blockDir := filepath.SplitList(envValue(got.env, "PATH"))[0]
	for _, tool := range []string{"mv", "rm"} {
		body, err := os.ReadFile(filepath.Join(blockDir, tool))
		if err != nil {
			t.Fatalf("no %s guard in %s: %v", tool, blockDir, err)
		}
		if !strings.Contains(string(body), "_yg_ws="+ws+"\n") {
			t.Errorf("the %s guard does not protect the launch directory %s:\n%s", tool, ws, body)
		}
	}
	if err := os.MkdirAll(filepath.Join(ws, ".yolo", "home"), 0o755); err != nil {
		t.Fatal(err)
	}
	rc, out := runWithEnv(t, got.env, ws, "rm -rf .yolo/home")
	if rc != 1 || !strings.Contains(out, "yolo --fresh-home") {
		t.Errorf("rm -rf .yolo/home: rc=%d, want the guard's refusal:\n%s", rc, out)
	}
}
