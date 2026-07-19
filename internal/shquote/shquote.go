// Package shquote implements POSIX shell quoting (shlex.quote / shlex.join).
// This is correctness-critical: commands round-trip through `bash -c` in-jail,
// so a quoting error can silently corrupt or inject argv. Pinned by a golden
// table over adversarial argv (shquote_test.go).
//
// Reference: CPython Lib/shlex.py:
//
//	_find_unsafe = re.compile(r'[^\w@%+=:,./-]', re.ASCII).search
//	def quote(s):
//	    if not s: return "''"
//	    if _find_unsafe(s) is None: return s
//	    return "'" + s.replace("'", "'\"'\"'") + "'"
//	join = ' '.join(quote(x) for x in split_command)
package shquote

import "strings"

// isSafe reports whether r is in shlex's ASCII "safe" set: word characters
// (ASCII letters, digits, underscore) plus @ % + = : , . / -. re.ASCII means
// \w is ONLY [A-Za-z0-9_] — no unicode letters — so any non-ASCII rune is
// unsafe and forces quoting.
func isSafe(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z':
		return true
	case r >= 'A' && r <= 'Z':
		return true
	case r >= '0' && r <= '9':
		return true
	}
	switch r {
	case '_', '@', '%', '+', '=', ':', ',', '.', '/', '-':
		return true
	}
	return false
}

// Quote returns a shell-escaped version of s, byte-identical to shlex.quote.
func Quote(s string) string {
	if s == "" {
		return "''"
	}
	safe := true
	for _, r := range s {
		if !isSafe(r) {
			safe = false
			break
		}
	}
	if safe {
		return s
	}
	// Single-quote, and put embedded single quotes into double quotes:
	// the string $'b is quoted as '$'"'"'b'.
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}

// Join returns the args joined with spaces, each Quote'd — byte-identical to
// shlex.join. An empty arg list yields "".
func Join(args []string) string {
	parts := make([]string, len(args))
	for i, a := range args {
		parts[i] = Quote(a)
	}
	return strings.Join(parts, " ")
}
