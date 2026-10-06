package run

// buildslot_test.go pins the fork-build slot as one act (buildslot.go;
// docs/design/pi-extension-store-builds.md XB-D10, XB-D30): a fresh launch hands every key of both
// halves to BuildSlot in one call, never to the two halves' acts, with the pool's bounds; the image's
// own build starts beside it; Apple Container builds one key at a time; the image identity is
// evaluated once; and the delivery record loses no hand that the pool's advances make at once.

import (
	"fmt"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/image"
)

// slotLaunchHome selects a patched fork of an agent's program and a patched extension the agent's
// settings list names, so a launch's slot has a key in each half.
func slotLaunchHome(t *testing.T) {
	t.Helper()
	home := packHome(t)
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("YOLO_PACK_ROOT", "")
	packs := t.TempDir()
	write := func(rel, body string) {
		p := filepath.Join(packs, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	patch := "From " + strings.Repeat("a", 40) + " Mon Sep 17 00:00:00 2001\nFrom: t <t@e>\nSubject: [PATCH] x\n\n---\n" +
		"diff --git a/f.txt b/f.txt\n--- a/f.txt\n+++ b/f.txt\n@@ -1 +1 @@\n-1\n+x\n-- \nbase-commit: " + patchedBase + "\n"
	write("agentpack/pack.json", `{"contributes":[{"kind":"program","bin":"tool","via":"npm","package":"tool"},
		{"kind":"config","config":[{"agent":"tool","name":"settings","codec":"json","path":"~/.tool/settings.json"}]}]}`)
	write("forkpack/pack.json", `{"contributes":[{"kind":"program","bin":"tool","via":"source","fork_of":"agentpack",`+
		`"source":"`+patchedSource+`","patches":"patches","build":"make install","produces":[".local/bin/tool"]}]}`)
	write("forkpack/patches/0001-x.patch", patch)
	write("treepack/pack.json", `{"contributes":[{"kind":"files","into":"`+treeInto+`",
		"source":"git+https://example.invalid/tree-ext?ref=main","patches":"patches"},
		{"kind":"config-list","surface":"tool/settings","path":"/packages","add":["~/`+treeInto+`"]}]}`)
	write("treepack/patches/0001-x.patch", patch)
	writeUserPacks(t, home, `[{"source":"file://`+filepath.Join(packs, "agentpack")+`","name":"agentpack"},`+
		`{"source":"file://`+filepath.Join(packs, "forkpack")+`","name":"forkpack"},`+
		`{"source":"file://`+filepath.Join(packs, "treepack")+`","name":"treepack"}]`)
}

// THE SLOT IS ONE ACT: a fresh launch hands its patched fork and its patched extension to BuildSlot
// in one call, with the pool's bounds, and calls neither half's own act; and the image's own build
// starts while the slot runs, with the image step's own request. Red with Run's runForkBuildSlot
// call back to the two halves, or startImagePrewarm's call deleted.
func TestTheSlotIsOneActAndTheImageBuildStartsBesideIt(t *testing.T) {
	slotLaunchHome(t)
	var req BuildSlotRequest
	calls := 0
	prewarmed := make(chan image.AutoLoadOptions, 1)
	_, printed := fakePodmanLaunch(t, func(o *Options) {
		o.BuildForks = func(ForkBuildRequest) map[string]entrypoint.ForkDelivery {
			t.Error("the launch called the fork builds' own act, not the slot's")
			return nil
		}
		o.BuildTrees = func(TreeBuildRequest) map[string]TreeDelivery {
			t.Error("the launch called the tree arm's own act, not the slot's")
			return nil
		}
		o.prewarmImage = func(opts image.AutoLoadOptions) { prewarmed <- opts }
		o.BuildSlot = func(r BuildSlotRequest) (map[string]entrypoint.ForkDelivery, map[string]TreeDelivery) {
			req, calls = r, calls+1
			select {
			case opts := <-prewarmed:
				if opts.RepoRoot == "" || opts.Runtime != "podman" {
					t.Errorf("the image build beside the slot asked for %+v", opts)
				}
			case <-time.After(10 * time.Second):
				t.Error("the image's own build did not start while the slot ran")
			}
			return map[string]entrypoint.ForkDelivery{"tool": {Reason: "test"}},
				map[string]TreeDelivery{treeKey: {Reason: "test"}}
		}
	})
	if calls != 1 || req.Forks == nil || len(req.Forks.Pins) != 1 || req.Forks.Pins[0].Fork.Key() != "forkpack/tool" ||
		req.Trees == nil || len(req.Trees.Trees) != 1 || req.Trees.Trees[0].Key() != treeKey {
		t.Fatalf("the slot was called %d times with %+v\n%s", calls, req, printed)
	}
	if req.MaxChecks != SlotChecks || req.MaxBuilds != SlotBuildJails("podman") {
		t.Errorf("the slot's bounds are %d checks and %d builds, want %d and %d", req.MaxChecks, req.MaxBuilds,
			SlotChecks, SlotBuildJails("podman"))
	}
	if req.Forks.Interrupt == nil || req.Trees.Interrupt != req.Forks.Interrupt {
		t.Error("the slot's two halves do not share the launch's act interrupt")
	}
}

// THE POOL'S BUILD BOUND (XB-D10): min(4, max(1, CPUs/2)), and one on Apple Container, where a
// second build jail cannot start beside the first.
func TestTheSlotBuildsAtMostFourAtOnceAndOneOnAppleContainer(t *testing.T) {
	if got := SlotBuildJails("container"); got != 1 {
		t.Errorf("Apple Container's slot builds %d at once, want 1", got)
	}
	if got, want := SlotBuildJails("podman"), min(4, max(1, goruntime.NumCPU()/2)); got != want {
		t.Errorf("podman's slot builds %d at once, want %d", got, want)
	}
}

// THE IDENTITY IS EVALUATED ONCE (imageprewarm.go): the image step asks the eval the prewarm runs,
// waiting for it, and an eval that failed is asked again.
func TestTheImageIdentityIsEvaluatedOnce(t *testing.T) {
	var mu sync.Mutex
	evals := 0
	release := make(chan struct{})
	fail := false
	prev := identityEval
	identityEval = func(string) (string, bool) {
		mu.Lock()
		evals++
		f := fail
		mu.Unlock()
		<-release
		if f {
			return "", false
		}
		return "sha256:x", true
	}
	t.Cleanup(func() { identityEval = prev })
	m := &identityMemo{}
	var wg sync.WaitGroup
	got := make([]string, 3)
	for i := range got {
		wg.Add(1)
		go func() { defer wg.Done(); got[i], _ = m.eval("/repo") }()
	}
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()
	if evals != 1 || got[0] != "sha256:x" || got[1] != got[0] || got[2] != got[0] {
		t.Errorf("three asks made %d evals and got %q, want one eval's answer", evals, got)
	}
	failed := &identityMemo{}
	fail = true
	if _, ok := failed.eval("/repo"); ok {
		t.Fatal("a failed eval answered")
	}
	fail = false
	if id, ok := failed.eval("/repo"); !ok || id != "sha256:x" || evals != 3 {
		t.Errorf("after a failed eval the next ask got %q (ok %v) after %d evals, want a fresh eval", id, ok, evals)
	}
}

// THE LAUNCH'S PREWARM AND ITS IMAGE STEP SHARE ONE IDENTITY EVAL (XB-D30): a fresh launch with a
// slot asks the identity from both, and the eval runs once. The memo's own behavior is pinned above;
// this pins its call sites. Red with imageLoadOptions' EvalIdentity no longer the launch's memo, or
// startImagePrewarm no longer making one.
func TestALaunchsPrewarmAndImageStepShareOneIdentityEval(t *testing.T) {
	slotLaunchHome(t)
	var mu sync.Mutex
	evals := 0
	prev := identityEval
	identityEval = func(string) (string, bool) {
		mu.Lock()
		defer mu.Unlock()
		evals++
		return "sha256:x", true
	}
	t.Cleanup(func() { identityEval = prev })
	ask := func(who string, opts image.AutoLoadOptions) {
		if opts.EvalIdentity == nil {
			t.Errorf("the %s's request carries no identity eval of the launch's", who)
			return
		}
		if id, ok := opts.EvalIdentity(opts.RepoRoot); !ok || id != "sha256:x" {
			t.Errorf("the %s's identity eval answered %q (ok %v)", who, id, ok)
		}
	}
	prewarmed := make(chan struct{})
	images := 0
	_, printed := fakePodmanLaunch(t, func(o *Options) {
		// The slot hands nothing: the refusal of a missing build (missingbuilds.go) would end the
		// launch before the image step this test counts.
		allowMissingPrograms(o)
		o.prewarmImage = func(opts image.AutoLoadOptions) {
			defer close(prewarmed)
			ask("prewarm", opts)
		}
		o.autoLoad = func(opts image.AutoLoadOptions) image.LoadResult {
			images++
			select {
			case <-prewarmed:
			case <-time.After(10 * time.Second):
				t.Error("the prewarm did not run")
			}
			ask("image step", opts)
			return image.LoadResult{OK: true, Ref: goldenImageRef}
		}
		o.BuildSlot = func(BuildSlotRequest) (map[string]entrypoint.ForkDelivery, map[string]TreeDelivery) {
			return map[string]entrypoint.ForkDelivery{"tool": {Reason: "test"}},
				map[string]TreeDelivery{treeKey: {Reason: "test"}}
		}
	})
	if images != 1 || evals != 1 {
		t.Errorf("the image step ran %d times and the identity was evaluated %d times, want once each\n%s", images,
			evals, printed)
	}
}

// NO HAND IS LOST (forkhanded.go's handedFileMu): the pool's advances record what each hands its
// jail at once, each a read-modify-write of the one record, and every one is kept. Red without the
// mutex: the hook between the read and the write lets every writer read the record before any
// writes it.
func TestTheDeliveryRecordKeepsEveryHandThePoolMakesAtOnce(t *testing.T) {
	tree := filepath.Join(t.TempDir(), "tree")
	prev := handedFileRead
	handedFileRead = func() { time.Sleep(20 * time.Millisecond) }
	t.Cleanup(func() { handedFileRead = prev })
	const n = 8
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := recordHandedFork(tree, fmt.Sprintf("bin%d", i), HandedFork{Key: fmt.Sprintf("k%d", i)}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	got, err := readHandedForks(tree)
	if err != nil || len(got) != n {
		t.Errorf("the record holds %d of %d hands (%v): %+v", len(got), n, err, got)
	}
}
