package loopholes

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetEnabledMissingUserLoophole(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	var out, errBuf bytes.Buffer
	deps := Deps{Out: &out, Err: &errBuf, Cwd: home}
	rc := CmdSetEnabled(deps, "nonexistent", true)
	if rc != 1 {
		t.Errorf("rc = %d, want 1", rc)
	}
	if !strings.Contains(errBuf.String(), "No user-installed loophole at") {
		t.Errorf("err = %q", errBuf.String())
	}
}

func TestSetEnabledRoundTrip(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// Create a user-installed loophole manifest.
	userDir := UserLoopholesDir()
	lhDir := filepath.Join(userDir, "myhole")
	must(t, os.MkdirAll(lhDir, 0o755))
	must(t, os.WriteFile(filepath.Join(lhDir, "manifest.jsonc"),
		[]byte(`{"name": "myhole", "description": "test", "transport": "none", "enabled": true}`), 0o644))

	var out, errBuf bytes.Buffer
	deps := Deps{Out: &out, Err: &errBuf, Cwd: home}
	if rc := CmdSetEnabled(deps, "myhole", false); rc != 0 {
		t.Fatalf("disable rc = %d, err=%q", rc, errBuf.String())
	}
	if out.String() != "disabled myhole\n" {
		t.Errorf("disable output = %q", out.String())
	}
	// Manifest now has enabled:false.
	data, _ := os.ReadFile(filepath.Join(lhDir, "manifest.jsonc"))
	if !strings.Contains(string(data), "false") {
		t.Errorf("manifest not updated: %s", data)
	}
	out.Reset()
	if rc := CmdSetEnabled(deps, "myhole", true); rc != 0 {
		t.Fatalf("enable rc = %d", rc)
	}
	if out.String() != "enabled myhole\n" {
		t.Errorf("enable output = %q", out.String())
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
