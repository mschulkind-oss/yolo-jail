package packload

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path"
	"path/filepath"
	"sort"

	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
)

func skillRowLabel(p *Pack, row int) string {
	if p.Projection != nil {
		return fmt.Sprintf("pack %s import %q skills[%d]", p.Name, p.Projection.ImportIdentity, row)
	}
	return fmt.Sprintf("pack %s skills[%d]", p.Name, row)
}

func (p *Pack) skillRequest(row SourcedSkill) packsrc.SkillRequest {
	request := packsrc.SkillRequest{Label: skillRowLabel(p, row.Row), Source: row.By.Source, License: row.By.License}
	if p.Projection == nil {
		return request
	}
	selected := p.Projection.Rows[row.Row]
	root := "."
	if p.Projection.Commit != "" && !selected.Sourced {
		address, _ := packsrc.ParsePackAddress(p.Projection.Source)
		root = path.Join(address.Path, p.Projection.Subdir)
	}
	request.Projection = &packsrc.SkillProjectionEvidence{Mode: p.Projection.Mode, Kind: selected.Kind, MarkerState: selected.MarkerState, PackRoot: root, Governor: selected.Governor, Child: selected.Name, Directory: selected.SourceDirectory}
	return request
}

// BindProjectedLocalCarrier validates a selected-directory copy with the same
// marker/kind, bounds, links and license rules as Git payloads. It retains no
// original-source filesystem dependency in the common registration snapshot.
func BindProjectedLocalCarrier(p *Pack, carrier string) error {
	if p.Projection == nil {
		return fmt.Errorf("local carrier requires a scoped projection")
	}
	var requests []packsrc.SkillRequest
	for _, row := range p.SourcedSkillRows() {
		selected := p.Projection.Rows[row.Row]
		if selected.Sourced || p.Projection.Commit != "" {
			continue
		}
		request := p.skillRequest(row)
		request.StagedDir = selected.Path
		requests = append(requests, request)
	}
	if len(requests) == 0 {
		return nil
	}
	plan, err := packsrc.ReadSkillPayloadTreeBeneath(filepath.Dir(carrier), carrier, requests)
	if err != nil {
		return err
	}
	if len(plan.Omissions) != 0 {
		return fmt.Errorf("selected local carrier omits %q; repair the selected directory", plan.Omissions[0].Path)
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
			row.PayloadRoot = carrier
			row.Plan = &plan.Rows[i]
			row.Outcome = packsrc.SkillPayloadResult{State: packsrc.SkillReady, Payload: carrier, Plan: plan}
			p.sourcedSkills[row.Row] = row
		}
	}
	p.Projection.CopiedContentDigest = copiedCarrierDigest(plan)
	return p.Projection.RefreshDigest()
}

func copiedCarrierDigest(plan *packsrc.SkillPayloadPlan) string {
	entries := append([]packsrc.SkillEntry(nil), plan.Entries...)
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	raw, _ := json.Marshal(entries)
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}
