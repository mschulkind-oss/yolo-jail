package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

func TestLoadPackEntriesScopedSyntax(t *testing.T) {
	for _, tc := range []struct{ name, fields, want string }{
		{"typed", `skills = { only = ["a*"] }`, ""},
		{"raw", `subdir = "nested", skills = { dir = "." }`, ""},
		{"whole relocated", `subdir = "nested/pack"`, ""},
		{"empty table", `skills = {}`, "add dir, only or exclude"},
		{"empty inclusion", `skills = { only = [] }`, "remove the key"},
		{"empty exclusion", `skills = { exclude = [] }`, "remove the key"},
		{"empty name", `skills = { only = [""] }`, "invalid skill selector"},
		{"dot name", `skills = { only = ["."] }`, "invalid skill selector"},
		{"parent name", `skills = { only = [".."] }`, "invalid skill selector"},
		{"slash name", `skills = { only = ["a/b"] }`, "invalid skill selector"},
		{"backslash name", `skills = { only = ['a\b'] }`, "invalid skill selector"},
		{"glob syntax", `skills = { only = ["["] }`, "invalid skill selector"},
		{"duplicate", `skills = { only = ["a", "a"] }`, "duplicate selector"},
		{"non string", `skills = { only = [1] }`, "expected"},
		{"non table", `skills = "a"`, "expected"},
		{"empty subdir", `subdir = ""`, "omit subdir"},
		{"dot subdir", `subdir = "."`, "prohibited"},
		{"traversal", `subdir = "a/../b"`, "prohibited"},
		{"empty segment", `subdir = "a//b"`, "prohibited"},
		{"git metadata", `skills = { dir = ".git" }`, "prohibited"},
		{"absolute", `skills = { dir = "/skills" }`, "relative"},
		{"tilde", `skills = { dir = "~/skills" }`, "relative"},
		{"row knob", `skills = { kind = "directory" }`, "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := readablePackTestHome(t)
			write(t, filepath.Join(home, ".config", "yolo-jail", paths.UserConfigTOMLName), fmt.Sprintf("packs = [{ pack = \"local/corpus\", %s }]\n", tc.fields))
			entries, problems, err := LoadPackEntries()
			entries = withoutDefaultSelected(entries)
			text := fmt.Sprintf("%v %v", err, problems)
			if tc.want == "" {
				if err != nil || len(problems) != 0 || len(entries) != 1 {
					t.Fatalf("%+v %s", entries, text)
				}
			} else if !strings.Contains(text, tc.want) || !strings.Contains(text, "config.toml") || !strings.Contains(text, "packs[0]") {
				t.Fatalf("wanted %q and origin: %s", tc.want, text)
			}
		})
	}
}

func TestLoadPackEntriesScopedImportTuples(t *testing.T) {
	for _, tc := range []struct{ name, entries, want string }{
		{"two raw roots", `{ pack = "local/corpus", skills = { dir = "a" } }, { pack = "local/corpus", skills = { dir = "b" } }`, ""},
		{"two subdirs", `{ pack = "local/corpus", subdir = "a", skills = { only = ["x"] } }, { pack = "local/corpus", subdir = "b", skills = { only = ["x"] } }`, ""},
		{"same typed root", `{ pack = "local/corpus", skills = { only = ["a"] } }, { pack = "local/corpus", skills = { only = ["b"] } }`, "duplicate pack handle"},
		{"whole and scoped", `"local/corpus", { pack = "local/corpus", skills = { dir = "a" } }`, "duplicate pack handle"},
		{"same raw root", `{ pack = "local/corpus", skills = { dir = "a" } }, { pack = "local/corpus", skills = { dir = "a", only = ["x"] } }`, "duplicate pack handle"},
		{"different refs", `{ git = "https://example.invalid/o/r", ref = "one", skills = { dir = "a" } }, { git = "https://example.invalid/o/r", ref = "two", skills = { dir = "b" } }`, "authored ref"},
		{"omitted explicit ref", `{ git = "https://example.invalid/o/r", skills = { dir = "a" } }, { git = "https://example.invalid/o/r", ref = "main", skills = { dir = "b" } }`, "authored ref"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := readablePackTestHome(t)
			if err := os.MkdirAll(filepath.Join(home, ".config/yolo-jail/packs/corpus"), 0755); err != nil {
				t.Fatal(err)
			}
			write(t, filepath.Join(home, ".config/yolo-jail/config.toml"), "packs = ["+tc.entries+"]\n")
			entries, problems, err := LoadPackEntries()
			entries = withoutDefaultSelected(entries)
			if err != nil {
				t.Fatal(err)
			}
			if tc.want == "" {
				if len(problems) != 0 || len(entries) != 2 || entries[0].ImportIdentity() == entries[1].ImportIdentity() {
					t.Fatalf("entries=%+v problems=%v", entries, problems)
				}
			} else if len(problems) != 1 || !strings.Contains(problems[0], tc.want) || !strings.Contains(problems[0], "packs[0]") || !strings.Contains(problems[0], "packs[1]") {
				t.Fatalf("%v", problems)
			}
		})
	}
}

func TestLoadPackEntriesScopedShippedAndLegacyBoundaries(t *testing.T) {
	for _, tc := range []struct{ body, file, want string }{
		{`packs = [{ pack = "yolo/claude", skills = { only = ["*"] } }]`, paths.UserConfigTOMLName, ""},
		{`packs = [{ pack = "yolo/claude", subdir = "a" }]`, paths.UserConfigTOMLName, "typed skills selectors only"},
		{`packs = [{ pack = "yolo/claude", skills = { dir = "." } }]`, paths.UserConfigTOMLName, "typed skills selectors only"},
		{`{"packs":[{"source":"claude","skills":{"only":["*"]}}]}`, paths.UserConfigLegacyJSONCName, "unknown"},
		{`{"packs":[{"source":"claude","subdir":"a"}]}`, paths.UserConfigLegacyJSONCName, "unknown"},
	} {
		t.Run(tc.body, func(t *testing.T) {
			home := readablePackTestHome(t)
			write(t, filepath.Join(home, ".config/yolo-jail", tc.file), tc.body+"\n")
			entries, problems, err := LoadPackEntries()
			entries = withoutDefaultSelected(entries)
			text := fmt.Sprintf("%v %v", err, problems)
			if tc.want == "" {
				if err != nil || len(problems) != 0 || len(entries) != 1 {
					t.Fatal(text)
				}
			} else if !strings.Contains(text, tc.want) {
				t.Fatalf("want %q: %s", tc.want, text)
			}
		})
	}
}

func TestLoadPackEntriesScopedArrayTableFragmentOrigin(t *testing.T) {
	home := readablePackTestHome(t)
	root := filepath.Join(home, ".config/yolo-jail")
	write(t, filepath.Join(root, "config.toml"), "# user\n")
	write(t, filepath.Join(root, "config.d/scoped.toml"), "[[packs]]\npack = \"local/corpus\"\n[packs.skills]\nonly = [\"chosen\"]\n")
	entries, problems, err := LoadPackEntries()
	entries = withoutDefaultSelected(entries)
	if err != nil || len(problems) != 0 || len(entries) != 1 {
		t.Fatalf("%v %v %+v", err, problems, entries)
	}
	if entries[0].Skills == nil || entries[0].Skills.Only[0] != "chosen" || !strings.Contains(entries[0].ConfigOrigin, "scoped.toml") {
		t.Fatalf("lost fragment selection/origin: %+v", entries[0])
	}
}
