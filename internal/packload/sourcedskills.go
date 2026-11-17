package packload

import (
	"fmt"

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
			request := packsrc.SkillRequest{Label: fmt.Sprintf("pack %s skills[%d]", p.Name, row.Row), Source: row.By.Source, License: row.By.License}
			requests = append(requests, request)
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

// BindSelectedSkillPayloads preserves one invocation's validated snapshot across
// host migration reloads instead of consulting a potentially newer shared record.
func BindSelectedSkillPayloads(packs []*Pack, results []packsrc.SkillPayloadResult) {
	for _, p := range packs {
		if p == nil {
			continue
		}
		for _, row := range p.SourcedSkillRows() {
			if row.Withheld != "" {
				continue
			}
			label := fmt.Sprintf("pack %s skills[%d]", p.Name, row.Row)
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
		}
	}
}
