package packsrc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A skill's own license is recognized by the four exact conventional names only. These
// cases pin what a differently cased own license does today, so an ergonomics change is
// proposed against a measured contract (docs/plans/git-skills-aws-verification.md).
// Canonical automatic-license refusal stays the contract: nothing here may overwrite.
func TestReadSkillPayloadTreeOwnLicenseIsRecognizedByExactNameOnly(t *testing.T) {
	for _, tc := range []struct {
		name        string
		files       map[string]string
		wantErr     string
		wantNote    string
		wantAddedAs string
	}{
		{name: "exact name is recognized", files: map[string]string{"skills/chosen/LICENSE": "own\n", "LICENSE": "root\n"}, wantNote: "LICENSE already in selected directory; no license added"},
		{name: "lowercase own license is not recognized and no root license means none found", files: map[string]string{"skills/chosen/license": "own\n"}, wantNote: "no license file found"},
		{name: "mixed case own license with extension is not recognized", files: map[string]string{"skills/chosen/License.md": "own\n"}, wantNote: "no license file found"},
		{name: "lowercase own license plus root license refuses by case-insensitive collision", files: map[string]string{"skills/chosen/license": "own\n", "LICENSE": "root\n"}, wantErr: "license basename LICENSE conflicts with skill entry skills/chosen/license"},
		{name: "mixed case own extension plus root license refuses by case-insensitive collision", files: map[string]string{"skills/chosen/License.md": "own\n", "LICENSE.md": "root\n"}, wantErr: "license basename LICENSE.md conflicts with skill entry skills/chosen/License.md"},
		{name: "lowercase root license is not a root license", files: map[string]string{"license": "root\n"}, wantNote: "no license file found"},
		{name: "lowercase own license does not stop an unrelated root name being added", files: map[string]string{"skills/chosen/license.txt": "own\n", "COPYING": "root\n"}, wantNote: "added repository COPYING as private COPYING", wantAddedAs: "COPYING"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.MkdirAll(filepath.Join(root, "skills/chosen"), 0755); err != nil {
				t.Fatal(err)
			}
			for file, data := range tc.files {
				if err := os.WriteFile(filepath.Join(root, file), []byte(data), 0644); err != nil {
					t.Fatal(err)
				}
			}
			request := SkillRequest{Label: "fixture", Source: "git+https://example.invalid/owner/repo?ref=main", Projection: &SkillProjectionEvidence{Mode: "typed", Kind: "directory", MarkerState: "absent", PackRoot: ".", Governor: "skills", Child: "chosen", Directory: "skills/chosen"}}
			plan, err := ReadSkillPayloadTree(root, []SkillRequest{request})
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("want error %q, got %v", tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			row := plan.Rows[0]
			if row.LicenseNote != tc.wantNote || row.LicenseName != tc.wantAddedAs {
				t.Fatalf("license note %q name %q, want %q / %q", row.LicenseNote, row.LicenseName, tc.wantNote, tc.wantAddedAs)
			}
		})
	}
}
