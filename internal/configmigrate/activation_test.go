package configmigrate

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// Every line of the activation layer names the step that makes it agree, and the refusal prints
// it under the line.
func TestEveryActivationDifferenceNamesItsNextStep(t *testing.T) {
	diffs := activationNextSteps([]Difference{
		{Layer: loopholeActivationLayer, Path: "aws-auth.enabled", Before: "false", After: "true"},
		{Layer: loopholeActivationLayer, Path: "journal.enabled", Before: "true", After: "false"},
		{Layer: loopholeActivationLayer, Path: "aws-auth.settings", Before: `{"profile": "a"}`, After: "(absent)"},
		{Layer: loopholeActivationLayer, Path: "audio.enabled", Before: "false", After: "true"},
		{Layer: loopholeActivationLayer, Path: "github-broker.enabled", Before: "true", After: "false"},
	}, shippedLoopholePacks(packload.Embedded()))
	for i, want := range []string{
		// An off-switch loophole runs whenever its pack is selected (legacy-config-compat.md
		// LC-D3): the old file keeps it off, and no "enabled": true is needed to run it.
		"the converted config runs aws-auth because its pack is selected, and the old one keeps it off; write `off = true` under [allow.aws-auth] in the TOML user config to keep it off, or remove the `\"enabled\": false` that keeps it off in the old file to let it run, then run `yolo config migrate` again",
		// Grants are built, so the next step is the pack line that runs it, never a wait.
		"so add the pack (`yolo pack add journal`) to the old file's packs, or keep the old file (this yolo keeps reading it), then run `yolo config migrate` again",
		`write them under loopholes.aws-auth.settings with "enabled": true in the old file, then run ` + "`yolo config migrate` again",
		`add "enabled": true under loopholes.audio in the old file to keep it running, or remove "audio" from packs to keep it off, then run ` + "`yolo config migrate` again",
		// The step names the pack that ships the loophole, through the shipped manifests: there
		// is no github-broker pack.
		"so add the pack (`yolo pack add github`) to the old file's packs",
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
