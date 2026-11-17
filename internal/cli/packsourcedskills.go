package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/hostskills"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

func configuredSourcedSkillProjection(write, force bool, pr richtext.Printer) ([]*packload.Pack, int) {
	provenEmpty := true
	selected := selectHostPacks(func(e config.PackEntry) (*packload.Pack, error) {
		p, err := resolveConfiguredPackWithSpec(e, hostPackResolveSpec(true))
		if err == nil {
			if len(p.SourcedSkillRows()) > 0 {
				provenEmpty = false
			}
		} else {
			has, proofErr := selectedEntryDeclaresSourced(e)
			if has || proofErr != nil {
				provenEmpty = false
			}
		}
		return p, err
	}, config.UserScopeSelection())
	if provenEmpty && selected.loadErr == nil && len(selected.entryProblems) == 0 && selected.closureErr == nil {
		return selected.packs, 0
	}
	if problems := selected.problems(); len(problems) != 0 {
		for _, row := range problems {
			pr.Printf("  sourced skills selection incomplete: %s — correct the named pack or remove it with yolo pack remove", richtext.Escape(row.Reason))
		}
		return selected.packs, 1
	}
	outcomes := packload.ResolveSelectedSkillPayloads(selected.packs, packsrc.LaunchStore(paths.PacksDir()), packsrc.SkillResolveOptions{LockPath: packsrc.SkillsLockPath(paths.UserConfigPath()), Force: force}, write)
	rc := 0
	for _, outcome := range outcomes {
		if write && (outcome.State != packsrc.SkillReady || outcome.Warning != "") {
			rc = 1
		}
	}
	return selected.packs, rc
}

func lintSourcedSkillsOnline(p *packload.Pack) ([]string, []string) {
	if len(p.SourcedSkillRows()) == 0 {
		return nil, nil
	}
	scratch, err := os.MkdirTemp("", "yolo-skill-lint-")
	if err != nil {
		return []string{"cannot create online sourced-skill scratch: " + err.Error() + " — retry yolo pack lint --online"}, nil
	}
	defer func() { _ = packsrc.RemoveSealedSkillTree(filepath.Dir(scratch), scratch) }()
	results := packload.ResolveSelectedSkillPayloads([]*packload.Pack{p}, &packsrc.Store{Dir: scratch}, packsrc.SkillResolveOptions{LockPath: filepath.Join(scratch, packsrc.SkillsLockName), Force: true}, true)
	var problems []string
	for _, result := range results {
		if result.State != packsrc.SkillReady {
			problems = append(problems, result.Diagnostic(packload.SkillPayloadRowLabels([]*packload.Pack{p}, result)))
		}
	}
	var output bytes.Buffer
	printSourcedSkillProjection(richtext.Printer{W: &output}, []*packload.Pack{p})
	return problems, strings.Split(strings.TrimSpace(output.String()), "\n")
}

func printSourcedSkillProjection(pr richtext.Printer, packs []*packload.Pack) {
	hostskills.PrintSourcedSkillProjection(pr, packs)
}
