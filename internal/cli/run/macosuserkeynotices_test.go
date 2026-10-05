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
		macosuser.HomeOverlay, macosuser.HostContext, bool, *jsonx.OrderedMap, []packload.BlockedTool, macosuser.JailDaemons) int {
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
	  "devices": ["/dev/ttyUSB0", {"usb": "1234:5678", "description": "my probe"},
	              {"cgroup_rule": "c 188:* rwm"}],
	  "gpu": {"enabled": true},
	  "kvm": true
	}`)

	for _, want := range []string{
		"`devices` USB and cgroup entries are not read on macos-user",
		"allows device control (ioctl) on /dev/ttyUSB0", // the raw-path entry, carved out
		"my probe",                // the USB entry, by its description
		"cgroup rule c 188:* rwm", // the cgroup entry, by its rule
		// Each form's own container mechanism: a USB entry attaches a device, and a cgroup
		// rule attaches nothing — it only lets a container's device cgroup open matching
		// device numbers (assembleRunCmd renders it as --device-cgroup-rule).
		"A USB entry attaches a device to a CONTAINER",
		"a cgroup rule lets a container's device cgroup open the device numbers it matches",
		"`gpu.enabled` is not read on macos-user",
		"`kvm` is not read on macos-user",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the launch never mentioned %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "Both attach a device") {
		t.Errorf("the notice says a cgroup rule attaches a device, which it never does:\n%s", got)
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
	for _, unwanted := range []string{"`devices`", "devices:", "`gpu.enabled`", "`kvm`",
		"ephemeral_storage", "nvim"} {
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
// human was told nothing — the asymmetry backendlimits.go's header records. Since 2026-10-05 the
// launch also RELAYS each remap (macosuserportrelay.go), and the notice says so from the same plan.
func TestMacosUserNamesThePortKeys(t *testing.T) {
	got := macosUserNoticeRun(t, `{
	  "network": {"ports": ["3000:3000", "8000:3000", "8001:3001/tcp", "8002:3002/udp"],
	              "forward_host_ports": [5432, "8080:9090"]}
	}`)

	for _, want := range []string{
		"`network.ports` confines nothing on macos-user",
		"3000:3000",
		"`network.forward_host_ports` on macos-user",
		"5432",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the launch never mentioned %q:\n%s", want, got)
		}
	}
	// §5.1.1's entry-form table, corrected: an entry whose two numbers MATCH is vacuously
	// satisfied (binding is publishing on a shared stack), and a TCP remap is RELAYED — named as
	// such, a protocol suffix included, which the old classifier dropped ("8001:3001/tcp" was
	// not named at all). A UDP remap is named as not relayed, with why and the next step.
	for _, want := range []string{
		"8000:3000, 8001:3001/tcp are port REMAPs, which this launch relays from outside the sandbox (TCP).",
		"8080:9090 is a port REMAP, which this launch relays from outside the sandbox (TCP).",
		"8002:3002/udp is a port REMAP this launch does not relay: the relay carries TCP only",
		"`runtime: \"podman\"`",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the notice lacks %q:\n%s", want, got)
		}
	}
	// The fact the key's author most needs and the briefing cannot give them: the listing
	// confines nothing. Every port the sandbox binds is on this machine's real interfaces,
	// listed or not — relay or no relay.
	if !strings.Contains(got, "listed here or not") {
		t.Errorf("the `ports` notice does not say that the list restricts nothing:\n%s", got)
	}
	// A forward list whose every remap is relayed leaves nothing undone, so it is a disclosure,
	// not a warning; the same list under `network.mode: "host"` is a warning naming the mode.
	if strings.Contains(got, "Warning: `network.forward_host_ports`") {
		t.Errorf("a fully delivered forward list was warned about:\n%s", got)
	}
	got = macosUserNoticeRun(t, `{"network": {"mode": "host", "forward_host_ports": ["8080:9090"]}}`)
	if !strings.Contains(got, "Warning: `network.forward_host_ports` is not fully delivered on macos-user") ||
		!strings.Contains(got, "8080:9090 is a port REMAP this launch does not relay: `network.mode: \"host\"` "+
			"drops both port keys, as it does on podman; remove it to have the remap relayed.") {
		t.Errorf("a host-mode forward remap was not warned about with its reason:\n%s", got)
	}
}

// TestMacosUserSaysNothingAboutPortKeysNobodyDeclared is the other control, and it is the
// exact condition §5.1.1 (2) states: non-empty only. Neither key has a default — which is
// what makes a notice safe here where a `network.mode` refusal would not be, since
// resolveNetMode gives every launch that names no mode "bridge".
func TestMacosUserSaysNothingAboutPortKeysNobodyDeclared(t *testing.T) {
	for _, cfg := range []string{`{}`, `{"network": {"mode": "bridge"}}`, `{"network": {"ports": []}}`} {
		got := macosUserNoticeRun(t, cfg)
		for _, unwanted := range []string{"`network.ports`", "`network.forward_host_ports`", "relay"} {
			if strings.Contains(got, unwanted) {
				t.Errorf("config %s produced a port line (%q):\n%s", cfg, unwanted, got)
			}
		}
	}
}

// A raw-path `devices` entry is now half READ: the profile re-allows its ioctls, and the launch
// says so — a disclosure, because it widens the sandbox — while an entry the classifier refuses
// is warned with the step that fixes it, and the USB form is still read by nothing. One
// classifier for the profile and this line (macosuser.DeviceIoctlPaths).
func TestMacosUserDisclosesTheDeviceCarveOut(t *testing.T) {
	got := macosUserNoticeRun(t, `{"devices": ["/dev/cu.usbserial-A1", "/dev/disk4", "/dev/cu.usbserial-A1"]}`)
	if !strings.Contains(got, "devices: the sandbox allows device control (ioctl) on /dev/cu.usbserial-A1. "+
		"The node opens under ordinary macOS permissions; nothing is attached.") {
		t.Errorf("the ioctl carve-out was not disclosed, or names the entry twice:\n%s", got)
	}
	if !strings.Contains(got, "`devices` entry /dev/disk4 is skipped on macos-user") ||
		!strings.Contains(got, "raw disks and packet capture stay denied") {
		t.Errorf("a raw disk entry was not refused by name with its reason:\n%s", got)
	}
	if strings.Contains(got, "USB and cgroup entries") {
		t.Errorf("a config with no USB or cgroup entry was told about them:\n%s", got)
	}
	got = macosUserNoticeRun(t, `{"devices": ["/Users/someone/notes.txt"]}`)
	if !strings.Contains(got, "is not a device node under /dev") || !strings.Contains(got, "`ls /dev/cu.*`") {
		t.Errorf("a non-device entry was not refused with the step that finds the node:\n%s", got)
	}
	if strings.Contains(got, "allows device control") {
		t.Errorf("a refused entry was disclosed as carved out:\n%s", got)
	}
}

// TestMacosUserNamesTheTmpfsScratchChoice is DP-B5: `ephemeral_storage: "tmpfs"` asks for
// RAM-backed scratch, which only a container mount gives, and this backend read the key nowhere
// and said nothing. "volume" and an absent key ask for what this backend already is, on disk.
func TestMacosUserNamesTheTmpfsScratchChoice(t *testing.T) {
	got := macosUserNoticeRun(t, `{"ephemeral_storage": "tmpfs"}`)
	for _, want := range []string{"`ephemeral_storage: \"tmpfs\"` is not read on macos-user",
		"/var/folders, on disk", "Remove the key, or use Apple Container"} {
		if !strings.Contains(got, want) {
			t.Errorf("a tmpfs request on macos-user lacks %q:\n%s", want, got)
		}
	}
	for _, cfg := range []string{`{}`, `{"ephemeral_storage": "volume"}`} {
		if got := macosUserNoticeRun(t, cfg); strings.Contains(got, "ephemeral_storage") {
			t.Errorf("%s asks for nothing this backend lacks, and was warned:\n%s", cfg, got)
		}
	}
}

// The host nvim config is delivered on every container launch and on none here; with one on
// the host, the launch says so once (a disclosure: nothing declares it, so nothing refuses).
func TestMacosUserDisclosesTheUndeliveredNvimConfig(t *testing.T) {
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "yolo-jail.jsonc"), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	run := func(withNvim bool) string {
		home := packHome(t)
		if withNvim {
			if err := os.MkdirAll(filepath.Join(home, ".config", "nvim"), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		var stdout, stderr bytes.Buffer
		o := dispatchOptions(t, ws, "macos-user", &stdout, &stderr, nil)
		o.DryRun = true
		o.MacosUserRun = func(*jsonx.OrderedMap, string, []string, []string, string, string,
			macosuser.HomeOverlay, macosuser.HostContext, bool, *jsonx.OrderedMap, []packload.BlockedTool, macosuser.JailDaemons) int {
			return 0
		}
		if rc := Run(*o); rc != 0 {
			t.Fatalf("Run() = %d\nstderr:\n%s", rc, stderr.String())
		}
		return stderr.String()
	}
	got := run(true)
	if n := strings.Count(got, "Host nvim config (~/.config/nvim) is not delivered on macos-user"); n != 1 {
		t.Errorf("the nvim disclosure appears %d times, want once:\n%s", n, got)
	}
	if !strings.Contains(got, "OQ-ED2") {
		t.Errorf("the nvim line does not name what delivery waits on:\n%s", got)
	}
	if got := run(false); strings.Contains(got, "nvim") {
		t.Errorf("a host with no nvim config was told about one:\n%s", got)
	}
}
