package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
)

// patchedextension_test.go is the container-level cell for a PATCHED EXTENSION
// (docs/design/patched-extensions.md §16's "done looks like" for steps 2 to 5): a pack whose `files`
// contribution names an upstream and a `git format-patch --base` series in place of `from`, and an
// agent pack that owns the list entry naming the tree, delivered by fresh launches with nobody
// pinning anything. No agent is started (AGENTS.md: no agent tests); the jail runs bash, and the
// test reads files and the launch's own lines.
//
//  1. The first launch checks the upstream, replays the series onto the newest version it fits,
//     builds the tree in a sealed capture jail, admits it, and mounts a per-launch copy read-only at
//     `~/<into>`; the agent's list names it, the jail is handed the build, and the launch discloses
//     the extension and the build.
//  2. A version the series does not fit is held: `yolo pack update` reports the conflict and names
//     `yolo pack rebase <pack>/<name>`; the next launch still mounts the previous build and its
//     line names what holds it; and the named command, run as printed, stops at the same conflict
//     in a clone of the upstream.
//
// HERMETIC, like patchedfork_test.go, whose upstream fixture it shares: the upstream is a local git
// repository (git+file://). ⚠ THE PACK AND CAPTURE STORES ARE SHARED with the machine
// (packHomeSharedStores), so the test removes the capture entries it added and its check record.

const (
	patchTreeAgentPack = "ptree-agent"
	patchTreeAgentBin  = "ptreeagent"
	patchTreeExtPack   = "ptree-ext"
	patchTreeName      = "ptree"
	patchTreeInto      = ".ptreeagent/ext/" + patchTreeName
)

func TestPatchedExtensionIsBuiltMountedReadOnlyAndHeldAtAConflict(t *testing.T) {
	requireJail(t)
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	up := patchFixtureUpstream{t: t, dir: t.TempDir()}
	up.git("init", "-q", "-b", "main")
	base := up.release("1.0.0", "ten", "v1.0.0")

	// THE SERIES: one member, exported from a branch of the upstream with its base named.
	ext := t.TempDir()
	up.git("checkout", "-q", "-b", "series")
	body, err := os.ReadFile(filepath.Join(up.dir, "f.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(up.dir, "f.txt"), []byte(strings.Replace(string(body), "ten\n", "patched\n", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	up.git("commit", "-qam", "patch line ten")
	up.git("format-patch", "-q", "--base="+base, "-o", filepath.Join(ext, "patches"), "main..series")
	up.git("checkout", "-q", "main")
	up.git("branch", "-q", "-D", "series")
	v11 := up.release("1.1.0", "ten", "v1.1.0")

	writeManifest := func(dir, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, "pack.json"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// THE OWNING AGENT PACK: a program (never run here) and the settings surface whose list names
	// the tree. Its installer exists only so the declaration is a whole one.
	agent := t.TempDir()
	if err := os.WriteFile(filepath.Join(agent, "install.sh"), []byte("#!/bin/bash\nexit 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Its home directory is its state, as pi's `~/.pi` is, and the tree lands inside it, as a pi
	// extension's does.
	writeManifest(agent, `{"name":"`+patchTreeAgentPack+`","contributes":[`+
		`{"kind":"program","bin":"`+patchTreeAgentBin+`","via":"installer","url":"file:///ctx/packs/`+patchTreeAgentPack+`/install.sh"},`+
		`{"kind":"state","at":".ptreeagent","scope":"workspace"},`+
		`{"kind":"config","config":[{"agent":"`+patchTreeAgentBin+`","name":"settings","codec":"json",`+
		`"path":"~/.ptreeagent/settings.json"}]}]}`)
	// THE CONTRIBUTING PACK: the patched extension, and the list entry that makes the agent load it.
	writeManifest(ext, `{"name":"`+patchTreeExtPack+`","contributes":[`+
		`{"kind":"files","into":"`+patchTreeInto+`","source":"git+file://`+up.dir+`?ref=main",`+
		`"patches":"patches","build":"true","produces":["f.txt"]},`+
		`{"kind":"config-list","surface":"`+patchTreeAgentBin+`/settings","path":"/packages",`+
		`"add":["~/`+patchTreeInto+`"]}]}`)
	packHome(t, `{"packs": [{"source": "file://`+agent+`", "name": "`+patchTreeAgentPack+`"}, `+
		`{"source": "file://`+ext+`", "name": "`+patchTreeExtPack+`"}]}`)

	state := filepath.Join(os.Getenv("HOME"), ".local", "share", "yolo-jail")
	store := filepath.Join(state, "captures")
	before := captureEntryNames(t, store)
	owner := patchTreeExtPack + "/" + patchTreeName
	record := (&packsrc.Store{Dir: filepath.Join(state, "packs")}).CheckRecordPath(owner)
	_ = os.Remove(record)
	t.Cleanup(func() {
		removeNewCaptureEntries(t, store, before)
		_ = os.Remove(record)
	})

	// The jail's probe: the patched line, whether the tree can be written, what the jail was handed,
	// and the agent's settings as rendered.
	probe := `d="$HOME/` + patchTreeInto + `"
echo "LINE10=$(sed -n 10p "$d/f.txt")"
if touch "$d/.probe" 2>/dev/null; then echo TREE_WRITABLE; else echo TREE_READONLY; fi
echo "TREES=$YOLO_PATCHED_TREES"
echo "SETTINGS=$(tr -d ' \n' < "$HOME/.ptreeagent/settings.json")"`
	launch := func(what string) string {
		t.Helper()
		r := runYoloDirect(t, t.TempDir(), "bash", "-c", probe)
		out := r.combined()
		if r.rc != 0 {
			t.Fatalf("%s: rc %d\n%s", what, r.rc, out)
		}
		return out
	}

	// 1. THE FIRST LAUNCH builds and mounts the tree, read-only, where the agent's list names it.
	out := launch("the first launch")
	for _, w := range []string{
		"LINE10=patched",
		"TREE_READONLY",
		`"~/` + patchTreeInto + `"`,
		"Patched extensions this launch:",
		"extension " + owner + ": ~/" + patchTreeInto + ", a patched extension of git+file://" + up.dir + "?ref=main + 1 patch",
		"built extension " + owner + ": v1.1.0 (" + v11[:8] + ") + 1 patch; this jail runs it",
	} {
		if !strings.Contains(out, w) {
			t.Errorf("the first launch lacks %q:\n%s", w, out)
		}
	}
	if !strings.Contains(out, "TREES=") || !strings.Contains(out[strings.Index(out, "TREES="):], owner) {
		t.Errorf("the jail was not handed the extension's build in YOLO_PATCHED_TREES:\n%s", out)
	}
	if live := liveCaptureEntries(t, store, newCaptureEntries(t, store, before)); len(live) != 1 {
		t.Errorf("the first launch added %d live entries, want the tree's one: %v", len(live), live)
	}
	userConfig := filepath.Join(os.Getenv("HOME"), ".config", "yolo-jail", "config.jsonc")
	if data, err := os.ReadFile(packsrc.ForkLockPath(userConfig)); err == nil && strings.Contains(string(data), owner) {
		t.Errorf("a patched extension was pinned in the fork lock:\n%s", data)
	}

	// 2. A VERSION THE SERIES DOES NOT FIT is held, and the conflict names the rebase that fixes it.
	v12 := up.release("1.2.0", "upstream-ten", "v1.2.0")
	upd := runYoloCLI(t, t.TempDir(), "pack", "update").combined()
	rebase := "yolo pack rebase " + owner
	if !strings.Contains(upd, "does not take the patch series") || !strings.Contains(upd, "rebase the series: "+rebase) {
		t.Fatalf("yolo pack update did not report the conflict at v1.2.0 with its rebase:\n%s", upd)
	}
	out = launch("the launch after v1.2.0")
	if !strings.Contains(out, "LINE10=patched") || !strings.Contains(out, "TREE_READONLY") {
		t.Fatalf("the held launch does not mount the previous build:\n%s", out)
	}
	if want := "held at v1.1.0 (" + v11[:8] + "): upstream v1.2.0 (" + v12[:8] + ") does not take " +
		"0001-patch-line-ten.patch — `" + rebase + "`"; !strings.Contains(out, want) {
		t.Errorf("the held launch's extension line lacks %q:\n%s", want, out)
	}
	clone := filepath.Join(t.TempDir(), "clone")
	r := runYoloCLI(t, t.TempDir(), append(strings.Fields(rebase)[1:], "--into", clone)...)
	if r.rc != 1 || !strings.Contains(r.combined(), "extension "+owner+": upstream v1.2.0 ("+v12[:8]+
		") does not take the patch series — the rebase stopped in "+clone) {
		t.Errorf("the conflict line's own command did not stop at the conflict: rc %d\n%s", r.rc, r.combined())
	}
}
