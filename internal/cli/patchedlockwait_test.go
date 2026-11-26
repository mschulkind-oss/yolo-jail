package cli

// patchedlockwait_test.go pins a launch whose 5-second deadline ends while the check waits on the mirror lock
// (docs/design/local-pack-clones.md §4.3, LPC-D6): nothing was asked of the upstream, so the record keeps its
// prior list, the good build runs, and the warning says why.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
)

func TestALockWaitTimeoutKeepsThePriorListAndRunsTheGoodBuild(t *testing.T) {
	fx, _, _, first, _, _ := firstAdvance(t)
	before := fx.record(t)
	locks := filepath.Join(patchedAdvanceStore(true).Dir, "locks")
	entries, err := os.ReadDir(locks)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "check-") {
			continue // the record locks: only the mirror's is held
		}
		f, err := os.OpenFile(filepath.Join(locks, e.Name()), os.O_RDWR, 0)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
			t.Fatal(err)
		}
	}
	prev := patchedCheckStart
	patchedCheckStart = func() (*packsrc.PackCheck, context.CancelFunc) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel() // the deadline fires while the check waits on the held lock
		return &packsrc.PackCheck{Ctx: ctx, Limit: packsrc.PackCheckDeadline}, cancel
	}
	t.Cleanup(func() { patchedCheckStart = prev })
	fx.later(10 * time.Minute)
	r, out, _ := fx.launch(t, "podman")
	if r.delivery.Key != first.delivery.Key || len(fx.builds) != 1 {
		t.Fatalf("a lock-wait timeout handed %+v after %d builds, want the good build\n%s", r.delivery, len(fx.builds), out)
	}
	for _, want := range []string{"could not check origin main (waiting for the mirror lock timed out after 5s) — still running v1.1.0",
		packsrc.LockWaitNext} {
		if !strings.Contains(out, want) {
			t.Errorf("the warning lacks %q:\n%s", want, out)
		}
	}
	after := fx.record(t)
	if after.Seq != before.Seq || after.Check == nil || after.Check.FetchErr != "" || len(after.Check.List) != len(before.Check.List) {
		t.Errorf("the record after a lock-wait timeout = %+v, want the prior check untouched (before %+v)", after.Check, before.Check)
	}
}
