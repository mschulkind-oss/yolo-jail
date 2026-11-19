package packload

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
)

// SkillPayloadTreeDir is reserved beside packs, outside every pack/discovery root.
const SkillPayloadTreeDir = "_skill-payloads"

type SkillPayloadTreeRecord struct {
	ID          string                  `json:"id"`
	Repository  string                  `json:"repository"`
	Commit      string                  `json:"commit"`
	Location    string                  `json:"location"`
	Files       int                     `json:"files"`
	Bytes       int64                   `json:"bytes"`
	Executables int                     `json:"executables"`
	Omissions   []packsrc.SkillOmission `json:"omissions,omitempty"`
}
type SkillTreeRow struct {
	Row      int    `json:"row"`
	Source   string `json:"source"`
	Alias    string `json:"as,omitempty"`
	Payload  string `json:"payload"`
	Location string `json:"location,omitempty"`
}

// StageSkillPayloads copies each payload once and fixes every registration's
// target root to the reading notch, before the composer writes any links.
func StageSkillPayloads(packs []*Pack, treeRoot, readingRoot string) error {
	copied := map[string]bool{}
	for _, p := range packs {
		for _, row := range p.SourcedSkillRows() {
			if row.Withheld != "" {
				continue
			}
			if row.Outcome.State != packsrc.SkillReady || row.Plan == nil {
				return fmt.Errorf("%s", row.Outcome.Diagnostic(fmt.Sprintf("pack %s skills[%d]", p.Name, row.Row)))
			}
			if row.LocalCarrier {
				location, err := filepath.Rel(treeRoot, row.Outcome.Payload)
				if err != nil || !filepath.IsLocal(location) {
					return fmt.Errorf("copied carrier is outside its launch tree; restart")
				}
				row.PayloadLocation = filepath.ToSlash(location)
				row.PayloadRoot = filepath.Join(readingRoot, location)
				p.sourcedSkills[row.Row] = row
				continue
			}
			id := packsrc.SkillPayloadIdentity(row.Outcome.Repo, row.Outcome.Commit)
			location := filepath.Join(SkillPayloadTreeDir, id)
			if !copied[id] {
				if err := packsrc.CopySkillPayload(row.Outcome.Payload, filepath.Join(treeRoot, location)); err != nil {
					return err
				}
				copied[id] = true
			}
			row.PayloadLocation = filepath.ToSlash(location)
			row.PayloadRoot = filepath.Join(readingRoot, location)
			if p.sourcedSkills == nil {
				p.sourcedSkills = map[int]SourcedSkill{}
			}
			p.sourcedSkills[row.Row] = row
		}
	}
	return nil
}

// MapSkillPayloadTargets changes only the reading notch; attach retains the
// running record and bytes, never re-resolving the shared host cache.
func MapSkillPayloadTargets(packs []*Pack, readingRoot string) {
	for _, p := range packs {
		for _, row := range p.SourcedSkillRows() {
			if row.PayloadLocation != "" {
				row.PayloadRoot = filepath.Join(readingRoot, filepath.FromSlash(row.PayloadLocation))
				p.sourcedSkills[row.Row] = row
			}
		}
	}
}

func (e PackTreeEntry) HydrateSourcedSkills(treeRoot string, p *Pack) error {
	p.SourcedSkillsWithheld = e.SourcedSkillsWithheld
	p.sourcedSkills = nil
	rows := p.SourcedSkillRows()
	if p.SourcedSkillsWithheld != "" && len(e.SkillRows) != 0 {
		return fmt.Errorf("pack %s: withheld sourced rows have recorded payloads — restore its launch tree or restart the jail", e.Name)
	}
	seen := map[int]bool{}
	for _, record := range e.SkillRows {
		if seen[record.Row] {
			return fmt.Errorf("pack %s: duplicate recorded sourced row %d — restore its launch tree or restart the jail", e.Name, record.Row)
		}
		seen[record.Row] = true
		payload, ok := e.skillPayloads[record.Payload]
		if !ok {
			return fmt.Errorf("pack %s skills[%d]: unknown recorded payload %q — restart the jail", e.Name, record.Row, record.Payload)
		}
		if record.Row < 0 || record.Row >= len(p.declaration()) {
			return fmt.Errorf("pack %s: invalid recorded skills row %d — restart the jail", e.Name, record.Row)
		}
		var selected *SourcedSkill
		for i := range rows {
			if rows[i].Row == record.Row {
				selected = &rows[i]
				break
			}
		}
		if selected == nil || selected.By.Source != record.Source || selected.By.As != record.Alias {
			return fmt.Errorf("pack %s skills[%d]: record disagrees with staged declaration — restart the jail", e.Name, record.Row)
		}
		addr, err := packsrc.Parse(record.Source)
		if err != nil || addr.Repo != payload.Repository {
			return fmt.Errorf("pack %s skills[%d]: source disagrees with recorded payload — restart the jail", e.Name, record.Row)
		}
		request := packsrc.SkillRequest{Label: fmt.Sprintf("pack %s skills[%d]", p.Name, record.Row), Source: record.Source, License: selected.By.License}
		physical := filepath.Join(treeRoot, filepath.FromSlash(payload.Location))
		plan, err := packsrc.ReadSkillPayloadTreeBeneath(treeRoot, physical, []packsrc.SkillRequest{request})
		if err != nil {
			return fmt.Errorf("%s: invalid recorded payload: %w — restore its launch tree or restart the jail", request.Label, err)
		}
		if len(plan.Omissions) != 0 {
			return fmt.Errorf("%s: recorded payload now omits %s — restore its launch tree or restart the jail", request.Label, plan.Omissions[0].Path)
		}
		plan.Omissions = payload.Omissions
		selected.Plan = &plan.Rows[0]
		selected.Outcome = packsrc.SkillPayloadResult{Repo: payload.Repository, Ref: addr.Ref, Commit: payload.Commit, Payload: physical, State: packsrc.SkillReady, Plan: plan}
		selected.PayloadLocation = payload.Location
		selected.PayloadRoot = physical
		if p.sourcedSkills == nil {
			p.sourcedSkills = map[int]SourcedSkill{}
		}
		p.sourcedSkills[record.Row] = *selected
	}
	if p.SourcedSkillsWithheld == "" {
		for _, row := range rows {
			if !seen[row.Row] {
				return fmt.Errorf("pack %s: missing recorded sourced row %d — restore its launch tree or restart the jail", e.Name, row.Row)
			}
		}
	}
	return nil
}

func validSkillPayloadLocation(r SkillPayloadTreeRecord) bool {
	return r.Repository != "" && len(r.Commit) == 40 && r.ID == packsrc.SkillPayloadIdentity(r.Repository, r.Commit) && r.Location == SkillPayloadTreeDir+"/"+r.ID && !strings.ContainsAny(r.Commit, "/\\")
}
