package packsrc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadSkillPayloadTreeValidatesProjectedRowKinds(t *testing.T) {
	for _, tc := range []struct{ name, kind, state, mode, governor, child, marker, plugin, want string }{
		{name: "markerless typed", kind: "directory", state: "absent", mode: "typed", governor: "skills", child: "chosen"},
		{name: "marked typed", kind: "directory", state: "present", mode: "typed", governor: "skills", child: "chosen", marker: "chosen\n"},
		{name: "raw requires marker", kind: "marker", state: "present", mode: "raw", want: "direct regular"},
		{name: "missing kind", state: "absent", mode: "typed", governor: "skills", child: "chosen", want: "unknown/missing"},
		{name: "unknown kind", kind: "folder", state: "absent", mode: "typed", governor: "skills", child: "chosen", want: "unknown/missing"},
		{name: "raw directory forgery", kind: "directory", state: "absent", mode: "raw", governor: "skills", child: "chosen", want: "typed governor"},
		{name: "wrong governor", kind: "directory", state: "absent", mode: "typed", governor: "other", child: "chosen", want: "typed governor"},
		{name: "wrong child", kind: "directory", state: "absent", mode: "typed", governor: "skills", child: "other", want: "typed governor"},
		{name: "present mismatch", kind: "directory", state: "present", mode: "typed", governor: "skills", child: "chosen", want: "direct regular"},
		{name: "absent mismatch", kind: "directory", state: "absent", mode: "typed", governor: "skills", child: "chosen", marker: "changed\n", want: "absent marker state"},
		{name: "plugin wrapper", kind: "directory", state: "absent", mode: "typed", governor: "skills", child: "chosen", plugin: `{"name":"wrapper"}`, want: "plugin wrapper"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			dir := filepath.Join(root, "skills/chosen")
			if err := os.MkdirAll(dir, 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "notes.md"), []byte("support\n"), 0644); err != nil {
				t.Fatal(err)
			}
			if tc.marker != "" {
				if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(tc.marker), 0644); err != nil {
					t.Fatal(err)
				}
			}
			if tc.plugin != "" {
				if err := os.MkdirAll(filepath.Join(dir, ".claude-plugin"), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, ".claude-plugin/plugin.json"), []byte(tc.plugin), 0644); err != nil {
					t.Fatal(err)
				}
			}
			request := SkillRequest{Label: "projected", Source: "git+https://example.invalid/owner/repo//skills/chosen?ref=main", Projection: &SkillProjectionEvidence{Mode: tc.mode, Kind: tc.kind, MarkerState: tc.state, PackRoot: ".", Governor: tc.governor, Child: tc.child}}
			plan, err := ReadSkillPayloadTree(root, []SkillRequest{request})
			if tc.want != "" {
				if err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("want %q: %+v %v", tc.want, plan, err)
				}
				return
			}
			if err != nil || len(plan.Rows) != 1 || plan.Rows[0].Kind != tc.kind || plan.Rows[0].MarkerState != tc.state {
				t.Fatalf("row kind/state lost: %+v %v", plan, err)
			}
			if tc.state == "absent" && plan.Rows[0].Marker != nil {
				t.Fatalf("absent marker invented: %+v", plan.Rows[0])
			}
		})
	}
}

func TestReadSkillPayloadTreeLiteralDirectDirectoryDoesNotUseLegacyAddressGrammar(t *testing.T) {
	root := t.TempDir()
	dir := "skills/a..b?folder"
	if err := os.MkdirAll(filepath.Join(root, dir), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, dir, "notes.md"), []byte("literal\n"), 0644); err != nil {
		t.Fatal(err)
	}
	request := SkillRequest{Label: "literal", Source: "git+https://example.invalid/owner/repo?ref=main", Projection: &SkillProjectionEvidence{Mode: "typed", Kind: "directory", MarkerState: "absent", PackRoot: ".", Governor: "skills", Child: "a..b?folder", Directory: dir}}
	if _, err := groupSkillRequests([]SkillRequest{request}); err != nil {
		t.Fatal(err)
	}
	plan, err := ReadSkillPayloadTree(root, []SkillRequest{request})
	if err != nil || plan.Rows[0].Path != dir {
		t.Fatalf("literal direct path: %+v %v", plan, err)
	}
}

func TestReadSkillPayloadTreeRawRootAndLegacyMarkerCompatibility(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "SKILL.md"), []byte("root\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "notes.md"), []byte("support\n"), 0644); err != nil {
		t.Fatal(err)
	}
	request := SkillRequest{Label: "root", Source: "git+https://example.invalid/owner/repo?ref=main", Projection: &SkillProjectionEvidence{Mode: "raw", Kind: "marker", MarkerState: "present"}}
	if _, err := groupSkillRequests([]SkillRequest{request}); err != nil {
		t.Fatal(err)
	}
	plan, err := ReadSkillPayloadTree(root, []SkillRequest{request})
	if err != nil || len(plan.Rows) != 1 || plan.Rows[0].Path != "." || string(plan.Rows[0].Marker) != "root\n" {
		t.Fatalf("raw root: %+v %v", plan, err)
	}
	request.Projection = nil
	if _, err := groupSkillRequests([]SkillRequest{request}); err == nil {
		t.Fatal("legacy manifest root requirement relaxed")
	}
}
