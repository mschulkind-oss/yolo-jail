package run

// hostloopbackconfigmode_test.go holds one property that the disposition sweeps in
// hostloopbackargv_test.go structurally cannot see: they vary the network mode
// through the `--network` FLAG only, and the flag is not where the mode usually
// comes from. `network.mode` in yolo-jail.jsonc OVERRIDES it, and that config value
// is read by TWO different call sites on two different code paths — the assembler,
// which decides both the network selector and what the jail is told, and the
// loophole runtime's advertiseHostFor, which decides what every loopback-TLS daemon
// PUBLISHES.
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
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// TestAssembleRunCmdReadsTheNetworkModeTheConfigChose drives the mode from the
// config in BOTH directions, because the config wins over the flag and each
// direction fails differently.
//
// Config `host` over flag `bridge` is the live shape — a user who set host
// networking in yolo-jail.jsonc — and a reader that took the flag would put the jail
// on the launcher's namespace while telling it `requested` and pointing it at a
// gateway name. Config `bridge` over flag `host` is the mirror: a reader that took
// the flag would tell a jail in its OWN namespace `shared`, which is also an
// escalating disposition, while the daemons published the gateway name it can
// actually reach. Both spend a healthy host's jail on a verdict the launch does not
// support, which is why the assertion is the biconditional rather than either half.
func TestAssembleRunCmdReadsTheNetworkModeTheConfigChose(t *testing.T) {
	cases := []struct {
		name string
		// flagMode is `yolo --network`; configMode is `network.mode` in the config,
		// which overrides it. They are deliberately opposed in every row, so no row
		// can pass by reading whichever one the implementation happens to reach for.
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
		name:     "the config makes a bridge launch share the launcher's namespace",
		flagMode: "bridge", configMode: "host",
		wantSelectors: []string{"--net=host"},
		wantDisp:      paths.HostLoopbackShared,
		wantAdvertise: "127.0.0.1",
	}, {
		name:     "the config puts a host launch back in its own namespace",
		flagMode: "host", configMode: "bridge",
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
			netSec := jsonx.NewOrderedMap()
			netSec.Set("mode", tc.configMode)
			in.cfg.Set("network", netSec)

			argv := o.assembleRunCmd(in)

			if got := networkSelectors(argv); !slices.Equal(got, tc.wantSelectors) {
				t.Errorf("network selectors = %v, want %v — the assembler resolved a different "+
					"mode than the config asked for", got, tc.wantSelectors)
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
