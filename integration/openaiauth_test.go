package integration

// openaiauth_test.go is step 12's first artifact in docs/design/openai-auth-broker-plan.md: a
// jail whose `packs` selects `codex`, completing a brokered token round trip through the
// endpoint Codex is told to use. It is the one test the unit suite cannot represent, because
// every hop it crosses is a different process on a different side of the jail boundary:
//
//	curl (in the jail) → yolo-jaild openai-auth-adapter on its served address (in the jail)
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
// # What a nested jail can and cannot tell
//
// Podman-in-podman forces --net=host, so a nested jail shares the launching jail's loopback,
// where that jail's own adapter already holds 127.0.0.1:1460. This test used to skip there, since
// the nested POST reached the outer adapter and the HOST's real broker. Since served addresses
// (docs/plans/notch-convergence.md NC-D41) a jail on a shared namespace serves its adapter on a
// port its launch picked, and the pointer Codex is handed names that port, so the nested run
// reaches its own adapter and this test runs there. What it still cannot see is AGENTS.md's first
// carve-out: reachability across the host loopback, whose instrument is ci.yml's rootless
// `integration` job, where the jail is bridged and serves the declared 1460.
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
	"os"
	"path/filepath"
	goruntime "runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/openaiauth"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// codexRefreshListen and codexRefreshPath are where packs/codex/pack.json points Codex
// (CODEX_REFRESH_TOKEN_URL_OVERRIDE, "http://{listen}/oauth/token") on a bridged jail: the
// openai-auth-broker manifest's declared `jail_daemon.listen`, and the adapter's path. Spelled
// literally, and asserted against the jail's own environment (servedURLProblem), so a pack that
// moved the URL fails here instead of this test quietly following it.
const (
	codexRefreshListen = "127.0.0.1:1460"
	codexRefreshPath   = "/oauth/token"
)

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
func singletonPID() int { return hostDaemonPID(openaiauth.LoopholeName) }

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

// stopSingletonPID stops the openai-auth-broker singleton process pid and removes its
// rendezvous files while its PID file still names it — homedaemons_test.go's stopHostDaemonPID,
// which never falls back to a process search.
func stopSingletonPID(pid int) error { return stopHostDaemonPID(openaiauth.LoopholeName, pid) }

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
		// A STRANGER first: the plain generation marker carries no caller token, so the adapter
		// refuses it before the broker is asked (docs/plans/notch-convergence.md §2.3).
		`  stranger=$(curl -sS --max-time 30 -X POST -H 'Content-Type: application/json' ` +
			`--data '{"client_id":"yolo-integration-probe","grant_type":"refresh_token","refresh_token":"yolo-broker:1"}' ` +
			`-o /dev/null -w '%{http_code}' "$url")`,
		`  echo "STRANGER_STATUS=$stranger"`,
		// Then Codex's own marker, as its launcher's auth.json writer binds it: the broker's
		// generation marker with this launch's caller token after a dot.
		`  tok="${YOLO_SERVICE_OPENAI_AUTH_BROKER_TOKEN:-}"`,
		`  echo "CALLER_TOKEN_SET=$([ -n "$tok" ] && echo yes || echo no)"`,
		`  resp=$(curl -sS --max-time 30 -X POST -H 'Content-Type: application/json' ` +
			`--data "{\"client_id\":\"yolo-integration-probe\",\"grant_type\":\"refresh_token\",\"refresh_token\":\"yolo-broker:1.$tok\"}" ` +
			`-w '\n%{http_code}' "$url")`,
		`  echo "HTTP_STATUS=$(printf '%s\n' "$resp" | tail -n 1)"`,
		`  body=$(printf '%s\n' "$resp" | sed '$d')`,
		`  echo "ACCESS_SHA256=$(printf '%s' "$body" | token_hash)"`,
		`  refresh=$(printf '%s' "$body" | jq -r '.refresh_token // empty')`,
		// Printed with the token cut off, so the log never holds it.
		`  echo "REFRESH_FIELD=${refresh%%.*}"`,
		`  echo "REFRESH_BOUND=$([ "$refresh" = "yolo-broker:1.$tok" ] && echo yes || echo no)"`,
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
//   - CODEX_REFRESH_TOKEN_URL_OVERRIDE in the jail is the adapter's served address: the declared
//     codexRefreshListen on a bridged jail, a picked port on a nested one. The pack's env reached
//     the jail, and the test POSTs where Codex would.
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
	// EXCLUSIVE: this test owns the machine's openai-auth-broker singleton for its duration,
	// and another run's claude, codex or pi launch would adopt it, or be the live singleton
	// this test stops (machinelock_test.go).
	requireJailExclusive(t, "TestOpenAIAuthBrokerRoundTripsAnImportedToken owns the "+
		"openai-auth-broker host singleton")
	if rt := detectRuntime(); rt != "podman" || goruntime.GOOS != "linux" {
		t.Skipf("this test proves singleton ownership from /proc and runs on podman on Linux — "+
			"ci.yml's rootless integration job, the instrument step 12 names; this is %q on %s",
			rt, goruntime.GOOS)
	}
	rootless := podmanRootless(t)

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
	overrideURL := kvLine(r.stdout, "OVERRIDE_URL")
	if p := servedURLProblem(overrideURL, codexRefreshListen, codexRefreshPath, inContainer()); p != "" {
		t.Fatalf("CODEX_REFRESH_TOKEN_URL_OVERRIDE in the jail is %s — the codex pack's env did not "+
			"reach the jail, or moved\nstdout: %s", p, r.stdout)
	}
	if kvLine(r.stdout, "ADAPTER_READY") != "yes" {
		t.Errorf("nothing answered on %s within 30s of the jail command starting — the "+
			"openai-auth-adapter jail daemon did not come up\nstdout: %s\nstderr: %s",
			overrideURL, r.stdout, r.stderr)
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
	// CALLER AUTHENTICATION (docs/plans/notch-convergence.md §2.3): the launch handed the jail
	// a caller token, the adapter refused the plain marker a stranger would send, and it bound
	// the token into the marker it answered with, so Codex's next refresh carries it too.
	if got := kvLine(r.stdout, "CALLER_TOKEN_SET"); got != "yes" {
		t.Errorf("the jail has no $YOLO_SERVICE_OPENAI_AUTH_BROKER_TOKEN, so the adapter serves nobody")
	}
	if got := kvLine(r.stdout, "STRANGER_STATUS"); got != "401" {
		t.Errorf("a refresh without the caller token got HTTP %s, want 401", got)
	}
	if got := kvLine(r.stdout, "REFRESH_BOUND"); got != "yes" {
		t.Errorf("the adapter's answer did not bind this launch's caller token into the marker")
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
