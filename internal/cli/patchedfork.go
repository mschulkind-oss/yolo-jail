package cli

// patchedfork.go is the explicit acts' half of a PATCHED FORK (docs/design/patched-forks.md §8.3,
// PF-D12): `yolo pack update` and `yolo pack install` force its check and replay its series down
// the walk's list, record each outcome, and report the candidate and whether the series replays
// there; `yolo pack status` reports the same from the check record, offline. None of them builds,
// and none moves the good build (P2: only an admitted build may).
//
// A patched fork has NO PIN (PF-D16): forks.lock.json holds no entry for it, its check record is
// machine-local in the pack store (packsrc.CheckRecord), and pinForks drops a plain fork's entry
// left under its key from before a migration.
//
// THE TERMS here are the design's: the CHECK reads the upstream for a newer candidate; the WALK'S
// LIST is the entries an advance may build, newest first; the REPLAY makes the series commits at
// its base and picks them onto an entry; the NEWEST FIT is the first entry the series replays onto
// cleanly; the GOOD BUILD is this machine's last admitted build. HELD means what runs is not
// following (§3.3).

import (
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/capture"
	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
	"github.com/mschulkind-oss/yolo-jail/internal/version"
)

// patchedYoloVersion is the yolo a recorded outcome is keyed by, so another yolo replays a
// conflict again (PF-D9): the stamped version and the commit it was built from. A var so a test can
// stand in a second yolo.
var patchedYoloVersion = func() string { return version.Baked() + "@" + version.GitCommit }

// patchedNow is the clock the explicit acts check and record by; a var for tests.
var patchedNow = time.Now

// patchedNotBuilt is what follows a clean replay in an explicit act's lines (§8.3: "applies; the
// next launch builds it"): the explicit acts build nothing and never move the good build (P2), and
// a fresh jail launch's advance builds the candidate (patchedadvance.go). It said that this yolo
// built nothing while only step 1 of §14 was in (PF-D35).
const patchedNotBuilt = "the next fresh launch builds it"

// patchedForkStore is the pack store the verbs check through: the user's own terminal, so not the
// launch's detached store, and the store's default budget. A var so a test can hand it a git of its
// own, as patchedAdvanceStore's tests do.
var patchedForkStore = func() *packsrc.Store { return &packsrc.Store{Dir: paths.PacksDir()} }

// explicitPostRetryRecordLoad is the actual post-retry reread. Its production default is the
// unchanged loader; tests replace it only to order a later check immediately before that read.
var explicitPostRetryRecordLoad = run.LoadPatchedRecord

// patchedForkHold is what holds a patched fork's upstream at the good build, "" when nothing does:
// `agent_updates` off for the fork pack or for its base (PF-D19), as a launch reads it.
func patchedForkHold(f packload.Fork) string { return run.PatchedForkHold(f) }

// checkPatchedForks is `yolo pack install`'s and `yolo pack update`'s arm for every patched fork in
// forks: update checks and replays each one; install only those with no good build on this
// machine, as install leaves a pinned plain fork alone. Returns the arm's exit status.
func checkPatchedForks(pr richtext.Printer, errw io.Writer, forks []packload.Fork, update bool) int {
	rc := 0
	store := patchedForkStore()
	for _, f := range forks {
		if !f.FollowsUpstream() {
			continue
		}
		if !update {
			if series, err := f.ReadSeries(); err == nil {
				if rec, err := run.LoadPatchedRecord(store, f, series); err == nil && rec != nil && rec.Good != nil {
					inputs, _, _, inputErr := f.CheckWant(series).Inputs()
					if inputErr == nil {
						if failure := rec.CurrentPatchFailure(inputs, series.Digest); failure != nil {
							if n := reportExplicitPatchFailure(pr, errw, f, series, rec, failure, 0); n != 0 {
								rc = 1
							}
							continue
						}
						pr.Printf("[dim]%s unchanged (good build %s) — `yolo pack update` checks its upstream[/dim]",
							f.Key(), goodLabel(rec.Good))
						continue
					}
				}
			}
		}
		if n := checkPatchedFork(pr, errw, store, f); n != 0 {
			rc = 1
		}
	}
	return rc
}

// checkPatchedFork is one patched fork's explicit check and walk.
func checkPatchedFork(pr richtext.Printer, errw io.Writer, store *packsrc.Store, f packload.Fork) int {
	series, err := f.ReadSeries()
	if err != nil {
		fmt.Fprintf(errw, "yolo pack: %s: %v\n", f.Label(), err)
		return 1
	}
	res := store.CheckPatched(f.CheckWant(series), packsrc.CheckOptions{Force: true, Now: patchedNow,
		Begin: func() (func(string), func()) {
			pr.Printf("[dim]checking %s's upstream %s[/dim]", f.Label(), f.Source)
			return func(line string) { pr.Printf("[dim]%s[/dim]", line) }, func() {}
		}})
	if res.Err != nil {
		fmt.Fprintf(errw, "yolo pack: %s: %v\n", f.Label(), res.Err)
		return 1
	}
	if res.Record == nil || res.Record.Check == nil {
		fmt.Fprintf(errw, "yolo pack: %s: the explicit check returned no original check record\n", f.Label())
		return 1
	}
	original := res.Record
	originalSnapshot, err := original.ReplaySnapshot(res.Inputs, series.Digest)
	if err != nil {
		fmt.Fprintf(errw, "yolo pack: %s: binding the explicit replay to its original check: %v\n", f.Label(), err)
		return 1
	}
	rec := run.RekeyLegacyGood(store, f, series, original)
	found := original.Check
	rc := 0
	if found.FetchErr != "" {
		fmt.Fprintf(errw, "yolo pack: %s: could not fetch %s (%s) — using this machine's copy\n",
			f.Label(), f.Source, found.FetchErr)
		rc = 1
	}
	failure := rec.CurrentPatchFailure(res.Inputs, series.Digest)
	if found.Problem != "" {
		fmt.Fprintf(errw, "yolo pack: %s: %s\n", f.Label(), found.Problem)
		if failure != nil {
			return reportExplicitPatchFailure(pr, errw, f, series, rec, failure, 1)
		}
		return 1
	}
	if found.NoVersion != "" {
		// A RELEASE RULE OVER A BRANCH WITH NO VERSION TAG (PF-D60): an empty list, and where it stays.
		pr.Printf("[yellow]⚠ %s[/yellow]", richtext.Escape(f.Label()+": "+found.NoVersionLine(stayAt(rec, series))))
	}

	var spent time.Duration
	var retried *packsrc.ListEntry
	var retryAtBase bool
	var retrySnapshot *packsrc.ReplaySnapshot
	if failure != nil {
		if found.FetchErr != "" {
			return reportExplicitPatchFailure(pr, errw, f, series, rec, failure, rc)
		}
		target, atBase, ok := explicitFailureTarget(failure, rec, found, series)
		if !ok {
			return reportExplicitPatchFailure(pr, errw, f, series, rec, failure, rc)
		}
		var retryReportedFailure *packsrc.PatchFailure
		walk, recorded, elapsed := recordExplicitWalk(store, f, series, res.Inputs,
			[]packsrc.ListEntry{target}, patchedYoloVersion(), 0, originalSnapshot,
			func(failure *packsrc.PatchFailure) {
				retryReportedFailure = failure
				writePatchFailure(errw, failure, f.Key(), f.Bin, false, "")
			}, func(err error) {
				if err != nil {
					fmt.Fprintf(errw, "yolo pack: %s: its clean replay was recorded, but the matching detached failure evidence could not be resolved (%v) — the next launch checks it again\n",
						f.Label(), err)
				}
			})
		spent += elapsed
		if errors.Is(recorded.Err, packsrc.ErrStaleReplay) {
			fresh, readErr := store.LoadCheckRecord(f.Key())
			if readErr != nil {
				fmt.Fprintf(errw, "yolo pack: %s: replay authority changed and the current check record could not be read: %v\n",
					f.Label(), readErr)
				return 1
			}
			fmt.Fprintf(errw, "yolo pack: %s: recording the replay: %v\n", f.Label(), recorded.Err)
			if current := fresh.CurrentPatchFailure(res.Inputs, series.Digest); current != nil {
				return reportExplicitPatchFailure(pr, errw, f, series, fresh, current, rc,
					retryReportedFailure != nil && reflect.DeepEqual(retryReportedFailure, current))
			}
			fmt.Fprintf(errw, "yolo pack: %s: replay authority changed during its forced check; run `yolo pack update` again\n", f.Label())
			return 1
		}
		if recorded.Err != nil {
			fmt.Fprintf(errw, "yolo pack: %s: recording the replay: %v\n", f.Label(), recorded.Err)
			rc = 1
		}
		if failure := explicitWalkFailure(walk, recorded, f, series, res.Inputs, originalSnapshot.Seq); failure != nil {
			return reportExplicitPatchFailure(pr, errw, f, series, rec, failure, rc,
				retryReportedFailure != nil && reflect.DeepEqual(retryReportedFailure, failure))
		}
		if walk.Err != nil || walk.Fit < 0 {
			for _, line := range walkReport(f, series, rec, walk, atBase) {
				pr.Printf("%s", line)
			}
			return 1
		}
		if !recorded.Recorded {
			fresh, readErr := run.LoadPatchedRecord(store, f, series)
			if readErr != nil {
				fmt.Fprintf(errw, "yolo pack: %s: reading the replay result: %v\n", f.Label(), readErr)
				return 1
			}
			if current := fresh.CurrentPatchFailure(res.Inputs, series.Digest); current != nil {
				return reportExplicitPatchFailure(pr, errw, f, series, fresh, current, rc)
			}
			fmt.Fprintf(errw, "yolo pack: %s: the clean retry did not record a changed replay result\n", f.Label())
			return 1
		}
		fresh, readErr := explicitPostRetryRecordLoad(store, f, series)
		if readErr != nil {
			fmt.Fprintf(errw, "yolo pack: %s: reading the replay result: %v\n", f.Label(), readErr)
			return 1
		}
		rec = fresh
		if current := rec.CurrentPatchFailure(res.Inputs, series.Digest); current != nil {
			return reportExplicitPatchFailure(pr, errw, f, series, rec, current, rc, false)
		}
		if !explicitSelectedCheckStillCurrent(fresh, originalSnapshot, found) {
			fmt.Fprintf(errw, "yolo pack: %s: replay authority changed after its successful retry; no later check will be replayed — run `yolo pack update` again\n", f.Label())
			return 1
		}
		nextSnapshot, snapshotErr := fresh.ReplaySnapshot(res.Inputs, series.Digest)
		if snapshotErr != nil {
			fmt.Fprintf(errw, "yolo pack: %s: binding the next replay to its original selected check: %v\n", f.Label(), snapshotErr)
			return 1
		}
		retrySnapshot = &nextSnapshot
		retried, retryAtBase = &target, atBase
	}

	list := rec.Candidates(res.Inputs)
	if retried != nil {
		targetIndex := -1
		for i, entry := range found.List {
			if entry.Commit == retried.Commit {
				targetIndex = i
				break
			}
		}
		filtered := list[:0]
		for _, entry := range list {
			for i, selected := range found.List {
				if selected.Commit == entry.Commit && (targetIndex < 0 && retryAtBase || targetIndex >= 0 && i < targetIndex) {
					filtered = append(filtered, entry)
					break
				}
			}
		}
		list = filtered
	}
	first := rec.Good == nil
	atBase := false
	if len(list) == 0 && first && found.BaseOnBranch && retried == nil {
		// NOTHING ON THE LIST: a series whose base is past the branch's newest version builds the
		// base, which it applies to by construction (§6.4), and nothing is held.
		list, atBase = []packsrc.ListEntry{{Commit: series.Base}}, true
	}
	if len(list) == 0 {
		if retried != nil {
			for _, line := range walkReport(f, series, rec, packsrc.WalkResult{Fit: 0,
				Results: []packsrc.ReplayResult{{Entry: *retried, Clean: true}}}, retryAtBase) {
				pr.Printf("%s", line)
			}
			if hold := patchedForkHold(f); hold != "" {
				pr.Printf("[dim]  %s: no launch checks it, and the good build stays where it is[/dim]", hold)
			}
			return rc
		}
		switch {
		case first:
			fmt.Fprintf(errw, "yolo pack: %s: %s names nothing this series can be built at — the "+
				"branch does not contain the series' base %s; follow the branch the series was made "+
				"on, or hold at a tag or a commit\n", f.Label(), f.Source, shortSHA(series.Base))
			return 1
		default:
			pr.Printf("[dim]%s: nothing upstream is newer than the good build %s[/dim]", f.Label(), goodLabel(rec.Good))
			return rc
		}
	}
	boundSnapshot := originalSnapshot
	if retried != nil {
		if retrySnapshot == nil {
			fmt.Fprintf(errw, "yolo pack: %s: the repaired replay has no renewed snapshot of its original selected check\n", f.Label())
			return 1
		}
		boundSnapshot = *retrySnapshot
	}
	var reportedFailure *packsrc.PatchFailure
	walk, recorded, _ := recordExplicitWalk(store, f, series, res.Inputs, list,
		patchedYoloVersion(), spent, boundSnapshot, func(failure *packsrc.PatchFailure) {
			reportedFailure = failure
			writePatchFailure(errw, failure, f.Key(), f.Bin, false, "")
		}, func(err error) {
			if err != nil {
				fmt.Fprintf(errw, "yolo pack: %s: its clean replay was recorded, but the matching detached failure evidence could not be resolved (%v) — the next launch checks it again\n",
					f.Label(), err)
			}
		})
	if errors.Is(recorded.Err, packsrc.ErrStaleReplay) {
		fresh, readErr := store.LoadCheckRecord(f.Key())
		if readErr != nil {
			fmt.Fprintf(errw, "yolo pack: %s: replay authority changed and the current check record could not be read: %v\n",
				f.Label(), readErr)
			return 1
		}
		fmt.Fprintf(errw, "yolo pack: %s: recording the replay: %v\n", f.Label(), recorded.Err)
		if current := fresh.CurrentPatchFailure(res.Inputs, series.Digest); current != nil {
			return reportExplicitPatchFailure(pr, errw, f, series, fresh, current, rc,
				reportedFailure != nil && reflect.DeepEqual(reportedFailure, current))
		}
		fmt.Fprintf(errw, "yolo pack: %s: replay authority changed during its forced check; run `yolo pack update` again\n", f.Label())
		return 1
	}
	if recorded.Err != nil {
		fmt.Fprintf(errw, "yolo pack: %s: recording the replay: %v\n", f.Label(), recorded.Err)
		rc = 1
	}
	if failure := explicitWalkFailure(walk, recorded, f, series, res.Inputs, rec.Seq); failure != nil {
		return reportExplicitPatchFailure(pr, errw, f, series, rec, failure, rc,
			reportedFailure != nil && reflect.DeepEqual(reportedFailure, failure))
	}
	for _, line := range walkReport(f, series, rec, walk, atBase) {
		pr.Printf("%s", line)
	}
	if walk.Base != nil || walk.Err != nil || walk.Fit < 0 {
		rc = 1
	}
	if hold := patchedForkHold(f); hold != "" {
		pr.Printf("[dim]  %s: no launch checks it, and the good build stays where it is[/dim]", hold)
	}
	return rc
}

func explicitWalkFailure(walk packsrc.WalkResult, recorded packsrc.RecordReplayResult, f packload.Fork,
	series *packsrc.Series, inputs packsrc.CheckInputs, seq int64) *packsrc.PatchFailure {
	failure := recorded.Failure
	if failure == nil {
		failure = walk.PatchFailure()
	}
	if failure != nil {
		failure.Owner, failure.Inputs, failure.Series, failure.Seq = f.Key(), inputs, series.Digest, seq
	}
	return failure
}

func explicitFailureTarget(failure *packsrc.PatchFailure, rec *packsrc.CheckRecord, check *packsrc.CheckFound, series *packsrc.Series) (packsrc.ListEntry, bool, bool) {
	if failure.Kind == "base" {
		if rec.Good == nil && check != nil && check.BaseOnBranch {
			return packsrc.ListEntry{Commit: series.Base}, true, true
		}
		return packsrc.ListEntry{}, false, false
	}
	if check != nil {
		for _, entry := range check.List {
			if entry.Commit == failure.Target.Commit {
				return entry, false, true
			}
		}
	}
	return packsrc.ListEntry{}, false, false
}

func explicitSelectedCheckStillCurrent(record *packsrc.CheckRecord, selected packsrc.ReplaySnapshot,
	check *packsrc.CheckFound) bool {
	return record != nil && record.Owner == selected.Owner && record.Seq == selected.Seq &&
		record.Read == selected.Inputs && record.Check != nil && record.Check.Seq == selected.Seq &&
		reflect.DeepEqual(record.Check, check)
}

func recordExplicitWalk(store *packsrc.Store, f packload.Fork, series *packsrc.Series,
	inputs packsrc.CheckInputs, list []packsrc.ListEntry, yolo string, spent time.Duration,
	snapshot packsrc.ReplaySnapshot, beforeRecord func(*packsrc.PatchFailure), afterRecord func(error)) (packsrc.WalkResult, packsrc.RecordReplayResult, time.Duration) {
	if store == nil || snapshot.Owner != f.Key() || snapshot.Series == "" {
		return packsrc.WalkResult{Fit: -1, Err: errors.New("explicit replay has no detached-evidence authority")},
			packsrc.RecordReplayResult{Err: errors.New("explicit replay has no detached-evidence authority")}, 0
	}
	token, tokenErr := observeBackgroundRepairToken(f.Key(), store, snapshot, list, nil, false)
	if tokenErr != nil {
		return packsrc.WalkResult{Fit: -1, Err: fmt.Errorf("reading detached failure evidence before replay: %w", tokenErr)},
			packsrc.RecordReplayResult{Err: fmt.Errorf("reading detached failure evidence before replay: %w", tokenErr)}, 0
	}
	start := time.Now()
	walk := store.WalkSeries(mustRepo(f.Source), subdirOf(f.Source), series, list, packsrc.WalkOptions{
		Snapshot: &snapshot, StopOnPatchFailure: true, Spent: spent,
	})
	elapsed := time.Since(start)
	if failure := walk.PatchFailure(); failure != nil {
		failure.Owner, failure.Inputs, failure.Series, failure.Seq = f.Key(), inputs, series.Digest, snapshot.Seq
		if beforeRecord != nil {
			beforeRecord(failure)
		}
	}
	recorded := store.RecordReplay(snapshot, yolo, walk, patchedNow())
	if recorded.Recorded && recorded.Err == nil {
		if err := recordBackgroundPatchRepair(context.Background(), token, store, snapshot, yolo, walk, recorded, false); err != nil && afterRecord != nil {
			afterRecord(err)
		}
	}
	return walk, recorded, elapsed
}

func reportExplicitPatchFailure(pr richtext.Printer, errw io.Writer, f packload.Fork, series *packsrc.Series,
	rec *packsrc.CheckRecord, failure *packsrc.PatchFailure, rc int, reported ...bool) int {
	alreadyReported := len(reported) > 0 && reported[0]
	admitted := ""
	if allowPatchFailures() {
		admitted = explicitCompatibleGood(f, series, rec)
	}
	if !alreadyReported {
		writePatchFailure(errw, failure, f.Key(), f.Bin, false, admitted)
	} else if admitted != "" {
		fmt.Fprintf(errw, "CONTINUING: using intact admitted build %s; this explicit subject was skipped.\n", admitted)
	}
	if admitted != "" {
		pr.Printf("[dim]%s: skipped this explicit subject; no build or fallback was accepted[/dim]", f.Label())
		return rc
	}
	return 1
}

func explicitCompatibleGood(f packload.Fork, series *packsrc.Series, rec *packsrc.CheckRecord) string {
	if rec == nil || rec.Good == nil || rec.Good.Series != series.Digest ||
		rec.Good.Recipe != run.PatchedRecipe(f, series.Digest) {
		return ""
	}
	runtime := captureRuntime()
	if runtime != "macos-user" {
		runtime = ""
	}
	if _, _, err := resolvePatchedBuild(&capture.Store{Dir: paths.CapturesDir()}, f.Key(), f.Bin,
		forkBuildPlatform(runtime), patchedBuildSource(f.Source), rec.Good.Commit, rec.Good.Recipe); err != nil {
		return ""
	}
	return goodLabel(rec.Good)
}

// walkReport is the lines an explicit act prints for a walk: the candidate and whether the series
// replays there, each member already upstream, the conflict message (§8.2) for every entry that
// did not take the series, and the newest fit. atBase is a walk of the first advance's fallback, the
// series' base alone, which is named as that rather than as an upstream version.
func walkReport(f packload.Fork, series *packsrc.Series, rec *packsrc.CheckRecord, w packsrc.WalkResult,
	atBase bool) []string {
	var lines []string
	var old *packsrc.GitTooOldError
	switch {
	case w.Base != nil:
		return []string{fmt.Sprintf("[yellow]⚠ %s: %s[/yellow]", f.Label(), w.Base.Error())}
	case errors.As(w.Err, &old):
		// A GIT TOO OLD FOR THE REPLAY (PF-D58): a retry meets the same git, so the step is updating it.
		return []string{fmt.Sprintf("[yellow]⚠ %s: %s, and this host's git is %s — update git, then run `yolo "+
			"pack update` again[/yellow]", f.Label(), old.Need(), old.Have)}
	case w.Err != nil:
		return []string{fmt.Sprintf("[yellow]⚠ %s: could not replay the series: %v — `yolo pack "+
			"update` retries[/yellow]", f.Label(), w.Err)}
	}
	var fit *packsrc.ReplayResult
	if w.Fit >= 0 {
		fit = &w.Results[w.Fit]
	}
	in, _, _, _ := f.CheckWant(series).Inputs()
	for i, r := range w.Results {
		switch {
		case r.Conflict != nil:
			lines = append(lines, conflictMessage(f, series, rec, r, fit,
				isRebaseDefault(rec, in, series.Digest, r.Entry))...)
		case r.Clean:
			for _, m := range r.Upstream {
				lines = append(lines, fmt.Sprintf("[dim]  %s is already in upstream %s; drop it from %s[/dim]",
					m, r.Entry.Label(), series.Dir))
			}
			what := "upstream " + r.Entry.Label()
			switch {
			case atBase:
				what = "the series' base " + r.Entry.Label() + " (no version of the branch contains it)"
			case i > 0:
				what = "the newest fit, " + what + ","
			}
			lines = append(lines, fmt.Sprintf("[green]%s[/green]: %s takes the series (%d %s, series %s): "+
				"applies — %s", f.Label(), what, series.Len(), plural(series.Len(), "patch", "patches"),
				series.ShortDigest(), patchedNotBuilt))
		case r.Err != nil:
			lines = append(lines, fmt.Sprintf("[yellow]⚠ %s: could not replay the series onto %s: %v — "+
				"`yolo pack update` retries[/yellow]", f.Label(), r.Entry.Label(), r.Err))
		}
	}
	if w.Fit < 0 && w.Err == nil && len(w.Results) > 0 && w.Results[len(w.Results)-1].Err == nil {
		lines = append(lines, fmt.Sprintf("[yellow]⚠ %s: no upstream version on the list takes the "+
			"series[/yellow] — %s", f.Label(), heldAt(rec, series, nil)))
	}
	return lines
}

// conflictMessage is §8.2's message for one entry the series did not take; fit is the walk's newest
// fit, nil when it found none. isDefault is whether the entry is the one `yolo pack rebase` takes
// with no --onto (isRebaseDefault); another's step names it with --onto.
func conflictMessage(f packload.Fork, series *packsrc.Series, rec *packsrc.CheckRecord, r packsrc.ReplayResult,
	fit *packsrc.ReplayResult, isDefault bool) []string {
	paths := strings.Join(r.Conflict.Paths, ", ")
	if paths == "" {
		paths = "(no path named)"
	}
	return []string{
		fmt.Sprintf("[yellow]%s: upstream %s does not take the patch series —[/yellow]", f.Label(), r.Entry.Label()),
		fmt.Sprintf("  %s conflicts in %s", r.Conflict.Member, paths),
		"  " + heldAt(rec, series, fit),
		"  rebase the series: " + rebaseCommand(f, r.Entry, isDefault),
	}
}

// rebaseCommand is the conflict message's next step (§8.2): `yolo pack rebase <key>` (§8.4, PF-D26),
// which rebases onto its default target (isDefault, rebaseDefaultTarget); for another entry,
// `--onto` it. It is the only step a conflict names: the verb clones, replays to the conflict and
// prints the continue and the export with the commits filled in (patchedrebase.go).
func rebaseCommand(f packload.Fork, onto packsrc.ListEntry, isDefault bool) string {
	cmd := "yolo pack rebase " + shquote.QuoteDisplay(f.Key())
	if !isDefault {
		ref := onto.Tag
		if ref == "" {
			ref = onto.Commit
		}
		cmd += " --onto " + shquote.QuoteDisplay(ref)
	}
	return cmd
}

// heldAt says what runs while the newest candidate does not fit: the good build; with none, the
// first build — the walk's newest fit, else the first advance's fallback to the series' base.
func heldAt(rec *packsrc.CheckRecord, series *packsrc.Series, fit *packsrc.ReplayResult) string {
	if rec != nil && rec.Good != nil {
		return fmt.Sprintf("still running %s + %d %s", goodLabel(rec.Good), rec.Good.Patches,
			plural(rec.Good.Patches, "patch", "patches"))
	}
	if fit != nil {
		return "nothing runs yet: the first build is the newest fit, " + fit.Entry.Label()
	}
	if rec != nil && rec.Check != nil && rec.Check.BaseOnBranch {
		return "nothing runs yet: the first build is the series' base " + shortSHA(series.Base)
	}
	return "nothing runs yet, and nothing on the list takes the series"
}

// stayAt is where a fork whose release rule finds no version tag stays (PF-D60), as its note names
// it: the good build, or with none the series' base, which a first advance builds.
func stayAt(rec *packsrc.CheckRecord, series *packsrc.Series) string {
	if rec != nil && rec.Good != nil {
		return "the good build " + goodLabel(rec.Good)
	}
	return "the series' base " + shortSHA(series.Base)
}

// goodLabel is a good build as a line names it: its tag and short commit, or the commit alone.
func goodLabel(g *packsrc.GoodBuild) string {
	return packsrc.ListEntry{Commit: g.Commit, Tag: g.Tag}.Label()
}

// mustRepo and subdirOf read a fork source's repository and subdirectory; the declaration was
// validated, so a source that does not parse yields "" and the walk names the missing mirror.
func mustRepo(source string) string { return packsrc.CheckRepo(source) }

func subdirOf(source string) string {
	a, err := packsrc.Parse(source)
	if err != nil {
		return ""
	}
	return a.Path
}

// patchedForkStatusLines is `yolo pack status`'s lines for one patched fork, offline: its series,
// the good build and whether the capture store holds it, the candidate and its outcome, what holds
// it — a tag or commit `?ref=`, or `agent_updates` — and when a fresh launch next checks it (§8.3),
// read from the check record, the series and the store, never git.
func patchedForkStatusLines(f packload.Fork) []string {
	series, serr := f.ReadSeries()
	rec, rerr := run.LoadPatchedRecord(patchedForkStore(), f, series) // nil series: no re-key
	var in packsrc.CheckInputs
	if serr == nil {
		in, _, _, _ = f.CheckWant(series).Inputs()
	}
	refKind := patchedRefKind(f, rec, in)
	head := fmt.Sprintf("%-20s patched fork of %s's %s, from %s, ", f.Key(), f.Base, f.Bin, f.Source)
	switch {
	case f.Unmodified():
		head = fmt.Sprintf("%-20s unmodified extension at ~/%s, from %s, ", f.Key(), strings.TrimSuffix(f.Into, "/"), f.Source)
	case f.IsTree():
		head = fmt.Sprintf("%-20s patched extension at ~/%s, from %s, ", f.Key(), strings.TrimSuffix(f.Into, "/"), f.Source)
	}
	switch {
	case refKind == "tag" || refKind == "commit":
		head += "held at that " + refKind
	case refKind == packsrc.RefKindNpmVersion:
		head += "held at that version"
	case f.Npm():
		head += "following the registry's answer for its spec"
	default:
		head += "following " + followLabel(f.Follow)
	}
	if serr != nil {
		return []string{head, "[yellow]  ⚠ " + serr.Error() + "[/yellow]"}
	}
	lines := []string{head}
	if series.Len() > 0 {
		lines = append(lines, fmt.Sprintf("[dim]  series: %d %s in %s (series %s), base %s[/dim]", series.Len(),
			plural(series.Len(), "patch", "patches"), series.Dir, series.ShortDigest(), shortSHA(series.Base)))
	}
	if refKind == "tag" || refKind == "commit" {
		// A HOLD BY THE MANIFEST (§3.4): the ref names a tag or a full commit, so nothing is followed.
		// An unmodified extension has no series to apply there (XB-D49).
		what := "the series is applied there, and it rebuilds only when the series or the recipe changes"
		if f.Unmodified() {
			what = "it is built there, and rebuilds only when its build recipe changes"
		}
		lines = append(lines, "[dim]  held: its ?ref= names a "+refKind+", so it follows nothing: "+what+
			" — a branch as the ?ref= follows one[/dim]")
	}
	if hold := patchedForkHold(f); hold != "" {
		lines = append(lines, "[dim]  held: "+hold+", so no launch checks it[/dim]")
	}
	switch {
	case errors.Is(rerr, packsrc.ErrNoCheckRecord):
		return append(lines, "[dim]  not checked on this machine yet — `yolo pack update` checks it now[/dim]")
	case rerr != nil:
		return append(lines, "[yellow]  ⚠ "+rerr.Error()+" — `yolo pack update` checks it again and starts the "+
			"record over[/yellow]")
	}
	if rec.Good == nil {
		lines = append(lines, "[dim]  good build: none on this machine yet — the next fresh launch builds it[/dim]")
	} else {
		if f.Unmodified() {
			lines = append(lines, fmt.Sprintf("[dim]  good build: %s, %s[/dim]", goodLabel(rec.Good),
				goodBuildStored(f, rec.Good)))
		} else {
			lines = append(lines, fmt.Sprintf("[dim]  good build: %s + %d %s (series %s), %s[/dim]", goodLabel(rec.Good),
				rec.Good.Patches, plural(rec.Good.Patches, "patch", "patches"), shortSHA(rec.Good.Series),
				goodBuildStored(f, rec.Good)))
		}
	}
	lines = append(lines, candidateLines(f, rec, series, in)...)
	return append(lines, nextCheckLine(f, rec, in)...)
}

// goodBuildStored says whether the capture store holds the good build, by the exact lookup (§6.3).
func goodBuildStored(f packload.Fork, g *packsrc.GoodBuild) string {
	platform := captureJailPlatform()
	store := &capture.Store{Dir: paths.CapturesDir()}
	if entry, _, err := resolvePatchedBuild(store, f.Key(), f.Bin, platform, patchedBuildSource(f.Source),
		g.Commit, g.Recipe); err == nil {
		return "built (" + entry.Key + ", " + platform + ")"
	}
	return "not in the capture store for " + platform + " — the next fresh launch builds it again"
}

// nextCheckLine is when a fresh launch next checks the fork's upstream (§8.3), or nothing while a
// hold keeps every launch from checking it.
func nextCheckLine(f packload.Fork, rec *packsrc.CheckRecord, in packsrc.CheckInputs) []string {
	if patchedForkHold(f) != "" || rec.CheckedAt == 0 {
		return nil
	}
	if rec.Settled(in) {
		// RESOLVED FOR GOOD (XB-D37): what it names never moves, so CheckDue never makes another
		// check due, and a time here would be a promise no launch keeps (XB-D49).
		return []string{"[dim]  next check: none — what it names never moves, so no launch checks it " +
			"again; `yolo pack update` checks now[/dim]"}
	}
	if due, _ := packsrc.CheckDue(rec, in, patchedNow(), 0); due {
		return []string{"[dim]  next check: due — the next fresh launch checks it, or `yolo pack update` checks now[/dim]"}
	}
	return []string{fmt.Sprintf("[dim]  next check: in %s, at a fresh launch then, or `yolo pack update` checks now[/dim]",
		patchedAge(rec.NextCheck(0).Sub(patchedNow())))}
}

// patchedRefKind is what the fork's ?ref= names, as far as is known offline: the last check's
// answer when it was a check of in, else "commit" for a full commit id, which can name nothing
// else, and "" (not known until a check) otherwise.
func patchedRefKind(f packload.Fork, rec *packsrc.CheckRecord, in packsrc.CheckInputs) string {
	if rec.Answers(in) && rec.Check.RefKind != "" {
		return rec.Check.RefKind
	}
	if a, err := packsrc.Parse(f.Source); err == nil && isFullCommitID(a.Ref) {
		return "commit"
	}
	return ""
}

// isFullCommitID reports whether ref is a full object id, 40 or 64 hex digits.
func isFullCommitID(ref string) bool {
	if len(ref) != 40 && len(ref) != 64 {
		return false
	}
	return strings.Trim(strings.ToLower(ref), "0123456789abcdef") == ""
}

// patchedAge renders a duration as a status line reads one.
func patchedAge(d time.Duration) string {
	unit := func(n int, one string) string {
		if n == 1 {
			return "1 " + one
		}
		return fmt.Sprintf("%d %ss", n, one)
	}
	switch {
	case d < time.Minute:
		return "under a minute"
	case d < time.Hour:
		return unit(int(d/time.Minute), "minute")
	case d < 48*time.Hour:
		return unit(int(d/time.Hour), "hour")
	}
	return unit(int(d/(24*time.Hour)), "day")
}

// candidateLines are the status lines for the last check's candidate and every replay recorded
// against the series as it stands, a conflict's naming `yolo pack rebase` as its next step. in is
// what a check of the fork reads now: a record whose last check read anything else answers another
// question, and its list is not shown as the candidate.
func candidateLines(f packload.Fork, rec *packsrc.CheckRecord, series *packsrc.Series, in packsrc.CheckInputs) []string {
	found := rec.Check
	if found == nil {
		return []string{"[dim]  checked, with no answer recorded — `yolo pack update` checks again[/dim]"}
	}
	if !rec.Answers(in) {
		return []string{"[yellow]  ⚠ the fork's " + changedInputs(rec.Read, in) + " changed since the last " +
			"check, so it names no candidate yet — `yolo pack update` checks it now[/yellow]"}
	}
	ago := patchedAge(patchedNow().Sub(time.Unix(found.At, 0)))
	var lines []string
	if found.FetchErr != "" {
		lines = append(lines, "[yellow]  ⚠ the last check could not fetch ("+found.FetchErr+") — next check "+
			"in an hour, or `yolo pack update` to check now[/yellow]")
	}
	if found.Problem != "" {
		return append(lines, "[yellow]  ⚠ "+found.Problem+"[/yellow]")
	}
	if found.NoVersion != "" {
		// A RELEASE RULE OVER A BRANCH WITH NO VERSION TAG (PF-D60), said whenever status is asked.
		lines = append(lines, "[yellow]  ⚠ "+richtext.Escape(found.NoVersionLine(stayAt(rec, series)))+"[/yellow]")
	}
	list := rec.Candidates(in)
	if len(list) == 0 {
		if rec.Good == nil && found.BaseOnBranch {
			return append(lines, fmt.Sprintf("[dim]  candidate: the series' base %s (no version of the "+
				"branch contains it; checked %s ago)[/dim]", shortSHA(series.Base), ago))
		}
		return append(lines, fmt.Sprintf("[dim]  candidate: none newer (checked %s ago)[/dim]", ago))
	}
	gitVer, _ := patchedForkStore().GitVersion()
	yolo := patchedYoloVersion()
	for i, e := range list {
		label := "candidate"
		if i > 0 {
			label = "below it"
		}
		state := "not replayed yet — `yolo pack update` replays it"
		if i > 0 {
			// BELOW A CONFLICT, NOTHING REPLAYS IT (PF-D81): an update stops at the newest's conflict
			// and builds no older fit, so the one way to it is a rebase onto it.
			state = "not tried — `yolo pack update` stops at the conflict above and builds no older " +
				"version; `" + rebaseCommand(f, e, isRebaseDefault(rec, in, series.Digest, e)) +
				"` rebases the series onto it"
		}
		if o := rec.Conflict(e.Commit, series.Digest, yolo, gitVer); o != nil {
			state = fmt.Sprintf("does not take %s (conflicts in %s) — `%s` rebases the series", o.Member,
				strings.Join(o.Paths, ", "), rebaseCommand(f, e, isRebaseDefault(rec, in, series.Digest, e)))
		} else if o := rec.Applies(e.Commit, series.Digest, yolo, gitVer); o != nil {
			state = "applies — " + patchedNotBuilt
		}
		lines = append(lines, fmt.Sprintf("[dim]  %s: %s, checked %s ago: %s[/dim]", label, e.Label(), ago, state))
		if !strings.HasPrefix(state, "does not take") {
			break // the walk stops at the first entry that may still fit
		}
	}
	return lines
}

// changedInputs names what differs between what the last check read and what a check reads now:
// the source's repository, subdirectory or ref, the follow rule, or the series' base.
func changedInputs(was, now packsrc.CheckInputs) string {
	var what []string
	if was.Repo != now.Repo || was.Subdir != now.Subdir || was.Ref != now.Ref {
		what = append(what, "source")
	}
	if was.Follow != now.Follow {
		what = append(what, "follow rule")
	}
	if was.Base != now.Base {
		what = append(what, "series base")
	}
	if len(what) == 0 {
		return "upstream"
	}
	return strings.Join(what, " and ")
}

// followLabel is a follow rule as written, the default named.
func followLabel(follow string) string {
	if follow == "" {
		return packsrc.DefaultFollow + " (the default)"
	}
	return follow
}
