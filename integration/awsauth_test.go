package integration

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// THE aws-auth CREDENTIAL CHANNEL, END TO END, OVER THE REAL LOOPBACK HOP.
//
// packs/aws-auth turns a host `aws sso login` into a credential a jail can use without ever
// holding the login (docs/design/sso-backed-bedrock.md). The chain has five links and each
// has unit tests of its own: the host daemon mints by shelling out to `aws`
// (internal/awsauthdaemon), yolo's front publishes it to the jail over loopback-TLS
// (internal/svcendpoint), the in-jail adapter serves the AWS container-credentials protocol
// on 127.0.0.1:1461 (internal/awscredadapter), and the pack's `bedrock`-gated `env`
// contribution points AWS_CONTAINER_CREDENTIALS_FULL_URI at it. These tests run all five
// together, from the in-jail curl to the host's `aws`.
//
// Each test launches a real jail selecting `claude` and `aws-auth` with the `bedrock`
// profile, and curls the pointer from inside it — no agent is started. The two cases:
//
//   - TestAWSAuthServesTheFourKeysOverTheLoopbackHop — a live session: a 200 whose body is
//     exactly AccessKeyId, SecretAccessKey, Token and Expiration, the values the host's `aws`
//     printed, with `Token` rather than the CLI's `SessionToken`.
//   - TestAWSAuthLapsedSessionIsA4xxNamingTheLogin — a lapsed session: a 400 whose body
//     carries the `aws sso login --profile …` command a human runs on the host (OQ-SSO6: a
//     lapsed session is a MESSAGE, not a request).
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
// ⚠ THE HOST SINGLETON'S SOCKET, PID FILE AND LOCK ARE NOT IN THE STATE DIR. They are
// /tmp/yolo-aws-auth.{sock,pid,lock} (paths.HostSingletonSocket), one per MACHINE, so a
// private state dir does not stop a launch from ADOPTING an aws-auth daemon that is already
// running — the developer's real one, serving real credentials. So each test refuses to run
// while one is alive (`yolo host-daemon status aws-auth`), and stops the one it spawned when
// it ends, so the next test's launch spawns afresh against its own fake.
//
// # Why a nested jail beside an aws-auth jail skips
//
// The adapter's port is fixed, 127.0.0.1:1461, and podman-in-podman forces --net=host, so a
// nested jail's 1461 IS the launching jail's. A development jail that selects aws-auth already
// runs its own adapter there, forwarding to the HOST's real aws-auth daemon: the nested adapter
// cannot bind, and the nested curl reaches the outer one and the developer's real profile. The
// PID-file guard above cannot see that, the outer jail's /tmp being its own. So the fixture
// skips when it runs in a container and 1461 already answers, before anything launches, as
// openaiauth_test.go does for 1460. A green result needs a jail that does not select aws-auth,
// or CI.
//
// Nor does a failure echo a served credential value: if a real adapter is ever reached anyway,
// a mismatch names the key and the fixture's value, never what was served
// (awsAuthCredentialMismatches).
//
// The runtime must be podman: Apple Container is inert for every loopback-tls loophole
// (backendInertReason), and macos-user declines every jail daemon, the adapter included.
//
// No t.Parallel(): the integration package runs serially by design, and these two tests
// share the machine-wide singleton paths above besides.

// awsAuthProfile is the AWS profile the fixture configures. It names nothing real, so the
// login command the lapsed case must carry is unambiguous in the body.
const awsAuthProfile = "yolo-integration"

// awsAuthAdapterAddr is the in-jail adapter's fixed listen address (the manifest's
// jail_daemon argv), which a nested jail shares with the jail that launched it.
const awsAuthAdapterAddr = "127.0.0.1:1461"

// awsAuthAdapterPortSkip is the skip reason when the launching jail's own adapter holds the port.
const awsAuthAdapterPortSkip = awsAuthAdapterAddr + " already answers on this loopback, and a " +
	"nested jail shares it (podman-in-podman forces --net=host): the curl would reach the " +
	"launching jail's own aws-auth adapter, which forwards to the HOST's real aws-auth daemon " +
	"and the developer's real profile, not this test's fake `aws`. Run it from a jail that does " +
	"not select aws-auth, or in CI (AGENTS.md's first carve-out)"

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
	dir     string
	opts    []runOption
	argvLog string
}

// newAWSAuthFixture prepares a launch whose host `aws` runs the shell fragment configure
// returns for `configure …`; configure is handed the fake's own directory, for any file the
// fragment needs. See the file header for each step's reason.
func newAWSAuthFixture(t *testing.T, configure func(bin string) string) awsAuthFixture {
	t.Helper()
	requireJail(t)
	if rt := detectRuntime(); rt != "podman" {
		t.Skipf("aws-auth's chain needs podman, and this runtime is %q: Apple Container is "+
			"inert for loopback-tls loopholes and macos-user declines the jail-side adapter", rt)
	}

	if inContainer() {
		if c, err := net.DialTimeout("tcp", awsAuthAdapterAddr, time.Second); err == nil {
			_ = c.Close()
			t.Skip(awsAuthAdapterPortSkip)
		}
	}

	dir := writeProject(t, `{}`)
	// `packs`, `use_profiles` and a loophole's `scope: "user"` settings are all user-scope
	// only. `unnarrowed` rather than a role: the narrowing arm needs `aws sts assume-role`,
	// and this test is about the channel, not the narrowing (which has its own unit tests).
	packHome(t, `{
		"packs": ["claude", "aws-auth"],
		"use_profiles": {"claude": "bedrock"},
		"loopholes": {"aws-auth": {
			"enabled": true,
			"settings": {"profile": "`+awsAuthProfile+`", "unnarrowed": true}
		}}
	}`)
	macArchivePrivateState(t)

	// Refuse to run beside a live aws-auth daemon: the launch would adopt it (see the
	// header), and its answer would be whatever that daemon serves. `status` exits 0 only
	// for a FULLY healthy daemon, so a live process behind a stale socket is caught by its
	// PID file instead — the launch's ensure would see it alive either way.
	if r := runYoloCLI(t, dir, "host-daemon", "status", "aws-auth"); r.rc == 0 || awsAuthDaemonAlive() {
		t.Fatalf("an aws-auth host daemon is already running on this machine, and a launch "+
			"here would adopt it instead of spawning one against this test's fake `aws` — "+
			"its socket is machine-wide (%s), not in the private state dir. Stop it with "+
			"`yolo host-daemon stop aws-auth` (the next launch that wants it starts it again) "+
			"and rerun.\n%s", paths.HostSingletonSocket("aws-auth"), r.combined())
	}
	// Registered AFTER macArchivePrivateState's cleanup so it runs FIRST: the daemon is
	// stopped before the state dir it writes into is removed.
	t.Cleanup(func() {
		if r := runYoloCLI(t, dir, "host-daemon", "stop", "aws-auth"); r.rc != 0 {
			t.Logf("stopping this test's aws-auth daemon: rc=%d\n%s", r.rc, r.combined())
		}
	})

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
	return awsAuthFixture{
		dir:     dir,
		argvLog: argvLog,
		opts: []runOption{withEnv(
			"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
			"YOLO_NO_AUTO_IMAGE_REAP=1",
		)},
	}
}

// awsAuthCurlScript curls the pointer from inside the jail and leaves the status on stdout
// and the body in the workspace, where the host reads it back. It waits for the adapter's
// port first, because the supervisor starts it at boot and a slow start must not read as a
// channel fault.
const awsAuthCurlScript = `set -u
uri=${AWS_CONTAINER_CREDENTIALS_FULL_URI:-}
if [ -z "$uri" ]; then echo "POINTER=unset"; exit 3; fi
echo "POINTER=$uri"
for i in $(seq 1 200); do (exec 3<>/dev/tcp/127.0.0.1/1461) 2>/dev/null && break; sleep 0.1; done
code=$(curl -sS -o /workspace/awsauth-body.json -w '%{http_code}' "$uri")
echo "HTTP=$code"
true`

// runAWSAuthCurl launches the jail, curls the pointer, and returns the HTTP status and body.
func runAWSAuthCurl(t *testing.T, fx awsAuthFixture) (string, []byte, result) {
	t.Helper()
	r := runYolo(t, fx.dir, awsAuthCurlScript, fx.opts...)
	if r.rc != 0 {
		t.Fatalf("the aws-auth launch failed: rc %d\n%s%s", r.rc, r.combined(), awsAuthDaemonLog(t))
	}
	if !strings.Contains(r.stdout, "POINTER=http://127.0.0.1:1461/credentials") {
		t.Fatalf("the jail's AWS_CONTAINER_CREDENTIALS_FULL_URI is not the pack's pointer — the "+
			"`bedrock`-gated env contribution was not delivered:\n%s", r.combined())
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

// awsAuthDaemonAlive reports whether the machine-wide aws-auth PID file names a live
// process. Signal 0 delivers nothing; it only asks whether the PID exists — and EPERM is
// an answer too: the process exists and belongs to someone else.
func awsAuthDaemonAlive() bool {
	raw, err := os.ReadFile(paths.HostSingletonPIDFile("aws-auth"))
	if err != nil {
		return false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil || pid <= 0 {
		return false
	}
	err = syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
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
