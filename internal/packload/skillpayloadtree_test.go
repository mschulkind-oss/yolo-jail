package packload

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
)

func TestHydrateSourcedSkillsRejectsIncompleteDuplicateAndUnknownRecordedRows(t *testing.T) {
	source := "git+https://example.invalid/repo//skills/chosen?ref=main"
	addr, _ := packsrc.Parse(source)
	commit := strings.Repeat("a", 40)
	id := packsrc.SkillPayloadIdentity(addr.Repo, commit)
	tree := t.TempDir()
	payload := filepath.Join(tree, SkillPayloadTreeDir, id)
	if err := os.MkdirAll(filepath.Join(payload, "skills/chosen"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(payload, "skills/chosen/SKILL.md"), []byte("body"), 0444); err != nil {
		t.Fatal(err)
	}
	info := SkillPayloadTreeRecord{ID: id, Repository: addr.Repo, Commit: commit, Location: SkillPayloadTreeDir + "/" + id}
	row := SkillTreeRow{Row: 0, Source: source, Alias: "alias", Payload: id}
	for _, tc := range []struct {
		name           string
		rows           []SkillTreeRow
		withheld, want string
	}{
		{name: "complete", rows: []SkillTreeRow{row}},
		{name: "missing", want: "missing recorded sourced row"},
		{name: "duplicate", rows: []SkillTreeRow{row, row}, want: "duplicate recorded sourced row"},
		{name: "unknown-row", rows: []SkillTreeRow{{Row: 1, Source: source, Alias: "alias", Payload: id}}, want: "invalid recorded skills row"},
		{name: "unknown-payload", rows: []SkillTreeRow{{Row: 0, Source: source, Alias: "alias", Payload: "unknown"}}, want: "unknown recorded payload"},
		{name: "withheld", withheld: "legacy only"},
		{name: "withheld-and-recorded", withheld: "legacy only", rows: []SkillTreeRow{row}, want: "withheld sourced rows"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &Pack{Name: "owner", Root: t.TempDir(), Decl: &packdecl.Manifest{Contributes: []packdecl.Contribution{{Kind: packdecl.KindSkills, Source: source, As: "alias"}}}}
			entry := PackTreeEntry{Name: "owner", SkillRows: tc.rows, SourcedSkillsWithheld: tc.withheld, skillPayloads: map[string]SkillPayloadTreeRecord{id: info}}
			err := entry.HydrateSourcedSkills(tree, p)
			if tc.want == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want %q, got %v", tc.want, err)
			}
		})
	}
	// An immutable recorded location never grants permission to walk a planted
	// payload ancestor into an outside directory.
	outside := t.TempDir()
	if err := os.Rename(filepath.Join(tree, SkillPayloadTreeDir), filepath.Join(outside, "payloads")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "payloads"), filepath.Join(tree, SkillPayloadTreeDir)); err != nil {
		t.Fatal(err)
	}
	p := &Pack{Name: "owner", Decl: &packdecl.Manifest{Contributes: []packdecl.Contribution{{Kind: packdecl.KindSkills, Source: source, As: "alias"}}}}
	entry := PackTreeEntry{Name: "owner", SkillRows: []SkillTreeRow{row}, skillPayloads: map[string]SkillPayloadTreeRecord{id: info}}
	if err := entry.HydrateSourcedSkills(tree, p); err == nil {
		t.Fatal("recorded reader followed linked payload ancestor")
	}
}
