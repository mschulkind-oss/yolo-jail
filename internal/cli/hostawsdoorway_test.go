package cli

// hostawsdoorway_test.go pins the `yolo host` launch's AWS DOORWAY
// (docs/design/host-notch-services.md HS-D15, the doorway rule, and HS-D21, its host build): for
// an agent whose profile selects a Bedrock provider, with aws-auth enabled, `yolo host --` opens
// aws-auth's container-credentials adapter as a launch-owned listener on a port it picked,
// behind a caller token it minted, forwarding to the real aws-auth host service through a front
// of the launch's own, and hands the agent AWS_CONTAINER_CREDENTIALS_FULL_URI and
// AWS_CONTAINER_AUTHORIZATION_TOKEN for it.
//
// EVERY LINK IS REAL BUT THE AGENT AND `aws`. The launch goes through hostMain; the host service
// is `yolo internal daemon aws-auth`, spawned as the host-wide singleton by the test binary
// standing in for yolo, under this package's private singleton dir (TestMain); the doorway is
// `yolo internal daemon aws-credential-adapter`, the same binary; the agent is this test binary
// in its fake-AWS-agent mode, which reads the environment it was handed and fetches the
// credential the way an AWS SDK's container provider does (GET the URI, the token verbatim as
// `Authorization`). `aws` is a shell script printing a `--format process` body. No agent CLI
// runs, and no request leaves loopback.
//
// ⚠ WHAT THIS DOES NOT PROVE. The host notch has no network namespace, so the loopback here is
// the machine's and there is no forwarding hop to get wrong: these tests settle the wiring, and
// nothing about a jail's reachability. Nor do they show that any agent's own client reads the
// pointer: that is read from each agent's published code (host-notch-services.md §4.8), never run.

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/broker"
	"github.com/mschulkind-oss/yolo-jail/internal/launchservice"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// testFakeAWSAgentArg makes a child of this test binary the fake AWS agent (TestMain).
const testFakeAWSAgentArg = "-yolo-cli-test-fake-aws-agent"

// awsAgentReport is what the fake AWS agent writes to the file YOLO_CLI_TEST_AGENT_DUMP names.
type awsAgentReport struct {
	Env         map[string]string `json:"env"`
	WithToken   int               `json:"with_token"`
	WithoutAuth int               `json:"without_auth"`
	Body        map[string]any    `json:"body"`
	Err         string            `json:"err"`
}

// fakeAWSAgentMain is the fake agent: it records the AWS_* and CLAUDE_CODE_* environment it was
// handed and, when it holds a container-credentials pointer, fetches it twice, once as an AWS
// SDK's container provider does (the token verbatim as `Authorization`) and once with none.
func fakeAWSAgentMain() int {
	rep := awsAgentReport{Env: map[string]string{}}
	for _, kv := range os.Environ() {
		if k, v, ok := strings.Cut(kv, "="); ok &&
			(strings.HasPrefix(k, "AWS_") || strings.HasPrefix(k, "CLAUDE_CODE_")) {
			rep.Env[k] = v
		}
	}
	if uri := rep.Env["AWS_CONTAINER_CREDENTIALS_FULL_URI"]; uri != "" {
		get := func(auth string) (int, []byte) {
			req, _ := http.NewRequest(http.MethodGet, uri, nil)
			if auth != "" {
				req.Header.Set("Authorization", auth)
			}
			client := &http.Client{Timeout: 10 * time.Second}
			resp, err := client.Do(req)
			if err != nil {
				rep.Err = err.Error()
				return -1, nil
			}
			defer resp.Body.Close()
			b, _ := io.ReadAll(resp.Body)
			return resp.StatusCode, b
		}
		var body []byte
		rep.WithToken, body = get(rep.Env["AWS_CONTAINER_AUTHORIZATION_TOKEN"])
		_ = json.Unmarshal(body, &rep.Body)
		rep.WithoutAuth, _ = get("")
	}
	data, _ := json.Marshal(rep)
	_ = os.WriteFile(os.Getenv("YOLO_CLI_TEST_AGENT_DUMP"), data, 0o600)
	return 0
}

// The values the fake `aws` prints, which the doorway must serve back.
const (
	doorwayAccessKey = "ASIAYOLOHOSTDOORWAY"
	doorwaySecret    = "yolo-host-doorway-secret"
	doorwayToken     = "yolo-host-doorway-session-token"
	doorwayProfile   = "yolo-host-doorway"
)

// doorwayLaunch is one `yolo host` run whose agent is the fake AWS agent.
type doorwayLaunch struct {
	rc      int
	errs    string
	report  awsAgentReport
	started []*launchservice.Running
	execed  bool
	argvLog string
}

// bedrockDoorwayConfig is a user config selecting agent on the bedrock profile, with a region on
// the provider and aws-auth enabled un-narrowed for the fake profile (a role needs `aws sts`,
// and the narrowing has its own tests). enabled false leaves the loophole off.
func bedrockDoorwayConfig(agent string, enabled bool) string {
	loophole := `"loopholes": {"aws-auth": {"enabled": true, "settings": {"profile": "` +
		doorwayProfile + `", "unnarrowed": true}}}`
	if !enabled {
		loophole = `"loopholes": {}`
	}
	return `{"packs": ["` + agent + `"], "profile": {"` + agent + `": "bedrock"}, ` +
		`"providers": {"bedrock": {"region": "eu-west-1"}}, ` + loophole + `}`
}

// runDoorwayLaunch runs `yolo host <flags> -- <agent>` over cfg, with shell set in the invoking
// environment, the fake `aws` first on PATH and the fake AWS agent as agent, and returns what
// happened. The aws-auth singleton the launch spawns is stopped when the test ends.
func runDoorwayLaunch(t *testing.T, cfg string, shell map[string]string, flags []string, agent string) doorwayLaunch {
	t.Helper()
	return runDoorwayLaunchAfter(t, cfg, shell, flags, agent, nil)
}

// runDoorwayLaunchAfter is runDoorwayLaunch with before run in the launch's workspace, its cwd,
// just before hostMain.
func runDoorwayLaunchAfter(t *testing.T, cfg string, shell map[string]string, flags []string,
	agent string, before func()) doorwayLaunch {
	t.Helper()
	home := hostGateHome(t, cfg, shell)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	for _, k := range []string{"AWS_PROFILE", "AWS_BEARER_TOKEN_BEDROCK", "AWS_SESSION_TOKEN",
		"AWS_CONTAINER_CREDENTIALS_RELATIVE_URI", "AWS_CONTAINER_AUTHORIZATION_TOKEN_FILE",
		"YOLO_SERVICE_AWS_AUTH_ENDPOINT", "CODEX_REFRESH_TOKEN_URL_OVERRIDE"} {
		if _, set := shell[k]; !set {
			t.Setenv(k, "")
		}
	}
	bin := t.TempDir()
	argvLog := filepath.Join(bin, "argv.log")
	raw, err := json.Marshal(map[string]any{"Version": 1, "AccessKeyId": doorwayAccessKey,
		"SecretAccessKey": doorwaySecret, "SessionToken": doorwayToken,
		"Expiration": time.Now().Add(time.Hour).UTC().Format(time.RFC3339)})
	if err != nil {
		t.Fatal(err)
	}
	processBody := filepath.Join(bin, "process.json")
	if err := os.WriteFile(processBody, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	awsScript := "#!/bin/sh\necho \"$*\" >> '" + argvLog + "'\n" +
		"case \"$1\" in\n" +
		"  --version) echo 'aws-cli/2.99.0 Python/3.12.0 Linux/yolo-test exe/fake'; exit 0 ;;\n" +
		"  configure) cat '" + processBody + "'; exit 0 ;;\n" +
		"esac\nexit 2\n"
	if err := os.WriteFile(filepath.Join(bin, "aws"), []byte(awsScript), 0o755); err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	agentScript := "#!/bin/sh\nexec '" + exe + "' " + testFakeAWSAgentArg + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, agent), []byte(agentScript), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	dump := filepath.Join(t.TempDir(), "agent.json")
	t.Setenv("YOLO_CLI_TEST_AGENT_DUMP", dump)
	// The host service is a machine-wide singleton, so each test stops the one its launch
	// spawned (in this package's private singleton dir), and the next spawns against its own
	// fake `aws`.
	t.Cleanup(func() {
		broker.BrokerKill(broker.SingletonDeps("aws-auth", nil), syscall.SIGTERM, 5*time.Second)
	})

	got := doorwayLaunch{argvLog: argvLog}
	origStart := startLaunchService
	startLaunchService = func(p *launchservice.Plan, env map[string]string) (*launchservice.Running, error) {
		r, err := origStart(p, env)
		if r != nil {
			got.started = append(got.started, r)
		}
		return r, err
	}
	t.Cleanup(func() { startLaunchService = origStart })
	origExec := hostSyscallExec
	hostSyscallExec = func(string, []string, []string) error { got.execed = true; return nil }
	t.Cleanup(func() { hostSyscallExec = origExec })

	if before != nil {
		before()
	}
	var out, errw bytes.Buffer
	got.rc = hostMain(append(append([]string{}, flags...), "--", agent), &out, &errw, false, nil)
	got.errs = errw.String()
	if data, err := os.ReadFile(dump); err == nil {
		_ = json.Unmarshal(data, &got.report)
	}
	return got
}

// assertDoorwayServed checks the headline for one agent: pointed at a loopback port the launch
// picked (never the declared 1461), holding a 64-hex caller token that is on no argv, served
// the fake `aws`'s credential as the four container-credentials keys with the token and refused
// 401 without it, and the doorway gone, its port closed, once the agent exited.
func assertDoorwayServed(t *testing.T, l doorwayLaunch) {
	t.Helper()
	if l.rc != 0 {
		t.Fatalf("rc = %d\n%s", l.rc, l.errs)
	}
	uri := l.report.Env["AWS_CONTAINER_CREDENTIALS_FULL_URI"]
	u, err := url.Parse(uri)
	if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.Port() == "" ||
		u.Port() == "1461" || u.Path != "/credentials" {
		t.Fatalf("AWS_CONTAINER_CREDENTIALS_FULL_URI = %q, want http://127.0.0.1:<a port this "+
			"launch picked>/credentials\n%s", uri, l.errs)
	}
	token := l.report.Env["AWS_CONTAINER_AUTHORIZATION_TOKEN"]
	if len(token) != 64 {
		t.Errorf("AWS_CONTAINER_AUTHORIZATION_TOKEN = %q, want the launch's 64-hex caller token", token)
	}
	if l.report.WithToken != http.StatusOK {
		t.Fatalf("the agent's credential fetch through the doorway got %d (%s): %v\n%s",
			l.report.WithToken, l.report.Err, l.report.Body, l.errs)
	}
	var keys []string
	for k := range l.report.Body {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if strings.Join(keys, ",") != "AccessKeyId,Expiration,SecretAccessKey,Token" {
		t.Errorf("served keys = %v, want exactly the container-credentials four", keys)
	}
	if l.report.Body["AccessKeyId"] != doorwayAccessKey || l.report.Body["Token"] != doorwayToken {
		t.Errorf("served AccessKeyId/Token differ from what the host's `aws` printed: %v", l.report.Body)
	}
	if l.report.WithoutAuth != http.StatusUnauthorized {
		t.Errorf("a fetch without the caller token got %d, want 401", l.report.WithoutAuth)
	}
	if len(l.started) != 1 || l.started[0].Plan.Service != "aws-auth" || l.execed {
		t.Fatalf("started %d launch-owned listeners, exec'd %v; want the aws-auth doorway as a "+
			"child and no exec\n%s", len(l.started), l.execed, l.errs)
	}
	for _, a := range l.started[0].Argv {
		if strings.Contains(a, token) {
			t.Errorf("the caller token is on the doorway's argv: %q", l.started[0].Argv)
		}
	}
	for _, want := range []string{`opened the "aws-auth" doorway (pack "aws-auth"`, u.Host,
		"answers only this launch's caller token", "This launch runs pack code on your machine"} {
		if !strings.Contains(l.errs, want) {
			t.Errorf("the launch must disclose the doorway it opens (%q):\n%s", want, l.errs)
		}
	}
	// The pointer is served, so the "Not set at this notch" disclosure no longer names it.
	if strings.Contains(l.errs, "AWS_CONTAINER_CREDENTIALS_FULL_URI") {
		t.Errorf("the launch still names the AWS pointer as withheld while it serves it:\n%s", l.errs)
	}
	select {
	case <-l.started[0].Done():
	default:
		t.Errorf("the doorway (pid %d) outlived its launch", l.started[0].PID())
	}
	if c, err := net.DialTimeout("tcp", u.Host, 200*time.Millisecond); err == nil {
		_ = c.Close()
		t.Errorf("something still listens at %s after the launch", u.Host)
	}
	argv, err := os.ReadFile(l.argvLog)
	if err != nil || !strings.Contains(string(argv), "configure export-credentials --profile "+doorwayProfile) {
		t.Errorf("the host's `aws` was not asked for the configured profile (the doorway did not "+
			"forward to the host service): %q, %v", argv, err)
	}
}

// THE MAINTAINER'S LAUNCH: `yolo host -- pi` with `"profile": {"pi": "bedrock"}`. pi receives the
// pointer and its token, the doorway serves the host service's credential with the token and
// refuses without it, the region the provider declares reaches pi as AWS_REGION exactly as in a
// jail, and the fronts and the session dir the launch made are gone once pi exits.
func TestHostPiOnBedrockGetsCredentialsThroughALaunchOwnedDoorway(t *testing.T) {
	l := runDoorwayLaunch(t, bedrockDoorwayConfig("pi", true), map[string]string{"AWS_REGION": ""}, nil, "pi")
	assertDoorwayServed(t, l)
	if got := l.report.Env["AWS_REGION"]; got != "eu-west-1" {
		t.Errorf("pi's AWS_REGION = %q, want the provider's eu-west-1 (pi's derive, as in a jail)", got)
	}
	// THE PROFILE LINE says pi's selection resolved to a provider pi reaches, and warns of
	// nothing: the doorway's pointer is a credential that reaches it.
	if want := `yolo host: Profile bedrock: declared by bedrock; pi → provider "bedrock", through ` +
		`pi's own "aws-bedrock" client`; !strings.Contains(l.errs, want) {
		t.Errorf("the profile line must say %q:\n%s", want, l.errs)
	}
	if strings.Contains(l.errs, "Warning: profile") {
		t.Errorf("a pi the doorway serves is warned about its profile:\n%s", l.errs)
	}
	sessions, _ := filepath.Glob(paths.HostServicesSessionGlob(paths.HostServicesBase(false)))
	for _, s := range sessions {
		if _, err := os.Stat(filepath.Join(s, "aws-auth"+paths.ServiceEndpointExt)); err == nil {
			t.Errorf("the launch's session dir %s outlived it, its aws-auth endpoint with it", s)
		}
	}
}

// THE PORT A `yolo host` LAUNCH PICKED FOR ITS AWS DOORWAY IS STILL THE DOORWAY'S WHEN ANOTHER
// LISTENER ASKS FOR IT FIRST. This launch fronts the aws-auth host service between the pick and the
// doorway's start, and a front binds port 0, so before the doorway held its pick the front could be
// handed the doorway's port. run.PlanHostDoorways' pick, the real doorway, and pi served through
// it (askForPlannedPortsFirst says how the race is made deterministic).
func TestHostAWSDoorwaysPickedPortIsStillItsOwnWhenAnotherListenerAsksFirst(t *testing.T) {
	var taken *[]string
	l := runDoorwayLaunchAfter(t, bedrockDoorwayConfig("pi", true), map[string]string{"AWS_REGION": ""},
		nil, "pi", func() { taken = askForPlannedPortsFirst(t) })
	if len(*taken) > 0 {
		t.Errorf("another listener bound %v, the port this launch picked for its doorway, before "+
			"the doorway opened: the pick let it go", *taken)
	}
	assertDoorwayServed(t, l)
}

// THE FRONT IS THE LAUNCH'S OWN, NEVER THE WORKSPACE'S JAIL'S. A `yolo host` launch that opens a
// doorway publishes its aws-auth front in a host-services dir of its own session, as a macos-user
// session does (startLoopholesMatching's session arm). The workspace-keyed dir is a container
// jail's: a jail of this workspace that is running has it mounted, with its own aws-auth.endpoint
// in it. Published there, the host launch would write over the jail's endpoint and unlink it
// when its agent exits, leaving the jail's in-jail adapter with no front and no warning.
func TestHostDoorwayFrontsInASessionDirNotTheWorkspacesJailDir(t *testing.T) {
	const jailEndpoint = "a running jail's own aws-auth front"
	var jailDir string
	l := runDoorwayLaunchAfter(t, bedrockDoorwayConfig("pi", true), nil, nil, "pi", func() {
		ws, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		jailDir = paths.HostServicesDir(runtime.FromWorkspace(ws), paths.IsMacOS)
		if err := os.MkdirAll(jailDir, 0o700); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.RemoveAll(jailDir) })
		endpoint := filepath.Join(jailDir, "aws-auth"+paths.ServiceEndpointExt)
		if err := os.WriteFile(endpoint, []byte(jailEndpoint), 0o600); err != nil {
			t.Fatal(err)
		}
	})
	assertDoorwayServed(t, l)
	got, err := os.ReadFile(filepath.Join(jailDir, "aws-auth"+paths.ServiceEndpointExt))
	if err != nil || string(got) != jailEndpoint {
		t.Errorf("the host launch touched the workspace jail's aws-auth endpoint in %s: %q, %v",
			jailDir, got, err)
	}
	const using = `using the host-wide "aws-auth" service for this launch, through a front of its own that closes when pi exits: `
	i := strings.Index(l.errs, using)
	if i < 0 {
		t.Fatalf("the launch must name the front it published:\n%s", l.errs)
	}
	published, _, _ := strings.Cut(l.errs[i+len(using):], "\n")
	if matched, _ := filepath.Match(paths.HostServicesSessionGlob(paths.HostServicesBase(paths.IsMacOS)),
		filepath.Dir(published)); !matched || filepath.Dir(published) == jailDir {
		t.Errorf("the front was published at %s, want a session dir of the launch's own (not %s)",
			published, jailDir)
	}
}

// EVERY AGENT WITH A BEDROCK CLIENT OF ITS OWN gets the same doorway at the host: claude (with
// its Bedrock switch), codex and opencode, each through `-p bedrock` as well as the `profile` key.
// codex's refresh pointer, which no host doorway serves for a launch like this, is still named
// as withheld: the line stops listing only what the launch now serves.
func TestHostOpensTheAWSDoorwayForEveryBedrockAgent(t *testing.T) {
	for _, agent := range []string{"claude", "codex", "opencode"} {
		t.Run(agent, func(t *testing.T) {
			l := runDoorwayLaunch(t, bedrockDoorwayConfig(agent, true), nil, nil, agent)
			assertDoorwayServed(t, l)
			if agent == "claude" && l.report.Env["CLAUDE_CODE_USE_BEDROCK"] != "1" {
				t.Errorf("claude lost its Bedrock switch: %v", l.report.Env)
			}
			if agent == "codex" && (!strings.Contains(l.errs, "CODEX_REFRESH_TOKEN_URL_OVERRIDE") ||
				!strings.Contains(l.errs, "Not set at this notch")) {
				t.Errorf("codex's withheld refresh pointer is no longer named:\n%s", l.errs)
			}
		})
	}
	l := runDoorwayLaunch(t, `{"packs": ["claude"], "loopholes": {"aws-auth": {"enabled": true, `+
		`"settings": {"profile": "`+doorwayProfile+`", "unnarrowed": true}}}}`, nil,
		[]string{"-p", "bedrock"}, "claude")
	assertDoorwayServed(t, l)
}

// CLAUDE'S OWN AWS PROFILE STILL WINS BESIDE THE POINTER. The maintainer runs claude on Bedrock
// at the host through settings of their own (an AWS_PROFILE and CLAUDE_CODE_USE_BEDROCK). The
// launch passes that AWS_PROFILE through untouched beside the pointer and refuses nothing: the
// OQ-SSO8 check declares no override for AWS_PROFILE, and it lets a static pair through beside
// it. Which credential claude then uses is its SDK's chain order, MEASURED from its published
// binary (sso-backed-bedrock.md §11): env, SSO, ini, process, token file, and the container
// pointer last, so a resolvable profile is used before the pointer is ever asked.
func TestHostClaudeKeepsItsOwnAWSProfileBesideTheDoorway(t *testing.T) {
	l := runDoorwayLaunch(t, bedrockDoorwayConfig("claude", true),
		map[string]string{"AWS_PROFILE": "someones-own-sso"}, nil, "claude")
	assertDoorwayServed(t, l)
	if got := l.report.Env["AWS_PROFILE"]; got != "someones-own-sso" {
		t.Errorf("claude's AWS_PROFILE = %q, want the shell's own, untouched", got)
	}
	l = runDoorwayLaunch(t, bedrockDoorwayConfig("claude", true), map[string]string{
		"AWS_PROFILE": "someones-own-sso", "AWS_ACCESS_KEY_ID": "AKIAUSER", "AWS_SECRET_ACCESS_KEY": "s"},
		nil, "claude")
	if l.rc != 0 {
		t.Errorf("a static pair beside AWS_PROFILE refused the launch (the declaration's `unless` "+
			"lets it through): rc %d\n%s", l.rc, l.errs)
	}
}

// A BEARER BESIDE THE POINTER NOW REFUSES AT THE HOST, as it does in a jail (OQ-SSO8;
// notch-convergence item 13's done-when, which NC-D34 recorded as unmeetable while the host
// served no pointer): the launch refuses before it opens anything.
func TestHostRefusesABearerBesideTheDoorwaysPointer(t *testing.T) {
	l := runDoorwayLaunch(t, bedrockDoorwayConfig("claude", true),
		map[string]string{"AWS_BEARER_TOKEN_BEDROCK": "bearer-from-the-shell"}, nil, "claude")
	if l.rc != 1 || l.execed || len(l.started) != 0 {
		t.Fatalf("rc = %d, exec'd %v, started %d: a bearer beside the served pointer must refuse "+
			"before anything opens\n%s", l.rc, l.execed, len(l.started), l.errs)
	}
	for _, want := range []string{"AWS_BEARER_TOKEN_BEDROCK", "AWS_CONTAINER_CREDENTIALS_FULL_URI"} {
		if !strings.Contains(l.errs, want) {
			t.Errorf("the refusal must name %s:\n%s", want, l.errs)
		}
	}
}

// NO DOORWAY WITHOUT ITS LOOPHOLE, AND THE LINE SAYS WHY. With aws-auth off (it ships disabled)
// the launch execs as before, withholds the pointer, and names the one change that opens the
// doorway, where the old line said a jail daemon never runs at the host.
func TestHostOpensNoDoorwayWhenTheLoopholeIsDisabled(t *testing.T) {
	l := runDoorwayLaunch(t, bedrockDoorwayConfig("pi", false), nil, nil, "pi")
	if l.rc != 0 || !l.execed || len(l.started) != 0 {
		t.Fatalf("rc = %d, exec'd %v, started %d; want a plain exec\n%s", l.rc, l.execed, len(l.started), l.errs)
	}
	for _, want := range []string{"Not set at this notch", "AWS_CONTAINER_CREDENTIALS_FULL_URI",
		`loophole "aws-auth" is disabled`, `"loopholes": {"aws-auth": {"enabled": true}}`,
		// THE MAINTAINER'S SYMPTOM, NAMED: pi reaches Bedrock through its own client, and nothing
		// delivers it a credential, so the profile line warns and names the pointer that would.
		`pi → provider "bedrock", through pi's own "aws-bedrock" client`,
		`yolo host: Warning: profile "bedrock" delivers pi no credential for provider "bedrock" at this notch`,
		`The "aws-auth" jail daemon's pointer would carry AWS_CONTAINER_CREDENTIALS_FULL_URI`} {
		if !strings.Contains(l.errs, want) {
			t.Errorf("the withheld pointer's line must say %q:\n%s", want, l.errs)
		}
	}
	if strings.Contains(l.errs, "never at the host") {
		t.Errorf("the line still says a jail daemon never runs at the host:\n%s", l.errs)
	}
}

// NO DOORWAY FOR AN AGENT OFF BEDROCK: the pointer is gated on the provider's platform, so pi
// with no profile, or on another provider, gets neither the pointer nor a listener.
func TestHostOpensNoDoorwayForAnAgentOffBedrock(t *testing.T) {
	l := runDoorwayLaunch(t, `{"packs": ["pi"], "loopholes": {"aws-auth": {"enabled": true, `+
		`"settings": {"profile": "`+doorwayProfile+`", "unnarrowed": true}}}}`, nil, nil, "pi")
	if l.rc != 0 || !l.execed || len(l.started) != 0 {
		t.Fatalf("rc = %d, exec'd %v, started %d; want a plain exec\n%s", l.rc, l.execed, len(l.started), l.errs)
	}
	// aws-auth still joins (bedrock needs it, and the cause line says so); nothing of its
	// pointer or its doorway reaches this launch.
	for _, not := range []string{"doorway", "AWS_CONTAINER_CREDENTIALS_FULL_URI"} {
		if strings.Contains(l.errs, not) {
			t.Errorf("an unprofiled pi launch mentions %s:\n%s", not, l.errs)
		}
	}
}

// NO DOORWAY FOR AN AGENT WITH NO CLIENT FOR IT. copilot has no Bedrock client of its own (its
// pack binds no aws-bedrock provider), so `-p bedrock` configures nothing for it, and the profile
// line warns so. The aws-auth pointer is gated on the provider's platform alone, so the gate
// would still hand copilot a live credential for AWS tools it happens to run; at the host that
// meant spawning aws-auth's host code and opening its doorway for an agent that is not its
// client. The launch now opens nothing and runs no pack code for it, withholds the pointer,
// names why on the "Not set at this notch" line, and the warning no longer claims copilot
// starts as if no profile were selected.
func TestHostOpensNoDoorwayForAnAgentWithNoClientOfThePlatform(t *testing.T) {
	cfg := `{"packs": ["copilot", "aws-auth", "bedrock"], ` +
		`"providers": {"bedrock": {"region": "eu-west-1"}}, ` +
		`"loopholes": {"aws-auth": {"enabled": true, "settings": {"profile": "` + doorwayProfile +
		`", "unnarrowed": true}}}}`
	l := runDoorwayLaunch(t, cfg, nil, []string{"-p", "bedrock"}, "copilot")
	if l.rc != 0 || !l.execed || len(l.started) != 0 {
		t.Fatalf("rc = %d, exec'd %v, started %d; want a plain exec with no doorway for an agent "+
			"with no Bedrock client\n%s", l.rc, l.execed, len(l.started), l.errs)
	}
	for _, not := range []string{`opened the "aws-auth" doorway`, "This launch runs pack code on your machine",
		"starts as if no profile were selected"} {
		if strings.Contains(l.errs, not) {
			t.Errorf("the launch must not say %q for copilot:\n%s", not, l.errs)
		}
	}
	for _, want := range []string{
		`Warning: profile "bedrock" reaches nothing for copilot`,
		"Not set at this notch", "AWS_CONTAINER_CREDENTIALS_FULL_URI",
		`because copilot has no client of platform "aws-bedrock"`,
	} {
		if !strings.Contains(l.errs, want) {
			t.Errorf("the launch must say %q:\n%s", want, l.errs)
		}
	}
}

// `yolo host env` RUNS NO PROCESS, so it opens no doorway: it withholds the pointer and names the
// launch that opens it, and starts nothing.
func TestHostEnvNamesTheLaunchThatOpensTheDoorway(t *testing.T) {
	hostGateHome(t, bedrockDoorwayConfig("pi", true), nil)
	origStart := startLaunchService
	startLaunchService = func(*launchservice.Plan, map[string]string) (*launchservice.Running, error) {
		t.Fatal("yolo host env opened a doorway")
		return nil, nil
	}
	t.Cleanup(func() { startLaunchService = origStart })
	var out, errw bytes.Buffer
	if rc := hostMain([]string{"env", "--agent", "pi", "-p", "bedrock"}, &out, &errw, false, nil); rc != 0 {
		t.Fatalf("rc = %d\n%s", rc, errw.String())
	}
	if hostExports(out.String(), "AWS_CONTAINER_CREDENTIALS_FULL_URI") {
		t.Errorf("yolo host env exported the pointer of a doorway nothing opened:\n%s", out.String())
	}
	for _, want := range []string{"AWS_CONTAINER_CREDENTIALS_FULL_URI", "`yolo host -p bedrock -- pi` opens it"} {
		if !strings.Contains(errw.String(), want) {
			t.Errorf("yolo host env must say %q:\n%s", want, errw.String())
		}
	}
}

// A HOST SERVICE WHOSE SETTINGS ARE REFUSED STOPS THE HOST LAUNCH, WORDED FOR THE HOST. The
// settings name a profile and no permission mode, which aws-auth's pure settings validator refuses
// before anything starts (docs/reference/host-service-startup-diagnostics.md#where-it-runs-on-a-launch): the launch is refused with
// the pack's cause and remedy and the host check to run next, opens no doorway, and says nothing
// about a jail there is none of. No `aws` runs, so no AWS state decides the result.
func TestHostWordsAFailedDoorwayServiceForTheHost(t *testing.T) {
	cfg := `{"packs": ["pi"], "profile": {"pi": "bedrock"}, "providers": {"bedrock": {"region": "eu-west-1"}}, ` +
		`"loopholes": {"aws-auth": {"enabled": true, "settings": {"profile": "` + doorwayProfile + `"}}}}`
	l := runDoorwayLaunch(t, cfg, nil, nil, "pi")
	if l.rc == 0 || len(l.started) != 0 {
		t.Fatalf("rc = %d, started %d: a host launch whose service settings are refused must stop "+
			"before opening a doorway\n%s", l.rc, len(l.started), l.errs)
	}
	for _, want := range []string{"refused startup (configuration)", "No AWS credential mode is configured",
		"Remedy:", "yolo check --no-build", "retry"} {
		if !strings.Contains(l.errs, want) {
			t.Errorf("the refusal must say %q:\n%s", want, l.errs)
		}
	}
	for _, not := range []string{"in-jail", "the jail cannot reach it", doorwayProfile} {
		if strings.Contains(l.errs, not) {
			t.Errorf("a host launch's settings refusal says %q:\n%s", not, l.errs)
		}
	}
}

// THE CODEX REFRESH POINTER'S CLAUSE AT THE HOST says no selection opens its doorway there
// (HS-D22): its pointer reaches every agent whatever it selects, so the old clause's "nothing this
// launch selects asks for this one", which implied some selection would, is gone.
func TestHostSaysNoSelectionOpensAnUngatedDoorway(t *testing.T) {
	l := runDoorwayLaunch(t, `{"packs": ["pi", "codex"]}`, nil, nil, "pi")
	if l.rc != 0 {
		t.Fatalf("rc = %d\n%s", l.rc, l.errs)
	}
	for _, want := range []string{"CODEX_REFRESH_TOKEN_URL_OVERRIDE", "opens for no selection", "HS-D22"} {
		if !strings.Contains(l.errs, want) {
			t.Errorf("the withheld Codex pointer's line must say %q:\n%s", want, l.errs)
		}
	}
	if strings.Contains(l.errs, "nothing this launch selects asks for this one") {
		t.Errorf("the line still implies a selection would open the Codex doorway:\n%s", l.errs)
	}
}

// A DOORWAY THAT DOES NOT START REFUSES THE LAUNCH before the agent runs (§4.5), and the front
// the launch opened for it closes: its session dir is gone.
func TestHostRefusesWhenTheDoorwayCannotStart(t *testing.T) {
	origStart := startLaunchService
	// runDoorwayLaunch wraps whatever startLaunchService is, so this stub is what it calls.
	startLaunchService = func(p *launchservice.Plan, _ map[string]string) (*launchservice.Running, error) {
		return nil, &launchservice.AdmissionError{Service: p.Service, Pack: p.Pack, Why: "stub refuses"}
	}
	t.Cleanup(func() { startLaunchService = origStart })
	l := runDoorwayLaunch(t, bedrockDoorwayConfig("pi", true), nil, nil, "pi")
	if l.rc != 1 || l.execed || l.report.Env != nil {
		t.Fatalf("rc = %d, exec'd %v, agent env %v: a doorway that cannot start must refuse "+
			"before the agent\n%s", l.rc, l.execed, l.report.Env, l.errs)
	}
	if !strings.Contains(l.errs, "refusing to launch") || !strings.Contains(l.errs, "stub refuses") {
		t.Errorf("the refusal must name the doorway's failure:\n%s", l.errs)
	}
	sessions, _ := filepath.Glob(paths.HostServicesSessionGlob(paths.HostServicesBase(false)))
	for _, s := range sessions {
		if _, err := os.Stat(filepath.Join(s, "aws-auth"+paths.ServiceEndpointExt)); err == nil {
			t.Errorf("the refused launch left its session dir %s behind", s)
		}
	}
}

// OVER THE ACTIVE SET (docs/design/active-provider-sets.md AP-P1): pi on [zai, bedrock], Bedrock
// its second entry, asks for the doorway as pi on bedrock does, so the launch opens it and pi
// reaches the host service's credential through it. Planning the doorways over the primary
// alone (zai, no platform) opens none, and pi's Bedrock entry starts with no credential.
func TestHostPiWithABedrockEntryAfterTheFirstGetsTheDoorway(t *testing.T) {
	cfg := `{"packs": ["pi", "zai"], "profile": {"pi": ["zai", "bedrock"]}, ` +
		`"providers": {"bedrock": {"region": "eu-west-1"}}, ` +
		`"env_sources": [{"ZAI_API_KEY": "tok-zai"}], ` +
		`"loopholes": {"aws-auth": {"enabled": true, "settings": {"profile": "` + doorwayProfile +
		`", "unnarrowed": true}}}}`
	l := runDoorwayLaunch(t, cfg, map[string]string{"AWS_REGION": ""}, nil, "pi")
	assertDoorwayServed(t, l)
	if got := l.report.Env["AWS_REGION"]; got != "eu-west-1" {
		t.Errorf("pi's AWS_REGION = %q, want the Bedrock entry's eu-west-1", got)
	}
}
