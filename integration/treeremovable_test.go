package integration

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// makeTreeRemovable restores the write bit on every DIRECTORY under root so that
// t.TempDir()'s own RemoveAll cleanup can unlink what a vendor installer left
// read-only.
//
// RemoveAll needs write AND execute on a DIRECTORY to unlink the children inside
// it, and Go's does not chmod on the way down. So a single 0555 directory
// anywhere in a fixture tree fails the whole cleanup — and t.TempDir() reports
// that as a TEST FAILURE, raised after the test's own assertions have already
// passed, which is about as misleading as a red badge gets.
//
// A pack's vendor installer writes into the workspace's home overlay
// (<workspace>/.yolo/home, paths.WorkspaceHomeState), which for these fixtures
// is a t.TempDir() — so whatever modes it leaves behind become this suite's
// cleanup problem. The Pack Installs job went down twice that way (runs
// 33938946012 and 33972100139).
//
// Only writeProject's WORKSPACE needs this. An isolated HOME does not: packHome
// symlinks the HOME-rooted stores back to the machine's copies, so installed
// content never lands inside that temp dir in the first place
// (packHomeSharedStores, and TestPackHomeSharesHostStores which pins it).
//
// ⚠ THIS IS ONE OF TWO CLASSES AND NOT THE ONE THAT WAS ACTUALLY FAILING. A
// path this cannot chmod — one owned by another uid, which a rootless-podman
// userns does produce — needs the userns escalation in removeWorkspaceTree,
// which is the caller. Shipping this helper alone left the job red; see
// removeWorkspaceTree for the measurement and why chmod could never have
// reached it. Do not call this on its own.
func makeTreeRemovable(root string) {
	// WalkDir is pre-order, so a directory is chmod'd BEFORE its contents are
	// read — which is what lets the walk descend into one that had no execute
	// bit when it started.
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			return nil
		}
		if perm := fi.Mode().Perm(); perm&0o300 != 0o300 {
			_ = os.Chmod(path, perm|0o300)
		}
		return nil
	})
}

// TestMakeTreeRemovableClearsAReadOnlyDir runs under -short (no container): it is
// a harness invariant, like TestChildRepoRootEnv and TestPackHomeSharesHostStores,
// so `just test-fast` and the check-go CI job enforce it.
func TestMakeTreeRemovableClearsAReadOnlyDir(t *testing.T) {
	root := t.TempDir()

	// The exact shape codex ships: a file inside a directory with no write bit.
	locked := filepath.Join(root, "codex-path")
	if err := os.MkdirAll(locked, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(locked, "rg"), []byte("not really ripgrep"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0o555); err != nil {
		t.Fatal(err)
	}

	// Prove the failure is REAL before proving the fix. Without this the test
	// would pass just as happily on a system where RemoveAll never minded, and
	// would then be pinning nothing at all.
	if err := os.RemoveAll(root); err == nil {
		_ = os.Chmod(locked, 0o755)
		t.Skip("this filesystem lets RemoveAll unlink through a read-only directory " +
			"(running as root?), so there is no failure here to fix")
	}

	makeTreeRemovable(root)

	if err := os.RemoveAll(root); err != nil {
		t.Fatalf("after makeTreeRemovable, RemoveAll still could not clear the tree: %v", err)
	}
}

// TestWriteProjectRemovesItsTree is the call-site pin. The tests above prove the
// two removal steps work; nothing in them would notice the call vanishing from
// writeProject, and the regression that follows is a cleanup failure in one
// scheduled workflow — visible eventually, but only the next time a vendor ships
// a tree the test user cannot unlink. Reading the source is this repo's existing
// answer for a call site a unit test cannot reach (the methodDecl pattern in
// configapproval_test.go; liveoverlayguard_test.go's plain-function form, which
// this mirrors).
//
// It pins removeWorkspaceTree, NOT makeTreeRemovable: the escalation is the half
// that fixes the failure this suite actually had, and a pin on the inner helper
// would stay green with the escalation deleted — which is this repo's named
// anti-pattern (AGENTS.md, "a test that pins the CALLEE while the CALL SITE is
// unpinned is not a test"). removeWorkspaceTree calls makeTreeRemovable itself,
// so pinning the outer one pins both.
func TestWriteProjectRemovesItsTree(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "harness_test.go", nil, 0)
	if err != nil {
		t.Fatalf("parse harness_test.go: %v", err)
	}
	var found bool
	for _, decl := range f.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Name.Name != "writeProject" {
			continue
		}
		ast.Inspect(fd, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if id, ok := call.Fun.(*ast.Ident); ok && id.Name == "removeWorkspaceTree" {
				found = true
			}
			return true
		})
	}
	if !found {
		t.Fatal("writeProject no longer calls removeWorkspaceTree — a pack whose vendor " +
			"leaves a tree this user cannot unlink will fail t.TempDir()'s cleanup again, " +
			"AFTER the test's assertions have passed (see removeWorkspaceTree for the runs " +
			"it took down). If the call moved, move this pin with it rather than " +
			"deleting it.")
	}
}

// removeWorkspaceTree removes a test workspace whose contents a jailed installer
// wrote, escalating until something works, so that t.TempDir()'s own RemoveAll
// finds nothing left to trip on.
//
// THREE STEPS, EACH FOR A DIFFERENT OWNER OF THE PROBLEM:
//
//  1. makeTreeRemovable, for a tree whose DIRECTORY MODES deny the unlink.
//  2. os.RemoveAll, which is the whole job whenever the files are ours.
//  3. `<runtime> unshare rm -rf`, for a tree whose files are not ours AT ALL —
//     the case step 1 cannot reach, because chmod on someone else's file is
//     EPERM no matter what mode you ask for.
//
// STEP 3 IS THE ONE THAT MATTERS, and it is worth stating what produces the
// ownership it answers. The jail runs as root, so a vendor installer's
// `tar -xzf` restores the ARCHIVE's uid/gid (GNU tar's --same-owner is the
// default for root). codex's standalone release stores every entry as 1001/1001
// — measured on codex-package-x86_64-unknown-linux-musl 0.153.4, whose
// `codex-path/` is mode 0755 and owner 1001 — so extracting it in the jail
// creates a host-side tree owned by whatever ROOTLESS PODMAN maps container uid
// 1001 to: a subuid (100000+ on a GitHub runner) that the test user owns no more
// than any other stranger's. It cannot chmod it and it cannot unlink through it.
// `podman unshare` enters the very user namespace those subuids are mapped in,
// where they are ordinary owned files, and rm succeeds.
//
// This is why makeTreeRemovable alone did not fix the Pack Installs job. That
// helper read the failure as read-only MODES and shipped on 2026-09-06
// (1200c9c7); the next run of the job (34134458395, four jobs red) failed
// identically, because 0755-owned-by-1001 was never a mode problem. Its own doc
// comment had named this as "a different failure with a different fix" — this is
// that fix, and the two compose: step 1 still handles the mode class on a
// runtime with no `unshare` at all.
//
// A residual failure is LOGGED rather than swallowed, and then left to
// t.TempDir()'s cleanup to report as it always has: a workspace nothing can
// remove is a real fact about the machine, and the log names both attempts so
// the next reader does not have to re-derive which one was supposed to work.
func removeWorkspaceTree(t *testing.T, dir string) {
	t.Helper()
	makeTreeRemovable(dir)
	if err := removeAllFn(dir); err == nil {
		return
	}
	// Re-read the error AFTER the escalation rather than reporting the first
	// one: what a reader needs is what is STILL wrong, and a plain RemoveAll
	// failure is only interesting when the userns could not fix it either.
	unshareErr := unshareRemoveAll(detectRuntime(), dir)
	err := removeAllFn(dir)
	if err == nil {
		return
	}
	t.Logf("could not remove the test workspace %s: RemoveAll says %v; "+
		"the userns escalation says %v. t.TempDir()'s own cleanup will report "+
		"this next — see removeWorkspaceTree for what each step is for.",
		dir, err, unshareErr)
}

// The two seams these tests replace. Package-level vars rather than parameters:
// every caller wants the real thing, and threading two funcs through
// writeProject's cleanup would put test-only arguments in the signature every
// integration test reads. They exist because NEITHER failure this helper handles
// can be manufactured in the unit suite — a read-only directory is removable by
// the root user this repo's own jail runs as, and a foreign-owned one cannot be
// CREATED by the unprivileged CI user that could then fail to remove it. Without
// the seams the escalation would be call-site-pinned and never executed, which
// is the shape AGENTS.md names as "not a test".
var (
	removeAllFn        = os.RemoveAll
	unshareRemoveAllFn = execUnshareRemoveAll
)

// unshareRemoveAll runs `rm -rf dir` inside rt's user namespace. It returns a
// non-nil error when it could not be attempted at all, and the caller reports
// that verbatim: "no container runtime on PATH" is a different diagnosis from
// "rm ran and failed", and collapsing the two is how a cleanup bug hides.
func unshareRemoveAll(rt, dir string) error {
	if rt == "" {
		return errors.New("no container runtime detected, so no user namespace to enter")
	}
	// Apple Container has no `unshare` subcommand, and on macOS a jail's writes
	// come back through virtiofs owned by the invoking user anyway — there is no
	// subuid to escalate into, so there is nothing here to attempt.
	if rt != "podman" {
		return fmt.Errorf("runtime %q has no `unshare` subcommand", rt)
	}
	return unshareRemoveAllFn(rt, dir)
}

// execUnshareRemoveAll is unshareRemoveAll's real work, split out so a test can
// replace it without also replacing the runtime gate above.
//
// `podman unshare` is a no-op on a ROOTFUL podman and says so rather than
// silently doing nothing, which is why its output is carried into the error: on
// yolo's own jail (root podman) the escalation cannot apply, and there it is
// also never needed — root already unlinks anything.
func execUnshareRemoveAll(rt, dir string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, rt, "unshare", "rm", "-rf", dir).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s unshare rm -rf: %w (%s)", rt, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// TestUnshareRemoveAllGatesOnTheRuntime pins the dispatch: only podman gets an
// exec, and the two refusals say which fact stopped them. Both non-podman arms
// would otherwise be indistinguishable from a silent success, and a cleanup that
// silently does nothing is exactly the bug that shipped once already.
func TestUnshareRemoveAllGatesOnTheRuntime(t *testing.T) {
	var calls [][2]string
	restore := unshareRemoveAllFn
	unshareRemoveAllFn = func(rt, dir string) error {
		calls = append(calls, [2]string{rt, dir})
		return nil
	}
	t.Cleanup(func() { unshareRemoveAllFn = restore })

	if err := unshareRemoveAll("", "/tmp/x"); err == nil {
		t.Error("no runtime must refuse rather than report success")
	}
	if err := unshareRemoveAll("container", "/tmp/x"); err == nil {
		t.Error("Apple Container has no `unshare`, so it must refuse rather than report success")
	}
	if len(calls) != 0 {
		t.Fatalf("a non-podman runtime reached the exec: %v", calls)
	}
	if err := unshareRemoveAll("podman", "/tmp/x"); err != nil {
		t.Errorf("podman must be attempted: %v", err)
	}
	if want := [][2]string{{"podman", "/tmp/x"}}; !reflect.DeepEqual(calls, want) {
		t.Errorf("escalation argv: got %v, want %v", calls, want)
	}
}

// TestRemoveWorkspaceTreeEscalatesWhenRemoveAllFails is the test that fails if
// the escalation is deleted from removeWorkspaceTree. The first RemoveAll fails
// the way a foreign-owned tree does; the escalation is what has to run next, and
// the SECOND RemoveAll is what turns it into a removed workspace.
//
// The stubs are the only way to reach this path (see the seams above for why
// neither real failure can be manufactured here), so what this pins is the
// SEQUENCE — chmod, remove, escalate, remove — and not the syscall behaviour of
// any one step.
func TestRemoveWorkspaceTreeEscalatesWhenRemoveAllFails(t *testing.T) {
	dir := t.TempDir()

	var order []string
	restoreRemove, restoreUnshare := removeAllFn, unshareRemoveAllFn
	t.Cleanup(func() { removeAllFn, unshareRemoveAllFn = restoreRemove, restoreUnshare })

	escalated := false
	removeAllFn = func(path string) error {
		order = append(order, "remove")
		if path != dir {
			t.Errorf("removal targeted %q, want the workspace %q", path, dir)
		}
		if !escalated {
			return errors.New("permission denied (a subuid owns this tree)")
		}
		return nil
	}
	unshareRemoveAllFn = func(rt, path string) error {
		order = append(order, "escalate")
		if path != dir {
			t.Errorf("escalation targeted %q, want the workspace %q", path, dir)
		}
		escalated = true
		return nil
	}

	removeWorkspaceTree(t, dir)

	want := []string{"remove", "escalate", "remove"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("removeWorkspaceTree did %v, want %v — the escalation between the two "+
			"removals is the half that fixes a tree owned by a rootless-podman subuid, "+
			"and a first RemoveAll that just gives up leaves the job red", order, want)
	}
}

// TestRemoveWorkspaceTreeStopsAtTheFirstSuccess pins the other half of the
// sequence: the escalation costs a process, so a workspace the plain RemoveAll
// already cleared — every test on every green run — must not pay for it.
func TestRemoveWorkspaceTreeStopsAtTheFirstSuccess(t *testing.T) {
	dir := t.TempDir()

	restoreUnshare := unshareRemoveAllFn
	t.Cleanup(func() { unshareRemoveAllFn = restoreUnshare })
	unshareRemoveAllFn = func(rt, path string) error {
		t.Errorf("escalated for a workspace os.RemoveAll had already removed (%s)", path)
		return nil
	}

	removeWorkspaceTree(t, dir)

	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("workspace still present after removeWorkspaceTree: %v", err)
	}
}
