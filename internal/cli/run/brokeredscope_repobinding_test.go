package run

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// staleRecordGitHub is the github source as the shipped github-broker declares it: its grant
// kinds and their capabilities, from packs/github/loopholes/github-broker/loophole.toml.
const staleRecordGitHub = `{"source": "github", "remote_host": "github.com", "grants": ["create-pr", "create-issue", "runs", "approve-run"], "read_capability": "read", "kind_capabilities": {"create-pr": "create_pr", "create-issue": "create_issue", "runs": "runs", "approve-run": "approve_runs"}}`

// staleRecordFixture is a github launch whose user config allows read, runs and approve-run, and
// whose workspace file is workspaceTOML. The workspace's origin is o/r.
func staleRecordFixture(t *testing.T, workspaceTOML string) (o *Options, buf *strings.Builder, marker string) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("the fixture daemon is /bin/cp")
	}
	o, buf, marker = brokeredFixtureBlock(t, true, staleRecordGitHub)
	mod := filepath.Join(t.TempDir(), "github-broker")
	if err := os.MkdirAll(mod, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name": "github-broker", "transport": "loopback-tls", "lifecycle": "spawned",
	  "host_daemon": {"cmd": ["/bin/cp", "{repository_scope}", "` + marker + `"], "publishes": "socket"},
	  "brokered": ` + staleRecordGitHub + `}`
	if err := os.WriteFile(filepath.Join(mod, "manifest.jsonc"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	loopholes.SetPackModules([]loopholes.PackModule{{Dir: mod, Pack: "github", HostExecApproved: true}})
	userDir := filepath.Join(os.Getenv("HOME"), ".config", "yolo-jail")
	if err := os.RemoveAll(userDir); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(userDir, 0o700); err != nil {
		t.Fatal(err)
	}
	user := "packs = ['github']\n\n[allow.github]\nalways = ['read']\non_request = ['runs', 'approve_runs']\n"
	if err := os.WriteFile(filepath.Join(userDir, paths.UserConfigTOMLName), []byte(user), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(o.Workspace, config.WorkspaceTOMLConfigName), []byte(workspaceTOML), 0o600); err != nil {
		t.Fatal(err)
	}
	return o, buf, marker
}

// staleRecordSeed records the approval the fan-out build left: read on o/r and o/other, runs and
// approve-run for the bot on both, as the gate approved them before origin-only lowering
// (GW-D79). The record is a comparison baseline; nothing hands it to a broker.
func staleRecordSeed(t *testing.T, o *Options) {
	t.Helper()
	const bot = "github-actions[bot]"
	grants := []brokerscope.Grant{
		{Kind: brokerscope.KindRead, Repo: "github.com/o/r"},
		{Kind: brokerscope.KindRead, Repo: "github.com/o/other"},
		{Kind: brokerscope.KindRuns, Repo: "github.com/o/r", Workflow: brokerscope.AnyWorkflow},
		{Kind: brokerscope.KindRuns, Repo: "github.com/o/other", Workflow: brokerscope.AnyWorkflow},
		{Kind: brokerscope.KindApproveRun, Repo: "github.com/o/r", Actor: bot},
		{Kind: brokerscope.KindApproveRun, Repo: "github.com/o/other", Actor: bot},
	}
	old := &config.ScopeCheck{Sources: []config.ScopeSource{{Source: "github", Label: "github-broker",
		Pack: "github", RemoteHost: "github.com",
		Read:   brokerscope.Read{Remotes: []brokerscope.Remote{{Name: "origin", Repo: "o/r"}}},
		Entry:  []config.EntryRepo{{Repo: "o/other", Files: []string{config.WorkspaceTOMLConfigName}}},
		Grants: grants,
	}}}
	if err := config.RecordApproval(o.Workspace, jsonx.NewOrderedMap(), old); err != nil {
		t.Fatal(err)
	}
}

// GW-D79 and GW-D76. A record of approvals is a comparison baseline: the gate replaces it at each
// approval, and a launch hands its broker the gate's own result. A stale record, one the fan-out
// build wrote, never becomes a scope file: the gate shows the change, and only an accepted gate
// hands the broker what the workspace approves now. Each subtest is its own launch.
func TestAStaleApprovalRecordNeverBecomesAScopeFile(t *testing.T) {
	const workspace = "[requests.github]\nruns = [\"*\"]\napprove_runs = [\"github-actions[bot]\"]\n\n[repos.notes]\nrepo = \"github.com/o/other\"\n"
	t.Run("accepted: the stale o/other grants are removed and never handed", func(t *testing.T) {
		o, buf, marker := staleRecordFixture(t, workspace)
		staleRecordSeed(t, o)
		o.AcceptConfigChanges = true
		if !o.checkConfigChanges(ghOn(), "podman") {
			t.Fatalf("an accepted gate refused:\n%s", buf.String())
		}
		if !strings.Contains(buf.String(), "- grant runs github.com/o/other") || !strings.Contains(buf.String(), "removed") {
			t.Errorf("the gate does not show the stale grant removed:\n%s", buf.String())
		}
		handles := o.startLoopholes("yolo-stale-record", "podman", ghOn())
		t.Cleanup(func() { o.stopLoopholes(handles, hostServiceSocketsDir("yolo-stale-record", false), "", "") })
		f := readHandedScope(t, marker, buf)
		if !slicesHasGrant(f.Grants, brokerscope.Grant{Kind: brokerscope.KindRead, Repo: "github.com/o/other"}) {
			t.Errorf("the handed file lost the read on o/other: %v", f.Grants)
		}
		for _, g := range f.Grants {
			if g.Kind != brokerscope.KindRead && (g.Repo == "github.com/o/other" || g.Repo == brokerscope.AnyRepo) {
				t.Errorf("the handed file hands a stale write grant: %+v", g)
			}
		}
		if !slicesHasGrant(f.Grants, brokerscope.Grant{Kind: brokerscope.KindRuns, Repo: "github.com/o/r", Workflow: brokerscope.AnyWorkflow}) {
			t.Errorf("the handed file does not hold origin's runs grant: %v", f.Grants)
		}
		for _, g := range config.ApprovedGrants(o.Workspace, "github") {
			if g.Repo == "github.com/o/other" && g.Kind != brokerscope.KindRead {
				t.Errorf("the record still holds the removed grant %+v", g)
			}
		}
	})

	t.Run("no terminal and no accept: the record stays and no scope file is handed", func(t *testing.T) {
		o, buf, marker := staleRecordFixture(t, workspace)
		staleRecordSeed(t, o)
		if o.checkConfigChanges(ghOn(), "podman") {
			t.Fatalf("an unanswered gate passed:\n%s", buf.String())
		}
		if _, err := os.Stat(marker); err == nil {
			t.Fatal("a refused gate still handed the broker a scope file")
		}
		if got := config.ApprovedGrants(o.Workspace, "github"); !slicesHasGrant(got, brokerscope.Grant{Kind: brokerscope.KindRuns, Repo: "github.com/o/other", Workflow: brokerscope.AnyWorkflow}) {
			t.Fatalf("a refused gate rewrote the record: %v", got)
		}
	})

	t.Run("origin moves: the old origin's grants are removed, and an accepted gate hands none of them", func(t *testing.T) {
		o, buf, marker := staleRecordFixture(t, workspace)
		staleRecordSeed(t, o)
		o.AcceptConfigChanges = true
		if !o.checkConfigChanges(ghOn(), "podman") {
			t.Fatalf("the first accepted gate refused:\n%s", buf.String())
		}
		if err := os.WriteFile(filepath.Join(o.Workspace, ".git", "config"),
			[]byte("[remote \"origin\"]\n\turl = https://github.com/o/new.git\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		o.AcceptConfigChanges = false
		buf.Reset()
		if o.checkConfigChanges(ghOn(), "podman") {
			t.Fatalf("a moved origin passed without an answer:\n%s", buf.String())
		}
		for _, want := range []string{"+ grant runs github.com/o/new", "- grant runs github.com/o/r"} {
			if !strings.Contains(buf.String(), want) {
				t.Errorf("the gate does not show %q:\n%s", want, buf.String())
			}
		}
		o.AcceptConfigChanges = true
		buf.Reset()
		if !o.checkConfigChanges(ghOn(), "podman") {
			t.Fatalf("the moved origin's accepted gate refused:\n%s", buf.String())
		}
		handles := o.startLoopholes("yolo-stale-record", "podman", ghOn())
		t.Cleanup(func() { o.stopLoopholes(handles, hostServiceSocketsDir("yolo-stale-record", false), "", "") })
		f := readHandedScope(t, marker, buf)
		for _, g := range f.Grants {
			if g.Repo == "github.com/o/r" {
				t.Errorf("the handed file names the old origin o/r: %+v", g)
			}
		}
		if !slicesHasGrant(f.Grants, brokerscope.Grant{Kind: brokerscope.KindRuns, Repo: "github.com/o/new", Workflow: brokerscope.AnyWorkflow}) {
			t.Errorf("the handed file does not hold the new origin's runs grant: %v", f.Grants)
		}
	})

	// A named table's request is approved on the table's repository; retargeting the local file
	// moves the approval, and the old repository's grant is never handed to the broker.
	t.Run("a local alias retargeted: the old repository's grant is removed, not handed", func(t *testing.T) {
		o, buf, marker := staleRecordFixture(t, "[repos.notes.requests.github]\nruns = [\"a.yml\"]\n")
		localPath := filepath.Join(o.Workspace, config.WorkspaceLocalTOMLConfigName)
		if err := os.WriteFile(localPath, []byte("[repos.notes]\nrepo = \"github.com/o/other\"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		o.AcceptConfigChanges = true
		if !o.checkConfigChanges(ghOn(), "podman") {
			t.Fatalf("the first accepted gate refused:\n%s", buf.String())
		}
		if err := os.WriteFile(localPath, []byte("[repos.notes]\nrepo = \"github.com/o/third\"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		o.AcceptConfigChanges = false
		buf.Reset()
		if o.checkConfigChanges(ghOn(), "podman") {
			t.Fatalf("a retargeted alias passed without an answer:\n%s", buf.String())
		}
		for _, want := range []string{"+ grant runs github.com/o/third", "- grant runs github.com/o/other"} {
			if !strings.Contains(buf.String(), want) {
				t.Errorf("the gate does not show %q:\n%s", want, buf.String())
			}
		}
		o.AcceptConfigChanges = true
		buf.Reset()
		if !o.checkConfigChanges(ghOn(), "podman") {
			t.Fatalf("the retargeted alias's accepted gate refused:\n%s", buf.String())
		}
		handles := o.startLoopholes("yolo-stale-record", "podman", ghOn())
		t.Cleanup(func() { o.stopLoopholes(handles, hostServiceSocketsDir("yolo-stale-record", false), "", "") })
		f := readHandedScope(t, marker, buf)
		for _, g := range f.Grants {
			if g.Repo == "github.com/o/other" {
				t.Errorf("the handed file still names the old target: %+v", g)
			}
		}
		if !slicesHasGrant(f.Grants, brokerscope.Grant{Kind: brokerscope.KindRuns, Repo: "github.com/o/third", Workflow: "a.yml"}) {
			t.Errorf("the handed file does not hold the new target's runs grant: %v", f.Grants)
		}
	})
}

func slicesHasGrant(grants []brokerscope.Grant, want brokerscope.Grant) bool {
	for _, g := range grants {
		if g == want {
			return true
		}
	}
	return false
}
