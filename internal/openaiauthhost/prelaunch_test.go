package openaiauthhost

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The prelaunches the shipped codex, pi and opencode packs declare, at a terminal.
var (
	codexPrelaunch    = Prelaunch{Bin: "codex", Flag: CodexViewFlag, Pack: "codex", Interactive: true}
	piPrelaunch       = Prelaunch{Bin: "pi", Flag: PiViewFlag, Pack: "pi", Interactive: true}
	opencodePrelaunch = Prelaunch{Bin: "opencode", Flag: OpencodeViewFlag, Pack: "opencode", Interactive: true}
)

// loggedOutDeps is a broker that has no login, recording every action it is asked.
func loggedOutDeps(actions *[]string) deps {
	return deps{
		ensure: func(io.Writer) (string, error) {
			*actions = append(*actions, "ensure")
			return "/tmp/broker.host", nil
		},
		request: func(_ string, request any, _ io.Writer) (json.RawMessage, error) {
			action := request.(map[string]any)["action"].(string)
			*actions = append(*actions, action)
			if action == "status" {
				return json.RawMessage(`{"logged_in":false}`), nil
			}
			return json.RawMessage(`{"ok":true}`), nil
		},
	}
}

// NOTHING DECLARED, NOTHING DONE: no broker, no login. This is `yolo host -p zai -- pi`, whose
// pack gates its prelaunch on the codex profile, and every command whose pack declares none.
func TestPrepareDoesNothingWhenNoPrelaunchIsDeclared(t *testing.T) {
	var actions []string
	launch, err := prepare(loggedOutDeps(&actions), Prelaunch{Bin: "pi", Interactive: true}, io.Discard)
	if err != nil || launch != nil || len(actions) != 0 {
		t.Fatalf("launch = %v, err = %v, actions = %v; want nothing at all", launch, err, actions)
	}
}

// A LOGIN ONLY AT A TERMINAL: off one, no login starts, the jail launcher's two lines are printed,
// and the launch continues without the credential.
func TestPrepareNeverLogsInWithoutATerminal(t *testing.T) {
	for _, p := range []Prelaunch{codexPrelaunch, piPrelaunch, opencodePrelaunch, {Bin: "claude", Login: true, Pack: "claude"}} {
		p.Interactive = false
		var actions []string
		var stderr bytes.Buffer
		launch, err := prepare(loggedOutDeps(&actions), p, &stderr)
		if err != nil || launch != nil {
			t.Fatalf("%s: launch = %v, err = %v", p.Bin, launch, err)
		}
		if strings.Contains(strings.Join(actions, ","), "login") {
			t.Errorf("%s: a login started with no terminal: %v", p.Bin, actions)
		}
		for _, want := range []string{p.Bin + ": OpenAI login is required, and this is not an interactive terminal",
			"run '" + p.Bin + "' once from a terminal to log in; continuing without a credential"} {
			if !strings.Contains(stderr.String(), want) {
				t.Errorf("%s: stderr must say %q:\n%s", p.Bin, want, stderr.String())
			}
		}
	}
}

// A login-only prelaunch proves the login and serves no view.
func TestPrepareLoginOnlyServesNoView(t *testing.T) {
	var actions []string
	launch, err := prepare(loggedOutDeps(&actions), Prelaunch{Bin: "claude", Login: true, Interactive: true}, io.Discard)
	if err != nil || launch != nil || strings.Join(actions, ",") != "ensure,status,login" {
		t.Fatalf("launch = %v, err = %v, actions = %v", launch, err, actions)
	}
}

// A view the host does not serve refuses by name, as the jail's credential client refuses a flag
// it does not know, and never starts the broker.
func TestPrepareRefusesAViewTheHostDoesNotServe(t *testing.T) {
	var actions []string
	_, err := prepare(loggedOutDeps(&actions), Prelaunch{Bin: "x", Flag: "--x-auth", Interactive: true}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), `"--x-auth"`) || len(actions) != 0 {
		t.Fatalf("err = %v, actions = %v", err, actions)
	}
}

// THE OPENCODE VIEW IS SERVED AS PI'S IS, by the host credential socket the yolo plugin dials: no
// managed home and no adapter, because opencode's own home is the user's and the plugin, not
// opencode, asks the broker for each request's token. Logged in, the launch carries the socket and
// nothing else, and asks for no browser login.
func TestPrepareServesOpencodeTheHostSocket(t *testing.T) {
	var actions []string
	d := deps{
		ensure: func(io.Writer) (string, error) { return "/tmp/broker.host", nil },
		request: func(_ string, request any, _ io.Writer) (json.RawMessage, error) {
			action := request.(map[string]any)["action"].(string)
			actions = append(actions, action)
			return json.RawMessage(`{"logged_in":true}`), nil
		},
	}
	launch, err := prepare(d, opencodePrelaunch, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if launch == nil || len(launch.vars) != 1 || launch.vars["YOLO_OPENAI_AUTH_HOST_SOCKET"] != "/tmp/broker.host" {
		t.Fatalf("launch = %#v, want the host socket alone", launch)
	}
	if launch.listener != nil || launch.noDaemon {
		t.Errorf("an opencode launch started a managed Codex adapter: %#v", launch)
	}
	if strings.Join(actions, ",") != "status" {
		t.Errorf("broker actions = %v, want the login check alone", actions)
	}
}

// AT THE HOST THE LAUNCH SAYS WHICH LOGIN opencode WILL RUN ON. yolo never writes the user's own
// opencode auth store there, so the shared login reaches opencode only once the user picks it in
// /connect. Until then the launch, which has just proved (or made) the shared login, would hand
// opencode a socket it never uses: opencode runs on its own ChatGPT login, or on nothing and fails.
// So the launch names the store and the /connect method, honoring XDG_DATA_HOME as opencode does,
// and says nothing once the store holds yolo's view.
func TestPrepareTellsTheHostsOpencodeWhichLoginItRunsOn(t *testing.T) {
	for _, tc := range []struct {
		name, stored, xdg string
		want              []string
	}{
		{"nothing stored", "", "", []string{"ChatGPT Plus/Pro (yolo shared login)", "/connect", "fail"}},
		{"an API key", `{"openai":{"type":"api","key":"sk-users-own"}}`, "", []string{"ChatGPT Plus/Pro (yolo shared login)", "/connect"}},
		{"opencode's own ChatGPT login", `{"openai":{"type":"oauth","refresh":"rt-users-own","access":"a","expires":1}}`, "",
			[]string{"its own ChatGPT login", "not on yolo's shared one", "ChatGPT Plus/Pro (yolo shared login)"}},
		{"yolo's view", `{"openai":{"type":"oauth","refresh":"yolo-broker:3","access":"a","expires":1}}`, "", nil},
		{"yolo's view under XDG_DATA_HOME", `{"openai":{"type":"oauth","refresh":"yolo-broker:3","access":"a","expires":1}}`, "xdg", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			data := filepath.Join(home, ".local", "share")
			env := map[string]string{}
			if tc.xdg != "" {
				data = filepath.Join(home, tc.xdg)
				env["XDG_DATA_HOME"] = data
			}
			store := filepath.Join(data, "opencode", "auth.json")
			if tc.stored != "" {
				if err := os.MkdirAll(filepath.Dir(store), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(store, []byte(tc.stored), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			d := deps{
				ensure: func(io.Writer) (string, error) { return "/tmp/broker.host", nil },
				request: func(string, any, io.Writer) (json.RawMessage, error) {
					return json.RawMessage(`{"logged_in":true}`), nil
				},
				home:   func() string { return home },
				getenv: func(k string) string { return env[k] },
			}
			var stderr bytes.Buffer
			launch, err := prepare(d, opencodePrelaunch, &stderr)
			if err != nil || launch == nil {
				t.Fatalf("launch = %v, err = %v", launch, err)
			}
			if len(tc.want) == 0 {
				if stderr.Len() != 0 {
					t.Errorf("opencode on yolo's view was told %q", stderr.String())
				}
				return
			}
			for _, want := range append(tc.want, store) {
				if !strings.Contains(stderr.String(), want) {
					t.Errorf("the host's opencode line lacks %q:\n%s", want, stderr.String())
				}
			}
			if strings.Contains(stderr.String(), "-users-own") {
				t.Errorf("the line printed a stored secret: %s", stderr.String())
			}
		})
	}
}

// THE MANAGED CODEX HOME IS KEYED ON THE DECLARING PACK, so a second pack declaring the codex view
// for its own binary never shares the shipped codex pack's home.
func TestManagedCodexHomeIsKeyedOnTheDeclaringPack(t *testing.T) {
	root := t.TempDir()
	d := deps{
		ensure: func(io.Writer) (string, error) { return "/tmp/broker.host", nil },
		request: func(_ string, request any, _ io.Writer) (json.RawMessage, error) {
			if request.(map[string]any)["action"] == "status" {
				return json.RawMessage(`{"logged_in":true}`), nil
			}
			return codexViewFixture(), nil
		},
		listen:    net.Listen,
		home:      func() string { return filepath.Join(root, "home") },
		storage:   func() string { return filepath.Join(root, "store") },
		workspace: func() (string, error) { return filepath.Join(root, "work"), nil },
		newToken:  func() (string, error) { return strings.Repeat("5a", 32), nil },
	}
	p := codexPrelaunch
	p.Pack = "codex-work"
	launch, err := prepare(d, p, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = launch.listener.Close(); _ = launch.live.Close() })
	if want := filepath.Join(root, "store", "host-agents", "codex-work"); launch.vars["CODEX_HOME"] != want {
		t.Errorf("CODEX_HOME = %q, want %q", launch.vars["CODEX_HOME"], want)
	}
}

// PrelaunchVar spells the variable the jail's launcher reads: the binary uppercased, anything
// outside A-Z, 0-9 and _ replaced by _.
func TestPrelaunchVarSpellsTheJailLaunchersName(t *testing.T) {
	for bin, want := range map[string]string{
		"codex":  "YOLO_AUTH_PRELAUNCH_CODEX_FLAG",
		"oh-omp": "YOLO_AUTH_PRELAUNCH_OH_OMP_FLAG",
		"pi2.x":  "YOLO_AUTH_PRELAUNCH_PI2_X_FLAG",
	} {
		if got := PrelaunchVar(bin, "FLAG"); got != want {
			t.Errorf("PrelaunchVar(%q) = %q, want %q", bin, got, want)
		}
	}
}
