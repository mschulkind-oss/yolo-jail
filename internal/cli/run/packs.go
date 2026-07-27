package run

// packs.go is the host-side pack pipeline (C3): resolve the user's `packs` entries,
// stage each pack's tree, and hand the results to skills staging and briefing
// composition.
//
// Everything here runs on the HOST, before the container exists, because that is
// where the inputs live: the pack store, the user config, and (later) git
// credentials. That is the "what needs the host" half of the composition-site
// ruling — the jail never reads config and never fetches.

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/mschulkind-oss/yolo-jail/internal/agents"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/packstage"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// stagePacks stages every configured pack for this run and returns their briefing
// contributions in config order.
//
// FAIL-CLOSED (A12): a declared pack that cannot be staged is an error. A jail that
// comes up silently missing a pack the user asked for is the failure mode this
// whole cluster of work exists to remove — and unlike a warning, an error is seen.
//
// Sets agents.SetPackSkillDirs as a side effect, which PrepareSkills consumes on the
// next call. Ordering is therefore load-bearing: stagePacks runs first.
func (o *Options) stagePacks(cname string) ([]agents.PackBriefing, error) {
	entries, err := config.LoadPacks(func(msg string) {
		o.pr(o.Stdout).print("[yellow]Warning: packs: " + msg + "[/yellow]")
	})
	if err != nil {
		return nil, fmt.Errorf("packs: %w", err)
	}
	if len(entries) == 0 {
		agents.SetPackSkillDirs(nil)
		return nil, nil
	}

	stagingRoot := filepath.Join(paths.AgentsDir(), cname, "packs")
	if err := os.MkdirAll(stagingRoot, 0o755); err != nil {
		return nil, err
	}

	var skillDirs []string
	var briefings []agents.PackBriefing
	for _, entry := range entries {
		root, err := packRoot(entry)
		if err != nil {
			return nil, err
		}
		dest := filepath.Join(stagingRoot, entry.Slug())
		res, err := packstage.Stage(packstage.Spec{
			Root:      root,
			Dest:      dest,
			Only:      entry.Only,
			Exclude:   entry.Exclude,
			AllowExec: entry.AllowExec,
		})
		if err != nil {
			return nil, fmt.Errorf("packs: %s: %w", entry.Name, err)
		}
		// NO SILENT CAPS: a pack that staged nothing is almost always an `only`/
		// `exclude` typo, and the user would otherwise just see a pack that "does
		// nothing". Say so, with the count that proves the tree was not empty.
		if len(res.Staged) == 0 {
			o.pr(o.Stdout).print(fmt.Sprintf(
				"[yellow]Warning: pack %s staged 0 files (%d excluded by only/exclude) — "+
					"check its filters[/yellow]", entry.Name, len(res.Excluded)))
		}

		if skills := filepath.Join(dest, "skills"); isDir(skills) {
			skillDirs = append(skillDirs, skills)
		}
		if text, ok := readPackBriefing(dest); ok {
			briefings = append(briefings, agents.PackBriefing{Name: entry.Name, Text: text})
		}
	}
	agents.SetPackSkillDirs(skillDirs)
	return briefings, nil
}

// packRoot resolves a pack entry to a directory on disk.
//
// Phase 0 handles file:// only. A git+ source is a clear, actionable error rather
// than a silent skip: the entry is valid config that this build cannot yet fetch,
// which is exactly the distinction a user needs to see.
func packRoot(entry config.PackEntry) (string, error) {
	if !entry.IsLocal() {
		return "", fmt.Errorf("packs: %s: git sources are not implemented yet "+
			"(%s) — use a file:// path for now", entry.Name, entry.Source)
	}
	path := entry.Source[len("file://"):]
	if path == "" {
		return "", fmt.Errorf("packs: %s: empty file:// path", entry.Name)
	}
	if !isDir(path) {
		return "", fmt.Errorf("packs: %s: %s is not a directory", entry.Name, path)
	}
	return path, nil
}

// readPackBriefing reads a pack's briefing prose, accepting either AGENTS.md or
// CLAUDE.md at the pack root. Both names are in the wild, and a pack author should
// not have to know which one yolo happens to read.
func readPackBriefing(dest string) (string, bool) {
	for _, name := range []string{"AGENTS.md", "CLAUDE.md"} {
		if data, err := os.ReadFile(filepath.Join(dest, name)); err == nil {
			return string(data), true
		}
	}
	return "", false
}
