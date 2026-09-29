package brokerscope

import (
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// fence.go is the mount fence (BB-D26): with a brokered loophole's pack selected, a `mounts`
// entry whose host source is, contains or lies inside yolo's broker directory (the store,
// the launches' scope files and the audit log) or a declared credential path is refused at
// workspace scope and disclosed at user scope. `mounts` is a workspace key and accepts any
// host path, so without this an agent could mount the host's login into the next launch
// once a human approved the diff.
//
// The approvals directory is deliberately NOT fenced (BB-D34): every `mounts` entry is a
// read-only bind, so no mount can write an approval, and what one can read there is
// repository names, not a credential.

// ExpandCredentialPath expands one `credential_paths` entry against the host: `~` from
// home, `$VAR` from getenv. It returns "" for a `$VAR` entry whose variable is unset, which
// the fence then simply does not include.
func ExpandCredentialPath(entry, home string, getenv func(string) string) string {
	switch {
	case entry == "~":
		return home
	case strings.HasPrefix(entry, "~/"):
		return filepath.Join(home, entry[2:])
	case strings.HasPrefix(entry, "$"):
		name, rest, _ := strings.Cut(entry[1:], "/")
		v := getenv(name)
		if v == "" {
			return ""
		}
		return filepath.Join(v, rest)
	}
	return filepath.Clean(entry)
}

// FencedPaths is every host path a mount may not reach for the given brokered declarations:
// the broker directory, always, and each declaration's credential paths, expanded.
func FencedPaths(credentialPaths []string, home string, getenv func(string) string) []string {
	out := []string{paths.BrokerDir()}
	for _, e := range credentialPaths {
		if p := ExpandCredentialPath(e, home, getenv); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// Reaches reports whether a mount's host source is, contains or lies inside fenced, after
// resolving symlinks on both sides so a link cannot walk around the fence. It returns the
// fenced path it reaches, or "".
func Reaches(hostSource string, fenced []string) string {
	src := resolve(hostSource)
	for _, f := range fenced {
		fp := resolve(f)
		if within(src, fp) || within(fp, src) {
			return f
		}
	}
	return ""
}

// resolve follows symlinks as far as the path exists, then appends the rest, so a fenced
// directory that does not exist yet is still compared by where it would be.
func resolve(p string) string {
	p = filepath.Clean(p)
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	parent, base := filepath.Dir(p), filepath.Base(p)
	if parent == p {
		return p
	}
	return filepath.Join(resolve(parent), base)
}

// within reports whether p is dir or lies inside it.
func within(p, dir string) bool {
	if p == dir {
		return true
	}
	rel, err := filepath.Rel(dir, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
