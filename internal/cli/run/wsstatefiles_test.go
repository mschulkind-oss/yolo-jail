package run

// wsstatefiles_test.go is wsstatelinks_test.go for the files the host writes DIRECTLY under
// `<workspace>/.yolo`, beside the overlay rather than in it: the launch log, the host perf log,
// the housekeeping log, the handoff rename and the pack `files` ownership manifest. `.yolo` is
// writable from inside the jail (the workspace bind hides nothing under it), so a link the last
// jail left at one of these names aimed the next launch's append, rewrite or rename at a host
// file of its choosing: the launch log's tee appended launch output, which quotes the
// jail-writable workspace config, to whatever the link named (~/.bashrc, say).
//
// Each case plants the link, runs the production call site, and checks the host file.

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/perf"
)

// preciousHostFile is a host file (a stand-in for ~/.bashrc) with known content, and the
// content, for a test to check the launch left alone.
func preciousHostFile(t *testing.T) (path, body string) {
	t.Helper()
	body = "# the host user's own file\n"
	path = filepath.Join(t.TempDir(), "bashrc")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path, body
}

// assertHostFileUnchanged fails when the host file at p no longer holds body.
func assertHostFileUnchanged(t *testing.T, p, body, what string) {
	t.Helper()
	if got, err := os.ReadFile(p); err != nil || string(got) != body {
		t.Errorf("%s: the host file behind the jail's link was changed: %q (err %v)", what, got, err)
	}
}

// assertRegularFile fails unless p is a regular file (the link was replaced, not followed).
func assertRegularFile(t *testing.T, p string) {
	t.Helper()
	if fi, err := os.Lstat(p); err != nil || !fi.Mode().IsRegular() {
		t.Errorf("%s is not a regular file after the write (%v, %v)", p, modeOf(fi), err)
	}
}

// linkTarget is one shape a planted link takes. plant puts the link at link and returns the
// check that the host side behind it was left alone.
type linkTarget struct {
	name  string
	plant func(t *testing.T, link string) (verify func(t *testing.T, what string))
}

// linkTargets is the two shapes: a link to an existing host file, which a follow writes into,
// and a dangling one, which an O_CREATE through the link creates.
var linkTargets = []linkTarget{
	{"a link to an existing host file", func(t *testing.T, link string) func(*testing.T, string) {
		p, body := preciousHostFile(t)
		symlinkAt(t, p, link)
		return func(t *testing.T, what string) { assertHostFileUnchanged(t, p, body, what) }
	}},
	{"a dangling link", func(t *testing.T, link string) func(*testing.T, string) {
		dir := t.TempDir()
		symlinkAt(t, filepath.Join(dir, "created-by-the-follow"), link)
		return func(t *testing.T, what string) { assertEmptyDir(t, dir, what) }
	}},
}

// THE LAUNCH LOG: attachLaunchLog trims then O_APPENDs .yolo/launch.log, and the tee writes
// every line of the launch into it.
func TestLaunchLogNeverAppendsThroughALink(t *testing.T) {
	for _, tc := range linkTargets {
		t.Run(tc.name, func(t *testing.T) {
			ws := t.TempDir()
			link := filepath.Join(ws, ".yolo", LaunchLogName)
			verify := tc.plant(t, link)
			o := &Options{Workspace: ws, Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{},
				Getenv: func(string) string { return "" }}

			l := attachLaunchLog(o)
			fmt.Fprintln(o.Stdout, "a line the workspace config chose")
			l.finish(0)

			verify(t, "launch.log")
			assertRegularFile(t, link)
			if got := readLaunchLog(t, ws); !bytes.Contains([]byte(got), []byte("a line the workspace config chose")) {
				t.Errorf("the launch log no longer records the launch: %q", got)
			}
		})
	}
}

// THE HOUSEKEEPING LOG: housekeepingNote O_APPENDs .yolo/housekeeping.log.
func TestHousekeepingNoteNeverAppendsThroughALink(t *testing.T) {
	for _, tc := range linkTargets {
		t.Run(tc.name, func(t *testing.T) {
			ws := t.TempDir()
			link := filepath.Join(ws, ".yolo", "housekeeping.log")
			verify := tc.plant(t, link)
			o := &Options{Workspace: ws}
			fillDefaults(o)

			o.housekeepingNote("reclaimed %d bytes", 42)

			verify(t, "housekeeping.log")
			assertRegularFile(t, link)
			if got := mustReadFile(t, link); !bytes.Contains([]byte(got), []byte("reclaimed 42 bytes")) {
				t.Errorf("the housekeeping note was not recorded: %q", got)
			}
		})
	}
}

// THE HOST PERF LOG: the file sink trims (a whole-file rewrite) then O_APPENDs
// .yolo/host-perf.log.
func TestHostPerfLogNeverAppendsThroughALink(t *testing.T) {
	for _, tc := range linkTargets {
		t.Run(tc.name, func(t *testing.T) {
			ws := t.TempDir()
			link := filepath.Join(ws, ".yolo", HostPerfLogName)
			verify := tc.plant(t, link)

			sink := hostPerfFileSink(ws, "jail-x", &bytes.Buffer{}, func(string) {})
			sink(perf.Event{Kind: perf.KindMark, Name: "launch.marked", At: time.Now()})

			verify(t, "host-perf.log")
			assertRegularFile(t, link)
			if got := mustReadFile(t, link); !bytes.Contains([]byte(got), []byte("launch.marked")) {
				t.Errorf("the perf log did not record the event: %q", got)
			}
		})
	}
}

// THE HANDOFF RENAME runs beneath `.yolo`: through a linked `.yolo` it renamed a host
// directory's handover.md.
func TestConsumeHandoffNeverRenamesThroughALinkedStateDir(t *testing.T) {
	ws := t.TempDir()
	hostDir := t.TempDir()
	writeFixture(t, filepath.Join(hostDir, handoffPointer), "the host's own file\n")
	symlinkAt(t, hostDir, filepath.Join(ws, ".yolo"))

	if consumeHandoff(ws) {
		t.Error("consumeHandoff reported a rename through the linked .yolo")
	}
	if _, err := os.Lstat(filepath.Join(hostDir, handoffPointer)); err != nil {
		t.Errorf("the host directory's %s was renamed through the link: %v", handoffPointer, err)
	}
}

// THE PACK files OWNERSHIP MANIFEST is saved through a temp file beside it, in `.yolo`; a
// link at the temp name had the host truncate the file it named and write the manifest there.
func TestPackFilesManifestNeverWritesThroughALink(t *testing.T) {
	for _, tc := range linkTargets {
		t.Run(tc.name, func(t *testing.T) {
			wsState := filepath.Join(t.TempDir(), ".yolo", "home")
			if err := os.MkdirAll(wsState, 0o755); err != nil {
				t.Fatal(err)
			}
			manifest := filepath.Join(filepath.Dir(wsState), packFilesMountpointManifestName)
			verify := tc.plant(t, manifest+".tmp")
			p := workspaceFilesPack(t, "pi-extension", "extension.ts",
				".pi/agent/extensions/thinking-preview.ts", ".pi")

			preparePackFiles([]*packload.Pack{p}, wsState, "podman")

			verify(t, "the manifest's temp file")
			assertRegularFile(t, manifest)
			if loaded := loadPackFilesMountpointManifest(manifest); len(loaded.Entries) != 1 {
				t.Errorf("the manifest was not saved: %+v", loaded)
			}
		})
	}
	t.Run("a link at the manifest itself is not read", func(t *testing.T) {
		wsState := filepath.Join(t.TempDir(), ".yolo", "home")
		if err := os.MkdirAll(wsState, 0o755); err != nil {
			t.Fatal(err)
		}
		// A host file shaped like a manifest, owning a path the launch would then remove. This
		// pins the READ half only: the jail can write a regular manifest in `.yolo` just as
		// well as a link to one, so what the refusal buys is that a host file is never parsed
		// as yolo's own state, not that the manifest's content is trustworthy. The removal
		// such a manifest can ask for stays beneath the overlay (preparePackFiles' pathParentWithin check).
		owned := filepath.Join(wsState, "pi", "agent", "stale")
		writeFixture(t, owned, "")
		hostManifest := filepath.Join(t.TempDir(), "m.json")
		writeFixture(t, hostManifest, `{"entries":{"pi/agent/stale":{"kind":"file"}}}`)
		symlinkAt(t, hostManifest, filepath.Join(filepath.Dir(wsState), packFilesMountpointManifestName))

		preparePackFiles(nil, wsState, "podman")

		if _, err := os.Lstat(owned); err != nil {
			t.Errorf("the launch acted on a manifest read through a link: %v", err)
		}
	})
}

// THE LEGACY ARCHIVE moves a stray scaffold into `.yolo/archive/...`; through a link at
// `.yolo/archive` it created directories, and moved the file, into a host directory.
func TestLegacyPackFileArchiveNeverMovesThroughALink(t *testing.T) {
	wsState := filepath.Join(t.TempDir(), ".yolo", "home")
	managed := filepath.Join(wsState, "pi", "agent", "extensions", "yolo-openai-auth.js")
	orphan := filepath.Join(wsState, "pi", "agent", "extensions", "thinking-preview.ts")
	writeFixture(t, managed, "")
	writeFixture(t, orphan, "")
	hostDir := t.TempDir()
	symlinkAt(t, hostDir, filepath.Join(filepath.Dir(wsState), "archive"))
	p := workspaceFilesPack(t, "pi", "extension.js",
		".pi/agent/extensions/yolo-openai-auth.js", ".pi")

	preparePackFiles([]*packload.Pack{p}, wsState, "podman")

	assertEmptyDir(t, hostDir, ".yolo/archive linked")
}
