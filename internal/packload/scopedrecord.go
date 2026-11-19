package packload

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
)

const ScopedCarrierDir = "_scoped-data"
const ScopedEntryMarker = "_scoped-entry"
const scopedRecordVersion = 2

type scopedPackEnvelope struct {
	Version int             `json:"version"`
	Entries []PackTreeEntry `json:"entries"`
}

func (r packTreeRecord) MarshalJSON() ([]byte, error) {
	scoped := false
	for _, entry := range r.Packs {
		scoped = scoped || entry.Projection != nil
	}
	if !scoped {
		type plain packTreeRecord
		return json.Marshal(plain(r))
	}
	return json.Marshal(struct {
		Packs    scopedPackEnvelope       `json:"packs"`
		Payloads []SkillPayloadTreeRecord `json:"skill_payloads,omitempty"`
	}{scopedPackEnvelope{Version: scopedRecordVersion, Entries: r.Packs}, r.SkillPayloads})
}

func (r *packTreeRecord) UnmarshalJSON(raw []byte) error {
	*r = packTreeRecord{}
	var wire struct {
		Packs    json.RawMessage          `json:"packs"`
		Payloads []SkillPayloadTreeRecord `json:"skill_payloads"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		return err
	}
	scoped := len(wire.Packs) > 0 && wire.Packs[0] == '{'
	if scoped {
		var envelope scopedPackEnvelope
		if err := json.Unmarshal(wire.Packs, &envelope); err != nil {
			return err
		}
		if envelope.Version != scopedRecordVersion || envelope.Entries == nil {
			return fmt.Errorf("unknown or missing scoped record version; restart with the matching yolo build")
		}
		r.Packs = envelope.Entries
	} else {
		if err := json.Unmarshal(wire.Packs, &r.Packs); err != nil {
			return err
		}
		for _, entry := range r.Packs {
			if entry.Projection != nil {
				return fmt.Errorf("scoped projection in an unversioned record; restore the launch tree or restart")
			}
		}
	}
	r.SkillPayloads = wire.Payloads
	return nil
}

// RefuseUnrecordedScopedPack prevents a new reader's legacy directory walk from
// silently dropping selected rows when the authoritative scoped record is lost.
func RefuseUnrecordedScopedPack(root string) error {
	if _, err := os.Lstat(filepath.Join(root, ScopedEntryMarker)); err == nil {
		return fmt.Errorf("scoped pack at %s requires its versioned launch record; restore the record or restart", root)
	} else if !os.IsNotExist(err) {
		return err
	}
	return nil
}

func validateRecordedProjection(p *SkillProjection) error {
	if p == nil || p.ImportIdentity == "" || p.Source == "" || p.Digest == "" || p.Mode != "typed" && p.Mode != "raw" {
		return fmt.Errorf("missing scoped projection identity/mode/digest; restart")
	}
	if err := p.Selection.Validate(); err != nil {
		return err
	}
	if p.Subdir != "" {
		if err := packsrc.ScopedPath(p.Subdir, false); err != nil {
			return err
		}
	}
	if p.Mode == "raw" && p.Selection.Dir == "" || p.Mode == "typed" && p.Selection.Dir != "" {
		return fmt.Errorf("scoped mode disagrees with selection; restart")
	}
	copy := *p
	if err := copy.RefreshDigest(); err != nil {
		return err
	}
	if copy.Digest != p.Digest {
		return fmt.Errorf("scoped projection digest disagrees with recorded rows; restore its launch tree or restart")
	}
	if len(p.Rows) == 0 || len(p.Rows) != len(p.Selected) {
		return fmt.Errorf("scoped selection has missing or extra rows; restart")
	}
	seen := map[string]bool{}
	for i, row := range p.Rows {
		if row.Name == "" || seen[row.Name] || row.Name != p.Selected[i] || row.By.Kind != packdecl.KindSkills || row.By.Source == "" || row.By.Agent != "" || row.By.From != "" || row.By.Into != "" || row.By.As != row.Name {
			return fmt.Errorf("invalid or duplicate scoped row %d; restore its launch tree or restart", i)
		}
		seen[row.Name] = true
		if row.Kind != "marker" && row.Kind != "directory" || row.MarkerState != "present" && row.MarkerState != "absent" {
			return fmt.Errorf("unknown/missing scoped row kind or marker state; restart")
		}
		if row.Kind == "marker" && row.MarkerState != "present" || row.Kind == "directory" && (p.Mode != "typed" || row.Sourced || row.Governor == "" || row.Path != path.Join(row.Governor, row.Name)) {
			return fmt.Errorf("ineligible scoped row kind; restore its launch tree or restart")
		}
		if !row.Sourced {
			if err := packsrc.ScopedPath(row.Path, true); err != nil {
				return err
			}
		}
	}
	return nil
}

// LoadRecordedScopedPack is the shared host/jail reader. It uses only the
// versioned effective rows and staged bytes, never original declarations or stores.
func (e PackTreeEntry) LoadRecordedScopedPack(tree string) (*Pack, error) {
	if err := validateRecordedProjection(e.Projection); err != nil {
		return nil, fmt.Errorf("pack %s: %w", e.Name, err)
	}
	if e.SourcedSkillsWithheld != "" || e.Namespace != e.Projection.Namespace || e.NamespaceOwner != e.Projection.NamespaceOwner {
		return nil, fmt.Errorf("pack %s: naming/withholding disagrees with scope; restore its launch tree or restart", e.Name)
	}
	root := filepath.Join(tree, filepath.FromSlash(e.Dir))
	marker, err := os.Lstat(filepath.Join(root, ScopedEntryMarker))
	if err != nil || !marker.Mode().IsRegular() {
		return nil, fmt.Errorf("pack %s: scoped entry marker missing or nonregular; restart", e.Name)
	}
	markerBytes, err := os.ReadFile(filepath.Join(root, ScopedEntryMarker))
	if err != nil || string(markerBytes) != e.Projection.Digest+"\n" {
		return nil, fmt.Errorf("pack %s: scoped entry marker disagrees with projection; restart", e.Name)
	}
	p := &Pack{Name: e.Name, Root: root, Decl: &packdecl.Manifest{}, Projection: e.Projection}
	for _, row := range e.Projection.Rows {
		p.Decl.Contributes = append(p.Decl.Contributes, row.By)
	}
	e.ApplyNaming(p)
	if err := e.hydrateScopedRows(tree, p); err != nil {
		return nil, err
	}
	return p, nil
}

func (e PackTreeEntry) hydrateScopedRows(tree string, p *Pack) error {
	if len(e.SkillRows) != len(p.Projection.Rows) {
		return fmt.Errorf("pack %s: missing/extra recorded scoped rows; restore its launch tree or restart", p.Name)
	}
	seen := map[int]bool{}
	var copiedPlan *packsrc.SkillPayloadPlan
	for _, record := range e.SkillRows {
		if record.Row < 0 || record.Row >= len(p.Projection.Rows) || seen[record.Row] {
			return fmt.Errorf("pack %s: invalid/duplicate scoped row %d; restart", p.Name, record.Row)
		}
		seen[record.Row] = true
		selected := p.Projection.Rows[record.Row]
		row := p.SourcedSkillRows()[record.Row]
		if record.Source != row.By.Source || record.Alias != row.By.As {
			return fmt.Errorf("pack %s: recorded row source/name disagrees with projection; restart", p.Name)
		}
		request := p.skillRequest(row)
		location := ""
		repo, ref, commit := "", "", ""
		if row.LocalCarrier {
			location = path.Join(e.Dir, ScopedCarrierDir)
			if record.Payload != "copy" || record.Location != location {
				return fmt.Errorf("pack %s: invalid copied carrier location; restart", p.Name)
			}
			request.StagedDir = selected.Path
		} else {
			payload, ok := e.skillPayloads[record.Payload]
			if !ok || record.Location != "" {
				return fmt.Errorf("pack %s: unknown/incompatible recorded Git payload; restart", p.Name)
			}
			address, err := packsrc.Parse(record.Source)
			if err != nil || address.Repo != payload.Repository || selected.ResolvedCommit == "" || payload.Commit != selected.ResolvedCommit {
				return fmt.Errorf("pack %s: source/pin disagrees with recorded payload; restart", p.Name)
			}
			location = payload.Location
			repo, ref, commit = payload.Repository, address.Ref, payload.Commit
		}
		physical := filepath.Join(tree, filepath.FromSlash(location))
		plan, err := packsrc.ReadSkillPayloadTreeBeneath(tree, physical, []packsrc.SkillRequest{request})
		if err != nil || len(plan.Omissions) > 0 {
			return fmt.Errorf("pack %s row %d: invalid recorded carrier: %v; restore its launch tree or restart", p.Name, record.Row, err)
		}
		if !row.LocalCarrier {
			plan.Omissions = e.skillPayloads[record.Payload].Omissions
		} else {
			copiedPlan = plan
		}
		row.Plan = &plan.Rows[0]
		row.PayloadRoot, row.PayloadLocation = physical, location
		row.Outcome = packsrc.SkillPayloadResult{Repo: repo, Ref: ref, Commit: commit, Payload: physical, State: packsrc.SkillReady, Plan: plan}
		if p.sourcedSkills == nil {
			p.sourcedSkills = map[int]SourcedSkill{}
		}
		p.sourcedSkills[row.Row] = row
	}
	if copiedPlan != nil {
		if copiedCarrierDigest(copiedPlan) != p.Projection.CopiedContentDigest {
			return fmt.Errorf("pack %s: copied carrier bytes changed; restore its launch tree or restart", p.Name)
		}
		for _, entry := range copiedPlan.Entries {
			allowed := false
			for _, selected := range p.Projection.Rows {
				if selected.Sourced {
					continue
				}
				rel := selected.Path
				if rel == "." || entry.Path == rel || strings.HasPrefix(entry.Path, rel+"/") || entry.Mode == "040000" && strings.HasPrefix(rel, entry.Path+"/") {
					allowed = true
					break
				}
			}
			for _, license := range []string{"LICENSE", "LICENSE.md", "LICENSE.txt", "COPYING"} {
				allowed = allowed || entry.Path == license && (entry.Mode == "100644" || entry.Mode == "100755")
			}
			if !allowed {
				return fmt.Errorf("pack %s: unrecorded copied content %q; restore its launch tree or restart", p.Name, entry.Path)
			}
		}
	}
	expected := []string{ScopedEntryMarker}
	if copiedPlan != nil {
		expected = append(expected, ScopedCarrierDir)
	}
	actual, err := os.ReadDir(p.Root)
	if err != nil {
		return err
	}
	var names []string
	for _, entry := range actual {
		names = append(names, entry.Name())
	}
	// os.ReadDir is sorted; normalize expected order the same way.
	if len(expected) == 2 {
		expected = []string{ScopedCarrierDir, ScopedEntryMarker}
	}
	if !reflect.DeepEqual(names, expected) {
		return fmt.Errorf("pack %s: unrecorded original-root content in scoped entry; restore its launch tree or restart", p.Name)
	}
	return nil
}
