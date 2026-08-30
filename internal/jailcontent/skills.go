package jailcontent

import (
	"io"
	"os"
	"path/filepath"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// PrepareSkills stages per-agent skills dirs on the host for :ro bind mounting.
// Each pack-declared destination's staging dir gets the built-in skill suite
// (builtinskills.FS) plus every selected pack's skills. Returns the staging
// directory (AGENTS_DIR/<cname>).
// includeDev stages the source-tree-only skills (e.g. developing-yolo-jail) —
// pass WorkspaceIsYoloSourceTree(workspace). CRITICAL: entries are cleared
// *inside* each skills_dir — the dir itself is NEVER rmtree+mkdir'd, because a
// running jail's bind mount captured its inode and a fresh inode would silently
// detach attach-time refreshes.
// packSkillDirs are the per-pack `skills/` directories to layer in, in config
// order (C3). Threaded as a package-level var rather than a parameter so the
// existing four-arg signature and its callers stay untouched; the CLI sets it once
// per run, before PrepareSkills.
//
// Precedence within one agent's staging dir: built-ins < packs, with the
// CONVENTIONAL LOCAL PACK last among the packs (config.LoadPacks appends it there).
// So a pack may override a built-in — a legitimate reason to ship one — and the
// user's own copy still outranks a shared pack's. There is no fourth layer: the one
// that read the destination back in was S3's defect.
var packSkillDirs []string

// SetPackSkillDirs sets the pack skills dirs consulted by the next PrepareSkills.
// Passing nil clears them.
func SetPackSkillDirs(dirs []string) { packSkillDirs = dirs }

// PackSkillDirs returns what SetPackSkillDirs last set — the SOURCE dirs the next
// PrepareSkills will copy from. Exported so the CLI's own tests can assert which dir a
// pack's `skills` contribution resolved to, which is otherwise observable only by
// inspecting the staged output after a full PrepareSkills run.
func PackSkillDirs() []string { return packSkillDirs }

// SkillTarget is one pack-declared skills destination: which staging dir to build, and
// the jail path it will be mounted at.
type SkillTarget struct {
	// Staging is the staging subdir name (per PACK, so two packs cannot collide).
	Staging string
	// Dest is the home-relative jail path the staged dir is mounted at.
	Dest string

	// A HostSource FIELD USED TO LIVE HERE, naming "the user's OWN skills tree to layer in
	// last" — and it was set to the DESTINATION (run.packSkillTargets), i.e. the host's own
	// copy of the very path this staging dir gets mounted over. That was right while the
	// destination held loose user files and became
	// circular once `yolo host apply` COMPOSED it: a jail read yolo's own generated output back
	// in as "the user's tree", and since the local pack is an ordinary pack entry its content
	// arrived twice by two paths (roadmap.md S3). Invisible only because flat is
	// last-writer-wins — and under S1, arriving twice is the kind of thing that becomes an
	// error rather than a coincidence.
	//
	// There is nothing to replace it with, because the slot it described already has a home:
	// the CONVENTIONAL LOCAL PACK is layer 4. config.LoadPacks appends it LAST, so its skills
	// are copied last in the packSkillDirs loop below and a personal skill still outranks
	// every shared pack's and every built-in — the exact precedence the field provided, now
	// reached by the same route every other pack's content takes.
}

// packSkillTargets are the destinations PrepareSkills builds. Set per run by the CLI
// from pack declarations; nil means none, which is why a jail with no packs stages
// nothing rather than inventing a destination.
var packSkillTargets []SkillTarget

// SetPackSkillTargets sets the pack-declared skills destinations for the next
// PrepareSkills call.
func SetPackSkillTargets(targets []SkillTarget) { packSkillTargets = targets }

// SkillStagingName is the staging subdir for one pack's skills.
func SkillStagingName(pack string) string { return "skills-" + pack }

// PACK-DECLARED skills destinations replace the agent list: SetPackSkillTargets is
// what tells this which staging dirs to build, so a pack gets its skills whether or not
// anything calls it an agent.
//
// homeDir and agentNames are both vestigial now and kept for their callers' sake: they existed
// to locate the host's own ~/.<agent>/skills trees, which S3 removed as a layer (see
// SkillTarget). The signature is left alone deliberately — five call sites pass them, and
// churning those would be a bigger diff than the fix, with no behavior in it.
func PrepareSkills(cname, homeDir string, agentNames []string, includeDev bool) (string, error) {
	staging := filepath.Join(paths.AgentsDir(), cname)
	if err := os.MkdirAll(staging, 0o755); err != nil {
		return "", err
	}

	for _, target := range packSkillTargets {
		skillsDir := filepath.Join(staging, target.Staging)
		if err := os.MkdirAll(skillsDir, 0o755); err != nil {
			return "", err
		}
		// Clear entries INSIDE skillsDir — never remove skillsDir itself.
		if err := clearDirContents(skillsDir); err != nil {
			return "", err
		}
		// 1. Built-in skill suite (every skills-bearing agent gets it).
		if err := writeBuiltinSkills(skillsDir, includeDev); err != nil {
			return "", err
		}
		// 2. PACK skills (C3), in config order — LAST, and that is now the whole of the
		//    precedence rather than the middle of it. A pack may override a built-in (a
		//    legitimate reason to ship one), and the CONVENTIONAL LOCAL PACK is appended last
		//    by config.LoadPacks, so a personal skill still outranks every shared pack's. The
		//    layer that used to follow this one read the DESTINATION and is gone — see
		//    SkillTarget for why that was circular.
		for _, dir := range packSkillDirs {
			if err := copySkillSubdirs(dir, skillsDir); err != nil {
				return "", err
			}
		}
	}
	return staging, nil
}

// clearDirContents removes every entry inside dir, leaving dir itself intact.
func clearDirContents(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if err := os.RemoveAll(filepath.Join(dir, e.Name())); err != nil {
			return err
		}
	}
	return nil
}

// copySkillSubdirs copies skill subdirectories from src into dst, following
// symlinks (a source that isn't a dir is a no-op). An existing target subdir is
// copy dereferences symlinks).
func copySkillSubdirs(src, dst string) error {
	info, err := os.Stat(src) // follows a symlinked src dir
	if err != nil || !info.IsDir() {
		return nil
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return nil
	}
	for _, e := range entries {
		// Stat (not Lstat) so a symlink to a dir counts as a dir.
		srcItem := filepath.Join(src, e.Name())
		si, err := os.Stat(srcItem)
		if err != nil || !si.IsDir() {
			continue
		}
		target := filepath.Join(dst, e.Name())
		if err := os.RemoveAll(target); err != nil {
			return err
		}
		if err := copyTreeDeref(srcItem, target); err != nil {
			return err
		}
	}
	return nil
}

// copyTreeDeref recursively copies src→dst, dereferencing symlinks (files and
// dirs).
func copyTreeDeref(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if info.IsDir() {
		if err := os.MkdirAll(dst, 0o755); err != nil {
			return err
		}
		entries, err := os.ReadDir(src)
		if err != nil {
			return err
		}
		for _, e := range entries {
			if err := copyTreeDeref(filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())); err != nil {
				return err
			}
		}
		return nil
	}
	return copyFileDeref(src, dst)
}

func copyFileDeref(src, dst string) error {
	in, err := os.Open(src) // follows symlink
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
