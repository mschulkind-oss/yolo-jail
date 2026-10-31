package slug

import "testing"

func TestEscapeMakesOneComponentAndRoundTrips(t *testing.T) {
	for in, want := range map[string]string{
		"pi":                 "pi",
		"house_rules":        "house_5frules",
		"yolo-packs/pi-fork": "yolo-packs_2fpi-fork",
		"local/matt":         "local_2fmatt",
		".config/x y":        ".config_2fx_20y",
	} {
		if got := Escape(in); got != want {
			t.Errorf("Escape(%q) = %q, want %q", in, got, want)
		}
		if back, ok := Unescape(want); !ok || back != in {
			t.Errorf("Unescape(%q) = %q, %v, want %q", want, back, ok, in)
		}
	}
	// Injective across the spellings a lossy map would merge.
	if Escape("a/b") == Escape("a_b") || Escape("a/b") == Escape("a_2fb") {
		t.Error("two names share a slug")
	}
	for _, bad := range []string{"a/b", "a_", "a_zz", "a_2F", ""} {
		if _, ok := Unescape(bad); ok {
			t.Errorf("Unescape(%q) accepted a string Escape cannot produce", bad)
		}
	}
}
