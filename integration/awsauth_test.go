package integration

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// THE aws-auth CREDENTIAL CHANNEL, END TO END, OVER THE REAL LOOPBACK HOP.
//
// packs/aws-auth turns a host `aws sso login` into a credential a jail can use without ever
// holding the login (docs/reference/agent-credentials.md). The chain has five links and each
// has unit tests of its own: the host daemon mints by shelling out to `aws`
// (internal/awsauthdaemon), yolo's front publishes it to the jail over loopback-TLS
// (internal/svcendpoint), the in-jail adapter serves the AWS container-credentials protocol
// on 127.0.0.1:1461 (internal/awscredadapter), and the pack's `bedrock`-gated `env`
// contribution points AWS_CONTAINER_CREDENTIALS_FULL_URI at it. These tests run all five
// together, from the in-jail curl to the host's `aws`.
//
// The jail tests select `claude` and `bedrock`; the host-agent test selects `pi` and `bedrock`.
// Bedrock declares the aws-auth dependency and contributes the provider and profile. The fixtures
// curl the pointer from inside their session — no agent is started. The two cases:
//
//   - TestAWSAuthServesTheFourKeysOverTheLoopbackHop — a live session: a 200 whose body is
//     exactly AccessKeyId, SecretAccessKey, Token and Expiration, the values the host's `aws`
//     printed, with `Token` rather than the CLI's `SessionToken`.
//   - TestAWSAuthLapsedSessionIsA4xxNamingTheLogin — a lapsed session: a 400 whose body
//     carries the `aws sso login --profile …` command a human runs on the host (OQ-SSO6: a
//     lapsed session is a MESSAGE, not a request), and a launch that printed the same command
//     as one warning before the jail started (the launch check, design SSO-D1). The live
//     session's launch prints no such warning.
//
// > [!WARNING]
// > READ THIS BEFORE TRUSTING A GREEN RUN HERE, exactly as reachability_test.go's header
// > says. Under podman-in-podman the CLI forces `--net=host`, the one mode in which a
// > loopback-forwarding bug cannot reproduce: the jail shares the launcher's network stack,
// > so the host's loopback and the jail's are one. This suite normally runs from inside a
// > jail, so these tests normally run in exactly that blind spot. A green nested run proves
// > the chain is WIRED; only a real jail on a rootless host, or CI, proves the hop
// > (docs/reference/loopback-tls-reachability.md#a-nested-jail-is-structurally-blind-to-this).
//
// # The fake `aws`
//
// No real login exists anywhere this suite runs, and the suite must never reach AWS. So
// the LAUNCHER's PATH gets a directory holding a shell script named `aws`, which the host
// daemon inherits when it is spawned (internal/broker's realSpawn passes the environment
// through): `--version` answers, and `configure export-credentials` either prints a
// `--format process` body the test wrote or fails with a lapsed-session message AWS CLI v2
// emits. Every argv it is handed is appended to a log the test reads back.
//
// # Hermetic state, and the one thing a private state dir cannot make private
//
// The launches get a PRIVATE ~/.local/share/yolo-jail (macArchivePrivateState, which states
// the pattern), never the developer's: the loophole's settings file and its minted-credential
// cache live there, and the daemon's log with them. Only `cache/` is re-linked, so npm and
// mise downloads stay warm. YOLO_NO_AUTO_IMAGE_REAP=1 rides along for that helper's reason: a
// private state dir knows only this test's workspaces, so a reap would take every other image.
//
// EACH LAUNCH RUNS ITS OWN aws-auth DAEMON (HD-R1, docs/design/host-daemon-ownership.md): spawned
// with the jail, under this test's HOME and so against its fake `aws` and private state, and
// ended with the jail. A machine-wide one the developer's older yolo left running is neither
// adopted nor touched, so the fixture neither refuses beside one nor stops one by name; the
// cleanup's wait (awaitHomeHostDaemons) is what sees the test's own gone.
//
// # A nested jail beside an aws-auth jail
//
// Podman-in-podman forces --net=host, so a nested jail shares the launching jail's loopback, and
// a development jail that selects aws-auth holds the adapter's declared 127.0.0.1:1461 there,
// forwarding to the HOST's real aws-auth daemon. The fixture used to skip in that case, since the
// nested adapter could not bind and the nested curl reached the outer one. Since served
// addresses (docs/plans/notch-convergence.md NC-D41) a jail on a shared namespace serves its
// adapter on a port its launch picked, and the pointer names that port, so the nested curl
// reaches its own adapter: the pointer is asserted to be a picked port there, and the declared
// one on a bridged jail (servedURLProblem).
//
// Nor does a failure echo a served credential value: if a real adapter is ever reached anyway,
// a mismatch names the key and the fixture's value, never what was served
// (awsAuthCredentialMismatches).
//
// The runtime must be podman: Apple Container is inert for every loopback-tls loophole
// (backendInertReason), and macos-user declines the jail-side adapter and opens it outside the
// sandbox as a doorway instead (docs/design/host-notch-services.md HS-D15), which
// TestMacosUserOpensTheAWSDoorwayOutsideTheSandbox measures on a Mac.
//
// No t.Parallel(): the integration package runs serially by design, and these two tests
// share the machine-wide singleton paths above besides.

// awsAuthProfile is the AWS profile the fixture configures. It names nothing real, so the
// login command the lapsed case must carry is unambiguous in the body.
const awsAuthProfile = "yolo-integration"

// awsAuthAdapterAddr is the in-jail adapter's DECLARED listen address (the manifest's
// `jail_daemon.listen`), where a bridged jail serves it.
const awsAuthAdapterAddr = "127.0.0.1:1461"

// awsAuthCredentialMismatches compares a served container-credentials body against the values
// the fake `aws` printed and returns one line per differing key. A line names the key and the
// WANTED value only: the served one is never echoed, so a run that reached a real adapter
// cannot print a real secret into the test log.
func awsAuthCredentialMismatches(served map[string]any, want map[string]string) []string {
	var keys []string
	for k := range want {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var out []string
	for _, k := range keys {
		got, present := served[k]
		switch {
		case !present:
			out = append(out, fmt.Sprintf("served body has no %s, want %q — the value the host's `aws` printed", k, want[k]))
		case got != want[k]:
			out = append(out, fmt.Sprintf("served %s differs from %q, the value the host's `aws` printed "+
				"(the served value is withheld: it may be a real credential)", k, want[k]))
		}
	}
	return out
}

// awsAuthFixture is one prepared launch: the workspace, the launcher options that put the
// fake `aws` first on PATH, and the fake's argv log.
type awsAuthFixture struct {
	dir  string
	opts []runOption
	// env is opts' launcher environment as KEY=VALUE pairs, for startYoloBackground.
	env     []string
	argvLog string
}

// newAWSAuthFixture prepares a launch whose host `aws` runs the shell fragment configure
// returns for `configure …`; configure is handed the fake's own directory, for any file the
// fragment needs. See the file header for each step's reason.
func newAWSAuthFixture(t *testing.T, configure func(bin string) string) awsAuthFixture {
	t.Helper()
	// `packs`, `profile` and a loophole's `scope: "user"` settings are all user-scope
	// only. `unnarrowed` rather than a role: the narrowing arm needs `aws sts assume-role`,
	// and this test is about the channel, not the narrowing (which has its own unit tests).
	// The provider's region is there because a `bedrock` launch with none is refused before
	// the jail starts (OQ-BR6); no request reaches AWS, so its value is never used.
	return newAWSAuthFixtureWith(t, awsAuthUserConfig(`["claude", "bedrock"]`, "claude", ""), true, configure)
}

// awsAuthUserConfig is the fixture's user config: packs, agent on the bedrock profile, a region,
// and aws-auth enabled un-narrowed for the fixture's profile. extra is more top-level keys,
// each followed by a comma, or "".
func awsAuthUserConfig(packs, agent, extra string) string {
	return `{
		` + extra + `"packs": ` + packs + `,
		"profile": {"` + agent + `": "bedrock"},
		"providers": {"bedrock": {"region": "us-east-1"}},
		"loopholes": {"aws-auth": {
			"enabled": true,
			"settings": {"profile": "` + awsAuthProfile + `", "unnarrowed": true}
		}}
	}`
}

// newAWSAuthFixtureWith is newAWSAuthFixture over a user config of the caller's. podman is
// whether the launch is a jail's, which needs podman; a `yolo host` launch (below) starts no
// container.
func newAWSAuthFixtureWith(t *testing.T, userConfig string, podman bool,
	configure func(bin string) string) awsAuthFixture {
	t.Helper()
	// EXCLUSIVE: the fixture owns the aws-auth adapter port on a bridged jail
	// for the test's duration (the file header, and machinelock_test.go).
	requireJailExclusive(t, "the aws-auth tests own the adapter port and the fake `aws` on PATH")
	if rt := detectRuntime(); podman && rt != "podman" {
		t.Skipf("aws-auth's chain needs podman, and this runtime is %q: Apple Container is "+
			"inert for loopback-tls loopholes and macos-user declines the jail-side adapter", rt)
	}

	dir := writeProject(t, `{}`)
	packHome(t, userConfig)
	macArchivePrivateState(t)

	bin := t.TempDir()
	argvLog := filepath.Join(bin, "argv.log")
	script := "#!/bin/sh\n" +
		"echo \"$*\" >> '" + argvLog + "'\n" +
		"case \"$1\" in\n" +
		"  --version) echo 'aws-cli/2.99.0 Python/3.12.0 Linux/yolo-integration exe/fake'; exit 0 ;;\n" +
		"  configure) " + configure(bin) + " ;;\n" +
		"esac\n" +
		"echo \"fake aws: unexpected argv: $*\" >&2\n" +
		"exit 2\n"
	if err := os.WriteFile(filepath.Join(bin, "aws"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	env := []string{
		"PATH=" + bin + string(os.PathListSeparator) + os.Getenv("PATH"),
		"YOLO_NO_AUTO_IMAGE_REAP=1",
	}
	return awsAuthFixture{
		dir:     dir,
		argvLog: argvLog,
		env:     env,
		opts:    []runOption{withEnv(env...)},
	}
}

// awsAuthCurlScript curls the pointer from inside the jail and leaves the status on stdout
// and the body in the workspace, where the host reads it back. It waits for the adapter's
// port first, because the supervisor starts it at boot and a slow start must not read as a
// channel fault. The pointer is claude's alone under the credential gate (the bedrock profile
// claims AWS_CONTAINER_CREDENTIALS_FULL_URI; docs/reference/providers.md#the-credential-gate), so
// the script reads it the way claude's launcher does, by sourcing claude's own env file: a bare
// shell carries none of it. The caller token is SCOPED the same way (OQ-CN7 (c)): claude's file
// carries it as AWS_CONTAINER_AUTHORIZATION_TOKEN, which the SDK sends verbatim as
// `Authorization`, and the bare shell the script starts in carries none — so its request is the
// stranger's.
const awsAuthCurlScript = `set -u
[ -z "${AWS_CONTAINER_AUTHORIZATION_TOKEN:-}" ] && echo "SHELL_TOKEN=absent"
. ~/.config/yolo-agent-env/claude.sh
uri=${AWS_CONTAINER_CREDENTIALS_FULL_URI:-}
if [ -z "$uri" ]; then echo "POINTER=unset"; exit 3; fi
echo "POINTER=$uri"
hostport=${uri#http://}; hostport=${hostport%%/*}
for i in $(seq 1 200); do (exec 3<>/dev/tcp/127.0.0.1/${hostport##*:}) 2>/dev/null && break; sleep 0.1; done
tok=${AWS_CONTAINER_AUTHORIZATION_TOKEN:-}
if [ -z "$tok" ]; then echo "TOKEN=missing"; exit 4; fi
echo "TOKEN=present"
echo "STRANGER_HTTP=$(curl -sS -o /dev/null -w '%{http_code}' "$uri")"
code=$(curl -sS -o /workspace/awsauth-body.json -w '%{http_code}' -H "Authorization: $tok" "$uri")
echo "HTTP=$code"
true`

// runAWSAuthCurl launches the jail, curls the pointer, and returns the HTTP status and body.
func runAWSAuthCurl(t *testing.T, fx awsAuthFixture) (string, []byte, result) {
	t.Helper()
	r := runYolo(t, fx.dir, awsAuthCurlScript, fx.opts...)
	if r.rc != 0 {
		t.Fatalf("the aws-auth launch failed: rc %d\n%s%s", r.rc, r.combined(), awsAuthDaemonLog(t))
	}
	if p := servedURLProblem(kvLine(r.stdout, "POINTER"), awsAuthAdapterAddr, "/credentials", inContainer()); p != "" {
		t.Fatalf("the jail's AWS_CONTAINER_CREDENTIALS_FULL_URI is %s — the `bedrock`-gated env "+
			"contribution was not delivered, or not at the adapter's served address:\n%s", p, r.combined())
	}
	// CALLER AUTHENTICATION (docs/plans/notch-convergence.md §2.3), SCOPED (OQ-CN7 (c)): the
	// token is delivered beside the pointer in claude's own file and in no other process's
	// environment, and a request that does not send it — a bare shell's, or any other process's
	// on a shared loopback — is refused before the host is asked.
	if !strings.Contains(r.stdout, "TOKEN=present") {
		t.Fatalf("AWS_CONTAINER_AUTHORIZATION_TOKEN is not in claude's env file, so no SDK can "+
			"authenticate to the adapter:\n%s", r.combined())
	}
	if !strings.Contains(r.stdout, "SHELL_TOKEN=absent") {
		t.Errorf("the bare shell's environment carries the adapter's scoped token:\n%s", r.combined())
	}
	if !strings.Contains(r.stdout, "STRANGER_HTTP=401") {
		t.Errorf("a request without the caller token was not refused 401:\n%s", r.combined())
	}
	status := ""
	for _, line := range strings.Split(r.stdout, "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "HTTP="); ok {
			status = v
		}
	}
	body, err := os.ReadFile(filepath.Join(fx.dir, "awsauth-body.json"))
	if err != nil {
		t.Fatalf("reading the body the in-jail curl wrote: %v\n%s%s", err, r.combined(), awsAuthDaemonLog(t))
	}
	_ = os.Remove(filepath.Join(fx.dir, "awsauth-body.json"))
	return status, body, r
}

// awsAuthDaemonLog is the host daemon's log, for a failure message: the host half of the
// chain says why it refused, and nothing else in the output does.
func awsAuthDaemonLog(t *testing.T) string {
	t.Helper()
	p := filepath.Join(os.Getenv("HOME"), ".local", "share", "yolo-jail", "logs", "host-service-aws-auth.log")
	b, err := os.ReadFile(p)
	if err != nil {
		return "\n(no host daemon log at " + p + ": " + err.Error() + ")"
	}
	return "\n--- " + p + " ---\n" + string(b)
}

// TestAWSAuthServesTheFourKeysOverTheLoopbackHop: a live session reaches the jail as the
// four-key container-credentials body, with the values the host's `aws` printed.
func TestAWSAuthServesTheFourKeysOverTheLoopbackHop(t *testing.T) {
	expires := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	fx := newAWSAuthFixture(t, func(bin string) string {
		// The `--format process` body, with the CLI's own `SessionToken` spelling.
		raw, err := json.Marshal(map[string]any{
			"Version":         1,
			"AccessKeyId":     "ASIAYOLOINTEGRATION",
			"SecretAccessKey": "yolo-integration-secret",
			"SessionToken":    "yolo-integration-session-token",
			"Expiration":      expires.Format(time.RFC3339),
		})
		if err != nil {
			t.Fatal(err)
		}
		body := filepath.Join(bin, "process.json")
		if err := os.WriteFile(body, raw, 0o600); err != nil {
			t.Fatal(err)
		}
		return "cat '" + body + "'; exit 0"
	})

	status, got, r := runAWSAuthCurl(t, fx)
	if status != "200" {
		t.Fatalf("GET the pointer = HTTP %q, want 200 with a live session\nbody: %s\n%s%s",
			status, got, r.combined(), awsAuthDaemonLog(t))
	}
	// A live session gives the launch nothing to warn about (the negative of the lapsed
	// test's launch-check assertion).
	if line := awsAuthLaunchWarning(r.combined()); line != "" {
		t.Errorf("a live session's launch printed an aws-auth warning:\n%s", line)
	}
	var served map[string]any
	if err := json.Unmarshal(got, &served); err != nil {
		t.Fatalf("the served body is not JSON: %v (%d bytes, withheld)", err, len(got))
	}
	var keys []string
	for k := range served {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	// EXACTLY the four keys an SDK requires — and `Token`, never the CLI's `SessionToken`,
	// which an SDK rejects without naming the field.
	if strings.Join(keys, ",") != "AccessKeyId,Expiration,SecretAccessKey,Token" {
		t.Errorf("served keys = %v, want exactly AccessKeyId, Expiration, SecretAccessKey, Token", keys)
	}
	for _, m := range awsAuthCredentialMismatches(served, map[string]string{
		"AccessKeyId":     "ASIAYOLOINTEGRATION",
		"SecretAccessKey": "yolo-integration-secret",
		"Token":           "yolo-integration-session-token",
	}) {
		t.Error(m)
	}
	exp, _ := served["Expiration"].(string)
	if at, err := time.Parse(time.RFC3339, exp); err != nil || !at.Equal(expires) {
		t.Errorf("served Expiration = %q, want RFC3339 %s (parse error: %v)", exp,
			expires.Format(time.RFC3339), err)
	}

	// The host daemon asked the CLI for the configured profile, the un-narrowed arm's argv.
	argv, err := os.ReadFile(fx.argvLog)
	if err != nil {
		t.Fatalf("the fake `aws` was never run on the host — the daemon did not spawn from this "+
			"launcher's PATH: %v%s", err, awsAuthDaemonLog(t))
	}
	if want := "configure export-credentials --profile " + awsAuthProfile + " --format process"; !strings.Contains(string(argv), want) {
		t.Errorf("the host daemon never ran `aws %s`; the fake saw:\n%s", want, argv)
	}
}

// TestAWSAuthLapsedSessionIsA4xxNamingTheLogin: a lapsed session reaches the jail as a 4xx
// whose body names the host-side login, and the Code is the lapse's, not a transport fault's
// (the adapter answers 400 for an unreachable front too, as ServiceUnreachable).
func TestAWSAuthLapsedSessionIsA4xxNamingTheLogin(t *testing.T) {
	fx := newAWSAuthFixture(t, func(string) string {
		return "echo 'Error when retrieving token from sso: Token has expired and refresh failed' >&2; exit 255"
	})

	status, got, r := runAWSAuthCurl(t, fx)
	if status != "400" {
		shown := string(got)
		if status == "200" {
			// A 200 body is a credential, and not necessarily the fake's: never echo it.
			shown = fmt.Sprintf("(%d bytes, withheld: a 200 body is a credential)", len(got))
		}
		t.Fatalf("GET the pointer = HTTP %q, want 400 for a lapsed session\nbody: %s\n%s%s",
			status, shown, r.combined(), awsAuthDaemonLog(t))
	}
	var served struct{ Code, Message string }
	if err := json.Unmarshal(got, &served); err != nil {
		t.Fatalf("the 4xx body is not JSON: %v\n%s", err, got)
	}
	if served.Code != "ExpiredToken" {
		t.Errorf("4xx Code = %q, want ExpiredToken — anything else means the chain failed "+
			"somewhere other than the session:\n%s%s", served.Code, got, awsAuthDaemonLog(t))
	}
	if want := "aws sso login --profile " + awsAuthProfile; !strings.Contains(served.Message, want) {
		t.Errorf("the 4xx Message does not carry %q, the one command a human needs:\n%s",
			want, served.Message)
	}
	// AND THE LAUNCH SAID SO FIRST (design §8, SSO-D1): before the jail took the terminal, the
	// launch asked the service whether its agent's first Bedrock request would be served and
	// printed the same fix as one warning, then went on to start the jail.
	if line := awsAuthLaunchWarning(r.combined()); line == "" {
		t.Errorf("the launch printed no aws-auth launch-check warning for a lapsed session:\n%s",
			r.combined())
	} else if want := "aws sso login --profile " + awsAuthProfile; !strings.Contains(line, want) {
		t.Errorf("the launch's warning does not carry %q:\n%s", want, line)
	}
}

// TestAWSAuthAnAttachWarnsOfALapseToo: attaching is how a user re-enters a running jail, and a
// session can lapse after that jail's launch. The attach asks the service the jail's launch
// started, through the front that launch still owns, and prints the same warning a launch does
// (SSO-D1, amended), before the attached session runs.
func TestAWSAuthAnAttachWarnsOfALapseToo(t *testing.T) {
	fx := newAWSAuthFixture(t, func(string) string {
		return "echo 'Error when retrieving token from sso: Token has expired and refresh failed' >&2; exit 255"
	})
	const release = "release-awsauth-attach"
	first := startYoloBackground(t, "first", fx.dir,
		`echo FIRST-UP; for _ in $(seq 1 600); do [ -f /workspace/`+release+` ] && break; sleep 0.5; done`,
		fx.env...)
	t.Cleanup(func() { _ = os.WriteFile(filepath.Join(fx.dir, release), []byte("go\n"), 0o644) })
	deadline := time.Now().Add(jailTimeout())
	for !strings.Contains(first.combined(), "FIRST-UP") {
		select {
		case err := <-first.done:
			t.Fatalf("the first launch exited (%v) before its session ran:\n%s%s", err,
				first.combined(), awsAuthDaemonLog(t))
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("the first session never started within %s:\n%s", jailTimeout(), first.combined())
		}
		time.Sleep(50 * time.Millisecond)
	}
	awaitLaunchLockReleased(t, fx.dir, first)

	r := runYolo(t, fx.dir, `echo ATTACHED`, fx.opts...)
	if r.rc != 0 {
		t.Fatalf("the attach failed: rc %d\n%s", r.rc, r.combined())
	}
	if !strings.Contains(r.combined(), "Attaching to existing jail") {
		t.Fatalf("the second launch did not attach, so this tested nothing:\n%s", r.combined())
	}
	if line := awsAuthLaunchWarning(r.combined()); line == "" {
		t.Errorf("the attach printed no aws-auth launch-check warning for a lapsed session:\n%s%s",
			r.combined(), awsAuthDaemonLog(t))
	} else if want := "aws sso login --profile " + awsAuthProfile; !strings.Contains(line, want) {
		t.Errorf("the attach's warning does not carry %q:\n%s", want, line)
	}
	if !strings.Contains(r.stdout, "ATTACHED") {
		t.Errorf("the attached session did not run after the warning:\n%s", r.combined())
	}

	_ = os.WriteFile(filepath.Join(fx.dir, release), []byte("go\n"), 0o644)
	select {
	case <-first.done:
	case <-time.After(jailTimeout()):
		t.Fatalf("the first launch did not exit after release:\n%s", first.combined())
	}
}

// awsAuthLaunchWarning is the launch's aws-auth launch-check warning line in out, or "".
func awsAuthLaunchWarning(out string) string {
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "loophole aws-auth: cannot mint a Bedrock credential") {
			return line
		}
	}
	return ""
}

// awsAuthHostAgentScript is a stand-in `pi` for `yolo host`: it fetches the pointer it was
// handed the way an AWS SDK's container provider does (the token verbatim as `Authorization`),
// once with the token and once without, and prints what it saw. No real pi runs. Its pre-launch
// refresh (`pi update --extensions`) succeeds and does nothing: a failed one stops the launch
// (PD-NF1), which is not what this test is about.
const awsAuthHostAgentScript = `#!/bin/sh
[ "${1:-}" = update ] && exit 0
uri=${AWS_CONTAINER_CREDENTIALS_FULL_URI:-}
echo "POINTER=$uri"
echo "REGION=${AWS_REGION:-}"
[ -n "$uri" ] || exit 3
tok=${AWS_CONTAINER_AUTHORIZATION_TOKEN:-}
[ -n "$tok" ] && echo "TOKEN=present"
echo "STRANGER_HTTP=$(curl -sS -o /dev/null -w '%{http_code}' "$uri")"
echo "HTTP=$(curl -sS -o "$PWD/awsauth-host-body.json" -w '%{http_code}' -H "Authorization: $tok" "$uri")"
`

// TestAWSAuthServesAYoloHostAgentThroughItsDoorway is the same chain at `yolo host`, with the
// real built `yolo` (docs/design/host-notch-services.md HS-D21, §4.8): `yolo host -- pi` on
// `"profile": {"pi": "bedrock"}` ensures the aws-auth host service (spawned against the fake
// `aws`), fronts it for this launch, opens the adapter as its own listener on a port it picked,
// and hands pi the pointer and the token; the stand-in pi gets the fake's credential with the
// token and a 401 without it, and the region the provider declares reaches it as in a jail.
//
// No container starts, so this runs anywhere the suite does, podman or not. And there is no
// network namespace in the way: the loopback is the machine's, so this proves the wiring and
// nothing about a jail's reachability (the warning at the top of this file).
func TestAWSAuthServesAYoloHostAgentThroughItsDoorway(t *testing.T) {
	expires := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	// `host_floor: false`: `yolo host -- pi` otherwise runs yolo's own floor copy of pi,
	// installing the real pi to do it, where this test needs the stand-in on PATH (and no
	// automated test may run an agent CLI).
	fx := newAWSAuthFixtureWith(t,
		awsAuthUserConfig(`["bedrock", "pi"]`, "pi", `"host_management": "own", "host_floor": false, `), false, func(bin string) string {
			raw, err := json.Marshal(map[string]any{"Version": 1, "AccessKeyId": "ASIAYOLOHOSTDOORWAY",
				"SecretAccessKey": "yolo-host-secret", "SessionToken": "yolo-host-session-token",
				"Expiration": expires.Format(time.RFC3339)})
			if err != nil {
				t.Fatal(err)
			}
			body := filepath.Join(bin, "process.json")
			if err := os.WriteFile(body, raw, 0o600); err != nil {
				t.Fatal(err)
			}
			return "cat '" + body + "'; exit 0"
		})
	if err := os.WriteFile(filepath.Join(filepath.Dir(fx.argvLog), "pi"), []byte(awsAuthHostAgentScript), 0o755); err != nil {
		t.Fatal(err)
	}
	// The shell the host agent inherits is this process's: blank what would decide the result
	// in its place (a developer's own AWS settings, or the jail this suite may run in).
	opts := append(fx.opts, withEnv("YOLO_VERSION=", "AWS_PROFILE=", "AWS_REGION=", "AWS_DEFAULT_REGION=",
		"AWS_BEARER_TOKEN_BEDROCK=", "AWS_ACCESS_KEY_ID=", "AWS_SECRET_ACCESS_KEY=",
		"AWS_CONTAINER_CREDENTIALS_FULL_URI=", "AWS_CONTAINER_AUTHORIZATION_TOKEN="))
	r := runCommand(t, fx.dir, []string{"host", "--", "pi"}, opts...)
	if r.rc != 0 {
		t.Fatalf("yolo host -- pi: rc %d\n%s%s", r.rc, r.combined(), awsAuthDaemonLog(t))
	}
	uri := kvLine(r.stdout, "POINTER")
	if !strings.HasPrefix(uri, "http://127.0.0.1:") || strings.HasPrefix(uri, "http://"+awsAuthAdapterAddr) ||
		!strings.HasSuffix(uri, "/credentials") {
		t.Fatalf("pi's AWS_CONTAINER_CREDENTIALS_FULL_URI = %q, want a loopback port this launch "+
			"picked:\n%s", uri, r.combined())
	}
	for _, want := range []string{"TOKEN=present", "STRANGER_HTTP=401", "HTTP=200", "REGION=us-east-1"} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("the stand-in pi did not see %s:\n%s%s", want, r.combined(), awsAuthDaemonLog(t))
		}
	}
	for _, want := range []string{`opened the "aws-auth" doorway`, `Profile bedrock: declared by bedrock; pi → provider "bedrock"`} {
		if !strings.Contains(r.stderr, want) {
			t.Errorf("the launch must say %q:\n%s", want, r.stderr)
		}
	}
	body, err := os.ReadFile(filepath.Join(fx.dir, "awsauth-host-body.json"))
	if err != nil {
		t.Fatalf("reading the body the stand-in pi fetched: %v\n%s", err, r.combined())
	}
	var served map[string]any
	if err := json.Unmarshal(body, &served); err != nil {
		t.Fatalf("the served body is not JSON: %v (%d bytes, withheld)", err, len(body))
	}
	for _, m := range awsAuthCredentialMismatches(served, map[string]string{
		"AccessKeyId": "ASIAYOLOHOSTDOORWAY", "SecretAccessKey": "yolo-host-secret",
		"Token": "yolo-host-session-token"}) {
		t.Error(m)
	}
}

// awsAuthKeyAgentScript is a stand-in `pi` that says which Bedrock credential it was handed: the
// pointer verbatim (a URL, not a secret) and whether the Bedrock API key is present, never its
// value. No real pi runs.
const awsAuthKeyAgentScript = `#!/bin/sh
[ "${1:-}" = update ] && exit 0
echo "POINTER=${AWS_CONTAINER_CREDENTIALS_FULL_URI:-}"
if [ -n "${AWS_BEARER_TOKEN_BEDROCK:-}" ]; then echo "BEARER=present"; else echo "BEARER=absent"; fi
`

// TestAWSAuthWithholdsItsPointerFromABedrockKeyHostAgent is docs/design/bedrock-key-provider.md
// end to end at `yolo host`, with the real built `yolo`: aws-auth enabled with an SSO profile, and
// pi on `bedrock-key` with the Bedrock API key in the private set. The key reaches pi; aws-auth's
// pointer does not, since bedrock-key claims only the key (OQ-BK2); and no aws-auth doorway is
// opened, since no agent's provider claims what it would serve (CN-D22). The launch is not refused
// for the SSO profile beside the key: the pointer is delivered to nobody, so nothing overrides it.
func TestAWSAuthWithholdsItsPointerFromABedrockKeyHostAgent(t *testing.T) {
	const probe = "bk-integration-probe-not-a-key"
	fx := newAWSAuthFixtureWith(t, `{}`, false, func(string) string { return "echo 'fake aws: not asked' >&2; exit 2" })
	// TOML, because the key belongs in the private set ([env.secrets]), which a provider's key
	// must ride (secret-env-scoping.md row 2): a legacy env_sources entry is public and refused.
	// Written in place rather than through writeUserTOML, which would re-isolate the home the
	// fixture just prepared.
	cfgDir := filepath.Join(os.Getenv("HOME"), ".config", "yolo-jail")
	if err := os.Remove(filepath.Join(cfgDir, paths.UserConfigLegacyJSONCName)); err != nil {
		t.Fatalf("removing the seeded config.jsonc: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, paths.UserConfigTOMLName), []byte(
		"packs = [\"bedrock\", \"pi\"]\nhost_management = \"own\"\nhost_floor = false\n\n"+
			"[active_provider]\npi = \"bedrock-key\"\n\n"+
			"[providers.bedrock-key]\nregion = \"us-east-1\"\n\n"+
			"[allow.aws-auth]\nprofile = \""+awsAuthProfile+"\"\nunnarrowed = true\n\n"+
			"[env.secrets]\nset = { AWS_BEARER_TOKEN_BEDROCK = \""+probe+"\" }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(fx.argvLog), "pi"), []byte(awsAuthKeyAgentScript), 0o755); err != nil {
		t.Fatal(err)
	}
	opts := append(fx.opts, withEnv("YOLO_VERSION=", "AWS_PROFILE=", "AWS_REGION=", "AWS_DEFAULT_REGION=",
		"AWS_BEARER_TOKEN_BEDROCK=", "AWS_ACCESS_KEY_ID=", "AWS_SECRET_ACCESS_KEY=",
		"AWS_CONTAINER_CREDENTIALS_FULL_URI=", "AWS_CONTAINER_AUTHORIZATION_TOKEN="))
	r := runCommand(t, fx.dir, []string{"host", "--", "pi"}, opts...)
	if r.rc != 0 {
		t.Fatalf("yolo host -- pi on bedrock-key: rc %d\n%s", r.rc, r.combined())
	}
	if got := kvLine(r.stdout, "POINTER"); got != "" {
		t.Errorf("pi on bedrock-key was handed aws-auth's pointer %q:\n%s", got, r.combined())
	}
	if !strings.Contains(r.stdout, "BEARER=present") {
		t.Errorf("pi on bedrock-key was not handed the Bedrock API key:\n%s", r.combined())
	}
	if strings.Contains(r.stderr, `opened the "aws-auth" doorway`) {
		t.Errorf("a launch where no agent claims aws-auth's pointer opened its doorway:\n%s", r.stderr)
	}
	if strings.Contains(r.combined(), probe) {
		t.Errorf("the launch printed the key's value")
	}
}

// TestAWSAuthCredentialMismatchesNeverEchoTheServedValue pins the helper both end-to-end tests
// report through: a mismatch names the key and the wanted value, and the served value — which,
// had the curl reached a real adapter, would be a real secret — appears in no line.
func TestAWSAuthCredentialMismatchesNeverEchoTheServedValue(t *testing.T) {
	const realSecret = "wJalrXUtnFEMI/K7MDENG/bPxRfiCYREALSECRET"
	got := awsAuthCredentialMismatches(
		map[string]any{"AccessKeyId": "ASIAYOLOINTEGRATION", "SecretAccessKey": realSecret},
		map[string]string{
			"AccessKeyId":     "ASIAYOLOINTEGRATION",
			"SecretAccessKey": "yolo-integration-secret",
			"Token":           "yolo-integration-session-token",
		})
	if len(got) != 2 {
		t.Fatalf("mismatches = %q, want two: the differing SecretAccessKey and the absent Token", got)
	}
	for _, line := range got {
		if strings.Contains(line, realSecret) {
			t.Errorf("a mismatch line echoes the served value: %q", line)
		}
	}
	if !strings.Contains(got[0], "SecretAccessKey") || !strings.Contains(got[0], "yolo-integration-secret") {
		t.Errorf("the SecretAccessKey line = %q, want the key and the wanted value", got[0])
	}
	if !strings.Contains(got[1], "no Token") {
		t.Errorf("the Token line = %q, want it to say the key is absent", got[1])
	}
	if m := awsAuthCredentialMismatches(
		map[string]any{"AccessKeyId": "a", "SecretAccessKey": "s", "Token": "t"},
		map[string]string{"AccessKeyId": "a", "SecretAccessKey": "s", "Token": "t"},
	); len(m) != 0 {
		t.Errorf("matching values reported mismatches: %q", m)
	}
}
