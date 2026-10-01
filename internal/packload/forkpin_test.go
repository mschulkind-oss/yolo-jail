package packload

// forkpin_test.go pins LoadForkPins, the one reader of the fork lock FILE that a launch, `yolo
// capture <forked bin>` and the host floor share (forked-programs-as-packs.md FP-D7): what each
// fork's pin is, and that a lock which cannot be read pins nothing for anyone.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
)

func TestLoadForkPinsReadsEachForksPinAndABrokenLockPinsNothing(t *testing.T) {
	pinned := Fork{Pack: "forkpack", Base: "base", Bin: "tool", Source: "git+https://example.invalid/tool?ref=main"}
	unpinned := Fork{Pack: "forkpack", Base: "base", Bin: "other", Source: "git+https://example.invalid/other?ref=main"}
	forks := []Fork{pinned, unpinned}
	path := filepath.Join(t.TempDir(), "forks.lock.json")
	commit := strings.Repeat("a", 40)

	if pins := LoadForkPins(nil, path); pins != nil {
		t.Errorf("no forks: %+v, want nil", pins)
	}
	// No lock file yet: every fork is unpinned, sent to `yolo pack install`.
	for _, p := range LoadForkPins(forks, path) {
		if p.Commit != "" || !strings.Contains(p.Reason, "run `yolo pack install`") {
			t.Errorf("no lock file: %+v", p)
		}
	}

	l := &packsrc.ForkLock{}
	l.Set(packsrc.ForkLockEntry{Key: pinned.Key(), Source: pinned.Source, Ref: "main", Commit: commit})
	if err := l.Save(path); err != nil {
		t.Fatal(err)
	}
	pins := LoadForkPins(forks, path)
	if len(pins) != 2 || pins[0].Commit != commit || pins[0].Ref != "main" || pins[0].Reason != "" ||
		pins[1].Commit != "" || pins[1].Reason == "" {
		t.Fatalf("a lock pinning one of two forks: %+v", pins)
	}

	// A LOCK THAT CANNOT BE READ PINS NOTHING, even the fork it named, and says why for each.
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	pins = LoadForkPins(forks, path)
	if len(pins) != 2 {
		t.Fatalf("a broken lock: %+v, want one answer per fork", pins)
	}
	for i, p := range pins {
		if p.Fork.Key() != forks[i].Key() || p.Commit != "" ||
			!strings.HasPrefix(p.Reason, "the fork lock cannot be read (") || !strings.Contains(p.Reason, path) {
			t.Errorf("a broken lock: %+v, want no pin and the read error as its reason", p)
		}
	}
}
