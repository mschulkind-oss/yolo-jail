package loopholes

// §3.1's by-name UNSUPPORTED-HERE report, and the message shape it shares with §8's
// inert-on-backend one.
//
// The declaration itself (loopholedecl/platforms.go) and its evaluation
// (SupportedHere) landed already; what these pin is the REPORT — that a selected
// pack's unsupported loophole is named once, with the platforms it does support and
// the sentence saying nothing is missing, through the one renderer both axes use.

import (
	"runtime"
	"strings"
	"testing"
)

// The motivating case: a loophole this machine does not support is reported BY NAME,
// once, with the platforms it DOES support — and the line must say nothing is
// missing, or the reader spends the afternoon looking for something to install.
func TestPlatformInertNotesReportsUnsupportedByName(t *testing.T) {
	other := "linux"
	if runtime.GOOS == "linux" {
		other = "darwin"
	}
	lp := loadWithPlatforms(t, "nativd", []any{other})
	notes := PlatformInertNotes([]*Loophole{lp})
	if len(notes) != 1 {
		t.Fatalf("notes = %+v, want one", notes)
	}
	if notes[0].Name != "nativd" || notes[0].Axis != AxisPlatform {
		t.Errorf("note = %+v, want name=nativd axis=%s", notes[0], AxisPlatform)
	}
	line := notes[0].Line()
	for _, want := range []string{
		"loophole nativd is", "unsupported on " + runtime.GOOS + "/" + runtime.GOARCH,
		"declares support for " + other, "Nothing is missing on this machine",
	} {
		if !strings.Contains(line, want) {
			t.Errorf("line does not carry %q:\n  %s", want, line)
		}
	}
}

// ONCE per loophole. Discovery merges four sources and a launch reads the list from
// several places; a duplicated line reads as two problems.
func TestPlatformInertNotesReportsEachNameOnce(t *testing.T) {
	other := "linux"
	if runtime.GOOS == "linux" {
		other = "darwin"
	}
	lp := loadWithPlatforms(t, "dupd", []any{other})
	if notes := PlatformInertNotes([]*Loophole{lp, lp, lp}); len(notes) != 1 {
		t.Fatalf("notes = %+v, want one per name", notes)
	}
}

// A supported loophole, and a DISABLED one, produce nothing: the first is not inert,
// and the second is inert for a reason the user chose and already hears about.
func TestPlatformInertNotesStaysQuietWhereItShould(t *testing.T) {
	supported := loadWithPlatforms(t, "here", []any{runtime.GOOS})
	none := loadWithPlatforms(t, "anywhere", nil)
	other := "linux"
	if runtime.GOOS == "linux" {
		other = "darwin"
	}
	off := loadWithPlatforms(t, "offd", []any{other})
	off.Enabled = false
	if notes := PlatformInertNotes([]*Loophole{supported, none, off, nil}); len(notes) != 0 {
		t.Errorf("notes = %+v, want none", notes)
	}
}

// The BACKEND axis renders through the SAME Line(), which is the whole point of §8's
// "one mechanism, one message": platform and backend are two axes with one answer
// shape, and two half-messages for one situation is the B-0 shape.
func TestInertNoteRendersBothAxesIdentically(t *testing.T) {
	platform := InertNote{Name: "acme", Axis: AxisPlatform,
		Reason: "unsupported on darwin/arm64 — it declares support for linux"}
	backend := InertNote{Name: "acme", Axis: AxisBackend,
		Reason: "inert on the 'container' backend, which skips every loophole"}
	for _, n := range []InertNote{platform, backend} {
		if !strings.HasPrefix(n.Line(), "loophole acme is ") {
			t.Errorf("axis %s renders differently: %q", n.Axis, n.Line())
		}
	}
}
