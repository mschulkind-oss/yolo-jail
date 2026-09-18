package openaiauthdaemon

// hostactions_test.go pins the two MACHINE-WIDE mutations — `import` and `logout` — to the
// private host socket, and pins their refusal on the jail-facing one.
//
// THE REFUSAL IS ASSERTED THROUGH THE REAL FRONT, not against jailActionAllowed. The gate was
// already unit-tested (TestJailActionsExcludeMachineWideDestructiveOperations) while nothing
// asserted that the fronted SOCKET is the one it guards — a callee pinned with its call site
// unpinned, which is the shape AGENTS.md says this repo has shipped five times. So these tests
// run serveSockets with both real handlers, publish a real loopback-TLS front over the fronted
// socket, and dial each door the way its client dials it.
//
// The mutation that matters: hand serveSockets the same handler twice (either way round) and
// one half of TestHostSocketServesMachineWideMutationsAndTheFrontRefusesThem fails.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/openaiauth"
	"github.com/mschulkind-oss/yolo-jail/internal/openauthclient"
	"github.com/mschulkind-oss/yolo-jail/internal/svcendpoint"
)

// codexAuthJSON is a credential file shaped like the one Codex writes from its own login: the
// access token is a real JWT carrying `exp`, because that is the only place an expiry exists
// (auth.json records none), and the refresh token is a plain opaque credential — NOT a
// `yolo-broker:` marker, which is the one thing import must refuse.
func codexAuthJSON(exp int64, refresh string) []byte {
	return []byte(`{"auth_mode":"chatgpt","OPENAI_API_KEY":null,"tokens":{` +
		`"id_token":"` + jwt(`{"exp":`+strconv.FormatInt(exp, 10)+`}`) + `",` +
		`"access_token":"` + jwt(`{"exp":`+strconv.FormatInt(exp, 10)+`,"https://api.openai.com/auth":{"chatgpt_account_id":"acct-7"}}`) + `",` +
		`"refresh_token":"` + refresh + `","account_id":"acct-7"},` +
		`"last_refresh":"2026-09-18T00:00:00Z"}`)
}

// hostAndFrontedDoors starts the daemon's two sockets with the two real handlers and returns
// the private host socket path, the published endpoint file, and the broker's state path.
func hostAndFrontedDoors(t *testing.T) (hostSocket, endpoint, statePath string) {
	t.Helper()
	dir := shortSocketDir(t)
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	fronted := filepath.Join(dir, "fronted.sock")
	hostSocket = HostSocketPath(fronted)
	endpoint = filepath.Join(dir, "broker.endpoint")
	statePath = filepath.Join(dir, "credentials.json")

	config := HandlerConfig{Broker: openaiauth.Broker{
		StatePath: statePath, LockPath: filepath.Join(dir, "refresh.lock"),
	}}
	stop := make(chan struct{})
	frontStop := make(chan struct{})
	var once sync.Once
	shutdown := func() { once.Do(func() { close(stop) }) }
	done := make(chan error, 1)
	go func() {
		done <- serveSockets(BuildHandler(config), BuildHostHandler(config),
			fronted, hostSocket, stop, shutdown)
	}()
	t.Cleanup(func() {
		close(frontStop)
		shutdown()
		<-done
	})
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(hostSocket); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the private host socket was not published")
		}
		time.Sleep(2 * time.Millisecond)
	}
	go func() { _ = svcendpoint.ServeFront(endpoint, "127.0.0.1", fronted, frontStop) }()
	for !svcendpoint.Probe(endpoint) {
		if time.Now().After(deadline) {
			t.Fatal("the jail-facing endpoint was not published")
		}
		time.Sleep(2 * time.Millisecond)
	}
	return hostSocket, endpoint, statePath
}

func TestHostSocketServesMachineWideMutationsAndTheFrontRefusesThem(t *testing.T) {
	hostSocket, endpoint, statePath := hostAndFrontedDoors(t)
	source := filepath.Join(t.TempDir(), "auth.json")
	original := codexAuthJSON(time.Now().Add(time.Hour).Unix(), "refresh-from-codex")
	if err := os.WriteFile(source, original, 0o600); err != nil {
		t.Fatal(err)
	}

	// IMPORT, on the private socket: it installs the first canonical generation.
	response, err := openauthclient.RequestUnix(hostSocket,
		map[string]any{"action": "import", "path": source}, nil)
	if err != nil {
		t.Fatalf("import on the host socket failed: %v", err)
	}
	var imported struct {
		OK         bool   `json:"ok"`
		Generation int64  `json:"generation"`
		AccountID  string `json:"account_id"`
	}
	if err := json.Unmarshal(response, &imported); err != nil {
		t.Fatal(err)
	}
	if !imported.OK || imported.Generation != 1 || imported.AccountID != "acct-7" {
		t.Fatalf("import reply = %+v, want the first generation and the file's account", imported)
	}
	// The CANONICAL state now holds the file's refresh token, which is the whole point: a
	// reply of ok with nothing installed would pass every assertion above.
	state, err := openaiauth.ReadState(statePath)
	if err != nil {
		t.Fatalf("read canonical state: %v", err)
	}
	if state.RefreshToken != "refresh-from-codex" {
		t.Errorf("canonical refresh token = %q, want the imported credential", state.RefreshToken)
	}
	// AND THE SOURCE IS UNTOUCHED. `import` reads a human's file; a reader that rewrote it
	// would be a second writer of the credential the broker now owns.
	if after, err := os.ReadFile(source); err != nil || string(after) != string(original) {
		t.Errorf("import modified the file it read (%v)", err)
	}

	// THE SAME ACTION, THROUGH THE FRONT: refused, with the jail-facing message, and the
	// canonical state is untouched.
	var diagnostic strings.Builder
	if _, err := openauthclient.Request(endpoint,
		map[string]any{"action": "import", "path": source}, &diagnostic); err == nil {
		t.Fatal("import succeeded through the jail-facing front — the machine-wide grant is " +
			"writable from inside a jail")
	}
	if !strings.Contains(diagnostic.String(), "unavailable from a jail") {
		t.Errorf("front refusal = %q, want the jail-facing refusal", diagnostic.String())
	}

	// LOGOUT is refused on the front too, and the state survives the attempt.
	diagnostic.Reset()
	if _, err := openauthclient.Request(endpoint, map[string]any{"action": "logout"}, &diagnostic); err == nil {
		t.Fatal("logout succeeded through the jail-facing front — a jail can log the whole " +
			"machine out")
	}
	if _, err := os.Stat(statePath); err != nil {
		t.Fatalf("a refused logout still removed the canonical state: %v", err)
	}

	// LOGOUT on the private socket removes it, and says so.
	response, err = openauthclient.RequestUnix(hostSocket, map[string]any{"action": "logout"}, nil)
	if err != nil {
		t.Fatalf("logout on the host socket failed: %v", err)
	}
	var loggedOut struct {
		OK        bool `json:"ok"`
		LoggedOut bool `json:"logged_out"`
	}
	if err := json.Unmarshal(response, &loggedOut); err != nil {
		t.Fatal(err)
	}
	if !loggedOut.OK || !loggedOut.LoggedOut {
		t.Errorf("logout reply = %s, want ok and logged_out", response)
	}
	if _, err := os.Stat(statePath); !os.IsNotExist(err) {
		t.Errorf("canonical state survived logout: %v", err)
	}
	// IDEMPOTENT: missing state is already logged out, so a second call is a success rather
	// than an error every script has to special-case.
	if _, err := openauthclient.RequestUnix(hostSocket, map[string]any{"action": "logout"}, nil); err != nil {
		t.Errorf("a second logout failed: %v", err)
	}
}

// The gate's two halves, as data: the private socket serves everything the front serves, plus
// exactly the two mutations.
func TestHostActionsAreTheJailActionsPlusTheTwoMutations(t *testing.T) {
	for _, action := range []string{"ping", "token", "refresh", "status", "login", "import", "logout"} {
		if !hostActionAllowed(action) {
			t.Errorf("the private host socket refuses %q", action)
		}
	}
	for _, action := range []string{"replace", "", "delete-everything"} {
		if hostActionAllowed(action) {
			t.Errorf("the private host socket allows the unknown action %q", action)
		}
	}
	for _, action := range []string{"import", "logout"} {
		if jailActionAllowed(action) {
			t.Fatalf("%q reached the jail gate — the two handlers have collapsed into one", action)
		}
	}
}

// WHAT IMPORT REFUSES. Each case is a refusal rather than a best effort, and the reasons are in
// import.go; this is the executable half.
func TestImportRefusesWhatItCannotSafelyInstall(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "auth.json")
	if err := os.WriteFile(real, codexAuthJSON(time.Now().Add(time.Hour).Unix(), "refresh-1"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.json")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct{ name, path, want string }{
		{"empty", "", "needs the path"},
		{"relative", "auth.json", "must be absolute"},
		{"missing", filepath.Join(dir, "nope.json"), "read Codex credential file"},
		{"symlink", link, "refusing to import a symlink"},
		{"directory", dir, "non-regular file"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ReadCodexAuthFile(tc.path)
			if err == nil {
				t.Fatalf("ReadCodexAuthFile(%q) succeeded", tc.path)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to name %q", err.Error(), tc.want)
			}
		})
	}

	// THE MARKER, which is the dangerous one: openauthclient.WriteCodexAuth renders a broker
	// VIEW whose refresh_token is `yolo-broker:<generation>`, so a file this tree wrote is
	// exactly the file this reader must refuse — importing one would install a marker as the
	// canonical credential and log the machine out of a grant nothing could recover.
	marker := filepath.Join(dir, "view.json")
	if err := os.WriteFile(marker, codexAuthJSON(time.Now().Add(time.Hour).Unix(), "yolo-broker:3"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadCodexAuthFile(marker); err == nil ||
		!strings.Contains(err.Error(), "generation marker") {
		t.Fatalf("importing a broker view: err = %v, want a refusal naming the marker", err)
	}

	for _, tc := range []struct{ name, body, want string }{
		{"malformed", `{"tokens":`, "decode Codex credential file"},
		{"no refresh", `{"tokens":{"id_token":"i","access_token":"a"}}`, "missing an access, id or refresh token"},
		{"no access", `{"tokens":{"id_token":"i","refresh_token":"r"}}`, "missing an access, id or refresh token"},
		{"no id", `{"tokens":{"access_token":"a","refresh_token":"r"}}`, "missing an access, id or refresh token"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := decodeCodexAuth([]byte(tc.body), time.Now()); err == nil ||
				!strings.Contains(err.Error(), tc.want) {
				t.Errorf("decode(%s) = %v, want a refusal naming %q", tc.body, err, tc.want)
			}
		})
	}
}

// AND WHAT IT ACCEPTS: a file whose access token has already lapsed.
//
// The plan asked import to refuse an "expired" file and that clause is overtaken — an access
// token's lifetime is minutes, so a file on disk has almost always outlived it, while the thing
// being imported is the REFRESH token. The expiry it carries becomes the broker's own, so the
// first request refreshes a generation that is already due.
func TestImportAcceptsALapsedAccessTokenAndCarriesItsExpiry(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	lapsed := now.Add(-2 * time.Hour)
	tokens, err := decodeCodexAuth(codexAuthJSON(lapsed.Unix(), "refresh-2"), now)
	if err != nil {
		t.Fatalf("a lapsed access token was refused: %v", err)
	}
	if !tokens.ExpiresAt.Equal(lapsed) {
		t.Errorf("ExpiresAt = %v, want the access token's own exp claim (%v)", tokens.ExpiresAt, lapsed)
	}
	if tokens.RefreshToken != "refresh-2" || tokens.AccountID != "acct-7" {
		t.Errorf("tokens = %+v, want the file's credential and account", tokens)
	}

	// AN UNREADABLE EXPIRY IS "DUE NOW", not an error: the refresh token is what is being
	// imported, and a generation that is already due is refreshed on first use.
	opaque := []byte(`{"tokens":{"id_token":"i","access_token":"not-a-jwt","refresh_token":"r"}}`)
	tokens, err = decodeCodexAuth(opaque, now)
	if err != nil {
		t.Fatalf("an opaque access token was refused: %v", err)
	}
	if !tokens.ExpiresAt.Equal(now) {
		t.Errorf("ExpiresAt = %v, want the caller's clock (due now)", tokens.ExpiresAt)
	}
}
