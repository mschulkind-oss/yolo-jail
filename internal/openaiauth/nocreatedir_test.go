package openaiauth

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A daemon's broker (NoCreateDir) refuses a write into a state dir that has gone, and leaves
// it gone: the daemon exits for that removal (hostservice.WatchStateDir), and a login landing
// before the exit must not bring back a directory a launch has just retired.
func TestANoCreateDirBrokerNeverRecreatesItsStateDir(t *testing.T) {
	b, now := testBroker(t, nil)
	dir := filepath.Join(filepath.Dir(b.StatePath), "state")
	b.StatePath, b.LockPath = filepath.Join(dir, StateFileName), filepath.Join(dir, "refresh.lock")
	b.NoCreateDir = true
	tokens := Tokens{AccessToken: "a", IDToken: "i", RefreshToken: "r", ExpiresAt: now.Add(time.Hour)}

	_, err := b.Replace(tokens)
	if err == nil || !strings.Contains(err.Error(), "is gone") || !strings.Contains(err.Error(), dir) {
		t.Fatalf("Replace into a missing dir = %v, want an error saying %s is gone", err, dir)
	}
	if _, err := os.Lstat(dir); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the refused write left %s behind (%v)", dir, err)
	}

	// With the dir present it writes as ever, 0700 included.
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Replace(tokens); err != nil {
		t.Fatalf("Replace into the existing dir: %v", err)
	}
	if st, err := os.Stat(dir); err != nil || st.Mode().Perm() != 0o700 {
		t.Errorf("state dir mode = %v (%v), want 0700", st.Mode().Perm(), err)
	}
}

// The default is unchanged: a broker that is not a daemon's creates what it needs.
func TestABrokerCreatesItsStateDirByDefault(t *testing.T) {
	b, now := testBroker(t, nil)
	dir := filepath.Join(filepath.Dir(b.StatePath), "state")
	b.StatePath, b.LockPath = filepath.Join(dir, StateFileName), filepath.Join(dir, "refresh.lock")
	if _, err := b.Replace(Tokens{AccessToken: "a", IDToken: "i", RefreshToken: "r",
		ExpiresAt: now.Add(time.Hour)}); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	if _, err := os.Stat(b.StatePath); err != nil {
		t.Fatalf("no state written: %v", err)
	}
}
