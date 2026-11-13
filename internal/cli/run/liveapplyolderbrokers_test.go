package run

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/setup"
)

// Apply compares the workspace config with what the jail was handed, and says nothing about who
// enforces it. A workspace's host brokers run the yolo that started them and reread the user
// config at every call as THAT yolo reads it; on 2026-11-09 a jail launched by an older yolo kept
// refusing the `branch:main` push grant its scope file held, while `yolo config apply` on the
// newer host yolo reported nothing to apply (applybrokerskew.go).

const olderYolo, newerYolo = "0.16.0+87.g14cc75609b4c", "0.17.1"

func wantBrokerSkewNote(t *testing.T, out, started, host string) {
	t.Helper()
	// The brokers are pinned to the yolo that started them (docs/reference/pinned-host-helpers.md
	// section 6), so the note says plainly that they run it: no "may", no "still".
	for _, want := range []string{"host brokers run yolo " + started + ", the build that started them", "this is yolo " + host, "yolo stop"} {
		if !strings.Contains(out, want) {
			t.Errorf("the apply does not say %q:\n%s", want, out)
		}
	}
	for _, hedge := range []string{"may still run", "may run", "still run it"} {
		if strings.Contains(out, hedge) {
			t.Errorf("the apply hedges with %q, though a pinned broker definitely runs the build that started it:\n%s", hedge, out)
		}
	}
}

// brokerSkewMarker is what the note always contains, and what a silent apply must not.
const brokerSkewMarker = "host brokers run yolo"

// "Nothing to apply" under brokers an older yolo started names both yolos and the restart, and
// never advises relaunching onto an older host yolo or the same one.
func TestConfigApplyNothingToApplyNamesBrokersAnOlderYoloStarted(t *testing.T) {
	o, buf := applyFixture(t, `{}`, `{}`, applyJail{running: true, env: "YOLO_VERSION=" + olderYolo + "\n"})
	t.Setenv("YOLO_VERSION", newerYolo)
	if rc := o.configApply(false); rc != 0 {
		t.Fatalf("rc = %d, want 0\n%s", rc, buf.String())
	}
	if !strings.Contains(buf.String(), "Nothing to apply") {
		t.Fatalf("no verdict:\n%s", buf.String())
	}
	wantBrokerSkewNote(t, buf.String(), olderYolo, newerYolo)

	for _, tc := range []struct{ name, jail, host string }{
		{"the same yolo", newerYolo, newerYolo},
		{"a jail newer than this yolo", newerYolo, olderYolo},
		{"a jail later on the same release", "0.16.0+87.g14cc75609b4c", "0.16.0+12.gabcdef0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o, buf := applyFixture(t, `{}`, `{}`, applyJail{running: true, env: "YOLO_VERSION=" + tc.jail + "\n"})
			t.Setenv("YOLO_VERSION", tc.host)
			if rc := o.configApply(false); rc != 0 {
				t.Fatalf("rc = %d, want 0\n%s", rc, buf.String())
			}
			if out := buf.String(); !strings.Contains(out, "Nothing to apply") || strings.Contains(out, brokerSkewMarker) {
				t.Errorf("no verdict, or a note advising a relaunch onto an older or the same yolo:\n%s", out)
			}
		})
	}
}

// A change applied now is enforced by the same brokers, so the result carries the note too.
func TestConfigApplyResultNamesBrokersAnOlderYoloStarted(t *testing.T) {
	o, buf := applyFixture(t, `{"agents_md_extra":"one"}`, `{"agents_md_extra":"two"}`,
		applyJail{running: true, env: "YOLO_VERSION=" + olderYolo + "\n"})
	t.Setenv("YOLO_VERSION", newerYolo)
	answer(o, "y")
	if rc := o.configApply(false); rc != 0 {
		t.Fatalf("rc = %d, want 0\n%s", rc, buf.String())
	}
	if !strings.Contains(buf.String(), "Config apply result:") {
		t.Fatalf("nothing was applied:\n%s", buf.String())
	}
	wantBrokerSkewNote(t, buf.String(), olderYolo, newerYolo)
}

// macos-user has no container whose environment names its launcher: its host brokers are held by
// the workspace's keyed keeper, whose live start record names the keeper's build (parity). The
// note reads from that record, and is silent with no live keeper.
func TestConfigApplyOnMacosUserNamesAKeeperAnOlderYoloStarted(t *testing.T) {
	o, buf := applyFixture(t, `{}`, `{}`, applyJail{})
	o.Setup = setup.MustBuiltIn("seatbelt-self")
	t.Setenv("YOLO_VERSION", newerYolo)
	if rc := o.configApply(false); rc != 0 {
		t.Fatalf("rc = %d, want 0\n%s", rc, buf.String())
	}
	if out := buf.String(); strings.Contains(out, brokerSkewMarker) {
		t.Fatalf("a note with no keeper running:\n%s", out)
	}

	buf.Reset()
	writeCurrentKeeperRecord(t, keeperKey(o.jailName(), keeperNotchMacosUser),
		keeperRecord{Contract: keeperRosterContract, Notch: keeperNotchMacosUser, Build: olderYolo + "@14cc75609b4c", Ready: true})
	if rc := o.configApply(false); rc != 0 {
		t.Fatalf("rc = %d, want 0\n%s", rc, buf.String())
	}
	wantBrokerSkewNote(t, buf.String(), olderYolo, newerYolo)
}
