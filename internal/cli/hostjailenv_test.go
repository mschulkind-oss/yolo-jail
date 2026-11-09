package cli

import (
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// Exercise the real host composition, not just its env-fold callee: removing
// ScopeInput.AtHost from composeHostVars must make this fail.
func TestHostPiLeavesTerminalClipboardCopyingToTheNativeDefault(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("PI_CLIPBOARD_OSC52", "")
	t.Chdir(t.TempDir())
	userCfg(t, home, `{"packs":["pi"]}`)
	selection := loadedHostPacks(config.UserScopeConfigOrEmpty(), "pi", "")
	if err := selection.launchRefusal(); err != nil {
		t.Fatal(err)
	}
	jail := packload.EnvVarsFor(selection.packs, packload.GateSelection{}, "pi")
	if jail["PI_CLIPBOARD_OSC52"] != "1" {
		t.Fatalf("jailed pi must enable OSC 52: %v", jail)
	}
	host := hostEnvMap(t, "pi", "")
	if host["PI_CLIPBOARD_OSC52"] != "" {
		t.Fatalf("host pi unexpectedly enables OSC 52: %q", host["PI_CLIPBOARD_OSC52"])
	}
	if host["PI_TELEMETRY"] != "0" {
		t.Fatal("ordinary pi pack env must still reach the host")
	}
	// An explicit user override is still the user's choice on the host.
	t.Setenv("PI_CLIPBOARD_OSC52", "1")
	if got := hostEnvMap(t, "pi", "")["PI_CLIPBOARD_OSC52"]; got != "1" {
		t.Fatalf("host lost the user's explicit OSC 52 setting: %q", got)
	}
}

func TestHostDeliveryWithholdsJailOnlyEnv(t *testing.T) {
	c := packdecl.Contribution{Kind: packdecl.KindEnv, JailOnly: true, Vars: map[string]string{"TEST": "1"}}
	if deliveredByHostLaunch(nil, nil, c, nil) {
		t.Fatal("host delivery must not report jail-only env as delivered")
	}
	c.JailOnly = false
	if !deliveredByHostLaunch(nil, nil, c, nil) {
		t.Fatal("ordinary env must still be reported as delivered")
	}
}
