package packload

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

const managedBundleManifest = `name="fixture"
[[mcp_sign_in_bundle]]
bundle="bundle"
format="claude-plugin"
origin="https://example.invalid/repo"
revision="0123456789abcdef0123456789abcdef01234567"
license="bundle/LICENSE"
`

func TestSelectedSignInBundleValidationUsesConfinedOriginalView(t *testing.T) {
	original := fstest.MapFS{
		"fixture/pack.toml":                         {Data: []byte(managedBundleManifest)},
		"fixture/bundle/LICENSE":                    {Data: []byte("MIT License")},
		"fixture/bundle/.claude-plugin/plugin.json": {Data: []byte(`{"name":"original"}`)},
		"fixture/bundle/.mcp.json":                  {Data: []byte(`{"server":{"type":"http","url":"https://mcp.fixture.invalid/mcp"}}`)},
	}
	for _, tc := range []struct {
		name, exclude string
		bad           bool
	}{{name: "complete"}, {name: "filtered license", exclude: "bundle/LICENSE", bad: true}, {name: "filtered manifest", exclude: "bundle/.claude-plugin/plugin.json", bad: true}} {
		t.Run(tc.name, func(t *testing.T) {
			var exclude []string
			if tc.exclude != "" {
				exclude = []string{tc.exclude}
			}
			p, ps, err := ReadSelectedPackFS(original, "fixture", "/unmaterialized/fixture", "fixture", nil, exclude)
			if err != nil {
				t.Fatal(err)
			}
			if (len(ps) > 0) != tc.bad {
				t.Fatalf("selected validation problems=%v", ps)
			}
			if !tc.bad {
				got, err := ReadableSignInBundles(p)
				if err != nil || len(got) != 1 || len(got[0].Inventory.Servers) != 1 {
					t.Fatalf("selected original inventory: %+v %v", got, err)
				}
				p.Decl.MCPSignInFormats = []string{"claude-plugin"}
				if _, err := ReadableSignInBundles(p); err == nil {
					t.Fatal("modified declaration retained original binding")
				}
			}
		})
	}
	dir := t.TempDir()
	for name, file := range original {
		rel := strings.TrimPrefix(name, "fixture/")
		full := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, file.Data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	p, ps := LoadDirForUse(dir, "fixture")
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	got, err := ReadableSignInBundles(p)
	if err != nil || len(got) != 1 || got[0].Inventory.ManifestName != "original" {
		t.Fatalf("disk original: %+v %v", got, err)
	}
	license := filepath.Join(dir, "bundle/LICENSE")
	if err := os.Rename(license, license+".real"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("LICENSE.real", license); err != nil {
		t.Fatal(err)
	}
	if _, ps := LoadDirForUse(dir, "fixture"); len(ps) == 0 {
		t.Fatal("linked retained license accepted")
	}
	original["fixture/bundle/LICENSE"].Mode = fs.ModeSymlink
	if _, ps, err := ReadSelectedPackFS(original, "fixture", "/unmaterialized/fixture", "fixture", nil, nil); err == nil && len(ps) == 0 {
		t.Fatal("embedded linked license accepted")
	}
}
