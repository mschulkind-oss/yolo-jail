package run

// briefingreadback_test.go pins mayPrependHostBriefing's IN-JAIL half: a nested launch must not
// prepend the outer jail's composed briefing to its own. Measured 2026-09-22 at 0c74ff45: a
// nested ~/.claude/CLAUDE.md held the outer jail's whole briefing, then `---`, then its own.
//
// EVERY TEST GOES THROUGH THE REAL refreshJailBriefings and asserts on the bytes it staged, so
// replacing the predicate call at the call site with the old `!generated[src]` fails them.

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/hostskills"
	"github.com/mschulkind-oss/yolo-jail/internal/ioprio"
	"github.com/mschulkind-oss/yolo-jail/internal/jailcontent"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// pinLauncherInJail sets which side of the jail boundary the launcher believes it is on, for
// the life of the test, restoring whatever the seam held before.
func pinLauncherInJail(t *testing.T, inJail bool) {
	t.Helper()
	prev := launcherInJail
	launcherInJail = func() bool { return inJail }
	t.Cleanup(func() { launcherInJail = prev })
}

// The seam must default to the real probe, or production never takes the in-jail branch and
// every test below is green over a dead rule.
func TestLauncherInJailDefaultsToConfigInJail(t *testing.T) {
	if reflect.ValueOf(launcherInJail).Pointer() != reflect.ValueOf(config.InJail).Pointer() {
		t.Error("launcherInJail does not default to config.InJail")
	}
}

// (1) THE BUG. In a jail, the shipped claude pack's `after: "host:.claude/CLAUDE.md"` names
// its own destination — a read-only bind of the outer launch's staged briefing — and it is
// NOT a file `yolo host apply` recorded, so only the in-jail rule can keep it out.
func TestNestedJailBriefingDoesNotPrependTheOuterJailsBriefing(t *testing.T) {
	body := briefingWithHostFileAt(t, true, "# YOLO Jail Environment\nOUTER JAIL BRIEFING\n", false)
	if strings.Contains(body, "OUTER JAIL BRIEFING") {
		t.Errorf("a nested jail prepended the outer jail's composed briefing — the whole "+
			"briefing is delivered twice:\n%s", body)
	}
}

// (2) The same setup OUT of a jail is the user's own host file, and is still prepended.
func TestHostLaunchStillPrependsTheSameDestinationFile(t *testing.T) {
	body := briefingWithHostFileAt(t, false, "MY OWN RULES\n", false)
	if !strings.HasPrefix(body, "MY OWN RULES\n\n---\n") {
		t.Errorf("out of a jail, the user's own host briefing was not prepended:\n%s", body)
	}
}

// (3) In a jail, a prepend source that is NOT a briefing destination is nothing yolo staged,
// so it is still prepended. No shipped pack has this shape (all declare `after` == `into`),
// so a test pack builds it.
func TestNestedJailStillPrependsASourceThatIsNotADestination(t *testing.T) {
	got := briefingsWithHostFiles(t, true,
		map[string]string{".config/mine/RULES.md": "MY OWN RULES\n"},
		jailPack(t, "claude", nil, packdecl.Contribution{Kind: packdecl.KindBriefing,
			Into: ".claude/CLAUDE.md", Agent: "claude", After: "host:.config/mine/RULES.md"}))
	if !strings.HasPrefix(got[".claude/CLAUDE.md"], "MY OWN RULES\n\n---\n") {
		t.Errorf("in a jail, a non-destination host file was not prepended:\n%s",
			got[".claude/CLAUDE.md"])
	}
}

// (4) "A destination" means ANY selected pack's, not only the declaring one's own: in a jail
// ~/.codex/AGENTS.md is the outer launch's staged codex briefing just as much.
func TestNestedJailDoesNotPrependAnotherPacksDestination(t *testing.T) {
	got := briefingsWithHostFiles(t, true,
		map[string]string{".codex/AGENTS.md": "OUTER CODEX BRIEFING\n"},
		jailPack(t, "claude", nil, packdecl.Contribution{Kind: packdecl.KindBriefing,
			Into: ".claude/CLAUDE.md", Agent: "claude", After: "host:.codex/AGENTS.md"}),
		jailDest(t, "codex", ".codex/AGENTS.md", "codex"))
	if strings.Contains(got[".claude/CLAUDE.md"], "OUTER CODEX BRIEFING") {
		t.Errorf("in a jail, another destination's staged briefing was prepended:\n%s",
			got[".claude/CLAUDE.md"])
	}
}

// (5) THE DOTFILES LAYOUT: one file under two names. ~/.foo/AGENTS.md is a link to ~/AGENTS.md
// and the pack's `after` names ~/AGENTS.md. `yolo host apply` composed the destination through
// the link, so the record lists ~/.foo/AGENTS.md while the bytes live at ~/AGENTS.md: comparing
// paths alone prepended yolo's own composition to the launch's. File identity keeps it out, on
// the host against the record and in a jail against every destination.
func TestABriefingLinkedUnderAnotherNameIsNotPrependedAsTheUsersOwn(t *testing.T) {
	for _, inJail := range []bool{false, true} {
		got := briefingsWithHostSetup(t, inJail, func(home string) {
			real := filepath.Join(home, "AGENTS.md")
			if err := os.WriteFile(real, []byte("YOLO COMPOSED THIS\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			link := filepath.Join(home, ".foo", "AGENTS.md")
			if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(real, link); err != nil {
				t.Fatal(err)
			}
			if !inJail {
				man, err := hostskills.LoadManifest(entrypoint.HostBriefingManifestPath(home))
				if err != nil {
					t.Fatal(err)
				}
				man.Record(link, entrypoint.HostBriefingOwner)
				if err := man.Save(entrypoint.HostBriefingManifestPath(home)); err != nil {
					t.Fatal(err)
				}
			}
		}, jailPack(t, "foo", nil, packdecl.Contribution{Kind: packdecl.KindBriefing,
			Into: ".foo/AGENTS.md", Agent: "foo", After: "host:AGENTS.md"}))
		if strings.Contains(got[".foo/AGENTS.md"], "YOLO COMPOSED THIS") {
			t.Errorf("inJail=%v: a briefing yolo composed, reached under another name, was prepended "+
				"as the user's own:\n%s", inJail, got[".foo/AGENTS.md"])
		}
	}
}

// (6) The identity check is identity, not "any link": a link to a file yolo did not compose is
// still the user's, and is still prepended.
func TestALinkedBriefingYoloDidNotComposeIsStillPrepended(t *testing.T) {
	got := briefingsWithHostSetup(t, false, func(home string) {
		real := filepath.Join(home, "dotfiles", "RULES.md")
		if err := os.MkdirAll(filepath.Dir(real), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(real, []byte("MY OWN RULES\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(real, filepath.Join(home, "AGENTS.md")); err != nil {
			t.Fatal(err)
		}
	}, jailPack(t, "foo", nil, packdecl.Contribution{Kind: packdecl.KindBriefing,
		Into: ".foo/AGENTS.md", Agent: "foo", After: "host:AGENTS.md"}))
	if !strings.HasPrefix(got[".foo/AGENTS.md"], "MY OWN RULES\n\n---\n") {
		t.Errorf("the user's own linked briefing was not prepended:\n%s", got[".foo/AGENTS.md"])
	}
}

// briefingsWithHostFiles writes hostFiles (home-relative → content) into a fresh home, runs the
// REAL refreshJailBriefings over packs with the launcher pinned to inJail, and returns
// {destination → staged bytes}.
func briefingsWithHostFiles(t *testing.T, inJail bool, hostFiles map[string]string,
	packs ...*packload.Pack) map[string]string {
	t.Helper()
	return briefingsWithHostSetup(t, inJail, func(home string) {
		for rel, body := range hostFiles {
			p := filepath.Join(home, filepath.FromSlash(rel))
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}, packs...)
}

// briefingsWithHostSetup is briefingsWithHostFiles with the home prepared by setup.
func briefingsWithHostSetup(t *testing.T, inJail bool, setup func(home string),
	packs ...*packload.Pack) map[string]string {
	t.Helper()
	pinLauncherInJail(t, inJail)
	ws, home := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	emptyLoopholeDirs(t)
	jailcontent.SetPackSkillDirs(nil)
	jailcontent.SetPackSkillTargets(nil)
	t.Cleanup(func() { jailcontent.SetPackSkillDirs(nil); jailcontent.SetPackSkillTargets(nil) })
	setup(home)

	o := goldenOptions(ws, home)
	o.Stdout = discardBuf()
	staging, err := o.refreshJailBriefings("yolo-ws-abcd1234", newConfig(), "podman",
		stagedPacks{packs: packs}, ioprio.Normal)
	if err != nil {
		t.Fatalf("refreshJailBriefings: %v", err)
	}
	out := map[string]string{}
	for _, d := range briefingDestinations(packs) {
		data, rerr := os.ReadFile(filepath.Join(staging, briefingStagingName(d.Into)))
		if rerr != nil {
			t.Fatalf("no briefing staged for %s: %v", d.Into, rerr)
		}
		out[d.Into] = string(data)
	}
	return out
}
