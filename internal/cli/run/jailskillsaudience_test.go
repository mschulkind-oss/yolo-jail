package run

// jailskillsaudience_test.go is the CALL-SITE half of the jail's `skills` audience (OQ-BA4).
//
// internal/jailcontent/skillsaudience_test.go pins the filter itself; this pins the two
// wirings that feed it, both of which are one field on a struct literal and both of which
// would leave that filter permanently inert if dropped:
//
//   - packSkillTargets must carry the DESTINATION's `agent` (prepare.go). Without it every
//     target's identity is "" and no addressed source ever matches — so scoping would
//     silently delete content instead of routing it.
//   - packSkillSourceDirs must carry each SOURCE's `agents` (packs.go). Without it every
//     source is a broadcast and nothing is scoped at all.
//
// Deleting either leaves internal/jailcontent green, which is exactly the shape AGENTS.md
// records this repo shipping five times.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jailcontent"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// skillsAudienceLaunch configures two agent packs, each naming its own skills destination and
// declaring the identity that destination answers to, plus one content pack whose skills are
// addressed to just the first. Returns the Options and the two destinations.
func skillsAudienceLaunch(t *testing.T, addressed string) *Options {
	t.Helper()
	home := packHome(t)
	base := t.TempDir()
	var entries []string
	for _, a := range []string{"alphacli", "betacli"} {
		dir := filepath.Join(base, a)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		writePack(t, dir, `{"name":"`+a+`","contributes":[`+
			`{"kind":"program","bin":"`+a+`","via":"npm","package":"`+a+`"},`+
			`{"kind":"skills","into":".`+a+`/skills","agent":"`+a+`"}]}`)
		entries = append(entries, `{"source":"file://`+dir+`","name":"`+a+`"}`)
	}
	house := filepath.Join(base, "house")
	if err := os.MkdirAll(filepath.Join(house, "skills", "alpha-only"), 0o755); err != nil {
		t.Fatal(err)
	}
	writePack(t, house, `{"name":"house","contributes":[`+
		`{"kind":"skills","from":"skills","agents":["`+addressed+`"]}]}`)
	if err := os.WriteFile(filepath.Join(house, "skills", "alpha-only", "SKILL.md"),
		[]byte("---\nname: alpha-only\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	entries = append(entries, `{"source":"file://`+house+`","name":"house"}`)
	writeUserPacks(t, home, "["+joinCommas(entries)+"]")

	o := goldenOptions(t.TempDir(), home)
	o.Stdout = discardBuf()
	return o
}

// joinCommas is strings.Join(x, ",") without pulling strings into this file's imports twice.
func joinCommas(xs []string) string {
	out := ""
	for i, x := range xs {
		if i > 0 {
			out += ","
		}
		out += x
	}
	return out
}

// stagedSkillsFor runs the whole jail content path — stagePacks, then refreshJailBriefings,
// which is what calls PrepareSkills — and returns {destination → staged skill subdirs}.
func stagedSkillsFor(t *testing.T, o *Options) map[string][]string {
	t.Helper()
	jailcontent.SetPackSkillDirs(nil)
	jailcontent.SetPackSkillTargets(nil)
	t.Cleanup(func() { jailcontent.SetPackSkillDirs(nil); jailcontent.SetPackSkillTargets(nil) })

	root, packs, briefings, err := o.stagePacks("yolo-test-skillsaudience")
	if err != nil {
		t.Fatalf("stagePacks: %v", err)
	}
	staging, err := o.refreshJailBriefings("yolo-test-skillsaudience", jsonx.NewOrderedMap(),
		"podman", stagedPacks{root: root, packs: packs, briefings: briefings})
	if err != nil {
		t.Fatalf("refreshJailBriefings: %v", err)
	}
	// The two destinations the fixture's own pack.json files name. Spelled out rather than
	// read back from jailcontent, because reading the targets back from the thing under test
	// would let a wiring that set no destinations at all pass with an empty map.
	out := map[string][]string{}
	for pack, dest := range map[string]string{
		"alphacli": ".alphacli/skills",
		"betacli":  ".betacli/skills",
	} {
		entries, rerr := os.ReadDir(filepath.Join(staging, jailcontent.SkillStagingName(pack)))
		if rerr != nil {
			t.Fatalf("no staging dir for %s: %v", dest, rerr)
		}
		for _, e := range entries {
			if e.IsDir() {
				out[dest] = append(out[dest], e.Name())
			}
		}
	}
	return out
}

// THE WIRING, end to end from two pack.json files to two staging dirs on disk.
func TestJailStagesAnAddressedSkillOnlyForItsAudience(t *testing.T) {
	got := stagedSkillsFor(t, skillsAudienceLaunch(t, "alphacli"))

	if !containsName(got[".alphacli/skills"], "alpha-only") {
		t.Errorf("the addressed skill never reached alphacli's staging dir: %v",
			got[".alphacli/skills"])
	}
	if containsName(got[".betacli/skills"], "alpha-only") {
		t.Errorf("an alphacli-addressed skill was staged for betacli — the source list is "+
			"GLOBAL, so the destination's declared identity is the only thing that can stop "+
			"it: %v", got[".betacli/skills"])
	}
	// And the built-in suite still reaches both, or a filter one layer too wide would look
	// like a pass here.
	for _, dest := range []string{".alphacli/skills", ".betacli/skills"} {
		if len(got[dest]) == 0 {
			t.Errorf("%s staged nothing at all — the built-in suite is core's own content and "+
				"is not addressable", dest)
		}
	}
}

// P2 through the same wiring: a content pack naming NO audience still reaches both.
func TestJailStagesAnUnaddressedSkillEverywhere(t *testing.T) {
	home := packHome(t)
	base := t.TempDir()
	var entries []string
	for _, a := range []string{"alphacli", "betacli"} {
		dir := filepath.Join(base, a)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		writePack(t, dir, `{"name":"`+a+`","contributes":[`+
			`{"kind":"skills","into":".`+a+`/skills","agent":"`+a+`"}]}`)
		entries = append(entries, `{"source":"file://`+dir+`","name":"`+a+`"}`)
	}
	house := filepath.Join(base, "house")
	if err := os.MkdirAll(filepath.Join(house, "skills", "everyones"), 0o755); err != nil {
		t.Fatal(err)
	}
	// NO skills contribution at all — which is how a content pack broadcasts, and the only
	// shape available to it: `into` is required unless the contribution names an audience
	// instead (they are two answers to one question), so "broadcast my skills" is expressed by
	// silence and picked up by SkillsSources' zero-ceremony fallback.
	writePack(t, house, `{"name":"house"}`)
	if err := os.WriteFile(filepath.Join(house, "skills", "everyones", "SKILL.md"),
		[]byte("---\nname: everyones\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	entries = append(entries, `{"source":"file://`+house+`","name":"house"}`)
	writeUserPacks(t, home, "["+joinCommas(entries)+"]")

	o := goldenOptions(t.TempDir(), home)
	o.Stdout = discardBuf()
	got := stagedSkillsFor(t, o)
	for _, dest := range []string{".alphacli/skills", ".betacli/skills"} {
		if !containsName(got[dest], "everyones") {
			t.Errorf("%s lost the broadcast skill: %v", dest, got[dest])
		}
	}
}

func containsName(names []string, want string) bool {
	for _, n := range names {
		if n == want {
			return true
		}
	}
	return false
}
