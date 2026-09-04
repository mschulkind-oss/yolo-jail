package packload

// skewvia_test.go is the LOADER half of program-delivery.md §6.2 / R6, the twin of
// skewkind_test.go one level down: not "DecodeTolerant skips an unknown via" (packdecl
// pins that) but "LoadDir under the jail's tolerance surfaces the skip as a SkewNote and
// hands back a pack with no such install".
//
// SkewNotes is what LoadJailPacks warns from, so a rule that dropped the contribution
// without producing a note would be a silent degradation — forbidden by the same contract
// §3.3a wrote for kinds.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// viaSkewManifest declares a program via a mechanism no build knows, between two valid
// siblings.
const viaSkewManifest = `{"name":"acme","contributes":[
	{"kind":"skills","from":"skills","into":".acme/skills"},
	{"kind":"program","bin":"ruff","via":"uv","package":"ruff"},
	{"kind":"env","vars":{"ACME":"1"}}]}`

// stageViaSkewPack writes viaSkewManifest as a staged pack tree, the shape LoadDir reads
// in the jail (YOLO_PACK_ROOT/<slug>/pack.json).
func stageViaSkewPack(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, packdecl.ManifestName), []byte(viaSkewManifest), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestJailLoadSkipsUnknownViaInsteadOfFailingBoot: under TolerateSkew (the jail's mode) a
// staged pack declaring an unrecognised `via` LOADS — the program is skipped and reported
// by name, never returned as a problem, because a problem fails the boot (A12).
func TestJailLoadSkipsUnknownViaInsteadOfFailingBoot(t *testing.T) {
	withSkewTolerance(t, func() {
		p, problems := LoadDir(stageViaSkewPack(t), "acme")
		if len(problems) != 0 {
			t.Fatalf("an unknown via under skew must not be a load problem — LoadJailPacks "+
				"fails the boot on any problem (A12): %v", problems)
		}
		if p == nil {
			t.Fatal("the pack must load")
		}
		if len(p.SkewNotes) != 1 {
			t.Fatalf("want exactly one skew note, got %v", p.SkewNotes)
		}
		for _, want := range []string{"pack acme", `"uv"`, `"ruff"`} {
			if !strings.Contains(p.SkewNotes[0], want) {
				t.Errorf("the skew note must name %s: %q", want, p.SkewNotes[0])
			}
		}
		// Skipped means DROPPED: nothing downstream can try to install it.
		if installs := p.Decl.InstallContributions(); len(installs) != 0 {
			t.Errorf("the unknown-via program must not survive as an install: %+v", installs)
		}
		// And the valid siblings are undisturbed, through the projections the boot reads.
		if srcs := p.Decl.SkillsSources(); len(srcs) != 1 || srcs[0] != "skills" {
			t.Errorf("the skills sibling (before the skip) must survive: %v", srcs)
		}
		if env := p.Decl.EnvContributions(); env["ACME"] != "1" {
			t.Errorf("the env sibling (after the skip) must survive: %v", env)
		}
	})
}

// TestStrictLoadStillRefusesUnknownVia: the authoring path (pack lint / install / every
// host-side read) keeps its strict semantics — the same manifest is refused loudly, naming
// the value. Tolerance belongs only at the version boundary.
func TestStrictLoadStillRefusesUnknownVia(t *testing.T) {
	if tolerateUnknownFields {
		t.Fatal("test precondition: the strict default must be in effect")
	}
	_, problems := LoadDir(stageViaSkewPack(t), "acme")
	if len(problems) == 0 {
		t.Fatal("the strict authoring path must refuse an unknown via")
	}
	if joined := strings.Join(problems, "\n"); !strings.Contains(joined, `unknown via "uv"`) {
		t.Errorf("the refusal must name the via value: %v", problems)
	}
}
