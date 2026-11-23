package skillimports

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/gitbroker"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
)

type launchCheckFixture struct {
	store     *Store
	entry     config.SkillImport
	authority Authority
	commit    string
	now       time.Time
}

func newLaunchCheckFixture(t *testing.T, resolvedAgo time.Duration) launchCheckFixture {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureObjectStore(); err != nil {
		t.Fatal(err)
	}
	commit := storeCommit(t, store)
	entry := config.SkillImport{Handle: "acme", Git: "git@fixture.test:o/r.git", Ref: "main", Path: "skills"}
	grant := []brokerscope.Grant{{Kind: "read", Repo: brokerscope.AnyRepo}}
	authority := Authority{ApprovedRemotes: []string{entry.Git}, CurrentRemotes: []string{entry.Git},
		ApprovedGrants: grant, CurrentGrants: grant}
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	if err := store.WriteResolution(Resolution{Version: resolutionVersion, Handle: entry.Handle, Git: entry.Git,
		Ref: entry.Ref, ResolvedRef: "refs/heads/main", Repo: "fixture.test/o/r", Commit: commit,
		AccessClass: AccessAuthenticated, ResolvedAt: now.Add(-resolvedAgo)}); err != nil {
		t.Fatal(err)
	}
	return launchCheckFixture{store: store, entry: entry, authority: authority, commit: commit, now: now}
}

func TestBranchImportFetchedMinutesAgoIsCheckedAtTheNextLaunch(t *testing.T) {
	f := newLaunchCheckFixture(t, 3*time.Minute)
	calls := 0
	var deadline time.Time
	fetch := func(ctx context.Context, _ gitbroker.ImportFetchRequest) (gitbroker.ImportFetchResult, error) {
		calls++
		deadline, _ = ctx.Deadline()
		return gitbroker.ImportFetchResult{Commit: f.commit, ResolvedRef: "refs/heads/main", AccessClass: AccessAuthenticated}, nil
	}
	if _, _, err := f.store.resolveLaunchWithFetcher(context.Background(), f.entry, f.authority, f.now, fetch); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("a branch import fetched minutes ago made %d transport calls at the next launch, want 1", calls)
	}
	if left := time.Until(deadline); deadline.IsZero() || left <= 0 || left > packsrc.PackCheckDeadline {
		t.Fatalf("the launch check must run under the %s pack-check deadline, got deadline %v", packsrc.PackCheckDeadline, deadline)
	}
	// Every fresh launch checks again, even one second after the last.
	if _, _, err := f.store.resolveLaunchWithFetcher(context.Background(), f.entry, f.authority, f.now.Add(time.Second), fetch); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("a second launch made %d total calls, want 2", calls)
	}
}

func TestUnmovedBranchImportChangesNothing(t *testing.T) {
	f := newLaunchCheckFixture(t, 3*time.Minute)
	fetch := func(context.Context, gitbroker.ImportFetchRequest) (gitbroker.ImportFetchResult, error) {
		return gitbroker.ImportFetchResult{Commit: f.commit, ResolvedRef: "refs/heads/main", AccessClass: AccessAuthenticated}, nil
	}
	got, warning, err := f.store.resolveLaunchWithFetcher(context.Background(), f.entry, f.authority, f.now, fetch)
	if err != nil || warning != "" || got.Commit != f.commit || got.Change != nil {
		t.Fatalf("unmoved import = (%+v, %q, %v), want the same commit, no change, no warning", got, warning, err)
	}
}

func TestBranchCheckTimeoutServesTheLastUsableCommit(t *testing.T) {
	f := newLaunchCheckFixture(t, 3*time.Minute)
	f.store.refreshDeadline = time.Millisecond
	fetch := func(ctx context.Context, _ gitbroker.ImportFetchRequest) (gitbroker.ImportFetchResult, error) {
		<-ctx.Done() // returns only when the injected deadline fires
		return gitbroker.ImportFetchResult{}, ctx.Err()
	}
	got, warning, err := f.store.resolveLaunchWithFetcher(context.Background(), f.entry, f.authority, f.now, fetch)
	if err != nil || got.Commit != f.commit {
		t.Fatalf("timeout = (%+v, %v), want the usable record", got, err)
	}
	if want := "the Git broker could not refresh this branch; the last usable commit is staged"; warning != want {
		t.Fatalf("warning = %q, want %q", warning, want)
	}
	failing := func(context.Context, gitbroker.ImportFetchRequest) (gitbroker.ImportFetchResult, error) {
		return gitbroker.ImportFetchResult{}, errors.New("broker down")
	}
	if _, warning, err = f.store.resolveLaunchWithFetcher(context.Background(), f.entry, f.authority, f.now, failing); err != nil || warning == "" {
		t.Fatalf("failure = (%q, %v), want the warning", warning, err)
	}
}

func TestPinnedRevMakesNoTransportCall(t *testing.T) {
	f := newLaunchCheckFixture(t, 3*time.Minute)
	pinned := config.SkillImport{Handle: "pinned", Git: f.entry.Git, Rev: f.commit, Ref: "main", Path: "skills"}
	if err := f.store.WriteResolution(Resolution{Version: resolutionVersion, Handle: pinned.Handle, Git: pinned.Git,
		Rev: pinned.Rev, Ref: pinned.Ref, Repo: "fixture.test/o/r", Commit: f.commit, AccessClass: AccessAuthenticated,
		ResolvedAt: f.now.Add(-time.Minute)}); err != nil {
		t.Fatal(err)
	}
	fetch := func(context.Context, gitbroker.ImportFetchRequest) (gitbroker.ImportFetchResult, error) {
		t.Fatal("a pinned rev made a transport call")
		return gitbroker.ImportFetchResult{}, nil
	}
	if _, _, err := f.store.resolveLaunchWithFetcher(context.Background(), pinned, f.authority, f.now, fetch); err != nil {
		t.Fatal(err)
	}
}
