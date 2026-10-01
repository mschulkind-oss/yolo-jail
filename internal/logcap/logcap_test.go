package logcap

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// overfull writes a log of whole numbered lines, just past MaxBytes, and returns its path and
// content.
func overfull(t *testing.T) (string, []byte) {
	t.Helper()
	var b bytes.Buffer
	for i := 0; b.Len() <= MaxBytes; i++ {
		b.WriteString("line ")
		b.WriteString(strings.Repeat("x", i%97))
		b.WriteString("\n")
	}
	path := filepath.Join(t.TempDir(), "host-service-x.log")
	if err := os.WriteFile(path, b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return path, b.Bytes()
}

func size(t *testing.T, path string) int64 {
	t.Helper()
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return st.Size()
}

// The bound itself: a log over the cap is emptied, and its newest lines — no more than the cap,
// starting at a whole line — become the one archive.
func TestTrimMovesTheNewestLinesToTheArchiveAndEmptiesTheLog(t *testing.T) {
	path, content := overfull(t)
	if err := Trim(path); err != nil {
		t.Fatal(err)
	}
	if got := size(t, path); got != 0 {
		t.Errorf("log is %d bytes after a trim, want 0", got)
	}
	archive, err := os.ReadFile(path + ArchiveSuffix)
	if err != nil {
		t.Fatalf("no archive: %v", err)
	}
	if len(archive) == 0 || len(archive) > MaxBytes {
		t.Errorf("archive is %d bytes, want 1..%d", len(archive), MaxBytes)
	}
	if !bytes.HasSuffix(content, archive) {
		t.Error("the archive is not the log's newest bytes")
	}
	if start := len(content) - len(archive); content[start-1] != '\n' {
		t.Errorf("the archive starts mid-line, at %q", archive[:min(20, len(archive))])
	}
}

// The reason it truncates instead of renaming: a writer holding the log keeps writing to the
// log, at its start, rather than into the archive.
func TestATrimmedLogKeepsTheInodeItsWriterHolds(t *testing.T) {
	path, _ := overfull(t)
	held, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	if err := Trim(path); err != nil {
		t.Fatal(err)
	}
	if _, err := held.WriteString("after the trim\n"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "after the trim\n" {
		t.Errorf("log = %q, want the holder's next line at offset 0 (no hole, not in the archive)", got)
	}
	archive, _ := os.ReadFile(path + ArchiveSuffix)
	if bytes.Contains(archive, []byte("after the trim")) {
		t.Error("the holder's write went into the archive: the log was renamed, not truncated")
	}
}

// One generation: a second trim replaces the archive rather than adding a second one.
func TestTrimKeepsOneArchivedGeneration(t *testing.T) {
	path, _ := overfull(t)
	if err := Trim(path); err != nil {
		t.Fatal(err)
	}
	second := bytes.Repeat([]byte("second generation\n"), MaxBytes/18+2)
	if err := os.WriteFile(path, second, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Trim(path); err != nil {
		t.Fatal(err)
	}
	archive, _ := os.ReadFile(path + ArchiveSuffix)
	if !bytes.HasPrefix(archive, []byte("second generation\n")) || bytes.Contains(archive, []byte("line ")) {
		t.Error("the archive is not the second generation alone")
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 2 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("dir holds %v, want the log and one archive", names)
	}
}

// Under the cap, and missing, are both left alone: no archive appears, nothing is created.
func TestTrimLeavesALogUnderTheCapAlone(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "small.log")
	if err := os.WriteFile(path, []byte("one line\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Trim(path); err != nil {
		t.Fatal(err)
	}
	if got := size(t, path); got != int64(len("one line\n")) {
		t.Errorf("a log under the cap changed size to %d", got)
	}
	if err := Trim(filepath.Join(dir, "absent.log")); err != nil {
		t.Errorf("trimming a missing log: %v", err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("dir holds %d entries, want only the small log", len(entries))
	}
}

// Open is what every spawn site calls: it trims, then hands back an appending descriptor.
func TestOpenTrimsBeforeItOpens(t *testing.T) {
	path, _ := overfull(t)
	f, err := Open(path, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("fresh\n"); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	if got, _ := os.ReadFile(path); string(got) != "fresh\n" {
		t.Errorf("log = %q, want only the line written after Open", got)
	}
	if _, err := os.Stat(path + ArchiveSuffix); err != nil {
		t.Errorf("Open did not archive the overfull log: %v", err)
	}
}

// A line longer than the whole cap still leaves an archive of exactly the cap: its newest bytes.
func TestTrimKeepsTheNewestBytesOfALineLongerThanTheCap(t *testing.T) {
	path := filepath.Join(t.TempDir(), "long.log")
	if err := os.WriteFile(path, bytes.Repeat([]byte("y"), MaxBytes+100), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Trim(path); err != nil {
		t.Fatal(err)
	}
	archive, err := os.ReadFile(path + ArchiveSuffix)
	if err != nil {
		t.Fatal(err)
	}
	if len(archive) != MaxBytes {
		t.Errorf("archive is %d bytes, want exactly %d", len(archive), MaxBytes)
	}
	if st, _ := os.Stat(path + ArchiveSuffix); st.Mode().Perm() != 0o600 {
		t.Errorf("archive mode = %v, want the log's own 0600", st.Mode().Perm())
	}
}
