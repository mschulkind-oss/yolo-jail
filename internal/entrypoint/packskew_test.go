package entrypoint

// packskew_test.go pins the OTHER half of loophole-packaging §3.3a: not "LoadDir skips an
// unknown kind" (packload/skewkind_test.go pins that) but "a jail BOOTS with such a pack
// staged, and says so on the way past".
//
// The requirement is literally worded as a boot property — "a regression test that a
// manifest carrying an unknown kind still BOOTS A JAIL" — and LoadJailPacks is the boot
// decision: it is the function that turns a manifest problem into "refusing to start the
// jail" (A12). A test one layer down cannot see that, because LoadJailPacks is also free
// to re-refuse what LoadDir tolerated.
//
// The warn loop is pinned in the same test on purpose. Skipping a contribution silently is
// forbidden by §3.3a and by the SkewNotes contract ("never silent either"), and a warning
// nobody asserts on is exactly the kind of six lines a later refactor drops with every
// suite still green.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// stageUnknownKindPack writes a pack root holding one pack whose middle contribution names
// a kind no build knows, flanked by two valid siblings.
func stageUnknownKindPack(t *testing.T) (root string) {
	t.Helper()
	root = filepath.Join(t.TempDir(), "packs")
	dir := filepath.Join(root, "acme")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name":"acme","contributes":[
		{"kind":"skills","from":"skills","into":".acme/skills"},
		{"kind":"kind-from-a-newer-yolo","from":"x"},
		{"kind":"env","vars":{"ACME":"1"}}]}`
	if err := os.WriteFile(filepath.Join(dir, packdecl.ManifestName), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// TestLoadJailPacksBootsWithAnUnknownContributionKind: the boot decision. A staged pack
// declaring a kind this build does not know must load — no error, so the entrypoint does
// not refuse to start — and the pack must still carry its valid contributions.
//
// Before the tolerance change this returned `pack acme: contributes[1]: unknown kind …`,
// which bricked every jail running a pre-`just load` image the moment a newer host staged
// a manifest using a newer kind. That is the `tier` incident's shape, third occurrence.
func TestLoadJailPacksBootsWithAnUnknownContributionKind(t *testing.T) {
	root := stageUnknownKindPack(t)
	var stderr bytes.Buffer
	e := NewEnv(map[string]string{"JAIL_HOME": t.TempDir(), "YOLO_PACK_ROOT": root})
	e.Stderr = &stderr

	packs, err := LoadJailPacks(e)
	if err != nil {
		t.Fatalf("an unknown contribution kind must not fail the boot (§3.3a): %v", err)
	}
	if len(packs) != 1 {
		t.Fatalf("want the staged pack loaded, got %d packs", len(packs))
	}
	if env := packs[0].Decl.EnvContributions(); env["ACME"] != "1" {
		t.Errorf("the valid sibling AFTER the skipped kind must survive the boot load: %v", env)
	}

	// The skip must be audible at boot. This is the half no other test covers: LoadJailPacks
	// owns the warn, and a silent skip is what §3.3a forbids.
	out := stderr.String()
	for _, want := range []string{"pack acme", `"kind-from-a-newer-yolo"`} {
		if !strings.Contains(out, want) {
			t.Errorf("the boot must warn, naming %s; stderr was %q", want, out)
		}
	}
}

// TestSkewNotesAreStatedOnceAcrossTheBootsRepeatedPackLoads: one skipped contribution is one
// finding, however many times the boot re-reads the tree that carries it.
//
// LoadJailPacks is called FIVE times in a single boot — pack surfaces, `requires`, the agent
// launchers, the bootstrap, and (since the orphan catalog landed) the catalog — and each
// pass re-derives the same SkewNotes from the same manifests. Unguarded, one pack declaring
// one unknown kind printed five identical lines on the way past, which reads as five
// problems and teaches the reader to skim exactly the warning that exists to be read.
func TestSkewNotesAreStatedOnceAcrossTheBootsRepeatedPackLoads(t *testing.T) {
	root := stageUnknownKindPack(t)
	var stderr bytes.Buffer
	e := NewEnv(map[string]string{"JAIL_HOME": t.TempDir(), "YOLO_PACK_ROOT": root})
	e.Stderr = &stderr

	// The boot's five readers, in one Env, exactly as Main has them.
	for i := 0; i < 5; i++ {
		if _, err := LoadJailPacks(e); err != nil {
			t.Fatalf("load %d: %v", i, err)
		}
	}

	if n := strings.Count(stderr.String(), "kind-from-a-newer-yolo"); n != 1 {
		t.Errorf("the skew note was printed %d times; one skipped contribution is one "+
			"finding:\n%s", n, stderr.String())
	}

	// The guard is per-Env, not a package-level latch. A second Env is a second boot's
	// worth of reporting (and, in-process, every other test), and silencing it would trade
	// five copies of a finding for none.
	var second bytes.Buffer
	e2 := NewEnv(map[string]string{"JAIL_HOME": t.TempDir(), "YOLO_PACK_ROOT": root})
	e2.Stderr = &second
	if _, err := LoadJailPacks(e2); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(second.String(), "kind-from-a-newer-yolo") {
		t.Errorf("a fresh Env must still report the skew: %q", second.String())
	}
}

// TestLoadJailPacksSaysNothingWhenNoKindWasSkipped: the warn loop must be driven by real
// skew, not fire on every boot — an unconditional notice trains readers to ignore it.
func TestLoadJailPacksSaysNothingWhenNoKindWasSkipped(t *testing.T) {
	root := filepath.Join(t.TempDir(), "packs")
	dir := filepath.Join(root, "acme")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name":"acme","contributes":[{"kind":"env","vars":{"ACME":"1"}}]}`
	if err := os.WriteFile(filepath.Join(dir, packdecl.ManifestName), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}

	var stderr bytes.Buffer
	e := NewEnv(map[string]string{"JAIL_HOME": t.TempDir(), "YOLO_PACK_ROOT": root})
	e.Stderr = &stderr
	if _, err := LoadJailPacks(e); err != nil {
		t.Fatalf("a fully-understood manifest must load: %v", err)
	}
	if out := stderr.String(); strings.Contains(out, "kind") {
		t.Errorf("no kind was skipped, so the boot must not mention one: %q", out)
	}
}
