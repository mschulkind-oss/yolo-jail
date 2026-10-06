package hostfloor

import (
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/capture"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// captured.go answers, from a capture's MANIFEST alone, whether the entry holds a runnable
// ~/.local/bin/<bin> — before the floor spends a materialize on it — and whether the install must
// capture the program again first.
//
// A capture records only its surfaces (paths.InstalledProgramSurfaces), and a vendor installer is
// free to leave ~/.local/bin/<bin> a link to the real program elsewhere. codex's does: the link names
// ~/.codex/packages/standalone/current/bin/codex, and `current` is itself a link to one release
// directory. So the manifest is followed as the kernel would follow the tree, a link in ANY
// component of the path resolved, not only a link that is the whole path. A program the links take
// out of what the capture recorded can run only in the jail that made it, where a launcher installs
// it anyway: for the floor that is NO FLOOR ENTRY, not a failed install.
//
// Two kinds of entry are not judged on their program at all, because the install captures the
// program again before it uses them (recaptureReason): one recorded before captures scanned their
// contents, which cannot move out of /home/agent, and one recorded before a capture surface it
// would need existed — the codex entries a machine captured before ~/.codex/packages/standalone was
// one (HP-D17, revising HP-D7).

// noEntryError is an install that found the floor cannot hold the program on this machine; it
// is ErrNoEntry to errors.Is, and carries the reason a launch prints.
type noEntryError struct{ reason string }

func (e *noEntryError) Error() string        { return "no floor entry: " + e.reason }
func (e *noEntryError) Is(target error) bool { return target == ErrNoEntry }

// noEntryReasonOf returns the reason an error carries when it is a noEntryError, else "".
func noEntryReasonOf(err error) string {
	var ne *noEntryError
	if errors.As(err, &ne) {
		return ne.reason
	}
	return ""
}

// capturedProgram reports why entry holds no runnable ~/.local/bin/<bin>, or "" when it does,
// with the home-relative path the program resolves to inside the capture: a regular file with an
// execute bit, reached through any links the capture recorded.
func capturedProgram(entry *capture.Entry, bin string) (final, why string) {
	m, err := capture.ReadManifest(entry.Root)
	if err != nil {
		return "", "its manifest is unreadable (" + err.Error() + ")"
	}
	return programInManifest(m, ".local/bin/"+bin)
}

// recaptureReason says why the install captures bin again before it uses entry, as a clause that
// follows "the capture of <bin> on this machine", or "" when it uses entry as it is: the entry was
// recorded for the capture jail's home only (no full reference scan), or it holds no runnable
// program and was recorded before a capture surface it lacks existed, which a new capture records.
func recaptureReason(entry *capture.Entry, bin string) string {
	m, err := capture.ReadManifest(entry.Root)
	if err != nil || !m.Relocatable {
		return "was recorded for a jail's home only"
	}
	_, unrecorded, why := walkManifest(m, ".local/bin/"+bin)
	if why == "" || unrecorded == "" {
		return ""
	}
	for _, s := range missingSurfaces(m) {
		if unrecorded == s || strings.HasPrefix(unrecorded, s+"/") {
			return "was recorded before captures kept ~/" + s + ", where its ~/.local/bin/" + bin + " leads"
		}
	}
	return ""
}

// missingSurfaces is every capture surface this yolo records that m's capture did not, in
// paths.InstalledProgramSurfaces order, home-relative and slash-separated.
func missingSurfaces(m *capture.Manifest) []string {
	have := map[string]bool{}
	for _, s := range m.Surfaces {
		have[s] = true
	}
	var out []string
	for _, s := range paths.InstalledProgramSurfaces() {
		if rel := filepath.ToSlash(s.HomeRel); !have[rel] {
			out = append(out, rel)
		}
	}
	return out
}

// tildeList writes home-relative paths as a reader sees them: "~/a", "~/a and ~/b", "~/a, ~/b and ~/c".
func tildeList(rels []string) string {
	ts := make([]string, len(rels))
	for i, r := range rels {
		ts[i] = "~/" + r
	}
	if len(ts) < 2 {
		return strings.Join(ts, "")
	}
	return strings.Join(ts[:len(ts)-1], ", ") + " and " + ts[len(ts)-1]
}

// programInManifest reports why the manifest holds no runnable program at the home-relative rel,
// or "" when it does, with the home-relative path the program resolves to: a regular file with an
// execute bit, reached by resolving a link in EVERY component of the path, as the kernel does. A
// directory component continues the walk; a link replaces the path up to it with its target — an
// absolute target under the capture's home read as home-relative, a relative one from the link's own
// directory — and the walk starts again, at most maxLinkHops links in all. The walk refuses a target
// outside the capture's home, a path that climbs out of it, a file where a directory should be, and
// a component the capture did not record. An installer's program is ~/.local/bin/<bin>; a fork's is
// the `produces` entry at a surface's bin/<bin> (packdecl.Install.ProgramPath).
func programInManifest(m *capture.Manifest, rel string) (final, why string) {
	final, _, why = walkManifest(m, rel)
	return final, why
}

// walkManifest is programInManifest, saying too which path the links led to that the capture did not
// record ("" for any other refusal): the one a recapture may record, when a surface holding it is
// newer than the capture (recaptureReason).
func walkManifest(m *capture.Manifest, rel string) (final, unrecorded, why string) {
	byPath := make(map[string]capture.ManifestEntry, len(m.Entries))
	// implied holds every directory some recorded entry is beneath: a materialize creates one the
	// manifest does not list (it existed before the capture, unchanged), so the walk passes it too.
	implied := map[string]bool{}
	for _, e := range m.Entries {
		byPath[e.Path] = e
		for d := path.Dir(e.Path); d != "." && d != "/" && !implied[d]; d = path.Dir(d) {
			implied[d] = true
		}
	}
	home := ""
	if m.Home != "" {
		home = strings.TrimSuffix(path.Clean(m.Home), "/")
	}
	rel = path.Clean(rel)
	p, followed := rel, false
	for hops := 0; ; {
		parts := strings.Split(p, "/")
		next := ""
		for i := range parts {
			prefix := strings.Join(parts[:i+1], "/")
			last := i == len(parts)-1
			e, ok := byPath[prefix]
			if !ok && !last && implied[prefix] {
				continue
			}
			if !ok {
				if !followed {
					return "", "", "it records no ~/" + rel
				}
				return "", p, fmt.Sprintf("~/%s links to ~/%s, which the capture did not record%s", rel, p,
					recordedOnly(m.Surfaces))
			}
			switch e.Kind {
			case capture.KindDir:
				if last {
					return "", "", "~/" + p + " is a directory"
				}
				continue
			case capture.KindFile:
				if !last {
					return "", "", "~/" + prefix + " is a file, so nothing can be beneath it"
				}
				if mode, err := strconv.ParseUint(e.Mode, 8, 32); err != nil || mode&0o111 == 0 {
					return "", "", "~/" + p + " is not executable"
				}
				return p, "", ""
			case capture.KindSymlink:
			default:
				return "", "", "~/" + p + " is a " + e.Kind
			}
			// A LINK, here or mid-path: the path up to it becomes its target, and the walk starts
			// again from the top of the new path.
			target := path.Clean(e.Target)
			var to string
			switch {
			case path.IsAbs(target) && home != "" && target == home:
				to = "."
			case path.IsAbs(target) && home != "" && strings.HasPrefix(target, home+"/"):
				to = strings.TrimPrefix(target, home+"/")
			case path.IsAbs(target):
				return "", "", fmt.Sprintf("~/%s links to %s, outside the home the capture recorded", prefix, target)
			default:
				to = path.Join(path.Dir(prefix), target)
			}
			next = path.Join(append([]string{to}, parts[i+1:]...)...)
			if next == ".." || strings.HasPrefix(next, "../") {
				return "", "", fmt.Sprintf("~/%s links to %s, which climbs out of the home", prefix, e.Target)
			}
			if next == "." {
				return "", "", fmt.Sprintf("~/%s links to the home itself", rel)
			}
			break
		}
		if hops++; hops > maxLinkHops {
			return "", "", "~/" + rel + " is a chain of links too long to follow"
		}
		p, followed = next, true
	}
}

// recordedOnly is the clause a "did not record" reason ends with: the surfaces this capture
// recorded, from its own manifest, "" when it names none.
func recordedOnly(surfaces []string) string {
	if len(surfaces) == 0 {
		return ""
	}
	return " (it recorded only " + tildeList(surfaces) + ")"
}
