package cli

// packkinddocs_test.go is the DRIFT GATE between the closed contribution-kind set and the
// two hand-written docs that enumerate it: `yolo config-ref` (config_ref.txt) and
// `yolo pack --help` (packUsage).
//
// Why this exists: the kind set is machine-enumerable (packdecl.KnownKinds()) but both docs
// list it as prose, so every kind added since the lists were written silently went
// undocumented. When this test was added, `config-ref` was missing `autonomy` (12 of 13
// listed) and `pack --help` was missing BOTH `autonomy` and `config-overlay` (11 of 13) —
// and the same gap had already been reported and fixed once before, which is the signature
// of a missing test rather than a careless edit.
//
// The fix is structural: a 14th kind now fails `just test-fast` until it is documented in
// both places. That is cheaper than the alternative (a user discovering a kind exists by
// reading packs/*/pack.json, which is how `autonomy`'s schema had to be learned).

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// TestEveryKindIsDocumented asserts each kind in the closed set is named in both
// user-facing kind listings. It checks for the kind NAME as a standalone token, which is
// what a reader scans for; it deliberately does not try to validate the surrounding
// description, since prose quality is not testable and the failure mode being prevented is
// total absence.
func TestEveryKindIsDocumented(t *testing.T) {
	docs := map[string]string{
		"config_ref.txt (yolo config-ref)": configRefContent,
		"packUsage (yolo pack --help)":     packUsage,
	}
	for _, kind := range packdecl.KnownKinds() {
		name := string(kind)
		for label, content := range docs {
			if !mentionsKind(content, name) {
				t.Errorf("kind %q is not documented in %s — add it to the kind list "+
					"(every kind in packdecl.KnownKinds() must appear in both docs)", name, label)
			}
		}
	}
}

// mentionsKind reports whether doc names the kind as its own token. `config` is a substring
// of `config-overlay`, so a naive strings.Contains would let a doc listing only
// config-overlay "document" config; requiring a non-name character (or a boundary) on both
// sides avoids that false pass.
func mentionsKind(doc, name string) bool {
	for i := 0; ; {
		idx := strings.Index(doc[i:], name)
		if idx < 0 {
			return false
		}
		start := i + idx
		end := start + len(name)
		if isKindTokenBoundary(doc, start-1) && isKindTokenBoundary(doc, end) {
			return true
		}
		i = start + 1
	}
}

// isKindTokenBoundary reports whether the byte at pos is outside a kind name — i.e. not a
// letter or a hyphen. An out-of-range pos (start or end of the doc) counts as a boundary.
func isKindTokenBoundary(doc string, pos int) bool {
	if pos < 0 || pos >= len(doc) {
		return true
	}
	c := doc[pos]
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c == '-':
		return false
	}
	return true
}
