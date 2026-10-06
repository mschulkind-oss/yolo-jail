package packsrc

// npm.go is an NPM SOURCE (docs/design/pi-extension-store-builds.md §4.1, §4.2; XB-D5, XB-D6,
// XB-D11): `npm:<name>[@<spec>]` on an unmodified extension, the second upstream a built tree can
// follow beside a git repository. Its CHECK runs here, on the host, in Go, and writes the same
// check record a git source's does, so the advance, the ratchet and every line above it read one
// record shape whichever upstream it names.
//
//   - THE SPEC is what npm itself reads after the `@` (npm-package-arg's rules): an exact version
//     names that version and needs no request at all; a dist-tag names whatever the registry's tag
//     says; anything node-semver reads as a range, and no spec, which npm reads as `*`, names the
//     version npm's own pick-manifest chooses (pick: the `latest` tag when it is listed, not
//     deprecated and satisfies the range, else the highest satisfying version that is not
//     deprecated, else the highest). A version's `engines` is not read, which npm's pick does
//     (XB-D47).
//   - THE REGISTRY is the public one. The request asks for the abbreviated metadata npm's own
//     installer reads, once per package name per check, through one keep-alive client per process.
//     No credential is sent, so a private registry is not covered (XB-D5).
//   - THE WALK has nothing to replay: an unmodified extension carries no series, so the registry's
//     answer is the walk's one entry, and it fits (WalkSeries).
//
// A version stands where a git source's commit does — in the list, the good build, the receipt's
// revision — and as its own tag, so a line names it once: `1.4.2`, never `1.4.2 (1.4.2)`.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/nodesemver"
)

// NpmSourcePrefix opens an npm source.
const NpmSourcePrefix = "npm:"

// The ref kinds an npm check records (CheckFound.RefKind): an exact version, which is fixed like a
// git tag; a dist-tag; a range.
const (
	RefKindNpmVersion = "npm-version"
	RefKindNpmTag     = "npm-tag"
	RefKindNpmRange   = "npm-range"
)

// IsNpmRefKind reports whether a check record's ref kind is an npm source's.
func IsNpmRefKind(kind string) bool {
	return kind == RefKindNpmVersion || kind == RefKindNpmTag || kind == RefKindNpmRange
}

// IsNpmSource reports whether source is an npm source rather than a pack address.
func IsNpmSource(source string) bool { return strings.HasPrefix(source, NpmSourcePrefix) }

// CheckRepo is the repository a check and a walk of source read: a git source's repository, an npm
// source's `npm:<name>` (NpmSource.Repo), which WalkSeries reads as no repository at all; "" when
// source is neither.
func CheckRepo(source string) string {
	if n, err := ParseNpm(source); err == nil {
		return n.Repo()
	}
	if a, err := Parse(source); err == nil {
		return a.Repo
	}
	return ""
}

// NpmSpecKind is what an npm source's spec names.
type NpmSpecKind int

const (
	// NpmTag is a dist-tag; no spec at all is one too, for its hourly check and its lines (Ref
	// says `latest`), though it picks as the range `*` does, as npm reads it (pick).
	NpmTag NpmSpecKind = iota
	// NpmVersion is one exact version.
	NpmVersion
	// NpmRange is a node-semver range.
	NpmRange
)

// NpmSource is a parsed npm source.
type NpmSource struct {
	// Name is the package name, `@scope/name` for a scoped one.
	Name string
	// Spec is what followed the name's `@`, as written; "" for none.
	Spec string
	Kind NpmSpecKind
	// Version is an exact spec's version; Range a range spec's.
	Version nodesemver.Version
	Range   nodesemver.Range
}

// Ref is the spec a check reads: the spec as written, or `latest` for none.
func (n NpmSource) Ref() string {
	if n.Spec == "" {
		return "latest"
	}
	return n.Spec
}

// String is the source as written: `npm:<name>`, then `@<spec>` when it has one.
func (n NpmSource) String() string {
	if n.Spec == "" {
		return n.Repo()
	}
	return n.Repo() + "@" + n.Spec
}

// Repo is the source's identity without its spec: `npm:<name>`, which a check record reads as its
// repository and a build's receipt as its source, as a git source's carries its repository and not
// its ref.
func (n NpmSource) Repo() string { return NpmSourcePrefix + n.Name }

// ParseNpm parses `npm:<name>[@<spec>]`.
func ParseNpm(source string) (NpmSource, error) {
	rest, ok := strings.CutPrefix(source, NpmSourcePrefix)
	if !ok {
		return NpmSource{}, fmt.Errorf("%q is not an npm source (npm:<name>[@<spec>])", source)
	}
	name, spec := rest, ""
	// The spec's `@` is the last one past the first character: a scoped name's own `@` leads it.
	if i := strings.LastIndex(rest, "@"); i > 0 {
		name, spec = rest[:i], rest[i+1:]
		if spec == "" {
			return NpmSource{}, fmt.Errorf("%q names no spec after its @ — drop the @ for the latest "+
				"version, or name a version, a dist-tag or a range", source)
		}
	}
	if why := npmNameProblem(name); why != "" {
		return NpmSource{}, fmt.Errorf("%q: the package name %q %s", source, name, why)
	}
	n := NpmSource{Name: name, Spec: spec}
	switch {
	case spec == "":
		n.Kind = NpmTag
	case strings.TrimSpace(spec) != spec:
		return NpmSource{}, fmt.Errorf("%q: the spec %q has leading or trailing space", source, spec)
	default:
		if v, ok := nodesemver.Parse(spec); ok {
			n.Kind, n.Version = NpmVersion, v
			break
		}
		if r, err := nodesemver.ParseRange(spec); err == nil {
			n.Kind, n.Range = NpmRange, r
			break
		}
		if !npmTagName(spec) {
			return NpmSource{}, fmt.Errorf("%q: the spec %q is neither a version, a node-semver range nor "+
				"a dist-tag name — write an exact version (1.2.3), a range (^1.2.0) or a tag (latest, next)",
				source, spec)
		}
		n.Kind = NpmTag
	}
	return n, nil
}

// npmNameProblem is why name cannot be an npm package's, or "": validate-npm-package-name's rules
// for a new package, which lowercase-only URL-safe names are, and no leading `-`, which would read
// as an option to npm.
func npmNameProblem(name string) string {
	switch {
	case name == "":
		return "is empty"
	case len(name) > 214:
		return "is longer than npm's 214 characters"
	}
	parts := []string{name}
	if strings.HasPrefix(name, "@") {
		scope, pkg, ok := strings.Cut(name[1:], "/")
		if !ok || strings.Contains(pkg, "/") {
			return "is a scope with no package (@scope/name)"
		}
		parts = []string{scope, pkg}
	} else if strings.Contains(name, "/") {
		return "holds a / outside a scope (@scope/name)"
	}
	for _, p := range parts {
		if p == "" {
			return "has an empty part"
		}
		if p[0] == '.' || p[0] == '_' || p[0] == '-' {
			return "starts with " + string(p[0]) + ", which npm refuses"
		}
		for _, r := range p {
			if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '.' || r == '_') {
				return fmt.Sprintf("holds %q — an npm package name is lowercase letters, digits, - . and _", r)
			}
		}
	}
	return ""
}

// npmTagName is a dist-tag name npm accepts as one: URL-safe, as npm-package-arg requires.
func npmTagName(s string) bool {
	if s == "" || s[0] == '-' || s[0] == '.' {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '.' || r == '_') {
			return false
		}
	}
	return true
}

// NpmRegistry is the registry an npm check asks: a var so a test serves its own.
var NpmRegistry = "https://registry.npmjs.org"

// npmClient is the one keep-alive client a process asks the registry through (XB-D11); each request
// is bounded by the check's own context.
var npmClient = &http.Client{}

// npmMaxMetadataBytes bounds one package's abbreviated metadata: the largest the design measured is
// well under a megabyte, and a bound keeps a hostile answer from becoming the host's memory.
const npmMaxMetadataBytes = 32 << 20

// npmPackument is the part of the registry's abbreviated metadata the check reads.
type npmPackument struct {
	DistTags map[string]string `json:"dist-tags"`
	Versions map[string]struct {
		Deprecated json.RawMessage `json:"deprecated"`
	} `json:"versions"`
}

// errNpmNoPackage is the registry's 404 for a package name: an answer, not a failed fetch, and one
// the next check would get again.
var errNpmNoPackage = errors.New("the registry has no package")

// fetchNpmPackument asks the registry for name's abbreviated metadata.
func fetchNpmPackument(ctx context.Context, name string) (*npmPackument, error) {
	u := strings.TrimSuffix(NpmRegistry, "/") + "/" + url.PathEscape(name)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.npm.install-v1+json; q=1.0, application/json; q=0.8")
	resp, err := npmClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return nil, fmt.Errorf("%w %s", errNpmNoPackage, name)
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("the registry answered %s for %s", resp.Status, name)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, npmMaxMetadataBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > npmMaxMetadataBytes {
		return nil, fmt.Errorf("the registry's metadata for %s is over %d bytes", name, npmMaxMetadataBytes)
	}
	var p npmPackument
	if err := json.Unmarshal(body, &p); err != nil {
		return nil, fmt.Errorf("the registry's metadata for %s does not parse: %v", name, err)
	}
	return &p, nil
}

// pick is the version npm's pick-manifest takes from p for n's spec, or why there is none
// (npm 11's bundled npm-pick-manifest, XB-D36 and XB-D47):
//
//   - an explicit dist-tag is that tag's version, deprecated or not, if the registry lists it;
//   - anything else is a range, no spec being `*` (npm-package-arg reads `npm install <name>` as
//     that range, and it is the install pi runs for `npm:<name>`): the `latest` tag's version when
//     the registry lists it, it is not deprecated, and it satisfies the range or the range is
//     literally `*`, a pre-release included; else the highest satisfying version that is not
//     deprecated; else the highest.
//
// Not read, so a tree may differ from pi's install there: a version's `engines`, which npm weighs
// against the installing node and npm, and a registry's staged or restricted versions, which the
// public registry's abbreviated metadata does not carry.
func (p *npmPackument) pick(n NpmSource) (string, error) {
	if n.Kind == NpmTag && n.Spec != "" {
		v, ok := p.DistTags[n.Spec]
		if !ok || v == "" {
			return "", fmt.Errorf("the registry has no dist-tag %q for %s", n.Spec, n.Name)
		}
		if _, listed := p.Versions[v]; !listed {
			return "", fmt.Errorf("the registry's dist-tag %q for %s names %s, which the registry does not list",
				n.Spec, n.Name, v)
		}
		return v, nil
	}
	rng, star := n.Range, n.Spec == "" || n.Spec == "*"
	if n.Spec == "" {
		var err error
		if rng, err = nodesemver.ParseRange("*"); err != nil {
			return "", err
		}
	}
	if latest, ok := p.DistTags["latest"]; ok {
		meta, listed := p.Versions[latest]
		if v, ok := nodesemver.Parse(latest); ok && listed && !npmDeprecated(meta.Deprecated) &&
			(star || rng.Satisfies(v)) {
			return v.String(), nil
		}
	}
	var live, all []nodesemver.Version
	for s, meta := range p.Versions {
		v, ok := nodesemver.Parse(s)
		if !ok {
			continue
		}
		all = append(all, v)
		if !npmDeprecated(meta.Deprecated) {
			live = append(live, v)
		}
	}
	if v, ok := rng.MaxSatisfying(live); ok {
		return v.String(), nil
	}
	if v, ok := rng.MaxSatisfying(all); ok {
		return v.String(), nil
	}
	return "", fmt.Errorf("no version of %s satisfies %q", n.Name, n.Ref())
}

// npmDeprecated reads a version's `deprecated`: a non-empty message deprecates it; false, "" or
// absent does not.
func npmDeprecated(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s != ""
	}
	var b bool
	if json.Unmarshal(raw, &b) == nil {
		return b
	}
	return false
}

// findNpmCandidates is an npm source's half of the check: the version the spec names now, as the
// walk's one entry. An exact version is that version with no request.
func (s *Store) findNpmCandidates(n NpmSource) CheckFound {
	var found CheckFound
	version := ""
	switch n.Kind {
	case NpmVersion:
		found.RefKind = RefKindNpmVersion
		version = n.Version.String()
	default:
		found.RefKind = RefKindNpmTag
		if n.Kind == NpmRange {
			found.RefKind = RefKindNpmRange
		}
		ctx, cancel := context.WithTimeout(s.parentCtx(), s.timeout())
		defer cancel()
		p, err := fetchNpmPackument(ctx, n.Name)
		switch {
		case errors.Is(err, errNpmNoPackage):
			// AN ANSWER, NOT A FAILED FETCH: the next check would get it again, so the next step is
			// the source's name, never a wait (every stop names the next step).
			found.Problem = oneLine(err) + " — check the source's package name, `" + n.String() + "`; " +
				"once it is edited, `yolo pack update` checks it now"
			return found
		case err != nil:
			found.FetchErr = oneLine(err)
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				found.FetchErr = fmt.Sprintf("the registry did not answer within %s", s.timeout().Round(time.Second))
			}
			found.Problem = "could not ask the registry about " + n.Repo() + " (" + found.FetchErr + ") — the " +
				"next check, in an hour, tries again, or `yolo pack update` checks now"
			return found
		}
		if version, err = p.pick(n); err != nil {
			// The registry answered, and nothing it carries is what the spec names: a dist-tag it
			// does not have, or a range nothing satisfies. The spec is the next step.
			found.Problem = "could not resolve " + n.String() + " (" + oneLine(err) + ") — check the " +
				"source's spec, `" + n.String() + "`; once it is edited, `yolo pack update` checks it now"
			return found
		}
		found.Fetched = true
	}
	found.Tip = version
	found.List = []ListEntry{NpmListEntry(version)}
	return found
}

// NpmListEntry is the walk's entry for an npm version: the version is the revision, and names
// itself as its tag.
func NpmListEntry(version string) ListEntry {
	return ListEntry{Commit: version, Tag: version, Version: version}
}
