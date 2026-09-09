package prune

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	goruntime "runtime"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/image"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// stubRun builds a RunFunc keyed by the joined argv, returning canned stdout
// (RC=0, Ran=true). An argv with no mapping returns Ran=true, RC=0, "" — the
// benign "container exists but no such mount / empty listing" case. absent
// argvs (to model a missing runtime) are handled by stubRunAbsent.
func stubRun(mapping map[string]string) RunFunc {
	return func(argv []string, _ time.Duration) ProbeResult {
		return ProbeResult{Stdout: mapping[strings.Join(argv, "\x00")], RC: 0, Ran: true}
	}
}

func key(argv ...string) string { return strings.Join(argv, "\x00") }

func TestFindYoloWorkspaces(t *testing.T) {
	wsA := t.TempDir()
	wsB := t.TempDir()
	mountsA, _ := json.Marshal([]map[string]any{{"Destination": "/workspace", "Source": wsA, "Type": "bind"}})
	mountsB, _ := json.Marshal([]map[string]any{{"Destination": "/workspace", "Source": wsB, "Type": "bind"}})
	run := stubRun(map[string]string{
		key("podman", "ps", "-a", "--format", "{{.Names}}"):                         "yolo-a-12345678\nyolo-b-87654321\nnot-a-yolo\n",
		key("podman", "inspect", "--format", "{{json .Mounts}}", "yolo-a-12345678"): string(mountsA),
		key("podman", "inspect", "--format", "{{json .Mounts}}", "yolo-b-87654321"): string(mountsB),
	})
	got := FindYoloWorkspaces("podman", run)
	want := []string{resolvePath(wsA), resolvePath(wsB)}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("FindYoloWorkspaces = %v, want %v", got, want)
	}
}

func TestFindYoloWorkspacesEmpty(t *testing.T) {
	// No yolo-* containers.
	run := stubRun(map[string]string{
		key("podman", "ps", "-a", "--format", "{{.Names}}"): "unrelated-db\nsome-app\n",
	})
	if got := FindYoloWorkspaces("podman", run); len(got) != 0 {
		t.Errorf("expected empty, got %v", got)
	}
	// Missing runtime (Ran=false) → empty.
	absent := func([]string, time.Duration) ProbeResult { return ProbeResult{Ran: false} }
	if got := FindYoloWorkspaces("podman", absent); len(got) != 0 {
		t.Errorf("missing-runtime = %v, want empty", got)
	}
}

func TestFindYoloWorkspacesMalformedInspect(t *testing.T) {
	run := stubRun(map[string]string{
		key("podman", "ps", "-a", "--format", "{{.Names}}"):                         "yolo-broken-abc\n",
		key("podman", "inspect", "--format", "{{json .Mounts}}", "yolo-broken-abc"): "this is not json",
	})
	if got := FindYoloWorkspaces("podman", run); len(got) != 0 {
		t.Errorf("malformed inspect = %v, want empty", got)
	}
}

func TestFindYoloWorkspacesDedup(t *testing.T) {
	ws := t.TempDir()
	mounts, _ := json.Marshal([]map[string]any{{"Destination": "/workspace", "Source": ws}})
	run := stubRun(map[string]string{
		key("podman", "ps", "-a", "--format", "{{.Names}}"):                  "yolo-x-1\nyolo-x-2\n",
		key("podman", "inspect", "--format", "{{json .Mounts}}", "yolo-x-1"): string(mounts),
		key("podman", "inspect", "--format", "{{json .Mounts}}", "yolo-x-2"): string(mounts),
	})
	got := FindYoloWorkspaces("podman", run)
	if !reflect.DeepEqual(got, []string{resolvePath(ws)}) {
		t.Errorf("dedup = %v, want [%s]", got, resolvePath(ws))
	}
}

func TestPruneStoppedContainers(t *testing.T) {
	// Exited yolo-* removed; running yolo-* kept; non-yolo untouched.
	psOut := "yolo-dead-1 Exited\nyolo-live-2 Running\nyolo-paused-3 Paused\nother-app Exited\nyolo-created-4 Created\n"
	var rmCalls []string
	run := func(argv []string, _ time.Duration) ProbeResult {
		if len(argv) >= 2 && argv[1] == "ps" {
			return ProbeResult{Stdout: psOut, Ran: true}
		}
		if len(argv) >= 2 && argv[1] == "rm" {
			rmCalls = append(rmCalls, argv[2])
			return ProbeResult{Ran: true}
		}
		return ProbeResult{Ran: true}
	}
	// Dry-run: reports targets, no rm calls.
	got := PruneStoppedContainers("podman", false, run)
	want := []string{"yolo-dead-1", "yolo-created-4"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("dry-run targets = %v, want %v", got, want)
	}
	if len(rmCalls) != 0 {
		t.Errorf("dry-run made rm calls: %v", rmCalls)
	}
	// Apply: same targets, rm called for each.
	got = PruneStoppedContainers("podman", true, run)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("apply targets = %v, want %v", got, want)
	}
	if !reflect.DeepEqual(rmCalls, want) {
		t.Errorf("rm calls = %v, want %v", rmCalls, want)
	}
}

func TestPruneStoppedContainersDegrade(t *testing.T) {
	absent := func([]string, time.Duration) ProbeResult { return ProbeResult{Ran: false} }
	if got := PruneStoppedContainers("podman", true, absent); len(got) != 0 {
		t.Errorf("missing runtime = %v, want empty", got)
	}
	failed := func([]string, time.Duration) ProbeResult { return ProbeResult{Ran: true, RC: 1} }
	if got := PruneStoppedContainers("podman", true, failed); len(got) != 0 {
		t.Errorf("nonzero rc = %v, want empty", got)
	}
}

// imagesRunner returns a RunFunc answering the `images` probe with rows and
// recording every `rmi <id>`. `ps` answers empty — nothing running — so a test
// that wants the in-use veto exercised uses imagesRunnerWithRunning instead.
func imagesRunner(rows string, rmiCalls *[]string) RunFunc {
	return imagesRunnerWithRunning(rows, "", rmiCalls)
}

// imagesRunnerWithRunning is imagesRunner plus the `podman ps` answer: one
// image ID per line, the images a running container is currently using.
func imagesRunnerWithRunning(rows, psRows string, rmiCalls *[]string) RunFunc {
	return func(argv []string, _ time.Duration) ProbeResult {
		if len(argv) >= 2 && argv[1] == "images" {
			return ProbeResult{Stdout: rows, Ran: true}
		}
		if len(argv) >= 2 && argv[1] == "ps" {
			return ProbeResult{Stdout: psRows, Ran: true}
		}
		if len(argv) >= 2 && argv[1] == "rmi" {
			// argv[2], not argv[3]: the removal is `rmi <id>` now. `rmi -f`
			// removed the CONTAINERS using an image, which is what killed live
			// jails mid-session on 2026-09-08.
			if len(argv) > 2 && argv[2] == "-f" {
				panic("prune must never force-remove an image: it takes running containers with it")
			}
			*rmiCalls = append(*rmiCalls, argv[2])
			return ProbeResult{Ran: true}
		}
		return ProbeResult{Ran: true}
	}
}

// TestPruneOldImages pins the pass against the row shape C2 actually produces:
// one row PER NAME, so the newest image appears twice (content tag + :latest),
// and every config keeps a permanent tag of its own. The fixture is the exact
// output measured from `podman images --format … yolo-jail` on 2026-08-25.
func TestPruneOldImages(t *testing.T) {
	// CreatedAt sorts lexically; keep=2 removes all but the 2 newest IMAGES.
	// id2 is the newest and therefore wears BOTH names.
	imgOut := "id1 localhost/yolo-jail:1111111111111111 2026-07-01 09:00:00 +0000 UTC\n" +
		"id2 localhost/yolo-jail:2222222222222222 2026-07-18 09:00:00 +0000 UTC\n" +
		"id2 localhost/yolo-jail:latest 2026-07-18 09:00:00 +0000 UTC\n" +
		"id3 localhost/yolo-jail:3333333333333333 2026-07-10 09:00:00 +0000 UTC\n" +
		"id4 localhost/yolo-jail:4444444444444444 2026-06-15 09:00:00 +0000 UTC\n"
	none := map[string]struct{}{}

	t.Run("keep counts images, not tag rows", func(t *testing.T) {
		// Without the dedup id2's two rows spend two of the two keep slots, and
		// id3 — the second-newest IMAGE — is selected for removal.
		var rmiCalls []string
		run := imagesRunner(imgOut, &rmiCalls)
		got, _ := PruneOldImages("podman", 2, none, true, false, run)
		want := []string{"id1", "id4"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("dry-run = %v, want %v", got, want)
		}
		if len(rmiCalls) != 0 {
			t.Errorf("dry-run made rmi calls: %v", rmiCalls)
		}
		if got, _ = PruneOldImages("podman", 2, none, true, true, run); !reflect.DeepEqual(got, want) {
			t.Errorf("apply = %v, want %v", got, want)
		}
		if !reflect.DeepEqual(rmiCalls, want) {
			t.Errorf("rmi calls = %v, want %v", rmiCalls, want)
		}
	})

	t.Run("a protected content tag is never removed", func(t *testing.T) {
		// id1 is the image another workspace's live jail runs. `rmi -f` would take
		// its container with it, so the veto must fire even though the keep window
		// selected it.
		var rmiCalls []string
		run := imagesRunner(imgOut, &rmiCalls)
		got, _ := PruneOldImages("podman", 2,
			map[string]struct{}{"1111111111111111": {}}, true, true, run)
		want := []string{"id4"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("removed = %v, want %v (id1 is in use)", got, want)
		}
		if !reflect.DeepEqual(rmiCalls, want) {
			t.Errorf("rmi calls = %v, want %v", rmiCalls, want)
		}
	})

	t.Run("any protected name saves the whole image", func(t *testing.T) {
		// :latest on an OLD image — the degraded fallback's only handle. Removal is
		// by ID, so a per-ROW verdict would let id4's content-tag row delete the
		// image its :latest row protects.
		rows := imgOut + "id4 localhost/yolo-jail:latest 2026-06-15 09:00:00 +0000 UTC\n"
		var rmiCalls []string
		run := imagesRunner(rows, &rmiCalls)
		got, _ := PruneOldImages("podman", 2, map[string]struct{}{"latest": {}}, true, true, run)
		want := []string{"id1"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("removed = %v, want %v (id4 still answers to :latest)", got, want)
		}
	})
}

// TestProtectedImageTagsReadsTheLoadSentinel pins the SOURCE of the veto set:
// the same LRU ledger PruneOrphanImageRoots' guard #2 reads, keyed the same way
// image.JailImageRef keys a tag. A second hash here would let the two disagree
// about which images are live.
func TestProtectedImageTagsReadsTheLoadSentinel(t *testing.T) {
	buildDir := t.TempDir()
	const live = "/nix/store/aaaa-live-image"
	if err := os.WriteFile(filepath.Join(buildDir, "last-load-podman"),
		[]byte(live+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tags, known := ProtectedImageTags(buildDir)
	if !known {
		t.Fatal("a readable sentinel with one path must report known=true")
	}
	if _, ok := tags[image.ImageStoreKey(live)]; !ok {
		t.Errorf("the loaded path's content tag %q is not protected: %v",
			image.ImageStoreKey(live), tags)
	}
	// The degraded fallback's only handle.
	if _, ok := tags["latest"]; !ok {
		t.Errorf("the legacy tag is not protected: %v", tags)
	}
	// A path nobody loaded must NOT be protected, or the veto degrades into
	// "never remove anything".
	if _, ok := tags[image.ImageStoreKey("/nix/store/bbbb-cold-image")]; ok {
		t.Error("an unloaded store path's tag was protected")
	}
}

func TestReapRelayOrphans(t *testing.T) {
	base := t.TempDir()
	now := time.Now()
	old := now.Add(-2 * time.Hour)
	// Live jail's relay pid file (kept), a dead orphan (reaped), and a
	// too-recent orphan (kept by grace floor).
	liveName := "yolo-live-aaaa"
	deadName := "yolo-dead-bbbb"
	liveHash := relayShortHash(liveName)
	deadHash := relayShortHash(deadName)
	livePid := filepath.Join(base, "yolo-broker-relay-"+liveHash+".pid")
	deadPid := filepath.Join(base, "yolo-broker-relay-"+deadHash+".pid")
	recentPid := filepath.Join(base, "yolo-broker-relay-cccccccc.pid")
	for _, p := range []string{livePid, deadPid, recentPid} {
		if err := os.WriteFile(p, []byte("123\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chtimes(livePid, old, old); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(deadPid, old, old); err != nil {
		t.Fatal(err)
	}
	// recentPid keeps its fresh mtime.

	live := map[string]struct{}{liveName: {}}

	// Unknown liveness → reap nothing.
	if got := ReapRelayOrphans(base, false, live, 3600, true, now, nil); len(got) != 0 {
		t.Errorf("unknown liveness reaped %v, want none", got)
	}

	// Dry-run: reports the dead orphan only.
	var killed []string
	got := ReapRelayOrphans(base, true, live, 3600, false, now, func(p string) { killed = append(killed, p) })
	if !reflect.DeepEqual(got, []string{deadPid}) {
		t.Errorf("dry-run reaped %v, want [%s]", got, deadPid)
	}
	if len(killed) != 0 {
		t.Errorf("dry-run killed %v", killed)
	}
	if _, err := os.Stat(deadPid); err != nil {
		t.Error("dry-run must not remove pid file")
	}

	// Apply: kills + removes the dead orphan; live + recent survive.
	got = ReapRelayOrphans(base, true, live, 3600, true, now, func(p string) { killed = append(killed, p) })
	if !reflect.DeepEqual(got, []string{deadPid}) {
		t.Errorf("apply reaped %v, want [%s]", got, deadPid)
	}
	if !reflect.DeepEqual(killed, []string{deadPid}) {
		t.Errorf("killed %v, want [%s]", killed, deadPid)
	}
}

func TestPySplitMax(t *testing.T) {
	cases := []struct {
		in  string
		max int
		out []string
	}{
		{"id repo:tag 2026-07-18 09:00:00 +0000 UTC", 2, []string{"id", "repo:tag", "2026-07-18 09:00:00 +0000 UTC"}},
		{"  leading   spaces  ", 2, []string{"leading", "spaces"}},
		{"a b c d e", 2, []string{"a", "b", "c d e"}},
		{"single", 2, []string{"single"}},
		{"", 2, nil},
		{"a\tb\tc\td", 2, []string{"a", "b", "c\td"}},
	}
	for _, c := range cases {
		if got := pySplitMax(c.in, c.max); !reflect.DeepEqual(got, c.out) {
			t.Errorf("pySplitMax(%q, %d) = %v, want %v", c.in, c.max, got, c.out)
		}
	}
}

// TestReapRelayOrphansRemovesHostOnlySocket: the reap must clean the relay's own
// socket, which no longer sits inside the per-jail directory it rmtrees.
//
// The relay's socket moved out of /tmp/yolo-host-services-<hash>/ when the jail hop
// became loopback-TLS — leaving it there would have kept the retired transport
// reachable from inside the jail. It now lives beside the pid and lock files, so
// the rmtree stopped covering it and a SIGKILLed relay (which cannot unlink its own
// socket) would litter /tmp permanently.
func TestReapRelayOrphansRemovesHostOnlySocket(t *testing.T) {
	base := t.TempDir()
	now := time.Now()
	old := now.Add(-2 * time.Hour)
	deadHash := relayShortHash("yolo-dead-bbbb")
	pid := filepath.Join(base, "yolo-broker-relay-"+deadHash+".pid")
	lock := filepath.Join(base, "yolo-broker-relay-"+deadHash+".lock")
	sock := filepath.Join(base, "yolo-broker-relay-"+deadHash+".sock")
	dir := filepath.Join(base, paths.HostServicesDirName(deadHash))
	for _, p := range []string{pid, lock, sock} {
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(pid, old, old); err != nil {
		t.Fatal(err)
	}

	var killed []string
	ReapRelayOrphans(base, true, map[string]struct{}{}, 3600, true, now,
		func(p string) { killed = append(killed, p) })

	// The pid file goes via the injected kill seam (it owns the signalling), so
	// assert the seam saw it rather than that this function unlinked it.
	if !reflect.DeepEqual(killed, []string{pid}) {
		t.Errorf("killed %v, want [%s]", killed, pid)
	}
	for _, p := range []string{lock, sock, dir} {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("%s survived the reap", filepath.Base(p))
		}
	}
}

// TestAnUnreadableLedgerDeclinesToSweep pins the FAIL-SAFE, which is the half of
// the veto that a set-shaped return could not express. The veto's only evidence
// is the load sentinel, so "no store paths" has two causes with opposite correct
// responses: nothing is live (sweep freely) and the ledger is gone (sweep
// nothing). Before the tri-state, both produced an empty protected set and
// PruneOldImages went on to `rmi -f` — measured 2026-08-25 against a real podman
// with $HOME pointed at an empty dir, where it selected an image the real
// sentinel vouched for.
//
// Reachability is not hypothetical: AddLoadedPath's error is discarded and
// os.WriteFile truncates before writing, so an ENOSPC — the very condition that
// makes someone run `yolo prune` — can leave the ledger empty.
//
// DELETE THE GUARD IN PruneOldImages AND THIS FAILS: with liveKnown=false the
// pass must return nothing and must not shell out at all.
func TestAnUnreadableLedgerDeclinesToSweep(t *testing.T) {
	// An empty build dir IS the missing-sentinel case.
	tags, known := ProtectedImageTags(t.TempDir())
	if known {
		t.Fatal("an absent sentinel must report known=false, or the veto fails open")
	}
	// It still reports the legacy tag, so a caller that ignores `known` gets a
	// non-empty map — which is exactly why the boolean has to carry the signal.
	if len(tags) == 0 {
		t.Fatal("expected the legacy tag even when the ledger is unreadable")
	}

	imgOut := "id1 localhost/yolo-jail:1111111111111111 2026-07-01 09:00:00 +0000 UTC\n" +
		"id2 localhost/yolo-jail:2222222222222222 2026-07-18 09:00:00 +0000 UTC\n" +
		"id3 localhost/yolo-jail:3333333333333333 2026-07-10 09:00:00 +0000 UTC\n"
	var rmiCalls []string
	run := imagesRunner(imgOut, &rmiCalls)
	if got, _ := PruneOldImages("podman", 2, tags, known, true, run); len(got) != 0 {
		t.Errorf("swept %v with an unreadable ledger; must decline entirely", got)
	}
	if len(rmiCalls) != 0 {
		t.Errorf("made rmi calls with an unreadable ledger: %v", rmiCalls)
	}
}

// THE 2026-09-08 INCIDENT, as a test. Four jails that had been up 3-4 days were
// killed mid-session by an auto-reap. The veto they should have been saved by is
// a TEN-ENTRY LRU of recently-LOADED store paths, and only a LAUNCH appends to
// it — so a jail that is running but not relaunching ages out while other
// launches load other images (that day: a commit to internal/ per launch, each
// moving goSrc, each loading a new image). Once outside the window the image was
// unprotected, `rmi -f` removed it, and podman took the running container too.
//
// The fix is to ask the runtime what is RUNNING rather than to infer it from
// load recency. This test fails if that probe is removed: the image is
// deliberately absent from the sentinel, exactly as an aged-out one is.
func TestPruneOldImagesSpareRunningContainersImageEvenWhenAgedOutOfTheLRU(t *testing.T) {
	rows := "idRunning localhost/yolo-jail:aaaaaaaaaaaaaaaa 2026-07-01 09:00:00 +0000 UTC\n" +
		"idCold localhost/yolo-jail:bbbbbbbbbbbbbbbb 2026-07-02 09:00:00 +0000 UTC\n"

	var rmiCalls []string
	// protected is EMPTY: the running jail's image has aged out of the LRU, which
	// is the whole premise. liveKnown=true, so the fail-safe is not what saves it.
	run := imagesRunnerWithRunning(rows, "idRunning\n", &rmiCalls)
	removed, _ := PruneOldImages("podman", 0, map[string]struct{}{}, true, true, run)

	for _, id := range removed {
		if id == "idRunning" {
			t.Errorf("prune selected the image a running container uses: %v", removed)
		}
	}
	for _, call := range rmiCalls {
		if call == "idRunning" {
			t.Errorf("prune removed a running container's image — this is the bug that "+
				"killed four live jails: %v", rmiCalls)
		}
	}
	// The veto must not become "remove nothing": a genuinely unused image goes.
	if len(rmiCalls) != 1 || rmiCalls[0] != "idCold" {
		t.Errorf("rmi calls = %v, want exactly [idCold]", rmiCalls)
	}
}

// An unanswerable `podman ps` must DECLINE, not proceed: "nothing is running"
// and "I cannot tell" are the same observation, and the action is destructive.
// Same polarity as the liveKnown fail-safe.
func TestPruneOldImagesDeclinesWhenRunningSetIsUnknown(t *testing.T) {
	rows := "idCold localhost/yolo-jail:bbbbbbbbbbbbbbbb 2026-07-02 09:00:00 +0000 UTC\n"
	var rmiCalls []string
	run := func(argv []string, _ time.Duration) ProbeResult {
		if len(argv) >= 2 && argv[1] == "ps" {
			return ProbeResult{Ran: false} // the runtime did not answer
		}
		if len(argv) >= 2 && argv[1] == "images" {
			return ProbeResult{Stdout: rows, Ran: true}
		}
		if len(argv) >= 2 && argv[1] == "rmi" {
			rmiCalls = append(rmiCalls, argv[2])
		}
		return ProbeResult{Ran: true}
	}
	if removed, _ := PruneOldImages("podman", 0, map[string]struct{}{}, true, true, run); len(removed) != 0 {
		t.Errorf("removed %v with an unreadable running set; want nothing", removed)
	}
	if len(rmiCalls) != 0 {
		t.Errorf("removed images with an unreadable running set: %v", rmiCalls)
	}
}

// ─── OQ-DF3 REACH: the two-probe union ───────────────────────────────────────

// fakeImage is one row of a pretend runtime image store: what `podman images`
// would print for it, plus whether it carries the owner label flake.nix bakes.
// The label is a property of the IMAGE here, exactly as it is on a real machine
// — which is what lets machineImagesRunner answer a query rather than replay a
// fixture.
type fakeImage struct {
	ID      string
	Ref     string // "localhost/yolo-jail:<tag>", or "<none>:<none>" once untagged
	Created string
	Labeled bool // built after the owner label shipped
}

// machineImagesRunner is a MEASURED model of `podman images`, not a canned
// answer: it filters a pretend store by the argv it is handed. That distinction
// is the whole point of these tests — a stub that returns the same rows to every
// `images` argv (imagesRunner, above) cannot tell a real union from a doubled
// single probe, and cannot notice a query that reaches further than the ruling
// allows.
//
// The three behaviours it models were all measured in this jail on 2026-09-08,
// podman 5.8.4:
//
//   - a positional repository argument AND `--filter` together are a HARD ERROR
//     ("cannot specify an image and a filter(s)"), which is why REACH is two
//     probes and can never be folded into one;
//   - the positional repository matches the name component EXACTLY, so
//     `yolo-jail` does not match `yolo-jail-builder`;
//   - a label filter returns the untagged row too, printing `<none>:<none>`.
//
// A query carrying NEITHER a repo nor a label filter returns EVERYTHING — the
// unlabeled, pre-label rows included. That is deliberate: it is how a widened
// query (a dropped filter value, an added `-a`, a `dangling=true`) shows up as a
// test failure instead of as bytes nobody authorized removing.
func machineImagesRunner(t *testing.T, store []fakeImage, psRows string, rmiCalls *[]string) RunFunc {
	t.Helper()
	return func(argv []string, _ time.Duration) ProbeResult {
		switch {
		case len(argv) >= 2 && argv[1] == "images":
			var repo, filter string
			// From argv[2]: argv[0] is the runtime and argv[1] the subcommand.
			for i := 2; i < len(argv); i++ {
				switch {
				case argv[i] == "--filter" && i+1 < len(argv):
					filter = argv[i+1]
					i++
				case argv[i] == "--format" && i+1 < len(argv):
					i++
				case argv[i] == "-a" || argv[i] == "--all":
					t.Errorf("the label probe must not pass %q: a plain listing already "+
						"returns the untagged row, while -a additionally surfaces build "+
						"intermediates the ruling does not authorize removing", argv[i])
				case !strings.HasPrefix(argv[i], "-"):
					repo = argv[i]
				}
			}
			if repo != "" && filter != "" {
				// podman's own refusal, reproduced.
				return ProbeResult{Ran: true, RC: 125,
					Stdout: "Error: cannot specify an image and a filter(s)"}
			}
			var b strings.Builder
			for _, img := range store {
				switch {
				case repo != "":
					if img.Ref != repo+":"+tagOf(img.Ref) && img.Ref != "localhost/"+repo+":"+tagOf(img.Ref) {
						continue
					}
				case filter != "":
					if filter != "label="+JailImageOwnerLabel &&
						filter != "label="+JailImageOwnerLabel+"="+JailImageOwnerValue {
						t.Errorf("unexpected image filter %q — REACH is NARROW: only the owner "+
							"label proves an image is ours, and dangling=true would reach the "+
							"user's own images on a shared podman", filter)
						continue
					}
					if !img.Labeled {
						continue
					}
				}
				b.WriteString(img.ID + " " + img.Ref + " " + img.Created + "\n")
			}
			return ProbeResult{Ran: true, Stdout: b.String()}
		case len(argv) >= 2 && argv[1] == "ps":
			return ProbeResult{Ran: true, Stdout: psRows}
		case len(argv) >= 2 && argv[1] == "rmi":
			if len(argv) > 2 && argv[2] == "-f" {
				panic("prune must never force-remove an image: it takes running containers with it")
			}
			*rmiCalls = append(*rmiCalls, argv[2])
			return ProbeResult{Ran: true}
		}
		return ProbeResult{Ran: true}
	}
}

// TestPruneOldImagesUnion is OQ-DF3's REACH ruling: an image yolo BUILT is
// reapable even after it loses its repository name, because the owner label
// flake.nix bakes survives untagging — and nothing else is.
func TestPruneOldImagesUnion(t *testing.T) {
	const (
		newest = "2026-08-01 09:00:00 +0000 UTC"
		middle = "2026-07-01 09:00:00 +0000 UTC"
		oldest = "2026-06-01 09:00:00 +0000 UTC"
	)
	none := map[string]struct{}{}

	t.Run("a labeled nameless row is selected", func(t *testing.T) {
		// The measured leak: a re-stream took the content tag and left the
		// previous image `<none>:<none>`. The repo-name probe cannot see it; the
		// label probe can, and `keep` counts it like any other image.
		store := []fakeImage{
			{ID: "id-tagged", Ref: "localhost/yolo-jail:aaaaaaaaaaaaaaaa", Created: newest, Labeled: true},
			{ID: "id-nameless", Ref: "<none>:<none>", Created: middle, Labeled: true},
		}
		var rmiCalls []string
		run := machineImagesRunner(t, store, "", &rmiCalls)
		got, declined := PruneOldImages("podman", 1, none, true, true, run)
		if declined != "" {
			t.Fatalf("declined %q on a healthy machine", declined)
		}
		if !reflect.DeepEqual(got, []string{"id-nameless"}) {
			t.Errorf("removed = %v, want [id-nameless] — the label is what makes a row "+
				"without a repository name provably ours", got)
		}
		if !reflect.DeepEqual(rmiCalls, []string{"id-nameless"}) {
			t.Errorf("rmi calls = %v, want [id-nameless]", rmiCalls)
		}
	})

	t.Run("a labeled nameless row a container is using is never removed", func(t *testing.T) {
		// Guard #0 is this row's ONLY veto: ProtectedImageTags matches TAGS and
		// `<none>` has none, so the load sentinel is structurally silent here.
		// `ps --format {{.ImageID}}` prints the same 12-hex short ID as
		// `images --format {{.ID}}` (MEASURED 2026-09-08), which is what makes
		// the match possible at all.
		store := []fakeImage{
			{ID: "id-tagged", Ref: "localhost/yolo-jail:aaaaaaaaaaaaaaaa", Created: newest, Labeled: true},
			{ID: "id-nameless", Ref: "<none>:<none>", Created: middle, Labeled: true},
		}
		var rmiCalls []string
		run := machineImagesRunner(t, store, "id-nameless\n", &rmiCalls)
		got, _ := PruneOldImages("podman", 1, none, true, true, run)
		if len(got) != 0 || len(rmiCalls) != 0 {
			t.Errorf("removed = %v (rmi %v), want none: a nameless row can be LIVE — the "+
				"re-stream took the tag off the image a running jail is on", got, rmiCalls)
		}
	})

	t.Run("a row both probes return is counted once", func(t *testing.T) {
		// Every image built after the label ships answers to BOTH probes. Without
		// one merged dedup, keep=2 spends its slots twice over and selects an
		// image it was meant to keep.
		store := []fakeImage{
			{ID: "id-new", Ref: "localhost/yolo-jail:3333333333333333", Created: newest, Labeled: true},
			{ID: "id-mid", Ref: "localhost/yolo-jail:2222222222222222", Created: middle, Labeled: true},
			{ID: "id-old", Ref: "localhost/yolo-jail:1111111111111111", Created: oldest, Labeled: true},
		}
		var rmiCalls []string
		run := machineImagesRunner(t, store, "", &rmiCalls)
		got, _ := PruneOldImages("podman", 2, none, true, false, run)
		if !reflect.DeepEqual(got, []string{"id-old"}) {
			t.Errorf("removed = %v, want [id-old] — `keep` counts IMAGES, and the union "+
				"returns each labeled image twice", got)
		}
	})

	t.Run("a protected tag saves an image its other probe's row would delete", func(t *testing.T) {
		// The `:latest` defect, one entrance over: the protected-tag scan has to
		// run over BOTH probes' rows before the keep decision, or the label
		// probe's duplicate row deletes the image the repo probe's row protects.
		store := []fakeImage{
			{ID: "id-new", Ref: "localhost/yolo-jail:3333333333333333", Created: newest, Labeled: true},
			{ID: "id-old", Ref: "localhost/yolo-jail:1111111111111111", Created: oldest, Labeled: true},
		}
		var rmiCalls []string
		run := machineImagesRunner(t, store, "", &rmiCalls)
		got, _ := PruneOldImages("podman", 1,
			map[string]struct{}{"1111111111111111": {}}, true, true, run)
		if len(got) != 0 || len(rmiCalls) != 0 {
			t.Errorf("removed = %v (rmi %v), want none — id-old's tag is protected", got, rmiCalls)
		}
	})

	t.Run("an unlabeled nameless row is never selected", func(t *testing.T) {
		// THE RULING'S PERMANENCE, and nothing else asserts it. A `<none>` row
		// that predates the label has no ownership evidence and never will:
		// yolo leaves it alone forever, and `yolo stores` is where the user
		// hears about it (disk-levers-and-backfill.md §5.5). This fails if the
		// query is ever widened — a dropped filter value, `-a`, `dangling=true`
		// — because machineImagesRunner answers an unfiltered query with the
		// whole store.
		store := []fakeImage{
			{ID: "id-tagged", Ref: "localhost/yolo-jail:aaaaaaaaaaaaaaaa", Created: newest, Labeled: true},
			{ID: "id-prelabel", Ref: "<none>:<none>", Created: oldest, Labeled: false},
		}
		var rmiCalls []string
		run := machineImagesRunner(t, store, "", &rmiCalls)
		got, _ := PruneOldImages("podman", 0, none, true, true, run)
		for _, id := range append(append([]string{}, got...), rmiCalls...) {
			if id == "id-prelabel" {
				t.Fatalf("a pre-label nameless row was selected (removed=%v rmi=%v): yolo "+
					"never removes an image it cannot prove is its own", got, rmiCalls)
			}
		}
		if !reflect.DeepEqual(got, []string{"id-tagged"}) {
			t.Errorf("removed = %v, want [id-tagged] only", got)
		}
	})

	t.Run("a failed label probe widens nothing and declines nothing", func(t *testing.T) {
		// Apple Container is NOT MEASURED for `--filter label=`. A runtime that
		// cannot answer the second probe must still get the tagged pass that has
		// shipped for a year — the set shrinks, which is the safe direction.
		imgOut := "id1 localhost/yolo-jail:1111111111111111 " + oldest + "\n" +
			"id2 localhost/yolo-jail:2222222222222222 " + newest + "\n"
		var rmiCalls []string
		run := func(argv []string, _ time.Duration) ProbeResult {
			if len(argv) >= 2 && argv[1] == "images" {
				for _, a := range argv {
					if strings.HasPrefix(a, "label=") {
						return ProbeResult{Ran: false} // the runtime refused it
					}
				}
				return ProbeResult{Ran: true, Stdout: imgOut}
			}
			if len(argv) >= 2 && argv[1] == "rmi" {
				rmiCalls = append(rmiCalls, argv[2])
			}
			return ProbeResult{Ran: true}
		}
		got, declined := PruneOldImages("podman", 1, none, true, true, run)
		if declined != "" {
			t.Errorf("declined %q — a label probe that cannot run is not a reason to "+
				"refuse the tagged pass", declined)
		}
		if !reflect.DeepEqual(got, []string{"id1"}) {
			t.Errorf("removed = %v, want [id1] — exactly today's tagged behaviour", got)
		}
	})

	t.Run("an unreadable repo probe still declines the whole pass", func(t *testing.T) {
		// The polarity that must NOT have moved: the repo-name probe is the one
		// whose failure means "there is no candidate set at all".
		run := func(argv []string, _ time.Duration) ProbeResult {
			if len(argv) >= 2 && argv[1] == "images" {
				for _, a := range argv {
					if strings.HasPrefix(a, "label=") {
						return ProbeResult{Ran: true, Stdout: "id-x <none>:<none> " + newest + "\n"}
					}
				}
				return ProbeResult{Ran: true, RC: 1}
			}
			return ProbeResult{Ran: true}
		}
		got, declined := PruneOldImages("podman", 1, none, true, true, run)
		if declined != DeclineImagesUnreadable || len(got) != 0 {
			t.Errorf("removed=%v declined=%q, want empty + %q", got, declined, DeclineImagesUnreadable)
		}
	})

	t.Run("candidates are listed before the guards", func(t *testing.T) {
		// OQ-LS2's ordering property, re-pinned across the union: BOTH probes run
		// before the liveness guard, so an unreadable ledger on a machine that
		// has candidates reports "prevented work" rather than "fresh machine".
		var order []string
		run := func(argv []string, _ time.Duration) ProbeResult {
			if len(argv) >= 2 {
				order = append(order, argv[1])
			}
			if len(argv) >= 2 && argv[1] == "images" {
				return ProbeResult{Ran: true, Stdout: "id-x <none>:<none> " + newest + "\n"}
			}
			return ProbeResult{Ran: true}
		}
		_, declined := PruneOldImages("podman", 1, none, false /*liveKnown*/, true, run)
		if declined != DeclineLedgerUnreadable {
			t.Errorf("declined %q, want %q", declined, DeclineLedgerUnreadable)
		}
		if !reflect.DeepEqual(order, []string{"images", "images"}) {
			t.Errorf("probe order = %v, want both image listings before any guard ran", order)
		}
	})
}

// TestOwnerLabelSpellingMatchesTheFlake is the two-language pin: the label is
// baked by Nix and filtered for by Go, and the two spellings are only equal
// because someone typed them that way. It is the same class as
// internal/entrypoint/shippedclients_test.go (Go ↔ flake.nix ↔ shell), and the
// consequence of drift is worse than a missing feature: every image already
// built keeps the old key, nothing filters for it, and they become precisely the
// unattributable rows OQ-DF3 created the label to stop making.
func TestOwnerLabelSpellingMatchesTheFlake(t *testing.T) {
	body, err := os.ReadFile(filepath.Join(pruneRepoRoot(t), "flake.nix"))
	if err != nil {
		t.Fatalf("read flake.nix: %v", err)
	}
	flake := string(body)

	labelsBlock := regexp.MustCompile(`(?s)Labels\s*=\s*\{(.*?)\}`).FindStringSubmatch(flake)
	if labelsBlock == nil {
		t.Fatalf("flake.nix has no `Labels = { … }` in the image config — the owner label "+
			"is how PruneOldImages proves an untagged image is ours (%s=%s)",
			JailImageOwnerLabel, JailImageOwnerValue)
	}
	pair := regexp.MustCompile(`"` + regexp.QuoteMeta(JailImageOwnerLabel) + `"\s*=\s*"` +
		regexp.QuoteMeta(JailImageOwnerValue) + `"`)
	if !pair.MatchString(labelsBlock[1]) {
		t.Errorf("flake.nix's Labels block does not bake %s=%q; it has:%s",
			JailImageOwnerLabel, JailImageOwnerValue, labelsBlock[1])
	}

	// The builder image must stay OUT of the reap's reach. podman's positional
	// repo filter matches the name component exactly, so `yolo-jail-builder` has
	// never been visible to listImagesByRepo — but the owner label is a second
	// entrance, and labelling it would enrol a whole class the ruling never
	// authorized. Nothing else would go red.
	if i := strings.Index(flake, `name = "yolo-jail-builder"`); i >= 0 {
		if strings.Contains(flake[i:], `"`+JailImageOwnerLabel+`"`) {
			t.Errorf("the yolo-jail-builder image carries %s — that puts it in "+
				"PruneOldImages' reach, which it has never been in", JailImageOwnerLabel)
		}
	}
}

// pruneRepoRoot locates the checkout from this test file's compile-time path.
// It FAILS rather than skips when the tree is unreadable: a skip would turn the
// cross-language drift this pins into silent non-coverage, which is the same
// shape of bug one level up. (The fifth copy of this eight-line helper in the
// repo — house style, not duplication: a shared one would be a package whose
// only purpose is to be imported by tests in five unrelated packages.)
func pruneRepoRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := goruntime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller gave no source path — cannot locate the repo root")
	}
	root := filepath.Join(filepath.Dir(thisFile), "..", "..")
	if _, err := os.Stat(filepath.Join(root, "flake.nix")); err != nil {
		t.Fatalf("no flake.nix at the resolved repo root %s: %v", root, err)
	}
	return root
}
