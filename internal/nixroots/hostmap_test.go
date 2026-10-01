package nixroots

import (
	"strings"
	"testing"
)

// The translation rule of in-jail-nix-roots.md §4, one row per clause: longest destination
// wins, whole path components only, a mask stops translation for everything under it, and a
// path under no mount does not translate.
func TestTranslateTakesTheLongestMountAndStopsAtAMask(t *testing.T) {
	m := HostMap{
		"/workspace":                  "/home/u/code/proj",
		"/home/agent":                 "", // the read-only home skeleton
		"/home/agent/.local":          "/home/u/code/proj/.yolo/home/local",
		"/home/agent/.local/ro-skill": "", // a read-only bind nested in a writable one
		"/home/agent/.cache":          "/home/u/.cache/yolo-jail",
		"/tmp":                        "", // a tmpfs
	}
	cases := []struct {
		in, want string
		ok       bool
	}{
		{"/workspace/result", "/home/u/code/proj/result", true},
		{"/workspace", "/home/u/code/proj", true},
		{"/workspace/./a/../result", "/home/u/code/proj/result", true},
		// Whole components: /workspace2 is not the workspace.
		{"/workspace2/result", "", false},
		{"/home/agent/.local/share/yolo-jail/build/roots/abc",
			"/home/u/code/proj/.yolo/home/local/share/yolo-jail/build/roots/abc", true},
		{"/home/agent/.local/ro-skill/x", "", false},
		{"/home/agent/.bashrc", "", false},
		{"/home/agent/.cache/nix/x", "/home/u/.cache/yolo-jail/nix/x", true},
		{"/tmp/result", "", false},
		{"/nix/store/abc-x", "", false},
		{"relative/result", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		got, ok := m.Translate(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("Translate(%q) = %q, %v; want %q, %v", c.in, got, ok, c.want, c.ok)
		}
	}
}

// The host launcher's composition: a writable bind of an absolute source is the source; a
// read-only bind, a volume and a tmpfs are masks; the last mount of one destination wins.
func TestComposeOnTheHostMasksEverythingThatIsNotAWritableBind(t *testing.T) {
	m := Compose([]Mount{
		{Dest: "/workspace", Source: "/home/u/proj", Writable: true},
		{Dest: "/home/agent", Source: "/home/u/.local/share/yolo-jail/skel", Writable: false},
		{Dest: "/mise", Source: "yolo-mise-data", Writable: true}, // a named volume
		{Dest: "/tmp", Source: "", Writable: false},
		{Dest: "/home/agent/.cache/", Source: "/home/u/.cache/y/", Writable: true},
		{Dest: "relative", Source: "/x", Writable: true},
		{Dest: "/dup", Source: "/first", Writable: true},
		{Dest: "/dup", Source: "/second", Writable: true},
	}, nil)
	want := HostMap{
		"/workspace":         "/home/u/proj",
		"/home/agent":        "",
		"/mise":              "",
		"/tmp":               "",
		"/home/agent/.cache": "/home/u/.cache/y",
		"/dup":               "/second",
	}
	if len(m) != len(want) {
		t.Fatalf("Compose = %v, want %v", m, want)
	}
	for k, v := range want {
		if got, ok := m[k]; !ok || got != v {
			t.Errorf("Compose[%q] = %q (present %v), want %q", k, got, ok, v)
		}
	}
}

// A NESTED launch composes through its own jail's map (§4, "A nested launch composes"): a
// source its parent can translate becomes the parent's host path, and one it cannot — the
// documented nested workspace under /tmp — is a mask, not an entry pointing at a jail path.
func TestComposeInAJailTranslatesThroughTheParentsMap(t *testing.T) {
	parent := HostMap{
		"/workspace":         "/home/u/proj",
		"/home/agent/.local": "/home/u/proj/.yolo/home/local",
		"/tmp":               "",
	}
	m := Compose([]Mount{
		{Dest: "/workspace", Source: "/tmp/yolo-nested", Writable: true},
		{Dest: "/home/agent/.local", Source: "/tmp/yolo-nested/.yolo/home/local", Writable: true},
		{Dest: "/home/agent/.cache", Source: "/home/agent/.local/cache", Writable: true},
		{Dest: "/ctx/x", Source: "/workspace/docs", Writable: true},
	}, parent.Translate)
	want := HostMap{
		"/workspace":         "",
		"/home/agent/.local": "",
		"/home/agent/.cache": "/home/u/proj/.yolo/home/local/cache",
		"/ctx/x":             "/home/u/proj/docs",
	}
	for k, v := range want {
		if got := m[k]; got != v {
			t.Errorf("nested Compose[%q] = %q, want %q", k, got, v)
		}
	}
	if !m.Translates() {
		t.Error("a nested map with translatable entries reports that it translates nothing")
	}
	if (HostMap{"/a": "", "/b": ""}).Translates() {
		t.Error("a map of masks reports that it translates")
	}
}

func TestEncodeAndParseRoundTrip(t *testing.T) {
	m := HostMap{"/workspace": "/home/u/a b", "/tmp": "", "/home/agent/.local": "/h/l"}
	enc := m.Encode()
	if strings.Contains(enc, "\n") {
		t.Errorf("Encode put a newline in an environment value: %q", enc)
	}
	got, err := ParseHostMap(enc)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(m) {
		t.Fatalf("round trip = %v, want %v", got, m)
	}
	for k, v := range m {
		if got[k] != v {
			t.Errorf("round trip [%q] = %q, want %q", k, got[k], v)
		}
	}
	if again := got.Encode(); again != enc {
		t.Errorf("Encode is not deterministic: %q then %q", enc, again)
	}
	if empty, err := ParseHostMap(""); err != nil || len(empty) != 0 {
		t.Errorf("ParseHostMap(\"\") = %v, %v; want the empty map", empty, err)
	}
}

// A value with one bad entry is refused whole: a caller then has no map, which is the
// state of a jail with none, rather than a map that is right in some places.
func TestParseHostMapRefusesAnythingButCleanAbsolutePaths(t *testing.T) {
	for _, bad := range []string{
		`not json`,
		`["/a"]`,
		`{"relative":"/h"}`,
		`{"/a/../b":"/h"}`,
		`{"/a":"relative"}`,
		`{"/a":"/h/"}`,
		`{"/a":1}`,
	} {
		if m, err := ParseHostMap(bad); err == nil {
			t.Errorf("ParseHostMap(%s) = %v, want an error", bad, m)
		}
	}
}
