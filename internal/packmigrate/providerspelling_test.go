package packmigrate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

const legacySpellingPack = `description = "fixture"
name = "fixture"

# The provider, in the legacy spelling.
[[provider]]
name = "bedrock"
platform = "aws-bedrock"

# Its bridged profile.
[[profile]]
name = "bedrock-bridge"
provider = "bedrock"
via = "wire-bridge"

[[profile]]
name = "br"
provider = "bedrock"
`

func TestRewriteProviderSpellingsWritesTheKeyedFormAndFoldsProfiles(t *testing.T) {
	rw, err := RewriteProviderSpellings([]byte(legacySpellingPack))
	if err != nil || rw == nil {
		t.Fatalf("RewriteProviderSpellings: %v %v", rw, err)
	}
	want := `description = "fixture"
name = "fixture"

# The provider, in the legacy spelling.
[providers.bedrock]
alias = "br"
platform = "aws-bedrock"

# Its bridged profile.
[providers.bedrock.variants.bridge]
name = "bedrock-bridge"
via = "wire-bridge"
`
	if string(rw.Manifest) != want {
		t.Fatalf("rewritten:\n%s\nwant:\n%s", rw.Manifest, want)
	}
	if len(rw.Folds) != 2 || len(rw.Kept) != 0 {
		t.Fatalf("folds %+v kept %+v", rw.Folds, rw.Kept)
	}
	got, problems := packdecl.DecodeTOML(rw.Manifest)
	if len(problems) != 0 || len(got.LegacySpellings) != 0 {
		t.Fatalf("read back: %v, legacy %v", problems, got.LegacySpellings)
	}
}

func TestRewriteProviderSpellingsLeavesAKeyedManifestAlone(t *testing.T) {
	rw, err := RewriteProviderSpellings([]byte("name = \"p\"\n\n[providers.zai]\nplatform = \"zai\"\n"))
	if err != nil || rw != nil {
		t.Fatalf("got %v %v, want nothing to rewrite", rw, err)
	}
}

func TestRewriteProviderSpellingsMergesAProviderDeclaredBothWays(t *testing.T) {
	rw, err := RewriteProviderSpellings([]byte(`name = "p"

[providers.zai]
platform = "zai"

[[provider]]
name = "zai"
pinned_endpoint = true
`))
	if err != nil || rw == nil || !rw.Merged {
		t.Fatalf("RewriteProviderSpellings: %+v %v", rw, err)
	}
	got, problems := packdecl.DecodeTOML(rw.Manifest)
	if len(problems) != 0 || len(got.Providers()) != 1 || !got.Providers()[0].PinnedEndpoint {
		t.Fatalf("read back %v:\n%s", problems, rw.Manifest)
	}
}

func TestRewriteProviderSpellingsRefusesABothWaysKeyClash(t *testing.T) {
	_, err := RewriteProviderSpellings([]byte(`name = "p"

[providers.zai]
platform = "zai"

[[provider]]
name = "zai"
platform = "other"
`))
	if err == nil || !strings.Contains(err.Error(), "Delete the key from one of them") {
		t.Fatalf("err = %v", err)
	}
}

// The verb on a pack.toml: Plan picks the in-place rewrite and Apply writes it, reporting each
// fold.
func TestPlanAndApplyRewriteAPackTOMLsLegacySpellingsInPlace(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, packdecl.ManifestTOMLName), []byte(legacySpellingPack), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := Plan(dir, Options{})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if m.Legacy != packdecl.ManifestTOMLName || len(m.ProfileFolds) != 2 {
		t.Fatalf("plan = %+v", m)
	}
	if err := Apply(m); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, packdecl.ManifestTOMLName))
	if len(packdecl.LegacyProviderSpellings(data)) != 0 {
		t.Fatalf("still legacy:\n%s", data)
	}
	if _, err := Plan(dir, Options{}); err == nil || !strings.Contains(err.Error(), "nothing to migrate") {
		t.Fatalf("a second Plan = %v, want nothing to migrate", err)
	}
}

// A legacy pack.json migrates straight to the keyed form, its profile folded.
func TestPlanFoldsALegacyPackJSONsProfiles(t *testing.T) {
	dir := writePack(t, `{"name": "fixture", "contributes": [
  {"kind": "provider", "name": "anthropic", "platform": "anthropic", "pinned_endpoint": true},
  {"kind": "profile", "name": "claude", "provider": "anthropic"}
]}`, nil)
	m, err := Plan(dir, Options{})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	text := string(m.Manifest)
	if !strings.Contains(text, "[providers.anthropic]\nalias = \"claude\"") || strings.Contains(text, "[[profile]]") || strings.Contains(text, "[[provider]]") {
		t.Fatalf("manifest:\n%s", text)
	}
}

// Every shipped pack is written in the keyed form, with nothing left to migrate.
func TestNoShippedPackWritesALegacyProviderSpelling(t *testing.T) {
	manifests, err := filepath.Glob("../../packs/*/" + packdecl.ManifestTOMLName)
	if err != nil || len(manifests) == 0 {
		t.Fatalf("glob: %v %v", manifests, err)
	}
	for _, path := range manifests {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if legacy := packdecl.LegacyProviderSpellings(data); len(legacy) != 0 {
			t.Errorf("%s writes %v; run `yolo pack migrate %s`", path, legacy, filepath.Dir(path))
		}
	}
}
