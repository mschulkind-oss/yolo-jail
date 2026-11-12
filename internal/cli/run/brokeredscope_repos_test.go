package run

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// GW-D76. A named repository table, `[repos.<name>]` in the workspace file, is read scope the
// gate shows and the launch hands the github broker, as a requested repository is, and it is
// approved only by its own table: it asks for nothing more than read.
func TestANamedRepositoryIsApprovedAtTheGateAndReachesTheBroker(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the fixture daemon is /bin/cp")
	}
	const repo = "acme/outside"
	setup := func(t *testing.T) (o *Options, buf *strings.Builder, marker string) {
		o, buf, marker = brokeredFixtureBlock(t, true,
			`{"source": "github", "remote_host": "github.com", "read_capability": "read"}`)
		// The readable path wants the broker's owning pack: re-record the fixture's module under one.
		mod := filepath.Join(t.TempDir(), "github-broker")
		if err := os.MkdirAll(mod, 0o755); err != nil {
			t.Fatal(err)
		}
		manifest := `{"name": "github-broker", "transport": "loopback-tls", "lifecycle": "spawned",
		  "host_daemon": {"cmd": ["/bin/cp", "{repository_scope}", "` + marker + `"], "publishes": "socket"},
		  "brokered": {"source": "github", "remote_host": "github.com", "read_capability": "read"}}`
		if err := os.WriteFile(filepath.Join(mod, "manifest.jsonc"), []byte(manifest), 0o644); err != nil {
			t.Fatal(err)
		}
		loopholes.SetPackModules([]loopholes.PackModule{{Dir: mod, Pack: "github", HostExecApproved: true}})
		// Not a Git workspace: the named table is the only thing that can put a repository in scope.
		if err := os.RemoveAll(filepath.Join(o.Workspace, ".git")); err != nil {
			t.Fatal(err)
		}
		userDir := filepath.Join(os.Getenv("HOME"), ".config", "yolo-jail")
		if err := os.RemoveAll(userDir); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(userDir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(userDir, paths.UserConfigTOMLName),
			[]byte("[allow.github]\nalways = [\"read\"]\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(o.Workspace, config.WorkspaceTOMLConfigName),
			[]byte("[repos.outside]\nrepo = \"github.com/"+repo+"\"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		return o, buf, marker
	}

	t.Run("accepted", func(t *testing.T) {
		o, buf, marker := setup(t)
		o.AcceptConfigChanges = true
		if !o.checkConfigChanges(ghOn(), "podman") {
			t.Fatalf("an accepted gate refused:\n%s", buf.String())
		}
		if !strings.Contains(buf.String(), repo) {
			t.Errorf("the gate did not show the named repository %q:\n%s", repo, buf.String())
		}
		if got := config.ApprovedScope(o.Workspace, "github"); strings.Join(got, ",") != repo {
			t.Fatalf("the approval record holds %v, want [%s]", got, repo)
		}
		handles := o.startLoopholes("yolo-brokered-repos", "podman", ghOn())
		t.Cleanup(func() { o.stopLoopholes(handles, hostServiceSocketsDir("yolo-brokered-repos", false), "", "") })
		f := readHandedScope(t, marker, buf)
		if strings.Join(f.Repos, ",") != repo {
			t.Fatalf("the broker's scope file holds %v, want [%s]", f.Repos, repo)
		}
	})
}
