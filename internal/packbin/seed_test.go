package packbin

// Seed's contract (packbin.go): a build this machine made enters the cache through the same
// verified rename a download takes, at its digest, under its name, 0555 — or not at all.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeSrc writes a build the way the pin tool leaves one: a plain file outside the cache.
func writeSrc(t *testing.T, body []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "toold")
	if err := os.WriteFile(p, body, 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// A seeded build lands where a fetched one would, through the same verified rename: at its
// digest, under its name, 0555 — so every reader of the cache finds the tree's build exactly
// where it would find a release's (broker-as-a-pack.md BP-D15). Nothing is downloaded: no
// client and no server exists in this test.
func TestSeedAdmitsAVerifiedCopyWhereAFetchWouldPutIt(t *testing.T) {
	body := []byte("#!/bin/sh\necho built from the tree\n")
	src := writeSrc(t, body)
	dir := t.TempDir()

	path, outcome, err := Seed(dir, "toold", digest(body), src)
	if err != nil {
		t.Fatal(err)
	}
	if outcome != Seeded || path != Path(dir, digest(body), "toold") {
		t.Errorf("Seed = %q, %v; want %q, Seeded", path, outcome, Path(dir, digest(body), "toold"))
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != string(body) {
		t.Fatalf("seeded bytes = %q, %v", got, err)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o555 || !Present(path) {
		t.Errorf("seeded mode = %v, want 0555", fi.Mode().Perm())
	}
	// A copy, never a move or a link: the tool's build dir is a temp dir it deletes.
	if _, err := os.Stat(src); err != nil {
		t.Errorf("Seed consumed its source: %v", err)
	}
	if fi, _ := os.Lstat(path); fi.Mode()&os.ModeSymlink != 0 {
		t.Error("the seeded build is a symlink to the source")
	}
	for _, e := range entries(t, dir) {
		if strings.HasPrefix(filepath.Base(e), ".seed-") {
			t.Errorf("Seed left its temp file behind: %s", e)
		}
	}

	// The same build again is already there, and re-hashed rather than trusted by name.
	if _, outcome, err := Seed(dir, "toold", digest(body), src); err != nil || outcome != Cached {
		t.Errorf("second Seed = %v, %v; want Cached", outcome, err)
	}
	// A seeded build satisfies a later `yolo pack install` with no network: Ensure finds it
	// Cached and never dials the URL, which does not resolve.
	f := Fetcher{Dir: dir}
	if _, outcome, err := f.Ensure(context.Background(), Want{Name: "toold",
		URL: "https://unreachable.invalid/toold", SHA256: digest(body)}); err != nil || outcome != Cached {
		t.Errorf("Ensure over a seeded build = %v, %v; want Cached with no fetch", outcome, err)
	}
}

func TestSeedRefusesAFileThatIsNotThePinAndCachesNothing(t *testing.T) {
	body := []byte("the tree's build\n")
	src := writeSrc(t, body)
	dir := t.TempDir()
	pin := digest([]byte("what the manifest pins\n"))

	_, _, err := Seed(dir, "toold", pin, src)
	var mm *SeedMismatchError
	if !errors.As(err, &mm) || mm.Want != pin || mm.Got != digest(body) {
		t.Fatalf("Seed of a file that is not the pin = %v, want a SeedMismatchError naming both digests", err)
	}
	for _, want := range []string{pin, digest(body), src} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %s: %v", want, err)
		}
	}
	if got := entries(t, dir); len(got) != 0 {
		t.Errorf("a refused seed left %v in the cache", got)
	}
}

func TestSeedReplacesACachedCopyThatStoppedMatching(t *testing.T) {
	body := []byte("good\n")
	dir := t.TempDir()
	p := Path(dir, digest(body), "toold")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("corrupted\n"), 0o555); err != nil {
		t.Fatal(err)
	}
	path, outcome, err := Seed(dir, "toold", digest(body), writeSrc(t, body))
	if err != nil || outcome != Replaced {
		t.Fatalf("Seed over a corrupted copy = %v, %v; want Replaced", outcome, err)
	}
	if got, _ := os.ReadFile(path); string(got) != string(body) {
		t.Errorf("the replaced copy holds %q", got)
	}
}

// The digest and the name become path segments under the cache root, so neither may name
// anything else.
func TestSeedRefusesAKeyThatIsNotOne(t *testing.T) {
	body := []byte("x\n")
	src := writeSrc(t, body)
	dir := t.TempDir()
	for _, tc := range []struct{ name, sum string }{
		{"toold", "../" + digest(body)[3:]},
		{"toold", strings.ToUpper(digest(body))},
		{"../toold", digest(body)},
		{"sub/toold", digest(body)},
		{"..", digest(body)},
		{"", digest(body)},
	} {
		if _, _, err := Seed(dir, tc.name, tc.sum, src); err == nil {
			t.Errorf("Seed(%q, %q) was accepted", tc.name, tc.sum)
		}
	}
	if got := entries(t, dir); len(got) != 0 {
		t.Errorf("refused seeds left %v", got)
	}
}
