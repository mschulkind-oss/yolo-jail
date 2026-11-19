package packload

import (
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
)

// SourcedSkill is a declaration row, never a local directory governor. Row is
// its original declaration index, preserved through audience/destination clones.
type SourcedSkill struct {
	Row             int
	By              packdecl.Contribution
	Outcome         packsrc.SkillPayloadResult
	Plan            *packsrc.SkillRowPlan
	PayloadRoot     string
	PayloadLocation string
	Withheld        string
	LocalCarrier    bool
}

// SourcedSkillRows includes unresolved declarations so static claims never depend
// on fetching. A legacy only filter withholds these rows before any store work.
func (p *Pack) SourcedSkillRows() []SourcedSkill {
	var rows []SourcedSkill
	for i, c := range p.declaration() {
		if !c.IsSourcedSkill() {
			continue
		}
		row := SourcedSkill{Row: i, By: c, Withheld: p.SourcedSkillsWithheld, Outcome: packsrc.SkillPayloadResult{State: packsrc.SkillNotFetched, Reason: "no shared record"}}
		if p.Projection != nil && i < len(p.Projection.Rows) && !p.Projection.Rows[i].Sourced && p.Projection.Commit == "" {
			row.LocalCarrier = true
			row.Outcome.State = packsrc.SkillNotMaterialized
			row.Outcome.Reason = "selected local directory is not copied; the next fresh launch or host writer validates its carrier"
		}
		if bound, ok := p.sourcedSkills[i]; ok {
			row = bound
		}
		rows = append(rows, row)
	}
	return rows
}

// SourcedSkillsFor matches one real or synthesized contribution to its original row.
func (p *Pack) SourcedSkillsFor(c packdecl.Contribution) []SourcedSkill {
	if !c.IsSourcedSkill() || c.Agent != "" {
		return nil
	}
	var out []SourcedSkill
	for _, row := range p.SourcedSkillRows() {
		if c.SkillRow > 0 && row.Row != c.SkillRow-1 {
			continue
		}
		if row.By.Source == c.Source && row.By.As == c.As && row.By.License == c.License {
			out = append(out, row)
		}
	}
	return out
}

// ResolveSelectedSkillPayloads is the single pack-set projection boundary. The
// complete selected closure is gathered before the grouped store invocation,
// whatever the audience. It never loads a payload as a pack or plugin.
func ResolveSelectedSkillPayloads(packs []*Pack, store *packsrc.Store, opts packsrc.SkillResolveOptions, write bool) []packsrc.SkillPayloadResult {
	var requests []packsrc.SkillRequest
	for _, p := range packs {
		if p == nil {
			continue
		}
		for _, row := range p.SourcedSkillRows() {
			if row.Withheld != "" {
				continue
			}
			if row.LocalCarrier {
				continue
			}
			requests = append(requests, p.skillRequest(row))
		}
	}
	var results []packsrc.SkillPayloadResult
	if write {
		results = store.ResolveSkillPayloads(requests, opts)
	} else {
		results = store.ReadSkillPayloads(requests, opts.LockPath)
	}
	BindSelectedSkillPayloads(packs, results)
	return results
}

// SkillPayloadRowLabels names the caller's rows sharing a result, including
// failures that have no validated plan. It consults declarations only.
func SkillPayloadRowLabels(packs []*Pack, result packsrc.SkillPayloadResult) string {
	var labels []string
	for _, p := range packs {
		if p == nil {
			continue
		}
		for _, row := range p.SourcedSkillRows() {
			if row.Withheld != "" {
				continue
			}
			addr, err := packsrc.Parse(row.By.Source)
			if err == nil && addr.Repo == result.Repo && addr.Ref == result.Ref {
				labels = append(labels, skillRowLabel(p, row.Row))
			}
		}
	}
	if len(labels) == 0 {
		return result.Repo + "?ref=" + result.Ref
	}
	return strings.Join(labels, ", ")
}

// BindSelectedSkillPayloads preserves one invocation's validated snapshot across
// host migration reloads instead of consulting a potentially newer shared record.
func BindSelectedSkillPayloads(packs []*Pack, results []packsrc.SkillPayloadResult) {
	for _, p := range packs {
		if p == nil {
			continue
		}
		for _, row := range p.SourcedSkillRows() {
			if row.Withheld != "" || row.LocalCarrier {
				continue
			}
			label := skillRowLabel(p, row.Row)
			a, _ := packsrc.Parse(row.By.Source)
			for _, result := range results {
				if result.Repo != a.Repo || result.Ref != a.Ref {
					continue
				}
				row.Outcome = result
				row.PayloadRoot = result.Payload
				row.Plan = nil
				if result.Plan != nil {
					for i := range result.Plan.Rows {
						plan := &result.Plan.Rows[i]
						if plan.Request.Label == label && plan.Request.Source == row.By.Source && plan.Request.License == row.By.License {
							row.Plan = plan
							break
						}
					}
				}
				if result.State == packsrc.SkillReady && row.Plan == nil {
					row.Outcome.State = packsrc.SkillRefused
					row.Outcome.Reason = "selection changed during apply — rerun yolo host apply to validate the complete selected pair"
				}
				break
			}
			if p.sourcedSkills == nil {
				p.sourcedSkills = map[int]SourcedSkill{}
			}
			p.sourcedSkills[row.Row] = row
			if p.Projection != nil && row.Outcome.State == packsrc.SkillReady {
				p.Projection.Rows[row.Row].ResolvedCommit = row.Outcome.Commit
				_ = p.Projection.RefreshDigest()
			}
		}
	}
}
