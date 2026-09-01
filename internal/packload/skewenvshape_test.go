package packload

// skewenvshape_test.go is the LOADER half of the env_shape skew rule, the twin of
// skewkind_test.go and skewvia_test.go above it: not "DecodeTolerant skips an unknown
// placeholder" (packdecl pins that) but "LoadDir under the jail's tolerance surfaces the
// skip as a SkewNote and hands back a provider that still carries its own facts".
//
// A placeholder this build does not know is the third closed VALUE set a manifest carries
// (kinds, `via`, and now env_shape), and the finding that put the tolerance here was the
// same one each time: the placeholder set was enforced on the TOLERANT path through
// validateContributionAt, so the day a fifth placeholder shipped, every entrypoint baked
// before it refused to boot over a variable it was never going to compose. SkewNotes is
// what LoadJailPacks warns from, so a rule that dropped the variable without producing a
// note would be a silent degradation — forbidden by the same contract §3.3a wrote for
// kinds.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// envShapeSkewManifest declares a provider whose env_shape names one placeholder this
// build knows and one it does not.
const envShapeSkewManifest = `{"name":"acme","contributes":[
	{"kind":"provider","name":"acme",
	 "api_key_env_name":"ACME_API_KEY",
	 "endpoints":{"anthropic":{"base_url":"https://api.acme.dev/v4"}},
	 "env_shape":{"anthropic":{
	     "ANTHROPIC_BASE_URL":"{endpoint}",
	     "ACME_CACHE":"{cache}"}}}]}`

// stageEnvShapeSkewPack writes envShapeSkewManifest as a staged pack tree, the shape
// LoadDir reads in the jail (YOLO_PACK_ROOT/<slug>/pack.json).
func stageEnvShapeSkewPack(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, packdecl.ManifestName), []byte(envShapeSkewManifest), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestJailLoadSkipsUnknownEnvShapePlaceholderInsteadOfFailingBoot: under TolerateSkew (the
// jail's mode) a staged pack naming an unrecognised placeholder LOADS — the variable is
// dropped and reported by name, never returned as a problem, because a problem fails the
// boot (A12).
func TestJailLoadSkipsUnknownEnvShapePlaceholderInsteadOfFailingBoot(t *testing.T) {
	withSkewTolerance(t, func() {
		p, problems := LoadDir(stageEnvShapeSkewPack(t), "acme", true)
		if len(problems) != 0 {
			t.Fatalf("an unknown placeholder under skew must not be a load problem — LoadJailPacks "+
				"fails the boot on any problem (A12): %v", problems)
		}
		if p == nil {
			t.Fatal("the pack must load")
		}
		if len(p.SkewNotes) != 1 {
			t.Fatalf("want exactly one skew note, got %v", p.SkewNotes)
		}
		for _, want := range []string{"pack acme", `"{cache}"`, `"ACME_CACHE"`} {
			if !strings.Contains(p.SkewNotes[0], want) {
				t.Errorf("the skew note must name %s: %q", want, p.SkewNotes[0])
			}
		}
		// Dropped means the VARIABLE, not the provider: the facts this build renders are
		// exactly what the composed table should still carry.
		provs := p.Decl.Providers()
		if len(provs) != 1 || provs[0].Name != "acme" || provs[0].APIKeyEnvName != "ACME_API_KEY" {
			t.Fatalf("the provider must survive the skip: %+v", provs)
		}
		if got := provs[0].EnvShape["anthropic"]; len(got) != 1 || got["ANTHROPIC_BASE_URL"] != "{endpoint}" {
			t.Errorf("want the unknown variable gone and the known one kept, got %+v", got)
		}
	})
}

// TestStrictLoadStillRefusesUnknownEnvShapePlaceholder: the authoring path (pack lint /
// install / every host-side read) keeps its strict semantics — the same manifest is
// refused loudly, naming the value. Tolerance belongs only at the version boundary.
func TestStrictLoadStillRefusesUnknownEnvShapePlaceholder(t *testing.T) {
	if tolerateUnknownFields {
		t.Fatal("test precondition: the strict default must be in effect")
	}
	_, problems := LoadDir(stageEnvShapeSkewPack(t), "acme", true)
	if len(problems) == 0 {
		t.Fatal("the strict authoring path must refuse an unknown env_shape placeholder")
	}
	if joined := strings.Join(problems, "\n"); !strings.Contains(joined, `"{cache}"`) {
		t.Errorf("the refusal must name the placeholder value: %v", problems)
	}
}
