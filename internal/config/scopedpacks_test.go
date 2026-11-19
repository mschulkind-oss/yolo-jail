package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

func TestLoadPackEntriesReadsDirectUserScopedTOML(t *testing.T) {
	home := readablePackTestHome(t)
	root := filepath.Join(home, ".config", "yolo-jail")
	pack := filepath.Join(root, "packs", "corpus")
	if err := os.MkdirAll(pack, 0755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(root, paths.UserConfigTOMLName),
		`packs = [{ pack = "local/corpus", skills = { dir = "skills/core-tools", only = ["skill-a", "skill-b"] } }]`+"\n")
	entries, problems, err := LoadPackEntries()
	entries = withoutDefaultSelected(entries)
	if err != nil || len(problems) != 0 || len(entries) != 1 {
		t.Fatalf("direct scoped TOML: entries=%+v problems=%v err=%v", entries, problems, err)
	}
	if len(entries[0].Only) != 0 || len(entries[0].Exclude) != 0 {
		t.Fatalf("modern selection leaked into legacy fields: %+v", entries[0])
	}
	wire, err := MarshalPacks(entries)
	if err != nil {
		t.Fatal(err)
	}
	roundtrip, err := UnmarshalPacks(wire)
	if err != nil {
		t.Fatal(err)
	}
	again, err := MarshalPacks(roundtrip)
	if err != nil || again != wire {
		t.Fatalf("wire drift: %s %s %v", wire, again, err)
	}
}
