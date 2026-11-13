package check

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
)

// `yolo check --accept-config-changes` records the scope part exactly where a launch would
// start a brokered loophole, from the same remotes (BB-D30): wherever its pack is selected,
// whatever a config switch says, since it has none (docs/design/boundary-broker.md BB-D106).
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
	home, err := filepath.EvalSymlinks(t.TempDir())
	must(t, err)
	t.Setenv("HOME", home)
	t.Setenv("YOLO_USER_LAYER", "")
	writeUserReadGrant(t, home)

	ws := t.TempDir()
	if err := os.MkdirAll(filepath.Join(ws, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, ".git", "config"),
		[]byte("[remote \"origin\"]\n\turl = https://github.com/o/r\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	s := brokeredScopeForCheck(ws, gbSwitched(true), "podman", nil)
	if s == nil || len(s.Sources) != 1 || s.Sources[0].Source != "gbsrc" || s.Sources[0].Label != "gb" {
		t.Fatalf("scope %+v", s)
	}
	if got := s.Sources[0].Read.Repos(); len(got) != 1 || got[0] != "o/r" {
		t.Fatalf("repos %v", got)
	}
	if brokeredScopeForCheck(ws, gbSwitched(true), "container", nil) != nil {
		t.Fatal("Apple Container starts no broker, so the check must record no scope there")
	}
	if brokeredScopeForCheck(ws, gbSwitched(false), "podman", nil) == nil {
		t.Fatal("a config switch turned a brokered loophole off, which it has no switch for")
	}
	if brokeredScopeForCheck(ws, jsonx.NewOrderedMap(), "podman", nil) == nil {
		t.Fatal("a brokered loophole with no switch starts no broker: it runs whenever its pack is selected")
	}
}

// The check's scope carries the grant kinds the source's manifest defines into the gate's filter
// (docs/reference/git-broker.md ID-10), so a grant the user allows and the workspace names
// reaches the record `yolo check --accept-config-changes` writes, as it would a launch's.
func TestCheckScopeCarriesTheSourcesGrantKinds(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("YOLO_USER_LAYER", "")
	cfgDir := filepath.Join(home, ".config", "yolo-jail")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "config.jsonc"),
		[]byte(`{"brokered": {"gbsrc": {"grant_kinds": ["create-branch"], "grants": [{"kind": "read", "repo": "*"}]}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	mod := filepath.Join(t.TempDir(), "gb")
	if err := os.MkdirAll(mod, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name": "gb", "transport": "loopback-tls",
	  "lifecycle": "spawned",
	  "host_daemon": {"cmd": ["/bin/true", "{socket}", "{repository_scope}"], "publishes": "socket"},
	  "brokered": {"source": "gbsrc", "remote_host": "*", "grants": ["create-branch"]}}`
	if err := os.WriteFile(filepath.Join(mod, "manifest.jsonc"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	restore := loopholes.SnapshotPackModules()
	t.Cleanup(restore)
	loopholes.SetPackModules([]loopholes.PackModule{{Dir: mod, HostExecApproved: true}})

	ws, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, "yolo-jail.jsonc"),
		[]byte(`{"brokered": {"gbsrc": {"grants": [{"kind": "create-branch", "repo": "github.com/o/r"}]}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	// A remote, so the source has concrete scope: grants over no repository are vacuous and
	// held by nothing (config.NewScopeCheck).
	if err := os.MkdirAll(filepath.Join(ws, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, ".git", "config"), []byte("[remote \"origin\"]\n\turl = git@github.com:o/r.git\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	read, err := config.ReadWorkspaceForGate(ws)
	if err != nil {
		t.Fatal(err)
	}
	s := brokeredScopeForCheck(ws, gbSwitched(true), "podman", read)
	want := []brokerscope.Grant{{Kind: "read", Repo: "*"}, {Kind: "create-branch", Repo: "github.com/o/r"}}
	if s == nil || len(s.Sources) != 1 || !reflect.DeepEqual(s.Sources[0].Grants, want) {
		t.Fatalf("scope %+v, want the grant %v", s, want)
	}
}

// The recorded line for a source on any host lists its remote URLs, and an empty one names no
// host and no entry key, neither of which that source has.
func TestDescribeScopeRemoteURLForm(t *testing.T) {
	s := config.ScopeSource{Source: "git", Label: "git-broker", RemoteHost: "*", Form: brokerscope.FormRemoteURL}
	if got := describeScope(s); got != "none (no remote)" {
		t.Errorf("empty remote-url scope reads %q", got)
	}
	s.Read.Remotes = []brokerscope.Remote{{Name: "origin", Repo: "git@github.com:o/r.git"}}
	if got := describeScope(s); got != "git@github.com:o/r.git" {
		t.Errorf("remote-url scope reads %q", got)
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
