package run

// unmatchedaudience_test.go pins the jail launch's R1 report (docs/reference/agent-briefings.md#ba-r1):
// addressed content whose audience is in this jail's vocabulary but reaches no destination of
// its kind is REPORTED at launch, and the launch proceeds.
//
// THROUGH stagePacks, not unmatchedAudiences alone: a test of the helper stays green with the
// call deleted from the launch, which is the exact shape this item was filed for (the host
// notch printed the report; the jail notch printed nothing). The agreement tests at the bottom
// pin the report's notion of "reaches a destination" to what the jail actually composes, stages
// and mounts, because a report computed a different way from delivery is a report that lies.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jailcontent"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// unmatchedLaunch configures two agent packs and one content pack, and returns Options ready
// for stagePacks plus the path of the content pack.
//
//   - `alphacli` installs a CLI and declares a briefing destination with `agent: alphacli`, plus
//     whatever `alphaExtra` adds (a leading-comma list of contributions, "" for none) — so by
//     default no skills destination and no files slot. A destination answering to `alphacli`
//     can only come from here: the name has one owning pack (P5), and a second pack declaring
//     one is refused by the name-collision pre-flight.
//   - `betacli` installs a CLI and declares no destination at all. It owns its name through the
//     `program` bin, so the vocabulary pre-flight accepts `agents: ["betacli"]`.
//   - `house` carries whatever `contributes` the caller passes, plus the files they name.
func unmatchedLaunch(t *testing.T, alphaExtra, houseContributes string, files map[string]string) *Options {
	t.Helper()
	home := packHome(t)
	base := t.TempDir()
	mk := func(name, manifest string) string {
		dir := filepath.Join(base, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		writePack(t, dir, manifest)
		return dir
	}
	alpha := mk("alphacli", `{"name":"alphacli","contributes":[`+
		`{"kind":"program","bin":"alphacli","via":"npm","package":"alphacli"},`+
		`{"kind":"briefing","into":".alpha/AGENTS.md","agent":"alphacli"}`+alphaExtra+`]}`)
	beta := mk("betacli", `{"name":"betacli","contributes":[`+
		`{"kind":"program","bin":"betacli","via":"npm","package":"betacli"}]}`)
	house := mk("house", `{"name":"house","contributes":[`+houseContributes+`]}`)
	for rel, body := range files {
		path := filepath.Join(house, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeUserPacks(t, home,
		`[{"source":"file://`+alpha+`","name":"alphacli"},`+
			`{"source":"file://`+beta+`","name":"betacli"},`+
			`{"source":"file://`+house+`","name":"house"}]`)
	return &Options{Workspace: t.TempDir(), Stdout: &strings.Builder{}}
}

// THE CALL-SITE TEST: each kind's unmatched audience is reported by the launch, names the pack,
// the kind, the source and the audience, says what is NOT the remedy — and does not refuse.
func TestLaunchReportsAnUnmatchedAudience(t *testing.T) {
	for _, tc := range []struct {
		name, contributes, source string
		files                     map[string]string
	}{
		{
			// A briefing destination EXISTS (alphacli's) and is receiving other content; the
			// audience still matches nothing, which is the shape the kind-level orphan line
			// cannot describe.
			name:        "briefing",
			contributes: `{"kind":"briefing","from":"prose/beta.md","agents":["betacli"]}`,
			source:      "prose/beta.md",
			files:       map[string]string{"prose/beta.md": "Beta-only rule.\n"},
		},
		{
			// alphacli owns a briefing destination but no skills destination.
			name:        "skills",
			contributes: `{"kind":"skills","agents":["alphacli"]}`,
			source:      "skills",
			files:       map[string]string{"skills/s/SKILL.md": "---\nname: s\ndescription: d\n---\n"},
		},
		{
			// No selected pack declares a files slot for alphacli.
			name:        "files",
			contributes: `{"kind":"files","from":"extras/tool.json","agents":["alphacli"]}`,
			source:      "extras/tool.json",
			files:       map[string]string{"extras/tool.json": "{}\n"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := unmatchedLaunch(t, "", tc.contributes, tc.files)
			if _, _, _, err := o.stagePacks("yolo-test-unmatched-" + tc.name); err != nil {
				t.Fatalf("an unmatched audience must be REPORTED, never refused (R1): %v", err)
			}
			out := o.Stdout.(*strings.Builder).String()
			for _, want := range []string{
				"Warning: pack house",
				tc.name + " from `" + tc.source + "` is addressed to",
				"no " + tc.name + " destination in this jail declares a matching `agent`",
				"declaring `into` is not the remedy",
			} {
				if !strings.Contains(out, want) {
					t.Errorf("the launch did not report the unmatched %s audience (missing %q); "+
						"stdout:\n%s", tc.name, want, out)
				}
			}
		})
	}
}

// The control: an audience that DOES match a destination prints no such line, so the report
// above is about the mismatch rather than about addressed content in general.
func TestLaunchDoesNotReportAMatchedAudience(t *testing.T) {
	o := unmatchedLaunch(t, "", `{"kind":"briefing","from":"prose/alpha.md","agents":["alphacli"]}`,
		map[string]string{"prose/alpha.md": "Alpha-only rule.\n"})
	if _, _, _, err := o.stagePacks("yolo-test-matched"); err != nil {
		t.Fatalf("a matched audience must launch: %v", err)
	}
	if out := o.Stdout.(*strings.Builder).String(); strings.Contains(out, "delivers it to no agent") {
		t.Errorf("a matched audience was reported as unmatched:\n%s", out)
	}
}

// A broadcast names no audience, so it can never be an unmatched one — even with no destination
// of its kind selected at all (pack-system.md: "With no destination selected it is simply
// unused, never fatal").
func TestLaunchDoesNotReportABroadcast(t *testing.T) {
	o := unmatchedLaunch(t, "", `{"kind":"skills"}`,
		map[string]string{"skills/s/SKILL.md": "---\nname: s\ndescription: d\n---\n"})
	if _, _, _, err := o.stagePacks("yolo-test-broadcast"); err != nil {
		t.Fatalf("a broadcast must launch: %v", err)
	}
	if out := o.Stdout.(*strings.Builder).String(); strings.Contains(out, "delivers it to no agent") {
		t.Errorf("a broadcast was reported as an unmatched audience:\n%s", out)
	}
}

// AGREEMENT WITH COMPOSITION, briefing: the report says "unmatched" exactly when the jail's own
// briefing composition puts the prose in no destination. Through refreshJailBriefings (the
// jailBriefings helper) over the packs and proses stagePacks itself returned.
func TestUnmatchedBriefingAudienceAgreesWithComposition(t *testing.T) {
	for _, tc := range []struct {
		audience  string
		unmatched bool
	}{{"betacli", true}, {"alphacli", false}} {
		t.Run(tc.audience, func(t *testing.T) {
			o := unmatchedLaunch(t, "",
				`{"kind":"briefing","from":"prose/x.md","agents":["`+tc.audience+`"]}`,
				map[string]string{"prose/x.md": "Addressed rule.\n"})
			_, loaded, proses, err := o.stagePacks("yolo-test-agree-" + tc.audience)
			if err != nil {
				t.Fatal(err)
			}
			reported := len(unmatchedAudiences(loaded)) > 0
			delivered := false
			for _, body := range jailBriefings(t, loaded, proses) {
				if strings.Contains(body, "Addressed rule.") {
					delivered = true
				}
			}
			if reported != tc.unmatched || delivered == reported {
				t.Errorf("audience %q: reported unmatched=%v, composed into a destination=%v — "+
					"the report must say unmatched exactly when composition delivers nothing",
					tc.audience, reported, delivered)
			}
		})
	}
}

// AGREEMENT WITH DELIVERY, files: the report says "unmatched" exactly when packFilesTargets —
// the function the mount half emits from — yields no target for the addressed contribution.
// filesSlotAgents restates packFilesTargets' slot rule, and this is what keeps the two one rule.
func TestUnmatchedFilesAudienceAgreesWithPackFilesTargets(t *testing.T) {
	for _, tc := range []struct {
		name, slot string
		unmatched  bool
	}{
		{"no slot", "", true},
		{"slot declared", `,{"kind":"files","agent":"alphacli","into":".alpha/extras"}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := unmatchedLaunch(t, tc.slot,
				`{"kind":"files","from":"extras/tool.json","agents":["alphacli"]}`,
				map[string]string{"extras/tool.json": "{}\n"})
			_, loaded, _, err := o.stagePacks("yolo-test-files-" + strings.ReplaceAll(tc.name, " ", "-"))
			if err != nil {
				t.Fatal(err)
			}
			reported := len(unmatchedAudiences(loaded)) > 0
			targeted := false
			for _, target := range packFilesTargets(loaded) {
				if target.Pack == "house" && target.From == "extras/tool.json" {
					targeted = true
				}
			}
			if reported != tc.unmatched || targeted == reported {
				t.Errorf("%s: reported unmatched=%v, packFilesTargets delivered=%v — the report "+
					"must say unmatched exactly when the mount half emits nothing",
					tc.name, reported, targeted)
			}
		})
	}
}

// AGREEMENT WITH STAGING, skills: the report says "unmatched" exactly when the jail's own
// skills stager puts the addressed tree into no staging dir. THROUGH THE REAL STAGER, not a
// restatement of its filter: stagePacks records the sources (SetPackSkillDirs), then
// refreshJailBriefings sets the targets and runs jailcontent.PrepareSkills — the same two
// steps a launch takes — and "delivered" is read off the staged files on disk. A test that
// re-derived delivery from packSkillTargets alone would stay green if the stager's audience
// filter (sourceAddressesAgent) stopped routing addressed content, leaving the report saying
// "delivered" about a skill the jail stages nowhere.
func TestUnmatchedSkillsAudienceAgreesWithSkillStaging(t *testing.T) {
	for _, tc := range []struct {
		name, dest string
		unmatched  bool
	}{
		{"no skills destination", "", true},
		{"skills destination declared", `,{"kind":"skills","into":".alpha/skills","agent":"alphacli"}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// The stager reads process-wide records; start from none and leave none behind,
			// as stagedSkillsFor does.
			jailcontent.SetPackSkillDirs(nil)
			jailcontent.SetPackSkillTargets(nil)
			t.Cleanup(func() { jailcontent.SetPackSkillDirs(nil); jailcontent.SetPackSkillTargets(nil) })

			o := unmatchedLaunch(t, tc.dest, `{"kind":"skills","agents":["alphacli"]}`,
				map[string]string{"skills/s/SKILL.md": "---\nname: s\ndescription: d\n---\n"})
			cname := "yolo-test-skills-" + strings.ReplaceAll(tc.name, " ", "-")
			root, loaded, proses, err := o.stagePacks(cname)
			if err != nil {
				t.Fatal(err)
			}
			reported := len(unmatchedAudiences(loaded)) > 0

			// goldenOptions for the refresh, because refreshJailBriefings reads the host
			// seams (PathExists, Getenv) that unmatchedLaunch's bare Options leaves unset.
			g := goldenOptions(o.Workspace, os.Getenv("HOME"))
			g.Stdout = discardBuf()
			staging, err := g.refreshJailBriefings(cname, jsonx.NewOrderedMap(), "podman",
				stagedPacks{root: root, packs: loaded, briefings: proses})
			if err != nil {
				t.Fatalf("refreshJailBriefings: %v", err)
			}
			// Any staging dir, not just alphacli's: the report's claim is "delivers it to no
			// agent", so a copy under any destination falsifies "unmatched".
			staged, err := filepath.Glob(filepath.Join(staging, jailcontent.SkillStagingName("*"), "s", "SKILL.md"))
			if err != nil {
				t.Fatal(err)
			}
			delivered := len(staged) > 0
			if reported != tc.unmatched || delivered == reported {
				t.Errorf("%s: reported unmatched=%v, staged by PrepareSkills=%v (%v) — the report "+
					"must say unmatched exactly when the stager delivers the skill to no agent",
					tc.name, reported, delivered, staged)
			}
		})
	}
}
