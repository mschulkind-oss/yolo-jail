package cli

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// TestConfigRefClaimsNoLoopholeReservation pins the config reference against the
// VALIDATOR rather than against itself, because that is the pair that drifted.
//
// `yolo config-ref` is the authority for config keys (AGENTS.md's own table says
// so), and until this test it told every reader that `loopholes` had a name they
// could not use: *"One name is reserved and cannot be used: \"claude-oauth-broker\",
// which yolo answers to itself."* That sentence outlived its mechanism by one
// commit. `loopholes.ReservedLoopholeNames` and the pack-vs-reserved half of the
// launch pre-flight were deleted on 2026-08-19 when the broker's manifest became a
// contribution of `packs/claude` — the broker was the set's last entry, so retiring
// it emptied the whole reserved namespace (docs/design/broker-as-a-pack.md §13). The
// reference was not touched in that commit.
//
// A stated refusal that does not happen is worse than an unstated one: it is the
// sentence a reader trusts INSTEAD of trying it, and what actually happens when they
// try is not a refusal but an inline loophole declared under the one name yolo still
// reaches by literal — `yolo broker status`, `yolo check`'s broker section, the
// host-singleton ensure and the in-jail terminator's endpoint variable all key on it.
//
// So the assertion is the behaviour, not the prose: whatever the reference says about
// reservations, it must be something the validator does. Today the validator reserves
// nothing, so the reference may not claim it does.
func TestConfigRefClaimsNoLoopholeReservation(t *testing.T) {
	// The behaviour half. If a reservation is ever reintroduced, this fails first and
	// the doc assertion below becomes the thing to update — which is the right order.
	raw := `{"loopholes": {"claude-oauth-broker": {"command": ["/bin/true"]}}}`
	decoded, err := jsonx.Decode([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	cfg, ok := decoded.(*jsonx.OrderedMap)
	if !ok {
		t.Fatal("fixture is not an object")
	}
	errs, _ := config.ValidateConfig(cfg, t.TempDir(), nil)
	reservationEnforced := len(errs) > 0

	// The doc half, matched on the CLAIM rather than on the name: any phrasing of
	// "this name is reserved" is the thing that has to be true.
	plain := Render(false)
	claimsReservation := strings.Contains(plain, "is reserved and cannot be used")

	if claimsReservation && !reservationEnforced {
		t.Error("yolo config-ref says a loopholes name \"is reserved and cannot be used\", but " +
			"ValidateConfig accepts it as an inline service — there is no reserved loophole " +
			"namespace left (loopholes.ReservedLoopholeNames was deleted with the broker's move " +
			"into packs/claude; docs/design/broker-as-a-pack.md §13). Either restore the " +
			"refusal or stop documenting one.")
	}
	if reservationEnforced && !claimsReservation {
		t.Error("ValidateConfig refuses a loopholes name that yolo config-ref does not " +
			"document as reserved — a refusal nobody can find is one the user hits at launch")
	}
}
