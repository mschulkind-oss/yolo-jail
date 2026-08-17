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

// pastaHostOptions wires an Options that looks like a rootless podman host running
// pasta with a binary that advertises --map-host-loopback — the one combination
// that makes decideHostLoopback emit an argv — and records every subprocess the
// assembler asked for, so a test can assert that NONE was run as easily as that
// the right one was.
func pastaHostOptions(t *testing.T, ws, home string, containerenv bool) (*Options, *[]string) {
	t.Helper()
	// The executable podman reports for pasta in podmanInfoFixture; the probe runs
	// exactly that path, so the canned --help must be keyed on it.
	const pastaExe = "/nix/store/xxxx-podman-5.8.4/libexec/podman/pasta"

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
			return ExecResult{Ran: true, RC: 0, Stdout: podmanInfoFixture}
		case pastaExe + " --help":
			return ExecResult{Ran: true, RC: 0,
				Stdout: "  " + pastaMapHostLoopbackFlag + " ADDR\tTranslate ADDR to refer to host\n"}
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
// The host underneath is held at the WORST case for this property throughout:
// rootless podman on pasta, with a pasta that advertises the flag. That is the
// only configuration in which decideHostLoopback wants to emit anything, so it is
// the only one in which a caller-side mistake is observable.
func TestAssembleRunCmdEmitsAtMostOneNetworkSelector(t *testing.T) {
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
