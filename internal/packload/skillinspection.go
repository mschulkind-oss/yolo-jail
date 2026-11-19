package packload

import (
	"errors"
	"fmt"

	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
)

// InspectProjectedSkillRows reads existing Git plans and immutable shipped bytes
// without a carrier. Trusted-local plans remain publication-undetermined: their
// selected carriers follow eligible links and do not charge unselected pack bytes.
func InspectProjectedSkillRows(p *Pack, store *packsrc.Store, lockPath string) error {
	packs := []*Pack{p}
	results := ResolveSelectedSkillPayloads(packs, store, packsrc.SkillResolveOptions{LockPath: lockPath}, false)
	var refusals []error
	for _, result := range results {
		if result.State == packsrc.SkillRefused {
			refusals = append(refusals, fmt.Errorf("selected skill payload refused for %s: %s", SkillPayloadRowLabels(packs, result), result.Reason))
		}
	}
	if err := errors.Join(refusals...); err != nil {
		return err
	}
	if p.Projection == nil || p.Projection.Commit != "" || !p.Official || p.projectionSource == nil {
		return nil
	}
	var requests []packsrc.SkillRequest
	for _, row := range p.SourcedSkillRows() {
		selected := p.Projection.Rows[row.Row]
		if selected.Sourced {
			continue
		}
		request := p.skillRequest(row)
		request.StagedDir = selected.Path
		requests = append(requests, request)
	}
	if len(requests) == 0 {
		return nil
	}
	plan, err := packsrc.ReadEmbeddedSkillPayloadTree(p.projectionSource, requests)
	if err != nil {
		return err
	}
	if p.sourcedSkills == nil {
		p.sourcedSkills = map[int]SourcedSkill{}
	}
	for _, row := range p.SourcedSkillRows() {
		for i := range plan.Rows {
			if plan.Rows[i].Request.Label != skillRowLabel(p, row.Row) {
				continue
			}
			row.LocalCarrier = true
			row.Plan = &plan.Rows[i]
			row.Outcome = packsrc.SkillPayloadResult{State: packsrc.SkillNotMaterialized, Plan: plan, Reason: "read-only validation plan; no selected carrier copy exists"}
			p.sourcedSkills[row.Row] = row
		}
	}
	return nil
}
