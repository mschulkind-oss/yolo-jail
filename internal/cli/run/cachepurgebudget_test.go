package run

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// TestACachePassThatOverrunsLeavesItsClassDue is CI-D7 on the launch path: a cache pass that hits
// its budget says its figure is partial, deletes nothing past the budget, and writes no
// completion stamp — the next launch's slot retries rather than waiting out a day on a pass that
// never finished. The same pass within its budget stamps, as before.
func TestACachePassThatOverrunsLeavesItsClassDue(t *testing.T) {
	for _, overrun := range []bool{false, true} {
		home := t.TempDir()
		t.Setenv("HOME", home)
		o := goldenOptions(t.TempDir(), home)
		o.Now = time.Now
		old := filepath.Join(paths.GlobalStorage(), "cache", "uv", "old")
		if err := os.MkdirAll(filepath.Dir(old), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(old, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		past := time.Now().Add(-60 * 24 * time.Hour)
		_ = os.Chtimes(old, past, past)

		orig := cacheWalkBudget
		if overrun {
			cacheWalkBudget = -time.Second // every deadline is already past
		}
		var c reclaimConsent
		c.grant(cachePurgeClass, true)
		o.measureAndPurgeCache(c, nil)
		cacheWalkBudget = orig

		_, err := os.Stat(old)
		if gone := os.IsNotExist(err); gone == overrun {
			t.Errorf("overrun=%v: removed=%v", overrun, gone)
		}
		if m := LastOfferMeasurement(cachePurgeClass); m.Partial != overrun {
			t.Errorf("overrun=%v: measurement partial=%v", overrun, m.Partial)
		}
		if due, _ := o.classDebounce("cache"); due != overrun {
			t.Errorf("overrun=%v: still due=%v; only a completed pass may stamp the debounce", overrun, due)
		}
	}
}
