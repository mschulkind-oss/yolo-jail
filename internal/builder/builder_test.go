package builder

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSSHConfigBlock(t *testing.T) {
	want := "Host linux-builder\n  Hostname localhost\n  HostKeyAlias linux-builder\n" +
		"  Port 31022\n  User builder\n  IdentityFile /etc/nix/builder_ed25519\n"
	if got := SSHConfigBlock(); got != want {
		t.Errorf("SSHConfigBlock =\n%q\nwant\n%q", got, want)
	}
}

func TestNixBuildersLine(t *testing.T) {
	want := "builders = ssh-ng://builder@linux-builder aarch64-linux /etc/nix/builder_ed25519 4 - - - -\n" +
		"builders-use-substitutes = true\n"
	if got := NixBuildersLine(4); got != want {
		t.Errorf("NixBuildersLine(4) =\n%q\nwant\n%q", got, want)
	}
}

func TestTrustedUsersLine(t *testing.T) {
	if got, ok := TrustedUsersLine([]string{"root", "alice"}, "bob"); !ok || got != "trusted-users = root alice bob" {
		t.Errorf("merge = %q, %v", got, ok)
	}
	if _, ok := TrustedUsersLine([]string{"root", "bob"}, "bob"); ok {
		t.Error("already-present me should return ok=false")
	}
	if _, ok := TrustedUsersLine([]string{"root", "@admin"}, "bob"); ok {
		t.Error("@admin coverage should return ok=false")
	}
	if _, ok := TrustedUsersLine([]string{"@wheel"}, "bob"); ok {
		t.Error("@wheel coverage should return ok=false")
	}
	// Dedup keeps order + root first.
	if got, _ := TrustedUsersLine([]string{"alice", "root"}, "bob"); got != "trusted-users = root alice bob" {
		t.Errorf("dedup/order = %q", got)
	}
}

// TestGeneratorsParity byte-diffs SSHConfigBlock / NixBuildersLine /
// TrustedUsersLine / SetupRootScript against the live Python builder.py. Skips
// without Python.
func TestGeneratorsParity(t *testing.T) {
	py := pythonRunner(t)
	if py == nil {
		t.Skip("python unavailable")
	}
	script := `
import sys; sys.path.insert(0, 'src')
from pathlib import Path
from cli import builder as b
import json
out = {
  "ssh": b.ssh_config_block(),
  "builders4": b.nix_builders_line(4),
  "builders1": b.nix_builders_line(1),
  "tu_merge": b.trusted_users_line(["root","alice"], "bob") or "",
  "tu_none": b.trusted_users_line(["root","bob"], "bob") or "",
  "root_script": b.setup_root_script(4, "bob", ["root","alice"], Path("/etc/nix/nix.conf")),
  "root_script_no_tu": b.setup_root_script(2, "bob", ["root","bob"], Path("/etc/nix/nix.conf")),
}
print(json.dumps(out))
`
	out, err := py("-c", script).Output()
	if err != nil {
		t.Skipf("python builder import failed: %v", err)
	}
	var want map[string]string
	if err := json.Unmarshal(out, &want); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// The root script resolves the nix-daemon label at runtime; on Linux
	// _detect_nix_daemon_label() returns the default, so pass "" (default).
	got := map[string]string{
		"ssh":               SSHConfigBlock(),
		"builders4":         NixBuildersLine(4),
		"builders1":         NixBuildersLine(1),
		"tu_merge":          orEmpty(TrustedUsersLine([]string{"root", "alice"}, "bob")),
		"tu_none":           orEmpty(TrustedUsersLine([]string{"root", "bob"}, "bob")),
		"root_script":       SetupRootScript(4, "bob", []string{"root", "alice"}, "/etc/nix/nix.conf", pyLabel(t, py)),
		"root_script_no_tu": SetupRootScript(2, "bob", []string{"root", "bob"}, "/etc/nix/nix.conf", pyLabel(t, py)),
	}
	for k, w := range want {
		if got[k] != w {
			t.Errorf("%s mismatch:\n go: %q\n py: %q", k, got[k], w)
		}
	}
}

// pyLabel reads Python's _detect_nix_daemon_label() so the root-script label
// matches (Linux -> the default).
func pyLabel(t *testing.T, py func(...string) *exec.Cmd) string {
	out, err := py("-c", "import sys; sys.path.insert(0,'src'); from cli import builder; print(builder._detect_nix_daemon_label() or 'org.nixos.nix-daemon')").Output()
	if err != nil {
		return "org.nixos.nix-daemon"
	}
	return strings.TrimSpace(string(out))
}

func orEmpty(s string, ok bool) string {
	if !ok {
		return ""
	}
	return s
}

func pythonRunner(t *testing.T) func(args ...string) *exec.Cmd {
	t.Helper()
	root := repoRoot(t)
	if _, err := exec.LookPath("uv"); err == nil {
		return func(args ...string) *exec.Cmd {
			c := exec.Command("uv", append([]string{"run", "python"}, args...)...)
			c.Dir = root
			return c
		}
	}
	if _, err := exec.LookPath("python3"); err == nil {
		return func(args ...string) *exec.Cmd {
			c := exec.Command("python3", args...)
			c.Dir = root
			return c
		}
	}
	return nil
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, _ := os.Getwd()
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found")
		}
		dir = parent
	}
}
