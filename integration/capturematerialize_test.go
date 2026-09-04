package integration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// capturematerialize_test.go is THE TEST THAT PROVES THE SLICE PAYS: capture once, launch
// two workspaces, and count the downloads. One.
//
// It is the acceptance cell for install-capture.md slice 4 and for
// docs/design/program-delivery.md §6.3's *materialize*, and nothing below a container can
// stand in for it. Three separate facts have to line up and each of them is a property of a
// real jail:
//
//   - the run pipeline binds the machine store at /ctx/captures and says so
//     (entrypoint.CapturesDirEnv);
//   - the boot bakes that path into the generated native launcher;
//   - the launcher, on a cold home, resolves an entry for this bin+platform and puts it in
//     place instead of fetching the vendor's installer.
//
// A unit test can pin each link. Only this one can fail when a link is missing, and the
// missing link this exists to catch is the one no unit test in the repo can reach:
// run.go's `capturesDir: o.CapturesDir()` sits on the CONTAINER arm of Run, and every unit
// test that drives Run goes down the macos-user arm, which builds no podman argv at all.
//
// IT ALSO RE-MEASURES THE PREMISE. `link(2)` compares the MOUNT, so a hardlink from the
// store's bind into a home surface's bind returns EXDEV even on one filesystem — which is
// why §6.3's original "unpack/hardlink" could not work from inside a jail and why reflink
// (FICLONE, whose predicate is the FILESYSTEM) is the primary mechanism. The launcher reports
// which arm it took, and this test reads that report: on a reflink-capable host it is the
// standing measurement that the mechanism survives the two mounts.
//
// HERMETIC, like capture_test.go: the fixture pack carries its own installer and the URL is
// `file:///ctx/packs/<name>/install.sh`. Nothing here touches a vendor or the network — and
// "the installer ran" is therefore an observation of a marker this repo wrote, not of a
// download nobody can see.

// materializeReport matches the line the in-jail materializer prints, whose fourth capture
// group is the fallback arm that actually ran.
var materializeReport = regexp.MustCompile(
	`Materialized (\S+) from capture ([0-9a-f]{16}) by (reflink|hardlink|copy) \((\d+) files`)

// TestCaptureMaterializesIntoTwoWorkspacesWithOneDownload is slice 4's acceptance case.
func TestCaptureMaterializesIntoTwoWorkspacesWithOneDownload(t *testing.T) {
	requireJail(t)

	pack := t.TempDir()
	if err := os.WriteFile(filepath.Join(pack, "install.sh"),
		[]byte(captureFixtureInstallerScript), 0o644); err != nil {
		t.Fatal(err)
	}
	manifest := `{
  "name": "` + captureFixturePack + `",
  "description": "install-capture materialize fixture",
  "contributes": [
    {"kind": "program", "bin": "` + captureFixtureBin + `", "via": "installer",
     "url": "file:///ctx/packs/` + captureFixturePack + `/install.sh"}
  ]
}`
	if err := os.WriteFile(filepath.Join(pack, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	packHome(t, `{"packs": [{"source": "file://`+pack+`", "name": "`+captureFixturePack+`"}]}`)

	store := filepath.Join(os.Getenv("HOME"), ".local", "share", "yolo-jail", "captures")
	before := captureEntryNames(t, store)
	t.Cleanup(func() { removeNewCaptureEntries(t, store, before) })
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Join(store, "staging", captureFixtureBin)) })

	// --- THE ONE DOWNLOAD -------------------------------------------------------------
	recorded := runYoloCLI(t, t.TempDir(), "capture", captureFixtureBin)
	if recorded.rc != 0 {
		t.Fatalf("yolo capture failed: rc %d\nstdout: %s\nstderr: %s",
			recorded.rc, recorded.stdout, recorded.stderr)
	}
	if !strings.Contains(recorded.combined(), captureFixtureRan+"-INSTALL") {
		t.Fatalf("the capture did not run the installer, so there is nothing to "+
			"materialize:\n%s", recorded.combined())
	}
	added := newCaptureEntries(t, store, before)
	if len(added) != 1 {
		t.Fatalf("got %d new capture entries, want 1: %v", len(added), added)
	}
	key := added[0]
	entryTool := filepath.Join(store, "entries", key, "tree",
		".local", "share", "fixturetool", "1.0.0", "tool")

	// --- TWO WORKSPACES, ZERO DOWNLOADS -----------------------------------------------
	var mechanisms []string
	inodes := map[string]uint64{}
	for _, name := range []string{"first", "second"} {
		ws := writeProject(t, `{"network": {"mode": "bridge"}}`)
		r := runYoloDirect(t, ws, captureFixtureBin)
		out := r.combined()
		if r.rc != 0 {
			t.Fatalf("%s workspace: launch failed rc %d:\n%s", name, r.rc, out)
		}

		// THE ASSERTION THE WHOLE SUBSYSTEM IS FOR. The installer's marker appears exactly
		// once in this test's life — during the capture above — and never in a launch.
		if strings.Contains(out, captureFixtureRan+"-INSTALL") {
			t.Errorf("%s workspace REFETCHED AND RAN the vendor installer instead of "+
				"materializing capture %s — this is the per-workspace download slice 4 "+
				"deletes:\n%s", name, key, out)
		}
		// And the program is really there and really runs, so the absence above is not
		// just an install that failed.
		if !strings.Contains(out, captureFixtureRan+"\n") {
			t.Errorf("%s workspace: the materialized tool did not run:\n%s", name, out)
		}

		m := materializeReport.FindStringSubmatch(out)
		if m == nil {
			t.Fatalf("%s workspace: no materialize report in the output — the launcher "+
				"never called `yolo internal capture-materialize`:\n%s", name, out)
		}
		if m[2] != key {
			t.Errorf("%s workspace materialized capture %s, want the one just recorded (%s)",
				name, m[2], key)
		}
		mechanisms = append(mechanisms, m[3])
		t.Logf("%s workspace materialized %s by %s", name, key, m[3])

		// A COPY MUST BE LOUD, and the report is the only place a machine learns that its
		// filesystems cannot share the bytes. This is the pairing, not the copy itself:
		// ext4 has no reflink and the store's bind is never the home's bind, so a copy is a
		// correct outcome on plenty of hosts — a SILENT one is not.
		if saidCopy := strings.Contains(out, "was COPIED into"); saidCopy != (m[3] == "copy") {
			t.Errorf("%s workspace: mechanism %q but the loud copy report %s:\n%s",
				name, m[3], map[bool]string{true: "was printed", false: "was not"}[saidCopy], out)
		}

		tool := filepath.Join(ws, ".yolo", "home", "local",
			"share", "fixturetool", "1.0.0", "tool")
		body, err := os.ReadFile(tool)
		if err != nil {
			t.Fatalf("%s workspace: the materialized binary is not in the home overlay: %v",
				name, err)
		}
		if want, _ := os.ReadFile(entryTool); string(body) != string(want) {
			t.Errorf("%s workspace: the materialized binary differs from the store entry",
				name)
		}
		// The absolute symlink the fixture installs comes across verbatim: on the container
		// backends the capture home and the materialize home are both /home/agent, which is
		// the whole reason relocation is macos-user's problem and not this path's.
		link, err := os.Readlink(filepath.Join(ws, ".yolo", "home", "local", "bin", captureFixtureBin))
		if err != nil {
			t.Errorf("%s workspace: the launcher symlink was not materialized: %v", name, err)
		} else if link != "/home/agent/.local/share/fixturetool/1.0.0/tool" {
			t.Errorf("%s workspace: symlink target = %q, want the capture's verbatim", name, link)
		}
		// The second surface, so this is not accidentally ".local only".
		if _, err := os.Stat(filepath.Join(ws, ".yolo", "home", "npm-global", "lib",
			"fixturetool-marker")); err != nil {
			t.Errorf("%s workspace: the .npm-global side of the entry is missing: %v", name, err)
		}

		// THE RECEIPT, in the WORKSPACE log — the line that would have been a
		// kind:"installer" install had there been no capture.
		rec := readLastReceipt(t, filepath.Join(ws, ".yolo", "receipts.jsonl"), "capture")
		for _, c := range []struct{ field, want string }{
			{"kind", "capture"},
			{"act", "materialize"},
			{"bin", captureFixtureBin},
			{"resolved", key},
			// THE ENTRY IN THE JAIL'S COORDINATES, not the host's, and that is right rather
			// than tolerated: every path in <ws>/.yolo/receipts.jsonl is written by an
			// in-jail process and is a jail path (the launcher funnels' `path` is
			// $HOME/.local/bin/<bin>), so a host path here would be the one string in the
			// file a reader could not resolve. The record receipt BESIDE THE ENTRY carries
			// the host path, because the host act writes it. `resolved` is the identity that
			// crosses both coordinate systems, which is why it is asserted above.
			{"path", "/ctx/captures/entries/" + key},
		} {
			if got := str(rec[c.field]); got != c.want {
				t.Errorf("%s workspace: materialize receipt %s = %q, want %q",
					name, c.field, got, c.want)
			}
		}
		if got := str(rec["platform"]); !strings.HasPrefix(got, "linux/") {
			t.Errorf("%s workspace: receipt platform = %q, want the JAIL's linux/<arch>",
				name, got)
		}

		if runtime.GOOS == "linux" {
			inodes[name] = inodeOfPath(t, tool)
		}
	}

	// --- WHAT THE MECHANISM IMPLIES ---------------------------------------------------
	// A REFLINKED file is its own inode sharing extents, which is what downgrades
	// install-capture.md's sharpest trap: "a hardlinked CAS file is the running program's
	// bytes", so an installer opening one for write corrupts every workspace at once. Under
	// reflink a write copies-on-write and reaches nobody. Asserted rather than assumed,
	// because it is the difference between the two arms and the reason reflink is first.
	if runtime.GOOS == "linux" && len(inodes) == 2 {
		entryIno := inodeOfPath(t, entryTool)
		switch mechanisms[0] {
		case "reflink", "copy":
			if inodes["first"] == entryIno || inodes["second"] == entryIno {
				t.Errorf("a %s'd file must be its OWN inode, but a workspace shares the "+
					"store entry's (%d)", mechanisms[0], entryIno)
			}
			if inodes["first"] == inodes["second"] {
				t.Errorf("the two workspaces share one inode (%d) after a %s",
					inodes["first"], mechanisms[0])
			}
		case "hardlink":
			if inodes["first"] != entryIno {
				t.Errorf("a hardlinked file must BE the store entry's inode (%d vs %d)",
					inodes["first"], entryIno)
			}
		}
	}

	// THE STORE IS UNTOUCHED by two materializes: entry files stay read-only, which is what
	// turns a write through a hardlinked one into an error instead of a silent
	// cross-workspace corruption.
	if fi, err := os.Lstat(entryTool); err != nil {
		t.Errorf("the store entry did not survive being materialized twice: %v", err)
	} else if fi.Mode().Perm()&0o222 != 0 {
		t.Errorf("the store entry is writable after materialize (%v)", fi.Mode())
	}
}

// readLastReceipt returns the last receipt line of the given kind, failing when there is none.
func readLastReceipt(t *testing.T, path, kind string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("no receipt log at %s: %v", path, err)
	}
	var found map[string]any
	for _, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			continue
		}
		if str(m["kind"]) == kind {
			found = m
		}
	}
	if found == nil {
		t.Fatalf("no %q receipt in %s:\n%s", kind, path, data)
	}
	return found
}
