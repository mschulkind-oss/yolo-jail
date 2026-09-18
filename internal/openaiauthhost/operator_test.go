package openaiauthhost

// operator_test.go drives the host operator verb end to end against the REAL host handler on a
// socket the test serves itself — so what is pinned is the whole path a human takes: the verb
// resolves the private socket, states what a mutation will do, and the daemon performs it.
//
// No agent process, no API call, no host daemon: the singleton resolver is the injected seam
// (`prepare` already uses it for the same dependency) and the upstream refresher is never
// reached, because `import` installs tokens and `logout` deletes them without redeeming
// anything.

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/hostservice"
	"github.com/mschulkind-oss/yolo-jail/internal/openaiauth"
	"github.com/mschulkind-oss/yolo-jail/internal/openaiauthdaemon"
)

// operatorDaemon serves the daemon's PRIVATE-socket handler over a plain Unix socket, exactly
// as serveSockets gives that handler to the private path, and returns an `ensure` the verb can
// be injected with plus the broker's state path.
func operatorDaemon(t *testing.T) (ensure func(io.Writer) (string, error), statePath string) {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "yj-oaop-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "broker.sock.host")
	statePath = filepath.Join(dir, "credentials.json")
	handler := openaiauthdaemon.BuildHostHandler(openaiauthdaemon.HandlerConfig{
		Broker: openaiauth.Broker{
			StatePath: statePath, LockPath: filepath.Join(dir, "refresh.lock"),
		},
	})
	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })
	go func() { _ = hostservice.ServeUnix(handler, socket, stop) }()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(socket); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the test daemon socket was not published")
		}
		time.Sleep(2 * time.Millisecond)
	}
	return func(io.Writer) (string, error) { return socket, nil }, statePath
}

// codexAuthFixture writes a credential file shaped like the one Codex writes from its own
// login: the expiry lives in the access token's JWT, because auth.json records none.
func codexAuthFixture(t *testing.T, refresh string) string {
	t.Helper()
	claims := `{"exp":` + strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10) + `}`
	token := "header." + base64.RawURLEncoding.EncodeToString([]byte(claims)) + ".signature"
	path := filepath.Join(t.TempDir(), "auth.json")
	body := `{"auth_mode":"chatgpt","OPENAI_API_KEY":null,"tokens":{"id_token":"` + token +
		`","access_token":"` + token + `","refresh_token":"` + refresh +
		`","account_id":"acct-op"},"last_refresh":"2026-09-18T00:00:00Z"}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestOperatorImportsThenLogsOutTheMachineWideGrant(t *testing.T) {
	ensure, statePath := operatorDaemon(t)
	source := codexAuthFixture(t, "refresh-operator")

	var stdout, stderr bytes.Buffer
	if rc := runOperator([]string{"import", "--from", source}, ensure, &stdout, &stderr); rc != 0 {
		t.Fatalf("import rc = %d\nstdout: %s\nstderr: %s", rc, stdout.String(), stderr.String())
	}
	// THE SCOPE IS STATED BEFORE THE ACT, which is the whole reason this verb exists rather
	// than a raw client call: a user typing `import` may not know it replaces the credential
	// every workspace and every jail on this machine uses.
	if !strings.Contains(stderr.String(), "MACHINE-WIDE") {
		t.Errorf("import said nothing about its scope: %q", stderr.String())
	}
	state, err := openaiauth.ReadState(statePath)
	if err != nil {
		t.Fatalf("read canonical state after import: %v", err)
	}
	if state.RefreshToken != "refresh-operator" || state.Generation != 1 {
		t.Fatalf("state = %+v, want the imported credential as generation 1", state)
	}

	// STATUS is not a mutation: no notice, and no token in the report.
	stdout.Reset()
	stderr.Reset()
	if rc := runOperator([]string{"status"}, ensure, &stdout, &stderr); rc != 0 {
		t.Fatalf("status rc = %d stderr=%s", rc, stderr.String())
	}
	if strings.Contains(stderr.String(), "MACHINE-WIDE") {
		t.Errorf("status printed a mutation notice: %q", stderr.String())
	}
	if strings.Contains(stdout.String(), "refresh-operator") {
		t.Errorf("status leaked the canonical refresh token: %s", stdout.String())
	}
	var status struct {
		LoggedIn bool `json:"logged_in"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &status); err != nil || !status.LoggedIn {
		t.Errorf("status = %s (%v), want logged_in after an import", stdout.String(), err)
	}

	// LOGOUT removes it, says so first, and is idempotent.
	stdout.Reset()
	stderr.Reset()
	if rc := runOperator([]string{"logout"}, ensure, &stdout, &stderr); rc != 0 {
		t.Fatalf("logout rc = %d stderr=%s", rc, stderr.String())
	}
	if !strings.Contains(stderr.String(), "every workspace, every jail") {
		t.Errorf("logout did not state its scope before acting: %q", stderr.String())
	}
	if _, err := os.Stat(statePath); !os.IsNotExist(err) {
		t.Errorf("canonical state survived logout: %v", err)
	}
	if rc := runOperator([]string{"logout"}, ensure, &stdout, &stderr); rc != 0 {
		t.Errorf("a second logout rc = %d, want an idempotent success", rc)
	}
}

// The verb refuses what it cannot do, and answers with the three subcommands that exist rather
// than with a client-level "unknown command".
func TestOperatorRefusalsNameTheVerbsThatExist(t *testing.T) {
	ensure, _ := operatorDaemon(t)
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"no args", nil},
		{"unknown", []string{"replace"}},
		{"a jail verb", []string{"token"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if rc := runOperator(tc.args, ensure, &stdout, &stderr); rc != 2 {
				t.Fatalf("rc = %d, want 2", rc)
			}
			if !strings.Contains(stderr.String(), "status|import|logout") {
				t.Errorf("refusal does not name the verbs: %q", stderr.String())
			}
		})
	}
	// An import with no file named is refused by the client's own flag check, before the
	// socket is dialled — so the daemon never sees a request it would only refuse.
	var stdout, stderr bytes.Buffer
	if rc := runOperator([]string{"import"}, ensure, &stdout, &stderr); rc != 2 ||
		!strings.Contains(stderr.String(), "--from") {
		t.Errorf("import with no --from: rc = %d, stderr = %q", rc, stderr.String())
	}
}

// A BROKER VIEW IS NOT AN IMPORT SOURCE, asserted through the verb because that is where a
// human would do it: `yolo internal openai-auth-client token --codex-auth <path>` writes a
// view whose refresh_token is a `yolo-broker:<generation>` marker, and importing one would
// install a marker as the canonical credential — logging the machine out of a grant nothing
// could recover. The refusal must survive the round trip, not just exist in the decoder.
func TestOperatorRefusesImportingABrokerView(t *testing.T) {
	ensure, statePath := operatorDaemon(t)
	source := codexAuthFixture(t, "yolo-broker:4")
	var stdout, stderr bytes.Buffer
	if rc := runOperator([]string{"import", "--from", source}, ensure, &stdout, &stderr); rc == 0 {
		t.Fatal("importing a yolo broker view succeeded")
	}
	if !strings.Contains(stderr.String(), "generation marker") {
		t.Errorf("refusal = %q, want it to name the marker", stderr.String())
	}
	if _, err := os.Stat(statePath); !os.IsNotExist(err) {
		t.Error("a refused import still wrote canonical state")
	}
}
