package serialdaemon

// grants_test.go pins the serial daemon's half of the loophole grants
// (docs/design/loophole-access-approval.md §4.6, §5; plan T9): the daemon reads only its grant
// file, `list` and every open see only the granted device globs, there are no default globs
// once a grant block is present, and a refusal is loopholegrant's text on stderr with exit 64.

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/frameproto"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholegrant"
	"github.com/mschulkind-oss/yolo-jail/internal/svcendpoint"
)

// grantFile writes the launch's grant file for the serial loophole: settings plus a grant block
// whose `devices` kind stands at standing with values.
func grantFile(t *testing.T, settings string, standing loopholegrant.Standing, values ...string) string {
	t.Helper()
	b := loopholegrant.Block{
		Pack: "serial", Workspace: "/home/u/proj", Container: "c", LaunchID: "l", PID: os.Getpid(),
		Kinds: map[string]loopholegrant.Kind{
			"devices": {Standing: standing, Values: values, Text: "open these host serial devices"},
		},
	}
	data, err := loopholegrant.Merge([]byte(settings), b)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "settings-l")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// isolateConfig points every config the daemon could read anywhere else at empty directories,
// so a test proves the daemon took its answer from the grant file alone.
func isolateConfig(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
}

type session struct {
	stdout, stderr string
	exit           int
}

// ask sends one request to a daemon built from cfg and returns what it answered.
func ask(t *testing.T, cfg Settings, req map[string]any) session {
	t.Helper()
	ep, stop := startTestDaemon(t, cfg)
	defer stop()
	conn, err := svcendpoint.Dial(ep, 5*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	body, _ := json.Marshal(req)
	if err := frameproto.WriteRequest(conn, body); err != nil {
		t.Fatalf("write request: %v", err)
	}
	var out, errb []byte
	for {
		f, err := frameproto.ReadFrame(conn)
		if err != nil {
			t.Fatalf("the stream ended before the exit frame: %v (stderr %q)", err, errb)
		}
		switch f.StreamID {
		case frameproto.StreamStdout:
			out = append(out, f.Payload...)
		case frameproto.StreamStderr:
			errb = append(errb, f.Payload...)
		case frameproto.StreamExit:
			rc, err := frameproto.ExitCode(f.Payload)
			if err != nil {
				t.Fatal(err)
			}
			return session{string(out), string(errb), rc}
		}
	}
}

func requestableText() string {
	return loopholegrant.Refusal{
		Client: "yolo-serial", Pack: "serial", Access: "open host serial devices", Kind: "devices",
		ValueNoun: "device globs", Dir: "/home/u/proj", Standing: loopholegrant.Requestable,
	}.Text()
}

// TestSerialdaemonReadsOnlyItsGrantFile: with no config anywhere but the grant file, a kind
// standing `requestable` refuses every mode with exit 64 and loopholegrant's requestable text,
// and the same file with the kind `granted` serves.
func TestSerialdaemonReadsOnlyItsGrantFile(t *testing.T) {
	isolateConfig(t)

	refused := LoadSettings(grantFile(t, `{"default_baud":9600}`, loopholegrant.Requestable))
	for _, req := range []map[string]any{
		{"mode": "list"},
		{"mode": "list", "format": "json"},
		{"mode": "read", "device": "/dev/null"},
		{"mode": "write", "device": "/dev/null", "data": "x"},
		{"mode": "monitor", "device": "/dev/null"},
	} {
		got := ask(t, refused, req)
		if got.exit != 64 || got.stderr != requestableText() || got.stdout != "" {
			t.Errorf("%v with devices requestable: exit %d, stdout %q, stderr:\n%s\nwant exit 64 and:\n%s",
				req, got.exit, got.stdout, got.stderr, requestableText())
		}
	}

	served := LoadSettings(grantFile(t, `{"default_baud":9600}`, loopholegrant.Granted, "/dev/null"))
	if served.DefaultBaud != 9600 {
		t.Errorf("DefaultBaud = %d, want the grant file's 9600", served.DefaultBaud)
	}
	got := ask(t, served, map[string]any{"mode": "list", "format": "json"})
	if got.exit != 0 || !strings.Contains(got.stdout, `"path": "/dev/null"`) {
		t.Errorf("list with /dev/null granted: exit %d, stdout %q, stderr %q", got.exit, got.stdout, got.stderr)
	}
}

// TestListSeesOnlyTheGrantedGlobs: a granted kind's values are the whole allowlist. A device a
// default glob or a legacy `allowed_devices` setting would have matched is not listed, and a
// granted kind with no values lists nothing: there are no defaults.
func TestListSeesOnlyTheGrantedGlobs(t *testing.T) {
	isolateConfig(t)
	legacy := `{"allowed_devices":["/dev/zero"]}`

	got := ask(t, LoadSettings(grantFile(t, legacy, loopholegrant.Granted, "/dev/null")),
		map[string]any{"mode": "list", "format": "json"})
	if want := `{"devices": [{"path": "/dev/null", "accessible": true}]}` + "\n"; got.exit != 0 || got.stdout != want {
		t.Errorf("list granting /dev/null: exit %d, stdout %q, want %q", got.exit, got.stdout, want)
	}

	cfg := LoadSettings(grantFile(t, legacy, loopholegrant.Granted))
	if len(cfg.AllowedDevices) != 0 {
		t.Errorf("a granted kind with no values allows %v, want nothing", cfg.AllowedDevices)
	}
	got = ask(t, cfg, map[string]any{"mode": "list", "format": "json"})
	if want := `{"devices": []}` + "\n"; got.exit != 0 || got.stdout != want {
		t.Errorf("list granting no globs: exit %d, stdout %q, want %q", got.exit, got.stdout, want)
	}
}

// TestOpenSeesOnlyTheGrantedGlobs: a device outside the granted globs is refused with exit 64,
// naming the device as the value to request; one inside passes the gate (here it reaches the
// open, which /dev/null fails as a serial port, exit 1).
func TestOpenSeesOnlyTheGrantedGlobs(t *testing.T) {
	isolateConfig(t)
	cfg := LoadSettings(grantFile(t, `{"allowed_devices":["/dev/zero"]}`, loopholegrant.Granted, "/dev/null"))

	want := loopholegrant.Refusal{
		Client: "yolo-serial", Pack: "serial", Access: "open host serial devices", Kind: "devices",
		ValueNoun: "device globs", Dir: "/home/u/proj", Standing: loopholegrant.Granted,
		Values: []string{"/dev/zero"},
	}.Text()
	for _, mode := range []string{"read", "write", "monitor"} {
		got := ask(t, cfg, map[string]any{"mode": mode, "device": "/dev/zero", "data": "x"})
		if got.exit != 64 || got.stderr != want {
			t.Errorf("%s /dev/zero outside the grant: exit %d, stderr:\n%s\nwant exit 64 and:\n%s", mode, got.exit, got.stderr, want)
		}
	}

	got := ask(t, cfg, map[string]any{"mode": "write", "device": "/dev/null", "data": "x"})
	if got.exit == 64 || strings.Contains(got.stderr, "nothing ran") {
		t.Errorf("write /dev/null inside the grant was refused: exit %d, stderr %q", got.exit, got.stderr)
	}
}

// TestEveryUngrantedStandingRefuses: pending names the requested globs, not_allowed (and a grant
// block that has no `devices` kind, and one the daemon cannot read) fails closed with the
// not_allowed text.
func TestEveryUngrantedStandingRefuses(t *testing.T) {
	isolateConfig(t)
	base := loopholegrant.Refusal{
		Client: "yolo-serial", Pack: "serial", Access: "open host serial devices", Kind: "devices",
		ValueNoun: "device globs", Dir: "/home/u/proj",
	}
	pending := base
	pending.Standing, pending.Values = loopholegrant.Pending, []string{"/dev/ttyUSB*"}
	notAllowed := base
	notAllowed.Standing = loopholegrant.NotAllowed

	noKind := filepath.Join(t.TempDir(), "settings-nokind")
	data, err := loopholegrant.Merge(nil, loopholegrant.Block{Pack: "serial", Workspace: "/home/u/proj"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(noKind, data, 0o600); err != nil {
		t.Fatal(err)
	}
	broken := filepath.Join(t.TempDir(), "settings-broken")
	if err := os.WriteFile(broken, []byte(`{"grants":{"pack":"serial","kinds":{"devices":{"standing":"maybe"}}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	notAllowedNoDir := notAllowed
	notAllowedNoDir.Dir = ""

	for name, c := range map[string]struct {
		path, want string
	}{
		"pending":     {grantFile(t, "", loopholegrant.Pending, "/dev/ttyUSB*"), pending.Text()},
		"not_allowed": {grantFile(t, "", loopholegrant.NotAllowed), notAllowed.Text()},
		"no kind":     {noKind, notAllowed.Text()},
		"unreadable":  {broken, notAllowedNoDir.Text()},
	} {
		got := ask(t, LoadSettings(c.path), map[string]any{"mode": "list"})
		if got.exit != 64 || got.stderr != c.want {
			t.Errorf("%s: exit %d, stderr:\n%s\nwant exit 64 and:\n%s", name, got.exit, got.stderr, c.want)
		}
	}
}

// TestASettingsFileWithoutGrantsKeepsTodaysAllowlist: plan PD-12. Until serial's flip, a file
// with no `grants` key is read as before, `allowed_devices` and its defaults included.
func TestASettingsFileWithoutGrantsKeepsTodaysAllowlist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte(`{"allowed_devices":["/dev/null"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got := ask(t, LoadSettings(path), map[string]any{"mode": "read", "device": "/dev/zero"})
	if got.exit != 2 || !strings.Contains(got.stderr, "not in the allowed_devices list") {
		t.Errorf("legacy refusal: exit %d, stderr %q", got.exit, got.stderr)
	}
}

// TestSelfCheckReportsTheStanding: under a grant block that is not granted, the self-check
// says where the kind stands instead of listing devices; an unreadable grant file is a failure.
func TestSelfCheckReportsTheStanding(t *testing.T) {
	isolateConfig(t)
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	prev := os.Stdout
	os.Stdout = w
	rc := SelfCheck(grantFile(t, "", loopholegrant.Requestable))
	os.Stdout = prev
	_ = w.Close()
	out, _ := io.ReadAll(r)
	if want := "serial: devices requestable for this project; nothing is listed or opened until a grant covers it\n"; rc != 0 || string(out) != want {
		t.Errorf("SelfCheck: rc %d, stdout %q, want 0 and %q", rc, out, want)
	}

	broken := filepath.Join(t.TempDir(), "settings")
	if err := os.WriteFile(broken, []byte(`{"grants":{"pack":"serial","kinds":{"devices":{"standing":"maybe"}}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if rc := SelfCheck(broken); rc != 1 {
		t.Errorf("SelfCheck of an unreadable grant file = %d, want 1", rc)
	}
}
