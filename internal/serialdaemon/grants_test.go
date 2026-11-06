package serialdaemon

// grants_test.go pins the serial daemon's half of the loophole grants
// (docs/reference/loophole-grants.md §4.6, §5; plan T9, F4): the daemon reads only its
// grant file, re-reading its block at each request, `list` and every open see only the granted
// device globs, there are no default globs, a file with no block grants nothing, and a refusal is
// loopholegrant's text on stderr with exit 64.

import (
	"context"
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
	return askAt(t, ep, req)
}

// granted is a daemon config whose grant file grants globs and nothing else.
func granted(t *testing.T, globs ...string) Settings {
	t.Helper()
	return LoadSettings(grantFile(t, "", loopholegrant.Granted, globs...))
}

// askAt sends one request to the running daemon at ep and returns what it answered.
func askAt(t *testing.T, ep string, req map[string]any) session {
	t.Helper()
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
		ValueNoun: "device globs", Standing: loopholegrant.Requestable,
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

	got = ask(t, LoadSettings(grantFile(t, legacy, loopholegrant.Granted)), map[string]any{"mode": "list", "format": "json"})
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
		ValueNoun: "device globs", Standing: loopholegrant.Granted,
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
		ValueNoun: "device globs",
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

	for name, c := range map[string]struct {
		path, want string
	}{
		"pending":     {grantFile(t, "", loopholegrant.Pending, "/dev/ttyUSB*"), pending.Text()},
		"not_allowed": {grantFile(t, "", loopholegrant.NotAllowed), notAllowed.Text()},
		"no kind":     {noKind, notAllowed.Text()},
		"unreadable":  {broken, notAllowed.Text()},
	} {
		got := ask(t, LoadSettings(c.path), map[string]any{"mode": "list"})
		if got.exit != 64 || got.stderr != c.want {
			t.Errorf("%s: exit %d, stderr:\n%s\nwant exit 64 and:\n%s", name, got.exit, got.stderr, c.want)
		}
	}
}

// TestTheRefusalSpeaksTheGrantFilesWords: the launch writes the manifest's `access` and
// `value_noun` into the grant file, and the refusal prints those, not the daemon's fallback.
func TestTheRefusalSpeaksTheGrantFilesWords(t *testing.T) {
	isolateConfig(t)
	data, err := loopholegrant.Merge(nil, loopholegrant.Block{
		Pack: "serial", Workspace: "/home/u/proj", Access: "drive the bench rig",
		Kinds: map[string]loopholegrant.Kind{"devices": {Standing: loopholegrant.Requestable, ValueNoun: "rig ports"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "settings")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	want := loopholegrant.Refusal{
		Client: "yolo-serial", Pack: "serial", Access: "drive the bench rig", Kind: "devices",
		ValueNoun: "rig ports", Standing: loopholegrant.Requestable,
	}.Text()
	if got := ask(t, LoadSettings(path), map[string]any{"mode": "list"}); got.exit != 64 || got.stderr != want {
		t.Errorf("exit %d, stderr:\n%s\nwant 64 and:\n%s", got.exit, got.stderr, want)
	}
}

// TestWithoutAGrantBlockEveryRequestFailsClosed: plan PD-12, serial's flip (F4). A settings file
// with no `grants` key, even one an older launch wrote with `allowed_devices` naming the device,
// no file at all, and no --settings grant nothing: there are no default globs any more, and every
// mode is refused with the not_allowed text, exit 64, before any device is listed or opened.
func TestWithoutAGrantBlockEveryRequestFailsClosed(t *testing.T) {
	isolateConfig(t)
	legacy := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(legacy, []byte(`{"allowed_devices":["/dev/null"],"default_baud":9600}`), 0o600); err != nil {
		t.Fatal(err)
	}
	want := loopholegrant.Refusal{
		Client: "yolo-serial", Pack: "serial", Access: "open host serial devices", Kind: "devices",
		ValueNoun: "device globs", Standing: loopholegrant.NotAllowed,
	}.Text()
	for name, path := range map[string]string{
		"no grants key": legacy,
		"no file":       filepath.Join(t.TempDir(), "absent"),
		"no --settings": "",
	} {
		for _, req := range []map[string]any{
			{"mode": "list", "format": "json"},
			{"mode": "read", "device": "/dev/null"},
			{"mode": "write", "device": "/dev/null", "data": "x"},
			{"mode": "monitor", "device": "/dev/null"},
		} {
			got := ask(t, LoadSettings(path), req)
			if got.exit != 64 || got.stderr != want || got.stdout != "" {
				t.Errorf("%s, %v: exit %d, stdout %q, stderr:\n%s\nwant exit 64 and:\n%s",
					name, req, got.exit, got.stdout, got.stderr, want)
			}
		}
	}
	if got := LoadSettings(legacy).DefaultBaud; got != 9600 {
		t.Errorf("DefaultBaud = %d, want the file's 9600: the settings still read without a block", got)
	}
}

// TestTheGrantIsReReadAtEachRequest: `yolo config apply` rewrites a running jail's grant file
// and says the change is applied now (plan T11), so one running daemon honors each rewrite at its
// next request, with no restart: requestable refuses, granted serves the granted globs only, and a
// grant taken back refuses again.
func TestTheGrantIsReReadAtEachRequest(t *testing.T) {
	isolateConfig(t)
	path := grantFile(t, `{"default_baud":9600}`, loopholegrant.Requestable)
	_, identity, err := loopholegrant.Read(path)
	if err != nil || identity == nil {
		t.Fatalf("grant file: %+v, %v", identity, err)
	}
	ep, stop := startTestDaemon(t, LoadSettings(path))
	defer stop()
	rewrite := func(standing loopholegrant.Standing, values ...string) {
		t.Helper()
		next := *identity
		next.Kinds = map[string]loopholegrant.Kind{"devices": {Standing: standing, Values: values, Text: "open"}}
		if err := loopholegrant.Replace(context.Background(), path, *identity, next); err != nil {
			t.Fatal(err)
		}
	}
	list := map[string]any{"mode": "list", "format": "json"}

	if got := askAt(t, ep, list); got.exit != 64 || got.stderr != requestableText() {
		t.Fatalf("before the grant: exit %d, stderr %q, want 64 and the requestable text", got.exit, got.stderr)
	}

	rewrite(loopholegrant.Granted, "/dev/null")
	if got, want := askAt(t, ep, list), `{"devices": [{"path": "/dev/null", "accessible": true}]}`+"\n"; got.exit != 0 || got.stdout != want {
		t.Fatalf("after the grant was applied: exit %d, stdout %q, stderr %q; want %q", got.exit, got.stdout, got.stderr, want)
	}
	if got := askAt(t, ep, map[string]any{"mode": "read", "device": "/dev/zero"}); got.exit != 64 ||
		!strings.Contains(got.stderr, "/dev/zero") {
		t.Fatalf("a device outside the applied grant: exit %d, stderr %q", got.exit, got.stderr)
	}

	rewrite(loopholegrant.NotAllowed)
	if got := askAt(t, ep, list); got.exit != 64 || got.stdout != "" {
		t.Fatalf("after the grant was taken back: exit %d, stdout %q, stderr %q", got.exit, got.stdout, got.stderr)
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
