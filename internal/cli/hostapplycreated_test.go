package cli

// hostapplycreated_test.go pins HC-D4 (docs/design/host-computed-layer.md §7) where a user
// meets it: a host apply that CREATES a config file counts it as a change, in the dry run and
// in the --assert, and never among the destinations "already in sync".
//
// Measured on host pi before the fix: the dry run listed pi/models as `unchanged`, the
// --assert's verdict counted it in "N destinations already in sync", and the file was
// created. Only `--verbose` showed `pi/models rendered`. pi/codex-models is the surface left
// in that state once pi/models declares a default (HC-D1): it declares no layer, so a fresh
// home gets a `{}` file from the first apply.

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// surveyApplyPosture runs one apply in the given posture and returns its survey and report.
func surveyApplyPosture(t *testing.T, write bool) (*hostApplySurvey, string) {
	t.Helper()
	var out, errw bytes.Buffer
	survey := &hostApplySurvey{}
	if rc := applyHostSurveyed(&out, &errw, false, write, nil, survey); rc != 0 {
		t.Fatalf("apply (write=%v) rc=%d\n%s%s", write, rc, out.String(), errw.String())
	}
	return survey, out.String() + errw.String()
}

// changedPath reports whether the survey lists path among the destinations it would change.
func changedPath(s *hostApplySurvey, path string) bool {
	for _, c := range s.Changed {
		if c.Path == path {
			return true
		}
	}
	return false
}

func TestAHostApplyCountsAFileItCreatesAsAChange(t *testing.T) {
	home := t.TempDir()
	// `own`, the one contract that renders a config file: the unset key is `none` since the
	// `assert` retirement (OQ-CO14), under which no file is created to be counted.
	writeFile(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"),
		`{"packs":["pi"],"host_management":"own"}`)
	stubDeclaredBins(t)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	created := filepath.Join(home, ".pi", "agent", "yolo-openai-codex-models.json")

	dry, report := surveyApplyPosture(t, false)
	if _, err := os.Stat(created); !os.IsNotExist(err) {
		t.Fatalf("the dry run created %s (stat: %v)\n%s", created, err, report)
	}
	if !changedPath(dry, created) {
		t.Errorf("the dry run over a home with no %s does not list it as a change, so it is "+
			"counted among the %d destinations already in sync while the --assert creates it: "+
			"%+v\n%s", created, dry.InSync, dry.Changed, report)
	}

	wrote, report := surveyApplyPosture(t, true)
	if _, err := os.Stat(created); err != nil {
		t.Fatalf("the --assert did not create %s: %v\n%s", created, err, report)
	}
	if !changedPath(wrote, created) {
		t.Errorf("the --assert created %s and did not count it as a change: %+v\n%s",
			created, wrote.Changed, report)
	}

	settled, report := surveyApplyPosture(t, false)
	if changedPath(settled, created) {
		t.Errorf("the dry run after the --assert still lists %s as a change — a created file "+
			"must read as in sync once it exists, or every wrapped launch re-prompts\n%s",
			created, report)
	}
	if settled.InSync == 0 {
		t.Errorf("the settled survey saw no destinations, so the assertion above is vacuous\n%s",
			report)
	}
}
