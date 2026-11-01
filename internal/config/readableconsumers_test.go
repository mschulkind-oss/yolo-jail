package config

import (
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// TestTheSharedReadersSeeTheLoadedReadableLayout: the loader hands every consumer the readable
// spelling (`machine.resources`, `install.mise`, `install.nix`) whichever format the file was
// written in, so each shared reader must find its setting there. PackagesExcludedOn is the one
// that read only the legacy top-level `packages`, which left macos-user's "already marked
// Linux-only" explanation empty after the cutover.
func TestTheSharedReadersSeeTheLoadedReadableLayout(t *testing.T) {
	for _, fx := range []struct{ name, file, body string }{
		{"legacy jsonc", "yolo-jail.jsonc", `{
  "packages": ["jq", {"name": "strace", "platforms": ["linux"]}],
  "mise_tools": {"neovim": "nightly"},
  "resources": {"cpus": 2}
}`},
		{"toml", "yolo-jail.toml", `[install]
nix = ["jq", {name = "strace", platforms = ["linux"]}]

[install.mise]
neovim = "nightly"

[machine.resources]
cpus = 2
`},
	} {
		t.Run(fx.name, func(t *testing.T) {
			_, ws := convertLaterEnv(t)
			writeConvertLaterFile(t, filepath.Join(ws, fx.file), fx.body)
			cfg, err := LoadConfig(ws, true, func(string) {})
			if err != nil {
				t.Fatal(err)
			}
			for _, legacy := range []string{"packages", "mise_tools", "resources"} {
				if _, present := cfg.Get(legacy); present {
					t.Fatalf("the loaded config still carries top-level %s, so this test cannot "+
						"tell the readers apart", legacy)
				}
			}
			if got := PackagesExcludedOn(cfg, PlatformDarwin); len(got) != 1 || got[0] != "strace" {
				t.Errorf("PackagesExcludedOn(darwin) = %v, want [strace]", got)
			}
			if got := EffectivePackages(cfg, PlatformDarwin); len(got) != 1 || got[0] != "jq" {
				t.Errorf("EffectivePackages(darwin) = %v, want [jq]", got)
			}
			if v, _ := MergeMiseTools(cfg).Get("neovim"); v != "nightly" {
				t.Errorf("MergeMiseTools lost install.mise: neovim = %v", v)
			}
			if _, withheld := JailMiseTools(cfg, true); len(withheld) != 1 || withheld[0] != "neovim" {
				t.Errorf("JailMiseTools(capture) withheld = %v, want [neovim]", withheld)
			}
			if res := Resources(cfg); res == nil {
				t.Error("Resources found no machine.resources")
			} else if _, ok := res.Get("cpus"); !ok {
				t.Errorf("Resources = %v, want cpus", res)
			}
		})
	}
}

// The legacy top-level spellings stay readable for a caller that hands an unprojected map in
// directly, and a present new spelling wins over an old one beside it.
func TestTheSharedReadersFallBackToTheLegacySpelling(t *testing.T) {
	cfg := mustDecodeConfig(t, `{"mise_tools": {"a": "1"}, "resources": {"cpus": 1}, "packages": ["jq"]}`)
	if tools := OwnMiseTools(cfg); tools == nil || tools.Len() != 1 {
		t.Errorf("OwnMiseTools legacy = %v", tools)
	}
	if res := Resources(cfg); res == nil {
		t.Error("Resources legacy = nil")
	}
	if got := OwnNixPackages(cfg); len(got) != 1 {
		t.Errorf("OwnNixPackages legacy = %v", got)
	}
	both := mustDecodeConfig(t, `{"mise_tools": {"old": "1"}, "install": {"mise": {"new": "1"}, "nix": []},
"packages": ["jq"]}`)
	if tools := OwnMiseTools(both); tools == nil || tools.Keys()[0] != "new" || tools.Len() != 1 {
		t.Errorf("install.mise did not win: %v", tools)
	}
	if got := OwnNixPackages(both); len(got) != 0 {
		t.Errorf("a present install.nix did not win: %v", got)
	}
}

func mustDecodeConfig(t *testing.T, body string) *jsonx.OrderedMap {
	t.Helper()
	decoded, err := jsonx.Decode([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	return decoded.(*jsonx.OrderedMap)
}
