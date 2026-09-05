package run

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// packname_test.go pins WHERE a pack's effective name comes from, on the real path — the
// user's `packs` line, through staging, to the `packload.Pack` every message and every
// /ctx mount reads.
//
// It exists because the answer was documented wrongly and cost a bug report. `Pack.Name`
// said "config override, else manifest, else dir", which reads as a ladder a pack author
// can climb by putting a `name` in pack.json. They cannot: the config layer fills the name
// in from the SOURCE ADDRESS before anything is staged, so the manifest rung is never
// reached by any production caller. That is deliberate — see the field comment in
// internal/packload/packload.go for the three things the name has to be — but nothing
// pinned it, so the next reader had only the wrong docstring to go on.

// TestConfiguredPackNameComesFromTheAddressNotTheManifest is the whole rule in one
// measurement: a bare `file://` entry with NO explicit `name`, whose pack.json declares a
// different one, ends up named after the last segment of its source address.
//
// It goes through stagePacks rather than calling packload.LoadDir directly, which is the
// point — LoadDir's own fallback ladder does prefer the manifest, and a test of LoadDir
// alone would report the docstring as true while the shipped path disagreed.
func TestConfiguredPackNameComesFromTheAddressNotTheManifest(t *testing.T) {
	home := packHome(t)
	// A distinctive basename, not t.TempDir()'s numeric one: the assertion has to be able
	// to tell "took the address" from "took anything at all".
	src := filepath.Join(t.TempDir(), "house-rules")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "pack.json"),
		[]byte(`{"name":"house","description":"addressed house rules"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	writeUserPacks(t, home, `["file://`+src+`"]`)

	o := &Options{Workspace: t.TempDir()}
	_, loaded, _, err := o.stagePacks("yolo-test-packname")
	if err != nil {
		t.Fatalf("stagePacks: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("loaded %d packs, want exactly the configured one", len(loaded))
	}
	p := loaded[0]
	// The manifest WAS read — this is not a "pack.json got skipped" result.
	if p.Decl.Name != "house" {
		t.Fatalf("Decl.Name = %q, want house — the manifest was not read, so this test is "+
			"measuring the wrong thing", p.Decl.Name)
	}
	if p.Name != "house-rules" {
		t.Errorf("Pack.Name = %q, want %q (the source address's last segment).\n"+
			"If this now reports the manifest's name, the effective name has moved and "+
			"three other things must move with it: config.PackEntry.Slug (the staging "+
			"dir), the pack-drop prune that keys on it, and the entrypoint's read of the "+
			"staged dir name — which is what makes packload.CtxPath resolve to the same "+
			"/ctx path on both sides.", p.Name, "house-rules")
	}
}

// TestStagedPackNameIsWhatTheJailWillDerive is the reason the rule above cannot simply be
// changed to prefer the manifest: the two halves derive the name from different things and
// must still agree.
//
// The host names a pack from its config entry; the entrypoint names it from the staged
// DIRECTORY (internal/entrypoint/packsurfaces.go, "<root>/<slug> for configured ones"),
// because a staged tree is all the jail has. packload.CtxPath turns that name into the
// /ctx path a `reads-host` grant is mounted at — the CLI emits the mount destination and
// the entrypoint reads the host layer from it — so a host that named the pack differently
// would mount the user's file where the jail does not look, and the surface would compose
// from defaults with nothing in the launch to say so.
func TestStagedPackNameIsWhatTheJailWillDerive(t *testing.T) {
	home := packHome(t)
	src := filepath.Join(t.TempDir(), "house-rules")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "pack.json"),
		[]byte(`{"name":"house","contributes":[`+
			`{"kind":"reads-host","host":".acme/settings.json"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	writeUserPacks(t, home, `["file://`+src+`"]`)

	o := &Options{Workspace: t.TempDir()}
	_, loaded, _, err := o.stagePacks("yolo-test-packname-agree")
	if err != nil {
		t.Fatalf("stagePacks: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("loaded %d packs, want exactly the configured one", len(loaded))
	}
	host := loaded[0]
	// Exactly what LoadJailPacks does with the tree the host just staged.
	jail, problems := packload.LoadDir(host.Root, filepath.Base(host.Root))
	if len(problems) != 0 {
		t.Fatalf("the jail could not load the staged tree: %v", problems)
	}
	if jail.Name != host.Name {
		t.Fatalf("host named the pack %q, the jail will name it %q — every /ctx path "+
			"derived from the name now differs between the two halves", host.Name, jail.Name)
	}
	granted, _ := host.HonoredHostFiles()
	if len(granted) != 1 {
		t.Fatalf("granted = %v, want the one reads-host declaration", granted)
	}
	// Spelled out rather than compared to itself: CtxPath is a pure function of the name,
	// so asserting CtxPath(jail) == CtxPath(host) would only restate the check above. This
	// is the path the CLI's hostFileArgs emits as a mount destination and the entrypoint's
	// host-layer read opens, and it says which name got baked into it.
	if got := packload.CtxPath(host.Name, granted[0]); got != "/ctx/host-house-rules/settings.json" {
		t.Errorf("mount destination = %q, want /ctx/host-house-rules/settings.json — a "+
			"grant with no `into` derives its /ctx dir from the pack name, so moving the "+
			"name moves where the user's host file lands", got)
	}
}
