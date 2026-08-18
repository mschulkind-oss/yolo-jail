package run

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/svcendpoint"
)

// The decision function is the half of the host-loopback fix that CAN be tested
// from a development jail: nobody here has a pasta host, and podman-in-podman
// forces --net=host, so the argv is the only thing a unit test can hold. It is
// also the half that can brick the product — a wrong --network flag stops a jail
// from starting at all — which is why the sweep below exists alongside the named
// cases: the named cases pin the intended behaviour, the sweep pins the SAFETY
// PROPERTY over every input combination there is.

const pastaArg = "--network=pasta:--map-host-loopback,169.254.1.2"

// slirpArgs is spelled out rather than taken from slirpForwardingArgs() on
// purpose: these two flags ARE the contract, and a test that calls the function
// it is testing would follow any future edit silently. Both are load-bearing and
// measured (see slirp4netnsHostAddr) — the option alone forwards the host's
// loopback to an address the jail never dials, because podman aims
// host.containers.internal at the host's GLOBAL address under slirp4netns.
var slirpArgs = []string{
	"--network=slirp4netns:allow_host_loopback=true",
	"--add-host=host.containers.internal:10.0.2.2",
}

func TestDecideHostLoopback(t *testing.T) {
	tests := []struct {
		name string
		f    hostLoopbackFacts
		args []string
		// warn is a substring the warning must contain; "" demands silence.
		warn string
		// disp is the verdict carried into the jail; "" demands that NOTHING is
		// carried, which is the value the in-jail witness can never escalate on.
		disp string
	}{
		// --- the fix firing ---
		{
			name: "pasta confirmed on the default path emits the map flag",
			f:    hostLoopbackFacts{netMode: "bridge", backend: "pasta", rootless: true, support: supportConfirmed},
			args: []string{pastaArg},
			disp: paths.HostLoopbackRequested,
		},
		{
			name: "slirp4netns confirmed on the default path emits the option AND the hosts entry",
			f:    hostLoopbackFacts{netMode: "bridge", backend: "slirp4netns", rootless: true, support: supportConfirmed},
			args: slirpArgs,
			disp: paths.HostLoopbackRequested,
		},

		// --- the slirp4netns FALLBACK: an old pasta host that can be made to work ---
		{
			name: "an old pasta with a usable slirp4netns is switched to it rather than degraded",
			f: hostLoopbackFacts{netMode: "bridge", backend: "pasta", rootless: true,
				support: supportAbsent, fallbackSupport: supportConfirmed,
				version: "pasta 2024_01_01.abc"},
			args: slirpArgs,
			warn: "yolo launched this jail on slirp4netns instead",
			disp: paths.HostLoopbackRequested,
		},
		{
			// Taken here too — with no forwarding option on the argv, pasta does not
			// forward loopback at any version — but the note may not claim the pasta
			// is old when yolo never got an answer out of it.
			name: "a pasta that could not be probed also gets the fallback, without being blamed",
			f: hostLoopbackFacts{netMode: "bridge", backend: "pasta", rootless: true,
				support: supportUnknown, fallbackSupport: supportConfirmed},
			args: slirpArgs,
			warn: "could not confirm this host's pasta supports",
			disp: paths.HostLoopbackRequested,
		},
		{
			// The whole point of "fallback, not preference": slirp4netns is the older
			// and slower stack, so a host whose pasta works must never be moved to it.
			name: "a working pasta is preferred even when slirp4netns is available",
			f: hostLoopbackFacts{netMode: "bridge", backend: "pasta", rootless: true,
				support: supportConfirmed, fallbackSupport: supportConfirmed},
			args: []string{pastaArg},
			disp: paths.HostLoopbackRequested,
		},
		{
			// The fallback is pasta-only. A slirp4netns host that cannot be confirmed
			// has nothing to fall back TO — asking pasta instead would be a preference
			// inversion nobody ruled on, so the honest warning stands.
			name: "an unconfirmable slirp4netns host is not rescued by the fallback fact",
			f: hostLoopbackFacts{netMode: "bridge", backend: "slirp4netns", rootless: true,
				support: supportAbsent, fallbackSupport: supportConfirmed},
			warn: "slirp4netns --help",
			disp: paths.HostLoopbackUnsupported,
		},
		{
			name: "an old pasta whose slirp4netns cannot do it either says which of the two failed",
			f: hostLoopbackFacts{netMode: "bridge", backend: "pasta", rootless: true,
				support: supportAbsent, fallbackSupport: supportAbsent},
			warn: "does not advertise",
			disp: paths.HostLoopbackUnsupported,
		},
		{
			name: "an old pasta on a host with no slirp4netns at all says that instead",
			f: hostLoopbackFacts{netMode: "bridge", backend: "pasta", rootless: true,
				support: supportAbsent, fallbackSupport: supportUnknown},
			warn: "reports no usable",
			disp: paths.HostLoopbackUnsupported,
		},
		{
			name: "the fallback is never taken on a rootful host",
			f: hostLoopbackFacts{netMode: "bridge", backend: "pasta", rootless: false,
				support: supportAbsent, fallbackSupport: supportConfirmed},
		},
		{
			name: "the fallback is never taken under an explicit network.mode",
			f: hostLoopbackFacts{netMode: "none", backend: "pasta", rootless: true,
				support: supportAbsent, fallbackSupport: supportConfirmed},
			warn: "network.mode is set to 'none'",
		},
		{
			name: "the opt-out suppresses the fallback too, and names the argv it suppressed",
			f: hostLoopbackFacts{netMode: "bridge", backend: "pasta", rootless: true,
				support: supportAbsent, fallbackSupport: supportConfirmed, optOut: true},
			warn: slirpArgs[0],
		},

		// --- capability not positively confirmed: emit nothing, say why ---
		{
			name: "pasta without the flag warns with the version and the check command",
			f: hostLoopbackFacts{netMode: "bridge", backend: "pasta", rootless: true,
				support: supportAbsent, probeCmd: "/usr/bin/pasta --help", version: "pasta 2024_01_01.abc"},
			warn: "Upgrade passt to 2024_08_21",
			disp: paths.HostLoopbackUnsupported,
		},
		{
			name: "pasta version is echoed so the user knows what they have",
			f: hostLoopbackFacts{netMode: "bridge", backend: "pasta", rootless: true,
				support: supportAbsent, version: "pasta 2024_01_01.abc"},
			warn: "(reported: pasta 2024_01_01.abc)",
			disp: paths.HostLoopbackUnsupported,
		},
		{
			name: "pasta that could not be probed says so rather than blaming the version",
			f: hostLoopbackFacts{netMode: "bridge", backend: "pasta", rootless: true,
				support: supportUnknown, probeCmd: "/usr/bin/pasta --help"},
			warn: "could not confirm it",
			disp: paths.HostLoopbackUnsupported,
		},
		{
			name: "pasta with no binary at all names that instead of a probe",
			f:    hostLoopbackFacts{netMode: "bridge", backend: "pasta", rootless: true, support: supportUnknown},
			warn: "could not find a pasta binary",
			disp: paths.HostLoopbackUnsupported,
		},
		{
			name: "slirp4netns without the flag warns and emits nothing",
			f: hostLoopbackFacts{netMode: "bridge", backend: "slirp4netns", rootless: true,
				support: supportAbsent, version: "slirp4netns version 0.3.0"},
			warn: "slirp4netns --help",
			disp: paths.HostLoopbackUnsupported,
		},

		// --- unrecognised / rootful: silence, exactly today's behaviour ---
		{
			name: "an unrecognised backend emits nothing and says nothing",
			f:    hostLoopbackFacts{netMode: "bridge", backend: "netavark", rootless: true, support: supportConfirmed},
		},
		{
			name: "an unreadable podman info (empty backend) emits nothing and says nothing",
			f:    hostLoopbackFacts{netMode: "bridge", backend: "", rootless: true, support: supportConfirmed},
		},
		{
			name: "rootful podman is never touched, even reporting pasta",
			f:    hostLoopbackFacts{netMode: "bridge", backend: "pasta", rootless: false, support: supportConfirmed},
		},

		// --- an explicit network.mode belongs to the user (OQ-R1) ---
		{
			name: "an explicit mode is never overridden, and is warned about",
			f:    hostLoopbackFacts{netMode: "none", backend: "pasta", rootless: true, support: supportConfirmed},
			warn: "network.mode is set to 'none'",
		},
		{
			name: "host mode shares the host stack, so there is nothing to warn about",
			f:    hostLoopbackFacts{netMode: "host", backend: "pasta", rootless: true, support: supportConfirmed},
		},
		{
			name: "an explicit mode on a backend we could not fix anyway stays silent",
			f:    hostLoopbackFacts{netMode: "none", backend: "netavark", rootless: true, support: supportConfirmed},
		},

		// --- the escape hatch ---
		{
			name: "the opt-out suppresses the flag and names what it suppressed",
			f: hostLoopbackFacts{netMode: "bridge", backend: "pasta", rootless: true,
				support: supportConfirmed, optOut: true},
			warn: pastaArg,
		},
		{
			name: "the opt-out is silent when it is suppressing nothing",
			f: hostLoopbackFacts{netMode: "bridge", backend: "netavark", rootless: true,
				support: supportConfirmed, optOut: true},
		},
		{
			name: "the opt-out also swallows the old-pasta warning rather than half-speaking",
			f: hostLoopbackFacts{netMode: "bridge", backend: "pasta", rootless: true,
				support: supportAbsent, optOut: true},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := decideHostLoopback(tc.f)
			if !slices.Equal(got.args, tc.args) {
				t.Errorf("args = %v, want %v", got.args, tc.args)
			}
			switch {
			case tc.warn == "" && got.warning != "":
				t.Errorf("expected silence, got warning:\n%s", got.warning)
			case tc.warn != "" && !strings.Contains(got.warning, tc.warn):
				t.Errorf("warning missing %q:\n%s", tc.warn, got.warning)
			}
			if got.disposition != tc.disp {
				t.Errorf("disposition = %q, want %q", got.disposition, tc.disp)
			}
			var wantEnv []string
			if tc.disp != "" {
				wantEnv = []string{"-e", paths.HostLoopbackEnvVar + "=" + tc.disp}
			}
			if env := got.jailEnvArgs(); !slices.Equal(env, wantEnv) {
				t.Errorf("jailEnvArgs = %v, want %v", env, wantEnv)
			}
		})
	}
}

// TestDecideHostLoopbackDispositionMatchesTheArgv is the property the in-jail
// witness's severity hangs off, swept over every input combination: the jail is
// told `requested` if and ONLY if the forwarding option actually went out on the
// argv.
//
// Both directions are failures with teeth, and in opposite ways. A `requested`
// that was not requested is a jail refused (once OQ-R2 flips) for a limitation of
// the host — precisely the refusal OQ-R3 rejected, arriving by the back door. A
// forwarding option emitted while the jail is told `unsupported` or nothing is a
// real fault reported as "your host is old", which sends the reader to upgrade
// passt for a dead daemon.
//
// Kept separate from the named cases for the same reason
// TestDecideHostLoopbackOnlyEmitsOnPositiveFacts is: those say what the feature
// does, this says what it can never do, and it has to survive somebody adding a
// backend, a support state, or a network mode.
func TestDecideHostLoopbackDispositionMatchesTheArgv(t *testing.T) {
	forEveryHostLoopbackFact(func(f hostLoopbackFacts) {
		got := decideHostLoopback(f)
		emitted := len(got.args) > 0
		if emitted != (got.disposition == paths.HostLoopbackRequested) {
			t.Errorf("%+v: argv %v but disposition %q — the jail's severity must "+
				"track what was actually requested", f, got.args, got.disposition)
		}
		// The only other value there is. An unrecognised spelling would
		// be read as "not attributable" in-jail, which is safe but silent
		// — so it must not be reachable from here.
		if got.disposition != "" &&
			got.disposition != paths.HostLoopbackRequested &&
			got.disposition != paths.HostLoopbackUnsupported {
			t.Errorf("%+v: unknown disposition %q", f, got.disposition)
		}
	})
}

// forEveryHostLoopbackFact calls fn once per combination of every input the
// decision has: the effective network mode, the backend podman reported, the
// capability verdict for it, the slirp4netns FALLBACK verdict, rootlessness, and
// the opt-out. Shared by the two sweeps below so a new input dimension is added in
// ONE place — the slirp4netns fallback was added as a sixth, and a sweep that had
// been left behind would have gone on passing while covering nothing.
func forEveryHostLoopbackFact(fn func(hostLoopbackFacts)) {
	netModes := []string{"bridge", "host", "none", "private"}
	backends := []string{"pasta", "slirp4netns", "netavark", "cni", ""}
	supports := []hostLoopbackSupport{supportUnknown, supportConfirmed, supportAbsent}

	for _, netMode := range netModes {
		for _, backend := range backends {
			for _, support := range supports {
				for _, fallback := range supports {
					for _, rootless := range []bool{true, false} {
						for _, optOut := range []bool{true, false} {
							fn(hostLoopbackFacts{
								netMode: netMode, backend: backend, rootless: rootless,
								support: support, fallbackSupport: fallback, optOut: optOut,
							})
						}
					}
				}
			}
		}
	}
}

// TestSlirpForwardingArgsPinTheNameTheDaemonsAdvertise is the bridge between the
// literal every other test in this file pins and the two facts it depends on: the
// slirp4netns host address, and the name yolo's host daemons publish for a jail to
// dial. The hosts entry is the whole reason the slirp4netns path works at all
// (podman aims host.containers.internal at the host's GLOBAL address under that
// stack), so pinning a name the daemons do not advertise would be a silent no-op.
func TestSlirpForwardingArgsPinTheNameTheDaemonsAdvertise(t *testing.T) {
	if !slices.Equal(slirpForwardingArgs(), slirpArgs) {
		t.Fatalf("slirpForwardingArgs() = %v, want the reviewed literal %v", slirpForwardingArgs(), slirpArgs)
	}
	want := "--add-host=" + svcendpoint.DefaultAdvertiseHost + ":" + slirp4netnsHostAddr
	if !slices.Contains(slirpForwardingArgs(), want) {
		t.Errorf("the hosts entry must aim %s at the slirp4netns host address; args = %v",
			svcendpoint.DefaultAdvertiseHost, slirpForwardingArgs())
	}
}

// TestReachabilityOptOutArgsForwardsOnlyWhenSet. The witness's escape hatch is
// typed on the HOST and honoured in the JAIL, and a container inherits nothing
// from the launcher's environment — so this forwarding is the entire mechanism. It
// is also the one an unlaunchable jail depends on, which is why it must not be
// conditional on anything else.
func TestReachabilityOptOutArgsForwardsOnlyWhenSet(t *testing.T) {
	o := &Options{}
	fillDefaults(o)
	o.Getenv = func(string) string { return "" }
	if got := o.reachabilityOptOutArgs(); got != nil {
		t.Errorf("an unset hatch must add nothing to the argv, got %v", got)
	}

	o.Getenv = func(k string) string {
		if k == paths.AllowUnreachableServicesEnv {
			return "1"
		}
		return ""
	}
	want := []string{"-e", paths.AllowUnreachableServicesEnv + "=1"}
	if got := o.reachabilityOptOutArgs(); !slices.Equal(got, want) {
		t.Errorf("reachabilityOptOutArgs = %v, want %v", got, want)
	}
}

// TestDecideHostLoopbackOnlyEmitsOnPositiveFacts sweeps EVERY combination of the
// inputs and asserts the one property that keeps a bad detection from bricking a
// user's environment: an argv is emitted if and only if every fact is positive —
// the default network mode, a rootless podman, a backend yolo recognises, a
// capability it confirmed by asking (its own, or slirp4netns's as the pasta
// fallback), and no opt-out. Anything else must fall back to emitting nothing,
// which is byte-for-byte the behaviour before this feature.
//
// This is deliberately a separate test from the named cases above. Those say what
// the feature does; this says what it can never do, and it is the one that has to
// survive somebody adding a backend or a support state.
func TestDecideHostLoopbackOnlyEmitsOnPositiveFacts(t *testing.T) {
	forEveryHostLoopbackFact(func(f hostLoopbackFacts) {
		// The fallback widens this predicate by exactly one term, and only for
		// pasta: a confirmed slirp4netns can stand in for an unconfirmable pasta,
		// never for an unrecognised backend, a rootful podman, or an explicit mode.
		confirmed := f.support == supportConfirmed ||
			(f.backend == backendPasta && f.fallbackSupport == supportConfirmed)
		wantEmit := f.netMode == "bridge" && f.rootless && !f.optOut &&
			confirmed && mappableBackend(f.backend)
		got := decideHostLoopback(f)
		if wantEmit != (len(got.args) > 0) {
			t.Errorf("%+v: emitted %v, wantEmit=%v", f, got.args, wantEmit)
			return
		}
		// A flag that is emitted must be one of the two argvs reviewed here — never
		// an improvised one, and never half of the slirp4netns pair.
		if len(got.args) > 0 &&
			!slices.Equal(got.args, []string{pastaArg}) &&
			!slices.Equal(got.args, slirpArgs) {
			t.Errorf("%+v: unexpected argv %v", f, got.args)
		}
	})
}

// TestDecideHostLoopbackNeverSwitchesStacksSilently. The fallback moves a jail onto
// a network stack the host's podman did not choose — slower, and a surprise to
// anyone measuring throughput — so the one path that does it must always say so.
// The healthy pasta path stays silent, which is what keeps the line worth reading.
func TestDecideHostLoopbackNeverSwitchesStacksSilently(t *testing.T) {
	forEveryHostLoopbackFact(func(f hostLoopbackFacts) {
		got := decideHostLoopback(f)
		fellBack := slices.Equal(got.args, slirpArgs) && f.backend == backendPasta
		if fellBack && !strings.Contains(got.warning, "slirp4netns") {
			t.Errorf("%+v: switched this jail to slirp4netns without saying so: %q", f, got.warning)
		}
		if slices.Equal(got.args, []string{pastaArg}) && got.warning != "" {
			t.Errorf("%+v: the healthy path must be silent, got:\n%s", f, got.warning)
		}
	})
}

// podmanInfoFixture is the shape of real `podman info --format json` output
// (trimmed to the fields read, values from podman 5.8.4). Keeping a realistic
// nesting rather than a hand-flattened blob is the point: the parse is the step
// that turns a host fact into an argv, and a fixture that does not match reality
// would validate nothing.
const podmanInfoFixture = `{
  "host": {
    "rootlessNetworkCmd": "pasta",
    "security": {"rootless": true, "seccompEnabled": true},
    "pasta": {
      "executable": "/nix/store/xxxx-podman-5.8.4/libexec/podman/pasta",
      "package": "Unknown",
      "version": "pasta 2026_07_16.090d739\nCopyright Red Hat\nGNU General Public License, version 2 or later\n"
    },
    "slirp4netns": {
      "executable": "/bin/slirp4netns",
      "package": "Unknown",
      "version": "slirp4netns version 1.3.4\ncommit: 129916bd"
    }
  },
  "store": {"graphDriverName": "overlay"}
}`

func TestParsePodmanInfo(t *testing.T) {
	info, ok := parsePodmanInfo(podmanInfoFixture)
	if !ok {
		t.Fatal("expected the fixture to parse")
	}
	if info.Host.RootlessNetworkCmd != "pasta" {
		t.Errorf("rootlessNetworkCmd = %q, want pasta", info.Host.RootlessNetworkCmd)
	}
	if !info.Host.Security.Rootless {
		t.Error("expected rootless=true")
	}
	if !strings.HasSuffix(info.Host.Pasta.Executable, "/pasta") {
		t.Errorf("pasta executable = %q", info.Host.Pasta.Executable)
	}
	if got := firstLine(info.Host.Pasta.Version); got != "pasta 2026_07_16.090d739" {
		t.Errorf("firstLine(version) = %q", got)
	}
}

func TestParsePodmanInfoRejectsGarbage(t *testing.T) {
	for _, in := range []string{"", "not json", "podman: command not found", "[]"} {
		if _, ok := parsePodmanInfo(in); ok {
			t.Errorf("parsePodmanInfo(%q) = ok, want failure", in)
		}
	}
}

// fakeHostExec answers `<argv...>` from canned results keyed by the joined argv,
// and reports "did not run" for anything unmatched — the same safe default the
// check package's fixture uses. No subprocess is ever started.
func fakeHostExec(cases map[string]ExecResult) func([]string, string, []string, time.Duration) ExecResult {
	return func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
		if r, ok := cases[strings.Join(argv, " ")]; ok {
			return r
		}
		return ExecResult{Ran: false}
	}
}

func TestProbeHostLoopbackSupport(t *testing.T) {
	const helpWithFlag = "Usage: pasta [OPTION]...\n  --map-host-loopback ADDR\tTranslate ADDR to refer to host\n"
	const helpWithout = "Usage: pasta [OPTION]...\n  --map-gw\tMap the gateway\n"

	tests := []struct {
		name     string
		exe      string
		lookPath map[string]string
		exec     map[string]ExecResult
		want     hostLoopbackSupport
		wantCmd  string
	}{
		{
			name: "the flag in --help output confirms support",
			exe:  "/usr/bin/pasta",
			exec: map[string]ExecResult{
				"/usr/bin/pasta --help": {Ran: true, RC: 0, Stdout: helpWithFlag},
			},
			want:    supportConfirmed,
			wantCmd: "/usr/bin/pasta --help",
		},
		{
			name: "help on stderr with a non-zero exit still counts as evidence",
			exe:  "/usr/bin/pasta",
			exec: map[string]ExecResult{
				"/usr/bin/pasta --help": {Ran: true, RC: 1, Stderr: helpWithFlag},
			},
			want:    supportConfirmed,
			wantCmd: "/usr/bin/pasta --help",
		},
		{
			name: "help without the flag is a positive absence",
			exe:  "/usr/bin/pasta",
			exec: map[string]ExecResult{
				"/usr/bin/pasta --help": {Ran: true, RC: 0, Stdout: helpWithout},
			},
			want:    supportAbsent,
			wantCmd: "/usr/bin/pasta --help",
		},
		{
			name: "empty output is 'could not ask', not 'does not have it'",
			exe:  "/usr/bin/pasta",
			exec: map[string]ExecResult{
				"/usr/bin/pasta --help": {Ran: true, RC: 0},
			},
			want:    supportUnknown,
			wantCmd: "/usr/bin/pasta --help",
		},
		{
			name: "a timeout is unknown",
			exe:  "/usr/bin/pasta",
			exec: map[string]ExecResult{
				"/usr/bin/pasta --help": {Ran: true, Timeout: true, Stdout: helpWithFlag},
			},
			want:    supportUnknown,
			wantCmd: "/usr/bin/pasta --help",
		},
		{
			name:     "no executable from podman info falls back to PATH",
			lookPath: map[string]string{"passt": "/opt/passt"},
			exec: map[string]ExecResult{
				"/opt/passt --help": {Ran: true, RC: 0, Stdout: helpWithFlag},
			},
			want:    supportConfirmed,
			wantCmd: "/opt/passt --help",
		},
		{
			name: "nothing to ask at all is unknown with no command to echo",
			want: supportUnknown,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			o := &Options{}
			fillDefaults(o)
			o.LookPath = func(name string) (string, bool) {
				p, ok := tc.lookPath[name]
				return p, ok
			}
			o.Exec = fakeHostExec(tc.exec)
			got, cmd := o.probeHostLoopbackSupport(tc.exe, []string{"pasta", "passt"}, pastaMapHostLoopbackFlag)
			if got != tc.want {
				t.Errorf("support = %v, want %v", got, tc.want)
			}
			if cmd != tc.wantCmd {
				t.Errorf("probeCmd = %q, want %q", cmd, tc.wantCmd)
			}
		})
	}
}

// TestHostLoopbackFactsForFailSafe pins the gathering half's failure paths: every
// one of them must produce facts that decide to nothing. These are the paths a
// real host takes when something is off, and they are exactly the paths nobody
// can rehearse on a pasta host from here.
func TestHostLoopbackFactsForFailSafe(t *testing.T) {
	tests := []struct {
		name     string
		rt       string
		isMacOS  bool
		lookPath map[string]string
		exec     map[string]ExecResult
	}{
		{name: "a non-podman runtime is out of scope", rt: "container"},
		{
			name:    "macOS is out of scope even on podman",
			rt:      "podman",
			isMacOS: true,
			exec: map[string]ExecResult{
				"/usr/bin/podman info --format json": {Ran: true, RC: 0, Stdout: podmanInfoFixture},
			},
			lookPath: map[string]string{"podman": "/usr/bin/podman"},
		},
		{name: "no podman on PATH", rt: "podman"},
		{
			name:     "podman info that fails to run",
			rt:       "podman",
			lookPath: map[string]string{"podman": "/usr/bin/podman"},
			exec:     map[string]ExecResult{"/usr/bin/podman info --format json": {Ran: false}},
		},
		{
			name:     "podman info that exits non-zero",
			rt:       "podman",
			lookPath: map[string]string{"podman": "/usr/bin/podman"},
			exec: map[string]ExecResult{
				"/usr/bin/podman info --format json": {Ran: true, RC: 125, Stderr: "cannot connect"},
			},
		},
		{
			name:     "podman info that times out",
			rt:       "podman",
			lookPath: map[string]string{"podman": "/usr/bin/podman"},
			exec: map[string]ExecResult{
				"/usr/bin/podman info --format json": {Ran: true, Timeout: true, Stdout: podmanInfoFixture},
			},
		},
		{
			name:     "podman info that is not JSON",
			rt:       "podman",
			lookPath: map[string]string{"podman": "/usr/bin/podman"},
			exec: map[string]ExecResult{
				"/usr/bin/podman info --format json": {Ran: true, RC: 0, Stdout: "host:\n  arch: amd64\n"},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			o := &Options{IsMacOS: tc.isMacOS}
			fillDefaults(o)
			o.IsMacOS = tc.isMacOS
			o.Getenv = func(string) string { return "" }
			o.LookPath = func(name string) (string, bool) {
				p, ok := tc.lookPath[name]
				return p, ok
			}
			o.Exec = fakeHostExec(tc.exec)

			plan := decideHostLoopback(o.hostLoopbackFactsFor(tc.rt, "bridge"))
			if len(plan.args) != 0 {
				t.Errorf("emitted %v, want nothing", plan.args)
			}
			if plan.warning != "" {
				t.Errorf("expected silence, got warning:\n%s", plan.warning)
			}
			// And the jail is told NOTHING. A host yolo could not read anything about
			// is exactly the case where a claim in either direction would be invented,
			// and the in-jail witness treats an absent variable as "not attributable"
			// — the only value that cannot cost a jail once OQ-R2 flips.
			if plan.disposition != "" {
				t.Errorf("disposition = %q, want nothing carried into the jail", plan.disposition)
			}
		})
	}
}

// TestHostLoopbackFactsForPasta walks the whole gathering path on a host that
// reports pasta and a pasta binary that advertises the flag — the case the fix
// exists for, assembled from the two real subprocess outputs it depends on.
func TestHostLoopbackFactsForPasta(t *testing.T) {
	const pastaExe = "/nix/store/xxxx-podman-5.8.4/libexec/podman/pasta"
	o := &Options{}
	fillDefaults(o)
	o.Getenv = func(string) string { return "" }
	o.LookPath = func(name string) (string, bool) {
		if name == "podman" {
			return "/usr/bin/podman", true
		}
		return "", false
	}
	o.Exec = fakeHostExec(map[string]ExecResult{
		"/usr/bin/podman info --format json": {Ran: true, RC: 0, Stdout: podmanInfoFixture},
		pastaExe + " --help":                 {Ran: true, RC: 0, Stdout: "  --map-host-loopback ADDR\tTranslate ADDR to refer to host\n"},
	})

	f := o.hostLoopbackFactsFor("podman", "bridge")
	if f.backend != "pasta" || !f.rootless || f.support != supportConfirmed {
		t.Fatalf("facts = %+v, want pasta/rootless/confirmed", f)
	}
	if f.version != "pasta 2026_07_16.090d739" {
		t.Errorf("version = %q, want the first line only", f.version)
	}
	if got := decideHostLoopback(f); !slices.Equal(got.args, []string{pastaArg}) {
		t.Errorf("args = %v, want %v", got.args, []string{pastaArg})
	}
}

// podmanInfoNoSlirpFixture is podman's answer on a host with NO slirp4netns
// installed: the helper block is present but empty. Measured 2026-08-17 by
// running `podman info` with the binary off podman's PATH — podman reports "" for
// the executable rather than omitting the block or erroring, which is what makes
// "" a positive "podman has none" rather than a fact yolo failed to read.
const podmanInfoNoSlirpFixture = `{
  "host": {
    "rootlessNetworkCmd": "pasta",
    "security": {"rootless": true},
    "pasta": {
      "executable": "/nix/store/xxxx-podman-5.8.4/libexec/podman/pasta",
      "version": "pasta 2024_01_01.abc\n"
    },
    "slirp4netns": {"executable": "", "package": "", "version": ""}
  }
}`

// slirpHelpWithFlag is the real slirp4netns 1.3.4 line the probe greps for.
const slirpHelpWithFlag = "Usage: slirp4netns [OPTION]... PID|PATH|FD [TAPNAME]\n" +
	"--disable-host-loopback  prohibit connecting to 127.0.0.1:* on the host namespace\n"

// recordingHostExec is fakeHostExec that also appends every argv it was asked for,
// so a test can assert a subprocess was NOT run as easily as that it was. Both
// directions matter here: the fallback probe must not cost a healthy host a third
// subprocess, and it must never reach a binary podman did not name.
func recordingHostExec(cases map[string]ExecResult, ran *[]string) func([]string, string, []string, time.Duration) ExecResult {
	inner := fakeHostExec(cases)
	return func(argv []string, dir string, env []string, d time.Duration) ExecResult {
		*ran = append(*ran, strings.Join(argv, " "))
		return inner(argv, dir, env, d)
	}
}

// TestHostLoopbackFactsForSlirpFallback walks the whole gathering path on the host
// this feature exists for: a rootless podman on pasta, a pasta too old to forward
// loopback, and a slirp4netns that podman knows about and that advertises
// host-loopback control. The outcome must be a WORKING jail on the older stack,
// not the warn-and-launch it used to be.
func TestHostLoopbackFactsForSlirpFallback(t *testing.T) {
	const pastaExe = "/nix/store/xxxx-podman-5.8.4/libexec/podman/pasta"
	var ran []string
	o := &Options{}
	fillDefaults(o)
	o.Getenv = func(string) string { return "" }
	o.LookPath = func(name string) (string, bool) {
		if name == "podman" {
			return "/usr/bin/podman", true
		}
		return "", false
	}
	o.Exec = recordingHostExec(map[string]ExecResult{
		"/usr/bin/podman info --format json": {Ran: true, RC: 0, Stdout: podmanInfoFixture},
		pastaExe + " --help":                 {Ran: true, RC: 0, Stdout: "Usage: pasta\n  --map-gw\tMap the gateway\n"},
		"/bin/slirp4netns --help":            {Ran: true, RC: 0, Stdout: slirpHelpWithFlag},
	}, &ran)

	f := o.hostLoopbackFactsFor("podman", "bridge")
	if f.support != supportAbsent || f.fallbackSupport != supportConfirmed {
		t.Fatalf("facts = %+v, want pasta absent / fallback confirmed", f)
	}
	plan := decideHostLoopback(f)
	if !slices.Equal(plan.args, slirpArgs) {
		t.Errorf("args = %v, want the slirp4netns pair %v", plan.args, slirpArgs)
	}
	if plan.disposition != paths.HostLoopbackRequested {
		t.Errorf("disposition = %q, want %q — the forwarding option DID go out",
			plan.disposition, paths.HostLoopbackRequested)
	}
	if !slices.Contains(ran, "/bin/slirp4netns --help") {
		t.Errorf("the fallback must be probed on this host, ran: %v", ran)
	}
}

// TestHostLoopbackFactsForNoFallbackProbeOnAHealthyHost. The fallback probe is a
// third subprocess on the launch path, and the overwhelmingly common host does not
// need it. Asserting it is not run keeps the gate a real one rather than a
// discarded answer — the same property the nested-podman argv test holds.
func TestHostLoopbackFactsForNoFallbackProbeOnAHealthyHost(t *testing.T) {
	const pastaExe = "/nix/store/xxxx-podman-5.8.4/libexec/podman/pasta"
	var ran []string
	o := &Options{}
	fillDefaults(o)
	o.Getenv = func(string) string { return "" }
	o.LookPath = func(name string) (string, bool) {
		if name == "podman" {
			return "/usr/bin/podman", true
		}
		return "", false
	}
	o.Exec = recordingHostExec(map[string]ExecResult{
		"/usr/bin/podman info --format json": {Ran: true, RC: 0, Stdout: podmanInfoFixture},
		pastaExe + " --help":                 {Ran: true, RC: 0, Stdout: "  --map-host-loopback ADDR\n"},
		"/bin/slirp4netns --help":            {Ran: true, RC: 0, Stdout: slirpHelpWithFlag},
	}, &ran)

	f := o.hostLoopbackFactsFor("podman", "bridge")
	if f.fallbackSupport != supportUnknown {
		t.Errorf("fallbackSupport = %v on a host that never needed one", f.fallbackSupport)
	}
	if slices.Contains(ran, "/bin/slirp4netns --help") {
		t.Errorf("a working pasta host must not pay for the fallback probe, ran: %v", ran)
	}
	if got := decideHostLoopback(f); !slices.Equal(got.args, []string{pastaArg}) {
		t.Errorf("args = %v, want pasta to keep winning", got.args)
	}
}

// podmanInfoRootfulFixture / podmanInfoUnknownBackendFixture are the two hosts whose
// answer is already decided before any capability matters: a rootful podman (where
// `rootlessNetworkCmd` is a containers.conf value it reports and never uses) and a
// backend yolo does not recognise. Trimmed to the fields read.
const (
	podmanInfoRootfulFixture = `{
  "host": {
    "rootlessNetworkCmd": "pasta",
    "security": {"rootless": false},
    "pasta": {"executable": "/nix/store/xxxx-podman-5.8.4/libexec/podman/pasta", "version": "pasta 2026_07_16.090d739\n"}
  }
}`
	podmanInfoUnknownBackendFixture = `{
  "host": {
    "rootlessNetworkCmd": "netavark",
    "security": {"rootless": true},
    "pasta": {"executable": "/nix/store/xxxx-podman-5.8.4/libexec/podman/pasta", "version": "pasta 2026_07_16.090d739\n"}
  }
}`
)

// TestHostLoopbackFactsForProbesOnlyWhereTheAnswerIsRead is the same gate
// TestHostLoopbackFactsForNoFallbackProbeOnAHealthyHost holds for the fallback probe,
// applied to the capability probe itself: every `<backend> --help` is a subprocess on
// the LAUNCH PATH, and three of the four branches above already know their answer
// without it.
//
// Asserting the probe did not run is what keeps each gate a real one rather than a
// discarded answer. A `return f` that gets deleted as a redundant early exit reads as
// a harmless cleanup and costs every launch on those hosts a subprocess — silently,
// because nothing in the output changes.
func TestHostLoopbackFactsForProbesOnlyWhereTheAnswerIsRead(t *testing.T) {
	const pastaExe = "/nix/store/xxxx-podman-5.8.4/libexec/podman/pasta"
	tests := []struct {
		name    string
		info    string
		netMode string
	}{
		{
			// OQ-R1: the decision is the user's, so the capability cannot change it.
			name: "an explicit network.mode has already decided", info: podmanInfoFixture, netMode: "none",
		},
		{
			name: "a rootful podman is out of scope whatever its pasta can do",
			info: podmanInfoRootfulFixture, netMode: "bridge",
		},
		{
			name: "an unrecognised backend has nothing to ask about",
			info: podmanInfoUnknownBackendFixture, netMode: "bridge",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var ran []string
			o := &Options{}
			fillDefaults(o)
			o.Getenv = func(string) string { return "" }
			o.LookPath = func(name string) (string, bool) {
				if name == "podman" {
					return "/usr/bin/podman", true
				}
				return "", false
			}
			o.Exec = recordingHostExec(map[string]ExecResult{
				"/usr/bin/podman info --format json": {Ran: true, RC: 0, Stdout: tc.info},
				pastaExe + " --help":                 {Ran: true, RC: 0, Stdout: "  --map-host-loopback ADDR\n"},
				"/bin/slirp4netns --help":            {Ran: true, RC: 0, Stdout: slirpHelpWithFlag},
			}, &ran)

			f := o.hostLoopbackFactsFor("podman", tc.netMode)
			for _, cmd := range ran {
				if strings.Contains(cmd, "pasta") || strings.Contains(cmd, "slirp4netns") {
					t.Errorf("asked the network stack a question nothing will read: %q (ran %v)", cmd, ran)
				}
			}
			if f.support != supportUnknown || f.fallbackSupport != supportUnknown {
				t.Errorf("facts = %+v, want no capability verdict at all", f)
			}
			// And the plan is still the safe one: nothing on the argv. The
			// explicit-mode branch warns, which is its own pinned behaviour.
			if plan := decideHostLoopback(f); len(plan.args) != 0 {
				t.Errorf("emitted %v, want nothing", plan.args)
			}
		})
	}
}

// TestHostLoopbackFallbackTrustsOnlyPodmansOwnLookup is the fail-safe with the
// sharpest edge in this feature. PODMAN is the process that will exec
// slirp4netns; a binary yolo can see on PATH and podman cannot is a container
// that fails to START — the one outcome this file may never produce — so an empty
// `podman info` executable must end the matter, however findable slirp4netns is
// from here.
func TestHostLoopbackFallbackTrustsOnlyPodmansOwnLookup(t *testing.T) {
	const pastaExe = "/nix/store/xxxx-podman-5.8.4/libexec/podman/pasta"
	var ran []string
	o := &Options{}
	fillDefaults(o)
	o.Getenv = func(string) string { return "" }
	o.LookPath = func(name string) (string, bool) {
		switch name {
		case "podman":
			return "/usr/bin/podman", true
		case "slirp4netns":
			return "/usr/local/bin/slirp4netns", true
		}
		return "", false
	}
	o.Exec = recordingHostExec(map[string]ExecResult{
		"/usr/bin/podman info --format json": {Ran: true, RC: 0, Stdout: podmanInfoNoSlirpFixture},
		pastaExe + " --help":                 {Ran: true, RC: 0, Stdout: "Usage: pasta\n  --map-gw\n"},
		"/usr/local/bin/slirp4netns --help":  {Ran: true, RC: 0, Stdout: slirpHelpWithFlag},
	}, &ran)

	f := o.hostLoopbackFactsFor("podman", "bridge")
	if f.fallbackSupport != supportUnknown {
		t.Errorf("fallbackSupport = %v; podman reported no slirp4netns, which settles it", f.fallbackSupport)
	}
	if slices.Contains(ran, "/usr/local/bin/slirp4netns --help") {
		t.Errorf("the fallback probe must never fall back to PATH, ran: %v", ran)
	}
	plan := decideHostLoopback(f)
	if len(plan.args) != 0 {
		t.Errorf("emitted %v, want the warn-and-launch path", plan.args)
	}
	if plan.disposition != paths.HostLoopbackUnsupported {
		t.Errorf("disposition = %q, want %q", plan.disposition, paths.HostLoopbackUnsupported)
	}
}

// TestHostLoopbackFactsForOptOut proves the escape hatch is read from the
// environment, since that is the only thing a user with a bricked launch can
// reach for.
func TestHostLoopbackFactsForOptOut(t *testing.T) {
	o := &Options{}
	fillDefaults(o)
	o.Getenv = func(k string) string {
		if k == hostLoopbackOptOutEnv {
			return "1"
		}
		return ""
	}
	o.LookPath = func(string) (string, bool) { return "", false }
	o.Exec = fakeHostExec(nil)

	if f := o.hostLoopbackFactsFor("podman", "bridge"); !f.optOut {
		t.Errorf("optOut = false with %s=1", hostLoopbackOptOutEnv)
	}
}
