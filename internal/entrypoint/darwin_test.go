package entrypoint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/setupcensus"
)

// TestWriteLoginRC re-prepends the PATH in all three login rc files — from the ENVIRONMENT,
// which is the half that matters.
//
// These three files sit at the root of a home every workspace on the machine shares (the
// home-tier layout leaves $HOME shared on purpose), so a baked PATH is one workspace's
// `packages:` store dirs in the next workspace's login shell — the same cross-workspace
// race the sidecar closed for briefings, in files nobody would think to look at. So the
// assertion is BOTH: the re-prepend happens, and no store path is written down.
func TestWriteLoginRCReadsThePathRatherThanBakingIt(t *testing.T) {
	home := t.TempDir()
	e := NewEnv(map[string]string{
		"HOME": home,
		// Set on the Env the way a launch sets it, to prove the writer does not consult it.
		DarwinLoginPathEnv: "/Users/dev/.yolo/bin/block:/nix/store/this-workspace-only/bin",
	})
	if err := WriteLoginRC(e); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{".zprofile", ".zshrc", ".bash_profile"} {
		got := string(mustRead(t, filepath.Join(home, name)))
		if !strings.Contains(got, `export PATH="$`+DarwinLoginPathEnv+`:$PATH"`) {
			t.Errorf("%s does not re-prepend from $%s:\n%s", name, DarwinLoginPathEnv, got)
		}
		if strings.Contains(got, "/nix/store/") {
			t.Errorf("%s bakes one workspace's store paths into a shared home:\n%s", name, got)
		}
	}
}

// TestRunDarwinBootstrapGeneratesConfig: the darwin entry runs the shared
// generators against a native home + writes the two macOS pieces, without the
// Linux-only boot steps. Uses a minimal env (no agents) so it exercises the
// generator sequence + the two writers end to end.
func TestRunDarwinBootstrapGeneratesConfig(t *testing.T) {
	home := t.TempDir()
	e := NewEnv(map[string]string{
		"HOME":              home,
		"YOLO_BLOCK_CONFIG": `[{"name":"grep","block_flags":["-r"],"message":"no","suggestion":"rg"}]`,
	})
	e.Workspace = "/Users/dev/proj"
	e.ShimBinDir = "/usr/bin"
	// A wrapper an older bootstrap wrote, which would shadow the macos-log bridge's client.
	stale := filepath.Join(home, ".local", "bin", "yolo-log")
	if err := os.MkdirAll(filepath.Dir(stale), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stale, []byte("#!/bin/bash\nset -euo pipefail\nexec /usr/bin/log \"$@\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	RunDarwinBootstrap(e, DarwinBootstrapOptions{})

	// Shim generated, exec'ing the macOS /usr/bin path.
	shim := string(mustRead(t, filepath.Join(home, ".yolo/bin/block", "grep")))
	if !strings.Contains(shim, "/usr/bin/grep") {
		t.Errorf("darwin shim should exec /usr/bin/grep:\n%s", shim)
	}
	// The retired in-sandbox yolo-log wrapper is gone (retire_yolo_log).
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("the retired yolo-log wrapper survived the bootstrap (stat: %v)", err)
	}
	// Login rc written, re-prepending the launch's PATH after path_helper.
	rc := string(mustRead(t, filepath.Join(home, ".zprofile")))
	if !strings.Contains(rc, "$"+DarwinLoginPathEnv) {
		t.Errorf(".zprofile does not restore the sandbox PATH:\n%s", rc)
	}
}

// The container MCP preset wrappers are Linux-absolute — /usr/bin/chromium,
// `exec /bin/node`, /etc/fonts/fonts.conf — and this backend bakes no image, so on
// macOS all three paths are absent. Generating them put three executables in the
// sandbox home that fail the moment anything execs one.
//
// Open Decision #4 is resolved by SKIPPING them and saying so. This pins both
// halves: no wrapper file appears, and a config that asked for presets is told — by name.
// The Env is the production translation (DarwinEnvFrom), which is what sets SkipMCPPresets.
func TestDarwinBootstrapSkipsLinuxMCPWrappers(t *testing.T) {
	home := t.TempDir()
	var warnings strings.Builder
	e := DarwinEnvFrom(map[string]string{
		"JAIL_HOME":             home,
		"YOLO_MCP_PRESETS":      `["chrome-devtools"]`,
		"YOLO_DARWIN_WORKSPACE": t.TempDir(),
	}, home)
	e.Stderr = &warnings

	_ = RunDarwinBootstrap(e, DarwinBootstrapOptions{})

	// The wrapper the container path writes must not exist here.
	if _, err := os.Stat(filepath.Join(e.LocalBin(), "chrome-devtools-mcp-wrapper")); err == nil {
		t.Error("generated the chrome-devtools wrapper on macos-user — its body execs " +
			"/usr/bin/chromium, which this backend never provisions")
	}
	// And the skip must be reported: an agent told an MCP server exists, whose wrapper
	// is silently absent, is the same lie in the other direction. In the setup census's words,
	// naming the preset: the census cell that marks mcp_presets Warned here is the line's source
	// (internal/setupcensus), so a boot that printed words of its own fails as surely as one that
	// printed none. The line says the preset was left out of every agent's config too
	// (Env.SkipMCPPresets), by name.
	want := setupcensus.Warning(setupcensus.MacosUser, "mcp_presets").Plain("chrome-devtools")
	if !strings.Contains(warnings.String(), want) {
		t.Errorf("skipped the wrappers without the census's line %q:\n%s", want, warnings.String())
	}
	if !strings.Contains(want, "chrome-devtools. Left out of every agent's MCP config") {
		t.Errorf("the census's mcp_presets line %q does not say the preset was left out of every "+
			"agent's config", want)
	}
	if n := setupcensus.Warning(setupcensus.MacosUser, "mcp_presets"); n.By != "entrypoint.mcp_presets_declined" {
		t.Errorf("the census names %q as the printer of the mcp_presets notice, and this boot step "+
			"prints it", n.By)
	}
}

// No presets configured → no notice. A warning that fires when nothing was asked for
// is the noise that trains people to skip the line that matters.
func TestDarwinBootstrapSilentAboutMCPWhenNonePresetsAsked(t *testing.T) {
	var warnings strings.Builder
	home := t.TempDir()
	e := DarwinEnvFrom(map[string]string{"JAIL_HOME": home, "YOLO_DARWIN_WORKSPACE": t.TempDir()}, home)
	e.Stderr = &warnings

	_ = RunDarwinBootstrap(e, DarwinBootstrapOptions{})

	if strings.Contains(warnings.String(), "mcp_presets") {
		t.Errorf("warned about mcp_presets when none were configured:\n%s", warnings.String())
	}
}

// RetireYoloLog removes only what the retired generator wrote — each of its three bodies —
// and keeps, and names, a file of the user's at the same path.
func TestRetireYoloLogRemovesOnlyTheRetiredWrapper(t *testing.T) {
	head := "#!/bin/bash\nset -euo pipefail\n"
	for _, tc := range []struct {
		body   string
		remove bool
	}{
		{head + "echo \"yolo-log: macOS log access is disabled.\" >&2\nexit 1\n", true},
		{head + "exec /usr/bin/log \"$@\"\n", true},
		{head + "if [ \"$#\" -eq 0 ]; then\n  exec /usr/bin/log show --last 5m --style compact\nfi\n", true},
		{"#!/bin/sh\nmy own log helper\n", false},
	} {
		home := t.TempDir()
		p := filepath.Join(home, ".local", "bin", "yolo-log")
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(tc.body), 0o755); err != nil {
			t.Fatal(err)
		}
		e := NewEnv(map[string]string{"HOME": home})
		var warn strings.Builder
		e.Stderr = &warn
		if err := RetireYoloLog(e); err != nil {
			t.Fatal(err)
		}
		_, err := os.Stat(p)
		if gone := os.IsNotExist(err); gone != tc.remove {
			t.Errorf("body %q: removed=%v, want %v", tc.body, gone, tc.remove)
		}
		if named := strings.Contains(warn.String(), "rename or delete it"); named == tc.remove {
			t.Errorf("body %q: warning %q (a kept file must be named with the fix, a removed one not)", tc.body, warn.String())
		}
	}
	// Nothing there is nothing to do.
	if err := RetireYoloLog(NewEnv(map[string]string{"HOME": t.TempDir()})); err != nil {
		t.Error(err)
	}
}
