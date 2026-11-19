package run

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// TestFirstSessionBytesAreTheGolden pins the provisioning stage and the first session's
// command for target_cmd="bash" against a golden (testdata/final_cmd_bash.txt), joined by
// "; " as the first session runs them: the stage, then the command. This is a frozen
// host-state contract — the bytes were the container's own `bash -c` payload until the main
// process became a hold, and splitting them between the stage and the session moved none.
func TestFirstSessionBytesAreTheGolden(t *testing.T) {
	want, err := os.ReadFile(filepath.Join("testdata", "final_cmd_bash.txt"))
	if err != nil {
		t.Fatal(err)
	}
	got := buildProvisionStage(true) + "; " + buildSessionCmd("bash", false, true)
	if got != string(want) {
		t.Errorf("first-session bytes mismatch\n got: %q\nwant: %q", got, string(want))
	}
}

// TestBuildSessionCmdQuotingEscapesDisplay: the raw target_cmd (unescaped) is what actually
// runs at the tail; only the banner's copy of it is quoted. How the banner renders it is
// TestExecutingBannerPrintsTheTargetVerbatim's job, which runs it rather than grepping for an
// escape.
func TestBuildSessionCmdQuotingEscapesDisplay(t *testing.T) {
	got := buildSessionCmd("echo 'hi'", false, true)
	if !hasSuffixStr(got, "; echo 'hi'") {
		t.Errorf("target_cmd tail not raw: %q", got)
	}
}

func TestUpdatePreludeCompletesBeforeUntouchedTarget(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "updates")
	launcher := filepath.Join(dir, "yolo")
	body := "#!/bin/sh\necho \"$*\" >> " + shquote.Quote(log) + "\n" +
		"if [ \"$2\" = refresh-servers ] && [ \"${FAIL_SERVERS:-}\" = 1 ]; then exit 7; fi\nexit 0\n"
	if err := os.WriteFile(launcher, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(dir, "target-ran")
	target := shquote.Join([]string{"sh", "-c", "touch " + shquote.Quote(marker) +
		"; printf '%s|%s\\n' \"$0\" \"$1\"", "--update", "arg with space"})
	o := &Options{Update: true, Getenv: func(string) string { return "" }}
	script := o.sessionCmd(target)
	cmd := exec.Command("bash", "-c", script)
	cmd.Env = append(os.Environ(), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	got, err := cmd.CombinedOutput()
	if err != nil || !strings.HasSuffix(string(got), "--update|arg with space\n") {
		t.Fatalf("fresh launch = %q, %v; want the untouched target argv\nscript: %s", got, err, script)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("the target marker is absent after successful preparation: %v", err)
	}
	updates, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if string(updates) != "internal refresh-programs\ninternal refresh-servers --fresh\n" {
		t.Fatalf("update-only helpers ran %q, want the exact selected-tree and MCP helpers", updates)
	}

	if err := os.Remove(log); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	cmd = exec.Command("bash", "-c", script)
	cmd.Env = append(os.Environ(), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"), "FAIL_SERVERS=1")
	got, err = cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("failed preparation returned success: output=%q", got)
	}
	if strings.Contains(string(got), "⚡ Executing:") {
		t.Fatalf("failed preparation printed Executing banner before target could run: output=%q", got)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("failed preparation reached target (marker stat err=%v): output=%q", err, got)
	}
}

func contains(s, sub string) bool { return indexOfStr(s, sub) >= 0 }
func hasSuffixStr(s, suf string) bool {
	return len(s) >= len(suf) && s[len(s)-len(suf):] == suf
}
func indexOfStr(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func TestUpdateTimingReportsAfterThePreparedTargetCompletes(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "yolo"), []byte("#!/bin/sh\nprintf 'prepared %s\\n' \"$2\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, timing := range []bool{false, true} {
		o := &Options{Update: true, Timing: timing, Getenv: func(string) string { return "" }}
		cmd := exec.Command("bash", "-c", o.sessionCmd("sh -c 'echo target-completed; exit 7'"))
		cmd.Env = append(os.Environ(), "HOME="+dir, "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"))
		out, err := cmd.CombinedOutput()
		if err == nil || err.(*exec.ExitError).ExitCode() != 7 {
			t.Fatalf("timing=%v target status: %v\n%s", timing, err, out)
		}
		text := string(out)
		wantOrder := []string{"prepared refresh-programs", "prepared refresh-servers", "⚡ Executing: ", "\ntarget-completed\n"}
		if timing {
			wantOrder = append(wantOrder, "=== YOLO Jail Profile ===", "command execution:")
		}
		last := -1
		for _, want := range wantOrder {
			at := strings.Index(text, want)
			if at < 0 || at <= last {
				t.Fatalf("timing=%v missing/out-of-order %q:\n%s", timing, want, text)
			}
			last = at
		}
	}
}
