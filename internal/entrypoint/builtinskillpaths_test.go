package entrypoint

import (
	"io/fs"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jailcontent/builtinskills"
)

// TestBuiltinSkillsNameNoRetiredGeneratedDir is the guard the diagnosing-the-jail
// skill needed and did not have.
//
// The built-in skills are staged into EVERY jail, so a path named there is one an
// agent will type. §3 of diagnosing-the-jail told every agent to run
// `ls ~/.yolo-shims/` and `ls ~/.yolo-launchers/` for nine days after those dirs
// were renamed (2026-08-30, a813b865) — both commands fail with "No such file or
// directory", and the doc that was supposed to explain a confusing tool was the
// confusing part. Nothing caught it because the rename's own cleanup list
// (retiredGeneratedDirs) is checked against the FILESYSTEM at boot and against no
// prose anywhere.
//
// This test lives in internal/entrypoint rather than beside the skills because
// that is where the authority is: retiredGeneratedDirs is the same list
// removeRetiredGeneratedDirs empties, so a future rename that appends to it makes
// this test fail on any skill still naming the old dir, in the same commit.
// Asserting against a copy of the list would be asserting against a copy.
func TestBuiltinSkillsNameNoRetiredGeneratedDir(t *testing.T) {
	for _, retired := range retiredGeneratedDirs {
		if !strings.HasPrefix(retired, ".") {
			t.Fatalf("retiredGeneratedDirs entry %q is not a dotfile — this test's "+
				"substring match assumes home-relative dotted dir names", retired)
		}
	}
	err := fs.WalkDir(builtinskills.FS, ".", func(p string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		if d.IsDir() || filepath.Ext(p) != ".md" {
			return nil
		}
		b, rerr := builtinskills.FS.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		for _, line := range strings.Split(string(b), "\n") {
			for _, retired := range retiredGeneratedDirs {
				if strings.Contains(line, retired) {
					t.Errorf("%s names the RETIRED generated dir %q, which no jail has:\n\t%s\n"+
						"Blockers are at HOME/.yolo/bin/block and lazy installers at "+
						"HOME/.yolo/bin/launch (Env.BlockDir / Env.LaunchDir).", p, retired, strings.TrimSpace(line))
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestBuiltinSkillsDoNotTeachTheDefeatedPathPosition pins the RATIONALE, not just
// the dir name — because the stale §3 got both wrong and the position is the half
// that misleads rather than merely failing.
//
// "Last on PATH, after /bin" was true until 2026-09-04 (B2, OQ-PD12a) and is the
// position whose defeat of the evergreen update is documented at Env.LaunchDir:
// a launcher ordered after the prefixes it installs into is unreachable the moment
// it succeeds. A skill still teaching it tells an agent that a launcher is reached
// only when nothing else provides the name, which is now exactly backwards — the
// launcher mediates EVERY invocation, and that is the point of it.
//
// BootPath is the authority; this asserts the prose agrees with it.
func TestBuiltinSkillsDoNotTeachTheDefeatedPathPosition(t *testing.T) {
	e := &Env{Home: "/home/agent"}
	launch := e.LaunchDir()
	err := fs.WalkDir(builtinskills.FS, ".", func(p string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		if d.IsDir() || filepath.Ext(p) != ".md" {
			return nil
		}
		b, rerr := builtinskills.FS.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		for _, line := range strings.Split(string(b), "\n") {
			low := strings.ToLower(line)
			if !strings.Contains(low, "last on path") {
				continue
			}
			// A line may legitimately RECORD the old position as history. What it
			// may not do is state it while naming the launcher dir, which is what
			// makes it an instruction rather than a note.
			if strings.Contains(line, launch) || strings.Contains(low, "launcher") {
				t.Errorf("%s teaches the DEFEATED path position:\n\t%s\n"+
					"%s is SECOND on PATH since B2, ahead of every install prefix — see "+
					"Env.LaunchDir and BootPath.", p, strings.TrimSpace(line), launch)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
