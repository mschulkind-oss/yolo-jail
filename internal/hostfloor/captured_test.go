package hostfloor

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/capture"
)

// TestTheManifestWalkResolvesALinkInEveryComponent is programInManifest read as the kernel reads a
// path: a link anywhere on the way — codex's `current`, absolute or relative — is followed, a
// directory nothing lists is passed when something beneath it is recorded, and each way a path can
// fail to lead to a runnable file inside the capture is refused with its own reason.
func TestTheManifestWalkResolvesALinkInEveryComponent(t *testing.T) {
	dir := func(p string) capture.ManifestEntry {
		return capture.ManifestEntry{Path: p, Kind: capture.KindDir, Mode: "0755"}
	}
	exe := func(p string) capture.ManifestEntry {
		return capture.ManifestEntry{Path: p, Kind: capture.KindFile, Mode: "0755"}
	}
	file := func(p string) capture.ManifestEntry {
		return capture.ManifestEntry{Path: p, Kind: capture.KindFile, Mode: "0644"}
	}
	link := func(p, to string) capture.ManifestEntry {
		return capture.ManifestEntry{Path: p, Kind: capture.KindSymlink, Target: to}
	}
	const bin = ".local/bin/x"
	for _, c := range []struct {
		name    string
		entries []capture.ManifestEntry
		final   string // the program's home-relative path, when it resolves
		why     string // a substring of the refusal, when it does not
	}{
		{"a file", []capture.ManifestEntry{dir(".local"), dir(".local/bin"), exe(bin)}, bin, ""},
		{"directories nothing lists", []capture.ManifestEntry{exe(bin)}, bin, ""},
		{"an absolute link to the program",
			[]capture.ManifestEntry{link(bin, "/home/agent/.local/share/x/1.0"), exe(".local/share/x/1.0")},
			".local/share/x/1.0", ""},
		{"an absolute link through an absolute directory link (codex's shape)",
			[]capture.ManifestEntry{link(bin, "/home/agent/.codex/s/current/bin/x"),
				link(".codex/s/current", "/home/agent/.codex/s/releases/1.0"), exe(".codex/s/releases/1.0/bin/x")},
			".codex/s/releases/1.0/bin/x", ""},
		{"a relative directory link mid-path",
			[]capture.ManifestEntry{link(bin, "../share/x/current/x"), link(".local/share/x/current", "1.0"),
				exe(".local/share/x/1.0/x")},
			".local/share/x/1.0/x", ""},
		{"not recorded", []capture.ManifestEntry{dir(".local")}, "", "it records no ~/.local/bin/x"},
		{"a link to what the capture did not record",
			[]capture.ManifestEntry{link(bin, "/home/agent/.vendor/current/bin/x")}, "",
			"~/.local/bin/x links to ~/.vendor/current/bin/x, which the capture did not record (it recorded only ~/.local)"},
		{"a directory link to what the capture did not record",
			[]capture.ManifestEntry{link(bin, "/home/agent/.codex/s/current/bin/x"),
				link(".codex/s/current", "/home/agent/.codex/s/releases/1.0")}, "",
			"links to ~/.codex/s/releases/1.0/bin/x, which the capture did not record"},
		{"a loop", []capture.ManifestEntry{link(bin, "y"), link(".local/bin/y", "x")}, "",
			"is a chain of links too long to follow"},
		{"a directory loop mid-path",
			[]capture.ManifestEntry{link(bin, "../d/x"), link(".local/d", "e"), link(".local/e", "d")}, "",
			"is a chain of links too long to follow"},
		{"a relative link that climbs out", []capture.ManifestEntry{link(bin, "../../../etc/x")}, "",
			"which climbs out of the home"},
		{"an absolute link outside the home", []capture.ManifestEntry{link(bin, "/usr/bin/x")}, "",
			"outside the home the capture recorded"},
		{"a file mid-path",
			[]capture.ManifestEntry{link(bin, "../share/f/x"), file(".local/share/f")}, "",
			"~/.local/share/f is a file, so nothing can be beneath it"},
		{"a directory at the end", []capture.ManifestEntry{dir(".local"), dir(".local/bin"), dir(bin)}, "",
			"~/.local/bin/x is a directory"},
		{"not executable", []capture.ManifestEntry{file(bin)}, "", "~/.local/bin/x is not executable"},
	} {
		t.Run(c.name, func(t *testing.T) {
			m := &capture.Manifest{Home: "/home/agent", Surfaces: []string{".local"}, Entries: c.entries}
			final, why := programInManifest(m, bin)
			if c.why == "" {
				if why != "" || final != c.final {
					t.Fatalf("programInManifest = %q, %q, want %s", final, why, c.final)
				}
				return
			}
			if final != "" || !strings.Contains(why, c.why) {
				t.Fatalf("programInManifest = %q, %q, want a refusal containing %q", final, why, c.why)
			}
		})
	}
}
