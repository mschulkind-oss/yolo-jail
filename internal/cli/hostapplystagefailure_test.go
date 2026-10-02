package cli

// hostapplystagefailure_test.go pins that a stage of `yolo host apply` that fails reaches the
// verdict (docs/reference/happy-path-principle.md, rule 5: never report OK over broken). A failure
// in the destinations, overlays, skills, briefing, retire or wrappers stage set only a flag the
// launch gate read, and the verdict read nothing of it: an --assert printed the stage's refusal,
// exited 1, and ended "Nothing to apply — this home is up to date." Each case below makes one stage
// fail for real, in a temp HOME, and runs the whole apply.

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// verdictLine is the report's verdict line: the one line that opens with an outcome's sentence.
func verdictLine(report string) string {
	for _, line := range strings.Split(report, "\n") {
		for _, open := range []string{"Incomplete — ", "An --assert would ", "Nothing to ", "Applied: ",
			"Installed ", "Refused — ", "No packs configured"} {
			if strings.HasPrefix(line, open) {
				return line
			}
		}
	}
	return ""
}

// dryRunDoc runs `yolo host apply --format json` and returns its document, whatever the exit code.
func dryRunDoc(t *testing.T) hostApplyDoc {
	t.Helper()
	var out, errw bytes.Buffer
	hostApply([]string{"--format", "json"}, &out, &errw, false, nil)
	var doc hostApplyDoc
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatalf("stdout is not a JSON document (%v):\n%s\n%s", err, out.String(), errw.String())
	}
	return doc
}

func TestAFailedHostApplyStageReachesTheVerdict(t *testing.T) {
	cases := []struct {
		stage string
		// setup makes the stage fail in a fresh HOME, and returns the stdin the --assert reads.
		setup func(t *testing.T) string
		// dry is whether the dry run meets the failure too: a stage that fails only by writing
		// (the retire's archive) has nothing to fail at in an observe pass.
		dry bool
	}{
		{"destinations", func(t *testing.T) string {
			// A manifest-less pack with no agent pack to borrow a destination from renders nothing.
			zeroCeremonyFixture(t, "")
			return ""
		}, true},
		{"overlays", func(t *testing.T) string {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
			owner := listPack(t, home, "acme", `{"kind":"config","config":[{"agent":"acme",`+
				`"name":"settings","codec":"json","mode":"rmw","path":"~/.acme/settings.json",`+
				`"managed":{"k":"v"}}]}`)
			bogus := listPack(t, home, "bogus", `{"kind":"config-list","surface":"noslash",`+
				`"path":"/extras","add":["one"]}`)
			selectPacks(t, home, owner+","+bogus)
			return ""
		}, true},
		{"skills", func(t *testing.T) string {
			// Two packs shipping one skill name at an unnamespaced destination.
			home := t.TempDir()
			shared := filepath.Join(t.TempDir(), "sflat")
			writeFile(t, filepath.Join(shared, "pack.json"), `{"name":"sflat","description":"s","contributes":[`+
				`{"kind":"skills","from":"skills","into":".codex/skills"}]}`)
			writeFile(t, filepath.Join(shared, "skills", "mine", "SKILL.md"), "---\nname: mine\ndescription: d\n---\nS\n")
			writeFile(t, filepath.Join(localPackSkills(home), "mine", "SKILL.md"), "---\nname: mine\ndescription: d\n---\nL\n")
			selectPacks(t, home, `"codex",{"source":"file://`+shared+`","name":"sflat"}`)
			t.Setenv("HOME", home)
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
			return ""
		}, true},
		{"briefing", func(t *testing.T) string {
			// The local pack's prose in both its old root AGENTS.md and briefing/local.md: yolo
			// will not choose between them.
			_, _, target := legacyLocalPackHome(t, "Old rule.\n")
			writeFile(t, target, "New rule.\n")
			return ""
		}, true},
		{"retire", func(t *testing.T) string {
			// A dropped pack's output, confirmed for retirement, with a file where its archive goes.
			home, _ := dropFixture(t, dropPackJSON)
			applyThenDrop(t, home)
			writeFile(t, string(hostArchiveRoot(archiveBucketRetired)), "not a directory\n")
			return "y\n"
		}, false},
		{"wrappers", func(t *testing.T) string {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
			writeFile(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"),
				`{"packs":["claude"],"host_wrappers":true}`)
			stubDeclaredBins(t)
			writeFile(t, paths.WrapDirUnder(home), "not a directory\n")
			return ""
		}, true},
	}
	for _, c := range cases {
		t.Run(c.stage, func(t *testing.T) {
			defaultReport(t)
			stdin := c.setup(t)
			clause := "the " + c.stage + " stage"

			if c.dry {
				_, report := applyWith(t, false, nil)
				want := "An --assert would be incomplete — " + clause + " would fail (above)."
				if got := verdictLine(report); !strings.HasPrefix(got, "An --assert would be incomplete — ") ||
					!strings.Contains(got, clause+" would fail (above)") {
					t.Errorf("the dry run's verdict is %q, want one naming the failed stage, like %q:\n%s",
						got, want, report)
				}
				if doc := dryRunDoc(t); doc.Outcome != outcomeIncomplete ||
					len(doc.FailedStages) != 1 || doc.FailedStages[0] != c.stage {
					t.Errorf("the document's outcome is %q and failed_stages %v, want %q and [%s]",
						doc.Outcome, doc.FailedStages, outcomeIncomplete, c.stage)
				}
			}

			rc, report := applyWith(t, true, strings.NewReader(stdin))
			if rc == 0 {
				t.Errorf("an --assert whose %s stage failed exited 0:\n%s", c.stage, report)
			}
			got := verdictLine(report)
			if !strings.HasPrefix(got, "Incomplete — ") || !strings.Contains(got, clause+" failed (above)") {
				t.Errorf("the --assert's verdict is %q, want \"Incomplete — %s failed (above); …\":\n%s",
					got, clause, report)
			}
			if strings.Contains(report, "up to date") {
				t.Errorf("an --assert whose %s stage failed says the home is up to date:\n%s", c.stage, report)
			}
		})
	}
}

// The verdict names every failed stage, once each, in one clause, beside the packs whose config was
// not written; and the --assert says whether the rest was applied.
func TestTheIncompleteVerdictNamesEveryFailedStage(t *testing.T) {
	s := &hostApplySurvey{}
	s.noteStageFailure(stageSkills)
	s.noteStageFailure(stageBriefing)
	s.noteStageFailure(stageSkills)
	s.noteRenderFailure("acme", "boom")
	if got := hostApplyOutcome(s, true); got != outcomeIncomplete {
		t.Fatalf("outcome = %q, want %q", got, outcomeIncomplete)
	}
	const blockers = "some of acme's config was not written, and the skills and briefing stages failed (above)"
	if got, want := hostApplyVerdict(s, true), "Incomplete — "+blockers+"; nothing else needed changing."; got != want {
		t.Errorf("verdict\n%s\nwant\n%s", got, want)
	}
	if got, want := hostApplyVerdict(s, false), "An --assert would be incomplete — some of acme's config "+
		"cannot be written, and the skills and briefing stages would fail (above)."; got != want {
		t.Errorf("dry-run verdict\n%s\nwant\n%s", got, want)
	}
}
