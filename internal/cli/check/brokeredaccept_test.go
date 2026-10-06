package check

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
)

// brokeredCheckHome is a host whose pack module gb brokers source gbsrc, run by
// runCheckOverConfigWith with the check on the host; prep also gets the home.
func brokeredCheckHome(t *testing.T) string {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	must(t, err)
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("YOLO_USER_LAYER", "")
	mod := filepath.Join(t.TempDir(), "gb")
	must(t, os.MkdirAll(mod, 0o755))
	must(t, os.WriteFile(filepath.Join(mod, "manifest.jsonc"), []byte(`{"name": "gb", "transport": "loopback-tls",
	  "lifecycle": "spawned",
	  "host_daemon": {"cmd": ["/bin/true", "{socket}", "{repository_scope}"], "publishes": "socket"},
	  "brokered": {"source": "gbsrc", "remote_host": "github.com"}}`), 0o644))
	recordPackModule(t, mod, true)
	return home
}

func onTheHost(o *Options) { o.Getenv = func(string) string { return "" } }

// resolvedDir is dir with its links resolved, as the check prints a path it resolved itself: on
// macOS t.TempDir() is under /var/folders, a link to /private/var/folders.
func resolvedDir(t *testing.T, dir string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(dir)
	must(t, err)
	return r
}

// `yolo check --accept-config-changes` reads the workspace's entry through the gate's own helper,
// shows the scope block and its count line before it records (WW-D20), records the union of the
// remotes and the entry, and says so (docs/design/workspace-widening.md §3.2).
func TestCheckAcceptShowsAndRecordsTheWorkspaceEntry(t *testing.T) {
	brokeredCheckHome(t)
	var ws string
	out := runCheckOverConfigWith(t, `{"brokered": {"gbsrc": {"repos": ["org/lib"]}}}`, false, func(w string) {
		ws = w
		must(t, os.MkdirAll(filepath.Join(w, ".git"), 0o755))
		must(t, os.WriteFile(filepath.Join(w, ".git", "config"),
			[]byte("[remote \"origin\"]\n\turl = https://github.com/o/r\n"), 0o644))
		_, err := config.SetWorkspaceLoophole(w, "gb", true)
		must(t, err)
	}, func(o *Options) {
		o.AcceptConfigChanges = true
		onTheHost(o)
	})
	block := strings.Index(out, "Repository scope changed since the last approval; recording it as approved:")
	recorded := strings.Index(out, "Approved gb repository scope recorded: o/r, org/lib")
	if block < 0 || recorded < 0 || block > recorded {
		t.Fatalf("the check did not show the block before recording the union:\n%s", out)
	}
	for _, want := range []string{"+ org/lib  yolo-jail.jsonc", "gb: 2 added, 0 removed, 0 source changed"} {
		if !strings.Contains(out, want) {
			t.Errorf("the check's block lacks %q:\n%s", want, out)
		}
	}
	if got := config.ApprovedScope(ws, "gbsrc"); strings.Join(got, ",") != "o/r,org/lib" {
		t.Errorf("the record holds %v", got)
	}
}

// WW-D21, WW-D22: host `yolo check` prints every project's move out of the retired form, which
// no launch prints, and warns of a source no selected pack brokers, with its next step.
func TestCheckListsEveryProjectsMoveAndWarnsOfAnUnbrokeredSource(t *testing.T) {
	home := brokeredCheckHome(t)
	other := filepath.Join(home, "code", "secret-client")
	var ws string
	out := runCheckOverConfigWith(t, `{"brokered": {"githb": {"repos": []}}}`, false, func(w string) {
		ws = w
		key, _ := json.Marshal(w)
		cfg := filepath.Join(home, ".config", "yolo-jail", "config.jsonc")
		must(t, os.MkdirAll(filepath.Dir(cfg), 0o755))
		must(t, os.WriteFile(cfg, []byte(`{"brokered": {"gbsrc": {"workspaces": {`+string(key)+
			`: {"repos": ["org/lib"]}, "~/code/secret-client": {"repos": ["acme/private-roadmap"]}}}}}`), 0o644))
	}, onTheHost)
	for _, want := range []string{"config.brokered.gbsrc.workspaces: RETIRED",
		"Each project's move out of the retired `brokered.<source>.workspaces` key",
		ws + `: put "brokered": {"gbsrc": {"repos": ["org/lib"]}} in ` + filepath.Join(resolvedDir(t, ws), "yolo-jail.local.jsonc"),
		`~/code/secret-client: put "brokered": {"gbsrc": {"repos": ["acme/private-roadmap"]}} in ` +
			filepath.Join(other, "yolo-jail.local.jsonc"),
		"config.brokered.githb: no loophole a selected pack ships brokers the source 'githb'",
		"Check the spelling"} {
		if !strings.Contains(out, want) {
			t.Errorf("yolo check does not say %q:\n%s", want, out)
		}
	}
}

// WW-P3: host `yolo check` prints what the agent chose as text, written in an include the agent
// named: the include's parse error, which names it, and the merged config's warning and error that
// echo a value and a key.
func TestCheckPrintsTheAgentsConfigTextAsText(t *testing.T) {
	const evil = "x\x1b[2K\x1b]0;owned\ay.jsonc"
	for name, c := range map[string]struct{ body, want string }{
		"a parse error": {`{"packages": `, `x\x1b[2K\x1b]0;owned\ay.jsonc`},
		"a warning":     {`{"devices": ["/dev/x\u001b]0;owned\u0007z"]}`, `may be skipped: /dev/x\x1b]0;owned\az`},
		"an error":      {`{"x\u001b]0;owned\u0007k": 1}`, `x\x1b]0;owned\ak: unknown key`},
	} {
		brokeredCheckHome(t)
		out := runCheckOverConfigWith(t, `{"include_if_found": ["x\u001b[2K\u001b]0;owned\u0007y.jsonc"]}`, false,
			func(w string) { must(t, os.WriteFile(filepath.Join(w, evil), []byte(c.body), 0o644)) }, onTheHost)
		if strings.ContainsAny(out, "\x1b\a") || !strings.Contains(out, c.want) {
			t.Errorf("%s: the agent's text did not reach the check's output as text (want %q):\n%q", name, c.want, out)
		}
	}
}
