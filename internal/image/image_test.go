package image

import (
	"path/filepath"
	"reflect"
	"testing"
)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestImageCommands(t *testing.T) {
	if got := ImageLoadCmd("podman", "/tmp/x.tar"); !reflect.DeepEqual(got, []string{"podman", "load", "-i", "/tmp/x.tar"}) {
		t.Errorf("podman load = %v", got)
	}
	if got := ImageLoadCmd("container", "/tmp/x.tar"); !reflect.DeepEqual(got, []string{"container", "image", "load", "-i", "/tmp/x.tar"}) {
		t.Errorf("container load = %v", got)
	}
	if got := ImageInspectCmd("podman", "img"); !reflect.DeepEqual(got, []string{"podman", "image", "inspect", "img"}) {
		t.Errorf("inspect = %v", got)
	}
	if got := ImageTagCmd("podman", "src:a", "dst:b"); !reflect.DeepEqual(got, []string{"podman", "tag", "src:a", "dst:b"}) {
		t.Errorf("tag = %v", got)
	}
	// The LEGACY refs. Since C2 these are not what a jail runs (see
	// TestJailImageRefIsContentAddressedPerRuntime for that); they are the name
	// the flake bakes, and therefore the DESTINATION of the best-effort alias
	// that keeps :latest on the newest load, plus the only name the
	// no-store-path fallback can ask about.
	// They are pinned as literals because the flake's `tag = "latest"` is what
	// makes them true, and a Go-side drift from it would be silent. ⚠ Since C9
	// nix2container's image.json carries NO name, so that flake `tag` reaches no
	// image any more — these constants are now purely the legacy alias plus the
	// legacy tars' baked name.
	if JailImage("container") != "yolo-jail:latest" {
		t.Errorf("container image = %q", JailImage("container"))
	}
	if JailImage("podman") != "localhost/yolo-jail:latest" {
		t.Errorf("podman image = %q", JailImage("podman"))
	}
	if JailImageRepository("container") != "yolo-jail" {
		t.Errorf("container repo = %q", JailImageRepository("container"))
	}
	if JailImageRepository("podman") != "localhost/yolo-jail" {
		t.Errorf("podman repo = %q", JailImageRepository("podman"))
	}
}

func TestSummarizeNixLine(t *testing.T) {
	cases := map[string]string{
		"copying path '/nix/store/abc123-hello-1.0' from 'https://cache'": "Fetching hello-1.0",
		"building '/nix/store/def456-foo.drv'...":                         "Building foo",
		"evaluating derivation 'x'":                                       "Evaluating flake...",
		"[3/5 built, 2 copied (10.2 MiB)]":                                "[3/5 built, 2 copied (10.2 MiB)]",
		"unrelated noise":                                                 "",
	}
	for in, want := range cases {
		if got := SummarizeNixLine(in); got != want {
			t.Errorf("SummarizeNixLine(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSentinelLRU(t *testing.T) {
	dir := t.TempDir()
	sentinel := filepath.Join(dir, "last-load-podman")
	// Missing file -> empty set.
	if len(ReadLoadedPaths(sentinel)) != 0 {
		t.Error("missing sentinel should be empty")
	}
	// Add 12 paths; only the last 10 survive; order-preserving move-to-end.
	for i := 0; i < 12; i++ {
		must(t, AddLoadedPath(sentinel, pathN(i)))
	}
	got := ReadLoadedPaths(sentinel)
	if len(got) != 10 {
		t.Fatalf("cap = %d, want 10", len(got))
	}
	if _, ok := got[pathN(0)]; ok {
		t.Error("oldest (0) should have been evicted")
	}
	if _, ok := got[pathN(11)]; !ok {
		t.Error("newest (11) should be present")
	}
	// Re-adding an existing path moves it to the end (no growth).
	must(t, AddLoadedPath(sentinel, pathN(5)))
	if len(ReadLoadedPaths(sentinel)) != 10 {
		t.Error("re-add should not grow past 10")
	}
}

func pathN(i int) string {
	return "/nix/store/path" + string(rune('a'+i))
}

// TestArchiveTempPathIsPerStorePathAndNeverATar covers the three properties the
// archive-delivering backends depend on and that nothing else can enforce.
//
// It replaced TestImageCachePathDeterministic, which pinned ImageCachePath after
// C9 deleted its last production caller — the callee-pinned-with-no-call-site
// shape AGENTS.md warns about, and one this repo has shipped five times.
func TestArchiveTempPathIsPerStorePathAndNeverATar(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	a, err := archiveTempPath("/nix/store/abc-jail", ociArchiveSuffix)
	must(t, err)
	b, err := archiveTempPath("/nix/store/abc-jail", ociArchiveSuffix)
	must(t, err)
	if a != b {
		t.Errorf("non-deterministic: %q vs %q", a, b)
	}
	// PER STORE PATH, or two concurrent launches of different configs collide on
	// one file and each removes the other's mid-copy.
	c, err := archiveTempPath("/nix/store/different", ociArchiveSuffix)
	must(t, err)
	if c == a {
		t.Error("different store paths must not share one archive path")
	}
	// PER FORMAT, so a machine that somehow delivered both does not hand a
	// docker-archive to `container image load`.
	d, err := archiveTempPath("/nix/store/abc-jail", dockerArchiveSuffix)
	must(t, err)
	if d == a {
		t.Error("the OCI and docker archives share one path")
	}
	// AND NEITHER SUFFIX IS `.tar`: newestTars matches that glob, so a crashed
	// launch would leave the degraded branch a candidate it loads and then
	// mis-names :latest.
	for _, suffix := range []string{ociArchiveSuffix, dockerArchiveSuffix} {
		p, err := archiveTempPath("/nix/store/abc-jail", suffix)
		must(t, err)
		if filepath.Ext(p) == ".tar" {
			t.Errorf("%q ends in .tar, which the degraded fallback's newestTars matches", p)
		}
	}
}
