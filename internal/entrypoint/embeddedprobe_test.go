package entrypoint

// embeddedprobe_test.go pins that the non-boot pack entry points here (ConfigurePackByName
// and the two `yolo check` probes beside it) read the process's ONE materialized embedded
// tree instead of extracting copies of their own.
//
// They used to share a private `yolo-embedded-packs-` temp dir that nothing removed, and
// two of the three re-ran MaterializeEmbedded on EVERY call — so one `yolo check` extracted
// the ~30-file tree three times and left the directory behind. Cheap to reintroduce
// (MaterializeEmbedded takes a dest, so a new dest looks like ordinary use) and invisible
// once introduced, which is why it is asserted rather than commented.

import (
	"os"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

func TestEmbeddedPackProbesReuseTheProcessTree(t *testing.T) {
	t.Cleanup(packload.ReleaseEmbedded)
	// Materialized BEFORE TMPDIR is redirected, so the observed dir stays empty unless a
	// probe below extracts a tree of its own.
	if len(packload.Embedded()) == 0 {
		t.Fatalf("Embedded() is empty: %v", packload.EmbeddedProblems())
	}

	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)

	names := EmbeddedPackNames()
	if len(names) == 0 {
		t.Fatal("EmbeddedPackNames() is empty; `yolo check`'s dry run would render nothing " +
			"and the assertion below would pass vacuously")
	}
	// Twice, plus the surface probe: the old shape re-extracted per call, so a single call
	// would not distinguish "one tree per process" from "one tree per call site".
	if second := EmbeddedPackNames(); len(second) != len(names) {
		t.Fatalf("EmbeddedPackNames() returned %d names then %d", len(names), len(second))
	}
	if surfaces := EmbeddedPackSurfaces(NewEnv(map[string]string{
		"JAIL_HOME": tmp + "/home",
		"HOME":      tmp + "/home",
	})); len(surfaces) == 0 {
		t.Fatal("EmbeddedPackSurfaces() is empty; the parse half of `yolo check`'s dry run " +
			"would check nothing")
	}

	entries, err := os.ReadDir(tmp)
	if err != nil {
		t.Fatalf("reading %s: %v", tmp, err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "yolo-") {
			t.Errorf("a pack probe extracted its own tree (%s); these entry points must "+
				"share the one packload.Embedded materialized for the process", e.Name())
		}
	}
}
