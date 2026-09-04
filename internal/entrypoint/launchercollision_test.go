package entrypoint

// launchercollision_test.go is B2's generation-time check (OQ-PD12a), driven through the
// PRODUCTION generators.
//
// THE SHAPE MATTERS MORE THAN USUAL HERE. B2 converts "a pack cannot shadow /bin/fzf" from
// a structural impossibility into a handled case, so the design's own instruction is that
// the test must fail when the CHECK is deleted, not merely show that the check works. Every
// cell below therefore drives GenerateAgentLaunchers or GeneratePackageManagerLaunchers —
// the functions boot.go calls — and asserts on the FILES they wrote.
//
// `sh` is the probe name because /bin/sh exists on every platform this test can run on
// (the nix image, a CI runner, a developer's Mac), which makes the cell a statement about a
// real baked binary rather than about a fixture.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writePackWithProgram stages a one-pack YOLO_PACK_ROOT declaring `program <bin>`.
func writePackWithProgram(t *testing.T, name string, bins ...string) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	var entries []string
	for _, b := range bins {
		entries = append(entries,
			`{"kind":"program","bin":"`+b+`","via":"npm","package":"`+b+`-pkg"}`)
	}
	manifest := `{"name":"` + name + `","contributes":[` + strings.Join(entries, ",") + `]}`
	if err := os.WriteFile(filepath.Join(dir, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// TestNoLauncherForANameTheImageProvides is the load-bearing cell. Delete the check in
// GenerateAgentLaunchers and ~/.yolo/bin/launch/sh appears — which, with the launch dir
// ahead of /bin after B2, is a lazy npm installer standing in front of the system shell.
//
// The second bin is not decoration: without it the cell would also pass against a generator
// that wrote NO launchers at all, which is the other way to make the shadowing impossible
// and the wrong way.
func TestNoLauncherForANameTheImageProvides(t *testing.T) {
	home := t.TempDir()
	e := NewEnv(map[string]string{
		"JAIL_HOME":      home,
		"YOLO_PACK_ROOT": writePackWithProgram(t, "shadowy", "sh", "yolo-not-a-real-binary"),
	})
	if err := GenerateAgentLaunchers(e); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(e.LaunchDir(), "sh")); !os.IsNotExist(err) {
		t.Errorf("a launcher was written for `sh`, which /bin provides (err=%v). With the "+
			"launch dir ahead of /bin, that launcher shadows the system shell — the "+
			"protection B2 moved from PATH position to this check", err)
	}
	if _, err := os.Stat(filepath.Join(e.LaunchDir(), "yolo-not-a-real-binary")); err != nil {
		t.Errorf("a name the image does NOT provide must still get its launcher, or the "+
			"check has switched the whole mechanism off: %v", err)
	}
}

// TestNoLauncherForADeclaredMiseTool is the other arm, and P6's one contention case: a
// project dependency must not lose its name to an agent-class mechanism.
//
// DECLARED, not installed. GenerateAgentLaunchers runs before ConfigureMisePrism, so on a
// cold boot the shim directory is empty — a check that read the directory would find
// nothing here and write the launcher anyway. The env var IS the declaration, so this cell
// sets it and creates no shim dir at all.
func TestNoLauncherForADeclaredMiseTool(t *testing.T) {
	home := t.TempDir()
	e := NewEnv(map[string]string{
		"JAIL_HOME":       home,
		"MISE_DATA_DIR":   filepath.Join(home, "mise"),
		"YOLO_MISE_TOOLS": `{"node":"24","npm:some-tool":"1.2.3"}`,
		"YOLO_PACK_ROOT":  writePackWithProgram(t, "toolsy", "node", "some-tool", "unclaimed-bin"),
	})
	if err := GenerateAgentLaunchers(e); err != nil {
		t.Fatal(err)
	}

	for _, bin := range []string{"node", "some-tool"} {
		if _, err := os.Stat(filepath.Join(e.LaunchDir(), bin)); !os.IsNotExist(err) {
			t.Errorf("a launcher was written for %q, which the workspace declares as a mise "+
				"tool (err=%v) — P6: a project dependency must not lose its name to an "+
				"agent-class mechanism", bin, err)
		}
	}
	if _, err := os.Stat(filepath.Join(e.LaunchDir(), "unclaimed-bin")); err != nil {
		t.Errorf("an undeclared name must still get its launcher: %v", err)
	}
}

// TestPnpmLauncherYieldsToADeclaredMiseTool is the case §3.5 names by hand. `pnpm` is
// written unconditionally from a hardcoded list in GeneratePackageManagerLaunchers, so it
// is the one launcher that could shadow a mise tool without any pack being involved.
func TestPnpmLauncherYieldsToADeclaredMiseTool(t *testing.T) {
	home := t.TempDir()
	e := NewEnv(map[string]string{
		"JAIL_HOME":       home,
		"MISE_DATA_DIR":   filepath.Join(home, "mise"),
		"YOLO_MISE_TOOLS": `{"pnpm":"9"}`,
	})
	if err := GenerateAgentLaunchers(e); err != nil {
		t.Fatal(err)
	}
	if err := GeneratePackageManagerLaunchers(e); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(e.LaunchDir(), "pnpm")); !os.IsNotExist(err) {
		t.Errorf("the pnpm launcher was written over a `mise_tools`-declared pnpm (err=%v)",
			err)
	}

	// And with no declaration it is still generated — the package-manager launchers exist
	// to install pnpm lazily, and a check that suppressed them unconditionally would take
	// the feature away instead of scoping it.
	e2 := NewEnv(map[string]string{"JAIL_HOME": t.TempDir()})
	if err := GenerateAgentLaunchers(e2); err != nil {
		t.Fatal(err)
	}
	if err := GeneratePackageManagerLaunchers(e2); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(e2.LaunchDir(), "pnpm")); err != nil {
		t.Errorf("pnpm must still get its lazy installer when nothing else claims it: %v", err)
	}
}

// TestTheCollisionCheckNeverConsidersTheInstallPrefixes is trap 1, and it is the highest-
// risk item in the whole change: spelled as "is this name already resolvable on PATH?", the
// check destroys the feature it protects — after one successful install ~/.local/bin/claude
// exists, the next boot writes no launcher, PATH resolves the installed binary directly,
// and evergreen works exactly once. Green, silent, and identical to the freeze §3.5 exists
// to end.
//
// So this cell seeds a binary in EVERY install prefix and requires the launcher anyway.
func TestTheCollisionCheckNeverConsidersTheInstallPrefixes(t *testing.T) {
	home := t.TempDir()
	e := NewEnv(map[string]string{
		"JAIL_HOME":         home,
		"NPM_CONFIG_PREFIX": filepath.Join(home, ".npm-global"),
		"GOPATH":            filepath.Join(home, "go"),
		"MISE_DATA_DIR":     filepath.Join(home, "mise"),
		"YOLO_PACK_ROOT": writePackWithProgram(t, "installed",
			"already-npm", "already-local", "already-go", "already-shimmed"),
	})
	for _, seed := range []struct{ dir, bin string }{
		{e.NpmBin(), "already-npm"},
		{e.LocalBin(), "already-local"},
		{e.GoBin(), "already-go"},
		{e.MiseShims(), "already-shimmed"},
	} {
		dir, bin := seed.dir, seed.bin
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, bin), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	if err := GenerateAgentLaunchers(e); err != nil {
		t.Fatal(err)
	}
	for _, bin := range []string{"already-npm", "already-local", "already-go", "already-shimmed"} {
		if _, err := os.Stat(filepath.Join(e.LaunchDir(), bin)); err != nil {
			t.Errorf("no launcher for %q, which is already INSTALLED in a per-home prefix "+
				"(%v). The check must ignore the install prefixes: a launcher suppressed "+
				"because its own first install succeeded is evergreen working exactly "+
				"once — the freeze this design exists to end, arriving silently", bin, err)
		}
	}
}

// TestImageProbePathDropsThePerHomePrefixes pins the scope directly, which the cell above
// pins through behaviour. Two readings of one rule, because the rule is the feature.
func TestImageProbePathDropsThePerHomePrefixes(t *testing.T) {
	home := "/home/agent"
	e := NewEnv(map[string]string{
		"JAIL_HOME":         home,
		"NPM_CONFIG_PREFIX": home + "/.npm-global",
		"GOPATH":            home + "/go",
		"MISE_DATA_DIR":     "/mise",
	})
	got := imageProbePath(e)
	if got != "/bin:/usr/bin" {
		t.Errorf("imageProbePath = %q, want the image dirs alone", got)
	}

	// macos-user has no image; $YOLO_DARWIN_LOGIN_PATH is the stand-in, and the same home
	// rule has to drop the sandbox's own prefixes out of it.
	e2 := NewEnv(map[string]string{
		"JAIL_HOME": "/Users/_yolojail",
		"YOLO_DARWIN_LOGIN_PATH": "/Users/_yolojail/.yolo/bin/block:/Users/_yolojail/.local/bin:" +
			"/Users/_yolojail/.npm-global/bin:/Users/_yolojail/.local/share/mise/shims:" +
			"/Users/_yolojail/go/bin:/nix/store/abc-env/bin:/usr/bin:/bin:/usr/sbin:/sbin:" +
			"/Users/_yolojail/.yolo/bin/launch",
	})
	if got := imageProbePath(e2); got != "/nix/store/abc-env/bin:/usr/bin:/bin:/usr/sbin:/sbin" {
		t.Errorf("macos-user probe path = %q — it must keep the native store and the system "+
			"dirs and drop every per-home prefix", got)
	}
}
