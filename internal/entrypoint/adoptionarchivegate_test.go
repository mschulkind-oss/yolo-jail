package entrypoint

// adoptionarchivegate_test.go measures the two things OQ-CO7's gate has to be able to TELL
// APART, each of which it could not until 2026-09-12 and each of which cost the net
// (docs/design/config-ownership-and-promotion.md §6.3.3, defects D1 and D2):
//
//  1. "bytes present" vs "bytes YOLO WROTE". A first migration over a non-empty file is an
//     adoption, and the file is not necessarily the user's: a home yolo has already rendered,
//     whose baseline sidecar is then lost, hands the next render yolo's own output. Archiving
//     that spends the one-per-surface slot on a copy of the render and announces it as "the
//     file as yolo found it" — false in the one line OQ-RO3 will not let a report hide, and
//     the next GENUINE adoption gets nothing.
//  2. "absent" vs "present but UNREADABLE". Both arrived as zero bytes, so a file that EXISTS
//     and cannot be read took the absent path: no archive, no capture, and a wholesale
//     replacement of a file yolo never saw.
//
// Every test here drives the REAL boot entry (ConfigurePackSurfaces) over the fixtures in
// adoptionarchive_test.go, for that file's stated reason: a gate is exactly the shape that
// invites a test of the writer in isolation, which keeps passing after adoption stops calling
// it. The pair in each half is deliberate — a skip test alone is satisfied by a gate that
// skips everything, so each is written beside the case that must still be archived.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// jailBaselinePath is the capture baseline for the fixture surface, at the one place
// render.Target puts it for a jail.
func jailBaselinePath(workspace string) string {
	return filepath.Join(workspace, ".yolo", "prism", "acme-settings.last_render")
}

// AN ADOPTION OVER YOLO'S OWN RENDER ARCHIVES NOTHING (D1, the gate's half).
//
// The reachable shape: a home where yolo CREATED the file (so the first render archived
// nothing and the one-per-surface slot is still empty), whose baseline sidecar then goes —
// a restored workspace, a half-wiped .yolo tree, a hand-deleted sidecar. The next render has
// no trusted last_render over a non-empty file, which is a first migration, which is an
// adoption. Every byte of that file is yolo's, so there is nothing here to net, and taking a
// copy would burn the slot AND print a sentence that is not true.
//
// Driven by deleting the sidecar rather than by `yolo config reset`, which is the other,
// commoner route into this state and has its own half of the fix in cli.reseedResetBaseline —
// reset now keeps the baseline it just made true, so it never reaches this gate at all. This
// is the residue that fix cannot reach, and it is this gate's alone.
func TestJailAdoptionSkipsAFileYoloItselfWrote(t *testing.T) {
	e, path := jailAdoptionHome(t, "")
	pack := archiveJailPack(t)
	var errw bytes.Buffer
	e.Stderr = &errw

	ConfigurePackSurfaces(e, []*packload.Pack{pack}) // creates the file from the layers
	if fails := e.GenFailures(); len(fails) != 0 {
		t.Fatalf("first boot render failed: %v", fails)
	}
	rendered, err := os.ReadFile(path)
	if err != nil || len(rendered) == 0 {
		t.Fatalf("the first boot rendered no file at %s (%v)", path, err)
	}
	if err := os.Remove(jailBaselinePath(e.Workspace)); err != nil {
		t.Fatal(err)
	}
	errw.Reset()

	ConfigurePackSurfaces(e, []*packload.Pack{pack}) // adoption, over yolo's own bytes
	if fails := e.GenFailures(); len(fails) != 0 {
		t.Fatalf("second boot render failed: %v", fails)
	}

	root := filepath.Join(e.Workspace, ".yolo", "archive")
	if _, err := os.Stat(root); err == nil {
		got, _ := os.ReadFile(jailConfigArchive(e.Workspace, "acme", "settings", "settings.json"))
		t.Errorf("an adoption over yolo's OWN render took the one-per-surface archive:\n%s\n\n"+
			"The slot is spent forever, on a copy of the render rather than of the user's "+
			"file, so the next genuine adoption of this surface has no net at all. The gate "+
			"has to tell \"bytes present\" from \"bytes yolo wrote\" (OQ-CO7 D1).", got)
	}
	if strings.Contains(errw.String(), "the file as yolo found it") {
		t.Errorf("the boot announced an archive of \"the file as yolo found it\" for a file "+
			"yolo wrote itself:\n%s", errw.String())
	}
	// And the render still HAPPENED: skipping the archive must not have skipped the surface.
	if after, err := os.ReadFile(path); err != nil || string(after) != string(rendered) {
		t.Errorf("the second boot did not re-render the surface (err=%v):\n%s", err, after)
	}
}

// THE COMPLEMENT, and without it the test above is satisfied by a gate that skips everything:
// the same home, the same lost baseline, and ONE KEY of the user's added to the rendered file.
// That file is no longer yolo's output, so the copy is owed — and it is owed for exactly the
// class the archive exists for, since `permissions.ask` under a managed object is what
// adoption's narrowing can drop while the render reports nothing lost.
//
// It also pins that the skip is BYTE IDENTITY rather than "close enough": one added key is the
// smallest difference there is, and a gate that tolerated it would drop the net for every file
// whose user content happens to be small.
func TestJailAdoptionArchivesAFileYoloWroteAndTheUserThenEdited(t *testing.T) {
	e, path := jailAdoptionHome(t, "")
	pack := archiveJailPack(t)

	ConfigurePackSurfaces(e, []*packload.Pack{pack})
	if fails := e.GenFailures(); len(fails) != 0 {
		t.Fatalf("first boot render failed: %v", fails)
	}
	rendered, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// The user's own edit on top of yolo's render: every key yolo just wrote, plus one of
	// theirs, re-emitted compactly — their formatting, not yolo's.
	var doc map[string]any
	if err := json.Unmarshal(rendered, &doc); err != nil {
		t.Fatalf("the fixture render is not JSON: %v\n%s", err, rendered)
	}
	doc["apiKeyHelper"] = "/usr/local/bin/acme-key.sh"
	edited, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, edited, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(jailBaselinePath(e.Workspace)); err != nil {
		t.Fatal(err)
	}

	ConfigurePackSurfaces(e, []*packload.Pack{pack})
	if fails := e.GenFailures(); len(fails) != 0 {
		t.Fatalf("second boot render failed: %v", fails)
	}

	want := jailConfigArchive(e.Workspace, "acme", "settings", "settings.json")
	got, err := os.ReadFile(want)
	if err != nil {
		t.Fatalf("a file carrying the user's own key was adopted with no archive at %s: %v\n\n"+
			"The gate skips a file that IS yolo's render; one the user has edited is not, and "+
			"a gate that cannot tell them apart deletes the net rather than the false copy.",
			want, err)
	}
	if string(got) != string(edited) {
		t.Errorf("the archive holds %q, want the edited file as yolo found it (%q)", got, edited)
	}
}

// A SURFACE YOLO CANNOT READ IS REFUSED, NOT REPLACED (D2).
//
// `current, _ := os.ReadFile(surfacePath)` made "the file is not there" and "the file is there
// and I cannot read it" one answer: zero bytes. So the render composed from nothing, the
// archive gate saw no bytes and took no copy, and the writer replaced a file that EXISTS —
// chmodding it writable first if it had to (WriteInPlace does). That is the unparseable case's
// total loss one errno away, with the one thing that makes it survivable missing.
//
// The obstruction is a DIRECTORY at the surface path, for the reason
// TestOwnAdoptionRefusesWhenTheArchiveCannotBeWritten gives about its own fixture and
// packrootunreadable_test.go about the same class: this suite runs as root in the jail, so a
// mode bit is not an obstruction, while EISDIR is one for every uid. ⚠ It therefore pins the
// REFUSAL rather than the survival of bytes — the EACCES twin, where the read fails and the
// write would have succeeded, is the sharper loss and cannot be written as a root-safe test.
// The refusal is what makes both cases safe, and the message is the difference between this
// and an opaque failure from the writer one line later.
func TestJailAdoptionRefusesASurfaceItCannotRead(t *testing.T) {
	e, path := jailAdoptionHome(t, "")
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	canary := filepath.Join(path, "canary")
	if err := os.WriteFile(canary, []byte("still here\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var errw bytes.Buffer
	e.Stderr = &errw

	ConfigurePackSurfaces(e, []*packload.Pack{archiveJailPack(t)})

	fails := fmt.Sprint(e.GenFailures())
	if len(e.GenFailures()) == 0 {
		t.Fatalf("a surface yolo could not read rendered as if the file were absent — no "+
			"refusal, no archive, and the file replaced wholesale.\nboot said:\n%s", errw.String())
	}
	for _, want := range []string{"refused", "cannot read", "left untouched", path} {
		if !strings.Contains(fails, want) {
			t.Errorf("the failure does not say %q — an unreadable surface has to be reported "+
				"as a file yolo DECLINED to touch, not as an I/O error from the writer:\n%s",
				want, fails)
		}
	}
	if _, err := os.ReadFile(canary); err != nil {
		t.Errorf("the render wrote over the surface it could not read: %v", err)
	}
	// Nothing was persisted for it either: a refusal at the read is a refusal for the whole
	// surface, so no baseline is left claiming yolo wrote a file it never composed.
	if _, err := os.Stat(jailBaselinePath(e.Workspace)); err == nil {
		t.Errorf("a refused surface still got a last_render baseline — the next boot would " +
			"read it as a steady state over a file yolo has never seen")
	}
}
