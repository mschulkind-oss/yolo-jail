package loopholedecl_test

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
)

// defaultenabled_test.go pins the OQ-A9 rename: `enabled` off the loophole MANIFEST,
// `default_enabled` on it, and the default flipped from ON to OFF
// (docs/design/loophole-activation.md R2).
//
// The property this file is really defending is that the two `enabled`s can no longer
// be confused. There are two switches with one old spelling — the manifest's, which is
// the PACK AUTHOR's default and is what moved, and the config's
// `loopholes.<name>.enabled`, which is the USER's and did not. Every assertion below is
// about the first one; the second is pinned in internal/loopholes and internal/config
// and must keep its spelling.

// bodyWith wraps a manifest fragment in the minimum a decode needs: a `name` matching
// the directory this file always decodes against.
func bodyWith(fragment string) []byte {
	body := `{"name": "probe", "transport": "none"`
	if fragment != "" {
		body += ", " + fragment
	}
	return []byte(body + "}")
}

const probeDir = "/loopholes/probe"

// TestAbsentDefaultEnabledMeansOff is R2 itself: "presence never activates". A manifest
// that says nothing about enablement declares a loophole that is OFF.
//
// Asserted on BOTH decoders because they are two entry points to one walk and only one
// of them (tolerant) is on the launch path — a default that held for `yolo pack lint`
// and not for discovery would be a default in name only.
func TestAbsentDefaultEnabledMeansOff(t *testing.T) {
	strictM, err := loopholedecl.Decode(bodyWith(""), probeDir)
	if err != nil {
		t.Fatalf("strict decode: %v", err)
	}
	if strictM.DefaultEnabled {
		t.Error("DefaultEnabled = true for a manifest that declares nothing; R2 says absent means OFF")
	}
	tolM, skipped, err := loopholedecl.DecodeTolerant(bodyWith(""), probeDir)
	if err != nil {
		t.Fatalf("tolerant decode: %v", err)
	}
	if len(skipped) != 0 {
		t.Errorf("tolerant decode skipped %v; nothing here is unknown", skipped)
	}
	if tolM.DefaultEnabled {
		t.Error("tolerant DefaultEnabled = true; the launch path must agree with `pack lint`")
	}
}

// TestDefaultEnabledIsHonoredBothWays: the key is read, not merely tolerated. `false`
// gets its own case because it is the value that would still pass if the decoder had
// dropped the key entirely — the absent-means-off default makes a broken reader look
// correct for exactly one of the two values.
func TestDefaultEnabledIsHonoredBothWays(t *testing.T) {
	for _, tc := range []struct {
		fragment string
		want     bool
	}{
		{`"default_enabled": true`, true},
		{`"default_enabled": false`, false},
	} {
		m, err := loopholedecl.Decode(bodyWith(tc.fragment), probeDir)
		if err != nil {
			t.Fatalf("%s: %v", tc.fragment, err)
		}
		if m.DefaultEnabled != tc.want {
			t.Errorf("%s: DefaultEnabled = %v, want %v", tc.fragment, m.DefaultEnabled, tc.want)
		}
	}
}

// TestDefaultEnabledMustBeABoolean pins the tightening the old key could never have.
//
// Truthy("false") is TRUE — a non-empty string — so under the loose coercion `enabled`
// used, the one slip a human writing this key is actually likely to make would turn the
// refusal into the grant. `default_enabled` is new, so no manifest can be relying on the
// coercion, and the direction the slip fails in is the one R4 exists to prevent: a
// quoted "false" would hand a jail host access on a manifest whose author wrote the word
// for refusing it.
func TestDefaultEnabledMustBeABoolean(t *testing.T) {
	for _, bad := range []string{`"default_enabled": "false"`, `"default_enabled": 0`, `"default_enabled": null`} {
		if _, err := loopholedecl.Decode(bodyWith(bad), probeDir); err == nil {
			t.Errorf("%s decoded without error; a non-boolean must be refused, not coerced", bad)
		} else if !strings.Contains(err.Error(), "must be a boolean") {
			t.Errorf("%s: error does not say what is wrong: %v", bad, err)
		}
	}
	// The refusal must not be so eager it rejects the legal values. Covered by
	// TestDefaultEnabledIsHonoredBothWays, restated here as the control for this test.
	if _, err := loopholedecl.Decode(bodyWith(`"default_enabled": true`), probeDir); err != nil {
		t.Fatalf("a legal boolean was refused: %v", err)
	}
}

// TestRetiredEnabledKeyIsRefusedAndNamesTheRename is the reverse of a tolerance, and
// the reason it has to be one is in the message it checks for.
//
// A REMOVED key cannot ride the unknown-key skew note: that note tells the reader "this
// build does not know it … a build that knows the key will read it", which is exactly
// backwards here — no future build will ever read `enabled`, it was deleted. So the key
// stays RECOGNIZED (out of KnownKeys, refused by name in the walk) and the error carries
// the migration, the flipped default, and the disambiguation from the config key.
func TestRetiredEnabledKeyIsRefusedAndNamesTheRename(t *testing.T) {
	for _, fragment := range []string{`"enabled": true`, `"enabled": false`} {
		strictErr := decodeErr(t, fragment, false)
		tolerantErr := decodeErr(t, fragment, true)
		// BOTH decoders, and this is the whole point of putting the refusal in the walk.
		// Tolerant is the launch path; if it shrugged, `enabled: true` would silently
		// become OFF and `enabled: false` would silently become "said nothing".
		for name, err := range map[string]error{"strict": strictErr, "tolerant": tolerantErr} {
			if err == nil {
				t.Fatalf("%s (%s): decoded without error; the retired key must be refused, not ignored",
					fragment, name)
			}
			msg := err.Error()
			for _, want := range []string{"default_enabled", "enabled"} {
				if !strings.Contains(msg, want) {
					t.Errorf("%s (%s): message does not name %q:\n%s", fragment, name, want, msg)
				}
			}
			// The rename is only half the news. A reader who writes `default_enabled`
			// with the same value they had is still wrong for the omitted case, so the
			// message has to say the default moved.
			if !strings.Contains(strings.ToUpper(msg), "FLIPPED") {
				t.Errorf("%s (%s): message does not say the default flipped:\n%s", fragment, name, msg)
			}
			// The single most likely way to misread this change is to "fix" the CONFIG
			// key too. The refusal says not to.
			if !strings.Contains(msg, "loopholes.<name>.enabled") {
				t.Errorf("%s (%s): message does not distinguish the unchanged CONFIG key:\n%s",
					fragment, name, msg)
			}
			// Not an unknown-key complaint. If this regressed, the key would be back in
			// topKeys or the census would be running first, and the tolerant reader would
			// be handing out the backwards "a newer build will read it" advice.
			if strings.Contains(msg, "unknown key") || strings.Contains(msg, "ignoring unknown key") {
				t.Errorf("%s (%s): reported as an unknown key rather than a retired one:\n%s",
					fragment, name, msg)
			}
		}
	}
}

// decodeErr runs one decoder over a fragment and returns only its error.
func decodeErr(t *testing.T, fragment string, tolerant bool) error {
	t.Helper()
	if tolerant {
		_, _, err := loopholedecl.DecodeTolerant(bodyWith(fragment), probeDir)
		return err
	}
	_, err := loopholedecl.Decode(bodyWith(fragment), probeDir)
	return err
}

// TestKnownKeysCarriesTheRename is the closed-census half: `default_enabled` must be in
// the key vocabulary or a strict decode reports yolo's own manifests as typos, and
// `enabled` must be OUT of it or an authoring tool suggests the spelling the walk
// refuses.
func TestKnownKeysCarriesTheRename(t *testing.T) {
	var hasNew, hasOld bool
	for _, k := range loopholedecl.KnownKeys() {
		switch k {
		case "default_enabled":
			hasNew = true
		case loopholedecl.RetiredKeyEnabled:
			hasOld = true
		}
	}
	if !hasNew {
		t.Errorf("KnownKeys() = %v, missing \"default_enabled\"", loopholedecl.KnownKeys())
	}
	if hasOld {
		t.Errorf("KnownKeys() = %v still advertises the retired %q; authoring tools suggest "+
			"spellings from this list, and this one is refused",
			loopholedecl.KnownKeys(), loopholedecl.RetiredKeyEnabled)
	}
}
