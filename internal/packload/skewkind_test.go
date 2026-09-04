package packload

// skewkind_test.go pins the §3.3a (loophole-packaging) decision at the JAIL-LOAD entry
// point: a contribution KIND this build does not know is version skew, not corruption.
//
// The A12 story, so a future reader does not re-learn it the hard way: the in-jail
// entrypoint calls TolerateSkew() and then fails the boot on ANY manifest problem
// (LoadJailPacks, A12). The host CLI and the baked entrypoint come from different
// places — the CLI is freshly built, the entrypoint frozen at the last host
// `just load` — so the moment a 15th kind exists, a pack declaring it used to brick
// every jail running the pre-`just load` image:
//
//	yolo-entrypoint: refusing to start the jail: … load_packs: pack acme:
//	  contributes[1]: unknown kind "totally-unknown-kind" (expected one of …)
//
// This is the `tier` / `skills_tier` incident a third time (see
// packdecl.DecodeTolerant and packdecl's retiredFieldProblems for the first two).
// The split: an author must hear (the strict path still refuses); a jail must boot
// (the tolerant path skips the contribution and reports it by name).

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// skewManifest carries one unknown-kind contribution BETWEEN two valid siblings, so the
// tests can pin that a skip disturbs neither neighbor.
const skewManifest = `{"name":"acme","contributes":[
	{"kind":"skills","from":"skills","into":".acme/skills"},
	{"kind":"totally-unknown-kind","from":"x"},
	{"kind":"env","vars":{"ACME":"1"}}]}`

// stageSkewPack writes skewManifest as a staged pack tree, the shape LoadDir reads in
// the jail (YOLO_PACK_ROOT/<slug>/pack.json).
func stageSkewPack(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, packdecl.ManifestName), []byte(skewManifest), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// withSkewTolerance runs the body under TolerateSkew semantics and restores the strict
// default after, so the package-level switch cannot leak into other tests.
func withSkewTolerance(t *testing.T, body func()) {
	t.Helper()
	prev := tolerateUnknownFields
	TolerateSkew()
	defer func() { tolerateUnknownFields = prev }()
	body()
}

// TestJailLoadSkipsUnknownKindInsteadOfFailingBoot: under TolerateSkew (the jail's
// mode), a staged pack declaring a kind this build does not know LOADS — the unknown
// contribution is skipped and reported by name (kind + pack), never returned as a
// problem, because a problem fails the boot (A12).
func TestJailLoadSkipsUnknownKindInsteadOfFailingBoot(t *testing.T) {
	withSkewTolerance(t, func() {
		p, problems := LoadDir(stageSkewPack(t), "acme")
		if len(problems) != 0 {
			t.Fatalf("an unknown kind under skew must not be a load problem — LoadJailPacks "+
				"fails the boot on any problem (A12): %v", problems)
		}
		if p == nil {
			t.Fatal("the pack must load")
		}
		// Reported by name: kind and pack, so the degradation is visible at boot
		// rather than silent.
		if len(p.SkewNotes) != 1 {
			t.Fatalf("want exactly one skew note, got %v", p.SkewNotes)
		}
		for _, want := range []string{"pack acme", `"totally-unknown-kind"`} {
			if !strings.Contains(p.SkewNotes[0], want) {
				t.Errorf("the skew note must name %s: %q", want, p.SkewNotes[0])
			}
		}
		// Skipped means DROPPED from the loaded manifest.
		for _, c := range p.Decl.Contributions() {
			if c.Kind == "totally-unknown-kind" {
				t.Errorf("the unknown contribution must be dropped, still present: %+v", c)
			}
		}
	})
}

// TestJailLoadKeepsValidSiblingsOfASkippedKind: a skipped contribution must not
// disturb the valid contributions around it — they all still load, through the same
// projections the boot path reads.
func TestJailLoadKeepsValidSiblingsOfASkippedKind(t *testing.T) {
	withSkewTolerance(t, func() {
		p, problems := LoadDir(stageSkewPack(t), "acme")
		if len(problems) != 0 || p == nil {
			t.Fatalf("load failed: %v", problems)
		}
		if cs := p.Decl.Contributions(); len(cs) != 2 {
			t.Fatalf("want the 2 valid siblings, got %+v", cs)
		}
		if srcs := p.Decl.SkillsSources(); len(srcs) != 1 || srcs[0] != "skills" {
			t.Errorf("the skills sibling (before the skip) must survive: %v", srcs)
		}
		if env := p.Decl.EnvContributions(); env["ACME"] != "1" {
			t.Errorf("the env sibling (after the skip) must survive: %v", env)
		}
	})
}

// TestStrictLoadStillRefusesUnknownKind: the authoring path (pack lint / install /
// every host-side read) keeps its strict semantics — the same manifest is refused
// loudly, naming the kind. A typo'd kind that silently rendered nothing would be the
// worst outcome for a pack author; tolerance belongs only at the version boundary.
func TestStrictLoadStillRefusesUnknownKind(t *testing.T) {
	if tolerateUnknownFields {
		t.Fatal("test precondition: the strict default must be in effect")
	}
	_, problems := LoadDir(stageSkewPack(t), "acme")
	if len(problems) == 0 {
		t.Fatal("the strict authoring path must refuse an unknown kind")
	}
	if joined := strings.Join(problems, "\n"); !strings.Contains(joined, `unknown kind "totally-unknown-kind"`) {
		t.Errorf("the refusal must name the kind: %v", problems)
	}
}
