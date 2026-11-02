package configmigrate

import (
	"strings"
	"testing"
)

// Every line of the activation layer names the step that makes it agree, and the refusal prints
// it under the line.
func TestEveryActivationDifferenceNamesItsNextStep(t *testing.T) {
	diffs := activationNextSteps([]Difference{
		{Layer: loopholeActivationLayer, Path: "aws-auth.enabled", Before: "false", After: "true"},
		{Layer: loopholeActivationLayer, Path: "journal.enabled", Before: "true", After: "false"},
		{Layer: loopholeActivationLayer, Path: "aws-auth.settings", Before: `{"profile": "a"}`, After: "(absent)"},
	})
	for i, want := range []string{
		`add "enabled": true under loopholes.aws-auth in the old file to keep it running, or remove "aws-auth" from packs to keep it off, then run ` + "`yolo config migrate` again",
		"keep the old file (this yolo keeps reading it) until they land, then run `yolo config migrate` again",
		`write them under loopholes.aws-auth.settings with "enabled": true in the old file, then run ` + "`yolo config migrate` again",
	} {
		if !strings.Contains(diffs[i].Next, want) {
			t.Errorf("%s: next = %q, want it to contain %q", diffs[i].Path, diffs[i].Next, want)
		}
	}
	text := (&InequalityError{Plan: &MigrationPlan{Differences: diffs}}).Error()
	if !strings.Contains(text, "\n    next: the converted config runs aws-auth") {
		t.Errorf("the refusal does not print the next step:\n%s", text)
	}
}
