package entrypoint

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// shareddirhook_test.go covers the shared_directory hook — the DIRECTORY twin of
// shared_credentials — on the rule both share (sharedlink.go): the shared side always wins,
// a populated local side populates an EMPTY shared side, and a copy that was attempted and
// FAILED leaves the local data exactly where it is.
//
// EVERY TEST HERE DRIVES RunPackHooks, not the hook function, and that is the point rather
// than ceremony. RunPackHooks is the entry the boot calls (boot.go, darwin.go), so deleting
// the dispatch arm, the hook's `case`, or pi's declaration fails these tests; a test calling
// linkSharedDirectory directly would survive all three. What it does NOT reach is boot.go's
// own call of RunPackHooks — that needs a whole boot, and nothing in this package has one.
//
// It also drives the REAL pi manifest rather than a fixture, so the pack's `at`/`from` and
// its machine-scope declaration have to keep agreeing (the hook refuses a shared dir the
// pack never declared, and that refusal is what bounds the cross-workspace leak).

// sharedDirHook returns the named embedded pack's shared_directory hook, failing the test if
// the pack stopped requesting one. It must never degrade to a skip: a silently dropped
// declaration is a jail that quietly goes back to a per-workspace store, which is the drift
// the machine tier exists to end and which nothing else would notice.
func sharedDirHook(t *testing.T, pack string) (*packload.Pack, packdecl.Hook) {
	t.Helper()
	p, err := embeddedPack(pack)
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range p.Decl.HookContributions() {
		if h.Name == HookSharedDirectory {
			return p, h
		}
	}
	t.Fatalf("%s pack no longer requests %s", pack, HookSharedDirectory)
	return nil, packdecl.Hook{}
}

// sharedDirEnv mints an Env over a fresh home, plus the three absolute paths the assertions
// need. EvalSymlinks at the mint point, per AGENTS.md's darwin rule: the resolved-target
// comparisons below would pass on Linux and fail on macOS otherwise.
func sharedDirEnv(t *testing.T, hook packdecl.Hook) (e *Env, link, shared string) {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	e = &Env{Home: home, Workspace: t.TempDir(), Vars: map[string]string{}}
	return e, filepath.Join(home, filepath.FromSlash(hook.File)),
		filepath.Join(home, filepath.FromSlash(hook.SharedDir))
}

// runHooks drives the boot entry and fails on any collected failure — genStep turns a hook
// error into a fatal boot problem (A12), so "no failures" is the real success condition.
func runHooks(t *testing.T, e *Env, p *packload.Pack) {
	t.Helper()
	RunPackHooks(e, []*packload.Pack{p})
	if fails := e.GenFailures(); len(fails) != 0 {
		t.Fatalf("the hook failed the boot: %v", fails)
	}
}

// sharedHookLog returns the persistent decision log, which is the only record of what
// crossed into the machine tier.
func sharedHookLog(t *testing.T, e *Env) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(e.Home, sharedCredsLog))
	if err != nil {
		t.Fatalf("no shared-tier hook log written: %v", err)
	}
	return string(data)
}

// assertLinkedToShared checks the link is a SYMLINK whose RELATIVE target resolves to the
// shared store. Reading through the link instead would pass on a plain directory.
func assertLinkedToShared(t *testing.T, link, shared string) {
	t.Helper()
	target, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("%s is not a symlink: %v", link, err)
	}
	if filepath.IsAbs(target) {
		t.Errorf("link target %q is absolute; it must be relative so it resolves through "+
			"whatever mount or sidecar backs the home", target)
	}
	resolved, err := filepath.EvalSymlinks(filepath.Join(filepath.Dir(link), target))
	if err != nil {
		t.Fatalf("the link target does not resolve: %v", err)
	}
	wantShared, err := filepath.EvalSymlinks(shared)
	if err != nil {
		t.Fatalf("shared store does not resolve: %v", err)
	}
	if resolved != wantShared {
		t.Errorf("link -> %q resolves to %q, want the shared store %q", target, resolved, wantShared)
	}
}

// seedStore writes a small store that exercises the three entry shapes a node_modules tree
// actually contains: a file, a nested file, and a RELATIVE symlink (npm's .bin entries).
func seedStore(t *testing.T, root, marker string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, "node_modules", ".bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "package.json"),
		[]byte(`{"name":"pi-extensions","private":true,"`+marker+`":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "node_modules", "pi-lens"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "node_modules", "pi-lens", "index.js"),
		[]byte("// "+marker+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../pi-lens/index.js",
		filepath.Join(root, "node_modules", ".bin", "pi-lens")); err != nil {
		t.Fatal(err)
	}
}

// TestSharedDirectoryHookLinksWhenNeitherSideExists is the FIRST-LAUNCH case, and the one a
// fresh machine takes: nothing at the local path, nothing in the store. The store must be
// created and the local path must become a relative symlink into it, because the agent
// writes through that path the moment it installs anything.
func TestSharedDirectoryHookLinksWhenNeitherSideExists(t *testing.T) {
	p, hook := sharedDirHook(t, "pi")
	e, link, shared := sharedDirEnv(t, hook)

	runHooks(t, e, p)

	assertLinkedToShared(t, link, shared)
	if fi, err := os.Stat(shared); err != nil || !fi.IsDir() {
		t.Fatalf("the shared store was not created as a directory: %v", err)
	}
	if !strings.Contains(sharedHookLog(t, e), "no local directory") {
		t.Errorf("the log does not record the first-launch decision:\n%s", sharedHookLog(t, e))
	}
	// The mark must never be left behind on a path that copied nothing, or the store reads
	// as empty forever and every launch re-runs a migration with nothing to migrate.
	if _, err := os.Lstat(filepath.Join(shared, sharedCopyIncomplete)); err == nil {
		t.Error("the in-progress mark survived a launch that copied nothing")
	}
	// And it is IDEMPOTENT: the second launch takes the already-linked fast path rather
	// than treating its own symlink as a local payload.
	e2 := &Env{Home: e.Home, Workspace: e.Workspace, Vars: map[string]string{}}
	runHooks(t, e2, p)
	assertLinkedToShared(t, link, shared)
	if !strings.Contains(sharedHookLog(t, e2), "already symlinked to shared") {
		t.Errorf("the second launch did not take the already-linked path:\n%s", sharedHookLog(t, e2))
	}
}

// TestSharedDirectoryHookMigratesAPopulatedStoreIntoAnEmptyShared is the DAY-ONE case, not an
// edge case: every jail that has ever run pi already holds a real, populated
// ~/.pi/agent/npm, and EnsureGlobalStorage has already created an empty
// ~/.pi-shared-npm on every machine from the ungated embedded list. So the branch that must
// work on the first launch after this ships is "a real populated directory where the symlink
// belongs, and an empty store" — the one the file-shaped helper cannot do.
//
// Both pre-migration shapes of the store are exercised, because they reach the branch by
// different tests inside one condition: ABSENT is a machine whose GlobalHome predates the
// declaration, and EMPTY-BUT-PRESENT is what EnsureGlobalStorage leaves — the common one, and
// the one a Size()-based emptiness test gets exactly backwards (a fresh directory Stats
// non-empty, so the rule would DISCARD the workspace's store).
func TestSharedDirectoryHookMigratesAPopulatedStoreIntoAnEmptyShared(t *testing.T) {
	for _, tc := range []struct {
		name       string
		seedShared bool
	}{
		{"shared absent", false},
		{"shared present but empty", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, hook := sharedDirHook(t, "pi")
			e, link, shared := sharedDirEnv(t, hook)
			if tc.seedShared {
				if err := os.MkdirAll(shared, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			seedStore(t, link, "from-the-workspace")

			runHooks(t, e, p)

			// THE ASSERTION: the store reached the machine tier. Read the SHARED paths
			// directly — reading through the link would pass even if the copy never
			// happened and the local tree had simply been left alone.
			got, err := os.ReadFile(filepath.Join(shared, "node_modules", "pi-lens", "index.js"))
			if err != nil {
				t.Fatalf("the workspace's store was not migrated: %v", err)
			}
			if !strings.Contains(string(got), "from-the-workspace") {
				t.Errorf("migrated file = %q, want the workspace's bytes", got)
			}
			// A symlink is copied AS a symlink. Dereferencing npm's .bin entries would
			// turn one store into a fan-out of copies that no later install updates.
			binLink := filepath.Join(shared, "node_modules", ".bin", "pi-lens")
			if target, err := os.Readlink(binLink); err != nil {
				t.Errorf("the .bin entry was dereferenced instead of copied as a symlink: %v", err)
			} else if target != "../pi-lens/index.js" {
				t.Errorf(".bin target = %q, want the relative link verbatim", target)
			}
			// The local path is now the symlink, and the mark is cleared.
			assertLinkedToShared(t, link, shared)
			if _, err := os.Lstat(filepath.Join(shared, sharedCopyIncomplete)); err == nil {
				t.Error("the in-progress mark survived a completed copy, so the store " +
					"still reads as empty and the next launch migrates over it again")
			}
			if log := sharedHookLog(t, e); !strings.Contains(log, "copied local directory into shared") {
				t.Errorf("the log does not record the migration:\n%s", log)
			}
		})
	}
}

// TestSharedDirectoryHookKeepsAPopulatedSharedStore is the other half of the rule, and
// without it the whole thing inverts on a one-line edit: when the store is already populated
// (another workspace migrated first, or an update ran) the SHARED side wins and the local
// tree is discarded.
//
// The discard is priced, and differently from the credential hook's: what is lost is a
// re-installable package tree, not a login. It is still recorded, because "my extensions
// changed version when I launched" needs a trace.
func TestSharedDirectoryHookKeepsAPopulatedSharedStore(t *testing.T) {
	p, hook := sharedDirHook(t, "pi")
	e, link, shared := sharedDirEnv(t, hook)
	seedStore(t, shared, "the-machine-store")
	seedStore(t, link, "this-workspaces-store")

	runHooks(t, e, p)

	got, err := os.ReadFile(filepath.Join(shared, "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "the-machine-store") {
		t.Errorf("the shared store was overwritten by the local one: %q", got)
	}
	assertLinkedToShared(t, link, shared)
	if log := sharedHookLog(t, e); !strings.Contains(log, "discarded local directory") {
		t.Errorf("the log does not record the discard:\n%s", log)
	}
}

// TestAFailedSharedDirectoryCopyLeavesTheLocalStoreAlone is the clause that cost credentials
// once, re-asserted for a payload that makes it harder: a tree copy can fail PART-WAY, where
// a file write cannot.
//
// Two provoked failures, both root-proof — this suite runs as root in the jail, so a
// permission-denied fixture proves nothing:
//
//   - the in-progress MARK cannot be written, so the copy never starts;
//   - a FIFO inside the local tree, so the copy begins, writes real entries, and then hits
//     something it cannot reproduce. That is the part-way case, and the assertion that
//     matters is the one about the NEXT launch: the store must still read as EMPTY, or the
//     partial tree becomes the "populated" side that wins and the local store is discarded
//     one launch later — a silent failure reported as a success, on a delay.
func TestAFailedSharedDirectoryCopyLeavesTheLocalStoreAlone(t *testing.T) {
	t.Run("the copy cannot be marked in progress", func(t *testing.T) {
		p, hook := sharedDirHook(t, "pi")
		e, link, shared := sharedDirEnv(t, hook)
		seedStore(t, link, "the-only-copy")
		// A DIRECTORY where the mark's file belongs: WriteFile fails EISDIR, for root as
		// much as for anyone. The store still reads as empty (the mark is present), which
		// is what a marker-blind rewrite would get wrong in the other direction.
		if err := os.MkdirAll(filepath.Join(shared, sharedCopyIncomplete), 0o755); err != nil {
			t.Fatal(err)
		}

		RunPackHooks(e, []*packload.Pack{p})

		assertStoreSurvived(t, e, link, "the-only-copy")
	})

	t.Run("an entry the copy cannot reproduce", func(t *testing.T) {
		p, hook := sharedDirHook(t, "pi")
		e, link, shared := sharedDirEnv(t, hook)
		seedStore(t, link, "the-only-copy")
		fifo := filepath.Join(link, "node_modules", "odd.sock")
		if err := syscall.Mkfifo(fifo, 0o600); err != nil {
			t.Skipf("cannot create a fifo here: %v", err)
		}

		RunPackHooks(e, []*packload.Pack{p})

		assertStoreSurvived(t, e, link, "the-only-copy")
		// THE PART-WAY GUARANTEE, and the mechanism changed on 2026-09-21 while the
		// guarantee did not. This used to assert that the partial tree REMAINS and that
		// sharedTreeIsEmpty distrusts it because the marker is still there. A failed copy
		// now ROLLS BACK what it wrote and releases the marker, which is available to a
		// returned error and not to a crash — so the store is genuinely empty rather than
		// dirty-but-flagged, and the retry is immediate instead of waiting out
		// sharedCopyStaleAfter.
		//
		// What must hold either way is the only thing that matters: the next launch must
		// not read this store as populated and discard the workspace's only copy.
		if !sharedTreeIsEmpty(shared) {
			t.Fatal("after a failed copy the store must read EMPTY, or the next launch " +
				"discards the workspace's store in favour of a partial tree")
		}
		if _, err := os.Lstat(filepath.Join(shared, "node_modules")); err == nil {
			t.Error("the rollback left part of the tree behind: a machine-wide store must not " +
				"keep the debris of a failed migration")
		}
		if _, err := os.Lstat(filepath.Join(shared, sharedCopyIncomplete)); err == nil {
			t.Error("the marker outlived a RETURNED error, which blocks the retry for " +
				"sharedCopyStaleAfter — only a crash may leave it behind")
		}
		// And the retry works once the blocker is gone — the migration is not wedged.
		if err := os.Remove(fifo); err != nil {
			t.Fatal(err)
		}
		e2 := &Env{Home: e.Home, Workspace: e.Workspace, Vars: map[string]string{}}
		runHooks(t, e2, p)
		assertLinkedToShared(t, link, shared)
		got, err := os.ReadFile(filepath.Join(shared, "package.json"))
		if err != nil {
			t.Fatalf("the retry did not migrate the store: %v", err)
		}
		if !strings.Contains(string(got), "the-only-copy") {
			t.Errorf("migrated package.json = %q, want the workspace's bytes", got)
		}
	})
}

// assertStoreSurvived checks the three halves that matter together, because each alone is
// passable while the defect is live: the bytes are still at the local path, the local path is
// still a real directory rather than a symlink to a store that was never written, and the
// decision says the copy did not happen.
func assertStoreSurvived(t *testing.T, e *Env, link, marker string) {
	t.Helper()
	if fi, err := os.Lstat(link); err != nil {
		t.Fatalf("the workspace's store was DESTROYED after the copy failed: %v", err)
	} else if fi.Mode()&os.ModeSymlink != 0 {
		t.Fatal("the local store was replaced by a symlink to a store that was never " +
			"written — the extensions are gone and the agent reads an empty directory")
	}
	got, err := os.ReadFile(filepath.Join(link, "package.json"))
	if err != nil {
		t.Fatalf("the local store's package.json is gone: %v", err)
	}
	if !strings.Contains(string(got), marker) {
		t.Errorf("the local store was rewritten: %q", got)
	}
	log := sharedHookLog(t, e)
	if strings.Contains(log, "copied local directory into shared") {
		t.Errorf("the log reports a copy that did not happen:\n%s", log)
	}
	if !strings.Contains(log, "left in place") {
		t.Errorf("the log must say the local store was left in place:\n%s", log)
	}
	// A failed migration is a FATAL boot problem in neither direction: the jail comes up on
	// its own per-workspace store, exactly as it did before the declaration existed.
	if fails := e.GenFailures(); len(fails) != 0 {
		t.Errorf("a failed migration must degrade, not fail the boot: %v", fails)
	}
}

// TestAnUnusableSharedStoreIsAReportedBootFailure marks the boundary between the two
// dispositions, because "a failed copy degrades" is not the same claim as "nothing about this
// hook can fail a boot" and conflating them would be the wrong lesson from the test above.
//
// A store path that cannot be a DIRECTORY at all is not a failed migration, it is a machine
// tier that does not exist: the hook's own MkdirAll of the pack's declared shared dir fails
// before the rule runs, which is fatal through genStep (A12) — identically to the credential
// hook, whose prologue is the same code. What must still hold is the part the data depends
// on: the local store is not touched, so the remedy is to fix the path and launch again.
func TestAnUnusableSharedStoreIsAReportedBootFailure(t *testing.T) {
	p, hook := sharedDirHook(t, "pi")
	e, link, shared := sharedDirEnv(t, hook)
	seedStore(t, link, "the-only-copy")
	// A regular file where the declared machine-scope directory belongs: MkdirAll fails
	// ENOTDIR.
	if err := os.MkdirAll(filepath.Dir(shared), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(shared, []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}

	RunPackHooks(e, []*packload.Pack{p})

	fails := e.GenFailures()
	if len(fails) == 0 {
		t.Fatal("an unusable machine tier was not reported at all")
	}
	if !strings.Contains(strings.Join(fails, "\n"), "hook_pi_"+HookSharedDirectory) {
		t.Errorf("the failure does not name the hook that failed: %v", fails)
	}
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("the local store was touched by a hook that could not reach the shared "+
			"tier at all (err=%v)", err)
	}
	if _, err := os.ReadFile(filepath.Join(link, "package.json")); err != nil {
		t.Errorf("the local store's bytes are gone: %v", err)
	}
}

// TestDarwinBootstrapResolvesASharedStoreTwoLevelsDeep is the macos-user half, and it is the
// one class a container-only test cannot see: that backend has NO MOUNTS, so the whole
// mechanism rests on the relative link resolving through a symlinked state dir into the
// sidecar MIRROR of the machine tier. It drives the real boot entry (RunDarwinBootstrap) with
// the real pi manifest staged, and reads the store's bytes THROUGH the layout.
//
// Depth is the reason it exists beside the credential twin. The claude case links one level
// down (`.claude/.credentials.json`, target `../.claude-shared-credentials/…`); this one links
// TWO (`.pi/agent/npm`, target `../../.pi-shared-npm`), and because the kernel resolves `..`
// PHYSICALLY the walk from inside the sidecar has to land on the mirror at the SIDECAR ROOT
// whatever the depth. Delete the mirror loop, or the layout step, and this fails.
//
// It runs on Linux for the same reason its neighbour does: the backend itself is unrunnable
// in CI, and the `..` resolution it rests on is physical on both kernels.
func TestDarwinBootstrapResolvesASharedStoreTwoLevelsDeep(t *testing.T) {
	home, ws := darwinBootstrapHome(t, map[string]string{
		"YOLO_PACK_ROOT": stagePackForBootstrap(t, "pi"),
	})
	sidecar := filepath.Join(ws, ".yolo", "home")

	// The workspace tier: ~/.pi is a symlink into the sidecar, which is what makes the
	// physical `..` resolution a problem in the first place.
	if target, err := os.Readlink(filepath.Join(home, ".pi")); err != nil {
		t.Fatalf("~/.pi is not a symlink into the workspace sidecar: %v", err)
	} else if want := filepath.Join(sidecar, "pi"); target != want {
		t.Fatalf("~/.pi -> %s, want %s", target, want)
	}

	// The machine tier's real bytes, written AFTER the boot so only that path holds them.
	if err := os.WriteFile(filepath.Join(home, ".pi-shared-npm", "package.json"),
		[]byte(`{"name":"pi-extensions","from":"the-machine-tier"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(home, ".pi", "agent", "npm", "package.json"))
	if err != nil {
		t.Fatalf("the shared store does not resolve through the workspace tier: %v\n"+
			"The hook's symlink is relative BY DESIGN and `..` resolves physically, so from "+
			"two levels inside %s it needs the SharedDirs mirror at that sidecar's root.",
			err, sidecar)
	}
	if !strings.Contains(string(got), "the-machine-tier") {
		t.Errorf("read %q through the layout, want the machine tier's own bytes", got)
	}

	// And the mirror is a symlink OUT of the sidecar, not a copy: a copy would be a second
	// package store, which is the drift the machine tier exists to end.
	mirror, err := os.Readlink(filepath.Join(sidecar, ".pi-shared-npm"))
	if err != nil || mirror != filepath.Join(home, ".pi-shared-npm") {
		t.Errorf("the sidecar mirror is %q (err %v), want a symlink to the account home's dir",
			mirror, err)
	}
}

// TestTheSharedStoreEmptinessRuleIsTheDirectoryOne pins the predicate that makes a directory
// hook a different mechanism rather than the same one with a wider type. Three states, and
// each one is a way of getting it wrong:
//
//   - a FRESH store is EMPTY. The file rule's `Size() == 0` cannot be trusted to say so,
//     because a directory's size is filesystem-defined: MEASURED on this btrfs, an empty
//     directory reports 0 and a populated one a small entry count, so the file rule happens
//     to agree — but on a filesystem whose directory size is a block allocation rather than
//     an entry count, an EMPTY store reads as populated and the rule discards the
//     workspace's tree. An accident of the filesystem is not a rule.
//   - a store holding anything is POPULATED, or the shared side never wins.
//   - a store holding ONLY the in-progress mark is EMPTY. This is the one a naive
//     `len(entries) == 0` gets wrong on every filesystem, and getting it wrong is what turns
//     an interrupted copy into a discarded store one launch later.
func TestTheSharedStoreEmptinessRuleIsTheDirectoryOne(t *testing.T) {
	store := filepath.Join(t.TempDir(), "store")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	if !sharedTreeNode.sharedEmpty(store) {
		t.Error("a freshly created store must read as EMPTY, or the first migration " +
			"discards the workspace's tree")
	}

	if err := os.WriteFile(filepath.Join(store, "package.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if sharedTreeNode.sharedEmpty(store) {
		t.Error("a store holding a file must read as POPULATED, or the shared side never wins")
	}

	marked := filepath.Join(t.TempDir(), "marked")
	if err := os.MkdirAll(marked, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(marked, sharedCopyIncomplete), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if !sharedTreeNode.sharedEmpty(marked) {
		t.Error("a store holding only the in-progress mark must read as EMPTY, or an " +
			"interrupted copy becomes the populated side that wins")
	}
	// And the mark does not make a REAL store read as empty — that would make every launch
	// re-migrate over a complete store.
	if err := os.WriteFile(filepath.Join(store, sharedCopyIncomplete), nil, 0o644); err == nil {
		if !sharedTreeNode.sharedEmpty(store) {
			t.Error("the mark must dominate: a store carrying it is a copy in progress " +
				"whatever else is in it")
		}
	} else {
		t.Fatal(err)
	}
}

// TestAConcurrentMigrationDeclinesInsteadOfInterleaving is the race the marker used to
// CAUSE rather than prevent.
//
// As a plain file the marker made two launches worse than none: sharedTreeIsEmpty reports a
// marked store as empty on purpose — a half-copied store is not trustworthy — so a second
// launch arriving mid-copy read "empty", copied its own tree in on top of the first, and
// then removed its local copy. Two interleaved trees in the machine-wide store, both local
// copies gone, both launches logging success.
//
// os.Mkdir is atomic and fails EEXIST, so the marker IS the lock and the loser gets an
// error — which is all it needs, because the caller keeps the local payload and does not
// symlink when copyIn fails.
func TestAConcurrentMigrationDeclinesInsteadOfInterleaving(t *testing.T) {
	root := t.TempDir()
	local := filepath.Join(root, "local")
	shared := filepath.Join(root, "shared")
	if err := os.MkdirAll(local, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(local, "mine.txt"), []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Another launch is already inside the copy.
	if err := os.MkdirAll(filepath.Join(shared, sharedCopyIncomplete), 0o755); err != nil {
		t.Fatal(err)
	}

	err := copyTreeIntoShared(local, shared)
	if err == nil {
		t.Fatal("copyTreeIntoShared SUCCEEDED while another launch held the marker — that is " +
			"two writers in one machine-wide store, and the caller will now delete the only " +
			"local copy")
	}
	if _, statErr := os.Stat(filepath.Join(shared, "mine.txt")); statErr == nil {
		t.Error("it copied anyway: the loser must not write into a store another launch is filling")
	}
	// The local tree is what the caller keeps, so it must still be there.
	if _, statErr := os.Stat(filepath.Join(local, "mine.txt")); statErr != nil {
		t.Errorf("the local tree must be untouched by a declined migration: %v", statErr)
	}
}

// TestAStaleMarkerIsTakenOver is the other half of the age rule: a marker with no live owner
// must not strand the store forever. Without it, one crashed copy means this directory is
// never migrated again on this machine.
func TestAStaleMarkerIsTakenOver(t *testing.T) {
	root := t.TempDir()
	local := filepath.Join(root, "local")
	shared := filepath.Join(root, "shared")
	if err := os.MkdirAll(local, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(local, "mine.txt"), []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(shared, sharedCopyIncomplete)
	if err := os.MkdirAll(marker, 0o755); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * sharedCopyStaleAfter)
	if err := os.Chtimes(marker, old, old); err != nil {
		t.Fatal(err)
	}

	if err := copyTreeIntoShared(local, shared); err != nil {
		t.Fatalf("a marker older than %s is a crashed copy, not a live one, and must be taken "+
			"over rather than stranding the store: %v", sharedCopyStaleAfter, err)
	}
	if _, err := os.Stat(filepath.Join(shared, "mine.txt")); err != nil {
		t.Errorf("the takeover must actually copy: %v", err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("a successful copy must clear the marker, or the next launch distrusts the store")
	}
}
