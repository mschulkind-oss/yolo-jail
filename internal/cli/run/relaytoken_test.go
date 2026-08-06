package run

import (
	"os"
	"path/filepath"
	"testing"
)

// relayTCPToken guards the macOS broker TCP front — knowing the token IS broker
// access — so these cover where it may live and what it may trust.

func TestRelayTCPTokenIsStableAndPrivateTestCase(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	o := &Options{IsMacOS: true}

	tok := o.relayTCPToken("yolo-demo")
	if !isHexToken(tok) {
		t.Fatalf("expected a hex token, got %q", tok)
	}
	// Both the terminator env and the relay's --token-file read this, so a
	// second call must agree rather than rotate under a running container.
	if again := o.relayTCPToken("yolo-demo"); again != tok {
		t.Errorf("token rotated: %q -> %q", tok, again)
	}
	if other := o.relayTCPToken("yolo-other"); other == tok {
		t.Error("two jails share one token")
	}

	path := relayTokenFile(relayShortHash("yolo-demo"))
	// A secret must not live in the world-writable /tmp beside the pid/lock
	// files, where any local user can pre-create or read the path.
	if filepath.Dir(path) == "/tmp" {
		t.Errorf("token file lives in shared /tmp: %s", path)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat token file: %v", err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Errorf("token file mode = %v, want 0600", st.Mode().Perm())
	}
	if d, err := os.Stat(filepath.Dir(path)); err != nil || d.Mode().Perm() != 0o700 {
		t.Errorf("token dir mode = %v (err %v), want 0700", d.Mode().Perm(), err)
	}
}

func TestRelayTCPTokenRejectsPlantedFileTestCase(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	o := &Options{IsMacOS: true}

	path := relayTokenFile(relayShortHash("yolo-demo"))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	// A planted symlink must neither be trusted as the token nor be followed by
	// the write — that would leak the secret into an attacker-chosen file.
	victim := filepath.Join(home, "victim")
	planted := "deadbeef00000000000000000000000000000000000000000000000000000000"
	if err := os.WriteFile(victim, []byte(planted+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, path); err != nil {
		t.Fatal(err)
	}

	tok := o.relayTCPToken("yolo-demo")
	if tok == planted {
		t.Error("trusted a token from a planted symlink")
	}
	if !isHexToken(tok) {
		t.Fatalf("expected a fresh hex token, got %q", tok)
	}
	data, err := os.ReadFile(victim)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != planted+"\n" {
		t.Error("wrote the token through the symlink")
	}
}

func TestRelayTCPTokenOffMacOSTestCase(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	o := &Options{IsMacOS: false}
	if tok := o.relayTCPToken("yolo-demo"); tok != "" {
		t.Errorf("token on Linux = %q, want empty (unix-only path)", tok)
	}
}
