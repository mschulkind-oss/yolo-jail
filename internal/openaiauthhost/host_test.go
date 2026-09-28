package openaiauthhost

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/tomlx"
)

func codexViewFixture() json.RawMessage {
	return json.RawMessage(`{"access_token":"access","id_token":"id","refresh_token":"yolo-broker:7","expires_at":4102444800000,"account_id":"acct"}`)
}

func TestPreparePiUsesHostSocketWithoutStartingAdapter(t *testing.T) {
	d := deps{
		ensure: func(io.Writer) (string, error) { return "/tmp/broker.host", nil },
		request: func(_ string, request any, _ io.Writer) (json.RawMessage, error) {
			return json.RawMessage(`{"logged_in":true}`), nil
		},
	}
	launch, err := prepare(d, "pi", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if launch.vars["YOLO_OPENAI_AUTH_HOST_SOCKET"] != "/tmp/broker.host" || launch.listener != nil {
		t.Fatalf("Pi launch = %#v", launch)
	}
}

func TestManagedCodexHomeLeavesOrdinaryAuthUntouchedAndAdapterFollowsAgent(t *testing.T) {
	root := t.TempDir()
	ordinary := filepath.Join(root, "home", ".codex")
	managedStore := filepath.Join(root, "store")
	if err := os.MkdirAll(filepath.Join(ordinary, "skills"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ordinary, "config.toml"), []byte("model = 'host'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ordinaryAuth := []byte(`{"refresh_token":"ordinary-secret"}`)
	if err := os.WriteFile(filepath.Join(ordinary, "auth.json"), ordinaryAuth, 0o600); err != nil {
		t.Fatal(err)
	}
	d := deps{
		ensure: func(io.Writer) (string, error) { return "/tmp/broker.host", nil },
		request: func(_ string, request any, _ io.Writer) (json.RawMessage, error) {
			action := request.(map[string]any)["action"]
			if action == "status" {
				return json.RawMessage(`{"logged_in":true}`), nil
			}
			return codexViewFixture(), nil
		},
		listen:    net.Listen,
		home:      func() string { return filepath.Join(root, "home") },
		storage:   func() string { return managedStore },
		workspace: func() (string, error) { return filepath.Join(root, "work", "repo"), nil },
		newToken:  func() (string, error) { return strings.Repeat("5a", 32), nil },
	}
	launch, err := prepare(d, "codex", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	managed := filepath.Join(managedStore, "host-agents", "codex")
	if launch.vars["CODEX_HOME"] != managed {
		t.Fatalf("CODEX_HOME = %q", launch.vars["CODEX_HOME"])
	}
	managedConfig, err := tomlx.DecodeFile(filepath.Join(managed, "config.toml"))
	if err != nil {
		t.Fatalf("decode managed config: %v", err)
	}
	if managedConfig["model"] != "host" {
		t.Errorf("managed config lost ordinary host keys: %v", managedConfig)
	}
	projects, _ := managedConfig["projects"].(map[string]any)
	project, _ := projects[filepath.Join(root, "work", "repo")].(map[string]any)
	if project["trust_level"] != "trusted" {
		t.Errorf("managed config workspace trust = %v", projects)
	}
	if _, wrong := projects["/workspace"]; wrong {
		t.Errorf("managed host config contains the container workspace: %v", projects)
	}
	if got, err := os.ReadFile(filepath.Join(ordinary, "config.toml")); err != nil || string(got) != "model = 'host'\n" {
		t.Fatalf("ordinary config changed: %q, %v", got, err)
	}
	if got, _ := os.ReadFile(filepath.Join(ordinary, "auth.json")); !bytes.Equal(got, ordinaryAuth) {
		t.Fatalf("ordinary auth changed: %s", got)
	}
	managedAuth, err := os.ReadFile(filepath.Join(managed, "auth.json"))
	if err != nil || !strings.Contains(string(managedAuth), "yolo-broker:7") || strings.Contains(string(managedAuth), "ordinary-secret") {
		t.Fatalf("managed auth = %s, %v", managedAuth, err)
	}
	address := launch.listener.Addr().String()
	rc, handled := launch.Run("/bin/sh", []string{"sh", "-c", "test -n \"$CODEX_HOME\""}, launch.Environ(os.Environ()), nil, io.Discard, io.Discard)
	if !handled || rc != 0 {
		t.Fatalf("Run = %d, %v", rc, handled)
	}
	if conn, err := net.Dial("tcp", address); err == nil {
		conn.Close()
		t.Fatal("Codex adapter still accepts connections after agent exit")
	}
}

func TestPrepareStartsBrowserLoginOnlyWhenStatusRequiresIt(t *testing.T) {
	var actions []string
	d := deps{
		ensure: func(io.Writer) (string, error) { return "/tmp/broker.host", nil },
		request: func(_ string, request any, _ io.Writer) (json.RawMessage, error) {
			action := request.(map[string]any)["action"].(string)
			actions = append(actions, action)
			if action == "status" {
				return json.RawMessage(`{"logged_in":false}`), nil
			}
			return json.RawMessage(`{"ok":true}`), nil
		},
	}
	if _, err := prepare(d, "pi", io.Discard); err != nil {
		t.Fatal(err)
	}
	if strings.Join(actions, ",") != "status,login" {
		t.Fatalf("actions = %v", actions)
	}
}

func TestManagedEnvironmentOverridesHaveStableOrder(t *testing.T) {
	launch := &Launch{vars: map[string]string{"Z_LAST": "z", "A_FIRST": "a"}}
	want := []string{"KEEP=1", "A_FIRST=a", "Z_LAST=z"}
	for i := 0; i < 20; i++ {
		got := launch.Environ([]string{"KEEP=1", "Z_LAST=old"})
		if strings.Join(got, "\n") != strings.Join(want, "\n") {
			t.Fatalf("Environ = %v, want stable %v", got, want)
		}
	}
}

func TestHostSocketWaitRetriesDuringDaemonStartup(t *testing.T) {
	attempts := 0
	err := waitHostSocket("/tmp/broker.host", time.Second,
		func(string, any, io.Writer) (json.RawMessage, error) {
			attempts++
			if attempts < 3 {
				return nil, os.ErrNotExist
			}
			return json.RawMessage(`{"pong":true}`), nil
		})
	if err != nil || attempts != 3 {
		t.Fatalf("waitHostSocket = %v after %d attempts, want success after retry", err, attempts)
	}
}

// THE HOST ADAPTER AUTHENTICATES ITS CALLER (docs/plans/notch-convergence.md §2.3, NC-D3).
// MEASURED 2026-09-27 before this: a caller holding no credential posted
// `refresh_token=yolo-broker:999999` to the managed Codex adapter on the host's loopback and got
// HTTP 200 with the access and id tokens. Now prepare mints a caller token, binds it into the
// managed auth.json, and the adapter refuses anything else before the broker is asked. Through
// the real prepare, so deleting the token from either the writer or Serve fails this.
func TestTheHostCodexAdapterServesOnlyTheMarkerItWrote(t *testing.T) {
	root := t.TempDir()
	token := strings.Repeat("5a", 32)
	refreshes := 0
	d := deps{
		ensure: func(io.Writer) (string, error) { return "/tmp/broker.host", nil },
		request: func(_ string, request any, _ io.Writer) (json.RawMessage, error) {
			switch request.(map[string]any)["action"] {
			case "status":
				return json.RawMessage(`{"logged_in":true}`), nil
			case "refresh":
				refreshes++
				if got := request.(map[string]any)["refresh_token"]; got != "yolo-broker:7" {
					t.Errorf("the broker was handed %q, want the plain marker", got)
				}
				return json.RawMessage(`{"access_token":"access-2","id_token":"id-2","refresh_token":"yolo-broker:8","expires_at":4102444800000}`), nil
			}
			return codexViewFixture(), nil
		},
		listen:    net.Listen,
		home:      func() string { return filepath.Join(root, "home") },
		storage:   func() string { return filepath.Join(root, "store") },
		workspace: func() (string, error) { return filepath.Join(root, "work"), nil },
		newToken:  func() (string, error) { return token, nil },
	}
	launch, err := prepare(d, "codex", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	defer launch.listener.Close()
	authBytes, err := os.ReadFile(filepath.Join(launch.vars["CODEX_HOME"], "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	var auth struct {
		Tokens struct {
			RefreshToken string `json:"refresh_token"`
		} `json:"tokens"`
	}
	if err := json.Unmarshal(authBytes, &auth); err != nil {
		t.Fatal(err)
	}
	if auth.Tokens.RefreshToken != "yolo-broker:7."+token {
		t.Fatalf("managed auth.json marker = %q, want the caller token bound", auth.Tokens.RefreshToken)
	}
	for k, v := range launch.vars {
		if strings.Contains(v, token) {
			t.Errorf("the caller token reached the launch environment as %s", k)
		}
	}
	url := launch.vars["CODEX_REFRESH_TOKEN_URL_OVERRIDE"]
	post := func(refresh string) (int, string) {
		t.Helper()
		body := `{"grant_type":"refresh_token","refresh_token":"` + refresh + `"}`
		client := http.Client{Timeout: 5 * time.Second}
		resp, err := client.Post(url, "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	if code, body := post("yolo-broker:999999"); code != http.StatusUnauthorized || strings.Contains(body, "access") {
		t.Fatalf("a stranger's marker got %d %s, want 401 with no token", code, body)
	}
	if refreshes != 0 {
		t.Fatalf("the broker was asked %d times for a stranger", refreshes)
	}
	code, body := post(auth.Tokens.RefreshToken)
	if code != http.StatusOK || !strings.Contains(body, "access-2") || !strings.Contains(body, "yolo-broker:8."+token) {
		t.Fatalf("Codex's own marker got %d %s, want 200 with the next marker bound", code, body)
	}
}

// TWO HOST CODEX SESSIONS AT ONCE BOTH REFRESH. Every `yolo host -- codex` shares ONE managed
// CODEX_HOME, so one auth.json, and Codex reloads that file before each refresh
// (docs/research/openai-subscription-auth.md §1.2). When each launch minted its own caller token,
// the second launch's token replaced the first's in the shared file, and the first session's
// Codex then sent a marker its own adapter refused 401: two concurrent host sessions, which
// worked before the adapter authenticated its caller, broke. Concurrent launches of one managed
// home now serve behind one token, so whichever session wrote the file last, every live adapter
// accepts what Codex reads from it. Through the real prepare twice, with a mint that would hand
// each launch a different token.
func TestConcurrentHostCodexLaunchesBothRefreshThroughTheSharedAuthFile(t *testing.T) {
	root := t.TempDir()
	generation := 7
	minted := []string{strings.Repeat("aa", 32), strings.Repeat("bb", 32), strings.Repeat("cc", 32)}
	mints := 0
	d := deps{
		ensure: func(io.Writer) (string, error) { return "/tmp/broker.host", nil },
		request: func(_ string, request any, _ io.Writer) (json.RawMessage, error) {
			switch request.(map[string]any)["action"] {
			case "status":
				return json.RawMessage(`{"logged_in":true}`), nil
			case "refresh":
				generation++
				return json.RawMessage(`{"access_token":"access","id_token":"id","refresh_token":"yolo-broker:` +
					strconv.Itoa(generation) + `","expires_at":4102444800000}`), nil
			}
			return codexViewFixture(), nil
		},
		listen:    net.Listen,
		home:      func() string { return filepath.Join(root, "home") },
		storage:   func() string { return filepath.Join(root, "store") },
		workspace: func() (string, error) { return filepath.Join(root, "work"), nil },
		newToken: func() (string, error) {
			mints++
			return minted[mints-1], nil
		},
	}
	first, err := prepare(d, "codex", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	second, err := prepare(d, "codex", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if first.vars["CODEX_HOME"] != second.vars["CODEX_HOME"] {
		t.Fatalf("the two launches got different CODEX_HOMEs (%q, %q); this test is about the shared one",
			first.vars["CODEX_HOME"], second.vars["CODEX_HOME"])
	}
	authFile := filepath.Join(first.vars["CODEX_HOME"], "auth.json")
	// refreshAs plays one session's Codex: reload the shared auth.json, POST its marker to this
	// session's own adapter, write the answer back.
	refreshAs := func(name string, launch *Launch) {
		t.Helper()
		raw, err := os.ReadFile(authFile)
		if err != nil {
			t.Fatal(err)
		}
		var auth map[string]any
		if err := json.Unmarshal(raw, &auth); err != nil {
			t.Fatal(err)
		}
		tokens := auth["tokens"].(map[string]any)
		body := `{"grant_type":"refresh_token","refresh_token":"` + tokens["refresh_token"].(string) + `"}`
		client := http.Client{Timeout: 5 * time.Second}
		resp, err := client.Post(launch.vars["CODEX_REFRESH_TOKEN_URL_OVERRIDE"], "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		answer, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("the %s session's refresh got %d %s, want 200", name, resp.StatusCode, answer)
		}
		var next map[string]any
		if err := json.Unmarshal(answer, &next); err != nil {
			t.Fatal(err)
		}
		tokens["refresh_token"] = next["refresh_token"]
		out, _ := json.Marshal(auth)
		if err := os.WriteFile(authFile, out, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	refreshAs("first", first)
	refreshAs("second", second)
	refreshAs("first", first)

	for _, launch := range []*Launch{first, second} {
		if rc, handled := launch.Run("/bin/sh", []string{"sh", "-c", "true"}, os.Environ(), nil, io.Discard, io.Discard); !handled || rc != 0 {
			t.Fatalf("Run = %d, %v", rc, handled)
		}
	}
	// Once no session of the home is live, the next launch mints afresh: the token is shared by
	// CONCURRENT launches, not kept forever.
	third, err := prepare(d, "codex", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	defer third.Run("/bin/sh", []string{"sh", "-c", "true"}, os.Environ(), nil, io.Discard, io.Discard)
	if mints != 2 {
		t.Fatalf("minted %d caller tokens over two concurrent launches and one later one, want 2 "+
			"(the second launch reuses the live token; the third, after both ended, mints)", mints)
	}
	raw, _ := os.ReadFile(authFile)
	if !strings.Contains(string(raw), minted[1]) {
		t.Fatalf("the launch after both sessions ended did not bind a fresh token: %s", raw)
	}
}
