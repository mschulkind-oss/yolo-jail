package integration

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
)

// unmodifiedextension_test.go is the container-level cell for an UNMODIFIED EXTENSION from an npm
// source (docs/design/pi-extension-store-builds.md §4, OQ-6 (c); XB-D1, XB-D5, XB-D6): a pack whose
// `files` contribution names `npm:<name>@<version>` and no series, built on the host exactly as a
// patched extension is — the version resolved on the host, npm's own install of it run in the
// sealed capture jail, the tree admitted and copied for the launch — and mounted read-only where the
// agent's list names the package inside it. No agent is started (AGENTS.md: no agent tests); the
// jail runs bash, and the test reads files and the launch's own lines.
//
// NOT HERMETIC, unlike patchedextension_test.go: the build jail installs from the public npm
// registry, as every integration cell that installs an npm agent does. The version is exact, so the
// host's check asks the registry nothing (XB-D5), and the package is one with no dependencies and no
// install script. The fixture uses a private HOME-derived capture/pack store, linking only the run's
// explicitly shared children.

const (
	npmTreeAgentPack = "utree-agent"
	npmTreeAgentBin  = "utreeagent"
	npmTreeExtPack   = "utree-ext"
	npmTreeName      = "is-number"
	npmTreeSource    = "npm:is-number@7.0.0"
	npmTreeInto      = ".utreeagent/ext/" + npmTreeName
	npmTreeEntry     = "~/" + npmTreeInto + "/node_modules/is-number"
)

func TestAnUnmodifiedNpmExtensionIsBuiltOnTheHostAndMountedReadOnly(t *testing.T) {
	requireJail(t)
	writeManifest := func(dir, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, "pack.json"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// THE OWNING AGENT PACK: a program (never run here) and the settings surface whose list names
	// the tree, its home directory its state, as pi's `~/.pi` is.
	agent := t.TempDir()
	if err := os.WriteFile(filepath.Join(agent, "install.sh"), []byte("#!/bin/bash\nexit 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeManifest(agent, `{"name":"`+npmTreeAgentPack+`","contributes":[`+
		`{"kind":"program","bin":"`+npmTreeAgentBin+`","via":"installer","url":"file:///ctx/packs/`+npmTreeAgentPack+`/install.sh"},`+
		`{"kind":"state","at":".utreeagent","scope":"workspace"},`+
		`{"kind":"config","config":[{"agent":"`+npmTreeAgentBin+`","name":"settings","codec":"json",`+
		`"path":"~/.utreeagent/settings.json"}]}]}`)
	// THE CONTRIBUTING PACK: the unmodified npm extension, its fallback, and the list entry naming
	// the package inside the tree's npm prefix.
	ext := t.TempDir()
	writeManifest(ext, `{"name":"`+npmTreeExtPack+`","contributes":[`+
		`{"kind":"files","into":"`+npmTreeInto+`","source":"`+npmTreeSource+`","fallback":"`+npmTreeSource+`"},`+
		`{"kind":"config-list","surface":"`+npmTreeAgentBin+`/settings","path":"/packages",`+
		`"add":["`+npmTreeEntry+`"]}]}`)
	packHome(t, `{"packs": [{"source": "file://`+agent+`", "name": "`+npmTreeAgentPack+`"}, `+
		`{"source": "file://`+ext+`", "name": "`+npmTreeExtPack+`"}]}`)
	withPrivateFixtureYoloStore(t)

	state := filepath.Join(os.Getenv("HOME"), ".local", "share", "yolo-jail")
	store := filepath.Join(state, "captures")
	before := captureEntryNames(t, store)
	owner := npmTreeExtPack + "/" + npmTreeName
	record := (&packsrc.Store{Dir: filepath.Join(state, "packs")}).CheckRecordPath(owner)
	_ = os.Remove(record)
	t.Cleanup(func() {
		removeNewCaptureEntries(t, store, before, npmTreeName)
		_ = os.Remove(record)
	})

	// The jail's probe: the package's own version as npm installed it, whether the tree can be
	// written, what the jail was handed, and the agent's settings as rendered.
	probe := `d="$HOME/` + npmTreeInto + `/node_modules/is-number"
echo "PKG=$(tr -d ' \n' < "$d/package.json" | grep -o '"version":"[^"]*"')"
if touch "$d/.probe" 2>/dev/null; then echo TREE_WRITABLE; else echo TREE_READONLY; fi
echo "TREES=$YOLO_PATCHED_TREES"
echo "SETTINGS=$(tr -d ' \n' < "$HOME/.utreeagent/settings.json")"`
	launch := func(what string) string {
		t.Helper()
		r := runCommand(t, t.TempDir(), append(jailRunArgs(), "--", "bash", "-c", probe),
			withoutFixtureProgramInstall(), withHostSemantics())
		out := r.combined()
		if r.rc != 0 {
			t.Fatalf("%s: rc %d\n%s", what, r.rc, out)
		}
		return out
	}

	// THE FIRST LAUNCH builds the tree on the host and mounts a copy, read-only, where the list
	// names the package; the fallback is not taken, since a tree was handed.
	out := launch("the first launch")
	for _, w := range []string{
		`PKG="version":"7.0.0"`,
		"TREE_READONLY",
		`"` + npmTreeEntry + `"`,
		"Patched extensions this launch:",
		"extension " + owner + ": ~/" + npmTreeInto + ", an unmodified extension of " + npmTreeSource,
		// The build's start line, with its disclosure: npm's own install of the version it builds.
		"build extension " + owner + ": 7.0.0, the first build of it on this machine",
		"it runs npm's own install of the package: npm install is-number@7.0.0 --prefix .",
		"built extension " + owner + ": 7.0.0; this jail runs it",
	} {
		if !strings.Contains(out, w) {
			t.Errorf("the first launch lacks %q:\n%s", w, out)
		}
	}
	if !strings.Contains(out, "TREES=") || !strings.Contains(out[strings.Index(out, "TREES="):], owner) {
		t.Errorf("the jail was not handed the extension's build in YOLO_PATCHED_TREES:\n%s", out)
	}
	if strings.Contains(out, `"`+npmTreeSource+`"`) {
		t.Errorf("the fallback was taken though a tree was handed:\n%s", out)
	}
	added := newCaptureEntries(t, store, before, npmTreeName)
	if live := liveCaptureEntries(t, store, added); len(live) != 1 || len(added) != 1 {
		t.Fatalf("the first launch added %d live tree entries, want one: %v", len(live), live)
	}
	receipts, receiptIdentity := buildReceiptSnapshot(t, store, added[0])
	if receipts[0].Bin != npmTreeName || !strings.HasPrefix(receipts[0].Source, "npm:"+npmTreeName) || receipts[0].Revision != "7.0.0" {
		t.Fatalf("the first npm tree build receipt does not identify its executed build: %+v", receipts[0])
	}
	t.Logf("npm tree build execution receipt: count=1 key=%s digest=%s source=%s revision=%s",
		receipts[0].Key, receipts[0].Digest, receipts[0].Source, receipts[0].Revision)
	if got := newCaptureEntries(t, store, before, npmTreeAgentBin); len(got) != 0 {
		t.Fatalf("the owning installer's disabled auto-capture added entries: %v", got)
	}

	// A SECOND LAUNCH builds nothing: an exact version is checked only until it first resolves
	// (XB-D2), and its build serves.
	out = launch("the second launch")
	if strings.Contains(out, "build extension "+owner+":") || !strings.Contains(out, `PKG="version":"7.0.0"`) {
		t.Errorf("the second launch built again, or mounts no tree:\n%s", out)
	}
	if got := newCaptureEntries(t, store, before, npmTreeName); len(got) != 1 || got[0] != receipts[0].Key {
		t.Fatalf("the second launch changed the built tree entry identity: %v, original %s", got, receipts[0].Key)
	}
	secondReceipts, secondIdentity := buildReceiptSnapshot(t, store, receipts[0].Key)
	if !bytes.Equal(secondIdentity, receiptIdentity) || secondReceipts[0] != receipts[0] {
		t.Errorf("the second launch changed the build receipt identity/count: before=%+v after=%+v", receipts, secondReceipts)
	}
	t.Logf("second launch kept build identity: key=%s build-receipt-count=%d", secondReceipts[0].Key, len(secondReceipts))
	if got := newCaptureEntries(t, store, before, npmTreeAgentBin); len(got) != 0 {
		t.Errorf("the second launch captured the owning installer despite both fixture suppression dials: %v", got)
	}
}
