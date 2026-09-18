package cli

// configprovenance_test.go pins where per-key provenance now lives, and that it is no longer
// anywhere else (docs/reference/config-target-resolution.md [OQ-CR7], ruled (a) against its own
// leaning).

import (
	"bytes"
	"strings"
	"testing"
)

// ONE VERB, ONE SUBJECT, ONE HOME — asserted as a MOVE rather than as a removal, because
// either half alone is a weaker statement than the ruling. That `diff` stopped printing the
// block would be satisfied by deleting the report; that `ls` prints it would be satisfied by
// printing it twice.
//
// The fixture is a REAL contribution rather than a seeded record: `yolo config promote` writes
// the key into the conventional local pack, which is then a `config-overlay` from `local` on a
// surface `claude` owns — the shape the report exists for. A hand-seeded provenance file would
// pass with the pack decoder refusing the manifest, which is the silent non-delivery promote
// itself exists to end.
//
// ⚠ AND IT HAS TO BE AN AGENT THE EMBEDDED MANIFEST KNOWS. The first version of this test used
// the `acme` fixture packs and was VACUOUS: `capturedSurfaces` reads surfaceManifest(), which
// is embedded packs only, so `diff acme` refuses for want of a capture surface before it could
// have printed anything. It passed with the block put back.
func TestPerKeyProvenanceMovedFromDiffToLs(t *testing.T) {
	w := newPromoteWorld(t, `["claude"]`)
	w.capture("claude", "settings", `{"autoMemoryEnabled":true}`, `{}`)
	if _, errw, rc := w.run("claude", "--accept-promotion"); rc != 0 {
		t.Fatalf("promote rc=%d: %s", rc, errw)
	}

	var lsOut, lsErr bytes.Buffer
	if rc := configRunW([]string{"ls", "--all"}, &lsOut, &lsErr); rc != 0 {
		t.Fatalf("config ls rc=%d: %s", rc, lsErr.String())
	}
	if !strings.Contains(lsOut.String(), "config-overlay from local") {
		t.Fatalf("`config ls` does not report the contribution, so this test cannot tell the "+
			"block MOVED from a block that was deleted:\n%s", lsOut.String())
	}

	var diffOut, diffErr bytes.Buffer
	if rc := configRunW([]string{"diff", "claude"}, &diffOut, &diffErr); rc != 0 {
		t.Fatalf("config diff rc=%d: %s%s", rc, diffOut.String(), diffErr.String())
	}
	for _, leaked := range []string{"config-overlay from", "autoMemoryEnabled"} {
		if strings.Contains(diffOut.String(), leaked) {
			t.Errorf("`config diff` still reports per-key provenance (%q). It answers *which "+
				"layer set this key*, a fact about the RENDER — it would read identically "+
				"before and after the wipe this verb measures against, so it cannot justify "+
				"its place here:\n%s", leaked, diffOut.String())
		}
	}
}
