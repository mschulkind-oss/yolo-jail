package cli

// hostapplywrappersnextstep_test.go pins that a failure of the wrappers stage names its next
// step (docs/reference/happy-path-principle.md, rule 1), and leads with the word the verdict
// names the stage by. The stage printed `yolo host apply: planning wrappers: <error>` and
// nothing after it.

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

func TestAWrappersFailureNamesItsNextStep(t *testing.T) {
	for _, write := range []bool{false, true} {
		name := "dry run"
		if write {
			name = "--assert"
		}
		t.Run(name, func(t *testing.T) {
			defaultReport(t)
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
			// `own`: the next step this failure names, `yolo host apply --assert`, refuses under
			// the unset key, which is `none` since the `assert` retirement (OQ-CO14).
			writeFile(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"),
				`{"packs":["claude"],"host_management":"own","host_wrappers":true}`)
			stubDeclaredBins(t)
			dir := paths.WrapDirUnder(home)
			writeFile(t, dir, "not a directory\n")

			_, report := applyWith(t, write, nil)
			lines := strings.Split(report, "\n")
			at := -1
			for i, line := range lines {
				if strings.HasPrefix(line, "yolo host apply: wrappers failed — ") {
					at = i
				}
			}
			if at < 0 || at+2 >= len(lines) {
				t.Fatalf("no `yolo host apply: wrappers failed — …` line with two after it:\n%s", report)
			}
			if !strings.Contains(lines[at], "not a directory") {
				t.Errorf("the failure line does not carry the error: %q", lines[at])
			}
			fix := "  fix: make ~/" + filepath.ToSlash(strings.TrimPrefix(dir, home+"/")) +
				" a directory you can write (yolo keeps only its wrappers there), then: " +
				"yolo host apply --assert"
			if lines[at+1] != fix {
				t.Errorf("the line after the failure is\n%q\nwant\n%q", lines[at+1], fix)
			}
			off := `  or stop yolo writing wrappers: set "host_wrappers": false in ` + paths.UserConfigPath()
			if lines[at+2] != off {
				t.Errorf("the second step is\n%q\nwant\n%q", lines[at+2], off)
			}
		})
	}
}

// With wrappers already off, the stage only clears what an earlier apply wrote, so the step that
// turns them off is not offered: it is already taken.
func TestAWrappersClearFailureDoesNotOfferToTurnThemOff(t *testing.T) {
	var b strings.Builder
	reportWrappersFailure(&b, "/h", "/h/.local/share/yolo-jail/bin/wrap", "clearing them",
		errString("boom"), false)
	got := b.String()
	if !strings.Contains(got, "yolo host apply: wrappers failed — clearing them: boom\n") ||
		!strings.Contains(got, "then: yolo host apply --assert") {
		t.Errorf("the clear failure lacks its error or its step:\n%s", got)
	}
	if strings.Contains(got, "host_wrappers") {
		t.Errorf("the clear failure offers to turn off wrappers that are off:\n%s", got)
	}
}

type errString string

func (e errString) Error() string { return string(e) }
