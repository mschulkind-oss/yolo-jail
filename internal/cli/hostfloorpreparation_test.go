package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/capture"
	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/hostfloor"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

func prepareFloorOpaqueApplyErr(t *testing.T, fx *patchedAdvanceFixture, version string, changes map[int]string) (*packsrc.Store, packload.Fork) {
	t.Helper()
	fx.commit(t, version, changes)
	fork := fx.fork(t)
	series, err := fork.ReadSeries()
	if err != nil {
		t.Fatal(err)
	}
	store := patchedAdvanceStore(true)
	checked := store.CheckPatched(fork.CheckWant(series), packsrc.CheckOptions{Force: true, Now: func() time.Time { return fx.now }})
	if checked.Err != nil || checked.Record == nil || checked.Record.Check == nil || len(checked.Record.Check.List) == 0 {
		t.Fatalf("fixture could not make the selected local check: %+v", checked)
	}
	if checked.Record.Good == nil || checked.Record.Good.Entry == "" {
		t.Fatalf("fixture has no admitted Good build for bypass checks: %+v", checked.Record)
	}
	if err := store.WithCheckRecord(fork.Key(), nil, func(r *packsrc.CheckRecord, readErr error, _ func() error) (bool, error) {
		if readErr != nil {
			return false, readErr
		}
		r.ApplyErr = &packsrc.ApplyError{Seq: r.Seq, Error: "opaque legacy apply diagnosis"}
		return true, nil
	}); err != nil {
		t.Fatal(err)
	}
	return store, fork
}

func newFloorPatchedFixture(t *testing.T) *patchedAdvanceFixture {
	t.Helper()
	fx := patchedFloorFixture(t)
	fx.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	if rc, _, output := fx.hostLaunch(t); rc != 0 {
		t.Fatalf("seed the real host floor with the fixture's admitted Good: rc=%d\\n%s", rc, output)
	}
	return fx
}

func selectedProductionFloor(t *testing.T) (*hostfloor.Floor, hostfloor.Program, []*packload.Pack) {
	t.Helper()
	selection := selectConfiguredHostPacks()
	progs := floorPrograms(selection.packs)
	p, ok := floorProgram(progs, "tool")
	if !ok || !p.Install.IsPatchedFork() {
		t.Fatalf("selected floor has no patched tool: %+v", progs)
	}
	floor := productionHostFloor(io.Discard, progs)
	// The build is the fixture's fake capture jail; make the path reachable so an erroneous second
	// advance is observed rather than hidden by this test machine's runtime installation.
	floor.CaptureUnavailable = nil
	return floor, p, selection.packs
}

func TestFloorReplayAuthorityMutationHelper(t *testing.T) {
	if os.Getenv("YOLO_TEST_REPLAY_MUTATION") != "1" {
		return
	}
	store := &packsrc.Store{Dir: os.Getenv("YOLO_TEST_REPLAY_STORE")}
	owner := os.Getenv("YOLO_TEST_REPLAY_OWNER")
	if owner == "" || store.Dir == "" {
		t.Fatal("concurrent replay mutation helper lacks its store binding")
	}
	if err := store.WithCheckRecord(owner, nil, func(r *packsrc.CheckRecord, readErr error, _ func() error) (bool, error) {
		if readErr != nil {
			return false, readErr
		}
		if r.ApplyErr == nil {
			return false, errors.New("the replay mutation helper found no opaque ApplyErr")
		}
		r.ApplyErr.Error = "concurrently changed opaque authority"
		return true, nil
	}); err != nil {
		t.Fatal(err)
	}
}

func obstructLegacyReplayRecord(t *testing.T, owner string) string {
	t.Helper()
	lock := patchedRecordLockPath(owner)
	t.Cleanup(func() { _ = os.RemoveAll(lock) })
	return "is_replay=0; for a in \"$@\"; do [ \"$a\" = merge-tree ] && is_replay=1; done; " +
		"if [ \"$is_replay\" -eq 1 ]; then rm -f " + shellQuote(lock) + "; mkdir -p " + shellQuote(lock) + "; fi"
}

func TestProductionFloorStaleLocalReplayAuthorityRefusesWithoutSecondAdvance(t *testing.T) {
	t.Setenv("YOLO_ALLOW_PATCH_FAILURES", "1")
	fx := newFloorPatchedFixture(t)
	_, fork := prepareFloorOpaqueApplyErr(t, fx, "v1.2.0", map[int]string{14: "fourteen", 11: "eleven"})
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	extra := "is_replay=0; for a in \"$@\"; do [ \"$a\" = merge-tree ] && is_replay=1; done; " +
		"if [ \"$is_replay\" -eq 1 ]; then YOLO_TEST_REPLAY_MUTATION=1 " +
		"YOLO_TEST_REPLAY_STORE=" + shellQuote(paths.PacksDir()) + " YOLO_TEST_REPLAY_OWNER=" + shellQuote(fork.Key()) + " " +
		shellQuote(executable) + " -test.run='^TestFloorReplayAuthorityMutationHelper$' -test.short=true -test.count=1 -test.timeout=120s >/dev/null 2>&1 || exit 88; fi"
	patchedGitWrapper(t, extra)
	floor, p, _ := selectedProductionFloor(t)
	previousAdvance := floorAdvance
	advances := 0
	floorAdvance = func(ctx context.Context, f packload.Fork, out io.Writer, installed *installedCopy, act *run.ActInterrupt) advanceResult {
		advances++
		return previousAdvance(ctx, f, out, installed, act)
	}
	t.Cleanup(func() { floorAdvance = previousAdvance })
	_, err = floor.PreparePatched(context.Background(), p, true)
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "stale") || advances != 0 {
		t.Fatalf("stale local authority was accepted or triggered a second advance: advances=%d err=%v", advances, err)
	}
}

func TestProductionFloorTypedLocalReplayFailureAndRecordErrorBothRefuseBypass(t *testing.T) {
	t.Setenv("YOLO_ALLOW_PATCH_FAILURES", "1")
	fx := newFloorPatchedFixture(t)
	_, fork := prepareFloorOpaqueApplyErr(t, fx, "v1.2.0", map[int]string{14: "fourteen", 11: "eleven"})
	patchedGitWrapper(t, obstructLegacyReplayRecord(t, fork.Key()))
	floor, p, _ := selectedProductionFloor(t)
	_, err := floor.PreparePatched(context.Background(), p, false)
	var failure *packsrc.PatchFailure
	if err == nil || !errors.As(err, &failure) || failure.Member != "0001-ten.patch" || !strings.Contains(err.Error(), "is a directory") {
		t.Fatalf("real typed local conflict or its failed RecordReplay was lost/waived: failure=%+v err=%v", failure, err)
	}
}

func TestProductionFloorCleanLocalReplayRecordErrorIsOrdinaryFailure(t *testing.T) {
	t.Setenv("YOLO_ALLOW_PATCH_FAILURES", "1")
	fx := newFloorPatchedFixture(t)
	_, fork := prepareFloorOpaqueApplyErr(t, fx, "v1.2.0", map[int]string{14: "fourteen", 20: "twenty"})
	patchedGitWrapper(t, obstructLegacyReplayRecord(t, fork.Key()))
	floor, p, _ := selectedProductionFloor(t)
	_, err := floor.PreparePatched(context.Background(), p, false)
	var failure *packsrc.PatchFailure
	if err == nil || errors.As(err, &failure) || !strings.Contains(err.Error(), "is a directory") {
		t.Fatalf("clean local replay with failed persistence fabricated a failure or succeeded: failure=%+v err=%v", failure, err)
	}
}

func TestProductionFloorCanceledPreparationContextReachesStoreAndDoesNotAdvance(t *testing.T) {
	t.Setenv("YOLO_ALLOW_PATCH_FAILURES", "1")
	fx := newFloorPatchedFixture(t)
	_, _ = prepareFloorOpaqueApplyErr(t, fx, "v1.2.0", map[int]string{14: "fourteen", 11: "eleven"})
	shared := patchedAdvanceStore(true)
	previousStore := patchedAdvanceStore
	patchedAdvanceStore = func(bool) *packsrc.Store { return shared }
	t.Cleanup(func() { patchedAdvanceStore = previousStore })
	floor, p, _ := selectedProductionFloor(t)
	previousAdvance := floorAdvance
	advances := 0
	floorAdvance = func(ctx context.Context, f packload.Fork, out io.Writer, installed *installedCopy, act *run.ActInterrupt) advanceResult {
		advances++
		return previousAdvance(ctx, f, out, installed, act)
	}
	t.Cleanup(func() { floorAdvance = previousAdvance })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := floor.PreparePatched(ctx, p, true)
	var failure *packsrc.PatchFailure
	if err == nil || errors.As(err, &failure) ||
		(!strings.Contains(strings.ToLower(err.Error()), "cancel") && !strings.Contains(strings.ToLower(err.Error()), "ran out")) ||
		shared.Ctx != nil || advances != 0 {
		t.Fatalf("cancelled local probe lost its authority error or mutated shared context: advances=%d shared_ctx=%v err=%v",
			advances, shared.Ctx, err)
	}
}

func TestProductionFloorForwardsContextToTheOrdinaryAdvanceSeam(t *testing.T) {
	t.Setenv("YOLO_ALLOW_PATCH_FAILURES", "")
	_ = newFloorPatchedFixture(t)
	floor, p, _ := selectedProductionFloor(t)
	ctx := context.WithValue(context.Background(), struct{ key string }{}, "operation-context")
	previousAdvance := floorAdvance
	var seen context.Context
	floorAdvance = func(got context.Context, f packload.Fork, out io.Writer, installed *installedCopy, act *run.ActInterrupt) advanceResult {
		seen = got
		return advanceResult{}
	}
	t.Cleanup(func() { floorAdvance = previousAdvance })
	state := floor.ResolvePatched(ctx, p, nil, true)
	if state.Recipe == "" || seen != ctx {
		t.Fatalf("ordinary advance did not receive the preparation context: seen=%v state=%+v", seen, state)
	}
}

func TestDeferredApplyFloorRetainsCachedResolverWithoutOrdinaryAdvance(t *testing.T) {
	t.Setenv("YOLO_ALLOW_PATCH_FAILURES", "")
	fx := newFloorPatchedFixture(t)
	selection := selectConfiguredHostPacks()
	gitLog := patchedGitWrapper(t, "")
	if err := os.WriteFile(gitLog, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	baselineGitLog, err := os.ReadFile(gitLog)
	if err != nil {
		t.Fatal(err)
	}
	beforeBuilds := len(fx.builds)
	previousFloor := newHostFloor
	var preparedFloor *hostfloor.Floor
	newHostFloor = func(out io.Writer, selected []hostfloor.Program) *hostfloor.Floor {
		preparedFloor = productionHostFloor(out, selected)
		preparedFloor.CaptureUnavailable = nil
		return preparedFloor
	}
	t.Cleanup(func() { newHostFloor = previousFloor })
	previousAdvance := floorAdvance
	advances := 0
	floorAdvance = func(ctx context.Context, f packload.Fork, out io.Writer, installed *installedCopy, act *run.ActInterrupt) advanceResult {
		advances++
		return previousAdvance(ctx, f, out, installed, act)
	}
	t.Cleanup(func() { floorAdvance = previousAdvance })
	var output strings.Builder
	survey := &hostApplySurvey{advanceDeferred: "the deferred apply act builds none of the patched programs"}
	rc := applyHostFloor(richtext.Printer{W: &output}, &output, selection.packs, true, false, survey)
	if rc != 0 || advances != 0 || preparedFloor == nil || preparedFloor.Advance != nil || preparedFloor.ResolvePatched == nil ||
		preparedFloor.NoAdvance != survey.advanceDeferred {
		t.Fatalf("deferred real apply did not retain cached-only resolver policy: rc=%d advances=%d floor=%+v\n%s",
			rc, advances, preparedFloor, output.String())
	}
	commands, err := os.ReadFile(gitLog)
	if err != nil {
		t.Fatal(err)
	}
	if len(fx.builds) != beforeBuilds || string(commands) != string(baselineGitLog) {
		t.Fatalf("deferred apply performed an ordinary check/build: builds=%d/%d git=%q", len(fx.builds), beforeBuilds, commands)
	}
}

func rewriteSelectedBuildReceipt(t *testing.T, entryKey string, change func(*entrypoint.BuildReceipt)) {
	t.Helper()
	store := &capture.Store{Dir: paths.CapturesDir()}
	entry, err := store.Resolve(entryKey)
	if err != nil {
		t.Fatalf("resolve fixture's admitted entry: %v", err)
	}
	path := capture.ReceiptsPath(entry.Root)
	builds, err := entrypoint.ReadBuildReceipts(path)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	index := 0
	changed := 0
	for i, line := range lines {
		var head struct {
			Kind string `json:"kind"`
		}
		if err := json.Unmarshal([]byte(line), &head); err != nil || head.Kind != entrypoint.ReceiptKindBuild {
			continue
		}
		if index >= len(builds) {
			t.Fatalf("receipt/build reader order diverged at line %d", i)
		}
		r := builds[index]
		index++
		if r.Key != entryKey {
			continue
		}
		change(&r)
		lines[i] = r.Line()
		changed++
	}
	if changed == 0 {
		t.Fatalf("no build receipt for selected entry %s", entryKey)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestProductionFloorSuccessfulLocalFailureStopsBeforeOrdinaryAdvance(t *testing.T) {
	t.Setenv("YOLO_ALLOW_PATCH_FAILURES", "1")
	fx := newFloorPatchedFixture(t)
	_, fork := prepareFloorOpaqueApplyErr(t, fx, "v1.2.0", map[int]string{14: "fourteen", 11: "eleven"})
	floor, p, _ := selectedProductionFloor(t)
	initial := floor.Patched(p)
	if initial.PatchFailure != nil || initial.Good == nil {
		t.Fatalf("fixture did not start with an offline Good and no classified failure: %+v", initial)
	}
	previousAdvance := floorAdvance
	advances := 0
	floorAdvance = func(ctx context.Context, f packload.Fork, out io.Writer, installed *installedCopy, act *run.ActInterrupt) advanceResult {
		advances++
		return previousAdvance(ctx, f, out, installed, act)
	}
	t.Cleanup(func() { floorAdvance = previousAdvance })
	state := floor.ResolvePatched(context.Background(), p, nil, true)
	var failure *packsrc.PatchFailure
	if state.PatchFailure == nil || !errors.As(state.PatchFailure, &failure) || failure.Owner != fork.Key() || failure.Member != "0001-ten.patch" {
		t.Fatalf("successful local classification did not retain its typed failure: %+v", state)
	}
	if advances != 0 {
		t.Fatalf("a classified local failure fell through to another floor advance: advances=%d state=%+v", advances, state)
	}
}

func TestProductionFloorPreparationDecidesClassifiedFailureBypass(t *testing.T) {
	for _, tc := range []struct {
		name          string
		allowFailures string
		wantRefusal   bool
	}{
		{name: "default refuses", wantRefusal: true},
		{name: "literal bypass keeps compatible copy", allowFailures: "1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("YOLO_ALLOW_PATCH_FAILURES", tc.allowFailures)
			fx := newFloorPatchedFixture(t)
			prepareFloorOpaqueApplyErr(t, fx, "v1.2.0", map[int]string{14: "fourteen", 11: "eleven"})
			floor, p, _ := selectedProductionFloor(t)
			previousAdvance := floorAdvance
			advances := 0
			floorAdvance = func(ctx context.Context, f packload.Fork, out io.Writer, installed *installedCopy, act *run.ActInterrupt) advanceResult {
				advances++
				return previousAdvance(ctx, f, out, installed, act)
			}
			t.Cleanup(func() { floorAdvance = previousAdvance })
			preparation, err := floor.PreparePatched(context.Background(), p, true)
			var failure *packsrc.PatchFailure
			if tc.wantRefusal {
				if err == nil || !errors.As(err, &failure) || failure.Member != "0001-ten.patch" {
					t.Fatalf("default policy did not refuse the classified failure: failure=%+v err=%v", failure, err)
				}
			} else {
				if err != nil || preparation == nil {
					t.Fatalf("literal bypass did not prepare the compatible installed copy: preparation=%+v err=%v", preparation, err)
				}
				_, outcome, ensureErr := floor.EnsurePrepared(context.Background(), p, preparation)
				if ensureErr != nil || outcome != hostfloor.Current {
					t.Fatalf("literal bypass did not keep the intact current copy: outcome=%v err=%v", outcome, ensureErr)
				}
			}
			if advances != 0 || len(fx.builds) != 1 {
				t.Fatalf("classified failure caused another advance/build: advances=%d builds=%d", advances, len(fx.builds))
			}
		})
	}
}

func prunedGoodWithValidNewerNeighbor(t *testing.T) (*patchedAdvanceFixture, hostfloor.Program, *hostfloor.Floor, *packsrc.GoodBuild, *capture.Entry) {
	t.Helper()
	fx := newFloorPatchedFixture(t)
	good := fx.record(t).Good
	if good == nil || good.Entry == "" {
		t.Fatalf("fixture has no admitted Good build: %+v", good)
	}
	floor, p, _ := selectedProductionFloor(t)
	neighbor := admitBuildReceipt(t, p, good, good.Commit, good.Recipe, fx.now.Add(time.Hour), good.Tag, good.Version)
	assertNewerNeighborWinsCaptureSelection(t, p, neighbor)
	if neighbor.Key == good.Entry {
		t.Fatalf("valid neighboring build reused the selected Good entry %s", good.Entry)
	}
	removeStoreEntry(t, good.Entry)
	return fx, p, floor, good, neighbor
}

func TestProductionFloorPrunedGoodEntryKeepsInstalledDelivery(t *testing.T) {
	t.Setenv("YOLO_ALLOW_PATCH_FAILURES", "")
	_, p, floor, good, neighbor := prunedGoodWithValidNewerNeighbor(t)
	state := floor.Patched(p)
	if state.AuthorityError != "" || state.Good == nil || state.Good.Entry != nil || state.Good.Commit != good.Commit || state.Good.Recipe != good.Recipe {
		t.Fatalf("absent selected store entry became authority failure or was substituted by its valid neighbor: good=%s neighbor=%s state=%+v",
			good.Entry, neighbor.Key, state)
	}
	preparation, err := floor.PreparePatched(context.Background(), p, false)
	if err != nil || preparation == nil {
		t.Fatalf("pruned store entry blocked the intact installed copy: preparation=%+v err=%v", preparation, err)
	}
	_, outcome, err := floor.EnsurePrepared(context.Background(), p, preparation)
	if err != nil || outcome != hostfloor.Current {
		t.Fatalf("pruned store entry did not retain current installed delivery: outcome=%v err=%v", outcome, err)
	}
}

func TestProductionFloorLiteralBypassKeepsInstalledCopyAfterGoodEntryPruned(t *testing.T) {
	t.Setenv("YOLO_ALLOW_PATCH_FAILURES", "1")
	fx, p, floor, good, neighbor := prunedGoodWithValidNewerNeighbor(t)
	fx.commit(t, "v1.2.0", map[int]string{14: "fourteen", 11: "eleven"})
	fx.later(2 * time.Hour)
	rc, target, output := fx.hostLaunch(t)
	if rc != 0 || target != filepath.Join(paths.HostFloorDir(), "bin", "tool") {
		t.Fatalf("literal bypass did not keep the installed copy after a real local conflict: rc=%d target=%s\n%s", rc, target, output)
	}
	state := floor.Patched(p)
	if state.AuthorityError != "" || state.PatchFailure == nil || state.Good == nil || state.Good.Entry != nil || state.Good.Commit != good.Commit {
		t.Fatalf("local failure or original pruned Good identity was lost/substituted: good=%s neighbor=%s state=%+v",
			good.Entry, neighbor.Key, state)
	}
	preparation, err := floor.PreparePatched(context.Background(), p, false)
	if err != nil || preparation == nil {
		t.Fatalf("literal bypass did not prepare the compatible installed copy: preparation=%+v err=%v", preparation, err)
	}
	_, outcome, err := floor.EnsurePrepared(context.Background(), p, preparation)
	if err != nil || outcome != hostfloor.Current || len(fx.builds) != 1 {
		t.Fatalf("bypass changed the current delivery or built again: outcome=%v builds=%d err=%v", outcome, len(fx.builds), err)
	}
}

func TestProductionFloorLiteralBypassRefusesPrunedStoreOnlyGood(t *testing.T) {
	t.Setenv("YOLO_ALLOW_PATCH_FAILURES", "")
	fx, _, _, good, neighbor := prunedGoodWithValidNewerNeighbor(t)
	fx.commit(t, "v1.2.0", map[int]string{14: "fourteen", 11: "eleven"})
	fx.later(2 * time.Hour)
	rc, _, output := fx.hostLaunch(t)
	if rc == 0 {
		t.Fatalf("default policy unexpectedly delivered the typed local failure before literal bypass: %s", output)
	}
	if fx.record(t).PatchFailure == nil {
		t.Fatalf("fixture did not persist the real classified local failure: %+v", fx.record(t))
	}
	if err := os.RemoveAll(paths.HostFloorDir()); err != nil {
		t.Fatal(err)
	}
	t.Setenv("YOLO_ALLOW_PATCH_FAILURES", "1")
	floor, p, _ := selectedProductionFloor(t)
	state := floor.Patched(p)
	if state.AuthorityError != "" || state.PatchFailure == nil || state.Good == nil || state.Good.Entry != nil || state.Good.Commit != good.Commit {
		t.Fatalf("store-only fixture lost the selected failure/Good identity or substituted its valid neighbor: good=%s neighbor=%s state=%+v",
			good.Entry, neighbor.Key, state)
	}
	preparation, err := floor.PreparePatched(context.Background(), p, false)
	var failure *packsrc.PatchFailure
	if err == nil || !errors.As(err, &failure) || failure.Member != "0001-ten.patch" {
		t.Fatalf("literal bypass waived a missing store-only selected build: preparation=%+v failure=%+v err=%v", preparation, failure, err)
	}
	if _, statErr := os.Stat(paths.HostFloorDir()); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("failed store-only bypass wrote into the host floor: stat err=%v", statErr)
	}
}

func TestProductionFloorSpecifiedBuildEntryRequiresAnExactReceipt(t *testing.T) {
	for _, field := range []string{"fork", "bin", "platform", "source", "revision", "recipe", "near-fit", "unpatched"} {
		t.Run(field, func(t *testing.T) {
			t.Setenv("YOLO_ALLOW_PATCH_FAILURES", "")
			fx := newFloorPatchedFixture(t)
			good := fx.record(t).Good
			if good == nil || good.Entry == "" {
				t.Fatalf("fixture lacks specified Good entry: %+v", good)
			}
			floor, p, _ := selectedProductionFloor(t)
			neighbor := admitBuildReceipt(t, p, good, good.Commit, good.Recipe, fx.now.Add(time.Hour), good.Tag, good.Version)
			assertNewerNeighborWinsCaptureSelection(t, p, neighbor)
			if neighbor.Key == good.Entry {
				t.Fatalf("valid receipt neighbor reused the selected Good entry %s", good.Entry)
			}
			rewriteSelectedBuildReceipt(t, good.Entry, func(r *entrypoint.BuildReceipt) {
				switch field {
				case "fork":
					r.Fork = "otherfork/tool"
				case "bin":
					r.Bin = "other-bin"
				case "platform":
					r.Platform = "unsupported/arch"
				case "source":
					r.Source = "git+file:///other/source"
				case "revision":
					r.Revision = "not-the-good-commit"
				case "recipe":
					r.Recipe = "not-the-current-recipe"
				case "near-fit":
					r.Recipe = good.Recipe + "-near-fit"
				case "unpatched":
					r.Fork, r.Series, r.Tree = "", "", ""
				}
			})
			state := floor.Patched(p)
			if state.Good != nil || state.AuthorityError == "" {
				t.Fatalf("selected receipt mismatch was trusted or replaced: state=%+v", state)
			}
		})
	}
}

func copyAdmittedTree(t *testing.T, source, destination string) {
	t.Helper()
	if err := filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, relative)
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(link, target)
		case info.IsDir():
			return os.MkdirAll(target, info.Mode().Perm()|0o700)
		case info.Mode().IsRegular():
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			return os.WriteFile(target, data, info.Mode().Perm())
		default:
			return fmt.Errorf("unsupported fixture entry %s with mode %s", path, info.Mode())
		}
	}); err != nil {
		t.Fatalf("copy the admitted build tree into its valid neighboring entry: %v", err)
	}
}

func TestProductionFloorUsesSpecifiedExactEntryBesideANewerUnrelatedBuild(t *testing.T) {
	t.Setenv("YOLO_ALLOW_PATCH_FAILURES", "")
	fx := newFloorPatchedFixture(t)
	old := fx.record(t).Good
	if old == nil || old.Entry == "" {
		t.Fatalf("fixture lacks old Good entry: %+v", old)
	}
	floor, p, _ := selectedProductionFloor(t)
	neighbor := admitBuildReceipt(t, p, old, "newer-unrelated-revision", old.Recipe, fx.now.Add(time.Hour), "v9.9.9", "9.9.9")
	assertNewerNeighborWinsCaptureSelection(t, p, neighbor)
	if neighbor.Key == old.Entry {
		t.Fatalf("neighbor build reused the selected Good entry %s", old.Entry)
	}
	state := floor.Patched(p)
	if state.AuthorityError != "" || state.Good == nil || state.Good.Entry == nil || state.Good.Entry.Key != old.Entry {
		t.Fatalf("resolver substituted a newer unrelated entry instead of the specified Good: old=%s neighbor=%s state=%+v",
			old.Entry, neighbor.Key, state)
	}
}

func admitBuildReceipt(t *testing.T, p hostfloor.Program, good *packsrc.GoodBuild, revision, recipe string, at time.Time, tag, version string) *capture.Entry {
	t.Helper()
	store := &capture.Store{Dir: paths.CapturesDir()}
	staged, err := store.Stage("floor-unrelated-neighbor")
	if err != nil {
		t.Fatal(err)
	}
	selected, err := store.Resolve(good.Entry)
	if err != nil {
		t.Fatalf("resolve source for valid neighboring build: %v", err)
	}
	tree := capture.TreeDir(staged)
	if err := os.MkdirAll(tree, 0o700); err != nil {
		t.Fatal(err)
	}
	copyAdmittedTree(t, selected.Tree, tree)
	markerPath := ".local/share/floor-neighbor-marker"
	markerBytes := []byte("neighbor build metadata\n")
	markerFile := filepath.Join(tree, filepath.FromSlash(markerPath))
	if err := os.MkdirAll(filepath.Dir(markerFile), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(markerFile, markerBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	manifest, err := capture.ReadManifest(selected.Root)
	if err != nil {
		t.Fatalf("read original admitted build manifest: %v", err)
	}
	manifest.Entries = append(manifest.Entries, capture.ManifestEntry{
		Path: markerPath, Kind: capture.KindFile, Mode: "0600", Size: int64(len(markerBytes)),
	})
	sort.Slice(manifest.Entries, func(i, j int) bool { return manifest.Entries[i].Path < manifest.Entries[j].Path })
	if err := capture.WriteManifest(staged, manifest); err != nil {
		t.Fatal(err)
	}
	entryKey, err := capture.KeyForTree(tree)
	if err != nil {
		t.Fatal(err)
	}
	fork := floorForkBuild(p, "").Fork
	receipt := entrypoint.BuildReceipt{
		Bin: p.Bin(), Source: patchedBuildSource(fork.Source), Key: entryKey,
		Bytes: -1, Path: store.EntryDir(entryKey), Platform: floorPatchedPlatform(), Revision: revision,
		Recipe: recipe, Fork: fork.Key(), Series: good.Series, Tag: tag, Version: version,
		Act: entrypoint.ReceiptActRecord, Time: at,
	}
	if err := os.WriteFile(capture.ReceiptsPath(staged), []byte(receipt.Line()+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	entry, err := store.AdmitEntry(staged)
	if err != nil {
		t.Fatal(err)
	}
	if entry.Key != entryKey || !entry.Complete() {
		t.Fatalf("neighbor admission did not create the expected complete entry: entry=%+v expected=%s", entry, entryKey)
	}
	admittedManifest, err := capture.ReadManifest(entry.Root)
	if err != nil {
		t.Fatalf("neighbor manifest is unreadable: %v", err)
	}
	if len(admittedManifest.Entries) != len(manifest.Entries) {
		t.Fatalf("neighbor manifest lost entries: got %d want %d", len(admittedManifest.Entries), len(manifest.Entries))
	}
	records, err := captureRecords(entry.Root)
	if err != nil {
		t.Fatalf("neighbor receipt is unreadable: %v", err)
	}
	recordMatches, executableExists := false, false
	for _, record := range records {
		if record.Fork == fork.Key() && record.Bin == p.Bin() && record.Platform == floorPatchedPlatform() &&
			record.Source == patchedBuildSource(fork.Source) && record.Revision == revision && record.Recipe == recipe {
			recordMatches = true
		}
	}
	for _, artifact := range admittedManifest.Entries {
		mode, err := strconv.ParseUint(strings.TrimPrefix(artifact.Mode, "0"), 8, 32)
		if artifact.Kind != capture.KindFile || err != nil || mode&0o111 == 0 {
			continue
		}
		info, err := os.Stat(filepath.Join(entry.Tree, filepath.FromSlash(artifact.Path)))
		if err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0 {
			executableExists = true
			break
		}
	}
	if !recordMatches || !executableExists {
		t.Fatalf("neighbor is not an admitted runnable build with a matching record receipt: match=%v executable=%v entry=%+v", recordMatches, executableExists, entry)
	}
	return entry
}

func assertNewerNeighborWinsCaptureSelection(t *testing.T, p hostfloor.Program, entry *capture.Entry) {
	t.Helper()
	fork := floorForkBuild(p, "").Fork
	selected, err := capture.Select(&capture.Store{Dir: paths.CapturesDir()}, captureRecords)
	if err != nil {
		t.Fatal(err)
	}
	program := capture.Program{Bin: p.Bin(), Platform: floorPatchedPlatform(), Fork: fork.Key()}
	winner, ok := selected[program]
	if !ok || winner.Key != entry.Key {
		t.Fatalf("valid newer neighbor did not win the capture selector: winner=%+v entry=%s", winner, entry.Key)
	}
}
