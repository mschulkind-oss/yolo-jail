package prune

// jailwritable_test.go pins that `yolo prune --apply` never follows a link the jail planted in
// its own writable state (docs/reference/jail-home.md, "Host code in jail-writable state").
// prune runs on the HOST, as the host user, and walks trees a jail can write: every tracked
// workspace's overlay (`<ws>/.yolo/home`, reachable from the jail at /workspace/.yolo) and the
// machine store's cache and shared dirs, which every jail binds read-write. A link at a
// directory on the way had the age purge DELETE host files behind it, and the dedup replace host
// files with hardlinks to jail-writable ones (or jail files with hardlinks to host ones, which
// then hands the jail a writable name for the host file's inode).
//
// Each case plants the link, runs the production call, and checks the host side.

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// pruneLinkShape is one shape a planted link takes: plant puts a link at link and returns the
// host directory the link names, populated by fill, or "" for a dangling link, together with
// the check that the host side was left alone.
type pruneLinkShape struct {
	name  string
	plant func(t *testing.T, link string, fill func(hostDir string)) (verify func(t *testing.T))
}

var pruneLinkShapes = []pruneLinkShape{
	{"a link to an existing host directory", func(t *testing.T, link string, fill func(string)) func(*testing.T) {
		host := t.TempDir()
		fill(host)
		before := pruneTreeListing(t, host)
		pruneSymlink(t, host, link)
		return func(t *testing.T) {
			t.Helper()
			if after := pruneTreeListing(t, host); after != before {
				t.Errorf("prune changed the host directory behind the jail's link:\nbefore:\n%s\nafter:\n%s", before, after)
			}
		}
	}},
	{"a dangling link", func(t *testing.T, link string, _ func(string)) func(*testing.T) {
		dir := t.TempDir()
		pruneSymlink(t, filepath.Join(dir, "created-by-the-follow"), link)
		return func(t *testing.T) {
			t.Helper()
			if entries, _ := os.ReadDir(dir); len(entries) != 0 {
				t.Errorf("prune created %v in a host directory through the jail's link", entries)
			}
		}
	}},
}

// pruneSymlink replaces whatever is at link with a symbolic link to target.
func pruneSymlink(t *testing.T, target, link string) {
	t.Helper()
	must(t, os.MkdirAll(filepath.Dir(link), 0o755))
	must(t, os.RemoveAll(link))
	must(t, os.Symlink(target, link))
}

// swapForLink moves the real directory dir aside and puts a link to host in its place, the
// move a jail makes between prune's walk and its mutation.
func swapForLink(t *testing.T, dir, host string) {
	t.Helper()
	must(t, os.Rename(dir, dir+".moved-aside"))
	must(t, os.Symlink(host, dir))
}

// pruneTreeListing is every path below dir with its inode and content, for a before/after
// compare: a removal, a new name, and a name now naming a different inode all show.
func pruneTreeListing(t *testing.T, dir string) string {
	t.Helper()
	var out string
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(dir, p)
		out += rel
		if d.Type().IsRegular() {
			ino, _, _ := inode(p)
			data, _ := os.ReadFile(p)
			out += " ino=" + itoa(ino) + " = " + string(data)
		}
		out += "\n"
		return nil
	})
	return out
}

func itoa(n uint64) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for ; n > 0; n /= 10 {
		b = append([]byte{byte('0' + n%10)}, b...)
	}
	return string(b)
}

var pruneOld = time.Now().Add(-90 * 24 * time.Hour)

// THE AGENT-LOG PURGE through a linked directory on the way to an allowlisted log dir: `.yolo`,
// `.yolo/home`, or the agent's dir in it. The plain path resolved through the link and the purge
// deleted the old files in the host directory it named.
func TestPurgeAgentLogsNeverFollowsALinkedDirectory(t *testing.T) {
	for _, linked := range []struct{ link, below string }{
		{".yolo", filepath.Join("home", "copilot", "logs")},
		{filepath.Join(".yolo", "home"), filepath.Join("copilot", "logs")},
		{filepath.Join(".yolo", "home", "copilot"), "logs"},
	} {
		for _, shape := range pruneLinkShapes {
			t.Run(linked.link+"/"+shape.name, func(t *testing.T) {
				ws := t.TempDir()
				verify := shape.plant(t, filepath.Join(ws, linked.link), func(host string) {
					mkLog(t, filepath.Join(host, linked.below), "process-old.log", 64, pruneOld)
				})

				_, files := PurgeAgentLogs([]string{ws}, filepath.Join(t.TempDir(), "cache"), 30, true, time.Now())

				verify(t)
				if files != 0 {
					t.Errorf("purged %d files through the link at %s, want 0", files, linked.link)
				}
			})
		}
	}
}

// THE AGE PURGE with a directory swapped for a link between the walk and the removal, in a
// workspace's log dir and in the machine store's cache (which every jail binds read-write).
// The plain os.Remove resolved the swapped-in link and deleted the host file of the same name.
func TestPurgeNeverRemovesThroughALinkSwappedInMidWalk(t *testing.T) {
	for _, tc := range []struct {
		name  string
		purge func(ws, cache string) int
		dir   func(ws, cache string) string // the directory holding the old file
	}{
		{"agent logs", func(ws, cache string) int {
			_, f := PurgeAgentLogs([]string{ws}, cache, 30, true, time.Now())
			return f
		}, func(ws, _ string) string {
			return filepath.Join(ws, ".yolo", "home", "copilot", "logs", "sub")
		}},
		{"the cache", func(_, cache string) int {
			_, f := PurgeCacheByAge(cache, []string{"uv"}, nil, 30, true, time.Now())
			return f
		}, func(_, cache string) string {
			return filepath.Join(cache, "uv", "sub")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ws, cache := t.TempDir(), t.TempDir()
			dir := tc.dir(ws, cache)
			mkLog(t, dir, "old.log", 64, pruneOld)
			host := t.TempDir()
			hostFile := mkLog(t, host, "old.log", 64, pruneOld)

			swapped := false
			purgeBeforeRemove = func(string) {
				if !swapped {
					swapped = true
					swapForLink(t, dir, host)
				}
			}
			t.Cleanup(func() { purgeBeforeRemove = nil })

			tc.purge(ws, cache)

			if !swapped {
				t.Fatal("the purge never reached the removal: the fixture is not exercising the race")
			}
			if _, err := os.Lstat(hostFile); err != nil {
				t.Errorf("the purge removed the host file through the link swapped in at %s: %v", dir, err)
			}
		})
	}
}

// THE DEDUP WALK through a linked directory on the way to a dedupable subtree. The walk
// followed it and yielded the host directory's files, which the apply then hardlinked.
func TestDedupNeverFollowsALinkedDirectory(t *testing.T) {
	sub := dedupeSubtrees[0]
	for _, linked := range []struct{ link, below string }{
		{".yolo", filepath.Join("home", sub)},
		{filepath.Join(".yolo", "home"), sub},
	} {
		for _, shape := range pruneLinkShapes {
			t.Run(linked.link+"/"+shape.name, func(t *testing.T) {
				ws := t.TempDir()
				verify := shape.plant(t, filepath.Join(ws, linked.link), func(host string) {
					for _, n := range []string{"a", "b"} {
						p := filepath.Join(host, linked.below, "pkg", n)
						must(t, os.MkdirAll(filepath.Dir(p), 0o755))
						must(t, os.WriteFile(p, []byte("identical host content"), 0o644))
					}
				})

				entries := WalkDedupableWorkspaces([]string{ws})
				_, links := HardlinkDuplicateFiles(entries, true)

				verify(t)
				if len(entries) != 0 || links != 0 {
					t.Errorf("the walk yielded %v and linked %d through the link at %s, want nothing",
						entries, links, linked.link)
				}
			})
		}
	}
}

// THE DEDUP APPLY with a directory swapped for a link between the walk and the link, either
// before the files are hashed or after (the hash, beneath the root, refuses the first; only the
// link's own beneath-resolution refuses the second). On the
// DUPLICATE's side the plain path replaced the host file of the same name with a hardlink to the
// jail's file. On the CANONICAL's side it hardlinked the HOST file into the jail's tree, handing
// the jail a writable name for the host file's inode.
func TestDedupApplyNeverFollowsALinkSwappedInAfterTheWalk(t *testing.T) {
	content := []byte("identical content, both sides")
	for _, tc := range []struct {
		name string
		// root is the jail-writable tree the walk covers; walk runs it.
		root func(ws, gs string) string
		walk func(ws, gs string) []Entry
	}{
		{"a workspace overlay", func(ws, _ string) string {
			return filepath.Join(ws, ".yolo", "home", dedupeSubtrees[0])
		}, func(ws, _ string) []Entry { return WalkDedupableWorkspaces([]string{ws}) }},
		{"the machine store", func(_, gs string) string {
			return filepath.Join(gs, "home", ".some-shared-dir")
		}, func(_, gs string) []Entry { return WalkGlobalDedupable(gs) }},
	} {
		for _, when := range []string{"after the walk", "after the hash"} {
			for _, side := range []string{"x", "y"} { // x is canonical (walked first), y the duplicate
				t.Run(tc.name+"/"+when+"/"+map[string]string{"x": "the canonical", "y": "the duplicate"}[side], func(t *testing.T) {
					ws, gs := t.TempDir(), t.TempDir()
					root := tc.root(ws, gs)
					for _, d := range []string{"x", "y"} {
						p := filepath.Join(root, d, "f")
						must(t, os.MkdirAll(filepath.Dir(p), 0o755))
						must(t, os.WriteFile(p, content, 0o644))
					}
					entries := tc.walk(ws, gs)
					if len(entries) != 2 {
						t.Fatalf("the walk yielded %v, want the two fixture files", entries)
					}
					if filepath.Base(filepath.Dir(entries[0].Path)) != "x" {
						t.Fatalf("the walk order changed (%v): the canonical must be x", entries)
					}
					host := t.TempDir()
					must(t, os.WriteFile(filepath.Join(host, "f"), content, 0o644))
					before := pruneTreeListing(t, host)
					swapped := false
					swap := func() {
						if !swapped {
							swapped = true
							swapForLink(t, filepath.Join(root, side), host)
						}
					}
					if when == "after the walk" {
						swap()
					} else {
						dedupBeforeLink = swap
						t.Cleanup(func() { dedupBeforeLink = nil })
					}

					_, links := HardlinkDuplicateFiles(entries, true)

					if !swapped {
						t.Fatal("the dedup never reached the link: the fixture is not exercising the race")
					}

					if after := pruneTreeListing(t, host); after != before {
						t.Errorf("the dedup changed the host directory swapped in at %s:\nbefore:\n%s\nafter:\n%s",
							side, before, after)
					}
					other := map[string]string{"x": "y", "y": "x"}[side]
					if sameInode(t, filepath.Join(host, "f"), filepath.Join(root, other, "f")) {
						t.Errorf("the dedup linked the host file and the jail's %s/f into one inode", other)
					}
					if links != 0 {
						t.Errorf("made %d links through the swapped-in link, want 0", links)
					}
				})
			}
		}
	}
}
