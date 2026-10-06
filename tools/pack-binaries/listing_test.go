package main

// listing_test.go pins WHICH manifests the tool reads (BP-D25): the ones the packs embed carries,
// which is what `go install` builds into yolo from the same tree, rather than every directory on
// disk under packs/ — so a pack the embed does not list, half-written or not, cannot stop
// `just install` — and a seed that skips, rather than refuses, a loophole directory it cannot
// read, which no pack.json names and yolo itself ignores.
//
// Call-site checks:
//   - read os.DirFS(root/packs) again in prepare → TestSeedReadsOnlyWhatTheEmbedCarries.
//   - drop the tolerant read from seed → TestSeedSkipsALoopholeDirItCannotRead.
//   - point defaultDeps' listing anywhere but packs.FS → TestTheToolListsTheEmbed.

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/packs"
)

// An untracked pack on disk that the embed does not list — a half-written manifest, mid-edit —
// is invisible to every verb, because `go install` would not build it into yolo either.
func TestSeedReadsOnlyWhatTheEmbedCarries(t *testing.T) {
	root := machineCheckout(t)
	mustRun(t, root, "pin", "0.2.0")
	listing := os.DirFS(filepath.Join(root, "packs")) // the embed, as it was before the stray pack
	listed := fixtureListing(t, listing)
	writeFile(t, root, "packs/wip/loopholes/half/manifest.jsonc", `{"name": "half", "binaries": {`)

	for _, args := range [][]string{{"seed", t.TempDir()}, {"check"}, {"check", "0.2.0"}} {
		r := runToolOver(t, root, listed, args...)
		if r.code != 0 || strings.Contains(r.stderr+r.stdout, "wip") {
			t.Errorf("%v with an untracked half-written pack on disk: exit %d\n%s%s", args, r.code,
				r.stdout, r.stderr)
		}
	}
}

// fixtureListing snapshots a packs tree into memory, standing in for the embed of the tree as
// `go run` compiled it.
func fixtureListing(t *testing.T, fsys fs.FS) fs.FS {
	t.Helper()
	out := map[string][]byte{}
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(fsys, p)
		out[p] = b
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return mapFS(out)
}

// A loophole directory inside a listed pack that holds no readable manifest — what a branch
// switch leaves when the directory held an ignored file, or an edit in progress — is skipped by
// seed with a note, so the install goes on; the gates still refuse it.
func TestSeedSkipsALoopholeDirItCannotRead(t *testing.T) {
	root := machineCheckout(t)
	mustRun(t, root, "pin", "0.2.0")
	writeFile(t, root, "packs/p/loopholes/stray/.DS_Store", "junk")
	writeFile(t, root, "packs/p/loopholes/broken/manifest.jsonc", `{"name": "broken",`)

	cache := t.TempDir()
	r := runTool(t, root, "seed", cache)
	if r.code != 0 {
		t.Fatalf("seed over an unreadable loophole dir: exit %d\n%s%s", r.code, r.stdout, r.stderr)
	}
	for _, want := range []string{"packs/p/loopholes/stray", "packs/p/loopholes/broken", "skipped"} {
		if !strings.Contains(r.stderr, want) {
			t.Errorf("seed did not say it skipped %q:\n%s", want, r.stderr)
		}
	}
	if !strings.Contains(r.stdout, "seeded binary toold") {
		t.Errorf("seed did not seed the readable loophole:\n%s", r.stdout)
	}
	if r := runTool(t, root, "check"); r.code != 1 {
		t.Errorf("the gate let an unreadable loophole dir pass: exit %d\n%s", r.code, r.stderr)
	}
}

// The production listing is the packs embed, compiled by `go run` from the tree it runs in.
func TestTheToolListsTheEmbed(t *testing.T) {
	if d := defaultDeps(t.TempDir(), nil); d.packs != fs.FS(packs.FS) {
		t.Errorf("the tool lists manifests from %T, not the packs embed", d.packs)
	}
}
