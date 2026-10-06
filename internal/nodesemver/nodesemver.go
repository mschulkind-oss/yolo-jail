// Package nodesemver reads versions and ranges the way node-semver does, for the one host-side
// reader that has to agree with npm about which version a spec names: the check of an npm source
// (docs/design/pi-extension-store-builds.md XB-D5). npm resolves `npm install <name>@<spec>` with
// node-semver, so a range this package matched differently would build a version npm would not
// have installed.
//
// THE GRAMMAR is node-semver's (its README's "Range Grammar"), without loose mode:
//
//	range-set  ::= range ( '||' range ) *
//	range      ::= hyphen | simple ( ' ' simple ) * | ''
//	hyphen     ::= partial ' - ' partial
//	simple     ::= primitive | partial | tilde | caret
//	primitive  ::= ( '<' | '>' | '>=' | '<=' | '=' ) partial
//	partial    ::= xr ( '.' xr ( '.' xr qualifier ? )? )?
//	xr         ::= 'x' | 'X' | '*' | nr
//	tilde      ::= '~' partial          caret ::= '^' partial
//
// A leading `v` or `=` on a version is accepted, as node-semver's own parse accepts it. Loose mode
// (`1.2.3beta`, leading zeros) is not: npm parses a registry spec loosely, and a spec only loose
// mode reads is refused where the manifest is validated, so nothing reaches the matcher that npm
// and this package could read two ways.
//
// PRE-RELEASES follow node-semver's rule (its testSet): a version with a pre-release satisfies a
// comparator set only when some comparator in that set names a pre-release on the same
// major.minor.patch, so `^1.2.3` never takes `1.3.0-beta.1` and `^1.2.3-beta.1` takes `1.2.3-beta.2`.
// There is no includePrerelease option, because npm's install does not set one.
package nodesemver

import (
	"fmt"
	"strconv"
	"strings"
)

// Version is a semantic version with its pre-release identifiers. Build metadata is dropped, as
// it is for precedence.
type Version struct {
	Major, Minor, Patch uint64
	// Pre is the pre-release's dot-separated identifiers, nil for a release.
	Pre []string
}

// String renders v as node-semver's `version` does: no `v`, no build metadata.
func (v Version) String() string {
	s := fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
	if len(v.Pre) > 0 {
		s += "-" + strings.Join(v.Pre, ".")
	}
	return s
}

// Parse reads a full version, MAJOR.MINOR.PATCH[-PRE][+BUILD], after an optional `v` or `=`.
func Parse(s string) (Version, bool) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(strings.TrimPrefix(s, "="), "v")
	p, ok := parsePartial(s)
	if !ok || p.xMajor || p.xMinor || p.xPatch {
		return Version{}, false
	}
	return p.version(), true
}

// Compare orders two versions by semver precedence (semver.org §11): -1, 0 or 1.
func (v Version) Compare(w Version) int {
	for _, d := range [3][2]uint64{{v.Major, w.Major}, {v.Minor, w.Minor}, {v.Patch, w.Patch}} {
		switch {
		case d[0] < d[1]:
			return -1
		case d[0] > d[1]:
			return 1
		}
	}
	switch {
	case len(v.Pre) == 0 && len(w.Pre) == 0:
		return 0
	case len(v.Pre) == 0:
		return 1
	case len(w.Pre) == 0:
		return -1
	}
	for i := 0; i < len(v.Pre) && i < len(w.Pre); i++ {
		if c := compareIdentifier(v.Pre[i], w.Pre[i]); c != 0 {
			return c
		}
	}
	switch {
	case len(v.Pre) < len(w.Pre):
		return -1
	case len(v.Pre) > len(w.Pre):
		return 1
	}
	return 0
}

// compareIdentifier orders two pre-release identifiers: numeric ones numerically and below every
// alphanumeric one, alphanumeric ones in ASCII order.
func compareIdentifier(a, b string) int {
	an, aNum := numeric(a)
	bn, bNum := numeric(b)
	switch {
	case aNum && bNum:
		switch {
		case an < bn:
			return -1
		case an > bn:
			return 1
		}
		return 0
	case aNum:
		return -1
	case bNum:
		return 1
	}
	return strings.Compare(a, b)
}

func numeric(s string) (uint64, bool) {
	if s == "" {
		return 0, false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, false
		}
	}
	n, err := strconv.ParseUint(s, 10, 64)
	return n, err == nil
}

// partial is one `partial` of the grammar: up to three numbers, any of which may be a wildcard
// or absent (a wildcard), and a qualifier on a full one.
type partial struct {
	major, minor, patch    uint64
	xMajor, xMinor, xPatch bool
	pre                    []string
}

func (p partial) version() Version {
	return Version{Major: p.major, Minor: p.minor, Patch: p.patch, Pre: p.pre}
}

// anyX reports whether some number of p is a wildcard.
func (p partial) anyX() bool { return p.xMajor || p.xMinor || p.xPatch }

// parsePartial reads a partial with no operator: `1`, `1.2`, `1.2.x`, `*`, `1.2.3-beta.1+b`.
func parsePartial(s string) (partial, bool) {
	var p partial
	if s == "" {
		return p, false
	}
	core := s
	rest := ""
	if i := strings.IndexAny(s, "-+"); i >= 0 {
		core, rest = s[:i], s[i:]
	}
	parts := strings.Split(core, ".")
	if len(parts) > 3 {
		return p, false
	}
	nums := []*uint64{&p.major, &p.minor, &p.patch}
	xs := []*bool{&p.xMajor, &p.xMinor, &p.xPatch}
	for i := range nums {
		if i >= len(parts) {
			*xs[i] = true
			continue
		}
		part := parts[i]
		if part == "x" || part == "X" || part == "*" {
			*xs[i] = true
			continue
		}
		if !numberIdentifier(part) {
			return p, false
		}
		n, err := strconv.ParseUint(part, 10, 64)
		if err != nil {
			return p, false
		}
		*nums[i] = n
	}
	// A wildcard swallows every number after it: `1.x.3` is `1.x`.
	if p.xMajor {
		p.xMinor, p.xPatch = true, true
	}
	if p.xMinor {
		p.xPatch = true
	}
	if rest != "" {
		if p.anyX() {
			return p, false // a qualifier belongs to a full version only
		}
		pre, build, hasBuild := "", "", false
		if strings.HasPrefix(rest, "-") {
			pre, build, hasBuild = strings.Cut(rest[1:], "+")
			if !validIdentifiers(pre, true) {
				return p, false
			}
		} else {
			build, hasBuild = rest[1:], true
		}
		if hasBuild && !validIdentifiers(build, false) {
			return p, false
		}
		if pre != "" {
			p.pre = strings.Split(pre, ".")
		}
	}
	return p, true
}

// numberIdentifier is a number with no leading zero (semver §2).
func numberIdentifier(s string) bool {
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

// validIdentifiers checks a dot-separated pre-release or build list (semver §9, §10).
func validIdentifiers(s string, pre bool) bool {
	if s == "" {
		return false
	}
	for _, id := range strings.Split(s, ".") {
		if id == "" {
			return false
		}
		allDigits := true
		for _, r := range id {
			switch {
			case r >= '0' && r <= '9':
			case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r == '-':
				allDigits = false
			default:
				return false
			}
		}
		if pre && allDigits && len(id) > 1 && id[0] == '0' {
			return false
		}
	}
	return true
}

// comparator is one `<op> <version>` a range reduces to; any matches every version (the `*`
// comparator, ANY in node-semver).
type comparator struct {
	op  string // ">", ">=", "<", "<=", "="
	v   Version
	any bool
}

func (c comparator) test(v Version) bool {
	if c.any {
		return true
	}
	cmp := v.Compare(c.v)
	switch c.op {
	case ">":
		return cmp > 0
	case ">=":
		return cmp >= 0
	case "<":
		return cmp < 0
	case "<=":
		return cmp <= 0
	}
	return cmp == 0
}

// Range is a parsed range: a union of comparator sets.
type Range struct {
	sets [][]comparator
	raw  string
}

// String is the range as it was written.
func (r Range) String() string { return r.raw }

// ParseRange reads a range in node-semver's grammar (without loose mode).
func ParseRange(s string) (Range, error) {
	r := Range{raw: s}
	for _, alt := range strings.Split(s, "||") {
		set, err := parseSet(strings.TrimSpace(alt))
		if err != nil {
			return Range{}, fmt.Errorf("%q is not a semver range: %v", s, err)
		}
		r.sets = append(r.sets, set)
	}
	return r, nil
}

// parseSet reads one range of the union: a hyphen range, or simples separated by spaces.
func parseSet(s string) ([]comparator, error) {
	if s == "" {
		return []comparator{{any: true}}, nil
	}
	if from, to, ok := cutHyphen(s); ok {
		return hyphenRange(from, to)
	}
	var out []comparator
	for _, tok := range simples(s) {
		cs, err := simple(tok)
		if err != nil {
			return nil, err
		}
		out = append(out, cs...)
	}
	if len(out) == 0 {
		return []comparator{{any: true}}, nil
	}
	return out, nil
}

// cutHyphen splits `A - B`, the one place a space-separated `-` is grammar.
func cutHyphen(s string) (string, string, bool) {
	fields := strings.Fields(s)
	if len(fields) == 3 && fields[1] == "-" {
		return fields[0], fields[2], true
	}
	return "", "", false
}

// simples splits a range into its simples, joining an operator to the version after it
// (`>= 1.2.3` is `>=1.2.3`, as node-semver's comparator trim makes it).
func simples(s string) []string {
	fields := strings.Fields(s)
	var out []string
	for i := 0; i < len(fields); i++ {
		f := fields[i]
		if isOperator(f) && i+1 < len(fields) {
			out = append(out, f+fields[i+1])
			i++
			continue
		}
		out = append(out, f)
	}
	return out
}

func isOperator(s string) bool {
	switch s {
	case "<", ">", "<=", ">=", "=", "~", "^", "~>":
		return true
	}
	return false
}

// simple reduces one simple to its comparators.
func simple(tok string) ([]comparator, error) {
	switch {
	case strings.HasPrefix(tok, "~>"):
		return tilde(tok[2:])
	case strings.HasPrefix(tok, "~"):
		return tilde(tok[1:])
	case strings.HasPrefix(tok, "^"):
		return caret(tok[1:])
	}
	op := ""
	for _, o := range []string{">=", "<=", ">", "<", "="} {
		if strings.HasPrefix(tok, o) {
			op, tok = o, tok[len(o):]
			break
		}
	}
	p, err := readPartial(tok)
	if err != nil {
		return nil, err
	}
	return xRange(op, p), nil
}

// readPartial reads a partial after its operator, dropping a leading `v` or `=`. An empty one is
// refused, as node-semver refuses `>=`, `~` or `v` alone: its partial needs a first number or
// wildcard.
func readPartial(s string) (partial, error) {
	trimmed := strings.TrimLeft(s, "=v")
	if trimmed == "" {
		return partial{}, fmt.Errorf("%q names no version", s)
	}
	s = trimmed
	p, ok := parsePartial(s)
	if !ok {
		return partial{}, fmt.Errorf("%q is not a version", s)
	}
	return p, nil
}

// zeroPre is the `-0` node-semver puts on an exclusive upper bound, so no pre-release of the
// bound itself satisfies it.
var zeroPre = []string{"0"}

func ge(v Version) comparator { return comparator{op: ">=", v: v} }
func lt(v Version) comparator { return comparator{op: "<", v: v} }

func ver(major, minor, patch uint64, pre []string) Version {
	return Version{Major: major, Minor: minor, Patch: patch, Pre: pre}
}

// nothing is the set node-semver writes for a range that matches no version: `<0.0.0-0`.
func nothing() []comparator { return []comparator{lt(ver(0, 0, 0, zeroPre))} }

// tilde is node-semver's replaceTilde: patch-level changes, or minor-level with no minor.
func tilde(s string) ([]comparator, error) {
	p, err := readPartial(s)
	if err != nil {
		return nil, err
	}
	switch {
	case p.xMajor:
		return []comparator{{any: true}}, nil
	case p.xMinor:
		return []comparator{ge(ver(p.major, 0, 0, nil)), lt(ver(p.major+1, 0, 0, zeroPre))}, nil
	case p.xPatch:
		return []comparator{ge(ver(p.major, p.minor, 0, nil)), lt(ver(p.major, p.minor+1, 0, zeroPre))}, nil
	}
	return []comparator{ge(p.version()), lt(ver(p.major, p.minor+1, 0, zeroPre))}, nil
}

// caret is node-semver's replaceCaret: changes that do not modify the left-most non-zero number.
func caret(s string) ([]comparator, error) {
	p, err := readPartial(s)
	if err != nil {
		return nil, err
	}
	switch {
	case p.xMajor:
		return []comparator{{any: true}}, nil
	case p.xMinor:
		return []comparator{ge(ver(p.major, 0, 0, nil)), lt(ver(p.major+1, 0, 0, zeroPre))}, nil
	case p.xPatch:
		if p.major == 0 {
			return []comparator{ge(ver(0, p.minor, 0, nil)), lt(ver(0, p.minor+1, 0, zeroPre))}, nil
		}
		return []comparator{ge(ver(p.major, p.minor, 0, nil)), lt(ver(p.major+1, 0, 0, zeroPre))}, nil
	}
	low := ge(p.version())
	switch {
	case p.major == 0 && p.minor == 0:
		return []comparator{low, lt(ver(0, 0, p.patch+1, zeroPre))}, nil
	case p.major == 0:
		return []comparator{low, lt(ver(0, p.minor+1, 0, zeroPre))}, nil
	}
	return []comparator{low, lt(ver(p.major+1, 0, 0, zeroPre))}, nil
}

// xRange is node-semver's replaceXRange: an operator and a partial, wildcards widened.
func xRange(op string, p partial) []comparator {
	if op == "=" && p.anyX() {
		op = ""
	}
	switch {
	case p.xMajor:
		if op == ">" || op == "<" {
			return nothing()
		}
		return []comparator{{any: true}}
	case op != "" && p.anyX():
		major, minor := p.major, p.minor
		if p.xMinor {
			minor = 0
		}
		var pre []string
		switch op {
		case ">":
			op = ">="
			if p.xMinor {
				major++
				minor = 0
			} else {
				minor++
			}
		case "<=":
			op = "<"
			if p.xMinor {
				major++
			} else {
				minor++
			}
		}
		if op == "<" {
			pre = zeroPre
		}
		return []comparator{{op: op, v: ver(major, minor, 0, pre)}}
	case p.xMinor:
		return []comparator{ge(ver(p.major, 0, 0, nil)), lt(ver(p.major+1, 0, 0, zeroPre))}
	case p.xPatch:
		return []comparator{ge(ver(p.major, p.minor, 0, nil)), lt(ver(p.major, p.minor+1, 0, zeroPre))}
	}
	if op == "" {
		op = "="
	}
	return []comparator{{op: op, v: p.version()}}
}

// hyphenRange is node-semver's hyphenReplace: `A - B`, inclusive at both ends, a partial B
// widened to its whole span.
func hyphenRange(fromS, toS string) ([]comparator, error) {
	from, err := readPartial(fromS)
	if err != nil {
		return nil, err
	}
	to, err := readPartial(toS)
	if err != nil {
		return nil, err
	}
	var out []comparator
	switch {
	case from.xMajor:
	case from.xMinor:
		out = append(out, ge(ver(from.major, 0, 0, nil)))
	case from.xPatch:
		out = append(out, ge(ver(from.major, from.minor, 0, nil)))
	default:
		out = append(out, ge(from.version()))
	}
	switch {
	case to.xMajor:
	case to.xMinor:
		out = append(out, lt(ver(to.major+1, 0, 0, zeroPre)))
	case to.xPatch:
		out = append(out, lt(ver(to.major, to.minor+1, 0, zeroPre)))
	default:
		out = append(out, comparator{op: "<=", v: to.version()})
	}
	if len(out) == 0 {
		return []comparator{{any: true}}, nil
	}
	return out, nil
}

// Satisfies reports whether v satisfies r: some comparator set holds for it, under the
// pre-release rule.
func (r Range) Satisfies(v Version) bool {
	for _, set := range r.sets {
		if testSet(set, v) {
			return true
		}
	}
	return false
}

// testSet is node-semver's testSet with includePrerelease off.
func testSet(set []comparator, v Version) bool {
	for _, c := range set {
		if !c.test(v) {
			return false
		}
	}
	if len(v.Pre) == 0 {
		return true
	}
	for _, c := range set {
		if c.any || len(c.v.Pre) == 0 {
			continue
		}
		if c.v.Major == v.Major && c.v.Minor == v.Minor && c.v.Patch == v.Patch {
			return true
		}
	}
	return false
}

// MaxSatisfying is the highest of versions that satisfies r, as node-semver's maxSatisfying.
func (r Range) MaxSatisfying(versions []Version) (Version, bool) {
	var best Version
	found := false
	for _, v := range versions {
		if !r.Satisfies(v) {
			continue
		}
		if !found || v.Compare(best) > 0 {
			best, found = v, true
		}
	}
	return best, found
}
