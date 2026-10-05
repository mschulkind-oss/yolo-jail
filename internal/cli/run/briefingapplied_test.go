package run

// The briefing is composed from what the launch APPLIED, not from the config map
// (docs/design/backend-parity.md §6). Every test here drives BOTH consumers of one
// backendcaps predicate — the argv and the written briefing — from a single config, so a
// row fails if either half stops reading the shared answer.
//
// That double assertion is the point rather than thoroughness. The defect this closes is
// not "the briefing is wrong"; it is "the briefing and the command line were computed
// separately", and a test that only read one of them would have passed throughout the
// entire life of the bug. The threading is a single struct field in refreshJailBriefings
// and a single `applied` in assembleRunCmd: delete either and the nested-networking row,
// the Apple Container rows and the mounts row all fail here.

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/hostfloor/floortest"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
)

// appliedTestConfig is the minimal config every row builds on: one agent, empty security,
// plus whatever key the row is about.
func appliedTestConfig(pairs ...any) *jsonx.OrderedMap {
	sec := jsonx.NewOrderedMap()
	sec.Set("blocked_tools", []any{})
	cfg := newConfig("agents", []any{"claude"}, "security", sec)
	for i := 0; i+1 < len(pairs); i += 2 {
		cfg.Set(pairs[i].(string), pairs[i+1])
	}
	return cfg
}

// appliedOptions is a deterministic host for these tests. nested makes o.inContainer()
// true — the podman-in-podman shape, which is the one the config cannot express and the
// briefing therefore could not previously see.
func appliedOptions(t *testing.T, ws, home string, nested bool) *Options {
	t.Helper()
	o := goldenOptions(ws, home)
	if nested {
		o.PathExists = func(p string) bool { return p == "/run/.containerenv" }
	}
	return o
}

// appliedBriefing writes the jail briefing for one (runtime, config) pair and returns the
// text the claude pack's declared destination received — the bytes an agent actually
// reads, not BriefingContent's return value, so nothing between the two can bypass it.
func appliedBriefing(t *testing.T, o *Options, rt string, cfg *jsonx.OrderedMap) string {
	t.Helper()
	staging, err := o.refreshJailBriefings("yolo-ws-abcd1234", cfg, rt,
		stagedPacks{packs: claudePackFixture(t)}, appliedIOPriority(rt, o.IsMacOS, cfgMap(cfg, "resources")))
	if err != nil {
		t.Fatalf("refreshJailBriefings: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(staging, briefingStagingName(claudeBriefingDest)))
	if err != nil {
		t.Fatalf("no briefing was written for the claude pack: %v", err)
	}
	return string(body)
}

// appliedArgv assembles the container argv for the same (runtime, config) pair.
func appliedArgv(t *testing.T, o *Options, rt string, cfg *jsonx.OrderedMap, wsState string) []string {
	t.Helper()
	in := relocationInput(t, rt, wsState, nil)
	in.cfg = cfg
	return o.assembleRunCmd(in)
}

// networkParagraph returns the briefing's "- **Network**:" line.
func networkParagraph(briefing string) string {
	for _, line := range strings.Split(briefing, "\n") {
		if strings.HasPrefix(line, "- **Network**:") {
			return line
		}
	}
	return ""
}

// THE CALL-SITE PIN for the network half. Both consumers must resolve the mode through
// appliedNetMode, so each row states the selector the argv carries AND the paragraph the
// jail is handed, from one config.
//
// The two rows that used to disagree are the reason this exists. Podman-in-podman is
// FORCED to --net=host by nesting, a fact `network.mode` cannot express, so a nested jail
// — this repo's own dev loop — read "Bridge mode … `localhost` in here is the JAIL's
// loopback" while its loopback was the launcher's. Apple Container is the mirror: it
// emits no selector at all, so a jail configured `network.mode: "host"` was told
// "localhost resolves directly to the host" one line after the launch warned that the key
// is not honored there.
func TestBriefingAndArgvAgreeOnTheAppliedNetMode(t *testing.T) {
	cases := []struct {
		name       string
		rt         string
		configMode string
		nested     bool
		// wantSelectors is the WHOLE network argv: podman refuses a container carrying
		// two spellings of --net, so "which selectors" is the contract, not "contains".
		wantSelectors []string
		wantApplied   string
		// wantParagraph is a fragment of the network line the jail is handed;
		// notParagraph must be absent from it.
		wantParagraph, notParagraph string
	}{{
		name: "podman bridge is bridge",
		rt:   "podman", configMode: "bridge",
		wantSelectors: nil, wantApplied: "bridge",
		wantParagraph: "Bridge mode", notParagraph: "Host networking",
	}, {
		name: "podman host is host",
		rt:   "podman", configMode: "host",
		wantSelectors: []string{"--net=host"}, wantApplied: "host",
		wantParagraph: "Host networking", notParagraph: "Bridge mode",
	}, {
		name: "podman-in-podman is forced to host whatever the config asked for",
		rt:   "podman", configMode: "bridge", nested: true,
		wantSelectors: []string{"--net=host"}, wantApplied: "host",
		wantParagraph: "Host networking", notParagraph: "Bridge mode",
	}, {
		name: "Apple Container never applies host networking, however the config is set",
		rt:   "container", configMode: "host",
		wantSelectors: nil, wantApplied: "bridge",
		wantParagraph: "Bridge mode", notParagraph: "Host networking",
	}}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			ws := t.TempDir()
			emptyLoopholeDirs(t)
			o := appliedOptions(t, ws, home, tc.nested)

			netSec := jsonx.NewOrderedMap()
			netSec.Set("mode", tc.configMode)
			cfg := appliedTestConfig("network", netSec)

			if got := appliedNetMode(tc.rt, tc.configMode, tc.nested); got != tc.wantApplied {
				t.Fatalf("appliedNetMode(%q, %q, nested=%v) = %q, want %q",
					tc.rt, tc.configMode, tc.nested, got, tc.wantApplied)
			}

			argv := appliedArgv(t, o, tc.rt, cfg, t.TempDir())
			if got := networkSelectors(argv); !slices.Equal(got, tc.wantSelectors) {
				t.Errorf("network selectors = %v, want %v", got, tc.wantSelectors)
			}

			para := networkParagraph(appliedBriefing(t, o, tc.rt, cfg))
			if para == "" {
				t.Fatal("the briefing carries no network line at all")
			}
			if !strings.Contains(para, tc.wantParagraph) || strings.Contains(para, tc.notParagraph) {
				t.Errorf("the jail is told %q, want a line saying %q and not %q — "+
					"the briefing must describe the mode the launch APPLIED (%q), not the one "+
					"the config asked for (%q)",
					para, tc.wantParagraph, tc.notParagraph, tc.wantApplied, tc.configMode)
			}
		})
	}
}

// THE CALL-SITE PIN for the PORT half, which is the same predicate one level down: both
// port keys are honored only under an applied bridge, so the argv's -p flags and the
// briefing's "Published Ports" section have to appear and disappear together.
//
// They did not, and the argv side was FATAL rather than merely untrue: the publish gate
// read the CONFIGURED mode while the selector read the applied one, so a nested launch
// emitted --net=host AND every -p, and a non-empty publish list appends
// `--sysctl net.ipv4.conf.all.route_localnet=1`, which podman refuses under host
// networking — a nested jail declaring any port could not be created at all
// (nestedports_test.go holds that half in detail).
//
// The Apple Container row is the one that pins the BRIEFING call site: there the applied
// mode is bridge however `network.mode` is set, so -p is emitted, and a briefing still
// gated on the config would omit a port section for ports that were published.
func TestBriefingAndArgvAgreeOnPublishedPorts(t *testing.T) {
	for _, tc := range []struct {
		name          string
		rt            string
		configMode    string
		nested        bool
		wantPublished bool
	}{
		{"podman bridge publishes and says so", "podman", "bridge", false, true},
		{"nested podman publishes nothing and says nothing", "podman", "bridge", true, false},
		{"Apple Container publishes despite an unhonored host mode", "container", "host", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			ws := t.TempDir()
			emptyLoopholeDirs(t)
			o := appliedOptions(t, ws, home, tc.nested)

			netSec := jsonx.NewOrderedMap()
			netSec.Set("mode", tc.configMode)
			netSec.Set("ports", []any{"8000:3000"})
			cfg := appliedTestConfig("network", netSec)

			published := len(publishedPortArgs(appliedArgv(t, o, tc.rt, cfg, t.TempDir()))) > 0
			if published != tc.wantPublished {
				t.Errorf("argv publishes = %v, want %v", published, tc.wantPublished)
			}

			briefed := strings.Contains(appliedBriefing(t, o, tc.rt, cfg), "**Published Ports**")
			if briefed != tc.wantPublished {
				t.Errorf("the briefing advertises published ports = %v while the argv publishes "+
					"= %v — the jail is told about forwarding the launch did not wire (or is not "+
					"told about forwarding it did)", briefed, published)
			}
		})
	}
}

// THE CALL-SITE PIN for the mounts half. §6 names network and resources; a section headed
// "Additional Context Mounts (read-only)" listing /ctx paths the backend refused is the
// same lie with a different key, so both consumers read roBindsUnsupported.
func TestBriefingListsOnlyTheContextMountsTheBackendBinds(t *testing.T) {
	for _, tc := range []struct {
		rt        string
		wantBound bool
	}{{"podman", true}, {"container", false}} {
		t.Run(tc.rt, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			ws := t.TempDir()
			emptyLoopholeDirs(t)
			dir := filepath.Join(t.TempDir(), "sysadmin")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			o := appliedOptions(t, ws, home, false)
			cfg := appliedTestConfig("mounts", []any{dir})

			bound := len(ctxMountArgs(appliedArgv(t, o, tc.rt, cfg, t.TempDir()))) > 0
			if bound != tc.wantBound {
				t.Fatalf("%s bound the ctx mount = %v, want %v", tc.rt, bound, tc.wantBound)
			}
			briefing := appliedBriefing(t, o, tc.rt, cfg)
			listed := strings.Contains(briefing, "## Additional Context Mounts")
			if listed != tc.wantBound {
				t.Errorf("%s briefing lists a context-mounts section = %v, want %v — "+
					"the section must name the mounts that were BOUND; this backend refused "+
					"them and the agent was handed /ctx paths that do not exist:\n%s",
					tc.rt, listed, tc.wantBound, briefing)
			}
			if strings.Contains(briefing, "sysadmin") != tc.wantBound {
				t.Errorf("%s briefing names the refused mount path", tc.rt)
			}
		})
	}
}

// resourceLine returns the briefing's "- **Resource limits**" line, or "".
func resourceLine(briefing string) string {
	for _, line := range strings.Split(briefing, "\n") {
		if strings.HasPrefix(line, "- **Resource limits**") {
			return line
		}
	}
	return ""
}

// THE CALL-SITE PIN for the resources half, and the ruling of §6 in both directions: the
// briefing states what is EMITTED. So Apple Container gains a line for the caps it applies
// by default — an agent believing it is uncapped while capped is the worse lie — and
// `pids_limit`, which that backend never passes, is described nowhere however the user set it.
//
// The podman rows are the other half of the ruling: the uniform `--pids-limit 32768`
// fallback is emitted and NOT briefed, because a standing line in every existing briefing
// reporting one constant is a cost with no reader.
func TestBriefingStatesTheResourceLimitsTheBackendPasses(t *testing.T) {
	withPids := jsonx.NewOrderedMap()
	withPids.Set("pids_limit", 100)
	withMemory := jsonx.NewOrderedMap()
	withMemory.Set("memory", "4g")

	cases := []struct {
		name string
		rt   string
		// resources is the `resources` config block, nil for "the user set nothing".
		resources *jsonx.OrderedMap
		// wantFlags/notFlags are checked against the argv; wantText/notText against the
		// briefing's resource line ("" for wantText means the line must be absent).
		wantFlags, notFlags []string
		wantText, notText   []string
	}{{
		name: "podman with nothing configured says nothing",
		rt:   "podman", resources: nil,
		wantFlags: []string{"--pids-limit"},
		wantText:  nil, notText: []string{"pids_limit"},
	}, {
		name: "podman states what it was given",
		rt:   "podman", resources: withMemory,
		wantFlags: []string{"--memory", "4g"},
		wantText:  []string{"memory=4g"},
	}, {
		name: "Apple Container states the caps it applies by default",
		rt:   "container", resources: nil,
		wantFlags: []string{"--memory", "--cpus"}, notFlags: []string{"--pids-limit"},
		wantText: []string{"memory=" + appleContainerDefaultMemoryDesc, "cpus="},
		notText:  []string{"pids_limit"},
	}, {
		name: "Apple Container does not describe a flag it never passes",
		rt:   "container", resources: withPids,
		notFlags: []string{"--pids-limit"},
		// The line is still there — the backend's own caps are still applied — it just
		// must not repeat back the one key that went nowhere.
		wantText: []string{"cpus="},
		notText:  []string{"pids_limit"},
	}}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			ws := t.TempDir()
			emptyLoopholeDirs(t)
			o := appliedOptions(t, ws, home, false)

			cfg := appliedTestConfig()
			if tc.resources != nil {
				cfg.Set("resources", tc.resources)
			}

			argv := appliedArgv(t, o, tc.rt, cfg, t.TempDir())
			for _, flag := range tc.wantFlags {
				if !slices.Contains(argv, flag) {
					t.Errorf("argv is missing %q; argv: %v", flag, argv)
				}
			}
			for _, flag := range tc.notFlags {
				if slices.Contains(argv, flag) {
					t.Errorf("argv carries %q, which %s does not honor; argv: %v", flag, tc.rt, argv)
				}
			}

			line := resourceLine(appliedBriefing(t, o, tc.rt, cfg))
			if len(tc.wantText) == 0 && line != "" {
				t.Errorf("the briefing gained a resource line nobody configured: %q", line)
			}
			for _, want := range tc.wantText {
				if !strings.Contains(line, want) {
					t.Errorf("the jail is told %q, want it to state %q", line, want)
				}
			}
			for _, not := range tc.notText {
				if strings.Contains(line, not) {
					t.Errorf("the jail is told %q, which names %q — that flag is never passed "+
						"on %s, so describing it as kernel-enforced is the §6 defect", line, not, tc.rt)
				}
			}
		})
	}
}

// The argv spelling and the briefing's prose come off ONE list. This is the invariant
// under the rows above, asserted directly so a future backend cannot gain a briefed limit
// it does not pass (or pass one it briefs) without a row here going red.
func TestBriefedResourceLimitsAreASubsetOfTheEmittedFlags(t *testing.T) {
	res := jsonx.NewOrderedMap()
	res.Set("memory", "4g")
	res.Set("cpus", 3)
	res.Set("pids_limit", 100)

	for _, rt := range []string{"podman", "container"} {
		t.Run(rt, func(t *testing.T) {
			emitted := map[string]string{}
			for _, lim := range appliedResourceLimits(rt, res, func() string { return "unused" }) {
				emitted[lim.key] = lim.value
			}
			for key, value := range briefedResourceLimits(rt, res) {
				got, ok := emitted[key]
				if !ok {
					t.Errorf("the briefing states %s=%v on %s, but no flag carries it", key, value, rt)
					continue
				}
				if got != value {
					t.Errorf("the briefing states %s=%v while the argv passes %q", key, value, got)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// macos-user: the briefing batch (docs/design/declaration-parity.md §11 step 1)
// ---------------------------------------------------------------------------
//
// THESE ROWS ASSERT THE BRIEFING ONLY, AND THAT IS FORCED. Every test above drives the
// argv and the briefing from one config, because for a container backend the two are the
// pair that must not disagree. macos-user reaches no argv at all — run.Run returns on its
// own arm, hundreds of lines above assembleRunCmd — so a row shaped like the ones above
// could not be written for it, which is exactly why the defects below survived:
// TestBriefingAndArgvAgreeOnTheAppliedNetMode has four rows and structurally cannot have a
// fifth (§5.1.1). The call site that does exist is refreshJailBriefings, on that arm, and
// appliedBriefing drives it — so every assertion here fails if the field it pins is deleted
// from the BriefingInput literal.

// macosUserBriefing composes the jail briefing for a macos-user launch of one config.
func macosUserBriefing(t *testing.T, cfg *jsonx.OrderedMap) string {
	t.Helper()
	return macosUserBriefingWith(t, cfg, nil)
}

// macosUserBriefingWith is macosUserBriefing with the options tweaked first — the context-mount
// siting, which a test states rather than inherits from where this machine keeps its temp dir.
func macosUserBriefingWith(t *testing.T, cfg *jsonx.OrderedMap, tweak func(*Options)) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	emptyLoopholeDirs(t)
	o := appliedOptions(t, t.TempDir(), home, false)
	o.IsMacOS, o.IsLinux = true, false
	if tweak != nil {
		tweak(o)
	}
	return appliedBriefing(t, o, "macos-user", cfg)
}

// briefingHeaderOf returns everything before "## Environment" — the confinement header.
func briefingHeaderOf(t *testing.T, briefing string) string {
	t.Helper()
	i := strings.Index(briefing, "## Environment")
	if i < 0 {
		t.Fatalf("briefing has no ## Environment section:\n%s", briefing)
	}
	return briefing[:i]
}

// DP-B3 / DP-L2. A macos-user sandbox is an ordinary child of the launcher on the
// launcher's own stack — neither Seatbelt profile yolo emits contains a `network*`
// operation at all — so `host` is the mode it HAS, not one it can be put into. The
// briefing told it the opposite in four directions at once: bridge mode, a
// `host.containers.internal` address to reach the host at, a port map that was never
// wired, and the caveat that "a `127.0.0.1` listener in here is not publishable" exactly
// inverted, since here a loopback listener IS the host's.
//
// Driven from a config that asks for BRIDGE and declares both port keys, because that is
// the live shape: resolveNetMode answers "bridge" for a launch that names no mode, so the
// wrong answer is the default one and a launch that never mentioned networking got it.
func TestMacosUserBriefingSaysHostNetworkingAndAdvertisesNoPorts(t *testing.T) {
	netSec := jsonx.NewOrderedMap()
	netSec.Set("mode", "bridge")
	netSec.Set("ports", []any{"8000:3000"})
	netSec.Set("forward_host_ports", []any{5432})
	got := macosUserBriefing(t, appliedTestConfig("network", netSec))

	if para := networkParagraph(got); !strings.Contains(para, "Host networking") ||
		strings.Contains(para, "Bridge mode") {
		t.Errorf("the jail is told %q — this backend is always on the launcher's own "+
			"network stack, so bridge mode is never what ran", para)
	}
	if strings.Contains(got, "host.containers.internal") {
		t.Errorf("named the podman gateway to a jail whose localhost IS the host's:\n%s", got)
	}
	// Both port sections describe forwarding, and nothing forwards here: `-p` and the
	// socat hop both sit below the macos-user return.
	for _, section := range []string{"**Published Ports**", "**Forwarded Host Ports**"} {
		if strings.Contains(got, section) {
			t.Errorf("advertised %s on a backend that publishes and forwards nothing:\n%s",
				section, got)
		}
	}
	// The `8000:3000` row: since 2026-10-05 the launch RELAYS the remap from outside the sandbox
	// (macosuserportrelay.go), so the briefing names it — in the standing-constraints section the
	// relay plan feeds (backendLimits, through refreshJailBriefings), never as a port section,
	// and nowhere else.
	relay := "your `127.0.0.1:3000` is also published at the host's `0.0.0.0:8000` (`network.ports` entry 8000:3000)"
	if !strings.Contains(got, relay) {
		t.Errorf("the briefing does not name the remap the launch relays (want %q):\n%s", relay, got)
	}
	if n := strings.Count(got, "8000"); n != 2 {
		t.Errorf("the briefing mentions 8000 %d times, want only the relay sentence's two:\n%s", n, got)
	}
}

// DP-B1 / DP-L7, BOTH briefing sites, now that macos-user delivers context mounts by link
// (docs/design/context-mounts.md §4 step 4). A mount the backend REFUSES must not be listed —
// a section naming it is a file-path map of a filesystem that does not exist, and the
// `## Limitations` bullet must not describe a context tree nothing linked. A mount it DELIVERS
// is listed at the path the agent opens — under the context dir, never /ctx — with the two
// deltas the link brings (§3.7, §3.8).
func TestMacosUserBriefingListsOnlyTheContextMountsItDelivers(t *testing.T) {
	dir := filepath.Join(floortest.ResolvedTemp(t), "sysadmin")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := func() *jsonx.OrderedMap { return appliedTestConfig("mounts", []any{dir}) }

	refused := macosUserBriefingWith(t, cfg(), func(o *Options) {
		o.macosCtxSiting = sitingWritable(t, "", filepath.Dir(dir))
	})
	if strings.Contains(refused, "## Additional Context Mounts") || strings.Contains(refused, "sysadmin") {
		t.Errorf("listed a context mount the backend refuses:\n%s", refused)
	}
	if strings.Contains(refused, "/ctx/") || strings.Contains(refused, "context mounts under") {
		t.Errorf("the Limitations bullet describes a context tree nothing linked:\n%s", refused)
	}

	delivered := macosUserBriefingWith(t, cfg(), func(o *Options) {
		o.macosCtxSiting = sitingWritable(t, "")
	})
	staged := macosuser.StagedCtxRoot("yolo-ws-abcd1234", "") + "/sysadmin"
	for _, want := range []string{
		"## Additional Context Mounts",
		"- `" + staged + "` (read-only; host `" + dir + "`)",
		"`pwd -P`, `realpath` and",
		"context mounts under `$YOLO_CONTEXT_DIR` are read-only unless marked read-write",
	} {
		if !strings.Contains(delivered, want) {
			t.Errorf("the briefing of a delivered mount lacks %q:\n%s", want, delivered)
		}
	}
	if strings.Contains(delivered, "`/ctx/") {
		t.Errorf("the briefing names a /ctx path, which does not exist on macOS:\n%s", delivered)
	}
}

// DP-B6 / DP-L8. `resources` is read on this backend and IGNORED — the launch says so to
// the human in the same breath the briefing told the agent the caps were kernel-enforced,
// and pointed it at `yolo-cglimit`, which has no delegate to talk to here. Two audiences,
// opposite answers, one launch.
func TestMacosUserBriefingStatesNoResourceLimits(t *testing.T) {
	res := jsonx.NewOrderedMap()
	res.Set("memory", "4g")
	res.Set("cpus", 3)
	got := macosUserBriefing(t, appliedTestConfig("resources", res))

	if line := resourceLine(got); line != "" {
		t.Errorf("the jail is told %q, but this backend passes no resource flag at all — "+
			"describing a cap as kernel-enforced when nothing enforces it is the §6 defect", line)
	}
	// The remedy the line carries is as wrong as the line: `yolo-cglimit` talks to the
	// cgroup delegate, and this backend starts no host service at all. (The limits
	// section names the same binary on purpose — to say it is INERT, which is the
	// opposite claim.)
	if strings.Contains(got, "Sub-limit your own processes") {
		t.Errorf("pointed the agent at a client with no delegate to talk to:\n%s", got)
	}
}

// DP-B21 / DP-L9, THE CALL SITE. backendLimits had no production caller for its whole
// life, so "What this environment does NOT do for you" — the section carrying every
// macos-user constraint an agent reasons wrongly without — had never rendered once. That
// was a stated precondition of a shipped ruling: the no-refusal carve-out of
// noteMacosUserHostByteGaps (since deleted) said it "is only defensible while the deficiency
// is SAID — here, and in the agent's own briefing (backendLimits)".
//
// Asserted through the WRITTEN briefing rather than through backendLimits, which is the
// whole point: the unit tests in backendlimits_test.go were green throughout, because the
// function was never wrong — it was never called.
func TestMacosUserBriefingCarriesTheBackendLimits(t *testing.T) {
	got := macosUserBriefing(t, appliedTestConfig())
	if !strings.Contains(got, "## What this environment does NOT do for you") {
		t.Errorf("the section backendLimits feeds did not render:\n%s", got)
	}
	for _, want := range []string{"writable COPY", "no network namespace", "yolo-ps"} {
		if !strings.Contains(got, want) {
			t.Errorf("the section does not carry %q:\n%s", want, got)
		}
	}

	// And a container backend does not gain it: these are this backend's constraints, and
	// a section that renders everywhere is one readers learn to skip.
	home := t.TempDir()
	t.Setenv("HOME", home)
	emptyLoopholeDirs(t)
	o := appliedOptions(t, t.TempDir(), home, false)
	if podman := appliedBriefing(t, o, "podman", appliedTestConfig()); strings.Contains(
		podman, "What this environment does NOT do for you") {
		t.Errorf("a podman jail gained a limits section it has no limits for:\n%s", podman)
	}
}

// DP-B19 / OQ-DP2, THE CALL SITE for the mechanism. `Mechanism: rt` in refreshJailBriefings
// is what carries the backend into the header; with the boolean it replaced, the header
// could deny a container and then print the container's own primitive vector three lines
// below it — "namespaces", "a baked image" — inside a Seatbelt sandbox with no image.
// The "Jail tooling" line printed twice for the same reason: the branch carried its own
// copy of the line enforcementLines appends.
func TestMacosUserBriefingHeaderDescribesSeatbeltNotNamespaces(t *testing.T) {
	header := briefingHeaderOf(t, macosUserBriefing(t, appliedTestConfig()))

	if strings.Contains(header, "sandboxed container") {
		t.Errorf("claimed a container on a backend that has none:\n%s", header)
	}
	for _, want := range []string{"Seatbelt", "a separate OS user"} {
		if !strings.Contains(header, want) {
			t.Errorf("the enforcement vector omits %q — this is what actually confines the "+
				"agent here:\n%s", want, header)
		}
	}
	for _, not := range []string{"namespaces", "a baked image"} {
		if strings.Contains(header, not) {
			t.Errorf("the enforcement vector claims %q, which no part of this backend "+
				"composes — it is the LINUX preset, printed under a paragraph saying there "+
				"is no container:\n%s", not, header)
		}
	}
	if n := strings.Count(header, "Jail tooling:"); n != 1 {
		t.Errorf("the `Jail tooling` line appears %d times, want 1:\n%s", n, header)
	}
}
