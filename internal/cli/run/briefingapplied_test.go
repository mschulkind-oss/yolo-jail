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

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
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
		stagedPacks{packs: claudePackFixture(t)})
	if err != nil {
		t.Fatalf("refreshJailBriefings: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(staging, briefingStagingName("claude")))
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
