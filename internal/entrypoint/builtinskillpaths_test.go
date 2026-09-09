package entrypoint

import (
	"io/fs"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jailcontent/builtinskills"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
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

// TestBuiltinSkillsTeachNoRetiredConfigKey is the third guard, and the one whose
// authority is a FUNCTION rather than a list.
//
// It found configuring-the-jail teaching `agents` in two places — "everything else …
// `agents` … is restart-only, no rebuild" and a whole paragraph on how a workspace
// `agents` merges — nine days after the key became a hard error on the host
// (validateAgentsRetired: *"REMOVED — which agents a jail gets is no longer a config
// key of its own"*). A skill is staged into every jail, so that paragraph told every
// agent to write a key whose only effect is to make `yolo check` fail, and then
// explained a merge rule for it.
//
// IT NAMES NO KEYS OF ITS OWN, deliberately. A test carrying its own retired-key list
// is a copy of the thing it is checking, and the next retirement would not reach it.
// Instead it takes every config-key-shaped token the PROSE mentions and asks
// config.ValidateConfig what that key is — so a key retired tomorrow fails this test
// for any skill still naming it, with nothing added here.
//
// A line that RECORDS a retirement is allowed, same carve-out and same reason as
// TestBuiltinSkillsDoNotTeachTheDefeatedPathPosition: saying a key was removed is the
// fix, not the defect.
func TestBuiltinSkillsTeachNoRetiredConfigKey(t *testing.T) {
	// A backticked token that could be a top-level config key. Dotted spellings
	// (`gpu.vaapi`) are reduced to their first segment, which is the only part
	// ValidateConfig judges.
	tick := regexp.MustCompile("`([a-z][a-z0-9_]*)(?:\\.[a-z0-9_.]+)?`")
	ws := t.TempDir()

	// retiredVerdict asks the authority. A retired key earns a targeted message
	// naming itself; an unknown word earns "unknown key" and is not a config key at
	// all. Probed with a null value because the retirement validators branch on
	// PRESENCE, and every live validator treats null as absent.
	retiredVerdict := func(key string) string {
		cfg := jsonx.NewOrderedMap()
		cfg.Set(key, nil)
		errs, warns := config.ValidateConfig(cfg, ws, nil)
		for _, m := range append(append([]string{}, errs...), warns...) {
			if !strings.HasPrefix(m, "config."+key+":") {
				continue
			}
			if strings.Contains(m, "REMOVED") || strings.Contains(m, "was retired") {
				return m
			}
		}
		return ""
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
		// NO per-file dedupe: configuring-the-jail named `agents` in two separate
		// places and a reader who fixed the one this reported would have thought
		// they were done. Every line that teaches the key is its own defect.
		for _, line := range strings.Split(string(b), "\n") {
			low := strings.ToLower(line)
			if strings.Contains(low, "retired") || strings.Contains(line, "REMOVED") {
				continue // recording the retirement is the fix, not the defect
			}
			for _, m := range tick.FindAllStringSubmatch(line, -1) {
				key := m[1]
				if verdict := retiredVerdict(key); verdict != "" {
					t.Errorf("%s teaches the RETIRED config key %q:\n\t%s\n"+
						"yolo itself says: %s\n"+
						"To RECORD a retirement rather than teach the key, say "+
						"\"retired\" or \"REMOVED\" on the same line — that is the "+
						"carve-out this check makes, and a line without it reads as "+
						"an instruction.", p, key, strings.TrimSpace(line), verdict)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
