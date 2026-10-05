package hostfloor

// fixture_test.go builds each test's floor over floortest's fake Node distribution and npm
// registry, under a temp HOME, on this machine's own platform or on Linux (newLinuxWorld), the
// floor always on the platform its distribution serves — and a fake capture store for the
// installer recipe. No test here touches the real internet, the real ~/.local, or a real agent.

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/capture"
	"github.com/mschulkind-oss/yolo-jail/internal/hostfloor/floortest"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// world is one test's fake distribution, registry and floor.
type world struct {
	t       *testing.T
	dist    *floortest.Dist
	plat    string
	version string
	out     *syncBuffer
	floor   *Floor
	// root holds the world's HOME and its stand-ins for the machine-wide hint folders (machineDir).
	root string
}

// syncBuffer is a bytes.Buffer safe for the concurrent writers a two-launch test has.
type syncBuffer struct {
	mu  chan struct{}
	buf bytes.Buffer
}

func newSyncBuffer() *syncBuffer { return &syncBuffer{mu: make(chan struct{}, 1)} }

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu <- struct{}{}
	defer func() { <-b.mu }()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu <- struct{}{}
	defer func() { <-b.mu }()
	return b.buf.String()
}

func resolvedTemp(t *testing.T) string { return floortest.ResolvedTemp(t) }

// newWorld builds a floor on this machine's platform whose Node comes from a fake distribution
// (its shipped release pinned by digest), and whose installers inherit an ambient environment full
// of the variables that would redirect an npm install — which the floor must drop.
func newWorld(t *testing.T) *world {
	t.Helper()
	return newWorldOn(t, floortest.NewDist(t))
}

// newLinuxWorld is a world whose floor is on Linux whatever machine runs the test: where the floor
// holds an installer agent's capture or a fork's build at all.
func newLinuxWorld(t *testing.T) *world {
	t.Helper()
	return newWorldOn(t, floortest.NewLinuxDist(t))
}

// newWorldOn is a world over dist, its floor on the platform dist serves Node for. A test never
// moves a world's floor to another platform afterwards when it fetches Node: the floor would ask
// for a release the distribution does not serve (on a Mac, a fork test's linux-arm64 from a
// darwin-arm64 distribution).
func newWorldOn(t *testing.T, dist *floortest.Dist) *world {
	t.Helper()
	w := &world{t: t, dist: dist, plat: dist.Platform, version: floortest.Shipped, out: newSyncBuffer()}
	root := resolvedTemp(t)
	w.root = root
	home := filepath.Join(root, "home")
	must(t, os.MkdirAll(home, 0o755))
	w.floor = &Floor{
		Dir:    filepath.Join(home, ".local", "share", "yolo-jail", "host-floor"),
		GOOS:   dist.GOOS,
		GOARCH: dist.GOARCH,
		Node:   NodeDist{BaseURL: dist.URL, Shipped: floortest.Shipped, Pinned: map[string]string{dist.Platform: dist.SHA256}},
		Environ: append(dist.Environ(), "NPM_CONFIG_PREFIX=/somewhere/else", "npm_config_prefix=/also/else",
			"NODE_OPTIONS=--require /tmp/evil.js", "PATH=/nowhere"),
		Home:   home,
		Hints:  worldHints(root),
		Out:    w.out,
		Prefix: "yolo host: ",
		// The world's own filesystem root for the loader check (HP-D15), holding each loader
		// Node's official Linux builds ask for, so no test's answer depends on what this machine
		// keeps in /lib64 — a NixOS machine without nix-ld included.
		Root: loaderSysroot(t, filepath.Join(root, "sysroot")),
	}
	return w
}

// loaderSysroot makes dir a filesystem root holding a plain file at each dynamic loader Node's
// official Linux builds ask for (officialNodeLoader), and returns it.
func loaderSysroot(t *testing.T, dir string) string {
	t.Helper()
	for _, loader := range officialNodeLoader {
		p := filepath.Join(dir, filepath.FromSlash(loader))
		must(t, os.MkdirAll(filepath.Dir(p), 0o755))
		must(t, os.WriteFile(p, []byte("a dynamic loader\n"), 0o755))
	}
	return dir
}

// worldHints is the Floor.Hints a world hands in: HintLocations with each folder outside home moved
// under root (machineDir), so every folder OtherCopies reads is the fixture's own. Without it, a
// Mac with Homebrew's claude in /opt/homebrew/bin (`brew install --cask claude-code`) reports one
// more copy than any other machine does.
func worldHints(root string) func(home string) []string {
	return func(home string) []string {
		var out []string
		for _, d := range HintLocations(home) {
			if rel, err := filepath.Rel(home, d); err != nil || !filepath.IsLocal(rel) {
				d = machineDir(root, d)
			}
			out = append(out, d)
		}
		return out
	}
}

// machineDir is a world's stand-in, under its root, for the machine-wide hint folder dir
// (/opt/homebrew/bin).
func machineDir(root, dir string) string { return filepath.Join(root, "machine", dir) }

func (w *world) publish(name, latest string, opts ...string) { w.dist.Publish(name, latest, opts...) }
func (w *world) npmCalls(verb string) []string               { return w.dist.NpmCalls(verb) }

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func npmProgram(pack, bin, pkg string) Program {
	return Program{Pack: pack, Install: packdecl.Install{Kind: "npm", Bin: bin, Package: pkg}}
}

func installerProgram(pack, bin string) Program {
	return Program{Pack: pack, Install: packdecl.Install{Kind: "native", Bin: bin,
		InstallerURL: "https://example.invalid/" + bin + "/install.sh"}}
}

// captureStore is a fake machine capture store with one admitted entry per call to add.
type captureStore struct {
	t     *testing.T
	store *capture.Store
	byBin map[string]*capture.Entry
	// home is the HOME the fake captures were taken under: "" is a container jail's /home/agent, and
	// a test sets a Mac's staging home (/Users/Shared/yolo-captures/<bin>/home) or a host capture's.
	home string
}

// captureHome is the HOME c's captures record.
func (c *captureStore) captureHome() string {
	if c.home != "" {
		return c.home
	}
	return "/home/agent"
}

func newCaptureStore(t *testing.T) *captureStore {
	return &captureStore{t: t, store: &capture.Store{Dir: filepath.Join(resolvedTemp(t), "captures")},
		byBin: map[string]*capture.Entry{}}
}

// add admits a capture of bin at version, taken under the capture's home (captureHome, the jail
// home /home/agent unless the test sets another): a vendor binary in
// ~/.local/share/<bin>/versions/<version> and ~/.local/bin/<bin> an ABSOLUTE link to it — the shape
// claude's installer leaves. relocatable says whether the manifest records the full scan.
func (c *captureStore) add(bin, version string, relocatable bool) *capture.Entry {
	c.t.Helper()
	return c.addShaped(bin, version, relocatable, nil)
}

// addShaped is add with a last say: shape, when set, may change the tree and the manifest before
// the entry is admitted — the manifest being the capture jail's claim, which a test can make lie.
func (c *captureStore) addShaped(bin, version string, relocatable bool,
	shape func(tree string, m *capture.Manifest)) *capture.Entry {
	t := c.t
	t.Helper()
	staged, err := c.store.Stage(bin + "-" + version)
	must(t, err)
	tree := capture.TreeDir(staged)
	body := "#!/bin/sh\necho " + bin + "-" + version + " \"$@\"\n"
	versions := ".local/share/" + bin + "/versions"
	must(t, os.MkdirAll(filepath.Join(tree, filepath.FromSlash(versions)), 0o755))
	must(t, os.MkdirAll(filepath.Join(tree, ".local", "bin"), 0o755))
	must(t, os.WriteFile(filepath.Join(tree, filepath.FromSlash(versions), version), []byte(body), 0o755))
	target := c.captureHome() + "/" + versions + "/" + version
	must(t, os.Symlink(target, filepath.Join(tree, ".local", "bin", bin)))
	m := &capture.Manifest{
		Schema: capture.ManifestSchema, Home: c.captureHome(), Platform: capture.Platform(),
		Surfaces: []string{".local"}, Excluded: []string{},
		Entries: []capture.ManifestEntry{
			{Path: ".local", Kind: capture.KindDir, Mode: "0755"},
			{Path: ".local/bin", Kind: capture.KindDir, Mode: "0755"},
			{Path: ".local/bin/" + bin, Kind: capture.KindSymlink, Target: target},
			{Path: ".local/share", Kind: capture.KindDir, Mode: "0755"},
			{Path: ".local/share/" + bin, Kind: capture.KindDir, Mode: "0755"},
			{Path: versions, Kind: capture.KindDir, Mode: "0755"},
			{Path: versions + "/" + version, Kind: capture.KindFile, Mode: "0755", Size: int64(len(body))},
		},
		AbsoluteRefs: []capture.AbsoluteRef{{Path: ".local/bin/" + bin, Kind: capture.RefSymlinkTarget, Value: target}},
		RefScan:      capture.RefScanSymlinks,
	}
	if relocatable {
		m.RefScan, m.Relocatable = capture.RefScanFull, true
	}
	if shape != nil {
		shape(tree, m)
	}
	must(t, capture.WriteManifest(staged, m))
	entry, err := c.store.AdmitEntry(staged)
	must(t, err)
	c.byBin[bin] = entry
	return entry
}

// resolve is the Floor.ResolveCapture a test hands in: the newest entry added for bin.
func (c *captureStore) resolve(bin string) (*capture.Entry, error) {
	if e, ok := c.byBin[bin]; ok {
		return e, nil
	}
	return nil, fmt.Errorf("nothing in %s records one: %w", c.store.Dir, capture.ErrNotCaptured)
}

// clock is a settable Floor.Now.
type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

// addLinkedOut admits a capture shaped like a HYPOTHETICAL vendor's: ~/.local/bin/<bin> an absolute
// link into ~/.<bin>/app/…, which no capture surface records, so the entry holds the link and
// nothing it names. It records every surface this yolo captures, so nothing a recapture could add is
// missing from it. (codex's installer once looked like this to the floor, before
// ~/.codex/packages/standalone was a surface; realcapture_test.go has codex's own shape.)
func (c *captureStore) addLinkedOut(bin string) *capture.Entry {
	c.t.Helper()
	return c.addLinkedOutAs(bin, c.captureHome()+"/."+bin+"/app/current/bin/"+bin, allSurfaces(), true)
}

// addStaleCodex admits codex's capture as a machine recorded it on 2026-09-09: before captures
// scanned their contents (so for the jail's home only), and before ~/.codex/packages/standalone was a
// surface, so it holds ~/.local/bin/codex, a link to the program, and nothing the link leads to.
func (c *captureStore) addStaleCodex() *capture.Entry {
	c.t.Helper()
	return c.addLinkedOutAs("codex", c.captureHome()+"/.codex/packages/standalone/current/bin/codex",
		[]string{".npm-global", ".local", "go"}, false)
}

// addLinkedOutAs admits a capture holding ~/.local/bin/<bin> alone, an absolute link to target,
// recording surfaces; relocatable says whether its manifest records the full scan.
func (c *captureStore) addLinkedOutAs(bin, target string, surfaces []string, relocatable bool) *capture.Entry {
	t := c.t
	t.Helper()
	staged, err := c.store.Stage(bin + "-linked-out")
	must(t, err)
	tree := capture.TreeDir(staged)
	must(t, os.MkdirAll(filepath.Join(tree, ".local", "bin"), 0o755))
	must(t, os.Symlink(target, filepath.Join(tree, ".local", "bin", bin)))
	m := &capture.Manifest{
		Schema: capture.ManifestSchema, Home: c.captureHome(), Platform: capture.Platform(),
		Surfaces: surfaces, Excluded: []string{},
		Entries: []capture.ManifestEntry{
			{Path: ".local", Kind: capture.KindDir, Mode: "0755"},
			{Path: ".local/bin", Kind: capture.KindDir, Mode: "0755"},
			{Path: ".local/bin/" + bin, Kind: capture.KindSymlink, Target: target},
		},
		AbsoluteRefs: []capture.AbsoluteRef{{Path: ".local/bin/" + bin, Kind: capture.RefSymlinkTarget, Value: target}},
		RefScan:      capture.RefScanSymlinks,
	}
	if relocatable {
		m.RefScan, m.Relocatable = capture.RefScanFull, true
	}
	must(t, capture.WriteManifest(staged, m))
	entry, err := c.store.AdmitEntry(staged)
	must(t, err)
	c.byBin[bin] = entry
	return entry
}

// allSurfaces is every capture surface this yolo records, as a manifest lists them.
func allSurfaces() []string {
	var out []string
	for _, s := range paths.InstalledProgramSurfaces() {
		out = append(out, filepath.ToSlash(s.HomeRel))
	}
	return out
}
