// Package packname is the spelling of every name an AGENT sees for a pack's content: a skill, a
// wrapped plugin, a `files` landing and a `/ctx/host-…` directory (docs/reference/pack-system.md#delivered-names).
//
// Every such name is lowercase letters, digits and single hyphens, at most MaxLen characters — the
// only spelling every agent yolo delivers to carries (pack-system.md#pn-p4). A name the USER writes
// (a `[pack_sources]` key, a `namespace` value, `shipped_skill_prefix`) must already be in that
// spelling and is refused otherwise (Word); a name an AUTHOR chose (a pack or skill directory, a
// plugin manifest's name) is normalized into it (Normalize).
package packname

import (
	"fmt"
	"strings"
)

// MaxLen is the longest delivered name: the strictest limit any agent states (pack-system.md#pn-p4).
const MaxLen = 64

// Normalize is an author's name in the agent-facing spelling: lowercased, every character other
// than [a-z0-9] turned into "-", runs of "-" collapsed, and leading and trailing "-" trimmed. It
// may return "" for a name with no letter or digit.
func Normalize(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			if dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			dash = false
			b.WriteRune(r)
			continue
		}
		dash = true
	}
	return b.String()
}

// IsWord reports whether s is already a legal user-written name: non-empty and its own
// normalization, so of [a-z0-9-] with no leading, trailing or doubled "-".
func IsWord(s string) bool { return s != "" && Normalize(s) == s }

// Word refuses a user-written name that is not IsWord, naming the legal spelling when there is one.
// what names the field ("pack_sources key", "namespace"), for the message.
func Word(what, s string) error {
	if IsWord(s) {
		if len(s) > MaxLen {
			return fmt.Errorf("%s %q is longer than %d characters; choose a shorter one", what, s, MaxLen)
		}
		return nil
	}
	legal := Normalize(s)
	if legal == "" {
		return fmt.Errorf("%s %q must be lowercase letters, digits and single hyphens", what, s)
	}
	return fmt.Errorf("%s %q must be lowercase letters, digits and single hyphens; write %q",
		what, s, legal)
}

// Delivered is the name an agent sees for an author's name under a namespace: `<namespace>-<name>`,
// with name normalized, or name EXACTLY as the author wrote it when namespace is "" (a bare entry,
// or a shipped pack). A bare name is left alone so a converted config, which the migration marks
// bare, delivers every name it delivered before (pack-system.md PN-D7); namespace is already a word.
func Delivered(namespace, name string) string {
	if namespace == "" {
		return name
	}
	n := Normalize(name)
	if n == "" {
		return namespace
	}
	return namespace + "-" + n
}

// TooLong is the refusal for a delivered name past MaxLen, or nil.
func TooLong(delivered, pack, what string) error {
	if len(delivered) <= MaxLen {
		return nil
	}
	return fmt.Errorf("pack %s: the %s name %q is %d characters, longer than the %d every agent "+
		"accepts — choose a shorter namespace for the pack, or shorten the name in the pack",
		pack, what, delivered, len(delivered), MaxLen)
}
