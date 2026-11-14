package macosuser

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/provision"
)

// The account-mode stop of docs/lab-reports/2026-11-13-mac-keychain-tmp-stores-bedrock.md §3:
// the provisioning stage found no `yolo` on its PATH, the first `yolo internal log-append` failed
// with `command not found`, and the stop said "repair workspace .yolo permissions or free disk
// space", which named neither the cause nor a step that helps. The only `yolo` the stage's PATH
// can reach is the one in this session's staged command directory (SessionGuestBinDir).

// provisionStageScript is the `bash -c` script of a plan's provisioning argv: its last word.
func provisionStageScript(t *testing.T, plan RunPlan) string {
	t.Helper()
	if len(plan.ProvisionArgv) == 0 {
		t.Fatal("the fixture asks for the stage, and the plan carries none")
	}
	return plan.ProvisionArgv[len(plan.ProvisionArgv)-1]
}

// THE CALL SITE: the plan's own stage script, run with a PATH that reaches no yolo, stops with
// the stage's own status for that, naming this session's staged command directory, and the
// orchestrator's stop names the cause and the next step. Neither blames permissions or disk.
func TestAStageWithNoYoloNamesTheStagedCommandDir(t *testing.T) {
	ws := t.TempDir()
	if err := os.MkdirAll(filepath.Join(ws, ".yolo", "logs"), 0o755); err != nil {
		t.Fatal(err)
	}
	d := mockDeps(nil)
	opts := newOpts(ws)
	opts.Config = provisionCfg()
	opts.SessionID = "0123456789abcdef"
	plan := buildPlan(d, opts, mockDarwin())
	if plan.SessionGuestBinDir == "" {
		t.Fatal("the plan names no session guest-bin child")
	}
	script := provisionStageScript(t, plan)
	// A PATH with the shell's tools and no yolo: the state the Mac run met. Built rather than
	// /usr/bin:/bin, which reaches a yolo inside a jail.
	tools := t.TempDir()
	for _, name := range []string{"date", "cat"} {
		real, err := exec.LookPath(name)
		if err != nil {
			t.Skipf("no %s on this machine: %v", name, err)
		}
		if err := os.Symlink(real, filepath.Join(tools, name)); err != nil {
			t.Fatal(err)
		}
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skipf("no bash: %v", err)
	}

	var console bytes.Buffer
	status := 0
	deps := Deps{
		Run: func(argv []string) int {
			// The script alone, as the sandbox's bash runs it.
			cmd := exec.Command(bash, "-c", script)
			cmd.Env = []string{"PATH=" + tools, "HOME=" + ws}
			cmd.Stdout, cmd.Stderr = &console, &console
			if err := cmd.Run(); err != nil {
				e, ok := err.(*exec.ExitError)
				if !ok {
					t.Fatal(err)
				}
				status = e.ExitCode()
			}
			return status
		},
		RemoveFile: func(string) bool { return true },
		ReadFile:   func(string) (string, bool) { return "", true },
		PathExists: func(string) bool { return false },
	}
	if runProvisionStage(deps, printer{w: &console}, plan) {
		t.Fatalf("a stage that found no yolo let the launch continue:\n%s", console.String())
	}
	if status != provision.StageYoloMissingStatus {
		t.Fatalf("stage status = %d, want %d (yolo missing):\n%s", status, provision.StageYoloMissingStatus,
			console.String())
	}
	got := console.String()
	for _, want := range []string{plan.SessionGuestBinDir, "staged command directory", "yolo run --dry-run"} {
		if !strings.Contains(got, want) {
			t.Errorf("the stop does not say %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "permissions or free disk space") {
		t.Errorf("the stop blames permissions or disk for a missing yolo:\n%s", got)
	}
}

// A LOG FAILURE WHOSE yolo IS GONE (the `--tee` writer, or an older stage script without the
// check) is reported as the missing command directory, not as permissions or disk; one whose yolo
// is still there keeps the log-failure stop, which is then the honest reading.
func TestALogFailureWithTheStagedYoloGoneNamesTheCause(t *testing.T) {
	plan := RunPlan{Workspace: t.TempDir(), SessionGuestBinDir: "/Users/Shared/yolo-staged/bin/0123456789abcdef-" +
		strings.Repeat("a", 64)}
	for _, tc := range []struct {
		name       string
		yoloExists bool
		want, not  string
	}{
		{"yolo gone", false, "staged command directory", "permissions or free disk space"},
		{"yolo present", true, "permissions or free disk space", "staged command directory"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var console bytes.Buffer
			var asked []string
			deps := Deps{
				Run:        func([]string) int { return provision.StageLogFailedStatus },
				RemoveFile: func(string) bool { return true },
				ReadFile:   func(string) (string, bool) { return "", true },
				PathExists: func(p string) bool { asked = append(asked, p); return tc.yoloExists },
			}
			if runProvisionStage(deps, printer{w: &console}, plan) {
				t.Fatalf("a log failure let the launch continue:\n%s", console.String())
			}
			if !strings.Contains(console.String(), tc.want) || strings.Contains(console.String(), tc.not) {
				t.Errorf("want %q and not %q in:\n%s", tc.want, tc.not, console.String())
			}
			if wantPath := filepath.Join(plan.SessionGuestBinDir, "yolo"); len(asked) != 1 || asked[0] != wantPath {
				t.Errorf("asked the host about %q, want only %s", asked, wantPath)
			}
		})
	}
}

// The stop says which of the two it is: the directory gone from the host, or still on the host and
// unreachable from the sandbox. That is the fact the Mac run could not establish (it looked for the
// dry run's placeholder name, which a live launch never stages).
func TestTheMissingYoloStopSaysWhetherTheDirIsOnTheHost(t *testing.T) {
	plan := RunPlan{Workspace: t.TempDir(), SessionGuestBinDir: "/Users/Shared/yolo-staged/bin/0123456789abcdef-" +
		strings.Repeat("a", 64)}
	for _, tc := range []struct {
		exists bool
		want   string
	}{{false, "is gone from the host"}, {true, "is on the host"}} {
		var console bytes.Buffer
		deps := Deps{
			Run:        func([]string) int { return provision.StageYoloMissingStatus },
			RemoveFile: func(string) bool { return true },
			ReadFile:   func(string) (string, bool) { return "", true },
			PathExists: func(string) bool { return tc.exists },
		}
		if runProvisionStage(deps, printer{w: &console}, plan) {
			t.Fatalf("a missing yolo let the launch continue:\n%s", console.String())
		}
		if !strings.Contains(console.String(), tc.want) {
			t.Errorf("exists=%v: want %q in:\n%s", tc.exists, tc.want, console.String())
		}
	}
}
