package check

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
)

// `yolo check --accept-config-changes` records the scope part exactly where a launch would
// start a brokered loophole, from the same remotes (BB-D30).
func TestCheckReadsTheScopeWhereALaunchWouldStartABroker(t *testing.T) {
	mod := filepath.Join(t.TempDir(), "gb")
	if err := os.MkdirAll(mod, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name": "gb", "transport": "loopback-tls",
	  "lifecycle": "spawned",
	  "host_daemon": {"cmd": ["/bin/true", "{socket}", "{repository_scope}"], "publishes": "socket"},
	  "brokered": {"source": "gbsrc", "remote_host": "github.com"}}`
	if err := os.WriteFile(filepath.Join(mod, "manifest.jsonc"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	restore := loopholes.SnapshotPackModules()
	t.Cleanup(restore)
	loopholes.SetPackModules([]loopholes.PackModule{{Dir: mod, HostExecApproved: true}})

	ws := t.TempDir()
	if err := os.MkdirAll(filepath.Join(ws, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, ".git", "config"),
		[]byte("[remote \"origin\"]\n\turl = https://github.com/o/r\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	s := brokeredScopeForCheck(ws, gbSwitched(true), "podman")
	if s == nil || len(s.Sources) != 1 || s.Sources[0].Source != "gbsrc" || s.Sources[0].Label != "gb" {
		t.Fatalf("scope %+v", s)
	}
	if got := s.Sources[0].Read.Repos(); len(got) != 1 || got[0] != "o/r" {
		t.Fatalf("repos %v", got)
	}
	if brokeredScopeForCheck(ws, gbSwitched(true), "container") != nil {
		t.Fatal("Apple Container starts no broker, so the check must record no scope there")
	}
	if brokeredScopeForCheck(ws, gbSwitched(false), "podman") != nil {
		t.Fatal("a disabled brokered loophole starts no broker, so its scope is not in play")
	}
	if brokeredScopeForCheck(ws, jsonx.NewOrderedMap(), "podman") != nil {
		t.Fatal("a brokered loophole no switch turned on starts no broker: it ships off")
	}
}

// gbSwitched is the merged config of a workspace whose per-workspace file switches gb, the only
// switch a brokered loophole has (docs/design/boundary-broker.md OQ-BB13).
func gbSwitched(on bool) *jsonx.OrderedMap {
	gb := jsonx.NewOrderedMap()
	gb.Set("enabled", on)
	lp := jsonx.NewOrderedMap()
	lp.Set("gb", gb)
	cfg := jsonx.NewOrderedMap()
	cfg.Set("loopholes", lp)
	return cfg
}
