package packsrc

import (
	"strings"
	"testing"
)

func fableEntries(extra ...SkillEntry) *SkillPayloadPlan {
	p := &SkillPayloadPlan{Entries: []SkillEntry{
		{Path: "skills", Mode: "040000"},
		{Path: "skills/x", Mode: "040000"},
		{Path: "skills/x/SKILL.md", Mode: "100644", Data: []byte("---\nname: x\n---\n"), Size: 16},
	}}
	p.Entries = append(p.Entries, extra...)
	return p
}

func fableLink(path, target string) SkillEntry {
	return SkillEntry{Path: path, Mode: "120000", Target: target, Data: []byte(target), Size: int64(len(target))}
}

var fableRow = []SkillRequest{{Label: "pack p skills[0]", Source: "git+https://example.invalid/r.git//skills/x?ref=main"}}

// Canonical §7.3/acceptance 9 refuse license collisions. The earlier
// reference's automatic-skip claim had no authority and was corrected.
func TestSkillPayloadAutomaticLicenseInCaseGroup(t *testing.T) {
	p := fableEntries(
		SkillEntry{Path: "LICENSE", Mode: "100644", Data: []byte("a"), Size: 1},
		SkillEntry{Path: "License", Mode: "100644", Data: []byte("b"), Size: 1},
	)
	_, err := validateSkillPayloadPlan(p, fableRow)
	if err == nil || !strings.Contains(err.Error(), "omitted by case-collision") {
		t.Fatalf("automatic license collision must refuse: %v", err)
	}
}

// A selected directory already holding its own (differently cased) license file.
func TestSkillPayloadAutomaticLicenseVersusLowercaseSkillLicense(t *testing.T) {
	p := fableEntries(
		SkillEntry{Path: "skills/x/license.txt", Mode: "100644", Data: []byte("own"), Size: 3},
		SkillEntry{Path: "LICENSE.txt", Mode: "100644", Data: []byte("root"), Size: 4},
	)
	_, err := validateSkillPayloadPlan(p, fableRow)
	if err == nil || !strings.Contains(err.Error(), "conflicts with skill entry") {
		t.Fatalf("automatic basename collision must refuse: %v", err)
	}
}

// A kept supporting link whose chain passes through an omitted link dangles physically.
func TestSkillPayloadKeptLinkThroughOmittedLink(t *testing.T) {
	p := fableEntries(
		SkillEntry{Path: "real", Mode: "100644", Data: []byte("r"), Size: 1},
		fableLink("a", "b"),
		fableLink("b", "real"),
		SkillEntry{Path: "B", Mode: "100644", Data: []byte("c"), Size: 1},
	)
	plan, err := validateSkillPayloadPlan(p, fableRow)
	if err != nil {
		t.Fatal(err)
	}
	kept := map[string]bool{}
	for _, e := range plan.Entries {
		kept[e.Path] = true
	}
	t.Logf("kept=%v omissions=%v", kept, plan.Omissions)
	if kept["a"] && !kept["b"] {
		// Re-validate what a physical copy of this effective tree looks like.
		again, err := validateSkillPayloadPlan(&SkillPayloadPlan{Entries: append([]SkillEntry(nil), plan.Entries...)}, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Fatalf("effective payload keeps link a -> b although b is omitted; a physical re-read omits it: %v (CopySkillPayload and HydrateSourcedSkills refuse any omission)", again.Omissions)
	}
}

func TestSkillPayloadParseSubdirNormalization(t *testing.T) {
	for _, src := range []string{
		"git+https://example.invalid/r.git//skills//x?ref=main",
		"git+https://example.invalid/r.git//./skills/x?ref=main",
		"git+https://example.invalid/r.git//skills/x/?ref=main",
		"git+https://example.invalid/r.git//skills/x?ref=main",
	} {
		a, err := Parse(src)
		t.Logf("%s -> path=%q repo=%q ref=%q err=%v", src, a.Path, a.Repo, a.Ref, err)
		if err == nil && !skillCleanPath(a.Path) {
			t.Errorf("%s: accepted with unclean selected path %q", src, a.Path)
		}
	}
}
