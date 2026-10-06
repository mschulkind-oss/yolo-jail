package entrypoint

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/miseuse"
)

// miseLsFixture answers the three `mise ls` calls the recorder makes, as mise would in a
// workspace that uses node 22 and has node 20 left over from an older config.
func miseLsFixture(store string) func(args ...string) ([]byte, error) {
	entry := func(rel string) string {
		return `{"version":"` + filepath.Base(rel) + `","install_path":"` + store + `/installs/` + rel + `","installed":true}`
	}
	return func(args ...string) ([]byte, error) {
		switch strings.Join(args, " ") {
		case "ls --installed --json":
			return []byte(`{"node":[` + entry("node/20.1.0") + `,` + entry("node/22.5.0") + `]}`), nil
		case "ls --prunable --json":
			return []byte(`{"node":[` + entry("node/20.1.0") + `]}`), nil
		case "ls --current --installed --json":
			return []byte(`{"node":[` + entry("node/22.5.0") + `]}`), nil
		}
		return nil, errors.New("unexpected mise call: " + strings.Join(args, " "))
	}
}

func readOnlyRecord(t *testing.T, store string) miseuse.Record {
	t.Helper()
	c, err := miseuse.ReadAll(store)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Records) != 1 || c.Records[0].Err != nil {
		t.Fatalf("want exactly one readable record, got %+v", c.Records)
	}
	return c.Records[0].Record
}

// TestTheRecorderRecordsWhatTheWorkspaceUses: the record names what the workspace's configs still
// name — never the version mise would prune — under the launching side's workspace path.
func TestTheRecorderRecordsWhatTheWorkspaceUses(t *testing.T) {
	store := t.TempDir()
	r := &miseUseRecorder{store: store, workspace: "/home/u/code/a", name: miseuse.NewName(),
		now: time.Now, mise: miseLsFixture(store)}
	if err := r.record(); err != nil {
		t.Fatal(err)
	}
	rec := readOnlyRecord(t, store)
	if rec.Workspace != "/home/u/code/a" || rec.Unknown != "" || !reflect.DeepEqual(rec.Installs, []string{"node/22.5.0"}) {
		t.Fatalf("record = %+v, want node/22.5.0 for /home/u/code/a", rec)
	}
}

// TestARecorderWhoseMiseFailsSaysSo: a jail that cannot tell what it uses must say so, because a
// missing record or an empty one reads as "this jail uses nothing" and lets the host remove what
// it is running.
func TestARecorderWhoseMiseFailsSaysSo(t *testing.T) {
	store := t.TempDir()
	ok := miseLsFixture(store)
	r := &miseUseRecorder{store: store, workspace: "/w", name: miseuse.NewName(), now: time.Now,
		mise: func(args ...string) ([]byte, error) {
			if args[1] == "--prunable" {
				return nil, errors.New("`mise ls --prunable --json`: exit status 1: invalid mise.toml")
			}
			return ok(args...)
		}}
	if err := r.record(); err != nil {
		t.Fatal(err)
	}
	rec := readOnlyRecord(t, store)
	if !strings.Contains(rec.Unknown, "invalid mise.toml") || len(rec.Installs) != 0 {
		t.Fatalf("record = %+v, want an unknown record carrying mise's reason", rec)
	}
}

// TestTheRecorderWritesAtStartOnProvisioningAndEveryRefresh pins the three writes: one at once,
// one when provisioning has an outcome (what `mise install` added), and one per refresh for as
// long as the jail runs — which is what keeps a jail running past the window from losing its
// tools.
func TestTheRecorderWritesAtStartOnProvisioningAndEveryRefresh(t *testing.T) {
	store := t.TempDir()
	writes := 0
	fixture := miseLsFixture(store)
	r := &miseUseRecorder{store: store, workspace: "/w", name: miseuse.NewName(), now: time.Now,
		mise: func(args ...string) ([]byte, error) {
			if args[1] == "--installed" && len(args) == 3 {
				writes++
			}
			return fixture(args...)
		}}
	polls := 0
	provisioned := func() bool { polls++; return polls > 2 }
	var waits []time.Duration
	wait := func(d time.Duration) bool {
		waits = append(waits, d)
		return len(waits) < 4 // two polls, then two refreshes, then the jail ends
	}
	recordMiseUseWhileHolding(r, provisioned, wait)
	if writes != 3 {
		t.Errorf("%d writes, want 3: at start, once provisioned, and one refresh before the end", writes)
	}
	want := []time.Duration{provisionOutcomePoll, provisionOutcomePoll, miseuse.Refresh, miseuse.Refresh}
	if !reflect.DeepEqual(waits, want) {
		t.Errorf("waits = %v, want %v", waits, want)
	}
}

// TestTheRealRecorderRunsMiseOfflineInTheWorkspace drives newMiseUseRecorder's own exec against a
// stand-in mise on the jail's PATH: offline, so "latest" is the installed latest the shims run,
// and in the workspace, whose configs are the ones that count.
func TestTheRealRecorderRunsMiseOfflineInTheWorkspace(t *testing.T) {
	home, store, ws := t.TempDir(), t.TempDir(), t.TempDir()
	e := NewEnv(map[string]string{"HOME": home, "MISE_DATA_DIR": store, "YOLO_WORKSPACE": ws,
		"YOLO_HOST_DIR": "/home/u/code/a"})
	seen := filepath.Join(t.TempDir(), "seen")
	script := "#!/bin/sh\n" +
		"echo \"$MISE_OFFLINE $(pwd) $*\" >> '" + seen + "'\n" +
		"case \"$*\" in\n" +
		"  'ls --prunable --json') echo '{}' ;;\n" +
		"  *) echo '{\"just\":[{\"install_path\":\"" + store + "/installs/just/1.58.0\",\"installed\":true}]}' ;;\n" +
		"esac\n"
	if err := os.MkdirAll(e.MiseShims(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(e.MiseShims(), "mise"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	r := newMiseUseRecorder(e)
	if err := r.record(); err != nil {
		t.Fatal(err)
	}
	rec := readOnlyRecord(t, store)
	if rec.Workspace != "/home/u/code/a" || !reflect.DeepEqual(rec.Installs, []string{"just/1.58.0"}) {
		t.Fatalf("record = %+v", rec)
	}
	b, _ := os.ReadFile(seen)
	resolvedWS, _ := filepath.EvalSymlinks(ws)
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if !strings.HasPrefix(line, "1 ") || (!strings.Contains(line, " "+ws+" ") && !strings.Contains(line, " "+resolvedWS+" ")) {
			t.Errorf("mise ran as %q; want MISE_OFFLINE=1 in the workspace %s", line, ws)
		}
	}
}

// TestTheMainProcessStartsTheRecorderBesideItsHold pins the call site: the recorder is started by
// the main process, in the hold branch, before it holds. Without the call no jail records
// anything, and the host's sweep waits forever on a record nobody writes — every unit test above
// green. The integration test that reads a real jail's record is the end-to-end half.
func TestTheMainProcessStartsTheRecorderBesideItsHold(t *testing.T) {
	src, err := os.ReadFile("boot.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	i := strings.Index(s, "\tif mode == modeHold {\n\t\tstartMiseUseRecorder(e)\n\t\treturn holdExitStatus(holdJail(command, os.Stderr))")
	if i < 0 {
		t.Fatal("Main's hold branch no longer starts the mise use recorder before it holds " +
			"(miseuserecord.go): no jail would record which tool versions it uses")
	}
}
