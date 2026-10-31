package patchrecovery

import "testing"

// TestAScopeNamesASlashedPackWhole: a readable-config pack's name is a source handle holding a "/"
// (OQ-RC15), so its key holds two. A scope entry matches a key, a pack or a name exactly, so the
// handle covers its pack, the full key its one build, and the handle's first segment nothing.
func TestAScopeNamesASlashedPackWhole(t *testing.T) {
	sub := Subject{Key: "yolo-packs/pi-fork/pi", Pack: "yolo-packs/pi-fork", Name: "pi"}
	for value, want := range map[string]bool{
		"yolo-packs/pi-fork":    true,
		"yolo-packs/pi-fork/pi": true,
		"yolo-packs":            false,
		"pi-fork/pi":            false,
	} {
		if got := Parse(value).Covers(sub); got != want {
			t.Errorf("Parse(%q).Covers(%s) = %v, want %v", value, sub.Key, got, want)
		}
	}
}
