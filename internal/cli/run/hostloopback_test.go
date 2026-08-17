package run

import (
	"slices"
	"strings"
	"testing"
	"time"
)

// The decision function is the half of the host-loopback fix that CAN be tested
// from a development jail: nobody here has a pasta host, and podman-in-podman
// forces --net=host, so the argv is the only thing a unit test can hold. It is
// also the half that can brick the product — a wrong --network flag stops a jail
// from starting at all — which is why the sweep below exists alongside the named
// cases: the named cases pin the intended behaviour, the sweep pins the SAFETY
// PROPERTY over every input combination there is.

const (
	pastaArg = "--network=pasta:--map-host-loopback,169.254.1.2"
	slirpArg = "--network=slirp4netns:allow_host_loopback=true"
)

func TestDecideHostLoopback(t *testing.T) {
	tests := []struct {
		name string
		f    hostLoopbackFacts
		args []string
		// warn is a substring the warning must contain; "" demands silence.
		warn string
	}{
		// --- the fix firing ---
		{
			name: "pasta confirmed on the default path emits the map flag",
			f:    hostLoopbackFacts{netMode: "bridge", backend: "pasta", rootless: true, support: supportConfirmed},
			args: []string{pastaArg},
		},
		{
			name: "slirp4netns confirmed on the default path emits allow_host_loopback",
			f:    hostLoopbackFacts{netMode: "bridge", backend: "slirp4netns", rootless: true, support: supportConfirmed},
			args: []string{slirpArg},
		},

		// --- capability not positively confirmed: emit nothing, say why ---
		{
			name: "pasta without the flag warns with the version and the check command",
			f: hostLoopbackFacts{netMode: "bridge", backend: "pasta", rootless: true,
				support: supportAbsent, probeCmd: "/usr/bin/pasta --help", version: "pasta 2024_01_01.abc"},
			warn: "Upgrade passt to 2024_08_21",
		},
		{
			name: "pasta version is echoed so the user knows what they have",
			f: hostLoopbackFacts{netMode: "bridge", backend: "pasta", rootless: true,
				support: supportAbsent, version: "pasta 2024_01_01.abc"},
			warn: "(reported: pasta 2024_01_01.abc)",
		},
		{
			name: "pasta that could not be probed says so rather than blaming the version",
			f: hostLoopbackFacts{netMode: "bridge", backend: "pasta", rootless: true,
				support: supportUnknown, probeCmd: "/usr/bin/pasta --help"},
			warn: "could not confirm it",
		},
		{
			name: "pasta with no binary at all names that instead of a probe",
			f:    hostLoopbackFacts{netMode: "bridge", backend: "pasta", rootless: true, support: supportUnknown},
			warn: "could not find a pasta binary",
		},
		{
			name: "slirp4netns without the flag warns and emits nothing",
			f: hostLoopbackFacts{netMode: "bridge", backend: "slirp4netns", rootless: true,
				support: supportAbsent, version: "slirp4netns version 0.3.0"},
			warn: "slirp4netns --help",
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
		})
	}
}

// TestDecideHostLoopbackOnlyEmitsOnPositiveFacts sweeps EVERY combination of the
// inputs and asserts the one property that keeps a bad detection from bricking a
// user's environment: an argv is emitted if and only if every fact is positive —
// the default network mode, a rootless podman, a backend yolo recognises, a
// capability it confirmed by asking, and no opt-out. Anything else must fall back
// to emitting nothing, which is byte-for-byte the behaviour before this feature.
//
// This is deliberately a separate test from the named cases above. Those say what
// the feature does; this says what it can never do, and it is the one that has to
// survive somebody adding a backend or a support state.
func TestDecideHostLoopbackOnlyEmitsOnPositiveFacts(t *testing.T) {
	netModes := []string{"bridge", "host", "none", "private"}
	backends := []string{"pasta", "slirp4netns", "netavark", "cni", ""}
	supports := []hostLoopbackSupport{supportUnknown, supportConfirmed, supportAbsent}

	for _, netMode := range netModes {
		for _, backend := range backends {
			for _, support := range supports {
				for _, rootless := range []bool{true, false} {
					for _, optOut := range []bool{true, false} {
						f := hostLoopbackFacts{
							netMode: netMode, backend: backend, rootless: rootless,
							support: support, optOut: optOut,
						}
						wantEmit := netMode == "bridge" && rootless && !optOut &&
							support == supportConfirmed && mappableBackend(backend)
						got := decideHostLoopback(f)
						if wantEmit != (len(got.args) > 0) {
							t.Errorf("%+v: emitted %v, wantEmit=%v", f, got.args, wantEmit)
							continue
						}
						// A flag that is emitted must be one of the two literals
						// reviewed here — never an improvised one.
						if len(got.args) > 0 &&
							!slices.Equal(got.args, []string{pastaArg}) &&
							!slices.Equal(got.args, []string{slirpArg}) {
							t.Errorf("%+v: unexpected argv %v", f, got.args)
						}
					}
				}
			}
		}
	}
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
