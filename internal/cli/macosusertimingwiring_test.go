package cli

// macosusertimingwiring_test.go pins that a macos-user launch's steps are spanned on the run's own
// collector: the handler `yolo run` injects reads the collector from the PerfRef it handed the
// pipeline and hands it to the backend (macosUserRun's log). It invokes the REAL handler, live, on
// Linux, where the backend refuses at its first precondition (not macOS) — after recording that
// step's span, which is what proves the collector arrived.

import (
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/perf"
)

func TestTheMacosUserHandlerSpansTheBackendOnTheRunsCollector(t *testing.T) {
	if paths.IsMacOS {
		t.Skip("the handler's live path refuses at its first precondition only off macOS")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	var seen run.Options
	prev := launchRunPipeline
	launchRunPipeline = func(o run.Options) int { seen = o; return 0 }
	t.Cleanup(func() { launchRunPipeline = prev })
	captureBoth(t, func() { _ = Main([]string{"yolo", "--", "true"}) })
	if seen.PerfRef == nil || seen.MacosUserRun == nil {
		t.Fatal("the front door handed the pipeline no PerfRef or no macos-user handler")
	}
	var events []perf.Event
	seen.PerfRef.Log = perf.New(nil, func(e perf.Event) { events = append(events, e) })
	ws, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var rc int
	captureBoth(t, func() {
		rc = seen.MacosUserRun(jsonx.NewOrderedMap(), ws, nil, []string{"true"}, "", "",
			macosuser.HomeOverlay{}, macosuser.HostContext{}, false, jsonx.NewOrderedMap(), nil,
			macosuser.JailDaemons{})
	})
	if rc != 1 {
		t.Fatalf("the live handler returned %d on Linux, want its precondition refusal", rc)
	}
	ended := false
	for _, e := range events {
		if e.Kind == perf.KindEnd && e.Name == "macos_user.preconditions" {
			ended = true
		}
	}
	if !ended {
		t.Errorf("the backend's first step was not spanned on the run's collector: %v", events)
	}
}
