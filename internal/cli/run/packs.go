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
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/packstage"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/packs"
)

// packCtxDir is where the staged pack trees are mounted in the jail. The entrypoint
// finds it via YOLO_PACK_ROOT rather than hardcoding it, because on Apple Container the
// trees are read from their host path instead (no nested mount).
const packCtxDir = "/ctx/packs"

// stagePacks stages every pack for this run — the EMBEDDED official packs plus the
// user's configured ones — and returns them loaded, so the mount assembler can act on
// their declarations.
//
// Embedded packs come FIRST so a user pack can override one: later wins, the same rule
// packs already use for same-named skills.
//
// FAIL-CLOSED (A12): a pack that cannot be staged is an error. A jail that comes up
// silently missing a pack the user asked for is the failure mode this whole cluster of
// work exists to remove — and unlike a warning, an error is seen.
//
// Sets agents.SetPackSkillDirs as a side effect, which PrepareSkills consumes on the
// next call. Ordering is therefore load-bearing: stagePacks runs first.
func (o *Options) stagePacks(cname string) (string, []*packload.Pack, []agents.PackBriefing, error) {
	entries, err := config.LoadPacks(func(msg string) {
		o.pr(o.Stdout).print("[yellow]Warning: packs: " + msg + "[/yellow]")
	})
	if err != nil {
		return "", nil, nil, fmt.Errorf("packs: %w", err)
	}

	stagingRoot := filepath.Join(paths.AgentsDir(), cname, "packs")
	if err := os.MkdirAll(stagingRoot, 0o755); err != nil {
		return "", nil, nil, err
	}

	// The OFFICIAL packs, materialized out of the binary. They ship with yolo, so their
	// declarations carry yolo's own authority (MayAccessHost).
	loaded, problems := packload.MaterializeEmbedded(packs.FS,
		filepath.Join(stagingRoot, "_official"))
	for _, prob := range problems {
		// A broken OFFICIAL pack is a yolo bug, not a user error, so it is fatal rather
		// than a warning the user can do nothing about.
		return "", nil, nil, fmt.Errorf("official packs: %s", prob)
	}

	var skillDirs []string
	var briefings []agents.PackBriefing
	for _, p := range loaded {
		if skills := filepath.Join(p.Root, "skills"); isDir(skills) {
			skillDirs = append(skillDirs, skills)
		}
		if text, ok := readPackBriefing(p.Root); ok {
			briefings = append(briefings, agents.PackBriefing{Name: p.Name, Text: text})
		}
	}

	for _, entry := range entries {
		root, err := packRoot(entry)
		if err != nil {
			return "", nil, nil, err
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
			return "", nil, nil, fmt.Errorf("packs: %s: %w", entry.Name, err)
		}
		// NO SILENT CAPS: a pack that staged nothing is almost always an `only`/
		// `exclude` typo, and the user would otherwise just see a pack that "does
		// nothing". Say so, with the count that proves the tree was not empty.
		if len(res.Staged) == 0 {
			o.pr(o.Stdout).print(fmt.Sprintf(
				"[yellow]Warning: pack %s staged 0 files (%d excluded by only/exclude) — "+
					"check its filters[/yellow]", entry.Name, len(res.Excluded)))
		}

		// A configured pack's declarations are honored only as far as its ORIGIN
		// permits: a fetched pack cannot read the host home or run an installer.
		p, probs := packload.LoadDir(dest, entry.Name, entry.MayGrantHostFiles())
		for _, prob := range probs {
			return "", nil, nil, fmt.Errorf("packs: %s", prob)
		}
		// Report every refused declaration. A pack silently not getting what it asked
		// for changes what the jail contains, so the user has to be told.
		_, refused := p.HonoredHostFiles()
		if _, why := p.HonoredInstall(); why != "" {
			refused = append(refused, why)
		}
		for _, msg := range refused {
			o.pr(o.Stdout).print("[yellow]Warning: " + msg + "[/yellow]")
		}
		loaded = append(loaded, p)

		if skills := filepath.Join(dest, "skills"); isDir(skills) {
			skillDirs = append(skillDirs, skills)
		}
		if text, ok := readPackBriefing(dest); ok {
			briefings = append(briefings, agents.PackBriefing{Name: entry.Name, Text: text})
		}
	}
	agents.SetPackSkillDirs(skillDirs)
	return stagingRoot, loaded, briefings, nil
}

// packRoot resolves a pack entry to a directory on disk.
//
// LAUNCH IS STRICTLY OFFLINE (C5): it resolves from the store and never fetches. A
// jail start must not depend on a reachable git server, and a missing pin must be a
// clear error pointing at `yolo pack install` rather than a surprise network call
// mid-boot — or worse, a 30-second askpass hang that reads as yolo wedging.
func packRoot(entry config.PackEntry) (string, error) {
	addr, err := packsrc.Parse(entry.Source)
	if err != nil {
		return "", fmt.Errorf("packs: %s: %w", entry.Name, err)
	}
	store := &packsrc.Store{Dir: paths.PacksDir()}
	res, err := store.Resolve(addr)
	if err != nil {
		return "", fmt.Errorf("packs: %s: %w", entry.Name, err)
	}
	return res.Root, nil
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
