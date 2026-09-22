package run

// wirebridgepack_test.go pins the shipped wire-bridge pack and the cerebras
// need that stages it (docs/reference/wire-bridge.md §3, §5) at the tier the
// launch actually runs: the staged set, the composed argv, and the two env
// vars a bridged launch carries. packload.ResolveNeeds' closure and
// wirebridged's boot decision have their own tables; what only this file can
// prove is that a launch joins the pack, starts its daemon, and registers the
// endpoint with the witness — and that a launch without one half never fakes
// the other.

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// bridgedLaunch is the acceptance story's pack set: claude and cerebras
// configured, the bridge joined the way the closure joins it — appended to the
// selected set exactly as packs.go appends ResolveNeeds' additions. Cerebras
// installs no CLI, so `-p cerebras` reaches the derive through the global
// profile table.
func bridgedLaunch(t *testing.T, profile string) assembled {
	packs := []*packload.Pack{
		officialPack(t, "claude"), officialPack(t, "cerebras"), officialPack(t, "wire-bridge"),
	}
	return zaiLaunchAssembled(t, packs, bareConfig(), cerebrasKey(),
		func(o *Options) { o.ProfileName = profile })
}

// TestBridgedLaunchComposesTheWholeStory: one argv carries all four facts the
// design's done-looks-like names — claude routed at the bridge's loopback URL,
// auto-compact sized to the declared 64K window (and the bare model id: a 64K
// context never earns the [1m] suffix), the bridge daemon in the supervisor's
// payload, and the endpoint file registered for the reachability witness.
// Delete any one production call site — the derive's endpoint read, the
// service composition, the witness emission — and its quarter of this test
// goes red.
func TestBridgedLaunchComposesTheWholeStory(t *testing.T) {
	la := bridgedLaunch(t, "cerebras")

	// The provider half crosses in yolo-user-env.sh's channel section now (per-entry
	// delivery), so the routing/window/alias facts are asserted on the rendered file
	// — the bytes the jail actually sources — not the argv.
	if v := la.channelEnv(t, "ANTHROPIC_BASE_URL"); len(v) != 1 ||
		v[0] != "ANTHROPIC_BASE_URL=http://127.0.0.1:8214" {
		t.Errorf("claude must be routed at the bridge's loopback URL: %q", v)
	}
	if v := la.channelEnv(t, "CLAUDE_CODE_AUTO_COMPACT_WINDOW"); len(v) != 1 ||
		v[0] != "CLAUDE_CODE_AUTO_COMPACT_WINDOW=65536" {
		t.Errorf("auto-compact = %q, want the manifest's 65536 — the free-tier window "+
			"the bridged launch makes live (WB-D8)", v)
	}
	if v := la.channelEnv(t, "ANTHROPIC_DEFAULT_OPUS_MODEL"); len(v) != 1 ||
		v[0] != "ANTHROPIC_DEFAULT_OPUS_MODEL=qwen-3.8-27b" {
		t.Errorf("opus alias = %q, want the bare wire-true id — a 64K context never "+
			"gets claude's [1m] suffix", v)
	}
	if v := envArgValues(la.argv, "YOLO_JAIL_DAEMONS"); len(v) != 1 ||
		!strings.Contains(v[0], `"wire-bridge"`) {
		t.Errorf("the bridge daemon must join the supervisor's payload: %q", v)
	}
	if v := envArgValues(la.argv, "YOLO_SERVICE_WIRE_BRIDGE_ENDPOINT"); len(v) != 1 ||
		v[0] != "YOLO_SERVICE_WIRE_BRIDGE_ENDPOINT=/run/yolo-services/wire-bridge.endpoint" {
		t.Errorf("the witness registration must name the manifest's endpoint file: %q", v)
	}
	if v := envArgValues(la.argv, paths.JailDaemonReadyNamesEnv); len(v) != 1 ||
		v[0] != paths.JailDaemonReadyNamesEnv+"=wire-bridge" {
		t.Errorf("the endpoint-publishing daemon must be a boot readiness dependency: %q", v)
	}
}

// TestBridgeStagedButUnroutedIdlesAndEmitsNothing: the selection-lazy half
// (§3.4). The bridge is staged and the daemon STILL joins the payload —
// supervise runs it — but nothing routes at it: claude's active profile rides
// zai, whose anthropic endpoint is z.ai's own, not this jail's loopback. The
// daemon will idle healthy in-jail, so no endpoint is registered: the variable
// would name a file an idle daemon never publishes, and the witness would
// refuse the launch as an unpublished service — the exact contradiction §5's
// WARNING exists to prevent. (The serve decision keys on the selection table,
// not on the agent's name — a copilot-profiled cerebras launch serves just as
// a claude one does; "routed at a bridged provider" is the predicate.)
func TestBridgeStagedButUnroutedIdlesAndEmitsNothing(t *testing.T) {
	packs := []*packload.Pack{
		officialPack(t, "claude"), officialPack(t, "zai"), officialPack(t, "wire-bridge"),
	}
	la := zaiLaunchAssembled(t, packs, bareConfig(), hydratedKey(),
		func(o *Options) { o.ProfileName = "zai" })

	if v := envArgValues(la.argv, "YOLO_JAIL_DAEMONS"); len(v) != 1 ||
		!strings.Contains(v[0], `"wire-bridge"`) {
		t.Errorf("a staged bridge runs its daemon even when it will idle: %q", v)
	}
	if v := envArgValues(la.argv, "YOLO_SERVICE_WIRE_BRIDGE_ENDPOINT"); len(v) != 0 {
		t.Errorf("an unrouted bridge must not register an endpoint with the witness: %q", v)
	}
	if v := envArgValues(la.argv, paths.JailDaemonReadyNamesEnv); len(v) != 0 {
		t.Errorf("an idle bridge must not become a boot readiness dependency: %q", v)
	}
	if v := la.channelEnv(t, "ANTHROPIC_BASE_URL"); len(v) != 1 ||
		v[0] != "ANTHROPIC_BASE_URL=https://api.z.ai/api/anthropic" {
		t.Errorf("claude riding zai must keep z.ai's own route: %q", v)
	}
}

// TestLaunchWithoutTheBridgeRefusesThePairing: claude and cerebras, NO bridge pack.
//
// THIS TEST'S SUBJECT INVERTED, and the inversion is the point of protocol resolution.
// It used to assert a DEAD URL: the derive composed `http://127.0.0.1:8214` whether or
// not a bridge was staged, because the URL was a literal in cerebras's own manifest and
// the derive "cannot see the pack set". A launch therefore started, pointed claude at a
// loopback port nothing was listening on, and failed at the first request.
//
// The address belongs to the adapter now, so with no adapter selected there is no
// address — and the pairing is refused instead of composed. The refusal is the whole
// remedy the old dead URL could not offer.
func TestLaunchWithoutTheBridgeRefusesThePairing(t *testing.T) {
	packs := []*packload.Pack{officialPack(t, "claude"), officialPack(t, "cerebras")}
	home := t.TempDir()
	t.Setenv("HOME", home)
	emptyLoopholeDirs(t)
	o := goldenOptions("/ws", home)
	o.ProfileName = "cerebras"

	_, err := o.composePackChannel(bareConfig(), packs, cerebrasKey())
	if err == nil {
		t.Fatal("claude beside cerebras with no adapter must REFUSE: there is no address " +
			"for the wire claude speaks, and composing one anyway is the dead URL this " +
			"replaced")
	}
	for _, want := range []string{
		`provider "cerebras"`, `agent "claude"`,
		// OUTCOME 3: the refusal names the pack to add, which is the discoverable half —
		// and it stops there. The resolver never joins the pack itself, because choosing a
		// provider must not decide what runs in your jail (OQ-PR3).
		`Pack "wire-bridge" adapts`, "Add it to `packs`",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal must name both sides; %q missing from:\n%v", want, err)
		}
	}
}

// TestStagePacksJoinsTheBridgeForCerebrasAndClaude: the REAL need — the
// embedded cerebras pack's own manifest, not a fixture — resolved through the
// actual selection path, joined and disclosed (WB-D12). needspack_test.go
// pins the closure mechanism on a fixture; this pins the shipped
// declaration's live condition against the real embedded universe.
func TestStagePacksJoinsTheBridgeForCerebrasAndClaude(t *testing.T) {
	home := packHome(t)
	writeUserPacks(t, home, `["claude", "cerebras"]`)

	var errBuf bytes.Buffer
	o := &Options{Workspace: t.TempDir(), Stdout: discardBuf(), Stderr: &errBuf}
	_, loaded, _, err := o.stagePacks("yolo-test-bridge-joined")
	if err != nil {
		t.Fatalf("stagePacks: %v", err)
	}

	joined := false
	var names []string
	for _, p := range loaded {
		names = append(names, p.Name)
		if p.Name == "wire-bridge" {
			joined = true
		}
	}
	if !joined {
		t.Fatalf("cerebras's live need did not join wire-bridge: loaded = %v", names)
	}
	if got := errBuf.String(); !strings.Contains(got, "+ wire-bridge (needed by claude)") {
		t.Errorf("the launch stderr must disclose Claude's selected bridge dependency:\n%s", got)
	}
}

// TestStagePacksKeepsTheBridgeOutWithoutAConsumer: the same shipped need with
// its condition unmet — no selected pack installs a bin whose derive reads the
// bridged anthropic endpoint (claude or copilot) — so nothing joins, and the
// control half of WB-D12: no cause line either.
func TestStagePacksKeepsTheBridgeOutWithoutAConsumer(t *testing.T) {
	home := packHome(t)
	writeUserPacks(t, home, `["pi", "cerebras"]`)

	var errBuf bytes.Buffer
	o := &Options{Workspace: t.TempDir(), Stdout: discardBuf(), Stderr: &errBuf}
	_, loaded, _, err := o.stagePacks("yolo-test-bridge-unmet")
	if err != nil {
		t.Fatalf("stagePacks: %v", err)
	}
	for _, p := range loaded {
		if p.Name == "wire-bridge" {
			t.Errorf("a need whose condition nothing satisfies must not join wire-bridge")
		}
	}
	if got := errBuf.String(); strings.Contains(got, "+ wire-bridge") {
		t.Errorf("an unmet condition must print no cause line:\n%s", got)
	}
}

// TestStagePacksJoinsTheBridgeForCopilot: the OTHER consumer bin. copilot's
// derive prefers the anthropic endpoint of any provider declaring one (D-3),
// so once cerebras declares the bridge's URL, a copilot-only launch is exactly
// as much a bridge launch as a claude one — and the need's when_bins names the
// copilot bin so the URL is never dead for it. The daemon runs, the witness is
// registered, and the cause line names the bin that triggered it.
func TestStagePacksJoinsTheBridgeForCopilot(t *testing.T) {
	home := packHome(t)
	writeUserPacks(t, home, `["copilot", "cerebras"]`)

	var errBuf bytes.Buffer
	o := &Options{Workspace: t.TempDir(), Stdout: discardBuf(), Stderr: &errBuf}
	_, loaded, _, err := o.stagePacks("yolo-test-bridge-copilot")
	if err != nil {
		t.Fatalf("stagePacks: %v", err)
	}
	joined := false
	for _, p := range loaded {
		if p.Name == "wire-bridge" {
			joined = true
		}
	}
	if !joined {
		t.Fatal("copilot selected must join wire-bridge — copilot's derive composes the bridged URL (D-3)")
	}
	if got := errBuf.String(); !strings.Contains(got, "+ wire-bridge (needed by cerebras: copilot selected)") {
		t.Errorf("the cause line must name the triggering bin:\n%s", got)
	}

	// And the argv half: with the closure's pack staged and copilot's profile
	// active, the daemon is composed AND the witness is registered — the launch
	// decided it will serve, though claude was never selected.
	packs := []*packload.Pack{
		officialPack(t, "copilot"), officialPack(t, "cerebras"), officialPack(t, "wire-bridge"),
	}
	argv := zaiLaunch(t, packs, bareConfig(), cerebrasKey(),
		func(o *Options) { o.ProfileName = "cerebras" })
	if v := envArgValues(argv, "YOLO_SERVICE_WIRE_BRIDGE_ENDPOINT"); len(v) != 1 {
		t.Errorf("a copilot-routed bridged launch must register the endpoint with the "+
			"witness: %q", v)
	}
}

// TestShippedWireBridgePackIsServiceOnly: the pack's whole manifest is ONE
// service contribution — the first kind:service instance, no grants, no
// provider facts of its own (the provider half lives on cerebras). Pinned
// against the embedded pack, so a manifest edit that grows the claim answers
// here.
func TestShippedWireBridgePackIsServiceOnly(t *testing.T) {
	p := officialPack(t, "wire-bridge")
	svcs := p.Decl.Services()
	if len(svcs) != 1 || svcs[0].Name != "wire-bridge" {
		t.Fatalf("services = %+v, want exactly one named wire-bridge", svcs)
	}
	if svcs[0].JailDaemon == nil || strings.Join(svcs[0].JailDaemon.Cmd, " ") != "yolo-jaild wire-bridge" {
		t.Errorf("jail_daemon = %+v, want [yolo-jaild wire-bridge]", svcs[0].JailDaemon)
	}
	if svcs[0].JailDaemon.Restart != "on-failure" {
		t.Errorf("restart = %q, want the manifest's on-failure", svcs[0].JailDaemon.Restart)
	}
	if svcs[0].Endpoint != "wire-bridge.endpoint" {
		t.Errorf("endpoint = %q, want the bare file name the daemon publishes", svcs[0].Endpoint)
	}
	if len(p.Decl.InstallContributions()) != 0 || len(p.Decl.HostFileContributions()) != 0 ||
		len(p.Decl.HostMountContributions()) != 0 {
		t.Errorf("the bridge pack ships a grant — a service holds none (wire-bridge.md §2.1)")
	}
}

// TestAUserMovesTheBridgesAddress pins the CALL SITE of the adapter address override
// (protocol-resolution.md), not the composer that honors it: the launch must READ the
// user's `adapters` table, and deleting that read from composedProviders leaves this
// test as the only thing that notices.
//
// WHY IT IS CONFIGURABLE AT ALL, and the reason is measured rather than hypothetical: in a
// container `127.0.0.1` is the jail's own private loopback, so a collision is only possible
// with another baked service. On `macos-user` there is no container and no network
// namespace, so the bridge's ports are HOST ports and collide with whatever the user is
// already running. What moves is the ADDRESS; the conversion stays the pack's claim.
func TestAUserMovesTheBridgesAddress(t *testing.T) {
	home := packHome(t)
	writeUserConfig(t, home, `{"adapters": {"openai->anthropic": {"address": "http://127.0.0.1:9214"}}}`)
	emptyLoopholeDirs(t)
	packs := []*packload.Pack{
		officialPack(t, "claude"), officialPack(t, "cerebras"), officialPack(t, "wire-bridge"),
	}
	o := goldenOptions("/ws", home)
	o.ProfileName = "cerebras"

	c, err := o.composePackChannel(bareConfig(), packs, cerebrasKey())
	if err != nil {
		t.Fatal(err)
	}
	var got string
	for _, v := range c.shapeVars {
		if v.Key == "ANTHROPIC_BASE_URL" {
			got = v.Value
		}
	}
	if got != "http://127.0.0.1:9214" {
		t.Errorf("ANTHROPIC_BASE_URL = %q, want the user's moved address — the launch must "+
			"read `adapters` from the user config, not only the pack's declared default", got)
	}
}
