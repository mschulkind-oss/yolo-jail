package main

import (
	"regexp"
	"strings"
)

// The wheel's METADATA carries README.md as the PyPI project description, and pypi.org
// renders it at pypi.org/project/yolo-jail/, where every repository-relative link in it
// resolves under pypi.org and 404s: LICENSE, the user guide, every doc the README points at.
// So the builder pins each such link to the release tag's tree on GitHub before it writes
// METADATA — the same move scripts/changelog-section.sh --link-base makes for a GitHub
// release body.
//
// Implementation decisions, recorded here because this package's doc comments are the only
// document it has:
//
//   - The tag is derived from --version as `v<version>`, the one tag shape `just release`
//     cuts, and the publish workflow only ever builds from that tag. A local build with an
//     invented version gets links to a tag that does not exist, which is harmless: nothing
//     local is published.
//   - A link becomes `https://github.com/<repo>/blob/<tag>/<path>`, and an image
//     `https://raw.githubusercontent.com/<repo>/<tag>/<path>`, because a blob URL serves an
//     HTML page that an <img> cannot display.
//   - A bare fragment (`#agents`) points at the README on GitHub, since PyPI does not give
//     headings the ids GitHub does.
//   - Absolute targets (any URL scheme, or `//host`) are left alone, and so is everything
//     inside a fenced code block or an inline code span, where a `](x)` is text, not a link.
//   - Inline links and images, and reference definitions (`[id]: target`), are rewritten.
//     Link text may hold one level of nested brackets, which is all the README uses.

const (
	repoSlug = "mschulkind-oss/yolo-jail"
	blobBase = "https://github.com/" + repoSlug + "/blob/"
	rawBase  = "https://raw.githubusercontent.com/" + repoSlug + "/"
)

var (
	// inlineLinkRe matches `[text](target` or `![alt](target`: group 1 is the `!` of an
	// image, group 2 the target up to whitespace or `)`, so a title after it is kept.
	inlineLinkRe = regexp.MustCompile(`(!?)\[(?:[^\[\]]|\[[^\[\]]*\])*\]\(([^)\s]+)`)
	// refDefRe matches a reference definition line: `[id]: target`, indented at most 3.
	refDefRe = regexp.MustCompile(`^( {0,3}\[[^\]]+\]:[ \t]*)(\S+)(.*)$`)
	// schemeRe is an absolute URL's scheme.
	schemeRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]*:`)
	// fenceRe opens or closes a fenced code block.
	fenceRe = regexp.MustCompile("^ {0,3}(```|~~~)")
	// codeSpanRe is a single-backtick inline code span, the only kind the README uses.
	codeSpanRe = regexp.MustCompile("`[^`]*`")
)

// pinReadmeLinks rewrites README's repository-relative link targets to the tag's tree.
func pinReadmeLinks(readme, version string) string {
	tag := "v" + strings.TrimPrefix(version, "v")
	resolve := func(target string, image bool) string {
		if schemeRe.MatchString(target) || strings.HasPrefix(target, "//") {
			return target
		}
		if strings.HasPrefix(target, "#") {
			return blobBase + tag + "/README.md" + target
		}
		target = strings.TrimPrefix(strings.TrimPrefix(target, "./"), "/")
		if image {
			return rawBase + tag + "/" + target
		}
		return blobBase + tag + "/" + target
	}

	lines := strings.Split(readme, "\n")
	fenced := false
	for i, line := range lines {
		if fenceRe.MatchString(line) {
			fenced = !fenced
			continue
		}
		if fenced {
			continue
		}
		if m := refDefRe.FindStringSubmatch(line); m != nil {
			lines[i] = m[1] + resolve(m[2], false) + m[3]
			continue
		}
		// A link whose `[` sits inside an inline code span is text. Deciding by where the
		// match STARTS, rather than splitting the line on backticks, keeps a link whose
		// text is itself code — [`flake.nix`](flake.nix), the README's commonest shape.
		spans := codeSpanRe.FindAllStringIndex(line, -1)
		var b strings.Builder
		last := 0
		for _, m := range inlineLinkRe.FindAllStringSubmatchIndex(line, -1) {
			if insideSpan(spans, m[0]) {
				continue
			}
			image := m[3] > m[2]
			b.WriteString(line[last:m[4]])
			b.WriteString(resolve(line[m[4]:m[5]], image))
			last = m[5]
		}
		b.WriteString(line[last:])
		lines[i] = b.String()
	}
	return strings.Join(lines, "\n")
}

func insideSpan(spans [][]int, at int) bool {
	for _, s := range spans {
		if at >= s[0] && at < s[1] {
			return true
		}
	}
	return false
}
