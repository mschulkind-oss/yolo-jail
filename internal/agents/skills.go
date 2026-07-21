package agents

import (
	"io"
	"os"
	"path/filepath"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// PrepareSkills stages per-agent skills dirs on the host for :ro bind mounting.
// For each SELECTED agent that has a user-skills dir, the staging dir gets the
// built-in skill suite (builtinskills.FS) plus a mirror of the host's
// per-agent user skills. Agents without a skills dir are skipped. Returns the
// staging directory (AGENTS_DIR/<cname>).
// homeDir is the host home (~) whose ~/.<agent>/skills dirs are the sources;
// agentNames is the selected set (nil → DefaultAgents). includeDev stages the
// source-tree-only skills (e.g. developing-yolo-jail) — pass
// WorkspaceIsYoloSourceTree(workspace). CRITICAL: entries are cleared *inside*
// each skills_dir — the dir itself is NEVER rmtree+mkdir'd, because a running
// jail's bind mount captured its inode and a fresh inode would silently detach
// attach-time refreshes.
func PrepareSkills(cname, homeDir string, agentNames []string, includeDev bool) (string, error) {
	staging := filepath.Join(paths.AgentsDir(), cname)
	if err := os.MkdirAll(staging, 0o755); err != nil {
		return "", err
	}

	specs := ResolveAgents(agentNames)
	for _, spec := range specs {
		if spec.Skills == "" {
			continue // agent has no user-skills dir (opencode, pi)
		}
		skillsDir := filepath.Join(staging, spec.SkillsStaging())
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
		// 2. Host user-level skills — strictly per-agent, no cross-agent merge.
		//    Written after the built-ins so a same-named host skill overrides.
		if err := copySkillSubdirs(filepath.Join(homeDir, spec.Skills), skillsDir); err != nil {
			return "", err
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
