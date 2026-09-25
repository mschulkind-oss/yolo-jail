package integration

// openaiauth_test.go is step 12's first artifact in docs/design/openai-auth-broker-plan.md: a
// jail whose `packs` selects `codex`, completing a brokered token round trip through the
// endpoint Codex is told to use. It is the one test the unit suite cannot represent, because
// every hop it crosses is a different process on a different side of the jail boundary:
//
//	curl (in the jail) → yolo-jaild openai-auth-adapter on 127.0.0.1:1460 (in the jail)
//	  → the loopback-TLS front the launch published (host) → the machine-wide
//	  openai-auth-broker singleton (host) → its credentials.json (host, never mounted)
//
// and, for pi's route, `yolo internal openai-auth-client token` (in the jail) straight to that
// same front, with no adapter in between.
//
// # What "never the developer's real grant" costs
//
// The singleton is keyed by NAME alone: its socket, PID file and spawn lock are fixed paths
// under /tmp (paths.HostSingletonSocket), shared by every HOME on the machine, and a launch that
// finds one alive REUSES it whatever state file it was started with. So a private state dir is
// necessary and not sufficient — an import into "the" singleton goes to whichever one is alive.
// The test therefore owns the singleton for its duration, and every step is guarded:
//
//   - if the machine's own state file exists (a possible real grant), the test skips, whether or
//     not a singleton is alive: owning the slot would hand every concurrent launch the forged
//     token and send any concurrent login into the private state this test deletes;
//   - a live singleton whose state file EXISTS may hold a real grant: the test skips and touches
//     nothing. One whose state file is absent holds no grant (every earlier codex-selecting test
//     in this suite leaves one), so it is stopped, by the PID its own PID file names;
//   - the test starts its own singleton with a no-op `status`, and imports only after proving,
//     from /proc/<pid>/cmdline, that the daemon answering is the one started with THIS test's
//     private --state-file;
//   - it is stopped afterwards, by that PID, so no later launch reuses a daemon whose state dir
//     has been deleted.
//
// # Why a nested jail skips it
//
// Podman-in-podman forces --net=host, so a nested jail's 127.0.0.1:1460 IS the launching jail's.
// A development jail that selects codex already runs its own adapter there, forwarding to the
// HOST's real broker, and the nested jail's POST would reach that one instead of its own. So the
// test skips when it runs in a container and 1460 already answers. That is also AGENTS.md's
// first carve-out restated: a nested jail cannot test reachability across the host loopback at
// all, and ci.yml's rootless `integration` job is the instrument step 12 names.
//
// # Terms
//
//   - FORGED LOGIN: coined here. A Codex-shaped auth.json whose three tokens are random test
//     strings, the access token a JWT whose `exp` is a day away. Nothing in it was issued by
//     OpenAI, and because it is not due for a day the broker serves it from cache: no upstream
//     request is made at any point (openaiauth.Broker.Refresh's DecisionCached arm).

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/openaiauth"
	"github.com/mschulkind-oss/yolo-jail/internal/openaiauthdaemon"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// codexRefreshURL is the refresh endpoint packs/codex/pack.json hands Codex
// (CODEX_REFRESH_TOKEN_URL_OVERRIDE), and the address the openai-auth-broker manifest's
// jail_daemon listens on. Spelled literally, and asserted against the jail's own environment, so
// a pack that moved the URL fails here instead of this test quietly following it.
const codexRefreshURL = "http://127.0.0.1:1460/oauth/token"

// forgedCodexLogin is a FORGED LOGIN (see the file header) and the file it was written to.
type forgedCodexLogin struct {
	access, id, refresh, account string
	path                         string
}

// forgeCodexLogin writes a forged Codex auth.json into a temp dir. Every token carries a random
// nonce, so a token served back can only have come from THIS import.
func forgeCodexLogin(t *testing.T) forgedCodexLogin {
	t.Helper()
	raw := make([]byte, 12)
	if _, err := rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	nonce := hex.EncodeToString(raw)
	b64 := base64.RawURLEncoding.EncodeToString
	// The `exp` claim is what openaiauthdaemon.ReadCodexAuthFile reads as the grant's expiry. A
	// day out keeps the generation far from the broker's 5-minute refresh lead, so neither the
	// round trip nor the daemon's proactive loop ever calls OpenAI.
	payload := fmt.Sprintf(`{"exp":%d,"jti":"yolo-integration-%s"}`, time.Now().Add(24*time.Hour).Unix(), nonce)
	f := forgedCodexLogin{
		access:  b64([]byte(`{"alg":"none","typ":"JWT"}`)) + "." + b64([]byte(payload)) + ".yolo-integration-forged",
		id:      "yolo-integration-forged-id-" + nonce,
		refresh: "yolo-integration-forged-refresh-" + nonce,
		account: "yolo-integration-account-" + nonce,
	}
	body, err := json.Marshal(map[string]any{"tokens": map[string]string{
		"id_token": f.id, "access_token": f.access, "refresh_token": f.refresh, "account_id": f.account,
	}})
	if err != nil {
		t.Fatal(err)
	}
	f.path = filepath.Join(t.TempDir(), "auth.json")
	if err := os.WriteFile(f.path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	return f
}

// singletonPID is the PID the openai-auth-broker singleton's PID file names, or 0.
func singletonPID() int {
	raw, err := os.ReadFile(paths.HostSingletonPIDFile(openaiauth.LoopholeName))
	if err != nil {
		return 0
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil || pid <= 0 {
		return 0
	}
	return pid
}

// processGone reports that pid is no longer a running process. A ZOMBIE counts as gone: the
// singleton is spawned detached, so its reaper is whatever adopted it, and a container's PID 1
// may never reap it — kill(pid, 0) alone would call it alive forever.
func processGone(pid int) bool {
	if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
		return true
	}
	stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return errors.Is(err, fs.ErrNotExist)
	}
	// The state is the first field after the parenthesised command name, which may itself
	// contain spaces or parentheses — hence the LAST ')'.
	s := string(stat)
	i := strings.LastIndexByte(s, ')')
	return i >= 0 && i+2 < len(s) && s[i+2] == 'Z'
}

// daemonStateFile returns the --state-file the process pid was started with, read from its
// command line. ok is false when the process is not an openai-auth-broker daemon at all, or its
// command line cannot be read or carries no --state-file.
func daemonStateFile(pid int) (string, bool) {
	raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	if err != nil {
		return "", false
	}
	args := strings.Split(strings.TrimRight(string(raw), "\x00"), "\x00")
	isBroker := false
	state := ""
	for i, a := range args {
		switch {
		case a == openaiauth.LoopholeName:
			isBroker = true
		case a == "--state-file" && i+1 < len(args):
			state = args[i+1]
		case strings.HasPrefix(a, "--state-file="):
			state = strings.TrimPrefix(a, "--state-file=")
		}
	}
	return state, isBroker && state != ""
}

// stopSingletonPID stops the singleton process pid — TERM, then KILL after a grace — and then
// removes the fixed-path files it owned, but only while its PID file still names it, so a
// successor that some other launch started in between is never unlinked. It never falls back
// to a process search: internal/broker's BrokerKill does, and a search could match a daemon
// this test never started.
func stopSingletonPID(pid int) error {
	_ = syscall.Kill(pid, syscall.SIGTERM)
	for deadline := time.Now().Add(5 * time.Second); !processGone(pid) && time.Now().Before(deadline); {
		time.Sleep(50 * time.Millisecond)
	}
	if !processGone(pid) {
		_ = syscall.Kill(pid, syscall.SIGKILL)
		for deadline := time.Now().Add(2 * time.Second); !processGone(pid) && time.Now().Before(deadline); {
			time.Sleep(50 * time.Millisecond)
		}
	}
	if !processGone(pid) {
		return fmt.Errorf("openai-auth-broker pid %d survived SIGTERM and SIGKILL", pid)
	}
	if singletonPID() != pid {
		return nil
	}
	socket := paths.HostSingletonSocket(openaiauth.LoopholeName)
	pidFile := paths.HostSingletonPIDFile(openaiauth.LoopholeName)
	for _, p := range []string{socket, openaiauthdaemon.HostSocketPath(socket), pidFile + ".capability", pidFile} {
		if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return nil
}

// skipIfMachineHoldsAGrant skips the test when the machine's OWN broker state file exists, live
// singleton or not. With none alive, clearGrantlessSingleton has nothing to judge, and this test
// then starts THE machine-wide singleton on its private state and holds it for a whole launch:
// any codex or pi jail the developer launches meanwhile is served the forged token
// (startHostSingleton reuses a live daemon), and a `yolo openai-auth import` or login run
// meanwhile lands in the private state dir this test deletes. On a machine with no grant the
// worst case left is the first; a real grant is never put in that window.
//
// The machine's path is the private one re-rooted at the machine home requireJail recorded, so
// it follows loopholes.StateDirFor rather than restating its layout.
func skipIfMachineHoldsAGrant(t *testing.T, privateState string) {
	t.Helper()
	rel, err := filepath.Rel(paths.GlobalStorage(), privateState)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		t.Fatalf("broker state %s is not under the private state dir %s (%v)", privateState,
			paths.GlobalStorage(), err)
	}
	machine := filepath.Join(paths.GlobalStorageUnder(hostHome), rel)
	if _, err := os.Lstat(machine); !errors.Is(err, fs.ErrNotExist) {
		t.Skipf("this machine's openai-auth-broker state %s exists (%v) — it may hold a real "+
			"OpenAI grant, and while this test owns the machine-wide singleton every other "+
			"launch and `yolo openai-auth` command on the machine would use the test's forged "+
			"state instead. Refusing to run; ci.yml's integration job runs this test", machine, err)
	}
}

// clearGrantlessSingleton makes the singleton slot free for this test, or skips it. See the file
// header for the rule; the short form is that a singleton is stopped only when it demonstrably
// holds no grant.
func clearGrantlessSingleton(t *testing.T) {
	t.Helper()
	pid := singletonPID()
	if pid == 0 || processGone(pid) {
		return
	}
	state, ok := daemonStateFile(pid)
	if !ok {
		t.Skipf("an openai-auth-broker singleton is alive (pid %d) and its state file cannot be "+
			"read from its command line, so whether it holds a real grant is unknown; this test "+
			"would have to replace it, and will not", pid)
	}
	if _, err := os.Lstat(state); !errors.Is(err, fs.ErrNotExist) {
		t.Skipf("an openai-auth-broker singleton is alive (pid %d) with state at %s, which exists "+
			"(%v) — it may hold this machine's real OpenAI grant, and a launch reuses whichever "+
			"singleton is alive. Refusing to touch it; ci.yml's integration job runs this test",
			pid, state, err)
	}
	t.Logf("stopping a grantless openai-auth-broker singleton (pid %d, state %s absent) so this "+
		"test can own the slot; the next launch that needs one starts it again", pid, state)
	if err := stopSingletonPID(pid); err != nil {
		t.Skipf("could not stop the grantless singleton: %v", err)
	}
}

// requireOwnSingleton returns the PID of the live singleton after proving it was started with
// wantState — the precondition for importing anything into it.
func requireOwnSingleton(t *testing.T, wantState string) int {
	t.Helper()
	pid := singletonPID()
	if pid == 0 || processGone(pid) {
		t.Fatalf("no live openai-auth-broker singleton after `yolo openai-auth status` started one "+
			"(see %s)", filepath.Join(paths.GlobalStorage(), "logs"))
	}
	got, ok := daemonStateFile(pid)
	if !ok || got != wantState {
		t.Fatalf("the live openai-auth-broker singleton (pid %d) was started with state %q, not "+
			"this test's %q — something else started it, and importing into it would replace "+
			"its grant. Refusing", pid, got, wantState)
	}
	return pid
}

// openaiBrokerProbeScript is the in-jail half of the round trip, over BOTH routes a client in
// the jail has to the broker, and prints `KEY=value` lines for kvLine. Only the access token's
// HASH and non-secret fields are printed. It uses curl, jq and coreutils, all in the image's core
// (flake.nix, coreFloorNames).
//
//   - CODEX'S ROUTE: wait for the refresh endpoint Codex is handed to answer, then POST Codex's own
//     request shape to it — a JSON body (openaiauthadapter.readTokenRequest) presenting the
//     generation marker as its refresh token. This crosses the in-jail adapter.
//   - PI'S ROUTE: `yolo internal openai-auth-client token`, the command pi's extension
//     (packs/pi/extensions/yolo-openai-auth.js) execs. This goes straight to the published
//     endpoint, with no adapter in between, and asks for the `access` view.
func openaiBrokerProbeScript() string {
	return strings.Join([]string{
		`token_hash() { jq -r '.access_token // empty' | tr -d '\n' | sha256sum | cut -d' ' -f1; }`,
		`url="${CODEX_REFRESH_TOKEN_URL_OVERRIDE:-}"`,
		`echo "OVERRIDE_URL=${url:-unset}"`,
		`if [ -n "$url" ]; then`,
		`  ready=no`,
		// A GET is answered 404 by the adapter, and curl without -f exits 0 on any HTTP answer:
		// only "nothing is listening yet" keeps this loop going.
		`  for _ in $(seq 1 150); do if curl -s -o /dev/null --max-time 2 "$url"; then ready=yes; break; fi; sleep 0.2; done`,
		`  echo "ADAPTER_READY=$ready"`,
		`  resp=$(curl -sS --max-time 30 -X POST -H 'Content-Type: application/json' ` +
			`--data '{"client_id":"yolo-integration-probe","grant_type":"refresh_token","refresh_token":"yolo-broker:1"}' ` +
			`-w '\n%{http_code}' "$url")`,
		`  echo "HTTP_STATUS=$(printf '%s\n' "$resp" | tail -n 1)"`,
		`  body=$(printf '%s\n' "$resp" | sed '$d')`,
		`  echo "ACCESS_SHA256=$(printf '%s' "$body" | token_hash)"`,
		`  echo "REFRESH_FIELD=$(printf '%s' "$body" | jq -r '.refresh_token // empty')"`,
		`  echo "EXPIRES_IN=$(printf '%s' "$body" | jq -r '.expires_in // empty')"`,
		`  echo "TOKEN_TYPE=$(printf '%s' "$body" | jq -r '.token_type // empty')"`,
		`  echo "ERROR_FIELD=$(printf '%s' "$body" | jq -r '((.error // "") + " " + (.error_description // "")) | ltrimstr(" ")')"`,
		`fi`,
		// Its stderr carries the daemon's error line on failure, never a token.
		`view=$(yolo internal openai-auth-client token 2>/tmp/openai-auth-client.err)`,
		`echo "CLIENT_RC=$?"`,
		`echo "CLIENT_ACCESS_SHA256=$(printf '%s' "$view" | token_hash)"`,
		`echo "CLIENT_DECISION=$(printf '%s' "$view" | jq -r '.decision // empty')"`,
		`echo "CLIENT_STDERR=$(tr '\n' ' ' </tmp/openai-auth-client.err)"`,
	}, "\n")
}

// kvLine returns the value of the first `KEY=value` line in out, or "".
func kvLine(out, key string) string {
	for _, line := range strings.Split(out, "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), key+"="); ok {
			return v
		}
	}
	return ""
}

// TestOpenAIAuthBrokerRoundTripsAnImportedToken imports a FORGED LOGIN into a private state dir
// through `yolo openai-auth import`, launches a codex-selecting jail, and asks for the token over
// both in-jail routes (openaiBrokerProbeScript): Codex's refresh POST to the endpoint Codex is
// handed, and pi's `openai-auth-client token`. The access token that comes back is compared BY
// HASH, in the jail, so no token is ever printed: the forged one is harmless, but a run that
// reached the wrong broker would otherwise log a real one.
//
// What it asserts, and the defect each one would catch:
//
//   - CODEX_REFRESH_TOKEN_URL_OVERRIDE in the jail is codexRefreshURL: the pack's env reached the
//     jail, and the test POSTs where Codex would.
//   - HTTP 200 with the imported token's hash: the adapter is up, the front is reachable from the
//     jail (step 12's reachability half), the adapter decoded Codex's JSON body (the 2026-09-22
//     `ParseForm` defect), and the token is the one the import installed.
//   - refresh_token is the generation MARKER `yolo-broker:1`, never the canonical refresh token:
//     no agent receives or submits the canonical token.
//   - the client route returns the same hash with decision `cached`: the endpoint serves a client
//     that bypasses the adapter, and the broker answered from its cache.
//   - the canonical state is byte-for-byte unchanged afterwards, and the same daemon is still the
//     singleton: no upstream redemption happened, and the launch reused the daemon rather than
//     starting a second one.
func TestOpenAIAuthBrokerRoundTripsAnImportedToken(t *testing.T) {
	requireJail(t)
	if rt := detectRuntime(); rt != "podman" || goruntime.GOOS != "linux" {
		t.Skipf("this test proves singleton ownership from /proc and runs on podman on Linux — "+
			"ci.yml's rootless integration job, the instrument step 12 names; this is %q on %s",
			rt, goruntime.GOOS)
	}
	rootless := podmanRootless(t)
	if inContainer() {
		if c, err := net.DialTimeout("tcp", "127.0.0.1:1460", time.Second); err == nil {
			_ = c.Close()
			t.Skip("127.0.0.1:1460 already answers on this loopback, and a nested jail shares it " +
				"(podman-in-podman forces --net=host): the POST would reach the launching jail's " +
				"own adapter, which forwards to the HOST's real broker. This test runs in ci.yml's " +
				"integration job; a nested jail cannot test this path (AGENTS.md's first carve-out)")
		}
	}

	dir := writeProject(t, `{}`)
	packHome(t, `{"packs": ["codex"]}`)
	// A private ~/.local/share/yolo-jail for everything this test and its launch write — the
	// broker state above all. macArchivePrivateState is the one implementation of that, named
	// for its first caller; it keeps cache/ shared so the launch's downloads stay warm.
	macArchivePrivateState(t)
	statePath := filepath.Join(loopholes.StateDirFor(openaiauth.LoopholeName), openaiauth.StateFileName)
	if !strings.HasPrefix(statePath, paths.GlobalStorage()+string(filepath.Separator)) {
		t.Fatalf("broker state %s is not under the private state dir %s", statePath, paths.GlobalStorage())
	}

	skipIfMachineHoldsAGrant(t, statePath)
	clearGrantlessSingleton(t)

	// A no-op first, so the daemon exists and can be identified BEFORE anything is imported.
	if r := runYoloCLI(t, dir, "openai-auth", "status"); r.rc != 0 {
		t.Fatalf("`yolo openai-auth status` failed: rc %d\n%s", r.rc, r.combined())
	}
	pid := requireOwnSingleton(t, statePath)
	t.Cleanup(func() {
		if err := stopSingletonPID(pid); err != nil {
			t.Errorf("stopping this test's openai-auth-broker singleton: %v — a later launch "+
				"would reuse a daemon whose state dir is gone", err)
		}
	})

	forged := forgeCodexLogin(t)
	if r := runYoloCLI(t, dir, "openai-auth", "import", "--from", forged.path); r.rc != 0 {
		t.Fatalf("`yolo openai-auth import` failed: rc %d\n%s", r.rc, r.combined())
	}
	if got := requireOwnSingleton(t, statePath); got != pid {
		t.Fatalf("the singleton changed from pid %d to %d during the import", pid, got)
	}
	before, err := openaiauth.ReadState(statePath)
	if err != nil {
		t.Fatalf("reading the imported state: %v", err)
	}
	if before.AccessToken != forged.access || before.RefreshToken != forged.refresh || before.Generation != 1 {
		t.Fatalf("the import did not install the forged login as generation 1: generation %d, "+
			"access %s, refresh %s", before.Generation, openaiauth.TokenFingerprint(before.AccessToken),
			openaiauth.TokenFingerprint(before.RefreshToken))
	}

	r := runYolo(t, dir, openaiBrokerProbeScript(), withEnv("YOLO_NO_AUTO_IMAGE_REAP=1"))
	sum := sha256.Sum256([]byte(forged.access))
	wantHash := hex.EncodeToString(sum[:])

	stepSummary(t, fmt.Sprintf("### OpenAI broker round trip: podman rootless=%s; Codex route "+
		"HTTP %s, hash match=%v, refresh field %q; client route rc %s, hash match=%v, decision %q",
		rootless, kvLine(r.stdout, "HTTP_STATUS"), kvLine(r.stdout, "ACCESS_SHA256") == wantHash,
		kvLine(r.stdout, "REFRESH_FIELD"), kvLine(r.stdout, "CLIENT_RC"),
		kvLine(r.stdout, "CLIENT_ACCESS_SHA256") == wantHash, kvLine(r.stdout, "CLIENT_DECISION")))

	if r.rc != 0 {
		t.Fatalf("codex-selecting launch failed: rc %d\nstdout: %s\nstderr: %s", r.rc, r.stdout, r.stderr)
	}
	if got := kvLine(r.stdout, "OVERRIDE_URL"); got != codexRefreshURL {
		t.Fatalf("CODEX_REFRESH_TOKEN_URL_OVERRIDE in the jail is %q, want %q — the codex pack's env "+
			"did not reach the jail, or moved\nstdout: %s", got, codexRefreshURL, r.stdout)
	}
	if kvLine(r.stdout, "ADAPTER_READY") != "yes" {
		t.Errorf("nothing answered on %s within 30s of the jail command starting — the "+
			"openai-auth-adapter jail daemon did not come up\nstdout: %s\nstderr: %s",
			codexRefreshURL, r.stdout, r.stderr)
	}
	if got := kvLine(r.stdout, "HTTP_STATUS"); got != "200" {
		t.Errorf("the refresh endpoint answered HTTP %s (error: %q), want 200\nstdout: %s\nstderr: %s",
			got, kvLine(r.stdout, "ERROR_FIELD"), r.stdout, r.stderr)
	}
	if got := kvLine(r.stdout, "ACCESS_SHA256"); got != wantHash {
		t.Errorf("the access token served in the jail hashes to %s, want %s (the imported forged "+
			"login's) — the jail reached a different broker, or a different grant", got, wantHash)
	}
	if got := kvLine(r.stdout, "REFRESH_FIELD"); got != "yolo-broker:1" {
		t.Errorf("refresh_token in the jail's view is %q, want the generation marker "+
			"\"yolo-broker:1\" — no agent may receive the canonical refresh token", got)
	}
	if strings.Contains(r.combined(), forged.refresh) || strings.Contains(r.combined(), forged.access) {
		t.Errorf("a forged token appeared verbatim in the launch output; this test prints hashes only")
	}
	if n, err := strconv.Atoi(kvLine(r.stdout, "EXPIRES_IN")); err != nil || n <= 0 {
		t.Errorf("expires_in is %q, want a positive number of seconds", kvLine(r.stdout, "EXPIRES_IN"))
	}
	if got := kvLine(r.stdout, "TOKEN_TYPE"); got != "Bearer" {
		t.Errorf("token_type is %q, want Bearer", got)
	}
	if got := kvLine(r.stdout, "CLIENT_RC"); got != "0" {
		t.Errorf("`yolo internal openai-auth-client token` (pi's route) exited %s in the jail: %s",
			got, kvLine(r.stdout, "CLIENT_STDERR"))
	}
	if got := kvLine(r.stdout, "CLIENT_ACCESS_SHA256"); got != wantHash {
		t.Errorf("pi's route served an access token hashing to %s, want %s (the imported forged "+
			"login's)", got, wantHash)
	}
	if got := kvLine(r.stdout, "CLIENT_DECISION"); got != string(openaiauth.DecisionCached) {
		t.Errorf("pi's route reported decision %q, want %q — a generation a day from expiry must be "+
			"served from cache, never redeemed", got, openaiauth.DecisionCached)
	}

	after, err := openaiauth.ReadState(statePath)
	if err != nil {
		t.Fatalf("reading the state after the round trip: %v", err)
	}
	if after != before {
		t.Errorf("the canonical state changed across a round trip that was not due for a day "+
			"(generation %d → %d, access %s → %s) — the broker redeemed upstream, or rewrote the "+
			"state, when it should have served its cache", before.Generation, after.Generation,
			openaiauth.TokenFingerprint(before.AccessToken), openaiauth.TokenFingerprint(after.AccessToken))
	}
	if got := singletonPID(); got != pid {
		t.Errorf("the singleton is pid %d after the launch, not this test's %d — the launch "+
			"replaced the daemon instead of reusing it", got, pid)
	}
}
