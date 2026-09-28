package image

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// These pin the second half of the stock-tag short-circuit: a matched launch
// ROOTS the store path it runs, from the record the load that wrote the tag left
// behind (stockimage.go, "what a match does not prove").
//
// The defect they reproduce was found reading code on 2026-09-28: a matched
// launch returned with no store path and registered no GC root, so every
// normally-launched jail ran from `stock-<hex>`, `yolo prune --nix-gc` could not
// map that ref to a root and refused while any such jail ran, and the root the
// first load registered aged out under a jail still executing from its closure
// (on podman/Linux the host /nix/store is mounted over the image's own).
//
// Every test drives the real AutoLoadImage twice — a load, then a matched
// launch — so deleting either call site (the record at the tag, the root at the
// match) fails one of them.

// twoLaunches runs a first launch that builds and loads the stock image, then
// returns the options for a second launch of the same tree against the same
// runtime, with RegisterRoot recording into *rooted.
func twoLaunches(t *testing.T, out *bytes.Buffer, rooted *[]string) (AutoLoadOptions, string) {
	t.Helper()
	withBuildDir(t)
	f := newFakeRuntime()
	o := stockOpts(t, f, out, testIdentity)
	manifest := storeManifest(t, "first-load")
	o.BuildStorePath = func(string, []any, string) (string, []string) { return manifest, nil }
	o.RegisterRoot = func(p string) { *rooted = append(*rooted, p) }
	if res := AutoLoadImage(o); !res.OK || res.StorePath != manifest {
		t.Fatalf("first launch: res=%+v\n%s", res, out.String())
	}
	*rooted = nil
	out.Reset()
	return o, manifest
}

func TestAMatchedStockLaunchRootsTheStorePathItsLoadRecorded(t *testing.T) {
	var out bytes.Buffer
	var rooted []string
	o, manifest := twoLaunches(t, &out, &rooted)
	o.BuildStorePath = func(string, []any, string) (string, []string) {
		t.Error("the matched launch built the image")
		return "", nil
	}

	res := AutoLoadImage(o)
	if !res.OK || res.Ref != StockImageRef("podman", testIdentity) {
		t.Fatalf("res = %+v, want the stock ref\n%s", res, out.String())
	}
	if len(rooted) != 1 || rooted[0] != manifest {
		t.Fatalf("matched launch rooted %v, want exactly [%s] — without it the jail runs a "+
			"closure no GC root holds, and `yolo prune --nix-gc` cannot map its ref", rooted, manifest)
	}
	if res.StorePath != manifest {
		t.Errorf("StorePath = %q, want %q: the workspace's current-image pointer is "+
			"written from it", res.StorePath, manifest)
	}
	sentinel := filepath.Join(os.Getenv("HOME"), ".local", "share", "yolo-jail", "build", "last-load-podman")
	if _, ok := ReadLoadedPaths(sentinel)[manifest]; !ok {
		t.Errorf("the matched launch did not append %s to the load sentinel", manifest)
	}
}

func TestTheStockLoadRecordsWhichStorePathItTagged(t *testing.T) {
	var out bytes.Buffer
	var rooted []string
	_, manifest := twoLaunches(t, &out, &rooted)
	bd := filepath.Join(os.Getenv("HOME"), ".local", "share", "yolo-jail", "build")
	hex := strings.TrimPrefix(testIdentity, "sha256:")
	got, ok := RecordedStockStorePath(bd, hex)
	if !ok || got != manifest {
		t.Fatalf("RecordedStockStorePath = (%q, %v), want %q — the load that writes the "+
			"stock tag is the one party that knows both the identity and the store path", got, ok, manifest)
	}
}

// An image tagged before the record existed: the content ref and the stock tag
// are both present, nothing is recorded. On a jail that reads the host store the
// match must not stand; the build runs, finds the image present, and heals the
// record so the NEXT match roots it.
func TestAnUnrecordedMatchBuildsWhenTheJailReadsTheHostStore(t *testing.T) {
	withBuildDir(t)
	var out bytes.Buffer
	manifest := storeManifest(t, "old-tagged")
	stockRef := StockImageRef("podman", testIdentity)
	f := newFakeRuntime(stockRef, JailImageRef("podman", manifest))
	o := stockOpts(t, f, &out, testIdentity)
	o.JailReadsHostStore = true
	built := false
	o.BuildStorePath = func(string, []any, string) (string, []string) { built = true; return manifest, nil }
	var rooted []string
	o.RegisterRoot = func(p string) { rooted = append(rooted, p) }

	res := AutoLoadImage(o)
	if !res.OK || !built {
		t.Fatalf("built=%v res=%+v — an unrecorded match on a host-store jail ran without "+
			"proof its closure is in the store\n%s", built, res, out.String())
	}
	if len(rooted) != 1 || rooted[0] != manifest {
		t.Errorf("rooted %v, want [%s]", rooted, manifest)
	}
	if !strings.Contains(out.String(), "no record of the store path") {
		t.Errorf("the launch did not say why it built:\n%s", out.String())
	}
	bd := filepath.Join(os.Getenv("HOME"), ".local", "share", "yolo-jail", "build")
	if got, ok := RecordedStockStorePath(bd, strings.TrimPrefix(testIdentity, "sha256:")); !ok || got != manifest {
		t.Errorf("the already-present image's record was not healed: (%q, %v)", got, ok)
	}
}

// The same unrecorded match on a jail that does NOT read the host store (macOS
// podman by default, where the nightly tags an archive it never built): the jail
// runs on the image's own store, so the match stands and the missing root is
// disclosed. Building here is what the short-circuit exists to avoid on a host
// with no Linux builder.
func TestAnUnrecordedMatchRunsAndSaysSoWhenTheJailHasItsOwnStore(t *testing.T) {
	withBuildDir(t)
	var out bytes.Buffer
	stockRef := StockImageRef("podman", testIdentity)
	o := stockOpts(t, newFakeRuntime(stockRef), &out, testIdentity)
	o.BuildStorePath = func(string, []any, string) (string, []string) {
		t.Error("built an image the runtime already holds, for a jail that does not read the host store")
		return "", nil
	}
	o.RegisterRoot = func(p string) { t.Errorf("rooted %q with no recorded store path", p) }

	res := AutoLoadImage(o)
	if !res.OK || res.Ref != stockRef || res.StorePath != "" {
		t.Fatalf("res = %+v\n%s", res, out.String())
	}
	if !strings.Contains(out.String(), "No GC root registered") {
		t.Errorf("the missing root was not disclosed:\n%s", out.String())
	}
}

// A recorded path a store GC has since deleted. `nix-store --add-root --realise`
// on it would substitute or build it — the build, disguised — so it must never
// reach RegisterRoot, and the launch must say the closure is gone.
func TestAMatchNeverRootsAPathTheStoreNoLongerHolds(t *testing.T) {
	for _, hostStore := range []bool{false, true} {
		name := map[bool]string{false: "own store", true: "host store"}[hostStore]
		t.Run(name, func(t *testing.T) {
			var out bytes.Buffer
			var rooted []string
			o, manifest := twoLaunches(t, &out, &rooted)
			o.JailReadsHostStore = hostStore
			var asked []string
			o.StorePathValid = func(p string) bool { asked = append(asked, p); return false }
			rebuilt := storeManifest(t, "rebuilt")
			built := false
			o.BuildStorePath = func(string, []any, string) (string, []string) { built = true; return rebuilt, nil }

			res := AutoLoadImage(o)
			if !res.OK {
				t.Fatalf("launch refused:\n%s", out.String())
			}
			if len(asked) != 1 || asked[0] != manifest {
				t.Errorf("validity asked of %v, want [%s]", asked, manifest)
			}
			for _, p := range rooted {
				if p == manifest {
					t.Fatalf("rooted %s, which the store no longer holds", manifest)
				}
			}
			if !strings.Contains(out.String(), "no longer valid in the nix store") {
				t.Errorf("the launch did not say the closure is gone:\n%s", out.String())
			}
			if built != hostStore {
				t.Errorf("built = %v, want %v (build exactly when the jail reads the host store)", built, hostStore)
			}
			if hostStore && (len(rooted) != 1 || rooted[0] != rebuilt) {
				t.Errorf("rooted %v, want the rebuilt path [%s]", rooted, rebuilt)
			}
		})
	}
}

func TestStockRecordRoundTripAndRejects(t *testing.T) {
	bd := t.TempDir()
	hex := strings.TrimPrefix(testIdentity, "sha256:")
	if _, ok := RecordedStockStorePath(bd, hex); ok {
		t.Fatal("a record was read before one was written")
	}
	if err := RecordStockStorePath(bd, testIdentity, "/nix/store/abc-image.json"); err != nil {
		t.Fatal(err)
	}
	if got, ok := RecordedStockStorePath(bd, hex); !ok || got != "/nix/store/abc-image.json" {
		t.Errorf("round trip = (%q, %v)", got, ok)
	}
	if st, err := os.Stat(filepath.Join(StockStorePathsDir(bd), hex)); err != nil {
		t.Fatal(err)
	} else if st.Mode().Perm() != 0o644 {
		t.Errorf("record mode = %v, want 0644 like the rest of the build dir", st.Mode().Perm())
	}
	// Not an identity, not an absolute path: nothing is written.
	_ = RecordStockStorePath(bd, "ABSENT", "/nix/store/x")
	_ = RecordStockStorePath(bd, otherIdentity, "relative/path")
	if _, ok := RecordedStockStorePath(bd, strings.TrimPrefix(otherIdentity, "sha256:")); ok {
		t.Error("a relative store path was recorded")
	}
	if h, ok := StockTagHex(StockImageTagPrefix + hex); !ok || h != hex {
		t.Errorf("StockTagHex = (%q, %v)", h, ok)
	}
	for _, bad := range []string{"latest", "0123456789abcdef", StockImageTagPrefix + "abc", StockImageTagPrefix + strings.ToUpper(hex)} {
		if _, ok := StockTagHex(bad); ok {
			t.Errorf("StockTagHex(%q) accepted a tag that is not a stock tag", bad)
		}
	}
}
