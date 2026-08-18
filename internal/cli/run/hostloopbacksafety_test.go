package run

// hostloopbacksafety_test.go answers ONE question, end to end and adversarially:
// can any host shape make the host-loopback decision produce an argv that does not
// launch?
//
// It is deliberately not a third copy of hostloopback_test.go's coverage.
//
//   - hostloopback_test.go drives decideHostLoopback (a plan) and hostLoopbackFactsFor
//     (facts). Both are one layer above the thing that actually starts a container.
//   - hostloopbackargv_test.go pins the SHAPES that emit something — one selector, the
//     right selector, the disposition beside it.
//   - This file pins the shapes that must emit NOTHING, and it pins them against the
//     PRE-FEATURE ARGV rather than against an empty plan. That difference is the point:
//     "the decision returned no args" and "the launch argv is the one from before this
//     feature existed" are different claims, and only the second is what "the worst case
//     must be today's behaviour" means (hostloopback.go's opening rule).
//
// The one thing an adverse host IS allowed to differ in is `-e YOLO_HOST_LOOPBACK=…`,
// an informational variable the in-jail witness reads. It cannot stop a container from
// starting — podman takes any KEY=VALUE — so the assertion strips it from BOTH sides and
// then demands byte equality on everything else, while separately holding that its value
// can never be `requested` on a launch that requested nothing.
//
// Stripping both sides is what keeps "today's argv" meaning what it says now that the
// variable is unconditional (OQ-R6): every launch carries one, the baseline included, so
// the claim under test is about the NETWORK argv — the flags that decide whether podman
// accepts the container — and the disposition is asserted separately as its own value.

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// todaysArgv is the launch argv from before the host-loopback feature existed, with
// the disposition pair removed. The golden fixture answers no LookPath and runs no
// subprocess, so hostLoopbackFactsFor returns at its first gate and the decision
// contributes no flag at all — only the informational variable every launch now
// carries, which is stripped here so both sides of the comparison are the same kind
// of thing.
//
// It is rebuilt per case rather than computed once because assembleRunCmd reads the
// per-case wsState; every other input is the same deterministic fixture.
func todaysArgv(t *testing.T, home, wsState, rt string) []string {
	t.Helper()
	argv := goldenOptions("/ws", home).assembleRunCmd(relocationInput(t, rt, wsState, nil))
	stripped, _ := withoutJailDisposition(argv)
	return stripped
}

// withoutJailDisposition splits an argv into "everything that is not the disposition
// pair" and the disposition value, so the byte-equality assertion below can be exact
// about the one addition an adverse host may make.
func withoutJailDisposition(argv []string) ([]string, string) {
	out := make([]string, 0, len(argv))
	disp := ""
	for i := 0; i < len(argv); i++ {
		if argv[i] == "-e" && i+1 < len(argv) {
			if v, ok := strings.CutPrefix(argv[i+1], paths.HostLoopbackEnvVar+"="); ok {
				disp = v
				i++
				continue
			}
		}
		out = append(out, argv[i])
	}
	return out, disp
}

// TestAssembleRunCmdAdverseHostsKeepTodaysArgv walks every way the fact gathering can
// fail to reach a positive conclusion — a runtime that is not podman, a podman that is
// not there, one that will not answer, one whose answer is not JSON, one whose answer
// is JSON of the wrong shape, a rootful one, an unrecognised backend, and a recognised
// backend whose helper binary cannot be confirmed — and holds the launch argv at the
// pre-feature one.
//
// Every case here is a host nobody in this repo can rehearse for real (this jail's
// podman reports slirp4netns and podman-in-podman forces --net=host anyway), which is
// exactly why the failure paths are the ones that need pinning: they are the paths a
// real user's machine will take, and the cost of getting one wrong is not a degraded
// jail but a jail that does not start.
func TestAssembleRunCmdAdverseHostsKeepTodaysArgv(t *testing.T) {
	const podmanInfoCmd = "/usr/bin/podman info --format json"
	const pastaExe = "/nix/store/xxxx-podman-5.8.4/libexec/podman/pasta"
	const slirpExe = "/bin/slirp4netns"

	// podmanInfoRootful / podmanInfoNetavark / podmanInfoSlirpBackend are the fixture
	// varied along one axis each, so a failing case names its own cause.
	const podmanInfoRootful = `{"host":{"rootlessNetworkCmd":"pasta","security":{"rootless":false},
	  "pasta":{"executable":"` + pastaExe + `","version":"pasta 2026_07_16\n"}}}`
	const podmanInfoNetavark = `{"host":{"rootlessNetworkCmd":"netavark","security":{"rootless":true}}}`
	const podmanInfoSlirpBackend = `{"host":{"rootlessNetworkCmd":"slirp4netns","security":{"rootless":true},
	  "slirp4netns":{"executable":"` + slirpExe + `","version":"slirp4netns version 0.3.0\n"}}}`

	cases := []struct {
		name string
		rt   string
		// lookPath/exec describe the host. An absent key means "not found" / "did not
		// run", which is the same safe default the production seams have.
		lookPath map[string]string
		exec     map[string]ExecResult
		// wantDisposition is what the jail is told. The ZERO VALUE means
		// paths.HostLoopbackUnknown — "yolo reached no conclusion", the answer every
		// adverse host below must land on and one the witness can never escalate. It
		// is spelled that way round deliberately: the table's default and the
		// production default are the same fact, so a row that says nothing is
		// asserting the safe answer rather than omitting an assertion.
		wantDisposition string
	}{{
		name: "Apple Container never reaches the decision",
		rt:   "container",
	}, {
		name:     "no podman on PATH",
		rt:       "podman",
		lookPath: map[string]string{},
	}, {
		name:     "podman info did not run at all",
		rt:       "podman",
		lookPath: map[string]string{"podman": "/usr/bin/podman"},
		exec:     map[string]ExecResult{podmanInfoCmd: {Ran: false}},
	}, {
		name:     "podman info exited non-zero",
		rt:       "podman",
		lookPath: map[string]string{"podman": "/usr/bin/podman"},
		exec:     map[string]ExecResult{podmanInfoCmd: {Ran: true, RC: 125, Stderr: "cannot connect to the socket"}},
	}, {
		name:     "podman info timed out",
		rt:       "podman",
		lookPath: map[string]string{"podman": "/usr/bin/podman"},
		exec:     map[string]ExecResult{podmanInfoCmd: {Ran: true, Timeout: true, Stdout: podmanInfoFixture}},
	}, {
		name:     "podman info printed nothing",
		rt:       "podman",
		lookPath: map[string]string{"podman": "/usr/bin/podman"},
		exec:     map[string]ExecResult{podmanInfoCmd: {Ran: true, RC: 0, Stdout: ""}},
	}, {
		name:     "podman info printed YAML",
		rt:       "podman",
		lookPath: map[string]string{"podman": "/usr/bin/podman"},
		exec:     map[string]ExecResult{podmanInfoCmd: {Ran: true, RC: 0, Stdout: "host:\n  arch: amd64\n"}},
	}, {
		name:     "podman info printed a JSON array",
		rt:       "podman",
		lookPath: map[string]string{"podman": "/usr/bin/podman"},
		exec:     map[string]ExecResult{podmanInfoCmd: {Ran: true, RC: 0, Stdout: "[]"}},
	}, {
		// A field of the wrong TYPE fails the whole decode, which is the safe
		// direction: nothing is read, so nothing is claimed.
		name:     "rootlessNetworkCmd is a number",
		rt:       "podman",
		lookPath: map[string]string{"podman": "/usr/bin/podman"},
		exec: map[string]ExecResult{podmanInfoCmd: {Ran: true, RC: 0,
			Stdout: `{"host":{"rootlessNetworkCmd":7,"security":{"rootless":true}}}`}},
	}, {
		// JSON null decodes to the zero value with no error, so this lands as an
		// unrecognised backend rather than as a parse failure — a different route to
		// the same silence, and worth pinning separately.
		name:     "rootlessNetworkCmd is null",
		rt:       "podman",
		lookPath: map[string]string{"podman": "/usr/bin/podman"},
		exec: map[string]ExecResult{podmanInfoCmd: {Ran: true, RC: 0,
			Stdout: `{"host":{"rootlessNetworkCmd":null,"security":{"rootless":true}}}`}},
	}, {
		name:     "podman info has no host block at all",
		rt:       "podman",
		lookPath: map[string]string{"podman": "/usr/bin/podman"},
		exec:     map[string]ExecResult{podmanInfoCmd: {Ran: true, RC: 0, Stdout: `{}`}},
	}, {
		name:     "podman info is the JSON literal null",
		rt:       "podman",
		lookPath: map[string]string{"podman": "/usr/bin/podman"},
		exec:     map[string]ExecResult{podmanInfoCmd: {Ran: true, RC: 0, Stdout: `null`}},
	}, {
		name:     "an unrecognised backend",
		rt:       "podman",
		lookPath: map[string]string{"podman": "/usr/bin/podman"},
		exec:     map[string]ExecResult{podmanInfoCmd: {Ran: true, RC: 0, Stdout: podmanInfoNetavark}},
	}, {
		// rootlessNetworkCmd is a containers.conf value a ROOTFUL podman still reports
		// and never uses. `--network=pasta` there would swap out a working bridge.
		name:     "a rootful podman that still reports pasta",
		rt:       "podman",
		lookPath: map[string]string{"podman": "/usr/bin/podman"},
		exec: map[string]ExecResult{
			podmanInfoCmd:           {Ran: true, RC: 0, Stdout: podmanInfoRootful},
			pastaExe + " --help":    {Ran: true, RC: 0, Stdout: pastaHelpWithFlag},
			slirpExe + " --help":    {Ran: true, RC: 0, Stdout: slirpHelpWithFlag},
			"/usr/bin/pasta --help": {Ran: true, RC: 0, Stdout: pastaHelpWithFlag},
		},
	}, {
		name:     "pasta whose --help does not list the flag, and no slirp4netns to fall back to",
		rt:       "podman",
		lookPath: map[string]string{"podman": "/usr/bin/podman"},
		exec: map[string]ExecResult{
			podmanInfoCmd:        {Ran: true, RC: 0, Stdout: podmanInfoNoSlirpFixture},
			pastaExe + " --help": {Ran: true, RC: 0, Stdout: pastaHelpWithoutFlag},
		},
		wantDisposition: paths.HostLoopbackUnsupported,
	}, {
		name:     "pasta without the flag and a slirp4netns without it either",
		rt:       "podman",
		lookPath: map[string]string{"podman": "/usr/bin/podman"},
		exec: map[string]ExecResult{
			podmanInfoCmd:        {Ran: true, RC: 0, Stdout: podmanInfoFixture},
			pastaExe + " --help": {Ran: true, RC: 0, Stdout: pastaHelpWithoutFlag},
			slirpExe + " --help": {Ran: true, RC: 0, Stdout: "Usage: slirp4netns [OPTION]... PID\n--netns-type\n"},
		},
		wantDisposition: paths.HostLoopbackUnsupported,
	}, {
		name:     "both helper probes time out",
		rt:       "podman",
		lookPath: map[string]string{"podman": "/usr/bin/podman"},
		exec: map[string]ExecResult{
			podmanInfoCmd:        {Ran: true, RC: 0, Stdout: podmanInfoFixture},
			pastaExe + " --help": {Ran: true, Timeout: true, Stdout: pastaHelpWithFlag},
			slirpExe + " --help": {Ran: true, Timeout: true, Stdout: slirpHelpWithFlag},
		},
		wantDisposition: paths.HostLoopbackUnsupported,
	}, {
		// Empty output is "could not ask", never "does not have it" — and neither may
		// emit a flag.
		name:     "the pasta binary prints nothing and there is no slirp4netns",
		rt:       "podman",
		lookPath: map[string]string{"podman": "/usr/bin/podman"},
		exec: map[string]ExecResult{
			podmanInfoCmd:        {Ran: true, RC: 0, Stdout: podmanInfoNoSlirpFixture},
			pastaExe + " --help": {Ran: true, RC: 0},
		},
		wantDisposition: paths.HostLoopbackUnsupported,
	}, {
		// The fallback is pasta-only: a slirp4netns HOST that cannot be confirmed has
		// nothing to fall back to, and must not borrow pasta's capability.
		name:     "a slirp4netns host whose binary does not advertise host-loopback control",
		rt:       "podman",
		lookPath: map[string]string{"podman": "/usr/bin/podman"},
		exec: map[string]ExecResult{
			podmanInfoCmd:        {Ran: true, RC: 0, Stdout: podmanInfoSlirpBackend},
			slirpExe + " --help": {Ran: true, RC: 0, Stdout: "Usage: slirp4netns [OPTION]... PID\n--netns-type\n"},
		},
		wantDisposition: paths.HostLoopbackUnsupported,
	}}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			emptyLoopholeDirs(t)
			wsState := t.TempDir()

			want := todaysArgv(t, home, wsState, tc.rt)

			o := goldenOptions("/ws", home)
			o.LookPath = func(name string) (string, bool) {
				p, ok := tc.lookPath[name]
				return p, ok
			}
			o.Exec = fakeHostExec(tc.exec)
			var stdout strings.Builder
			o.Stdout = &stdout

			got := o.assembleRunCmd(relocationInput(t, tc.rt, wsState, nil))
			stripped, disp := withoutJailDisposition(got)

			if !slices.Equal(stripped, want) {
				t.Errorf("this host must launch with the pre-feature argv\ngot:  %v\nwant: %v",
					stripped, want)
			}
			wantDisp := tc.wantDisposition
			if wantDisp == "" {
				wantDisp = paths.HostLoopbackUnknown
			}
			if disp != wantDisp {
				t.Errorf("%s = %q, want %q", paths.HostLoopbackEnvVar, disp, wantDisp)
			}
			// The cross-check that makes the two assertions above one property: the
			// disposition is the in-jail witness's severity dial, and `requested` on a
			// launch that put no forwarding option on the argv is a real host limitation
			// reported as a fault — the refusal OQ-R3 rejected, arriving from the other
			// side. Held here as well as in the decision sweep because THIS is the layer
			// that can drop a flag on the floor after the decision returned it.
			if disp == paths.HostLoopbackRequested {
				t.Errorf("nothing was requested on this argv, yet the jail was told %q",
					paths.HostLoopbackRequested)
			}
			if sel := networkSelectors(got); len(sel) != 0 {
				t.Errorf("an unconfirmed host must select no network mode, got %v", sel)
			}
		})
	}
}

// TestAssembleRunCmdConfirmedHostsAddOnlyTheReviewedFlags is the other half of the same
// property, and the reason it is stated as "today's argv PLUS an exact set" rather than
// as "the selector is right": a flag that reaches the argv without anyone intending it
// is how a launch breaks, and the selector-shaped assertions elsewhere in this package
// cannot see a stray -v, --sysctl or --cap-add.
//
// The two confirmed shapes are the only ones that may emit anything at all.
func TestAssembleRunCmdConfirmedHostsAddOnlyTheReviewedFlags(t *testing.T) {
	cases := []struct {
		name      string
		pastaHelp string
		slirpHelp string
		want      []string
	}{
		{
			name:      "a pasta that advertises the flag",
			pastaHelp: pastaHelpWithFlag,
			want:      []string{pastaArg},
		},
		{
			name:      "an old pasta rescued by slirp4netns",
			pastaHelp: pastaHelpWithoutFlag,
			slirpHelp: slirpHelpWithFlag,
			want:      slirpArgs,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			emptyLoopholeDirs(t)
			wsState := t.TempDir()

			base := todaysArgv(t, home, wsState, "podman")
			o, _ := pastaHostOptionsWithHelps(t, "/ws", home, false, tc.pastaHelp, tc.slirpHelp)
			var stdout strings.Builder
			o.Stdout = &stdout

			got := o.assembleRunCmd(relocationInput(t, "podman", wsState, nil))
			stripped, disp := withoutJailDisposition(got)

			if disp != paths.HostLoopbackRequested {
				t.Errorf("%s = %q, want %q — the option DID go out",
					paths.HostLoopbackEnvVar, disp, paths.HostLoopbackRequested)
			}
			if extra := argvDiff(stripped, base); !slices.Equal(extra, tc.want) {
				t.Errorf("this launch added %v to the pre-feature argv, want exactly %v",
					extra, tc.want)
			}
			if missing := argvDiff(base, stripped); len(missing) != 0 {
				t.Errorf("the fix must ADD to today's argv, never remove from it; missing %v", missing)
			}
		})
	}
}

// argvDiff returns the elements of a that are not in b, preserving a's order and
// multiplicity. It is a positional diff rather than a set difference so a duplicated
// flag shows up as the duplicate it is.
func argvDiff(a, b []string) []string {
	rest := slices.Clone(b)
	var out []string
	for _, x := range a {
		if i := slices.Index(rest, x); i >= 0 {
			rest = slices.Delete(rest, i, i+1)
			continue
		}
		out = append(out, x)
	}
	return out
}

// TestReachabilityOptOutArgsCarriesTheValueVerbatim. The witness's hatch is a string
// the user types on the HOST and this forwards into the container unparsed, so the two
// ways it could go wrong are both about the string: dropping a value it does not
// recognise (a hatch that works only for "1" is a hatch that fails the user who typed
// "true"), and mangling one that contains the characters an env pair is made of.
//
// podman splits `-e KEY=VALUE` at the FIRST `=`, so a value containing `=` survives; a
// value containing a newline or a space survives because this is an argv element and
// never a shell word (runWithProxy execs the slice directly). Both are pinned rather
// than reasoned about, because the day someone adds quoting here is the day the hatch
// stops being the thing an unlaunchable jail depends on.
func TestReachabilityOptOutArgsCarriesTheValueVerbatim(t *testing.T) {
	values := []string{
		"1", "true", "yes",
		// A hatch that is "any non-empty value" (the YOLO_ALLOW_STALE_IMAGE convention)
		// honours these too. Pinned so the asymmetry is a decision, not a surprise.
		"0", "false",
		// The characters an env pair is made of.
		"a=b", "with spaces", "with\nnewline", "  ", "ünïcode", "--not-a-flag",
	}
	for _, v := range values {
		t.Run(v, func(t *testing.T) {
			o := &Options{}
			fillDefaults(o)
			o.Getenv = func(k string) string {
				if k == paths.AllowUnreachableServicesEnv {
					return v
				}
				return ""
			}
			want := []string{"-e", paths.AllowUnreachableServicesEnv + "=" + v}
			if got := o.reachabilityOptOutArgs(); !slices.Equal(got, want) {
				t.Errorf("reachabilityOptOutArgs = %q, want %q", got, want)
			}
		})
	}
}

// TestHostLoopbackOptOutNeedsNoSubprocessToBeHonoured is a property about the ONE user
// this escape hatch exists for: somebody whose jail stopped launching. If the only way
// to reach today's behaviour ran through the same `podman info` and `--help` scrapes
// that produced the bad argv, the hatch would be useless in the case where the fact
// gathering itself is what is wrong.
//
// It is stated against a host wired to answer every probe with a confirmed pasta —
// the one shape that emits a flag — so the silence is the hatch and not an empty
// fixture.
func TestHostLoopbackOptOutNeedsNoSubprocessToBeHonoured(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	emptyLoopholeDirs(t)
	wsState := t.TempDir()

	base := todaysArgv(t, home, wsState, "podman")
	o, _ := pastaHostOptions(t, "/ws", home, false)
	o.Getenv = func(k string) string {
		if k == hostLoopbackOptOutEnv {
			return "anything-non-empty"
		}
		return ""
	}
	var stdout strings.Builder
	o.Stdout = &stdout

	stripped, disp := withoutJailDisposition(o.assembleRunCmd(relocationInput(t, "podman", wsState, nil)))
	if !slices.Equal(stripped, base) {
		t.Errorf("%s must restore the pre-feature argv for ANY non-empty value\ngot:  %v\nwant: %v",
			hostLoopbackOptOutEnv, stripped, base)
	}
	// And the jail-side variable says the true thing about that launch: `unknown`.
	// The hatch's severity promise is what has to survive — a user who deliberately
	// turned the fix off must never have their own choice reported back as a broken
	// jail — and it does, because `unknown` never escalates. What it may NOT be is
	// `requested`, which is the value that costs a jail once OQ-R2 flips.
	if disp != paths.HostLoopbackUnknown {
		t.Errorf("the opt-out carried %s=%q into the jail, want %q",
			paths.HostLoopbackEnvVar, disp, paths.HostLoopbackUnknown)
	}
}

// fixtureExecTimeouts guards the two subprocess budgets. They are on the launch path,
// so a runtime that cannot answer must be abandoned rather than waited on: a jail that
// takes a minute to start because podman is wedged is a jail nobody launches twice.
func TestHostLoopbackProbeBudgetsAreBounded(t *testing.T) {
	if podmanInfoTimeout <= 0 || podmanInfoTimeout > 30*time.Second {
		t.Errorf("podmanInfoTimeout = %s; it bounds a subprocess on the launch path", podmanInfoTimeout)
	}
	if flagProbeTimeout <= 0 || flagProbeTimeout > 30*time.Second {
		t.Errorf("flagProbeTimeout = %s; it bounds a subprocess on the launch path", flagProbeTimeout)
	}
}
