package storage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
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

func TestEnsureCacheRelocations(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	other := t.TempDir()

	// The target's last component is missing (the supported fresh-host case);
	// the mountpoint under GLOBAL_CACHE does not exist at all.
	target := filepath.Join(other, "huggingface")
	must(t, EnsureCacheRelocations([]config.CacheRelocation{{Subdir: "huggingface", Target: target}}))
	if st, err := os.Stat(target); err != nil || !st.IsDir() {
		t.Errorf("target %s not created: %v", target, err)
	}
	mountpoint := filepath.Join(paths.GlobalCache(), "huggingface")
	if st, err := os.Stat(mountpoint); err != nil || !st.IsDir() {
		t.Errorf("mountpoint %s not created: %v", mountpoint, err)
	}

	// Idempotent: a second call over the now-existing dirs succeeds.
	must(t, EnsureCacheRelocations([]config.CacheRelocation{{Subdir: "huggingface", Target: target}}))

	// Nothing configured => nothing created, no error.
	must(t, EnsureCacheRelocations(nil))
}

// TestEnsureCacheRelocationsRefusesMissingParent pins the asymmetry that makes
// the feature safe: the last component is created, a missing PARENT is a typo
// and must fail loudly instead of materializing an empty dir on the very
// filesystem the user is trying to move bytes off.
func TestEnsureCacheRelocationsRefusesMissingParent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	target := filepath.Join(t.TempDir(), "relcoated", "huggingface")

	err := EnsureCacheRelocations([]config.CacheRelocation{{Subdir: "huggingface", Target: target}})
	if err == nil {
		t.Fatal("missing parent must be an error")
	}
	if !strings.Contains(err.Error(), "parent directory of the target does not exist") {
		t.Errorf("error = %q, want the missing-parent wording", err)
	}
	if _, statErr := os.Stat(filepath.Dir(target)); statErr == nil {
		t.Errorf("%s was created despite the missing parent", filepath.Dir(target))
	}
	// The mountpoint must not be created either — a half-provisioned relocation
	// leaves an empty stub in the cache that looks like real (lost) data.
	if _, statErr := os.Stat(filepath.Join(paths.GlobalCache(), "huggingface")); statErr == nil {
		t.Error("mountpoint created for a rejected relocation")
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
