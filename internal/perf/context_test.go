package perf

import (
	"regexp"
	"strings"
	"testing"
)

var traceparentForm = regexp.MustCompile(`^00-[0-9a-f]{32}-[0-9a-f]{16}-01$`)

func TestContextRoundTripsThroughItsStringForm(t *testing.T) {
	c := Context{Trace: MintTrace().ID, Span: newSpanID()}
	s := c.String()
	if !traceparentForm.MatchString(s) {
		t.Fatalf("context string %q is not the W3C traceparent form 00-<32 hex>-<16 hex>-01", s)
	}
	got, ok := ParseContext(s)
	if !ok || got != c {
		t.Fatalf("ParseContext(%q) = %+v, %v; want %+v, true", s, got, ok, c)
	}
	if j := got.Join(); j.ID != c.Trace || j.Parent != c.Span {
		t.Errorf("Join() = %+v; want the context's trace with its span as the lane's parent", j)
	}
}

func TestParseContextRefusesMalformedStrings(t *testing.T) {
	good := "00-" + strings.Repeat("ab", 16) + "-" + strings.Repeat("cd", 8) + "-01"
	if _, ok := ParseContext(good); !ok {
		t.Fatalf("the well-formed fixture %q must parse", good)
	}
	for name, s := range map[string]string{
		"empty":              "",
		"short":              good[:54],
		"long":               good + "0",
		"uppercase trace":    "00-" + strings.Repeat("AB", 16) + "-" + strings.Repeat("cd", 8) + "-01",
		"uppercase span":     "00-" + strings.Repeat("ab", 16) + "-" + strings.Repeat("CD", 8) + "-01",
		"uppercase flags":    good[:53] + "0A",
		"wrong version":      "01" + good[2:],
		"version ff":         "ff" + good[2:],
		"non-hex digit":      "00-" + strings.Repeat("ag", 16) + "-" + strings.Repeat("cd", 8) + "-01",
		"misplaced dash":     "00_" + good[3:],
		"zero trace id":      "00-" + strings.Repeat("0", 32) + "-" + strings.Repeat("cd", 8) + "-01",
		"zero span id":       "00-" + strings.Repeat("ab", 16) + "-" + strings.Repeat("0", 16) + "-01",
		"multibyte in place": "00-" + strings.Repeat("ab", 15) + "é" + "-" + strings.Repeat("cd", 8) + "-01",
	} {
		t.Run(name, func(t *testing.T) {
			got, ok := ParseContext(s)
			if ok || got != (Context{}) {
				t.Errorf("ParseContext(%q) = %+v, %v; want no context", s, got, ok)
			}
		})
	}
}

func TestInvalidContextHasNoStringForm(t *testing.T) {
	for _, c := range []Context{{}, {Trace: MintTrace().ID}, {Span: newSpanID()}} {
		if c.String() != "" {
			t.Errorf("context %+v names no trace and span, so it has no string form; got %q", c, c.String())
		}
	}
}

func TestMintedIDsAreNonZeroAndDistinct(t *testing.T) {
	seen := map[TraceID]bool{}
	for range 64 {
		id := MintTrace().ID
		if id.IsZero() || seen[id] {
			t.Fatalf("MintTrace gave %s: a trace id is random and never zero", id)
		}
		seen[id] = true
	}
	if p := MintTrace().Parent; !p.IsZero() {
		t.Errorf("a minted trace has no parent; got %s", p)
	}
}
