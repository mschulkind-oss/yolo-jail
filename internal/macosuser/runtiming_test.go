package macosuser

// runtiming_test.go pins RunMacosUser's spans on the launch's collector (Deps.Perf;
// docs/reference/perf-logging.md, the macos_user family): every step of a launch in order, the
// session's span ended before the teardown's own, and a refused step's span ended without the
// steps after it. Deleting the collector's use, or a step's begin, fails these.

import (
	"slices"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/perf"
)

// spanEnds is the names of every span that ended among events, in order.
func spanEnds(events *[]perf.Event) []string {
	var out []string
	for _, e := range *events {
		if e.Kind == perf.KindEnd {
			out = append(out, e.Name)
		}
	}
	return out
}

// recordingLog is a collector whose events the test reads back.
func recordingLog() (*perf.Log, *[]perf.Event) {
	var events []perf.Event
	return perf.New(nil, func(e perf.Event) { events = append(events, e) }), &events
}

func TestRunMacosUserSpansEveryStepInOrder(t *testing.T) {
	var rec []string
	d := mockDeps(&rec)
	ws := "/Users/Shared/yolo/proj"
	fakeSupervisor(&d, &rec, ws, "", readyLine, "", false)
	d.GuestBinaries = func(string) (string, error) { return "/opt/yolo/bin/darwin-arm64", nil }
	log, events := recordingLog()
	d.Perf = log
	o := newOpts(ws)
	o.Config = provisionCfg()
	o.JailDaemons = openAIAdapterDaemons("")
	if rc := RunMacosUser(d, o); rc != 42 {
		t.Fatalf("rc = %d", rc)
	}
	// The top-level steps only: each step's own sub-spans (macos_user.<step>.<sub>) are
	// stagetiming_test.go's.
	var got []string
	for _, name := range spanEnds(events) {
		if strings.Count(name, ".") == 1 {
			got = append(got, name)
		}
	}
	want := []string{"preconditions", "context_preflight", "materialize", "host_nix",
		"guest_binaries", "ca_trust", "build_plan", "workspace_lock", "session_sweep", "install_profile",
		"stage", "env_file", "bootstrap", "provision", "start_jail_daemons", "service_probe", "agent",
		"stop_jail_daemons", "remove_env_file", "release_pack_tree"}
	for i := range want {
		want[i] = "macos_user." + want[i]
	}
	if !slices.Equal(got, want) {
		t.Errorf("the launch's spans ended as\n  %s\nwant\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
	// Every span that started ended: a dangling start is a hang in the host perf log.
	starts, ends := 0, 0
	for _, e := range *events {
		switch e.Kind {
		case perf.KindStart:
			starts++
		case perf.KindEnd:
			ends++
		}
	}
	if starts != ends {
		t.Errorf("%d spans started and %d ended", starts, ends)
	}
}

// A REFUSED BUILD ends its own span, and no later step's is ever started.
func TestAFailedMaterializeEndsItsSpanAndStartsNoLaterOne(t *testing.T) {
	d := mockDeps(nil)
	d.MaterializeDarwin = func(string, []any) (*Darwin, bool, error) { return nil, false, errFake("no build") }
	log, events := recordingLog()
	d.Perf = log
	if rc := RunMacosUser(d, newOpts("/Users/Shared/yolo/proj")); rc != 1 {
		t.Fatalf("rc = %d, want the refusal", rc)
	}
	got := spanEnds(events)
	want := []string{"macos_user.preconditions", "macos_user.context_preflight",
		"macos_user.materialize"}
	if !slices.Equal(got, want) {
		t.Errorf("a refused build's spans: %v, want %v", got, want)
	}
	for _, e := range *events {
		if e.Kind == perf.KindStart && !slices.Contains(want, e.Name) {
			t.Errorf("a step after the refusal started: %s", e.Name)
		}
	}
}

// A DRY RUN times nothing: it runs no step.
func TestADryRunRecordsNoSpan(t *testing.T) {
	d := mockDeps(nil)
	log, events := recordingLog()
	d.Perf = log
	o := newOpts("/Users/Shared/yolo/proj")
	o.DryRun = true
	RunMacosUser(d, o)
	if len(*events) != 0 {
		t.Errorf("a dry run recorded %d events", len(*events))
	}
}
