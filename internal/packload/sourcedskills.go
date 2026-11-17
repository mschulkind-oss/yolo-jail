package packload

import (
	"fmt"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
)

// SourcedSkill is a declaration row, never a local directory governor. Row is
// its original declaration index, preserved through audience/destination clones.
type SourcedSkill struct {
	Row         int
	By          packdecl.Contribution
	Outcome     packsrc.SkillPayloadResult
	Plan        *packsrc.SkillRowPlan
	PayloadRoot string
	Withheld    string
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
	type selected struct {
		p       *Pack
		row     SourcedSkill
		request packsrc.SkillRequest
	}
	var rows []selected
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
			rows = append(rows, selected{p, row, request})
		}
	}
	var results []packsrc.SkillPayloadResult
	if write {
		results = store.ResolveSkillPayloads(requests, opts)
	} else {
		results = store.ReadSkillPayloads(requests, opts.LockPath)
	}
	for _, selected := range rows {
		a, _ := packsrc.Parse(selected.request.Source)
		row := selected.row
		for _, result := range results {
			if result.Repo == a.Repo && result.Ref == a.Ref {
				row.Outcome = result
				row.PayloadRoot = result.Payload
				if result.Plan != nil {
					for i := range result.Plan.Rows {
						plan := &result.Plan.Rows[i]
						if plan.Request.Label == selected.request.Label {
							row.Plan = plan
							break
						}
					}
				}
				break
			}
		}
		if selected.p.sourcedSkills == nil {
			selected.p.sourcedSkills = map[int]SourcedSkill{}
		}
		selected.p.sourcedSkills[row.Row] = row
	}
	return results
}
