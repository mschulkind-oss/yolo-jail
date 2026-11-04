package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/setup"
)

// macosModeLaunch runs `yolo -- true` with userCfg as the user config, captures the run.Options
// the front door hands the pipeline, fills its SetupRef with setupName as the launch's setup gate
// does (run.Options.resolveSetup; its own tests pin that), then invokes the macos-user handler it
// found with the backend stubbed, and returns both Options and the handler's exit code. The
// handler is the REAL closure the run command assembled, so deleting the mode's wiring from it
// fails a test.
func macosModeLaunch(t *testing.T, userCfg, setupName string) (run.Options, macosuser.Options, bool, int) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("USER", "you")
	if userCfg != "" {
		p := paths.UserConfigPath()
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(userCfg), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var seen run.Options
	prevPipeline := launchRunPipeline
	launchRunPipeline = func(o run.Options) int {
		if o.SetupRef == nil {
			t.Fatal("the front door handed the pipeline no SetupRef for the gate to fill")
		}
		o.SetupRef.Setup = setup.MustBuiltIn(setupName)
		seen = o
		return 0
	}
	t.Cleanup(func() { launchRunPipeline = prevPipeline })
	var backend macosuser.Options
	called := false
	prevBackend := runMacosUserBackend
	runMacosUserBackend = func(_ macosuser.Deps, o macosuser.Options) int { backend, called = o, true; return 7 }
	t.Cleanup(func() { runMacosUserBackend = prevBackend })

	captureBoth(t, func() { _ = Main([]string{"yolo", "--", "true"}) })
	if seen.MacosUserRun == nil {
		t.Fatal("the front door handed the pipeline no macos-user handler")
	}
	var rc int
	captureBoth(t, func() {
		rc = seen.MacosUserRun(jsonx.NewOrderedMap(), t.TempDir(), nil, []string{"true"}, "", "",
			macosuser.HomeOverlay{}, macosuser.HostContext{}, false, jsonx.NewOrderedMap(), nil,
			macosuser.JailDaemons{})
	})
	return seen, backend, called, rc
}

// TestMacosLaunchPassesSelfModeToTheBackend: the seatbelt-self setup runs the sandbox as you
// (named-setups.md; the macos-user mode is the setup's run_as), and the backend reads it off the
// SetupRef the gate fills, with the invoking user's home and name. Fails if the handler stops
// reading the setup.
func TestMacosLaunchPassesSelfModeToTheBackend(t *testing.T) {
	seen, backend, called, rc := macosModeLaunch(t, "", "seatbelt-self")
	if !called || rc != 7 {
		t.Fatalf("the handler did not reach the backend (called %v, rc %d)", called, rc)
	}
	if backend.Mode != macosuser.ModeSelf {
		t.Errorf("mode: backend %v; want self, the setup's run_as", backend.Mode)
	}
	home, _ := filepath.EvalSymlinks(os.Getenv("HOME"))
	if backend.HostHome != home || seen.MacosUserHostHome != home || backend.HostUser != "you" {
		t.Errorf("identity: backend %q/%q, pipeline %q; want %q/you", backend.HostHome, backend.HostUser,
			seen.MacosUserHostHome, home)
	}
}

// TestMacosLaunchPassesTheSharedTmpHatch: `"macos_user_shared_tmp": true` in the user config
// reaches both readers from one read (OQ-MM8, MMP-D18): the briefing (run.Options) and the
// profile (macosuser.Options). Absent, both read false. Fails if either half of the wiring is
// deleted.
func TestMacosLaunchPassesTheSharedTmpHatch(t *testing.T) {
	seen, backend, called, _ := macosModeLaunch(t, `{"macos_user_shared_tmp": true}`, "seatbelt-self")
	if !called || !backend.SharedTmp || !seen.MacosUserSharedTmp {
		t.Errorf("hatch set: called %v, backend %v, pipeline %v; want true for both", called,
			backend.SharedTmp, seen.MacosUserSharedTmp)
	}
	seen, backend, _, _ = macosModeLaunch(t, "", "seatbelt-self")
	if backend.SharedTmp || seen.MacosUserSharedTmp {
		t.Errorf("hatch unset: backend %v, pipeline %v; want false", backend.SharedTmp, seen.MacosUserSharedTmp)
	}
}

// TestMacosLaunchRunsTheYoloAccountSetupAsTheAccount: seatbelt-yolo-account is the second
// account, whatever the user config says, since the mode is the setup's run_as.
func TestMacosLaunchRunsTheYoloAccountSetupAsTheAccount(t *testing.T) {
	for _, cfg := range []string{"", `{"macos_user_mode": "self"}`} {
		_, backend, called, _ := macosModeLaunch(t, cfg, "seatbelt-yolo-account")
		if !called || backend.Mode != macosuser.ModeAccount {
			t.Errorf("config %q: called %v, backend %v; want account", cfg, called, backend.Mode)
		}
	}
}

// TestMacosLaunchRefusesAnUnknownMode: a value that slipped past validation never silently picks
// a mode; the line names the key and the next step.
func TestMacosLaunchRefusesAnUnknownMode(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("YOLO_VERSION", "")
	p := paths.UserConfigPath()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(`{"macos_user_mode": "jail"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	mode, refusal := macosUserLaunchMode()
	if mode != macosuser.ModeAccount || !strings.Contains(refusal, "macos_user_mode") ||
		!strings.Contains(refusal, `"self" or "account"`) {
		t.Errorf("macosUserLaunchMode() = %v, %q", mode, refusal)
	}
}

// TestPruneHandsTheGuestPackTreeSweepTheUserMode: `yolo prune` in the run-as-you mode looks for
// the sandbox among your own processes, not the second account's, so on a Mac without the
// account it can still remove a tree. Fails if guestPackTreeSweep stops reading the mode.
func TestPruneHandsTheGuestPackTreeSweepTheUserMode(t *testing.T) {
	for cfg, want := range map[string]macosuser.Mode{
		`{"macos_user_mode": "self"}`: macosuser.ModeSelf,
		``:                            macosuser.ModeAccount,
	} {
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
		t.Setenv("YOLO_VERSION", "")
		if cfg != "" {
			p := paths.UserConfigPath()
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte(cfg), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		var got macosuser.Mode = -1
		prev := prunePackTrees
		prunePackTrees = func(m macosuser.Mode, apply bool) macosuser.PackTreeReap {
			got = m
			return macosuser.PackTreeReap{}
		}
		guestPackTreeSweep(false)
		prunePackTrees = prev
		if got != want {
			t.Errorf("config %q: the sweep was handed mode %v, want %v", cfg, got, want)
		}
	}
}

// TestMacosLaunchPassesShareHostFontsToTheBackend: the user-scope `share_host_fonts` opt-in
// (OQ-NX9) reaches the backend, which grants the separate account the user's font folder only
// with it. Fails if the front door stops reading it.
func TestMacosLaunchPassesShareHostFontsToTheBackend(t *testing.T) {
	_, backend, called, _ := macosModeLaunch(t, `{"share_host_fonts": true}`, "seatbelt-yolo-account")
	if !called || !backend.ShareHostFonts {
		t.Errorf("share_host_fonts true: called %v, backend.ShareHostFonts %v; want true", called,
			backend.ShareHostFonts)
	}
	_, backend, _, _ = macosModeLaunch(t, "", "seatbelt-yolo-account")
	if backend.ShareHostFonts {
		t.Error("no share_host_fonts in the user config, and the backend was told to share fonts")
	}
}
