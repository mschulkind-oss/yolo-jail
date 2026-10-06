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
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// verdictLine is the report's verdict line: the last line that opens with an outcome's sentence.
// The last, because an empty `packs` opens its report with a header that reads like one ("No
// packs configured — nothing to apply, so this run only retires …").
func verdictLine(report string) string {
	verdict := ""
	for _, line := range strings.Split(report, "\n") {
		for _, open := range []string{"Incomplete — ", "An --assert would ", "Nothing to ", "Applied: ",
			"Installed ", "Refused — ", "No packs configured"} {
			if strings.HasPrefix(line, open) {
				verdict = line
			}
		}
	}
	return verdict
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
		// name is the subtest's, when one stage is met in two places.
		name  string
		stage string
		// says is what the verdict names the failed stage by, when that is not "the <stage>
		// stage": the words its lines above lead with, which for these two stages are a kind's.
		says string
		// setup makes the stage fail in a fresh HOME, and returns the stdin the --assert reads.
		setup func(t *testing.T) string
		// dry is whether the dry run meets the failure too: a stage that fails only by writing
		// (the retire's archive) has nothing to fail at in an observe pass.
		dry bool
	}{
		{"", "destinations", "the skills and briefing stages", func(t *testing.T) string {
			// A manifest-less pack with no agent pack to borrow a destination from renders nothing.
			zeroCeremonyFixture(t, "")
			return ""
		}, true},
		{"", "overlays", "the config-list stage", func(t *testing.T) string {
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
		// A files slot whose `register` names a surface its own pack does not declare: a problem
		// the overlay collector reports, about a `files` declaration, so its line leads with
		// `files` and the verdict names it so, never `config-overlay`, which the author never wrote.
		{"overlays, from a files slot", "overlays", "the files stage", func(t *testing.T) string {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
			owner := listPack(t, home, "acme", `{"kind":"config","config":[{"agent":"acme",`+
				`"name":"settings","codec":"json","mode":"rmw","path":"~/.acme/settings.json",`+
				`"managed":{"k":"v"}}]},`+
				`{"kind":"files","agent":"acme","into":".acme/packs",`+
				`"register":{"surface":"acme/other","path":"/packages"}}`)
			selectPacks(t, home, owner)
			return ""
		}, true},
		{"", "skills", "", func(t *testing.T) string {
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
		{"", "briefing", "", func(t *testing.T) string {
			// The local pack's prose in both its old root AGENTS.md and briefing/local.md: yolo
			// will not choose between them.
			_, _, target := legacyLocalPackHome(t, "Old rule.\n")
			writeFile(t, target, "New rule.\n")
			return ""
		}, true},
		{"", "retire", "", func(t *testing.T) string {
			// A dropped pack's output, confirmed for retirement, with a file where its archive goes.
			home, _ := dropFixture(t, dropPackJSON)
			applyThenDrop(t, home)
			writeFile(t, string(hostArchiveRoot(archiveBucketRetired)), "not a directory\n")
			return "y\n"
		}, false},
		{"", "wrappers", "", func(t *testing.T) string {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
			writeFile(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"),
				`{"packs":["claude"],"host_wrappers":true}`)
			stubDeclaredBins(t)
			writeFile(t, paths.WrapDirUnder(home), "not a directory\n")
			return ""
		}, true},
		// AN EMPTY `packs` takes a branch of its own, which runs the retire and the wrappers too
		// (with no pack left, everything delivered is an orphan). It ended "No packs configured —
		// nothing to apply, and nothing left to retire." over either one failing.
		{"retire, with no packs", "retire", "", func(t *testing.T) string {
			home, _ := dropFixture(t, dropPackJSON)
			if rc, report := applyWith(t, true, nil); rc != 0 {
				t.Fatalf("first apply rc=%d\n%s", rc, report)
			}
			selectPacks(t, home, "")
			writeFile(t, string(hostArchiveRoot(archiveBucketRetired)), "not a directory\n")
			return "y\n"
		}, false},
		{"briefing, with no packs", "briefing", "", func(t *testing.T) string {
			// The composed briefing a dropped pack left, with a file where its archive goes.
			home, _ := dropFixture(t, dropPackJSON)
			if rc, report := applyWith(t, true, nil); rc != 0 {
				t.Fatalf("first apply rc=%d\n%s", rc, report)
			}
			selectPacks(t, home, "")
			writeFile(t, string(hostArchiveRoot(string(packdecl.KindBriefing))), "not a directory\n")
			return "y\n"
		}, false},
		{"wrappers, with no packs", "wrappers", "", func(t *testing.T) string {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
			writeFile(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"),
				`{"packs":[],"host_wrappers":true}`)
			writeFile(t, paths.WrapDirUnder(home), "not a directory\n")
			return ""
		}, true},
	}
	for _, c := range cases {
		name := c.name
		if name == "" {
			name = c.stage
		}
		t.Run(name, func(t *testing.T) {
			defaultReport(t)
			stdin := c.setup(t)
			clause := "the " + c.stage + " stage"
			if c.says != "" {
				clause = c.says
			}

			if c.dry {
				_, report := applyWith(t, false, nil)
				want := "An --assert would be incomplete — " + clause + " would fail (above)."
				if got := verdictLine(report); !strings.HasPrefix(got, "An --assert would be incomplete — ") ||
					!strings.Contains(got, clause+" would fail (above)") {
					t.Errorf("the dry run's verdict is %q, want one naming the failed stage, like %q:\n%s",
						got, want, report)
				}
				assertStageNamedAsItsLinesSay(t, clause, report)
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
			assertStageNamedAsItsLinesSay(t, clause, report)
		})
	}
}

// assertStageNamedAsItsLinesSay checks that every name the verdict's stage clause ("the skills
// and briefing stages") gives is a word some line above it carries, followed later on that line by
// `refused` or `failed`: the reader of the verdict finds the failure by the word the verdict used.
// It used to name "the destinations stage" and "the overlays stage" over lines that read
// `skills     refused — …` and `config-list refused — …`.
func assertStageNamedAsItsLinesSay(t *testing.T, clause, report string) {
	t.Helper()
	names := strings.TrimPrefix(clause, "the ")
	names = strings.TrimSuffix(strings.TrimSuffix(names, " stages"), " stage")
	for _, name := range strings.Split(strings.ReplaceAll(names, " and ", ", "), ", ") {
		found := false
		for _, line := range strings.Split(report, "\n") {
			if line == verdictLine(report) {
				continue
			}
			fields := strings.Fields(line)
			for i, f := range fields {
				if f != name {
					continue
				}
				for _, after := range fields[i+1:] {
					if strings.HasPrefix(after, "refused") || strings.HasPrefix(after, "failed") {
						found = true
					}
				}
			}
		}
		if !found {
			t.Errorf("the verdict names %q, but no line above it says %q refused or failed:\n%s",
				name, name, report)
		}
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

// A config whose providers or profiles cannot be composed for the host is a refusal, not a failed
// stage among others: the --assert writes nothing at all. The dry run's document said
// `incomplete` and "An --assert would be incomplete — the inputs stage would fail (above).", the
// outcome whose --assert "writes the rest", in a word ("inputs") the report never prints.
func TestAnInputsRefusalIsARefusalInTheDocument(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	// OUT OF A JAIL, pinned rather than inherited: in a jail (YOLO_VERSION set) the retired key is
	// a WARNING, the config being the host-generated snapshot, so the host apply had nothing to
	// refuse and wrote the home. Unset, not emptied, because internal/loopholes reads the
	// variable's presence.
	t.Setenv("YOLO_VERSION", "")
	os.Unsetenv("YOLO_VERSION")
	// The retired `use_profiles` key: every launch refuses it, and so does the host apply.
	selectPacks(t, home, `"claude"`)
	writeFile(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"),
		`{"packs":["claude"],"use_profiles":{"claude":"bedrock"}}`)

	doc := dryRunDoc(t)
	if doc.Outcome != outcomeRefused || len(doc.FailedStages) != 1 || doc.FailedStages[0] != stageInputs {
		t.Errorf("the document's outcome is %q and failed_stages %v, want %q and [%s]",
			doc.Outcome, doc.FailedStages, outcomeRefused, stageInputs)
	}
	const want = "An --assert would REFUSE: your config's providers or profiles are refused (above), " +
		"so nothing would be written."
	if doc.Verdict != want {
		t.Errorf("the document's verdict is\n%s\nwant\n%s", doc.Verdict, want)
	}

	before := hashTree(t, home)
	rc, report := applyWith(t, true, nil)
	if rc == 0 || !strings.Contains(report, "host apply: refused") {
		t.Errorf("the --assert did not refuse (rc=%d):\n%s", rc, report)
	}
	if after := hashTree(t, home); after != before {
		t.Errorf("the refused --assert wrote into the home:\n%s", report)
	}
}
