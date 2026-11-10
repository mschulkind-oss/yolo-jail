package model

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"unicode/utf8"
)

// SecretFlags are the options whose value is masked in the page: the same list as yolo's
// summaryArgv (internal/brokerstore/watch.go at yolo 04f300a9e), which hides option values that
// commonly carry request bodies, headers or input paths. A test pins the two together.
var SecretFlags = []string{"--body", "--body-file", "--field", "-F", "--raw-field", "-f", "--header", "-H", "--input"}

// GluedFlags are the short options whose value may be attached ("-Fvalue"); yolo's summaryArgv
// treats exactly these three that way.
var GluedFlags = []string{"-F", "-f", "-H"}

// ArgvItem is one argv value as the page receives it. A Secret item is shown masked until the
// reader clicks Show; its Value still reaches the page (masking defends against eyes, not
// against the tab's own script), made safe by Visible.
type ArgvItem struct {
	Prefix string `json:"prefix"` // "--body=" or "-F" for a joined or glued secret; else empty
	Value  string `json:"value"`
	Secret bool   `json:"secret"`
	Length int    `json:"length"` // characters in the secret value
	SHA    string `json:"sha"`    // first 8 hex digits of its sha256
}

// MaskText is the placeholder shown for a secret value.
func (a ArgvItem) MaskText() string {
	return fmt.Sprintf("[hidden: %d characters, sha256 %s]", a.Length, a.SHA)
}

func secretItem(prefix, value string) ArgvItem {
	sum := sha256.Sum256([]byte(value))
	return ArgvItem{Prefix: Visible(prefix), Value: Visible(value), Secret: true,
		Length: utf8.RuneCountInString(value), SHA: fmt.Sprintf("%x", sum[:4])}
}

func isSecretFlag(arg string) bool {
	for _, f := range SecretFlags {
		if arg == f {
			return true
		}
	}
	return false
}

// MaskArgv classifies every argv value by the same rule as yolo's summaryArgv: the value
// after a secret flag, the "--flag=value" form of each secret flag, and the "-Fvalue" glued
// form of -F, -f and -H.
func MaskArgv(argv []string) []ArgvItem {
	out := make([]ArgvItem, 0, len(argv))
	next := false
	for _, arg := range argv {
		if next {
			out = append(out, secretItem("", arg))
			next = false
			continue
		}
		if isSecretFlag(arg) {
			out = append(out, ArgvItem{Value: Visible(arg)})
			next = true
			continue
		}
		done := false
		for _, f := range SecretFlags {
			if p := f + "="; strings.HasPrefix(arg, p) {
				out = append(out, secretItem(p, arg[len(p):]))
				done = true
				break
			}
		}
		if !done {
			for _, f := range GluedFlags {
				if strings.HasPrefix(arg, f) && len(arg) > len(f) {
					out = append(out, secretItem(f, arg[len(f):]))
					done = true
					break
				}
			}
		}
		if !done {
			out = append(out, ArgvItem{Value: Visible(arg)})
		}
	}
	return out
}

// MaskedCommand renders the command for reading from the masked argv, so the raw "command"
// string yolo prints, which holds every secret value, is never published.
func MaskedCommand(argv []string) string {
	parts := make([]string, 0, len(argv))
	for _, it := range MaskArgv(argv) {
		if it.Secret {
			parts = append(parts, it.Prefix+it.MaskText())
		} else {
			parts = append(parts, quote(it.Value))
		}
	}
	return strings.Join(parts, " ")
}

func quote(s string) string {
	if s == "" {
		return "''"
	}
	plain := true
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("_@%+=:,./-", r)) {
			plain = false
			break
		}
	}
	if plain {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
