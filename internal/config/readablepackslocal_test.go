package config

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

const localTestRepo = `git = "https://github.com/owner/tillr"`

// The `local` key of a git pack entry (docs/design/local-pack-clones.md §6.1, LPC-D7): every accepted and
// refused spelling, read through the real loader so a validation that stops being called fails here.
func TestLoadPackEntriesLocalKey(t *testing.T) {
	for _, tc := range []struct {
		name, entry, want string // want "" = accepted
		wantLocal         string // with ~ meaning the test home
	}{
		{"absolute", `{ ` + localTestRepo + `, ref = "main", local = "/srv/clones/tillr" }`, "", "/srv/clones/tillr"},
		{"tilde", `{ ` + localTestRepo + `, ref = "main", local = "~/src/tillr" }`, "", "~/src/tillr"},
		{"bare tilde", `{ ` + localTestRepo + `, ref = "main", local = "~" }`, "", "~"},
		{"slash branch", `{ ` + localTestRepo + `, ref = "feature/x", local = "/srv/c" }`, "", "/srv/c"},
		{"with subdir", `{ ` + localTestRepo + `, ref = "main", subdir = "packs/a", local = "/srv/c" }`, "", "/srv/c"},
		{"with namespace", `{ ` + localTestRepo + `, ref = "main", namespace = "mine", local = "/srv/c" }`, "", "/srv/c"},
		{"no local", `{ ` + localTestRepo + `, ref = "main" }`, "", ""},
		{"relative", `{ ` + localTestRepo + `, ref = "main", local = "src/tillr" }`, "not absolute", ""},
		{"dot relative", `{ ` + localTestRepo + `, ref = "main", local = "./tillr" }`, "not absolute", ""},
		{"user tilde", `{ ` + localTestRepo + `, ref = "main", local = "~bob/tillr" }`, "not absolute", ""},
		{"empty", `{ ` + localTestRepo + `, ref = "main", local = "" }`, "non-empty", ""},
		{"not a string", `{ ` + localTestRepo + `, ref = "main", local = 3 }`, "expected a string", ""},
		{"no ref", `{ ` + localTestRepo + `, local = "/srv/c" }`, `local follows a branch: write ref = "main"`, ""},
		{"commit ref", `{ ` + localTestRepo + `, ref = "0123456789abcdef0123456789abcdef01234567", local = "/srv/c" }`, "local follows a branch", ""},
		{"tag ref", `{ ` + localTestRepo + `, ref = "refs/tags/v1", local = "/srv/c" }`, "local follows a branch", ""},
		{"pack handle", `{ pack = "yolo/claude", local = "/srv/c" }`, "only on a { git = … } entry", ""},
		{"skills", `{ ` + localTestRepo + `, ref = "main", local = "/srv/c", skills = { only = ["a"] } }`, "drop skills", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := readablePackTestHome(t)
			write(t, filepath.Join(home, ".config", "yolo-jail", paths.UserConfigTOMLName), "packs = ["+tc.entry+"]\n")
			entries, problems, err := LoadPackEntries()
			entries = withoutDefaultSelected(entries)
			text := fmt.Sprintf("%v %v", err, problems)
			if tc.want != "" {
				if !strings.Contains(text, tc.want) || !strings.Contains(text, "packs[0]") {
					t.Fatalf("want refusal %q naming the entry, got %s", tc.want, text)
				}
				if !strings.Contains(text, "yolo config") {
					t.Errorf("refusal names no next step (yolo config …): %s", text)
				}
				return
			}
			if err != nil || len(problems) != 0 || len(entries) != 1 {
				t.Fatalf("want one accepted entry, got %+v %s", entries, text)
			}
			want := strings.Replace(tc.wantLocal, "~", home, 1)
			if entries[0].Local != want {
				t.Fatalf("Local = %q, want %q", entries[0].Local, want)
			}
		})
	}
}

// The other scopes: a workspace `packs` is refused whole, `pack_sources` takes no `local`, and the legacy
// JSONC spelling does not know the key.
func TestLocalKeyScope(t *testing.T) {
	t.Run("workspace packs", func(t *testing.T) {
		home := t.TempDir()
		ws := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("YOLO_VERSION", "")
		write(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"), `{}`)
		write(t, filepath.Join(ws, WorkspaceConfigName), `{"packs": [{"git": "https://github.com/owner/tillr", "ref": "main", "local": "/srv/c"}]}`)
		errs, _ := ValidateConfig(decode(t, `{}`), ws, nil)
		if !strings.Contains(strings.Join(errs, "\n"), "config.packs: user-scope only") {
			t.Fatalf("workspace packs with local not refused as user-scope: %v", errs)
		}
	})
	t.Run("pack_sources", func(t *testing.T) {
		home := readablePackTestHome(t)
		write(t, filepath.Join(home, ".config", "yolo-jail", paths.UserConfigTOMLName),
			"packs = ['src/x']\n[pack_sources]\nsrc = { "+localTestRepo+", ref = \"main\", local = \"/srv/c\" }\n")
		_, problems, err := LoadPackEntries()
		text := fmt.Sprintf("%v %v", err, problems)
		if !strings.Contains(text, "local") {
			t.Fatalf("pack_sources local not refused: %s", text)
		}
	})
	t.Run("legacy jsonc", func(t *testing.T) {
		home := readablePackTestHome(t)
		write(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"),
			`{"packs": [{"source": "git+https://github.com/owner/tillr?ref=main", "local": "/srv/c"}]}`)
		_, problems, err := LoadPackEntries()
		text := fmt.Sprintf("%v %v", err, problems)
		if !strings.Contains(text, "local") {
			t.Fatalf("legacy JSONC local not refused: %s", text)
		}
	})
}

// THE HOP TO THE STORE (docs/design/local-pack-clones.md slice 5): the launch hands the store its packs through
// RefreshSpec, and the store decides a copy only for a pack whose spec carries the clone. A spec that drops
// `local` leaves every clone unconsulted with no other test failing.
func TestRefreshSpecCarriesTheLocalClone(t *testing.T) {
	home := readablePackTestHome(t)
	write(t, filepath.Join(home, ".config", "yolo-jail", paths.UserConfigTOMLName),
		"packs = [{ "+localTestRepo+", ref = \"main\", local = \"~/src/tillr\" }, { git = \"https://github.com/owner/other\", ref = \"main\" }]\n")
	entries, problems, err := LoadPackEntries()
	entries = withoutDefaultSelected(entries)
	if err != nil || len(problems) != 0 || len(entries) != 2 {
		t.Fatalf("want two entries, got %d %v %v", len(entries), problems, err)
	}
	if got, want := entries[0].RefreshSpec(false).Local, filepath.Join(home, "src", "tillr"); got != want {
		t.Errorf("RefreshSpec().Local = %q, want %q", got, want)
	}
	if got := entries[1].RefreshSpec(false).Local; got != "" {
		t.Errorf("a pack with no local has RefreshSpec().Local = %q", got)
	}
}
