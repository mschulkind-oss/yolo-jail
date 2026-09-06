package run

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/image"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// TestAutoReapOldImagesWiring is the run-package half of the call-site
// coverage AGENTS.md asks for: it exercises the actual method runContainer
// calls (o.autoReapOldImages), through the REAL o.Exec-to-prune.RunFunc
// adapter, so a broken adapter (wrong argv indexing, a dropped Timeout
// check, …) fails here rather than only in the lower-level internal/prune
// unit tests. It follows the same direct-call pattern as
// TestReapOrphanedJailsPolarity above: runContainer itself has no unit test
// (it only runs against a real container backend), so — exactly like
// reapOrphanedJails already does — the callee is pinned directly and the
// wiring is additionally checked by nested-jail verification (see AGENTS.md).
func TestAutoReapOldImagesWiring(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	buildDir := paths.BuildDir()
	if err := os.MkdirAll(buildDir, 0o755); err != nil {
		t.Fatal(err)
	}
	const live = "/nix/store/aaaa-live-image"
	if err := os.WriteFile(filepath.Join(buildDir, "last-load-podman"),
		[]byte(live+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Production keep is prune.DefaultKeepImages (2), so this fixture needs FOUR
	// images for the veto to matter: with only two, both would already survive
	// on count alone and the test would prove nothing about the veto. Newest 2
	// (id-newest, id-mid) survive by count; of the remaining two, id-live is
	// the load sentinel's own (oldest — a week-old still-running jail, the
	// exact minimal-disk-footprint.md §3.3 scenario) and must survive only via
	// ProtectedImageTags, while id-old has no protection and must go.
	imgOut := "id-newest localhost/yolo-jail:4444444444444444 2026-09-01 09:00:00 +0000 UTC\n" +
		"id-mid localhost/yolo-jail:3333333333333333 2026-08-01 09:00:00 +0000 UTC\n" +
		"id-live localhost/yolo-jail:" + image.ImageStoreKey(live) + " 2026-07-01 09:00:00 +0000 UTC\n" +
		"id-old localhost/yolo-jail:1111111111111111 2026-06-01 09:00:00 +0000 UTC\n"

	var rmiCalls []string
	var buf bytes.Buffer
	o := &Options{
		Stdout: &buf,
		Now:    func() time.Time { return time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC) },
		Exec: func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
			if len(argv) >= 2 && argv[1] == "images" {
				return ExecResult{Ran: true, RC: 0, Stdout: imgOut}
			}
			if len(argv) >= 2 && argv[1] == "rmi" {
				rmiCalls = append(rmiCalls, argv[3])
				return ExecResult{Ran: true, RC: 0}
			}
			return ExecResult{Ran: true, RC: 0}
		},
	}
	// fillDefaults only fills NIL seams (see runcmd.go), so the three set above
	// survive it; this just fills whatever else autoReapOldImages's call graph
	// might touch (e.g. o.Color's zero value already skips IsTTYStdout via
	// short-circuit, but filling defaults is the pattern every other test in
	// this package uses rather than reasoning about which seams are load-bearing).
	fillDefaults(o)

	o.autoReapOldImages("podman")

	if len(rmiCalls) != 1 || rmiCalls[0] != "id-old" {
		t.Fatalf("rmi calls = %v, want [id-old] (id-live is protected by the load sentinel)", rmiCalls)
	}
	if !strings.Contains(buf.String(), "Reclaimed 1 stale yolo-jail image") {
		t.Errorf("expected a notice naming the reclaim, got: %q", buf.String())
	}

	// A second call on the same clock must be debounced: no further rmi and no
	// further notice. This is the assertion that fails if the wiring ever
	// drops the debounce (e.g. by calling prune.PruneOldImages directly
	// instead of prune.AutoReapOldImages).
	buf.Reset()
	o.autoReapOldImages("podman")
	if len(rmiCalls) != 1 {
		t.Errorf("a debounced second call issued more rmi calls: %v", rmiCalls)
	}
	if buf.Len() != 0 {
		t.Errorf("a debounced call must print nothing, got: %q", buf.String())
	}
}

// TestAutoReapOldImagesWiringDeclinesOnUnknownLiveness pins the fail-safe
// polarity through the SAME adapter this package's launch path uses: a
// timed-out `images` probe must degrade to "liveness unknown", never to an
// empty (therefore all-removable) result.
func TestAutoReapOldImagesWiringDeclinesOnUnknownLiveness(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// No last-load-<runtime> sentinel anywhere under buildDir: the ledger is
	// unreadable, so ProtectedImageTags reports known=false regardless of what
	// the images probe below would return.
	var rmiCalls []string
	var buf bytes.Buffer
	o := &Options{}
	fillDefaults(o)
	o.Stdout = &buf
	o.Now = time.Now
	o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
		if len(argv) >= 2 && argv[1] == "images" {
			t.Error("an unreadable liveness ledger must decline before ever probing images")
		}
		if len(argv) >= 2 && argv[1] == "rmi" {
			rmiCalls = append(rmiCalls, argv[3])
		}
		return ExecResult{Ran: true, RC: 0}
	}

	o.autoReapOldImages("podman")

	if len(rmiCalls) != 0 {
		t.Errorf("rmi called with no readable liveness ledger: %v", rmiCalls)
	}
	if buf.Len() != 0 {
		t.Errorf("a declined pass must print nothing, got: %q", buf.String())
	}
}

// TestAutoReapOldImagesOptOut pins the escape hatch: with
// YOLO_NO_AUTO_IMAGE_REAP set, autoReapOldImages must not probe podman at
// all — the same "opt out entirely, before any probe" polarity
// YOLO_ALLOW_STALE_IMAGE and YOLO_NO_HOST_LOOPBACK already use elsewhere in
// this package. This is also the mechanism nested-jail verification of this
// feature relies on to avoid touching a real, shared podman store.
func TestAutoReapOldImagesOptOut(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(autoReapOptOutEnv, "1")
	o := &Options{}
	fillDefaults(o)
	o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
		t.Errorf("opted out but still probed: %v", argv)
		return ExecResult{Ran: true, RC: 0}
	}

	o.autoReapOldImages("podman")
}
