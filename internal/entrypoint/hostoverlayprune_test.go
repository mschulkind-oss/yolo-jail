package entrypoint

// hostoverlayprune_test.go pins the MECHANISM half of the config-overlay key prune: what
// PruneHostOverlayKeys reads, what it refuses, and what it writes. The command-level behavior
// (the shared confirmation, the observe report, the whole drop lifecycle) is
// internal/cli/applyhostoverlaykeys_test.go's.
//
// Every test uses a t.TempDir() home. The real $HOME is never read or written.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// overlayPruneHome builds a home holding one claude/settings-shaped file plus a provenance
// record, and returns the home. The record is CONSTRUCTED rather than produced by a render: the
// states worth testing here (a `host` key, a stale attribution) are exactly the ones no apply
// sequence can be relied on to reach, and building them directly makes the test about the rule
// rather than about a two-apply setup.
func overlayPruneHome(t *testing.T, settings, record string) string {
	t.Helper()
	home := t.TempDir()
	writeHostFile(t, filepath.Join(home, ".acme", "settings.json"), settings)
	writeHostFile(t, filepath.Join(home, ".local", "share", "yolo-jail", "host-provenance",
		"acme-settings.provenance"), record)
	return home
}

func writeHostFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// overlayPrunePack is a pack OWNING the acme/settings surface — the destination a prune has to
// know about. It declares no overlay of its own: the orphaned key's contributor is gone by
// definition, so the surface is all the candidate set has to supply.
func overlayPrunePack(t *testing.T) *packload.Pack {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "acme")
	writeHostFile(t, filepath.Join(dir, "pack.json"),
		`{"name":"acme","description":"d","contributes":[
		  {"kind":"config","config":[{"agent":"acme","name":"settings","codec":"json",
		   "path":"~/.acme/settings.json","mode":"rmw","managed":{"telemetry":false}}]}]}`)
	writeHostFile(t, filepath.Join(dir, "AGENTS.md"), "prose\n")
	p, problems := packload.LoadDir(dir, "acme")
	if p == nil {
		t.Fatalf("the fixture pack did not load: %v", problems)
	}
	return p
}

func settingsBytes(t *testing.T, home string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(home, ".acme", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// A NIL active set is REFUSED, not read as "no pack is configured". The caller always builds a
// non-nil map, so this is reachable only through a future bug — and that bug would strip every
// pack-contributed key from the user's config files, which is why it is guarded rather than
// merely avoided. Same posture PruneHostBriefings takes, for the same reason.
func TestPruneHostOverlayKeysRefusesNilActiveSet(t *testing.T) {
	home := overlayPruneHome(t, `{"mine":1,"theirs":2}`, "theirs\tconfig-overlay:dropme\n")
	before := settingsBytes(t, home)

	out, err := PruneHostOverlayKeys([]*packload.Pack{overlayPrunePack(t)}, nil, nil, home, false)
	if err == nil {
		t.Fatal("a nil active set must be refused, not treated as 'no pack is active'")
	}
	if len(out) != 0 {
		t.Errorf("a refused prune reported %d orphan(s)", len(out))
	}
	if after := settingsBytes(t, home); after != before {
		t.Errorf("a refused prune wrote to the file:\n%s", after)
	}
}

// An EMPTY non-nil active set is the honest "no packs configured", and it PRUNES. This is the
// distinction the nil guard exists to preserve: emptying `packs` is the most complete drop
// there is, so it must not be the one case that cleans up nothing.
func TestPruneHostOverlayKeysPrunesWithAnEmptyActiveSet(t *testing.T) {
	home := overlayPruneHome(t, `{"mine":1,"theirs":2}`,
		"mine\thost\ntheirs\tconfig-overlay:dropme\n")

	out, err := PruneHostOverlayKeys([]*packload.Pack{overlayPrunePack(t)},
		map[string]bool{}, nil, home, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out[0].Key != "theirs" || out[0].Pack != "dropme" {
		t.Fatalf("want one orphan theirs/dropme, got %+v", out)
	}
	after := settingsBytes(t, home)
	if strings.Contains(after, "theirs") {
		t.Errorf("the orphaned key survived an empty active set:\n%s", after)
	}
	if !strings.Contains(after, "mine") {
		t.Errorf("the user's own key was removed:\n%s", after)
	}
}

// OBSERVE reports and writes NOTHING — neither the surface nor the provenance record. A dry run
// that consumed the record would make the confirmation it is supposed to inform impossible.
func TestPruneHostOverlayKeysObserveWritesNothing(t *testing.T) {
	home := overlayPruneHome(t, `{"mine":1,"theirs":2}`, "theirs\tconfig-overlay:dropme\n")
	before := settingsBytes(t, home)
	recPath := filepath.Join(home, ".local", "share", "yolo-jail", "host-provenance",
		"acme-settings.provenance")
	recBefore, err := os.ReadFile(recPath)
	if err != nil {
		t.Fatal(err)
	}

	out, err := PruneHostOverlayKeys([]*packload.Pack{overlayPrunePack(t)},
		map[string]bool{}, nil, home, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out[0].Action != "would remove" {
		t.Fatalf("observe must report a would-remove, got %+v", out)
	}
	if after := settingsBytes(t, home); after != before {
		t.Errorf("observe wrote to the surface:\n%s", after)
	}
	recAfter, err := os.ReadFile(recPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(recAfter) != string(recBefore) {
		t.Errorf("observe rewrote the provenance record:\n%s", recAfter)
	}
}

// Only a CONFIG-OVERLAY attribution is eligible. Every other layer names something that is not
// a dropped pack's contribution — `host` is the user's, `defaults` is fill-if-absent (theirs
// once written), and managed/computed belong to the surface's OWNER, which is the other axis.
func TestPruneHostOverlayKeysOnlyTouchesOverlayAttributions(t *testing.T) {
	for _, layer := range []string{"host", "defaults", "managed", "computed",
		"retired:managed", "retired:computed", "config-overlay:", "retired:", "retired:host"} {
		t.Run(layer, func(t *testing.T) {
			home := overlayPruneHome(t, `{"theirs":2}`, "theirs\t"+layer+"\n")
			out, err := PruneHostOverlayKeys([]*packload.Pack{overlayPrunePack(t)},
				map[string]bool{}, nil, home, false)
			if err != nil {
				t.Fatal(err)
			}
			if len(out) != 0 {
				t.Errorf("layer %q must not be retirable, got %+v", layer, out)
			}
			if !strings.Contains(settingsBytes(t, home), "theirs") {
				t.Errorf("layer %q had its key removed:\n%s", layer, settingsBytes(t, home))
			}
		})
	}
}

// A key the RECORD names but the FILE no longer has is not reported. Removing nothing and
// claiming to have removed something is a phantom line, and it would keep appearing on every
// apply — the same reason removeHostBriefingBlockAt returns nil for a block that was not there.
func TestPruneHostOverlayKeysIgnoresAKeyAlreadyGoneFromTheFile(t *testing.T) {
	home := overlayPruneHome(t, `{"mine":1}`, "theirs\tconfig-overlay:dropme\n")

	out, err := PruneHostOverlayKeys([]*packload.Pack{overlayPrunePack(t)},
		map[string]bool{}, nil, home, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 0 {
		t.Errorf("a key the file does not have must not be reported as removed: %+v", out)
	}
}

// An UNPARSEABLE surface file is left byte-for-byte alone. RMW means "preserve everything yolo
// does not declare"; a read that cannot see the user's keys cannot honor that, so removing one
// key from a file yolo cannot parse is not an option. The render loop refuses the same file
// loudly, so the user still hears about it once.
func TestPruneHostOverlayKeysLeavesAnUnparseableFileAlone(t *testing.T) {
	home := overlayPruneHome(t, "{not json at all", "theirs\tconfig-overlay:dropme\n")
	before := settingsBytes(t, home)

	out, err := PruneHostOverlayKeys([]*packload.Pack{overlayPrunePack(t)},
		map[string]bool{}, nil, home, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 0 {
		t.Errorf("an unreadable file yields no orphans, got %+v", out)
	}
	if after := settingsBytes(t, home); after != before {
		t.Errorf("an unparseable file was rewritten:\n%s", after)
	}
}

// The record is UPDATED to stop naming a removed key, so the next prune does not find an orphan
// that no longer exists — and the keys it still describes are untouched.
func TestPruneHostOverlayKeysUpdatesTheProvenanceRecord(t *testing.T) {
	home := overlayPruneHome(t, `{"mine":1,"theirs":2}`,
		"mine\thost\ntheirs\tconfig-overlay:dropme\n")

	if _, err := PruneHostOverlayKeys([]*packload.Pack{overlayPrunePack(t)},
		map[string]bool{}, nil, home, false); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(home, ".local", "share", "yolo-jail",
		"host-provenance", "acme-settings.provenance"))
	if err != nil {
		t.Fatalf("the record must still exist — absent means 'never rendered here': %v", err)
	}
	if strings.Contains(string(data), "theirs") {
		t.Errorf("the record still attributes the removed key:\n%s", data)
	}
	if !strings.Contains(string(data), "mine\thost") {
		t.Errorf("an unrelated attribution was lost:\n%s", data)
	}
}

// A pack STILL configured keeps its keys, whatever else changed. This is the eligibility rule
// from the other side: the pass keys on the active set, not on "is this key still declared".
func TestPruneHostOverlayKeysKeepsAnActivePacksKeys(t *testing.T) {
	home := overlayPruneHome(t, `{"theirs":2}`, "theirs\tconfig-overlay:stillhere\n")

	out, err := PruneHostOverlayKeys([]*packload.Pack{overlayPrunePack(t)},
		map[string]bool{"stillhere": true}, nil, home, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 0 {
		t.Errorf("a configured pack's key must not be retired: %+v", out)
	}
	if !strings.Contains(settingsBytes(t, home), "theirs") {
		t.Errorf("a configured pack's key was removed:\n%s", settingsBytes(t, home))
	}
}
