package run

// agentenvfiles_test.go pins the CONTAINER VEHICLE of the credential gate
// (docs/design/provider-credential-scope.md, OQ-CN6): what deliverChannel writes into the
// shared yolo-user-env.sh and into each agent's own env file, composed through the real
// composePackChannel over the SHIPPED packs. The launch-level half — Run() writing these
// files on a fresh container launch and on an attach — is credentialgate_test.go's.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
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
	deliverChannel(ws, channel)
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
	deliverChannel(ws, channelFor(t, o, bareConfig(), packs, awsAndZaiKeys()))
	piFile := filepath.Join(ws, agentEnvStateDir, "pi.sh")
	if _, err := os.Stat(piFile); err != nil {
		t.Fatalf("the first entry wrote no pi file: %v", err)
	}
	if st, _ := os.Stat(piFile); st != nil && st.Mode().Perm() != 0o600 {
		t.Errorf("pi's credential file mode = %04o, want 0600", st.Mode().Perm())
	}

	o.UseProfiles = nil
	deliverChannel(ws, channelFor(t, o, bareConfig(), packs, awsAndZaiKeys()))
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
