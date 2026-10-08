package packload

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// buildLineDigestLen is how many hex characters of a build line's SHA-256 a launch shows: 48 bits,
// enough that a changed line reads as a new digest to a person comparing two launches, and short
// enough for the disclosure block's line. An implementation choice under OQ-RO9
// (docs/reference/report-tiers.md), which rules the digest and leaves its length open.
const buildLineDigestLen = 12

// BuildLineDigest is a build line's recipe digest: the SHA-256 of the WHOLE line, shortened. Never
// a digest of a prefix, so a payload at any character of the line moves it (OQ-RO9: "the digest is
// taken over the whole line, never over a prefix of it").
func BuildLineDigest(line string) string {
	sum := sha256.Sum256([]byte(line))
	return hex.EncodeToString(sum[:])[:buildLineDigestLen]
}

// BuildLineQuote is how a claim's Detail quotes its build line: "built by `<line>`". The launch's
// rendering replaces exactly this (LaunchDisclosureSentence).
func BuildLineQuote(line string) string { return "built by `" + line + "`" }

// BuildLineReference is what a launch shows in a build line's place: its digest, and the command
// that prints the line whole.
func BuildLineReference(key, line string) string {
	return "built by recipe " + BuildLineDigest(line) + " (`yolo pack status " + key + "` prints its build line)"
}

// LaunchDisclosureSentence is c's sentence as a launch's disclosure block prints it: the
// DisclosureSentence, with a fork's or built tree's build line named by its recipe digest and
// `yolo pack status <key>` instead of printed again (OQ-RO9, ruled (c) 2026-10-05). Every act that
// builds prints the line whole before it runs, so a new or changed line is seen whole once, and
// on demand after that. A sentence that does not quote the line as its Detail does is returned
// whole: falling back to the full line is always safe, and truncating it never is.
func (c Claim) LaunchDisclosureSentence() string {
	s := c.DisclosureSentence()
	if c.BuildLine == "" || c.BuildKey == "" {
		return s
	}
	q := BuildLineQuote(c.BuildLine)
	if !strings.Contains(s, q) {
		return s
	}
	// The LAST occurrence: every Detail quotes the build line after its source, so a source that
	// happened to contain the same text is never the one rewritten.
	i := strings.LastIndex(s, q)
	return s[:i] + BuildLineReference(c.BuildKey, c.BuildLine) + s[i+len(q):]
}
