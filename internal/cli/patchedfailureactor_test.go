package cli

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

func patchedActorLaunch(t *testing.T) (*patchedAdvanceFixture, string) {
	t.Helper()
	fx := newPatchedAdvanceFixture(t, "")
	fx.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	var out bytes.Buffer
	delivered := buildForksForLaunch(forkLaunchRequest(&run.ActInterrupt{}), &out, &out, false)
	if delivered["tool"].Key == "" {
		t.Fatalf("clean actual BuildForks launch did not deliver a build: %+v\n%s", delivered["tool"], out.String())
	}
	return fx, delivered["tool"].Key
}

func TestPatchActorSnapshotDoesNotAdoptLaterCheck(t *testing.T) {
	t.Setenv("YOLO_ALLOW_PATCH_FAILURES", "")
	fx, oldKey := patchedActorLaunch(t)
	fx.commit(t, "v1.2.0", map[int]string{14: "fourteen", 11: "eleven"})
	fx.later(2 * time.Hour)
	store := patchedAdvanceStore(true)
	newer := fx.record(t)
	if newer.Check == nil || len(newer.Check.List) == 0 {
		t.Fatal("initial record has no selected check list")
	}
	newer.Seq += 10
	newer.Check.Seq = newer.Seq
	newer.PatchFailure = &packsrc.PatchFailure{Owner: newer.Owner, Inputs: newer.Read, Series: mustSeries(t, fx).Digest,
		Target: newer.Check.List[0], Kind: "conflict", Member: "newer-authority.patch", Paths: []string{"newer.txt"}, Seq: newer.Seq}
	data, err := json.Marshal(newer)
	if err != nil {
		t.Fatal(err)
	}
	prepared := filepath.Join(t.TempDir(), "newer-check.json")
	if err := os.WriteFile(prepared, data, 0o600); err != nil {
		t.Fatal(err)
	}
	extra := "for a in \"$@\"; do if [ \"$a\" = merge-tree ]; then cp " + shellQuote(prepared) + " " +
		shellQuote(store.CheckRecordPath(newer.Owner)) + "; break; fi; done"
	patchedGitWrapper(t, extra)
	result, out, _ := fx.launch(t, "podman")
	if result.delivery.Key != oldKey || result.patchFailure == nil || result.patchFailure.Member != "newer-authority.patch" || len(fx.builds) != 1 {
		t.Fatalf("old replay adopted or built after the later check authority: %+v, builds=%d\n%s", result, len(fx.builds), out)
	}
	if current := fx.record(t); current.Seq != newer.Seq || current.PatchFailure == nil || current.PatchFailure.Member != "newer-authority.patch" {
		t.Fatalf("the newer check authority was overwritten: %+v", current)
	}
}

func TestPatchActorBuildForksRetainsFailureWithGood(t *testing.T) {
	t.Setenv("YOLO_ALLOW_PATCH_FAILURES", "")
	fx, oldKey := patchedActorLaunch(t)
	good := fx.record(t).Good
	fx.commit(t, "v1.2.0", map[int]string{14: "fourteen", 11: "eleven"})
	fx.later(2 * time.Hour)
	var out bytes.Buffer
	delivered := buildForksForLaunch(forkLaunchRequest(&run.ActInterrupt{}), &out, &out, false)
	got := delivered["tool"]
	if got.Key != oldKey || got.PatchFailure == nil || len(fx.builds) != 1 {
		t.Fatalf("actual BuildForks lost failure or moved its good build: %+v, builds=%d\n%s", got, len(fx.builds), out.String())
	}
	if current := fx.record(t).Good; current == nil || current.Entry != good.Entry {
		t.Fatalf("the refusal moved Good: before=%+v after=%+v", good, current)
	}
}

func patchedRecordLockPath(owner string) string {
	sum := sha256.Sum256([]byte(owner))
	tail := owner
	if i := strings.LastIndexAny(tail, "/:"); i >= 0 {
		tail = tail[i+1:]
	}
	return filepath.Join(paths.PacksDir(), "locks", "check-"+tail+"-"+hex.EncodeToString(sum[:6])+".lock")
}

func TestPatchActorBuildForksRetainsFailureOnRecordError(t *testing.T) {
	t.Setenv("YOLO_ALLOW_PATCH_FAILURES", "")
	fx, oldKey := patchedActorLaunch(t)
	fx.commit(t, "v1.2.0", map[int]string{14: "fourteen", 11: "eleven"})
	fx.later(2 * time.Hour)
	lock := patchedRecordLockPath("forkpack/tool")
	extra := "for a in \"$@\"; do if [ \"$a\" = merge-tree ]; then rm -f " + shellQuote(lock) + "; mkdir -p " + shellQuote(lock) + "; break; fi; done"
	patchedGitWrapper(t, extra)
	var out bytes.Buffer
	delivered := buildForksForLaunch(forkLaunchRequest(&run.ActInterrupt{}), &out, &out, false)
	got := delivered["tool"]
	if got.Key != oldKey || got.PatchFailure == nil || !strings.Contains(got.PatchFailure.Member, "0001-ten.patch") {
		t.Fatalf("record error erased the current operation's typed evidence: %+v\n%s", got, out.String())
	}
	if len(fx.builds) != 1 {
		t.Fatalf("record error allowed another build: %d\n%s", len(fx.builds), out.String())
	}
}

func TestPatchActorLegacyConflictBeforeHoldAndGoodCut(t *testing.T) {
	t.Setenv("YOLO_ALLOW_PATCH_FAILURES", "")
	fx, oldKey := patchedActorLaunch(t)
	good := fx.record(t).Good
	series, err := fx.fork(t).ReadSeries()
	if err != nil {
		t.Fatal(err)
	}
	fx.writeUserConfig(t, `,"agent_updates":{"forkpack":false}`)
	store := patchedAdvanceStore(true)
	if err := store.WithCheckRecord("forkpack/tool", nil, func(r *packsrc.CheckRecord, readErr error, _ func() error) (bool, error) {
		if readErr != nil {
			return false, readErr
		}
		r.PatchFailure = nil
		r.SetOutcome(packsrc.EntryOutcome{Commit: good.Commit, Kind: packsrc.OutcomeConflict, Series: series.Digest,
			Member: "0001-ten.patch", Paths: []string{"f.txt"}})
		return true, nil
	}); err != nil {
		t.Fatal(err)
	}
	patchedGitWrapper(t, `echo git-was-called >&2; exit 99`)
	var out bytes.Buffer
	delivered := buildForksForLaunch(forkLaunchRequest(&run.ActInterrupt{}), &out, &out, false)
	got := delivered["tool"]
	if got.Key != oldKey || got.PatchFailure == nil || got.PatchFailure.Target.Commit != good.Commit {
		t.Fatalf("legacy selected conflict was not synthesized before Good/pending serving: %+v\n%s", got, out.String())
	}
	if len(fx.builds) != 1 || strings.Contains(out.String(), "git-was-called") {
		t.Fatalf("held cached conflict ran Git or rebuilt: builds=%d\n%s", len(fx.builds), out.String())
	}
}

func TestPatchActorSecondReplayFailureNeverFallsBack(t *testing.T) {
	t.Setenv("YOLO_ALLOW_PATCH_FAILURES", "")
	fx := newPatchedAdvanceFixture(t, "")
	fx.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	count := filepath.Join(t.TempDir(), "merge-tree-count")
	lock := patchedRecordLockPath("forkpack/tool")
	extra := "is_merge=0; for a in \"$@\"; do [ \"$a\" = merge-tree ] && is_merge=1; done; " +
		"if [ \"$is_merge\" -eq 1 ]; then n=0; [ ! -f " + shellQuote(count) + " ] || n=$(cat " + shellQuote(count) + "); " +
		"n=$((n+1)); echo $n > " + shellQuote(count) + "; if [ \"$n\" -eq 3 ]; then rm -f " + shellQuote(lock) + "; mkdir -p " + shellQuote(lock) + "; " +
		"printf 'second replay first line\\nsecond replay second line\\n' >&2; exit 2; fi; fi"
	patchedGitWrapper(t, extra)
	var out bytes.Buffer
	delivered := buildForksForLaunch(forkLaunchRequest(&run.ActInterrupt{}), &out, &out, false)
	got := delivered["tool"]
	if got.PatchFailure == nil || got.PatchFailure.Kind != "application-command" ||
		!strings.Contains(got.PatchFailure.Detail, "second replay first line") || !strings.Contains(got.PatchFailure.Detail, "second replay second line") {
		t.Fatalf("second replay lost typed application failure or diagnostics: %+v\n%s", got.PatchFailure, out.String())
	}
	if len(fx.builds) != 0 || fx.record(t).Good != nil {
		t.Fatalf("second replay failure built a fallback or moved Good: builds=%d Good=%+v\n%s", len(fx.builds), fx.record(t).Good, out.String())
	}
	var asFailure *packsrc.PatchFailure
	if !errors.As(got.PatchFailure, &asFailure) {
		t.Fatalf("delivery failure is not the classified application-command type: %T", got.PatchFailure)
	}
}

func TestPatchActorOpaqueBeforeNextLaunchDelivery(t *testing.T) {
	t.Setenv("YOLO_ALLOW_PATCH_FAILURES", "")
	fx := newTreeFixture(t, `"f.txt"`)
	prevTiming := treeUpdateTiming
	treeUpdateTiming = func(packload.Fork) updateTiming { return timingNextLaunch }
	t.Cleanup(func() { treeUpdateTiming = prevTiming })
	first, _ := fx.deliver(t, true)
	if first.Dir == "" {
		t.Fatalf("initial tree was not built: %+v", first)
	}
	fx.commit(t, "v1.2.0", map[int]string{14: "fourteen", 11: "eleven"})
	fx.now = fx.now.Add(2 * time.Hour)
	store := patchedAdvanceStore(true)
	series, err := fx.tree(t).ReadSeries()
	if err != nil {
		t.Fatal(err)
	}
	checked := store.CheckPatched(fx.tree(t).CheckWant(series), packsrc.CheckOptions{Force: true, Now: func() time.Time { return fx.now }})
	if checked.Err != nil || checked.Record == nil || checked.Record.Check == nil {
		t.Fatalf("prepare the actual selected check before installing its old opaque diagnosis: %+v", checked)
	}
	if err := store.WithCheckRecord(treeKeyCLI, nil, func(r *packsrc.CheckRecord, readErr error, _ func() error) (bool, error) {
		if readErr != nil {
			return false, readErr
		}
		r.ApplyErr = &packsrc.ApplyError{Seq: r.Seq, Error: "legacy opaque apply error"}
		return true, nil
	}); err != nil {
		t.Fatal(err)
	}
	beforeBuilds := len(fx.builds)
	got, out := fx.deliver(t, true)
	if got.Dir == "" || got.PatchFailure == nil || got.PatchFailure.Target.Tag != "v1.2.0" || len(fx.builds) != beforeBuilds {
		t.Fatalf("supported no-check delivery failed to classify opaque state before Good/fallback: %+v, builds=%d/%d\n%s",
			got, len(fx.builds), beforeBuilds, out)
	}
}

func TestPatchActorForkWireRemainsCompatible(t *testing.T) {
	failure := &packsrc.PatchFailure{Owner: "forkpack/tool", Kind: "conflict", Target: packsrc.ListEntry{Tag: "v1.2.0", Commit: strings.Repeat("a", 40)},
		Member: "0001-ten.patch", Paths: []string{"f.txt"}}
	wire := entrypoint.ForkBuildsWire(map[string]entrypoint.ForkDelivery{"tool": {Key: "kept-key", PatchFailure: failure}})
	if strings.Contains(wire, "patch_failure") || strings.Contains(wire, "0001-ten.patch") || !strings.Contains(wire, "kept-key") {
		t.Fatalf("host-only failure changed fork wire compatibility: %s", wire)
	}
}

func TestPatchActorUnsupportedTreeNoLegacyProbe(t *testing.T) {
	t.Setenv("YOLO_ALLOW_PATCH_FAILURES", "")
	fx := newTreeFixture(t, `"f.txt"`)
	first, _ := fx.deliver(t, true)
	if first.Dir == "" {
		t.Fatalf("initial tree was not built: %+v", first)
	}
	series, err := fx.tree(t).ReadSeries()
	if err != nil {
		t.Fatal(err)
	}
	store := patchedAdvanceStore(true)
	check := store.CheckPatched(fx.tree(t).CheckWant(series), packsrc.CheckOptions{Force: true, Now: func() time.Time { return fx.now.Add(2 * time.Hour) }})
	if check.Err != nil {
		t.Fatal(check.Err)
	}
	if err := store.WithCheckRecord(treeKeyCLI, nil, func(r *packsrc.CheckRecord, readErr error, _ func() error) (bool, error) {
		if readErr != nil {
			return false, readErr
		}
		r.ApplyErr = &packsrc.ApplyError{Seq: r.Seq, Error: "legacy opaque failure"}
		return true, nil
	}); err != nil {
		t.Fatal(err)
	}
	calls := filepath.Join(t.TempDir(), "git-called")
	patchedGitWrapper(t, "echo called > "+shellQuote(calls)+"; exit 99")
	got, _ := fx.deliver(t, false)
	if got.Dir == "" || got.PatchFailure != nil {
		t.Fatalf("unsupported tree launch probed or misclassified opaque legacy state: %+v", got)
	}
	if _, err := os.Stat(calls); err == nil {
		t.Fatal("unsupported no-build tree delivery ran a local replay")
	}
}
