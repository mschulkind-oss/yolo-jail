package prune

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/outfmt"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

const (
	hashCur   = "0123456789abcdef0123456789abcdef"
	hashOther = "fedcba9876543210fedcba9876543210"
)

// mkEmbeddedDir makes dir with a file inside, an optional .lease, and an mtime age ago.
func mkEmbeddedDir(t *testing.T, dir string, lease bool, age time.Duration, now time.Time) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "pack"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pack", "pack.json"), []byte("{}\n"), 0o444); err != nil {
		t.Fatal(err)
	}
	if lease {
		if err := os.WriteFile(filepath.Join(dir, packload.EmbeddedLeaseName), nil, 0o444); err != nil {
			t.Fatal(err)
		}
	}
	when := now.Add(-age)
	if err := os.Chtimes(dir, when, when); err != nil {
		t.Fatal(err)
	}
	return dir
}

// holdShared takes the SHARED lock a live reader holds, until the test ends.
func holdShared(t *testing.T, dir string) {
	t.Helper()
	f, err := os.Open(filepath.Join(dir, packload.EmbeddedLeaseName))
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_SH); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
}

func exists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

func whyOf(r EmbeddedReap, name string) (string, bool) {
	for _, e := range r.Removed {
		if e.Name == name {
			return e.Why, true
		}
	}
	for _, e := range r.Kept {
		if e.Name == name {
			return e.Why, false
		}
	}
	return "", false
}

// The cache base, entry by entry: the lease decides, the current build's tree is never
// touched, and anything that cannot be proven unused stays.
func TestEmbeddedPackTreesLeaseRule(t *testing.T) {
	now := time.Now()
	for _, apply := range []bool{false, true} {
		t.Run(map[bool]string{false: "dry-run", true: "apply"}[apply], func(t *testing.T) {
			base := t.TempDir()
			old := 2 * time.Hour
			cur := mkEmbeddedDir(t, filepath.Join(base, hashCur), true, old, now)
			free := mkEmbeddedDir(t, filepath.Join(base, hashOther), true, old, now)
			held := mkEmbeddedDir(t, filepath.Join(base, "11111111111111111111111111111111"), true, old, now)
			holdShared(t, held)
			noLease := mkEmbeddedDir(t, filepath.Join(base, "22222222222222222222222222222222"), false, old, now)
			young := mkEmbeddedDir(t, filepath.Join(base, "33333333333333333333333333333333"), true, time.Minute, now)
			unknown := mkEmbeddedDir(t, filepath.Join(base, "44444444444444444444444444444444"), false, old, now)
			// A lease that cannot be opened for a reason other than absence: a symlink loop.
			if err := os.Symlink(packload.EmbeddedLeaseName, filepath.Join(unknown, packload.EmbeddedLeaseName)); err != nil {
				t.Fatal(err)
			}
			backdate(t, unknown, now.Add(-old))
			tmpFree := mkEmbeddedDir(t, filepath.Join(base, ".tmp-"+hashOther+"-123"), true, 20*time.Minute, now)
			tmpHeld := mkEmbeddedDir(t, filepath.Join(base, ".tmp-"+hashCur+"-456"), true, 20*time.Minute, now)
			holdShared(t, tmpHeld)
			tmpUnleasedYoung := mkEmbeddedDir(t, filepath.Join(base, ".tmp-"+hashOther+"-789"), false, 30*time.Minute, now)
			tmpUnleasedOld := mkEmbeddedDir(t, filepath.Join(base, ".tmp-"+hashOther+"-999"), false, old, now)
			bad := mkEmbeddedDir(t, filepath.Join(base, ".bad-"+hashCur+"-1-2"), true, old, now)
			notOurs := mkEmbeddedDir(t, filepath.Join(base, "notours"), true, old, now)
			// A final-shaped name that is a symlink to a real tree elsewhere: never followed.
			elsewhere := mkEmbeddedDir(t, filepath.Join(t.TempDir(), "x"), true, old, now)
			link := filepath.Join(base, "55555555555555555555555555555555")
			if err := os.Symlink(elsewhere, link); err != nil {
				t.Fatal(err)
			}

			r := PruneEmbeddedPackTrees(base, hashCur, true, apply, now)
			if r.Declined != "" {
				t.Fatalf("declined: %s", r.Declined)
			}

			reaped := []string{free, tmpFree, tmpUnleasedOld, bad}
			kept := map[string]string{
				cur:              whyCurrent,
				held:             whyHeld,
				noLease:          whyNoLease,
				young:            whyYoung,
				unknown:          whyCouldNotAsk,
				tmpHeld:          whyHeld,
				tmpUnleasedYoung: whyYoung,
				notOurs:          whyUnrecognized,
				link:             whyUnrecognized,
			}
			for _, p := range reaped {
				if _, removed := whyOf(r, filepath.Base(p)); !removed {
					t.Errorf("%s was not reaped: %+v", filepath.Base(p), r.Kept)
				}
				if exists(p) == apply {
					t.Errorf("%s exists=%v after apply=%v", filepath.Base(p), exists(p), apply)
				}
			}
			for p, want := range kept {
				why, removed := whyOf(r, filepath.Base(p))
				if removed || why != want {
					t.Errorf("%s: removed=%v why=%q, want kept for %q", filepath.Base(p), removed, why, want)
				}
				if !exists(p) {
					t.Errorf("kept entry %s is gone", filepath.Base(p))
				}
			}
			if !exists(filepath.Join(elsewhere, "pack", "pack.json")) {
				t.Error("the symlink's target was touched")
			}
			if r.RemovedCount() != len(reaped) || r.Bytes <= 0 {
				t.Errorf("removed %d (%d B), want %d with bytes", r.RemovedCount(), r.Bytes, len(reaped))
			}
			// Nothing is left mid-rename, and no lock this sweep took is still held: a
			// dry run must leave every free lease free for the next reader.
			entries, _ := os.ReadDir(base)
			for _, e := range entries {
				if strings.HasPrefix(e.Name(), ".reap-") {
					t.Errorf("left %s behind", e.Name())
				}
			}
			if !apply {
				if st, unlock, _ := packload.ProbeLease(free); st != packload.LeaseFree {
					t.Errorf("after a dry run the free tree's lease is %v, want free", st)
				} else {
					unlock()
				}
			}
		})
	}
}

// THE CURRENT BUILD'S TREE SURVIVES --apply even when nothing holds it and it is old —
// the case where the lease alone would say "reap". Driven through Run with the REAL
// packload tree and hash, so it fails if the section's current-hash wiring goes, not
// only if the function's skip does.
func TestPruneApplyNeverRemovesTheCurrentBuildsTree(t *testing.T) {
	o, gs := baseOpts(t)
	// Resolved: the loader resolves its base through symlinks (darwin's t.TempDir() is under
	// one), and the comparisons below are against the tree root it reports.
	resolvedGs, err := filepath.EvalSymlinks(gs)
	if err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(resolvedGs, embeddedPacksLeaf)
	restore := packload.OverrideEmbeddedCacheDir(base)
	t.Cleanup(restore)
	if len(packload.Embedded()) == 0 {
		t.Fatalf("no embedded packs: %v", packload.EmbeddedProblems())
	}
	root, fallback := packload.EmbeddedLocation()
	if fallback || filepath.Dir(root) != base {
		t.Fatalf("tree at %s (fallback=%v), want under %s", root, fallback, base)
	}
	sum, ok := packload.EmbeddedHash()
	if !ok || filepath.Base(root) != sum {
		t.Fatalf("hash %q ok=%v does not name the tree %s", sum, ok, root)
	}
	packload.ReleaseEmbedded() // the lease is FREE now: only the current-build rule keeps it
	if st, unlock, _ := packload.ProbeLease(root); st != packload.LeaseFree {
		t.Fatalf("lease %v after release, want free", st)
	} else {
		unlock()
	}
	// Old by the real clock (the sweep's age floor) and by the fixture's.
	ancient := time.Now().Add(-48 * time.Hour)
	if o.Now().Before(ancient) {
		ancient = o.Now().Add(-48 * time.Hour)
	}
	backdate(t, root, ancient)
	other := mkEmbeddedDir(t, filepath.Join(base, hashOther), true, 0, ancient)

	var buf bytes.Buffer
	o.Out = &buf
	o.Apply = true
	if rc := Run(o); rc != 0 {
		t.Fatalf("rc=%d\n%s", rc, buf.String())
	}
	if !exists(filepath.Join(root, packload.EmbeddedLeaseName)) {
		t.Fatalf("--apply deleted the current build's tree %s:\n%s", root, buf.String())
	}
	if exists(other) {
		t.Errorf("another build's free tree survived --apply:\n%s", buf.String())
	}
	out := buf.String()
	for _, want := range []string{
		"Embedded pack trees  (state dir: embedded-packs/, one per build)",
		"  this build's tree: " + sum + " (never removed)",
		"    • " + hashOther + "  ",
		"  kept: 1 " + whyCurrent,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("report lacks %q:\n%s", want, out)
		}
	}
	// And the tree is still adoptable: the next reader gets the same root back.
	if len(packload.Embedded()) == 0 {
		t.Fatal(packload.EmbeddedProblems())
	}
	if again, _ := packload.EmbeddedLocation(); again != root {
		t.Errorf("re-adopted %s, want %s", again, root)
	}
}

// With no hash, any tree might be this build's, so every final tree stays — but a
// leftover (not a tree anyone adopts) is still judged by its lease.
func TestEmbeddedPackTreesUnknownHashKeepsEveryTree(t *testing.T) {
	now := time.Now()
	base := t.TempDir()
	a := mkEmbeddedDir(t, filepath.Join(base, hashCur), true, 48*time.Hour, now)
	b := mkEmbeddedDir(t, filepath.Join(base, hashOther), true, 48*time.Hour, now)
	tmp := mkEmbeddedDir(t, filepath.Join(base, ".tmp-"+hashOther+"-1"), true, 48*time.Hour, now)
	r := PruneEmbeddedPackTrees(base, "", false, true, now)
	if !exists(a) || !exists(b) {
		t.Fatalf("a tree went with the hash unknown: %+v", r.Removed)
	}
	if exists(tmp) {
		t.Error("a free leftover survived")
	}
	if why, _ := whyOf(r, hashCur); why != whyCurrentUnknown {
		t.Errorf("why = %q", why)
	}
}

// Only a WHOLE scan failing declines (OQ-LS2); a missing base is simply nothing to do.
func TestEmbeddedSweepsDeclineOnlyOnAWholeScanFailure(t *testing.T) {
	now := time.Now()
	if r := PruneEmbeddedPackTrees(filepath.Join(t.TempDir(), "absent"), hashCur, true, true, now); r.Declined != "" {
		t.Errorf("a missing base declined: %s", r.Declined)
	}
	file := filepath.Join(t.TempDir(), "f")
	mustWrite(t, file, []byte("x"))
	if r := PruneEmbeddedPackTrees(file, hashCur, true, true, now); r.Declined == "" {
		t.Error("an unlistable base did not decline")
	}
	if r := PruneLegacyEmbeddedTemp([]string{file}, nil, true, true, now); r.Declined == "" {
		t.Error("an unlistable temp dir did not decline")
	}
	if r := PruneLegacyEmbeddedTemp([]string{filepath.Join(t.TempDir(), "absent")}, nil, true, true, now); r.Declined != "" {
		t.Errorf("a missing temp dir declined: %s", r.Declined)
	}
}

// TMPDIR: the three name classes, each by its own evidence, and nothing that merely
// shares a prefix.
func TestLegacyEmbeddedTempRules(t *testing.T) {
	now := time.Now()
	dir := t.TempDir()
	mtime := now.Add(-5 * time.Hour)
	mk := func(name string, lease bool, at time.Time) string {
		return mkEmbeddedDir(t, filepath.Join(dir, name), lease, now.Sub(at), now)
	}
	yolo := func(pid int, start time.Time) ProcStart { return ProcStart{PID: pid, Name: "yolo", Start: start} }

	aFree := mk("yolo-embedded-100", false, mtime)                                // no candidate: reap
	aDaemon := mk("yolo-embedded-101", false, mtime)                              // only a daemon from a day before: reap
	aPinned := mk("yolo-embedded-102", false, mtime.Add(-time.Hour))              // its creator lives
	aTest := mk("yolo-embedded-103", false, mtime.Add(-2*time.Hour))              // a .test binary started earlier lives
	aYoung := mk("yolo-embedded-104", false, now.Add(-30*time.Minute))            // under the 1h floor
	bPinned := mk("yolo-embedded-packs-200", false, mtime.Add(-3*time.Hour))      // lazily made by a long-lived yolo
	bFree := mk("yolo-cli-packs-201", false, mtime)                               // every candidate started after it
	cFree := mk(packload.EmbeddedFallbackPrefix+"300", true, now.Add(-time.Hour)) // dead fallback
	cHeld := mk(packload.EmbeddedFallbackPrefix+"301", true, now.Add(-time.Hour))
	holdShared(t, cHeld)
	cYoung := mk(packload.EmbeddedFallbackPrefix+"302", true, now.Add(-time.Minute))
	// Not ours, whatever their prefix.
	ignored := []string{
		mk("yolo-embedded-abc", false, mtime),
		mk("yolo-embedded-packs-12x", false, mtime),
		mk("yolo-official-packs-400", false, mtime),
	}
	sock := filepath.Join(dir, "yolo-embedded-500")
	_ = os.RemoveAll(sock)
	mustWrite(t, sock, []byte("not a dir"))
	ignored = append(ignored, sock)

	procs := []ProcStart{
		yolo(1, mtime.Add(-24*time.Hour)),                            // a long-lived daemon: pins only class B
		yolo(2, mtime.Add(-time.Hour).Add(-time.Second)),             // aPinned's creator
		{PID: 3, Name: "cli.test", Start: mtime.Add(-3 * time.Hour)}, // pins class A written after it
		{PID: 4, Name: "bash", Start: mtime.Add(-48 * time.Hour)},    // not a candidate at all
		yolo(5, now.Add(-time.Minute)),                               // this prune: pins nothing old
	}
	// Each run hands the sweep one set of evidence, so each rule is seen on its own.
	r := PruneLegacyEmbeddedTemp([]string{dir, dir + "/."}, procs[:2], true, false, now)
	check := func(r EmbeddedReap, p string, wantRemoved bool) {
		t.Helper()
		why, removed := whyOf(r, filepath.Base(p))
		if removed != wantRemoved {
			t.Errorf("%s: removed=%v (%s), want %v", filepath.Base(p), removed, why, wantRemoved)
		}
	}
	check(r, aFree, true)
	check(r, aDaemon, true)
	check(r, aPinned, false)
	check(r, aYoung, false)
	check(r, bPinned, false)
	check(r, bFree, false) // the day-old daemon started before it too: pinned
	check(r, cFree, true)
	check(r, cHeld, false)
	check(r, cYoung, false)
	for _, p := range ignored {
		if _, removed := whyOf(r, filepath.Base(p)); removed {
			t.Errorf("%s matched a pattern it must not", filepath.Base(p))
		}
		for _, e := range r.Kept {
			if e.Path == p {
				t.Errorf("%s was judged at all", p)
			}
		}
	}
	// The same directory twice (once through a different spelling) is scanned once.
	seen := map[string]int{}
	for _, e := range append(append([]EmbeddedEntry{}, r.Removed...), r.Kept...) {
		seen[e.Name]++
	}
	for n, c := range seen {
		if c != 1 {
			t.Errorf("%s judged %d times", n, c)
		}
	}
	// The .test rule: a test binary materializes whenever a test asks, not only at init.
	r = PruneLegacyEmbeddedTemp([]string{dir}, []ProcStart{procs[2], procs[3], procs[4]}, true, false, now)
	check(r, aTest, false)
	check(r, aFree, false) // cli.test started before it
	// The lazy rule's other side: the only candidates started after its mtime+2m.
	r = PruneLegacyEmbeddedTemp([]string{dir}, []ProcStart{procs[3], procs[4]}, true, false, now)
	check(r, bFree, true)
	check(r, bPinned, true)
	// A truncated name cannot be ruled out, so it pins like a test binary.
	r = PruneLegacyEmbeddedTemp([]string{dir}, []ProcStart{{PID: 9, Name: "long-process-na", NameTruncated: true, Start: mtime.Add(-48 * time.Hour)}}, true, false, now)
	check(r, aFree, false)

	// Dry run: nothing above was deleted.
	for _, p := range []string{aFree, aDaemon, cFree, bFree} {
		if !exists(p) {
			t.Fatalf("dry run deleted %s", p)
		}
	}

	// An unreadable process table keeps every legacy dir; the leased fallback is still
	// judged, since its evidence is its lease.
	r = PruneLegacyEmbeddedTemp([]string{dir}, nil, false, true, now)
	for _, p := range []string{aFree, aDaemon, bFree} {
		if why, removed := whyOf(r, filepath.Base(p)); removed || why != whyProcsUnknown {
			t.Errorf("%s: removed=%v why=%q with the table unknown", filepath.Base(p), removed, why)
		}
		if !exists(p) {
			t.Errorf("%s deleted with the process table unknown", p)
		}
	}
	if exists(cFree) || !exists(cHeld) || !exists(cYoung) {
		t.Errorf("fallback lease rule under apply: free gone=%v held kept=%v young kept=%v",
			!exists(cFree), exists(cHeld), exists(cYoung))
	}

	// Apply with the evidence: the unattributed legacy dir goes, the pinned ones stay.
	r = PruneLegacyEmbeddedTemp([]string{dir}, procs[:2], true, true, now)
	if exists(aFree) || exists(aDaemon) || exists(aTest) {
		t.Error("unattributed legacy dirs survived --apply (aTest's pin is not in this evidence)")
	}
	if !exists(aPinned) || !exists(bPinned) || !exists(aYoung) {
		t.Error("a pinned or young legacy dir was deleted")
	}
	for _, p := range ignored {
		if !exists(p) {
			t.Errorf("%s was deleted", p)
		}
	}
	if r.Bytes <= 0 || r.RemovedCount() != 3 {
		t.Errorf("apply reclaimed %d dirs / %d B, want 3 with bytes", r.RemovedCount(), r.Bytes)
	}
}

// The attribution window, at its edges.
func TestLegacyAttributionWindow(t *testing.T) {
	mtime := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	at := func(d time.Duration) []ProcStart {
		return []ProcStart{{PID: 1, Name: "yolo-jaild", Start: mtime.Add(d)}}
	}
	for _, tc := range []struct {
		init   bool
		offset time.Duration
		pinned bool
	}{
		{true, 0, true},
		{true, -legacyAttributeBefore, true},
		{true, -legacyAttributeBefore - time.Second, false},
		{true, legacyAttributeAfter, true},
		{true, legacyAttributeAfter + time.Second, false},
		{false, -30 * 24 * time.Hour, true},
		{false, legacyAttributeAfter, true},
		{false, legacyAttributeAfter + time.Second, false},
	} {
		if _, got := legacyAttributed(tc.init, mtime, at(tc.offset)); got != tc.pinned {
			t.Errorf("init=%v start=mtime%+v: pinned=%v, want %v", tc.init, tc.offset, got, tc.pinned)
		}
	}
	// A non-candidate never pins, whenever it started.
	if _, got := legacyAttributed(true, mtime, []ProcStart{{Name: "bash", Start: mtime}}); got {
		t.Error("bash pinned a legacy tree")
	}
}

// The derived cache base is the directory packload writes: the state dir's child, under
// whatever home resolves — and it moves with an injected GlobalStorage, so a test's temp
// root never reaches the real home.
func TestEmbeddedPacksDirDefaultIsThePathsLocation(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	var o Options
	fillDefaults(&o)
	if got, want := o.EmbeddedPacksDir(), paths.EmbeddedPacksDir(); got != want || want == "" {
		t.Errorf("default base %q, want paths.EmbeddedPacksDir() %q", got, want)
	}
	o2 := Options{GlobalStorage: func() string { return "/elsewhere" }}
	fillDefaults(&o2)
	if got := o2.EmbeddedPacksDir(); got != "/elsewhere/"+embeddedPacksLeaf {
		t.Errorf("base did not follow the injected storage root: %q", got)
	}
}

// The section is WIRED: it is in the report, gated by its flag, counted in the total and
// carried by the JSON document.
func TestEmbeddedSectionsAreWired(t *testing.T) {
	o, gs := baseOpts(t)
	ancient := o.Now().Add(-48 * time.Hour)
	mkEmbeddedDir(t, filepath.Join(gs, embeddedPacksLeaf, hashOther), true, 0, ancient)
	tmp := o.TempDirs()[0]
	mkEmbeddedDir(t, filepath.Join(tmp, "yolo-embedded-777"), false, 0, ancient)
	// The fixture's clock is in the past; the real sweep compares against it, so the
	// dirs are old by that clock.
	var buf bytes.Buffer
	o.Out = &buf
	o.Format = outfmt.JSON
	if rc := Run(o); rc != 0 {
		t.Fatalf("rc=%d", rc)
	}
	var rep Report
	if err := json.Unmarshal(buf.Bytes(), &rep); err != nil {
		t.Fatal(err)
	}
	got := map[string]ReportCategory{}
	for _, c := range rep.Categories {
		got[c.Name] = c
	}
	if c := got["embedded_pack_trees"]; c.Count != 1 || c.Bytes <= 0 {
		t.Errorf("embedded_pack_trees = %+v, want 1 tree with bytes", c)
	}
	if c := got["leaked_embedded_temp"]; c.Count != 1 || c.Bytes <= 0 {
		t.Errorf("leaked_embedded_temp = %+v, want 1 dir with bytes", c)
	}
	if rep.TotalBytes < got["embedded_pack_trees"].Bytes+got["leaked_embedded_temp"].Bytes {
		t.Errorf("total %d does not include the embedded sweeps", rep.TotalBytes)
	}

	o.Format = ""
	o.NoEmbeddedPacks = true
	buf.Reset()
	Run(o)
	for _, gone := range []string{"Embedded pack trees", "Leaked embedded-pack temp dirs"} {
		if strings.Contains(buf.String(), gone) {
			t.Errorf("--no-embedded-packs left %q in the report", gone)
		}
	}
	if !exists(filepath.Join(gs, embeddedPacksLeaf, hashOther)) || !exists(filepath.Join(tmp, "yolo-embedded-777")) {
		t.Error("a dry run deleted something")
	}
}

// Another user's directory is never judged — on macOS the sandbox account's copies share
// /tmp with the admin's. Needs root to mint a foreign owner, so it skips elsewhere.
func TestEmbeddedSweepsSkipAnotherUsersDirs(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("minting a foreign-owned directory needs root")
	}
	now := time.Now()
	tmp := t.TempDir()
	legacy := mkEmbeddedDir(t, filepath.Join(tmp, "yolo-embedded-900"), false, 48*time.Hour, now)
	fallback := mkEmbeddedDir(t, filepath.Join(tmp, packload.EmbeddedFallbackPrefix+"901"), true, 48*time.Hour, now)
	base := t.TempDir()
	tree := mkEmbeddedDir(t, filepath.Join(base, hashOther), true, 48*time.Hour, now)
	for _, p := range []string{legacy, fallback, tree} {
		if err := os.Lchown(p, 4242, 4242); err != nil {
			t.Fatal(err)
		}
	}
	r := PruneLegacyEmbeddedTemp([]string{tmp}, nil, true, true, now)
	if r.RemovedCount() != 0 || r.Skipped[whyOtherUser] != 2 || !exists(legacy) || !exists(fallback) {
		t.Errorf("another user's TMPDIR copies: removed %d, skipped %v", r.RemovedCount(), r.Skipped)
	}
	c := PruneEmbeddedPackTrees(base, hashCur, true, true, now)
	if c.RemovedCount() != 0 || !exists(tree) {
		t.Errorf("another user's cache tree was removed: %+v", c.Removed)
	}
}
