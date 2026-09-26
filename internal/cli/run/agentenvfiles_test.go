package run

// agentenvfiles_test.go pins the CONTAINER VEHICLE of the credential gate
// (docs/design/provider-credential-scope.md, OQ-CN6): what deliverChannel writes into the
// shared yolo-user-env.sh and into each agent's own env file, composed through the real
// composePackChannel over the SHIPPED packs. The launch-level half — Run() writing these
// files on a fresh container launch and on an attach — is credentialgate_test.go's.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// inlinePack is a real staged-shape pack (LoadDir) from one manifest string.
func inlinePack(t *testing.T, name, manifest string) *packload.Pack {
	t.Helper()
	root := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	p, problems := packload.LoadDir(root, name)
	if len(problems) != 0 {
		t.Fatalf("loading fixture pack %s: %v", name, problems)
	}
	return p
}

// deliveredFiles runs deliverChannel into a fresh wsState and returns the shared file and
// every agent file it wrote, keyed by agent.
func deliveredFiles(t *testing.T, channel *packChannel) (shared string, agents map[string]string) {
	t.Helper()
	ws := t.TempDir()
	deliverChannel(ws, "podman", channel)
	b, err := os.ReadFile(filepath.Join(ws, "yolo-user-env.sh"))
	if err != nil {
		t.Fatalf("the shared file was not written: %v", err)
	}
	agents = map[string]string{}
	entries, _ := os.ReadDir(filepath.Join(ws, agentEnvStateDir))
	for _, e := range entries {
		body, err := os.ReadFile(filepath.Join(ws, agentEnvStateDir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		agents[strings.TrimSuffix(e.Name(), ".sh")] = string(body)
	}
	return string(b), agents
}

// awsAndZaiKeys is an env_sources hydration carrying the §2.1 leak's measured pair, a zai
// key, and one variable no provider claims.
func awsAndZaiKeys() *jsonx.OrderedMap {
	m := jsonx.NewOrderedMap()
	m.Set("AWS_ACCESS_KEY_ID", "AKIA-test")
	m.Set("AWS_SECRET_ACCESS_KEY", "secret-test")
	m.Set("ZAI_API_KEY", "tok-zai")
	m.Set("GH_TOKEN", "gh-test")
	return m
}

// The container vehicle's whole contract over the shipped claude + pi + zai packs with
// pi on zai: the shared file keeps only what no provider claims, pi's own file carries
// zai's key, and neither carries the Bedrock pair nobody selected.
func TestDeliverChannelScopesCredentialsPerAgent(t *testing.T) {
	home := packHome(t)
	o := goldenOptions(t.TempDir(), home)
	packs := []*packload.Pack{officialPack(t, "claude"), officialPack(t, "pi"), officialPack(t, "zai")}
	o.UseProfiles = map[string]string{"pi": "zai"}
	channel := channelFor(t, o, bareConfig(), packs, awsAndZaiKeys())

	shared, agents := deliveredFiles(t, channel)
	// VALUES, and export lines: the provider table in the channel section names the
	// variables (never their values), which is the secret-free wire the derives read.
	for _, gone := range []string{"AKIA-test", "secret-test", "tok-zai",
		"export AWS_ACCESS_KEY_ID", "export AWS_SECRET_ACCESS_KEY", "export ZAI_API_KEY"} {
		if strings.Contains(shared, gone) {
			t.Errorf("the shared file — exported into every process — carries %s:\n%s", gone, shared)
		}
	}
	if !strings.Contains(shared, "export GH_TOKEN=${GH_TOKEN:-'gh-test'}\n") {
		t.Errorf("a variable no provider claims must stay shared, def-form:\n%s", shared)
	}
	pi, ok := agents["pi"]
	if !ok {
		t.Fatalf("no env file for pi, which selected zai; files: %v", agents)
	}
	if !strings.Contains(pi, "export ZAI_API_KEY=${ZAI_API_KEY:-'tok-zai'}\n") {
		t.Errorf("pi's file must carry its provider's key, def-form:\n%s", pi)
	}
	for agent, body := range agents {
		if strings.Contains(body, "AWS_") || strings.Contains(body, "AKIA-test") {
			t.Errorf("%s's file carries an AWS variable although no agent selected bedrock:\n%s", agent, body)
		}
	}
	if _, leaked := agents["claude"]; leaked {
		t.Errorf("claude selected nothing, so it must get no file of its own: %s", agents["claude"])
	}
}

// An attach that deselects rewrites the directory WHOLE: the agent that lost its profile
// loses its file, so a credential the previous entry scoped to it is not left behind.
func TestDeliverChannelRevokesADeselectedAgentsFile(t *testing.T) {
	home := packHome(t)
	o := goldenOptions(t.TempDir(), home)
	packs := []*packload.Pack{officialPack(t, "claude"), officialPack(t, "pi"), officialPack(t, "zai")}
	ws := t.TempDir()

	o.UseProfiles = map[string]string{"pi": "zai"}
	deliverChannel(ws, "podman", channelFor(t, o, bareConfig(), packs, awsAndZaiKeys()))
	piFile := filepath.Join(ws, agentEnvStateDir, "pi.sh")
	if _, err := os.Stat(piFile); err != nil {
		t.Fatalf("the first entry wrote no pi file: %v", err)
	}
	if st, _ := os.Stat(piFile); st != nil && st.Mode().Perm() != 0o600 {
		t.Errorf("pi's credential file mode = %04o, want 0600", st.Mode().Perm())
	}
	if st, err := os.Stat(filepath.Join(ws, agentEnvStateDir)); err != nil || st.Mode().Perm() != 0o700 {
		t.Errorf("the per-agent directory must be 0700 (CN-D7): mode %s, err %v", permOf(st), err)
	}

	o.UseProfiles = nil
	deliverChannel(ws, "podman", channelFor(t, o, bareConfig(), packs, awsAndZaiKeys()))
	if _, err := os.Stat(piFile); !os.IsNotExist(err) {
		t.Errorf("the deselecting entry left pi's credential file behind: %v", err)
	}
}

// The file grammar: env_sources def-form, the composed values plain-form, a tombstone as
// `unset` — and a key that is not a variable name never reaches the bash source.
func TestAgentEnvFileContentGrammar(t *testing.T) {
	claude := inlinePack(t, "claude", `{"name":"claude","contributes":[`+
		`{"kind":"program","bin":"claude","via":"npm","package":"@acme/claude"},`+
		`{"kind":"profile","name":"zai","provider":"zai"},`+
		`{"kind":"env","profile":"zai","vars":{"GATED":"it's"}}]}`)
	root := claude.Root
	derive := `yolo.env("claude", function(ctx)
  return { SHAPE = "s", GONE = ctx.tombstone, ["bad name"] = "x" }
end)`
	if err := os.WriteFile(filepath.Join(root, "derive.lua"), []byte(derive), 0o644); err != nil {
		t.Fatal(err)
	}
	providers := jsonx.NewOrderedMap()
	zai := jsonx.NewOrderedMap()
	zai.Set("api_key_env_name", "ZAI_API_KEY")
	providers.Set("zai", zai)
	profiles := jsonx.NewOrderedMap()
	profiles.Set("claude", "zai")
	env := jsonx.NewOrderedMap()
	env.Set("ZAI_API_KEY", "k")
	resolved := map[string]packload.ResolvedProfile{"zai": {Provider: "zai"}}
	scope, err := packload.ScopeCredentials(packload.ScopeInput{
		Packs: []*packload.Pack{claude}, Providers: providers,
		Profiles: packload.ProfileTable(profiles), Resolved: resolved, EnvSources: env,
	})
	if err != nil {
		t.Fatal(err)
	}
	got := agentEnvFileContent(&packChannel{profiles: profiles, providers: providers, scope: scope}, "claude")
	for _, want := range []string{
		"export ZAI_API_KEY=${ZAI_API_KEY:-'k'}\n",
		`export GATED='it'\''s'` + "\n",
		"export SHAPE='s'\n",
		"unset GONE\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("claude's file lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "bad name") {
		t.Errorf("a derive key that is not a variable name was spliced into bash source:\n%s", got)
	}
}

// permOf is a stat result's permission bits for a failure message, "none" when there is none.
func permOf(st os.FileInfo) string {
	if st == nil {
		return "none"
	}
	return fmt.Sprintf("%04o", st.Mode().Perm())
}

// Apple Container binds wsState ITSELF at /home/agent, so the jail reads the per-agent files
// at <wsState>/.config/yolo-agent-env — a real host path under the workspace. They must be
// owner-only there as on podman (CN-D7: 0600 in a 0700 directory): the copy the fresh
// launch's assembly used to make wrote them 0644 in a 0755 directory, so any local user who
// could walk the workspace read pi's zai key. A directory an earlier build left wide is
// narrowed again, a stale file is removed, and a link planted where the directory belongs is
// replaced rather than written through.
func TestAppleContainerWritesOwnerOnlyAgentFilesAtTheJailsPath(t *testing.T) {
	home := packHome(t)
	o := goldenOptions(t.TempDir(), home)
	packs := []*packload.Pack{officialPack(t, "claude"), officialPack(t, "pi"), officialPack(t, "zai")}
	o.UseProfiles = map[string]string{"pi": "zai"}
	channel := channelFor(t, o, bareConfig(), packs, awsAndZaiKeys())

	ws := t.TempDir()
	inHome := filepath.Join(ws, entrypoint.AgentEnvDirRel)
	// What an earlier build's copy left: a wide directory and a stale agent's file.
	if err := os.MkdirAll(inHome, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(inHome, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(inHome, "codex.sh"), []byte("export AWS_ACCESS_KEY_ID='old'\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	deliverChannel(ws, "container", channel)

	st, err := os.Stat(inHome)
	if err != nil || st.Mode().Perm() != 0o700 {
		t.Fatalf("the in-home per-agent directory must be 0700: mode %s, err %v", permOf(st), err)
	}
	pi := filepath.Join(inHome, "pi.sh")
	body, err := os.ReadFile(pi)
	if err != nil {
		t.Fatalf("Apple Container's jail reads %s, and nothing was written there: %v", pi, err)
	}
	if !strings.Contains(string(body), "ZAI_API_KEY") {
		t.Errorf("pi's in-home file lacks its provider's key:\n%s", body)
	}
	if st, _ := os.Stat(pi); st == nil || st.Mode().Perm() != 0o600 {
		t.Errorf("pi's credential file on Apple Container must be 0600, got %s", permOf(st))
	}
	if _, err := os.Stat(filepath.Join(inHome, "codex.sh")); !os.IsNotExist(err) {
		t.Errorf("a file the previous entry left must be removed: %v", err)
	}
	if st, err := os.Stat(filepath.Join(ws, ".config", "yolo-user-env.sh")); err != nil || st.Mode().Perm() != 0o600 {
		t.Errorf("the shared file's in-home copy must exist and be 0600: mode %s, err %v", permOf(st), err)
	}
	// ONE copy of the credential per backend: the podman bind source is not written too,
	// since wsState is the home here and a second copy would sit at ~/agent-env.
	if _, err := os.Stat(filepath.Join(ws, agentEnvStateDir)); !os.IsNotExist(err) {
		t.Errorf("Apple Container needs no podman bind source beside the in-home files: %v", err)
	}

	// A link where the directory belongs is replaced, never followed.
	outside := t.TempDir()
	if err := os.RemoveAll(inHome); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, inHome); err != nil {
		t.Fatal(err)
	}
	deliverChannel(ws, "container", channel)
	if fi, err := os.Lstat(inHome); err != nil || !fi.IsDir() {
		t.Errorf("the planted link must be replaced by a real directory: %v %v", fi, err)
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Errorf("the credential was written through the jail's link into %s: %v", outside, entries)
	}
}

// AN ATTACH ON APPLE CONTAINER RE-SCOPES WHAT THE JAIL READS. The running jail sources
// <wsState>/.config/yolo-agent-env/<agent>.sh, and the attach's delivery used to rewrite only
// the podman bind source beside it — so an attach that deselected pi's profile left pi's zai
// key in the file pi's launcher keeps sourcing, and the attach's disclosure named a scope
// the jail did not get. The shared file's in-home copy had the same staleness.
func TestAppleContainerAttachRevokesADeselectedAgentsFile(t *testing.T) {
	packs := []*packload.Pack{officialPack(t, "claude"), officialPack(t, "pi"), officialPack(t, "zai")}
	o, cfg, channel, stderr := attachFixture(t, "YOLO_VERSION=9.9.9-test\n", packs, awsAndZaiKeys(),
		func(o *Options, _ *jsonx.OrderedMap) { o.UseProfiles = map[string]string{"pi": "zai"} })
	ws := paths.WorkspaceHomeState(o.Workspace)
	deliverChannel(ws, "container", channel) // the fresh launch
	pi := filepath.Join(ws, entrypoint.AgentEnvDirRel, "pi.sh")
	if _, err := os.Stat(pi); err != nil {
		t.Fatalf("the fresh launch wrote no in-home pi file: %v", err)
	}

	o.UseProfiles = nil
	deselected := channelFor(t, o, cfg, packs, awsAndZaiKeys())
	if rc := o.deliverChannelOnAttach("yolo-ws-abcd1234", "container", cfg,
		stagedPacks{root: "/ctx/packs", packs: packs}, deselected, []string{"YOLO_VERSION=9.9.9-test"}); rc != 0 {
		t.Fatalf("the attach refused: rc=%d\n%s", rc, stderr.String())
	}
	if _, err := os.Stat(pi); !os.IsNotExist(err) {
		b, _ := os.ReadFile(pi)
		t.Errorf("the deselecting attach left pi's credential in the file its launcher sources:\n%s", b)
	}
	shared, err := os.ReadFile(filepath.Join(ws, ".config", "yolo-user-env.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(shared), `"pi"`) {
		t.Errorf("the attach left the previous entry's selection in the shared file the jail reads:\n%s", shared)
	}
}
