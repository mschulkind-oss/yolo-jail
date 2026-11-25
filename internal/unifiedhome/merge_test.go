package unifiedhome

import (
	"errors"
	"fmt"
	"io/fs"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// tree makes a fresh real directory and returns its path.
func tree(t *testing.T) string {
	t.Helper()
	d, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func put(t *testing.T, base, rel, content string) {
	t.Helper()
	p := filepath.Join(base, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func stamp(t *testing.T, base, rel string, at time.Time) {
	t.Helper()
	if err := os.Chtimes(filepath.Join(base, rel), at, at); err != nil {
		t.Fatal(err)
	}
}

func open(t *testing.T, dir string) *os.Root {
	t.Helper()
	r, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	return r
}

func read(t *testing.T, base, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(base, rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(b)
}

// snapshot maps every regular file's relative path to its bytes and every link to "-> target".
func snapshot(t *testing.T, base string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(base, p)
		switch {
		case d.Type()&fs.ModeSymlink != 0:
			tg, _ := os.Readlink(p)
			out[rel] = "-> " + tg
		case d.Type().IsRegular():
			b, _ := os.ReadFile(p)
			out[rel] = string(b)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func mustMerge(t *testing.T, dstBase, dstDir, label, srcBase string, dirs ...string) Report {
	t.Helper()
	rep, err := Merge(open(t, dstBase), dstDir, Source{Label: label, Root: open(t, srcBase), Dirs: dirs}, Options{})
	if err != nil {
		t.Fatalf("merge %s: %v", label, err)
	}
	return rep
}

func TestMergeConservesEveryInputByteString(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	srcs := []string{tree(t), tree(t), tree(t)}
	dirs := []string{"projects", "projects/sub", "todos"}
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 40; i++ {
		s := srcs[r.Intn(3)]
		rel := filepath.Join(".claude", dirs[r.Intn(len(dirs))], fmt.Sprintf("f%d.jsonl", r.Intn(8)))
		put(t, s, rel, fmt.Sprintf("content-%d", r.Intn(25)))
		stamp(t, s, rel, base.Add(time.Duration(r.Intn(1000))*time.Second))
	}
	inputs := map[string]bool{}
	for _, s := range srcs {
		for _, v := range snapshot(t, s) {
			inputs[v] = true
		}
	}
	dst := tree(t)
	for i, s := range srcs {
		mustMerge(t, dst, ".claude", fmt.Sprintf("s%d", i), s, ".claude")
	}
	inDst := map[string]bool{}
	for _, v := range snapshot(t, dst) {
		inDst[v] = true
	}
	for v := range inputs {
		if !inDst[v] {
			t.Errorf("input byte string %q is not in the unified tree", v)
		}
	}
}

func TestMergeTheThreeStrandedHomes(t *testing.T) {
	podman, apple := tree(t), tree(t)
	put(t, podman, "claude/projects/b.jsonl", "b")
	put(t, apple, ".claude/projects/c.jsonl", "c")
	put(t, apple, "claude/projects/a.jsonl", "a")
	dst := tree(t)
	mustMerge(t, dst, ".claude", "podman-self", podman, ".claude", "claude")
	mustMerge(t, dst, ".claude", "apple-container-self", apple, ".claude", "claude")
	want := map[string]string{
		".claude/projects/a.jsonl": "a",
		".claude/projects/b.jsonl": "b",
		".claude/projects/c.jsonl": "c",
	}
	got := snapshot(t, dst)
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("unified tree = %v, want %v", got, want)
	}
}

func TestMergeCollisionKinds(t *testing.T) {
	dst, src := tree(t), tree(t)
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	put(t, dst, ".claude/p/same.jsonl", "s")
	put(t, src, ".claude/p/same.jsonl", "s")
	put(t, dst, ".claude/p/dstnewer.jsonl", "dstv")
	put(t, src, ".claude/p/dstnewer.jsonl", "srcv")
	put(t, dst, ".claude/p/srcnewer.jsonl", "dstv")
	put(t, src, ".claude/p/srcnewer.jsonl", "srcv")
	stamp(t, dst, ".claude/p/dstnewer.jsonl", t0.Add(10*time.Second))
	stamp(t, src, ".claude/p/dstnewer.jsonl", t0)
	stamp(t, dst, ".claude/p/srcnewer.jsonl", t0)
	stamp(t, src, ".claude/p/srcnewer.jsonl", t0.Add(10*time.Second))
	put(t, dst, ".claude/p/thing", "file")
	put(t, src, ".claude/p/thing/inner", "dir")

	rep := mustMerge(t, dst, ".claude", "x", src, ".claude")
	want := map[string]string{
		".claude/p/same.jsonl":            "s",
		".claude/p/dstnewer.jsonl":        "dstv",
		".claude/p/dstnewer.jsonl.from-x": "srcv",
		".claude/p/srcnewer.jsonl":        "srcv",
		".claude/p/srcnewer.jsonl.from-x": "dstv",
		".claude/p/thing":                 "file",
		".claude/p/thing.from-x/inner":    "dir",
	}
	if got := snapshot(t, dst); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("unified tree = %v, want %v", got, want)
	}
	if rep.Skipped != 1 || rep.KeptBoth != 3 {
		t.Fatalf("report = %+v, want Skipped 1 KeptBoth 3", rep)
	}
}

func TestMergeSecondFromNameGetsANumericSuffix(t *testing.T) {
	dst, a1, a2 := tree(t), tree(t), tree(t)
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	put(t, dst, ".claude/x.jsonl", "d")
	put(t, a1, ".claude/x.jsonl", "one")
	put(t, a2, ".claude/x.jsonl", "two")
	stamp(t, dst, ".claude/x.jsonl", t0.Add(time.Hour))
	stamp(t, a1, ".claude/x.jsonl", t0)
	stamp(t, a2, ".claude/x.jsonl", t0)
	mustMerge(t, dst, ".claude", "a", a1, ".claude")
	mustMerge(t, dst, ".claude", "a", a2, ".claude")
	want := map[string]string{
		".claude/x.jsonl":          "d",
		".claude/x.jsonl.from-a":   "one",
		".claude/x.jsonl.from-a.2": "two",
	}
	if got := snapshot(t, dst); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("unified tree = %v, want %v", got, want)
	}
}

func TestMergeMovesALinkAsALinkAndNeverTouchesItsTarget(t *testing.T) {
	dst, src, outside := tree(t), tree(t), tree(t)
	put(t, outside, "secret", "sentinel-bytes")
	before := snapshot(t, outside)
	if err := os.MkdirAll(filepath.Join(src, ".claude/projects"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(src, ".claude/projects/lnk")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(src, ".claude/projects/clash")); err != nil {
		t.Fatal(err)
	}
	put(t, dst, ".claude/projects/clash/inside", "real")
	mustMerge(t, dst, ".claude", "x", src, ".claude")

	if tg, err := os.Readlink(filepath.Join(dst, ".claude/projects/lnk")); err != nil || tg != outside {
		t.Fatalf("lnk = %q, %v; want a link to %q", tg, err, outside)
	}
	if tg, err := os.Readlink(filepath.Join(dst, ".claude/projects/clash.from-x")); err != nil || tg != outside {
		t.Fatalf("clash.from-x = %q, %v; want a link to %q", tg, err, outside)
	}
	if read(t, dst, ".claude/projects/clash/inside") != "real" {
		t.Fatal("the real directory at the clashing name was disturbed")
	}
	if fmt.Sprint(snapshot(t, outside)) != fmt.Sprint(before) {
		t.Fatal("the link's target changed")
	}
}

func fixture(t *testing.T) (dst string, srcs []Source) {
	t.Helper()
	dst = tree(t)
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	a, b := tree(t), tree(t)
	put(t, dst, ".claude/projects/both.jsonl", "dst-both")
	stamp(t, dst, ".claude/projects/both.jsonl", t0)
	put(t, dst, ".claude/projects/keep.jsonl", "dst-keep")
	stamp(t, dst, ".claude/projects/keep.jsonl", t0.Add(time.Hour))
	put(t, a, ".claude/projects/both.jsonl", "a-both")
	stamp(t, a, ".claude/projects/both.jsonl", t0.Add(time.Minute))
	put(t, a, ".claude/projects/keep.jsonl", "a-keep")
	stamp(t, a, ".claude/projects/keep.jsonl", t0)
	put(t, a, ".claude/projects/only-a.jsonl", "only-a")
	put(t, a, ".claude/todos/t1", "t1")
	put(t, b, "claude/projects/only-b.jsonl", "only-b")
	put(t, b, "claude/projects/sub/deep", "deep")
	put(t, dst, ".claude/projects/sub/other", "other")
	if err := os.Symlink("/nowhere", filepath.Join(b, "claude/projects/lnk")); err != nil {
		t.Fatal(err)
	}
	srcs = []Source{
		{Label: "a", Root: open(t, a), Dirs: []string{".claude", "claude"}},
		{Label: "b", Root: open(t, b), Dirs: []string{".claude", "claude"}},
	}
	return dst, srcs
}

func runAll(t *testing.T, dst string, srcs []Source, rename func(int, string, int, string) error) error {
	t.Helper()
	root := open(t, dst)
	var errs []error
	for _, s := range srcs {
		if _, err := Merge(root, ".claude", s, Options{Rename: rename}); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func TestMergeIsIdempotentAfterAnyFailedRename(t *testing.T) {
	cleanDst, cleanSrcs := fixture(t)
	n := 0
	count := func(od int, o string, nd int, nn string) error {
		n++
		return defaultRename(od, o, nd, nn)
	}
	if err := runAll(t, cleanDst, cleanSrcs, count); err != nil {
		t.Fatal(err)
	}
	want := snapshot(t, cleanDst)
	if n == 0 {
		t.Fatal("the fixture renamed nothing")
	}
	for fail := 1; fail <= n; fail++ {
		dst, srcs := fixture(t)
		i := 0
		inject := func(od int, o string, nd int, nn string) error {
			i++
			if i == fail {
				return syscall.EIO
			}
			return defaultRename(od, o, nd, nn)
		}
		if err := runAll(t, dst, srcs, inject); err == nil {
			t.Fatalf("rename %d failed and the merge reported no error", fail)
		}
		if err := runAll(t, dst, srcs, nil); err != nil {
			t.Fatalf("rerun after rename %d: %v", fail, err)
		}
		if got := snapshot(t, dst); fmt.Sprint(got) != fmt.Sprint(want) {
			t.Fatalf("after failing rename %d the union is %v, want %v", fail, got, want)
		}
	}
}

func TestMergeLeavesSocketsAndNeverMergesSettings(t *testing.T) {
	dst, src := tree(t), tree(t)
	put(t, dst, ".claude/settings.json", "dst-settings")
	put(t, src, ".claude/settings.json", "src-settings")
	put(t, dst, ".claude/history.jsonl", "dst-hist")
	put(t, src, ".claude/history.jsonl", "src-hist")
	put(t, src, ".claude/ide/1.lock", "lock")
	put(t, src, ".claude/daemon.pid", "42")
	if err := unix.Mkfifo(filepath.Join(src, ".claude/pipe"), 0o644); err != nil {
		t.Fatal(err)
	}
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, p := range []string{".claude/settings.json", ".claude/history.jsonl"} {
		stamp(t, dst, p, t0.Add(time.Hour))
		stamp(t, src, p, t0)
	}
	rep := mustMerge(t, dst, ".claude", "x", src, ".claude")
	got := snapshot(t, dst)
	want := map[string]string{
		".claude/settings.json":        "dst-settings",
		".claude/settings.json.from-x": "src-settings",
		".claude/history.jsonl":        "dst-hist",
		".claude/history.jsonl.from-x": "src-hist",
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("unified tree = %v, want %v", got, want)
	}
	var left []string
	for _, l := range rep.Left {
		left = append(left, filepath.Base(l.Path))
	}
	sort.Strings(left)
	if strings.Join(left, ",") != "1.lock,daemon.pid,pipe" {
		t.Fatalf("left = %v", left)
	}
	for _, p := range []string{".claude/pipe", ".claude/daemon.pid", ".claude/ide/1.lock"} {
		if _, err := os.Lstat(filepath.Join(src, p)); err != nil {
			t.Errorf("%s was not left in its source: %v", p, err)
		}
	}
}

func TestMergeLeavesASourceNotOwnedByTheUser(t *testing.T) {
	dst, src := tree(t), tree(t)
	put(t, src, ".claude/projects/a.jsonl", "a")
	other := uint32(os.Getuid() + 1)
	rep, err := Merge(open(t, dst), ".claude", Source{Label: "x", Root: open(t, src), Dirs: []string{".claude"}}, Options{OwnerUID: &other})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Left) != 1 || rep.Moved != 0 {
		t.Fatalf("report = %+v, want the source left whole", rep)
	}
	if read(t, src, ".claude/projects/a.jsonl") != "a" {
		t.Fatal("a source owned by someone else was touched")
	}
}

func TestMergeLeavesALinkedSourceDirectory(t *testing.T) {
	dst, src, outside := tree(t), tree(t), tree(t)
	put(t, outside, "x.jsonl", "x")
	if err := os.Symlink(outside, filepath.Join(src, ".claude")); err != nil {
		t.Fatal(err)
	}
	rep := mustMerge(t, dst, ".claude", "x", src, ".claude")
	if len(rep.Left) != 1 || len(snapshot(t, dst)) != 0 {
		t.Fatalf("report = %+v, dst = %v; a linked state directory must be left unread", rep, snapshot(t, dst))
	}
}

func TestPlanLevelIsPureAndNamesFreeSuffixes(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	src := []Entry{
		{Name: "x", Kind: KindFile, ModTime: t0},
		{Name: "new", Kind: KindFile, ModTime: t0},
		{Name: "d", Kind: KindDir},
		{Name: "same", Kind: KindFile, ModTime: t0},
	}
	dst := []Entry{
		{Name: "x", Kind: KindFile, ModTime: t0.Add(time.Hour)},
		{Name: "x.from-s", Kind: KindFile},
		{Name: "d", Kind: KindDir},
		{Name: "same", Kind: KindFile, ModTime: t0},
	}
	steps := PlanLevel(src, dst, func(n string) bool { return n == "same" }, "s")
	want := []Step{
		{Name: "d", Op: OpRecurse},
		{Name: "new", Op: OpRename},
		{Name: "same", Op: OpSkip},
		{Name: "x", Op: OpKeepBoth, Target: "x.from-s.2"},
	}
	if fmt.Sprint(steps) != fmt.Sprint(want) {
		t.Fatalf("steps = %v, want %v", steps, want)
	}
}
