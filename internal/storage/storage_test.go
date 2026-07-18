package storage

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestLinuxMultilib(t *testing.T) {
	// Whatever this host's GOARCH is, the result must be one of the known
	// mappings and end in -linux-gnu.
	got := LinuxMultilib()
	switch pythonMachine() {
	case "x86_64":
		if got != "x86_64-linux-gnu" {
			t.Errorf("x86_64 => %q", got)
		}
	case "aarch64":
		if got != "aarch64-linux-gnu" {
			t.Errorf("aarch64 => %q", got)
		}
	}
}

func TestNixCustomConfIncluded(t *testing.T) {
	dir := t.TempDir()
	// Not present -> (false, false).
	if inc, ok := nixCustomConfIncludedAt(filepath.Join(dir, "nope.conf")); inc || ok {
		t.Errorf("missing file => (%v,%v), want (false,false)", inc, ok)
	}
	// Present with !include -> (true, true).
	conf := filepath.Join(dir, "nix.conf")
	must(t, os.WriteFile(conf, []byte("# comment\nexperimental-features = nix-command\n!include /etc/nix/nix.custom.conf\n"), 0o644))
	if inc, ok := nixCustomConfIncludedAt(conf); !inc || !ok {
		t.Errorf("!include => (%v,%v), want (true,true)", inc, ok)
	}
	// Present without the include -> (false, true).
	must(t, os.WriteFile(conf, []byte("max-jobs = auto\n"), 0o644))
	if inc, ok := nixCustomConfIncludedAt(conf); inc || !ok {
		t.Errorf("no include => (%v,%v), want (false,true)", inc, ok)
	}
	// Bare `include` (fatal-if-missing form) also matches.
	must(t, os.WriteFile(conf, []byte("include /etc/nix/nix.custom.conf\n"), 0o644))
	if inc, ok := nixCustomConfIncludedAt(conf); !inc || !ok {
		t.Errorf("bare include => (%v,%v), want (true,true)", inc, ok)
	}
}

func TestDetectNixDaemonLabel(t *testing.T) {
	dir := t.TempDir()
	// Empty dir -> not found.
	if _, ok := detectNixDaemonLabelIn(dir); ok {
		t.Error("empty dir should not find a daemon label")
	}
	// Determinate + official present; sorted order => determinate wins
	// ("systems..." sorts after "org..."? no — 'o' < 's', so org wins).
	must(t, os.WriteFile(filepath.Join(dir, "org.nixos.nix-daemon.plist"), nil, 0o644))
	must(t, os.WriteFile(filepath.Join(dir, "systems.determinate.nix-daemon.plist"), nil, 0o644))
	label, ok := detectNixDaemonLabelIn(dir)
	if !ok || label != "org.nixos.nix-daemon" {
		t.Errorf("label = %q,%v (want org.nixos.nix-daemon, first sorted)", label, ok)
	}
}

func TestDetectHostTimezone(t *testing.T) {
	dir := t.TempDir()
	// 1. $TZ wins.
	env := func(k string) string {
		if k == "TZ" {
			return "America/New_York"
		}
		return ""
	}
	if tz, ok := detectHostTimezone(env, filepath.Join(dir, "tz"), filepath.Join(dir, "lt")); !ok || tz != "America/New_York" {
		t.Errorf("TZ => %q,%v", tz, ok)
	}
	// 2. /etc/timezone plain text.
	etcTz := filepath.Join(dir, "timezone")
	must(t, os.WriteFile(etcTz, []byte("Europe/Berlin\n"), 0o644))
	noEnv := func(string) string { return "" }
	if tz, ok := detectHostTimezone(noEnv, etcTz, filepath.Join(dir, "lt")); !ok || tz != "Europe/Berlin" {
		t.Errorf("/etc/timezone => %q,%v", tz, ok)
	}
	// 3. /etc/localtime symlink suffix after /zoneinfo/.
	lt := filepath.Join(dir, "localtime")
	must(t, os.Symlink("/usr/share/zoneinfo/Asia/Tokyo", lt))
	if tz, ok := detectHostTimezone(noEnv, filepath.Join(dir, "none"), lt); !ok || tz != "Asia/Tokyo" {
		t.Errorf("localtime => %q,%v", tz, ok)
	}
	// Nothing works -> ("", false).
	if _, ok := detectHostTimezone(noEnv, filepath.Join(dir, "none"), filepath.Join(dir, "none2")); ok {
		t.Error("no signals should return ok=false")
	}
}

func TestEnsureSymlinkMigratesRegularFile(t *testing.T) {
	dir := t.TempDir()
	link := filepath.Join(dir, ".gitconfig")
	must(t, os.WriteFile(link, []byte("[user]\n\tname = matt\n"), 0o644))
	target := filepath.Join(".config", "git", "config")
	must(t, EnsureSymlink(link, target))
	// link is now a symlink pointing at the relative target.
	if !isSymlink(link) {
		t.Fatal("link should be a symlink after migration")
	}
	got, _ := os.Readlink(link)
	if got != target {
		t.Errorf("readlink = %q, want %q", got, target)
	}
	// Data migrated to the real location.
	real := filepath.Join(dir, target)
	if data, _ := os.ReadFile(real); string(data) != "[user]\n\tname = matt\n" {
		t.Errorf("migrated data = %q", data)
	}
	// Idempotent: a second call is a no-op (same target).
	must(t, EnsureSymlink(link, target))
	if got, _ := os.Readlink(link); got != target {
		t.Errorf("second call changed link to %q", got)
	}
}

func TestFindDanglingMiseSymlinks(t *testing.T) {
	dir := t.TempDir()
	installs := filepath.Join(dir, "installs", "node")
	must(t, os.MkdirAll(installs, 0o755))
	// A resolving symlink (kept) + a dangling one (found) + a regular file.
	realTarget := filepath.Join(dir, "real")
	must(t, os.WriteFile(realTarget, []byte("x"), 0o644))
	must(t, os.Symlink(realTarget, filepath.Join(installs, "20.0.0")))
	must(t, os.Symlink("/workspace/.cargo/nonexistent", filepath.Join(installs, "18.0.0")))
	must(t, os.WriteFile(filepath.Join(installs, "regular"), nil, 0o644))
	got := FindDanglingMiseSymlinks(dir)
	if len(got) != 1 || filepath.Base(got[0]) != "18.0.0" {
		t.Errorf("dangling = %v, want only 18.0.0", got)
	}
}

func TestMigrateStorageLayoutFailSafe(t *testing.T) {
	// insideJail short-circuits regardless.
	called := false
	MigrateStorageLayout(true, func() bool { called = true; return true }, nil)
	if called {
		t.Error("insideJail must not probe liveness")
	}
}

// TestClaudeJSONSyncParity byte-diffs SyncClaudeJSONSeed's file outputs against
// live _sync_claude_json_seed for the forward, reverse, and no-op cases. Skips
// without Python.
func TestClaudeJSONSyncParity(t *testing.T) {
	py := pythonRunner(t)
	if py == nil {
		t.Skip("python unavailable")
	}

	cases := []struct {
		name     string
		seed, ws string // initial JSON contents ("" = file absent)
	}{
		{
			name: "forward_seed_to_ws",
			seed: `{"oauthAccount":{"emailAddress":"a@b.co"},"hasCompletedOnboarding":true}`,
			ws:   `{"mcpServers":{"x":1},"projects":{}}`,
		},
		{
			name: "reverse_ws_to_seed",
			seed: "",
			ws:   `{"oauthAccount":{"emailAddress":"z@z.co"},"hasCompletedOnboarding":true,"mcpServers":{"k":2}}`,
		},
		{
			name: "noop_both_logged_in",
			seed: `{"oauthAccount":{"emailAddress":"a@b.co"}}`,
			ws:   `{"oauthAccount":{"emailAddress":"a@b.co"},"projects":{"p":1}}`,
		},
		{
			name: "seed_empty_ws_empty",
			seed: "",
			ws:   "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			goSeed, goWS := runSync(t, tc.seed, tc.ws, func(seedP, wsP string) {
				SyncClaudeJSONSeed(seedP, wsP)
			})
			pySeed, pyWS := runSync(t, tc.seed, tc.ws, func(seedP, wsP string) {
				pySync(t, py, seedP, wsP)
			})
			if goSeed != pySeed {
				t.Errorf("seed mismatch:\n go: %q\n py: %q", goSeed, pySeed)
			}
			if goWS != pyWS {
				t.Errorf("ws mismatch:\n go: %q\n py: %q", goWS, pyWS)
			}
		})
	}
}

// runSync sets up a temp dir with seed/ws files, invokes fn, and returns the
// resulting file contents ("<absent>" when the file does not exist).
func runSync(t *testing.T, seed, ws string, fn func(seedP, wsP string)) (string, string) {
	t.Helper()
	dir := t.TempDir()
	seedP := filepath.Join(dir, "seed.json")
	wsP := filepath.Join(dir, "ws.json")
	if seed != "" {
		must(t, os.WriteFile(seedP, []byte(seed), 0o644))
	}
	if ws != "" {
		must(t, os.WriteFile(wsP, []byte(ws), 0o644))
	}
	fn(seedP, wsP)
	return readOrAbsent(seedP), readOrAbsent(wsP)
}

func readOrAbsent(p string) string {
	data, err := os.ReadFile(p)
	if err != nil {
		return "<absent>"
	}
	return string(data)
}

func pySync(t *testing.T, py func(...string) *exec.Cmd, seedP, wsP string) {
	t.Helper()
	script := `
import sys; sys.path.insert(0, 'src')
from pathlib import Path
from cli.storage import _sync_claude_json_seed
_sync_claude_json_seed(Path(sys.argv[1]), Path(sys.argv[2]))
`
	cmd := py("-c", script, seedP, wsP)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("python sync failed: %v\n%s", err, out)
	}
}

// TestProbesParity cross-checks the pure probes against live Python where the
// filesystem inputs are controllable (nix.conf include, tz text). Skips without
// Python.
func TestProbesParity(t *testing.T) {
	py := pythonRunner(t)
	if py == nil {
		t.Skip("python unavailable")
	}
	var pyOut struct {
		Multilib string `json:"multilib"`
	}
	script := `
import sys; sys.path.insert(0, 'src')
import json
from cli.storage import _linux_multilib
print(json.dumps({"multilib": _linux_multilib()}))
`
	out, err := py("-c", script).Output()
	if err != nil {
		t.Skipf("python import failed: %v", err)
	}
	if err := json.Unmarshal(out, &pyOut); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if LinuxMultilib() != pyOut.Multilib {
		t.Errorf("multilib go=%q py=%q", LinuxMultilib(), pyOut.Multilib)
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
