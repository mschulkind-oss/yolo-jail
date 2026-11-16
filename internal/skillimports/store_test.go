package skillimports

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/gitbroker"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

func TestImportResolverWritesOnlySuccessfulBrokerResult(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	entry := config.SkillImport{Handle: "acme", Git: "https://example.test/acme/skills", Ref: "main", Path: "skills"}
	authority := Authority{
		ApprovedRemotes: []string{entry.Git}, CurrentRemotes: []string{entry.Git},
		ApprovedGrants: []brokerscope.Grant{{Kind: "read", Repo: brokerscope.AnyRepo}},
		CurrentGrants:  []brokerscope.Grant{{Kind: "read", Repo: brokerscope.AnyRepo}},
	}
	commit := strings.Repeat("a", 40)
	resolved, err := store.resolveWithFetcher(context.Background(), entry, authority, func(_ context.Context, request gitbroker.ImportFetchRequest) (gitbroker.ImportFetchResult, error) {
		if request.Workspace != store.workspace || request.URL != entry.Git || request.Ref != entry.Ref || request.StoreDir != store.ObjectDirectory() {
			t.Fatalf("broker request lost import selection: %+v", request)
		}
		if len(request.ApprovedRemotes) != 1 || request.ApprovedRemotes[0] != entry.Git || len(request.CurrentGrants) != 1 {
			t.Fatalf("broker request lost explicit authority: %+v", request)
		}
		return gitbroker.ImportFetchResult{Commit: commit, ResolvedRef: "refs/heads/main", AccessClass: AccessAuthenticated}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Commit != commit || resolved.ResolvedRef != "refs/heads/main" || resolved.AccessClass != AccessAuthenticated {
		t.Fatalf("Resolve result = %+v", resolved)
	}
	if got, ok := store.Resolution(entry.Handle, entry); !ok || got != resolved {
		t.Fatalf("written resolution = (%+v, %v), want %+v", got, ok, resolved)
	}

	failed := entry
	failed.Handle = "broken"
	if _, err := store.resolveWithFetcher(context.Background(), failed, authority, func(context.Context, gitbroker.ImportFetchRequest) (gitbroker.ImportFetchResult, error) {
		return gitbroker.ImportFetchResult{}, errors.New("broker refused")
	}); err == nil {
		t.Fatal("Resolve succeeded after broker refusal")
	}
	if _, ok := store.Resolution(failed.Handle, failed); ok {
		t.Fatal("broker refusal wrote a resolution record")
	}
}

func TestImportStateIsPerWorkspaceAndOutsideApprovalRecords(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	workspaceA, workspaceB := t.TempDir(), t.TempDir()
	first, err := Open(workspaceA)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Open(workspaceB)
	if err != nil {
		t.Fatal(err)
	}
	if first.Directory() == second.Directory() {
		t.Fatal("two workspaces share host import state")
	}
	for _, state := range []*Store{first, second} {
		if !strings.HasPrefix(state.Directory(), paths.GlobalStorage()+string(os.PathSeparator)) {
			t.Errorf("import state path %q is not beneath GlobalStorage", state.Directory())
		}
	}
	for _, path := range config.ApprovalRecordFiles(workspaceA) {
		if strings.HasPrefix(path, first.Directory()+string(os.PathSeparator)) || path == first.Directory() {
			t.Errorf("import state %s entered approval record files", first.Directory())
		}
	}

	entry := config.SkillImport{Handle: "acme", Git: "https://example.test/acme/skills", Ref: "main", Path: "skills"}
	record := Resolution{Version: resolutionVersion, Handle: "acme", Git: entry.Git, Ref: entry.Ref, Repo: "example.test/acme/skills", Commit: strings.Repeat("a", 40),
		AccessClass: AccessAnonymous, ResolvedAt: time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)}
	if err := first.WriteResolution(record); err != nil {
		t.Fatal(err)
	}
	got, ok := first.Resolution("acme", entry)
	if !ok || got != record {
		t.Fatalf("Resolution = (%+v, %v), want %+v", got, ok, record)
	}
	if _, ok := second.Resolution("acme", entry); ok {
		t.Fatal("another workspace read this workspace's record")
	}
	changed := entry
	changed.Ref = "release"
	if _, ok := first.Resolution("acme", changed); ok {
		t.Fatal("record stayed valid after configured ref changed")
	}
	changed = entry
	changed.Git = "https://example.test/acme/other"
	if _, ok := first.Resolution("acme", changed); ok {
		t.Fatal("record stayed valid after configured Git URL changed")
	}
	pinned := config.SkillImport{Handle: "acme", Git: entry.Git, Rev: strings.Repeat("b", 40), Path: "skills"}
	if _, ok := first.Resolution("acme", pinned); ok {
		t.Fatal("moving-ref record stayed valid for a pinned entry")
	}
	pinnedRecord := Resolution{Version: resolutionVersion, Handle: "acme", Git: pinned.Git, Rev: pinned.Rev,
		Repo: "example.test/acme/skills", Commit: pinned.Rev, AccessClass: AccessAnonymous, ResolvedAt: record.ResolvedAt}
	if err := first.WriteResolution(pinnedRecord); err != nil {
		t.Fatal(err)
	}
	if got, ok := first.Resolution("acme", pinned); !ok || got != pinnedRecord {
		t.Fatalf("pinned resolution = (%+v, %v), want %+v", got, ok, pinnedRecord)
	}
	pinned.Rev = strings.Repeat("d", 40)
	if _, ok := first.Resolution("acme", pinned); ok {
		t.Fatal("pinned record stayed valid after configured rev changed")
	}
}

func TestImportStateRejectsMalformedAndInvalidRecords(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.WriteResolution(Resolution{Handle: "../escape", Git: "https://example.test/a/b", Ref: "main",
		Commit: strings.Repeat("a", 40), AccessClass: AccessAnonymous, ResolvedAt: time.Now()}); err == nil {
		t.Fatal("unsafe handle wrote a resolution record")
	}
	path := store.RecordPath("acme")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"version":1,"handle":"acme","git":"https://example.test/a/b","ref":"main","commit":"short","access_class":"unknown","resolved_at":"bad"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	entry := config.SkillImport{Handle: "acme", Git: "https://example.test/a/b", Ref: "main", Path: "skills"}
	if _, ok := store.Resolution("acme", entry); ok {
		t.Fatal("malformed record was treated as a usable resolution")
	}
	if err := os.WriteFile(path, []byte(`{"version":1,"handle":"acme","git":"https://example.test/a/b","ref":"main","commit":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","access_class":"anonymous"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := store.Resolution("acme", entry); ok {
		t.Fatal("incomplete record was treated as a usable resolution")
	}
}

func TestImportStateRecordReplacementIsAtomic(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	base := Resolution{Version: resolutionVersion, Handle: "acme", Git: "https://example.test/acme/skills",
		Ref: "main", Repo: "example.test/acme/skills", Commit: strings.Repeat("a", 40),
		AccessClass: AccessAnonymous, ResolvedAt: time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)}
	if err := store.WriteResolution(base); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	written := make(chan error, 1)
	go func() {
		<-start
		for i := 0; i < 200; i++ {
			record := base
			if i%2 == 1 {
				record.Commit = strings.Repeat("b", 40)
			}
			if err := store.WriteResolution(record); err != nil {
				written <- err
				return
			}
		}
		written <- nil
	}()
	close(start)
	for {
		record, err := store.readResolution("acme")
		if err != nil || !validResolution(record, "acme") ||
			record.Commit != strings.Repeat("a", 40) && record.Commit != strings.Repeat("b", 40) {
			t.Fatalf("reader observed a partial resolution: (%+v, %v)", record, err)
		}
		select {
		case err := <-written:
			if err != nil {
				t.Fatal(err)
			}
			return
		default:
		}
	}
}

func TestImportStateObjectStoreIsWorkspaceOwnedAndAddOnly(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureObjectStore(); err != nil {
		t.Fatal(err)
	}
	if store.HasCommit(strings.Repeat("c", 40)) {
		t.Fatal("empty object store unexpectedly has an object")
	}

	root := t.TempDir()
	source := filepath.Join(root, "source.git")
	work := filepath.Join(root, "work")
	_ = runGit(t, "", "init", "--bare", "--template=", source)
	_ = runGit(t, "", "clone", source, work)
	_ = runGit(t, work, "config", "user.name", "skill import test")
	_ = runGit(t, work, "config", "user.email", "skills@example.test")
	if err := os.WriteFile(filepath.Join(work, "SKILL.md"), []byte("skill"), 0o600); err != nil {
		t.Fatal(err)
	}
	_ = runGit(t, work, "add", "SKILL.md")
	_ = runGit(t, work, "commit", "-m", "skill")
	commit := runGit(t, work, "rev-parse", "HEAD")
	_ = runGit(t, work, "push", "origin", "HEAD:refs/heads/main")
	_ = runGit(t, "", "--git-dir", store.ObjectDirectory(), "fetch", "--no-tags", source, commit)
	if !store.HasCommit(commit) {
		t.Fatalf("object store did not retain imported commit %s", commit)
	}
	if err := store.EnsureObjectStore(); err != nil {
		t.Fatal(err)
	}
	if !store.HasCommit(commit) {
		t.Fatalf("reinitializing existing object store removed commit %s", commit)
	}
	if got := store.ObjectDirectory(); !strings.HasPrefix(got, store.Directory()+string(os.PathSeparator)) {
		t.Fatalf("object store %q escaped workspace state %q", got, store.Directory())
	}
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = isolatedGitEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestImportStateLockSerializesWriters(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	firstEntered := make(chan struct{})
	releaseFirst := make(chan struct{})
	firstDone := make(chan struct{})
	secondStarted := make(chan struct{})
	secondEntered := make(chan struct{})
	go func() {
		defer close(firstDone)
		_ = store.WithLock(func() error {
			close(firstEntered)
			<-releaseFirst
			return nil
		})
	}()
	<-firstEntered
	go func() {
		close(secondStarted)
		_ = store.WithLock(func() error {
			close(secondEntered)
			return nil
		})
	}()
	<-secondStarted
	lockFile, err := os.OpenFile(filepath.Join(store.Directory(), ".lock"), os.O_RDONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	lockErr := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if lockErr == nil {
		_ = syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)
		_ = lockFile.Close()
		t.Fatal("competing process acquired the import lock while first writer held it")
	}
	_ = lockFile.Close()
	if lockErr != syscall.EWOULDBLOCK && lockErr != syscall.EAGAIN {
		t.Fatalf("competing import lock probe = %v, want busy", lockErr)
	}
	// The lock release is the event that lets a correct second writer enter. Verify ordering
	// by waiting for both callbacks, not by asking either one to beat a wall-clock budget.
	close(releaseFirst)
	<-firstDone
	<-secondEntered
}
