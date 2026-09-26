package capture

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"testing/iotest"
)

// rewrite_test.go measures the REWRITE half of relocation (install-capture.md hand-off H2):
// an entry RECORDED under one home and MATERIALIZED under another.
//
// Every fixture here is recorded by the real driver (capture.Run, with the content scan on, the
// way the macos-user capture runs it) and admitted by the real store, so what Materialize is
// handed is exactly what a relocated capture would hand it. Two Linux temp dirs stand in for the
// macos-user pair — the staging home /Users/Shared/yolo-captures/<bin>/home and the account home
// /Users/_yolojail. No Mac has run any of this.

// rewriteInstaller writes one of each thing a relocation has to handle, with every absolute path
// spliced from $HOME so it names the capture home the way a vendor installer's would.
const rewriteInstaller = `#!/bin/sh
set -eu
v="$HOME/.local/share/vendor/1.0.0"
mkdir -p "$v" "$HOME/.local/bin"
printf '#!/bin/sh\necho "vendor ran as $0"\n' > "$v/vendor"
chmod 755 "$v/vendor"

# An ABSOLUTE symlink into the capture home — claude's ~/.local/bin/claude shape.
ln -s "$v/vendor" "$HOME/.local/bin/vendor"

# An absolute symlink with a .. in it, which the rewrite must clean rather than copy.
ln -s "$HOME/.local/share/../share/vendor/1.0.0/vendor" "$HOME/.local/bin/vendor-dotdot"

# A TEXT shim embedding the capture home — the launcher-script shape.
printf '#!/bin/sh\nexec %s/.local/share/vendor/1.0.0/vendor "$@"\n' "$HOME" > "$HOME/.local/bin/vendor-shim"
chmod 755 "$HOME/.local/bin/vendor-shim"

# A config file naming the home more than once on one line.
printf 'root=%s bin=%s/.local/bin\n' "$HOME" "$HOME" > "$v/config"

# A RELATIVE link and a file with no reference: both already correct anywhere.
ln -s ../share/vendor/1.0.0/vendor "$HOME/.local/bin/vendor-rel"
printf 'no paths in here\n' > "$v/README"
`

// recordRelocatable records a capture of script under a fresh capture home and admits it,
// returning the capture home, the store, and the admitted entry. mutate, when non-nil, edits the
// manifest between recording and admission — which is how the refusal tests build a manifest
// that disagrees with itself or its tree.
func recordRelocatable(t *testing.T, script string, mutate func(*Manifest)) (string, *Store, *Entry) {
	t.Helper()
	captureHome := t.TempDir()
	fixtureHome(t, captureHome)
	store := &Store{Dir: t.TempDir()}
	staged, err := store.Stage("vendor")
	must(t, err)
	res, err := Run(Options{
		Home: captureHome, Out: staged, Command: writeInstaller(t, script),
		ScanContentRefs: true,
	})
	must(t, err)
	if mutate != nil {
		mutate(res.Manifest)
		must(t, WriteManifest(staged, res.Manifest))
	}
	entry, err := store.AdmitEntry(staged)
	must(t, err)
	return captureHome, store, entry
}

// THE H2 PROPERTY. A relocatable capture materialized into another home has every recorded
// reference rewritten to that home, and the program runs from there after the capture home is
// gone — which on macos-user it always is, because the staging tree is swept when the capture
// ends.
func TestRelocatingMaterializeRewritesEveryReference(t *testing.T) {
	from, _, entry := recordRelocatable(t, rewriteInstaller, nil)
	m, err := ReadManifest(entry.Root)
	must(t, err)
	if !m.Relocatable {
		t.Fatalf("the fixture must record a relocatable capture: %v", m.NotRelocatable)
	}
	to := t.TempDir()

	res, err := Materialize(MaterializeOptions{Entry: entry, Home: to})
	if err != nil {
		t.Fatalf("relocating materialize: %v", err)
	}
	// The capture home is thrown away. Everything below must work without it.
	must(t, os.RemoveAll(from))

	// Symlinks: created with the rewritten target, never the recorded one.
	for link, want := range map[string]string{
		".local/bin/vendor":        to + "/.local/share/vendor/1.0.0/vendor",
		".local/bin/vendor-dotdot": to + "/.local/share/vendor/1.0.0/vendor",
		".local/bin/vendor-rel":    "../share/vendor/1.0.0/vendor",
	} {
		got, err := os.Readlink(filepath.Join(to, filepath.FromSlash(link)))
		if err != nil {
			t.Errorf("%s: %v", link, err)
			continue
		}
		if got != want {
			t.Errorf("%s -> %q, want %q", link, got, want)
		}
	}

	// File contents: the prefix substituted everywhere, the rest byte-identical.
	shim := readString(t, filepath.Join(to, ".local", "bin", "vendor-shim"))
	if want := "#!/bin/sh\nexec " + to + "/.local/share/vendor/1.0.0/vendor \"$@\"\n"; shim != want {
		t.Errorf("rewritten shim = %q, want %q", shim, want)
	}
	cfg := readString(t, filepath.Join(to, ".local", "share", "vendor", "1.0.0", "config"))
	if want := "root=" + to + " bin=" + to + "/.local/bin\n"; cfg != want {
		t.Errorf("rewritten config = %q, want %q (every occurrence, not the first)", cfg, want)
	}
	if readme := readString(t, filepath.Join(to, ".local", "share", "vendor", "1.0.0", "README")); readme != "no paths in here\n" {
		t.Errorf("a file with no reference changed: %q", readme)
	}

	// Nothing under the new home still names the old one.
	must(t, filepath.WalkDir(to, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		switch {
		case d.Type()&fs.ModeSymlink != 0:
			target, _ := os.Readlink(p)
			if strings.Contains(target, from) {
				t.Errorf("%s still links into the capture home: %s", p, target)
			}
		case d.Type().IsRegular():
			if strings.Contains(readString(t, p), from) {
				t.Errorf("%s still names the capture home", p)
			}
			if strings.Contains(d.Name(), ".yolo-rewrite-") {
				t.Errorf("a rewrite temp file was left behind: %s", p)
			}
		}
		return nil
	}))

	// A rewritten file gets the installer's mode, which CreateTemp's 0600 is not.
	if fi, err := os.Stat(filepath.Join(to, ".local", "bin", "vendor-shim")); err != nil {
		t.Fatal(err)
	} else if fi.Mode().Perm() != 0o755 {
		t.Errorf("rewritten shim mode = %v, want the manifest's 0755", fi.Mode().Perm())
	}

	// THE PROGRAM RUNS FROM THE NEW HOME, through both rewritten kinds.
	for _, bin := range []string{"vendor", "vendor-shim"} {
		out, err := exec.Command(filepath.Join(to, ".local", "bin", bin)).CombinedOutput()
		if err != nil {
			t.Errorf("running %s from the relocated home: %v\n%s", bin, err, out)
			continue
		}
		if !strings.Contains(string(out), "vendor ran as "+to+"/") {
			t.Errorf("%s ran something other than the relocated vendor: %s", bin, out)
		}
	}

	if res.RelocatedFrom != filepath.Clean(from) {
		t.Errorf("RelocatedFrom = %q, want %q", res.RelocatedFrom, from)
	}
	if res.Rewritten != 2 || res.RewrittenLinks != 2 {
		t.Errorf("rewrote %d files / %d links, want 2/2", res.Rewritten, res.RewrittenLinks)
	}
	if res.Reflinked+res.Linked+res.Copied+res.Rewritten != res.Files {
		t.Errorf("the mechanism counters (%d/%d/%d/%d) do not partition the %d files",
			res.Reflinked, res.Linked, res.Copied, res.Rewritten, res.Files)
	}
}

// A REWRITE NEVER WRITES THROUGH A PLACED FILE. On the hardlink arm a placed file is the store's
// own inode, so a rewrite made in place would rewrite the entry every other workspace
// materializes from. Forced here because on this test's filesystem reflink may win, and the
// hazard exists only on the arm that shares an inode.
func TestARelocationRewriteLeavesTheStoreEntryUntouched(t *testing.T) {
	from, _, entry := recordRelocatable(t, rewriteInstaller, nil)
	restore := forceChain(t,
		func(src, dst string, perm fs.FileMode) error { return errCloneUnsupported },
		os.Link)
	defer restore()
	to := t.TempDir()

	res, err := Materialize(MaterializeOptions{Entry: entry, Home: to})
	if err != nil {
		t.Fatalf("relocating materialize on the hardlink arm: %v", err)
	}
	if res.Linked == 0 {
		t.Fatalf("the hardlink arm placed nothing (%+v); the test is not on the arm it is about", res)
	}

	storeShim := filepath.Join(entry.Tree, ".local", "bin", "vendor-shim")
	if body := readString(t, storeShim); !strings.Contains(body, from) || strings.Contains(body, to) {
		t.Errorf("the STORE's shim was rewritten — every workspace now runs %q", body)
	}
	if statT(t, storeShim).Ino == statT(t, filepath.Join(to, ".local", "bin", "vendor-shim")).Ino {
		t.Error("the rewritten shim shares the store's inode")
	}
	// A file with no reference still takes the chain, hardlink and all.
	vendor := filepath.Join(".local", "share", "vendor", "1.0.0", "vendor")
	if statT(t, filepath.Join(entry.Tree, vendor)).Ino != statT(t, filepath.Join(to, vendor)).Ino {
		t.Error("an unreferenced file did not take the hardlink arm")
	}
}

// EVERY WAY A REFERENCE CAN FAIL TO BE REWRITTEN IS A REFUSAL BEFORE THE HOME IS TOUCHED. Each
// case edits a real recorded manifest into a shape the contract cannot honor, and each must be
// ErrNotRelocatable, name the obstacle, and leave the destination empty.
func TestARelocationThatCannotBeHonoredRefusesBeforeWriting(t *testing.T) {
	binaryInstaller := `#!/bin/sh
set -eu
mkdir -p "$HOME/.local/share/vendor"
printf 'ELF\000\000%s/.local/share/vendor\000\n' "$HOME" > "$HOME/.local/share/vendor/vendor"
`
	cases := []struct {
		name   string
		script string
		mutate func(*Manifest)
		want   string
	}{
		{
			name: "the record says no", script: rewriteInstaller,
			mutate: func(m *Manifest) {
				m.Relocatable, m.NotRelocatable = false, []string{"because the fixture said so"}
			},
			want: "because the fixture said so",
		},
		{
			name: "relocatable over a symlink-only scan", script: rewriteInstaller,
			mutate: func(m *Manifest) { m.RefScan = RefScanSymlinks },
			want:   RefScanSymlinks,
		},
		{
			name: "a reference to a path the capture does not hold", script: rewriteInstaller,
			mutate: func(m *Manifest) {
				m.AbsoluteRefs = append(m.AbsoluteRefs, AbsoluteRef{
					Path: ".local/bin/ghost", Kind: RefSymlinkTarget, Value: m.Home + "/x",
				})
			},
			want: ".local/bin/ghost",
		},
		{
			name: "a symlink reference that is not the link's target", script: rewriteInstaller,
			mutate: func(m *Manifest) {
				setRef(m, ".local/bin/vendor", RefSymlinkTarget, m.Home+"/somewhere/else")
			},
			want: "somewhere/else",
		},
		{
			name: "a symlink reference on a file", script: rewriteInstaller,
			mutate: func(m *Manifest) {
				m.AbsoluteRefs = append(m.AbsoluteRefs, AbsoluteRef{
					Path: ".local/share/vendor/1.0.0/README", Kind: RefSymlinkTarget, Value: m.Home,
				})
			},
			want: "README",
		},
		{
			name: "a file-content prefix that is not the capture home", script: rewriteInstaller,
			mutate: func(m *Manifest) {
				setRef(m, ".local/bin/vendor-shim", RefFileContent, m.Home+"/.local")
			},
			want: "vendor-shim",
		},
		{
			name: "a reference inside a binary the record called relocatable", script: binaryInstaller,
			mutate: func(m *Manifest) { m.Relocatable, m.NotRelocatable = true, nil },
			want:   "is not text",
		},
		{
			name: "a reference kind this yolo cannot rewrite", script: rewriteInstaller,
			mutate: func(m *Manifest) {
				m.AbsoluteRefs = append(m.AbsoluteRefs, AbsoluteRef{
					Path: ".local/bin/vendor-shim", Kind: "mach-o-load-command", Value: m.Home,
				})
			},
			want: "mach-o-load-command",
		},
		{
			name: "an absolute link the record does not list", script: rewriteInstaller,
			mutate: func(m *Manifest) { dropRef(m, ".local/bin/vendor", RefSymlinkTarget) },
			want:   "lists no reference",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, _, entry := recordRelocatable(t, c.script, c.mutate)
			to := t.TempDir()
			_, err := Materialize(MaterializeOptions{Entry: entry, Home: to})
			if !errors.Is(err, ErrNotRelocatable) {
				t.Fatalf("materialize = %v, want ErrNotRelocatable", err)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("the refusal does not name %q: %v", c.want, err)
			}
			if ents, _ := os.ReadDir(to); len(ents) != 0 {
				t.Errorf("a refused relocation wrote into the home: %v", ents)
			}
		})
	}
}

// A WRITE THAT FAILS HALFWAY NEVER LANDS AT THE DESTINATION PATH. The rewrite streams into a temp
// file beside the destination and renames it into place, so a stale shim already in the home
// survives a failed rewrite intact, and no temp file is left behind.
func TestAFailedRewriteLeavesThePreviousFileInPlace(t *testing.T) {
	_, _, entry := recordRelocatable(t, rewriteInstaller, nil)
	to := t.TempDir()
	shim := filepath.Join(to, ".local", "bin", "vendor-shim")
	must(t, os.MkdirAll(filepath.Dir(shim), 0o755))
	must(t, os.WriteFile(shim, []byte("the previous shim\n"), 0o755))

	old := rewriteStream
	rewriteStream = func(w io.Writer, r io.Reader, from, to []byte) (int64, error) {
		n, _ := w.Write([]byte("half a sh"))
		return int64(n), errors.New("disk full, as far as this test is concerned")
	}
	defer func() { rewriteStream = old }()

	_, err := Materialize(MaterializeOptions{Entry: entry, Home: to})
	if err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("materialize = %v, want the write failure", err)
	}
	if errors.Is(err, ErrNotRelocatable) {
		t.Errorf("an I/O failure is not a relocation verdict: %v", err)
	}
	if got := readString(t, shim); got != "the previous shim\n" {
		t.Errorf("a failed rewrite changed the destination to %q", got)
	}
	ents, err := os.ReadDir(filepath.Dir(shim))
	must(t, err)
	for _, e := range ents {
		if strings.Contains(e.Name(), ".yolo-rewrite-") {
			t.Errorf("the failed rewrite left its temp file: %s", e.Name())
		}
	}
}

// The substitution is a streaming bytes.ReplaceAll, and it must agree with the real one on the
// cases a chunked implementation gets wrong: a match straddling a read, back-to-back matches,
// a needle that overlaps itself, and a tail shorter than the needle.
func TestStreamReplaceAgreesWithReplaceAll(t *testing.T) {
	from := []byte("/Users/Shared/yolo-captures/vendor/home")
	to := []byte("/Users/_yolojail")
	straddle := append(bytes.Repeat([]byte("x"), scanChunk-5), from...)
	inputs := map[string][]byte{
		"empty":           {},
		"no match":        []byte("nothing to see"),
		"one match":       append([]byte("exec "), append(from, []byte("/bin/v\n")...)...),
		"back to back":    bytes.Repeat(from, 3),
		"straddles reads": append(straddle, []byte("/tail\n")...),
		"trailing prefix": append([]byte("a"), from[:len(from)-1]...),
	}
	for name, in := range inputs {
		for _, reader := range []struct {
			kind string
			r    func() io.Reader
		}{
			{"whole", func() io.Reader { return bytes.NewReader(in) }},
			{"one byte at a time", func() io.Reader { return iotest.OneByteReader(bytes.NewReader(in)) }},
		} {
			var out bytes.Buffer
			n, err := streamReplace(&out, reader.r(), from, to)
			must(t, err)
			want := bytes.ReplaceAll(in, from, to)
			if !bytes.Equal(out.Bytes(), want) {
				t.Errorf("%s, %s: got %d bytes, want %d", name, reader.kind, out.Len(), len(want))
			}
			if n != int64(len(want)) {
				t.Errorf("%s, %s: reported %d bytes written, wrote %d", name, reader.kind, n, len(want))
			}
		}
	}
	// A self-overlapping needle: ReplaceAll's non-overlapping, left-to-right answer.
	var out bytes.Buffer
	_, err := streamReplace(&out, iotest.OneByteReader(strings.NewReader("aaaaa")), []byte("aa"), []byte("b"))
	must(t, err)
	if got := out.String(); got != "bba" {
		t.Errorf("self-overlapping needle: %q, want %q", got, "bba")
	}
}

func readString(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	must(t, err)
	return string(b)
}

// setRef replaces the value of the one reference of kind at path, failing if there is none.
func setRef(m *Manifest, path, kind, value string) {
	for i := range m.AbsoluteRefs {
		if m.AbsoluteRefs[i].Path == path && m.AbsoluteRefs[i].Kind == kind {
			m.AbsoluteRefs[i].Value = value
			return
		}
	}
	panic(fmt.Sprintf("fixture has no %s reference at %s", kind, path))
}

// dropRef removes the reference of kind at path, failing if there is none.
func dropRef(m *Manifest, path, kind string) {
	for i := range m.AbsoluteRefs {
		if m.AbsoluteRefs[i].Path == path && m.AbsoluteRefs[i].Kind == kind {
			m.AbsoluteRefs = append(m.AbsoluteRefs[:i], m.AbsoluteRefs[i+1:]...)
			return
		}
	}
	panic(fmt.Sprintf("fixture has no %s reference at %s", kind, path))
}
