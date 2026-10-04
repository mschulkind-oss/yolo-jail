package entrypoint

// buildreceipt_test.go pins a fork build's receipt fields round trip (buildreceipt.go;
// docs/design/forked-programs-as-packs.md FP-D8, docs/design/patched-forks.md §6.3): a PATCHED
// build's fork, series, tree, tag and version are written and read back, and a plain build writes
// none of them, so a plain fork's line is what it was.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAPatchedBuildReceiptRoundTripsItsFields(t *testing.T) {
	p := filepath.Join(t.TempDir(), "receipts.jsonl")
	when := time.Date(2026, 10, 4, 1, 2, 3, 0, time.UTC)
	patched := BuildReceipt{Bin: "pi", Source: "git+https://example.com/up", Key: "k1", Digest: "d",
		Bytes: 10, Path: "/store/entries/k1", Platform: "linux/amd64", Revision: "c1", Recipe: "r1",
		Toolchain: "img", Fork: "pi-fork/pi", Series: "s1", Tree: "t1", Tag: "v1.0.1", Version: "1.0.1",
		Act: ReceiptActRecord, Time: when}
	plain := patched
	plain.Fork, plain.Series, plain.Tree, plain.Tag, plain.Version = "", "", "", "", ""
	for _, r := range []BuildReceipt{patched, plain} {
		if err := AppendReceiptLine(p, r.Line()); err != nil {
			t.Fatal(err)
		}
	}
	got, err := ReadBuildReceipts(p)
	if err != nil || len(got) != 2 {
		t.Fatalf("read %d receipts, err %v", len(got), err)
	}
	if g := got[0]; g.Fork != "pi-fork/pi" || g.Series != "s1" || g.Tree != "t1" || g.Tag != "v1.0.1" ||
		g.Version != "1.0.1" || g.Revision != "c1" || !g.Time.Equal(when) {
		t.Errorf("the patched receipt read back as %+v", g)
	}
	if g := got[1]; g.Fork != "" || g.Series != "" || g.Tree != "" || g.Tag != "" {
		t.Errorf("the plain receipt read back with patched fields: %+v", g)
	}
	data, _ := os.ReadFile(p)
	plainLine := strings.Split(strings.TrimSpace(string(data)), "\n")[1]
	for _, field := range []string{`"fork"`, `"series"`, `"tree"`, `"tag"`, `"version"`} {
		if strings.Contains(plainLine, field) {
			t.Errorf("a plain fork's receipt writes %s: %s", field, plainLine)
		}
	}
}
