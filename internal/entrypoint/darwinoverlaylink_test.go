package entrypoint

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// darwinoverlaylink_test.go pins MMP-D35 through the real install (InstallHomeOverlay): every
// delivered file is a COPY of the staged one, an unchanged relaunch keeps it (no rewrite, the inode
// stays), and a delivered file that no longer matches the staged one by content is replaced.

// countStagedWindows counts, per destination, the installs that built a replacement (the "staged"
// window of overlayInstallHook), through the real install.
func countStagedWindows(t *testing.T) map[string]int {
	t.Helper()
	n := map[string]int{}
	withOverlayInstallHook(t, func(dest, w string) {
		if w == "staged" {
			n[dest]++
		}
	})
	return n
}

// requireCopyOf fails unless delivered is a regular file of its own — never the staged file under
// a second name — with the staged file's mode and bytes.
func requireCopyOf(t *testing.T, delivered, staged, why string) os.FileInfo {
	t.Helper()
	di, err := os.Lstat(delivered)
	if err != nil {
		t.Fatalf("%s: %v", why, err)
	}
	si, err := os.Lstat(staged)
	if err != nil {
		t.Fatal(err)
	}
	if !di.Mode().IsRegular() || os.SameFile(di, si) || di.Mode().Perm() != si.Mode().Perm() {
		t.Fatalf("%s: %s (%v) is not a copy of %s (%v)", why, delivered, di.Mode(), staged, si.Mode())
	}
	a, _ := os.ReadFile(delivered)
	b, _ := os.ReadFile(staged)
	if !bytes.Equal(a, b) {
		t.Fatalf("%s: %s holds %q, the staged file %q", why, delivered, a, b)
	}
	return di
}

func requireSameInode(t *testing.T, path string, before os.FileInfo, why string) {
	t.Helper()
	after, err := os.Lstat(path)
	if err != nil || !os.SameFile(before, after) {
		t.Fatalf("%s: %s was replaced (err %v)", why, path, err)
	}
}

var (
	overlaySkillRel = filepath.Join(".pi", "agent", "skills", "demo", "SKILL.md")
	overlayBriefRel = filepath.Join(".pi", "agent", "AGENTS.md")
)

// AN UNCHANGED RELAUNCH REPLACES NOTHING: the delivered files are copies of the staged ones, and the
// second install of the same staged tree finds every destination matching by content, builds no
// replacement for any of them, and leaves every delivered inode in place.
func TestOverlayInstallOfAnUnchangedStagedTreeReplacesNothing(t *testing.T) {
	f := newOverlayFixture(t)
	f.stage(t)
	staged := countStagedWindows(t)
	if err := f.install(t); err != nil {
		t.Fatal(err)
	}
	skill := requireCopyOf(t, filepath.Join(f.home, overlaySkillRel), filepath.Join(f.overlay, overlaySkillRel),
		"a delivered skill file is a copy of the staged one")
	brief := requireCopyOf(t, filepath.Join(f.home, overlayBriefRel), filepath.Join(f.overlay, overlayBriefRel),
		"a delivered briefing is a copy of the staged one")
	if staged[".pi/agent/skills"] != 1 || staged[".pi/agent/AGENTS.md"] != 1 {
		t.Fatalf("first install built %v replacements, want one per destination", staged)
	}

	if err := f.install(t); err != nil {
		t.Fatal(err)
	}
	if staged[".pi/agent/skills"] != 1 || staged[".pi/agent/AGENTS.md"] != 1 {
		t.Fatalf("an install of an unchanged staged tree built replacements: %v", staged)
	}
	requireSameInode(t, filepath.Join(f.home, overlaySkillRel), skill, "the unchanged skill")
	requireSameInode(t, filepath.Join(f.home, overlayBriefRel), brief, "the unchanged briefing")
}

// A COPY IS THE DESIGN, NOT A FALLBACK: the install never tries a link, so the boot log carries no
// "delivered as a copy … refused" line (the one W1 saw on every Mac launch).
func TestOverlayInstallDoesNotReportItsCopies(t *testing.T) {
	f := newOverlayFixture(t)
	f.stage(t)
	out, err := f.installReporting(t)
	if err != nil {
		t.Fatal(err)
	}
	requireCopyOf(t, filepath.Join(f.home, overlaySkillRel), filepath.Join(f.overlay, overlaySkillRel), "the skill is delivered")
	if strings.Contains(out, "delivered as a copy") || strings.Contains(out, "refused") {
		t.Fatalf("the install reported its copy as a refused link:\n%s", out)
	}
}

// A RE-STAGED TREE WITH THE SAME CONTENT REPLACES NOTHING — new staged inodes are not a new
// delivery, only new bytes are — and one with new content is delivered again.
func TestOverlayInstallOfARestagedTreeReplacesOnlyWhatChanged(t *testing.T) {
	f := newOverlayFixture(t)
	f.stage(t)
	if err := f.install(t); err != nil {
		t.Fatal(err)
	}
	brief, _ := os.Lstat(filepath.Join(f.home, overlayBriefRel))
	f.restage(t)
	staged := countStagedWindows(t)
	if err := f.install(t); err != nil {
		t.Fatal(err)
	}
	if staged[".pi/agent/skills"] != 0 || staged[".pi/agent/AGENTS.md"] != 0 {
		t.Fatalf("an install of a same-content re-stage built %v replacements, want none", staged)
	}

	// New skill bytes: only the skills destination is rebuilt.
	stagedSkill := filepath.Join(f.overlay, overlaySkillRel)
	if err := os.Remove(stagedSkill); err != nil {
		t.Fatal(err)
	}
	writeTreeFile(t, stagedSkill, "a newer skill")
	if err := f.install(t); err != nil {
		t.Fatal(err)
	}
	if staged[".pi/agent/skills"] != 1 || staged[".pi/agent/AGENTS.md"] != 0 {
		t.Fatalf("an install of a changed skill built %v replacements, want the skills destination only", staged)
	}
	requireCopyOf(t, filepath.Join(f.home, overlaySkillRel), stagedSkill, "the re-staged skill is delivered")
	requireSameInode(t, filepath.Join(f.home, overlayBriefRel), brief, "the unchanged briefing")
}

// A DESTINATION CHANGED BETWEEN SESSIONS IS REPLACED, however it changed — the check compares
// names, types, modes and bytes with the staged tree, never a timestamp. Each case leaves the
// staged tree alone, so only the tampering can make the install rebuild.
func TestOverlayInstallReplacesADestinationChangedBetweenSessions(t *testing.T) {
	cases := map[string]func(t *testing.T, f overlayFixture){
		"a file edited in place": func(t *testing.T, f overlayFixture) {
			p := filepath.Join(f.home, overlaySkillRel)
			if err := os.WriteFile(p, []byte("the agent's edit"), 0o644); err != nil {
				t.Fatal(err)
			}
		},
		"a file edited to the same size with its mtime put back": func(t *testing.T, f overlayFixture) {
			p := filepath.Join(f.home, overlaySkillRel)
			fi, err := os.Stat(p)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte("NEW SKILL"), 0o644); err != nil { // len("new skill")
				t.Fatal(err)
			}
			if err := os.Chtimes(p, time.Now(), fi.ModTime()); err != nil {
				t.Fatal(err)
			}
		},
		"a file's mode widened": func(t *testing.T, f overlayFixture) {
			if err := os.Chmod(filepath.Join(f.home, overlaySkillRel), 0o666); err != nil {
				t.Fatal(err)
			}
		},
		"a file replaced by the staged file itself (an earlier yolo's hard link)": func(t *testing.T, f overlayFixture) {
			p := filepath.Join(f.home, overlaySkillRel)
			if err := os.Remove(p); err != nil {
				t.Fatal(err)
			}
			if err := os.Link(filepath.Join(f.overlay, overlaySkillRel), p); err != nil {
				t.Skipf("no hard links here: %v", err)
			}
		},
		"a file added": func(t *testing.T, f overlayFixture) {
			writeTreeFile(t, filepath.Join(f.home, ".pi", "agent", "skills", "demo", "EXTRA.md"), "planted")
		},
		"a file removed": func(t *testing.T, f overlayFixture) {
			if err := os.Remove(filepath.Join(f.home, overlaySkillRel)); err != nil {
				t.Fatal(err)
			}
		},
		"a file swapped for a link": func(t *testing.T, f overlayFixture) {
			p := filepath.Join(f.home, overlaySkillRel)
			if err := os.Remove(p); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("/etc/hostname", p); err != nil {
				t.Fatal(err)
			}
		},
		"a directory's mode widened": func(t *testing.T, f overlayFixture) {
			if err := os.Chmod(filepath.Join(f.home, ".pi", "agent", "skills", "demo"), 0o777); err != nil {
				t.Fatal(err)
			}
		},
		"the destination swapped for a link to a copy of itself": func(t *testing.T, f overlayFixture) {
			dest := filepath.Join(f.home, ".pi", "agent", "skills")
			if err := os.Rename(dest, dest+".real"); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("skills.real", dest); err != nil {
				t.Fatal(err)
			}
		},
		"a briefing rewritten": func(t *testing.T, f overlayFixture) {
			p := filepath.Join(f.home, overlayBriefRel)
			if err := os.Remove(p); err != nil {
				t.Fatal(err)
			}
			writeTreeFile(t, p, "the agent's own instructions")
		},
		"a briefing swapped for a link": func(t *testing.T, f overlayFixture) {
			p := filepath.Join(f.home, overlayBriefRel)
			if err := os.Remove(p); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(filepath.Join(f.overlay, overlayBriefRel), p); err != nil {
				t.Fatal(err)
			}
		},
	}
	for name, tamper := range cases {
		t.Run(name, func(t *testing.T) {
			f := newOverlayFixture(t)
			f.stage(t)
			if err := f.install(t); err != nil {
				t.Fatal(err)
			}
			tamper(t, f)
			staged := countStagedWindows(t)
			if err := f.install(t); err != nil {
				t.Fatal(err)
			}
			if staged[".pi/agent/skills"]+staged[".pi/agent/AGENTS.md"] != 1 {
				t.Fatalf("the install after tampering built %v replacements, want exactly the tampered destination", staged)
			}
			requireCopyOf(t, filepath.Join(f.home, overlaySkillRel), filepath.Join(f.overlay, overlaySkillRel),
				"the skill is a copy of the staged file again")
			requireCopyOf(t, filepath.Join(f.home, overlayBriefRel), filepath.Join(f.overlay, overlayBriefRel),
				"the briefing is a copy of the staged file again")
			requireAbsent(t, filepath.Join(f.home, ".pi", "agent", "skills", "demo", "EXTRA.md"),
				"a planted file survived the install")
		})
	}
}

// A FILE WRITTEN AGAIN WITH THE STAGED BYTES AND MODE IS KEPT: the check is the content, not who
// wrote it or which inode holds it — an identical file is already this delivery.
func TestOverlayInstallKeepsAFileThatStillMatchesByContent(t *testing.T) {
	f := newOverlayFixture(t)
	f.stage(t)
	if err := f.install(t); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(f.home, overlaySkillRel)
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	writeTreeFile(t, p, "new skill")
	if err := os.Chtimes(p, time.Unix(0, 0), time.Unix(0, 0)); err != nil {
		t.Fatal(err)
	}
	rewritten, _ := os.Lstat(p)
	staged := countStagedWindows(t)
	if err := f.install(t); err != nil {
		t.Fatal(err)
	}
	if staged[".pi/agent/skills"] != 0 {
		t.Fatalf("a file matching the staged one by content was replaced: %v", staged)
	}
	requireSameInode(t, p, rewritten, "the matching file")
}
