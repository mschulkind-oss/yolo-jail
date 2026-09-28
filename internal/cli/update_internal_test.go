package cli

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/selfupdate"
)

// updateCheckTestHome points HOME at a fresh directory, clears every variable
// that turns the check off (CI is set on every CI runner), and returns the
// home.
func updateCheckTestHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, k := range []string{"CI", "YOLO_VERSION", selfupdate.DisableEnv} {
		t.Setenv(k, "")
	}
	return home
}

// The background check's whole call path: the argv SpawnBackgroundCheck runs,
// through runInternal's dispatch, to the state file and the lock. Renaming the
// verb on either side, or dropping the runInternal case, fails this.
func TestInternalUpdateCheckRunsEndToEnd(t *testing.T) {
	updateCheckTestHome(t)
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"tag_name": "v0.11.0"}`))
	}))
	t.Cleanup(srv.Close)
	origCh, origURL := internalCheckChannel, internalCheckReleaseURL
	t.Cleanup(func() { internalCheckChannel, internalCheckReleaseURL = origCh, origURL })
	internalCheckChannel = func() selfupdate.Channel { return testChannel }
	internalCheckReleaseURL = srv.URL

	statePath := filepath.Join(t.TempDir(), "update-check", "state.json")
	lock := selfupdate.LockPath(statePath)
	run := func() int {
		t.Helper()
		// The spawner takes the lock before it starts the child; the child
		// must give it back.
		if !selfupdate.AcquireLock(lock, time.Now()) {
			t.Fatal("could not take the lock the spawner would hold")
		}
		args := selfupdate.BackgroundCheckArgs(statePath)
		if len(args) == 0 || args[0] != "internal" {
			t.Fatalf("background check argv %v does not start with `internal`", args)
		}
		return runInternal(args[1:])
	}

	if rc := run(); rc != 0 {
		t.Fatalf("runInternal exited %d", rc)
	}
	st := selfupdate.LoadState(statePath)
	if st.Identity != testChannel.Identity() || st.Latest != "0.11.0" || !st.Available || st.Error != "" {
		t.Errorf("state after the check = %+v; want 0.11.0 available for %s", st, testChannel.Identity())
	}
	if requests.Load() != 1 {
		t.Errorf("release server saw %d requests, want 1", requests.Load())
	}
	if _, err := os.Stat(lock); !os.IsNotExist(err) {
		t.Errorf("the lock survived the check (stat err %v)", err)
	}

	// "update_check": false in the user config turns the check off here too,
	// and the lock is still given back.
	writeUserConfig(t, `{"update_check": false}`)
	if err := os.Remove(statePath); err != nil {
		t.Fatal(err)
	}
	if rc := run(); rc != 0 {
		t.Fatalf("runInternal exited %d with the check off", rc)
	}
	if _, err := os.Stat(statePath); !os.IsNotExist(err) {
		t.Errorf("a check ran with update_check false (stat err %v)", err)
	}
	if requests.Load() != 1 {
		t.Errorf("release server saw %d requests with the check off, want still 1", requests.Load())
	}
	if _, err := os.Stat(lock); !os.IsNotExist(err) {
		t.Errorf("the lock survived a disabled check (stat err %v)", err)
	}
}

// The user config's opt-out reaches the real dependencies the notice and the
// launch offer run with, not only a test's injected gate.
func TestUpdateCheckConfigOptOutReachesDefaultUpdateDeps(t *testing.T) {
	updateCheckTestHome(t)
	if !defaultUpdateDeps().configEnabled() {
		t.Error("with no user config the check must be on")
	}
	writeUserConfig(t, `{
  // turned off by the user
  "update_check": false
}`)
	if defaultUpdateDeps().configEnabled() {
		t.Error(`"update_check": false in the user config did not turn the check off`)
	}
}
