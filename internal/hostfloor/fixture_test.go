package hostfloor

// fixture_test.go builds each test's floor over floortest's fake Node distribution and npm
// registry, under a temp HOME, on this machine's own platform — and a fake capture store for the
// installer recipe. No test here touches the real internet, the real ~/.local, or a real agent.

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/capture"
	"github.com/mschulkind-oss/yolo-jail/internal/hostfloor/floortest"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// world is one test's fake distribution, registry and floor.
type world struct {
	t       *testing.T
	dist    *floortest.Dist
	plat    string
	version string
	out     *syncBuffer
	floor   *Floor
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

// newWorld builds a floor whose Node comes from a fake distribution (its shipped release pinned
// by digest), and whose installers inherit an ambient environment full of the variables that
// would redirect an npm install — which the floor must drop.
func newWorld(t *testing.T) *world {
	t.Helper()
	dist := floortest.NewDist(t)
	w := &world{t: t, dist: dist, plat: dist.Platform, version: floortest.Shipped, out: newSyncBuffer()}
	home := filepath.Join(resolvedTemp(t), "home")
	must(t, os.MkdirAll(home, 0o755))
	w.floor = &Floor{
		Dir:    filepath.Join(home, ".local", "share", "yolo-jail", "host-floor"),
		GOOS:   runtime.GOOS,
		GOARCH: runtime.GOARCH,
		Node:   NodeDist{BaseURL: dist.URL, Shipped: floortest.Shipped, Pinned: map[string]string{dist.Platform: dist.SHA256}},
		Environ: append(dist.Environ(), "NPM_CONFIG_PREFIX=/somewhere/else", "npm_config_prefix=/also/else",
			"NODE_OPTIONS=--require /tmp/evil.js", "PATH=/nowhere"),
		Home:   home,
		Out:    w.out,
		Prefix: "yolo host: ",
	}
	return w
}

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
}

func newCaptureStore(t *testing.T) *captureStore {
	return &captureStore{t: t, store: &capture.Store{Dir: filepath.Join(resolvedTemp(t), "captures")},
		byBin: map[string]*capture.Entry{}}
}

// add admits a capture of bin at version, taken under the jail home /home/agent: a vendor binary
// in ~/.local/share/<bin>/versions/<version> and ~/.local/bin/<bin> an ABSOLUTE link to it — the
// shape claude's installer leaves. relocatable says whether the manifest records the full scan.
func (c *captureStore) add(bin, version string, relocatable bool) *capture.Entry {
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
	target := "/home/agent/" + versions + "/" + version
	must(t, os.Symlink(target, filepath.Join(tree, ".local", "bin", bin)))
	m := &capture.Manifest{
		Schema: capture.ManifestSchema, Home: "/home/agent", Platform: capture.Platform(),
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
