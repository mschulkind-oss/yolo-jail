package entrypoint

// packviaskew_test.go is the BOOT half of program-delivery.md §6.2 / R6, the twin of
// packskew_test.go one level down: not "DecodeTolerant skips an unknown `via`"
// (packdecl/skewvia_test.go pins that) and not "LoadDir reports it" (packload does) but
// "a jail BOOTS with such a pack staged, says so on the way past, and writes no launcher
// for the program it could not deliver".
//
// LoadJailPacks is the boot decision — the function that turns a manifest problem into
// "refusing to start the jail" (A12) — and GenerateAgentLaunchers is the consumer that
// would otherwise have to invent an answer for a `via` it does not know. A test one layer
// down can see neither, and this one fails outright if the tolerance rule is removed: the
// unknown `via` goes back to being a validation problem, LoadJailPacks returns an error,
// and both halves below stop before they assert anything.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// stageUnknownViaPack writes a pack root holding one pack that declares a program via a
// delivery mechanism no build knows, BESIDE one this build delivers — so the tests can tell
// "the unknown one was dropped" from "the launcher loop never ran".
func stageUnknownViaPack(t *testing.T) (root string) {
	t.Helper()
	root = filepath.Join(t.TempDir(), "packs")
	dir := filepath.Join(root, "acme")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name":"acme","contributes":[
		{"kind":"program","bin":"ruff","via":"uv","package":"ruff"},
		{"kind":"program","bin":"tool","via":"npm","package":"tool"}]}`
	if err := os.WriteFile(filepath.Join(dir, packdecl.ManifestName), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// TestLoadJailPacksBootsWithAnUnknownProgramVia: the boot decision. A staged pack declaring
// `via: "uv"` must load — no error, so the entrypoint does not refuse to start — with the
// skip warned by name and the pack's deliverable program untouched.
func TestLoadJailPacksBootsWithAnUnknownProgramVia(t *testing.T) {
	root := stageUnknownViaPack(t)
	var stderr bytes.Buffer
	e := NewEnv(map[string]string{"JAIL_HOME": t.TempDir(), "YOLO_PACK_ROOT": root})
	e.Stderr = &stderr

	packs, err := LoadJailPacks(e)
	if err != nil {
		t.Fatalf("an unknown program `via` must not fail the boot (§6.2 / R6): %v", err)
	}
	if len(packs) != 1 {
		t.Fatalf("want the staged pack loaded, got %d packs", len(packs))
	}
	installs := packs[0].Decl.InstallContributions()
	if len(installs) != 1 || installs[0].Bin != "tool" {
		t.Errorf("the unknown-via program must be dropped and its deliverable sibling kept: %+v",
			installs)
	}

	// The skip must be audible at boot: a silently dropped contribution is what the
	// SkewNotes contract forbids, and a warning nobody asserts on is six lines a refactor
	// drops with the suite still green.
	out := stderr.String()
	for _, want := range []string{"pack acme", `"uv"`, `"ruff"`} {
		if !strings.Contains(out, want) {
			t.Errorf("the boot must warn, naming %s; stderr was %q", want, out)
		}
	}
}

// TestGenerateAgentLaunchersWritesNoLauncherForAnUnknownVia is the consumer half, and the
// reason the drop belongs at the DECODER rather than at the launcher loop.
//
// GenerateAgentLaunchers has no template for a mechanism it does not know, so its only
// options are to invent one or to skip. It skips — but by the time it runs, the tolerant
// decode has already removed the contribution, which is what makes "no launcher for ruff"
// a decided outcome rather than a `default:` falling through. The negative assertion on
// stderr is what distinguishes the two.
func TestGenerateAgentLaunchersWritesNoLauncherForAnUnknownVia(t *testing.T) {
	home := t.TempDir()
	root := stageUnknownViaPack(t)
	var stderr bytes.Buffer
	e := NewEnv(map[string]string{"JAIL_HOME": home, "YOLO_PACK_ROOT": root})
	e.Stderr = &stderr

	if err := GenerateAgentLaunchers(e); err != nil {
		t.Fatalf("an unknown program `via` must not fail launcher generation: %v", err)
	}
	if _, err := os.Stat(filepath.Join(e.LauncherDir(), "ruff")); !os.IsNotExist(err) {
		t.Errorf("a program whose `via` this build cannot deliver must get NO launcher — a "+
			"launcher that installs nothing is a name on PATH that fails at use (err=%v)", err)
	}
	// The sibling proves the loop ran at all, so the absence above is a decision rather
	// than a generator that gave up on the whole pack.
	if _, err := os.Stat(filepath.Join(e.LauncherDir(), "tool")); err != nil {
		t.Errorf("the deliverable sibling must still get its launcher: %v", err)
	}
	// And it was dropped by the DECODER: the launcher loop's own defense-in-depth warn
	// (shims.go's `default:`) is unreachable from the boot path, so it must not have fired.
	if out := stderr.String(); strings.Contains(out, "unknown install kind") {
		t.Errorf("the unknown `via` must be dropped by DecodeTolerant before the launcher "+
			"loop sees it — the loop's fallback warn is defense-in-depth only: %q", out)
	}
}
