package run

// consumehandoff_test.go pins the one-time host→jail carry-in
// (docs/design/host-to-jail-handoff.md): a fresh .yolo/handover.md is rendered into the
// jail's briefing and the pointer is consumed, so the handoff surfaces on exactly one
// launch and never returns as a stale task.
//
// The load-bearing test here is the WIRE one — TestRefreshJailBriefingsCarriesHandoff —
// and it exists because the first version of this file was the shape AGENTS.md warns
// about: it exercised the helper directly and stayed green with the call site deleted, so
// the whole feature could be switched off without a red test. Measured: replacing
// `handoff := readHandoff(o.Workspace)` in refreshJailBriefings with `handoff := ""` left
// the package green. Everything below goes through refreshJailBriefings for that reason.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jailcontent"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// handoffResult is what one refreshJailBriefings call did to a workspace carrying a
// handoff: the briefing it composed (empty when it wrote none), whether the pointer
// survived, and what it told the user.
type handoffResult struct {
	briefing        string
	pointerSurvives bool
	consumedExists  bool
	stderr          string
}

// runRefreshWithHandoff files `handoff` at .yolo/handover.md, stages either a
// briefing-declaring pack or no packs at all, and runs the real refreshJailBriefings over
// it. Going through the run pipeline's own function — not readHandoff, not
// BriefingContent — is the whole point: this is the call site.
func runRefreshWithHandoff(t *testing.T, handoff string, withBriefingPack bool) handoffResult {
	t.Helper()
	home := packHome(t)
	ws := t.TempDir()
	yoloDir := filepath.Join(ws, ".yolo")
	if err := os.MkdirAll(yoloDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if handoff != "" {
		if err := os.WriteFile(filepath.Join(yoloDir, handoffPointer), []byte(handoff), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	packsJSON := "[]"
	if withBriefingPack {
		packDir := filepath.Join(t.TempDir(), "bp")
		if err := os.MkdirAll(packDir, 0o755); err != nil {
			t.Fatal(err)
		}
		writePack(t, packDir, `{"name":"bp","contributes":[`+
			`{"kind":"briefing","into":".claude/CLAUDE.md"}]}`)
		packsJSON = `[{"source":"file://` + packDir + `","name":"bp"}]`
	}
	writeUserPacks(t, home, packsJSON)

	jailcontent.SetPackSkillDirs(nil)
	jailcontent.SetPackSkillTargets(nil)
	t.Cleanup(func() { jailcontent.SetPackSkillDirs(nil); jailcontent.SetPackSkillTargets(nil) })

	var errBuf bytes.Buffer
	o := goldenOptions(ws, home)
	o.Stdout = discardBuf()
	o.Stderr = &errBuf

	const cname = "yolo-test-handoff"
	root, packs, briefings, err := o.stagePacks(cname)
	if err != nil {
		t.Fatalf("stagePacks: %v", err)
	}
	staging, err := o.refreshJailBriefings(cname, jsonx.NewOrderedMap(), "podman",
		stagedPacks{root: root, packs: packs, briefings: briefings})
	if err != nil {
		t.Fatalf("refreshJailBriefings: %v", err)
	}

	res := handoffResult{stderr: errBuf.String()}
	if data, err := os.ReadFile(filepath.Join(staging, briefingStagingName("bp"))); err == nil {
		res.briefing = string(data)
	}
	if _, err := os.Stat(filepath.Join(yoloDir, handoffPointer)); err == nil {
		res.pointerSurvives = true
	}
	if _, err := os.Stat(filepath.Join(yoloDir, handoffConsumed)); err == nil {
		res.consumedExists = true
	}
	return res
}

// The wire: a filed handoff reaches the briefing the jail will read, and the pointer is
// consumed. Deleting either half of the wiring in refreshJailBriefings — the readHandoff
// call or the BriefingInput.Handoff field — fails this.
func TestRefreshJailBriefingsCarriesHandoff(t *testing.T) {
	const task = "Task: wire the OAuth broker. Context: docs/handoff/oauth.md"
	got := runRefreshWithHandoff(t, task, true)

	if got.briefing == "" {
		t.Fatal("no briefing written for a pack that declares one")
	}
	for _, want := range []string{"## Handoff", "it is **the task**", task} {
		if !strings.Contains(got.briefing, want) {
			t.Errorf("briefing missing %q:\n%s", want, got.briefing)
		}
	}
	if got.pointerSurvives {
		t.Error("pointer should be consumed once its handoff reached a briefing")
	}
	if !got.consumedExists {
		t.Error("consumed marker missing — the rename is what makes the carry-in one-time")
	}
	// The burn is announced. Core cannot tell an agent launch from `yolo -- bash`, so the
	// only protection against a handoff consumed by a shell is that the human sees it go.
	if !strings.Contains(got.stderr, handoffConsumed) {
		t.Errorf("consuming a handoff must say so and name the restore path, got: %q", got.stderr)
	}
}

// A second launch finds nothing: the whole point of consuming. This is the stale-handoff
// bug the design was written for — a four-week-old handover read as the current task.
func TestRefreshJailBriefingsHandoffSurfacesOnce(t *testing.T) {
	got := runRefreshWithHandoff(t, "", true)
	if got.briefing == "" {
		t.Fatal("no briefing written for a pack that declares one")
	}
	if strings.Contains(got.briefing, "## Handoff") {
		t.Errorf("no pointer must yield no Handoff section:\n%s", got.briefing)
	}
	if got.consumedExists {
		t.Error("nothing to consume, yet a consumed marker appeared")
	}
	if got.stderr != "" {
		t.Errorf("no handoff should be silent, got: %q", got.stderr)
	}
}

// A jail with no briefing destination (no packs — run.go's warnIfNoPacks) renders the
// handoff NOWHERE, so it must not consume the pointer. Consuming here loses the task
// outright: the section was never written, and the next launch finds a consumed marker.
// The pre-fix code consumed unconditionally, ahead of the write loop.
func TestRefreshJailBriefingsKeepsHandoffWhenNothingCarriesIt(t *testing.T) {
	const task = "Task: wire the OAuth broker."
	got := runRefreshWithHandoff(t, task, false)

	if got.briefing != "" {
		t.Fatalf("no pack declares a briefing, yet one was written:\n%s", got.briefing)
	}
	if !got.pointerSurvives {
		t.Error("handoff consumed by a launch that wrote no briefing — the task is now lost")
	}
	if got.consumedExists {
		t.Error("consumed marker written for a handoff that reached no briefing")
	}
}

// readHandoff/consumeHandoff at the unit level: read does not consume, consume is
// one-shot, and both are quiet about an absent pointer.
func TestReadAndConsumeHandoff(t *testing.T) {
	ws := t.TempDir()
	dir := filepath.Join(ws, ".yolo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	const content = "Task: wire the broker. Context: docs/handoff/broker.md"
	if err := os.WriteFile(filepath.Join(dir, handoffPointer), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	// Reading twice is non-destructive — the split from consume is what lets the caller
	// wait until a briefing has actually carried it.
	for i := range 2 {
		if got := readHandoff(ws); got != content {
			t.Fatalf("read %d = %q, want %q", i, got, content)
		}
	}

	if !consumeHandoff(ws) {
		t.Fatal("consume reported no pointer, but one was filed")
	}
	if got := readHandoff(ws); got != "" {
		t.Errorf("read after consume = %q, want empty", got)
	}
	if consumeHandoff(ws) {
		t.Error("second consume reported success — nothing was there to consume")
	}

	// Absent from the start: empty, no marker.
	empty := t.TempDir()
	if got := readHandoff(empty); got != "" {
		t.Errorf("absent handoff = %q, want empty", got)
	}
	if consumeHandoff(empty) {
		t.Error("consume of an absent handoff reported success")
	}
}
