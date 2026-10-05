package run

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
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
		if got := backendLimits(rt, []*packload.Pack{limitPack(t)}, jsonx.NewOrderedMap(), nil); len(got) != 0 {
			t.Errorf("%s: got %d limits, want none: %v", rt, len(got), got)
		}
	}
}

// The three facts an agent reasons WRONGLY from without them. Each is printed at
// launch to stderr, where the human reads it and the agent never does.
func TestBackendLimitsTellTheAgentWhatStderrTellsTheHuman(t *testing.T) {
	got := strings.Join(backendLimits("macos-user",
		[]*packload.Pack{limitPack(t)}, jsonx.NewOrderedMap(), nil), "\n")

	// The home is machine-wide: an agent believing it is its own writes project state
	// into a directory every other workspace reads.
	if !strings.Contains(got, "SHARED by every workspace") {
		t.Errorf("does not say the home is shared:\n%s", got)
	}
	// Content is a writable copy: a skill the agent edits is silently overwritten.
	if !strings.Contains(got, "writable COPY") {
		t.Errorf("does not say content is a writable copy:\n%s", got)
	}
	// The Linux-only loopholes' clients are not in this sandbox.
	if !strings.Contains(got, "yolo-journalctl") || !strings.Contains(got, "yolo-cglimit") {
		t.Errorf("does not say the Linux-only loopholes' clients are unavailable:\n%s", got)
	}
}

// THE CLIENTS THE GUEST STAGES ARE NOT CALLED UNAVAILABLE, AND ARE SAID TO WORK. `yolo-ps` and
// `yolo-serial` run in the macos-user sandbox whenever their loophole's endpoint is published
// (macosuser.GuestClients), so a standing briefing line calling them unavailable would have the
// agent decline a tool it has, all session. Driven over every guest client, so a client added to
// the set without being moved out of the unavailable sentence fails here.
func TestBackendLimitsDoNotCallTheGuestClientsUnavailable(t *testing.T) {
	var line string
	for _, l := range backendLimits("macos-user", []*packload.Pack{limitPack(t)}, jsonx.NewOrderedMap(), nil) {
		if strings.Contains(l, "are not available here") {
			line = l
		}
	}
	if line == "" {
		t.Fatal("the macos-user briefing no longer says which loophole clients are unavailable")
	}
	unavailable, available, _ := strings.Cut(line, ". ")
	for _, c := range macosuser.GuestClients {
		if strings.Contains(unavailable, c.Binary) {
			t.Errorf("the briefing calls %s (the %s loophole's client) unavailable, and the "+
				"macos-user guest stages it:\n%s", c.Binary, c.Loophole, line)
		}
		if !strings.Contains(available, "`"+c.Binary+"`") || !strings.Contains(available, "do run here") {
			t.Errorf("the briefing does not say %s runs here:\n%s", c.Binary, line)
		}
	}
}

// A jail with no packs has no shared state, no ungranted host files and no copied
// content — so only the backend's own standing facts survive. A limit list that reported
// constraints a jail does not have would be the same overclaim in a new place.
func TestBackendLimitsScaleWithWhatIsActuallyThere(t *testing.T) {
	got := backendLimits("macos-user", nil, jsonx.NewOrderedMap(), nil)
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
		[]*packload.Pack{limitPack(t)}, jsonx.NewOrderedMap(), nil), "\n")

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
		[]*packload.Pack{workspaceStatePack(t)}, jsonx.NewOrderedMap(), nil), "\n")

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
		got := strings.Join(backendLimits("macos-user", packs, jsonx.NewOrderedMap(), nil), "\n")
		if !strings.Contains(got, "no network namespace") {
			t.Errorf("does not tell the agent there is no network namespace:\n%s", got)
		}
		if !strings.Contains(got, "real interfaces") {
			t.Errorf("does not say a bound port lands on the machine's real interfaces:\n%s", got)
		}
	}
	// And no container backend gains it: there the usual reading is the correct one.
	for _, rt := range []string{"podman", "container"} {
		if got := strings.Join(backendLimits(rt, nil, jsonx.NewOrderedMap(), nil), "\n"); got != "" {
			t.Errorf("%s gained a standing limit: %s", rt, got)
		}
	}
}

// The Mac's userland: an agent reaches for GNU flags by habit, and on this backend `sed -i`
// with no suffix, `find -printf`, `grep -P` and `tar --wildcards` fail. Unconditional, with or
// without packs, and never on a container backend, whose userland is GNU.
func TestBackendLimitsSayTheUserlandIsBSD(t *testing.T) {
	for _, packs := range [][]*packload.Pack{nil, {limitPack(t)}} {
		got := strings.Join(backendLimits("macos-user", packs, jsonx.NewOrderedMap(), nil), "\n")
		for _, want := range []string{"the Mac's own BSD tools", "unless `packages:` or a mise tool",
			"`sed -i ''`", "`find -printf`", "`grep -P`", "`tar --wildcards`", "Write portable invocations"} {
			if !strings.Contains(got, want) {
				t.Errorf("packs=%d: the userland sentence lacks %q:\n%s", len(packs), want, got)
			}
		}
	}
	for _, rt := range []string{"podman", "container"} {
		if got := strings.Join(backendLimits(rt, nil, jsonx.NewOrderedMap(), nil), "\n"); strings.Contains(got, "BSD") {
			t.Errorf("%s was told its userland is BSD: %s", rt, got)
		}
	}
	// And it reaches the briefing a macos-user launch composes, not only this function.
	if got := macosUserBriefing(t, appliedTestConfig()); !strings.Contains(got, "the Mac's own BSD tools") {
		t.Errorf("the composed macos-user briefing does not carry the userland sentence:\n%s", got)
	}
}

// The two refusals this backend's profile makes by default, and the setting that lifts each.
// An agent that runs `/usr/bin/log show` or configures a serial adapter is refused by the
// Seatbelt profile with nothing it can see naming the cause; the stop names its next step here
// (AGENTS.md "Every stop names the next step"). The log sentence follows the profile's own
// reading of `macos_log` (macosuser.MacosLogOff): absent, "off" and every value the profile
// treats as off carry it, "user" and "full" do not. The device sentence is unconditional, like
// the ioctl deny it describes. Both are asserted on the composed briefing, so deleting the
// branch from backendLimits fails them.
func TestBackendLimitsNameTheLogAndDeviceSettings(t *testing.T) {
	const logLine = "The macOS unified log is unreadable here"
	const devLine = "Device control calls (`ioctl`) on /dev nodes are refused here"
	for _, mode := range []any{nil, "off", "bogus", "user", "full"} {
		cfg := appliedTestConfig()
		if mode != nil {
			cfg.Set("macos_log", mode)
		}
		got := macosUserBriefing(t, cfg)
		wantLog := mode == nil || mode == "off" || mode == "bogus"
		if has := strings.Contains(got, logLine); has != wantLog {
			t.Errorf("macos_log %v: the briefing carries the log sentence = %v, want %v:\n%s", mode, has, wantLog, got)
		}
		if wantLog && !strings.Contains(got, "ask the human to set `\"macos_log\": \"user\"`") {
			t.Errorf("macos_log %v: the log sentence does not name the setting that lifts it:\n%s", mode, got)
		}
		if !strings.Contains(got, devLine) || !strings.Contains(got, "add its path") ||
			!strings.Contains(got, "to `devices` in yolo-jail.jsonc") {
			t.Errorf("macos_log %v: the briefing lacks the device sentence with its next step:\n%s", mode, got)
		}
	}
	for _, rt := range []string{"podman", "container"} {
		got := strings.Join(backendLimits(rt, nil, jsonx.NewOrderedMap(), nil), "\n")
		if strings.Contains(got, logLine) || strings.Contains(got, devLine) {
			t.Errorf("%s was told about the macos-user log or device refusal: %s", rt, got)
		}
	}
}

// THE REMAPS THE LAUNCH RELAYS reach the agent, which reads no stderr: one sentence naming each in
// the direction the agent uses it, only when there is one, and only on macos-user.
func TestBackendLimitsNameTheRelayedRemaps(t *testing.T) {
	netSec := jsonx.NewOrderedMap()
	netSec.Set("ports", []any{"8000:3000", "3001:3001"})
	netSec.Set("forward_host_ports", []any{"8080:9090", 5432})
	cfg := newConfig("network", netSec)
	relays := planMacosUserPortRelays(cfg, "").relays

	got := strings.Join(backendLimits("macos-user", nil, cfg, relays), "\n")
	for _, want := range []string{
		"This launch relays the config's port remaps from outside the sandbox, over TCP and for this session only",
		"`localhost:8080` here reaches the host's port 9090 (`network.forward_host_ports` entry 8080:9090)",
		"your `127.0.0.1:3000` is also published at the host's `0.0.0.0:8000` (`network.ports` entry 8000:3000)",
		"A relay whose host port was already taken at launch is not running",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the relay sentence lacks %q:\n%s", want, got)
		}
	}
	for _, unwanted := range []string{"3001", "5432"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("a same-port entry (%s) was named as relayed:\n%s", unwanted, got)
		}
	}
	if got := strings.Join(backendLimits("macos-user", nil, cfg, nil), "\n"); strings.Contains(got, "relays") {
		t.Errorf("a launch relaying nothing was told it relays:\n%s", got)
	}
	if got := backendLimits("podman", nil, cfg, relays); len(got) != 0 {
		t.Errorf("podman gained a limits section from relays it never opens: %v", got)
	}

	// And through the briefing a launch composes, from the plan the launch acts on: the relay
	// sentence is there for the default bridge, and gone for a typed `--network host`, which
	// drops both keys — so refreshJailBriefings must read the RESOLVED mode, not the config's.
	if got := macosUserBriefing(t, appliedTestConfig("network", netSec)); !strings.Contains(got, "`localhost:8080` here reaches") {
		t.Errorf("the composed briefing does not name the relayed remap:\n%s", got)
	}
	got = macosUserBriefingWith(t, appliedTestConfig("network", netSec), func(o *Options) { o.Network = "host" })
	if strings.Contains(got, "relays the config's port remaps") {
		t.Errorf("a `--network host` briefing names remaps the launch does not relay:\n%s", got)
	}
}
