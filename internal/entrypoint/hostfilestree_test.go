package entrypoint

// hostfilestree_test.go pins the host `files` render, whose whole risk is that the kind is
// declared CombineExclusive — "the pack owns this path" — and a naive reading of that in a
// real home means overwriting whatever the user has there.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/hostskills"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// filesPack builds a pack declaring one `files` contribution, with the given tree written
// under its root. mode applies to every file, so a test can ship an executable.
func filesPack(t *testing.T, name, from, into string, tree map[string]string, mode os.FileMode) *packload.Pack {
	t.Helper()
	root := t.TempDir()
	for rel, content := range tree {
		full := filepath.Join(root, from, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(full, mode); err != nil {
			t.Fatal(err)
		}
	}
	return &packload.Pack{
		Name: name, Root: root,
		Decl: &packdecl.Manifest{Contributes: []packdecl.Contribution{{
			Kind: packdecl.KindFiles, From: from, Into: into,
		}}},
		MayAccessHost: true,
	}
}

func filesReq(t *testing.T) HostFilesRequest {
	t.Helper()
	return HostFilesRequest{
		Manifest:    &hostskills.Manifest{Entries: map[string]string{}},
		ArchiveRoot: hostskills.ArchiveRoot(filepath.Join(t.TempDir(), "archive")),
		Stamp:       "20260802-000000",
	}
}

// The headline: a tree lands, recursively, read-only.
func TestHostFilesTreeRenders(t *testing.T) {
	home := t.TempDir()
	p := filesPack(t, "matt-core", "tree", ".claude/mine", map[string]string{
		"top.txt":        "top",
		"nested/deep.md": "deep",
	}, 0o644)
	results, err := RenderHostFiles(p, home, filesReq(t), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("want one result per file, got %+v", results)
	}
	for rel, want := range map[string]string{"top.txt": "top", "nested/deep.md": "deep"} {
		path := filepath.Join(home, ".claude", "mine", filepath.FromSlash(rel))
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s: %v", rel, err)
		}
		if string(data) != want {
			t.Errorf("%s = %q, want %q", rel, data, want)
		}
		fi, _ := os.Stat(path)
		if fi.Mode().Perm() != 0o444 {
			t.Errorf("%s mode = %o, want 444 (the :ro mount's closest filesystem analogue)",
				rel, fi.Mode().Perm())
		}
	}
}

// THE ownership test. `files` being sole-owned decides which PACK may claim a path; it is
// not a licence over a file the user put there. An occupied path yolo cannot prove it wrote
// is refused BY NAME and left byte-for-byte alone.
func TestHostFilesRefusesUserOwnedPath(t *testing.T) {
	home := t.TempDir()
	dest := filepath.Join(home, ".claude", "mine", "top.txt")
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest, []byte("MINE"), 0o644); err != nil {
		t.Fatal(err)
	}
	p := filesPack(t, "matt-core", "tree", ".claude/mine",
		map[string]string{"top.txt": "FROM PACK"}, 0o644)

	results, err := RenderHostFiles(p, home, filesReq(t), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || !strings.HasPrefix(results[0].Action, "refused") {
		t.Fatalf("want a refusal for the user's file, got %+v", results)
	}
	data, _ := os.ReadFile(dest)
	if string(data) != "MINE" {
		t.Errorf("the user's file was overwritten: %q", data)
	}
}

// Per-FILE, not per-tree: a tree where the user owns ONE path must still deliver the others.
// A whole-tree verdict would either refuse everything over one file or clobber that one.
func TestHostFilesRefusalIsPerFile(t *testing.T) {
	home := t.TempDir()
	mine := filepath.Join(home, ".claude", "mine", "b.txt")
	if err := os.MkdirAll(filepath.Dir(mine), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mine, []byte("MINE"), 0o644); err != nil {
		t.Fatal(err)
	}
	p := filesPack(t, "matt-core", "tree", ".claude/mine", map[string]string{
		"a.txt": "pack a", "b.txt": "pack b", "c.txt": "pack c",
	}, 0o644)

	results, err := RenderHostFiles(p, home, filesReq(t), false)
	if err != nil {
		t.Fatal(err)
	}
	var refused, rendered int
	for _, r := range results {
		if strings.HasPrefix(r.Action, "refused") {
			refused++
		} else {
			rendered++
		}
	}
	if refused != 1 || rendered != 2 {
		t.Errorf("want 1 refused + 2 rendered, got %d/%d: %+v", refused, rendered, results)
	}
	if data, _ := os.ReadFile(mine); string(data) != "MINE" {
		t.Errorf("the user's file was overwritten: %q", data)
	}
	for _, rel := range []string{"a.txt", "c.txt"} {
		if _, err := os.Stat(filepath.Join(home, ".claude", "mine", rel)); err != nil {
			t.Errorf("%s should still have been delivered: %v", rel, err)
		}
	}
}

// The fzf case: an executable in the pack arrives executable, at 0o555. The consumer's
// allow_exec opt-in was already enforced when the tree was staged, so honoring the bit here
// needs no second gate — and refusing it would make the script undeliverable again.
func TestHostFilesCarriesExecBitFromPack(t *testing.T) {
	home := t.TempDir()
	p := filesPack(t, "claude-fzf", "files", ".claude",
		map[string]string{"file-suggestion.sh": "#!/bin/sh\nfd | fzf\n"}, 0o755)

	if _, err := RenderHostFiles(p, home, filesReq(t), false); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(home, ".claude", "file-suggestion.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o555 {
		t.Errorf("mode = %o, want 555 — an executable the pack ships must arrive runnable, "+
			"or the agent configured to run it gets EACCES", fi.Mode().Perm())
	}
}

// Idempotence: a second assert produces identical content and does not trip its own
// read-only mode (a 0o444 file cannot be reopened for writing by a non-root user, which is
// exactly the bug the host_files readonly path had to solve).
func TestHostFilesIsIdempotent(t *testing.T) {
	home := t.TempDir()
	p := filesPack(t, "matt-core", "tree", ".claude/mine",
		map[string]string{"a.txt": "content"}, 0o644)
	req := filesReq(t)

	if _, err := RenderHostFiles(p, home, req, false); err != nil {
		t.Fatal(err)
	}
	results, err := RenderHostFiles(p, home, req, false)
	if err != nil {
		t.Fatalf("second assert must not fail on its own read-only mode: %v", err)
	}
	for _, r := range results {
		if strings.HasPrefix(r.Action, "refused") {
			t.Errorf("second assert refused its OWN output: %+v", r)
		}
	}
	data, _ := os.ReadFile(filepath.Join(home, ".claude", "mine", "a.txt"))
	if string(data) != "content" {
		t.Errorf("content drifted across applies: %q", data)
	}
}

// A single-FILE source means `into` names the file itself — the same meaning the jail gives
// it, so one pack.json cannot deliver to two different paths depending on the notch.
func TestHostFilesSingleFileIntoNamesTheFile(t *testing.T) {
	home := t.TempDir()
	p := filesPack(t, "matt-core", "one.txt", ".claude/renamed.txt",
		map[string]string{}, 0o644)
	// filesPack writes nothing for an empty tree, so create the single-file source directly.
	if err := os.WriteFile(filepath.Join(p.Root, "one.txt"), []byte("solo"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := RenderHostFiles(p, home, filesReq(t), false); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(home, ".claude", "renamed.txt"))
	if err != nil {
		t.Fatalf("`into` must name the destination FILE for a single-file source: %v", err)
	}
	if string(data) != "solo" {
		t.Errorf("content = %q, want %q", data, "solo")
	}
}

// Observe writes nothing while still reporting what it would do.
func TestHostFilesObserveWritesNothing(t *testing.T) {
	home := t.TempDir()
	p := filesPack(t, "matt-core", "tree", ".claude/mine",
		map[string]string{"a.txt": "x"}, 0o644)
	results, err := RenderHostFiles(p, home, filesReq(t), true)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Action != "would render" {
		t.Fatalf("observe should report a would-render, got %+v", results)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "mine")); !os.IsNotExist(err) {
		t.Error("observe wrote to the home")
	}
}

// A missing source is a pack-authoring problem (a typo'd `from`, an only/exclude filter that
// removed the tree), reported by name rather than failing the whole apply — and never a
// silent skip, which is the failure mode this body of work exists to remove.
func TestHostFilesMissingSourceIsReported(t *testing.T) {
	home := t.TempDir()
	p := filesPack(t, "matt-core", "tree", ".claude/mine", map[string]string{}, 0o644)
	// No tree was written (empty map), so the source path does not exist.
	results, err := RenderHostFiles(p, home, filesReq(t), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || !strings.Contains(results[0].Action, "source is missing") {
		t.Fatalf("a missing source must be reported by name, got %+v", results)
	}
}
