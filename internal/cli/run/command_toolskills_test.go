package run

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/testutil"
)

// THE FIRST SESSION'S STAGE RUNS THE TOOL-SKILLS STEP AFTER `mise install` (design §4 step 2,
// slice 3). The stage bytes are run as the entrypoint runs them, with a fake `mise` and a `yolo`
// that records the tool-skills call and hands everything else (the log writer) to the real one.
// Deleting the step from setupScript fails the order test; deleting `|| true` or the step's
// guard fails the other two.
func toolSkillsStage(t *testing.T, allowlist string, toolSkillsRC string) (rc int, calls string, f stageFixture) {
	t.Helper()
	f = newStageFixture(t, 0)
	order := filepath.Join(f.home, "order")
	realYolo := filepath.Join(testutil.YoloBinaryDir(t), "yolo")
	files := map[string]string{
		filepath.Join(f.fakeBin, "mise"): "#!/bin/sh\nprintf 'mise %s\\n' \"$*\" >> " + shellQuoteForTest(order) + "\nexit 0\n",
		filepath.Join(f.fakeBin, "yolo"): "#!/bin/sh\nif [ \"$1 $2\" = \"internal tool-skills\" ]; then printf 'yolo %s\\n' \"$*\" >> " +
			shellQuoteForTest(order) + "; exit " + toolSkillsRC + "; fi\nexec " + shellQuoteForTest(realYolo) + " \"$@\"\n",
	}
	for p, body := range files {
		if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	var out, errb strings.Builder
	env := []string{}
	if allowlist != "" {
		env = append(env, "YOLO_MISE_SKILL_TOOLS="+allowlist)
	}
	rc = f.runBash(t, stageAt(t, f.log, func() string { return buildProvisionStage(true) }), env, &out, &errb)
	b, _ := os.ReadFile(order)
	return rc, string(b), f
}

func TestFirstSessionStageRunsToolSkillsAfterMiseInstall(t *testing.T) {
	rc, calls, f := toolSkillsStage(t, `["packslip:x"]`, "0")
	if rc != 0 {
		t.Fatalf("rc %d", rc)
	}
	lines := strings.Split(strings.TrimSpace(calls), "\n")
	install, skills := -1, -1
	for i, l := range lines {
		if strings.HasPrefix(l, "mise install") {
			install = i
		}
		if strings.HasPrefix(l, "yolo internal tool-skills /workspace ") {
			skills = i
		}
	}
	if install < 0 || skills < 0 || skills < install {
		t.Fatalf("tool-skills must follow mise install:\n%s", calls)
	}
	if _, err := os.Stat(f.bootRan); err != nil {
		t.Fatalf("the bootstrap did not run: %v", err)
	}
}

func TestFirstSessionStageRunsNothingForAnEmptyAllowlist(t *testing.T) {
	for _, v := range []string{"", "[]"} {
		rc, calls, _ := toolSkillsStage(t, v, "0")
		if rc != 0 || strings.Contains(calls, "tool-skills") {
			t.Errorf("allowlist %q: rc %d calls:\n%s", v, rc, calls)
		}
	}
}

func TestAFailingToolSkillsStepNeverGatesTheStage(t *testing.T) {
	rc, calls, f := toolSkillsStage(t, `["packslip:x"]`, "9")
	if rc != 0 || !strings.Contains(calls, "tool-skills") {
		t.Fatalf("rc %d calls:\n%s", rc, calls)
	}
	for _, marker := range []string{f.bootRan} {
		if _, err := os.Stat(marker); err != nil {
			t.Fatalf("a failed tool-skills step skipped %s: %v", marker, err)
		}
	}
}
