package macosuser

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// A declared serial port gets its control calls back, AFTER the deny it overrides, and nothing
// else: no read or write allow names it, and a repeated entry renders once.
func TestSeatbeltReallowsIoctlOnDeclaredDevices(t *testing.T) {
	p := SeatbeltProfileWithContext("/Users/Shared/proj", "", nil, HomeReadonly{}, nil,
		[]string{"/dev/cu.usbserial-1410", "/dev/cu.usbserial-1410"}, "off")
	want := "(allow file-ioctl\n    (literal \"/dev/cu.usbserial-1410\"))"
	if !strings.Contains(p, want) {
		t.Fatalf("no device ioctl allow\n%s", p)
	}
	if !strings.Contains(p, "#seatbelt-test-id:device-ioctl-allow#") {
		t.Errorf("the device allow carries no test id, so its proof cannot be found\n%s", p)
	}
	if n := strings.Count(p, `(literal "/dev/cu.usbserial-1410")`); n != 1 {
		t.Errorf("a duplicated entry is emitted %d times\n%s", n, p)
	}
	mustPrecede(t, p, "(deny file-ioctl)", want, "the device allow is inert above the ioctl deny")
	for _, bad := range []string{"(allow file-read* (literal \"/dev/cu", "(allow file-write* (literal \"/dev/cu",
		"(allow file-read*\n    (literal \"/dev/cu", "(allow file-write*\n    (literal \"/dev/cu"} {
		if strings.Contains(p, bad) {
			t.Errorf("the device carve-out widened reads or writes: %q\n%s", bad, p)
		}
	}
	// Nothing follows it: the allow is the profile's last rule, so no later deny can make it
	// inert and it cannot sit above anything it would have to beat.
	if !strings.HasSuffix(p, want+"\n") {
		t.Errorf("the device allow is not the profile's last rule\n%s", p)
	}
}

// No devices, no block: the profile is byte-identical to the one this backend always emitted.
func TestSeatbeltWithoutDevicesIsUnchanged(t *testing.T) {
	a := SeatbeltProfile("/Users/Shared/proj", "", []string{"vendored"}, HomeReadonly{})
	for _, devs := range [][]string{nil, {}, {"/Users/matt/.ssh/id_ed25519"}} {
		b := SeatbeltProfileWithContext("/Users/Shared/proj", "", []string{"vendored"}, HomeReadonly{}, nil, devs, "off")
		if a != b || strings.Contains(b, "device-ioctl-allow") {
			t.Errorf("devices %q changed the profile of a launch that admits no device", devs)
		}
	}
}

// The classifier is the profile's and the notice's: a non-device path and a raw disk or bpf
// node are refused with a reason and a next step, and never reach the profile.
func TestDeviceIoctlPathsRefusesNonDevicesAndRawDisks(t *testing.T) {
	allowed, refused := DeviceIoctlPaths([]string{
		"/dev/cu.usbmodem101", "/Users/matt/.ssh/id_ed25519", "/dev/disk4", "/dev/rdisk4",
		"/private/dev/disk2", "/dev/bpf0", "relative", "/dev", " ", "/dev/cu.usbmodem101",
	})
	if len(allowed) != 1 || allowed[0] != "/dev/cu.usbmodem101" {
		t.Errorf("allowed = %v, want only /dev/cu.usbmodem101", allowed)
	}
	refusedEntries := map[string]DeviceRefusal{}
	for _, r := range refused {
		refusedEntries[r.Entry] = r
		if r.Reason == "" || r.Next == "" {
			t.Errorf("refusal %+v names no reason or no next step", r)
		}
	}
	for _, e := range []string{"/Users/matt/.ssh/id_ed25519", "/dev/disk4", "/dev/rdisk4",
		"/private/dev/disk2", "/dev/bpf0", "relative", "/dev"} {
		if _, ok := refusedEntries[e]; !ok {
			t.Errorf("%q was not refused (refused: %v)", e, refused)
		}
	}
	if r := refusedEntries["/dev/rdisk4"]; !strings.Contains(r.Reason, "raw disks") {
		t.Errorf("a raw disk's refusal does not say why: %+v", r)
	}
	if r := refusedEntries["/Users/matt/.ssh/id_ed25519"]; !strings.Contains(r.Next, "ls /dev/cu.*") {
		t.Errorf("a non-device entry's refusal does not say how to find the node: %+v", r)
	}
	p := SeatbeltProfileWithContext("/Users/Shared/proj", "", nil, HomeReadonly{}, nil,
		[]string{"/Users/matt/.ssh/id_ed25519", "/dev/disk4", "/dev/bpf0"}, "off")
	if strings.Contains(p, "device-ioctl-allow") || strings.Contains(p, "/Users/matt") ||
		strings.Contains(p, `(literal "/dev/disk4")`) {
		t.Errorf("a refused entry reached the profile\n%s", p)
	}
}

// THE CALL SITE: the plan a launch installs carries the config's raw-path devices, and ignores
// the USB form, which names no node. Fails if BuildRunPlanWithDaemons stops handing `devices`
// to the generator.
func TestBuildRunPlanCarriesDeclaredDevicesIntoTheProfile(t *testing.T) {
	usb := jsonx.NewOrderedMap()
	usb.Set("usb", "1234:5678")
	cfg := jsonx.NewOrderedMap()
	cfg.Set("devices", []any{"/dev/cu.usbserial-A1", usb})
	plan := BuildRunPlan("/Users/Shared/proj", cfg, nil, []string{"bash"}, "/usr/local/bin/yolo", "",
		HomeOverlay{}, HostContext{}, jsonx.NewOrderedMap(), nil, nil)
	if !strings.Contains(plan.Seatbelt, "(allow file-ioctl\n    (literal \"/dev/cu.usbserial-A1\"))") {
		t.Errorf("the launch's profile does not carry the declared device\n%s", plan.Seatbelt)
	}
	if strings.Contains(plan.Seatbelt, "1234") {
		t.Errorf("a USB entry reached the profile\n%s", plan.Seatbelt)
	}
}
