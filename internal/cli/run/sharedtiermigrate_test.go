package run

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// sharedCredFixture puts `body` at <root>/<sharedDir>/creds.json with mode 0600 and
// returns the file path. 0600 is the mode the real credential carries, and the test
// asserts it survives — a migration that widened a token to 0644 would be worse than
// the bug it repairs.
func sharedCredFixture(t *testing.T, root, sharedDir, body string) string {
	t.Helper()
	dir := filepath.Join(root, sharedDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "creds.json")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// prepareWithClaudePack runs the wsState preparation with the real claude pack loaded
// (the one that declares .claude-shared-credentials) and returns wsState.
func prepareWithClaudePack(t *testing.T, ws string) string {
	t.Helper()
	o := goldenOptions(ws, os.Getenv("HOME"))
	return o.prepareWsState(jsonx.NewOrderedMap(), claudePackFixture(t))
}

// A credential stranded in wsState by the pre-2026-08-24 Apple Container mount gap must
// be rescued into GlobalHome, or mounting the shared dir reads to the user as "the
// upgrade logged me out" while a good credential sits behind a mount they cannot see.
func TestStrandedSharedCredentialIsRescuedIntoGlobalHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	ws := t.TempDir()
	shared := packload.SharedDirs(claudePackFixture(t))[0]

	// The AC shape: /home/agent IS wsState, so the credential landed here.
	wsState := filepath.Join(ws, ".yolo", "home")
	sharedCredFixture(t, wsState, shared, `{"token":"stranded"}`)

	prepareWithClaudePack(t, ws)

	rescued := filepath.Join(paths.GlobalHome(), shared, "creds.json")
	got, err := os.ReadFile(rescued)
	if err != nil {
		t.Fatalf("the stranded credential was not rescued into GlobalHome (%s): %v\n"+
			"Without this, mounting the shared dir shadows the only copy the user has "+
			"and the fix presents as a forced re-login.", rescued, err)
	}
	if string(got) != `{"token":"stranded"}` {
		t.Errorf("rescued content = %q, want the stranded credential", got)
	}
	fi, err := os.Stat(rescued)
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("rescued credential mode = %04o, want 0600 — a migration must not widen a token", perm)
	}
}

// THE CASE THAT MUST NOT HAPPEN. A machine that already has a real machine-wide
// credential must keep it: the rescue copies only into a MISSING file. The Python-era
// version of this same path unlinked the real file before re-linking and destroyed the
// token on every boot, which is the symptom that opened #39 — so the clobber direction
// is the one worth a dedicated test, not the happy path.
func TestRescueNeverClobbersAnExistingSharedCredential(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	ws := t.TempDir()
	shared := packload.SharedDirs(claudePackFixture(t))[0]

	wsState := filepath.Join(ws, ".yolo", "home")
	sharedCredFixture(t, wsState, shared, `{"token":"stale-per-workspace"}`)
	good := sharedCredFixture(t, paths.GlobalHome(), shared, `{"token":"the-real-one"}`)

	prepareWithClaudePack(t, ws)

	got, err := os.ReadFile(good)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"token":"the-real-one"}` {
		t.Fatalf("the machine-wide credential was overwritten by a stranded per-workspace copy: %q\n"+
			"The rescue must copy only into a missing file — losing a live token is strictly "+
			"worse than leaving a stale duplicate on disk.", got)
	}
}
