package configmigrate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/pidlock"
)

const (
	oldBuildKey = "pi-fork/pi-patched"
	newBuildKey = "yolo-packs/pi-fork/pi-patched"
)

func assertGone(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); err == nil {
		t.Errorf("%s is still under the old name", path)
	}
}

// RC-D47 (A): a renamed pack's build logs move to the new key's slug, the host's and the
// workspace's, and a log whose new name is already taken is left with the one there untouched.
func TestARenamedPacksBuildLogsMoveToTheNewSlug(t *testing.T) {
	dir, ws := renamedPackFixture(t)
	logs := paths.LogsDir()
	stateDir := paths.WorkspaceStateDir(ws)
	global := filepath.Join(logs, paths.BuildLogName(oldBuildKey))
	tree := filepath.Join(logs, paths.TreeBuildLogName(oldBuildKey))
	rotated := tree + ".1"
	inWorkspace := filepath.Join(stateDir, paths.BuildLogName(oldBuildKey))
	write(t, global, "global build\n")
	write(t, tree, "tree build\n")
	write(t, rotated, "tree build, rotated\n")
	write(t, inWorkspace, "workspace build\n")
	// A second key whose new log already exists: neither is touched.
	taken := filepath.Join(logs, paths.BuildLogName("pi-fork/pi"))
	there := filepath.Join(logs, paths.BuildLogName("yolo-packs/pi-fork/pi"))
	write(t, taken, "old pi\n")
	write(t, there, "new pi\n")
	// A key of a pack that is not renamed stays.
	other := filepath.Join(logs, paths.BuildLogName("other/pi"))
	write(t, other, "other\n")

	res, err := Apply(plan(t, Request{UserConfigDir: dir, Workspace: ws}), ApplyOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for old, want := range map[string]struct{ path, text string }{
		global:      {filepath.Join(logs, paths.BuildLogName(newBuildKey)), "global build\n"},
		tree:        {filepath.Join(logs, paths.TreeBuildLogName(newBuildKey)), "tree build\n"},
		rotated:     {filepath.Join(logs, paths.TreeBuildLogName(newBuildKey)) + ".1", "tree build, rotated\n"},
		inWorkspace: {filepath.Join(stateDir, paths.BuildLogName(newBuildKey)), "workspace build\n"},
	} {
		if got := readFile(t, want.path); got != want.text {
			t.Errorf("%s holds %q, want %q", want.path, got, want.text)
		}
		assertGone(t, old)
	}
	if readFile(t, taken) != "old pi\n" || readFile(t, there) != "new pi\n" {
		t.Error("a log whose new name was taken was clobbered or moved")
	}
	if readFile(t, other) != "other\n" {
		t.Error("a log of a pack that is not renamed was moved")
	}
	joined := strings.Join(res.PackRecords, "\n")
	if !strings.Contains(joined, paths.BuildLogName(newBuildKey)) || !strings.Contains(joined, "is already there") {
		t.Errorf("the record lines do not say what moved and what was left:\n%s", joined)
	}
}

func writeOutcome(t *testing.T, path, key string, generation string) {
	t.Helper()
	write(t, path, `{"key":"`+key+`","label":"extension `+key+`","state":"failed","at":1,"log":"x",`+
		`"generation":`+generation+`,"patch_failure":{"owner":"`+key+`","target":{},"kind":"k","member":"m"}}`)
}

// RC-D47 (B): a renamed key's background-advance outcome, its reported outcome and its claims move
// to the new key's slug, respelled to the new key, when no process holds the key's locks.
func TestARenamedKeysBackgroundStateMovesWhenUnlocked(t *testing.T) {
	dir, ws := renamedPackFixture(t)
	bg := paths.BackgroundAdvanceDir()
	outcome := paths.BackgroundAdvanceKeyFile(oldBuildKey, paths.BackgroundOutcomeSuffix)
	said := paths.BackgroundAdvanceKeyFile(oldBuildKey, paths.BackgroundSaidSuffix)
	claim := filepath.Join(bg, paths.BackgroundClaimPrefix+paths.KeySlug(oldBuildKey)+"-123")
	const big = "9007199254740993" // past float64's exact integers
	writeOutcome(t, outcome, oldBuildKey, big)
	writeOutcome(t, said, oldBuildKey, "3")
	writeOutcome(t, claim, oldBuildKey, "4")

	if _, err := Apply(plan(t, Request{UserConfigDir: dir, Workspace: ws}), ApplyOptions{}); err != nil {
		t.Fatal(err)
	}
	for old, to := range map[string]string{
		outcome: paths.BackgroundAdvanceKeyFile(newBuildKey, paths.BackgroundOutcomeSuffix),
		said:    paths.BackgroundAdvanceKeyFile(newBuildKey, paths.BackgroundSaidSuffix),
		claim:   filepath.Join(bg, paths.BackgroundClaimPrefix+paths.KeySlug(newBuildKey)+"-123"),
	} {
		assertGone(t, old)
		text := readFile(t, to)
		var rec struct {
			Key          string `json:"key"`
			Label        string `json:"label"`
			PatchFailure struct {
				Owner string `json:"owner"`
			} `json:"patch_failure"`
		}
		if err := json.Unmarshal([]byte(text), &rec); err != nil {
			t.Fatal(err)
		}
		if rec.Key != newBuildKey || rec.PatchFailure.Owner != newBuildKey || rec.Label != "extension "+newBuildKey {
			t.Errorf("%s was not respelled to the new key: %s", to, text)
		}
	}
	if got := readFile(t, paths.BackgroundAdvanceKeyFile(newBuildKey, paths.BackgroundOutcomeSuffix)); !strings.Contains(got, `"generation":`+big) {
		t.Errorf("the generation lost precision: %s", got)
	}
}

// RC-D47 (B): a key whose lock a background advance holds is left whole, never waited for.
func TestARenamedKeysBackgroundStateStaysWhileItsLockIsHeld(t *testing.T) {
	for _, suffix := range []string{paths.BackgroundKeyLockSuffix, paths.BackgroundArtifactLockSuffix} {
		t.Run(suffix, func(t *testing.T) {
			dir, ws := renamedPackFixture(t)
			outcome := paths.BackgroundAdvanceKeyFile(oldBuildKey, paths.BackgroundOutcomeSuffix)
			writeOutcome(t, outcome, oldBuildKey, "2")
			lk, err := pidlock.Acquire(paths.BackgroundAdvanceKeyFile(oldBuildKey, suffix), pidlock.NoWait, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer lk.Release()

			res, err := Apply(plan(t, Request{UserConfigDir: dir, Workspace: ws}), ApplyOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(readFile(t, outcome), `"key":"`+oldBuildKey+`"`) {
				t.Error("the held key's outcome changed")
			}
			if _, err := os.Lstat(paths.BackgroundAdvanceKeyFile(newBuildKey, paths.BackgroundOutcomeSuffix)); err == nil {
				t.Error("the held key's outcome was copied to the new key")
			}
			if !strings.Contains(strings.Join(res.PackRecords, "\n"), "starts over") {
				t.Errorf("the record lines do not say the held key starts over: %v", res.PackRecords)
			}
		})
	}
}

// RC-D47 (E): the rename list pads the old names so the arrows line up, and the closing sentence
// says the skills keep their names only when they do.
func TestTheRenameListAlignsAndSaysWhatKeepsItsName(t *testing.T) {
	renames := []PackRename{{Old: "matt", New: "yolo-packs/matt"}, {Old: "matt-craft", New: "mschulkind-oss/matt-craft"}}
	var lines []string
	for _, r := range RenameRows(renames) {
		lines = append(lines, "  "+r[0]+r[1]+"  →  "+r[2])
	}
	want := "  matt        →  yolo-packs/matt\n  matt-craft  →  mschulkind-oss/matt-craft"
	if got := strings.Join(lines, "\n"); got != want {
		t.Errorf("rows:\n%s\nwant:\n%s", got, want)
	}
	if got := RenamesClosing(renames); got != "Your skills keep their names, and nothing is fetched or rebuilt." {
		t.Errorf("closing = %q", got)
	}
	renames[1].SkillsRenamed, renames[1].Namespace = true, "mschulkind-oss"
	got := RenamesClosing(renames)
	if strings.Contains(got, "Your skills keep their names") || !strings.Contains(got, "mschulkind-oss/matt-craft") ||
		!strings.Contains(got, "nothing is fetched or rebuilt") {
		t.Errorf("closing with a renamed skill = %q", got)
	}
}

// The plan's transcript says the rename the way the launch's offer does, and the conversion keeps
// the fixture's skill names (it writes bare = true).
func TestTheTranscriptNamesTheRenameInTheOffersWords(t *testing.T) {
	dir, ws := renamedPackFixture(t)
	p := plan(t, Request{UserConfigDir: dir, Workspace: ws})
	if len(p.Renames) != 1 || p.Renames[0].SkillsRenamed {
		t.Fatalf("renames = %+v, want one that keeps its skill names", p.Renames)
	}
	s := p.Transcript()
	want := RenamesHeading + "\n  pi-fork  →  yolo-packs/pi-fork\nYour skills keep their names, and nothing is fetched or rebuilt.\n"
	if !strings.Contains(s, want) {
		t.Errorf("the transcript does not say:\n%s\nit says:\n%s", want, s)
	}
	for _, old := range []string{"starts over", "made again under the new name", "readable handles"} {
		if strings.Contains(s, old) {
			t.Errorf("the transcript still says %q:\n%s", old, s)
		}
	}
}

// "Your skills keep their names" is said only when it is true: a converted entry a launch would
// read into a namespace (here, the fixture's conversion with its `bare = true` taken out) is a
// rename whose skills change, and the closing sentence names it.
func TestARenameThatNamespacesTheSkillsIsCaught(t *testing.T) {
	dir, ws := renamedPackFixture(t)
	p := plan(t, Request{UserConfigDir: dir, Workspace: ws})
	var converted string
	for _, tg := range p.Targets {
		if strings.HasSuffix(tg.New, paths.UserConfigTOMLName) {
			converted = string(tg.Bytes)
		}
	}
	if !strings.Contains(converted, "bare = true") {
		t.Fatalf("the conversion did not write bare = true:\n%s", converted)
	}
	staged := realDir(t)
	write(t, filepath.Join(staged, paths.UserConfigTOMLName), strings.ReplaceAll(converted, "bare = true", "bare = false"))
	renames, problems := packRenames(dir, staged)
	if len(problems) != 0 || len(renames) != 1 {
		t.Fatalf("renames = %+v, problems = %v", renames, problems)
	}
	if !renames[0].SkillsRenamed || renames[0].Namespace == "" {
		t.Fatalf("a namespaced conversion was not caught: %+v", renames[0])
	}
	if got := RenamesClosing(renames); strings.Contains(got, "Your skills keep their names") ||
		!strings.Contains(got, "yolo-packs/pi-fork") {
		t.Errorf("closing = %q", got)
	}
}

// "nothing is fetched" holds for a git pack whose converted entry drops its repository's trailing
// `.git`: its git mirror and fetch stamps are keyed by the repository as packsrc.Parse spells it,
// which is the same before and after, so the next launch finds its mirror where it was.
func TestARenamedGitPackKeepsItsMirror(t *testing.T) {
	home := env(t)
	dir := filepath.Join(home, ".config", "yolo-jail")
	write(t, filepath.Join(dir, "config.jsonc"), `{"packs": ["git+https://example.invalid/o/r.git?ref=main"]}`+"\n")
	p := plan(t, Request{UserConfigDir: dir, Workspace: realDir(t)})
	if len(p.Renames) != 1 || p.Renames[0].OldSource == p.Renames[0].NewSource {
		t.Fatalf("renames = %+v, want one whose source drops .git", p.Renames)
	}
	before, err1 := packsrc.Parse(p.Renames[0].OldSource)
	after, err2 := packsrc.Parse(p.Renames[0].NewSource)
	if err1 != nil || err2 != nil || before.Repo != after.Repo {
		t.Errorf("the renamed pack's repository is keyed %q before and %q after (%v, %v): the next launch fetches it again",
			before.Repo, after.Repo, err1, err2)
	}
	if got := RenamesClosing(p.Renames); got != "Your skills keep their names, and nothing is fetched or rebuilt." {
		t.Errorf("closing = %q", got)
	}
}
