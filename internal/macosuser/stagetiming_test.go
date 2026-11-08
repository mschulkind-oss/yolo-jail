package macosuser

// stagetiming_test.go pins the SUB-STEP spans inside RunMacosUser's slow steps
// (docs/reference/perf-logging.md, the macos_user family): the stage's groups of commands, the env
// file's two writes, the bootstrap's guest steps read back from its perf file, and the pack tree's
// release. Each runs through RunMacosUser itself, so deleting a span at its call site fails here.

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/perf"
)

// tickingLog is a collector whose clock moves only when a command runs: every Deps.Run costs one
// second and nothing else costs anything, so a parent span's duration is exactly the sum of what
// ran inside it, and a sub-span that misses a command shows up as a remainder.
func tickingLog(d *Deps) (*perf.Log, *[]perf.Event) {
	now := time.Unix(1_000_000, 0)
	run := d.Run
	d.Run = func(argv []string) int {
		now = now.Add(time.Second)
		return run(argv)
	}
	var events []perf.Event
	return perf.New(func() time.Time { return now }, func(e perf.Event) { events = append(events, e) }), &events
}

// durations sums each ended span's duration by name.
func durations(events []perf.Event) map[string]time.Duration {
	out := map[string]time.Duration{}
	for _, e := range events {
		if e.Kind == perf.KindEnd {
			out[e.Name] += e.Dur
		}
	}
	return out
}

func stageTimingOpts(t *testing.T, ws string) Options {
	o := newOpts(ws)
	o.Config = provisionCfg()
	o.JailDaemons = openAIAdapterDaemons("")
	o.HostPackRoot = t.TempDir()
	o.HostHomeOverlay = HomeOverlay{Tree: t.TempDir()}
	o.HostCtx = HostContext{Tree: t.TempDir()}
	return o
}

func TestTheStageSpansEveryGroupOfItsCommandsAndTheyAccountForIt(t *testing.T) {
	var rec []string
	d := mockDeps(&rec)
	ws := "/Users/Shared/yolo/proj"
	fakeSupervisor(&d, &rec, ws, "", readyLine, "", false)
	d.GuestBinaries = func(string) (string, error) { return "/opt/yolo/bin/darwin-arm64", nil }
	log, events := tickingLog(&d)
	d.Perf = log
	if rc := RunMacosUser(d, stageTimingOpts(t, ws)); rc != 42 {
		t.Fatalf("rc = %d", rc)
	}
	got := durations(*events)
	for _, name := range []string{"claim_pack_tree", "binary", "pack_tree", "home_overlay", "context_dir",
		"guest_binaries", "daemon_endpoint_grants", "captures"} {
		if _, ok := got["macos_user.stage."+name]; !ok {
			t.Errorf("no macos_user.stage.%s span; recorded %v", name, spanEnds(events))
		}
	}
	// THE GROUPS ACCOUNT FOR THE STAGE: every command the stage ran is inside one of them.
	var sum time.Duration
	for name, d := range got {
		if strings.HasPrefix(name, "macos_user.stage.") {
			sum += d
		}
	}
	if stage := got["macos_user.stage"]; stage == 0 || sum != stage {
		t.Errorf("the stage took %v and its sub-spans %v: a stage command ran outside every sub-span", stage, sum)
	}
	// AND EACH COMMAND IS NOTED, with its step, in the file alone.
	notes := 0
	for _, e := range *events {
		if e.Kind == perf.KindNote && e.Name == "macos_user.stage.command" {
			notes++
			if !strings.Contains(e.Detail, "s ") {
				t.Errorf("a command's note carries no duration: %q", e.Detail)
			}
		}
	}
	if notes == 0 {
		t.Error("no stage command was noted")
	}
	// THE ENV FILE'S TWO WRITES and the pack tree's release are spanned too.
	for _, name := range []string{"macos_user.env_file.session_env", "macos_user.env_file.ca_trust_files",
		"macos_user.release_pack_tree"} {
		if _, ok := got[name]; !ok {
			t.Errorf("no %s span", name)
		}
	}
	if envSum := got["macos_user.env_file.session_env"] + got["macos_user.env_file.ca_trust_files"]; envSum != got["macos_user.env_file"] {
		t.Errorf("the env file took %v and its sub-spans %v", got["macos_user.env_file"], envSum)
	}
}

// A REFUSED STAGE COMMAND ends the group it ran in, and no group after it starts.
func TestAFailedStageCommandEndsItsGroupsSpan(t *testing.T) {
	d := mockDeps(nil)
	ws := "/Users/Shared/yolo/proj"
	o := stageTimingOpts(t, ws)
	o.JailDaemons = JailDaemons{}
	d.Run = func(argv []string) int {
		if len(argv) > 1 && argv[0] == cpBin && argv[1] == "-R" && strings.HasPrefix(argv[2], o.HostHomeOverlay.Tree) {
			return 1
		}
		return 0
	}
	log, events := recordingLog()
	d.Perf = log
	if rc := RunMacosUser(d, o); rc != 1 {
		t.Fatalf("rc = %d, want the refusal", rc)
	}
	starts, ends := map[string]int{}, map[string]int{}
	for _, e := range *events {
		switch e.Kind {
		case perf.KindStart:
			starts[e.Name]++
		case perf.KindEnd:
			ends[e.Name]++
		}
	}
	for name, n := range starts {
		if ends[name] != n {
			t.Errorf("%s started %d times and ended %d", name, n, ends[name])
		}
	}
	if ends["macos_user.stage.home_overlay"] != 1 {
		t.Errorf("the failing group's span did not end: %v", spanEnds(events))
	}
	if starts["macos_user.stage.context_dir"] != 0 {
		t.Error("a group after the refusal started")
	}
}

// THE BOOTSTRAP'S GUEST STEPS: the guest writes its step marks to BootPerfPath, and the host
// records one span per step, named, after the bootstrap — and removes a stale file first, so a
// previous launch's marks are never read as this one's.
func TestTheBootstrapsGuestStepsAreRecordedFromItsPerfFile(t *testing.T) {
	var rec []string
	d := mockDeps(&rec)
	ws := "/Users/Shared/yolo/proj"
	perfFile := entrypoint.BootPerfPath(ws)
	written := ""
	removed := false
	d.RemoveFile = func(p string) bool {
		if p == perfFile {
			removed = true
			written = ""
		}
		return true
	}
	run := d.Run
	d.Run = func(argv []string) int {
		if slices.Contains(argv, "darwin-bootstrap") {
			if !removed {
				t.Error("the bootstrap ran before its stale perf file was removed")
			}
			written = "0.250000\thydrate_user_env\n1.750000\tconfigure pack surfaces\n" +
				"bogus line\n2.000000\t../../evil\n"
		}
		return run(argv)
	}
	d.ReadFile = func(p string) (string, bool) {
		if p == perfFile {
			return written, true
		}
		return "", true
	}
	log, events := recordingLog()
	d.Perf = log
	o := newOpts(ws)
	if rc := RunMacosUser(d, o); rc != 42 {
		t.Fatalf("rc = %d", rc)
	}
	got := durations(*events)
	want := map[string]time.Duration{
		"macos_user.bootstrap.hydrate_user_env":        250 * time.Millisecond,
		"macos_user.bootstrap.configure_pack_surfaces": 1500 * time.Millisecond,
		"macos_user.bootstrap.evil":                    250 * time.Millisecond,
	}
	for name, dur := range want {
		if got[name] != dur {
			t.Errorf("%s = %v, want %v (recorded %v)", name, got[name], dur, spanEnds(events))
		}
	}
	for name := range got {
		if strings.HasPrefix(name, "macos_user.bootstrap.") {
			if _, ok := want[name]; !ok {
				t.Errorf("unexpected bootstrap span %q", name)
			}
		}
	}
}

// A bootstrap from an older yolo writes its perf file directly in `.yolo`, not in `.yolo/logs`:
// both places are cleared before the bootstrap runs, and the steps are read from whichever one
// it wrote.
func TestTheBootstrapsStepsAreReadFromTheOlderPerfFilePlace(t *testing.T) {
	var rec []string
	d := mockDeps(&rec)
	ws := "/Users/Shared/yolo/proj"
	legacy := paths.LegacyWorkspaceLogPath(ws, entrypoint.BootPerfName)
	written, removedLegacy := "", false
	d.RemoveFile = func(p string) bool {
		if p == legacy {
			removedLegacy = true
		}
		return true
	}
	run := d.Run
	d.Run = func(argv []string) int {
		if slices.Contains(argv, "darwin-bootstrap") {
			if !removedLegacy {
				t.Error("the bootstrap ran before the older place's stale perf file was removed")
			}
			written = "0.500000\thydrate_user_env\n"
		}
		return run(argv)
	}
	d.ReadFile = func(p string) (string, bool) {
		if p == legacy && written != "" {
			return written, true
		}
		return "", false
	}
	log, events := recordingLog()
	d.Perf = log
	if rc := RunMacosUser(d, newOpts(ws)); rc != 42 {
		t.Fatalf("rc = %d", rc)
	}
	if got := durations(*events)["macos_user.bootstrap.hydrate_user_env"]; got != 500*time.Millisecond {
		t.Errorf("hydrate_user_env = %v, want 500ms (recorded %v)", got, spanEnds(events))
	}
}

func TestParseBootPerfBoundsWhatTheSandboxWrote(t *testing.T) {
	var b strings.Builder
	for i := range maxBootPerfSteps + 10 {
		fmt.Fprintf(&b, "%d.0\tstep_%d\n", i+1, i)
	}
	if n := len(parseBootPerf(b.String())); n != maxBootPerfSteps {
		t.Errorf("parsed %d steps, want the cap %d", n, maxBootPerfSteps)
	}
	// A step whose elapsed time goes backwards is not a step.
	if got := parseBootPerf("2.0\ta\n1.0\tb\n"); len(got) != 1 {
		t.Errorf("a backwards mark was kept: %v", got)
	}
}
