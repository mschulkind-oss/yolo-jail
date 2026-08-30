package packload

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/packs"
)

// skillsPack writes a pack root with a manifest and returns a loaded Pack.
func skillsPack(t *testing.T, manifest string, skillDirs ...string) *Pack {
	t.Helper()
	root := t.TempDir()
	if manifest != "" {
		if err := os.WriteFile(filepath.Join(root, "pack.json"), []byte(manifest), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, d := range skillDirs {
		sk := filepath.Join(root, filepath.FromSlash(d), "example")
		if err := os.MkdirAll(sk, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(sk, "SKILL.md"),
			[]byte("---\nname: example\ndescription: d\n---\nbody\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	p, probs := LoadDir(root, "sf", true)
	if len(probs) > 0 {
		t.Fatalf("LoadDir problems: %v", probs)
	}
	return p
}

// THE BUG: `from` on a `skills` contribution was accepted and silently ignored — every
// reader hardcoded <packRoot>/skills. A pack declaring a non-default source got skills/
// read instead, with no warning anywhere.
func TestSkillsSourceHonorsFrom(t *testing.T) {
	p := skillsPack(t,
		`{"contributes":[{"kind":"skills","from":"my-skills","into":".claude/skills"}]}`,
		"my-skills")

	dirs, problems := p.SkillsSourceDirs()
	if len(problems) > 0 {
		t.Fatalf("unexpected problems: %v", problems)
	}
	want := filepath.Join(p.Root, "my-skills")
	if len(dirs) != 1 || dirs[0] != want {
		t.Fatalf("SkillsSourceDirs() = %v, want [%s] — `from` must be honored, not ignored "+
			"in favor of the conventional skills/ dir", dirs, want)
	}
}

// The DEFAULT must not break: every pack yolo ships declares `from: "skills"`, and a
// zero-ceremony pack declares nothing at all. Both must resolve to skills/, or this fix
// breaks every shipped pack (the render fingerprint is the other half of that gate).
func TestSkillsSourceDefaultsToConvention(t *testing.T) {
	for _, tc := range []struct{ name, manifest string }{
		{"no manifest at all (zero-ceremony)", ""},
		{"manifest with no skills contribution", `{"name":"sf"}`},
		{"skills contribution with from: skills", `{"contributes":[{"kind":"skills","from":"skills","into":".x/skills"}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := skillsPack(t, tc.manifest, "skills")
			dirs, problems := p.SkillsSourceDirs()
			if len(problems) > 0 {
				t.Fatalf("unexpected problems: %v", problems)
			}
			want := filepath.Join(p.Root, "skills")
			if len(dirs) != 1 || dirs[0] != want {
				t.Fatalf("SkillsSourceDirs() = %v, want [%s]", dirs, want)
			}
		})
	}
}

// An EMPTY `from` resolves to the convention too. `from` stays REQUIRED by
// packdecl.Validate (that is the documented schema and this fix does not widen it), so an
// empty one is only reachable from a manifest whose Decode problems a caller discarded —
// which `yolo host apply` does, via packForCheckDeps. Defaulting rather than resolving to the
// pack ROOT is what keeps that path from copying the whole pack tree in as skills.
func TestSkillsSourceEmptyFromResolvesToConvention(t *testing.T) {
	p := skillsPack(t, "", "skills")
	dir, prob := p.SkillsSourceDir(packdecl.Contribution{
		Kind: packdecl.KindSkills, Into: ".x/skills",
	})
	if prob != "" {
		t.Fatalf("unexpected problem: %s", prob)
	}
	if want := filepath.Join(p.Root, "skills"); dir != want {
		t.Fatalf("SkillsSourceDir() = %q, want %q", dir, want)
	}
}

// A NON-CONVENTIONAL source that is not there delivers nothing, so it is reported. A
// declaration yolo accepts and ignores is the defect being fixed; a declaration yolo accepts
// and silently no-ops would just move it.
func TestSkillsSourceReportsMissingDeclaredDir(t *testing.T) {
	p := skillsPack(t,
		`{"contributes":[{"kind":"skills","from":"my-skills","into":".x/skills"}]}`,
		"skills") // the CONVENTIONAL dir exists; the declared one does not

	dirs, problems := p.SkillsSourceDirs()
	if len(dirs) != 0 {
		t.Errorf("SkillsSourceDirs() = %v, want none — a missing declared source must NOT "+
			"silently fall back to skills/, which is the bug being fixed", dirs)
	}
	if len(problems) != 1 || !strings.Contains(problems[0], "my-skills") {
		t.Fatalf("problems = %v, want one naming my-skills", problems)
	}
}

// The CONVENTIONAL source being absent is normal, not a problem: all six shipped packs
// declare `from: "skills"` purely to name the destination other packs merge into and carry
// no skills at all. Warning there would fire on every launch of a stock config.
func TestSkillsSourceSilentOnAbsentConvention(t *testing.T) {
	p := skillsPack(t, `{"contributes":[{"kind":"skills","from":"skills","into":".x/skills"}]}`)
	dirs, problems := p.SkillsSourceDirs()
	if len(dirs) != 0 {
		t.Errorf("SkillsSourceDirs() = %v, want none", dirs)
	}
	if len(problems) != 0 {
		t.Errorf("problems = %v, want none — an absent CONVENTIONAL skills dir is the "+
			"normal case for a pack whose contribution only names a destination, and "+
			"warning there would fire on every launch of a stock config", problems)
	}
}

// EVERY SHIPPED PACK stays silent. The regression this guards is the one the first cut
// caused: keying the missing-source warning on `from != ""` instead of on the CONVENTION made
// all six shipped packs warn on every apply, because each declares `from: "skills"` and ships
// no skills of its own.
// Materialized from packs.FS directly rather than via Embedded(), which needs the packreg
// side-effect import this test binary cannot have (that import is the cycle packreg exists to
// break — see embeddrift_test.go / configexclusive_test.go, which read packs.FS for the same
// reason). Going through Embedded() here made the test SKIP silently, which a mutation run
// caught: the noise regression it is meant to guard passed it.
func TestShippedPacksProduceNoSkillsSourceProblems(t *testing.T) {
	shipped, probs := MaterializeEmbedded(packs.FS, t.TempDir())
	if len(probs) > 0 {
		t.Fatalf("materializing the shipped packs: %v", probs)
	}
	if len(shipped) == 0 {
		t.Fatal("no shipped packs materialized — this test would cover nothing")
	}
	for _, p := range shipped {
		if _, problems := p.SkillsSourceDirs(); len(problems) > 0 {
			t.Errorf("shipped pack %s reports skills-source problems %v — a warning on a "+
				"stock config trains users to ignore warnings", p.Name, problems)
		}
	}
}

// A file where a directory was declared is reported by name rather than read.
func TestSkillsSourceReportsFileNotDir(t *testing.T) {
	p := skillsPack(t, `{"contributes":[{"kind":"skills","from":"my-skills","into":".x/skills"}]}`)
	if err := os.WriteFile(filepath.Join(p.Root, "my-skills"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	dirs, problems := p.SkillsSourceDirs()
	if len(dirs) != 0 {
		t.Errorf("dirs = %v, want none", dirs)
	}
	if len(problems) != 1 || !strings.Contains(problems[0], "file, not a") {
		t.Fatalf("problems = %v, want one saying it is a file not a directory", problems)
	}
}

// CONTAINMENT: `from` must not reach outside the pack tree. packdecl.Validate already
// refuses ".." at the authoring boundary, but a caller may hold a pack whose Decode problems
// it discarded (yolo host apply reads a local pack that way), so the resolver enforces it too.
func TestSkillsSourceRefusesEscapingFrom(t *testing.T) {
	p := skillsPack(t, "", "skills")
	// Constructed directly: a manifest with ".." would be rejected by Validate, and the point
	// is the resolver's own guard for a caller that ignored those problems.
	dir, prob := p.SkillsSourceDir(packdecl.Contribution{
		Kind: packdecl.KindSkills, From: "../../elsewhere", Into: ".x/skills",
	})
	if dir != "" {
		t.Errorf("SkillsSourceDir returned %q for an escaping `from` — must refuse", dir)
	}
	if !strings.Contains(prob, "escapes the pack tree") {
		t.Errorf("problem = %q, want one naming the escape", prob)
	}
}

// Two contributions naming ONE source (the same skills delivered to two agents' dirs) is one
// tree to read, so the source list is deduped — a repeat would copy the same content twice.
func TestSkillsSourceDedupesRepeatedSource(t *testing.T) {
	p := skillsPack(t, `{"contributes":[
		{"kind":"skills","from":"my-skills","into":".claude/skills"},
		{"kind":"skills","from":"my-skills","into":".codex/skills"}]}`, "my-skills")
	dirs, problems := p.SkillsSourceDirs()
	if len(problems) > 0 {
		t.Fatalf("problems: %v", problems)
	}
	if len(dirs) != 1 {
		t.Fatalf("dirs = %v, want one (deduped)", dirs)
	}
}

// Two contributions naming DIFFERENT sources both count: a pack may split its corpus.
func TestSkillsSourceKeepsDistinctSources(t *testing.T) {
	p := skillsPack(t, `{"contributes":[
		{"kind":"skills","from":"a-skills","into":".claude/skills"},
		{"kind":"skills","from":"b-skills","into":".codex/skills"}]}`, "a-skills", "b-skills")
	dirs, problems := p.SkillsSourceDirs()
	if len(problems) > 0 {
		t.Fatalf("problems: %v", problems)
	}
	if len(dirs) != 2 {
		t.Fatalf("dirs = %v, want both sources", dirs)
	}
}

// A WRAPPED PLUGIN is carried BY a skills contribution (it has no kind of its own), so
// plugin discovery has to follow `from` too — or the footprint reports no plugin claim while
// delivery finds one.
func TestPluginsFollowSkillsFrom(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "pack.json"),
		[]byte(`{"skills_tier":"namespaced","contributes":[{"kind":"skills","from":"my-skills","into":".claude/skills"}]}`),
		0o644); err != nil {
		t.Fatal(err)
	}
	md := filepath.Join(root, "my-skills", "acme-tools", ".claude-plugin")
	if err := os.MkdirAll(md, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(md, "plugin.json"),
		[]byte(`{"name":"acme-tools","skills":["./"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	p, probs := LoadDir(root, "wrapper", true)
	if len(probs) > 0 {
		t.Fatalf("LoadDir: %v", probs)
	}
	plugins := p.Plugins()
	if len(plugins) != 1 || plugins[0].Name() != "acme-tools" {
		t.Fatalf("Plugins() found %d plugin(s), want the one under my-skills/ — discovery "+
			"scanning a hardcoded skills/ would miss it", len(plugins))
	}
}

// The footprint must NAME the resolved source. A claim line reading only "merged" is
// identical for a working pack and one whose skills yolo never reads — one of the reports
// that let the ignored `from` stay hidden.
func TestFootprintNamesSkillsSource(t *testing.T) {
	m := &packdecl.Manifest{Contributes: []packdecl.Contribution{
		{Kind: packdecl.KindSkills, From: "my-skills", Into: ".claude/skills"},
	}}
	fp := FootprintOf(pk("sf", true, m))
	if len(fp.Claims) != 1 {
		t.Fatalf("claims = %+v, want one", fp.Claims)
	}
	if !strings.Contains(fp.Claims[0].Detail, "my-skills") {
		t.Errorf("footprint detail = %q, want it to name the resolved source my-skills",
			fp.Claims[0].Detail)
	}
}
