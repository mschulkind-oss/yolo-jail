package packdecl

// platform.go is the SHAPE rule for a provider's `platform` (docs/design/
// providers-and-profiles-redesign.md OQ-BR2, ruled 2026-09-29): the one statement of what a
// platform value may look like, read by the manifest validator and by internal/config's check
// of a user's own `providers.<name>.platform`, so the two layers of one composed table cannot
// accept different spellings of one field.
//
// The VOCABULARY is open and deliberately not checked here: which platforms exist is the
// consuming derive's business, and an unknown value is inert (Contribution.Platform says why).
// What IS checked is that the value could ever equal one a derive compares against: a
// platform is one token, so an empty value or one carrying whitespace names no service a
// consumer could recognize, and accepting it would be a declaration that silently does
// nothing.

import (
	"fmt"
	"strings"
	"unicode"
)

// PlatformProblem reports what is wrong with a platform value at path ("" when nothing is): it
// must be a non-empty token with no whitespace in it.
func PlatformProblem(path, v string) string {
	switch {
	case v == "":
		return path + ": an empty platform names no service — omit the key instead"
	case strings.IndexFunc(v, unicode.IsSpace) >= 0:
		return fmt.Sprintf("%s: %q carries whitespace, so it could never equal a platform a "+
			"derive recognizes (a platform is one token, such as \"aws-bedrock\")", path, v)
	}
	return ""
}
