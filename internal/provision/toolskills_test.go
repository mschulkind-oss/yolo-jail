package provision

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fakeYolo is a `yolo` on PATH that records its argv, one line per call, and exits rc.
func fakeYolo(t *testing.T, rc string) (dir, calls string) {
	t.Helper()
	dir = t.TempDir()
	calls = filepath.Join(t.TempDir(), "calls")
	body := "#!/bin/sh\necho \"$*\" >> " + calls + "\nexit " + rc + "\n"
	if err := os.WriteFile(filepath.Join(dir, "yolo"), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir, calls
}

func runStep(t *testing.T, step, allowlist, rc string) (status int, calls string) {
	t.Helper()
	dir, log := fakeYolo(t, rc)
	cmd := exec.Command("sh", "-c", step)
	cmd.Env = []string{"PATH=" + dir + ":/bin:/usr/bin"}
	if allowlist != "<unset>" {
		cmd.Env = append(cmd.Env, "YOLO_MISE_SKILL_TOOLS="+allowlist)
	}
	err := cmd.Run()
	if ee, ok := err.(*exec.ExitError); ok {
		status = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(log)
	return status, string(b)
}

// An empty allowlist runs no process at all (design §6.3), and a nonempty one calls the
// subcommand with the workspace and the lock; the step's status is always 0, so a stage joined
// with && is never stopped by it.
func TestStepToolSkillsIsInertWithoutAnAllowlistAndNeverFails(t *testing.T) {
	step := StepToolSkills("/workspace", "/run/yolo/main/tool-skills.lock")
	for _, empty := range []string{"<unset>", "", "[]"} {
		if rc, calls := runStep(t, step, empty, "0"); rc != 0 || calls != "" {
			t.Errorf("allowlist %q: rc %d, ran %q; want inert", empty, rc, calls)
		}
	}
	rc, calls := runStep(t, step, `["packslip:x"]`, "0")
	if rc != 0 || calls != "internal tool-skills /workspace /run/yolo/main/tool-skills.lock\n" {
		t.Errorf("rc %d calls %q", rc, calls)
	}
	if rc, calls := runStep(t, step, `["packslip:x"]`, "9"); rc != 0 || calls == "" {
		t.Errorf("a failing subcommand gated the stage: rc %d calls %q", rc, calls)
	}
	if strings.Contains(step, "'") {
		t.Errorf("the container wraps the stage in sh -c '…': no single quote allowed in %q", step)
	}
}

// The lock argument is optional: macos-user has no /run/yolo/main, so the snapshot's own default
// lock is used and the word is simply absent.
func TestStepToolSkillsWithoutALockWordNamesOnlyTheWorkspace(t *testing.T) {
	step := StepToolSkills(`"$PWD"`, "")
	dir := t.TempDir()
	ydir, log := fakeYolo(t, "0")
	cmd := exec.Command("sh", "-c", step)
	cmd.Dir = dir
	cmd.Env = []string{"PATH=" + ydir + ":/bin:/usr/bin", `YOLO_MISE_SKILL_TOOLS=["a"]`}
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	real, _ := filepath.EvalSymlinks(dir)
	b, _ := os.ReadFile(log)
	if got := strings.TrimSpace(string(b)); got != "internal tool-skills "+real && got != "internal tool-skills "+dir {
		t.Errorf("calls %q", got)
	}
}
