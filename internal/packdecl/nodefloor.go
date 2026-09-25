package packdecl

// nodefloor.go is the `node_floor` field's semantics: a MINIMUM version a `program`'s entrypoint
// requires, compared against a candidate interpreter's version
// (docs/reference/agent-program-runtimes.md, OQ-AR1 and OQ-AR4 under "Why it's this way").
//
// # Why a floor, and why yolo compares it itself
//
// The vendor's own constraint is a range floor (`engines.node: ">=22.19.0"`), and a floor survives
// the image moving to a newer Node without a manifest edit. It is DECLARED rather than read from the
// installed package's `engines`, because core does not guess and the package is not installed when
// `yolo check` runs, so a derived floor could never be validated.
//
// ⚠ A MISE SELECTOR CANNOT EXPRESS THIS, which is why the comparison lives here at all. Measured
// 2026-09-22: `mise install --dry-run node@22.19` reports "22.19.0 would install" with 22.20.2 and
// 22.23.2 already present. A selector is a PREFIX that fetches, not a floor that accepts — so
// resolution enumerates candidates and compares them, rather than handing the string to mise.

import (
	"fmt"
	"strconv"
	"strings"
)

// ValidNodeFloor reports whether v is a usable floor: one to three dot-separated non-negative
// integers, optionally `v`-prefixed. "22", "22.19" and "22.19.0" are all legal, and a floor shorter
// than three parts is NOT padded at declaration — CompareVersions treats a missing part as zero,
// which is what makes "22.19" mean "22.19.0 or newer".
//
// A prerelease or build suffix is REFUSED rather than tolerated: `22.19.0-rc.1` would compare as
// something this package does not model, and a floor that silently compares wrong is worse than one
// that will not load.
func ValidNodeFloor(v string) bool {
	if v == "" {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(v, "v"), ".")
	if len(parts) > 3 {
		return false
	}
	for _, p := range parts {
		if p == "" {
			return false
		}
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return false
		}
	}
	return true
}

// CompareVersions returns -1, 0 or 1 comparing a and b numerically, part by part.
//
// # Why not strings.Compare
//
// This is the whole reason the function exists: lexically "20.20.2" > "22.19" because '0' < '2' at
// the second character, so a string compare would accept Node 20 against a floor of 22.19 — the
// exact failure the floor exists to prevent, arrived at by the cheapest possible implementation.
//
// A missing part counts as zero, so "22" == "22.0.0" and "22.19" < "22.19.1". An unparseable part
// counts as zero rather than erroring: the caller has already validated the floor, and a CANDIDATE
// whose version string is odd should lose to a floor rather than crash a launch.
func CompareVersions(a, b string) int {
	pa := strings.Split(strings.TrimPrefix(a, "v"), ".")
	pb := strings.Split(strings.TrimPrefix(b, "v"), ".")
	n := len(pa)
	if len(pb) > n {
		n = len(pb)
	}
	for i := 0; i < n; i++ {
		x, y := versionPart(pa, i), versionPart(pb, i)
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	return 0
}

// versionPart reads index i of a split version, treating absent or unparseable as zero.
func versionPart(parts []string, i int) int {
	if i >= len(parts) {
		return 0
	}
	n, err := strconv.Atoi(parts[i])
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// SatisfiesNodeFloor reports whether a candidate interpreter's version meets floor.
//
// The comparison is `candidate >= floor`, which is the whole of the semantics: a NEWER Node
// satisfies an older floor, so the image moving forward needs no manifest edit. An empty floor is
// satisfied by anything — a program that declares nothing keeps today's behaviour byte-for-byte,
// which is load-bearing rather than tidy, because `opencode-ai` ships a native ELF through the same
// `via: npm` route and must never be wrapped in an interpreter.
func SatisfiesNodeFloor(candidate, floor string) bool {
	if floor == "" {
		return true
	}
	return CompareVersions(candidate, floor) >= 0
}

// nodeFloorProblem returns the validation message for a bad floor, or "".
func nodeFloorProblem(label, v string) string {
	if v == "" || ValidNodeFloor(v) {
		return ""
	}
	return fmt.Sprintf("%s: %q is not a usable node floor — give one to three dot-separated "+
		"numbers (\"22\", \"22.19\", \"22.19.0\"), with no prerelease or build suffix, because a "+
		"floor this package cannot compare would silently accept the wrong interpreter", label, v)
}
