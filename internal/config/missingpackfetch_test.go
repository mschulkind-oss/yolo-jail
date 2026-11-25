package config

import (
	"bytes"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// lockedBuffer is a log both a fetch goroutine and the test read.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// enableFetchForTest turns the fetch on for one test, host-side (no YOLO_VERSION), with run as the
// fetch, and restores the process state afterward.
func enableFetchForTest(t *testing.T, run func(PackEntry) error) *lockedBuffer {
	t.Helper()
	t.Setenv("YOLO_VERSION", "")
	log := &lockedBuffer{}
	missingFetchMu.Lock()
	priorRun, priorJoined := missingFetchRun, missingFetchJoined
	if run != nil {
		missingFetchRun = run
	}
	missingFetchMu.Unlock()
	EnableMissingPackFetch(log)
	t.Cleanup(func() {
		missingFetchMu.Lock()
		defer missingFetchMu.Unlock()
		missingFetchLog, missingFetchRun, missingFetchJoined = nil, priorRun, priorJoined
	})
	return log
}

// The real fetch, end to end against a real git remote: the broker read meets the unfetched pack,
// fetches it in line and reads it, so the missing state is simply gone: no note, the pack in the
// approval selection. The fetch never writes the lock file, so the next launch still discloses the
// pack as fetched.
func TestAMissingPackIsFetchedInLineAndThenRead(t *testing.T) {
	repo := gitPackRepo(t, map[string]string{"pack/pack.json": `{"name":"pack"}`}, nil)
	writeGitUserConfigWithPacks(t, "['git', 'rem/pack']", "[pack_sources.rem]\ngit = 'file://"+repo+"'\nref = 'main'\n\n")
	log := enableFetchForTest(t, fetchMissingPack)

	kinds, _, notes, err := BrokeredGrantKindsWithNotes("git")
	if err != nil || len(notes) != 0 || !slices.Contains(kinds, "branch") {
		t.Fatalf("kinds %v notes %q err %v; want the pack fetched and read, nothing to note", kinds, notes, err)
	}
	if out := log.String(); !strings.Contains(out, "fetching it") || !strings.Contains(out, "fetched selected pack rem/pack") {
		t.Fatalf("log = %q; want the fetch and its result", out)
	}
	packs, notes, problems, complete := UserScopeSelectedPacksWithNotes()
	if !complete || len(notes) != 0 || len(problems) != 0 {
		t.Fatalf("approval selection: complete %t notes %q problems %q", complete, notes, problems)
	}
	var sawPack bool
	for _, p := range packs {
		sawPack = sawPack || p.Name == "pack" || strings.HasSuffix(p.Name, "pack")
	}
	if !sawPack {
		t.Fatalf("the fetched pack is not in the approval selection (%d packs)", len(packs))
	}
	if _, err := os.Stat(packsrc.LockPath(paths.UserConfigPath())); !os.IsNotExist(err) {
		t.Fatalf("the in-line fetch wrote the lock file (%v); the launch's disclosure compares against it", err)
	}
}

// Only a fetch that fails leaves the pack skipped: the note carries the fetch's own error and the
// next step, the grant is kept, and the next evaluation tries again (there is no attempt record).
func TestAFailedFetchSkipsThePackWithItsError(t *testing.T) {
	writeGitUserConfigWithPacks(t, "['git', 'rem/tools']", unfetchedRemote)
	var calls int
	log := enableFetchForTest(t, func(PackEntry) error { calls++; return os.ErrPermission })

	for i := 1; i <= 2; i++ {
		kinds, _, notes, err := BrokeredGrantKindsWithNotes("git")
		if err != nil || !slices.Contains(kinds, "branch") || len(notes) != 1 {
			t.Fatalf("read %d: kinds = %v notes %q err %v", i, kinds, notes, err)
		}
		for _, want := range []string{"rem/tools", "fetching it failed: permission denied", "yolo pack install"} {
			if !strings.Contains(notes[0], want) {
				t.Fatalf("note %q lacks %q", notes[0], want)
			}
		}
		if calls != i {
			t.Fatalf("after read %d the fetch ran %d times; want one attempt per evaluation", i, calls)
		}
	}
	if _, notes, _, complete := UserScopeSelectedPacksWithNotes(); !complete || len(notes) != 1 || !strings.Contains(notes[0], "fetching it failed") {
		t.Fatalf("approval selection: complete %t notes %q; want the fetch error named", complete, notes)
	}
	if out := log.String(); !strings.Contains(out, "could not fetch selected pack rem/tools") || !strings.Contains(out, "permission denied") {
		t.Fatalf("log = %q; want the failure and its error", out)
	}
}

// Concurrent evaluations of one missing pack share one fetch: the fetch holds until every other
// evaluation has joined it, runs once, and each evaluation gets its result.
func TestConcurrentEvaluationsShareOneFetch(t *testing.T) {
	writeGitUserConfigWithPacks(t, "['git', 'rem/tools']", unfetchedRemote)
	const evaluations = 4
	var runs int
	joined := make(chan struct{}, evaluations)
	enableFetchForTest(t, func(PackEntry) error {
		runs++
		for i := 0; i < evaluations-1; i++ {
			<-joined
		}
		return nil
	})
	missingFetchMu.Lock()
	missingFetchJoined = func(PackEntry) { joined <- struct{}{} }
	missingFetchMu.Unlock()

	entry := PackEntry{Name: "rem/tools", Source: "https://example.invalid/acme/tools"}
	var wg sync.WaitGroup
	results := make([]error, evaluations)
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, results[i] = fetchMissingPackNow(entry)
		}()
	}
	wg.Wait()
	if runs != 1 {
		t.Fatalf("the fetch ran %d times; want one shared by %d evaluations", runs, evaluations)
	}
	for i, err := range results {
		if err != nil {
			t.Fatalf("evaluation %d: %v", i, err)
		}
	}
}

// Nothing is fetched for a pack the read cannot skip anyway (a vanished local one has nothing to
// fetch; one named like a shipped pack fails closed), and nothing where fetching is not enabled
// (a test, `yolo check`, a launch, the jail): the read just skips with its note.
func TestNoFetchStartsForAPackTheReadDoesNotSkipOrWhereItIsNotEnabled(t *testing.T) {
	root := t.TempDir()
	local := "[pack_sources]\nsrc = '" + root + "'\n\n"
	var calls int
	run := func(PackEntry) error { calls++; return nil }

	writeGitUserConfigWithPacks(t, "['git', 'rem/tools']", unfetchedRemote)
	t.Setenv("YOLO_VERSION", "")
	missingFetchMu.Lock()
	missingFetchRun = run
	missingFetchMu.Unlock()
	t.Cleanup(func() { missingFetchMu.Lock(); missingFetchRun = fetchMissingPack; missingFetchMu.Unlock() })
	if _, _, notes, err := BrokeredGrantKindsWithNotes("git"); err != nil || len(notes) != 1 {
		t.Fatalf("not enabled: notes %q err %v; want the pack skipped with a note", notes, err)
	}

	enableFetchForTest(t, run)
	for _, c := range [][2]string{
		{"['git', 'src/gone']", local},
		{"['git', 'rem/github']", unfetchedRemote},
	} {
		writeGitUserConfigWithPacks(t, c[0], c[1])
		t.Setenv("YOLO_VERSION", "")
		BrokeredGrantKindsWithNotes("git")
		UserScopeSelectedPacksWithNotes()
	}
	if calls != 0 {
		t.Fatalf("a fetch ran %d times for a pack that is never fetched", calls)
	}
}
