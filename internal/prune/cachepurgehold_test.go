package prune

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// agedTree writes each rel (slash-separated, under root) as a small file dated 40 days back —
// the fixture docs/design/cache-isolation-plan.md (V6) ran, in which the age purge deleted
// uv/.lock and huggingface/token.
func agedTree(t *testing.T, root string, rels ...string) {
	t.Helper()
	old := time.Now().Add(-40 * 24 * time.Hour)
	for _, rel := range rels {
		p := filepath.Join(root, filepath.FromSlash(rel))
		must(t, os.MkdirAll(filepath.Dir(p), 0o755))
		must(t, os.WriteFile(p, []byte("x"), 0o600))
		must(t, os.Chtimes(p, old, old))
	}
}

func present(root, rel string) bool {
	_, err := os.Lstat(filepath.Join(root, filepath.FromSlash(rel)))
	return err == nil
}

// TestTheCachePurgeHoldsCredentials: huggingface/token is where huggingface_hub keeps its login
// token by default, and stored_tokens holds the named ones. A credential is not cache whatever its
// age, so the purge holds it, and holds any name containing "token" or "auth" (the plan's
// Credentials row). The aged blob beside them is the control: the purge still runs.
func TestTheCachePurgeHoldsCredentials(t *testing.T) {
	held := []string{
		"huggingface/token",
		"huggingface/stored_tokens",
		"npm/_authToken.json",
		"pip/auth/netrc",
		"uv/credentials/oauth.json",
	}
	for _, apply := range []bool{false, true} {
		root := t.TempDir()
		agedTree(t, root, append([]string{"huggingface/xet/old.blob"}, held...)...)
		_, files := PurgeCacheByAge(root, append(append([]string{}, CachePurgeDefaultSubdirs...),
			CachePurgeHeavySubdirs...), nil, 30, apply, time.Now())
		if files != 1 {
			t.Errorf("apply=%v: counted %d files, want 1 (the aged blob only; a credential is "+
				"never reclaimable, so the measurement must not offer it either)", apply, files)
		}
		for _, rel := range held {
			if !present(root, rel) {
				t.Errorf("apply=%v: the purge deleted %s — a credential, not cache", apply, rel)
			}
		}
		if apply && present(root, "huggingface/xet/old.blob") {
			t.Error("the control: an aged blob must still be purged")
		}
	}
}

// TestTheCachePurgeHoldsARelocatedCredential: a relocated huggingface is walked at its target,
// whose own root is where the token sits.
func TestTheCachePurgeHoldsARelocatedCredential(t *testing.T) {
	cache, target := t.TempDir(), t.TempDir()
	agedTree(t, target, "token", "stored_tokens", "old.blob")
	_, files := PurgeCacheByAge(cache, []string{"huggingface"},
		map[string]string{"huggingface": target}, 30, true, time.Now())
	if files != 1 || !present(target, "token") || !present(target, "stored_tokens") {
		t.Errorf("relocated: removed %d, token kept=%v, stored_tokens kept=%v; want 1, true, true",
			files, present(target, "token"), present(target, "stored_tokens"))
	}
}

// TestTheCachePurgeHoldsLockFiles: uv/.lock is uv's live cache lock, and the plan's class table
// holds it with the rest of uv's control files; a lock file anywhere (a *.lock, a LevelDB LOCK)
// is held for the same reason — its age says nothing about whether a process holds it.
func TestTheCachePurgeHoldsLockFiles(t *testing.T) {
	held := []string{
		"uv/.lock",
		"uv/CACHEDIR.TAG",
		"uv/.gitignore",
		"pip/http-v2/a/b/entry.lock",
		"pants/lmdb_store/LOCK",
		"mise/node/download.lck",
	}
	root := t.TempDir()
	agedTree(t, root, append([]string{"uv/wheels-v5/old.whl"}, held...)...)
	_, files := PurgeCacheByAge(root, CachePurgeDefaultSubdirs, nil, 30, true, time.Now())
	for _, rel := range held {
		if !present(root, rel) {
			t.Errorf("the purge deleted %s — a lock or control file, not cache", rel)
		}
	}
	if files != 1 || present(root, "uv/wheels-v5/old.whl") {
		t.Errorf("the control: removed %d files, want the aged wheel only", files)
	}
}

// TestTheCachePurgeStopsAtItsDeadline is CI-D7's bounded traversal: the walk and the delete pass
// both stop at the deadline and say so, rather than measuring the budget only after an unbounded
// walk. A deadline already past touches nothing; one that passes mid-walk leaves the rest.
func TestTheCachePurgeStopsAtItsDeadline(t *testing.T) {
	files := []string{"uv/a", "uv/b", "uv/c", "uv/d", "uv/e"}

	t.Run("already past", func(t *testing.T) {
		for _, apply := range []bool{false, true} {
			root := t.TempDir()
			agedTree(t, root, files...)
			_, n, partial := PurgeCacheByAgeWithin(root, []string{"uv"}, nil, 30, apply, time.Now(), nil,
				time.Now().Add(-time.Second))
			if !partial || n != 0 {
				t.Errorf("apply=%v: (files=%d, partial=%v), want (0, true)", apply, n, partial)
			}
			for _, rel := range files {
				if !present(root, rel) {
					t.Errorf("apply=%v: removed %s past the deadline", apply, rel)
				}
			}
		}
	})

	t.Run("passes mid-walk", func(t *testing.T) {
		root := t.TempDir()
		agedTree(t, root, files...)
		base := time.Now()
		calls := 0
		orig := purgeClock
		purgeClock = func() time.Time { calls++; return base.Add(time.Duration(calls) * time.Second) }
		defer func() { purgeClock = orig }()
		_, n, partial := PurgeCacheByAgeWithin(root, []string{"uv"}, nil, 30, true, time.Now(), nil,
			base.Add(3*time.Second+time.Millisecond))
		left := 0
		for _, rel := range files {
			if present(root, rel) {
				left++
			}
		}
		if !partial || n == 0 || left == 0 || n+left != len(files) {
			t.Errorf("(removed=%d, left=%d, partial=%v): want some removed, the rest left, partial", n, left, partial)
		}
	})

	t.Run("none", func(t *testing.T) {
		root := t.TempDir()
		agedTree(t, root, files...)
		_, n, partial := PurgeCacheByAgeWithin(root, []string{"uv"}, nil, 30, true, time.Now(), nil, time.Time{})
		if partial || n != len(files) {
			t.Errorf("no deadline: (files=%d, partial=%v), want (%d, false)", n, partial, len(files))
		}
	})
}

// TestTheCachePurgeReadsLargeDirectoriesWhole: the walk reads a directory in batches, and every
// batch is walked — a directory larger than one batch is purged entirely.
func TestTheCachePurgeReadsLargeDirectoriesWhole(t *testing.T) {
	root := t.TempDir()
	var rels []string
	for i := 0; i < 3*purgeBatch+7; i++ {
		rels = append(rels, "pip/http-v2/"+strconv.Itoa(i))
	}
	agedTree(t, root, rels...)
	_, n, partial := PurgeCacheByAgeWithin(root, []string{"pip"}, nil, 30, true, time.Now(), nil,
		time.Now().Add(time.Hour))
	if partial || n != len(rels) {
		t.Errorf("(files=%d, partial=%v), want (%d, false)", n, partial, len(rels))
	}
}

// TestManualPruneStopsTheCachePurgeAtItsBudget: `yolo prune`'s cache purge is bounded like the
// launch's (CI-D7) — past CachePurgeBudget it stops deleting and says the figure is partial, with
// the step that continues it.
func TestManualPruneStopsTheCachePurgeAtItsBudget(t *testing.T) {
	o, gs := baseOpts(t)
	o.Apply = true
	o.NoContainers, o.NoImages, o.NoImageCache, o.NoBuildRoots = true, true, true, true
	o.NoShadowedHome, o.NoEmbeddedPacks, o.NoHardlink = true, true, true
	wheel := filepath.Join(gs, "cache", "uv", "old.whl")
	must(t, os.MkdirAll(filepath.Dir(wheel), 0o755))
	must(t, os.WriteFile(wheel, []byte("x"), 0o644))
	past := o.Now().Add(-60 * 24 * time.Hour)
	must(t, os.Chtimes(wheel, past, past))

	start := time.Now()
	calls := 0
	orig := purgeClock
	purgeClock = func() time.Time { // the first read sets the deadline; every later one is past it
		calls++
		if calls == 1 {
			return start
		}
		return start.Add(2 * CachePurgeBudget)
	}
	defer func() { purgeClock = orig }()

	var buf bytes.Buffer
	o.Out = &buf
	Run(o)
	if !present(gs, "cache/uv/old.whl") {
		t.Error("the manual purge deleted past its budget")
	}
	if !strings.Contains(buf.String(), "partial") || !strings.Contains(buf.String(), "yolo prune --apply") {
		t.Errorf("a purge cut short must say it is partial and how to continue:\n%s", buf.String())
	}
}
