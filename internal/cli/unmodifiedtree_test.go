package cli

// unmodifiedtree_test.go drives an UNMODIFIED EXTENSION through the patched extension's own tree
// build and delivery (docs/design/pi-extension-store-builds.md §4, OQ-6 (c); XB-D1 to XB-D6), with
// the build jail substituted as patchedtree_test.go substitutes it: a git source follows its
// branch's tip by default and is built with npm's dependency install, with nothing replayed; an
// npm source's spec is resolved on the host against the registry, and its tree is npm's own
// install of that version into an empty checkout.

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/capture"
	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// unmodifiedGitFixture is the tree fixture with its pack's extension declared unmodified: the
// upstream's own source, no series, no build, no follow.
func unmodifiedGitFixture(t *testing.T) *treeFixture {
	t.Helper()
	fx := newTreeFixture(t, "")
	writeFile(t, filepath.Join(fx.treeDir, "pack.json"), `{"name":"treepack","contributes":[{"kind":"files",`+
		`"into":".tool/ext/tool-ext","source":"git+file://`+fx.repo+`?ref=main",`+
		`"fallback":"git:example.com/tool-ext"}]}`)
	return fx
}

// AN UNMODIFIED GIT EXTENSION is built by the same advance a patched one is, with an empty series:
// its branch's tip by default (XB-D2), nothing replayed, the default dependency install as its build
// (XB-D3), and a recipe of its own (XB-D4).
func TestAnUnmodifiedGitExtensionIsBuiltAtItsTipWithTheDefaultBuild(t *testing.T) {
	fx := unmodifiedGitFixture(t)
	tip := fx.commit(t, "", map[int]string{3: "three"})
	f := fx.tree(t)
	if !f.Unmodified() || f.Follow != "head" || f.Build != packdecl.UnmodifiedGitBuild || f.Fallback == "" {
		t.Fatalf("the selection's tree = %+v, want an unmodified one following head with the default build", f)
	}
	d, out := fx.deliver(t, true)
	if d.Dir == "" {
		t.Fatalf("no copy was delivered: %+v\n%s", d, out)
	}
	if len(fx.builds) != 1 || fx.builds[0] != lines30(map[int]string{3: "three"}) {
		t.Fatalf("the build saw %q, want the tip's f.txt with nothing replayed", fx.builds)
	}
	script := strings.Join(fx.seen[0].Args, " ")
	if !strings.Contains(script, packdecl.UnmodifiedGitBuild) || !strings.Contains(script, "cp -a") {
		t.Errorf("the build jail's command does not run the default build: %q", script)
	}
	rec := patchedRecordOf(t, treeKeyCLI)
	empty := packsrc.EmptySeries().Digest
	if rec.Good == nil || rec.Good.Commit != tip || rec.Good.Patches != 0 || rec.Good.Series != empty ||
		rec.Good.Recipe != packdecl.TreeSourceRecipe(f.Source, packdecl.UnmodifiedGitBuild, nil, empty) {
		t.Fatalf("the good build = %+v, want the tip under the empty series' recipe", rec.Good)
	}
	if !strings.Contains(out, "build extension "+treeKeyCLI+": "+tip[:8]+", the first build of it on this machine") ||
		!strings.Contains(out, "built extension "+treeKeyCLI+": "+tip[:8]+"; this jail runs it") ||
		strings.Contains(out, "0 patches") || strings.Contains(out, "takes the series") {
		t.Errorf("the advance's lines speak of a series:\n%s", out)
	}
	// THE UPSTREAM MOVES: the next check builds the new tip, with nothing to replay.
	next := fx.commit(t, "", map[int]string{3: "three", 4: "four"})
	fx.now = fx.now.Add(2 * time.Hour)
	d2, out2 := fx.deliver(t, true)
	if d2.Entry == d.Entry || patchedRecordOf(t, treeKeyCLI).Good.Commit != next ||
		!strings.Contains(out2, "updated extension "+treeKeyCLI+": "+tip[:8]+" → "+next[:8]+"; this jail runs the new build") {
		t.Errorf("the moved tip was not built and handed:\n%s", out2)
	}
}

// npmTreeFixture is the tree fixture with its extension declared from an npm source, a registry
// served locally, and a build jail that leaves what npm's install into the checkout would: the
// package inside node_modules.
type npmTreeFixture struct {
	*treeFixture
	latest atomic.Value // string: the registry's latest dist-tag
	hits   int64
}

func newNpmTreeFixture(t *testing.T, spec string) *npmTreeFixture {
	t.Helper()
	nf := &npmTreeFixture{treeFixture: newTreeFixture(t, "")}
	nf.latest.Store("1.2.0")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&nf.hits, 1)
		latest := nf.latest.Load().(string)
		_, _ = w.Write([]byte(`{"name":"tool-ext","dist-tags":{"latest":"` + latest + `"},` +
			`"versions":{"1.1.0":{},"1.2.0":{},"1.3.0":{},"2.0.0":{}}}`))
	}))
	t.Cleanup(srv.Close)
	prev := packsrc.NpmRegistry
	packsrc.NpmRegistry = srv.URL
	t.Cleanup(func() { packsrc.NpmRegistry = prev })
	writeFile(t, filepath.Join(nf.treeDir, "pack.json"), `{"name":"treepack","contributes":[{"kind":"files",`+
		`"into":".tool/ext/tool-ext","source":"npm:tool-ext`+spec+`","fallback":"npm:tool-ext"}]}`)
	withFakeCaptureJail(t, func(o run.Options) int {
		nf.seen = append(nf.seen, o)
		if entries, err := os.ReadDir(filepath.Join(o.Workspace, forkSourceLeaf)); err != nil || len(entries) != 0 {
			t.Errorf("an npm tree's checkout is not empty: %v %v", entries, err)
		}
		writeFile(t, filepath.Join(o.Workspace, forkToolchainLeaf), "image-identity node v24 npm 11\n")
		out := filepath.Join(o.Workspace, captureOutLeaf)
		reserved := packdecl.TreeReservedDir("tool-ext")
		pkg := reserved + "/node_modules/tool-ext/package.json"
		// The version npm was asked to install, as its package.json would record it.
		_, version, _ := strings.Cut(strings.Join(o.Args, " "), "tool-ext@")
		version, _, _ = strings.Cut(version, " ")
		body := `{"name":"tool-ext","version":"` + version + `"}`
		writeFile(t, filepath.Join(capture.TreeDir(out), filepath.FromSlash(pkg)), body)
		m := &capture.Manifest{Schema: capture.ManifestSchema, Home: "/home/agent", Platform: patchedTestPlatform,
			Surfaces: []string{".local"}, Excluded: capture.DefaultExcludes(), RefScan: capture.RefScanFull,
			Relocatable: true, Entries: []capture.ManifestEntry{
				{Path: ".local", Kind: capture.KindDir, Mode: "0755"},
				{Path: ".local/share", Kind: capture.KindDir, Mode: "0755"},
				{Path: packdecl.TreeReservedRoot, Kind: capture.KindDir, Mode: "0755"},
				{Path: reserved, Kind: capture.KindDir, Mode: "0755"},
				{Path: reserved + "/node_modules", Kind: capture.KindDir, Mode: "0755"},
				{Path: reserved + "/node_modules/tool-ext", Kind: capture.KindDir, Mode: "0755"},
				{Path: pkg, Kind: capture.KindFile, Mode: "0644", Size: int64(len(body))},
			}}
		slices.SortFunc(m.Entries, func(a, b capture.ManifestEntry) int { return strings.Compare(a.Path, b.Path) })
		if err := capture.WriteManifest(out, m); err != nil {
			t.Fatal(err)
		}
		return 0
	})
	return nf
}

// AN NPM EXTENSION (XB-D5, XB-D6): the host resolves the spec against the registry, the build jail
// runs npm's own install of that version into an empty checkout, and the tree is the npm prefix,
// which the agent loads at ~/<into>/node_modules/<name>. Within the hour no request is made; past
// it, a moved dist-tag is built and handed.
func TestAnNpmExtensionIsBuiltWithNpmsInstallOfTheVersionItsSpecNames(t *testing.T) {
	nf := newNpmTreeFixture(t, "@^1.1.0")
	f := nf.tree(t)
	if !f.Npm() || f.Follow != "" || f.ListEntry() != "~/.tool/ext/tool-ext/node_modules/tool-ext" {
		t.Fatalf("the selection's npm tree = %+v (entry %q)", f, f.ListEntry())
	}
	d, out := nf.deliver(t, true)
	if d.Dir == "" {
		t.Fatalf("no copy was delivered: %+v\n%s", d, out)
	}
	if _, err := os.Stat(filepath.Join(d.Dir, "node_modules", "tool-ext", "package.json")); err != nil {
		t.Errorf("the copy holds no package: %v", err)
	}
	script := strings.Join(nf.seen[0].Args, " ")
	if !strings.Contains(script, "npm install tool-ext@1.2.0 --prefix . --legacy-peer-deps") {
		t.Errorf("the build jail does not run npm's install of the resolved version: %q", script)
	}
	rec := patchedRecordOf(t, treeKeyCLI)
	if rec.Good == nil || rec.Good.Commit != "1.2.0" || rec.Good.Series != packsrc.EmptySeries().Digest {
		t.Fatalf("the good build = %+v, want 1.2.0", rec.Good)
	}
	entry, err := (&capture.Store{Dir: paths.CapturesDir()}).Resolve(d.Entry)
	if err != nil {
		t.Fatal(err)
	}
	recs, _ := entrypoint.ReadBuildReceipts(capture.ReceiptsPath(entry.Root))
	if len(recs) != 1 || recs[0].Source != "npm:tool-ext" || recs[0].Revision != "1.2.0" {
		t.Errorf("the receipt = %+v, want the package and its version", recs)
	}
	if !strings.Contains(out, "built extension "+treeKeyCLI+": 1.2.0; this jail runs it") {
		t.Errorf("the move line does not name the version once:\n%s", out)
	}
	hits := atomic.LoadInt64(&nf.hits)
	if again, _ := nf.deliver(t, true); again.Entry != d.Entry || atomic.LoadInt64(&nf.hits) != hits || len(nf.seen) != 1 {
		t.Errorf("a second launch inside the hour asked the registry or built: %d hits, %d builds", nf.hits, len(nf.seen))
	}
	nf.latest.Store("1.3.0")
	nf.now = nf.now.Add(2 * time.Hour)
	d2, out2 := nf.deliver(t, true)
	if d2.Entry == d.Entry || !strings.Contains(out2, "updated extension "+treeKeyCLI+": 1.2.0 → 1.3.0; this jail runs the new build") ||
		!strings.Contains(strings.Join(nf.seen[len(nf.seen)-1].Args, " "), "tool-ext@1.3.0") {
		t.Errorf("the moved tag was not built and handed:\n%s", out2)
	}
}

// THE BUILD LINE A CHILD BUILD JAIL IS HANDED is the version's own (forkBuild.buildLine), never the
// recipe's template: deleting the call in forkBuildChildArgv hands the child `<version>`.
func TestAChildBuildJailIsHandedTheVersionsInstall(t *testing.T) {
	f := packload.Fork{Pack: "treepack", Bin: "tool-ext", Into: ".tool/ext/tool-ext", Source: "npm:tool-ext@^1",
		Build: packdecl.TreeBuildLine("npm:tool-ext@^1", "", "")}
	argv := forkBuildChildArgv("/staging", forkBuild{Fork: f, Commit: "1.2.0", Series: packsrc.EmptySeries()}, false)
	if got := argv[len(argv)-1]; got != packdecl.NpmTreeInstall("tool-ext", "1.2.0") {
		t.Errorf("the child's build line = %q", got)
	}
	git := packload.Fork{Pack: "treepack", Bin: "x", Into: ".x/x", Source: "git+https://h/x?ref=main",
		Build: packdecl.UnmodifiedGitBuild}
	argv = forkBuildChildArgv("/staging", forkBuild{Fork: git, Commit: "abc", Series: packsrc.EmptySeries()}, false)
	if got := argv[len(argv)-1]; got != packdecl.UnmodifiedGitBuild {
		t.Errorf("the git child's build line = %q", got)
	}
}

// `yolo pack rebase` has nothing to rebase for an unmodified extension, and says what to do instead.
func TestRebaseRefusesAnUnmodifiedExtensionNamingWhy(t *testing.T) {
	trees := []packload.Fork{{Pack: "treepack", Bin: "tool-ext", Into: ".tool/ext/tool-ext", Source: "npm:tool-ext"}}
	var errw strings.Builder
	if _, rc := pickRebaseFork(trees, treeKeyCLI, "", &errw); rc != 1 ||
		!strings.Contains(errw.String(), "unmodified extension") || !strings.Contains(errw.String(), `"patches"`) {
		t.Errorf("rebase of an unmodified extension: rc %d, %q", rc, errw.String())
	}
}
