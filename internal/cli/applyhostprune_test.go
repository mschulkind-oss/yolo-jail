package cli

// applyhostprune_test.go pins the retire-a-dropped-pack behavior at the COMMAND level, which
// is the only level it exists at: the defect was that nothing ever asked about a pack absent
// from `entries`, so every test here drives `applyHost` with a config the pack has LEFT.
//
// Every test uses a t.TempDir() home and points XDG_CONFIG_HOME inside it. The real $HOME is
// never read or written — that is the one failure whose cost is not bounded by the test run.

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

// dropPackJSON declares the four kinds a pack can render into a real home, so a test can
// assert that the CONFIRMED half (skills, files) and the unconditional half (briefing) keep
// their separate behavior.
const dropPackJSON = `{"name":"dropme","description":"d","contributes":[
  {"kind":"skills","from":"skills","into":".claude/skills","tier":"flat"},
  {"kind":"files","from":"bin","into":".claude/bin"},
  {"kind":"briefing","from":"AGENTS.md","into":".claude/CLAUDE.md"}]}`

// dropFixture writes a pack tree and a user config selecting it, and returns the home and the
// pack dir. Selecting `claude` alongside is what makes the skills DESTINATION discoverable
// after the pack leaves — the same shape the briefing prune relies on.
func dropFixture(t *testing.T, packJSON string) (home, packDir string) {
	t.Helper()
	home = t.TempDir()
	packDir = filepath.Join(t.TempDir(), "dropme")
	writeFile(t, filepath.Join(packDir, "pack.json"), packJSON)
	writeFile(t, filepath.Join(packDir, "skills", "demo", "SKILL.md"),
		"---\nname: demo\ndescription: d\n---\nDemo body.\n")
	writeFile(t, filepath.Join(packDir, "bin", "pick.sh"), "#!/bin/sh\necho pick\n")
	writeFile(t, filepath.Join(packDir, "AGENTS.md"), "Dropme prose.\n")

	selectPacks(t, home, `"claude",{"source":"file://`+packDir+`","name":"dropme"}`)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	return home, packDir
}

// selectPacks rewrites the user-scope config's `packs` list. `packs` is user-scope only, so
// this is the ONLY place a test can drop a pack from.
func selectPacks(t *testing.T, home, list string) {
	t.Helper()
	writeFile(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"),
		`{"packs":[`+list+`]}`)
}

// applyWith runs one apply and returns its rc plus the whole report.
func applyWith(t *testing.T, write bool, stdin io.Reader) (int, string) {
	t.Helper()
	var out, errw bytes.Buffer
	rc := applyHost(&out, &errw, false, write, stdin)
	return rc, out.String() + errw.String()
}

// deliveredPaths are the home-relative paths a dropPackJSON apply writes.
func deliveredPaths(home string) (skill, file string) {
	return filepath.Join(home, ".claude", "skills", "demo"),
		filepath.Join(home, ".claude", "bin", "pick.sh")
}

func mustExist(t *testing.T, path, why string) {
	t.Helper()
	if _, err := os.Lstat(path); err != nil {
		t.Errorf("%s should still be there (%s): %v", path, why, err)
	}
}

func mustNotExist(t *testing.T, path, why string) {
	t.Helper()
	if _, err := os.Lstat(path); err == nil {
		t.Errorf("%s should be gone (%s)", path, why)
	}
}

// archivedUnder returns every file the CONFIRMED retire archived, for asserting that retirement
// MOVED rather than deleted (ruling R2).
//
// The briefing subtree is excluded, and that exclusion is the R4 asymmetry made visible in the
// helper rather than left to each test: since §6a a briefing destination is a whole yolo-owned
// file, so its retirement archives too — but UNCONFIRMED, because every byte being moved is a
// byte yolo wrote. A helper that counted both would make "an unconfirmed retire archived nothing"
// unassertable. archivedBriefings is the other half.
func archivedUnder(t *testing.T, home string) []string {
	t.Helper()
	var out []string
	for _, rel := range archivedAll(t, home) {
		if strings.Contains(rel, string(filepath.Separator)+"briefing"+string(filepath.Separator)) {
			continue
		}
		out = append(out, rel)
	}
	return out
}

// archivedBriefings returns every file the BRIEFING retire archived (the `<stamp>/briefing/…`
// generation subtree).
func archivedBriefings(t *testing.T, home string) []string {
	t.Helper()
	var out []string
	for _, rel := range archivedAll(t, home) {
		if strings.Contains(rel, string(filepath.Separator)+"briefing"+string(filepath.Separator)) {
			out = append(out, rel)
		}
	}
	return out
}

// archivedAll lists every file under the shared host-render archive root, generation-relative.
func archivedAll(t *testing.T, home string) []string {
	t.Helper()
	root := filepath.Join(home, ".local", "share", "yolo-jail", "archive", "skills")
	var out []string
	_ = filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
		if err == nil && fi != nil && !fi.IsDir() {
			rel, _ := filepath.Rel(root, p)
			out = append(out, rel)
		}
		return nil
	})
	return out
}

// applyThenDrop applies with the pack selected, then rewrites the config without it — the
// lifecycle the whole feature is about.
func applyThenDrop(t *testing.T, home string) {
	t.Helper()
	if rc, report := applyWith(t, true, nil); rc != 0 {
		t.Fatalf("first apply rc=%d\n%s", rc, report)
	}
	skill, file := deliveredPaths(home)
	mustExist(t, skill, "the first apply delivered it")
	mustExist(t, file, "the first apply delivered it")
	selectPacks(t, home, `"claude"`)
}

// THE GOAL. A dropped pack's skill and file are archived out of the real home once the user
// confirms, and the report says where each one went.
func TestApplyHostRetiresDroppedPackOutputOnConfirm(t *testing.T) {
	home, _ := dropFixture(t, dropPackJSON)
	applyThenDrop(t, home)

	rc, report := applyWith(t, true, strings.NewReader("y\n"))
	if rc != 0 {
		t.Fatalf("a confirmed retire rc=%d\n%s", rc, report)
	}
	skill, file := deliveredPaths(home)
	mustNotExist(t, skill, "its pack left the config and the user confirmed")
	mustNotExist(t, file, "its pack left the config and the user confirmed")

	// R2: archived, not deleted — and both paths' CONTENT is recoverable.
	archived := archivedUnder(t, home)
	for _, want := range []string{"demo/SKILL.md", "pick.sh"} {
		found := false
		for _, got := range archived {
			if strings.HasSuffix(got, want) {
				found = true
			}
		}
		if !found {
			t.Errorf("%s must be recoverable from the archive, got %v", want, archived)
		}
	}
	// The report names each retired path AND its destination: an archive the user cannot find
	// is a deletion from their point of view.
	for _, want := range []string{"demo", "pick.sh", "archive", "no longer configured"} {
		if !strings.Contains(report, want) {
			t.Errorf("the report must contain %q:\n%s", want, report)
		}
	}
	// The record must no longer claim the pack owns paths that are gone, or the next apply
	// would report them forever.
	if owners := manifestOwners(t, home); len(owners) != 0 {
		t.Errorf("the ownership record should be empty after retirement, got %v", owners)
	}
}

// FAIL-CLOSED. With no stdin — a CI or scripted `apply --host --assert` — nothing is moved.
// A confirmation nobody can answer must not default to touching a real home.
func TestApplyHostRetireFailsClosedWithoutStdin(t *testing.T) {
	home, _ := dropFixture(t, dropPackJSON)
	applyThenDrop(t, home)

	rc, report := applyWith(t, true, nil)
	skill, file := deliveredPaths(home)
	mustExist(t, skill, "no stdin could confirm the removal")
	mustExist(t, file, "no stdin could confirm the removal")
	if archived := archivedUnder(t, home); len(archived) != 0 {
		t.Errorf("an unconfirmed retire archived something: %v", archived)
	}
	// The rc is deliberately unchanged: nothing the user asked for failed, and a permanent
	// non-zero exit would make every scripted apply after any drop look broken.
	if rc != 0 {
		t.Errorf("an unconfirmed retire must not fail the apply, rc=%d\n%s", rc, report)
	}
	if !strings.Contains(report, "still in your home") {
		t.Errorf("the report must say the files stayed:\n%s", report)
	}
}

// DECLINING leaves EVERYTHING — no partial apply, and the ownership record intact so a later
// `y` can still retire them.
func TestApplyHostRetireDeclineLeavesEverything(t *testing.T) {
	home, _ := dropFixture(t, dropPackJSON)
	applyThenDrop(t, home)

	before := manifestOwners(t, home)
	if _, report := applyWith(t, true, strings.NewReader("n\n")); !strings.Contains(
		report, "Nothing was moved") {
		t.Errorf("a decline must say nothing was moved:\n%s", report)
	}
	skill, file := deliveredPaths(home)
	mustExist(t, skill, "the user declined")
	mustExist(t, file, "the user declined")
	if got := manifestOwners(t, home); len(got) != len(before) {
		t.Errorf("a decline changed the ownership record: %v -> %v", before, got)
	}
	// And a later `y` still works — declining is a deferral, not a dead end.
	if rc, report := applyWith(t, true, strings.NewReader("y\n")); rc != 0 {
		t.Fatalf("retire after a decline rc=%d\n%s", rc, report)
	}
	mustNotExist(t, skill, "the user confirmed on the second run")
}

// OBSERVE NEVER PROMPTS AND NEVER WRITES. It reports what an --assert would archive, which is
// how the user learns about the paths before any prompt exists.
func TestApplyHostRetireObserveReportsWithoutWriting(t *testing.T) {
	home, _ := dropFixture(t, dropPackJSON)
	applyThenDrop(t, home)

	rc, report := applyWith(t, false, nil)
	if rc != 0 {
		t.Fatalf("observe rc=%d\n%s", rc, report)
	}
	if strings.Contains(report, "[y/N]") {
		t.Errorf("observe must not prompt — it writes nothing:\n%s", report)
	}
	if !strings.Contains(report, "would archive") {
		t.Errorf("observe must report what an --assert would archive:\n%s", report)
	}
	skill, file := deliveredPaths(home)
	mustExist(t, skill, "observe writes nothing")
	mustExist(t, file, "observe writes nothing")
}

// NO PROMPT WHEN NOTHING WOULD BE REMOVED. A confirmation that fires on every run trains
// people to answer it blind, so the ordinary case — no pack dropped — must be silent, and a
// second apply after a completed retirement must be silent too.
func TestApplyHostRetireDoesNotPromptWithNothingToRetire(t *testing.T) {
	home, _ := dropFixture(t, dropPackJSON)
	if rc, report := applyWith(t, true, nil); rc != 0 || strings.Contains(report, "[y/N]") {
		t.Fatalf("an apply with no pack dropped must not prompt, rc=%d\n%s", rc, report)
	}
	selectPacks(t, home, `"claude"`)
	if rc, report := applyWith(t, true, strings.NewReader("y\n")); rc != 0 {
		t.Fatalf("retire rc=%d\n%s", rc, report)
	}
	rc, report := applyWith(t, true, nil)
	if rc != 0 {
		t.Fatalf("the apply after a completed retirement rc=%d\n%s", rc, report)
	}
	if strings.Contains(report, "[y/N]") || strings.Contains(report, "still in your home") {
		t.Errorf("a second apply after retirement must be silent:\n%s", report)
	}
}

// A STILL-CONFIGURED pack's output, and the USER'S OWN skill, survive a retirement aimed at a
// third pack. This is the blast-radius test: the orphan scan keys on the owner recorded per
// path, so a bug that widened it would take these with it.
func TestApplyHostRetireSparesConfiguredPacksAndUserSkills(t *testing.T) {
	home, dropDir := dropFixture(t, dropPackJSON)
	keepDir := filepath.Join(t.TempDir(), "keepme")
	writeFile(t, filepath.Join(keepDir, "pack.json"),
		`{"name":"keepme","description":"k","contributes":[
		  {"kind":"skills","from":"skills","into":".claude/skills","tier":"flat"}]}`)
	writeFile(t, filepath.Join(keepDir, "skills", "kept", "SKILL.md"),
		"---\nname: kept\ndescription: k\n---\nKept.\n")
	mine := filepath.Join(home, ".claude", "skills", "myown", "SKILL.md")
	writeFile(t, mine, "---\nname: myown\ndescription: m\n---\nMINE.\n")

	both := `"claude",{"source":"file://` + dropDir + `","name":"dropme"},` +
		`{"source":"file://` + keepDir + `","name":"keepme"}`
	selectPacks(t, home, both)
	if rc, report := applyWith(t, true, nil); rc != 0 {
		t.Fatalf("first apply rc=%d\n%s", rc, report)
	}
	selectPacks(t, home, `"claude",{"source":"file://`+keepDir+`","name":"keepme"}`)
	if rc, report := applyWith(t, true, strings.NewReader("y\n")); rc != 0 {
		t.Fatalf("retire rc=%d\n%s", rc, report)
	}

	skill, _ := deliveredPaths(home)
	mustNotExist(t, skill, "dropme left the config")
	mustExist(t, filepath.Join(home, ".claude", "skills", "kept"), "keepme is still configured")
	data, err := os.ReadFile(mine)
	if err != nil || !strings.Contains(string(data), "MINE") {
		t.Errorf("the user's own skill must be byte-identical: %v %q", err, data)
	}
}

// A NAMESPACED (tier-A) pack's whole subtree is retired too. It is the sharper half: tier A
// records nothing in the ownership manifest — the subtree's own plugin marker is the only
// evidence — so a manifest-only scan leaves an entire loadable namespace behind.
func TestApplyHostRetiresDroppedNamespacedSubtree(t *testing.T) {
	home := t.TempDir()
	packDir := filepath.Join(t.TempDir(), "nsdrop")
	writeFile(t, filepath.Join(packDir, "pack.json"),
		`{"name":"nsdrop","description":"n","contributes":[
		  {"kind":"skills","from":"skills","into":".claude/skills","tier":"namespaced"}]}`)
	writeFile(t, filepath.Join(packDir, "skills", "nsdemo", "SKILL.md"),
		"---\nname: nsdemo\ndescription: n\n---\nNS.\n")
	selectPacks(t, home, `"claude",{"source":"file://`+packDir+`","name":"nsdrop"}`)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	if rc, report := applyWith(t, true, nil); rc != 0 {
		t.Fatalf("first apply rc=%d\n%s", rc, report)
	}
	subtree := filepath.Join(home, ".claude", "skills", "nsdrop")
	mustExist(t, subtree, "the first apply delivered the namespaced subtree")

	selectPacks(t, home, `"claude"`)
	rc, report := applyWith(t, true, strings.NewReader("y\n"))
	if rc != 0 {
		t.Fatalf("retire rc=%d\n%s", rc, report)
	}
	mustNotExist(t, subtree, "nsdrop left the config")
	if !strings.Contains(report, "namespaced subtree") {
		t.Errorf("retiring a whole namespace must say so — it takes every skill inside "+
			"it at once:\n%s", report)
	}
	if archived := archivedUnder(t, home); len(archived) == 0 {
		t.Error("the namespaced subtree must be archived, not deleted")
	}
}

// A user's OWN plugin dir in the same skills dir is untouchable. tier-A ownership rests on
// yolo's marker in the plugin manifest, and a hand-authored plugin has none — so the scan must
// read "cannot prove it is mine" as "not mine", never as "unowned, therefore free".
func TestApplyHostRetireSparesUserAuthoredPluginDir(t *testing.T) {
	home, _ := dropFixture(t, dropPackJSON)
	mine := filepath.Join(home, ".claude", "skills", "myplugin")
	writeFile(t, filepath.Join(mine, ".claude-plugin", "plugin.json"),
		`{"name":"myplugin","skills":["./"]}`)
	applyThenDrop(t, home)

	if rc, report := applyWith(t, true, strings.NewReader("y\n")); rc != 0 {
		t.Fatalf("retire rc=%d\n%s", rc, report)
	}
	mustExist(t, filepath.Join(mine, ".claude-plugin", "plugin.json"),
		"a plugin without yolo's marker is the user's")
}

// AN UNRESOLVABLE pack is NOT a dropped pack. A fetched pack whose remote is unreachable
// resolves to nothing this run, so it is absent from the ACTIVE set — and reading that as
// "dropped" would archive a working setup's output the first time the user is offline.
//
// The BRIEFING half used to diverge here deliberately: under the delimited block its removal
// re-rendered from prose inside the pack the moment the remote was reachable, so it could afford
// the mistake that an archived skills tree could not. §6a removed that affordance — a briefing
// destination is now a whole yolo-owned file, and archiving it costs the same trip to the state
// dir — so the two thresholds are now DELIBERATELY THE SAME, and this test pins the convergence
// rather than the old split.
func TestApplyHostRetireKeepsUnresolvablePackOutput(t *testing.T) {
	home, _ := dropFixture(t, dropPackJSON)
	if rc, report := applyWith(t, true, nil); rc != 0 {
		t.Fatalf("first apply rc=%d\n%s", rc, report)
	}
	// Same pack NAME, now addressed at a git remote nothing can reach.
	selectPacks(t, home,
		`"claude",{"source":"git+https://example.invalid/dropme.git","name":"dropme"}`)

	rc, report := applyWith(t, true, strings.NewReader("y\n"))
	if rc != 0 {
		t.Fatalf("apply with an unresolvable pack rc=%d\n%s", rc, report)
	}
	skill, file := deliveredPaths(home)
	mustExist(t, skill, "the pack is still CONFIGURED, only unresolvable this run")
	mustExist(t, file, "the pack is still CONFIGURED, only unresolvable this run")
	if archived := archivedUnder(t, home); len(archived) != 0 {
		t.Errorf("an unresolvable pack's output must not be archived: %v", archived)
	}
	if strings.Contains(report, "/retire") {
		t.Errorf("an unresolvable pack must not be reported as dropped:\n%s", report)
	}
	// The BRIEFING destination survives too, and its content is left exactly as the last
	// resolvable apply composed it. A prune that read "no prose composes here this run" as
	// "orphaned" would archive it — which is the §6a regression this asserts against.
	brief := filepath.Join(home, ".claude", "CLAUDE.md")
	data, rerr := os.ReadFile(brief)
	if rerr != nil || !strings.Contains(string(data), "Dropme prose.") {
		t.Errorf("the briefing of a pack that is merely unresolvable must survive intact: %v %q",
			rerr, data)
	}
	if got := archivedBriefings(t, home); len(got) != 0 {
		t.Errorf("an unresolvable pack's briefing must not be archived: %v", got)
	}
}

// A STALE RECORD — the path is already gone, the record still names it — is dropped silently
// and never prompts. Nothing is lost, so a confirmation would be pure noise.
func TestApplyHostRetireDropsStaleRecordWithoutPrompting(t *testing.T) {
	home, _ := dropFixture(t, dropPackJSON)
	applyThenDrop(t, home)
	skill, file := deliveredPaths(home)
	// The user removed both themselves, which is exactly how the record goes stale.
	if err := os.RemoveAll(skill); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}

	rc, report := applyWith(t, true, nil)
	if rc != 0 {
		t.Fatalf("apply rc=%d\n%s", rc, report)
	}
	if strings.Contains(report, "[y/N]") {
		t.Errorf("a stale record alone must not prompt — nothing would be removed:\n%s", report)
	}
	if !strings.Contains(report, "stale record dropped") {
		t.Errorf("the stale record must be reported as dropped:\n%s", report)
	}
	if owners := manifestOwners(t, home); len(owners) != 0 {
		t.Errorf("the stale entries must be forgotten, got %v", owners)
	}
}

// R4 SURVIVES §6a, with a new reason: the BRIEFING half is still unconfirmed. Under the
// delimited block that held because removing it restored the user's own bytes; now a briefing
// destination is a file yolo composed WHOLESALE, so its retirement ARCHIVES — and still needs no
// [y/N], because every byte being moved is a byte yolo wrote. The gate protects USER content,
// and there is none in a generated file.
//
// So a declined skills/files retire still retires the briefing, and the two halves stay
// genuinely separate.
func TestApplyHostRetireDoesNotGateBriefingRemoval(t *testing.T) {
	home, _ := dropFixture(t, dropPackJSON)
	dest := filepath.Join(home, ".claude", "CLAUDE.md")
	applyThenDrop(t, home)
	data, err := os.ReadFile(dest)
	if err != nil || !strings.Contains(string(data), "Dropme prose.") {
		t.Fatalf("the composed briefing should be there after the first apply: %v %q", err, data)
	}

	if rc, report := applyWith(t, true, strings.NewReader("n\n")); rc != 0 {
		t.Fatalf("declined retire rc=%d\n%s", rc, report)
	}
	if _, err := os.Lstat(dest); err == nil {
		after, _ := os.ReadFile(dest)
		t.Errorf("the orphaned briefing destination must be retired even when the FILE retire "+
			"is DECLINED — the confirmation gate is about user content, and a composed briefing "+
			"has none:\n%q", after)
	}
	// ARCHIVED, not deleted: the composed bytes are recoverable, so being wrong costs one `mv`.
	if got := archivedBriefings(t, home); len(got) == 0 {
		t.Error("the retired briefing must be archived, not deleted")
	}
	// And the skill it was declined for is still there — the two halves really are separate.
	skill, _ := deliveredPaths(home)
	mustExist(t, skill, "the file retire was declined")
}

// EMPTYING `packs` retires too. This is the most complete drop there is, and it took the
// "nothing to apply" early return — so the case with the MOST to clean up was the one case
// that cleaned up nothing. Found by running the lifecycle, not by reading it.
func TestApplyHostRetiresWhenPacksListIsEmptied(t *testing.T) {
	home, _ := dropFixture(t, dropPackJSON)
	if rc, report := applyWith(t, true, nil); rc != 0 {
		t.Fatalf("first apply rc=%d\n%s", rc, report)
	}
	skill, file := deliveredPaths(home)
	selectPacks(t, home, ``)

	rc, report := applyWith(t, true, strings.NewReader("y\n"))
	if rc != 0 {
		t.Fatalf("retire with an empty packs list rc=%d\n%s", rc, report)
	}
	mustNotExist(t, skill, "the packs list is empty, so every pack is dropped")
	mustNotExist(t, file, "the packs list is empty, so every pack is dropped")
	if archived := archivedUnder(t, home); len(archived) == 0 {
		t.Error("emptying the packs list must archive, not delete")
	}
}

// A FAILED archive keeps the ownership record. The record is what makes the path retirable at
// all, so dropping it for a path still sitting in the home is the one state yolo can never
// recover from: the next apply reads the path as the user's own and leaves it forever. The
// failure is forced by making the archive root a FILE, so the move cannot create its directory.
func TestApplyHostRetireKeepsRecordWhenArchiveFails(t *testing.T) {
	home, _ := dropFixture(t, dropPackJSON)
	applyThenDrop(t, home)
	before := manifestOwners(t, home)
	if len(before) == 0 {
		t.Fatal("the first apply should have recorded what it delivered")
	}
	writeFile(t, filepath.Join(home, ".local", "share", "yolo-jail", "archive"), "not a dir\n")

	rc, report := applyWith(t, true, strings.NewReader("y\n"))
	if rc == 0 {
		t.Errorf("a retire whose archive failed must not report success:\n%s", report)
	}
	skill, file := deliveredPaths(home)
	mustExist(t, skill, "the archive failed, so nothing moved")
	mustExist(t, file, "the archive failed, so nothing moved")
	if got := manifestOwners(t, home); len(got) != len(before) {
		t.Errorf("a failed archive must not drop the record — without it the path becomes "+
			"permanently unretirable: %v -> %v", before, got)
	}
}

// A NIL configured set is REFUSED, not read as "no pack is configured". applyHost always
// builds a non-nil map, so this is reachable only by a future bug — and that bug's blast radius
// is every skill yolo ever delivered into the user's home, which is why it is guarded rather
// than merely avoided. Same guard PruneHostBriefings carries, for the sharper stakes.
func TestApplyHostRetireRefusesNilConfiguredSet(t *testing.T) {
	home, _ := dropFixture(t, dropPackJSON)
	applyThenDrop(t, home)
	skill, file := deliveredPaths(home)

	var out bytes.Buffer
	pr := richtext.Printer{W: &out}
	rc := pruneDroppedPackOutput(pr, &out, strings.NewReader("y\n"), nil, nil, home,
		"20260803-000000", true, overlayKeyRetirement{})
	if rc == 0 {
		t.Errorf("a nil configured set must be refused, not treated as 'no pack is active'")
	}
	mustExist(t, skill, "an unknown configured set authorizes nothing")
	mustExist(t, file, "an unknown configured set authorizes nothing")
	if archived := archivedUnder(t, home); len(archived) != 0 {
		t.Errorf("a refused prune archived something: %v", archived)
	}
}

// manifestOwners reads the ownership record's dest->pack map from a test home.
func manifestOwners(t *testing.T, home string) map[string]string {
	t.Helper()
	path := filepath.Join(home, ".local", "share", "yolo-jail", "host-skills-manifest.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatal(err)
	}
	var m struct {
		Entries map[string]string `json:"entries"`
	}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	return m.Entries
}
