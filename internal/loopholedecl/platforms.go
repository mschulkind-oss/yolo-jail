package loopholedecl

// platforms.go is the `platforms` key: WHERE A LOOPHOLE CAN RUN AT ALL
// (docs/design/loophole-packaging.md §3.1, "A loophole must declare where it can
// run").
//
// # Why `requires` could not already say this
//
// `requires` (command_on_path, file_exists) is a RUNTIME PROBE: "the thing I need
// is present". It cannot express "I only exist for this platform", and the
// difference is not cosmetic. A pack shipping a compiled Linux daemon on macOS,
// gated only by `requires`, is reported as a requirement that happened to be
// unmet — which reads as *install the missing thing*, advice that can never
// succeed — and a manifest with no `requires` at all is not reported at all: it
// goes Active, its daemon spawns, and it dies five seconds later through the
// silent readiness path (§2.1c). Both are the same defect: a fact the author knew
// statically, discovered dynamically and misattributed.
//
// So the declaration is STATIC (validated here, at load, where a typo is an
// author-visible error) and its EVALUATION is a pure function of (GOOS, GOARCH) —
// SupportsPlatform. This package never reads runtime.GOOS: the platform of the
// machine is not a fact about the schema, and a leaf that reads the world is a
// leaf that will grow an import. `internal/loopholes` supplies the pair.
//
// # The vocabulary is Go's, deliberately
//
// An entry is `<goos>` or `<goos>/<goarch>`, spelled exactly as Go spells them,
// and both halves are checked against a CLOSED list. The closed list is the whole
// point: `"platforms": ["darwins"]` under an open list is a loophole supported
// NOWHERE, on every machine, forever, with no message — the silent-nothing shape
// this field exists to end. The list is Go's own (`go tool dist list`) rather than
// "the platforms yolo runs on" because the field's units are GOOS/GOARCH; a
// manifest declaring `windows` is honest and merely never supported, while
// coupling the enum to yolo's backend support would make tomorrow's new backend a
// migration for every manifest in existence.
//
// # The skew shape, named
//
// A build that predates this key reads it TOLERANTLY (it is unknown, so it is
// skipped and reported) and therefore treats the loophole as supported
// everywhere — i.e. exactly today's behaviour, which is why adding the key is
// safe. Whoever widens the GOOS/GOARCH lists should note the other direction:
// VALUES are not tolerated, so a value only a newer Go knows is a refusal on an
// older build. That is the `tier` incident's shape and the reason the lists live
// beside the enums they resemble.

import (
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/pytext"
)

// Known GOOS / GOARCH values, from `go tool dist list`. Sorted, so the "not a
// known GOOS" message renders deterministically.
var (
	knownGOOS = []string{
		"aix", "android", "darwin", "dragonfly", "freebsd", "illumos", "ios", "js",
		"linux", "netbsd", "openbsd", "plan9", "solaris", "wasip1", "windows",
	}
	knownGOARCH = []string{
		"386", "amd64", "arm", "arm64", "loong64", "mips", "mips64", "mips64le",
		"mipsle", "ppc64", "ppc64le", "riscv64", "s390x", "wasm",
	}
)

// KnownGOOS returns the accepted `platforms[]` operating systems, sorted.
func KnownGOOS() []string { return copyOf(knownGOOS) }

// KnownGOARCH returns the accepted `platforms[]` architectures, sorted.
func KnownGOARCH() []string { return copyOf(knownGOARCH) }

// parsePlatforms validates the optional `platforms` list.
//
// Absent means EVERY platform — the back-compatible reading, and the one that
// keeps every manifest written before this key existed meaning what it meant.
// An empty list is refused rather than read as "every platform": a list the author
// wrote and left empty declares support for nothing, so honoring it literally
// would make the loophole inert everywhere while honoring it loosely would ignore
// what they wrote. Neither is a good silence, so it is an error with the two fixes
// in it.
func parsePlatforms(manifestPath string, raw any) ([]string, bool, error) {
	if raw == nil {
		return nil, false, nil
	}
	list, ok := raw.([]any)
	if !ok {
		return nil, false, Errorf("%s: 'platforms' must be a list of '<goos>' or"+
			" '<goos>/<goarch>' strings (e.g. [\"linux\", \"darwin/arm64\"])", manifestPath)
	}
	if len(list) == 0 {
		return nil, false, Errorf("%s: 'platforms' is an empty list, which declares support for"+
			" nothing — omit the key to mean every platform, or name the ones that work"+
			" (e.g. [\"linux\"])", manifestPath)
	}
	out := make([]string, 0, len(list))
	for i, entry := range list {
		s, isStr := entry.(string)
		if !isStr || s == "" {
			return nil, false, Errorf("%s: platforms[%d] must be a non-empty string"+
				" ('<goos>' or '<goos>/<goarch>')", manifestPath, i)
		}
		if err := validatePlatform(manifestPath, i, s); err != nil {
			return nil, false, err
		}
		out = append(out, s)
	}
	return out, true, nil
}

// validatePlatform splits and validates one entry. Both halves go through a closed
// list, because the value of this field is that a misspelling is loud.
func validatePlatform(manifestPath string, i int, entry string) error {
	goos, goarch := entry, ""
	if slash := strings.IndexByte(entry, '/'); slash >= 0 {
		goos, goarch = entry[:slash], entry[slash+1:]
		if strings.Contains(goarch, "/") {
			return Errorf("%s: platforms[%d]=%s has more than one '/' — an entry is"+
				" '<goos>' or '<goos>/<goarch>', nothing deeper",
				manifestPath, i, pytext.Repr(entry))
		}
	}
	if !inList(goos, knownGOOS) {
		return Errorf("%s: platforms[%d]=%s names %s, which is not a known GOOS —"+
			" spell it as Go does, one of %s. A misspelling here would make the loophole"+
			" supported on no machine at all, silently, which is what this key exists to"+
			" prevent", manifestPath, i, pytext.Repr(entry), pytext.Repr(goos),
			sortedListRepr(knownGOOS))
	}
	if goarch != "" && !inList(goarch, knownGOARCH) {
		return Errorf("%s: platforms[%d]=%s names %s, which is not a known GOARCH —"+
			" spell it as Go does, one of %s, or drop the '/<goarch>' half to accept every"+
			" architecture on %s", manifestPath, i, pytext.Repr(entry), pytext.Repr(goarch),
			sortedListRepr(knownGOARCH), pytext.Repr(goos))
	}
	return nil
}

// SupportsPlatform reports whether this manifest declares support for the given
// GOOS/GOARCH pair.
//
// A manifest with no `platforms` key supports every platform, so a caller may
// evaluate this unconditionally. An entry naming only a GOOS matches every
// architecture on it, which is the common case: a shell or Python daemon is
// OS-shaped, not machine-shaped, and forcing it to enumerate architectures would
// make the field a liability the first time someone runs riscv64.
//
// PURE, and passed the pair rather than reading runtime.GOOS, so every
// combination is testable from one process and this package stays a leaf.
func (m *Manifest) SupportsPlatform(goos, goarch string) bool {
	if !m.PlatformsSet {
		return true
	}
	for _, entry := range m.Platforms {
		want, wantArch, _ := strings.Cut(entry, "/")
		if want != goos {
			continue
		}
		if wantArch == "" || wantArch == goarch {
			return true
		}
	}
	return false
}

// PlatformsDeclared returns the declared platform strings, sorted, for a message
// that has to say what IS supported. Empty when the manifest declares none (i.e.
// supports all), so a caller printing "supports: <joined>" must check
// PlatformsSet first rather than reading an empty list as "nothing".
func (m *Manifest) PlatformsDeclared() []string {
	out := copyOf(m.Platforms)
	sort.Strings(out)
	return out
}

// PlatformsUnsupportedReason renders the by-name report §3.1 asks for: what this
// machine is, what the loophole supports, and — critically — that this is not a
// missing prerequisite. It returns "" when the platform IS supported.
//
// The wording carries that last part on purpose. The failure this field exists to
// fix is a Linux-only daemon reported on macOS as an unmet `requires`, which tells
// the reader to install something; there is nothing to install, and the sentence
// has to say so or the reader spends the afternoon proving it.
func (m *Manifest) PlatformsUnsupportedReason(goos, goarch string) string {
	if m.SupportsPlatform(goos, goarch) {
		return ""
	}
	return "unsupported on " + goos + "/" + goarch + " — it declares support for " +
		strings.Join(m.PlatformsDeclared(), ", ") +
		". Nothing is missing on this machine and nothing can be installed to fix it"
}

// platformsFrom is the decode-side hook, kept beside the vocabulary it validates.
func platformsFrom(manifestPath string, data *jsonx.OrderedMap) ([]string, bool, error) {
	return parsePlatforms(manifestPath, getOrNil(data, keyPlatforms))
}
