package hostskills

import (
	"errors"
	"fmt"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

// InspectScopedPack resolves and reports one authored selection without any
// transport, checkout, carrier copy or publication capability.
func InspectScopedPack(pr richtext.Printer, entry config.PackEntry, getenv func(string) string) (*packload.Pack, error) {
	mode := "typed"
	if entry.Skills.Dir != "" {
		mode = "raw"
	}
	pr.Printf("  mode=%s subdir=%q skills.dir=%q; only=%v exclude=%v; namespace=%q", mode, entry.Subdir, entry.Skills.Dir, entry.Skills.Only, entry.Skills.Exclude, entry.Namespace)
	pr.Printf("  source: %s; origin: %s", richtext.Escape(entry.Source), richtext.Escape(entry.ConfigOrigin))
	resolved, err := config.InspectPack(entry, config.ResolvePackSpec{Getenv: getenv})
	if err != nil {
		if !entry.Embedded() && (errors.Is(err, packsrc.ErrNotFetched) || errors.Is(err, packsrc.ErrNotCheckedOut) || errors.Is(err, packsrc.ErrDefaultRefUnavailable)) {
			pr.Printf("  selection not yet verified; resolved at next launch")
			return nil, nil
		}
		return nil, err
	}
	p := resolved.Pack
	if err := packload.InspectProjectedSkillRows(p, packsrc.LaunchStore(paths.PacksDir()), packsrc.SkillsLockPath(paths.UserConfigPath())); err != nil {
		return nil, fmt.Errorf("%s: %w; repair selected content, then rerun yolo check", entry.ConfigOrigin, err)
	}
	switch {
	case entry.Embedded():
		pr.Printf("  inspected: shipped")
	case entry.IsLocal():
		pr.Printf("  inspected: local directory")
	default:
		pr.Printf("  inspected: cached commit %s", p.Projection.Commit)
	}
	PrintSourcedSkillInspection(pr, []*packload.Pack{p})
	return p, nil
}
