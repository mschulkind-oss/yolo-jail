package entrypoint

// packskewdisclosed_test.go pins the boot's half of one skipped contribution being ONE line
// (docs/design/patched-forks.md PF-D68): the host's use read skips what the boot's tolerant read
// skips, the launch prints it and records it in the tree (packload.PackTreeEntry.Skipped), and the
// boot logs a recorded note to boot.log instead of printing it a second time. A note the launch
// did not record — an entrypoint of another build finding what the host could read — still warns.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// stageFieldSkewTree writes a pack root holding the pack acme, whose first contribution has a field
// no yolo knows, and — when recorded — the tree record a launch writes after its use read.
func stageFieldSkewTree(t *testing.T, recorded bool) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "packs")
	dir := filepath.Join(root, "acme")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name":"acme","contributes":[
		{"kind":"skills","from":"skills","into":".acme/skills","field_from_a_newer_yolo":1},
		{"kind":"env","vars":{"ACME":"1"}}]}`
	if err := os.WriteFile(filepath.Join(dir, packdecl.ManifestName), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if recorded {
		// The launch's own read and record, as run's stagePacks makes them.
		p, problems := packload.LoadDirForUse(dir, "acme")
		if len(problems) > 0 || len(p.SkewNotes) != 1 {
			t.Fatalf("the host's use read: problems=%v notes=%v", problems, p.SkewNotes)
		}
		if err := packload.WritePackTreeRecord(root, []*packload.Pack{p}); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestTheBootLogsASkipTheLaunchAlreadyPrinted(t *testing.T) {
	for _, recorded := range []bool{true, false} {
		root := stageFieldSkewTree(t, recorded)
		var stderr, log bytes.Buffer
		e := NewEnv(map[string]string{"JAIL_HOME": t.TempDir(), "YOLO_PACK_ROOT": root})
		e.Stderr, e.LogOnly = &stderr, &log
		packs, err := LoadJailPacks(e)
		if err != nil {
			t.Fatalf("recorded=%v: a field from a newer yolo must not fail the boot: %v", recorded, err)
		}
		if len(packs) != 1 || packs[0].Decl.EnvContributions()["ACME"] != "1" {
			t.Fatalf("recorded=%v: the readable sibling must survive: %+v", recorded, packs)
		}
		for _, c := range packs[0].Decl.Contributions() {
			if c.Kind == packdecl.KindSkills {
				t.Errorf("recorded=%v: the boot rendered the contribution the launch said it skipped", recorded)
			}
		}
		const field = `"field_from_a_newer_yolo"`
		onTerminal, inLog := strings.Contains(stderr.String(), field), strings.Contains(log.String(), field)
		if recorded && (onTerminal || !inLog) {
			t.Errorf("a skip the launch printed goes to boot.log only: terminal=%q log=%q",
				stderr.String(), log.String())
		}
		if !recorded && !onTerminal {
			t.Errorf("a skip the launch did not print must warn on the terminal: %q", stderr.String())
		}
	}
}
