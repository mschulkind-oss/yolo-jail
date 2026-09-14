package integration

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// THE ONE-COMMIT MISTAKE THIS FILE EXISTS TO MAKE IMPOSSIBLE.
//
// A self-hosted runner executes whatever a workflow tells it to, on a machine somebody
// owns. This repository is PUBLIC and forkable, and GitHub's own guidance is that
// self-hosted runners belong on private repositories for exactly that reason.
//
// What keeps a fork off the maintainer's Mac is the TRIGGER LIST and nothing else:
// `schedule` and `workflow_dispatch` both run the workflow file from the BASE branch,
// neither can be caused by a fork, and `workflow_dispatch` is restricted to write access.
// Add `pull_request` and every forker gets a shell on that machine — as an ordinary-looking
// convenience commit, in a file no test read.
//
// apple-container.yml says "**Do not add a trigger to this file**" in prose. Prose does not
// fail a build. This does.
//
// ⚠ IT IS KEYED ON `self-hosted`, NOT ON A FILENAME, and that is the point: the guard has
// to cover the workflow somebody adds next year, not the one that exists today. A new
// self-hosted workflow is protected the moment it is written, without anybody remembering
// this file.
//
// STATIC — no containers, no requireJail. It reads two files off disk, so it runs under
// `-short` on every push, which is where a guard on a one-line mistake has to run.
// (readWorkflow and moduleRoot live in macosmachineshares_test.go, which pins the nightly's
// `podman machine init` shares the same way.)

// safeSelfHostedTriggers is the complete set of events allowed to reach a self-hosted
// runner, and WHY each one is safe. Anything absent from this map is a trigger a fork can
// influence, or one that runs a fork's version of the workflow file.
var safeSelfHostedTriggers = map[string]string{
	"schedule": "cron fires the BASE branch's copy of the workflow; no outside actor " +
		"can cause it",
	"workflow_dispatch": "runs the ref's workflow file and is restricted to users with " +
		"write access; a fork cannot dispatch into this repository",
}

// unsafeSelfHostedTriggers names the events whose danger is worth spelling out when one
// appears, rather than reporting a bare "not allowed". Every one of them is a real way a
// fork's code, or a stranger's comment, reaches the runner. A trigger missing from BOTH
// maps is still refused — the default is deny.
var unsafeSelfHostedTriggers = map[string]string{
	"pull_request": "a fork's PR would run the FORK's workflow file on the runner. This " +
		"is the single commit that hands every forker a shell on the maintainer's machine",
	"pull_request_target": "runs the base workflow but with the fork's CODE checked out, " +
		"and with secrets available — the classic self-hosted-runner compromise",
	"issue_comment": "anyone who can comment on an issue can fire it",
	"push": "safe from forks, but it would run the runner on every commit rather " +
		"than on the two-hourly poll the job is designed around, including commits pushed " +
		"by an agent working in this repo",
	"fork":                        "fires on somebody else's action",
	"watch":                       "fires when a stranger stars the repository",
	"repository_dispatch":         "fires from an API call carrying a token this test cannot reason about",
	"workflow_run":                "inherits the triggering workflow's provenance, which may be a fork's",
	"pull_request_review":         "a fork's PR review reaches it",
	"pull_request_review_comment": "same as pull_request_review",
}

// onBlockRe captures the top-level `on:` mapping: the `on:` line plus every following line
// that is indented, blank or a comment, stopping at the next column-0 key.
//
// A line-based parse rather than a YAML one because no YAML package is vendored and adding
// a dependency for a ten-line check would also have to pass the hermetic nix build
// (AGENTS.md, the `vendor/` rule). The shape it has to read is fixed and small: GitHub
// requires `on` at the top level, and both the mapping and inline-list forms are handled
// below.
var onBlockRe = regexp.MustCompile(`(?m)^(?:on|"on"|'on'):[ \t]*(.*)$((?:\n(?:[ \t]+[^\n]*|[ \t]*))*)`)

// triggerKeyRe matches one trigger inside the mapping form — a key at any indent, which is
// enough because a nested key (`schedule:`'s `- cron:`) is a list item and starts with `-`.
var triggerKeyRe = regexp.MustCompile(`(?m)^[ \t]{1,4}([a-z_]+):`)

// inlineListRe pulls the names out of the `on: [a, b]` form.
var inlineListRe = regexp.MustCompile(`\[([^\]]*)\]`)

// selfHostedRe finds a runs-on naming a self-hosted runner, in either the bare form
// (`runs-on: self-hosted`) or the label-list form this repo uses
// (`runs-on: [self-hosted, macOS, ARM64, apple-container]`).
var selfHostedRe = regexp.MustCompile(`(?m)^[ \t]*runs-on:[ \t]*.*\bself-hosted\b`)

// workflowTriggers returns the event names a workflow declares.
func workflowTriggers(t *testing.T, body string) []string {
	t.Helper()
	m := onBlockRe.FindStringSubmatch(body)
	if m == nil {
		t.Fatal("no top-level `on:` block found — either the workflow is malformed or " +
			"this parse no longer matches the file's shape, and a guard that silently " +
			"finds nothing is worse than none")
	}
	// `on: [schedule, workflow_dispatch]` — the inline form, on the `on:` line itself.
	if inline := inlineListRe.FindStringSubmatch(m[1]); inline != nil {
		var out []string
		for _, name := range strings.Split(inline[1], ",") {
			if n := strings.TrimSpace(name); n != "" {
				out = append(out, n)
			}
		}
		return out
	}
	// `on: push` — a single event with no block.
	if bare := strings.TrimSpace(m[1]); bare != "" && !strings.HasPrefix(bare, "#") {
		return []string{bare}
	}
	var out []string
	for _, km := range triggerKeyRe.FindAllStringSubmatch(m[2], -1) {
		out = append(out, km[1])
	}
	return out
}

// selfHostedWorkflows lists every workflow that schedules work onto a self-hosted runner.
func selfHostedWorkflows(t *testing.T) map[string]string {
	t.Helper()
	root, err := moduleRoot()
	if err != nil {
		t.Fatalf("locating module root: %v", err)
	}
	dir := filepath.Join(root, ".github", "workflows")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("cannot read %s: %v", dir, err)
	}
	found := map[string]string{}
	for _, e := range entries {
		if e.IsDir() || !(strings.HasSuffix(e.Name(), ".yml") || strings.HasSuffix(e.Name(), ".yaml")) {
			continue
		}
		body := readWorkflow(t, e.Name())
		if selfHostedRe.MatchString(body) {
			found[e.Name()] = body
		}
	}
	return found
}

// TestSelfHostedWorkflowsCannotBeTriggeredByAFork is the guard.
//
// It asserts the DIRECTION that matters: every trigger on a self-hosted workflow must be one
// a fork cannot cause. It deliberately does not assert the reverse (that both safe triggers
// are present) — dropping `schedule` makes the job manual, which is a choice, not a
// vulnerability.
func TestSelfHostedWorkflowsCannotBeTriggeredByAFork(t *testing.T) {
	workflows := selfHostedWorkflows(t)
	if len(workflows) == 0 {
		// NOT a failure. The repo is allowed to have no self-hosted job — it had none
		// before 2026-09-14 — and this test must not become a reason to keep one.
		t.Skip("no workflow targets a self-hosted runner")
	}
	for name, body := range workflows {
		triggers := workflowTriggers(t, body)
		if len(triggers) == 0 {
			t.Errorf("%s targets a self-hosted runner and this test could not read its "+
				"triggers. That is a FAILURE, not a pass: the trigger list is the only "+
				"thing keeping a fork off the runner, so a guard that cannot read it has "+
				"to say so.", name)
			continue
		}
		for _, ev := range triggers {
			if _, ok := safeSelfHostedTriggers[ev]; ok {
				continue
			}
			why, known := unsafeSelfHostedTriggers[ev]
			if !known {
				why = "this test does not know that event, and the default is DENY — if it " +
					"genuinely cannot be caused by a fork, add it to safeSelfHostedTriggers " +
					"with the reason"
			}
			t.Errorf("%s runs on a SELF-HOSTED runner and declares the %q trigger.\n"+
				"  %s.\n"+
				"A self-hosted runner executes the workflow on a machine somebody owns, and "+
				"this repository is public. The allowed triggers are %s.\n"+
				"If a change to that workflow needs testing before it merges, dispatch it by "+
				"hand from its branch — `workflow_dispatch` takes a ref.",
				name, ev, why, strings.Join(sortedKeys(safeSelfHostedTriggers), " and "))
		}
	}
}

// TestSelfHostedJobsRefuseToRunForAFork pins the second half of the protection.
//
// The trigger list stops a fork from CAUSING a run; this stops a run that happens anyway
// from reaching the runner. They are independent: a scheduled workflow in a fork of this
// repository fires on the fork's own schedule, with the fork's own workflow file, and would
// look for the fork's runners — but a fork that adds the maintainer's runner, or an
// organisation-level runner shared wider than intended, is reached by nothing else.
func TestSelfHostedJobsRefuseToRunForAFork(t *testing.T) {
	workflows := selfHostedWorkflows(t)
	if len(workflows) == 0 {
		t.Skip("no workflow targets a self-hosted runner")
	}
	for name, body := range workflows {
		if !strings.Contains(body, "github.repository ==") {
			t.Errorf("%s targets a self-hosted runner but no job guards on "+
				"`github.repository == '<owner>/<repo>'`.\nWithout it a fork that "+
				"scheduled this workflow would try to reach a runner it must not, and the "+
				"trigger list cannot help — a fork's cron fires the fork's own copy of the "+
				"file. Add the condition to the self-hosted job's `if:`.", name)
		}
	}
}

// sortedKeys is the deterministic message helper — an error listing the allowed triggers in
// map order would differ run to run.
func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}
