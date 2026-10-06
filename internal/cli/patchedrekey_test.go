package cli

// patchedrekey_test.go pins the RE-KEY of a good build recorded under its series' LEGACY digest
// (docs/design/patched-forks.md PF-D62, run/patchedrekey.go): the digest of the same files before
// PF-D61 left git's mbox first line and signature out of it. Such a build is a build of the series as
// it stands, so every reader of the record keeps it serving and moves it to the digest as it stands —
// the advance, its recovery from the store, `yolo pack status`, `yolo pack update`, `yolo pack
// rebase` and the host floor's offline read — and none reads it as the user's own edit.

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/capture"
	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// legacyGood puts f's good build back in the state a yolo that predates PF-D61 left it in: its check
// record's good build and outcomes, and its store entry's receipts, name the series' legacy digest
// and the recipe that digest gives. It returns the series, the legacy recipe and the recipe as it
// stands.
func legacyGood(t *testing.T, f packload.Fork) (*packsrc.Series, string, string) {
	t.Helper()
	s, err := f.ReadSeries()
	if err != nil {
		t.Fatal(err)
	}
	oldRecipe, newRecipe := run.PatchedRecipe(f, s.LegacyDigest), run.PatchedRecipe(f, s.Digest)
	if s.LegacyDigest == s.Digest || oldRecipe == newRecipe {
		t.Fatal("the series' legacy digest is its digest, so there is nothing to re-key")
	}
	var entry string
	err = (&packsrc.Store{Dir: paths.PacksDir()}).WithCheckRecord(f.Key(), nil,
		func(r *packsrc.CheckRecord, _ error, _ func() error) (bool, error) {
			if r.Good == nil || r.Good.Recipe != newRecipe {
				t.Fatalf("the good build %+v is not a build of the series as it stands", r.Good)
			}
			r.Good.Series, r.Good.Recipe, entry = s.LegacyDigest, oldRecipe, r.Good.Entry
			for i := range r.Outcomes {
				if r.Outcomes[i].Series == s.Digest {
					r.Outcomes[i].Series = s.LegacyDigest
				}
			}
			return true, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	e, err := (&capture.Store{Dir: paths.CapturesDir()}).Resolve(entry)
	if err != nil {
		t.Fatal(err)
	}
	p := capture.ReceiptsPath(e.Root)
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	legacy := strings.ReplaceAll(strings.ReplaceAll(string(data), newRecipe, oldRecipe), s.Digest, s.LegacyDigest)
	if legacy == string(data) {
		t.Fatal("the good build's receipt names neither the recipe nor the digest")
	}
	writeFile(t, p, legacy)
	return s, oldRecipe, newRecipe
}

// receiptsUnder counts the build receipts of entry key that name recipe.
func receiptsUnder(t *testing.T, key, recipe string) int {
	t.Helper()
	e, err := (&capture.Store{Dir: paths.CapturesDir()}).Resolve(key)
	if err != nil {
		t.Fatal(err)
	}
	recs, err := entrypoint.ReadBuildReceipts(capture.ReceiptsPath(e.Root))
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, r := range recs {
		if r.Recipe == recipe {
			n++
		}
	}
	return n
}

// assertReKeyed fails unless f's record names its good build, still entry key, under the digest and
// the recipe as they stand.
func assertReKeyed(t *testing.T, f packload.Fork, s *packsrc.Series, key, newRecipe string) {
	t.Helper()
	g := patchedRecordOf(t, f.Key()).Good
	if g == nil || g.Series != s.Digest || g.Recipe != newRecipe || g.Entry != key {
		t.Errorf("the good build after the read = %+v, want entry %s under the digest %s as it stands", g, key,
			s.ShortDigest())
	}
}

// THE ADVANCE KEEPS IT SERVING: a launch after the upgrade, inside the hour, builds nothing, hands
// the same build, says no edit, and leaves the record and the entry under the digest as it stands;
// a receipt names the entry under the new recipe once, however many launches read it.
func TestALegacyDigestGoodBuildServesWithNoRebuild(t *testing.T) {
	fx, _, _, r, _, _ := firstAdvance(t)
	f := fx.fork(t)
	s, oldRecipe, newRecipe := legacyGood(t, f)
	fx.later(10 * time.Minute)
	again, out, handed := fx.launch(t, "podman")
	if len(fx.builds) != 1 || again.delivery.Key != r.delivery.Key || len(handed) != 1 || handed[0].Key != r.delivery.Key {
		t.Fatalf("the launch after the upgrade handed %+v after %d builds, want the same build and no new one\n%s",
			again.delivery, len(fx.builds), out)
	}
	if strings.Contains(out, "changed since the good build") {
		t.Errorf("the launch read the legacy digest as an edit:\n%s", out)
	}
	assertReKeyed(t, f, s, r.delivery.Key, newRecipe)
	fx.launch(t, "podman")
	if n, old := receiptsUnder(t, r.delivery.Key, newRecipe), receiptsUnder(t, r.delivery.Key, oldRecipe); n != 1 || old != 1 {
		t.Errorf("the entry carries %d receipts under the new recipe and %d under the old, want one each", n, old)
	}
}

// A LOST RECORD IS RECOVERED FROM A RECEIPT UNDER THE LEGACY RECIPE, and re-keyed: no first advance.
func TestALostRecordRecoversALegacyDigestBuild(t *testing.T) {
	fx, _, _, r, _, _ := firstAdvance(t)
	f := fx.fork(t)
	s, _, newRecipe := legacyGood(t, f)
	if err := os.Remove((&packsrc.Store{Dir: paths.PacksDir()}).CheckRecordPath(f.Key())); err != nil {
		t.Fatal(err)
	}
	again, out, _ := fx.launch(t, "podman")
	if len(fx.builds) != 1 || again.delivery.Key != r.delivery.Key || !strings.Contains(out, "recovered its good build") {
		t.Fatalf("the launch with no record handed %+v after %d builds, want the legacy build recovered\n%s",
			again.delivery, len(fx.builds), out)
	}
	assertReKeyed(t, f, s, r.delivery.Key, newRecipe)
}

// `yolo pack status` names the good build under the series' digest as it stands, and `yolo pack
// update` leaves the record re-keyed.
func TestPackStatusAndUpdateReKeyALegacyDigestGoodBuild(t *testing.T) {
	for _, verb := range []string{"status", "update"} {
		t.Run(verb, func(t *testing.T) {
			fx, _, _, r, _, _ := firstAdvance(t)
			f := fx.fork(t)
			s, _, newRecipe := legacyGood(t, f)
			_, out, errw := packVerb(t, verb)
			if verb == "status" && !strings.Contains(out, "+ 2 patches (series "+s.ShortDigest()+"), built") {
				t.Errorf("status does not name the good build under the digest as it stands (%s):\n%s\n%s",
					s.ShortDigest(), out, errw)
			}
			assertReKeyed(t, f, s, r.delivery.Key, newRecipe)
		})
	}
}

// `yolo pack rebase --onto` the good build's own commit says the good build already runs the series:
// the legacy digest is no edit there either.
func TestPackRebaseOntoALegacyDigestGoodBuildSaysItRunsTheSeries(t *testing.T) {
	fx, _, _, r, _, _ := firstAdvance(t)
	f := fx.fork(t)
	s, _, newRecipe := legacyGood(t, f)
	rc, out, errw := rebaseVerb(t, "forkpack/tool", "--onto", "v1.1.0", "--into", filepath.Join(t.TempDir(), "clone"))
	if rc != 0 || !strings.Contains(out, "the good build already runs it") {
		t.Errorf("rebase onto the good build's commit rc=%d, want it named as running the series:\n%s\n%s", rc, out, errw)
	}
	assertReKeyed(t, f, s, r.delivery.Key, newRecipe)
}

// THE HOST FLOOR'S OFFLINE READ serves it too, with its store entry, where no advance runs first.
func TestTheFloorsReadServesALegacyDigestGoodBuild(t *testing.T) {
	fx := patchedFloorFixture(t)
	fx.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	progs := floorPrograms(selectConfiguredHostPacks().packs)
	p, ok := floorProgram(progs, "tool")
	if !ok {
		t.Fatalf("the selection's floor has no patched tool: %+v", progs)
	}
	floor := productionHostFloor(io.Discard, progs)
	built := floor.Advance(context.Background(), p, nil)
	if built.Good == nil || built.Good.Entry == nil {
		t.Fatalf("the floor's advance built nothing: %+v", built)
	}
	f := fx.fork(t)
	s, _, newRecipe := legacyGood(t, f)
	ps := floor.Patched(p)
	if ps.Good == nil || ps.Good.Entry == nil || ps.Good.Entry.Key != built.Good.Entry.Key || ps.Good.Recipe != ps.Recipe {
		t.Fatalf("the floor's read of a legacy-digest good build = %+v (reason %q), want it serving with its entry",
			ps.Good, ps.Reason)
	}
	assertReKeyed(t, f, s, built.Good.Entry.Key, newRecipe)
}

// THE HOST FLOOR'S OFFLINE READ WITH NO RECORD finds a build receipted under the series' legacy
// digest, as the advance's own recovery does, and serves it with its entry; it writes nothing, so
// the receipt still names the legacy recipe alone until an advance re-keys it.
func TestTheFloorsReadWithNoRecordFindsALegacyDigestBuild(t *testing.T) {
	fx := patchedFloorFixture(t)
	fx.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	progs := floorPrograms(selectConfiguredHostPacks().packs)
	p, ok := floorProgram(progs, "tool")
	if !ok {
		t.Fatalf("the selection's floor has no patched tool: %+v", progs)
	}
	floor := productionHostFloor(io.Discard, progs)
	built := floor.Advance(context.Background(), p, nil)
	if built.Good == nil || built.Good.Entry == nil {
		t.Fatalf("the floor's advance built nothing: %+v", built)
	}
	f := fx.fork(t)
	_, oldRecipe, newRecipe := legacyGood(t, f)
	if err := os.Remove((&packsrc.Store{Dir: paths.PacksDir()}).CheckRecordPath(f.Key())); err != nil {
		t.Fatal(err)
	}
	ps := floor.Patched(p)
	if ps.Good == nil || ps.Good.Entry == nil || ps.Good.Entry.Key != built.Good.Entry.Key || ps.Good.Recipe != ps.Recipe {
		t.Fatalf("the floor's offline read with no record = %+v (reason %q), want the legacy build serving", ps.Good,
			ps.Reason)
	}
	if n := receiptsUnder(t, built.Good.Entry.Key, newRecipe); n != 0 {
		t.Errorf("the floor's offline read wrote %d receipts under the recipe as it stands, want none", n)
	}
	if n := receiptsUnder(t, built.Good.Entry.Key, oldRecipe); n == 0 {
		t.Errorf("the legacy receipt is gone")
	}
}
