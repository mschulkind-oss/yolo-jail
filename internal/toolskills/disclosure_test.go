package toolskills

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// The in-jail lines of docs/reference/mise-tool-skills.md section 6.2, one test per row, through the
// real RunStep (the call `yolo internal tool-skills` makes). A row that prints nothing is pinned
// too: the step says a change once, and never when the host already delivered it.

func stepEnv(f snapFixture, delivered string, tools ...string) map[string]string {
	list := `["` + strings.Join(tools, `","`) + `"]`
	return map[string]string{
		"YOLO_MISE_SKILL_TOOLS":      list,
		"YOLO_TOOL_SKILLS_TOKEN":     "tok-1",
		"YOLO_TOOL_SKILLS_DELIVERED": delivered,
		"MISE_DATA_DIR":              filepath.Dir(f.installs),
	}
}

func runStepOut(t *testing.T, f snapFixture, env map[string]string, mise string) string {
	t.Helper()
	var out strings.Builder
	if rc := RunStep(context.Background(), envOf(env), []string{f.ws}, Runner{Mise: mise}, &out); rc != 0 {
		t.Fatalf("rc %d", rc)
	}
	return out.String()
}

func TestRunStepFirstSnapshotSaysTheSkillsArriveWithTheNextSession(t *testing.T) {
	f := newFixture(t)
	m, _ := miseListing(t, f.skill(t, "tool-a", "1.2", "sa", "a"), f.skill(t, "tool-a", "1.2", "sb", "b"))
	got := runStepOut(t, f, stepEnv(f, "", "tool-a"), m)
	want := "yolo: mise skills: 2 skills from tool-a are ready; they reach the agent with the next session: relaunch or attach.\n"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestRunStepChangedSnapshotNamesTheToolAndVersion(t *testing.T) {
	f := newFixture(t)
	m1, _ := miseListing(t, f.skill(t, "tool-a", "1.2", "sa", "a"))
	first := mustPublish(t, f.opts(), f.skill(t, "tool-a", "1.2", "sa", "a"))
	_ = m1
	m2, _ := miseListing(t, f.skill(t, "tool-a", "1.3", "sa", "a2"))
	got := runStepOut(t, f, stepEnv(f, first.Digest, "tool-a"), m2)
	want := "yolo: mise skills changed (tool-a 1.2 -> 1.3); they reach the agent with the next session: relaunch or attach.\n"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

// A tool that is new in the snapshot is marked new, and one that left is marked removed, beside
// the one whose version moved.
func TestRunStepChangedSnapshotMarksNewAndRemovedTools(t *testing.T) {
	f := newFixture(t)
	first := mustPublish(t, f.opts(), f.skill(t, "tool-a", "1.2", "sa", "a"), f.skill(t, "tool-b", "2.0", "sb", "b"))
	m, _ := miseListing(t, f.skill(t, "tool-a", "1.3", "sa", "a2"), f.skill(t, "tool-c", "0.1", "sc", "c"))
	got := runStepOut(t, f, stepEnv(f, first.Digest, "tool-a", "tool-b", "tool-c"), m)
	want := "yolo: mise skills changed (tool-a 1.2 -> 1.3, tool-c 0.1 (new), tool-b 2.0 (removed)); they reach the agent with the next session: relaunch or attach.\n"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestRunStepSaysNothingWhenTheHostAlreadyDeliveredThisDigest(t *testing.T) {
	f := newFixture(t)
	a := f.skill(t, "tool-a", "1.2", "sa", "a")
	snap := mustPublish(t, f.opts(), a)
	m, _ := miseListing(t, a)
	if got := runStepOut(t, f, stepEnv(f, snap.Digest, "tool-a"), m); got != "" {
		t.Fatalf("an unchanged digest printed %q", got)
	}
}

// Two sessions entering at once: the second finds `current` already at the new digest (the
// publish lock serializes them), so only the first says it, though both were handed the same stale
// YOLO_TOOL_SKILLS_DELIVERED.
func TestRunStepSecondSessionPrintsNothing(t *testing.T) {
	f := newFixture(t)
	m, _ := miseListing(t, f.skill(t, "tool-a", "1.2", "sa", "a"))
	env := stepEnv(f, "", "tool-a")
	if first := runStepOut(t, f, env, m); first == "" {
		t.Fatal("the first session said nothing")
	}
	if second := runStepOut(t, f, env, m); second != "" {
		t.Fatalf("the second session printed %q", second)
	}
}

func TestRunStepEmptyFirstSnapshotIsSilent(t *testing.T) {
	f := newFixture(t)
	m, _ := miseListing(t)
	if got := runStepOut(t, f, stepEnv(f, "", "tool-a"), m); got != "" {
		t.Fatalf("an empty first snapshot printed %q", got)
	}
}

func TestRunStepDeclaredButNotInstalledNamesMisesLineAndTheNextStep(t *testing.T) {
	f := newFixture(t)
	stderr, _ := filepath.Abs(filepath.Join("testdata", "ls-missing.stderr"))
	m := fakeMise(t, "cat "+shquote.Quote(stderr)+" >&2\necho '[]'")
	got := runStepOut(t, f, stepEnv(f, "", "packslip:github.com/jdx/hk"), m)
	for _, want := range []string{"yolo: mise skills: packslip:github.com/jdx/hk declares skills that are not installed (",
		"declares a skill hk-debug that is not installed", "Run `mise install` in the workspace, then start a new session."} {
		if !strings.Contains(got, want) {
			t.Errorf("%q\nmissing %q", got, want)
		}
	}
	if strings.Count(got, "\n") != 1 {
		t.Errorf("want one line, got %q", got)
	}
}

func TestRunStepIgnoresAMissingSkillOfAToolNotOnTheAllowlist(t *testing.T) {
	f := newFixture(t)
	stderr, _ := filepath.Abs(filepath.Join("testdata", "ls-missing.stderr"))
	m := fakeMise(t, "cat "+shquote.Quote(stderr)+" >&2\necho '[]'")
	if got := runStepOut(t, f, stepEnv(f, "", "tool-a"), m); got != "" {
		t.Fatalf("printed %q", got)
	}
}

func TestRunStepMiseWithoutASkillsCommandSaysToUpdateMise(t *testing.T) {
	f := newFixture(t)
	m := fakeMise(t, "echo \"error: unrecognized subcommand 'skills'\" >&2; exit 2")
	got := runStepOut(t, f, stepEnv(f, "", "tool-a"), m)
	want := "yolo: mise skills: this mise has no `skills` command. Update mise to 2026.9.2 or later.\n"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}
