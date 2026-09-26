package run

// credentialgate_test.go pins the credential gate's done conditions
// (docs/design/provider-credential-scope.md §5) on the CONTAINER vehicle, end to end: what
// each agent's PROCESS receives in a jail, not what a writer was handed.
//
// The chain is the real one, every link of it: composePackChannel over the SHIPPED packs
// (the composition Run makes above the backend dispatch), deliverChannel (the one writer a
// fresh launch and an attach both call), the files laid out where the container binds put
// them, the REAL launchers entrypoint.GenerateAgentLaunchers writes from the shipped
// manifests, and each launcher RUN the way the jail runs a command — the shared file
// sourced first, then the agent's own launcher by path — against a fake program that
// reports the environment it was handed. A bare shell is the same entry without an agent.
// No real agent runs, and nothing is fetched: every program already "exists".
//
// What Run itself contributes — calling deliverChannel on the fresh path and on the attach,
// and printing the disclosure beside each — is pinned where Run can be observed:
// TestTheLaunchPathsDeliverThroughTheGate below reads runContainer's and
// deliverChannelOnAttach's call graph (runContainer starts a real container, so a unit test
// has no other witness there), attachchannel_test.go drives the attach, and the macos-user
// arm is driven through Run in TestMacosUserLaunchCarriesOnlyTheLaunchedAgentsCredentials.

import (
	"bytes"
	"go/ast"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	officialpacks "github.com/mschulkind-oss/yolo-jail/packs"
)

// gateCredentials is the env_sources hydration every cell launches with: the §2.1 leak's
// measured AWS pair, a zai key, and one variable no provider claims.
func gateCredentials() *jsonx.OrderedMap {
	m := jsonx.NewOrderedMap()
	m.Set("AWS_ACCESS_KEY_ID", "AKIA-gate")
	m.Set("AWS_SECRET_ACCESS_KEY", "secret-gate")
	m.Set("ZAI_API_KEY", "tok-gate")
	m.Set("GH_TOKEN", "gh-gate")
	return m
}

// gateJail is one launch's delivery laid out as a container jail sees it, with the shipped
// agents' real launchers generated into it.
type gateJail struct {
	t       *testing.T
	home    string
	fakeBin string
}

// launchGateJail composes the channel for packs under the given selection, delivers it, and
// lays out the jail home: the shared file at ~/.config/yolo-user-env.sh and the agent files
// under ~/.config/yolo-agent-env — the podman binds' destinations — and the launchers of
// every shipped agent pack in ~/.yolo/bin/launch.
func launchGateJail(t *testing.T, packNames []string, tune func(*Options)) (*gateJail, *packChannel, string) {
	t.Helper()
	home := packHome(t)
	o := goldenOptions(t.TempDir(), home)
	var stderr bytes.Buffer
	o.Stderr = &stderr
	if tune != nil {
		tune(o)
	}
	var packs []*packload.Pack
	for _, name := range packNames {
		packs = append(packs, officialPack(t, name))
	}
	channel := channelFor(t, o, bareConfig(), packs, gateCredentials())
	ws := t.TempDir()
	deliverChannel(ws, channel)
	o.noteCredentialScope(channel)

	jail := &gateJail{t: t, home: t.TempDir(), fakeBin: t.TempDir()}
	cfgDir := filepath.Join(jail.home, ".config")
	if err := os.MkdirAll(filepath.Join(jail.home, entrypoint.AgentEnvDirRel), 0o700); err != nil {
		t.Fatal(err)
	}
	copyFile(t, filepath.Join(ws, "yolo-user-env.sh"), filepath.Join(cfgDir, "yolo-user-env.sh"))
	entries, _ := os.ReadDir(filepath.Join(ws, agentEnvStateDir))
	for _, e := range entries {
		copyFile(t, filepath.Join(ws, agentEnvStateDir, e.Name()),
			filepath.Join(jail.home, entrypoint.AgentEnvDirRel, e.Name()))
	}

	// The staged pack tree the entrypoint renders launchers from: the shipped packs, as the
	// jail's /ctx/packs mount carries them.
	root := t.TempDir()
	if _, problems := packload.MaterializeEmbedded(officialpacks.FS, filepath.Join(root, "_official")); len(problems) != 0 {
		t.Fatalf("materializing official packs: %v", problems)
	}
	e := entrypoint.NewEnv(map[string]string{
		"JAIL_HOME": jail.home, "HOME": jail.home, "YOLO_PACK_ROOT": root,
		// Frozen, so no launcher carries an update branch: nothing may reach a network.
		entrypoint.AgentUpdatesEnv: "false",
	})
	// The jail's generator switches this process to tolerant manifest reads, as the boot
	// does; hand the next test a strict host process back.
	t.Cleanup(packload.OverrideSkewTolerance(false))
	if err := entrypoint.GenerateAgentLaunchers(e); err != nil {
		t.Fatalf("GenerateAgentLaunchers: %v", err)
	}
	// The pre-launch authentication step asks `yolo` for a token view; a fake answers so no
	// real broker is ever reached.
	writeExec(t, filepath.Join(jail.fakeBin, "yolo"), "#!/bin/sh\nexit 0\n")
	return jail, channel, stderr.String()
}

func copyFile(t *testing.T, from, to string) {
	t.Helper()
	b, err := os.ReadFile(from)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(to, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeExec(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

// agentEnv runs agent the way the jail runs `yolo -- <agent>` — the shared file sourced
// (execBash, .bashrc), then the agent's own launcher — and returns the environment the
// agent's program was handed. The program is a fake at the path the launcher execs.
func (j *gateJail) agentEnv(agent string) map[string]string {
	t := j.t
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not found")
	}
	out := filepath.Join(j.home, agent+".env")
	switch agent {
	case "pi": // npm, under its declared node floor: the fake must be a node program
		if _, err := exec.LookPath("node"); err != nil {
			t.Skip("node not found; pi's launcher execs its program under node")
		}
		writeExec(t, filepath.Join(j.home, ".npm-global", "bin", "pi"), "#!/usr/bin/env node\n"+
			"require('fs').writeFileSync(process.env.ENV_OUT, Object.entries(process.env)"+
			".map(([k, v]) => k + '=' + v).join('\\n') + '\\n')\n")
	default: // claude, codex: native installers landing in ~/.local/bin
		writeExec(t, filepath.Join(j.home, ".local", "bin", agent), "#!/bin/sh\nenv > \"$ENV_OUT\"\n")
	}
	launcher := filepath.Join(j.home, ".yolo", "bin", "launch", agent)
	if _, err := os.Stat(launcher); err != nil {
		t.Fatalf("no launcher for %s: %v", agent, err)
	}
	j.run(`. "$HOME/.config/yolo-user-env.sh"; exec "$0"`, launcher, out)
	return readEnvDump(t, out)
}

// shellEnv is a bare `yolo -- bash`: the shared file sourced and nothing else.
func (j *gateJail) shellEnv() map[string]string {
	j.t.Helper()
	out := filepath.Join(j.home, "shell.env")
	j.run(`. "$HOME/.config/yolo-user-env.sh"; env > "$ENV_OUT"`, "", out)
	return readEnvDump(j.t, out)
}

func (j *gateJail) run(script, arg0, out string) {
	t := j.t
	t.Helper()
	args := []string{"-c", script}
	if arg0 != "" {
		args = append(args, arg0)
	}
	cmd := exec.Command("bash", args...)
	cmd.Env = []string{"HOME=" + j.home, "PATH=" + j.fakeBin + ":/usr/bin:/bin", "ENV_OUT=" + out}
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("running %q: %v\n%s", script, err, b)
	}
}

func readEnvDump(t *testing.T, path string) map[string]string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the program never ran (no env dump at %s): %v", path, err)
	}
	env := map[string]string{}
	for _, line := range strings.Split(string(b), "\n") {
		if k, v, ok := strings.Cut(line, "="); ok {
			env[k] = v
		}
	}
	return env
}

// requireAbsent fails for each name env carries.
func requireAbsent(t *testing.T, who string, env map[string]string, names ...string) {
	t.Helper()
	for _, n := range names {
		if v, ok := env[n]; ok {
			t.Errorf("%s's environment carries %s=%q", who, n, v)
		}
	}
}

var awsNames = []string{"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_CONTAINER_CREDENTIALS_FULL_URI"}

// DONE CONDITION 1: `yolo -p zai -- pi`. pi's environment carries zai's key and no AWS
// variable; neither does a bare shell; and the launch discloses what it withheld. (pi's
// models.json half is internal/entrypoint's TestPiModelsJSONCarriesNoMultiRouteCredential,
// where the render runs.)
func TestGateDoneCondition1PiOnZaiSeesNoAWS(t *testing.T) {
	jail, _, disclosed := launchGateJail(t, []string{"claude", "pi", "zai"},
		func(o *Options) { o.ProfileName = "zai" })

	pi := jail.agentEnv("pi")
	if pi["ZAI_API_KEY"] != "tok-gate" {
		t.Errorf("pi selected zai and must receive its key, got %q", pi["ZAI_API_KEY"])
	}
	requireAbsent(t, "pi", pi, awsNames...)
	shell := jail.shellEnv()
	requireAbsent(t, "a bare shell", shell, append(awsNames, "ZAI_API_KEY")...)
	if shell["GH_TOKEN"] != "gh-gate" {
		t.Errorf("a variable no provider claims must still reach every process, got %q", shell["GH_TOKEN"])
	}
	for _, want := range []string{"Credential scope", "AWS_ACCESS_KEY_ID", "withheld", "bedrock"} {
		if !strings.Contains(disclosed, want) {
			t.Errorf("the launch must disclose what it withheld (%q missing):\n%s", want, disclosed)
		}
	}
	if strings.Contains(disclosed, "AKIA-gate") || strings.Contains(disclosed, "tok-gate") {
		t.Errorf("the disclosure printed a credential VALUE:\n%s", disclosed)
	}
}

// DONE CONDITIONS 2 AND 3: `-p codex=bedrock` with aws-auth selected. codex gets the Bedrock
// credentials and aws-auth's pointer; claude and a bare shell get neither the pointer, nor
// claude's CLAUDE_CODE_USE_BEDROCK, nor the AWS pair — trap D2 closed. The control is
// claude selecting bedrock itself, which must still hand claude its own flag, so the
// absences above are the gate and not a flag that stopped existing.
func TestGateDoneConditions2And3CodexOnBedrock(t *testing.T) {
	packs := []string{"claude", "codex", "aws-auth"}
	jail, _, _ := launchGateJail(t, packs,
		func(o *Options) { o.UseProfiles = map[string]string{"codex": "bedrock"} })

	codex := jail.agentEnv("codex")
	for name, want := range map[string]string{
		"AWS_CONTAINER_CREDENTIALS_FULL_URI": "http://127.0.0.1:1461/credentials",
		"AWS_ACCESS_KEY_ID":                  "AKIA-gate",
		"AWS_SECRET_ACCESS_KEY":              "secret-gate",
	} {
		if codex[name] != want {
			t.Errorf("codex selected bedrock: %s = %q, want %q", name, codex[name], want)
		}
	}
	requireAbsent(t, "codex", codex, "CLAUDE_CODE_USE_BEDROCK")
	requireAbsent(t, "claude", jail.agentEnv("claude"), append(awsNames, "CLAUDE_CODE_USE_BEDROCK")...)
	requireAbsent(t, "a bare shell", jail.shellEnv(), append(awsNames, "CLAUDE_CODE_USE_BEDROCK")...)

	control, _, _ := launchGateJail(t, packs,
		func(o *Options) { o.UseProfiles = map[string]string{"claude": "bedrock"} })
	claude := control.agentEnv("claude")
	if claude["CLAUDE_CODE_USE_BEDROCK"] != "1" || claude["AWS_CONTAINER_CREDENTIALS_FULL_URI"] == "" {
		t.Errorf("control: claude selecting bedrock must receive its own flag and the pointer, "+
			"got flag=%q pointer=%q", claude["CLAUDE_CODE_USE_BEDROCK"], claude["AWS_CONTAINER_CREDENTIALS_FULL_URI"])
	}
	requireAbsent(t, "codex (control)", control.agentEnv("codex"), append(awsNames, "CLAUDE_CODE_USE_BEDROCK")...)
}

// DONE CONDITION 4, at Run's own call sites: the fresh container launch and the attach both
// deliver through deliverChannel (the one writer of the shared file's gated half and of
// every agent file) and print the gate's disclosure beside it. runContainer starts a real
// container, so the call graph is the witness, the answer this package already gives for
// checkProviderCredentials (TestFreshLaunchChecksProviderCredentialsOnTheAssembledEnv).
// Deleting either call, or writing the shared file directly again, fails here.
func TestTheLaunchPathsDeliverThroughTheGate(t *testing.T) {
	for _, fn := range []string{"runContainer", "deliverChannelOnAttach"} {
		t.Run(fn, func(t *testing.T) {
			decl := methodDecl(t, "run.go", fn)
			calls := map[string]int{}
			ast.Inspect(decl, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				switch f := call.Fun.(type) {
				case *ast.Ident:
					calls[f.Name]++
				case *ast.SelectorExpr:
					calls[f.Sel.Name]++
				}
				return true
			})
			if calls["deliverChannel"] != 1 {
				t.Errorf("%s calls deliverChannel %d times, want once — the gate's delivery "+
					"is the channel's only crossing", fn, calls["deliverChannel"])
			}
			if calls["writeUserEnvFile"] != 0 {
				t.Errorf("%s writes yolo-user-env.sh directly — a writer handed the hydration "+
					"instead of the gate's shared half puts every credential in every process", fn)
			}
			if calls["noteCredentialScope"] != 1 {
				t.Errorf("%s must disclose the gate's scope once, beside the delivery; got %d",
					fn, calls["noteCredentialScope"])
			}
		})
	}
}

// A credential the gate WITHHOLDS is not delivered, so it overrides nothing: the env-override
// pre-flight reads "delivered" off the gate (deliverySource → DeliversEnvSource), and a
// token claimed by a provider no agent selected must not refuse a launch whose jail never
// sees it. The control moves the claim to the selected provider, which delivers the token to
// the agent beside the pointer it overrides — and that still refuses.
func TestEnvOverrideIgnoresACredentialTheGateWithholds(t *testing.T) {
	widget := func(claimer string) *packload.Pack {
		return inlinePack(t, "widgetpack", `{"name": "widgetpack", "contributes": [
    {"kind": "program", "bin": "someagent", "via": "npm", "package": "@example/someagent",
     "protocols": ["openai"]},
    {"kind": "provider", "name": "gatedprofile",`+claimer+`
     "endpoints": {"openai": {"base_url": "https://api.example.test/v1"}}},
    {"kind": "provider", "name": "elsewhere", "api_key_env_name": "WIDGET_TOKEN"},
    {"kind": "profile", "name": "gatedprofile", "provider": "gatedprofile"},
    {"kind": "env", "profile": "gatedprofile", "vars": {"WIDGET_POINTER": "http://127.0.0.1:1461/x"},
     "overridden_by": [{"vars": ["WIDGET_TOKEN"], "because": "the token wins"}]}]}`)
	}
	token := userEnvWith(map[string]string{"WIDGET_TOKEN": "frozen"})
	for _, tc := range []struct {
		name, claimer string
		refuse        bool
	}{
		{"claimed by a provider nobody selected", ``, false},
		{"claimed by the selected provider too", ` "api_key_env_name": "WIDGET_TOKEN",`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := overrideOptions(t)
			o.Getenv = shellWith(nil)
			o.UseProfiles = map[string]string{"someagent": "gatedprofile"}
			packs := []*packload.Pack{widget(tc.claimer)}
			cfg := newConfig()
			lines := o.checkEnvOverrides(cfg, "podman", packs, channelFor(t, o, cfg, packs, token), nil)
			if got := len(lines) != 0; got != tc.refuse {
				t.Errorf("refused = %v, want %v:\n%s", got, tc.refuse, strings.Join(lines, "\n"))
			}
		})
	}
}

// The macos-user launch env keeps that backend's precedence under the gate: env_sources is
// LAST, where macosuser.buildPlan layered its own hydration before the gate took that call
// away, so a user's own dotenv entry still beats a composed provider variable there; and
// the launched agent's own claimed credential rides the session while another's does not.
func TestLaunchEnvLayersTheGatedEnvSourcesLast(t *testing.T) {
	home := packHome(t)
	o := goldenOptions(t.TempDir(), home)
	o.ProfileName = "zai"
	userEnv := gateCredentials()
	userEnv.Set("ANTHROPIC_BASE_URL", "https://mine.example")
	channel := channelFor(t, o, bareConfig(), zaiSelected(t), userEnv)

	env := channel.launchEnv("claude")
	if envAt(env, "ANTHROPIC_BASE_URL") != "https://mine.example" {
		t.Errorf("a user's own env_sources value must win over the composed provider variable; "+
			"ANTHROPIC_BASE_URL = %q", envAt(env, "ANTHROPIC_BASE_URL"))
	}
	if envAt(env, "ZAI_API_KEY") != "tok-gate" {
		t.Errorf("claude selected zai and launches here: its key must ride the session")
	}
	if envAt(env, "AWS_ACCESS_KEY_ID") != "" {
		t.Errorf("no agent selected bedrock, so its pair rides no session")
	}
	if shell := channel.launchEnv("zsh"); envAt(shell, "ZAI_API_KEY") != "" || envAt(shell, "ANTHROPIC_AUTH_TOKEN") != "" {
		t.Errorf("a shell launch carries no agent's scoped values: %v", shell.Keys())
	}
}

// DONE CONDITION 5, the macos-user vehicle, driven through Run: the sandbox session
// carries the gate's shared values plus the LAUNCHED agent's own, and never another's.
// pi on zai launching pi gets zai's key; launching claude, which selected nothing, gets
// neither zai's key nor the AWS pair — and the launch says so, twice: the gate's
// disclosure, and this backend's per-launch cost.
func TestMacosUserLaunchCarriesOnlyTheLaunchedAgentsCredentials(t *testing.T) {
	for _, tc := range []struct {
		launched string
		wantZai  bool
	}{{"pi", true}, {"claude", false}} {
		t.Run(tc.launched, func(t *testing.T) {
			home := packHome(t)
			writeUserConfig(t, home, `{"packs": ["claude", "pi", "zai"], "env_sources": [`+
				`{"AWS_ACCESS_KEY_ID": "AKIA-gate", "AWS_SECRET_ACCESS_KEY": "secret-gate", `+
				`"ZAI_API_KEY": "tok-gate", "GH_TOKEN": "gh-gate"}]}`)
			var stdout, stderr bytes.Buffer
			o := dispatchOptions(t, t.TempDir(), "macos-user", &stdout, &stderr, nil)
			o.Args = []string{tc.launched}
			o.UseProfiles = map[string]string{"pi": "zai"}
			var got *jsonx.OrderedMap
			o.MacosUserRun = func(_ *jsonx.OrderedMap, _ string, _, _ []string, _, _, _ string,
				_ macosuser.HostContext, _ bool, packEnv *jsonx.OrderedMap, _ []packload.BlockedTool) int {
				got = packEnv
				return 0
			}
			if rc := Run(*o); rc != 0 {
				t.Fatalf("Run() = %d\nstdout:\n%s\nstderr:\n%s", rc, stdout.String(), stderr.String())
			}
			if got == nil {
				t.Fatal("Run never handed the macos-user backend a launch env")
			}
			for _, k := range []string{"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY"} {
				if envAt(got, k) != "" {
					t.Errorf("launching %s carried %s, which no agent selected", tc.launched, k)
				}
			}
			if gotZai := envAt(got, "ZAI_API_KEY") == "tok-gate"; gotZai != tc.wantZai {
				t.Errorf("launching %s: ZAI_API_KEY delivered = %v, want %v", tc.launched, gotZai, tc.wantZai)
			}
			if envAt(got, "GH_TOKEN") != "gh-gate" {
				t.Error("an unclaimed env_sources value must still reach the sandbox")
			}
			errs := stderr.String()
			if !strings.Contains(errs, "Credential scope") {
				t.Errorf("the macos-user launch must disclose the gate's scope:\n%s", errs)
			}
			if !tc.wantZai && !strings.Contains(errs, "per launch") {
				t.Errorf("launching %s while pi holds values of its own must say this backend "+
					"delivers per launch:\n%s", tc.launched, errs)
			}
		})
	}
}
