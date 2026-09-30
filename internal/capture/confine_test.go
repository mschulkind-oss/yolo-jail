package capture

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// confine_test.go pins confine.go: a manifest can place files only inside the home it is
// materialized into, and a CONFINED materialize (the host agent floor's) writes through no link
// beneath that home. Every refusal is asserted twice — the error, and that nothing was written
// where the entry pointed.

// resolvedTempDir is t.TempDir() with its symlinks resolved, so a path compared against what
// the materialize wrote agrees on darwin, whose TMPDIR sits under /var -> /private/var.
func resolvedTempDir(t *testing.T) string {
	t.Helper()
	d, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// craftedEntry admits an entry whose tree holds files (path → body) and links (path → target),
// under a manifest that says exactly entries — nothing derived from the tree, which is the
// point: the manifest is the capture jail's claim, and these tests are about a claim that lies.
func craftedEntry(t *testing.T, files, links map[string]string, entries []ManifestEntry) *Entry {
	t.Helper()
	store := &Store{Dir: resolvedTempDir(t)}
	staged, err := store.Stage("crafted")
	if err != nil {
		t.Fatal(err)
	}
	tree := TreeDir(staged)
	if err := os.MkdirAll(tree, 0o755); err != nil {
		t.Fatal(err)
	}
	for rel, body := range files {
		p := filepath.Join(tree, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for rel, target := range links {
		p := filepath.Join(tree, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, p); err != nil {
			t.Fatal(err)
		}
	}
	if err := WriteManifest(staged, &Manifest{
		Schema: ManifestSchema, Home: "/home/agent", Platform: Platform(),
		Surfaces: []string{".local"}, Excluded: []string{}, Entries: entries,
		AbsoluteRefs: []AbsoluteRef{}, RefScan: RefScanFull, Relocatable: true,
	}); err != nil {
		t.Fatal(err)
	}
	entry, err := store.AdmitEntry(staged)
	if err != nil {
		t.Fatal(err)
	}
	return entry
}

func dirEntry(p string) ManifestEntry { return ManifestEntry{Path: p, Kind: KindDir, Mode: "0755"} }
func fileEntry(p string) ManifestEntry {
	return ManifestEntry{Path: p, Kind: KindFile, Mode: "0755", Size: 1}
}
func linkEntry(p, target string) ManifestEntry {
	return ManifestEntry{Path: p, Kind: KindSymlink, Target: target}
}

// climb is a home-relative path that cleans to abs from any home: enough `..` to reach the root.
func climb(abs string) string {
	return ".local/" + strings.Repeat("../", 64) + strings.TrimPrefix(filepath.ToSlash(abs), "/")
}

// TestMaterializeRefusesAManifestThatPlacesAnythingOutsideTheHome: every shape of entry that would
// land outside the home is refused BEFORE the first write — confined or not, since the check is
// every materialize's — and nothing appears where it pointed, nor anything in the home.
func TestMaterializeRefusesAManifestThatPlacesAnythingOutsideTheHome(t *testing.T) {
	outside := resolvedTempDir(t)
	cases := []struct {
		name    string
		files   map[string]string
		links   map[string]string
		entries []ManifestEntry
		escaped string
	}{
		{name: "a path that climbs with ..", entries: []ManifestEntry{dirEntry(".local"),
			dirEntry(climb(filepath.Join(outside, "by-dotdot")))},
			escaped: filepath.Join(outside, "by-dotdot")},
		{name: "a path beneath a link the same run creates",
			links: map[string]string{".local/evil": outside},
			entries: []ManifestEntry{dirEntry(".local"), linkEntry(".local/evil", outside),
				dirEntry(".local/evil/by-link")},
			escaped: filepath.Join(outside, "by-link")},
		{name: "a path listed twice, first as a link",
			links: map[string]string{".local/twice": outside},
			entries: []ManifestEntry{dirEntry(".local"), linkEntry(".local/twice", outside),
				dirEntry(".local/twice"), dirEntry(".local/twice/by-dup")},
			escaped: filepath.Join(outside, "by-dup")},
		{name: "an absolute path", entries: []ManifestEntry{dirEntry(filepath.Join(outside, "by-abs"))},
			escaped: filepath.Join(outside, "by-abs")},
		{name: "a path beneath a file", files: map[string]string{".local/f": "x"},
			entries: []ManifestEntry{dirEntry(".local"), fileEntry(".local/f"), dirEntry(".local/f/x")}},
		{name: "an unclean path", entries: []ManifestEntry{dirEntry(".local"), dirEntry(".local/./x")}},
		{name: "a doubled separator", entries: []ManifestEntry{dirEntry(".local"), dirEntry(".local//x")}},
		{name: "a kind no capture writes", entries: []ManifestEntry{{Path: ".local/p", Kind: "fifo"}}},
		{name: "the home itself", entries: []ManifestEntry{dirEntry(".")}},
	}
	for _, confined := range []bool{false, true} {
		for _, c := range cases {
			t.Run(fmt.Sprintf("%s/confined=%v", c.name, confined), func(t *testing.T) {
				entry := craftedEntry(t, c.files, c.links, c.entries)
				home := filepath.Join(resolvedTempDir(t), "home")
				_, err := Materialize(MaterializeOptions{Entry: entry, Home: home, Confined: confined})
				if err == nil {
					t.Fatalf("confined=%v: the manifest was materialized", confined)
				}
				if !strings.Contains(err.Error(), "manifest no capture writes") {
					t.Errorf("confined=%v: the refusal does not say what is wrong: %v", confined, err)
				}
				if c.escaped != "" {
					if _, serr := os.Lstat(c.escaped); serr == nil {
						t.Errorf("confined=%v: %s was created outside the home", confined, c.escaped)
					}
				}
				if _, serr := os.Lstat(home); serr == nil {
					t.Errorf("confined=%v: the refusal wrote into the home first", confined)
				}
			})
		}
	}
}

// TestMaterializeRefusesAFileEntryTheStoreHoldsAsALink: the manifest says file and the tree holds
// a link. Copied, the link would be read through — a file outside the store copied into the home
// — and hardlinked it would put an undeclared link in the home. Refused, and nothing is placed.
func TestMaterializeRefusesAFileEntryTheStoreHoldsAsALink(t *testing.T) {
	secret := filepath.Join(resolvedTempDir(t), "secret")
	if err := os.WriteFile(secret, []byte("not the vendor's\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	entry := craftedEntry(t, nil, map[string]string{".local/bin/tool": secret},
		[]ManifestEntry{dirEntry(".local"), dirEntry(".local/bin"), fileEntry(".local/bin/tool")})
	home := filepath.Join(resolvedTempDir(t), "home")
	_, err := Materialize(MaterializeOptions{Entry: entry, Home: home})
	if err == nil || !strings.Contains(err.Error(), "the store holds a symlink") {
		t.Fatalf("a link recorded as a file was materialized (err %v)", err)
	}
	if _, serr := os.Lstat(filepath.Join(home, ".local", "bin", "tool")); serr == nil {
		t.Error("the link's target, or the link, was placed in the home")
	}
}

// TestAConfinedMaterializeWritesOnlyIntoAnEmptyHomeAndOnlyTheSurfaces: the two up-front rules a
// confined materialize adds. An entry outside the capture surfaces is ordinary to a jail's home,
// and refused for the floor's; a home that already holds something is refused before a write.
func TestAConfinedMaterializeWritesOnlyIntoAnEmptyHomeAndOnlyTheSurfaces(t *testing.T) {
	entry := craftedEntry(t, map[string]string{".ssh/authorized_keys": "k"}, nil,
		[]ManifestEntry{dirEntry(".ssh"), fileEntry(".ssh/authorized_keys")})
	jailHome := filepath.Join(resolvedTempDir(t), "home")
	if _, err := Materialize(MaterializeOptions{Entry: entry, Home: jailHome}); err != nil {
		t.Fatalf("an unconfined materialize of an off-surface entry: %v", err)
	}
	floorHome := filepath.Join(resolvedTempDir(t), "home")
	_, err := Materialize(MaterializeOptions{Entry: entry, Home: floorHome, Confined: true})
	if err == nil || !strings.Contains(err.Error(), "outside the capture surfaces") {
		t.Fatalf("a confined materialize placed an off-surface entry (err %v)", err)
	}
	if _, serr := os.Lstat(floorHome); serr == nil {
		t.Error("the refusal wrote into the home first")
	}

	ok := craftedEntry(t, map[string]string{".local/bin/tool": "t"}, nil,
		[]ManifestEntry{dirEntry(".local"), dirEntry(".local/bin"), fileEntry(".local/bin/tool")})
	busy := resolvedTempDir(t)
	if err := os.WriteFile(filepath.Join(busy, "already"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Materialize(MaterializeOptions{Entry: ok, Home: busy, Confined: true}); err == nil ||
		!strings.Contains(err.Error(), "not empty") {
		t.Fatalf("a confined materialize into a populated home was not refused (err %v)", err)
	}
	fresh := filepath.Join(resolvedTempDir(t), "home")
	if _, err := Materialize(MaterializeOptions{Entry: ok, Home: fresh, Confined: true}); err != nil {
		t.Fatalf("a confined materialize of an honest entry into a fresh home: %v", err)
	}
	if _, err := os.Stat(filepath.Join(fresh, ".local", "bin", "tool")); err != nil {
		t.Errorf("the honest entry was not placed: %v", err)
	}
}

// TestConfinedParentsRefusesALinkOnTheWay: the Lstat walk a confined materialize makes before each
// entry. A link anywhere between the home and the entry is refused; a component that does not
// exist yet ends the walk, since the materialize creates it as a real directory.
func TestConfinedParentsRefusesALinkOnTheWay(t *testing.T) {
	home := resolvedTempDir(t)
	if err := os.MkdirAll(filepath.Join(home, ".local", "real"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(resolvedTempDir(t), filepath.Join(home, ".local", "link")); err != nil {
		t.Fatal(err)
	}
	for rel, self := range map[string]bool{".local/link/x": false, ".local/link": true} {
		if err := confinedParents(home, rel, self); err == nil {
			t.Errorf("confinedParents(%s, self=%v) passed through a link", rel, self)
		}
	}
	for _, rel := range []string{".local/real/x", ".local/new/deeper/x", ".local/link"} {
		if err := confinedParents(home, rel, false); err != nil {
			t.Errorf("confinedParents(%s): %v", rel, err)
		}
	}
}

// TestAConfinedMaterializeFollowsNoLinkACaseInsensitiveHomeAliases is the one case the entry
// checks cannot see, driven through Materialize: `.local/share/Evil` is a link and
// `.local/share/evil/made` a different path — to the manifest. On a case-insensitive filesystem
// (macOS's default) they are one directory, and only the Lstat walk stops the write through it.
// Skipped where the temp dir is case-sensitive, which on Linux it is.
func TestAConfinedMaterializeFollowsNoLinkACaseInsensitiveHomeAliases(t *testing.T) {
	probe := resolvedTempDir(t)
	if err := os.WriteFile(filepath.Join(probe, "Probe"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(probe, "probe")); err != nil {
		t.Skip("the temp dir's filesystem is case-sensitive; the aliasing this pins cannot occur here")
	}
	outside := resolvedTempDir(t)
	entry := craftedEntry(t, nil, map[string]string{".local/share/Evil": outside},
		[]ManifestEntry{dirEntry(".local"), dirEntry(".local/share"), linkEntry(".local/share/Evil", outside),
			dirEntry(".local/share/evil"), dirEntry(".local/share/evil/made")})
	home := filepath.Join(resolvedTempDir(t), "home")
	if _, err := Materialize(MaterializeOptions{Entry: entry, Home: home, Confined: true}); err == nil {
		t.Error("the confined materialize wrote through a case-aliased link")
	}
	if _, err := os.Lstat(filepath.Join(outside, "made")); err == nil {
		t.Errorf("%s was created through the link", filepath.Join(outside, "made"))
	}
}
