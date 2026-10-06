package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestMacosUserOpensTheCodexDoorwayOutsideTheSandbox is HS-D15, the doorway rule
// (docs/design/host-notch-services.md, ruled 2026-09-29; OQ-OA6 route (b)), on the hardware. A
// DOORWAY is that ruling's word for the thin adapter an agent's client talks to, which checks the
// launch's caller token and forwards to the credential service's host daemon. The sandbox shares
// the Mac's loopback, so a `"packs": ["codex"]` launch opens Codex's refresh doorway itself,
// OUTSIDE Seatbelt, as the launch's own listener, and stops it when the command exits.
//
// WHAT ONLY THIS TEST CAN SEE, none of it executed before: that a process the launch starts
// outside the sandbox, as the HOST user, is reachable from inside it at the address
// CODEX_REFRESH_TOKEN_URL_OVERRIDE names; that it refuses a refresh without the launch's caller
// token and admits one bound to it (the marker shape the Codex launcher writes); and that it is
// gone once the session ends. It does not run Codex: the probe speaks Codex's request shape with
// curl, and the broker behind the doorway needs no login for a refused or a forwarded request to
// be told apart from a missing listener.
func TestMacosUserOpensTheCodexDoorwayOutsideTheSandbox(t *testing.T) {
	requireMacosUser(t)
	packHome(t, `{"packs": ["codex"]}`)
	ws := macosUserWorkspace(t, `{}`)
	uid := sandboxUID(t)
	me := strconv.Itoa(os.Getuid())

	watch := watchForDoorway(codexDoorwayArgv)
	t.Cleanup(func() { watch.stop() })
	body := func(marker string) string {
		return `'{"grant_type":"refresh_token","refresh_token":"'` + marker + `'"}'`
	}
	curl := func(marker string) string {
		return `curl -s -o /dev/null -w '%{http_code}' -H 'Content-Type: application/json' -d ` +
			body(marker) + ` "$CODEX_REFRESH_TOKEN_URL_OVERRIDE"`
	}
	r := macosUserRunProbe(t, "doorway", ws, strings.Join([]string{
		`echo "=== DOOR ==="`,
		`echo "URL=${CODEX_REFRESH_TOKEN_URL_OVERRIDE-UNSET}"`,
		`echo "NOTOKEN=$(` + curl("yolo-broker:1") + `)"`,
		`echo "TOKEN=$(` + curl(`yolo-broker:1.$YOLO_SERVICE_OPENAI_AUTH_BROKER_TOKEN`) + `)"`,
		`echo "=== END ==="`,
	}, "\n"))
	saw := watch.stop()
	door := section(r.stdout, "=== DOOR ===", "=== END ===")
	diag := "\n--- probe:\n" + door + "\n--- launch stderr:\n" + r.stderr

	if !strings.Contains(r.combined(), `Opened the "openai-auth-broker" doorway (pack "openai-auth"`) {
		t.Errorf("the launch did not say it opened the doorway%s", diag)
	}
	if strings.Contains(r.combined(), "Started openai-auth-broker inside the sandbox") {
		t.Errorf("the guest ran the adapter's jail daemon too, so one address is served twice%s", diag)
	}
	got := map[string]string{}
	for _, line := range strings.Split(door, "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(line), "="); ok {
			got[k] = v
		}
	}
	if !strings.HasPrefix(got["URL"], "http://127.0.0.1:") || strings.HasPrefix(got["URL"], "http://127.0.0.1:1460/") {
		t.Errorf("the sandbox's CODEX_REFRESH_TOKEN_URL_OVERRIDE is not a port picked for this launch%s", diag)
	}
	if got["NOTOKEN"] != "401" {
		t.Errorf("a refresh from inside the sandbox without the launch's token was not refused 401 "+
			"(000 means nothing answered at the address)%s", diag)
	}
	if got["TOKEN"] == "401" || got["TOKEN"] == "000" || got["TOKEN"] == "" {
		t.Errorf("a refresh carrying the launch's token did not get past the doorway%s", diag)
	}
	requireDoorwayRanOutsideAndStopped(t, codexDoorwayArgv, saw, me, uid, diag)
}

// codexDoorwayArgv and awsDoorwayArgv are what a doorway's host process listing contains: the
// shipped manifests' `jail_daemon.host_cmd`, less the `yolo` its argv starts with.
const (
	codexDoorwayArgv = "internal daemon openai-auth-adapter --listen"
	awsDoorwayArgv   = "internal daemon aws-credential-adapter --listen"
)

// requireDoorwayRanOutsideAndStopped is the two host-side facts every doorway test asserts: the
// host saw the doorway (saw, its listing line) run as the host user me, never as the sandbox
// account uid, and no process with its argv survives the session.
func requireDoorwayRanOutsideAndStopped(t *testing.T, argv, saw, me, uid, diag string) {
	t.Helper()
	if saw == "" {
		t.Errorf("the host saw no `%s` during the session%s", argv, diag)
	} else if f := strings.Fields(saw); len(f) < 1 || f[0] != me || f[0] == uid {
		t.Errorf("the doorway ran as uid %s, want the host user's %s (the sandbox account is %s): %s%s",
			f[0], me, uid, saw, diag)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		left, _ := exec.Command("pgrep", "-f", argv).Output()
		if strings.TrimSpace(string(left)) == "" {
			break
		}
		if time.Now().After(deadline) {
			t.Errorf("the doorway survives the session (pids %s)", strings.TrimSpace(string(left)))
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// TestMacosUserOpensTheAWSDoorwayOutsideTheSandbox is HS-D15 for aws-auth on the hardware: with
// `claude` on the `bedrock` profile, the launch opens the AWS container-credentials doorway itself,
// OUTSIDE Seatbelt, as the launch's own listener, and it forwards to the host aws-auth service
// through the front the launch published for this session.
//
// WHAT ONLY THIS TEST CAN SEE, none of it executed before: that the address
// AWS_CONTAINER_CREDENTIALS_FULL_URI names in claude's env file answers from inside the sandbox;
// that a request without AWS_CONTAINER_AUTHORIZATION_TOKEN is refused 401; that one carrying it
// reaches the host service and comes back 200 with the credentials the host's `aws` printed, which
// proves the doorway was handed the endpoint it forwards to; that the doorway runs as the host
// user; and that it is gone once the session ends. It runs no agent: the probe sources claude's
// env file, as claude's launcher does, and speaks the protocol with curl.
//
// The test supplies parent-jail-looking pointer sentinels to the macos-user launch and confirms
// its own listener answers instead: inheritance is a podman-in-podman affordance only (SSO-D2).
// This is the permanent Mac CI guard for the non-inheriting backend; native execution remains
// pending until macos-user.yml runs this test on its hosted Mac.
//
// The host `aws` is a stand-in on the launcher's PATH, which the host daemon inherits, as in
// awsauth_test.go, and the aws-auth host daemon is a machine-wide singleton: the test refuses to
// run beside a live one, which the launch would adopt, and stops the one it started.
//
// The user config is the container chain's own (awsAuthUserConfig), so it carries the provider
// region a `bedrock` launch with none is refused for before the sandbox starts (OQ-BR6,
// docs/design/bedrock-plumbing.md): written out here without one, this test was stopped by that
// refusal in macos-user.yml run 36719581090 and never reached the doorway. No request reaches AWS,
// so the region's value is never used.
func TestMacosUserOpensTheAWSDoorwayOutsideTheSandbox(t *testing.T) {
	requireMacosUser(t)
	holdMachineLock(t, true, "the macos-user AWS doorway test owns the aws-auth host singleton")
	packHome(t, awsAuthUserConfig(`["claude", "aws-auth"]`, "claude", ""))
	ws := macosUserWorkspace(t, `{}`)
	uid := sandboxUID(t)
	me := strconv.Itoa(os.Getuid())
	if r := runYoloCLI(t, ws, "host-daemon", "status", "aws-auth"); r.rc == 0 || awsAuthDaemonAlive() {
		t.Fatalf("an aws-auth host daemon is already running on this machine, and the launch would "+
			"adopt it instead of spawning one against this test's stand-in `aws`. Stop it with "+
			"`yolo host-daemon stop aws-auth` and rerun.\n%s", r.combined())
	}
	t.Cleanup(func() {
		if r := runYoloCLI(t, ws, "host-daemon", "stop", "aws-auth"); r.rc != 0 {
			t.Logf("stopping this test's aws-auth daemon: rc=%d\n%s", r.rc, r.combined())
		}
	})

	bin := resolvedTempDir(t)
	expires := time.Now().Add(time.Hour).UTC().Truncate(time.Second).Format(time.RFC3339)
	process := `{"Version": 1, "AccessKeyId": "ASIAYOLODOORWAY", "SecretAccessKey": "yolo-doorway-secret", ` +
		`"SessionToken": "yolo-doorway-session-token", "Expiration": "` + expires + `"}`
	if err := os.WriteFile(filepath.Join(bin, "process.json"), []byte(process), 0o600); err != nil {
		t.Fatal(err)
	}
	fakeAWS := "#!/bin/sh\n" +
		"case \"$1\" in\n" +
		"  --version) echo 'aws-cli/2.99.0 Python/3.12.0 Darwin/yolo-integration exe/fake'; exit 0 ;;\n" +
		"  configure) cat '" + filepath.Join(bin, "process.json") + "'; exit 0 ;;\n" +
		"esac\n" +
		"echo \"fake aws: unexpected argv: $*\" >&2\n" +
		"exit 2\n"
	if err := os.WriteFile(filepath.Join(bin, "aws"), []byte(fakeAWS), 0o755); err != nil {
		t.Fatal(err)
	}

	watch := watchForDoorway(awsDoorwayArgv)
	t.Cleanup(func() { watch.stop() })
	curl := func(auth string) string {
		return `curl -s -o /dev/null -w '%{http_code}' ` + auth + ` "$AWS_CONTAINER_CREDENTIALS_FULL_URI"`
	}
	r := macosUserRunProbe(t, "aws-doorway", ws, strings.Join([]string{
		`echo "=== DOOR ==="`,
		`[ -z "${AWS_CONTAINER_AUTHORIZATION_TOKEN:-}" ] && echo "SHELL_TOKEN=absent"`,
		`. ~/.config/yolo-agent-env/claude.sh`,
		`echo "URI=${AWS_CONTAINER_CREDENTIALS_FULL_URI-UNSET}"`,
		`echo "NOTOKEN=$(` + curl("") + `)"`,
		`echo "TOKEN=$(` + curl(`-H "Authorization: ${AWS_CONTAINER_AUTHORIZATION_TOKEN:-}"`) + `)"`,
		`echo "=== END ==="`,
	}, "\n"), withEnv("PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"AWS_CONTAINER_CREDENTIALS_FULL_URI=http://127.0.0.1:52001/nested-inheritance-sentinel",
		"AWS_CONTAINER_AUTHORIZATION_TOKEN=integration-parent-pointer-sentinel"))
	saw := watch.stop()
	door := section(r.stdout, "=== DOOR ===", "=== END ===")
	diag := "\n--- probe:\n" + door + "\n--- launch stderr:\n" + r.stderr + awsAuthDaemonLog(t)

	if !strings.Contains(r.combined(), `Opened the "aws-auth" doorway (pack "aws-auth"`) {
		t.Errorf("the launch did not say it opened the AWS doorway%s", diag)
	}
	got := map[string]string{}
	for _, line := range strings.Split(door, "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(line), "="); ok {
			got[k] = v
		}
	}
	if !strings.HasPrefix(got["URI"], "http://127.0.0.1:") || strings.HasPrefix(got["URI"], "http://"+awsAuthAdapterAddr+"/") ||
		!strings.HasSuffix(got["URI"], "/credentials") {
		t.Errorf("claude's AWS_CONTAINER_CREDENTIALS_FULL_URI is not a port picked for this launch%s", diag)
	}
	if got["URI"] == "http://127.0.0.1:52001/nested-inheritance-sentinel" {
		t.Errorf("macos-user inherited the launching environment's parent-jail pointer%s", diag)
	}
	if got["SHELL_TOKEN"] != "absent" {
		t.Errorf("the bare shell carries the AWS doorway's token, which only a bedrock agent's env gets%s", diag)
	}
	if got["NOTOKEN"] != "401" {
		t.Errorf("a request from inside the sandbox without the caller token was not refused 401 "+
			"(000 means nothing answered at the address)%s", diag)
	}
	if got["TOKEN"] != "200" {
		t.Errorf("a request carrying claude's caller token got %q, want 200 with the stand-in's "+
			"credentials: the doorway did not reach the host aws-auth service%s", got["TOKEN"], diag)
	}
	requireDoorwayRanOutsideAndStopped(t, awsDoorwayArgv, saw, me, uid, diag)
}

// doorwayWatch polls the host's process table for the doorway during a session.
type doorwayWatch struct {
	quit chan struct{}
	done chan struct{}
	once sync.Once
	mu   sync.Mutex
	saw  string
}

// watchForDoorway polls for a host process whose listing contains argv.
func watchForDoorway(argv string) *doorwayWatch {
	w := &doorwayWatch{quit: make(chan struct{}), done: make(chan struct{})}
	go func() {
		defer close(w.done)
		tick := time.NewTicker(250 * time.Millisecond)
		defer tick.Stop()
		for {
			for _, line := range strings.Split(hostProcessListing(), "\n") {
				if strings.Contains(line, argv) && !strings.Contains(line, "pgrep") {
					w.mu.Lock()
					w.saw = strings.TrimSpace(line)
					w.mu.Unlock()
					return
				}
			}
			select {
			case <-w.quit:
				return
			case <-tick.C:
			}
		}
	}()
	return w
}

// stop ends the poll and returns the doorway's listing line ("" for none).
func (w *doorwayWatch) stop() string {
	w.once.Do(func() { close(w.quit) })
	<-w.done
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.saw
}
