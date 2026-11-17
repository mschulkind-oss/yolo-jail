package run

import (
	"bytes"
	"fmt"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrepareFreshSourcedPayloadFailureNamesEverySelectedRowAndRemedy(t *testing.T) {
	packHome(t)
	source := "git+file://" + filepath.Join(t.TempDir(), "absent-origin") + "//skills/chosen?ref=main"
	var packs []*packload.Pack
	for _, name := range []string{"first", "second"} {
		dir := t.TempDir()
		writePack(t, dir, fmt.Sprintf(`{"contributes":[{"kind":"skills","source":%q,"as":%q}]}`, source, name))
		p, problems := packload.LoadDir(dir, name)
		if len(problems) != 0 {
			t.Fatal(problems)
		}
		packs = append(packs, p)
	}
	var output bytes.Buffer
	o := &Options{Stderr: &output}
	if o.prepareFreshSkillPayloads("yolo-sourced-unreachable", "podman", stagedPacks{root: t.TempDir(), packs: packs}) {
		t.Fatal("unreachable source accepted")
	}
	text := output.String()
	for _, want := range []string{"pack first skills[0]", "pack second skills[0]", "yolo pack install"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q:\n%s", want, text)
		}
	}
}
