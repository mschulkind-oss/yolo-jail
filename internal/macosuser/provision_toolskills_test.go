package macosuser

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// THE MACOS-USER STAGE TAKES THE TOOL-SKILLS STEP, after `mise install` and before the bootstrap
// (design §7: the confined provisioning stage, never the privileged bootstrap). The stage body
// runs in a workspace-like cwd with a fake mise and yolo recording into one file. Deleting the
// step from ProvisionSetup fails the order test.
func runToolSkillsStage(t *testing.T, allowlist, yoloRC string) (rc int, order string) {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("no bash on PATH")
	}
	dir := t.TempDir()
	ws := filepath.Join(dir, "ws")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(dir, "order")
	script := filepath.Join(dir, "yolo-bootstrap.sh")
	for path, body := range map[string]string{
		filepath.Join(dir, "mise"): "#!/bin/sh\necho \"mise $*\" >> " + log + "\nexit 0\n",
		filepath.Join(dir, "yolo"): "#!/bin/sh\necho \"yolo $*\" >> " + log + "\nexit " + yoloRC + "\n",
		script:                     "#!/bin/sh\necho bootstrap >> " + log + "\nexit 0\n",
	} {
		if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command("bash", "-c", "("+ProvisionSetup(script)+")")
	cmd.Dir = ws
	cmd.Env = []string{"PATH=" + dir + ":/bin:/usr/bin", "YOLO_MISE_SKILL_TOOLS=" + allowlist}
	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			rc = ee.ExitCode()
		} else {
			t.Fatal(err)
		}
	}
	b, _ := os.ReadFile(log)
	return rc, string(b)
}

func TestTheStageRunsToolSkillsAfterMiseInstallAndBeforeTheBootstrap(t *testing.T) {
	rc, order := runToolSkillsStage(t, `["packslip:x"]`, "0")
	lines := strings.Split(strings.TrimSpace(order), "\n")
	pos := map[string]int{}
	for i, l := range lines {
		switch {
		case strings.HasPrefix(l, "mise install"):
			pos["install"] = i + 1
		case strings.HasPrefix(l, "yolo internal tool-skills "):
			pos["skills"] = i + 1
		case l == "bootstrap":
			pos["boot"] = i + 1
		}
	}
	if rc != 0 || pos["install"] == 0 || pos["skills"] <= pos["install"] || pos["boot"] <= pos["skills"] {
		t.Fatalf("rc %d, order:\n%s", rc, order)
	}
}

func TestAFailingToolSkillsStepNeverGatesThisBackendsBootstrap(t *testing.T) {
	rc, order := runToolSkillsStage(t, `["packslip:x"]`, "9")
	if rc != 0 || !strings.Contains(order, "tool-skills") || !strings.Contains(order, "bootstrap") {
		t.Fatalf("rc %d, order:\n%s", rc, order)
	}
}

func TestAnEmptyAllowlistRunsNoToolSkillsProcessOnThisBackend(t *testing.T) {
	for _, v := range []string{"", "[]"} {
		rc, order := runToolSkillsStage(t, v, "0")
		if rc != 0 || strings.Contains(order, "yolo") {
			t.Fatalf("allowlist %q: rc %d, order:\n%s", v, rc, order)
		}
	}
}
