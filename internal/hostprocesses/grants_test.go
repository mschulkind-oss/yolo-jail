package hostprocesses

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholegrant"
)

// The grant half of the daemon (docs/design/loophole-access-approval.md §4.3, §4.6, §5; plan T8).
// Each test drives the REAL transport through startDaemon, with the settings loaded by
// LoadSettings from a file written by loopholegrant.Merge, which is the launch's own writer.

// writeGrantFile writes the grant file the launch hands this daemon: settings plus a `grants`
// block whose one kind is view at standing, with values.
func writeGrantFile(t *testing.T, path, settings string, standing loopholegrant.Standing, values ...string) {
	t.Helper()
	b := loopholegrant.Block{
		Pack: "host-processes", Workspace: "/home/u/proj", Container: "yolo-proj",
		LaunchID: "launch-1", PID: os.Getpid(),
		Kinds: map[string]loopholegrant.Kind{
			"view": {Standing: standing, Values: values, Text: "see these host processes"},
		},
	}
	data, err := loopholegrant.Merge([]byte(settings), b)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// emptyHome points every config root the daemon could reach for at empty directories, so a
// daemon that consulted config instead of its grant file would find nothing to grant from.
func emptyHome(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
}

// TestHostprocessesReadsOnlyItsGrantFile is the plan's per-daemon split of
// TestEachShippedDaemonReadsOnlyItsGrantFile: no config on disk, a grant file present. With
// view requestable a request is refused, exit 64, with §5's requestable text; with view
// granted the same request is served.
func TestHostprocessesReadsOnlyItsGrantFile(t *testing.T) {
	withHostOS(t, "linux")
	emptyHome(t)
	path := filepath.Join(t.TempDir(), "host-processes.settings.json")
	writeGrantFile(t, path, `{"fields":["pid","comm"]}`, loopholegrant.Requestable)
	cfg := LoadSettings(path)
	ps := fakePS(t, `echo "ARGS: $*"`+"\n")
	ep, stop := startDaemon(t, cfg, ps)
	defer stop()

	out, stderr, rc := query(t, ep, map[string]any{"mode": "list"})
	if rc != 64 {
		t.Fatalf("requestable: rc=%d, want 64 (out=%q stderr=%q)", rc, out, stderr)
	}
	want := loopholegrant.Refusal{Client: "yolo-ps", Pack: "host-processes",
		Access: "see host processes", Kind: "view", ValueNoun: "process names",
		Dir: "/home/u/proj", Standing: loopholegrant.Requestable}.Text()
	if string(stderr) != want {
		t.Errorf("requestable stderr:\n%s\nwant:\n%s", stderr, want)
	}
	if !strings.Contains(string(stderr), "yolo request host-processes view <process names>") {
		t.Errorf("the refusal does not name the request: %q", stderr)
	}
	if len(out) != 0 {
		t.Errorf("a refused request printed %q", out)
	}
	if b, _ := os.ReadFile(filepath.Join(ps, psInvocationLog)); len(b) != 0 {
		t.Errorf("a refused request ran ps: %s", b)
	}

	// The same running daemon takes a rewritten grant at its next request: the launch and
	// `yolo config apply` are the file's one writer, and the daemon re-reads only the block.
	writeGrantFile(t, path, `{"fields":["pid","comm"]}`, loopholegrant.Granted, "sway")
	out, stderr, rc = query(t, ep, map[string]any{"mode": "list"})
	if rc != 0 {
		t.Fatalf("granted: rc=%d, want 0 (stderr=%q, ps: %s)", rc, stderr, psInvocations(ps))
	}
	if string(out) != "ARGS: -o pid,comm -C sway\n" {
		t.Errorf("granted list argv = %q", out)
	}
}

// TestHostprocessesEachStandingRefusesWithItsText: pending and not_allowed each print their
// own §5 text, and a block with no view kind at all fails closed with the not_allowed text
// (plan PD-12).
func TestHostprocessesEachStandingRefusesWithItsText(t *testing.T) {
	withHostOS(t, "linux")
	emptyHome(t)
	for _, tc := range []struct {
		standing loopholegrant.Standing
		values   []string
		want     string
	}{
		{loopholegrant.Pending, []string{"postgres"}, "this project's request for view postgres is not approved yet"},
		{loopholegrant.NotAllowed, nil, "yolo allow host-processes on_request view"},
	} {
		t.Run(string(tc.standing), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "s.json")
			writeGrantFile(t, path, `{}`, tc.standing, tc.values...)
			ps := fakePS(t, "echo x\n")
			ep, stop := startDaemon(t, LoadSettings(path), ps)
			defer stop()
			_, stderr, rc := query(t, ep, map[string]any{"mode": "tree"})
			if rc != 64 || !strings.Contains(string(stderr), tc.want) {
				t.Errorf("rc=%d stderr=%q, want 64 and %q", rc, stderr, tc.want)
			}
		})
	}

	t.Run("no view kind", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "s.json")
		data, err := loopholegrant.Merge([]byte(`{}`), loopholegrant.Block{Pack: "host-processes",
			Workspace: "/w", Kinds: map[string]loopholegrant.Kind{}})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		ep, stop := startDaemon(t, LoadSettings(path), fakePS(t, "echo x\n"))
		defer stop()
		_, stderr, rc := query(t, ep, map[string]any{"mode": "list"})
		if rc != 64 || !strings.Contains(string(stderr), "Your user config does not allow host-processes view on request") {
			t.Errorf("rc=%d stderr=%q, want the not_allowed refusal", rc, stderr)
		}
	})

	t.Run("a grant file that stops reading fails closed", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "s.json")
		writeGrantFile(t, path, `{}`, loopholegrant.Granted, "sway")
		ep, stop := startDaemon(t, LoadSettings(path), fakePS(t, "echo x\n"))
		defer stop()
		if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
			t.Fatal(err)
		}
		_, stderr, rc := query(t, ep, map[string]any{"mode": "list"})
		if rc != 64 || !strings.Contains(string(stderr), "so nothing ran") {
			t.Errorf("rc=%d stderr=%q, want a fail-closed refusal", rc, stderr)
		}
	})
}

// TestHostprocessesAnUngrantedNameIsInvisible: the granted values ARE the allowlist. A
// `visible` key an older launch left beside the block widens nothing, list mode asks ps for the
// granted names only, and pid mode on a process whose name is not granted is refused without
// naming that process.
func TestHostprocessesAnUngrantedNameIsInvisible(t *testing.T) {
	withHostOS(t, "linux")
	emptyHome(t)
	path := filepath.Join(t.TempDir(), "s.json")
	writeGrantFile(t, path, `{"visible":["waykeeper","definitely-not-our-comm"],"fields":["pid"]}`,
		loopholegrant.Granted, "sway")
	ps := fakePS(t, `echo "ARGS: $*"`+"\n")
	ep, stop := startDaemon(t, LoadSettings(path), ps)
	defer stop()

	out, stderr, rc := query(t, ep, map[string]any{"mode": "list"})
	if rc != 0 || string(out) != "ARGS: -o pid -C sway\n" {
		t.Errorf("list: rc=%d out=%q stderr=%q, want only the granted name", rc, out, stderr)
	}

	self, ok := linuxComm(strconv.Itoa(os.Getpid()))
	if !ok {
		t.Skip("no /proc on this host; pid mode names a pid through it")
	}
	_, stderr, rc = query(t, ep, map[string]any{"mode": "pid", "pid": os.Getpid()})
	if rc != 64 {
		t.Fatalf("pid of an ungranted name: rc=%d, want 64 (stderr=%q)", rc, stderr)
	}
	if strings.Contains(string(stderr), self) {
		t.Errorf("the refusal named the ungranted process %q: %q", self, stderr)
	}
	if !strings.Contains(string(stderr), "yolo request host-processes view <process names>") {
		t.Errorf("the refusal does not name the request: %q", stderr)
	}
}

// TestHostprocessesRefusalSpeaksTheGrantFilesWords: the access line and the value noun come
// from the grant file the launch wrote (the manifest's words), and the daemon's constants
// are only the fallback for a file that does not carry them.
func TestHostprocessesRefusalSpeaksTheGrantFilesWords(t *testing.T) {
	withHostOS(t, "linux")
	emptyHome(t)
	path := filepath.Join(t.TempDir(), "s.json")
	data, err := loopholegrant.Merge([]byte(`{}`), loopholegrant.Block{
		Pack: "host-processes", Workspace: "/w", Access: "peek at the host",
		Kinds: map[string]loopholegrant.Kind{"view": {
			Standing: loopholegrant.Requestable, ValueNoun: "program names",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	ep, stop := startDaemon(t, LoadSettings(path), fakePS(t, "echo x\n"))
	defer stop()
	_, stderr, rc := query(t, ep, map[string]any{"mode": "list"})
	if rc != 64 {
		t.Fatalf("rc=%d, want 64 (stderr=%q)", rc, stderr)
	}
	for _, want := range []string{"no grant to peek at the host", "yolo request host-processes view <program names>"} {
		if !strings.Contains(string(stderr), want) {
			t.Errorf("stderr=%q, want %q from the grant file", stderr, want)
		}
	}
}

// TestSelfCheckReportsTheGrantFile: `yolo check` reports a grant file's view standing, and
// FAILs one whose block no longer reads, since the daemon then refuses every request.
func TestSelfCheckReportsTheGrantFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.json")
	writeGrantFile(t, path, `{}`, loopholegrant.Granted, "sway", "waykeeper")
	var out strings.Builder
	if rc := settingsCheck(path, &out); rc != 0 || !strings.Contains(out.String(), "grants view for 2 process names") {
		t.Errorf("granted: rc=%d out=%q", rc, out.String())
	}
	if err := os.WriteFile(path, []byte(`{"grants":{"pack":""}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if rc := settingsCheck(path, &out); rc != 1 || !strings.Contains(out.String(), "FAIL") {
		t.Errorf("unreadable block: rc=%d out=%q", rc, out.String())
	}
}
