package run

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// limitPack builds a pack declaring a MACHINE-scoped state dir and a reads-host grant —
// the two contributions whose macos-user behaviour differs from every other backend.
//
// `scope: "machine"` and not "workspace", which is the DP-B11 flip. The fixture declared
// the workspace tier until 2026-09-13 and the assertion below passed on it, which is what
// made the stale claim invisible: since entrypoint.InstallDarwinHomeLayout a
// `scope: workspace` dir is symlinked into <workspace>/.yolo/home and is NOT shared, so
// the old pairing asserted the "shared by every workspace" sentence about precisely the
// directories that are not. TestBackendLimitsDoNotCallWorkspaceStateShared below is the
// other half — it fails if the call site goes back to packload.WritableDirs.
func limitPack(t *testing.T) *packload.Pack {
	t.Helper()
	// MayAccessHost: HonoredHostFiles refuses a FETCHED pack's grants outright, so a
	// pack without it produces no ungranted list and the test would pass vacuously.
	return &packload.Pack{Name: "acme", Root: t.TempDir(), Decl: &packdecl.Manifest{
		Name: "acme",
		Contributes: []packdecl.Contribution{
			{Kind: packdecl.KindState, At: ".acme", Scope: "machine"},
			{Kind: packdecl.KindReadsHost, Host: ".acme/settings.json", From: ".acme/settings.json",
				Into: "settings.json"},
		},
	}}
}

// workspaceStatePack declares the OTHER tier and nothing else: one `scope: workspace`
// state dir, which the darwin home layout links into this workspace's own sidecar.
func workspaceStatePack(t *testing.T) *packload.Pack {
	t.Helper()
	return &packload.Pack{Name: "wsonly", Root: t.TempDir(), Decl: &packdecl.Manifest{
		Name: "wsonly",
		Contributes: []packdecl.Contribution{
			{Kind: packdecl.KindState, At: ".wsonly", Scope: "workspace"},
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
// content — so only the backend's own standing facts survive. A limit list that reported
// constraints a jail does not have would be the same overclaim in a new place.
func TestBackendLimitsScaleWithWhatIsActuallyThere(t *testing.T) {
	got := backendLimits("macos-user", nil, jsonx.NewOrderedMap())
	joined := strings.Join(got, "\n")
	if strings.Contains(joined, "SHARED by every workspace") {
		t.Errorf("claimed shared state dirs for a jail with no packs:\n%s", joined)
	}
}

// ⚠ THE INVERSION, and the negative half of DP-L1. This briefing used to tell the agent
// its config files "were rendered from DEFAULTS, not from the human's own", naming every
// pack `reads-host` grant. That was true while the bytes crossed on a /ctx mount this
// backend does not have; since DP-L1 they cross by COPY into a root-owned tree
// (macosctxtree.go) and the surface composes the human's real file.
//
// The same move TestMacosUserNoLongerWarnsThatToolsAreUninstallable made, and for a
// sharper reason: a stderr warning is read once, while a briefing line is a STANDING
// constraint an agent reasons from all session. Told its settings are not the human's, an
// agent discounts preferences that are — and there is no moment of use at which anything
// corrects it.
//
// ⚠ NEGATIVE HALF ONLY. That the bytes really arrive is pinned where it can be observed
// rather than inferred from silence: TestMacosUserLaunchDeliversAPackReadsHostGrant drives
// a real Run() and reads the composed tree. Absence of a warning is not evidence of a
// feature.
func TestBackendLimitsNoLongerCallTheAgentConfigADefault(t *testing.T) {
	got := strings.Join(backendLimits("macos-user",
		[]*packload.Pack{limitPack(t)}, jsonx.NewOrderedMap()), "\n")

	if strings.Contains(got, "DEFAULTS") {
		t.Errorf("the briefing still tells the agent its config was rendered from defaults, "+
			"but a pack `reads-host` grant is now copied into the sandbox's context tree "+
			"and composed like it is on every other backend:\n%s", got)
	}
	if strings.Contains(got, ".acme/settings.json") {
		t.Errorf("the briefing still names a reads-host grant as undelivered:\n%s", got)
	}
}

// DP-B11, the direction the old fixture could not see. A pack whose only state dir is
// `scope: workspace` gets that directory symlinked into <workspace>/.yolo/home by
// entrypoint.DeriveDarwinHomeLayout, so it is this workspace's alone — and naming it in a
// sentence that says "the same directories another workspace's session reads and writes"
// is not a vague overclaim but a specific false one, about the very dirs an agent writes
// its state into.
//
// This fails if the call site goes back to packload.WritableDirs, which is the whole point
// of asserting on the PACK rather than on the helper: WritableDirs and SharedDirs have the
// same signature and the same shape of answer, so only a fixture that distinguishes the
// two tiers can tell them apart.
func TestBackendLimitsDoNotCallWorkspaceStateShared(t *testing.T) {
	got := strings.Join(backendLimits("macos-user",
		[]*packload.Pack{workspaceStatePack(t)}, jsonx.NewOrderedMap()), "\n")

	if strings.Contains(got, "SHARED by every workspace") {
		t.Errorf("a scope:workspace state dir is linked into this workspace's own sidecar, "+
			"so calling it machine-wide tells the agent the opposite of what is true:\n%s", got)
	}
	if strings.Contains(got, ".wsonly") {
		t.Errorf("named a workspace-tier directory in the machine-tier sentence:\n%s", got)
	}
}

// §5.1.1 (3): the one network fact neither briefing paragraph states. Under host
// networking the briefing says `localhost` reaches the host and that no port mapping is
// needed — both true here and both incomplete, because they leave the usual container
// reading intact: that a listener is confined until something publishes it. There is no
// namespace on this backend, so binding IS publishing and `network.ports` pins nothing.
//
// It is unconditional: every macos-user jail has it, whether or not the config ever
// mentioned a port, because the fact is about the absence of a namespace rather than about
// any declaration.
func TestBackendLimitsSayBindingIsPublishingWithNoNamespace(t *testing.T) {
	for _, packs := range [][]*packload.Pack{nil, {limitPack(t)}} {
		got := strings.Join(backendLimits("macos-user", packs, jsonx.NewOrderedMap()), "\n")
		if !strings.Contains(got, "no network namespace") {
			t.Errorf("does not tell the agent there is no network namespace:\n%s", got)
		}
		if !strings.Contains(got, "real interfaces") {
			t.Errorf("does not say a bound port lands on the machine's real interfaces:\n%s", got)
		}
	}
	// And no container backend gains it: there the usual reading is the correct one.
	for _, rt := range []string{"podman", "container"} {
		if got := strings.Join(backendLimits(rt, nil, jsonx.NewOrderedMap()), "\n"); got != "" {
			t.Errorf("%s gained a standing limit: %s", rt, got)
		}
	}
}

// TestBackendLimitsNameNoPackageStoreOnTheShippedPacks: since XB-D14
// (docs/design/pi-extension-store-builds.md) no shipped pack shares a package store at
// machine scope, pi's npm prefix being per workspace again, so the macos-user briefing's
// machine-tier sentence over the shipped claude and pi packs names their credential dir and
// no store. It fails if pi goes back to declaring `.pi-shared-npm` shared, or if the sentence
// goes back to telling the agent to expect a package store another workspace installed into.
func TestBackendLimitsNameNoPackageStoreOnTheShippedPacks(t *testing.T) {
	got := strings.Join(backendLimits("macos-user", packsFixture(t, "claude", "pi"), jsonx.NewOrderedMap()), "\n")
	if !strings.Contains(got, ".claude-shared-credentials") {
		t.Fatalf("the machine-tier sentence is gone, so this cell checks nothing:\n%s", got)
	}
	for _, stale := range []string{".pi-shared-npm", "package store"} {
		if strings.Contains(got, stale) {
			t.Errorf("the briefing still names %q in the machine tier:\n%s", stale, got)
		}
	}
}
