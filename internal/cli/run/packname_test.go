package run

import (
	"os"
	"path/filepath"
	"strings"
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
	if got := packload.CtxPath(host.StagedSlug(), granted[0]); got != "/ctx/host-house-rules/settings.json" {
		t.Errorf("mount destination = %q, want /ctx/host-house-rules/settings.json — a "+
			"grant with no `into` derives its /ctx dir from the pack's staged directory, so "+
			"moving the name moves where the user's host file lands", got)
	}
}

// TestPackCtxMountAgreesAcrossTheSplitForANonSlugCleanName is the mount-path half of the
// rule above, and it is a SEPARATE test because the check above cannot see the bug it
// pins: that one compares two names, and the mount path is not keyed on the name.
//
// THE MEASUREMENT it reproduces (2026-09-05): with a `packs` entry named `house_rules`,
// the CLI mounted the user's file at /ctx/host-house_rules/settings.json while the
// entrypoint read /ctx/host-house_5frules/settings.json — because the host loaded the
// pack under its CONFIG name and the jail under its STAGED DIRECTORY, which is
// config.PackEntry.Slug's escaping of that name (`_` is outside [A-Za-z0-9.-], so it
// becomes `_5f`). The entrypoint's host-layer read is fail-open, so the surface composed
// from its defaults and the launch said nothing, while the disclosure banner still
// printed the grant.
//
// The fixture's name is NOT slug-clean on purpose. Every pack yolo ships is, so a test
// over a shipped name would pass against a broken derivation.
//
// Both sides are DERIVED HERE THE WAY EACH REALLY DERIVES THEM rather than spelled as two
// literals that happen to agree today: the host side is the destination `hostFileArgs`
// puts in its `-v` argument, and the jail side is the `HostSource` the surface carries
// after being loaded from the staged tree the way entrypoint.LoadJailPacks loads it —
// which is the exact string entrypoint.hostSurfaceBytes opens. Revert either site to
// `p.Name` and this goes red.
func TestPackCtxMountAgreesAcrossTheSplitForANonSlugCleanName(t *testing.T) {
	home := packHome(t)
	src := filepath.Join(t.TempDir(), "house_rules")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	// A `reads-host` grant with NO `into` — the only shape that derives its /ctx dir from
	// the pack at all (CtxPath returns early for an `into`, which is why both shipped
	// grants route around this entirely) — plus the config surface whose host layer reads
	// it.
	if err := os.WriteFile(filepath.Join(src, "pack.json"),
		[]byte(`{"name":"house_rules","contributes":[`+
			`{"kind":"reads-host","host":".acme/settings.json"},`+
			`{"kind":"config","config":[{"agent":"acme","name":"settings","codec":"json",`+
			`"path":"~/.acme/settings.json","managed":{"x":1}}]}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	writeUserPacks(t, home, `["file://`+src+`"]`)
	// hostFileArgs skips a host file that does not exist (a normal state), so the grant
	// has to have something to mount or the host side of the comparison is empty.
	if err := os.MkdirAll(filepath.Join(home, ".acme"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".acme", "settings.json"),
		[]byte(`{"user":true}`), 0o644); err != nil {
		t.Fatal(err)
	}

	o := &Options{Workspace: t.TempDir()}
	_, loaded, _, err := o.stagePacks("yolo-test-ctxagree")
	if err != nil {
		t.Fatalf("stagePacks: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("loaded %d packs, want exactly the configured one", len(loaded))
	}
	host := loaded[0]
	// The fixture only measures anything while the two strings differ. If staging ever
	// stops escaping, this test is comparing a value with itself and must say so rather
	// than passing quietly.
	if filepath.Base(host.Root) == host.Name {
		t.Fatalf("staged dir %q equals the pack name %q — the escaping this test exists "+
			"to cross is gone, so the comparison below cannot fail",
			filepath.Base(host.Root), host.Name)
	}

	// HOST SIDE: the destination the launcher really emits.
	in := &assembleInput{
		wsState:      filepath.Join(home, ".yolo", "home"),
		mountTargets: map[string]struct{}{},
		packs:        loaded,
	}
	args := o.hostFileArgs(in)
	if len(args) != 2 || args[0] != "-v" {
		t.Fatalf("hostFileArgs = %v, want one -v mount for the granted host file", args)
	}
	spec := strings.TrimSuffix(args[1], ":ro")
	hostDest := spec[strings.LastIndex(spec, ":")+1:]

	// JAIL SIDE: exactly what entrypoint.LoadJailPacks does with the tree the host just
	// staged (it has only the directory name to go on), then the HostSource that
	// entrypoint.hostSurfaceBytes opens.
	jail, problems := packload.LoadDir(host.Root, filepath.Base(host.Root))
	if len(problems) != 0 {
		t.Fatalf("the jail could not load the staged tree: %v", problems)
	}
	surfaces, probs := jail.Surfaces()
	if len(probs) != 0 {
		t.Fatalf("jail surfaces: %v", probs)
	}
	if len(surfaces) != 1 {
		t.Fatalf("decoded %d surfaces, want the one the fixture declares", len(surfaces))
	}
	jailSource := surfaces[0].HostSource
	if jailSource == "" {
		t.Fatal("the jail surface has no host layer — the reads-host grant did not resolve, " +
			"so this test is not measuring the mount path")
	}

	if hostDest != jailSource {
		t.Errorf("the two halves disagree about where the grant lands:\n"+
			"  host mounts at %s\n"+
			"  jail reads     %s\n"+
			"Both must be packload.CtxPath of the SAME string. The host loads the pack "+
			"under its config name and the jail under the staged directory, so the shared "+
			"key has to be the staged directory on both sides (Pack.StagedSlug), never "+
			"Pack.Name — which is the user's handle and is not escaped.",
			hostDest, jailSource)
	}

	// AND THE SAME PACK, LOADED THE HOST'S WAY, MUST RESOLVE THE SAME HOST LAYER. The
	// check above cannot see packload's own half of the derivation: the jail-loaded pack
	// has Name == staged dir, so hostSourceFor gives the same answer from either
	// expression for THAT pack. This is the one that separates them — the same staged tree,
	// loaded under the config name, must still resolve to the path the jail will open, or
	// the surface's host layer depends on which half loaded the pack.
	hostSurfaces, probs := host.Surfaces()
	if len(probs) != 0 || len(hostSurfaces) != 1 {
		t.Fatalf("host surfaces = %d (%v), want the one the fixture declares",
			len(hostSurfaces), probs)
	}
	if hostSurfaces[0].HostSource != jailSource {
		t.Errorf("the surface's host layer moves with the loader:\n"+
			"  loaded under the config name: %s\n"+
			"  loaded under the staged dir:  %s\n"+
			"packload.hostSourceFor must key on the staged directory, so one pack has one "+
			"host layer whichever half read it.",
			hostSurfaces[0].HostSource, jailSource)
	}
}
