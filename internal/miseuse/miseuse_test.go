package miseuse

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// lsJSON renders `mise ls --json` output naming the given install paths, every one installed.
func lsJSON(t *testing.T, paths map[string][]string) []byte {
	t.Helper()
	out := map[string][]map[string]any{}
	for tool, ps := range paths {
		for _, p := range ps {
			out[tool] = append(out[tool], map[string]any{"version": filepath.Base(p), "install_path": p, "installed": true})
		}
	}
	b, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestNeededIsWhatMiseWouldKeepPlusWhatIsCurrent pins what a jail records: every installed version
// its workspace's tracked configs still name (installed minus prunable), joined with what its
// current configs resolve to, so a version mise's prune logic misjudged can only be kept.
func TestNeededIsWhatMiseWouldKeepPlusWhatIsCurrent(t *testing.T) {
	store := "/mise"
	installed := lsJSON(t, map[string][]string{
		"node":                                  {"/mise/installs/node/20.1.0", "/mise/installs/node/22.5.0"},
		"python":                                {"/mise/installs/python/3.11.9", "/mise/installs/python/3.12.4"},
		"go:honnef.co/go/tools/cmd/staticcheck": {"/mise/installs/go-honnef-co-go-tools-cmd-staticcheck/2026.1"},
	})
	prunable := lsJSON(t, map[string][]string{
		"node":   {"/mise/installs/node/20.1.0"},
		"python": {"/mise/installs/python/3.11.9", "/mise/installs/python/3.12.4"},
	})
	// python 3.12.4 is prunable by mise's reckoning and current all the same: kept.
	current := lsJSON(t, map[string][]string{"python": {"/mise/installs/python/3.12.4"}})
	got, err := Needed(store, installed, prunable, current)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"go-honnef-co-go-tools-cmd-staticcheck/2026.1", "node/22.5.0", "python/3.12.4"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Needed = %v, want %v", got, want)
	}
	if _, err := Needed(store, []byte("mise WARN something"), prunable, current); err == nil {
		t.Error("output that is not mise's JSON must be an error, never an empty set: an empty set " +
			"reads as \"this jail uses nothing\"")
	}
}

// TestInstallRelIsOneVersionDirectoryBeneathTheStore: a record names install directories, and
// only exactly one version directory beneath installs/ counts, so no record can name a path the
// host would read as something else.
func TestInstallRelIsOneVersionDirectoryBeneathTheStore(t *testing.T) {
	for in, want := range map[string]string{
		"/mise/installs/node/22.5.0":          "node/22.5.0",
		"/mise/installs/node/22.5.0/":         "node/22.5.0",
		"/mise/installs/node":                 "",
		"/mise/installs/node/22.5.0/bin":      "",
		"/mise/installs/node/../cargo":        "",
		"/mise/installs/node/.yolo-reclaim-1": "",
		"/mise/cargo/bin":                     "",
		"/elsewhere/installs/node/22.5.0":     "",
	} {
		if got := InstallRel("/mise", in); got != want {
			t.Errorf("InstallRel(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestWriteReplacesOneRecordAndNeverStartsTheClock: a jail's writes replace its own file, and no
// jail's write creates the since marker — the start of the window the host waits out. A nested or
// development jail running newer code than the host's yolo would otherwise start that clock while
// the host's own launches still record nothing.
func TestWriteReplacesOneRecordAndNeverStartsTheClock(t *testing.T) {
	store := t.TempDir()
	name := NewName()
	first := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	if err := Write(store, name, Record{Workspace: "/w", Recorded: first, Installs: []string{"node/22.5.0"}}); err != nil {
		t.Fatal(err)
	}
	if err := Write(store, name, Record{Workspace: "/w", Recorded: first.Add(time.Hour), Installs: []string{"node/24.1.0"}}); err != nil {
		t.Fatal(err)
	}
	if err := Write(store, NewName(), Record{Workspace: "/v", Recorded: first.Add(2 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	c, err := ReadAll(store)
	if err != nil {
		t.Fatal(err)
	}
	if !c.Since.IsZero() {
		t.Errorf("a jail's record started the clock (Since = %v); only a host launch may", c.Since)
	}
	if len(c.Records) != 2 {
		t.Fatalf("%d records, want 2 (one per writer): %+v", len(c.Records), c.Records)
	}
	for _, r := range c.Records {
		if r.Err != nil {
			t.Fatalf("record %s unreadable: %v", r.File, r.Err)
		}
		if r.Record.Workspace == "/w" && !reflect.DeepEqual(r.Record.Installs, []string{"node/24.1.0"}) {
			t.Errorf("the second write did not replace the first: %v", r.Record.Installs)
		}
	}
	entries, _ := os.ReadDir(filepath.Join(store, DirName))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			t.Errorf("a write left its temporary %s behind", e.Name())
		}
	}
}

// TestMarkSinceIsTheFirstHostLaunchsAndNeverMoves: the clock starts at the first host launch that
// binds the store, and a later launch leaves it where it is.
func TestMarkSinceIsTheFirstHostLaunchsAndNeverMoves(t *testing.T) {
	store := t.TempDir()
	first := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	for _, at := range []time.Time{first, first.Add(48 * time.Hour)} {
		if err := MarkSince(store, at); err != nil {
			t.Fatal(err)
		}
	}
	c, err := ReadAll(store)
	if err != nil {
		t.Fatal(err)
	}
	if !c.Since.Equal(first) {
		t.Fatalf("Since = %v, want the first launch's %v", c.Since, first)
	}
}

// TestAWriterExpiresOnlyOldWriterFiles: the writers bound the directory, removing a record or a
// temporary only once it is a week past the window, and nothing that is not a writer's file.
func TestAWriterExpiresOnlyOldWriterFiles(t *testing.T) {
	store := t.TempDir()
	dir := filepath.Join(store, DirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// The wall clock: a file's age is how long it has been on disk, whatever the record says.
	now := time.Now()
	old := now.Add(-expireAfter - time.Hour)
	recent := now.Add(-Window + time.Hour)
	plant := func(name string, mtime time.Time) {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(`{}`), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, mtime, mtime); err != nil {
			t.Fatal(err)
		}
	}
	plant("0123456789abcdef.json", old)
	plant(".0123456789abcdef.json.42", old)
	plant("fedcba9876543210.json", recent)
	plant("notes.txt", old)
	if err := Write(store, NewName(), Record{Workspace: "/w", Recorded: now}); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]bool{
		"0123456789abcdef.json":     false,
		".0123456789abcdef.json.42": false,
		"fedcba9876543210.json":     true,
		"notes.txt":                 true,
	} {
		_, err := os.Lstat(filepath.Join(dir, name))
		if got := err == nil; got != want {
			t.Errorf("%s present = %v, want %v", name, got, want)
		}
	}
}

// TestALinkAtTheRecordDirectoryIsRefused is the cross-jail case. Every jail writes the store, so
// one can replace the record directory with a link; in the NEXT jail that link resolves into that
// jail's own files, and the writer expires old files. A writer must refuse it, never follow it.
func TestALinkAtTheRecordDirectoryIsRefused(t *testing.T) {
	store := t.TempDir()
	victim := t.TempDir()
	old := time.Now().Add(-expireAfter - 24*time.Hour)
	keep := filepath.Join(victim, "0123456789abcdef.json")
	if err := os.WriteFile(keep, []byte("the user's"), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = os.Chtimes(keep, old, old)
	if err := os.Symlink(victim, filepath.Join(store, DirName)); err != nil {
		t.Fatal(err)
	}
	if err := Write(store, NewName(), Record{Workspace: "/w"}); err == nil {
		t.Error("Write wrote through a link at the record directory")
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("a file the link led to was removed: %v", err)
	}
	if entries, _ := os.ReadDir(victim); len(entries) != 1 {
		t.Errorf("the writer created files where the link led: %v", entries)
	}
	if _, err := ReadAll(store); err == nil {
		t.Error("ReadAll read through a link at the record directory; it must say it cannot read it")
	}

	// And a link that stays INSIDE the store, which os.Root alone would follow: the writer would
	// then expire files in whatever store directory it names.
	store = t.TempDir()
	inner := filepath.Join(store, "installs")
	if err := os.MkdirAll(inner, 0o755); err != nil {
		t.Fatal(err)
	}
	keep = filepath.Join(inner, "0123456789abcdef.json")
	if err := os.WriteFile(keep, []byte("not a record"), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = os.Chtimes(keep, old, old)
	if err := os.Symlink("installs", filepath.Join(store, DirName)); err != nil {
		t.Fatal(err)
	}
	if err := Write(store, NewName(), Record{Workspace: "/w"}); err == nil {
		t.Error("Write wrote through a link at the record directory that stays inside the store")
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("a file in the store the link named was expired: %v", err)
	}
}

// TestAnUnparseableRecordIsReportedNotDropped: a reader must be able to tell "no record" from
// "a record I cannot read", because only the first is evidence of nothing.
func TestAnUnparseableRecordIsReportedNotDropped(t *testing.T) {
	store := t.TempDir()
	if err := Write(store, NewName(), Record{Workspace: "/w"}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store, DirName, "00000000000000ff.json"), []byte("{trunc"), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := ReadAll(store)
	if err != nil {
		t.Fatal(err)
	}
	bad := 0
	for _, r := range c.Records {
		if r.Err != nil {
			bad++
			if r.Modified.IsZero() {
				t.Error("an unreadable record carries no modification time, so a reader cannot age it")
			}
		}
	}
	if bad != 1 || len(c.Records) != 2 {
		t.Fatalf("records = %+v, want two with one unreadable", c.Records)
	}
	if c2, err := ReadAll(t.TempDir()); err != nil || len(c2.Records) != 0 || !c2.Since.IsZero() {
		t.Errorf("a store with no records = %+v, %v; want an empty census and no error", c2, err)
	}
}

// TestABrokenSinceMarkerIsReportedAndRepaired: a marker created but never written — a write that
// failed, on the full disk this feature exists for — used to read as "no marker" forever, since
// every later MarkSince saw it exist and left it alone. ReadAll says it cannot be read, and the
// next host launch's MarkSince replaces it, starting the clock from that launch.
func TestABrokenSinceMarkerIsReportedAndRepaired(t *testing.T) {
	for name, content := range map[string]string{"empty": "", "garbage": "not a time\n"} {
		t.Run(name, func(t *testing.T) {
			store := t.TempDir()
			if err := os.MkdirAll(filepath.Join(store, DirName), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(store, DirName, SinceName), []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
			c, err := ReadAll(store)
			if err != nil {
				t.Fatal(err)
			}
			if !c.Since.IsZero() || c.SinceErr == nil {
				t.Fatalf("a broken marker read as Since=%v SinceErr=%v; want it reported", c.Since, c.SinceErr)
			}
			at := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
			if err := MarkSince(store, at); err != nil {
				t.Fatal(err)
			}
			if err := MarkSince(store, at.Add(time.Hour)); err != nil {
				t.Fatal(err)
			}
			c, err = ReadAll(store)
			if err != nil {
				t.Fatal(err)
			}
			if !c.Since.Equal(at) || c.SinceErr != nil {
				t.Fatalf("after a host launch the marker reads Since=%v SinceErr=%v; want %v", c.Since, c.SinceErr, at)
			}
		})
	}
}

// TestMarkSinceNeverLeavesAPartialMarker: the marker appears whole or not at all, so no failed
// write can leave the empty file every later launch would leave alone. Its temporary goes too.
func TestMarkSinceNeverLeavesAPartialMarker(t *testing.T) {
	store := t.TempDir()
	at := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	if err := MarkSince(store, at); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(store, DirName))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != SinceName {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("the record directory holds %v after MarkSince, want only %s", names, SinceName)
	}
}
