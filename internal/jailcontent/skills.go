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
// packSkillDirs are the per-pack `skills/` sources to layer in, in config
// order (C3). Threaded as a package-level var rather than a parameter so the
// existing four-arg signature and its callers stay untouched; the CLI sets it once
// per run, before PrepareSkills.
//
// Precedence within one agent's staging dir: built-ins < packs, with the
// CONVENTIONAL LOCAL PACK last among the packs (config.LoadPacks appends it there).
// So a pack may override a built-in — a legitimate reason to ship one — and the
// user's own copy still outranks a shared pack's. There is no fourth layer: the one
// that read the destination back in was S3's defect.
//
// IT CARRIES AN AUDIENCE PER SOURCE since briefing-audiences.md, where it was a flat
// []string. A flat list was the `skills` half of the same defect the briefing half had: the
// list is GLOBAL — every selected pack's skills reach every destination — so a source that
// arrived as a bare path had no way to say who it was for, and a claude-specific skill was
// copied into ~/.pi/agent/skills with nothing able to stop it.
var packSkillDirs []PackSkillSource

// PackSkillSource is one skills SOURCE to layer in, and the audience it names.
//
// A re-declaration of packload.SkillsSource rather than a use of it, and that is not
// duplication for its own sake: jailcontent is core's own content package and does not import
// packload (the boot path stages a tree and this reads it), so the two types meet at the CLI,
// which resolves one into the other. The FIELDS are deliberately identical so the conversion
// is a copy with nothing to get wrong.
type PackSkillSource struct {
	// Dir is the absolute source directory to copy skill subdirs from.
	Dir string
	// Agents is the audience this source names. EMPTY MEANS BROADCAST (P2) — every pack that
	// ships today, and the only thing a pack with no pack.json can ask for.
	Agents []string
}

// SetPackSkillDirs sets the pack skills sources consulted by the next PrepareSkills.
// Passing nil clears them.
func SetPackSkillDirs(sources []PackSkillSource) { packSkillDirs = sources }

// PackSkillDirs returns what SetPackSkillDirs last set — the SOURCES the next
// PrepareSkills will copy from. Exported so the CLI's own tests can assert which dir a
// pack's `skills` contribution resolved to, which is otherwise observable only by
// inspecting the staged output after a full PrepareSkills run.
func PackSkillDirs() []PackSkillSource { return packSkillDirs }

// SkillTarget is one pack-declared skills destination: which staging dir to build, and
// the jail path it will be mounted at.
type SkillTarget struct {
	// Staging is the staging subdir name (per PACK, so two packs cannot collide).
	Staging string
	// Dest is the home-relative jail path the staged dir is mounted at.
	Dest string
	// Agent is the IDENTITY the declaring pack gave this destination — the launcher command
	// whose agent reads it — or "" for a destination that declared none.
	//
	// "" is not an error: it means no `agents` selector can name this destination, which is
	// the state every pack.json was in before the field existed (R4). Such a destination
	// still receives every broadcast source; it just cannot be addressed.
	Agent string

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
		for _, src := range packSkillDirs {
			// THE AUDIENCE FILTER, and it is the whole `skills` half of
			// briefing-audiences.md. The list is global, so this is the only point at which
			// "who is this content for?" can be asked — and it is asked against the string
			// the DESTINATION declared about itself, never anything derived (OQ-BA2).
			if !sourceAddressesAgent(src.Agents, target.Agent) {
				continue
			}
			if err := copySkillSubdirs(src.Dir, skillsDir); err != nil {
				return "", err
			}
		}
	}
	return staging, nil
}

// sourceAddressesAgent reports whether a source naming `agents` belongs in the staging dir of
// a destination whose declared identity is `agent`.
//
// The `skills` twin of briefing.go's addressesAgent, and identical by intent: EMPTY IS
// BROADCAST, so a jail of packs that name nobody stages exactly what it did before, and an
// empty `agent` receives every broadcast and no addressed source. It is a second small
// predicate rather than a shared one because the two live in the same package and neither
// imports the other's type — folding them would mean a shared helper over two field lists,
// which is more coupling than the four lines are worth.
func sourceAddressesAgent(agents []string, agent string) bool {
	if len(agents) == 0 {
		return true
	}
	for _, a := range agents {
		if a == agent {
			return true
		}
	}
	return false
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
