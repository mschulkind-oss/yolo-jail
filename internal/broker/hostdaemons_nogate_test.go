package broker

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
)

// A host-wide daemon whose pack module nothing in this process vouches for is refused a
// spawn, and the refusal says what is true.
//
// It used to say "its pack's host access is not approved on this machine (`yolo pack
// install` records the approval)". OQ-TP9 (docs/design/trust-paths.md) deleted that
// approval, so the remedy it named does nothing: `yolo pack install` records no approval,
// and no command does. Every module pack resolution records passes the origin gate, so the
// branch is reachable only from a Set built without resolving packs — a yolo bug, which is
// what the reason now says.
func TestAnUnvouchedHostDaemonNamesNoApprovalRemedy(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "yjtest-unvouched")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(hostScopedManifest("yjtest-unvouched"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.jsonc"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	restore := loopholes.SnapshotPackModules()
	loopholes.SetPackModules([]loopholes.PackModule{{Dir: dir, HostExecApproved: false}})
	t.Cleanup(restore)

	var found *Singleton
	for _, s := range DeclaredSingletons("") {
		if s.Name == "yjtest-unvouched" {
			c := s
			found = &c
		}
	}
	if found == nil {
		t.Fatal("the fixture's host-scoped daemon is not in the derived set")
	}
	if len(found.Argv) != 0 {
		t.Fatalf("an unvouched pack module was given a spawn argv %v", found.Argv)
	}
	if !strings.Contains(found.NoSpawn, "nothing vouches for the module") {
		t.Errorf("the refusal does not say why: %q", found.NoSpawn)
	}
	for _, stale := range []string{"approv", "pack install"} {
		if strings.Contains(found.NoSpawn, stale) {
			t.Errorf("the refusal names the deleted approval (%q): %q", stale, found.NoSpawn)
		}
	}
}
