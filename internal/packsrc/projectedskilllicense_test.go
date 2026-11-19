package packsrc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadSkillPayloadTreeProjectedDirectoryLicenses(t *testing.T) {
	for _, tc := range []struct {
		name        string
		files       map[string]string
		added, want string
	}{
		{name: "none", files: map[string]string{}},
		{name: "root", files: map[string]string{"LICENSE": "root\n"}, added: "LICENSE"},
		{name: "own", files: map[string]string{"skills/chosen/LICENSE": "own\n", "LICENSE": "root\n"}},
		{name: "own conflict", files: map[string]string{"skills/chosen/LICENSE": "one\n", "skills/chosen/LICENSE.txt": "two\n"}, want: "conflicting selected licenses"},
		{name: "root conflict", files: map[string]string{"LICENSE": "one\n", "COPYING": "two\n"}, want: "conflicting root licenses"},
		{name: "oversized own", files: map[string]string{"skills/chosen/LICENSE": strings.Repeat("x", skillMarkerLimit+1)}, want: "oversized"},
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
			if tc.want != "" {
				if err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("want %q: %v", tc.want, err)
				}
				return
			}
			if err != nil || plan.Rows[0].LicenseName != tc.added {
				t.Fatalf("license: %+v %v", plan, err)
			}
			if tc.name == "none" && plan.Rows[0].LicenseNote != "no license file found" {
				t.Fatalf("absence not recorded: %+v", plan.Rows[0])
			}
		})
	}
}
