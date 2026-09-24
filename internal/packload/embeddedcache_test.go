package packload

// embeddedcache_test.go pins the content-addressed embedded-pack cache's PROTOCOL — the
// hash, the atomic populate and its race, the trust rule for a tree found on disk, the
// fallback and its dead-sibling sweep — against small in-memory filesystems, so each rule
// is exercised on a tree whose every byte the test chose.
//
// In-package (it drives populateEmbedded and the rename seam directly), which means it
// shares a binary with packload_test files that register the REAL embedded packs; every
// test here swaps the FS in and restores it (withEmbeddedFS).

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"testing/fstest"
	"time"
)

func sampleFS() fstest.MapFS {
	return fstest.MapFS{
		"alpha/pack.json":          {Data: []byte(`{"name":"alpha"}`)},
		"alpha/skills/x/SKILL.md":  {Data: []byte("# x\n")},
		"beta/pack.json":           {Data: []byte(`{"name":"beta"}`)},
		"beta/briefing/section.md": {Data: []byte("hello\n")},
	}
}

// withEmbeddedFS registers f for the test, points the cache base at a fresh dir and TMPDIR
// at another, and puts everything back afterwards. Returns (base, tmp).
func withEmbeddedFS(t *testing.T, f fs.FS) (string, string) {
	t.Helper()
	embeddedMu.Lock()
	releaseEmbeddedLocked()
	prevFS, prevMemo := embeddedFS, embeddedHashMemo
	embeddedFS, embeddedHashMemo = f, nil
	embeddedMu.Unlock()
	// RESOLVED where minted: the loader resolves its base through symlinks, and on darwin
	// t.TempDir() is under /var/folders, itself a symlink — a raw one would never compare
	// equal to a Pack.Root.
	base, tmp := resolvedTempDir(t), resolvedTempDir(t)
	t.Setenv("TMPDIR", tmp)
	restore := OverrideEmbeddedCacheDir(base)
	t.Cleanup(func() {
		restore()
		embeddedMu.Lock()
		embeddedFS, embeddedHashMemo = prevFS, prevMemo
		embeddedMu.Unlock()
	})
	return base, tmp
}

func resolvedTempDir(t *testing.T) string {
	t.Helper()
	d, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func names(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

func mustHash(t *testing.T, f fs.FS, format string) string {
	t.Helper()
	sum, err := hashEmbeddedFS(f, format)
	if err != nil {
		t.Fatal(err)
	}
	return sum
}

// The name must change with ANY content change and with nothing else: a collision would
// hand one build another build's tree.
func TestEmbeddedHashIsStableAndSensitive(t *testing.T) {
	base := mustHash(t, sampleFS(), EmbeddedTreeFormat)
	if again := mustHash(t, sampleFS(), EmbeddedTreeFormat); again != base {
		t.Fatalf("the same tree hashed twice: %s then %s", base, again)
	}
	if len(base) != 32 {
		t.Errorf("hash %q is %d chars, want 32 (16 bytes hex)", base, len(base))
	}

	oneByte := sampleFS()
	oneByte["beta/briefing/section.md"] = &fstest.MapFile{Data: []byte("hellO\n")}
	renamed := sampleFS()
	renamed["beta/briefing/other.md"] = renamed["beta/briefing/section.md"]
	delete(renamed, "beta/briefing/section.md")
	// Framing: moving bytes between a path and its content must not collide.
	shifted := fstest.MapFS{"alpha/pack.jsonx": {Data: []byte(`{"name":"alpha"}`)}}
	unshifted := fstest.MapFS{"alpha/pack.json": {Data: []byte(`x{"name":"alpha"}`)}}

	for name, sum := range map[string]string{
		"one byte changed": mustHash(t, oneByte, EmbeddedTreeFormat),
		"a path renamed":   mustHash(t, renamed, EmbeddedTreeFormat),
		"format bumped":    mustHash(t, sampleFS(), EmbeddedTreeFormat+"-next"),
	} {
		if sum == base {
			t.Errorf("%s: hash unchanged (%s)", name, sum)
		}
	}
	if mustHash(t, shifted, EmbeddedTreeFormat) == mustHash(t, unshifted, EmbeddedTreeFormat) {
		t.Error("a byte moved from a path into its content hashed the same: the stream is not framed")
	}
}

// TestEmbeddedHashCoversTheFormatConstant: the process's hash IS the format-keyed hash, so
// bumping EmbeddedTreeFormat really does move every build to a fresh tree.
func TestEmbeddedHashCoversTheFormatConstant(t *testing.T) {
	withEmbeddedFS(t, sampleFS())
	got, ok := EmbeddedHash()
	if !ok {
		t.Fatal("EmbeddedHash() not ok with a registered FS")
	}
	if want := mustHash(t, sampleFS(), EmbeddedTreeFormat); got != want {
		t.Errorf("EmbeddedHash() = %s, want the EmbeddedTreeFormat-keyed hash %s", got, want)
	}
}

// TestPopulateMakesOneReadOnlyTreeAndReusesIt: the first load writes <base>/<hash> and
// nothing else — no stray temp dir, nothing in TMPDIR — with read-only files, and every
// later load (after a release, as a second process would) adopts that same tree.
func TestPopulateMakesOneReadOnlyTreeAndReusesIt(t *testing.T) {
	base, tmp := withEmbeddedFS(t, sampleFS())
	sum, _ := EmbeddedHash()

	packs := Embedded()
	if len(packs) != 2 {
		t.Fatalf("Embedded() = %d packs, want 2: %v", len(packs), EmbeddedProblems())
	}
	root, fallback := EmbeddedLocation()
	if fallback || root != filepath.Join(base, sum) {
		t.Fatalf("tree at %s (fallback=%v), want %s", root, fallback, filepath.Join(base, sum))
	}
	if got := names(t, base); len(got) != 1 || got[0] != sum {
		t.Errorf("base holds %v, want exactly [%s]", got, sum)
	}
	if got := names(t, tmp); len(got) != 0 {
		t.Errorf("TMPDIR holds %v; a usable cache must leave nothing per-process", got)
	}
	if err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		fi, _ := os.Lstat(p)
		want := os.FileMode(0o444)
		if d.IsDir() {
			want = 0o755 | fs.ModeDir
		}
		if fi.Mode() != want {
			t.Errorf("%s mode %v, want %v", p, fi.Mode(), want)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	ReleaseEmbedded()
	if _, err := os.Stat(filepath.Join(root, "alpha", "pack.json")); err != nil {
		t.Fatalf("ReleaseEmbedded deleted the SHARED tree: %v", err)
	}
	again := Embedded()
	if len(again) != 2 || again[0].Root != packs[0].Root {
		t.Errorf("re-adopt gave %d packs at %v, want the same tree", len(again), again)
	}
}

// TestPopulateRaceLoserAdoptsTheWinner forces the one window a race lands in: another
// process renames ITS tree into place between our materialize and our rename. The loser must
// remove its own temp dir and use the winner's tree — ending with ONE tree and no stray .tmp-.
func TestPopulateRaceLoserAdoptsTheWinner(t *testing.T) {
	base, _ := withEmbeddedFS(t, sampleFS())
	sum, _ := EmbeddedHash()
	final := filepath.Join(base, sum)

	var winnerLease *os.File
	raced := false
	embeddedBeforeRename = func(string) {
		if raced {
			return
		}
		raced = true
		var outcome populateOutcome
		winnerLease, outcome = populateEmbedded(sampleFS(), base, sum, final)
		if outcome != populateDone {
			t.Errorf("the simulated winner's populate = %v, want done", outcome)
		}
	}
	t.Cleanup(func() {
		embeddedBeforeRename = nil
		if winnerLease != nil {
			winnerLease.Close()
		}
	})

	winnerIno := func() uint64 {
		fi, err := os.Stat(filepath.Join(final, EmbeddedLeaseName))
		if err != nil {
			t.Fatal(err)
		}
		return fi.Sys().(*syscall.Stat_t).Ino
	}

	packs := Embedded()
	if !raced {
		t.Fatal("the rename seam never ran; the loser path was not exercised")
	}
	if len(packs) != 2 {
		t.Fatalf("the loser got %d packs: %v", len(packs), EmbeddedProblems())
	}
	root, fallback := EmbeddedLocation()
	if fallback || root != final {
		t.Errorf("the loser loaded from %s (fallback=%v), want the winner's %s", root, fallback, final)
	}
	held, _ := embeddedLease.Stat()
	if held == nil || held.Sys().(*syscall.Stat_t).Ino != winnerIno() {
		t.Error("the loser's lease is not on the winner's tree")
	}
	if got := names(t, base); len(got) != 1 || got[0] != sum {
		t.Errorf("base holds %v after the race, want exactly [%s] (a loser's .tmp- leaked)", got, sum)
	}
}

// TestUntrustedTreeIsQuarantinedAndRepopulated: a tree found at the final name is trusted
// only if it holds exactly the embedded entries with identical bytes. Anything else is
// renamed aside as .bad- and a fresh tree written in its place.
func TestUntrustedTreeIsQuarantinedAndRepopulated(t *testing.T) {
	for name, damage := range map[string]func(t *testing.T, root string){
		"a file edited": func(t *testing.T, root string) {
			p := filepath.Join(root, "alpha", "pack.json")
			os.Chmod(p, 0o644)
			if err := os.WriteFile(p, []byte(`{"name":"evil"}`), 0o644); err != nil {
				t.Fatal(err)
			}
		},
		"a file missing (partial tree)": func(t *testing.T, root string) {
			if err := os.Remove(filepath.Join(root, "beta", "briefing", "section.md")); err != nil {
				t.Fatal(err)
			}
		},
		"an extra file planted": func(t *testing.T, root string) {
			if err := os.WriteFile(filepath.Join(root, "alpha", "loophole.json"), nil, 0o644); err != nil {
				t.Fatal(err)
			}
		},
		"a file swapped for a symlink": func(t *testing.T, root string) {
			p := filepath.Join(root, "alpha", "pack.json")
			os.Remove(p)
			if err := os.Symlink("/etc/passwd", p); err != nil {
				t.Fatal(err)
			}
		},
		"the lease removed": func(t *testing.T, root string) {
			if err := os.Remove(filepath.Join(root, EmbeddedLeaseName)); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			base, _ := withEmbeddedFS(t, sampleFS())
			sum, _ := EmbeddedHash()
			final := filepath.Join(base, sum)
			if len(Embedded()) != 2 {
				t.Fatalf("setup: %v", EmbeddedProblems())
			}
			ReleaseEmbedded()
			damage(t, final)

			packs := Embedded()
			if len(packs) != 2 {
				t.Fatalf("after quarantine: %d packs, %v", len(packs), EmbeddedProblems())
			}
			root, fallback := EmbeddedLocation()
			if fallback || root != final {
				t.Errorf("loaded from %s (fallback=%v), want a repopulated %s", root, fallback, final)
			}
			if err := verifyEmbeddedTree(sampleFS(), final); err != nil {
				t.Errorf("the tree now at the final name is still untrusted: %v", err)
			}
			var bad int
			for _, n := range names(t, base) {
				if strings.HasPrefix(n, ".bad-"+sum+"-") {
					bad++
				}
			}
			if bad != 1 {
				t.Errorf("base holds %v, want the damaged tree quarantined once as .bad-", names(t, base))
			}
		})
	}
}

// TestForeignFinalFallsBack: a final name that is a symlink, or not ours, is never read or
// moved — the process takes its own fallback tree instead.
func TestForeignFinalFallsBack(t *testing.T) {
	cases := map[string]func(t *testing.T, final string){
		"a symlink": func(t *testing.T, final string) {
			elsewhere := t.TempDir()
			if _, out := populateEmbedded(sampleFS(), elsewhere, "x", filepath.Join(elsewhere, "x")); out != populateDone {
				t.Fatal("setup populate failed")
			}
			if err := os.Symlink(filepath.Join(elsewhere, "x"), final); err != nil {
				t.Fatal(err)
			}
		},
	}
	if os.Geteuid() == 0 {
		cases["owned by another uid"] = func(t *testing.T, final string) {
			if err := os.Mkdir(final, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Chown(final, 12345, 12345); err != nil {
				t.Skipf("chown: %v", err)
			}
		}
	}
	for name, plant := range cases {
		t.Run(name, func(t *testing.T) {
			base, tmp := withEmbeddedFS(t, sampleFS())
			sum, _ := EmbeddedHash()
			final := filepath.Join(base, sum)
			plant(t, final)

			if len(Embedded()) != 2 {
				t.Fatalf("fallback load failed: %v", EmbeddedProblems())
			}
			root, fallback := EmbeddedLocation()
			if !fallback || filepath.Dir(root) != tmp {
				t.Errorf("loaded from %s (fallback=%v), want a fallback in %s", root, fallback, tmp)
			}
			if _, err := os.Lstat(final); err != nil {
				t.Errorf("the foreign final was moved or removed: %v", err)
			}
		})
	}
}

// TestUnwritableCacheFallsBackAndReleaseRemovesIt: a base that cannot be created (here, a
// path through a regular file — ENOTDIR, which even root cannot write past) sends the process
// to its own TMPDIR tree, which ReleaseEmbedded then deletes.
func TestUnwritableCacheFallsBackAndReleaseRemovesIt(t *testing.T) {
	_, tmp := withEmbeddedFS(t, sampleFS())
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	restore := OverrideEmbeddedCacheDir(filepath.Join(blocker, "embedded-packs"))
	t.Cleanup(restore)

	if len(Embedded()) != 2 {
		t.Fatalf("fallback load failed: %v", EmbeddedProblems())
	}
	root, fallback := EmbeddedLocation()
	if !fallback || filepath.Dir(root) != tmp || !strings.HasPrefix(filepath.Base(root), EmbeddedFallbackPrefix) {
		t.Fatalf("loaded from %s (fallback=%v), want %s* in %s", root, fallback, EmbeddedFallbackPrefix, tmp)
	}
	if st, _, _ := ProbeLease(root); st != LeaseHeld {
		t.Errorf("the fallback tree's lease is %v, want held — a reaper could delete it under us", st)
	}
	ReleaseEmbedded()
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Errorf("ReleaseEmbedded left the fallback tree %s (stat %v)", root, err)
	}
	ReleaseEmbedded() // idempotent
}

// TestDeadFallbackSiblingsAreSwept: a fallback tree whose owner died (its lease lockable)
// is reaped by the next fallback materialization; a held, young, unleased-but-young, or
// LEGACY dir is kept.
func TestDeadFallbackSiblingsAreSwept(t *testing.T) {
	_, tmp := withEmbeddedFS(t, sampleFS())
	restore := OverrideEmbeddedCacheDir("")
	t.Cleanup(restore)

	old := time.Now().Add(-20 * time.Minute)
	ancient := time.Now().Add(-2 * time.Hour)
	mk := func(name string, lease bool, mtime time.Time) string {
		p := filepath.Join(tmp, name)
		if err := os.MkdirAll(filepath.Join(p, "claude"), 0o755); err != nil {
			t.Fatal(err)
		}
		if lease {
			if err := os.WriteFile(filepath.Join(p, EmbeddedLeaseName), nil, 0o444); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.Chtimes(p, mtime, mtime); err != nil {
			t.Fatal(err)
		}
		return p
	}
	dead := mk(EmbeddedFallbackPrefix+"dead", true, old)
	deadUnleased := mk(EmbeddedFallbackPrefix+"unleased-old", false, ancient)
	live := mk(EmbeddedFallbackPrefix+"live", true, old)
	young := mk(EmbeddedFallbackPrefix+"young", true, time.Now())
	unleasedYoung := mk(EmbeddedFallbackPrefix+"unleased-young", false, old)
	legacy := mk(LegacyEmbeddedPrefix+"123", false, ancient)

	holder, err := holdLease(filepath.Join(live, EmbeddedLeaseName))
	if err != nil || holder == nil {
		t.Fatalf("holding the live lease: %v", err)
	}
	t.Cleanup(func() { holder.Close() })

	if len(Embedded()) != 2 {
		t.Fatalf("fallback load failed: %v", EmbeddedProblems())
	}
	for _, p := range []string{dead, deadUnleased} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s survived the sweep (stat %v)", filepath.Base(p), err)
		}
	}
	for _, p := range []string{live, young, unleasedYoung, legacy} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s was reaped: %v", filepath.Base(p), err)
		}
	}
}

func TestProbeLeaseStates(t *testing.T) {
	dir := t.TempDir()
	if st, unlock, err := ProbeLease(dir); st != LeaseAbsent || err != nil {
		t.Errorf("no lease file: %v %v, want absent", st, err)
	} else {
		unlock()
	}
	lease, err := createLease(filepath.Join(dir, EmbeddedLeaseName))
	if err != nil || lease == nil {
		t.Fatalf("createLease: %v", err)
	}
	if st, _, _ := ProbeLease(dir); st != LeaseHeld {
		t.Errorf("shared-held lease probed %v, want held", st)
	}
	lease.Close()
	st, unlock, _ := ProbeLease(dir)
	if st != LeaseFree {
		t.Fatalf("released lease probed %v, want free", st)
	}
	// While the prober holds it, an adopter's shared lock must wait: the probe holds EX.
	if st2, _, _ := ProbeLease(dir); st2 != LeaseHeld {
		t.Errorf("a second probe during the first's hold saw %v, want held", st2)
	}
	unlock()
}

// TestTestBinariesNeverUseHome pins R5's base resolution: under cmd/go the base is inside
// $WORK (deleted when the run ends), and it is never under HOME.
func TestTestBinariesNeverUseHome(t *testing.T) {
	dir, isTest := testBinaryCacheDir()
	if !isTest {
		t.Fatal("a .test binary was not recognised as one; it would write the real home")
	}
	exe, _ := os.Executable()
	if !strings.HasPrefix(filepath.Base(filepath.Dir(filepath.Dir(exe))), "go-build") {
		if dir != "" {
			t.Errorf("a test binary outside cmd/go's $WORK got base %q, want the fallback", dir)
		}
		return
	}
	if filepath.Dir(dir) != filepath.Dir(exe) {
		t.Errorf("base %s is not beside the test binary %s inside cmd/go's $WORK", dir, exe)
	}
	// And HOME plays no part: pointing it somewhere else moves nothing.
	t.Setenv("HOME", t.TempDir())
	if again, _ := testBinaryCacheDir(); again != dir {
		t.Errorf("the test base moved with HOME: %s -> %s", dir, again)
	}
	embeddedMu.Lock()
	resolved, strict := embeddedCacheBaseLocked()
	embeddedMu.Unlock()
	if !cacheOverrideSet && (resolved != dir || !strict) {
		t.Errorf("embeddedCacheBaseLocked() = %s strict=%v, want the $WORK base %s, strict", resolved, strict, dir)
	}
}

// A rename that fails because final EXISTS is a lost race even when the winner's tree is
// gone by the time anyone looks. Before, the loser removed its temp dir, then looked, found
// nothing (a reaper had moved the winner's tree away in between) and took the per-process
// fallback — 19 of 400 racing processes did, measured.
func TestRenameOntoAnExistingTreeIsALostRaceEvenIfItVanished(t *testing.T) {
	dir := t.TempDir()
	src, dst := filepath.Join(dir, "src"), filepath.Join(dir, "dst")
	for _, d := range []string{src, filepath.Join(dst, "pack")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	err := os.Rename(src, dst)
	if err == nil {
		t.Fatal("setup: a rename onto a non-empty directory succeeded")
	}
	if err := os.RemoveAll(dst); err != nil {
		t.Fatal(err)
	}
	if !renameLost(err, dst) {
		t.Errorf("rename error %v with final since reaped = not lost; the loser would fall back", err)
	}
	// And a rename that failed for another reason, with nothing at final, is NOT a lost race.
	other := os.Rename(filepath.Join(dir, "absent"), filepath.Join(dir, "also-absent"))
	if other == nil || renameLost(other, filepath.Join(dir, "also-absent")) {
		t.Errorf("rename error %v with no final = lost; a real failure would retry instead of falling back", other)
	}
}

// The $WORK base is STRICT: a test binary still running after cmd/go deleted $WORK must not
// recreate $WORK/b<N> on its first pack read — nothing would ever delete it.
func TestStrictBaseNeverRecreatesAVanishedParent(t *testing.T) {
	withEmbeddedFS(t, sampleFS())
	gone := filepath.Join(t.TempDir(), "go-build999", "b001")
	base := filepath.Join(gone, "embedded-packs")
	embeddedMu.Lock()
	ok := loadFromCacheLocked(base, true)
	embeddedMu.Unlock()
	if ok {
		t.Error("a strict base under a deleted parent loaded; it should fall back")
	}
	if _, err := os.Stat(gone); !os.IsNotExist(err) {
		t.Errorf("the deleted $WORK dir %s was recreated (stat %v)", gone, err)
	}
	// Non-strict (the state dir, an override) still creates its parents.
	if _, err := ensureEmbeddedBase(base, false); err != nil {
		t.Errorf("a non-strict base did not create its parents: %v", err)
	}
}

func TestTestBinaryDetection(t *testing.T) {
	for _, tc := range []struct {
		exe          string
		testingFlags bool
		wantDir      string
		wantTest     bool
	}{
		{"/tmp/go-build123/b001/pkg.test", true, "/tmp/go-build123/b001/embedded-packs", true},
		{"/tmp/pkg.test", true, "", true},             // `go test -c` run by hand
		{"/tmp/debug.test2963", true, "", true},       // delve's name: the flags give it away
		{"/tmp/renamed", true, "", true},              // `go test -c -o renamed`
		{"/home/u/.local/bin/yolo", false, "", false}, // a real yolo
	} {
		dir, isTest := testBinaryCacheDirFor(tc.exe, tc.testingFlags)
		if dir != tc.wantDir || isTest != tc.wantTest {
			t.Errorf("%s (flags=%v) = (%q, %v), want (%q, %v)", tc.exe, tc.testingFlags, dir, isTest, tc.wantDir, tc.wantTest)
		}
	}
	// And the production probe sees THIS binary's flags: package testing registered them.
	if _, isTest := testBinaryCacheDir(); !isTest {
		t.Error("testBinaryCacheDir() does not recognise a running go test binary")
	}
}

// failingFS fails every Open of one path — a read error, not a content mismatch.
type failingFS struct {
	fs.FS
	bad string
}

func (f failingFS) Open(name string) (fs.File, error) {
	if name == f.bad {
		return nil, &fs.PathError{Op: "open", Path: name, Err: syscall.EMFILE}
	}
	return f.FS.Open(name)
}

// Only a MISMATCH quarantines. A verify that could not read proves nothing about the tree,
// and a quarantine is a rename out from under every process holding it.
func TestVerifyReadErrorFallsBackWithoutQuarantine(t *testing.T) {
	base, _ := withEmbeddedFS(t, sampleFS())
	sum, _ := EmbeddedHash()
	final := filepath.Join(base, sum)
	if len(Embedded()) != 2 {
		t.Fatalf("setup: %v", EmbeddedProblems())
	}
	ReleaseEmbedded()

	lease, outcome := adoptEmbedded(failingFS{sampleFS(), "beta/briefing/section.md"}, final)
	if lease != nil {
		lease.Close()
	}
	if outcome != adoptFailed {
		t.Errorf("adopt with a read error = %v, want adoptFailed (fall back, touch nothing)", outcome)
	}
	if got := names(t, base); len(got) != 1 || got[0] != sum {
		t.Errorf("base holds %v, want the tree left at its final name", got)
	}
	var mm treeMismatch
	if err := verifyEmbeddedTree(failingFS{sampleFS(), "beta/briefing/section.md"}, final); errors.As(err, &mm) {
		t.Errorf("a read error was classified as a mismatch: %v", err)
	}
}

// flakyFS answers one path with alternating bytes: the populate's copy and its verify read
// different content, which is what a filesystem that cannot hold a faithful copy looks like.
type flakyFS struct {
	a, b fstest.MapFS
	path string
	n    *int
}

func (f flakyFS) Open(name string) (fs.File, error) {
	if name == f.path {
		*f.n++
		if *f.n%2 == 0 {
			return f.b.Open(name)
		}
	}
	return f.a.Open(name)
}

// A tree this process cannot write faithfully is never published: it would make every LATER
// process quarantine a copy, forever. The populate falls back and leaves the base empty.
func TestUnfaithfulPopulateIsNeverPublished(t *testing.T) {
	a := sampleFS()
	b := sampleFS()
	b["beta/briefing/section.md"] = &fstest.MapFile{Data: []byte("HELLO\n")}
	n := 0
	base, _ := withEmbeddedFS(t, flakyFS{a: a, b: b, path: "beta/briefing/section.md", n: &n})

	Embedded()
	if _, fallback := EmbeddedLocation(); !fallback {
		t.Error("an unfaithful populate was adopted; want the fallback")
	}
	if got := names(t, base); len(got) != 0 {
		t.Errorf("base holds %v after an unfaithful populate, want nothing published or quarantined", got)
	}
}

// The base's own leftovers — a populate killed mid-write, a quarantine, a prune killed
// mid-delete — are swept by the next process that loads from it, by prune's rule. Other
// builds' FINAL trees are left alone: that is prune's question.
func TestDeadCacheLeftoversAreSwept(t *testing.T) {
	base, _ := withEmbeddedFS(t, sampleFS())
	old := time.Now().Add(-20 * time.Minute)
	ancient := time.Now().Add(-2 * time.Hour)
	mk := func(name string, lease bool, mtime time.Time) string {
		p := filepath.Join(base, name)
		if err := os.MkdirAll(filepath.Join(p, "alpha"), 0o755); err != nil {
			t.Fatal(err)
		}
		if lease {
			if err := os.WriteFile(filepath.Join(p, EmbeddedLeaseName), nil, 0o444); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.Chtimes(p, mtime, mtime); err != nil {
			t.Fatal(err)
		}
		return p
	}
	deadTmp := mk(".tmp-deadbeef-1", true, old)
	deadBad := mk(".bad-deadbeef-2-3", true, old)
	deadReap := mk(".reap-deadbeef-4-5", false, ancient)
	liveTmp := mk(".tmp-deadbeef-6", true, old)
	youngTmp := mk(".tmp-deadbeef-7", true, time.Now())
	unleasedYoung := mk(".tmp-deadbeef-8", false, old)
	otherBuild := mk("0123456789abcdef0123456789abcdef", true, ancient)

	holder, err := holdLease(filepath.Join(liveTmp, EmbeddedLeaseName))
	if err != nil || holder == nil {
		t.Fatalf("holding the live lease: %v", err)
	}
	t.Cleanup(func() { holder.Close() })

	if len(Embedded()) != 2 {
		t.Fatalf("load: %v", EmbeddedProblems())
	}
	for _, p := range []string{deadTmp, deadBad, deadReap} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s survived the base sweep (stat %v)", filepath.Base(p), err)
		}
	}
	for _, p := range []string{liveTmp, youngTmp, unleasedYoung, otherBuild} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s was reaped: %v", filepath.Base(p), err)
		}
	}
}

// The base is resolved through its symlinks where it is minted, so Pack.Root and the lease
// stay one directory for the process's life even if a link on the way is re-pointed later
// (macos-user's ~/.local, which every launch re-points at its own workspace).
func TestBaseIsResolvedThroughSymlinks(t *testing.T) {
	withEmbeddedFS(t, sampleFS())
	real := resolvedTempDir(t)
	link := filepath.Join(resolvedTempDir(t), "local")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	restore := OverrideEmbeddedCacheDir(filepath.Join(link, "embedded-packs"))
	t.Cleanup(restore)
	packs := Embedded()
	if len(packs) != 2 {
		t.Fatalf("load: %v", EmbeddedProblems())
	}
	root, _ := EmbeddedLocation()
	if !strings.HasPrefix(root, filepath.Join(real, "embedded-packs")+string(filepath.Separator)) {
		t.Errorf("tree root %s is not under the resolved base %s", root, real)
	}
	if !strings.HasPrefix(packs[0].Root, root) {
		t.Errorf("Pack.Root %s is not under the resolved tree %s", packs[0].Root, root)
	}
}
