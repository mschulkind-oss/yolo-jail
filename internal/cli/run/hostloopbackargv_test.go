package run

// hostloopbackargv_test.go covers the WIRING of the host-loopback decision into
// the launch argv. hostloopback_test.go proves decideHostLoopback returns the
// right plan; this file proves assembleRunCmd puts that plan on the command line
// without contradicting the network flag it already emits.
//
// # Why this is a separate, harder property than "the decision is right"
//
// `--network=` and `--net=` are the SAME podman flag, and podman does not merge
// two spellings of it — it refuses the container outright. Measured on podman
// 5.8.4 (2026-08-17), every pairing this file exists to prevent:
//
//	--net=host  --network=pasta:…  → cannot set multiple networks without bridge
//	                                 network mode, selected mode host
//	--net=none  --network=pasta:…  → …selected mode none
//	--net=bridge --network=pasta:… → can only set extra network names, selected
//	                                 mode pasta conflicts with bridge
//
// All three are `podman create` errors, before the image is even pulled. So a
// decision function that returns a perfectly correct plan still BRICKS EVERY
// LAUNCH if the caller appends it next to an existing --net=. That caller-side
// property is what these tests hold, and nothing in hostloopback_test.go can see
// it: it never builds an argv.
//
// The nested case is the one that would hurt first and loudest. Podman-in-podman
// forces --net=host (netavark cannot create a netns without NET_ADMIN), and it is
// also the mode this repo's own mandated verification loop runs in — `yolo --
// bash` from inside the dev jail (AGENTS.md, "Nested-jail verification"). Hoisting
// the decision out of the else-branch, which reads like a harmless "compute it
// once" cleanup, would therefore take out the developer's ability to launch a jail
// at all, on the very machine where the fix cannot be tested for real.

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// networkProbes filters a recorded subprocess list down to the two commands the
// host-loopback fact gathering runs. The assembler shells out for other reasons
// (git identity, device probes) on every path, so "ran nothing at all" is not the
// assertion available — "never asked the network stack anything" is.
func networkProbes(ran []string) []string {
	var out []string
	for _, cmd := range ran {
		if strings.Contains(cmd, "podman info") || strings.Contains(cmd, "pasta") ||
			strings.Contains(cmd, "slirp4netns") {
			out = append(out, cmd)
		}
	}
	return out
}

// envValue returns the value of the LAST `-e KEY=VALUE` pair naming key, and
// whether one was present at all. Last wins because that is what podman does with
// a repeated -e, so a test that read the first would pass on an argv the runtime
// resolves differently.
func envValue(argv []string, key string) (string, bool) {
	val, found := "", false
	for i := 0; i+1 < len(argv); i++ {
		if argv[i] != "-e" {
			continue
		}
		if v, ok := strings.CutPrefix(argv[i+1], key+"="); ok {
			val, found = v, true
		}
	}
	return val, found
}

// networkSelectors returns every argument in argv that selects podman's network
// mode. Both spellings are collected because podman treats them as one flag: the
// count is the invariant, not which name was used.
func networkSelectors(argv []string) []string {
	var out []string
	for _, a := range argv {
		if strings.HasPrefix(a, "--net=") || strings.HasPrefix(a, "--network=") ||
			a == "--net" || a == "--network" {
			out = append(out, a)
		}
	}
	return out
}

// pastaFixtureExe is the executable podman reports for pasta in
// podmanInfoFixture. The capability probe runs exactly that path, so the canned
// --help has to be keyed on it.
const pastaFixtureExe = "/nix/store/xxxx-podman-5.8.4/libexec/podman/pasta"

// slirpFixtureExe is the slirp4netns executable podman reports in the same
// fixture. The fallback probe runs THAT path and never a PATH lookup, so the
// canned --help has to be keyed on it — see
// TestHostLoopbackFallbackTrustsOnlyPodmansOwnLookup for why the distinction is
// load-bearing rather than incidental.
const slirpFixtureExe = "/bin/slirp4netns"

// pastaHelpWithFlag / pastaHelpWithoutFlag are the two answers a real pasta gives
// about itself, and they are the fork in the whole feature: the first makes yolo
// emit the forwarding option, the second makes it degrade and say so (OQ-R3).
const (
	pastaHelpWithFlag    = "Usage: pasta [OPTION]...\n  " + pastaMapHostLoopbackFlag + " ADDR\tTranslate ADDR to refer to host\n"
	pastaHelpWithoutFlag = "Usage: pasta [OPTION]...\n  --map-gw\tMap the gateway\n"
)

// pastaHostOptions wires an Options that looks like a rootless podman host running
// pasta with a binary that advertises --map-host-loopback — the one combination
// that makes decideHostLoopback emit an argv — and records every subprocess the
// assembler asked for, so a test can assert that NONE was run as easily as that
// the right one was.
func pastaHostOptions(t *testing.T, ws, home string, containerenv bool) (*Options, *[]string) {
	t.Helper()
	return pastaHostOptionsWithHelp(t, ws, home, containerenv, pastaHelpWithFlag)
}

// pastaHostOptionsWithHelp is pastaHostOptions with the pasta binary's own answer
// as a parameter, so the degraded host — a pasta whose --help does not list the
// flag — can be assembled too. That host launches, which is the point of OQ-R3,
// and what it tells the jail differs from both a working host and an unknown one.
//
// Its slirp4netns answers nothing, so the degraded host stays degraded: that is a
// host with no usable fallback. pastaHostOptionsWithHelps is the one that has one.
func pastaHostOptionsWithHelp(t *testing.T, ws, home string, containerenv bool, help string) (*Options, *[]string) {
	t.Helper()
	return pastaHostOptionsWithHelps(t, ws, home, containerenv, help, "")
}

// pastaHostOptionsWithHelps adds the slirp4netns binary's own answer, which is the
// second fork in the feature: an old pasta on a host WITH a usable slirp4netns is
// moved onto that stack and works, rather than launching with dead services. An
// empty slirpHelp means the probe finds nothing to run, the shape of a host with no
// slirp4netns installed.
func pastaHostOptionsWithHelps(t *testing.T, ws, home string, containerenv bool, pastaHelp, slirpHelp string) (*Options, *[]string) {
	t.Helper()
	return podmanHostOptions(t, ws, home, containerenv, podmanInfoFixture, pastaHelp, slirpHelp)
}

// podmanHostOptions is pastaHostOptionsWithHelps with `podman info`'s own answer as
// a parameter too. It exists for the host shape whose answer is the thing under test
// rather than a constant: a podman below minPodmanReportingVersion names no rootless
// stack at all, so the fixture — not the helper --help output — is what puts the
// launch on the unnamed-backend path.
func podmanHostOptions(t *testing.T, ws, home string, containerenv bool, info, pastaHelp, slirpHelp string) (*Options, *[]string) {
	t.Helper()

	var ran []string
	o := goldenOptions(ws, home)
	o.LookPath = func(name string) (string, bool) {
		if name == "podman" {
			return "/usr/bin/podman", true
		}
		return "", false
	}
	o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
		joined := strings.Join(argv, " ")
		ran = append(ran, joined)
		switch joined {
		case "/usr/bin/podman info --format json":
			return ExecResult{Ran: true, RC: 0, Stdout: info}
		case pastaFixtureExe + " --help":
			return ExecResult{Ran: true, RC: 0, Stdout: pastaHelp}
		case slirpFixtureExe + " --help":
			if slirpHelp == "" {
				return ExecResult{Ran: false}
			}
			return ExecResult{Ran: true, RC: 0, Stdout: slirpHelp}
		}
		return ExecResult{Ran: false}
	}
	// PathExists answers ONLY the nested-container probe. Everything else (device
	// nodes, the host nix store) stays absent so the rest of the argv is the same
	// deterministic fixture the golden test uses.
	o.PathExists = func(p string) bool { return containerenv && p == "/run/.containerenv" }
	return o, &ran
}

// TestAssembleRunCmdEmitsAtMostOneNetworkSelector is the launch-safety property,
// swept over every shape a real host can present: the runtime, whether this
// process is itself in a container, and the effective network.mode. Podman refuses
// a container carrying two network selectors (see the file header for the measured
// errors), so "at most one" is not a style preference — it is the difference
// between a jail that starts and a jail that does not.
//
// The host underneath is held at the WORST cases for this property throughout:
// the two shapes in which decideHostLoopback wants to emit a selector at all — a
// pasta that advertises the flag, and an old pasta with a slirp4netns to fall back
// to. Those are the only configurations in which a caller-side mistake is
// observable, and the fallback is the one that emits a selector for a stack the
// host's podman did NOT default to.
func TestAssembleRunCmdEmitsAtMostOneNetworkSelector(t *testing.T) {
	hosts := map[string]func(*testing.T, string, string, bool) (*Options, *[]string){
		"pasta": pastaHostOptions,
		"slirpfallback": func(t *testing.T, ws, home string, inContainer bool) (*Options, *[]string) {
			return pastaHostOptionsWithHelps(t, ws, home, inContainer, pastaHelpWithoutFlag, slirpHelpWithFlag)
		},
	}
	for hostName, mkHost := range hosts {
		for _, rt := range []string{"podman", "container"} {
			for _, inContainer := range []bool{false, true} {
				for _, netMode := range []string{"bridge", "host", "none", "private"} {
					name := hostName + "/" + rt + "/" + netMode
					if inContainer {
						name += "/nested"
					}
					t.Run(name, func(t *testing.T) {
						home := t.TempDir()
						t.Setenv("HOME", home)
						emptyLoopholeDirs(t)
						o, _ := mkHost(t, "/ws", home, inContainer)
						o.Network = netMode

						got := networkSelectors(o.assembleRunCmd(relocationInput(t, rt, t.TempDir(), nil)))
						if len(got) > 1 {
							t.Errorf("podman refuses a container with two network selectors; argv has %d: %v",
								len(got), got)
						}
					})
				}
			}
		}
	}
}

// TestAssembleRunCmdNestedPodmanKeepsNetHostAlone pins the specific pairing that
// would break this repo's own development loop. Podman-in-podman MUST get
// --net=host and nothing else; asking the runtime for host-loopback forwarding
// there is both meaningless (the jail shares the launcher's stack, so the two
// loopbacks are already one — design doc §7) and fatal (podman: "cannot set
// multiple networks … selected mode host").
//
// It also asserts the assembler asked the host NOTHING. Reaching the decision at
// all on this path means `podman info` and `pasta --help` ran to produce an answer
// that must be discarded, which is latency on every nested launch and, more to the
// point, evidence that the guard is positional rather than real.
func TestAssembleRunCmdNestedPodmanKeepsNetHostAlone(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	emptyLoopholeDirs(t)
	o, ran := pastaHostOptions(t, "/ws", home, true)

	got := networkSelectors(o.assembleRunCmd(relocationInput(t, "podman", t.TempDir(), nil)))
	if !slices.Equal(got, []string{"--net=host"}) {
		t.Errorf("nested podman must launch with exactly --net=host, got %v", got)
	}
	if probes := networkProbes(*ran); len(probes) != 0 {
		t.Errorf("the nested branch must not ask the network stack anything, it ran: %v", probes)
	}
}

// TestAssembleRunCmdAppleContainerAsksForNothing: Apple Container handles its own
// networking, so the assembler emits no selector and must not shell out to a
// podman that may not even be installed on that host.
func TestAssembleRunCmdAppleContainerAsksForNothing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	emptyLoopholeDirs(t)
	o, ran := pastaHostOptions(t, "/ws", home, false)

	got := networkSelectors(o.assembleRunCmd(relocationInput(t, "container", t.TempDir(), nil)))
	if len(got) != 0 {
		t.Errorf("Apple Container takes no network selector, got %v", got)
	}
	if probes := networkProbes(*ran); len(probes) != 0 {
		t.Errorf("the Apple Container branch must not run podman, it ran: %v", probes)
	}
}

// TestAssembleRunCmdBridgePastaReachesTheArgv is the other direction: the fix has
// to actually arrive. A decision function that returns the right plan into a
// caller that drops it is indistinguishable from the bug it was written to fix —
// silently, since the symptom is a service being unreachable hours later.
func TestAssembleRunCmdBridgePastaReachesTheArgv(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	emptyLoopholeDirs(t)
	o, _ := pastaHostOptions(t, "/ws", home, false)

	got := networkSelectors(o.assembleRunCmd(relocationInput(t, "podman", t.TempDir(), nil)))
	if !slices.Equal(got, []string{pastaArg}) {
		t.Errorf("the default bridge path on a confirmed pasta host must emit exactly %q, got %v",
			pastaArg, got)
	}
}

// TestAssembleRunCmdExplicitModeKeepsOnlyItsOwnFlag. An explicit network.mode is
// the user's (OQ-R1) and yolo warns instead of overriding — but the warning is
// only safe because the argv still carries ONE selector. Pinning the flag next to
// the warning is what stops a future "warn AND fix it anyway" from shipping: that
// version emits --net=none --network=pasta:… and podman refuses the container.
func TestAssembleRunCmdExplicitModeKeepsOnlyItsOwnFlag(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	emptyLoopholeDirs(t)
	o, _ := pastaHostOptions(t, "/ws", home, false)
	o.Network = "none"
	var stdout strings.Builder
	o.Stdout = &stdout

	got := networkSelectors(o.assembleRunCmd(relocationInput(t, "podman", t.TempDir(), nil)))
	if !slices.Equal(got, []string{"--net=none"}) {
		t.Errorf("an explicit mode must be the only selector, got %v", got)
	}
	if !strings.Contains(stdout.String(), "network.mode is set to 'none'") {
		t.Errorf("the user keeps the bug and must be told so, got:\n%s", stdout.String())
	}
}

// TestAssembleRunCmdOptOutRestoresTodaysArgv. YOLO_NO_HOST_LOOPBACK is the escape
// hatch for exactly the failure this file is about — a user whose jail stopped
// launching after the emitted flag — so it has to be worth reaching for: the argv
// it produces must be byte-for-byte the one from before the feature existed.
// Comparing whole argvs rather than just the selectors is deliberate; the hatch
// promises "today's behaviour", not "today's behaviour except for one flag".
func TestAssembleRunCmdOptOutRestoresTodaysArgv(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	emptyLoopholeDirs(t)
	wsState := t.TempDir()

	base := goldenOptions("/ws", home)
	want := base.assembleRunCmd(relocationInput(t, "podman", wsState, nil))

	o, _ := pastaHostOptions(t, "/ws", home, false)
	o.Getenv = func(k string) string {
		if k == hostLoopbackOptOutEnv {
			return "1"
		}
		return ""
	}
	var stdout strings.Builder
	o.Stdout = &stdout
	got := o.assembleRunCmd(relocationInput(t, "podman", wsState, nil))

	if !slices.Equal(got, want) {
		t.Errorf("%s must restore the pre-feature argv exactly\ngot:  %v\nwant: %v",
			hostLoopbackOptOutEnv, got, want)
	}
	// And it must SAY what it suppressed, or a user who set it once and forgot has
	// no way to connect "my broker is unreachable" back to their own env var.
	if !strings.Contains(stdout.String(), hostLoopbackOptOutEnv) {
		t.Errorf("the opt-out must name itself when it suppresses the flag, got:\n%s", stdout.String())
	}
}

// TestAssembleRunCmdTellsTheJailWhatItRequested. The jail cannot see the argv it
// was launched with, and from inside it an unreachable service looks the same
// whether the forwarding was requested or was never asked for — so the launcher's
// decision has to travel with the container. This is the wiring of that: a host
// where the option WAS emitted must carry `requested`, because that is the value
// the in-jail witness is allowed to escalate on (OQ-R2, scoped by OQ-R3).
func TestAssembleRunCmdTellsTheJailWhatItRequested(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	emptyLoopholeDirs(t)
	o, _ := pastaHostOptions(t, "/ws", home, false)

	argv := o.assembleRunCmd(relocationInput(t, "podman", t.TempDir(), nil))
	if !slices.Contains(argv, pastaArg) {
		t.Fatalf("fixture drift: the forwarding option is not on the argv: %v", networkSelectors(argv))
	}
	got, ok := envValue(argv, paths.HostLoopbackEnvVar)
	if !ok {
		t.Fatalf("the jail was told nothing about a launch that DID request forwarding; "+
			"an unreachable service there is a fault and must be reportable as one. argv: %v", argv)
	}
	if got != paths.HostLoopbackRequested {
		t.Errorf("%s = %q, want %q", paths.HostLoopbackEnvVar, got, paths.HostLoopbackRequested)
	}
}

// TestAssembleRunCmdTellsTheJailWhenTheHostCannot is the other branch of "
// unsupported is not broken": a pasta that does not advertise the flag launches
// (OQ-R3 re-ruled refusal out), emits no network selector, and must tell the jail
// so — otherwise the witness sees an unreachable service on a launch it cannot
// attribute, and the honest "this host cannot do it, here is what to upgrade"
// becomes a generic outage report.
func TestAssembleRunCmdTellsTheJailWhenTheHostCannot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	emptyLoopholeDirs(t)
	o, _ := pastaHostOptionsWithHelp(t, "/ws", home, false, pastaHelpWithoutFlag)
	var stdout strings.Builder
	o.Stdout = &stdout

	argv := o.assembleRunCmd(relocationInput(t, "podman", t.TempDir(), nil))
	if sel := networkSelectors(argv); len(sel) != 0 {
		t.Errorf("an old pasta must be asked for nothing, got %v", sel)
	}
	got, ok := envValue(argv, paths.HostLoopbackEnvVar)
	if !ok {
		t.Fatalf("the degraded host must be declared to the jail as a known limitation; argv: %v", argv)
	}
	if got != paths.HostLoopbackUnsupported {
		t.Errorf("%s = %q, want %q", paths.HostLoopbackEnvVar, got, paths.HostLoopbackUnsupported)
	}
	// And it launches, loudly. A refusal here is the outcome OQ-R3 rejected.
	if !strings.Contains(stdout.String(), minPasstVersion) {
		t.Errorf("the limitation must name the version that fixes it, got:\n%s", stdout.String())
	}
}

// TestAssembleRunCmdOldPastaWithSlirpLaunchesOnTheOlderStack is the fallback
// arriving on the argv, and it is TWO flags that have to travel together: the
// network selector that makes slirp4netns forward the host's loopback, and the
// hosts entry that aims the name the jail dials at the address that forwards it.
// Measured 2026-08-17 (see slirp4netnsHostAddr) — with the option alone, podman
// points host.containers.internal at the host's GLOBAL address and the jail
// reaches nothing, which the launcher would then have reported as `requested`.
//
// It is also still ONE network selector, the property this whole file exists for.
func TestAssembleRunCmdOldPastaWithSlirpLaunchesOnTheOlderStack(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	emptyLoopholeDirs(t)
	o, _ := pastaHostOptionsWithHelps(t, "/ws", home, false, pastaHelpWithoutFlag, slirpHelpWithFlag)
	var stdout strings.Builder
	o.Stdout = &stdout

	argv := o.assembleRunCmd(relocationInput(t, "podman", t.TempDir(), nil))
	if got := networkSelectors(argv); !slices.Equal(got, []string{slirpArgs[0]}) {
		t.Errorf("want exactly one selector %q, got %v", slirpArgs[0], got)
	}
	if !slices.Contains(argv, slirpArgs[1]) {
		t.Errorf("the hosts entry is half the fix and must reach the argv (%q): %v", slirpArgs[1], argv)
	}
	// The jail is told `requested`: yolo DID ask this host to forward the loopback,
	// so a service that is still unreachable in there is a fault, not the known
	// limitation an old passt would otherwise have been.
	got, ok := envValue(argv, paths.HostLoopbackEnvVar)
	if !ok || got != paths.HostLoopbackRequested {
		t.Errorf("%s = %q (present=%v), want %q", paths.HostLoopbackEnvVar, got, ok,
			paths.HostLoopbackRequested)
	}
	if !strings.Contains(stdout.String(), "slirp4netns") {
		t.Errorf("moving a jail onto another network stack must be said out loud, got:\n%s",
			stdout.String())
	}
}

// TestAssembleRunCmdSpellsEveryStateOnTheWire covers the shapes that do NOT go
// through decideHostLoopback with a positive answer, which is where OQ-R6 actually
// bites. The variable used to be omitted for all of them, so a single absence stood
// for a jail that SHARES this process's network namespace, a launcher that reached no
// conclusion, and a launcher too old to have the variable at all — three different
// severities collapsed into one silence, the strongest case indistinguishable from
// the vaguest.
//
// The nested row is the one that has to be asserted HERE and not against the decision
// function: podman-in-podman never reaches it (the branch above emits --net=host and
// returns), so a `shared` computed inside hostloopback.go would be computed by code
// that did not run. That mistake was made and MEASURED in this repo on 2026-08-18 —
// the value never appeared in the jail — which is why the assertion is on the argv.
func TestAssembleRunCmdSpellsEveryStateOnTheWire(t *testing.T) {
	cases := []struct {
		name        string
		rt          string
		netMode     string
		inContainer bool
		optOut      bool
		want        string
	}{{
		// The forced --net=host: one namespace, so there is no forwarding hop to
		// have got wrong and an unreachable service has no host-stack excuse.
		name: "nested podman shares the launcher's stack", rt: "podman", inContainer: true,
		want: paths.HostLoopbackShared,
	}, {
		// The same namespace by choice rather than by force. It reaches the decision
		// (unlike the row above) and the decision declines to act on it, which used
		// to leave it absent.
		name: "network.mode host shares it by choice", rt: "podman", netMode: "host",
		want: paths.HostLoopbackShared,
	}, {
		// Apple Container does its own networking and gets no host services at all,
		// so it shares nothing — even spelling `host`, which it never receives.
		name: "apple container shares nothing, whatever the mode says", rt: "container",
		netMode: "host", want: paths.HostLoopbackUnknown,
	}, {
		// An explicit mode yolo will not override (OQ-R1). This host may well support
		// the forwarding; yolo simply did not ask, and `unsupported` would be a lie.
		name: "an explicit mode yolo declined to touch", rt: "podman", netMode: "none",
		want: paths.HostLoopbackUnknown,
	}, {
		// The user turned the fix off. Their own choice must never come back to them
		// as a broken jail, and `unknown` is both true and never escalated.
		name: "the opt-out threw the decision away", rt: "podman", optOut: true,
		want: paths.HostLoopbackUnknown,
	}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			emptyLoopholeDirs(t)
			o, _ := pastaHostOptions(t, "/ws", home, tc.inContainer)
			if tc.netMode != "" {
				o.Network = tc.netMode
			}
			if tc.optOut {
				o.Getenv = func(k string) string {
					if k == hostLoopbackOptOutEnv {
						return "1"
					}
					return ""
				}
			}
			var stdout strings.Builder
			o.Stdout = &stdout

			argv := o.assembleRunCmd(relocationInput(t, tc.rt, t.TempDir(), nil))
			got, ok := envValue(argv, paths.HostLoopbackEnvVar)
			if !ok {
				t.Fatalf("every launch carries a disposition; absent is reserved for a launcher "+
					"older than the variable. argv: %v", argv)
			}
			if got != tc.want {
				t.Errorf("%s = %q, want %q", paths.HostLoopbackEnvVar, got, tc.want)
			}
		})
	}
}

// TestAssembleRunCmdSharedMatchesWhatTheDaemonsPublish is the property that keeps the
// new spelling honest, swept over every shape a launch can take: the jail is told
// `shared` if and ONLY if the loophole runtime publishes 127.0.0.1 to it.
//
// The two answers come from one predicate (sharesLauncherNetns) and this is what that
// is FOR. `shared` is an escalating disposition under OQ-R5 — with one namespace an
// unreachable service has no host-stack excuse — so a launch told `shared` while its
// daemons advertised the gateway name would escalate a failure at an address the jail
// was never given, which is a refused launch manufactured out of a healthy host. The
// inverse is quieter and just as wrong: daemons on 127.0.0.1 with the jail told
// something unattributable is a real fault that can never be reported as one.
func TestAssembleRunCmdSharedMatchesWhatTheDaemonsPublish(t *testing.T) {
	for _, rt := range []string{"podman", "container"} {
		for _, inContainer := range []bool{false, true} {
			for _, netMode := range []string{"bridge", "host", "none", "private"} {
				name := rt + "/" + netMode
				if inContainer {
					name += "/nested"
				}
				t.Run(name, func(t *testing.T) {
					home := t.TempDir()
					t.Setenv("HOME", home)
					emptyLoopholeDirs(t)
					o, _ := pastaHostOptions(t, "/ws", home, inContainer)
					o.Network = netMode
					in := relocationInput(t, rt, t.TempDir(), nil)

					argv := o.assembleRunCmd(in)
					got, _ := envValue(argv, paths.HostLoopbackEnvVar)
					advertised := o.advertiseHostFor(rt, in.cfg)

					if (got == paths.HostLoopbackShared) != (advertised == "127.0.0.1") {
						t.Errorf("%s = %q but the daemons publish %q — the severity the witness "+
							"applies and the address it dials must come from one fact",
							paths.HostLoopbackEnvVar, got, orDefaultAdvertise(advertised))
					}
				})
			}
		}
	}
}

// orDefaultAdvertise names the empty advertise host in a failure message. "" means
// "leave it to svcendpoint's default", which is the runtime's gateway name — the
// thing the reader has to picture to see why the mismatch matters.
func orDefaultAdvertise(s string) string {
	if s == "" {
		return "the gateway name (svcendpoint default)"
	}
	return s
}

// TestAssembleRunCmdForwardsTheWitnessEscapeHatch. The hatch is typed on the host
// in front of `yolo` (the YOLO_ALLOW_STALE_IMAGE convention) and honoured inside
// the jail, and a container inherits NOTHING from the launcher's environment — so
// without this forwarding the hatch is unreachable from the only place a user with
// an unlaunchable jail can type it.
//
// Swept over every runtime and nesting shape because the witness runs on all of
// them: a way out that exists on some launches is not a way out.
func TestAssembleRunCmdForwardsTheWitnessEscapeHatch(t *testing.T) {
	for _, rt := range []string{"podman", "container"} {
		for _, inContainer := range []bool{false, true} {
			name := rt
			if inContainer {
				name += "/nested"
			}
			t.Run(name, func(t *testing.T) {
				home := t.TempDir()
				t.Setenv("HOME", home)
				emptyLoopholeDirs(t)
				o, _ := pastaHostOptions(t, "/ws", home, inContainer)
				o.Getenv = func(k string) string {
					if k == paths.AllowUnreachableServicesEnv {
						return "1"
					}
					return ""
				}

				argv := o.assembleRunCmd(relocationInput(t, rt, t.TempDir(), nil))
				got, ok := envValue(argv, paths.AllowUnreachableServicesEnv)
				if !ok || got != "1" {
					t.Errorf("%s must reach the jail that honours it (got %q, present=%v)",
						paths.AllowUnreachableServicesEnv, got, ok)
				}
			})
		}
	}
}

// TestAssembleRunCmdWithoutTheHatchIsUnchanged. The hatch may not cost anything on
// a launch that did not ask for it: an env var that appears on every argv is one
// more thing frozen into every jail's environment, and the golden argv is a
// contract (assemble_test.go).
func TestAssembleRunCmdWithoutTheHatchIsUnchanged(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	emptyLoopholeDirs(t)
	o, _ := pastaHostOptions(t, "/ws", home, false)

	argv := o.assembleRunCmd(relocationInput(t, "podman", t.TempDir(), nil))
	if val, ok := envValue(argv, paths.AllowUnreachableServicesEnv); ok {
		t.Errorf("an unset hatch must not appear in the argv, got %s=%q",
			paths.AllowUnreachableServicesEnv, val)
	}
}
