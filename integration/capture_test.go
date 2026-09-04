package integration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

// capture_test.go is the container-level cell for `yolo capture` (install-capture.md slice
// 3, docs/design/program-delivery.md §6.3): one real jail, one installer, one admitted
// entry.
//
// It exists because every interesting property of a capture is invisible below a container.
// The delta is defined by the jail's own bind surfaces; the baseline has to be walked AFTER
// the boot has already written into them; and whether the move is a rename or a 1.2 GB copy
// is decided by which of a bind-mounted directory's two paths the driver used — a
// distinction with no meaning outside a container, and the one a unit test can only model.
//
// HERMETIC, like installmechanism_test.go's installer cell: the fixture pack carries its own
// installer script and the URL is `file:///ctx/packs/<name>/install.sh`, so nothing here
// touches a vendor or the network. Never a real vendor installer — that question belongs to
// the weekly job, and a capture of a 1.2 GB agent CLI is not a per-push test.
//
// ONE THING TO KNOW ABOUT THE HOME: requireJail isolates $HOME, but
// `.local/share/yolo-jail` is deliberately re-linked to the machine's real store
// (packHomeSharedStores) so the podman image cache is shared. The capture store lives under
// that path, so this test really does admit an entry into the developer's own store — which
// is why it records what was there first and removes only what it added.

const (
	captureFixturePack = "capture-fixture-pack"
	captureFixtureBin  = "yolo-capture-fixture"
	captureFixtureRan  = "CAPTURE_FIXTURE_INSTALLER_RAN"
)

// captureFixtureInstallerScript is the pack's own installer: the shape a vendor installer of
// this class has — a versioned directory plus an absolute symlink into it from
// ~/.local/bin — plus two things the assertions need.
//
// It writes into `~/.local/share/yolo-jail`, which is INSIDE the `.local` capture surface
// and is yolo's own state dir. Nothing about a fixture makes that write special: it stands
// for the receipt a launcher appends and the boot step that finishes late, and the capture
// must exclude it either way.
//
// And it prints the INODE of what it created. That is the external oracle for "the move was
// a rename": the number is observed by the installer inside the jail, and a bind mount
// reports the filesystem's own inode, so an entry the host later finds under the same inode
// cannot have been copied.
const captureFixtureInstallerScript = `#!/bin/bash
set -euo pipefail
mkdir -p "$HOME/.local/share/fixturetool/1.0.0"
printf '#!/bin/bash\necho ` + captureFixtureRan + `\n' > "$HOME/.local/share/fixturetool/1.0.0/tool"
chmod +x "$HOME/.local/share/fixturetool/1.0.0/tool"
mkdir -p "$HOME/.local/bin"
ln -s "$HOME/.local/share/fixturetool/1.0.0/tool" "$HOME/.local/bin/` + captureFixtureBin + `"
mkdir -p "$HOME/.npm-global/lib"
printf 'npm side\n' > "$HOME/.npm-global/lib/fixturetool-marker"

# yolo's OWN state dir, inside the .local surface, written after the baseline walk.
mkdir -p "$HOME/.local/share/yolo-jail"
printf 'yolo state\n' > "$HOME/.local/share/yolo-jail/capture-fixture-marker"

printf 'FIXTURE_INODE %s\n' "$(ls -di "$HOME/.local/share/fixturetool" | awk '{print $1}')"

# Did the BOOT already put npm packages in a capture surface? If so, the assertions can
# check that they stayed out of the delta — which is the whole reason the baseline walk
# happens in here rather than on the host, before the launch.
if [ -d "$HOME/.npm-global/lib/node_modules" ]; then echo BOOT_NPM_PRESENT; fi

echo "` + captureFixtureRan + `-INSTALL"
`

// TestCaptureRecordsAnInstallerIntoTheStore is the acceptance case for slice 3.
func TestCaptureRecordsAnInstallerIntoTheStore(t *testing.T) {
	requireJail(t)

	pack := t.TempDir()
	if err := os.WriteFile(filepath.Join(pack, "install.sh"),
		[]byte(captureFixtureInstallerScript), 0o644); err != nil {
		t.Fatal(err)
	}
	manifest := `{
  "name": "` + captureFixturePack + `",
  "description": "install-capture fixture",
  "contributes": [
    {"kind": "program", "bin": "` + captureFixtureBin + `", "via": "installer",
     "url": "file:///ctx/packs/` + captureFixturePack + `/install.sh"}
  ]
}`
	if err := os.WriteFile(filepath.Join(pack, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	// The entry names itself explicitly: a pack's staged directory is named by
	// defaultPackName off the SOURCE URL's last segment, which under t.TempDir() is a
	// counter — so the bare form would stage this pack somewhere the installerUrl above
	// does not point. Same trap installmechanism_test.go records.
	packHome(t, `{"packs": [{"source": "file://`+pack+`", "name": "`+captureFixturePack+`"}]}`)

	store := filepath.Join(os.Getenv("HOME"), ".local", "share", "yolo-jail", "captures")
	before := captureEntryNames(t, store)
	t.Cleanup(func() { removeNewCaptureEntries(t, store, before) })
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Join(store, "staging", captureFixtureBin)) })

	r := runYoloCLI(t, t.TempDir(), "capture", captureFixtureBin)
	if r.rc != 0 {
		t.Fatalf("yolo capture failed: rc %d\nstdout: %s\nstderr: %s", r.rc, r.stdout, r.stderr)
	}
	combined := r.combined()
	if !strings.Contains(combined, captureFixtureRan+"-INSTALL") {
		t.Errorf("the pack's installer never ran:\n%s", combined)
	}
	// THE TOOL ITSELF MUST NOT HAVE RUN. The launcher installs under
	// entrypoint.InstallOnlyEnv and stops; if it exec'd the tool, the tool's first-run
	// state would be written into the very surfaces being captured.
	if strings.Contains(combined, captureFixtureRan+"\n") {
		t.Errorf("the captured tool was EXECUTED during the capture — its first-run state "+
			"would land in the entry:\n%s", combined)
	}

	// Exactly one new entry, and it is complete.
	added := newCaptureEntries(t, store, before)
	if len(added) != 1 {
		t.Fatalf("got %d new capture entries, want 1: %v", len(added), added)
	}
	entry := filepath.Join(store, "entries", added[0])
	tree := filepath.Join(entry, "tree")
	if _, err := os.Stat(filepath.Join(entry, ".yolo-capture-complete")); err != nil {
		t.Errorf("the entry has no completion marker: %v", err)
	}

	// 1. The vendor's bytes are there, with their shape intact.
	toolPath := filepath.Join(tree, ".local", "share", "fixturetool", "1.0.0", "tool")
	fi, err := os.Lstat(toolPath)
	if err != nil {
		t.Fatalf("the installed binary is not in the entry: %v", err)
	}
	if fi.Mode().Perm()&0o111 == 0 {
		t.Errorf("the captured binary lost its exec bit: %v", fi.Mode())
	}
	// Read-only at admit: a materialized file is a HARDLINK to this inode, so a write
	// through any workspace would corrupt every workspace at once.
	if fi.Mode().Perm()&0o222 != 0 {
		t.Errorf("entry files must be read-only, got %v", fi.Mode())
	}
	link, err := os.Readlink(filepath.Join(tree, ".local", "bin", captureFixtureBin))
	if err != nil {
		t.Errorf("the launcher symlink is not in the entry: %v", err)
	} else if link != "/home/agent/.local/share/fixturetool/1.0.0/tool" {
		t.Errorf("symlink target = %q, want the absolute jail path verbatim", link)
	}
	// A second surface, so the delta is not accidentally ".local only".
	if _, err := os.Stat(filepath.Join(tree, ".npm-global", "lib", "fixturetool-marker")); err != nil {
		t.Errorf("the .npm-global side of the delta is missing: %v", err)
	}

	// 2. THE BOOT'S OWN WRITES ARE NOT THE VENDOR'S. This is the property that forces the
	//    driver to run inside the jail at all: the bootstrap fills `.npm-global` before any
	//    installer does, and a host-side before/after diff of the bind dirs could not tell
	//    those bytes from these. Conditional on the boot having actually written, and the
	//    fixture says so rather than leaving the assertion silently vacuous.
	if strings.Contains(combined, "BOOT_NPM_PRESENT") {
		if _, err := os.Lstat(filepath.Join(tree, ".npm-global", "lib", "node_modules")); !os.IsNotExist(err) {
			t.Errorf("the boot's own npm packages were captured as the vendor's (%v)", err)
		}
	} else {
		t.Log("the boot left no node_modules in this jail, so the baseline-after-boot " +
			"assertion had nothing to measure")
	}

	// 3. YOLO'S OWN STATE IS NOT IN THE CAPTURE, though the installer wrote into it and
	//    `.local` is a captured surface.
	if _, err := os.Lstat(filepath.Join(tree, ".local", "share", "yolo-jail")); !os.IsNotExist(err) {
		t.Errorf("yolo's own state dir was captured (%v) — it would then be hardlinked "+
			"into every workspace on this machine", err)
	}

	// 4. The manifest describes THIS tree, from inside the jail.
	m := readCaptureManifest(t, filepath.Join(entry, "capture-manifest.json"))
	if got := str(m["home"]); got != "/home/agent" {
		t.Errorf("manifest home = %q, want the jail HOME (not the workspace-side view)", got)
	}
	if got := str(m["platform"]); !strings.HasPrefix(got, "linux/") {
		t.Errorf("manifest platform = %q, want the JAIL's linux/<arch>", got)
	}
	if got := strList(m["excluded"]); len(got) != 1 || got[0] != ".local/share/yolo-jail" {
		t.Errorf("manifest excluded = %v, want yolo's state dir", got)
	}

	// 5. The receipt is beside the entry, in the same schema every other receipt uses.
	rec := readOneJSONLine(t, filepath.Join(entry, "receipts.jsonl"))
	for _, c := range []struct{ field, want string }{
		{"kind", "capture"},
		{"act", "record"},
		{"bin", captureFixtureBin},
		{"declared", "file:///ctx/packs/" + captureFixturePack + "/install.sh"},
		{"resolved", added[0]},
		{"path", entry},
	} {
		if got := str(rec[c.field]); got != c.want {
			t.Errorf("receipt %s = %q, want %q", c.field, got, c.want)
		}
	}
	if got := str(rec["platform"]); got != str(m["platform"]) {
		t.Errorf("receipt platform %q disagrees with the manifest's %q", got, str(m["platform"]))
	}

	// 6. THE DELTA MOVED BY RENAME. Measured against the inode the installer itself
	//    observed inside the jail — the number neither the driver's counter nor these
	//    assertions control. This is the whole reason the capture workspace lives under
	//    the store and the driver reaches the surfaces through the /workspace bind; get
	//    either wrong and the capture still works, by copying every byte twice.
	if runtime.GOOS != "linux" {
		t.Log("skipping the rename oracle: a Linux jail's inodes are not this host's")
	} else if ino, ok := fixtureInode(combined); !ok {
		t.Errorf("the fixture printed no inode; stdout was:\n%s", combined)
	} else if got := inodeOfPath(t, filepath.Join(tree, ".local", "share", "fixturetool")); got != ino {
		t.Errorf("the version dir has inode %d in the store and had %d when the installer "+
			"made it — the delta was COPIED, which is the cost this subsystem exists to "+
			"delete", got, ino)
	}

	// 7. The scratch workspace is gone: a capture boots a whole jail into it, and one
	//    provisioned home per captured program would accumulate forever.
	if _, err := os.Stat(filepath.Join(store, "staging", captureFixtureBin)); !os.IsNotExist(err) {
		t.Errorf("the capture workspace survived: %v", err)
	}
}

// captureEntryNames lists the store's entry keys, tolerating an absent store.
func captureEntryNames(t *testing.T, store string) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	ents, err := os.ReadDir(filepath.Join(store, "entries"))
	if err != nil {
		return out
	}
	for _, e := range ents {
		out[e.Name()] = true
	}
	return out
}

// newCaptureEntries names the keys that appeared since before.
func newCaptureEntries(t *testing.T, store string, before map[string]bool) []string {
	t.Helper()
	var added []string
	for name := range captureEntryNames(t, store) {
		if !before[name] {
			added = append(added, name)
		}
	}
	return added
}

// removeNewCaptureEntries deletes only what this test added, because the store it wrote into
// is the machine's own (see the file comment).
func removeNewCaptureEntries(t *testing.T, store string, before map[string]bool) {
	t.Helper()
	for _, name := range newCaptureEntries(t, store, before) {
		_ = os.RemoveAll(filepath.Join(store, "entries", name))
	}
}

func readCaptureManifest(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("no capture manifest beside the tree: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("the manifest is not JSON: %v", err)
	}
	return m
}

func readOneJSONLine(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("no receipt beside the entry: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("got %d receipt lines, want 1:\n%s", len(lines), data)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &m); err != nil {
		t.Fatalf("the receipt is not JSON: %v\n%s", err, lines[0])
	}
	return m
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

func strList(v any) []string {
	raw, _ := v.([]any)
	out := make([]string, 0, len(raw))
	for _, e := range raw {
		out = append(out, str(e))
	}
	return out
}

// fixtureInode parses the `FIXTURE_INODE <n>` line the installer printed.
func fixtureInode(out string) (uint64, bool) {
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) == 2 && f[0] == "FIXTURE_INODE" {
			n, err := strconv.ParseUint(f[1], 10, 64)
			return n, err == nil
		}
	}
	return 0, false
}

func inodeOfPath(t *testing.T, path string) uint64 {
	t.Helper()
	fi, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("lstat %s: %v", path, err)
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		t.Skip("no syscall.Stat_t on this platform")
	}
	return st.Ino
}
