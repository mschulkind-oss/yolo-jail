// Package slug is THE one escaping that turns a name into a single path component: a pack's
// staging directory (config.PackEntry.Slug), a host file's surface name
// (config.HostFileEntry.Slug), and every directory or plugin name yolo derives from a pack name
// that may hold a "/" — a readable-config pack is named by its source handle, `local/matt` or
// `yolo-packs/pi-fork` (OQ-RC15, docs/design/readable-config.md).
//
// One function, so one name has one spelling wherever it becomes a path: a pack's staged tree,
// its namespaced skills subtree and its `files` landing all read `yolo-packs_2fpi-fork`, never a
// second variant a reader has to map back (RC-D48).
//
// [A-Za-z0-9.-] pass through unchanged; every other byte, '_' itself included, becomes "_hh" (two
// lowercase hex digits). '_' is the SOLE escape sentinel, which is what makes the map reversible
// and therefore injective: two different names never share a slug. The result never holds a path
// separator, and is non-empty for a non-empty name.
package slug

import (
	"fmt"
	"strings"
)

// Escape is s as one path component, by the scheme in the package comment.
func Escape(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 8)
	for i := 0; i < len(s); i++ {
		c := s[i]
		if passes(c) {
			b.WriteByte(c)
			continue
		}
		fmt.Fprintf(&b, "_%02x", c)
	}
	return b.String()
}

// Unescape inverts Escape: the name s was escaped from, and false for a string Escape cannot have
// produced.
func Unescape(s string) (string, bool) {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '_':
			if i+2 >= len(s) {
				return "", false
			}
			var v byte
			if _, err := fmt.Sscanf(s[i+1:i+3], "%02x", &v); err != nil {
				return "", false
			}
			b.WriteByte(v)
			i += 2
		case passes(c):
			b.WriteByte(c)
		default:
			return "", false
		}
	}
	out := b.String()
	if out == "" || Escape(out) != s {
		return "", false
	}
	return out, true
}

func passes(c byte) bool {
	return c == '.' || c == '-' ||
		(c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}
