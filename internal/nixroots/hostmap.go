// Package nixroots registers nix GC roots from inside a container jail so the HOST honors
// them: a TRANSLATED ROOT, a term coined in docs/design/in-jail-nix-roots.md §4 for an
// ordinary indirect root sent under the host's absolute spelling of a link the jail made.
//
// WHY A JAIL NEEDS THIS AT ALL. A podman jail reaches the host's nix daemon through the
// bind-mounted socket, and the daemon records an indirect root as the path STRING the
// client sends, then resolves that string later on the HOST filesystem. nix's own client
// sends the jail's spelling (`/home/agent/.local/…`, `/workspace/result`), which does not
// exist on the host, so the daemon deletes the root as stale at its next GC or root query
// (the design's §2). Sending the host's spelling of the same link is honored, and dies when
// the link does, exactly as a root made on the host (its §3, M6).
//
// Two halves, and they live on opposite sides of the jail boundary:
//
//   - The HOST PATH MAP (this file): the launcher states, for every mount it gives a jail,
//     which host directory a jail path under it is. The jail does not derive this — the
//     `root` field of mountinfo is relative to a filesystem, not a host path — so the
//     launcher hands it over in MapEnv.
//   - The DAEMON CLIENT and the registrar (daemon.go, register.go): the jail side, which
//     makes the link, translates it through the map and sends the one operation.
package nixroots

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
)

// MapEnv is the container environment variable carrying a launch's HostMap, encoded by
// (HostMap).Encode. The launcher emits it only when it mounts the host nix daemon, and only
// when at least one entry translates; an absent variable means nothing in this jail
// translates, which is also what a launcher older than the variable leaves.
const MapEnv = "YOLO_HOST_PATH_MAP"

// HostMap maps a mount's destination in a jail to the host directory it is, keyed by the
// destination's clean absolute path.
//
// An entry whose value is "" is a MASK: a mount whose contents are not a host directory the
// launcher can name — a read-only bind, a named or anonymous volume, a tmpfs, or a bind whose
// source the launcher could not itself translate. It is kept rather than dropped because
// translation is by LONGEST destination prefix, and mounts nest: a volume or a read-only bind
// inside a writable one would otherwise be translated through its parent, to a host path that
// is not where the jail's file is.
type HostMap map[string]string

// Mount is one mount a launch gives a jail, as the argv states it.
type Mount struct {
	// Dest is where the mount appears in the jail.
	Dest string
	// Source is the mount's source as the launcher spells it: a path in the LAUNCHER'S own
	// filesystem for a bind, a name for a volume, "" for a tmpfs.
	Source string
	// Writable is true only for a read-write bind of an absolute source, the one kind of
	// mount whose files a jail-made link can live in and whose host path is the source's.
	Writable bool
}

// Compose builds the map a launch states for the jail it is starting: one entry per mount,
// the last mount of a destination winning as it does in the runtime.
//
// A writable mount's source is passed through toHost, which says what that source is on the
// host. A launcher running on the host passes nil, since its own paths are the host's. A
// launcher inside a jail passes its own jail's map (with the source's symlinks resolved
// first), and a source that map cannot translate becomes a mask — the design's "a nested
// launch composes" (§4), with the same effect as a path under no mount.
func Compose(mounts []Mount, toHost func(source string) (string, bool)) HostMap {
	m := HostMap{}
	for _, mt := range mounts {
		dest, ok := cleanAbs(mt.Dest)
		if !ok {
			continue
		}
		host := ""
		if mt.Writable {
			if src, ok := cleanAbs(mt.Source); ok {
				if toHost == nil {
					host = src
				} else if h, ok := toHost(src); ok {
					host = h
				}
			}
		}
		m[dest] = host
	}
	return m
}

// Translates reports whether any entry names a host path. A map that translates nothing
// is not worth stating: every path under it would stay as unrooted as it is with no map.
func (m HostMap) Translates() bool {
	for _, host := range m {
		if host != "" {
			return true
		}
	}
	return false
}

// Translate returns the host path for an absolute path in the jail: the host source of the
// mount with the longest destination containing it, joined with the rest of the path. ok is
// false for a relative path, a path under no mount, and a path whose nearest mount is a mask.
//
// The path is matched AS SPELLED. A caller holding a path that may run through a symlink in
// the jail resolves it first, since a mount is found by where the bytes are, not by the name
// a link gives them (Registrar.Root resolves the link's directory for this reason).
func (m HostMap) Translate(jailPath string) (string, bool) {
	p, ok := cleanAbs(jailPath)
	if !ok {
		return "", false
	}
	best, found := "", false
	for dest := range m {
		if within(p, dest) && (!found || len(dest) > len(best)) {
			best, found = dest, true
		}
	}
	if !found || m[best] == "" {
		return "", false
	}
	rel, err := filepath.Rel(best, p)
	if err != nil {
		return "", false
	}
	return filepath.Join(m[best], rel), true
}

// Encode is the map as MapEnv carries it: a JSON object, keys sorted.
func (m HostMap) Encode() string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(map[string]string(m)); err != nil {
		return "{}" // unreachable: a map of strings always encodes
	}
	return strings.TrimSpace(b.String())
}

// ParseHostMap reads MapEnv's value. "" is the empty map. Every key must be a clean
// absolute path and every value "" or one; anything else rejects the whole value, because a
// map with one wrong entry cannot be told apart from a map that is wrong throughout.
func ParseHostMap(s string) (HostMap, error) {
	if strings.TrimSpace(s) == "" {
		return HostMap{}, nil
	}
	var raw map[string]string
	if err := json.Unmarshal([]byte(s), &raw); err != nil {
		return nil, fmt.Errorf("%s is not a JSON object of strings: %w", MapEnv, err)
	}
	m := HostMap{}
	for dest, host := range raw {
		if c, ok := cleanAbs(dest); !ok || c != dest {
			return nil, fmt.Errorf("%s: destination %q is not a clean absolute path", MapEnv, dest)
		}
		if host != "" {
			if c, ok := cleanAbs(host); !ok || c != host {
				return nil, fmt.Errorf("%s: host path %q for %q is not a clean absolute path",
					MapEnv, host, dest)
			}
		}
		m[dest] = host
	}
	return m, nil
}

// cleanAbs is p cleaned, and whether it is an absolute path at all.
func cleanAbs(p string) (string, bool) {
	if p == "" || !filepath.IsAbs(p) {
		return "", false
	}
	return filepath.Clean(p), true
}

// within reports whether the clean absolute path p is dir or lies under it, by whole path
// components: /workspace2 is not under /workspace.
func within(p, dir string) bool {
	return p == dir || dir == "/" || strings.HasPrefix(p, dir+"/")
}
