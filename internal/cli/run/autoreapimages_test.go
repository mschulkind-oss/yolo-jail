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
	var buf, stdout bytes.Buffer
	o := &Options{
		// A REAL workspace, because the slot's only output surface is
		// <workspace>/.yolo/housekeeping.log — without it housekeepingNote has
		// nowhere to write and the assertion below passes vacuously. Caught by
		// mutating the log text and watching the test stay green.
		Workspace: t.TempDir(),
		Stdout:    &stdout,
		Stderr:    &buf,
		Now:       func() time.Time { return time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC) },
		Exec: func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
			if len(argv) >= 2 && argv[1] == "images" {
				return ExecResult{Ran: true, RC: 0, Stdout: imgOut}
			}
			if len(argv) >= 2 && argv[1] == "rmi" {
				// argv[2]: the removal is `rmi <id>`, never `rmi -f <id>` —
				// forcing takes the running containers with the image.
				rmiCalls = append(rmiCalls, argv[2])
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
	// TO THE LOG, NOT THE TERMINAL — rewritten, not repaired. This asserted
	// stderr, which was wrong for a reason a terminal shows and a test does not:
	// the slot runs after the container attaches, so BOTH streams belong to the
	// jailed command, and a dim line from the launcher's goroutine lands on top
	// of whatever the agent's TUI is drawing. Reported live by the maintainer.
	if !strings.Contains(readHousekeepingLog(t, o.Workspace), "reclaimed 1 stale yolo-jail image") {
		t.Errorf("expected the reclaim in <workspace>/.yolo/housekeeping.log, got: %q",
			readHousekeepingLog(t, o.Workspace))
	}
	if stdout.Len() != 0 || buf.Len() != 0 {
		t.Errorf("the slot wrote to the terminal (stdout=%q stderr=%q) — both streams are the "+
			"jailed command's once it has attached", stdout.String(), buf.String())
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
// polarity through the SAME adapter this package's launch path uses — and,
// since OQ-LS2, the two cases that used to be one.
//
// REWRITTEN, not repaired. The old version asserted "decline before ever
// probing images" and "a declined pass must print nothing", and LS2 reverses
// the second: a decline on a launch that is about to start a container should
// be impossible, so it warns loudly rather than passing in silence. The first
// was reordered deliberately — listing candidates first is what distinguishes
// a denied sweep from a machine where yolo has never loaded an image.
func TestAutoReapOldImagesWiringDeclinesOnUnknownLiveness(t *testing.T) {
	// The ledger is unreadable in BOTH cases (no last-load-<runtime> sentinel
	// under buildDir), so what separates them is only whether there was anything
	// to reap.
	newOpts := func(t *testing.T, imagesOut string, rmiCalls *[]string) (*Options, *bytes.Buffer, *bytes.Buffer) {
		t.Setenv("HOME", t.TempDir())
		var out, errBuf bytes.Buffer
		o := &Options{Workspace: t.TempDir()} // see the note above: the log needs a workspace
		fillDefaults(o)
		o.Stdout = &out
		o.Stderr = &errBuf
		o.Now = time.Now
		o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
			if len(argv) >= 2 && argv[1] == "rmi" {
				*rmiCalls = append(*rmiCalls, argv[2])
			}
			if len(argv) >= 2 && argv[1] == "images" {
				return ExecResult{Ran: true, RC: 0, Stdout: imagesOut}
			}
			return ExecResult{Ran: true, RC: 0}
		}
		return o, &out, &errBuf
	}

	t.Run("nothing of ours: silent, and not a decline", func(t *testing.T) {
		var rmiCalls []string
		o, out, errBuf := newOpts(t, "", &rmiCalls)
		o.autoReapOldImages("podman")
		if len(rmiCalls) != 0 {
			t.Errorf("rmi called with no readable liveness ledger: %v", rmiCalls)
		}
		if out.Len() != 0 || errBuf.Len() != 0 {
			t.Errorf("a machine with no yolo images must say nothing; stdout=%q stderr=%q",
				out.String(), errBuf.String())
		}
	})

	t.Run("candidates exist but the ledger does not: loud, on stderr, and still no rmi", func(t *testing.T) {
		var rmiCalls []string
		o, out, errBuf := newOpts(t,
			"id1 localhost/yolo-jail:1111111111111111 2026-07-01 09:00:00 +0000 UTC\n", &rmiCalls)
		o.autoReapOldImages("podman")
		if len(rmiCalls) != 0 {
			t.Errorf("rmi called with no readable liveness ledger: %v", rmiCalls)
		}
		// RECORDED IN FULL, and not on the terminal. OQ-LS2's "loud" is about not
		// being silent, and a log line the user can grep is not silence — where a
		// human actually asked, `yolo prune` still fails with a non-zero exit.
		// Overlaying a running agent's TUI was never what loud meant.
		if !strings.Contains(readHousekeepingLog(t, o.Workspace), "DECLINED") {
			t.Errorf("a decline must be recorded (OQ-LS2): log=%q",
				readHousekeepingLog(t, o.Workspace))
		}
		if out.Len() != 0 || errBuf.Len() != 0 {
			t.Errorf("the decline touched the terminal (stdout=%q stderr=%q) — the slot runs "+
				"after attach, so both streams are the container's", out.String(), errBuf.String())
		}
	})
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

// readHousekeepingLog reads the slot's log for a workspace, or "" when it does
// not exist. The slot's ONLY output surface, by design: see
// Options.housekeepingNote for why nothing there may reach the terminal.
func readHousekeepingLog(t *testing.T, ws string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(ws, ".yolo", "housekeeping.log"))
	if err != nil {
		return ""
	}
	return string(data)
}
