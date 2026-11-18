// Package brokerpolicy contains pure declaration-owned scope matching. Matching assumes
// admission has already been proved by the caller; it never admits a resource.
package brokerpolicy

import (
	"fmt"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ScopeMatcher is immutable after construction; anchors are copied on entry.
type ScopeMatcher struct {
	nesting   string
	separator byte
	anchors   []string
}

func NewScopeMatcher(nesting, separator string, anchors []string) (ScopeMatcher, error) {
	if nesting == "" {
		nesting = "exact"
	}
	m := ScopeMatcher{nesting: nesting, anchors: slices.Clone(anchors)}
	switch nesting {
	case "exact":
		if separator != "" || len(anchors) != 0 {
			return m, fmt.Errorf("exact nesting takes no separator or anchors; remove those fields")
		}
	case "segment":
		if len(separator) != 1 || separator[0] < 32 || separator[0] > 126 || separator[0] >= 'a' && separator[0] <= 'z' || separator[0] >= 'A' && separator[0] <= 'Z' || separator[0] >= '0' && separator[0] <= '9' {
			return m, fmt.Errorf("segment separator must be one printable ASCII non-alphanumeric byte; repair the declaration")
		}
		m.separator = separator[0]
		if len(anchors) == 0 {
			return m, fmt.Errorf("segment nesting requires nonempty anchors; declare literal prefixes")
		}
		for i, anchor := range anchors {
			if err := ValidateText(anchor); err != nil {
				return m, fmt.Errorf("anchor %d: %w", i, err)
			}
			for j, other := range anchors {
				if i != j && strings.HasPrefix(anchor, other) {
					return m, fmt.Errorf("anchor %q prefixes %q; declare nonoverlapping anchors", other, anchor)
				}
			}
		}
	default:
		return m, fmt.Errorf("unknown nesting %q; use exact or segment", nesting)
	}
	return m, nil
}

// ValidateText is the byte-length and Unicode control bound shared by scopes and
// nonsecret context values. It does not trim, normalize or interpret the text.
func ValidateText(value string) error {
	if value == "" || !utf8.ValidString(value) || len(value) > 1024 {
		return fmt.Errorf("must be nonempty valid UTF-8 of at most 1024 bytes; use yolo config set with a valid value")
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return fmt.Errorf("must contain no control characters; use yolo config set with a valid value")
		}
	}
	return nil
}

func (m ScopeMatcher) ValidateScope(scope string) error {
	if err := ValidateText(scope); err != nil {
		return err
	}
	if m.nesting == "segment" {
		matched := 0
		for _, anchor := range m.anchors {
			if strings.HasPrefix(scope, anchor) {
				matched++
			}
		}
		if matched != 1 {
			return fmt.Errorf("scope must start with exactly one declared anchor; use yolo config set with a declared prefix")
		}
	}
	return nil
}

func (m ScopeMatcher) Contains(scope, id string) bool {
	if scope == id {
		return true
	}
	return m.nesting == "segment" && strings.HasPrefix(id, scope) && len(scope) > 0 && (scope[len(scope)-1] == m.separator || len(id) > len(scope) && id[len(scope)] == m.separator)
}

// ScopeMatch orders scopes by first contained chain position (leaf is zero), then
// descending byte length. The caller handles the source scope last, separately.
type ScopeMatch struct {
	Scope         string
	ChainPosition int
	ByteLength    int
}

func (m ScopeMatcher) ApplyingScopes(scopes, chain []string) []ScopeMatch {
	var matches []ScopeMatch
	for _, scope := range scopes {
		for i, id := range chain {
			if m.Contains(scope, id) {
				matches = append(matches, ScopeMatch{Scope: scope, ChainPosition: i, ByteLength: len(scope)})
				break
			}
		}
	}
	slices.SortStableFunc(matches, func(a, b ScopeMatch) int {
		if a.ChainPosition != b.ChainPosition {
			return a.ChainPosition - b.ChainPosition
		}
		return b.ByteLength - a.ByteLength
	})
	return matches
}

// RootRelation is report metadata, never admission evidence. Exact non-root
// scopes are classifier-conditional; segment overlap is tested both ways.
func (m ScopeMatcher) RootRelation(scope string, roots []string) string {
	for _, root := range roots {
		if scope == root {
			return "root"
		}
	}
	if m.nesting != "segment" {
		return "classifier-conditional"
	}
	for _, root := range roots {
		if m.Contains(scope, root) || m.Contains(root, scope) {
			return "overlap"
		}
	}
	return "inert"
}
