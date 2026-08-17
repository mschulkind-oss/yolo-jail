package run

// localpack_test.go is the JAIL-NOTCH half of the conventional local pack
// (`~/.config/yolo-jail/local`, roadmap.md §6a-2). The host half lives in
// internal/config/localpack_test.go and internal/cli/applyhostlocalpack_test.go.
//
// It must work at BOTH notches or the convention reintroduces exactly the asymmetry finding F1
// closed: the jail inferring a destination the host does not (or, here, the host including a
// pack the jail does not). So this drives the real jail staging path — stagePacks, which reads
// the same config.LoadPacks entries — rather than asserting on the entry list a second time.
//
// packHome points HOME at a t.TempDir(), so the real ~/.config/yolo-jail (which on a
// development machine holds the live jail's own config) is never read or written.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
)

// localPackTree creates `~/.config/yolo-jail/local/skills/<skill>` under `home` with a body
// that identifies its source, and returns the local pack's root.
func localPackTree(t *testing.T, home, skill, body string) string {
	t.Helper()
	root := filepath.Join(home, ".config", "yolo-jail", "local")
	dir := filepath.Join(root, "skills", skill)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"),
		[]byte("---\nname: "+skill+"\ndescription: d\n---\n"+body+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// The jail stages the local pack's skills with NO `packs` entry naming it. This is the
// convention's whole claim, at the notch where a pack's skills actually reach an agent.
func TestJailStagesTheLocalPack(t *testing.T) {
	home := packHome(t)
	localPackTree(t, home, "myskill", "PERSONAL")
	writeUserPacks(t, home, `["claude"]`)

	dirs, warnings := stagedSkillDirs(t, &Options{Workspace: t.TempDir()})
	found := stagedLocalPackDir(dirs)
	if found == "" {
		t.Fatalf("the local pack's skills dir was not staged: %v\nwarnings:\n%s", dirs, warnings)
	}
	// The STAGED copy holds the content, which is the proof the jail treats this as an
	// ordinary local pack: stagePacks ran packstage over it like any other file:// source
	// rather than reading the config dir directly.
	if _, err := os.Stat(filepath.Join(found, "myskill", "SKILL.md")); err != nil {
		t.Errorf("the staged source does not hold the skill: %v", err)
	}
	if strings.Contains(warnings, "Warning") {
		t.Errorf("staging the conventional local pack warned — its presence is ordinary:\n%s",
			warnings)
	}
}

// stagedLocalPackDir picks the local pack's staged skills source out of the list, or "".
//
// It matches on the STAGING slug rather than on `~/.config/yolo-jail/local`, because the jail
// path stages every configured pack through packstage: the source handed to skills staging is
// the staged copy under <agents>/<cname>/packs/<slug>/skills, never the original tree. Keying
// on the original path is what made the first cut of these tests fail while the feature worked.
func stagedLocalPackDir(dirs []string) string {
	want := filepath.Join("packs", config.LocalPackName, "skills")
	for _, d := range dirs {
		if strings.HasSuffix(d, want) {
			return d
		}
	}
	return ""
}

// ABSENT IS SILENT AND FREE at the jail too: no entry, no warning, and a launch that looks
// exactly like today's.
func TestJailWithoutALocalPackStagesNothingExtra(t *testing.T) {
	home := packHome(t)
	writeUserPacks(t, home, `["claude"]`)

	dirs, warnings := stagedSkillDirs(t, &Options{Workspace: t.TempDir()})
	if d := stagedLocalPackDir(dirs); d != "" {
		t.Errorf("an absent local pack still produced a source dir: %s", d)
	}
	if strings.Contains(warnings, "local") {
		t.Errorf("an absent local pack was mentioned at all:\n%s", warnings)
	}
}

// ORDER IS LOAD-BEARING AND IT IS LAST. jailcontent.PrepareSkills copies packSkillDirs in this
// order with later winning a same-named skill (copySkillSubdirs replaces the target), so the
// local pack coming last is precisely what makes a PERSONAL skill outrank a shared pack's —
// the precedence the jail already had when the user's own tree was a separate final layer.
func TestJailLocalPackSkillsSourceComesLast(t *testing.T) {
	home := packHome(t)
	localPackTree(t, home, "dup", "PERSONAL")
	shared := filepath.Join(t.TempDir(), "shared")
	writeSkillTree(t, filepath.Join(shared, "skills"), "dup")
	writeUserPacks(t, home, `["claude",{"source":"file://`+shared+`","name":"shared"}]`)

	dirs, warnings := stagedSkillDirs(t, &Options{Workspace: t.TempDir()})
	if len(dirs) == 0 {
		t.Fatalf("no skills sources staged\n%s", warnings)
	}
	last := dirs[len(dirs)-1]
	if last != stagedLocalPackDir(dirs) {
		t.Errorf("the last skills source is %q, not the local pack's — a personal skill would "+
			"lose to a shared pack's same-named one. Full order: %v", last, dirs)
	}
}

// A LOCAL PACK IS CONTENT, NEVER AN AGENT: it must not silence the empty-packs notice. A jail
// whose only pack is ~/.config/yolo-jail/local has skills and prose and still nothing to run
// them, so "no packs are configured, so this jail has no coding agent" is still true — and
// silencing it would restore exactly the contradiction the opt-in ruling removed (a user
// discovering they have no agent only by looking in ~/.yolo-shims).
//
// This is the USER-VISIBLE half of config.HasConfiguredPack. Its config-level twin
// (TestLocalPackDoesNotCountAsAConfiguredPack) pins the predicate; this pins the consequence,
// which is what a reader of the notice actually experiences.
func TestLocalPackDoesNotSilenceTheNoPacksNotice(t *testing.T) {
	home := packHome(t)
	localPackTree(t, home, "myskill", "PERSONAL")
	writeUserPacks(t, home, `[]`)

	if got := noPacksOutput(t); got == "" {
		t.Fatal("a jail whose only pack is the conventional local pack printed no notice — the " +
			"local pack is content, so the jail still has no coding agent")
	}
	// And the notice is still silenced by a real agent pack alongside it, so the local pack has
	// not made the check unconditional either.
	home = packHome(t)
	localPackTree(t, home, "myskill", "PERSONAL")
	writeUserPacks(t, home, `["claude"]`)
	if got := noPacksOutput(t); got != "" {
		t.Errorf("a configured `claude` alongside the local pack did not silence the notice:\n%s",
			got)
	}
}
