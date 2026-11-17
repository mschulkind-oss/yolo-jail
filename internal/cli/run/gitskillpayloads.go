package run

import (
	"github.com/mschulkind-oss/yolo-jail/internal/hostskills"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

func skillPayloadReadingRoot(cname, rt, hostRoot string) string {
	switch rt {
	case "apple-container":
		return acPackRootInJail
	case "macos-user":
		return macosuser.StagedPackTreeRoot(cname, hostRoot, "")
	default:
		return paths.ContextPacksDir
	}
}

// Called only after the final attach decision and fresh config approval. Earlier
// staging is a validating reader and cannot advance a Git skill pair or stamp.
func (o *Options) prepareFreshSkillPayloads(cname, rt string, staged stagedPacks) bool {
	hasRows := false
	for _, p := range staged.packs {
		hasRows = hasRows || len(p.SourcedSkillRows()) > 0
	}
	if !hasRows {
		return true
	}
	results := packload.ResolveSelectedSkillPayloads(staged.packs, launchStore(), packsrc.SkillResolveOptions{LockPath: packsrc.SkillsLockPath(paths.UserConfigPath()), Force: o.Update}, true)
	for _, result := range results {
		if result.Warning != "" {
			o.pr(o.Stderr).printf("[yellow]%s[/yellow]", result.Warning)
		}
		if result.State != packsrc.SkillReady {
			o.pr(o.Stderr).printf("Refusing fresh launch: %s", result.Diagnostic(result.Repo+"?ref="+result.Ref))
			return false
		}
	}
	if err := packload.StageSkillPayloads(staged.packs, staged.root, skillPayloadReadingRoot(cname, rt, staged.root)); err != nil {
		o.pr(o.Stderr).printf("Refusing fresh launch: %s — correct the sourced skill or pin a usable full commit", err)
		return false
	}
	if err := packload.WritePackTreeRecordForWorkspace(staged.root, staged.packs, o.Workspace); err != nil {
		o.pr(o.Stderr).printf("Cannot record skill payloads: %s — retry a fresh launch", err)
		return false
	}
	adoptPackRecords(staged.packs)
	hostskills.PrintSourcedSkillProjection(o.pr(o.Stderr).rt, staged.packs)
	return true
}
