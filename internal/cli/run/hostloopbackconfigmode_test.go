package run

// hostloopbackconfigmode_test.go holds one property that the disposition sweeps in
// hostloopbackargv_test.go structurally cannot see: they vary the network mode
// through the `--network` FLAG only, and the flag is not where the mode usually
// comes from. `network.mode` in yolo-jail.jsonc decides it whenever the launch typed
// no `--network`, and a typed `--network` beats the key (G25 in
// docs/plans/setup-support-gaps.md: `yolo run --help` calls the flag an override, and
// until 2026-09-30 the key silently won). That one resolved mode is read by TWO
// different call sites on two different code paths — the assembler, which decides both
// the network selector and what the jail is told, and the loophole runtime's
// advertiseHostFor, which decides what every loopback-TLS daemon PUBLISHES.
//
// Those two answers come from one predicate (sharesLauncherNetns) precisely so they
// cannot disagree, but a shared predicate only helps if it is fed the same fact. The
// mode is that fact, and it reaches the predicate through o.resolveNetMode at both
// sites — a single-line detail that reads like a cleanup and is the whole of the
// guarantee. MEASURED 2026-08-18: with the assembler reverted to the inline
// `netMode := o.Network` it used to carry, a jail configured `network.mode: "host"`
// got no `--net=host`, a pasta forwarding option it never needed, and
// `YOLO_HOST_LOOPBACK=requested` — an ESCALATING disposition — while its daemons
// published 127.0.0.1, the one address a jail in its own namespace cannot reach.
// That is a refused launch manufactured out of a healthy host, which is the single
// outcome the shared predicate exists to prevent, and every test in this package
// stayed green through it.

import (
	"slices"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// TestAssembleRunCmdReadsTheNetworkModeTheLaunchChose drives the mode from the flag
// AND from the config, opposed, because which one wins is the property and each wrong
// winner fails differently.
//
// A typed `--network` beats `network.mode` (G25). `--network bridge` over a config
// `host` is the shape G25 was filed on, and it is also where the host-loopback decision
// has to come back: bridge is the one mode yolo may take ownership of (OQ-R1), so the
// launch asks pasta to forward the host's loopback and tells the jail `requested`. A
// reader that let the config win would put the jail on the launcher's namespace, the
// thing the flag asked it not to do. `--network host` over a config `bridge` is the
// mirror: a reader that let the config win would tell a jail the user put on the
// launcher's namespace `requested`, an escalating disposition, while its daemons
// published the gateway name. An explicit mode yolo does not own (`slirp4netns` here)
// is never overridden BY that decision: it is the one selector, and no forwarding
// option joins it.
//
// With no flag the config decides, in both directions, and with neither the launch
// runs on the default bridge. A flag of "" is "not typed", which is what the front
// door hands over when the user typed none (TestTheDefaultOptionsLeaveTheModeToTheConfig).
//
// The biconditional, not either half: a severity and an address taken from two
// resolved modes spend a healthy host's jail on a verdict the launch does not support.
func TestAssembleRunCmdReadsTheNetworkModeTheLaunchChose(t *testing.T) {
	cases := []struct {
		name string
		// flagMode is `yolo --network`, "" when the launch typed none; configMode is
		// `network.mode` in the config, "" when the key is unset. Where both are set
		// they are opposed, so no row can pass by reading whichever one the
		// implementation happens to reach for.
		flagMode, configMode string
		// wantSelectors is the WHOLE network argv, because one selector is this
		// feature's hard constraint: podman refuses a container carrying two
		// spellings of --net (see hostloopbackargv_test.go's header).
		wantSelectors []string
		wantDisp      string
		// wantAdvertise is what advertiseHostFor publishes; "" means "leave it to
		// svcendpoint's default", which is the runtime's gateway name.
		wantAdvertise string
	}{{
		name:     "the flag puts a config-host launch back in its own namespace",
		flagMode: "bridge", configMode: "host",
		wantSelectors: []string{pastaArg},
		wantDisp:      paths.HostLoopbackRequested,
		wantAdvertise: "",
	}, {
		name:     "the flag makes a config-bridge launch share the launcher's namespace",
		flagMode: "host", configMode: "bridge",
		wantSelectors: []string{"--net=host"},
		wantDisp:      paths.HostLoopbackShared,
		wantAdvertise: "127.0.0.1",
	}, {
		name:     "an explicit mode yolo does not own gets no forwarding option",
		flagMode: "slirp4netns", configMode: "bridge",
		wantSelectors: []string{"--net=slirp4netns"},
		wantDisp:      paths.HostLoopbackUnknown,
		wantAdvertise: "",
	}, {
		name:     "with no flag the config makes the launch share the launcher's namespace",
		flagMode: "", configMode: "host",
		wantSelectors: []string{"--net=host"},
		wantDisp:      paths.HostLoopbackShared,
		wantAdvertise: "127.0.0.1",
	}, {
		name:     "with no flag the config keeps the launch in its own namespace",
		flagMode: "", configMode: "bridge",
		wantSelectors: []string{pastaArg},
		wantDisp:      paths.HostLoopbackRequested,
		wantAdvertise: "",
	}, {
		name:     "with neither the launch runs on the default bridge",
		flagMode: "", configMode: "",
		wantSelectors: []string{pastaArg},
		wantDisp:      paths.HostLoopbackRequested,
		wantAdvertise: "",
	}}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			emptyLoopholeDirs(t)
			o, _ := pastaHostOptions(t, "/ws", home, false)
			o.Network = tc.flagMode

			in := relocationInput(t, "podman", t.TempDir(), nil)
			if tc.configMode != "" {
				netSec := jsonx.NewOrderedMap()
				netSec.Set("mode", tc.configMode)
				in.cfg.Set("network", netSec)
			}

			argv := o.assembleRunCmd(in)

			if got := networkSelectors(argv); !slices.Equal(got, tc.wantSelectors) {
				t.Errorf("network selectors = %v, want %v — the assembler resolved a different "+
					"mode than the launch asked for", got, tc.wantSelectors)
			}
			got, ok := envValue(argv, paths.HostLoopbackEnvVar)
			if !ok {
				t.Fatalf("every launch carries a disposition; argv: %v", argv)
			}
			if got != tc.wantDisp {
				t.Errorf("%s = %q, want %q", paths.HostLoopbackEnvVar, got, tc.wantDisp)
			}
			advertised := o.advertiseHostFor("podman", in.cfg)
			if advertised != tc.wantAdvertise {
				t.Errorf("advertiseHostFor = %q, want %q", orDefaultAdvertise(advertised),
					orDefaultAdvertise(tc.wantAdvertise))
			}
			// The biconditional, restated on this input for the same reason
			// TestAssembleRunCmdSharedMatchesWhatTheDaemonsPublish states it on the
			// flag: `shared` escalates, so a jail told it while its daemons published
			// the gateway name refuses a launch nothing was wrong with, and a jail on
			// the launcher's own loopback told anything else can never report a real
			// fault as one.
			if (got == paths.HostLoopbackShared) != (advertised == "127.0.0.1") {
				t.Errorf("%s = %q but the daemons publish %q — the severity the witness applies "+
					"and the address it dials must come from one resolved mode",
					paths.HostLoopbackEnvVar, got, orDefaultAdvertise(advertised))
			}
		})
	}
}

// TestTheDefaultOptionsLeaveTheModeToTheConfig is the other half of G25, and the one a
// precedence fix alone gets wrong: NewDefaultOptions is what the CLI front door, the
// keeper and the capture jail all start from, and it used to say `Network: "bridge"` —
// a launch that typed no flag was indistinguishable from one that typed
// `--network bridge`. Let the flag win without emptying that default and every launch
// beats its own config: a workspace configured `network.mode: "host"` launches bridged.
//
// Driven through the production constructor and fillDefaults, the two things every
// production Options passes through, and asked through resolveNetMode, the one reader
// of the mode.
func TestTheDefaultOptionsLeaveTheModeToTheConfig(t *testing.T) {
	o := NewDefaultOptions()
	fillDefaults(&o)
	for _, mode := range []string{"host", "bridge"} {
		netSec := jsonx.NewOrderedMap()
		netSec.Set("mode", mode)
		if got := o.resolveNetMode(newConfig("network", netSec)); got != mode {
			t.Errorf("a launch that typed no --network resolves %q under network.mode %q — the "+
				"default Options carry a mode of their own, so the config's is overridden", got, mode)
		}
	}
	if got := o.resolveNetMode(jsonx.NewOrderedMap()); got != "bridge" {
		t.Errorf("with neither a flag nor a key the launch resolves %q, want the default bridge", got)
	}
}

// TestTheKeeperResolvesTheNetworkModeItsLaunchResolved carries the same precedence
// through the plan a fresh launch hands its keeper, which starts the host services and
// so decides what each loopback-TLS daemon advertises (keeperPlan.Network). The plan
// carries the flag as typed, "" for none, and the keeper resolves it against the
// plan's config: a keeper that turned "" back into an explicit bridge would advertise
// the gateway name to a jail its launch had put on the launcher's namespace under a
// config `host`, and tell nobody.
//
// Through keeperPlanFor and newKeeper, the production pair, so neither the plan's
// field nor the keeper's own defaults can drop the distinction unnoticed.
func TestTheKeeperResolvesTheNetworkModeItsLaunchResolved(t *testing.T) {
	cases := []struct {
		flagMode, configMode, want string
	}{
		{"", "host", "host"},
		{"bridge", "host", "bridge"},
		{"host", "bridge", "host"},
		{"", "", "bridge"},
	}
	for _, tc := range cases {
		t.Run("flag="+tc.flagMode+",config="+tc.configMode, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			emptyLoopholeDirs(t)
			o := goldenOptions(t.TempDir(), home)
			o.Network = tc.flagMode
			cfg := jsonx.NewOrderedMap()
			if tc.configMode != "" {
				netSec := jsonx.NewOrderedMap()
				netSec.Set("mode", tc.configMode)
				cfg.Set("network", netSec)
			}
			if got := o.resolveNetMode(cfg); got != tc.want {
				t.Fatalf("the launch resolves %q, want %q", got, tc.want)
			}

			plan, err := o.keeperPlanFor(cfg, "podman", "yolo-netmode", stagedPacks{}, nil, nil, nil,
				"", "", []string{"podman", "run"}, &assembleInput{})
			if err != nil {
				t.Fatal(err)
			}
			k := newKeeper(plan, KeeperSeams{}, nil, nil, nil, nil)
			keeperCfg, err := plan.config()
			if err != nil {
				t.Fatal(err)
			}
			if got := k.o.resolveNetMode(keeperCfg); got != tc.want {
				t.Errorf("the keeper resolves %q where its launch resolved %q — the daemons it "+
					"starts would advertise for a different namespace than the jail's", got, tc.want)
			}
		})
	}
}

// TestTheModeWarningsNameWhatChoseIt pins the two launch warnings about an explicit mode to
// whichever source chose it. Once a typed `--network` beats `network.mode` (G25), a warning
// that always said "network.mode" sends the user to the wrong place, and on Apple Container
// it told someone whose key says bridge that their key says host and to remove it.
//
// The explicit-mode warning on a pasta host is reachable only through the flag, since the
// validator refuses a key other than bridge or host and host is silent there, so its row is
// a flag over a config bridge. The Apple Container warning has a row for each source.
func TestTheModeWarningsNameWhatChoseIt(t *testing.T) {
	cases := []struct {
		name, rt, flagMode, configMode string
		want, notWant                  []string
	}{{
		name: "a typed explicit mode is named as the flag",
		rt:   "podman", flagMode: "none", configMode: "bridge",
		want:    []string{"Warning: --network none was given, so yolo is not requesting"},
		notWant: []string{"network.mode is set to"},
	}, {
		name: "a typed host on Apple Container is named as the flag",
		rt:   "container", flagMode: "host", configMode: "bridge",
		want:    []string{"Warning: --network host is NOT honored on Apple Container", "Drop the flag"},
		notWant: []string{"network.mode", "Remove the key"},
	}, {
		name: "a configured host on Apple Container is named as the key",
		rt:   "container", flagMode: "", configMode: "host",
		want:    []string{`Warning: network.mode "host" is NOT honored on Apple Container`, "Remove the key"},
		notWant: []string{"--network host", "Drop the flag"},
	}}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			emptyLoopholeDirs(t)
			o, _ := pastaHostOptions(t, "/ws", home, false)
			o.Network = tc.flagMode
			var stderr strings.Builder
			o.Stderr = &stderr

			in := relocationInput(t, tc.rt, t.TempDir(), nil)
			netSec := jsonx.NewOrderedMap()
			netSec.Set("mode", tc.configMode)
			in.cfg.Set("network", netSec)
			o.assembleRunCmd(in)

			got := stderr.String()
			for _, w := range tc.want {
				if !strings.Contains(got, w) {
					t.Errorf("stderr lacks %q:\n%s", w, got)
				}
			}
			for _, w := range tc.notWant {
				if strings.Contains(got, w) {
					t.Errorf("stderr names %q, which did not choose this launch's mode:\n%s", w, got)
				}
			}
		})
	}
}
