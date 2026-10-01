package hostfloor

import (
	"errors"
	"fmt"
	"path"
	"strconv"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/capture"
)

// captured.go answers, from a capture's MANIFEST alone, whether the entry holds a runnable
// ~/.local/bin/<bin> — before the floor spends a materialize on it.
//
// It exists because a capture records only its surfaces (~/.local, ~/.npm-global, ~/go), and a
// vendor installer is free to put the real program elsewhere and leave ~/.local/bin/<bin> a link
// to it. codex's does exactly that — ~/.local/bin/codex links into ~/.codex/packages/…, which no
// capture records (MEASURED 2026-09-29 against this machine's store: the entry holds the link and
// nothing it points at) — so its capture can never run anywhere but the jail that made it, where
// a launcher reinstalls it anyway. For the floor that is NO FLOOR ENTRY, not a failed install:
// re-capturing records the same link again.

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

// captureRelocatable reports whether entry's manifest lets it be materialized into a home other
// than the one it was captured in — the full reference scan ran and found nothing it cannot
// rewrite (capture.Manifest.Relocatable). An unreadable manifest is not relocatable.
func captureRelocatable(entry *capture.Entry) bool {
	m, err := capture.ReadManifest(entry.Root)
	return err == nil && m.Relocatable
}

// capturedProgram reports why entry holds no runnable ~/.local/bin/<bin>, or "" when it does: a
// regular file with an execute bit, or a symlink chain that ends at one inside the capture.
func capturedProgram(entry *capture.Entry, bin string) string {
	m, err := capture.ReadManifest(entry.Root)
	if err != nil {
		return "its manifest is unreadable (" + err.Error() + ")"
	}
	return programInManifest(m, ".local/bin/"+bin)
}

// programInManifest reports why the manifest holds no runnable program at the home-relative rel,
// or "" when it does: a regular file with an execute bit, or a symlink chain that ends at one
// inside the entry. An installer's program is ~/.local/bin/<bin>; a fork's is the `produces`
// entry at a surface's bin/<bin> (packdecl.Install.ProgramPath).
func programInManifest(m *capture.Manifest, rel string) string {
	byPath := make(map[string]capture.ManifestEntry, len(m.Entries))
	for _, e := range m.Entries {
		byPath[e.Path] = e
	}
	home := strings.TrimSuffix(path.Clean(m.Home), "/")
	p := rel
	for hops := 0; hops < 8; hops++ {
		e, ok := byPath[p]
		if !ok {
			if hops == 0 {
				return "it records no ~/" + p
			}
			return fmt.Sprintf("~/%s links to ~/%s, which the capture did not record (a capture "+
				"records only ~/.local, ~/.npm-global and ~/go)", rel, p)
		}
		switch e.Kind {
		case capture.KindFile:
			if mode, err := strconv.ParseUint(e.Mode, 8, 32); err != nil || mode&0o111 == 0 {
				return "~/" + p + " is not executable"
			}
			return ""
		case capture.KindSymlink:
			target := e.Target
			switch {
			case path.IsAbs(target) && home != "" && strings.HasPrefix(target, home+"/"):
				p = strings.TrimPrefix(target, home+"/")
			case path.IsAbs(target):
				return fmt.Sprintf("~/%s links to %s, outside the home the capture recorded", p, target)
			default:
				p = path.Join(path.Dir(p), target)
			}
			p = path.Clean(p)
		default:
			return "~/" + p + " is a " + e.Kind
		}
	}
	return "~/" + rel + " is a chain of links too long to follow"
}
