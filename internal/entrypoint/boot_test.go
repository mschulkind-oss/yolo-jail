package entrypoint

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// hydrationCorpus is the writer->reader round-trip corpus for the
// yolo-user-env.sh export-line grammar (go-port plan section 1.1 frozen
// contract). It exercises the ${KEY:-'value'} default form (launch env wins),
// the embedded single-quote escape, bare/single/double forms, comments, and
// blank lines. Both the Go hydrator and the LIVE Python
// _hydrate_env_from_user_env_file are driven over this same input and their
// resulting env maps compared.
const hydrationCorpus = `# Auto-generated from yolo-jail.jsonc env config.
# Override by editing this file or workspace .env (mise).
export FOO=${FOO:-'bar baz'}
export QUOTED=${QUOTED:-'it'\''s a test'}
export EMPTY=${EMPTY:-''}
export SINGLE='literal single'
export SINGLE_ESC='a'\''b'
export DOUBLE="double val"
export BARE=bareword
export BARE_EMPTY=
export MULTI=${MULTI:-'x'\''y'\''z'}

not an export line
export = malformed
`

// TestHydrateEnvFromUserEnvFile validates the Go hydrator against the committed
// corpus (the ${KEY:-'value'} default form, embedded quotes, bare/single/double,
// launch-env-wins precedence).
func TestHydrateEnvFromUserEnvFile(t *testing.T) {
	home := t.TempDir()
	cfgDir := filepath.Join(home, ".config")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "yolo-user-env.sh"), []byte(hydrationCorpus), 0o644); err != nil {
		t.Fatal(err)
	}
	e := NewEnv(map[string]string{"JAIL_HOME": home, "FOO": "LAUNCH_WINS"})
	before := map[string]string{}
	for k, v := range e.Vars {
		before[k] = v
	}
	hydrateEnvFromUserEnvFile(e)
	delta := map[string]string{}
	for k, v := range e.Vars {
		if bv, existed := before[k]; existed && bv == v {
			continue
		}
		delta[k] = v
	}

	// FOO should NOT appear (launch env wins over the default).
	if _, ok := delta["FOO"]; ok {
		t.Error("FOO should not be in delta — launch env must win")
	}
	// Keys from the corpus that had no launch override should appear.
	for _, key := range []string{"QUOTED", "EMPTY", "SINGLE", "SINGLE_ESC", "DOUBLE", "BARE", "BARE_EMPTY", "MULTI"} {
		if _, ok := delta[key]; !ok {
			t.Errorf("expected %s in delta", key)
		}
	}
	if delta["QUOTED"] != "it's a test" {
		t.Errorf("QUOTED = %q, want %q", delta["QUOTED"], "it's a test")
	}
	if delta["SINGLE"] != "literal single" {
		t.Errorf("SINGLE = %q", delta["SINGLE"])
	}
	if delta["DOUBLE"] != "double val" {
		t.Errorf("DOUBLE = %q", delta["DOUBLE"])
	}
	if delta["BARE"] != "bareword" {
		t.Errorf("BARE = %q", delta["BARE"])
	}
}

func TestForwardEntryPort(t *testing.T) {
	// JSON integers decode to jsonInt; strings stay strings; floats/bools/nil
	// are invalid (warn + skip).
	cases := []struct {
		raw       string // JSON array with one element
		wantPort  int
		wantOK    bool
		wantPanic bool
	}{
		{`[8080]`, 8080, true, false},
		{`["8080"]`, 8080, true, false},
		{`["8080:80"]`, 8080, true, false},
		{`["127.0.0.1:5000:5000"]`, 0, true, true}, // "127.0.0.1" head -> int() crashes
		{`["nope"]`, 0, true, true},                // bare non-numeric -> boot crash quirk
		{`[3.5]`, 0, false, false},                 // float -> invalid
		{`[true]`, 0, false, false},                // bool -> invalid
		{`[null]`, 0, false, false},                // null -> invalid
	}
	for _, c := range cases {
		// Decode through jsonx so integers are jsonInt (matching runtime).
		entry := decodeFirst(t, c.raw)
		if c.wantPanic {
			assertPanics(t, func() { forwardEntryPort(entry) }, c.raw)
			continue
		}
		got, ok := forwardEntryPort(entry)
		if ok != c.wantOK || got != c.wantPort {
			t.Errorf("forwardEntryPort(%s) = (%d, %v), want (%d, %v)", c.raw, got, ok, c.wantPort, c.wantOK)
		}
	}
}

func TestEnvWith(t *testing.T) {
	base := []string{"A=1", "B=2", "PYTHONPATH=old"}
	got := envWith(base, "PYTHONPATH", "new")
	want := []string{"A=1", "B=2", "PYTHONPATH=new"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("envWith override: got %v want %v", got, want)
	}
	got2 := envWith([]string{"A=1"}, "NEW", "v")
	want2 := []string{"A=1", "NEW=v"}
	if !reflect.DeepEqual(got2, want2) {
		t.Errorf("envWith append: got %v want %v", got2, want2)
	}
}

func TestSplitLines(t *testing.T) {
	if got := splitLines("a\nb\n"); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Errorf("trailing newline dropped: %v", got)
	}
	if got := splitLines("a\nb"); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Errorf("no trailing newline: %v", got)
	}
	if got := splitLines(""); len(got) != 0 {
		t.Errorf("empty: %v", got)
	}
}

func TestCopyTree(t *testing.T) {
	src := t.TempDir()
	dst := filepath.Join(t.TempDir(), "nvim")
	if err := os.MkdirAll(filepath.Join(src, "lua"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "init.lua"), []byte("-- init"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "lua", "opts.lua"), []byte("-- opts"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := copyTree(src, dst); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dst, "lua", "opts.lua"))
	if err != nil || string(got) != "-- opts" {
		t.Errorf("nested file not copied: %q %v", got, err)
	}
}

func TestSupervisorIsAliveMissing(t *testing.T) {
	if supervisorIsAlive(filepath.Join(t.TempDir(), "nope.pid")) {
		t.Error("missing pid file should be not-alive")
	}
	// Our own PID is alive.
	pidFile := filepath.Join(t.TempDir(), "self.pid")
	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !supervisorIsAlive(pidFile) {
		t.Error("own pid should be alive")
	}
	// Garbage content -> not alive.
	if err := os.WriteFile(pidFile, []byte("not-a-pid"), 0o644); err != nil {
		t.Fatal(err)
	}
	if supervisorIsAlive(pidFile) {
		t.Error("garbage pid should be not-alive")
	}
}

func TestSetupCgroupDelegationMessages(t *testing.T) {
	prev := cgdSocket
	t.Cleanup(func() { cgdSocket = prev })

	// Absent socket.
	cgdSocket = filepath.Join(t.TempDir(), "nope.sock")
	var sb strings.Builder
	setupCgroupDelegation(&sb)
	if got := sb.String(); got != "  cgroup delegate: not available (no host daemon socket)\n" {
		t.Errorf("absent socket message: %q", got)
	}

	// Present socket (a regular file suffices for the Stat existence check).
	present := filepath.Join(t.TempDir(), "cgd.sock")
	if err := os.WriteFile(present, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	cgdSocket = present
	sb.Reset()
	setupCgroupDelegation(&sb)
	if got := sb.String(); got != "  cgroup delegate: available (host-side daemon)\n" {
		t.Errorf("present socket message: %q", got)
	}
}

func TestHydrationCorpusKeysDeterministic(t *testing.T) {
	// Guard: the Go hydrator must set exactly these keys (excluding FOO which is
	// launch-preset, and excluding the malformed lines).
	home := t.TempDir()
	cfgDir := filepath.Join(home, ".config")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "yolo-user-env.sh"), []byte(hydrationCorpus), 0o644); err != nil {
		t.Fatal(err)
	}
	e := NewEnv(map[string]string{"JAIL_HOME": home, "FOO": "LAUNCH_WINS"})
	before := map[string]struct{}{}
	for k := range e.Vars {
		before[k] = struct{}{}
	}
	hydrateEnvFromUserEnvFile(e)

	got := map[string]string{}
	var keys []string
	for k, v := range e.Vars {
		if _, existed := before[k]; existed && k != "FOO" {
			continue
		}
		got[k] = v
		keys = append(keys, k)
	}
	sort.Strings(keys)

	want := map[string]string{
		"FOO":        "LAUNCH_WINS", // launch env beats the file default
		"QUOTED":     "it's a test",
		"EMPTY":      "",
		"SINGLE":     "literal single",
		"SINGLE_ESC": "a'b",
		"DOUBLE":     "double val",
		"BARE":       "bareword",
		"BARE_EMPTY": "",
		"MULTI":      "x'y'z",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("hydration keys/values mismatch\n  got:  %#v\n  want: %#v", got, want)
	}
}

// --- small test helpers ---

// decodeFirst decodes a one-element JSON array through jsonx (so integers are
// jsonInt, matching what start_container_port_forwarding feeds forwardEntryPort)
// and returns the first element.
func decodeFirst(t *testing.T, arrJSON string) any {
	t.Helper()
	dec, err := jsonx.Decode([]byte(arrJSON))
	if err != nil {
		t.Fatalf("jsonx.Decode(%q): %v", arrJSON, err)
	}
	arr, ok := dec.([]any)
	if !ok || len(arr) == 0 {
		t.Fatalf("bad array json %q", arrJSON)
	}
	return arr[0]
}

func assertPanics(t *testing.T, fn func(), label string) {
	t.Helper()
	defer func() {
		if r := recover(); r == nil {
			t.Errorf("expected panic for %s, got none", label)
		}
	}()
	fn()
}
