package entrypoint

// adoptionarchivelosses_test.go measures OQ-CO7's archive AGAINST ADOPTIONS THAT ACTUALLY LOSE
// (docs/design/config-ownership-and-promotion.md §6.3.3).
//
// ⚠ WHY THIS IS A SEPARATE MEASUREMENT FROM adoptionarchive_test.go. Every fixture there is one
// adoption PRESERVES: the seeds carry an undeclared top-level key and an undeclared leaf under a
// declared object, both of which the two narrowing passes keep, so those tests pin that a copy
// was taken and that it holds the right bytes — never that the copy is the only remaining record
// of something. A net measured exclusively on inputs that lose nothing is a net measured with
// the load off. The two fixtures below are the two adoptions this engine really does destroy:
//
//	a LEAF, at object granularity — dropComputedTables drops a top-level key the computed
//	layer holds as an object WHOLESALE, so a user's own entry under it goes. This is the
//	residue hostownedadoption_test.go's ⚠ names as known, documented and not that step's to
//	close; it is precisely the "deep-merged leaf" the ruling exists to net.
//
//	the WHOLE FILE, at keyless granularity — ComposeStateful adopts nothing from a raw/lines
//	surface ("KEYLESS surfaces are deliberately NOT adopted"), so its first migration replaces
//	the file outright. The host notch REFUSES such a surface (OQ-CO9); the jail does not, which
//	makes this the most destructive adoption reachable anywhere and the one with no other
//	record at all.
//
// Both drive the boot loop verbatim (ConfigurePackSurfaces), for the reason adoptionarchive_test.go
// states: delete the archiveAdoption call from persistStatefulSurface and both go red.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// computedTablePack owns a JSON surface whose DERIVE emits a top-level object (`env`), which is
// what makes that key a computed table. The pack needs a Root because packload.DeriveScript
// reads derive.lua off disk — the registration IS the declaration, so there is no manifest field
// to set instead.
func computedTablePack(t *testing.T) *packload.Pack {
	t.Helper()
	root := t.TempDir()
	script := "yolo.derive(\"acme\", \"settings\", function(ctx)\n" +
		"  return { env = { YOLO_OWNED = \"1\" } }\n" +
		"end)\n"
	if err := os.WriteFile(filepath.Join(root, "derive.lua"), []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal([]any{map[string]any{
		"agent": "acme", "name": "settings", "codec": "json",
		"path":     "~/.acme/settings.json",
		"defaults": map[string]any{"theme": "system"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	return &packload.Pack{Name: "acme", Root: root, Decl: &packdecl.Manifest{
		Contributes: []packdecl.Contribution{{Kind: packdecl.KindConfig, Raw: raw}},
	}}
}

// THE ARCHIVE HOLDS A KEY THE ADOPTION DESTROYED. The user's own `env` entry sits under a key
// the computed layer holds as an object, so the residue loses it wholesale and the rendered file
// does not carry it — the archive is where it still is.
func TestJailAdoptionArchivesAKeyTheRenderDropped(t *testing.T) {
	const seed = `{
  "env": {
    "MY_HAND_WRITTEN": "keepme"
  }
}
`
	e, path := jailAdoptionHome(t, seed)
	ConfigurePackSurfaces(e, []*packload.Pack{computedTablePack(t)})
	if fails := e.GenFailures(); len(fails) != 0 {
		t.Fatalf("boot render failed: %v", fails)
	}
	rendered, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(rendered), "MY_HAND_WRITTEN") {
		t.Skip("adoption no longer drops a user's entry under a computed table; the loss this " +
			"test nets does not exist here any more (see dropComputedTables)")
	}

	got, err := os.ReadFile(jailConfigArchive(e.Workspace, "acme", "settings", "settings.json"))
	if err != nil {
		t.Fatalf("adoption dropped the user's `env` entry and archived nothing: %v\n\nThe "+
			"render below no longer holds it, so the archive is the only record it existed — "+
			"which is the whole of what OQ-CO7 rules.\n%s", err, rendered)
	}
	if !strings.Contains(string(got), "MY_HAND_WRITTEN") {
		t.Errorf("the archive does not hold the key the render dropped.\narchived:\n%s\nthe "+
			"file before the render:\n%s", got, seed)
	}
}

// keylessPack owns a `lines` surface — the kind ComposeStateful adopts nothing from, so its
// first migration replaces the file outright. No defaults: a pack's SurfaceDTO carries object
// layers only, so a keyless surface's pure render is the empty file, which is exactly the
// wholesale loss worth measuring.
func keylessPack(t *testing.T) *packload.Pack {
	t.Helper()
	raw, err := json.Marshal([]any{map[string]any{
		"agent": "acme", "name": "ignore", "codec": "lines",
		"path": "~/.acme/ignore",
	}})
	if err != nil {
		t.Fatal(err)
	}
	return &packload.Pack{Name: "acme", Decl: &packdecl.Manifest{
		Contributes: []packdecl.Contribution{{Kind: packdecl.KindConfig, Raw: raw}},
	}}
}

// THE WHOLE FILE, AND THE ARCHIVE IS ALL THAT IS LEFT OF IT. A keyless stateful surface is not
// adopted at all, so the first migration blanks it; there is no residue, no overlay and no
// last_render of a previous render to read it back out of.
//
// The host notch cannot reach this — hostStatefulRefusal declines a keyless surface under `own`
// (OQ-CO9) precisely because confirmHostLosses is blind to a loss with no named entries in it —
// so the jail is where the class lives, and the jail has no prompt. That asymmetry is the whole
// argument for the copy (P5), which makes this the sharpest case it has to cover.
func TestJailAdoptionArchivesAKeylessFileItReplacesWholesale(t *testing.T) {
	e := &Env{Home: t.TempDir(), Workspace: t.TempDir(), Vars: map[string]string{}}
	withCtxRoot(t, t.TempDir(), "acme")
	path := filepath.Join(e.Home, ".acme", "ignore")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	const seed = "my-private-dir\nhand-written\n"
	if err := os.WriteFile(path, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}

	ConfigurePackSurfaces(e, []*packload.Pack{keylessPack(t)})
	if fails := e.GenFailures(); len(fails) != 0 {
		t.Fatalf("boot render failed: %v", fails)
	}
	rendered, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(rendered) == seed {
		t.Skip("a keyless stateful surface is no longer replaced wholesale at its first " +
			"migration; the loss this test nets does not exist here any more")
	}

	got, err := os.ReadFile(jailConfigArchive(e.Workspace, "acme", "ignore", "ignore"))
	if err != nil {
		t.Fatalf("a keyless surface was replaced wholesale with no archive: %v\n\nNothing else "+
			"records what the file held — a keyless surface takes no residue, so the copy is "+
			"the only net. The gate must key on BYTES BEING PRESENT, never on the surface "+
			"having keys to adopt.", err)
	}
	if string(got) != seed {
		t.Errorf("the archive holds %q, want the file as yolo found it (%q)", got, seed)
	}
}
