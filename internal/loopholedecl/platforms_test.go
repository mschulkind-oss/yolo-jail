package loopholedecl_test

// The `platforms` key (docs/design/loophole-packaging.md §3.1, "A loophole must
// declare where it can run").
//
// The failure it exists to fix: a pack shipping a compiled Linux daemon on macOS
// had exactly two ways to surface, and both misled. With a `requires` gate it read
// as an unmet prerequisite — "install the missing thing", advice that can never
// succeed — and with no gate at all it went Active, spawned, and died five seconds
// later through §2.1c's silent readiness path. So the tests here check the MESSAGE
// as much as the predicate.

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
)

func TestPlatformsAbsentMeansEveryPlatform(t *testing.T) {
	m, err := decodeMap(t, "anywhere", map[string]any{"name": "anywhere"})
	if err != nil {
		t.Fatal(err)
	}
	if m.PlatformsSet {
		t.Error("PlatformsSet = true with no `platforms` key")
	}
	// Every manifest written before this key existed must keep meaning what it
	// meant, or adding the field is a migration for all of them.
	for _, pair := range [][2]string{{"linux", "amd64"}, {"darwin", "arm64"}, {"windows", "386"}} {
		if !m.SupportsPlatform(pair[0], pair[1]) {
			t.Errorf("SupportsPlatform(%q, %q) = false; an absent key means every platform",
				pair[0], pair[1])
		}
		if reason := m.PlatformsUnsupportedReason(pair[0], pair[1]); reason != "" {
			t.Errorf("PlatformsUnsupportedReason(%q, %q) = %q, want empty", pair[0], pair[1], reason)
		}
	}
}

// A bare GOOS matches every architecture on it. A shell or Python daemon is
// OS-shaped, not machine-shaped, and making it enumerate architectures would turn
// the field into a liability the first time someone runs riscv64.
func TestPlatformsGOOSOnlyMatchesEveryArch(t *testing.T) {
	m, err := decodeMap(t, "linuxonly", map[string]any{
		"name": "linuxonly", "platforms": []any{"linux"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !m.PlatformsSet {
		t.Error("PlatformsSet = false with a `platforms` key present")
	}
	for _, arch := range []string{"amd64", "arm64", "riscv64"} {
		if !m.SupportsPlatform("linux", arch) {
			t.Errorf("SupportsPlatform(linux, %s) = false; a bare GOOS covers every arch", arch)
		}
	}
	if m.SupportsPlatform("darwin", "arm64") {
		t.Error("SupportsPlatform(darwin, arm64) = true for a linux-only manifest")
	}
}

func TestPlatformsGOOSSlashGOARCHIsExact(t *testing.T) {
	m, err := decodeMap(t, "narrow", map[string]any{
		"name": "narrow", "platforms": []any{"linux/amd64", "darwin/arm64"},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		goos, goarch string
		want         bool
	}{
		{"linux", "amd64", true},
		{"darwin", "arm64", true},
		{"linux", "arm64", false},
		{"darwin", "amd64", false},
		{"windows", "amd64", false},
	} {
		if got := m.SupportsPlatform(c.goos, c.goarch); got != c.want {
			t.Errorf("SupportsPlatform(%q, %q) = %v, want %v", c.goos, c.goarch, got, c.want)
		}
	}
}

// R8: the report has to say what the situation IS, and specifically that nothing
// is missing — otherwise it reads as the unmet-requirement message it replaces and
// the reader goes looking for something to install.
func TestPlatformsUnsupportedReasonSaysNothingIsMissing(t *testing.T) {
	m, err := decodeMap(t, "linuxd", map[string]any{
		"name": "linuxd", "platforms": []any{"linux/arm64", "linux/amd64"},
	})
	if err != nil {
		t.Fatal(err)
	}
	reason := m.PlatformsUnsupportedReason("darwin", "arm64")
	if reason == "" {
		t.Fatal("PlatformsUnsupportedReason returned empty for an unsupported platform")
	}
	for _, want := range []string{
		"unsupported on darwin/arm64",
		"linux/amd64, linux/arm64", // sorted, so the message is stable
		"Nothing is missing on this machine",
		"nothing can be installed to fix it",
	} {
		if !strings.Contains(reason, want) {
			t.Errorf("reason %q does not carry %q", reason, want)
		}
	}
}

// A misspelled GOOS under an OPEN list would be a loophole supported on no machine
// at all, silently, on every machine, forever — the exact silent-nothing shape this
// key exists to end. So the list is closed and the message names the fix.
func TestPlatformsRefusesAnUnknownGOOS(t *testing.T) {
	_, err := decodeMap(t, "typo", map[string]any{
		"name": "typo", "platforms": []any{"darwins"},
	})
	if err == nil {
		t.Fatal("a misspelled GOOS decoded cleanly")
	}
	msg := err.Error()
	for _, want := range []string{"platforms[0]", "'darwins'", "not a known GOOS", "'darwin'"} {
		if !strings.Contains(msg, want) {
			t.Errorf("refusal %q does not carry %q", msg, want)
		}
	}
}

func TestPlatformsRefusesAnUnknownGOARCH(t *testing.T) {
	_, err := decodeMap(t, "typo", map[string]any{
		"name": "typo", "platforms": []any{"linux/x86_64"},
	})
	if err == nil {
		t.Fatal("a misspelled GOARCH decoded cleanly")
	}
	msg := err.Error()
	for _, want := range []string{
		"platforms[0]", "'x86_64'", "not a known GOARCH", "'amd64'",
		"drop the '/<goarch>' half", // the second fix, for the author who meant "all of linux"
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("refusal %q does not carry %q", msg, want)
		}
	}
}

// An empty list is the one case where honoring the author literally (supports
// nothing) and honoring them loosely (supports everything) are both defensible and
// both silent. Refused, with BOTH fixes named.
func TestPlatformsRefusesAnEmptyList(t *testing.T) {
	_, err := decodeMap(t, "empty", map[string]any{
		"name": "empty", "platforms": []any{},
	})
	if err == nil {
		t.Fatal("an empty platforms list decoded cleanly")
	}
	msg := err.Error()
	for _, want := range []string{"declares support for nothing", "omit the key", "name the ones that work"} {
		if !strings.Contains(msg, want) {
			t.Errorf("refusal %q does not carry %q", msg, want)
		}
	}
}

func TestPlatformsRefusesMalformedShapes(t *testing.T) {
	for _, c := range []struct {
		name  string
		value any
		want  string
	}{
		{"not-a-list", "linux", "must be a list"},
		{"non-string-entry", []any{float64(1)}, "platforms[0] must be a non-empty string"},
		{"empty-entry", []any{""}, "platforms[0] must be a non-empty string"},
		{"too-deep", []any{"linux/amd64/v3"}, "more than one '/'"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := decodeMap(t, "shape", map[string]any{"name": "shape", "platforms": c.value})
			if err == nil {
				t.Fatalf("platforms=%v decoded cleanly", c.value)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("refusal %q does not carry %q", err, c.want)
			}
		})
	}
}

// The declared list is what the report prints, so it must be stable regardless of
// the order the author wrote — a message that reorders between runs cannot be
// diffed or pinned.
func TestPlatformsDeclaredIsSorted(t *testing.T) {
	m, err := decodeMap(t, "multi", map[string]any{
		"name": "multi", "platforms": []any{"linux/arm64", "darwin", "linux/amd64"},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(m.PlatformsDeclared(), ",")
	if want := "darwin,linux/amd64,linux/arm64"; got != want {
		t.Errorf("PlatformsDeclared() = %q, want %q", got, want)
	}
	// And the raw field keeps the author's order, because the RAW guarantee is
	// what lets a footprint quote what was written.
	if m.Platforms[0] != "linux/arm64" {
		t.Errorf("Platforms[0] = %q; the decoded field keeps the author's order", m.Platforms[0])
	}
}

// The key is in KnownKeys, or a strict decode reports the field this change adds
// as a typo — which is how `version` came to be declared everywhere and read by
// nothing.
func TestPlatformsIsAKnownKey(t *testing.T) {
	for _, k := range loopholedecl.KnownKeys() {
		if k == "platforms" {
			return
		}
	}
	t.Errorf("KnownKeys() = %v, missing \"platforms\"", loopholedecl.KnownKeys())
}

// The vocabulary lists are handed out as copies: a package-level slice a caller can
// reorder is a message that renders differently for the next reader.
func TestKnownPlatformListsAreCopies(t *testing.T) {
	for _, c := range []struct {
		name string
		get  func() []string
		want string
	}{
		{"GOOS", loopholedecl.KnownGOOS, "linux"},
		{"GOARCH", loopholedecl.KnownGOARCH, "amd64"},
	} {
		first := c.get()
		for i := range first {
			first[i] = "clobbered"
		}
		second := c.get()
		found := false
		for _, v := range second {
			if v == c.want {
				found = true
			}
			if v == "clobbered" {
				t.Fatalf("Known%s() handed out its backing array", c.name)
			}
		}
		if !found {
			t.Errorf("Known%s() = %v, missing %q", c.name, second, c.want)
		}
	}
}
