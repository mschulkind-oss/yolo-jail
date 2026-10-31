package cli

// stopforce_test.go pins `yolo stop --force`: the flag parses (both spellings) and is documented,
// the container stop drops the grace it otherwise gives, force reaches the keeper's teardown, and a
// flag stop does not define is refused as misuse rather than silently dropped.

import (
	"bytes"
	"io"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// TestStopForceIsParsedAndDocumented: both spellings parse, a plain stop asks for none, and the help
// names the flag and the new usage line.
func TestStopForceIsParsedAndDocumented(t *testing.T) {
	for _, args := range [][]string{{"stop", "--force"}, {"stop", "-f"}} {
		if !stopForceRequested(args) {
			t.Errorf("stopForceRequested(%v) = false, want true", args)
		}
	}
	if stopForceRequested([]string{"stop"}) {
		t.Error("a plain stop asked for force")
	}
	for _, want := range []string{"--force", "-f", "Usage: yolo stop [--force]"} {
		if !strings.Contains(stopUsage, want) {
			t.Errorf("stopUsage does not mention %q", want)
		}
	}
}

// TestAForcedStopDropsTheGraceAndForcesTheKeeper is the call-site pin on the --force plumbing: a
// plain stop gives the runtime the shared grace and hands force=false to the keeper's teardown; a
// forced one gives the runtime none and hands force=true.
func TestAForcedStopDropsTheGraceAndForcesTheKeeper(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	saved := finishStop
	t.Cleanup(func() { finishStop = saved })
	var forced []bool
	finishStop = func(_, _ io.Writer, _, _ string, _ int64, _ func(string, string), force bool) int {
		forced = append(forced, force)
		return 0
	}
	cname := runtime.FromWorkspace("/ws")

	var out, errb bytes.Buffer
	s := &stopRun{stats: []string{"true\n", "", "!Error: no such container"}}
	if rc := stopJail(&out, &errb, "/ws", "podman", s.run, nil, false); rc != 0 {
		t.Fatalf("rc=%d, err=%s", rc, errb.String())
	}
	want := []string{"podman", "stop", "-t", strconv.Itoa(run.TeardownStopTimeoutSeconds), cname}
	if len(s.calls) < 2 || !slices.Equal(s.calls[1], want) {
		t.Errorf("a plain stop's argv = %v, want %v", s.calls, want)
	}

	out.Reset()
	errb.Reset()
	s = &stopRun{stats: []string{"true\n", "", "!Error: no such container"}}
	if rc := stopJail(&out, &errb, "/ws", "podman", s.run, nil, true); rc != 0 {
		t.Fatalf("rc=%d, err=%s", rc, errb.String())
	}
	wantForced := []string{"podman", "stop", "-t", "0", cname}
	if len(s.calls) < 2 || !slices.Equal(s.calls[1], wantForced) {
		t.Errorf("a forced stop's argv = %v, want %v", s.calls, wantForced)
	}
	if !slices.Equal(forced, []bool{false, true}) {
		t.Errorf("the keeper's teardown was handed force=%v, want [false true]", forced)
	}
}

// TestAForcedStopReachesTheRuntimeFromTheFrontDoor drives runStop: `yolo stop --force` asks the
// runtime for a zero-grace stop.
func TestAForcedStopReachesTheRuntimeFromTheFrontDoor(t *testing.T) {
	cname, _ := stopWorkspace(t, `{"runtime": "podman"}`)
	s := &stopRun{stats: []string{"true\n", "", "!Error: no such container"}}
	useStopExec(t, s.run)
	var rc int
	stdout, stderr := captureBoth(t, func() { rc = runStop([]string{"stop", "--force"}) })
	if rc != 0 {
		t.Fatalf("runStop = %d\nstdout:\n%s\nstderr:\n%s", rc, stdout, stderr)
	}
	want := []string{"podman", "stop", "-t", "0", cname}
	if len(s.calls) < 2 || !slices.Equal(s.calls[1], want) {
		t.Errorf("the forced stop's argv = %v, want %v", s.calls, want)
	}
}

func TestAForcedStopFailsClosedWhenPodmanInspectCannotDistinguishTheContainer(t *testing.T) {
	cname, _ := stopWorkspace(t, `{"runtime": "podman"}`)
	var calls [][]string
	runner := func(argv []string, _ time.Duration) (string, bool, int) {
		calls = append(calls, slices.Clone(argv))
		switch {
		case len(argv) > 1 && argv[1] == "inspect":
			if slices.Contains(argv, "{{.State.Running}}") {
				if slices.Contains(argv, "--type") {
					return "", true, 125 // typed container inspect cannot answer
				}
				return "", true, 0 // a same-name volume has no Running field
			}
			return "", true, 125
		case len(argv) > 2 && argv[1] == "container" && argv[2] == "exists":
			return "", true, 0 // the typed container exists, but its running state is unknown
		case len(argv) > 1 && (argv[1] == "stop" || argv[1] == "rm"):
			return "", true, 0
		default:
			t.Fatalf("unexpected podman argv %v", argv)
			return "", false, 1
		}
	}
	useStopExec(t, runner)
	var rc int
	stdout, stderr := captureBoth(t, func() { rc = runStop([]string{"stop", "--force"}) })
	if rc == 0 {
		t.Fatalf("unknown container state was treated as stopped; calls=%v\nstdout:\n%s\nstderr:\n%s", calls, stdout, stderr)
	}
	for _, argv := range calls {
		if len(argv) > 1 && (argv[1] == "stop" || argv[1] == "rm") {
			t.Fatalf("unknown container state authorized teardown: %v", calls)
		}
		if len(argv) > 1 && argv[1] == "inspect" && slices.Contains(argv, "{{.State.Running}}") {
			if !slices.Contains(argv, "--type") || !slices.Contains(argv, "container") || argv[len(argv)-1] != cname {
				t.Fatalf("running-state inspect was not container-typed for %q: %v", cname, argv)
			}
		}
	}
}

func TestStopJailDoesNotTreatUnknownAfterStopAsGone(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	// The container never leaves, so the wait for it to go runs to its bound: shrunk, as every
	// other still-there stop test does, rather than waited out at the production 15 s.
	shrinkStopGone(t)
	cname := runtime.FromWorkspace("/ws")
	var calls [][]string
	runningProbes := 0
	finished := false
	savedFinish := finishStop
	finishStop = func(_, _ io.Writer, _, _ string, _ int64, _ func(string, string), _ bool) int {
		finished = true
		return 0
	}
	t.Cleanup(func() { finishStop = savedFinish })
	runner := func(argv []string, _ time.Duration) (string, bool, int) {
		calls = append(calls, slices.Clone(argv))
		switch {
		case argv[1] == "inspect":
			if slices.Contains(argv, "{{.State.Running}}") {
				runningProbes++
				if runningProbes == 1 {
					return "true", true, 0
				}
			}
			return "", true, 125
		case argv[1] == "stop":
			return "", true, 1
		case argv[1] == "rm":
			return "", true, 125
		case argv[1] == "info":
			return `{}`, true, 0
		case len(argv) > 2 && argv[1] == "container" && argv[2] == "exists":
			return "", true, 0
		default:
			t.Fatalf("unexpected podman argv %v", argv)
			return "", false, 1
		}
	}
	var out, errb bytes.Buffer
	if rc := stopJail(&out, &errb, "/ws", "podman", runner, nil, true); rc != 1 {
		t.Fatalf("unknown post-stop running state returned %d, want failure\nstdout:\n%s\nstderr:\n%s", rc, out.String(), errb.String())
	}
	if !slices.Equal(calls[0], []string{"podman", "inspect", "--type", "container", "--format", "{{.State.Running}}", cname}) {
		t.Fatalf("pre-stop running probe = %v", calls[0])
	}
	if finished {
		t.Fatal("unknown post-stop running state authorized keeper teardown")
	}
	if !strings.Contains(errb.String(), "still there") {
		t.Fatalf("missing unresolved-container diagnosis: %s", errb.String())
	}
}

func TestProbeJailRunningRejectsMalformedSuccessfulInspectOutput(t *testing.T) {
	for _, output := range []string{"", "running", "true false", "maybe"} {
		t.Run(strconv.Quote(output), func(t *testing.T) {
			cname := "jail-name"
			var got []string
			runner := func(argv []string, _ time.Duration) (string, bool, int) {
				got = argv
				return output, true, 0
			}
			running, answered, _ := probeJailRunning("podman", cname, runner)
			if running || answered {
				t.Fatalf("malformed successful inspect gave running=%v answered=%v", running, answered)
			}
			want := []string{"podman", "inspect", "--type", "container", "--format", "{{.State.Running}}", cname}
			if !slices.Equal(got, want) {
				t.Fatalf("running inspect argv = %v, want %v", got, want)
			}
		})
	}
}

func TestProbeJailRunningUsesTypedExistenceAfterInspectFailure(t *testing.T) {
	for _, tc := range []struct {
		name       string
		presentRan bool
		presentRC  int
		wantKnown  bool
	}{
		{name: "absent", presentRan: true, presentRC: 1, wantKnown: true},
		{name: "present-unknown", presentRan: true, presentRC: 0},
		{name: "existence-error", presentRan: true, presentRC: 125},
		{name: "existence-incomplete", presentRan: false, presentRC: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls [][]string
			runner := func(argv []string, _ time.Duration) (string, bool, int) {
				calls = append(calls, slices.Clone(argv))
				if argv[1] == "inspect" {
					return "", true, 125
				}
				if !slices.Equal(argv, []string{"podman", "container", "exists", "jail-name"}) {
					t.Fatalf("unexpected fallback argv %v", argv)
				}
				return "", tc.presentRan, tc.presentRC
			}
			running, answered, _ := probeJailRunning("podman", "jail-name", runner)
			if running || answered != tc.wantKnown {
				t.Fatalf("probe returned running=%v answered=%v, want running=false answered=%v", running, answered, tc.wantKnown)
			}
			if len(calls) != 2 || !slices.Equal(calls[0], []string{"podman", "inspect", "--type", "container", "--format", "{{.State.Running}}", "jail-name"}) {
				t.Fatalf("inspect/existence calls = %v", calls)
			}
		})
	}
}

// TestStopRefusesAnUnknownFlag: a flag stop does not define is misuse — exit 2 and the pointer to
// help — never silently dropped with the ordinary status returned.
func TestStopRefusesAnUnknownFlag(t *testing.T) {
	var rc int
	_, stderr := captureBoth(t, func() { rc = runStop([]string{"stop", "--nope"}) })
	if rc != 2 {
		t.Fatalf("runStop with --nope = %d, want 2", rc)
	}
	for _, want := range []string{`unknown flag "--nope"`, "`yolo stop --help`"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("the refusal must say %q:\n%s", want, stderr)
		}
	}
}
