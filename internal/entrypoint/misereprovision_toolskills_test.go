package entrypoint

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// toolSkillsRun is a `run` for reprovisionMise that executes the stage bytes under sh with a fake
// mise and a fake yolo, both recording into one file, so the ORDER of the two is observable.
func toolSkillsRun(t *testing.T, allowlist, yoloRC string) (run func(*Env, string) int, order func() string) {
	t.Helper()
	bin := t.TempDir()
	log := filepath.Join(t.TempDir(), "order")
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write("mise", "#!/bin/sh\necho \"mise $*\" >> "+shquote.Quote(log)+"\nexit 0\n")
	write("yolo", "#!/bin/sh\necho \"yolo $*\" >> "+shquote.Quote(log)+"\nexit "+yoloRC+"\n")
	run = func(_ *Env, stage string) int {
		cmd := exec.Command("sh", "-c", stage)
		cmd.Env = []string{"PATH=" + bin + ":/bin:/usr/bin", "YOLO_MISE_SKILL_TOOLS=" + allowlist}
		if err := cmd.Run(); err != nil {
			if ee, ok := err.(*exec.ExitError); ok {
				return ee.ExitCode()
			}
			t.Fatal(err)
		}
		return 0
	}
	return run, func() string { b, _ := os.ReadFile(log); return string(b) }
}

func TestALaterSessionsReinstallRunsToolSkillsAfterMiseInstall(t *testing.T) {
	withJailMainDir(t)
	if err := writeMainState(miseToolsRecordFile, miseSessionKey(miseSessionEnv(t, `{"jq":"1.7.1"}`, "pnpm"))); err != nil {
		t.Fatal(err)
	}
	run, order := toolSkillsRun(t, `["packslip:x"]`, "0")
	if rc := reprovisionMise(miseSessionEnv(t, `{"jq":"1.8.0"}`, "pnpm"), run, &bytes.Buffer{}); rc != 0 {
		t.Fatalf("rc %d", rc)
	}
	lines := strings.Split(strings.TrimSpace(order()), "\n")
	if len(lines) < 2 || !strings.HasPrefix(lines[len(lines)-2], "mise install") ||
		!strings.HasPrefix(lines[len(lines)-1], "yolo internal tool-skills /workspace ") {
		t.Fatalf("want mise install, then tool-skills:\n%s", order())
	}
}

func TestAFailingToolSkillsStepNeverFailsTheReinstall(t *testing.T) {
	withJailMainDir(t)
	if err := writeMainState(miseToolsRecordFile, miseSessionKey(miseSessionEnv(t, `{"jq":"1.7.1"}`, "pnpm"))); err != nil {
		t.Fatal(err)
	}
	run, order := toolSkillsRun(t, `["packslip:x"]`, "9")
	newEnv := miseSessionEnv(t, `{"jq":"1.8.0"}`, "pnpm")
	var said bytes.Buffer
	if rc := reprovisionMise(newEnv, run, &said); rc != 0 {
		t.Fatalf("a failing tool-skills step failed the reinstall: rc %d\n%s", rc, said.String())
	}
	if !strings.Contains(order(), "tool-skills") {
		t.Fatalf("the step did not run:\n%s", order())
	}
	if rec, ok := readMainState(miseToolsRecordFile); !ok || rec != miseSessionKey(newEnv) {
		t.Fatalf("the install was not recorded: %q %v", rec, ok)
	}
}

func TestAReinstallWithAnEmptyAllowlistRunsNoToolSkillsProcess(t *testing.T) {
	withJailMainDir(t)
	if err := writeMainState(miseToolsRecordFile, miseSessionKey(miseSessionEnv(t, `{"jq":"1.7.1"}`, "pnpm"))); err != nil {
		t.Fatal(err)
	}
	for _, v := range []string{"", "[]"} {
		run, order := toolSkillsRun(t, v, "0")
		if rc := reprovisionMise(miseSessionEnv(t, `{"jq":"1.8.`+map[string]string{"": "0", "[]": "1"}[v]+`"}`, "pnpm"), run, &bytes.Buffer{}); rc != 0 {
			t.Fatalf("rc %d", rc)
		}
		if got := order(); !strings.Contains(got, "mise install") || strings.Contains(got, "yolo") {
			t.Fatalf("allowlist %q:\n%s", v, got)
		}
	}
}
