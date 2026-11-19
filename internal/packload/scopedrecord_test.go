package packload

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
)

// The decoder struct from 15da1299ffe8c65db4a3f2beef9f8421dabbb99c:
// its packs field is an array, not a version-max switch in today's reader.
type baselinePackTreeRecord struct {
	Packs []struct {
		Name           string                        `json:"name"`
		Dir            string                        `json:"dir"`
		Skipped        []string                      `json:"skipped,omitempty"`
		PatchBranches  map[string]PatchBranchMapping `json:"patch_branches,omitempty"`
		Namespace      string                        `json:"namespace,omitempty"`
		NamespaceOwner string                        `json:"namespace_owner,omitempty"`
		Landing        string                        `json:"landing,omitempty"`
		BareStep       string                        `json:"bare_step,omitempty"`
		SkillRows      []struct {
			Row     int    `json:"row"`
			Source  string `json:"source"`
			Alias   string `json:"as,omitempty"`
			Payload string `json:"payload"`
		} `json:"skill_rows,omitempty"`
		SourcedSkillsWithheld string `json:"sourced_skills_withheld,omitempty"`
		skillPayloads         map[string]SkillPayloadTreeRecord
	} `json:"packs"`
	SkillPayloads []SkillPayloadTreeRecord `json:"skill_payloads,omitempty"`
}

func scopedRecordFixture(t *testing.T) (string, []byte) {
	t.Helper()
	root := t.TempDir()
	p := &Pack{Name: "local/corpus", Root: filepath.Join(root, "selected"), Decl: &packdecl.Manifest{}, Projection: &SkillProjection{Mode: "typed", Source: "file:///fixture/corpus", Selection: packsrc.SkillSelection{Only: []string{"chosen"}}, ImportIdentity: "local/corpus\x00\x00", Selected: []string{"chosen"}, Candidates: []string{"chosen"}, Rows: []ProjectedSkillRow{{Name: "chosen", Path: "skills/chosen", Governor: "skills", Kind: "directory", MarkerState: "absent", By: packdecl.Contribution{Kind: packdecl.KindSkills, Source: "file:///fixture/corpus/skills/chosen", As: "chosen"}}}}}
	p.Decl.Contributes = append(p.Decl.Contributes, p.Projection.Rows[0].By)
	carrier := filepath.Join(p.Root, ScopedCarrierDir)
	if err := os.MkdirAll(filepath.Join(carrier, "skills/chosen"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(carrier, "skills/chosen/notes.md"), []byte("folder\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := BindProjectedLocalCarrier(p, carrier); err != nil {
		t.Fatal(err)
	}
	if err := StageSkillPayloads([]*Pack{p}, root, root); err != nil {
		t.Fatal(err)
	}
	if err := WritePackTreeRecord(root, []*Pack{p}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(root, PackTreeRecordName))
	if err != nil {
		t.Fatal(err)
	}
	return root, raw
}

func TestScopedRecordRejectsActualBaselineArrayDecoder(t *testing.T) {
	_, raw := scopedRecordFixture(t)
	var old baselinePackTreeRecord
	if err := json.Unmarshal(raw, &old); err == nil || !strings.Contains(err.Error(), "cannot unmarshal object") {
		t.Fatalf("baseline accepted scoped record: %v", err)
	}
	var modern packTreeRecord
	if err := json.Unmarshal(raw, &modern); err != nil {
		t.Fatal(err)
	}
	// Normal old-shaped records still roundtrip as arrays, and both readers accept them.
	normal, err := json.Marshal(packTreeRecord{Packs: []PackTreeEntry{{Name: "normal", Dir: "normal"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(normal, &old); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(normal, &modern); err != nil {
		t.Fatal(err)
	}
}

func TestScopedRecordRefusesRowAndVersionTampering(t *testing.T) {
	root, raw := scopedRecordFixture(t)
	var original packTreeRecord
	if err := json.Unmarshal(raw, &original); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		mutate func(*packTreeRecord)
	}{
		{"missing row", func(r *packTreeRecord) { r.Packs[0].SkillRows = nil }},
		{"duplicate row", func(r *packTreeRecord) { r.Packs[0].SkillRows = append(r.Packs[0].SkillRows, r.Packs[0].SkillRows[0]) }},
		{"unrecorded row", func(r *packTreeRecord) { r.Packs[0].SkillRows[0].Row = 9 }},
		{"path escape", func(r *packTreeRecord) { r.Packs[0].SkillRows[0].Location = "../outside" }},
		{"wrong carrier", func(r *packTreeRecord) { r.Packs[0].SkillRows[0].Payload = "git" }},
		{"missing kind", func(r *packTreeRecord) { r.Packs[0].Projection.Rows[0].Kind = "" }},
		{"unknown kind", func(r *packTreeRecord) { r.Packs[0].Projection.Rows[0].Kind = "anything" }},
		{"marker state", func(r *packTreeRecord) { r.Packs[0].Projection.Rows[0].MarkerState = "present" }},
		{"forged raw directory", func(r *packTreeRecord) { r.Packs[0].Projection.Mode = "raw"; r.Packs[0].Projection.Selection.Dir = "." }},
		{"wrong governor", func(r *packTreeRecord) { r.Packs[0].Projection.Rows[0].Governor = "other" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var r packTreeRecord
			if err := json.Unmarshal(raw, &r); err != nil {
				t.Fatal(err)
			}
			tc.mutate(&r)
			if err := r.Packs[0].Projection.RefreshDigest(); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "selected", ScopedEntryMarker), []byte(r.Packs[0].Projection.Digest+"\n"), 0644); err != nil {
				t.Fatal(err)
			}
			data, err := json.Marshal(r)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, PackTreeRecordName), data, 0644); err != nil {
				t.Fatal(err)
			}
			entries, _, err := ReadPackTreeRecord(root)
			if err == nil {
				_, err = entries[0].LoadRecordedScopedPack(root)
			}
			if err == nil {
				t.Fatal("tampered record accepted")
			}
		})
	}
	for _, version := range []string{"0", "99"} {
		data := strings.Replace(string(raw), `"version": 2`, `"version": `+version, 1)
		if data == string(raw) {
			t.Fatal("version mutation did not apply")
		}
		var r packTreeRecord
		if err := json.Unmarshal([]byte(data), &r); err == nil {
			t.Fatalf("version %s accepted", version)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "selected", ScopedEntryMarker), []byte(original.Packs[0].Projection.Digest+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, PackTreeRecordName), raw, 0644); err != nil {
		t.Fatal(err)
	}
	entries, _, err := ReadPackTreeRecord(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := entries[0].LoadRecordedScopedPack(root); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "selected", ScopedCarrierDir, "skills/chosen/SKILL.md"), []byte("invented\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := entries[0].LoadRecordedScopedPack(root); err == nil {
		t.Fatal("absent marker changed to present accepted")
	}
}
