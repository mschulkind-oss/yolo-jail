package entrypoint

// probedsurfaces_test.go pins the contract between the render and `yolo check`'s dry run
// (internal/cli/check/entrypoint.go): the preflight renders every embedded pack with
// ConfigurePackByName, then reads back every surface EmbeddedPackSurfaces does not mark
// Unrendered. A surface that the render legitimately skipped must be marked, or the dry run
// reports a missing file for a surface behaving exactly as declared.
//
// It failed in the in-jail `yolo check` when pi/subagents-mcp (a `whenListed` surface) was
// added: with pi-subagents absent from pi's packages the render wrote nothing, but the probe
// still listed the file, and the preflight failed on `open …/.config/mcp/mcp.json`.

import (
	"os"
	"testing"
)

func TestEveryProbedSurfaceIsOneTheRenderWrote(t *testing.T) {
	// pi/settings reads a host layer; in a jail /ctx/host-pi holds the real host's
	// settings.json, whose packages could select pi-subagents and hide the skip.
	t.Setenv("YOLO_CTX_ROOT", t.TempDir())
	home := t.TempDir()
	e := NewEnv(map[string]string{
		"JAIL_HOME":      home,
		"HOME":           home,
		"YOLO_WORKSPACE": t.TempDir(),
	})
	for _, name := range EmbeddedPackNames() {
		if err := ConfigurePackByName(e, name); err != nil {
			t.Fatalf("ConfigurePackByName(%s): %v", name, err)
		}
	}
	probed := 0
	for _, sf := range EmbeddedPackSurfaces(e) {
		if sf.Unrendered {
			continue
		}
		probed++
		if _, err := os.Stat(sf.Path); err != nil {
			t.Errorf("%s: the probe lists it as rendered, but the render wrote nothing: %v",
				sf.Label, err)
		}
	}
	if probed == 0 {
		t.Fatal("no surface was probed; the assertion above passed vacuously")
	}
}
