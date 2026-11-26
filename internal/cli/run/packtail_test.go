package run

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/perf"
	"github.com/mschulkind-oss/yolo-jail/internal/progress"
)

// packtail_test.go pins that the launch's two slow pack steps feed the progress tail (the latest
// lines of what they are doing, under their progress line), at the CALL SITES: delete the Tail
// handed to Refresh in refreshPacks, or the one handed to the resolver in stagePackEntry, and the
// matching test goes red. The tail itself is internal/progress's.

// liveTailConfig is a live rendering a test drives: shown at Start, with a Tick that never fires
// (no wall-clock ticker) and no color.
func liveTailConfig(live bool) *progress.Config {
	return &progress.Config{Live: live, Immediate: true, Plain: true,
		Width: func() int { return 100 }, Tick: make(chan time.Time)}
}

func TestPackRefreshFeedsTheLaunchTail(t *testing.T) {
	home := hostRefreshEnv(t)
	repo := refreshGitRepo(t)
	writeUserPacks(t, home, `[{"name": "gp", "source": "git+file://`+repo+`?ref=main"}]`)

	var live bytes.Buffer
	o := &Options{Stderr: &live, Perf: perf.New(time.Now), Progress: liveTailConfig(true)}
	o.refreshPacks()
	if !strings.Contains(live.String(), "  checking ") {
		t.Errorf("the pack check showed no tail row of what the mirror was doing:\n%q", live.String())
	}

	// THE LINE-ORIENTED RENDERING PRINTS NO TAIL: a pipe or a log already holds what git said.
	var plain bytes.Buffer
	o = &Options{Stderr: &plain, Perf: perf.New(time.Now), Progress: liveTailConfig(false)}
	o.refreshPacks()
	if strings.Contains(plain.String(), "checking ") {
		t.Errorf("the line-oriented rendering carried the tail:\n%q", plain.String())
	}
}

func TestStagingPhaseFeedsTheLaunchTail(t *testing.T) {
	t.Cleanup(packload.ReleaseEmbedded)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "pack.json"), []byte(`{"name":"plain"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	entry := config.PackEntry{Source: "file://" + root, Name: "plain"}

	var live bytes.Buffer
	o := &Options{Stderr: &live, Perf: perf.New(time.Now), Progress: liveTailConfig(true)}
	ok := o.withPhase("launch.test_stage", "Staging packs", func() bool {
		_, err := o.stagePackEntry(entry, t.TempDir()+"/plain")
		return err == nil
	})
	if !ok {
		t.Fatal("staging the pack failed")
	}
	// The row is cut to the terminal's width, and a darwin TempDir is long, so match its head.
	if !strings.Contains(live.String(), "  resolving file://") {
		t.Errorf("the staging phase showed no tail row for the pack:\n%q", live.String())
	}
	if o.phaseLine != nil {
		t.Error("the phase's line outlived the phase")
	}

	// Outside a phase there is no line to tail: staging is what it was.
	var quiet bytes.Buffer
	o = &Options{Stderr: &quiet, Perf: perf.New(time.Now)}
	if _, err := o.stagePackEntry(entry, t.TempDir()+"/plain"); err != nil {
		t.Fatalf("stagePackEntry outside a phase: %v", err)
	}
	if strings.Contains(quiet.String(), "resolving") {
		t.Errorf("a staging with no phase line printed the tail:\n%q", quiet.String())
	}
}
