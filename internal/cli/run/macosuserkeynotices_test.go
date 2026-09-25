package run

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// macosuserkeynotices_test.go pins the two stderr notices the macos-user arm owes the
// HUMAN: the platform keys (docs/design/declaration-parity.md DP-B4 / DP-L10) and the port
// keys (DP-B3 / DP-L2's second half).
//
// ⚠ EVERY CASE RUNS Run(). Both printers are pure functions of the config, so a unit test
// of either passes with its call site deleted from the arm — and a call site deleted from
// the arm is EXACTLY the defect both rows describe: the container path's own printers
// (deviceArgs, kvmArgs, the GPU line, hostForwardPorts) all exist and all sit below the
// `rt == "macos-user"` return, which is why these keys were silent rather than unhonored.
// A test that did not launch would reproduce the bug it is guarding.

// macosUserNoticeRun launches the macos-user arm with `cfg` as the workspace config and
// returns what the launch wrote to stderr.
//
// --dry-run, because it is the one thing on this arm exempt from the repo-root gate and it
// still reaches every note* printer: the plan describes the launch, so the launch's
// disclosures are part of it.
func macosUserNoticeRun(t *testing.T, cfg string) string {
	t.Helper()
	// A stub HOME, so the user config these notices read is the fixture's and not the
	// developer's own ~/.config/yolo-jail/config.jsonc — the same reason every seam in
	// dispatchOptions is a seam.
	packHome(t)
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "yolo-jail.jsonc"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, ws, "macos-user", &stdout, &stderr, nil)
	o.DryRun = true
	o.MacosUserRun = func(*jsonx.OrderedMap, string, []string, []string, string, string,
		string, macosuser.HostContext, bool, *jsonx.OrderedMap, []packload.BlockedTool) int {
		return 0
	}
	if rc := Run(*o); rc != 0 {
		t.Fatalf("Run() = %d\nstdout:\n%s\nstderr:\n%s", rc, stdout.String(), stderr.String())
	}
	return stderr.String()
}

// TestMacosUserNamesThePlatformKeysItDoesNotRead is DP-L10. All three keys were silent on
// this backend while userguide/guides/macos.md said each was "skipped with a warning" (DP-B36).
func TestMacosUserNamesThePlatformKeysItDoesNotRead(t *testing.T) {
	got := macosUserNoticeRun(t, `{
	  "devices": ["/dev/ttyUSB0", {"usb": "1234:5678", "description": "my probe"}],
	  "gpu": {"enabled": true},
	  "kvm": true
	}`)

	for _, want := range []string{
		"`devices` is not read on macos-user",
		"/dev/ttyUSB0", // the raw-path entry, named
		"my probe",     // the USB entry, by its description
		"`gpu.enabled` is not read on macos-user",
		"`kvm` is not read on macos-user",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the launch never mentioned %q:\n%s", want, got)
		}
	}
	// The REASON must be this backend's, not the container path's. "not supported on
	// macOS" is true of podman/Apple Container on a Mac — a Linux VM that cannot see a
	// host device — and false here, where there is no container at all. Asserting the
	// absence keeps a later "just reuse the existing string" from asserting the wrong
	// reason on the one backend whose reason is structural.
	if strings.Contains(got, "not supported on macOS") {
		t.Errorf("the macos-user notice borrowed the container path's platform claim:\n%s", got)
	}
}

// TestMacosUserSaysNothingAboutPlatformKeysNobodyDeclared is the control, and it is the
// difference between a disclosure and the warning OQ-BP-3 says people learn to skip: a
// config that mentions none of the three keys must produce none of the three lines.
func TestMacosUserSaysNothingAboutPlatformKeysNobodyDeclared(t *testing.T) {
	got := macosUserNoticeRun(t, `{}`)
	for _, unwanted := range []string{"`devices`", "`gpu.enabled`", "`kvm`"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("a config declaring nothing was warned about %s:\n%s", unwanted, got)
		}
	}
	// A gpu section that exists but is DISABLED is the same case: the key is present and
	// asks for nothing, so there is nothing unhonored to report.
	got = macosUserNoticeRun(t, `{"gpu": {"enabled": false, "vendor": "amd"}}`)
	if strings.Contains(got, "`gpu.enabled` is not read") {
		t.Errorf("`gpu.enabled: false` asks for no passthrough and must not be warned "+
			"about:\n%s", got)
	}
}

// TestMacosUserNamesThePortKeys is DP-L2's stderr half. Before it, the agent was told the
// network fact (backendLimits, and both port sections suppressed from the briefing) and the
// human was told nothing — the asymmetry backendlimits.go's header records.
func TestMacosUserNamesThePortKeys(t *testing.T) {
	got := macosUserNoticeRun(t, `{
	  "network": {"ports": ["3000:3000", "8000:3000"], "forward_host_ports": [5432, "8080:9090"]}
	}`)

	for _, want := range []string{
		"`network.ports` is not honored on macos-user",
		"3000:3000",
		"`network.forward_host_ports` is not honored on macos-user",
		"5432",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the launch never mentioned %q:\n%s", want, got)
		}
	}
	// §5.1.1's entry-form table: an entry whose two numbers MATCH is vacuously satisfied
	// (binding is publishing on a shared stack), and one that REMAPS is not satisfiable at
	// all. Both keys must name their remapping entries, because those are the ones whose
	// author will otherwise wait for a port that never appears.
	for _, remap := range []string{"8000:3000", "8080:9090"} {
		idx := strings.Index(got, remap)
		if idx < 0 {
			t.Fatalf("the remapping entry %q was not named:\n%s", remap, got)
		}
		if !strings.Contains(got[idx:], "REMAP") {
			t.Errorf("%q is named but not called out as a remap, which is the half that "+
				"cannot be delivered:\n%s", remap, got)
		}
	}
	// The fact the key's author most needs and the briefing cannot give them: the listing
	// confines nothing. Every port the sandbox binds is on this machine's real interfaces,
	// listed or not.
	if !strings.Contains(got, "listed here or not") {
		t.Errorf("the `ports` notice does not say that the list restricts nothing:\n%s", got)
	}
}

// TestMacosUserSaysNothingAboutPortKeysNobodyDeclared is the other control, and it is the
// exact condition §5.1.1 (2) states: non-empty only. Neither key has a default — which is
// what makes a notice safe here where a `network.mode` refusal would not be, since
// NewDefaultOptions gives every launch `Network: "bridge"`.
func TestMacosUserSaysNothingAboutPortKeysNobodyDeclared(t *testing.T) {
	for _, cfg := range []string{`{}`, `{"network": {"mode": "bridge"}}`, `{"network": {"ports": []}}`} {
		got := macosUserNoticeRun(t, cfg)
		if strings.Contains(got, "not honored on macos-user") {
			t.Errorf("config %s produced a port notice:\n%s", cfg, got)
		}
	}
}
