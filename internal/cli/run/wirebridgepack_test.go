package run

// wirebridgepack_test.go pins the shipped wire-bridge pack and the cerebras
// need that stages it (docs/design/wire-bridge.md §3, §5) at the tier the
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
)

// bridgedLaunch is the acceptance story's pack set: claude and cerebras
// configured, the bridge joined the way the closure joins it — appended to the
// selected set exactly as packs.go appends ResolveNeeds' additions. Cerebras
// installs no CLI, so `-p cerebras` reaches the derive through the global
// profile table.
func bridgedLaunch(t *testing.T, profile string) []string {
	packs := []*packload.Pack{
		officialPack(t, "claude"), officialPack(t, "cerebras"), officialPack(t, "wire-bridge"),
	}
	return zaiLaunch(t, packs, bareConfig(), cerebrasKey(),
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
	argv := bridgedLaunch(t, "cerebras")

	if v := envArgValues(argv, "ANTHROPIC_BASE_URL"); len(v) != 1 ||
		v[0] != "ANTHROPIC_BASE_URL=http://127.0.0.1:8214" {
		t.Errorf("claude must be routed at the bridge's loopback URL: %q", v)
	}
	if v := envArgValues(argv, "CLAUDE_CODE_AUTO_COMPACT_WINDOW"); len(v) != 1 ||
		v[0] != "CLAUDE_CODE_AUTO_COMPACT_WINDOW=65536" {
		t.Errorf("auto-compact = %q, want the manifest's 65536 — the free-tier window "+
			"the bridged launch makes live (WB-D8)", v)
	}
	if v := envArgValues(argv, "ANTHROPIC_DEFAULT_OPUS_MODEL"); len(v) != 1 ||
		v[0] != "ANTHROPIC_DEFAULT_OPUS_MODEL=qwen-3.8-27b" {
		t.Errorf("opus alias = %q, want the bare wire-true id — a 64K context never "+
			"gets claude's [1m] suffix", v)
	}
	if v := envArgValues(argv, "YOLO_JAIL_DAEMONS"); len(v) != 1 ||
		!strings.Contains(v[0], `"wire-bridge"`) {
		t.Errorf("the bridge daemon must join the supervisor's payload: %q", v)
	}
	if v := envArgValues(argv, "YOLO_SERVICE_WIRE_BRIDGE_ENDPOINT"); len(v) != 1 ||
		v[0] != "YOLO_SERVICE_WIRE_BRIDGE_ENDPOINT=/run/yolo-services/wire-bridge.endpoint" {
		t.Errorf("the witness registration must name the manifest's endpoint file: %q", v)
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
	argv := zaiLaunch(t, packs, bareConfig(), hydratedKey(),
		func(o *Options) { o.ProfileName = "zai" })

	if v := envArgValues(argv, "YOLO_JAIL_DAEMONS"); len(v) != 1 ||
		!strings.Contains(v[0], `"wire-bridge"`) {
		t.Errorf("a staged bridge runs its daemon even when it will idle: %q", v)
	}
	if v := envArgValues(argv, "YOLO_SERVICE_WIRE_BRIDGE_ENDPOINT"); len(v) != 0 {
		t.Errorf("an unrouted bridge must not register an endpoint with the witness: %q", v)
	}
	if v := envArgValues(argv, "ANTHROPIC_BASE_URL"); len(v) != 1 ||
		v[0] != "ANTHROPIC_BASE_URL=https://api.z.ai/api/anthropic" {
		t.Errorf("claude riding zai must keep z.ai's own route: %q", v)
	}
}

// TestLaunchWithoutTheBridgeNeverRegistersIt: claude and cerebras, NO bridge
// pack — the shape a launch had the one moment the endpoint shipped without the
// need (the plan's bad direction), and the shape that survives only if the
// closure is bypassed. The derive composes the loopback URL regardless — it
// cannot see whether a bridge exists (§3.3) — but nothing joins the daemons
// payload and the witness hears nothing: a dead URL, loud about who staged it.
func TestLaunchWithoutTheBridgeNeverRegistersIt(t *testing.T) {
	packs := []*packload.Pack{officialPack(t, "claude"), officialPack(t, "cerebras")}
	argv := zaiLaunch(t, packs, bareConfig(), cerebrasKey(),
		func(o *Options) { o.ProfileName = "cerebras" })

	if got := envArgValues(argv, "ANTHROPIC_BASE_URL"); len(got) != 1 ||
		got[0] != "ANTHROPIC_BASE_URL=http://127.0.0.1:8214" {
		t.Fatalf("the derive composes the manifest URL whether or not the bridge is "+
			"staged — it cannot see the pack set (§3.3): %q", got)
	}
	if v := envArgValues(argv, "YOLO_JAIL_DAEMONS", "YOLO_SERVICE_WIRE_BRIDGE_ENDPOINT"); len(v) != 0 {
		t.Errorf("a launch without the bridge pack must stage no daemon and register "+
			"no endpoint: %q", v)
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
	if got := errBuf.String(); !strings.Contains(got, "+ wire-bridge (needed by cerebras: claude selected)") {
		t.Errorf("the launch stderr must carry the cause line (WB-D12):\n%s", got)
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
