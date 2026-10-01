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

func TestNixCustomConfTakesEffect(t *testing.T) {
	dir := t.TempDir()
	const key = "trusted-users"
	// Not present -> (false, false).
	if eff, ok := NixCustomConfTakesEffect(filepath.Join(dir, "nope.conf"), key); eff || ok {
		t.Errorf("missing file => (%v,%v), want (false,false)", eff, ok)
	}
	conf := filepath.Join(dir, "nix.conf")
	cases := []struct {
		name, body string
		want       bool
	}{
		{"absolute !include", "# comment\nexperimental-features = nix-command\n!include /etc/nix/nix.custom.conf\n", true},
		{"no include", "max-jobs = auto\n", false},
		// Bare `include` (fatal-if-missing form) also matches.
		{"bare include", "include /etc/nix/nix.custom.conf\n", true},
		// The Determinate installer's own footer is RELATIVE, which nix resolves against the
		// including file's directory. Reading it as "no include" sent every untrusted Determinate
		// user to append a line to the nix.conf Determinate marks "do not modify".
		{"relative !include", "# DETERMINATE NIX CONFIG\n!include nix.custom.conf\n", true},
		{"dot-relative !include", "# DETERMINATE NIX CONFIG\n!include ./nix.custom.conf\n", true},
		// A relative include of some OTHER file is still not the custom conf.
		{"unrelated relative include", "!include machines.conf\n", false},
		// nix keeps a setting's LAST assignment and reads an include where it stands, so nix.conf
		// assigning the key itself after the include overrides the custom file's line. Measured on
		// a real Mac: docs/plans/runbooks/mac-sandvault-session.md §5, item 2.
		{"assigned again after the include", "!include /etc/nix/nix.custom.conf\ntrusted-users = root matt\n", false},
		{"assigned again after the include, unspaced", "!include nix.custom.conf\ntrusted-users=root\n", false},
		// Before the include, an `extra-` append, a comment or another key does not override it.
		{"assigned before the include", "trusted-users = root\n!include nix.custom.conf\n", true},
		{"extra- after the include", "!include nix.custom.conf\nextra-trusted-users = matt\n", true},
		{"commented after the include", "!include nix.custom.conf\n# trusted-users = root\n", true},
		{"another key after the include", "!include nix.custom.conf\ntrusted-substituters = x\n", true},
	}
	for _, tc := range cases {
		must(t, os.WriteFile(conf, []byte(tc.body), 0o644))
		if eff, ok := NixCustomConfTakesEffect(conf, key); eff != tc.want || !ok {
			t.Errorf("%s: %q => (%v,%v), want (%v,true)", tc.name, tc.body, eff, ok, tc.want)
		}
	}
}

func TestNixDaemonLabelIn(t *testing.T) {
	dir := t.TempDir()
	// Empty dir -> not found.
	if _, ok := NixDaemonLabelIn(dir, ""); ok {
		t.Error("empty dir should not find a daemon label")
	}
	// The installers' nix-hook plist is not a daemon.
	must(t, os.WriteFile(filepath.Join(dir, "systems.determinate.nix-installer.nix-hook.plist"), nil, 0o644))
	if label, ok := NixDaemonLabelIn(dir, DeterminateNixDaemonLabel); ok {
		t.Errorf("the nix-hook plist is not a daemon, got %q", label)
	}
	// Only the upstream daemon's plist: it is found whatever is preferred.
	must(t, os.WriteFile(filepath.Join(dir, "org.nixos.nix-daemon.plist"), nil, 0o644))
	if label, ok := NixDaemonLabelIn(dir, DeterminateNixDaemonLabel); !ok || label != UpstreamNixDaemonLabel {
		t.Errorf("label = %q,%v, want the only daemon there, %s", label, ok, UpstreamNixDaemonLabel)
	}
	// Both daemons' plists, which a switch between distributions leaves behind: the preferred one
	// wins, and with no preference the first in sorted order.
	must(t, os.WriteFile(filepath.Join(dir, "systems.determinate.nix-daemon.plist"), nil, 0o644))
	for prefer, want := range map[string]string{
		DeterminateNixDaemonLabel: DeterminateNixDaemonLabel,
		UpstreamNixDaemonLabel:    UpstreamNixDaemonLabel,
		"":                        UpstreamNixDaemonLabel,
	} {
		if label, ok := NixDaemonLabelIn(dir, prefer); !ok || label != want {
			t.Errorf("prefer %q: label = %q,%v, want %q", prefer, label, ok, want)
		}
	}
}

// `nix --version` names the distribution: Determinate Nix prints its own version and the Nix
// version it is built from (nix-src src/libmain/shared.cc), upstream Nix only its own.
func TestParseNixVersion(t *testing.T) {
	cases := []struct {
		out      string
		dist     NixDistribution
		describe string
	}{
		{"nix (Determinate Nix 3.22.5) 2.35.2\n", NixDeterminate, "Determinate Nix 3.22.5 (based on Nix 2.35.2)"},
		{"nix (Nix) 2.34.7\n", NixUpstream, "2.34.7"},
		{"nix (Nix) 2.3.16", NixUpstream, "2.3.16"},
		{"nix (Lix, like Nix) 2.91.1\n", NixUnknown, "nix (Lix, like Nix) 2.91.1"},
		{"", NixUnknown, ""},
	}
	for _, tc := range cases {
		v := ParseNixVersion(tc.out)
		if v.Distribution != tc.dist || v.Describe() != tc.describe {
			t.Errorf("%q => %v %q, want %v %q", tc.out, v.Distribution, v.Describe(), tc.dist, tc.describe)
		}
	}
	for dist, want := range map[NixDistribution]string{
		NixDeterminate: DeterminateNixDaemonLabel,
		NixUpstream:    UpstreamNixDaemonLabel,
		NixUnknown:     "",
	} {
		if got := dist.DaemonLabel(); got != want {
			t.Errorf("%v's daemon label = %q, want %q", dist, got, want)
		}
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

// TestEnsureGlobalStorageCreatesTheCapturesDir pins the CALL SITE, not the path helper.
//
// paths.CapturesDir() is only a string until something makes the directory, and the capture store
// needs it to exist for a reason nothing else in that MkdirAll list shares: admission is an
// os.Rename out of <CapturesDir>/staging, so the staging root and the entries must be provisioned
// on the same filesystem, up front, by boot — not lazily by whoever happens to run a capture
// first. Delete paths.CapturesDir() from EnsureGlobalStorage's list and this test fails.
func TestEnsureGlobalStorageCreatesTheCapturesDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	must(t, EnsureGlobalStorage(nil))

	if st, err := os.Stat(paths.CapturesDir()); err != nil || !st.IsDir() {
		t.Fatalf("EnsureGlobalStorage did not create %s: %v", paths.CapturesDir(), err)
	}
	// A sibling of the other machine-wide stores, under one state dir — a capture is
	// per-machine like a fetched pack, never per-workspace.
	if got, want := filepath.Dir(paths.CapturesDir()), paths.GlobalStorage(); got != want {
		t.Errorf("captures dir parent = %q, want %q", got, want)
	}
}
