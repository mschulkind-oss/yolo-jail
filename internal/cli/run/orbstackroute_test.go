package run

// orbstackroute_test.go pins component C of docs/research/orbstack-as-a-podman-host-plan.md
// through its production callers: the macOS readiness answer identifies the endpoint, the
// assembler tells the jail `requested`, the keeper's plan carries the fact to the fresh Options
// that starts the daemons, and a spawned loopback-TLS daemon publishes host.orb.internal. A
// Linux run proves only this logic; the Mac-to-jail hop is the native workflow's
// (.github/workflows/orbstack.yml).

import (
	"bytes"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/svcendpoint"
)

// orbStackInfoFixture is `podman info --format json` from a rootless Podman in an OrbStack
// machine, trimmed to the fields read. The kernel release has OrbStack's shape.
const orbStackInfoFixture = `{
  "host": {
    "kernel": "6.17.8-orbstack-00308-g8f9c941121b1",
    "rootlessNetworkCmd": "pasta",
    "security": {"rootless": true}
  },
  "version": {"Version": "6.1.2"}
}`

// podmanMachineInfoFixture is the same answer from a Podman Machine (Fedora CoreOS kernel).
const podmanMachineInfoFixture = `{
  "host": {
    "kernel": "6.16.7-200.fc42.aarch64",
    "rootlessNetworkCmd": "pasta",
    "security": {"rootless": true}
  }
}`

func TestIsOrbStackPodmanNeedsBothPositiveFacts(t *testing.T) {
	cases := []struct {
		name, info string
		want       bool
	}{
		{"rootless OrbStack machine", orbStackInfoFixture, true},
		{"Podman Machine", podmanMachineInfoFixture, false},
		{"rootful OrbStack machine",
			`{"host":{"kernel":"6.17.8-orbstack-00308-g8f9c941121b1","security":{"rootless":false}}}`, false},
		{"no security block", `{"host":{"kernel":"6.17.8-orbstack-00308-g8f9c941121b1"}}`, false},
		{"no kernel", `{"host":{"security":{"rootless":true}}}`, false},
	}
	for _, tc := range cases {
		info, ok := parsePodmanInfo(tc.info)
		if !ok {
			t.Fatalf("%s: fixture does not parse", tc.name)
		}
		if got := isOrbStackPodman(info); got != tc.want {
			t.Errorf("%s: isOrbStackPodman = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// The fact comes from the macOS patient one-shot's own answer, through runtime selection: no
// second `podman info` is asked for it, and a Podman Machine's answer sets nothing.
func TestTheMacosReadinessAnswerIdentifiesAnOrbStackEndpoint(t *testing.T) {
	for _, tc := range []struct {
		name, answer string
		want         bool
	}{
		{"OrbStack", orbStackInfoFixture, true},
		{"Podman Machine", podmanMachineInfoFixture, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := gatePaths()[0]
			var stdout, stderr bytes.Buffer
			m := &fakeMachine{answer: tc.answer}
			p.prepare(t)
			o := m.asMac(p, &stdout, &stderr)
			rt, ok := o.resolveRuntime(p.cfg)
			if !ok || rt != "podman" {
				t.Fatalf("resolveRuntime = %q,%v; stderr:\n%s", rt, ok, stderr.String())
			}
			if len(m.probes) != 1 {
				t.Errorf("asked podman %d times, want once: %q", len(m.probes), m.probes)
			}
			if o.orbStackPodman != tc.want {
				t.Errorf("orbStackPodman = %v, want %v", o.orbStackPodman, tc.want)
			}
		})
	}
}

// orbStackMacOptions is a macOS launch whose readiness answer named endpoint info, with getenv
// answering the environment.
func orbStackMacOptions(t *testing.T, info string, env map[string]string) *Options {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	emptyLoopholeDirs(t)
	o := goldenOptions("/Users/me/ws", home)
	o.IsMacOS, o.IsLinux = true, false
	o.Getenv = func(k string) string { return env[k] }
	o.acceptMachineFacts(info)
	return o
}

// The disposition the jail is told and the name the daemons publish come from one predicate:
// `requested` exactly when the daemons publish host.orb.internal, on every mode and with and
// without the opt-out, and neither on a Podman Machine.
func TestAssembleRunCmdOrbStackDispositionMatchesWhatTheDaemonsPublish(t *testing.T) {
	cases := []struct {
		name, info, flagMode string
		optOut               bool
		wantDisp, wantAdv    string
		wantWarning          string
	}{
		{"OrbStack bridge", orbStackInfoFixture, "", false, paths.HostLoopbackRequested, orbStackHostName, ""},
		{"OrbStack explicit mode", orbStackInfoFixture, "none", false, paths.HostLoopbackUnknown, "", ""},
		{"OrbStack opt-out", orbStackInfoFixture, "", true, paths.HostLoopbackUnknown, "",
			"NOT routing this jail's host services through OrbStack's " + orbStackHostName},
		{"Podman Machine bridge", podmanMachineInfoFixture, "", false, paths.HostLoopbackUnknown, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := map[string]string{}
			if tc.optOut {
				env[hostLoopbackOptOutEnv] = "1"
			}
			o := orbStackMacOptions(t, tc.info, env)
			o.Network = tc.flagMode
			var stderr strings.Builder
			o.Stderr = &stderr
			in := relocationInput(t, "podman", t.TempDir(), nil)

			argv := o.assembleRunCmd(in)

			got, ok := envValue(argv, paths.HostLoopbackEnvVar)
			if !ok {
				t.Fatalf("every launch carries a disposition; argv: %v", argv)
			}
			if got != tc.wantDisp {
				t.Errorf("%s = %q, want %q", paths.HostLoopbackEnvVar, got, tc.wantDisp)
			}
			adv := o.advertiseHostFor("podman", in.cfg)
			if adv != tc.wantAdv {
				t.Errorf("advertiseHostFor = %q, want %q", orDefaultAdvertise(adv), orDefaultAdvertise(tc.wantAdv))
			}
			if (got == paths.HostLoopbackRequested) != (adv == orbStackHostName) {
				t.Errorf("%s = %q but the daemons publish %q: the severity and the address must come "+
					"from one fact", paths.HostLoopbackEnvVar, got, orDefaultAdvertise(adv))
			}
			if s := networkSelectors(argv); tc.flagMode == "" && len(s) != 0 {
				t.Errorf("the OrbStack route needs no network argv, got %v", s)
			}
			if tc.wantWarning != "" && !strings.Contains(stderr.String(), tc.wantWarning) {
				t.Errorf("stderr lacks %q:\n%s", tc.wantWarning, stderr.String())
			}
		})
	}
}

// The keeper starts the daemons in fresh Options (newKeeper), so the fact crosses its plan, and
// an opt-out at the launch is the keeper's too.
func TestTheKeeperAdvertisesTheOrbStackRouteItsLaunchTookAndNoOther(t *testing.T) {
	for _, tc := range []struct {
		name   string
		optOut bool
		want   string
	}{
		{"route", false, orbStackHostName},
		{"opt-out", true, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := map[string]string{}
			if tc.optOut {
				env[hostLoopbackOptOutEnv] = "1"
			}
			o := orbStackMacOptions(t, orbStackInfoFixture, env)
			cfg := jsonx.NewOrderedMap()
			plan, err := o.keeperPlanFor(cfg, "podman", "yolo-orb", stagedPacks{}, nil, nil, nil,
				"", "", []string{"podman", "run"}, &assembleInput{})
			if err != nil {
				t.Fatal(err)
			}
			k := newKeeper(plan, KeeperSeams{}, nil, nil, nil, nil)
			k.o.Getenv = func(string) string { return "" }
			// Not a nested jail: the keeper's real probe would see this test's own container.
			k.o.PathExists = func(string) bool { return false }
			if got := k.o.advertiseHostFor("podman", cfg); got != tc.want {
				t.Errorf("the keeper advertises %q, want %q", orDefaultAdvertise(got), orDefaultAdvertise(tc.want))
			}
		})
	}
}

// End to end on the host side: a keeper built from an OrbStack launch's plan starts a
// loopback-TLS loophole whose published endpoint names host.orb.internal, the address the jail
// was told to treat as `requested`.
func TestAnOrbStackKeepersDaemonPublishesTheOrbStackHost(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("spawns a host process")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	emptyLoopholeDirs(t)

	loopCfg := jsonx.NewOrderedMap()
	entry := jsonx.NewOrderedMap()
	entry.Set("description", "echo daemon on a plain unix socket")
	entry.Set("command", []any{os.Args[0], "-front-upstream-child", "line", "{socket}"})
	loopCfg.Set("echoer", entry)
	cfg := newConfig("loopholes", loopCfg)

	launch := &Options{}
	fillDefaults(launch)
	launch.Getenv = func(string) string { return "" }
	launch.acceptMachineFacts(orbStackInfoFixture)
	plan, err := launch.keeperPlanFor(cfg, "podman", "yolo-orb-"+sha1Hex8(home), stagedPacks{}, nil, nil, nil,
		"", "", []string{"podman", "run"}, &assembleInput{})
	if err != nil {
		t.Fatal(err)
	}
	k := newKeeper(plan, KeeperSeams{}, nil, nil, nil, nil)
	o := k.o
	o.helperPin = testPin()
	var buf strings.Builder
	o.Stderr = &buf
	o.Getenv = func(string) string { return "" }
	o.PathExists = func(string) bool { return false }
	o.Exec = func([]string, string, []string, time.Duration) ExecResult { return ExecResult{Ran: true} }

	cname := plan.Cname
	socketsDir := hostServiceSocketsDir(cname, o.IsMacOS)
	handles := o.startLoopholes(cname, "podman", cfg)
	defer o.stopLoopholes(handles, socketsDir, cname, "podman")

	var h *loopholeDaemon
	for i := range handles {
		if handles[i].name == "echoer" {
			h = &handles[i]
		}
	}
	if h == nil {
		t.Fatalf("no handle for the loophole; output=%q", buf.String())
	}
	ep, err := svcendpoint.Read(h.hostPath)
	if err != nil {
		t.Fatalf("read the published endpoint: %v", err)
	}
	host, _, err := net.SplitHostPort(ep.HostPort)
	if err != nil {
		t.Fatalf("endpoint address %q: %v", ep.HostPort, err)
	}
	if host != orbStackHostName {
		t.Errorf("the daemon publishes %q, want %q", host, orbStackHostName)
	}
}
