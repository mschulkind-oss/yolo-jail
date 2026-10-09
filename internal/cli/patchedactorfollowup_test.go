package cli

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
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

func TestPatchActorStaleReplayReadFailureKeepsFailureChain(t *testing.T) {
	t.Setenv("YOLO_ALLOW_PATCH_FAILURES", "")
	fx, _ := patchedActorLaunch(t)
	fx.commit(t, "v1.2.0", map[int]string{14: "fourteen", 11: "eleven"})
	fx.later(2 * time.Hour)
	f := fx.fork(t)
	store := patchedAdvanceStore(true)
	series, err := f.ReadSeries()
	if err != nil {
		t.Fatal(err)
	}
	check := store.CheckPatched(f.CheckWant(series), packsrc.CheckOptions{Force: true, Now: func() time.Time { return fx.now }})
	if check.Err != nil || check.Record == nil || check.Record.Check == nil {
		t.Fatalf("prepare the selected check: %+v", check)
	}
	var output bytes.Buffer
	a, early := newAdvance(f, advanceOptions{platform: patchedTestPlatform, runtime: "podman", launch: true,
		out: &output, errw: &output})
	if early != nil {
		t.Fatal("could not initialize the replay actor")
	}
	a.seq = check.Record.Seq
	snapshot, err := a.replaySnapshot()
	if err != nil {
		t.Fatal(err)
	}
	walk := a.packs.WalkSeries(a.repo, a.subdir, a.series, check.Record.Check.List,
		packsrc.WalkOptions{Snapshot: &snapshot, StopOnPatchFailure: true})
	if walk.PatchFailure() == nil {
		t.Fatalf("fixture did not produce a typed replay failure: %+v", walk)
	}
	checkPath := store.CheckRecordPath(f.Key())
	if err := os.RemoveAll(checkPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(checkPath, 0o700); err != nil {
		t.Fatal(err)
	}

	result := a.recordReplay(snapshot, walk)
	if !errors.Is(result.Err, packsrc.ErrStaleReplay) {
		t.Errorf("fresh-read failure erased stale replay identity: %v", result.Err)
	}
	var failure *packsrc.PatchFailure
	if !errors.As(result.Err, &failure) || result.Failure == nil || a.patchFailure != nil {
		t.Errorf("fresh-read failure erased the original classified chain or promoted stale evidence: result=%+v actor-failure=%+v err=%v",
			result, a.patchFailure, result.Err)
	}
}

func TestPatchActorUnreadableStaleCleanWalkStopsBeforeBuild(t *testing.T) {
	t.Setenv("YOLO_ALLOW_PATCH_FAILURES", "")
	fx := newPatchedAdvanceFixture(t, "")
	fx.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	store := patchedAdvanceStore(true)
	checkPath := store.CheckRecordPath("forkpack/tool")
	countPath := filepath.Join(t.TempDir(), "merge-count")
	extra := "is_merge=0; for a in \"$@\"; do [ \"$a\" = merge-tree ] && is_merge=1; done; " +
		"if [ \"$is_merge\" -eq 1 ]; then n=0; [ ! -f " + shellQuote(countPath) + " ] || n=$(cat " + shellQuote(countPath) + "); " +
		"n=$((n+1)); echo $n > " + shellQuote(countPath) + "; if [ \"$n\" -eq 1 ]; then rm -rf " + shellQuote(checkPath) + "; mkdir -p " + shellQuote(checkPath) + "; fi; fi"
	patchedGitWrapper(t, extra)
	result, out, _ := fx.launch(t, "podman")
	count, _ := os.ReadFile(countPath)
	if len(fx.builds) != 0 || result.delivery.Key != "" || strings.TrimSpace(string(count)) != "2" {
		t.Fatalf("a clean stale replay used fit/base authority or built after its fresh record read failed: result=%+v builds=%d merge-tree=%s\n%s",
			result, len(fx.builds), strings.TrimSpace(string(count)), out)
	}
}

func TestPatchActorStaleSecondReplayReadFailureStopsSettlement(t *testing.T) {
	t.Setenv("YOLO_ALLOW_PATCH_FAILURES", "")
	fx := newPatchedAdvanceFixture(t, "")
	fx.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	f := fx.fork(t)
	store := patchedAdvanceStore(true)
	series, err := f.ReadSeries()
	if err != nil {
		t.Fatal(err)
	}
	check := store.CheckPatched(f.CheckWant(series), packsrc.CheckOptions{Force: true, Now: func() time.Time { return fx.now }})
	if check.Err != nil || check.Record == nil {
		t.Fatalf("prepare the selected check: %+v", check)
	}
	countPath := filepath.Join(t.TempDir(), "merge-count")
	extra := "is_merge=0; for a in \"$@\"; do [ \"$a\" = merge-tree ] && is_merge=1; done; " +
		"if [ \"$is_merge\" -eq 1 ]; then n=0; [ ! -f " + shellQuote(countPath) + " ] || n=$(cat " + shellQuote(countPath) + "); " +
		"n=$((n+1)); echo $n > " + shellQuote(countPath) + "; if [ \"$n\" -eq 3 ]; then rm -rf " + shellQuote(store.CheckRecordPath(f.Key())) + "; mkdir -p " + shellQuote(store.CheckRecordPath(f.Key())) + "; printf 'source replay operation failed\\n' >&2; exit 2; fi; fi"
	patchedGitWrapper(t, extra)
	var output bytes.Buffer
	a, early := newAdvance(f, advanceOptions{platform: patchedTestPlatform, runtime: "podman", launch: true,
		out: &output, errw: &output})
	if early != nil {
		t.Fatal("could not initialize the source replay actor")
	}
	a.seq = check.Record.Seq
	walk := a.walk(check.Record.Check.List, true)
	if walk.Fit < 0 || a.rec.Check == nil {
		t.Fatalf("first clean walk did not establish its selected fit: %+v", walk)
	}
	snapshot := a.replayBinding
	entry := a.rec.Check.List[0]
	b := forkBuild{Fork: f, Commit: entry.Commit, Platform: patchedTestPlatform, Series: a.series, Entry: entry}
	dst := filepath.Join(t.TempDir(), "source")
	bound := *snapshot
	_, sourceErr := replayIntoSource(a.packs, b, dst, 0, sourceReplayOptions{Snapshot: &bound,
		Record: func(w packsrc.WalkResult) packsrc.RecordReplayResult { return a.recordSourceReplay(bound, w) }})
	if !errors.Is(sourceErr, packsrc.ErrStaleReplay) {
		t.Errorf("source replay lost stale identity: %v", sourceErr)
	}
	var failure *packsrc.PatchFailure
	if !errors.As(sourceErr, &failure) {
		t.Errorf("source replay lost its operation failure chain: %v", sourceErr)
	}
	settled := a.settle(b, nil, sourceErr, baseNone, false)
	count, _ := os.ReadFile(countPath)
	if settled.patchFailure != nil || settled.fellShort || strings.TrimSpace(string(count)) != "3" {
		t.Errorf("a stale source failure was used as current failure authority or fell back to base: settled=%+v merge-tree=%s",
			settled, strings.TrimSpace(string(count)))
	}
}

func TestPatchActorNextLaunchConcreteFailureStaysOffline(t *testing.T) {
	t.Setenv("YOLO_ALLOW_PATCH_FAILURES", "")
	fx := newTreeFixture(t, `"f.txt"`)
	originalTiming := treeUpdateTiming
	treeUpdateTiming = func(packload.Fork) updateTiming { return timingAtLaunch }
	t.Cleanup(func() { treeUpdateTiming = originalTiming })
	initial, _ := fx.deliver(t, true)
	if initial.Dir == "" {
		t.Fatalf("initial tree was not built: %+v", initial)
	}
	fx.commit(t, "v1.2.0", map[int]string{14: "fourteen", 11: "eleven"})
	fx.now = fx.now.Add(2 * time.Hour)
	failed, _ := fx.deliver(t, true)
	if failed.PatchFailure == nil || failed.Entry == "" {
		t.Fatalf("fixture did not retain a concrete failure with intact Good: %+v", failed)
	}
	buildCount := len(fx.builds)
	fx.now = fx.now.Add(2 * time.Hour)
	treeUpdateTiming = func(packload.Fork) updateTiming { return timingNextLaunch }
	calls := filepath.Join(t.TempDir(), "git-called")
	patchedGitWrapper(t, "echo called > "+shellQuote(calls)+"; exit 99")
	spawned := 0
	previousBackground := backgroundTreeAdvance
	backgroundTreeAdvance = func([]packload.Fork, run.TreeBuildRequest, io.Writer, bool) { spawned++ }
	t.Cleanup(func() { backgroundTreeAdvance = previousBackground })
	// Use the production pool path: BuildTrees' answer and next-launch spawn decision are one act.
	var output bytes.Buffer
	req := run.TreeBuildRequest{Trees: []packload.Fork{fx.tree(t)}, Platform: patchedTestPlatform, Runtime: "podman",
		Workspace: "/ws", Build: true, CopyRoot: filepath.Join(t.TempDir(), "tree.patched")}
	_, trees := runBuildSlot(run.BuildSlotRequest{Trees: &req}, &output, false)
	if delivered := trees[treeKeyCLI]; delivered.Dir == "" || delivered.PatchFailure == nil {
		t.Errorf("the offline next-launch consumer lost its old Good or concrete failure: %+v\n%s", delivered, output.String())
	}
	if len(fx.builds) != buildCount || spawned != 0 {
		t.Errorf("next-launch failure ran foreground build/background work: builds=%d/%d spawned=%d", len(fx.builds), buildCount, spawned)
	}
	if _, err := os.Stat(calls); err == nil {
		t.Errorf("next-launch failure probed Git before its offline delivery")
	}
}

func TestPatchActorNextLaunchStillBuildsFirstTreeWithoutFallback(t *testing.T) {
	fx := newTreeFixture(t, `"f.txt"`)
	previousTiming := treeUpdateTiming
	treeUpdateTiming = func(packload.Fork) updateTiming { return timingNextLaunch }
	t.Cleanup(func() { treeUpdateTiming = previousTiming })
	delivered, _ := fx.deliver(t, true)
	if delivered.Dir == "" || len(fx.builds) != 1 {
		t.Fatalf("first tree with no Good and no fallback stopped building in front: %+v builds=%d", delivered, len(fx.builds))
	}
}

func TestPatchActorReapedCopyClearsOnlyFreshlyIrrelevantFailure(t *testing.T) {
	for _, mode := range []string{"accepted-removal", "failed-fetch", "problem", "unreadable"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("YOLO_ALLOW_PATCH_FAILURES", "")
			fx := newTreeFixture(t, `"f.txt"`)
			first, _ := fx.deliver(t, true)
			if first.Entry == "" {
				t.Fatalf("initial tree was not built: %+v", first)
			}
			fx.commit(t, "v1.2.0", map[int]string{14: "fourteen", 11: "eleven"})
			fx.now = fx.now.Add(2 * time.Hour)
			failed, _ := fx.deliver(t, true)
			if failed.PatchFailure == nil || failed.Entry == "" {
				t.Fatalf("fixture did not create Good plus a concrete failure: %+v", failed)
			}
			store := patchedAdvanceStore(true)
			key := failed.Entry
			previous := treeCopied
			reaped := false
			treeCopied = func(copied string) {
				if reaped || copied != key {
					return
				}
				reaped = true
				if err := (&capture.Store{Dir: paths.CapturesDir()}).ReapEntry(copied); err != nil {
					t.Fatal(err)
				}
				if mode == "unreadable" {
					checkPath := store.CheckRecordPath(treeKeyCLI)
					if err := os.RemoveAll(checkPath); err != nil {
						t.Fatal(err)
					}
					if err := os.Mkdir(checkPath, 0o700); err != nil {
						t.Fatal(err)
					}
					return
				}
				if err := store.WithCheckRecord(treeKeyCLI, nil, func(r *packsrc.CheckRecord, readErr error, _ func() error) (bool, error) {
					if readErr != nil {
						return false, readErr
					}
					switch mode {
					case "accepted-removal":
						r.Seq++
						r.CheckedAt = fx.now.Unix()
						r.Check.Seq, r.Check.At = r.Seq, fx.now.Unix()
						list := r.Check.List[:0]
						for _, e := range r.Check.List {
							if e.Commit != failed.PatchFailure.Target.Commit {
								list = append(list, e)
							}
						}
						r.Check.List = list
					case "failed-fetch":
						r.Check.FetchErr = "offline fixture"
					case "problem":
						r.Check.Problem = "fixture check problem"
					}
					return true, nil
				}); err != nil {
					t.Fatal(err)
				}
			}
			t.Cleanup(func() { treeCopied = previous })
			delivered, _ := fx.deliver(t, true)
			if !reaped {
				t.Fatal("the selected copy was not reaped")
			}
			if mode == "accepted-removal" {
				if delivered.PatchFailure != nil {
					t.Fatalf("a fresh successful check did not clear the positively irrelevant target: %+v", delivered.PatchFailure)
				}
			} else if delivered.PatchFailure == nil {
				t.Fatalf("%s cleared the failure without accepted evidence", mode)
			}
			if mode == "accepted-removal" {
				if current, err := store.LoadCheckRecord(treeKeyCLI); err != nil || current.CurrentPatchFailure(current.Read, failed.PatchFailure.Series) != nil {
					t.Fatalf("fixture did not establish an accepted successful check without the failed target: record=%+v err=%v", current, err)
				}
			} else if delivered.PatchFailure.Target.Commit != failed.PatchFailure.Target.Commit {
				t.Fatalf("%s did not retain the prior failure: got=%+v want=%+v", mode, delivered.PatchFailure, failed.PatchFailure)
			}
		})
	}
}

func deliverAfterReapedCopyWithLocalFailure(t *testing.T, fx *treeFixture, key string, failure *packsrc.PatchFailure,
	firstAdvance func(packload.Fork, advanceOptions) advanceResult) (run.TreeDelivery, int, bool) {
	t.Helper()
	store := patchedAdvanceStore(true)
	previousAdvance, previousCopy := treeAdvance, treeCopied
	localFailure := failure
	calls, reaped := 0, false
	treeAdvance = func(f packload.Fork, o advanceOptions) advanceResult {
		calls++
		if calls == 1 {
			result := firstAdvance(f, o)
			if result.delivery.PatchFailure != nil {
				localFailure = result.delivery.PatchFailure
			}
			return result
		}
		return advanceResult{delivery: entrypoint.ForkDelivery{Reason: "no offline result on the reread"}}
	}
	treeCopied = func(copied string) {
		if reaped || copied != key {
			return
		}
		reaped = true
		if err := (&capture.Store{Dir: paths.CapturesDir()}).ReapEntry(copied); err != nil {
			t.Fatal(err)
		}
		f := fx.tree(t)
		series, err := f.ReadSeries()
		if err != nil {
			t.Fatal(err)
		}
		checked := store.CheckPatched(f.CheckWant(series), packsrc.CheckOptions{Force: true, Now: func() time.Time { return fx.now }})
		if checked.Err != nil || checked.Record == nil || checked.Record.Check == nil {
			t.Fatalf("prepare the newer successful check after the copy reap: %+v", checked)
		}
		if err := store.WithCheckRecord(treeKeyCLI, nil, func(r *packsrc.CheckRecord, readErr error, _ func() error) (bool, error) {
			if readErr != nil {
				return false, readErr
			}
			if localFailure == nil || r.Owner != treeKeyCLI || r.Check == nil || r.Read != localFailure.Inputs ||
				r.Seq <= localFailure.Seq || r.Check.Seq != r.Seq {
				return false, fmt.Errorf("unexpected post-check authority: owner=%q inputs=%+v seq=%d failure=%+v", r.Owner, r.Read, r.Seq, localFailure)
			}
			r.PatchFailure = nil
			r.ApplyErr = nil
			if r.Check.FetchErr != "" || r.Check.Problem != "" {
				return false, fmt.Errorf("fixture check is not a successful check: %+v", r.Check)
			}
			assessment := *r
			assessment.PatchFailure = localFailure
			if current := assessment.CurrentPatchFailure(r.Read, localFailure.Series); current == nil {
				return false, fmt.Errorf("core relevance reader did not retain the local failure against its unchanged successful check")
			}
			return true, nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { treeAdvance, treeCopied = previousAdvance, previousCopy })
	delivered, _ := fx.deliver(t, true)
	return delivered, calls, reaped
}

func forceAdvanceForPatchActorMatrix(t *testing.T, fx *patchedAdvanceFixture) (advanceResult, string) {
	t.Helper()
	var output bytes.Buffer
	result := advancePatchedFork(fx.fork(t), advanceOptions{platform: patchedTestPlatform, runtime: "podman", workspace: "/ws",
		out: &output, errw: &output, launch: true, force: true, act: &run.ActInterrupt{}})
	return result, output.String()
}

func TestPatchActorFailedReplayRecordingDoesNotAuthorizeOldApplyErrClear(t *testing.T) {
	t.Setenv("YOLO_ALLOW_PATCH_FAILURES", "")
	fx, _ := patchedActorLaunch(t)
	fork := fx.fork(t)
	fx.commit(t, "v1.2.0", map[int]string{14: "fourteen", 20: "twenty"})
	store := patchedAdvanceStore(true)
	series, err := fork.ReadSeries()
	if err != nil {
		t.Fatal(err)
	}
	checked := store.CheckPatched(fork.CheckWant(series), packsrc.CheckOptions{Force: true, Now: func() time.Time { return fx.now }})
	if checked.Err != nil || checked.Record == nil || checked.Record.Check == nil || len(checked.Record.Check.List) == 0 {
		t.Fatalf("prepare newer successful check: %+v", checked)
	}
	olderApplyErr := &packsrc.ApplyError{Seq: checked.Record.Seq - 1, Error: "opaque diagnosis from the earlier check"}
	if err := store.WithCheckRecord(fork.Key(), nil, func(r *packsrc.CheckRecord, readErr error, _ func() error) (bool, error) {
		if readErr != nil {
			return false, readErr
		}
		r.ApplyErr = olderApplyErr
		return true, nil
	}); err != nil {
		t.Fatal(err)
	}
	before, err := store.LoadCheckRecord(fork.Key())
	if err != nil || before.Seq != checked.Record.Seq || before.Check.Seq != before.Seq || before.ApplyErr == nil ||
		before.ApplyErr.Seq == before.Seq || before.Check.FetchErr != "" || before.Check.Problem != "" {
		t.Fatalf("fixture lacks unchanged authority plus an older ApplyErr: record=%+v err=%v", before, err)
	}
	lock := patchedRecordLockPath(fork.Key())
	trip := filepath.Join(t.TempDir(), "fail-record-on-next-merge")
	extra := "if [ -e " + shellQuote(trip) + " ]; then for a in \"$@\"; do if [ \"$a\" = merge-tree ]; then rm -f " +
		shellQuote(lock) + "; mkdir -p " + shellQuote(lock) + "; rm -f " + shellQuote(trip) + "; break; fi; done; fi"
	patchedGitWrapper(t, extra)
	var output bytes.Buffer
	a, early := newAdvance(fork, advanceOptions{platform: patchedTestPlatform, runtime: "podman", workspace: "/ws",
		out: &output, errw: &output, launch: true, act: &run.ActInterrupt{}})
	if early != nil {
		t.Fatalf("newer advance setup failed: %+v\n%s", early, output.String())
	}
	a.seq = a.rec.Seq
	writeFile(t, trip, "fail first bound recording")
	walk := a.walk(a.rec.Check.List, true)
	if walk.Fit != 0 || len(walk.Results) == 0 || !walk.Results[0].Clean || !strings.Contains(output.String(), "recording the replay") {
		t.Fatalf("expected a clean newer walk and deterministic non-stale recording failure: walk=%+v\n%s", walk, output.String())
	}
	if info, err := os.Stat(lock); err != nil || !info.IsDir() {
		t.Fatalf("record lock obstruction did not remain in place after RecordReplay: info=%v err=%v", info, err)
	}
	if err := os.RemoveAll(lock); err != nil {
		t.Fatalf("restore record persistence before finish: %v", err)
	}
	// There is deliberately no successful replay recording between the failed RecordReplay above
	// and finish below: merely restoring the lock must not grant the failed pass write authority.
	result := a.finish(nil, forkBuild{}, 0, nil, "")
	after, err := store.LoadCheckRecord(fork.Key())
	if err != nil || after.Seq != before.Seq || after.Read != before.Read || after.Check.Seq != before.Check.Seq ||
		after.ApplyErr == nil || after.ApplyErr.Seq != olderApplyErr.Seq || after.ApplyErr.Error != olderApplyErr.Error ||
		result.delivery.Key == "" || result.delivery.PatchFailure != nil {
		t.Fatalf("failed replay recording authorized opaque cleanup after persistence recovered: result=%+v before=%+v after=%+v err=%v\n%s",
			result, before, after, err, output.String())
	}
	firstWalk := a.walk(a.rec.Check.List, true)
	if firstWalk.Fit != 0 || !a.replayRecordAuthorized {
		t.Fatalf("successful guarded recording did not establish its own cleanup authorization: walk=%+v authorized=%v\n%s",
			firstWalk, a.replayRecordAuthorized, output.String())
	}
	writeFile(t, trip, "fail a later bound recording")
	secondWalk := a.walk(a.rec.Check.List, true)
	if secondWalk.Fit != 0 || a.replayRecordAuthorized || !strings.Contains(output.String(), "recording the replay") {
		t.Fatalf("later failed pass reused prior recording authorization: walk=%+v authorized=%v\n%s",
			secondWalk, a.replayRecordAuthorized, output.String())
	}
	if err := os.RemoveAll(lock); err != nil {
		t.Fatal(err)
	}
	// The first pass succeeded, but the second pass failed; finish must not reuse its earlier
	// authorization for the current attempt.
	a.finish(nil, forkBuild{}, 0, nil, "")
	final, err := store.LoadCheckRecord(fork.Key())
	if err != nil || final.ApplyErr == nil || final.ApplyErr.Seq != olderApplyErr.Seq || final.ApplyErr.Error != olderApplyErr.Error {
		t.Fatalf("later failed recording reused stale earlier authorization to clear ApplyErr: record=%+v err=%v", final, err)
	}
}

func TestPatchActorDueForcedAndNewerVersionRetriesKeepExactFailure(t *testing.T) {
	for _, mode := range []string{"due", "force", "newer-version"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("YOLO_ALLOW_PATCH_FAILURES", "")
			fx, oldKey := patchedActorLaunch(t)
			fx.commit(t, "v1.2.0", map[int]string{14: "fourteen", 11: "eleven"})
			fx.later(2 * time.Hour)
			first, _, _ := fx.launch(t, "podman")
			if first.patchFailure == nil || first.patchFailure.Target.Tag != "v1.2.0" || first.delivery.Key != oldKey {
				t.Fatalf("fixture did not retain its selected v1.2.0 conflict with Good: %+v", first)
			}
			switch mode {
			case "due":
				fx.later(2 * time.Hour)
			case "newer-version":
				fx.commit(t, "v1.3.0", map[int]string{14: "fourteen", 20: "twenty"})
				fx.later(2 * time.Hour)
			}
			var retried advanceResult
			if mode == "force" {
				retried, _ = forceAdvanceForPatchActorMatrix(t, fx)
			} else {
				retried, _, _ = fx.launch(t, "podman")
			}
			current := fx.record(t)
			if !retried.failed || retried.patchFailure == nil || retried.patchFailure.Kind != "conflict" ||
				retried.patchFailure.Target.Tag != "v1.2.0" || retried.delivery.Key != oldKey || len(fx.builds) != 1 {
				t.Fatalf("%s retry hid or built past the current failed target: result=%+v builds=%d", mode, retried, len(fx.builds))
			}
			if current.PatchFailure == nil || current.PatchFailure.Target.Commit != retried.patchFailure.Target.Commit ||
				current.Good == nil || current.Good.Entry != oldKey {
				t.Fatalf("%s retry changed persisted failure or Good: record=%+v", mode, current)
			}
			if mode == "newer-version" {
				selected := false
				for _, target := range current.Check.List {
					selected = selected || target.Tag == "v1.2.0"
				}
				if !selected {
					t.Fatalf("newer v1.3.0 check does not retain older failed target in its full selected list: %+v", current.Check.List)
				}
			}
		})
	}
}

func TestPatchActorForcedExactCleanReplayRepairsOnlyThatFailure(t *testing.T) {
	t.Setenv("YOLO_ALLOW_PATCH_FAILURES", "")
	fx := newPatchedAdvanceFixture(t, "")
	fx.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	target := fx.commit(t, "v1.2.0", map[int]string{14: "fourteen", 20: "twenty"})
	once := filepath.Join(t.TempDir(), "reject-once")
	patchedGitWrapper(t, "is_merge=0; for a in \"$@\"; do [ \"$a\" = merge-tree ] && is_merge=1; done; "+
		"if [ \"$is_merge\" -eq 1 ] && [ ! -e "+shellQuote(once)+" ]; then : > "+shellQuote(once)+
		"; printf 'one operation rejection\\n' >&2; exit 2; fi")
	first, out, _ := fx.launch(t, "podman")
	if !first.failed || first.patchFailure == nil || first.patchFailure.Kind != "application-command" || len(fx.builds) != 0 {
		t.Fatalf("one-shot command failure did not establish the exact repair authority: %+v builds=%d\n%s", first, len(fx.builds), out)
	}
	repaired, out := forceAdvanceForPatchActorMatrix(t, fx)
	current := fx.record(t)
	if repaired.failed || !repaired.built || repaired.delivery.Key == "" || len(fx.builds) != 1 ||
		current.PatchFailure != nil || current.Good == nil || current.Good.Commit != target || current.Good.Entry != repaired.delivery.Key {
		t.Fatalf("forced clean replay did not clear only the repaired target and continue: result=%+v record=%+v builds=%d\n%s",
			repaired, current, len(fx.builds), out)
	}
}

func TestPatchActorFreshSuccessfulCheckRemovesFailedTarget(t *testing.T) {
	t.Setenv("YOLO_ALLOW_PATCH_FAILURES", "")
	fx, oldKey := patchedActorLaunch(t)
	v11 := fx.record(t).Good.Commit
	fx.commit(t, "v1.2.0", map[int]string{14: "fourteen", 11: "eleven"})
	fx.later(2 * time.Hour)
	failed, _, _ := fx.launch(t, "podman")
	if failed.patchFailure == nil || failed.patchFailure.Target.Tag != "v1.2.0" {
		t.Fatalf("fixture did not record v1.2.0 failure: %+v", failed)
	}
	for _, args := range [][]string{{"-C", fx.repo, "tag", "-d", "v1.2.0"}, {"-C", fx.repo, "update-ref", "refs/heads/main", v11}} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("update local fixture ref with git %q: %v\n%s", args, err, out)
		}
	}
	fx.later(2 * time.Hour)
	cleared, out, _ := fx.launch(t, "podman")
	current := fx.record(t)
	if cleared.patchFailure != nil || cleared.delivery.PatchFailure != nil || cleared.delivery.Key != oldKey || len(fx.builds) != 1 ||
		current.PatchFailure != nil || current.Good == nil || current.Good.Entry != oldKey {
		t.Fatalf("fresh successful check did not remove the target and preserve only the still-admitted Good: result=%+v record=%+v builds=%d\n%s",
			cleared, current, len(fx.builds), out)
	}
	for _, target := range current.Check.List {
		if target.Tag == "v1.2.0" {
			t.Fatalf("the successful check still selects removed v1.2.0: %+v", current.Check.List)
		}
	}
}

func TestPatchActorOnlyLiteralOneBypassesAndBackgroundNeverDoes(t *testing.T) {
	for _, value := range []string{"0", "true", "unset"} {
		t.Run(value, func(t *testing.T) {
			fx, oldKey := patchedActorLaunch(t)
			fx.commit(t, "v1.2.0", map[int]string{14: "fourteen", 11: "eleven"})
			fx.later(2 * time.Hour)
			if value == "unset" {
				prior, had := os.LookupEnv("YOLO_ALLOW_PATCH_FAILURES")
				_ = os.Unsetenv("YOLO_ALLOW_PATCH_FAILURES")
				t.Cleanup(func() {
					if had {
						_ = os.Setenv("YOLO_ALLOW_PATCH_FAILURES", prior)
					} else {
						_ = os.Unsetenv("YOLO_ALLOW_PATCH_FAILURES")
					}
				})
			} else {
				t.Setenv("YOLO_ALLOW_PATCH_FAILURES", value)
			}
			result, out, _ := fx.launch(t, "podman")
			if !result.failed || result.patchFailure == nil || result.delivery.Key != oldKey || len(fx.builds) != 1 ||
				strings.Contains(out, "CONTINUING:") {
				t.Fatalf("bypass value %q did not refuse on the intact Good: result=%+v builds=%d\n%s", value, result, len(fx.builds), out)
			}
		})
	}
	t.Run("background", func(t *testing.T) {
		fx, oldKey := patchedActorLaunch(t)
		fx.commit(t, "v1.2.0", map[int]string{14: "fourteen", 11: "eleven"})
		fx.later(2 * time.Hour)
		initial, _, _ := fx.launch(t, "podman")
		if initial.patchFailure == nil {
			t.Fatal("fixture did not establish cached failure")
		}
		t.Setenv("YOLO_ALLOW_PATCH_FAILURES", "1")
		var out bytes.Buffer
		result := advancePatchedFork(fx.fork(t), advanceOptions{platform: patchedTestPlatform, runtime: "podman", workspace: "/ws",
			out: &out, errw: &out, launch: true, background: true, act: &run.ActInterrupt{}})
		if !result.failed || result.patchFailure == nil || result.delivery.Key != oldKey || strings.Contains(out.String(), "CONTINUING:") ||
			len(fx.builds) != 1 {
			t.Fatalf("background operation inherited foreground continuation authority: result=%+v builds=%d\n%s",
				result, len(fx.builds), out.String())
		}
	})
}

func TestPatchActorExactBypassRequiresAnIntactCompatibleGood(t *testing.T) {
	t.Run("no-good", func(t *testing.T) {
		t.Setenv("YOLO_ALLOW_PATCH_FAILURES", "1")
		fx := newPatchedAdvanceFixture(t, "")
		fx.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
		fx.commit(t, "v1.2.0", map[int]string{14: "fourteen", 11: "eleven"})
		result, out, _ := fx.launch(t, "podman")
		if !result.failed || result.patchFailure == nil || result.delivery.Key != "" || len(fx.builds) != 0 ||
			strings.Contains(out, "CONTINUING:") {
			t.Fatalf("exact bypass manufactured a compatible Good where none existed: result=%+v builds=%d\n%s",
				result, len(fx.builds), out)
		}
	})
	t.Run("changed-recipe", func(t *testing.T) {
		fx, oldKey := patchedActorLaunch(t)
		manifest := mustRead(t, fx.manifest)
		if !strings.Contains(manifest, `"build":"sh build.sh"`) {
			t.Fatal("fixture manifest no longer carries the expected build line")
		}
		writeFile(t, fx.manifest, strings.Replace(manifest, `"build":"sh build.sh"`, `"build":"sh build2.sh"`, 1))
		fx.commit(t, "v1.2.0", map[int]string{14: "fourteen", 11: "eleven"})
		fx.later(2 * time.Hour)
		t.Setenv("YOLO_ALLOW_PATCH_FAILURES", "1")
		result, out, _ := fx.launch(t, "podman")
		if !result.failed || result.patchFailure == nil || result.delivery.Key != "" || len(fx.builds) != 1 ||
			result.delivery.Key == oldKey || strings.Contains(out, "CONTINUING:") {
			t.Fatalf("exact bypass delivered a Good built under the changed recipe: result=%+v old=%s builds=%d\n%s",
				result, oldKey, len(fx.builds), out)
		}
	})
}

func prepareOpaqueTreeActorRecord(t *testing.T, fx *treeFixture) (*packsrc.Store, *packsrc.Series) {
	t.Helper()
	store := patchedAdvanceStore(true)
	f := fx.tree(t)
	series, err := f.ReadSeries()
	if err != nil {
		t.Fatal(err)
	}
	checked := store.CheckPatched(f.CheckWant(series), packsrc.CheckOptions{Force: true, Now: func() time.Time { return fx.now }})
	if checked.Err != nil || checked.Record == nil || checked.Record.Check == nil || checked.Record.Check.FetchErr != "" ||
		checked.Record.Check.Problem != "" {
		t.Fatalf("prepare the successful selected check for opaque replay: %+v", checked)
	}
	if err := store.WithCheckRecord(f.Key(), nil, func(r *packsrc.CheckRecord, readErr error, _ func() error) (bool, error) {
		if readErr != nil {
			return false, readErr
		}
		r.ApplyErr = &packsrc.ApplyError{Seq: r.Seq, Error: "opaque prior replay diagnosis"}
		return true, nil
	}); err != nil {
		t.Fatal(err)
	}
	return store, series
}

func TestPatchActorOpaqueNextLaunchCleanProbeIsOfflineAndNonfatal(t *testing.T) {
	t.Setenv("YOLO_ALLOW_PATCH_FAILURES", "")
	fx := newTreeFixture(t, `"f.txt"`)
	old, _ := fx.deliver(t, true)
	if old.Dir == "" {
		t.Fatalf("initial tree was not delivered: %+v", old)
	}
	fx.commit(t, "v1.2.0", map[int]string{14: "fourteen", 20: "twenty"})
	fx.now = fx.now.Add(2 * time.Hour)
	store, _ := prepareOpaqueTreeActorRecord(t, fx)
	previousTiming := treeUpdateTiming
	treeUpdateTiming = func(packload.Fork) updateTiming { return timingNextLaunch }
	t.Cleanup(func() { treeUpdateTiming = previousTiming })
	fetchCalled := filepath.Join(t.TempDir(), "fetch-called")
	patchedGitWrapper(t, "is_fetch=0; for a in \"$@\"; do [ \"$a\" = fetch ] && is_fetch=1; done; "+
		"if [ \"$is_fetch\" -eq 1 ]; then echo called > "+shellQuote(fetchCalled)+"; exit 99; fi")
	beforeBuilds := len(fx.builds)
	var output bytes.Buffer
	req := run.TreeBuildRequest{Trees: []packload.Fork{fx.tree(t)}, Platform: patchedTestPlatform, Runtime: "podman",
		Workspace: "/ws", Build: true, CopyRoot: filepath.Join(t.TempDir(), "tree.patched")}
	_, trees := runBuildSlot(run.BuildSlotRequest{Trees: &req}, &output, false)
	delivered := trees[treeKeyCLI]
	current, err := store.LoadCheckRecord(treeKeyCLI)
	if delivered.Dir == "" || delivered.Entry != old.Entry || delivered.PatchFailure != nil || len(fx.builds) != beforeBuilds ||
		err != nil || current.ApplyErrAtLastCheck() != nil || current.PatchFailure != nil {
		t.Fatalf("clean local opaque classification became fatal, built, or lost the admitted tree: delivery=%+v record=%+v builds=%d/%d err=%v\n%s",
			delivered, current, len(fx.builds), beforeBuilds, err, output.String())
	}
	if _, err := os.Stat(fetchCalled); err == nil {
		t.Fatal("opaque local-only clean probe called fetch")
	}
}

func TestPatchActorOpaqueUnavailableDeduplicatesAndLaterRetries(t *testing.T) {
	t.Setenv("YOLO_ALLOW_PATCH_FAILURES", "")
	fx := newTreeFixture(t, `"f.txt"`)
	old, _ := fx.deliver(t, true)
	if old.Dir == "" {
		t.Fatalf("initial tree was not delivered: %+v", old)
	}
	fx.commit(t, "v1.2.0", map[int]string{14: "fourteen", 11: "eleven"})
	fx.now = fx.now.Add(2 * time.Hour)
	store, _ := prepareOpaqueTreeActorRecord(t, fx)
	previousTiming := treeUpdateTiming
	treeUpdateTiming = func(packload.Fork) updateTiming { return timingNextLaunch }
	t.Cleanup(func() { treeUpdateTiming = previousTiming })
	oldGitLog := patchedGitWrapper(t, "if [ \"$1\" = version ]; then echo 'git version 1.0'; exit 0; fi")
	req := run.TreeBuildRequest{Trees: []packload.Fork{fx.tree(t)}, Platform: patchedTestPlatform, Runtime: "podman",
		Workspace: "/ws", Build: true, CopyRoot: filepath.Join(t.TempDir(), "tree.patched")}
	beforeBuilds := len(fx.builds)
	for attempt := 0; attempt < 2; attempt++ {
		var output bytes.Buffer
		_, trees := runBuildSlot(run.BuildSlotRequest{Trees: &req}, &output, false)
		delivered := trees[treeKeyCLI]
		current, err := store.LoadCheckRecord(treeKeyCLI)
		if delivered.Dir == "" || delivered.Entry != old.Entry || delivered.PatchFailure != nil || len(fx.builds) != beforeBuilds ||
			err != nil || current.PatchFailure != nil || current.ApplyErrAtLastCheck() == nil || current.ApplyErr.Legacy == nil ||
			current.ApplyErr.Legacy.State != "unavailable" || current.ApplyErr.Legacy.Target.Tag != "v1.2.0" {
			t.Fatalf("unavailable opaque attempt %d became fatal or lost evidence: delivery=%+v record=%+v builds=%d/%d err=%v\n%s",
				attempt+1, delivered, current, len(fx.builds), beforeBuilds, err, output.String())
		}
	}
	log, err := os.ReadFile(oldGitLog)
	if err != nil {
		t.Fatal(err)
	}
	versions := 0
	for _, line := range strings.Split(string(log), "\n") {
		if line == "version" {
			versions++
		}
		if strings.Contains(" "+line+" ", " merge-tree ") || strings.Contains(" "+line+" ", " fetch ") {
			t.Fatalf("unavailable local classification replayed an entry or fetched: %s", line)
		}
	}
	if versions != 1 {
		t.Fatalf("unchanged unavailable identity called git version %d times, want one cached classification: %s", versions, log)
	}
	newGitLog := patchedGitWrapper(t, "is_fetch=0; for a in \"$@\"; do [ \"$a\" = fetch ] && is_fetch=1; done; "+
		"if [ \"$is_fetch\" -eq 1 ]; then echo unexpected fetch >&2; exit 99; fi")
	var output bytes.Buffer
	_, trees := runBuildSlot(run.BuildSlotRequest{Trees: &req}, &output, false)
	delivered := trees[treeKeyCLI]
	current, err := store.LoadCheckRecord(treeKeyCLI)
	if delivered.Dir == "" || delivered.Entry != old.Entry || delivered.PatchFailure == nil ||
		delivered.PatchFailure.Kind != "conflict" || delivered.PatchFailure.Target.Tag != "v1.2.0" ||
		len(fx.builds) != beforeBuilds || err != nil || current.PatchFailure == nil || current.ApplyErr != nil {
		t.Fatalf("a changed Git identity did not retry the opaque target and persist its concrete conflict: delivery=%+v record=%+v builds=%d/%d err=%v\n%s",
			delivered, current, len(fx.builds), beforeBuilds, err, output.String())
	}
	if log, err := os.ReadFile(newGitLog); err != nil || strings.Contains(string(log), " fetch ") {
		t.Fatalf("later local-only retry fetched: err=%v log=%s", err, log)
	}
}

func treeFailureWithAdmittedGood(t *testing.T) (*treeFixture, run.TreeDelivery) {
	t.Helper()
	t.Setenv("YOLO_ALLOW_PATCH_FAILURES", "")
	fx := newTreeFixture(t, `"f.txt"`)
	good, _ := fx.deliver(t, true)
	if good.Entry == "" {
		t.Fatalf("initial tree was not admitted: %+v", good)
	}
	fx.commit(t, "v1.2.0", map[int]string{14: "fourteen", 11: "eleven"})
	fx.now = fx.now.Add(2 * time.Hour)
	failed, _ := fx.deliver(t, true)
	if failed.Entry == "" || failed.PatchFailure == nil || failed.PatchFailure.Target.Tag != "v1.2.0" {
		t.Fatalf("fixture did not admit Good alongside a concrete failure: %+v", failed)
	}
	return fx, failed
}

func TestPatchActorTreeFailureReturnsWithoutAKeyOrCopy(t *testing.T) {
	t.Setenv("YOLO_ALLOW_PATCH_FAILURES", "")
	fx := newTreeFixture(t, `"f.txt"`)
	fx.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	fx.commit(t, "v1.2.0", map[int]string{14: "fourteen", 11: "eleven"})
	delivered, out := fx.deliver(t, true)
	if delivered.Dir != "" || delivered.Entry != "" || delivered.PatchFailure == nil || len(fx.builds) != 0 ||
		delivered.Reason != delivered.PatchFailure.Error() {
		t.Fatalf("tree operation failure with no key was hidden or built: %+v builds=%d\n%s", delivered, len(fx.builds), out)
	}
}

func TestPatchActorOrdinaryTreeCopyErrorIsNotPatchFailure(t *testing.T) {
	t.Setenv("YOLO_ALLOW_PATCH_FAILURES", "")
	fx := newTreeFixture(t, `"f.txt"`)
	good, _ := fx.deliver(t, true)
	if good.Entry == "" {
		t.Fatalf("initial tree was not delivered: %+v", good)
	}
	copyRoot := filepath.Join(t.TempDir(), "copy-root-file")
	if err := os.WriteFile(copyRoot, []byte("ordinary copy setup failure"), 0o600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	req := run.TreeBuildRequest{Trees: []packload.Fork{fx.tree(t)}, Platform: patchedTestPlatform, Runtime: "podman",
		Workspace: "/ws", Build: true, CopyRoot: copyRoot}
	_, trees := runBuildSlot(run.BuildSlotRequest{Trees: &req}, &output, false)
	delivered := trees[treeKeyCLI]
	if delivered.PatchFailure != nil || delivered.Entry != "" || delivered.Dir != "" || len(fx.builds) != 1 ||
		!strings.Contains(delivered.Reason, "could not be copied") {
		t.Fatalf("ordinary per-launch copy failure acquired patch authority or moved Good: delivery=%+v builds=%d\n%s",
			delivered, len(fx.builds), output.String())
	}
}

func TestPatchActorTreeFailureSurvivesMissingCopyRootAndCopyError(t *testing.T) {
	for _, mode := range []string{"missing-root", "copy-error"} {
		t.Run(mode, func(t *testing.T) {
			fx, failed := treeFailureWithAdmittedGood(t)
			beforeBuilds := len(fx.builds)
			copyRoot := ""
			if mode == "copy-error" {
				copyRoot = filepath.Join(t.TempDir(), "not-a-directory")
				if err := os.WriteFile(copyRoot, []byte("block copy root"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			var output bytes.Buffer
			req := run.TreeBuildRequest{Trees: []packload.Fork{fx.tree(t)}, Platform: patchedTestPlatform, Runtime: "podman",
				Workspace: "/ws", Build: true, CopyRoot: copyRoot}
			_, trees := runBuildSlot(run.BuildSlotRequest{Trees: &req}, &output, false)
			delivered := trees[treeKeyCLI]
			if delivered.Dir != "" || delivered.PatchFailure == nil || delivered.PatchFailure.Target.Commit != failed.PatchFailure.Target.Commit ||
				len(fx.builds) != beforeBuilds {
				t.Fatalf("%s dropped a current failure or started a build: delivery=%+v builds=%d/%d\n%s",
					mode, delivered, len(fx.builds), beforeBuilds, output.String())
			}
			if mode == "missing-root" && !strings.Contains(delivered.Reason, "staged no pack tree to copy") {
				t.Errorf("missing root reason does not name the copy surface: %+v", delivered)
			}
			if mode == "copy-error" && !strings.Contains(delivered.Reason, "could not be copied") {
				t.Errorf("copy failure reason does not preserve the ordinary disposition: %+v", delivered)
			}
		})
	}
}

func TestPatchActorReapedCopyAfterFreshSuccessfulRemovalSurvivesSecondReap(t *testing.T) {
	fx, failed := treeFailureWithAdmittedGood(t)
	before, err := patchedAdvanceStore(true).LoadCheckRecord(treeKeyCLI)
	if err != nil || before.Good == nil {
		t.Fatalf("load admitted Good before reaping: record=%+v err=%v", before, err)
	}
	goodCommit := before.Good.Commit
	previousCopy := treeCopied
	copies, varErrors := 0, []error(nil)
	treeCopied = func(key string) {
		copies++
		if err := (&capture.Store{Dir: paths.CapturesDir()}).ReapEntry(key); err != nil {
			varErrors = append(varErrors, err)
		}
		if copies != 1 {
			return
		}
		for _, args := range [][]string{{"-C", fx.repo, "tag", "-d", "v1.2.0"}, {"-C", fx.repo, "update-ref", "refs/heads/main", goodCommit}} {
			if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
				varErrors = append(varErrors, fmt.Errorf("git %q: %w: %s", args, err, out))
			}
		}
		fx.now = fx.now.Add(2 * time.Hour)
	}
	t.Cleanup(func() { treeCopied = previousCopy })
	beforeBuilds := len(fx.builds)
	var output bytes.Buffer
	req := run.TreeBuildRequest{Trees: []packload.Fork{fx.tree(t)}, Platform: patchedTestPlatform, Runtime: "podman",
		Workspace: "/ws", Build: true, CopyRoot: filepath.Join(t.TempDir(), "tree.patched")}
	_, trees := runBuildSlot(run.BuildSlotRequest{Trees: &req}, &output, false)
	delivered := trees[treeKeyCLI]
	current, err := patchedAdvanceStore(true).LoadCheckRecord(treeKeyCLI)
	if len(varErrors) != 0 || copies != 2 || delivered.Dir != "" || delivered.PatchFailure != nil ||
		!strings.Contains(delivered.Reason, "reaped twice") || len(fx.builds) != beforeBuilds+1 || err != nil ||
		current.PatchFailure != nil || current.Check == nil || current.Check.FetchErr != "" || current.Check.Problem != "" {
		t.Fatalf("fresh positive target removal was lost across the second reaped copy: delivery=%+v copies=%d failure=%+v record=%+v builds=%d/%d err=%v errors=%v\n%s",
			delivered, copies, failed.PatchFailure, current, len(fx.builds), beforeBuilds, err, varErrors, output.String())
	}
}

func TestPatchActorTreeFailureSurvivesTwiceReapedCopyAfterFailedFetch(t *testing.T) {
	fx, failed := treeFailureWithAdmittedGood(t)
	fx.now = fx.now.Add(2 * time.Hour)
	fetchLog := patchedGitWrapper(t, "is_fetch=0; for a in \"$@\"; do [ \"$a\" = fetch ] && is_fetch=1; done; "+
		"if [ \"$is_fetch\" -eq 1 ]; then printf 'fixture fetch unavailable\\n' >&2; exit 2; fi")
	previousAdvance, previousCopy := treeAdvance, treeCopied
	advances, reaped := 0, false
	treeAdvance = func(f packload.Fork, o advanceOptions) advanceResult {
		advances++
		if advances == 1 {
			return advancePatchedFork(f, o)
		}
		return advanceResult{delivery: entrypoint.ForkDelivery{Key: failed.Entry, PatchFailure: failed.PatchFailure}}
	}
	treeCopied = func(key string) {
		if reaped || key != failed.Entry {
			return
		}
		reaped = true
		if err := (&capture.Store{Dir: paths.CapturesDir()}).ReapEntry(key); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { treeAdvance, treeCopied = previousAdvance, previousCopy })
	beforeBuilds := len(fx.builds)
	var output bytes.Buffer
	req := run.TreeBuildRequest{Trees: []packload.Fork{fx.tree(t)}, Platform: patchedTestPlatform, Runtime: "podman",
		Workspace: "/ws", Build: true, CopyRoot: filepath.Join(t.TempDir(), "tree.patched")}
	_, trees := runBuildSlot(run.BuildSlotRequest{Trees: &req}, &output, false)
	delivered := trees[treeKeyCLI]
	current, err := patchedAdvanceStore(true).LoadCheckRecord(treeKeyCLI)
	if !reaped || advances != 2 || delivered.Dir != "" || delivered.PatchFailure == nil ||
		delivered.PatchFailure.Target.Commit != failed.PatchFailure.Target.Commit || !strings.Contains(delivered.Reason, "reaped twice") ||
		len(fx.builds) != beforeBuilds || err != nil || current.Check == nil || current.Check.FetchErr == "" {
		t.Fatalf("second reaped copy after failed fresh check hid its failure: delivery=%+v advances=%d record=%+v builds=%d/%d err=%v\n%s",
			delivered, advances, current, len(fx.builds), beforeBuilds, err, output.String())
	}
	if log, err := os.ReadFile(fetchLog); err != nil || !strings.Contains(string(log), "fetch") {
		t.Fatalf("failed-fetch control did not run its local Git tripwire: err=%v log=%s", err, log)
	}
}

func TestPatchActorReapedCopyRetainsLocalCommandFailureWhenTargetRemainsSelected(t *testing.T) {
	t.Setenv("YOLO_ALLOW_PATCH_FAILURES", "")
	fx := newTreeFixture(t, `"f.txt"`)
	good, _ := fx.deliver(t, true)
	if good.Entry == "" {
		t.Fatalf("initial tree was not built: %+v", good)
	}
	fx.commit(t, "v1.2.0", map[int]string{14: "fourteen", 11: "eleven"})
	fx.now = fx.now.Add(2 * time.Hour)
	wrapperLog := patchedGitWrapper(t, "is_merge=0; for a in \"$@\"; do [ \"$a\" = merge-tree ] && is_merge=1; done; "+
		"if [ \"$is_merge\" -eq 1 ]; then printf 'test operation-command rejection\\n' >&2; exit 2; fi")
	var localFailure *packsrc.PatchFailure
	delivered, calls, reaped := deliverAfterReapedCopyWithLocalFailure(t, fx, good.Entry, nil,
		func(f packload.Fork, o advanceOptions) advanceResult {
			result := advancePatchedFork(f, o)
			localFailure = result.delivery.PatchFailure
			if localFailure == nil || localFailure.Kind != "application-command" {
				log, _ := os.ReadFile(wrapperLog)
				t.Fatalf("the real first advance did not classify its rejected member command: %+v; wrapper log:\n%s", result, log)
			}
			return result
		})
	if !reaped || calls != 2 || localFailure == nil || !strings.Contains(localFailure.Detail, "test operation-command rejection") {
		t.Fatalf("fixture missed its real local command failure/reap/reread: reaped=%v advances=%d failure=%+v", reaped, calls, localFailure)
	}
	if delivered.PatchFailure == nil || delivered.PatchFailure.Kind != "application-command" ||
		delivered.PatchFailure.Target.Commit != localFailure.Target.Commit {
		t.Fatalf("the adapter cleared a local typed command failure although a newer successful same-input check still selects its target: %+v (local %+v)",
			delivered.PatchFailure, localFailure)
	}
	current, err := patchedAdvanceStore(true).LoadCheckRecord(treeKeyCLI)
	if err != nil || current.PatchFailure != nil || current.Seq <= localFailure.Seq || current.Read != localFailure.Inputs ||
		current.CurrentPatchFailure(current.Read, localFailure.Series) != nil {
		t.Fatalf("fixture did not leave a newer same-input successful record with no persisted typed field: record=%+v err=%v", current, err)
	}
	selected := false
	for _, target := range current.Check.List {
		if target.Commit == localFailure.Target.Commit {
			selected = true
		}
	}
	if !selected {
		t.Fatalf("the current successful check no longer selects the local failure's target: %+v", current.Check.List)
	}
}

func TestPatchActorReapedCopyRetainsLocalBaseFailureForUnchangedBase(t *testing.T) {
	fx := newTreeFixture(t, `"f.txt"`)
	good, _ := fx.deliver(t, true)
	if good.Entry == "" {
		t.Fatalf("initial tree was not built: %+v", good)
	}
	f := fx.tree(t)
	series, err := f.ReadSeries()
	if err != nil {
		t.Fatal(err)
	}
	record, err := patchedAdvanceStore(true).LoadCheckRecord(f.Key())
	if err != nil {
		t.Fatal(err)
	}
	failure := &packsrc.PatchFailure{Owner: f.Key(), Inputs: record.Read, Series: series.Digest,
		Target: packsrc.ListEntry{Commit: series.Base}, Kind: "base", Member: series.Members[0].Name, Seq: record.Seq}
	delivered, calls, reaped := deliverAfterReapedCopyWithLocalFailure(t, fx, good.Entry, failure,
		func(_ packload.Fork, _ advanceOptions) advanceResult {
			return advanceResult{delivery: entrypoint.ForkDelivery{Key: good.Entry, PatchFailure: failure}, patchFailure: failure}
		})
	if !reaped || calls != 2 {
		t.Fatalf("fixture did not exercise one real copy reap and adapter reread: reaped=%v advances=%d", reaped, calls)
	}
	if delivered.PatchFailure == nil || delivered.PatchFailure.Kind != "base" || delivered.PatchFailure.Target.Commit != series.Base {
		t.Fatalf("the adapter cleared a local base failure although the base and fresh bound inputs are unchanged: %+v", delivered.PatchFailure)
	}
}

type patchFailureReapWriter struct {
	store  *capture.Store
	key    string
	err    error
	reaped bool
	said   bytes.Buffer
}

func (w *patchFailureReapWriter) Write(p []byte) (int, error) {
	_, _ = w.said.Write(p)
	if !w.reaped {
		w.reaped = true
		w.err = w.store.ReapEntry(w.key)
	}
	return len(p), nil
}

func TestPatchActorExactBypassRevalidatesServingKeyBeforeFinish(t *testing.T) {
	t.Setenv("YOLO_ALLOW_PATCH_FAILURES", "")
	fx, _ := patchedActorLaunch(t)
	fx.commit(t, "v1.2.0", map[int]string{14: "fourteen", 11: "eleven"})
	fx.later(2 * time.Hour)
	failed, _, _ := fx.launch(t, "podman")
	if failed.patchFailure == nil || failed.delivery.Key == "" {
		t.Fatalf("fixture did not establish Good and a concrete failure: %+v", failed)
	}
	t.Setenv("YOLO_ALLOW_PATCH_FAILURES", "1")
	writer := &patchFailureReapWriter{store: &capture.Store{Dir: paths.CapturesDir()}, key: failed.delivery.Key}
	a, early := newAdvance(fx.fork(t), advanceOptions{platform: patchedTestPlatform, runtime: "podman", launch: true,
		errw: writer})
	if early != nil || a.serving == nil {
		t.Fatalf("the exact last-good entry was not selected: actor=%v early=%+v", a, early)
	}
	builds := len(fx.builds)
	result := a.patchFailureResult(failed.patchFailure)
	if writer.err != nil || result.delivery.Key != "" || !result.failed || result.patchFailure == nil || len(fx.builds) != builds ||
		strings.Contains(writer.said.String(), "CONTINUING: using intact admitted build") {
		t.Fatalf("exact bypass reused a reaped serving key or rebuilt instead of preserving the refusal: writer=%v result=%+v builds=%d/%d",
			writer.err, result, len(fx.builds), builds)
	}
}
