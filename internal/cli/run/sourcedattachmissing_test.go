package run

import (
	"fmt"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunningJailPackViewWithoutRecordedTreeDoesNotAdoptHostSourcedPayload(t *testing.T) {
	packHome(t)
	dir := t.TempDir()
	source := "git+file://" + filepath.Join(t.TempDir(), "origin") + "//skills/chosen?ref=main"
	writePack(t, dir, fmt.Sprintf(`{"contributes":[{"kind":"skills","source":%q,"as":"chosen"},{"kind":"skills","agent":"fixture","into":".fixture/skills"}]}`, source))
	p, problems := packload.LoadDir(dir, "selected")
	if len(problems) != 0 {
		t.Fatal(problems)
	}
	a, _ := packsrc.Parse(source)
	packload.BindSelectedSkillPayloads([]*packload.Pack{p}, []packsrc.SkillPayloadResult{{Repo: a.Repo, Ref: a.Ref, State: packsrc.SkillReady, Payload: "/host/skill-cache/payload", Plan: &packsrc.SkillPayloadPlan{Rows: []packsrc.SkillRowPlan{{Request: packsrc.SkillRequest{Label: "pack selected skills[0]", Source: source}, Path: "skills/chosen", Marker: []byte("chosen")}}}}})
	if p.SourcedSkillRows()[0].Outcome.State != packsrc.SkillReady {
		t.Fatal("fixture not READY")
	}
	o := &Options{}
	view, skew := o.runningJailPackView("yolo-missing-sourced-tree", "podman", jsonx.NewOrderedMap(), stagedPacks{root: dir, packs: []*packload.Pack{p}}, nil, "true")
	if skew == nil || !view.unreadable {
		t.Fatalf("missing recorded tree adopted host registrations: skew=%+v rows=%+v", skew, view.staged.packs)
	}
	if !strings.Contains(skew.err.Error(), "restart") || !strings.Contains(skew.err.Error(), "selected") {
		t.Fatalf("missing row/remedy: %v", skew.err)
	}
}
