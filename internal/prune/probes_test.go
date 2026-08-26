package prune

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
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
// recording every `rmi -f <id>`.
func imagesRunner(rows string, rmiCalls *[]string) RunFunc {
	return func(argv []string, _ time.Duration) ProbeResult {
		if len(argv) >= 2 && argv[1] == "images" {
			return ProbeResult{Stdout: rows, Ran: true}
		}
		if len(argv) >= 2 && argv[1] == "rmi" {
			*rmiCalls = append(*rmiCalls, argv[3]) // rmi -f <id>
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
		got := PruneOldImages("podman", 2, none, false, run)
		want := []string{"id1", "id4"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("dry-run = %v, want %v", got, want)
		}
		if len(rmiCalls) != 0 {
			t.Errorf("dry-run made rmi calls: %v", rmiCalls)
		}
		if got = PruneOldImages("podman", 2, none, true, run); !reflect.DeepEqual(got, want) {
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
		got := PruneOldImages("podman", 2,
			map[string]struct{}{"1111111111111111": {}}, true, run)
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
		got := PruneOldImages("podman", 2, map[string]struct{}{"latest": {}}, true, run)
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
	tags := ProtectedImageTags(buildDir)
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
