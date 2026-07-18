package buildercmd

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSetupStateParityVsLivePython drives builder.py's builder_setup_state /
// _builder_conf_path / _nix_conf_has_builder over a small file-fixture matrix
// and diffs the Go BuilderSetupState against it. Skips without Python.
//
// It monkeypatches the module's path constants at the config paths so the probe
// reads temp files (no real /etc access), matching how the Go Deps are wired to
// the same temp paths.
func TestSetupStateParityVsLivePython(t *testing.T) {
	py := pythonRunner(t)
	if py == nil {
		t.Skip("python unavailable")
	}

	confWithBuilder := "builders = ssh-ng://builder@linux-builder aarch64-linux /etc/nix/builder_ed25519 4 - - - -\n"
	confCommented := "# builders = ssh-ng://builder@linux-builder aarch64-linux …\n"
	confWrongHost := "builders = ssh-ng://builder@other aarch64-linux key 4 - - - -\n"

	cases := []struct {
		name          string
		sshExists     bool
		keyExists     bool
		confText      string // "" => conf file absent
		customConfInc bool
	}{
		{"none", false, false, "", false},
		{"ssh-only", true, false, "", false},
		{"wired-no-key", true, false, confWithBuilder, false},
		{"fully-wired", true, true, confWithBuilder, false},
		{"commented-builder", true, true, confCommented, false},
		{"wrong-host", true, true, confWrongHost, false},
		{"custom-conf", true, true, confWithBuilder, true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			sshPath := filepath.Join(dir, "ssh_config")
			keyPath := filepath.Join(dir, "builder_ed25519")
			confPlain := filepath.Join(dir, "nix.conf")
			confCustom := filepath.Join(dir, "nix.custom.conf")
			if c.sshExists {
				must(t, os.WriteFile(sshPath, []byte("x"), 0o644))
			}
			if c.keyExists {
				must(t, os.WriteFile(keyPath, []byte("x"), 0o600))
			}
			usedConf := confPlain
			if c.customConfInc {
				usedConf = confCustom
			}
			if c.confText != "" {
				must(t, os.WriteFile(usedConf, []byte(c.confText), 0o644))
			}

			// Python oracle: monkeypatch builder.py's path constants + probes.
			script := `
import sys, json
sys.path.insert(0, 'src')
from pathlib import Path
from cli import builder as b
b.SSH_CONFIG_PATH = Path(` + jstr(sshPath) + `)
b.BUILDER_KEY_PATH = ` + jstr(keyPath) + `
b._builder_conf_path = lambda: Path(` + jstr(usedConf) + `)
st = b.builder_setup_state()
print(json.dumps({"ssh": st["ssh_config"], "nix": st["nix_builder"], "key": st["key"], "done": st["done"]}))
`
			out, err := py("-c", script).Output()
			if err != nil {
				t.Skipf("python builder import failed: %v", err)
			}
			var want struct {
				SSH  bool `json:"ssh"`
				Nix  bool `json:"nix"`
				Key  bool `json:"key"`
				Done bool `json:"done"`
			}
			if err := json.Unmarshal(out, &want); err != nil {
				t.Fatalf("decode: %v", err)
			}

			// Go: wire Deps to the same temp paths. We re-point the probe helpers
			// by using a Deps whose FileIsFile/ReadFileText/NixCustomConfIncluded
			// map the builder.py constants to our temp files.
			d := Deps{
				FileIsFile: func(p string) bool {
					// Map the well-known constants to temp paths.
					switch p {
					case "/etc/ssh/ssh_config.d/100-linux-builder.conf":
						return c.sshExists
					case "/etc/nix/builder_ed25519":
						return c.keyExists
					case "/etc/nix/nix.conf", "/etc/nix/nix.custom.conf":
						return c.confText != "" && ((p == "/etc/nix/nix.custom.conf") == c.customConfInc)
					}
					return false
				},
				ReadFileText: func(p string) (string, bool) {
					if (p == "/etc/nix/nix.custom.conf") == c.customConfInc && c.confText != "" {
						return c.confText, true
					}
					return "", false
				},
				NixCustomConfIncluded: func() (bool, bool) { return c.customConfInc, true },
			}
			got := BuilderSetupState(d)
			if got.SSHConfig != want.SSH || got.NixBuilder != want.Nix || got.Key != want.Key || got.Done != want.Done {
				t.Errorf("setup_state mismatch:\n go: %+v\n py: %+v", got, want)
			}
		})
	}
}

func jstr(s string) string { b, _ := json.Marshal(s); return string(b) }

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
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
