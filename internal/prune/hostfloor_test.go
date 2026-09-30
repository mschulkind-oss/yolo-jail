package prune

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// TestYoloPruneReclaimsAnInterruptedHostFloorInstallAndNothingElse pins the `yolo prune` call
// site: an install directory with no completion marker is reported on a line of its own and
// removed under --apply, while a COMPLETE install beside it — the agent `yolo host` runs — is
// never touched. Delete the PruneHostFloor call in Run and this fails.
func TestYoloPruneReclaimsAnInterruptedHostFloorInstallAndNothingElse(t *testing.T) {
	o, gs := baseOpts(t)
	programs := filepath.Join(gs, hostFloorLeaf, "programs", "claude")
	torn := filepath.Join(programs, "20260929T120000.000000000Z-111")
	whole := filepath.Join(programs, "20260929T110000.000000000Z-222")
	must(t, os.MkdirAll(torn, 0o700))
	must(t, os.WriteFile(filepath.Join(torn, "half-written"), []byte("12345"), 0o600))
	must(t, os.MkdirAll(whole, 0o700))
	must(t, os.WriteFile(filepath.Join(whole, ".yolo-floor-complete"), nil, 0o600))

	var buf bytes.Buffer
	o.Out = &buf
	Run(o)
	want := "  would remove: " + FmtBytes(5) + " across 1 interrupted install(s) in host-floor/"
	if !hasLine(&buf, want) {
		t.Errorf("missing %q in:\n%s", want, buf.String())
	}
	if _, err := os.Stat(torn); err != nil {
		t.Fatal("the dry run removed the interrupted install")
	}
	o.Apply = true
	buf.Reset()
	Run(o)
	if _, err := os.Stat(torn); !os.IsNotExist(err) {
		t.Errorf("--apply left the interrupted install in place:\n%s", buf.String())
	}
	if _, err := os.Stat(whole); err != nil {
		t.Errorf("prune removed a complete floor install: %v", err)
	}
}
