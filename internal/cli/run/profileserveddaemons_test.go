package run

// profileserveddaemons_test.go pins OQ-CN7 (docs/reference/providers.md, ruled
// 2026-09-28) on the launcher: (b) aws-auth's in-jail credential adapter starts only when some
// agent's profile selects what it serves, and says so when it does not; (c) its caller token is
// scoped to the agents that selected it, the adapter demands it, and an agent that did not
// select it — or a bare shell — is refused. Driven through the real composition over the
// shipped packs (jailDaemonsFor, composePackChannel, deliverChannel) and the real launchers,
// against the real adapter handler on an httptest server. No AWS call is made: the handler's
// fetch is a fake.
//
// The wire bridge's half of (b) was already true when CN7 was ruled — its daemon is
// selection-lazy and publishes a route only for a selected provider (wire-bridge.md §3.4) — and
// is pinned through the same assembly by TestBridgedLaunchComposesTheWholeStory and
// TestBridgeStagedButUnroutedIdlesAndEmitsNothing (wirebridgepack_test.go), with the daemon's
// own decision in internal/wirebridged's TestWillServeTruthTable.

import (
	"bytes"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/awscredadapter"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// awsPayloadNames composes the jail-daemon payload a podman launch of packs would run, with
// the aws-auth loophole enabled, and returns its daemon names and what the launch disclosed.
func awsPayloadNames(t *testing.T, packs []*packload.Pack, tune func(*Options)) (map[string]bool, string) {
	t.Helper()
	o := goldenOptions(t.TempDir(), packHome(t))
	var stderr bytes.Buffer
	o.Stderr = &stderr
	if tune != nil {
		tune(o)
	}
	cfg := awsAuthServedConfig(t, packs)
	names := map[string]bool{}
	for _, s := range o.jailDaemonsFor(cfg, "podman", packs) {
		names[s.Name] = true
	}
	o.noteUnstartedProfileDaemons()
	return names, stderr.String()
}

// A LATER ENTRY OF AN ACTIVE SET starts the daemon its platform serves (docs/design/active-
// provider-sets.md AP-P1): pi on [zai, bedrock] receives aws-auth's pointer for its second entry,
// so the adapter behind it must join the payload. daemonSelection builds its view over each
// agent's whole set; over the primary alone it sees zai, and the pointer pi is handed would name
// an adapter that never started.
func TestTheAWSAdapterStartsForALaterEntryOfASet(t *testing.T) {
	packs := []*packload.Pack{officialPack(t, "pi"), officialPack(t, "zai"),
		officialPack(t, "aws-auth"), officialPack(t, "bedrock")}
	names, said := awsPayloadNames(t, packs,
		func(o *Options) { o.UseProfiles = map[string]string{"pi": "zai,bedrock"} })
	if !names["aws-auth"] {
		t.Errorf("pi's set holds bedrock second, so the aws-auth adapter must join the payload:\n%s", said)
	}
	if strings.Contains(said, "Not started") {
		t.Errorf("a started adapter was disclosed as not started:\n%s", said)
	}
}

// CN7 (b): enabling the loophole no longer starts the adapter; selecting `bedrock` does. The
// OpenAI refresh adapter, whose pointer codex receives ungated, is not a profile-served daemon
// and starts either way.
func TestTheAWSAdapterStartsOnlyWhenAProfileSelectsWhatItServes(t *testing.T) {
	packs := []*packload.Pack{officialPack(t, "claude"), officialPack(t, "codex"),
		officialPack(t, "openai-auth"), officialPack(t, "aws-auth"), officialPack(t, "bedrock")}

	unselected, said := awsPayloadNames(t, packs, nil)
	if unselected["aws-auth"] {
		t.Error("the aws-auth adapter joined the payload although no agent selected bedrock")
	}
	if !unselected["openai-auth-broker"] {
		t.Error("the OpenAI refresh adapter must still start: codex's pointer to it is ungated")
	}
	// Keyed on the provider's platform since OQ-BR8, and the remedy names a declared profile
	// over a provider of it.
	for _, want := range []string{"Not started", "aws-auth", `platform "aws-bedrock"`,
		"`-p <agent>=bedrock`", "OQ-CN7"} {
		if !strings.Contains(said, want) {
			t.Errorf("the launch must say what it did not start and why (%q missing):\n%s", want, said)
		}
	}

	selected, said := awsPayloadNames(t, packs,
		func(o *Options) { o.UseProfiles = map[string]string{"codex": "bedrock"} })
	if !selected["aws-auth"] {
		t.Error("codex selected bedrock, so the aws-auth adapter must join the payload")
	}
	if strings.Contains(said, "Not started") {
		t.Errorf("a started adapter was disclosed as not started:\n%s", said)
	}
	// A config-side selection is the same selection.
	cfgSelected, _ := awsPayloadNames(t, packs, func(o *Options) { o.ProfileName = "bedrock" })
	if !cfgSelected["aws-auth"] {
		t.Error("a bare -p bedrock selects bedrock for every agent and must start the adapter")
	}
}

// KEYED ON THE PROVIDER'S PLATFORM (OQ-BR8): the adapter starts for a user's own profile over
// the shipped `bedrock` provider (trap D5's `bedrock-sso`) and for a user's own provider that
// declares "platform": "aws-bedrock", neither of which is named `bedrock`. The payload is decided
// before the channel composes, so this pins that its selection (daemonSelection) resolves the
// provider and its platform as the gate does; a profile-names-only selection starts neither.
func TestTheAWSAdapterStartsForEveryBedrockSelection(t *testing.T) {
	packs := []*packload.Pack{officialPack(t, "claude"), officialPack(t, "codex"),
		officialPack(t, "openai-auth"), officialPack(t, "aws-auth"), officialPack(t, "bedrock")}
	for _, profile := range []string{"bedrock-sso", "eu"} {
		o := goldenOptions(t.TempDir(), packHome(t))
		var stderr bytes.Buffer
		o.Stderr = &stderr
		writeUserConfig(t, os.Getenv("HOME"), `{"profiles": {"bedrock-sso": {"provider": "bedrock"},
		  "eu": {"provider": "bedrock-eu"}}}`)
		o.UseProfiles = map[string]string{"claude": profile}
		cfg := awsAuthServedConfig(t, packs)
		providers := jsonx.NewOrderedMap()
		eu := jsonx.NewOrderedMap()
		eu.Set("platform", "aws-bedrock")
		eu.Set("region", "eu-west-1")
		providers.Set("bedrock-eu", eu)
		cfg.Set("providers", providers)
		started := false
		for _, s := range o.jailDaemonsFor(cfg, "podman", packs) {
			started = started || s.Name == "aws-auth"
		}
		if !started {
			t.Errorf("claude on %s (a Bedrock provider) must start the aws-auth adapter", profile)
		}
	}
}

// The disclosure has a call site on each arm that starts a launch's daemons — the fresh
// container path and the macos-user arm — so deleting either fails here (runContainer starts a
// real container, so the call graph is the witness, as in TestTheLaunchPathsDeliverThroughTheGate).
func TestTheUnstartedDaemonDisclosureIsPrintedByEachFreshArm(t *testing.T) {
	for _, fn := range []string{"runContainer", "Run"} {
		t.Run(fn, func(t *testing.T) {
			decl := funcDeclIn(t, "run.go", fn)
			found := false
			ast.Inspect(decl, func(n ast.Node) bool {
				if call, ok := n.(*ast.CallExpr); ok {
					if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "noteUnstartedProfileDaemons" {
						found = true
					}
				}
				return true
			})
			if !found {
				t.Errorf("%s never prints noteUnstartedProfileDaemons — a daemon left out would go unsaid", fn)
			}
		})
	}
}

// funcDeclIn finds a function or method declaration by name in one of this package's files.
func funcDeclIn(t *testing.T, file, name string) *ast.FuncDecl {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", file, err)
	}
	for _, decl := range f.Decls {
		if fd, ok := decl.(*ast.FuncDecl); ok && fd.Name.Name == name {
			return fd
		}
	}
	t.Fatalf("%s has no function %s", file, name)
	return nil
}

// AN ATTACH STARTS NO DAEMON: one whose selection needs the adapter, into a jail whose launch
// did not start it, takes the attach-skew disposition instead of handing codex a pointer to
// nothing; the same attach into a jail that runs it proceeds.
func TestAnAttachNeedingAnUnstartedAdapterTakesTheSkewDisposition(t *testing.T) {
	packs := []*packload.Pack{officialPack(t, "claude"), officialPack(t, "codex"), officialPack(t, "aws-auth"), officialPack(t, "bedrock")}
	enable := func(o *Options, cfg *jsonx.OrderedMap) {
		o.UseProfiles = map[string]string{"codex": "bedrock"}
		v, _ := awsAuthServedConfig(t, packs).Get("loopholes")
		cfg.Set("loopholes", v)
		withBedrockRegion(cfg)
	}
	o, cfg, channel, stderr := attachFixture(t, currentJailEnv, packs, emptyEnv(), enable)
	rc, _ := o.attachExisting("yolo-ws-abcd1234", "podman", "true", cfg,
		stagedPacks{root: "/ctx/packs", packs: packs}, channel, false, nil)
	if rc != 1 {
		t.Fatalf("an attach needing an adapter the jail never started must refuse off a "+
			"terminal, rc=%d\n%s", rc, stderr.String())
	}
	for _, want := range []string{`"aws-auth" jail daemon`, "OQ-CN7", AllowAttachSkewEnv} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("the refusal must name %q:\n%s", want, stderr.String())
		}
	}

	o, cfg, channel, stderr = attachFixture(t, awsAdapterJailEnv, packs, emptyEnv(), enable)
	// A proceeding attach ends at the exec, which a stand-in runtime first on PATH answers
	// (attachToExec) rather than the machine's podman: what matters is that it delivered
	// codex's file rather than refusing.
	_, _, _ = attachToExec(t, o, cfg, packs, channel)
	if strings.Contains(stderr.String(), "jail daemon that this") {
		t.Fatalf("an attach into a jail that runs the adapter took the skew disposition:\n%s", stderr.String())
	}
	if _, err := os.Stat(filepath.Join(paths.WorkspaceHomeState(o.Workspace), agentEnvStateDir, "codex.sh")); err != nil {
		t.Errorf("the attach into a jail that runs the adapter delivered no file for codex: %v\n%s", err, stderr.String())
	}
}

// missingProfileServedDaemons claims nothing it cannot see: an unreadable container
// environment is no evidence of a missing daemon.
func TestMissingProfileServedDaemonsNeedsTheFrozenEnvironment(t *testing.T) {
	packs := []*packload.Pack{officialPack(t, "aws-auth")}
	specs := []loopholes.JailDaemonSpec{{Name: "aws-auth"}}
	if got := missingProfileServedDaemons(nil, specs, packs); got != nil {
		t.Errorf("an unread environment reported %v missing", got)
	}
	if got := missingProfileServedDaemons([]string{"YOLO_VERSION=1"}, specs, packs); len(got) != 1 {
		t.Errorf("a jail with no payload lacks the adapter, got %v", got)
	}
}

// CN7 (c), END TO END: codex selects bedrock; the launch mints the adapter's token, records it
// (never exports it) in the shared file, and exports it only as codex's
// AWS_CONTAINER_AUTHORIZATION_TOKEN. The adapter reads the record exactly as it does at boot
// (entrypoint.ScopedCallerToken, from the jail home the files are laid into) and serves codex's
// request, whose Authorization is that value as the AWS SDK sends it; claude's and a bare
// shell's environments carry no token, so a request from either is refused 401 before the
// host is asked.
func TestTheAWSAdapterServesOnlyTheAgentThatSelectedBedrock(t *testing.T) {
	jail, channel, _ := launchGateJail(t, []string{"claude", "codex", "aws-auth", "bedrock"},
		func(o *Options) { o.UseProfiles = map[string]string{"codex": "bedrock"} })
	tokenEnv := paths.ServiceCallerTokenEnv("aws-auth")
	minted := channel.callerTokens[tokenEnv]
	if minted == "" {
		t.Fatalf("the launch minted no caller token for the adapter: %v", channel.callerTokens)
	}

	demanded := entrypoint.ScopedCallerToken(jail.home, tokenEnv)
	if demanded != minted {
		t.Fatalf("the adapter would read %q from the shared file, want this launch's token", demanded)
	}
	asked := 0
	srv := httptest.NewServer(awscredadapter.Handler(demanded, func() (awscredadapter.Answer, error) {
		asked++
		body, _ := json.Marshal(map[string]string{"AccessKeyId": "AKIA-fake", "SecretAccessKey": "s",
			"Token": "t", "Expiration": "2099-01-01T00:00:00Z"})
		return awscredadapter.Answer{OK: true, Body: body}, nil
	}))
	defer srv.Close()
	get := func(authorization string) int {
		req, _ := http.NewRequest(http.MethodGet, srv.URL+awscredadapter.CredentialsPath, nil)
		if authorization != "" {
			req.Header.Set("Authorization", authorization) // verbatim, as the SDK sends it
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}

	codex := jail.agentEnv("codex")
	if codex["AWS_CONTAINER_AUTHORIZATION_TOKEN"] != minted {
		t.Fatalf("codex selected bedrock: AWS_CONTAINER_AUTHORIZATION_TOKEN = %q, want the minted token",
			codex["AWS_CONTAINER_AUTHORIZATION_TOKEN"])
	}
	if _, ok := codex["AWS_CONTAINER_AUTHORIZATION_TOKEN_FILE"]; ok {
		t.Error("codex carries AWS_CONTAINER_AUTHORIZATION_TOKEN_FILE, which the SDK prefers over the token")
	}
	if code := get(codex["AWS_CONTAINER_AUTHORIZATION_TOKEN"]); code != http.StatusOK {
		t.Errorf("codex's request = %d, want 200", code)
	}
	for who, env := range map[string]map[string]string{
		"claude": jail.agentEnv("claude"), "a bare shell": jail.shellEnv(),
	} {
		for k, v := range env {
			if v == minted {
				t.Errorf("%s's environment carries the adapter's token as %s", who, k)
			}
		}
		if code := get(env["AWS_CONTAINER_AUTHORIZATION_TOKEN"]); code != http.StatusUnauthorized {
			t.Errorf("a request from %s = %d, want 401", who, code)
		}
	}
	if asked != 1 {
		t.Errorf("the host was asked %d times, want once (codex's request only)", asked)
	}
	// Nothing in the jail home names the token file the boot used to write for every process.
	shared, _ := os.ReadFile(filepath.Join(jail.home, ".config", "yolo-user-env.sh"))
	if strings.Contains(string(shared), "export "+tokenEnv) {
		t.Errorf("the shared file exports the scoped token:\n%s", shared)
	}
}

// THE SCOPED TOKEN SURVIVES ATTACHES (OQ-CN7 (c)). The running adapter read its token from the
// shared file's record once at boot, so every later entry must deliver THAT token — found in the
// record, since no process's environment carries it — and an entry that selects no bedrock must
// still carry the record forward, or the entry after it, selecting bedrock again, would mint a
// token the running adapter refuses. Deleting the record read in runningCallerTokens, or the
// carry in rekeyChannelForAttach, fails it.
func TestAnAttachKeepsTheRunningAdaptersScopedToken(t *testing.T) {
	packs := []*packload.Pack{officialPack(t, "claude"), officialPack(t, "codex"), officialPack(t, "aws-auth"), officialPack(t, "bedrock")}
	tokenEnv := paths.ServiceCallerTokenEnv("aws-auth")
	running := strings.Repeat("5a", 32)
	wsStateOf := func(o *Options) string { return paths.WorkspaceHomeState(o.Workspace) }
	attach := func(profiles map[string]string, seed func(wsState string)) (string, map[string]string, string) {
		t.Helper()
		o, cfg, channel, stderr := attachFixture(t, awsAdapterJailEnv, packs, emptyEnv(),
			func(o *Options, cfg *jsonx.OrderedMap) {
				o.UseProfiles = profiles
				v, _ := awsAuthServedConfig(t, packs).Get("loopholes")
				cfg.Set("loopholes", v)
				withBedrockRegion(cfg)
			})
		seed(wsStateOf(o))
		_, _, _ = attachToExec(t, o, cfg, packs, channel)
		shared, err := os.ReadFile(filepath.Join(wsStateOf(o), "yolo-user-env.sh"))
		if err != nil {
			t.Fatalf("the attach wrote no shared file: %v\n%s", err, stderr.String())
		}
		agents := map[string]string{}
		entries, _ := os.ReadDir(filepath.Join(wsStateOf(o), agentEnvStateDir))
		for _, e := range entries {
			b, _ := os.ReadFile(filepath.Join(wsStateOf(o), agentEnvStateDir, e.Name()))
			agents[strings.TrimSuffix(e.Name(), ".sh")] = string(b)
		}
		return string(shared), agents, stderr.String()
	}
	seedRecord := func(wsState string) {
		if err := os.MkdirAll(wsState, 0o700); err != nil {
			t.Fatal(err)
		}
		body := entrypoint.EntryChannelSectionHeader + "\n" + "export YOLO_PROVIDERS='{}'\n" +
			"export YOLO_PROFILES='{}'\nexport YOLO_USE_PROFILES='{}'\n" +
			entrypoint.ScopedCallerTokenRecord(tokenEnv, running)
		if err := os.WriteFile(filepath.Join(wsState, "yolo-user-env.sh"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	// An entry selecting bedrock again hands codex the running adapter's token.
	shared, agents, said := attach(map[string]string{"codex": "bedrock"}, seedRecord)
	if !strings.Contains(agents["codex"], "'"+running+"'") {
		t.Errorf("codex's file does not carry the running adapter's token:\n%s\n%s", agents["codex"], said)
	}
	if got := entrypoint.ParseScopedCallerTokens([]byte(shared))[tokenEnv]; got != running {
		t.Errorf("the shared file records %q, want the running adapter's token", got)
	}

	// An entry selecting no bedrock needs no token of its own, and still carries the record.
	shared, agents, said = attach(nil, seedRecord)
	if got := entrypoint.ParseScopedCallerTokens([]byte(shared))[tokenEnv]; got != running {
		t.Errorf("a deselecting attach dropped the running adapter's token (record %q):\n%s\n%s",
			got, shared, said)
	}
	if strings.Contains(shared, "export "+tokenEnv) {
		t.Errorf("the carried token was exported:\n%s", shared)
	}
	for agent, body := range agents {
		if strings.Contains(body, running) {
			t.Errorf("%s received the token although no agent selected bedrock:\n%s", agent, body)
		}
	}
}
