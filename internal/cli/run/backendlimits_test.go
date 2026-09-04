package run

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// limitPack builds a pack declaring a writable state dir and a reads-host grant —
// the two contributions whose macos-user behaviour differs from every other backend.
func limitPack(t *testing.T) *packload.Pack {
	t.Helper()
	// MayAccessHost: HonoredHostFiles refuses a FETCHED pack's grants outright, so a
	// pack without it produces no ungranted list and the test would pass vacuously.
	return &packload.Pack{Name: "acme", Root: t.TempDir(), MayAccessHost: true, Decl: &packdecl.Manifest{
		Name: "acme",
		Contributes: []packdecl.Contribution{
			{Kind: packdecl.KindState, At: ".acme", Scope: "workspace"},
			{Kind: packdecl.KindReadsHost, Host: ".acme/settings.json", From: ".acme/settings.json",
				Into: "settings.json"},
		},
	}}
}

// Container backends impose no standing constraints beyond what the rest of the
// briefing already says, so they get nothing — a section that always renders trains
// the reader to skip it.
func TestBackendLimitsAreEmptyForContainerBackends(t *testing.T) {
	for _, rt := range []string{"podman", "container"} {
		if got := backendLimits(rt, []*packload.Pack{limitPack(t)}, jsonx.NewOrderedMap()); len(got) != 0 {
			t.Errorf("%s: got %d limits, want none: %v", rt, len(got), got)
		}
	}
}

// The three facts an agent reasons WRONGLY from without them. Each is printed at
// launch to stderr, where the human reads it and the agent never does.
func TestBackendLimitsTellTheAgentWhatStderrTellsTheHuman(t *testing.T) {
	got := strings.Join(backendLimits("macos-user",
		[]*packload.Pack{limitPack(t)}, jsonx.NewOrderedMap()), "\n")

	// The home is machine-wide: an agent believing it is its own writes project state
	// into a directory every other workspace reads.
	if !strings.Contains(got, "SHARED by every workspace") {
		t.Errorf("does not say the home is shared:\n%s", got)
	}
	// Config rendered from DEFAULTS: an agent reading its own settings.json otherwise
	// takes it for the human's preferences and acts on them.
	if !strings.Contains(got, "DEFAULTS") {
		t.Errorf("does not say the agent config is not the human's:\n%s", got)
	}
	// Content is a writable copy: a skill the agent edits is silently overwritten.
	if !strings.Contains(got, "writable COPY") {
		t.Errorf("does not say content is a writable copy:\n%s", got)
	}
	// The in-jail loophole clients have nothing to talk to.
	if !strings.Contains(got, "yolo-ps") {
		t.Errorf("does not say the loophole clients are inert:\n%s", got)
	}
}

// A jail with no packs has no shared state, no ungranted host files and no copied
// content — so only the loophole fact survives. A limit list that reported
// constraints a jail does not have would be the same overclaim in a new place.
func TestBackendLimitsScaleWithWhatIsActuallyThere(t *testing.T) {
	got := backendLimits("macos-user", nil, jsonx.NewOrderedMap())
	joined := strings.Join(got, "\n")
	if strings.Contains(joined, "SHARED by every workspace") {
		t.Errorf("claimed shared state dirs for a jail with no packs:\n%s", joined)
	}
	if strings.Contains(joined, "DEFAULTS") {
		t.Errorf("claimed ungranted host files for a jail with no packs:\n%s", joined)
	}
}
