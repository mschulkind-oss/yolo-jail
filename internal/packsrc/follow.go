package packsrc

// follow.go is a patched fork's FOLLOW RULE (docs/design/patched-forks.md §3.3, PF-D3, PF-D4,
// PF-D24): which commit of its upstream a patched fork takes when its `?ref=` names a branch, and
// the version grammar the release rule reads tag names with.
//
//	follow: "release"           the newest tag merged into the branch whose name is a semantic
//	(the default, PF-D24)       version, optionally v-led
//	follow: "release:<prefix>"  the same, among tags named <prefix> followed by a semantic version
//	follow: "head"              the branch's newest commit
//
// A tag or a full-commit ref holds the fork there, whatever `follow` says, and a ref that is
// neither (HEAD, an abbreviated commit) is refused at the check (patchcheck.go).
//
// THE VERSION GRAMMAR is semantic versioning's (semver.org §2 and §9-§11): MAJOR.MINOR.PATCH, no
// leading zeros, an optional `-pre.release` and an optional `+build`. NEWEST is semver precedence
// and never a tag's date; build metadata is ignored for precedence, a PRE-RELEASE is never chosen,
// and a tag that does not parse is ignored. Major versions are crossed: `release` takes v1.0.0
// after v0.99.2 as it takes any newer version.

import (
	"fmt"
	"strconv"
	"strings"
)

// FollowKind is what a follow rule takes from a branch.
type FollowKind int

const (
	// FollowRelease takes the newest version tag merged into the branch.
	FollowRelease FollowKind = iota
	// FollowHead takes the branch's newest commit.
	FollowHead
)

// FollowRule is a parsed `follow`.
type FollowRule struct {
	Kind FollowKind
	// Prefix is release:<prefix>'s prefix: a version tag is then <prefix><semver>, with no `v`
	// between them (a prefix that wants one spells it). "" for plain "release", whose tags are
	// <semver> or v<semver>.
	Prefix string
}

// DefaultFollow is `follow` when a patched fork declares none: the newest version tag (PF-D24,
// OQ-PFK2 decided A on 2026-10-04).
const DefaultFollow = "release"

// ParseFollow parses a `follow` value; "" is DefaultFollow.
func ParseFollow(s string) (FollowRule, error) {
	switch {
	case s == "" || s == "release":
		return FollowRule{Kind: FollowRelease}, nil
	case s == "head":
		return FollowRule{Kind: FollowHead}, nil
	case strings.HasPrefix(s, "release:"):
		prefix := strings.TrimPrefix(s, "release:")
		if prefix == "" {
			return FollowRule{}, fmt.Errorf("%q names an empty prefix — write \"release\" for tags that "+
				"are a version alone (1.2.3 or v1.2.3)", s)
		}
		for _, r := range prefix {
			if r <= ' ' || r == 0x7f || strings.ContainsRune("~^:?*[\\", r) {
				return FollowRule{}, fmt.Errorf("%q: the prefix %q holds a character no git tag name "+
					"can, so it could never match a tag", s, prefix)
			}
		}
		return FollowRule{Kind: FollowRelease, Prefix: prefix}, nil
	}
	return FollowRule{}, fmt.Errorf("%q is not a follow rule — write \"release\" (the newest version "+
		"tag, the default), \"release:<prefix>\" (the newest version among tags named "+
		"<prefix><version>) or \"head\" (the branch's newest commit)", s)
}

// String is the rule as a manifest spells it.
func (f FollowRule) String() string {
	switch {
	case f.Kind == FollowHead:
		return "head"
	case f.Prefix != "":
		return "release:" + f.Prefix
	}
	return "release"
}

// Version is a parsed semantic version.
type Version struct {
	Major, Minor, Patch uint64
	// Pre is the pre-release suffix without its `-`, "" for a release.
	Pre string
}

// String renders the version, with no build metadata and no `v`.
func (v Version) String() string {
	s := fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
	if v.Pre != "" {
		s += "-" + v.Pre
	}
	return s
}

// Compare orders two RELEASES by precedence (semver §11), -1, 0 or 1. Pre-releases are never
// candidates (the release rule skips them), so their precedence is not implemented: two versions
// that differ only in Pre compare equal here.
func (v Version) Compare(w Version) int {
	for _, d := range [3][2]uint64{{v.Major, w.Major}, {v.Minor, w.Minor}, {v.Patch, w.Patch}} {
		switch {
		case d[0] < d[1]:
			return -1
		case d[0] > d[1]:
			return 1
		}
	}
	return 0
}

// TagVersion reads a tag's short name (no refs/tags/) as a version under f, and reports whether it
// is one. Under plain "release" the name is <semver> or v<semver>; under "release:<prefix>" it is
// <prefix><semver>. A pre-release parses (so a caller can tell it from a stray tag), and the
// release rule then skips it.
func (f FollowRule) TagVersion(tag string) (Version, bool) {
	if f.Prefix != "" {
		rest, ok := strings.CutPrefix(tag, f.Prefix)
		if !ok {
			return Version{}, false
		}
		return ParseVersion(rest)
	}
	return ParseVersion(strings.TrimPrefix(tag, "v"))
}

// ParseVersion parses MAJOR.MINOR.PATCH[-PRE][+BUILD] strictly: no leading zeros in a number, no
// empty identifier. Build metadata is dropped.
func ParseVersion(s string) (Version, bool) {
	core, build, hasBuild := strings.Cut(s, "+")
	if hasBuild && !validIdentifiers(build, false) {
		return Version{}, false
	}
	core, pre, hasPre := strings.Cut(core, "-")
	if hasPre && !validIdentifiers(pre, true) {
		return Version{}, false
	}
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return Version{}, false
	}
	var nums [3]uint64
	for i, p := range parts {
		if !numericIdentifier(p) {
			return Version{}, false
		}
		n, err := strconv.ParseUint(p, 10, 64)
		if err != nil {
			return Version{}, false
		}
		nums[i] = n
	}
	return Version{Major: nums[0], Minor: nums[1], Patch: nums[2], Pre: pre}, true
}

// numericIdentifier is a number with no leading zero (semver §2).
func numericIdentifier(s string) bool {
	if s == "" || (len(s) > 1 && s[0] == '0') {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// validIdentifiers checks a dot-separated pre-release or build list: each identifier non-empty and
// of [0-9A-Za-z-]; a numeric pre-release identifier has no leading zero (semver §9, §10).
func validIdentifiers(s string, pre bool) bool {
	for _, id := range strings.Split(s, ".") {
		if id == "" {
			return false
		}
		numeric := true
		for _, r := range id {
			switch {
			case r >= '0' && r <= '9':
			case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r == '-':
				numeric = false
			default:
				return false
			}
		}
		if pre && numeric && len(id) > 1 && id[0] == '0' {
			return false
		}
	}
	return true
}
