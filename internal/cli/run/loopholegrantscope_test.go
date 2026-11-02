package run

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// grantScopeFixture selects a readable GitHub broker and a grant-declaring loophole pack beside it,
// with a user config whose `allow` holds both the broker's ceiling and the grant pack's
// `on_request`, and a workspace that requests the grant pack's `read_all` and has a GitHub
// remote. It returns the workspace and the gate's read of it.
func grantScopeFixture(t *testing.T) (string, *config.WorkspaceRead) {
	t.Helper()
	root := t.TempDir()
	broker := filepath.Join(root, "github-broker")
	grants := filepath.Join(root, "journal")
	for _, d := range []string{broker, grants} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(broker, "manifest.jsonc"), []byte(`{"name":"github-broker","transport":"loopback-tls","lifecycle":"spawned",
		"host_daemon":{"cmd":["/bin/true","{socket}","{repository_scope}"],"publishes":"socket"},
		"brokered":{"source":"github","remote_host":"github.com","grants":["create-pr"],"read_capability":"read","kind_capabilities":{"create-pr":"create_pr"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(grants, "loophole.toml"), []byte(`name = "journal"
description = "a grant-declaring fixture"
version = 1
transport = "loopback-tls"
access = "read the host's journal"

[host_daemon]
cmd = ["/bin/true", "{socket}"]
publishes = "socket"

[[grants]]
kind = "read"
text = "read your user journal"
requestable_by_default = true

[[grants]]
kind = "read_all"
text = "read the whole host journal"
`), 0o600); err != nil {
		t.Fatal(err)
	}
	restore := loopholes.SnapshotPackModules()
	t.Cleanup(restore)
	loopholes.SetPackModules([]loopholes.PackModule{{Dir: broker, Pack: "github-owner", HostExecApproved: true},
		{Dir: grants, Pack: "journal", HostExecApproved: true}})

	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("YOLO_USER_LAYER", "")
	userDir := filepath.Join(home, ".config", "yolo-jail")
	if err := os.MkdirAll(userDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(userDir, paths.UserConfigTOMLName),
		[]byte("[allow.github]\nalways = [\"read\"]\n\n[allow.journal]\non_request = [\"read_all\"]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ws, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(ws, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, ".git", "config"),
		[]byte("[remote \"origin\"]\n\turl = https://github.com/org/repo.git\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, config.WorkspaceTOMLConfigName),
		[]byte("[requests.journal]\nread_all = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	read, err := config.ReadWorkspaceForGate(ws)
	if err != nil {
		t.Fatal(err)
	}
	return ws, read
}

// grantScopeConfig enables both fixture loopholes, as a workspace's `loopholes` table does.
func grantScopeConfig() *jsonx.OrderedMap {
	cfg := jsonx.NewOrderedMap()
	lps := jsonx.NewOrderedMap()
	for _, name := range []string{"github-broker", "journal"} {
		on := jsonx.NewOrderedMap()
		on.Set("enabled", true)
		lps.Set(name, on)
	}
	cfg.Set("loopholes", lps)
	return cfg
}

// The launch's gate reads a grant-declaring pack's `allow.<pack>.on_request` as its ceiling even
// where a readable broker starts beside it: the collector hands the readable resolution the
// pack's grant kinds, so the table is not refused as an undeclared setting (T3's seam).
func TestTheLaunchScopeReadsAGrantDeclaringPacksCeilingBesideABroker(t *testing.T) {
	ws, read := grantScopeFixture(t)
	o := &Options{Workspace: ws}
	got := o.brokeredScopeCheck("podman", grantScopeConfig(), read)
	if got == nil {
		t.Fatal("no scope check for a selected broker and grant pack")
	}
	if got.Fatal != nil {
		t.Fatalf("the grant pack's ceiling was refused: %v", got.Fatal)
	}
	if len(got.Sources) != 1 || got.Sources[0].Source != "github" {
		t.Fatalf("broker sources %+v", got.Sources)
	}
	if len(got.LoopholeGrants) != 1 || got.LoopholeGrants[0].Pack != "journal" {
		t.Fatalf("grant sources %+v, want the journal fixture", got.LoopholeGrants)
	}
	standing := got.ApprovedLoopholeGrants()["journal"]
	if standing["read_all"].Standing != config.LoopholeGrantGranted {
		t.Fatalf("approved standings %+v", standing)
	}
}

// The launch collects its gate's scope and grants through the one collector `yolo check` uses
// (CollectScopeCheck), and with a launch's own backend filter it collects exactly what the check
// collects with its default one. The check's half of this pin is the test of the same name in
// internal/cli/check.
func TestCheckAndLaunchCollectTheSameGrantSources(t *testing.T) {
	ws, read := grantScopeFixture(t)
	calls := 0
	orig := collectScopeCheck
	t.Cleanup(func() { collectScopeCheck = orig })
	collectScopeCheck = func(in ScopeInput) *config.ScopeCheck {
		calls++
		return orig(in)
	}
	o := &Options{Workspace: ws}
	got := o.brokeredScopeCheck("podman", grantScopeConfig(), read)
	if calls != 1 {
		t.Fatalf("the launch's gate called the shared collector %d times, want 1", calls)
	}
	asCheck := CollectScopeCheck(ScopeInput{Workspace: ws, Merged: grantScopeConfig(), Runtime: "podman", Read: read})
	if !reflect.DeepEqual(got, asCheck) {
		t.Fatalf("launch and check collect different scopes:\nlaunch %+v\ncheck  %+v", got, asCheck)
	}
	if got == nil || len(got.LoopholeGrants) != 1 {
		t.Fatalf("no grant source collected: %+v", got)
	}
}

// On Apple Container no grant-declaring loophole runs, so its grants stay out of the gate and are
// kept as inert in the inert report's words (LG-D10).
func TestAnInertLoopholesRequestStaysOutOfTheLaunchGate(t *testing.T) {
	ws, read := grantScopeFixture(t)
	got := CollectScopeCheck(ScopeInput{Workspace: ws, Merged: grantScopeConfig(), Runtime: "container", Read: read})
	if got == nil || len(got.LoopholeGrants) != 0 || len(got.Sources) != 0 {
		t.Fatalf("container gate %+v, want nothing in play", got)
	}
	if len(got.InertLoopholeGrants) != 1 || got.InertLoopholeGrants[0].Requested[0] != "read_all" {
		t.Fatalf("inert grants %+v", got.InertLoopholeGrants)
	}
	rep, err := config.ScopeChanges(ws, got)
	if err != nil || rep.GrantsChanged || len(rep.GrantBlock) > 0 {
		t.Fatalf("an inert loophole reached the gate: %+v err=%v", rep, err)
	}
}
