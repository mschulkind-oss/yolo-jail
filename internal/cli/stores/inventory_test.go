package stores

import (
	"go/ast"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/prune"
)

// testOptions builds an Options pointed entirely at temp roots, so no test in
// this package can read or write the developer's real stores.
func testOptions(t *testing.T) (Options, string) {
	t.Helper()
	root := t.TempDir()
	state := filepath.Join(root, "state")
	o := Options{
		Out:           new(strings.Builder),
		Errs:          new(strings.Builder),
		Now:           func() time.Time { return time.Date(2026, 3, 11, 12, 0, 0, 0, time.UTC) },
		InJail:        func() bool { return false },
		DetectRuntime: func() string { return "podman" },
		GlobalStorage: func() string { return state },
		GlobalCache:   func() string { return filepath.Join(state, "cache") },
		SamplesDir:    func() string { return filepath.Join(state, "stores") },
		NixStore:      filepath.Join(root, "nix-store"),
		Exec: func([]string, time.Duration) prune.ProbeResult {
			return prune.ProbeResult{Ran: false}
		},
	}
	// Filled here so a test may call one collector directly (they are the units
	// worth testing) without every one of them having to defend against a nil seam.
	fillDefaults(&o)
	return o, state
}

func storeByKey(t *testing.T, rep Report, key string) Store {
	t.Helper()
	for _, s := range rep.Stores {
		if s.Key == key {
			return s
		}
	}
	t.Fatalf("no store %q in the inventory (have %v)", key, keysOf(rep))
	return Store{}
}

func keysOf(rep Report) []string {
	var out []string
	for _, s := range rep.Stores {
		out = append(out, s.Key)
	}
	return out
}

// TestInventoryOnAnEmptyMachineIsAllAbsent covers §5.5's other degenerate input:
// a store that does not exist prints absent with size 0, "not an error, because
// a fresh machine has almost none of them".
func TestInventoryOnAnEmptyMachineIsAllAbsent(t *testing.T) {
	o, _ := testOptions(t)
	rep := Inventory(o)
	for _, s := range rep.Stores {
		if s.Sizing != SizingAbsent && s.Sizing != SizingUnknown {
			t.Errorf("store %s on an empty machine reported %q (%d bytes); want absent",
				s.Key, s.Sizing, s.Bytes)
		}
	}
	if len(rep.Stores) == 0 {
		t.Fatal("an empty machine produced no rows at all; the store TABLE is the inventory, " +
			"and a store that is absent is still a store the user asked about")
	}
}

// TestCacheCoverageComesFromPrunesOwnLists: whether a cache subdir has a
// reclaimer is not a claim this package makes — it is read off the list the
// purge itself iterates, so the two cannot disagree.
//
// MUTATION: hardcode the covered list here and this test still passes, but the
// day prune adds a subdir the table goes stale silently. The assertion below is
// what makes the derivation load-bearing: an uncovered subdir must report none.
func TestCacheCoverageComesFromPrunesOwnLists(t *testing.T) {
	o, state := testOptions(t)
	covered := prune.CachePurgeDefaultSubdirs[0]
	for _, sub := range []string{covered, "nce-not-covered"} {
		writeFile(t, filepath.Join(state, "cache", sub, "f"), 10)
	}
	rows := cacheStores(o)

	got := map[string]Store{}
	for _, r := range rows {
		got[r.Key] = r
	}
	if r := got["cache."+covered]; r.Reclaimer.Func != "PurgeCacheByAge" || r.Verdict != VerdictYolo {
		t.Errorf("%s is in prune.CachePurgeDefaultSubdirs but reported reclaimer %q / verdict %q",
			covered, r.Reclaimer.Func, r.Verdict)
	}
	if r := got["cache.nce-not-covered"]; r.Reclaimer.Func != "" || r.Verdict != VerdictHuman {
		t.Errorf("an uncovered cache subdir reported reclaimer %q / verdict %q; the whole point of "+
			"this command is that a store nothing sweeps says so", r.Reclaimer.Func, r.Verdict)
	}
}

// TestImageCacheKeepIsAskedNotAssumed: the tar retention default moved with
// OQ-BF6 (0 on podman, 3 on Apple Container), and this row must ask
// prune.ResolveImageCacheKeep rather than carry a number of its own.
func TestImageCacheKeepIsAskedNotAssumed(t *testing.T) {
	o, state := testOptions(t)
	writeFile(t, filepath.Join(state, "cache", "images", "x.tar"), 4)
	for _, rt := range []string{"podman", "container"} {
		o.DetectRuntime = func() string { return rt }
		var row Store
		for _, r := range cacheStores(o) {
			if r.Key == "cache.images" {
				row = r
			}
		}
		want := prune.ResolveImageCacheKeep(prune.ImageCacheKeepUnset, rt)
		if !strings.Contains(row.Reclaimer.Detail, "keep "+itoa(want)) {
			t.Errorf("on %s the tar row says %q, want the runtime-resolved keep %d",
				rt, row.Reclaimer.Detail, want)
		}
	}
}

func itoa(n int) string { return fmtCount(n) }

// podmanListing is a fake `podman images -a --format json` payload.
const podmanListing = `[
 {"Id":"aaa","Names":["localhost/yolo-jail:8245a04218e9aabb"],"Size":3000000000},
 {"Id":"aaa","Names":["localhost/yolo-jail:latest"],"Size":3000000000},
 {"Id":"bbb","Names":null,"Size":2000000000},
 {"Id":"ccc","Names":null,"Size":1000000000},
 {"Id":"ddd","Names":["docker.io/library/postgres:16"],"Size":500000000}
]`

// TestUntaggedRowsAreSurfacedAsAClassNothingReclaims is the OQ-DF3 REACH half of
// this command's job: rows that predate the provenance label are left alone
// PERMANENTLY and surfaced HERE — one row naming the count and the bytes, why
// yolo declines (no ownership evidence), and that podman image prune is the
// user's to run. Explicitly not on the launch path.
func TestUntaggedRowsAreSurfacedAsAClassNothingReclaims(t *testing.T) {
	o, _ := testOptions(t)
	o.Exec = func(argv []string, _ time.Duration) prune.ProbeResult {
		if len(argv) > 1 && argv[1] == "images" {
			return prune.ProbeResult{Ran: true, RC: 0, Stdout: podmanListing}
		}
		return prune.ProbeResult{Ran: false}
	}
	rep := Inventory(o)

	untagged := storeByKey(t, rep, "images.untagged")
	if untagged.Count != 2 {
		t.Errorf("untagged rows counted %d, want 2", untagged.Count)
	}
	if untagged.Bytes != 3000000000 {
		t.Errorf("untagged bytes = %d, want 3000000000 — the row must name the BYTES, not just the count",
			untagged.Bytes)
	}
	if untagged.Reclaimer.Func != "" || untagged.Reclaimer.Trigger != "" {
		t.Errorf("the untagged class named a reclaimer (%+v); OQ-DF3 REACH left these alone permanently",
			untagged.Reclaimer)
	}
	if untagged.Verdict != VerdictHuman {
		t.Errorf("untagged verdict = %q, want %q", untagged.Verdict, VerdictHuman)
	}
	for _, want := range []string{"evidence", "podman image prune"} {
		if !strings.Contains(untagged.Note, want) {
			t.Errorf("the untagged row's note does not mention %q; the ruling requires it to say why "+
				"yolo declines and what is the user's to run:\n%s", want, untagged.Note)
		}
	}

	ours := storeByKey(t, rep, "images.yolo")
	if ours.Count != 1 || ours.Bytes != 3000000000 {
		t.Errorf("yolo-jail images = %d rows / %d bytes, want 1 / 3000000000 (one image, two tags)",
			ours.Count, ours.Bytes)
	}
	if ours.Reclaimer.Func != "PruneOldImages" {
		t.Errorf("tagged images report reclaimer %q, want PruneOldImages", ours.Reclaimer.Func)
	}
	other := storeByKey(t, rep, "images.other")
	if other.Count != 1 || other.Verdict != VerdictNotOurs {
		t.Errorf("a non-yolo image reported %d rows / verdict %q, want 1 / %q",
			other.Count, other.Verdict, VerdictNotOurs)
	}
}

// TestAnUnreachableRuntimeIsUnknownNotEmpty: "podman is down" and "you have no
// images" are different facts. Reporting the second for the first would tell a
// user their image store is empty while it holds 50 GB.
func TestAnUnreachableRuntimeIsUnknownNotEmpty(t *testing.T) {
	o, _ := testOptions(t) // the default Exec fake never runs
	rep := Inventory(o)
	for _, key := range []string{"images.yolo", "images.untagged", "images.other"} {
		s := storeByKey(t, rep, key)
		if s.Sizing != SizingUnknown {
			t.Errorf("%s reported %q with an unreachable runtime, want %q", key, s.Sizing, SizingUnknown)
		}
		if s.Reason == "" {
			t.Errorf("%s reported unknown with no reason", key)
		}
	}
}

// TestNixOutputClassesAreCountedAndSized covers the largest class the design
// found with no reclaimer and no trigger.
func TestNixOutputClassesAreCountedAndSized(t *testing.T) {
	o, _ := testOptions(t)
	for _, name := range []string{
		"aaa-yolo-jail-install-prefix", "bbb-yolo-jail-install-prefix",
		"ccc-yolo-jail-go-0-dev", "ddd-something-else",
	} {
		writeFile(t, filepath.Join(o.NixStore, name, "bin", "yolo"), 100)
	}
	writeFile(t, filepath.Join(o.NixStore, "eee-stream-yolo-jail"), 50)

	rep := Inventory(o)
	prefixes := storeByKey(t, rep, "nix.install-prefix")
	if prefixes.Count != 2 || prefixes.Bytes != 200 {
		t.Errorf("install prefixes = %d paths / %d bytes, want 2 / 200", prefixes.Count, prefixes.Bytes)
	}
	if prefixes.Reclaimer.Func != "SupersededStoreOutputs" || prefixes.Verdict != VerdictYolo {
		t.Errorf("install prefixes report reclaimer %+v / verdict %q; OQ-BF3 shipped the named "+
			"store delete and this row has to follow the tree", prefixes.Reclaimer, prefixes.Verdict)
	}
	streams := storeByKey(t, rep, "nix.stream")
	if streams.Count != 1 || streams.Sizing != SizingMeasured || streams.Bytes != 50 {
		t.Errorf("stream scripts = %d paths / %q / %d bytes, want 1 / measured / 50 "+
			"(a single-file store path is not an unreadable one)",
			streams.Count, streams.Sizing, streams.Bytes)
	}
	goBuilds := storeByKey(t, rep, "nix.go-build")
	if goBuilds.Count != 1 {
		t.Errorf("go builds = %d paths, want 1", goBuilds.Count)
	}
}

// TestTheFrameIsStatedAndFollowsInJail. §5.5 puts this in an IMPORTANT callout:
// paths.GlobalCache() resolves to the host's tree for a host yolo and a jail's
// own for an in-jail one, and a report that does not say whose disk it walked
// invites the exact misreading that produced a wrong retraction in the design.
func TestTheFrameIsStatedAndFollowsInJail(t *testing.T) {
	o, _ := testOptions(t)
	if rep := Inventory(o); rep.Frame != "host" || rep.FrameNote == "" {
		t.Errorf("frame = %q / note %q, want host with a note", rep.Frame, rep.FrameNote)
	}
	o.InJail = func() bool { return true }
	rep := Inventory(o)
	if rep.Frame != "in-jail" {
		t.Fatalf("frame = %q inside a jail, want in-jail", rep.Frame)
	}
	out := new(strings.Builder)
	o.Out = out
	renderText(rep, o)
	if !strings.Contains(out.String(), "in-jail") {
		t.Error("the rendered header does not state the frame; it is part of the output, not a footnote")
	}
}

// TestEveryNamedReclaimerExists parses internal/prune and internal/capture and
// requires every reclaimer this package NAMES to be a real exported function
// there.
//
// It is what keeps the reclaimer column from becoming prose: a symbol that was
// renamed or deleted fails here instead of being printed to users for months as
// the thing that looks after their gigabytes.
func TestEveryNamedReclaimerExists(t *testing.T) {
	exported := map[string]bool{}
	for _, pkg := range []string{"../../prune", "../../capture"} {
		short := filepath.Base(pkg)
		for _, f := range parsePackageFiles(t, pkg) {
			for _, decl := range f.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || !fn.Name.IsExported() || fn.Recv != nil {
					continue
				}
				exported[fn.Name.Name] = true
				exported[short+"."+fn.Name.Name] = true
			}
		}
	}

	named := map[string]string{}
	for name, r := range stateReclaimers {
		named[r.Func] = "stateReclaimers[" + name + "]"
	}
	// The rows built inline, which the table above does not cover.
	named["PurgeCacheByAge"] = "cacheStores"
	named["PruneImageCache"] = "cacheStores (the tar row)"
	named["PruneOldImages"] = "imageStores (the tagged row)"

	for fn, where := range named {
		if fn == "" {
			continue
		}
		if !exported[fn] {
			t.Errorf("%s names reclaimer %q, which is not an exported function of internal/prune "+
				"or internal/capture any more", where, fn)
		}
	}
}

// TestNixClassCoverageMatchesTheReclaimer derives what this package CLAIMS about
// OQ-BF3's reach from what internal/prune actually selects.
//
// The suffix list lives unexported in internal/prune, so the table here cannot
// read it — but it can run the reclaimer against a temp store holding one path
// of every class and compare. A suffix added there and not here (or removed
// there and still claimed here) fails, instead of being printed to users as the
// thing looking after their gigabytes.
func TestNixClassCoverageMatchesTheReclaimer(t *testing.T) {
	store := t.TempDir()
	roots := t.TempDir() // no roots at all: everything is unrooted
	bySuffix := map[string]string{}
	for i, c := range nixOutputClasses {
		p := filepath.Join(store, string(rune('a'+i))+c.suffix)
		writeFile(t, filepath.Join(p, "f"), 1)
		bySuffix[c.suffix] = p
	}
	// Older than the grace floor, which exists for a launch whose rooting has not
	// happened yet — not a retention policy.
	old := time.Now().Add(-2 * prune.StoreOutputGrace)
	for _, p := range bySuffix {
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatal(err)
		}
	}

	selected := map[string]bool{}
	for _, p := range prune.SupersededStoreOutputs(store, []string{roots}, prune.StoreOutputGrace, time.Now()) {
		for suffix, path := range bySuffix {
			if p == path {
				selected[suffix] = true
			}
		}
	}
	for _, c := range nixOutputClasses {
		if selected[c.suffix] != c.reaped {
			t.Errorf("class %s: this package says reaped=%v, prune.SupersededStoreOutputs says %v",
				c.key, c.reaped, selected[c.suffix])
		}
	}
}

// TestInJailTheStoreOutputReclaimerIsNotClaimed: OQ-BF3's pass refuses in-jail,
// where /nix/store is a read-only bind of the host's and the gcroots dir is
// unmounted. A row promising a sweep that will never run there is the kind of
// unchecked claim this whole command replaces.
func TestInJailTheStoreOutputReclaimerIsNotClaimed(t *testing.T) {
	o, _ := testOptions(t)
	writeFile(t, filepath.Join(o.NixStore, "aaa-yolo-jail-install-prefix", "bin", "yolo"), 10)
	o.InJail = func() bool { return true }

	s := storeByKey(t, Inventory(o), "nix.install-prefix")
	if s.Reclaimer.Func != "" || s.Verdict != VerdictHuman {
		t.Errorf("in-jail the install-prefix row claims %+v / %q; the pass is host-only",
			s.Reclaimer, s.Verdict)
	}
	if !strings.Contains(s.Note, "HOST") {
		t.Errorf("the in-jail row does not say a host yolo is what reclaims these: %q", s.Note)
	}
}
