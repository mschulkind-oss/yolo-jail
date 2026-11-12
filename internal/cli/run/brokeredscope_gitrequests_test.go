package run

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// gitAndGitHubOn turns both brokers on, as the workspace's own file does.
func gitAndGitHubOn() *jsonx.OrderedMap {
	cfg := ghOn()
	loops, _ := cfg.Get("loopholes")
	entry := jsonx.NewOrderedMap()
	entry.Set("enabled", true)
	loops.(*jsonx.OrderedMap).Set("git-broker", entry)
	return cfg
}

// RC-D26, end to end through the launch's own gate: the same `requests.github.repos` entry that
// reaches the GitHub broker joins the GIT broker's scope too, as READ only. The git broker's scope
// is exact remote URLs, so the request becomes the HTTPS and the scp-style SSH URL of the
// repository, each one row of the gate's block; accepted, the git broker's scope file holds them
// and a read grant for the repository and no grant of any other kind; declined, nothing is
// recorded. Deleting the requested-repository branch of config.scopeCheckFromReadable fails it.
func TestAReadableRequestedRepoJoinsGitsReadScopeAtTheGateAndNeverItsPushes(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the fixture daemon is /bin/cp")
	}
	const repo = "acme/outside"
	wantURLs := []string{"https://github.com/" + repo, "git@github.com:" + repo + ".git"}
	setup := func(t *testing.T) (o *Options, buf *strings.Builder, gitMarker string) {
		o, buf, gitMarker = brokeredFixtureBlock(t, true,
			`{"source": "git", "remote_host": "*", "read_capability": "fetch"}`)
		module := func(name, marker, block string) loopholes.PackModule {
			mod := filepath.Join(t.TempDir(), name)
			if err := os.MkdirAll(mod, 0o755); err != nil {
				t.Fatal(err)
			}
			manifest := `{"name": "` + name + `", "transport": "loopback-tls", "lifecycle": "spawned",
			  "host_daemon": {"cmd": ["/bin/cp", "{repository_scope}", "` + marker + `"], "publishes": "socket"},
			  "brokered": ` + block + `}`
			if err := os.WriteFile(filepath.Join(mod, "manifest.jsonc"), []byte(manifest), 0o644); err != nil {
				t.Fatal(err)
			}
			return loopholes.PackModule{Dir: mod, Pack: strings.TrimSuffix(name, "-broker"), HostExecApproved: true}
		}
		loopholes.SetPackModules([]loopholes.PackModule{
			module("git-broker", gitMarker, `{"source": "git", "remote_host": "*", "read_capability": "fetch"}`),
			module("github-broker", filepath.Join(t.TempDir(), "gh-handed"), `{"source": "github", "remote_host": "github.com", "read_capability": "read"}`),
		})
		// Not a Git workspace: the request is the only thing that can put a repository in scope.
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
			[]byte("[allow.git]\nalways = [\"fetch\"]\n[allow.github]\nalways = [\"read\"]\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(o.Workspace, config.WorkspaceTOMLConfigName),
			[]byte("[repos.outside]\nrepo = \"github.com/"+repo+"\"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		return o, buf, gitMarker
	}

	t.Run("accepted", func(t *testing.T) {
		o, buf, marker := setup(t)
		o.AcceptConfigChanges = true
		if !o.checkConfigChanges(gitAndGitHubOn(), "podman") {
			t.Fatalf("an accepted gate refused:\n%s", buf.String())
		}
		for _, u := range wantURLs {
			if !strings.Contains(buf.String(), u) {
				t.Errorf("the gate did not show the requested repository's URL %q:\n%s", u, buf.String())
			}
		}
		if strings.Contains(buf.String(), "cannot be encoded") {
			t.Errorf("the gate still calls the request unrepresentable:\n%s", buf.String())
		}
		if got := config.ApprovedScope(o.Workspace, "git"); len(got) != 2 {
			t.Fatalf("the approval record holds %v, want %v", got, wantURLs)
		}
		handles := o.startLoopholes("yolo-brokered-gitreq", "podman", gitAndGitHubOn())
		t.Cleanup(func() { o.stopLoopholes(handles, hostServiceSocketsDir("yolo-brokered-gitreq", false), "", "") })
		f := readHandedScope(t, marker, buf)
		if strings.Join(f.Repos, ",") != strings.Join(wantURLs, ",") && strings.Join(f.Repos, ",") != wantURLs[1]+","+wantURLs[0] {
			t.Fatalf("the git broker's scope file holds %v, want %v", f.Repos, wantURLs)
		}
		if len(f.Grants) != 1 || f.Grants[0].Kind != "read" || f.Grants[0].Repo != "github.com/"+repo {
			t.Fatalf("the git broker holds grants %v, want the read of github.com/%s alone", f.Grants, repo)
		}
	})

	t.Run("declined", func(t *testing.T) {
		o, buf, marker := setup(t)
		if o.checkConfigChanges(gitAndGitHubOn(), "podman") {
			t.Fatalf("an unanswered gate passed:\n%s", buf.String())
		}
		if got := config.ApprovedScope(o.Workspace, "git"); len(got) != 0 {
			t.Fatalf("a declined gate recorded %v", got)
		}
		if _, err := os.Stat(marker); err == nil {
			t.Fatal("a declined gate still handed the git broker a scope file")
		}
	})
}
