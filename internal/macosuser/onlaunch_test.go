package macosuser

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/provision"
)

// onlaunch_test.go pins WHEN RunMacosUser runs the session-start hook the run pipeline hands it
// (JailDaemons.OnLaunch, which opens the launch's port relays): once, after every step that can
// refuse the launch and immediately before the session's command, with what it returns run after
// that command and before the guest supervisor stops. internal/cli/run's
// macosuserportrelay_test.go pins the other half: that the arm hands the hook over and opens
// nothing itself.

// recordingOnLaunch is an OnLaunch hook that records its call and its stop in rec.
func recordingOnLaunch(rec *[]string) func() func() {
	return func() func() {
		*rec = append(*rec, "onlaunch")
		return func() { *rec = append(*rec, "onlaunch-stop") }
	}
}

// onLaunchRecIndex is the index of the first record r with prefix, or -1.
func onLaunchRecIndex(rec []string, prefix string) int {
	for i, r := range rec {
		if strings.HasPrefix(r, prefix) {
			return i
		}
	}
	return -1
}

// A launch that starts its session runs the hook after the supervisor and the witness and before
// the session's command, and what it returned after that command and before the supervisor stops.
// Fails if the call moves above any step that can refuse, or below the command.
func TestOnLaunchRunsJustBeforeTheSessionAndStopsAfterIt(t *testing.T) {
	var rec []string
	d := mockDeps(&rec)
	run := d.Run
	d.Run = func(argv []string) int {
		got := run(argv)
		if containsArgRun(argv, []string{"internal", ProbeServicesVerb}) {
			return 0
		}
		return got
	}
	fakeSupervisor(&d, &rec, probeWS, "", readyLine, "", false)
	d.GuestBinaries = func(string) (string, error) { return "/opt/yolo/bin/darwin-arm64", nil }
	var buf bytes.Buffer
	d.Out = &buf
	o := newOpts(probeWS)
	o.SandboxEnv = brokerEndpointEnv()
	o.JailDaemons = openAIAdapterDaemons("")
	o.JailDaemons.OnLaunch = recordingOnLaunch(&rec)
	if rc := RunMacosUser(d, o); rc != 42 {
		t.Fatalf("rc = %d, want the session's\n%s", rc, buf.String())
	}
	start := onLaunchRecIndex(rec, "start:")
	probe := -1
	for i, r := range rec {
		if strings.HasSuffix(r, " internal "+ProbeServicesVerb) {
			probe = i
		}
	}
	hook, proxy, hookStop := onLaunchRecIndex(rec, "onlaunch"), onLaunchRecIndex(rec, "proxy:"), onLaunchRecIndex(rec, "onlaunch-stop")
	stop := -1
	for i, r := range rec {
		if r == "stop" {
			stop = i
		}
	}
	if start < 0 || probe < 0 || hook < 0 || proxy < 0 || hookStop < 0 || stop < 0 ||
		!(start < probe && probe < hook && hook < proxy && proxy < hookStop && hookStop < stop) {
		t.Fatalf("want supervisor start < witness < hook < session < hook's stop < supervisor stop; "+
			"got %d %d %d %d %d %d:\n%s", start, probe, hook, proxy, hookStop, stop, strings.Join(rec, "\n"))
	}
	if n := strings.Count(strings.Join(rec, "\n"), "onlaunch\n"); n != 1 {
		t.Errorf("the hook ran %d times, want once:\n%s", n, strings.Join(rec, "\n"))
	}
}

// A launch refused at any step, or a dry run, never runs the hook: so the port relays it opens
// never listen for a launch that does not start its session, or while it is still building.
func TestOnLaunchNeverRunsForARefusedLaunchOrADryRun(t *testing.T) {
	for _, tc := range []struct {
		name   string
		setup  func(*Deps, *Options, *[]string)
		wantRC int
		said   string // what shows the launch stopped at that step
	}{
		{name: "a precondition (root)", wantRC: 1, said: "Don't run `yolo` under sudo",
			setup: func(d *Deps, _ *Options, _ *[]string) { d.Geteuid = func() int { return 0 } }},
		{name: "the nix build", wantRC: 1, said: "Could not materialize packages natively: boom", setup: func(d *Deps, _ *Options, _ *[]string) {
			d.MaterializeDarwin = func(string, []any) (*Darwin, bool, error) { return nil, false, errFake("boom") }
		}},
		{name: "the bootstrap", wantRC: 1, said: "entrypoint bootstrap failed", setup: func(d *Deps, _ *Options, rec *[]string) {
			run := d.Run
			d.Run = func(argv []string) int {
				if strings.Contains(strings.Join(argv, " "), "internal darwin-bootstrap") {
					*rec = append(*rec, "run:"+strings.Join(argv, " "))
					return 1
				}
				return run(argv)
			}
		}},
		{name: "the host-service witness", wantRC: 1,
			said: "The sandbox cannot use a host service this launch enabled", setup: func(d *Deps, o *Options, _ *[]string) {
				run := d.Run
				d.Run = func(argv []string) int {
					got := run(argv)
					if containsArgRun(argv, []string{"internal", ProbeServicesVerb}) {
						return provision.RefusedStatus
					}
					return got
				}
				o.SandboxEnv = brokerEndpointEnv()
			}},
		{name: "a dry run", wantRC: 0, said: "", setup: func(d *Deps, o *Options, _ *[]string) {
			d.IsMacOS = func() bool { return false }
			o.DryRun = true
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var rec []string
			d := mockDeps(&rec)
			var buf bytes.Buffer
			d.Out = &buf
			o := newOpts(probeWS)
			tc.setup(&d, &o, &rec)
			called := false
			o.JailDaemons.OnLaunch = func() func() {
				called = true
				return nil
			}
			if rc := RunMacosUser(d, o); rc != tc.wantRC {
				t.Fatalf("rc = %d, want %d\n%s", rc, tc.wantRC, buf.String())
			}
			if !strings.Contains(buf.String(), tc.said) {
				t.Errorf("the launch did not stop where this case means (want %q):\n%s", tc.said, buf.String())
			}
			if called {
				t.Errorf("the session-start hook ran for a launch that never started its session:\n%s",
					strings.Join(rec, "\n"))
			}
		})
	}
}

// A SIGNAL AT THE LAST BOUNDARY (the release of the workspace lock, after every step that can
// refuse) ends the launch without running the hook: the arm's Ending is asked first, so no port
// relay opens for a session that never starts. Fails if the hook's call moves above that check.
func TestOnLaunchNeverRunsForALaunchASignalEnded(t *testing.T) {
	var rec []string
	d := mockDeps(&rec)
	flip := endingAfter(&d)
	d.LockWorkspace = func(string, string) func() { return flip }
	var buf bytes.Buffer
	d.Out = &buf
	o := newOpts(probeWS)
	called := false
	o.JailDaemons.OnLaunch = func() func() {
		called = true
		return nil
	}
	if rc := RunMacosUser(d, o); rc != 143 {
		t.Fatalf("rc = %d, want 143 (the signal's status)\n%s\n%s", rc, buf.String(), strings.Join(rec, "\n"))
	}
	if called {
		t.Errorf("the session-start hook ran for a launch a signal ended:\n%s", strings.Join(rec, "\n"))
	}
	if onLaunchRecIndex(rec, "proxy:") >= 0 {
		t.Errorf("the session ran after the signal:\n%s", strings.Join(rec, "\n"))
	}
}
