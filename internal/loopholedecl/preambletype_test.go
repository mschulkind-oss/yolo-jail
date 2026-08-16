package loopholedecl_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
)

// TestHostDaemonPreambleValueMustBeABoolean closes the OTHER half of getting this
// key wrong. TestHostDaemonPreambleTypoIsUnknown makes a misspelled KEY loud; a
// mistyped VALUE was silent, and it is the likelier slip by a wide margin.
//
// `Truthy("false")` is TRUE — a non-empty string — so a manifest saying
// `"preamble": "false"` used to decode with the preamble ON: the exact inverse of
// what its author wrote, on the key whose whole purpose is to protect a daemon
// that cannot survive an extra frame. Nothing downstream could report it. `yolo
// pack lint`'s strict decode reports unknown KEYS, not wrong types; the run
// pipeline just reads a bool off the record; and the consequence lands inside a
// third-party daemon, which for the common one-frame-per-request shape consumes
// yolo's preamble AS the request and then blocks forever on a request already
// spent (hostservice.ServeFrontedUnix, "the one mismatch nothing in this tree can
// detect"). No readiness probe fails, no access line is written, nothing logs —
// the only symptom is a jail request that never answers.
//
// internal/config already guards the CONFIG spelling of this key against exactly
// this input (validateInlineService's isBool check, added in the same change);
// this is the manifest side of the same rule, and host_bind_mounts.readonly is
// the in-file precedent — a default-TRUE bool whose wrong value is silent gets a
// type check rather than a coercion.
func TestHostDaemonPreambleValueMustBeABoolean(t *testing.T) {
	for _, tc := range []struct {
		name string
		val  any
	}{
		// THE ONE THAT MATTERS: quoted, and it used to mean the opposite.
		{"quoted false", "false"},
		{"quoted true", "true"},
		{"zero", 0},
		{"empty string", ""},
		{"null", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manifest := map[string]any{
				"name": "typed-pre", "description": "x",
				"host_daemon": map[string]any{
					"cmd":       []any{"d", "{socket}"},
					"publishes": "socket",
					"preamble":  tc.val,
				},
			}
			_, err := decodeMap(t, "typed-pre", manifest)
			if err == nil {
				t.Fatalf("decode accepted a non-boolean preamble (%#v); a quoted "+
					"\"false\" then means the preamble is ON, which is the inverse of "+
					"what the author wrote and is undetectable downstream", tc.val)
			}
			if !strings.Contains(err.Error(), "host_daemon.preamble") {
				t.Errorf("error does not name the key: %s", err.Error())
			}
			if !strings.Contains(err.Error(), "boolean") {
				t.Errorf("error does not say what is wrong with the value: %s", err.Error())
			}

			// AND THE TOLERANT DECODE REFUSES IT TOO. This is not an unknown-key
			// skew note: a launch that shrugged and took the default would put the
			// preamble back ON for the daemon whose author was trying to turn it
			// off — the precise failure being refused above, reintroduced by the
			// path that actually runs at boot.
			if _, _, terr := loopholedecl.DecodeTolerant(
				manifestBytes(t, manifest), filepath.Join("/loopholes", "typed-pre")); terr == nil {
				t.Error("the tolerant decode accepted a non-boolean preamble; a wrong TYPE " +
					"is an author error at any strictness, unlike an unknown key")
			}
		})
	}
}

// TestHostDaemonPreambleBooleansStillDecode is the anti-overreach half: the check
// above must refuse only the wrong TYPE, never a legitimate declaration.
func TestHostDaemonPreambleBooleansStillDecode(t *testing.T) {
	for _, want := range []bool{true, false} {
		m, err := decodeMap(t, "typed-pre", map[string]any{
			"name": "typed-pre", "description": "x",
			"host_daemon": map[string]any{
				"cmd":       []any{"d", "{socket}"},
				"publishes": "socket",
				"preamble":  want,
			},
		})
		if err != nil {
			t.Fatalf("decode refused \"preamble\": %v: %v", want, err)
		}
		if m.HostDaemon.Preamble != want {
			t.Errorf("Preamble = %v, want %v", m.HostDaemon.Preamble, want)
		}
	}
}
