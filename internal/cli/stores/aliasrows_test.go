package stores

import (
	"encoding/json"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/hostcas"
	"github.com/mschulkind-oss/yolo-jail/internal/prune"
)

// aliasDisposition is a fixture whose paths cannot be confused with any temp
// root, so an assertion about "the host's bytes" can only be satisfied by the
// row this section adds.
func aliasDisposition(hostSrc, stranded string, aliased bool) hostcas.Disposition {
	d := hostcas.Disposition{
		Store: hostcas.Store{
			Name: "pants-lmdb-store", Tool: "pants", Root: hostcas.RootCache, JailRel: "pants/lmdb_store",
			Evidence: "digest-named blobs, verified",
		},
		Aliased:  aliased,
		Source:   hostSrc,
		Dest:     "/home/agent/.cache/pants/lmdb_store",
		Stranded: stranded,
		Code:     hostcas.CodeAliased,
	}
	if !aliased {
		d.Code = hostcas.CodeAbsent
		d.Reason = "this host has no pants store"
	}
	return d
}

// THE ANTI-DOUBLE-COUNT, and it is the property the whole section exists to
// protect: the host's own store must never be summed into yolo's footprint.
// stateStores folds the shared-cache section into the state dir's `cache/` row,
// so an alias row placed in that section would inflate yolo's total by a 27 G
// tree yolo does not own and cannot reclaim.
func TestAliasRowIsNeverCountedInYolosOwnCache(t *testing.T) {
	o, state := testOptions(t)
	hostSrc := filepath.Join(t.TempDir(), "host-cache", "pants", "lmdb_store")
	writeFile(t, filepath.Join(hostSrc, "data.mdb"), 4096)
	stranded := filepath.Join(state, "cache", "pants", "lmdb_store")
	writeFile(t, filepath.Join(stranded, "old.mdb"), 100)
	o.HostCAS = func() []hostcas.Disposition {
		return []hostcas.Disposition{aliasDisposition(hostSrc, stranded, true)}
	}

	rep := Inventory(o)
	alias := storeByKey(t, rep, "alias.pants-lmdb-store")
	if alias.Section != SectionAlias {
		t.Fatalf("alias row is in section %q, want %q — any other section is summed somewhere",
			alias.Section, SectionAlias)
	}
	if alias.Bytes != 4096 {
		t.Errorf("alias row bytes = %d, want the host store's 4096", alias.Bytes)
	}

	// The jail's own cache row must count ONLY the stranded copy.
	cacheRow := storeByKey(t, rep, "state.cache")
	if cacheRow.Bytes != 100 {
		t.Errorf("state.cache = %d bytes, want 100 (the stranded copy alone). The host's %d "+
			"bytes have been folded into yolo's own footprint.", cacheRow.Bytes, alias.Bytes)
	}
	pants := storeByKey(t, rep, "cache.pants")
	if pants.Bytes != 100 {
		t.Errorf("cache.pants = %d bytes, want 100 — the shared-cache section walks yolo's "+
			"tree, never the host's", pants.Bytes)
	}
}

// MUST NOT OFFER TO RECLAIM BYTES THE HOST OWNS. renderText's "what nothing
// reclaims" list filters on VerdictHuman precisely because that is the class a
// user could decide about on yolo's behalf; another owner's build cache is not
// in it, and §5.5's forbidden list is what says so.
func TestAliasRowIsNotOffered(t *testing.T) {
	o, state := testOptions(t)
	hostSrc := filepath.Join(t.TempDir(), "host-cache", "pants", "lmdb_store")
	writeFile(t, filepath.Join(hostSrc, "data.mdb"), 4096)
	o.HostCAS = func() []hostcas.Disposition {
		return []hostcas.Disposition{
			aliasDisposition(hostSrc, filepath.Join(state, "cache", "pants", "lmdb_store"), true),
		}
	}

	rep := Inventory(o)
	alias := storeByKey(t, rep, "alias.pants-lmdb-store")
	if alias.Verdict != VerdictNotOurs {
		t.Errorf("alias verdict = %q, want %q — anything else puts a host-owned store in the "+
			"reclaimable list", alias.Verdict, VerdictNotOurs)
	}
	if alias.Reclaimer.Func != "" {
		t.Errorf("alias row names reclaimer %q; yolo has none for the host user's own store",
			alias.Reclaimer.Func)
	}

	var buf strings.Builder
	o.Out = &buf
	renderText(rep, o)
	out := buf.String()
	nothing := out[strings.Index(out, "What nothing reclaims"):]
	if strings.Contains(nothing, "pants/lmdb_store") {
		t.Errorf("the host's store appears under \"what nothing reclaims\", which reads as an "+
			"offer to delete it:\n%s", nothing)
	}
	// It still has to be VISIBLE, in its own section, with the paths.
	if !strings.Contains(out, SectionAlias) || !strings.Contains(out, hostSrc) {
		t.Errorf("the alias is not legible in the report at all:\n%s", out)
	}
}

// The row must say WHAT HAPPENS TO THE STATE ALREADY ON DISK — the design's own
// completeness question. Migrated, ignored, or left to be reclaimed? Left to be
// reclaimed, by the age purge that already covers the subdir, and the row names
// the path so a user can act sooner.
func TestAliasedRowNamesTheStrandedCopyAndWhatReclaimsIt(t *testing.T) {
	o, state := testOptions(t)
	hostSrc := filepath.Join(t.TempDir(), "host-cache", "pants", "lmdb_store")
	writeFile(t, filepath.Join(hostSrc, "data.mdb"), 4096)
	stranded := filepath.Join(state, "cache", "pants", "lmdb_store")
	o.HostCAS = func() []hostcas.Disposition {
		return []hostcas.Disposition{aliasDisposition(hostSrc, stranded, true)}
	}

	alias := storeByKey(t, Inventory(o), "alias.pants-lmdb-store")
	for _, want := range []string{"ALIASED", "/home/agent/.cache/pants/lmdb_store", stranded,
		"stranded", "PurgeCacheByAge", "cache/pants"} {
		if !strings.Contains(alias.Note, want) {
			t.Errorf("the aliased row's note does not mention %q:\n%s", want, alias.Note)
		}
	}
}

// A DECLINE IS EXPLAINED, not omitted. §5.5's whole subject is the inventory
// "including what nothing reclaims", and a store yolo considered and refused is
// exactly the kind of fact that is otherwise invisible.
func TestDeclinedRowExplainsItselfAndReportsAbsent(t *testing.T) {
	o, state := testOptions(t)
	stranded := filepath.Join(state, "cache", "pants", "lmdb_store")
	missing := filepath.Join(t.TempDir(), "host-cache", "pants", "lmdb_store")
	o.HostCAS = func() []hostcas.Disposition {
		return []hostcas.Disposition{aliasDisposition(missing, stranded, false)}
	}

	alias := storeByKey(t, Inventory(o), "alias.pants-lmdb-store")
	if alias.Sizing != SizingAbsent {
		t.Errorf("a missing host store reported %q, want absent (§5.5: not an error)", alias.Sizing)
	}
	if !strings.Contains(alias.Note, "not aliased") || !strings.Contains(alias.Note, "no pants store") {
		t.Errorf("the declined row does not say why:\n%s", alias.Note)
	}
	if !strings.Contains(alias.Note, stranded) {
		t.Errorf("the declined row does not say where this jail keeps its own copy:\n%s", alias.Note)
	}
}

// A disposition with no resolvable host cache root still gets a row: naming the
// store and the reason is the useful answer, and a silently missing row would
// read as "yolo does not know about this store".
func TestRowSurvivesAnUnresolvableHostCacheRoot(t *testing.T) {
	o, _ := testOptions(t)
	d := aliasDisposition("", "", false)
	d.Code = hostcas.CodeNoCacheRoot
	d.Reason = "the launching user's cache directory could not be resolved"
	o.HostCAS = func() []hostcas.Disposition { return []hostcas.Disposition{d} }

	alias := storeByKey(t, Inventory(o), "alias.pants-lmdb-store")
	if alias.Sizing != SizingAbsent {
		t.Errorf("sizing = %q, want absent", alias.Sizing)
	}
	if !strings.Contains(alias.Note, "could not be resolved") {
		t.Errorf("note does not carry the reason:\n%s", alias.Note)
	}
}

// THE CALL-SITE PIN for the inventory: Inventory must ASK. Delete the
// aliasStores call and every assertion above still passes when run against
// aliasStores directly, which is the shape AGENTS.md names.
func TestInventoryAsksForTheAliasSection(t *testing.T) {
	o, state := testOptions(t)
	hostSrc := filepath.Join(t.TempDir(), "host-cache", "pants", "lmdb_store")
	writeFile(t, filepath.Join(hostSrc, "data.mdb"), 7)
	called := false
	o.HostCAS = func() []hostcas.Disposition {
		called = true
		return []hostcas.Disposition{
			aliasDisposition(hostSrc, filepath.Join(state, "cache", "pants", "lmdb_store"), true),
		}
	}

	rep := Inventory(o)
	if !called {
		t.Fatal("Inventory never consulted the host-CAS seam — the section is unreachable from " +
			"the command")
	}
	found := false
	for _, s := range rep.Stores {
		if s.Section == SectionAlias {
			found = true
		}
	}
	if !found {
		t.Error("no alias row in the report Inventory built")
	}
}

// The section has to reach --format json too: it is the form an agent parses,
// and a row that only exists in the text renderer is a row half the consumers
// cannot see.
func TestAliasRowIsInTheJSON(t *testing.T) {
	o, state := testOptions(t)
	hostSrc := filepath.Join(t.TempDir(), "host-cache", "pants", "lmdb_store")
	writeFile(t, filepath.Join(hostSrc, "data.mdb"), 11)
	o.HostCAS = func() []hostcas.Disposition {
		return []hostcas.Disposition{
			aliasDisposition(hostSrc, filepath.Join(state, "cache", "pants", "lmdb_store"), true),
		}
	}
	o.JSON = true
	var buf strings.Builder
	o.Out = &buf
	if rc := Run(o); rc != 0 {
		t.Fatalf("Run = %d, want 0", rc)
	}
	var rep Report
	if err := json.Unmarshal([]byte(buf.String()), &rep); err != nil {
		t.Fatalf("decode: %v\n%s", err, buf.String())
	}
	for _, s := range rep.Stores {
		if s.Key == "alias.pants-lmdb-store" {
			if s.Verdict != VerdictNotOurs || s.Section != SectionAlias {
				t.Errorf("json row = %+v, want section %q and verdict %q", s, SectionAlias, VerdictNotOurs)
			}
			return
		}
	}
	t.Errorf("no alias row in the JSON report: %s", buf.String())
}

// THE NOTE MAKES A CLAIM ABOUT internal/prune, so it is checked against prune's
// own list rather than trusted: an aliased row tells the reader that the stranded
// private copy is reclaimed by PurgeCacheByAge once its files age out. That is
// only true while the store's top-level cache segment is on the purge's subdir
// list, so the claim is made exactly for the stores whose segment is on it, and
// the content-addressed store (27 G of pants) must stay one of them.
//
// It is also the answer to "what happens to the state already on disk the day
// this ships": nothing is migrated and nothing is deleted by the alias — the
// bytes stop being read and are left to the reclaimer that covers them, or the
// note says none does.
func TestEveryRecognisedStoresStrandedCopyHasAReclaimer(t *testing.T) {
	covered := map[string]bool{}
	for _, sub := range prune.CachePurgeDefaultSubdirs {
		covered[sub] = true
	}
	for _, sub := range prune.CachePurgeHeavySubdirs {
		covered[sub] = true
	}
	for _, s := range hostcas.Stores {
		seg := s.CacheSegment()
		claims := strings.Contains(strandedReclaimer(s), "PurgeCacheByAge")
		if want := seg != "" && covered[seg]; claims != want {
			t.Errorf("hostcas.Stores[%q] (cache segment %q): the note claims PurgeCacheByAge=%v, "+
				"but prune's lists say %v", s.Name, seg, claims, want)
		}
	}
	if !covered[hostcas.Stores[0].CacheSegment()] {
		t.Errorf("the content-addressed store's segment %q left PurgeCacheByAge's list",
			hostcas.Stores[0].CacheSegment())
	}
}

// `yolo stores` has no readiness gate, so it asks `podman info` itself for
// OQ-BF11's ownership fact — through the Exec seam, only for podman, and an
// answer it cannot read is unknown (which declines every path-keyed store, as
// the launch does).
func TestPodmanOwnershipAsksPodmanInfo(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the ownership read is podman-on-Linux only; macOS answers unknown without asking")
	}
	for _, tc := range []struct {
		name string
		rt   string
		res  prune.ProbeResult
		want hostcas.Ownership
		asks bool
	}{
		{"rootless", "podman", prune.ProbeResult{Ran: true, Stdout: `{"host":{"security":{"rootless":true}}}`}, hostcas.OwnershipYours, true},
		{"rootful", "podman", prune.ProbeResult{Ran: true, Stdout: `{"host":{"security":{"rootless":false}}}`}, hostcas.OwnershipNotYours, true},
		{"podman failed", "podman", prune.ProbeResult{Ran: true, RC: 125}, hostcas.OwnershipUnknown, true},
		{"podman absent", "podman", prune.ProbeResult{}, hostcas.OwnershipUnknown, true},
		{"apple container", "container", prune.ProbeResult{Ran: true, Stdout: `{"host":{"security":{"rootless":true}}}`}, hostcas.OwnershipUnknown, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			asked := false
			o := &Options{Exec: func(argv []string, _ time.Duration) prune.ProbeResult {
				asked = true
				if strings.Join(argv, " ") != tc.rt+" info --format json" {
					t.Errorf("asked %v, want `%s info --format json`", argv, tc.rt)
				}
				return tc.res
			}}
			if got := podmanOwnership(o, tc.rt); got != tc.want {
				t.Errorf("ownership = %d, want %d", got, tc.want)
			}
			if asked != tc.asks {
				t.Errorf("asked podman = %v, want %v", asked, tc.asks)
			}
		})
	}
}

// The default HostCAS reader hands hostcas the ownership answer: on a rootless
// podman with a host npm cache, the inventory's npm row says ALIASED.
func TestDefaultHostCASReaderPassesTheOwnershipFact(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the alias is never-macOS")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	// Every variable the npm store's host path or the scope guard reads, cleared so
	// the developer's own environment cannot move the store.
	for _, k := range []string{"XDG_CACHE_HOME", "XDG_CONFIG_HOME", "npm_config_cache", "NPM_CONFIG_CACHE"} {
		t.Setenv(k, "")
	}
	writeFile(t, filepath.Join(home, ".npm", "_cacache", "index"), 10)
	o := &Options{
		SelectedRuntime: func() string { return "podman" },
		Exec: func([]string, time.Duration) prune.ProbeResult {
			return prune.ProbeResult{Ran: true, Stdout: `{"host":{"security":{"rootless":true}}}`}
		},
		GlobalCache: func() string { return filepath.Join(t.TempDir(), "cache") },
	}
	fillDefaults(o)
	for _, d := range o.HostCAS() {
		if d.Store.Name == "npm-cacache" {
			if !d.Aliased {
				t.Errorf("npm not aliased on a rootless podman: %q %s", d.Code, d.Reason)
			}
			return
		}
	}
	t.Fatal("no npm-cacache disposition")
}

// A per-workspace store's private copy is named through paths.HomeSurfaces: the
// jail's ~/.local is the overlay's `local`, not `.local`.
func TestPerWorkspaceStrandedUsesTheOverlayMapping(t *testing.T) {
	for dest, want := range map[string]string{
		"/home/agent/.local/share/pnpm/store": "each workspace's .yolo/setups/<setup>/home/local/share/pnpm/store",
		"/home/agent/go/pkg/mod":              "each workspace's .yolo/setups/<setup>/home/go/pkg/mod",
	} {
		if got := perWorkspaceStranded(dest); got != want {
			t.Errorf("perWorkspaceStranded(%q) = %q, want %q", dest, got, want)
		}
	}
}
