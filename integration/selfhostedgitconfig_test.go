package integration

// A self-hosted job must not edit the runner owner's global git config.
//
// `actions/checkout` defaults to `set-safe-directory: true`, which runs
// `git config --global --add safe.directory <workspace>`. On a GitHub-hosted runner that
// is invisible — the VM is discarded. On a self-hosted one the runner executes as a
// PERSON, so `--global` is that person's real ~/.gitconfig.
//
// MEASURED 2026-09-16 on the runner Mac, which is why this test exists rather than a note:
// ~/.gitconfig there is a symlink into a tracked dotfiles repo; the Apple Container job's
// checkout appended `[safe] directory = /Users/…/_work/yolo-jail/yolo-jail` at 01:29:19Z,
// the same second the runner's own Worker log evaluated `set-safe-directory`; the dotfiles
// repo went dirty. Because the flag is `--add` and the paired removal does not stick, every
// run left one more identical line, and 116 had accumulated before anyone looked.
//
// The guard is on the WORKFLOW rather than on the machine because the machine cannot defend
// itself: nothing on a runner host can tell a legitimate `git config --global` from this one.
//
// It reads the same self-hosted workflow set as selfhostedtriggers_test.go
// (selfHostedWorkflows), so a self-hosted workflow added later is covered by construction —
// the property that made that file's guard worth having.

import (
	"strings"
	"testing"
)

// checkoutSteps returns each `- uses: actions/checkout…` step block in a workflow.
//
// It splits on the six-space step indentation every workflow here uses, like
// stepContaining, but keys on `uses:` rather than `- name:` — the checkout steps in this
// repo carry no name, so the existing splitter cannot see them. A caller that gets zero
// blocks out of a body that mentions checkout must fail rather than pass: see the vacuity
// check below.
func checkoutSteps(body string) []string {
	var steps []string
	for _, chunk := range strings.Split(body, "\n      - ")[1:] {
		if strings.HasPrefix(strings.TrimSpace(chunk), "uses: actions/checkout") {
			steps = append(steps, chunk)
		}
	}
	return steps
}

func TestSelfHostedCheckoutsDoNotTouchTheRunnersGlobalGitConfig(t *testing.T) {
	workflows := selfHostedWorkflows(t)
	if len(workflows) == 0 {
		// NOT a failure, for the reason the sibling guard gives: the repo is allowed to
		// have no self-hosted job, and this test must not become a reason to keep one.
		t.Skip("no workflow targets a self-hosted runner")
	}
	for name, body := range workflows {
		steps := checkoutSteps(body)
		if len(steps) == 0 {
			if strings.Contains(uncommentedYAML(body), "actions/checkout") {
				t.Errorf("%s targets a self-hosted runner and uses actions/checkout, but this "+
					"guard parsed no checkout step out of it. That is a FAILURE, not a pass — "+
					"the assertion below would be vacuous, and the thing it protects is "+
					"somebody's dotfiles.", name)
			}
			continue
		}
		for i, step := range steps {
			if strings.Contains(uncommentedYAML(step), "set-safe-directory: false") {
				continue
			}
			t.Errorf("%s runs on a SELF-HOSTED runner and its checkout step %d does not set "+
				"`set-safe-directory: false`.\n"+
				"  The default (`true`) runs `git config --global --add safe.directory "+
				"<workspace>` as the runner's OWNER, editing a real person's ~/.gitconfig — "+
				"measured on the runner Mac 2026-09-16, where it appended to a tracked "+
				"dotfiles repo once per run until 116 identical lines had piled up.\n"+
				"  git's ownership check only fires when the repo is owned by a different uid "+
				"than the one running git, so on a runner that checks out and builds as one "+
				"user the entry buys nothing. If a runner ever genuinely needs it, grant the "+
				"ownership another way — the entry this writes is machine-wide and permanent.",
				name, i+1)
		}
	}
}
