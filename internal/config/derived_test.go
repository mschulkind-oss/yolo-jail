package config

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// `platforms` filters BEFORE anything materializes, which is the whole design: nix
// never evaluates the excluded entry, never reports it skipped, and the aggregated
// "no build for this platform" error downstream can treat everything still missing
// as a genuine problem.
func TestEffectivePackagesFiltersByPlatform(t *testing.T) {
	cfg := jsonx.NewOrderedMap()
	linuxOnly := jsonx.NewOrderedMap()
	linuxOnly.Set("name", "strace")
	linuxOnly.Set("platforms", []any{"linux"})
	darwinOnly := jsonx.NewOrderedMap()
	darwinOnly.Set("name", "darwin-thing")
	darwinOnly.Set("platforms", []any{"darwin"})
	pinned := jsonx.NewOrderedMap() // object form, no platforms → everywhere
	pinned.Set("name", "freetype")
	pinned.Set("nixpkgs", "abc123")
	cfg.Set("packages", []any{"jq", linuxOnly, darwinOnly, pinned})

	names := func(entries []any) []string {
		var out []string
		for _, e := range entries {
			if s, ok := e.(string); ok {
				out = append(out, s)
				continue
			}
			if m, ok := e.(*jsonx.OrderedMap); ok {
				if v, _ := m.Get("name"); v != nil {
					out = append(out, v.(string))
				}
			}
		}
		return out
	}

	// A bare string and an object with no `platforms` both mean EVERY platform, so
	// every config written before this key existed means exactly what it did before.
	gotLinux := names(EffectivePackages(cfg, PlatformLinux))
	wantLinux := []string{"jq", "strace", "freetype"}
	if strings.Join(gotLinux, ",") != strings.Join(wantLinux, ",") {
		t.Errorf("linux = %v, want %v", gotLinux, wantLinux)
	}
	gotDarwin := names(EffectivePackages(cfg, PlatformDarwin))
	wantDarwin := []string{"jq", "darwin-thing", "freetype"}
	if strings.Join(gotDarwin, ",") != strings.Join(wantDarwin, ",") {
		t.Errorf("darwin = %v, want %v", gotDarwin, wantDarwin)
	}
	// An empty platform disables filtering — what a config dump or a diff wants.
	if got := len(EffectivePackages(cfg, "")); got != 4 {
		t.Errorf("unfiltered = %d entries, want all 4", got)
	}
}

// PackagesExcludedOn names what `platforms` dropped, so the hard error can explain
// why a package the user wrote is absent without complaining about it.
func TestPackagesExcludedOnNamesTheDroppedEntries(t *testing.T) {
	cfg := jsonx.NewOrderedMap()
	linuxOnly := jsonx.NewOrderedMap()
	linuxOnly.Set("name", "strace")
	linuxOnly.Set("platforms", []any{"linux"})
	cfg.Set("packages", []any{"jq", linuxOnly})

	if got := PackagesExcludedOn(cfg, PlatformDarwin); len(got) != 1 || got[0] != "strace" {
		t.Errorf("excluded on darwin = %v, want [strace]", got)
	}
	if got := PackagesExcludedOn(cfg, PlatformLinux); len(got) != 0 {
		t.Errorf("excluded on linux = %v, want none", got)
	}
}

// `platforms` alone is a COMPLETE object form. The object exists to carry something a
// bare string cannot, and a platform restriction is exactly that — and it is the
// spelling the macos-user skip error tells users to write, so it must validate on its
// own. Found 2026-09-04 by running that error's own advice, which failed.
func TestPlatformsAloneIsAValidPackageObject(t *testing.T) {
	cfg := jsonx.NewOrderedMap()
	entry := jsonx.NewOrderedMap()
	entry.Set("name", "strace")
	entry.Set("platforms", []any{"linux"})
	cfg.Set("packages", []any{entry})

	var errs []string
	validatePackages(cfg, &errs)
	if len(errs) != 0 {
		t.Errorf("a name+platforms object was refused: %v", errs)
	}
}

// An unknown platform is an ERROR, not an entry that quietly never matches: a typo
// like "macos" would otherwise drop the package on every platform in silence, which
// is the exact failure the aggregated skip error exists to prevent.
func TestUnknownPlatformIsAConfigError(t *testing.T) {
	cfg := jsonx.NewOrderedMap()
	entry := jsonx.NewOrderedMap()
	entry.Set("name", "strace")
	entry.Set("platforms", []any{"macos"})
	cfg.Set("packages", []any{entry})

	var errs []string
	validatePackages(cfg, &errs)
	joined := strings.Join(errs, "\n")
	if !strings.Contains(joined, "unknown platform") {
		t.Errorf("an unknown platform was accepted: %v", errs)
	}
	// Naming the two legal values, and that they are GOOS rather than nix doubles,
	// is what makes the error self-fixing.
	if !strings.Contains(joined, "x86_64-linux") {
		t.Errorf("the error does not warn off the nix system-double spelling: %v", errs)
	}
}
