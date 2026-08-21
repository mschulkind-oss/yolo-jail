package integration

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPackDeliversSkillAndBriefing is C3 end to end in a real container: a local
// (file://) pack's skills/ tree reaches the agent's :ro-mounted skills dir, and its
// AGENTS.md prose reaches the briefing WITH a provenance header naming the pack.
//
// The provenance header is the part worth an integration test rather than a unit
// test: pack prose is instructions the agent will follow, so if attribution were
// lost anywhere in the staging→mount→compose chain, a surprising rule would be
// untraceable and nobody would notice until it mattered.
//
// `packs` is USER-scope only by construction, so the fixture writes a user config
// under a temp HOME rather than a workspace config — which is itself worth
// exercising, since it is the only channel that works.
func TestPackDeliversSkillAndBriefing(t *testing.T) {
	requireJail(t)

	pack := t.TempDir()
	skill := filepath.Join(pack, "skills", "pack-demo")
	if err := os.MkdirAll(skill, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skill, "SKILL.md"),
		[]byte("---\nname: pack-demo\ndescription: from a pack\n---\n# Pack Demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pack, "AGENTS.md"),
		[]byte("PACKRULE always prefer rg\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// The user config carries BOTH: the claude pack (so there is an agent whose skills dir
	// and briefing the user pack layers into) and the user pack under test. One key, because
	// `packs` is a single user-scope list — there is no separate "official" tier.
	dir := writeProject(t, `{}`)
	packHome(t, `{"packs": ["claude", "file://`+pack+`"]}`)

	r := runYolo(t, dir,
		`ls /home/agent/.claude/skills/pack-demo/SKILL.md && `+
			`rg -c PACKRULE /home/agent/.claude/CLAUDE.md && `+
			`rg -c 'from pack:' /home/agent/.claude/CLAUDE.md`)
	if r.rc != 0 {
		t.Fatalf("pack delivery failed: rc %d\nstdout: %s\nstderr: %s", r.rc, r.stdout, r.stderr)
	}
	if !strings.Contains(r.stdout, "SKILL.md") {
		t.Errorf("pack skill not mounted:\n%s", r.stdout)
	}
}

// A pack whose only/exclude filters match nothing must WARN rather than silently
// deliver an empty tree: that is nearly always a filter typo, and the user would
// otherwise just see a pack that does nothing.
func TestPackWithNoMatchingFilesWarns(t *testing.T) {
	requireJail(t)

	pack := t.TempDir()
	if err := os.WriteFile(filepath.Join(pack, "AGENTS.md"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	dir := writeProject(t, `{}`)
	packHome(t, `{"packs": ["claude", {"source": "file://`+pack+`", "only": ["nothing-matches/*"]}]}`)

	r := runYolo(t, dir, "true")
	if !strings.Contains(r.combined(), "staged 0 files") {
		t.Errorf("expected a 0-files warning for a pack whose filters match nothing:\n%s", r.combined())
	}
}

// TestPackFilesTreeReachesTheJail is the `files` kind end to end in a real container.
//
// It needs to be an integration test, not a unit test: `files` shipped INERT for exactly
// the reason a unit test could not have caught — `pack lint` passed, `pack footprint`
// printed a "read-only tree" claim, and the mount assembler never switched on the kind, so
// every host-side signal said it worked and nothing in a jail did
// (docs/plans/pack-host-management-plan.md N1). Only a started container proves delivery.
//
// Two assertions, both load-bearing:
//
//   - the tree is present RECURSIVELY at the declared `into` (a bind, not a file copy), and
//   - the destination is READ-ONLY, which is the `files` contract: the claim is
//     CombineExclusive, so the pack owns the path and nothing in the jail rewrites it.
func TestPackFilesTreeReachesTheJail(t *testing.T) {
	requireJail(t)

	pack := t.TempDir()
	if err := os.MkdirAll(filepath.Join(pack, "files", "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pack, "files", "marker.txt"),
		[]byte("FILESKINDMARKER\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pack, "files", "nested", "deep.txt"),
		[]byte("nested-ok\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// `from` is HONORED for files (unlike skills/briefing, where the stager reads the
	// conventional location) — so the manifest is what names the tree.
	if err := os.WriteFile(filepath.Join(pack, "pack.json"),
		[]byte(`{"name":"filespack","contributes":[`+
			`{"kind":"files","from":"files","into":".filespack"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	// No agent pack: the destination therefore lands straight on the :ro GlobalHome base
	// rather than nesting inside a pack's writable state dir, which is the harder of the
	// two mountpoint cases (the OCI runtime will not always create a mountpoint inside a
	// :ro bind — see preparePackFiles).
	dir := writeProject(t, `{}`)
	packHome(t, `{"packs": ["file://`+pack+`"]}`)

	r := runYolo(t, dir,
		`cat /home/agent/.filespack/marker.txt && `+
			`cat /home/agent/.filespack/nested/deep.txt && `+
			`(touch /home/agent/.filespack/should-fail 2>&1 || echo REFUSED-RW)`)
	if r.rc != 0 {
		t.Fatalf("`files` delivery failed: rc %d\nstdout: %s\nstderr: %s", r.rc, r.stdout, r.stderr)
	}
	for _, want := range []string{"FILESKINDMARKER", "nested-ok", "REFUSED-RW"} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("missing %q in jail output — `files` must deliver the whole tree, "+
				"read-only:\n%s", want, r.stdout)
		}
	}
}

// TestPackFilesCollisionFailsPreflight: two packs claiming one `files` destination must
// fail on the HOST, naming both packs, before podman is invoked.
//
// Without the pre-flight the assembler emits two binds at one path and podman kills the
// boot with "duplicate mount destination" — a runtime error that names neither pack and
// reads as a yolo bug. `files` is sole-owned, so a second claimant is a footprint
// violation and the diagnosis belongs on the host side.
func TestPackFilesCollisionFailsPreflight(t *testing.T) {
	requireJail(t)

	var dirs []string
	for _, name := range []string{"alpha", "beta"} {
		root := filepath.Join(t.TempDir(), name)
		if err := os.MkdirAll(filepath.Join(root, "files"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "files", name+".txt"),
			[]byte(name+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "pack.json"),
			[]byte(`{"name":"`+name+`","contributes":[`+
				`{"kind":"files","from":"files","into":".shared/tree"}]}`), 0o644); err != nil {
			t.Fatal(err)
		}
		dirs = append(dirs, root)
	}

	dir := writeProject(t, `{}`)
	packHome(t, `{"packs": ["file://`+dirs[0]+`", "file://`+dirs[1]+`"]}`)

	r := runYolo(t, dir, `echo SHOULD-NOT-REACH-THE-JAIL`)
	if r.rc == 0 {
		t.Fatalf("a `files` collision must fail the launch, got rc 0:\n%s", r.combined())
	}
	if strings.Contains(r.stdout, "SHOULD-NOT-REACH-THE-JAIL") {
		t.Errorf("the container started despite a sole-ownership violation:\n%s", r.stdout)
	}
	out := r.combined()
	for _, want := range []string{"alpha", "beta", ".shared/tree"} {
		if !strings.Contains(out, want) {
			t.Errorf("pre-flight error missing %q — podman's own error names neither pack, "+
				"which is why this check exists:\n%s", want, out)
		}
	}
}

// packHomeSharedStores are the HOME-relative store paths packHome re-links back to
// the real home. Both entries are load-bearing; TestPackHomeSharesHostStores pins them.
//
//   - yolo's own store, because paths.GlobalStorage() is $HOME/.local/share/yolo-jail:
//     redirecting it moves the podman IMAGE CACHE and its last-load sentinel, so a pack
//     test would build and load into a throwaway store and the next test needing a
//     freshly-built image (the lib-farm `packages:` tests) would reuse a stale one.
//
//   - ROOTLESS PODMAN's own store, whose graphroot is
//     $HOME/.local/share/containers/storage. Redirecting THAT makes the child re-load
//     the entire jail image into the t.TempDir(); the image's overlay diffs carry
//     read-only nix-store trees that t.TempDir()'s RemoveAll cannot unlink, so the test
//     fails in cleanup ("permission denied") after its assertions already passed. Not
//     observable in yolo's own jail, where podman is root with a graphroot at
//     /var/lib/containers/storage that no HOME redirect can move — which is why this
//     only ever broke CI.
//
//   - podman's CONFIG dir, which on macOS holds the MACHINE CONNECTIONS
//     (podman-connections.json / machine state). Hide it and `podman info` fails, so
//     the CLI's runtimeIsConnectable() probe concludes the VM is down and every
//     packHome test dies with "Configured runtime 'podman' is installed but not
//     started" — while the interleaved real-HOME tests pass against the very same
//     running machine. That contradiction is the tell; the machine was never down.
var packHomeSharedStores = []string{
	".local/share/yolo-jail",
	".local/share/containers",
	".config/containers",
}

// packHome points HOME at a temp dir carrying only the given user config, and
// RE-LINKS the real home's stores into it (see packHomeSharedStores for which, and
// why each one matters). Packs need a custom CONFIG, not a custom store.
//
// Each link's TARGET IS CREATED FIRST, and that is not defensive tidying. A symlink
// to a missing directory is DANGLING, and os.MkdirAll — which
// storage.EnsureGlobalStorage runs on $HOME/.local/share/yolo-jail at the start of
// every invocation — refuses a dangling link with "mkdir <path>: file exists" instead
// of following it. A developer machine has always already created these stores, so the
// link resolves and the bug is invisible; a fresh CI runner has not, so every packHome
// test that ran before the first real-HOME test died in setup. MkdirAll on the real
// path is the fix and is safe: it is the same call, on the same paths, that a normal
// yolo run makes anyway.
func packHome(t *testing.T, userConfig string) {
	t.Helper()
	home := t.TempDir()
	if err := seedPackHome(home, os.Getenv("HOME"), userConfig); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
}

// seedPackHome writes userConfig into home and re-links realHome's shared stores into it.
// It is packHome without the *testing.T, so warmJail (which runs from TestMain and has no
// T) obeys the SAME store rule instead of carrying a second copy of it.
//
// Splitting it out is not tidiness: the rule is subtle enough that
// TestPackHomeSharedStores documents three separate CI outages caused by getting it wrong,
// and warmJail's first version skipped the redirect entirely and inherited the developer's
// own `packs` selection — which named a host path invisible inside the jail. A second
// implementation of this would have been a third outage waiting for its turn.
func seedPackHome(home, realHome, userConfig string) error {
	cfgDir := filepath.Join(home, ".config", "yolo-jail")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "config.jsonc"), []byte(userConfig), 0o644); err != nil {
		return err
	}
	for _, store := range packHomeSharedStores {
		rel := filepath.FromSlash(store)
		target := filepath.Join(realHome, rel)
		if err := os.MkdirAll(target, 0o755); err != nil {
			return fmt.Errorf("creating host store %s (a symlink to a missing dir is dangling, "+
				"and MkdirAll rejects that as %q): %w", target, "file exists", err)
		}
		link := filepath.Join(home, rel)
		if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
			return err
		}
		if err := os.Symlink(target, link); err != nil {
			return err
		}
	}
	return nil
}

// TestFzfAcceptanceCaseInJail is THE acceptance test for the pack/host-render work
// (docs/plans/pack-host-management-plan.md): one pack delivering an executable script, the
// settings key that points at it, and briefing prose — all three reaching a real container.
//
// It is an integration test because every interesting failure in this cluster was invisible
// to unit tests. The exec bit was stripped at three separate layers, each with a passing unit
// test asserting the old behavior; a `files` tree mounted :ro over a directory an agent writes
// killed the boot with an error naming the wrong culprit; and two packs at one briefing
// destination hit podman's duplicate-mount-destination. None of that shows up until a
// container starts.
//
// The shape is the requester's real case: a `fileSuggestion` command pointing at a script the
// pack owns. Before this work the script reached NO jail — in-jail Claude had a
// fileSuggestion pointing at a nonexistent file — so this is a live-breakage regression test,
// not a tidiness one.
func TestFzfAcceptanceCaseInJail(t *testing.T) {
	requireJail(t)

	pack := t.TempDir()
	if err := os.MkdirAll(filepath.Join(pack, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	// 0o755 in the pack. It must arrive 0o755 in the jail: the CONSUMER opted in with
	// allow_exec below, and an admission gate that stripped the bit anyway would mean a pack
	// can ship a script nothing can run.
	script := filepath.Join(pack, "bin", "file-suggestion.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho FZF-RAN\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(script, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pack, "AGENTS.md"),
		[]byte("Use the fzf finder.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// `into` is .claude/bin, NOT .claude: a files tree is a :ro mount, so claiming the whole
	// dir would shadow claude's own settings.json surface and refuse the boot. That conflict
	// is caught in pre-flight now, and this is the shape that avoids it.
	if err := os.WriteFile(filepath.Join(pack, "pack.json"),
		[]byte(`{"name":"fzfpack","contributes":[`+
			`{"kind":"files","from":"bin","into":".claude/bin"},`+
			`{"kind":"briefing","from":"AGENTS.md","into":".claude/CLAUDE.md"},`+
			`{"kind":"config","config":[{"agent":"claude","name":"fzfsettings",`+
			`"codec":"json","path":"~/.claude/fzf-settings.json","mode":"rmw",`+
			`"managed":{"fileSuggestion":{"type":"command",`+
			`"command":"~/.claude/bin/file-suggestion.sh"}}}]}]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	// WITH the claude pack: it declares .claude as writable state, and it also declares a
	// briefing at the same destination this pack does — which is the duplicate-mount case,
	// now deduped. Both facts are load-bearing, so the test carries them.
	dir := writeProject(t, `{}`)
	packHome(t, `{"packs": ["claude", {"source": "file://`+pack+`", "allow_exec": true}]}`)

	r := runYolo(t, dir,
		`test -x /home/agent/.claude/bin/file-suggestion.sh && echo IS-EXECUTABLE; `+
			`/home/agent/.claude/bin/file-suggestion.sh; `+
			`grep -q file-suggestion /home/agent/.claude/fzf-settings.json && echo KEY-WIRED; `+
			`grep -q "fzf finder" /home/agent/.claude/CLAUDE.md && echo PROSE-PRESENT`)
	if r.rc != 0 {
		t.Fatalf("fzf acceptance case failed: rc %d\nstdout: %s\nstderr: %s", r.rc, r.stdout, r.stderr)
	}
	for _, want := range []string{"IS-EXECUTABLE", "FZF-RAN", "KEY-WIRED", "PROSE-PRESENT"} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("missing %q — the pack must deliver a RUNNABLE script, the settings key "+
				"pointing at it, and its briefing prose:\n%s", want, r.stdout)
		}
	}
}
