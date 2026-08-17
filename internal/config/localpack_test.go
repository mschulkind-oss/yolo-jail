package config

// localpack_test.go pins the CONVENTIONAL LOCAL PACK (`~/.config/yolo-jail/local`,
// roadmap.md §6a-2): an implicitly-included pack needing no `packs` entry.
//
// Every test points HOME at a t.TempDir(), so the real ~/.config/yolo-jail — which on a
// development machine holds the live jail's own config — is never read or written.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// localPackHome points HOME at a temp dir, writes the user config body, and creates the
// local pack dir when `withLocal` — the two inputs every test here varies.
func localPackHome(t *testing.T, body string, withLocal bool) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	write(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"), body)
	if withLocal {
		if err := os.MkdirAll(filepath.Join(home, ".config", "yolo-jail", "local", "skills"),
			0o755); err != nil {
			t.Fatal(err)
		}
	}
	return home
}

// ABSENT IS NORMAL, and silent. Most users will never create the directory; its absence must
// add no entry, no warning and no error — the state a stock install is in.
func TestLocalPackAbsentIsSilent(t *testing.T) {
	localPackHome(t, `{"packs": ["claude"]}`, false)
	var warnings []string
	entries, err := LoadPacks(func(m string) { warnings = append(warnings, m) })
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name != "claude" {
		t.Fatalf("entries = %v, want just claude — an absent local pack must add nothing", entries)
	}
	if len(warnings) != 0 {
		t.Errorf("an absent local pack warned: %v", warnings)
	}
}

// PRESENT means included with NO config line at all. This is the whole point of the
// convention: a user with three personal skills should never have to author a manifest or a
// `packs` entry.
func TestLocalPackIncludedWithoutAConfigEntry(t *testing.T) {
	home := localPackHome(t, `{"packs": ["claude"]}`, true)
	entries, err := LoadPacks(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries = %v, want claude plus the implicit local pack", entries)
	}
	local := entries[1]
	if local.Name != LocalPackName {
		t.Errorf("local entry name = %q, want %q", local.Name, LocalPackName)
	}
	if !local.Implicit {
		t.Error("the local pack is not marked Implicit — the empty-packs notice keys on that")
	}
	want := "file://" + filepath.Join(home, ".config", "yolo-jail", "local")
	if local.Source != want {
		t.Errorf("source = %q, want %q", local.Source, want)
	}
	if local.Source != "file://"+paths.LocalPackDir() {
		t.Errorf("source %q does not match paths.LocalPackDir() %q — the two must agree or the "+
			"host and the jail resolve different directories", local.Source, paths.LocalPackDir())
	}
}

// A config with NO `packs` key at all still gets the local pack. That is the config of the
// exact user this convention is for, and an implementation that only appended inside the
// "packs key present" branch would deliver nothing to them.
func TestLocalPackIncludedWithNoPacksKey(t *testing.T) {
	localPackHome(t, `{}`, true)
	entries, err := LoadPacks(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name != LocalPackName {
		t.Fatalf("entries = %v, want just the implicit local pack", entries)
	}
}

// ORDER IS LOAD-BEARING AND IT IS LAST. The delivery order at both notches is entry order —
// the jail merges pack skills dirs in it (later wins a same-named skill) and the host renders
// `loaded` in it — so the local pack being last is what makes a PERSONAL skill outrank a
// shared pack's.
func TestLocalPackComposesLast(t *testing.T) {
	localPackHome(t, `{"packs": ["claude", "pi", "codex"]}`, true)
	entries, err := LoadPacks(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 4 {
		t.Fatalf("entries = %v, want three configured packs plus the local one", entries)
	}
	if entries[len(entries)-1].Name != LocalPackName {
		t.Errorf("the local pack is at position %d of %d, not last — a personal skill would "+
			"then lose to a shared pack's: %v", len(entries), len(entries), entries)
	}
}

// A CONFIGURED pack named `local` wins the slot. Two entries with one name share a staging dir
// and the second silently overwrites the first, and yielding to the explicit one is the honest
// direction: a config line the user wrote outranks a convention yolo applied for them.
func TestLocalPackYieldsToAConfiguredPackOfTheSameName(t *testing.T) {
	elsewhere := t.TempDir()
	localPackHome(t, `{"packs": [{"source": "file://`+elsewhere+`", "name": "local"}]}`, true)
	entries, err := LoadPacks(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %v, want exactly the configured pack — two packs named `local` would "+
			"share one staging dir", entries)
	}
	if entries[0].Source != "file://"+elsewhere {
		t.Errorf("source = %q, want the CONFIGURED address %q", entries[0].Source, elsewhere)
	}
	if entries[0].Implicit {
		t.Error("the surviving entry is marked Implicit — it came from a config line")
	}
}

// A NON-DIRECTORY at the conventional path is treated as absent. Handing a pack loader a file
// would fail with an error about a path nothing in the user's config asked for.
func TestLocalPackNonDirectoryIsAbsent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	write(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"), `{"packs": ["claude"]}`)
	write(t, filepath.Join(home, ".config", "yolo-jail", "local"), "not a pack\n")

	entries, err := LoadPacks(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %v, want just claude — a FILE at the local-pack path is not a pack",
			entries)
	}
}

// THE TRUST DECISION. The local pack may read the host, and it gets there by BEING an ordinary
// local pack: a file:// source means Origin() is OriginLocal and MayGrantHostFiles() is true
// with no special case. Asserted because it is a trust-boundary property, not an incidental
// one — the fetched-pack gate exists because installing someone else's pack is not consent to
// hand that repository your settings; here the author IS the user, and anything the directory
// could declare they could declare in config.jsonc one level up.
func TestLocalPackMayAccessTheHostAsAnOrdinaryLocalPack(t *testing.T) {
	localPackHome(t, `{}`, true)
	entries, err := LoadPacks(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %v", entries)
	}
	local := entries[0]
	if !local.IsLocal() {
		t.Errorf("source %q is not a file:// address — the whole trust argument rests on it "+
			"being an ordinary local pack", local.Source)
	}
	if local.Origin() != OriginLocal {
		t.Errorf("origin = %v, want OriginLocal", local.Origin())
	}
	if !local.MayGrantHostFiles() {
		t.Error("the local pack may not grant host files — it is the user's own directory on " +
			"their own machine, readable by them without yolo's help")
	}
	if local.Embedded() {
		t.Error("the local pack reports Embedded() — it is not shipped with yolo, and embedded " +
			"origin carries yolo's release authority rather than the user's")
	}
}

// The local pack is CONTENT, never an agent, so it must not silence the empty-packs notice: a
// jail whose only pack is ~/.config/yolo-jail/local has skills and prose and still nothing to
// run them. This is the assertion that keeps HasConfiguredPack from collapsing to len().
func TestLocalPackDoesNotCountAsAConfiguredPack(t *testing.T) {
	localPackHome(t, `{}`, true)
	entries, err := LoadPacks(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("the local pack was not included at all")
	}
	if HasConfiguredPack(entries) {
		t.Error("a lone local pack counts as a configured pack — the no-agent notice would be " +
			"silenced for a jail that genuinely has no coding agent")
	}
	// And the inverse, so the helper is not vacuously false.
	localPackHome(t, `{"packs": ["claude"]}`, true)
	entries, err = LoadPacks(nil)
	if err != nil {
		t.Fatal(err)
	}
	if !HasConfiguredPack(entries) {
		t.Error("a configured `claude` alongside the local pack does not count as configured")
	}
}
