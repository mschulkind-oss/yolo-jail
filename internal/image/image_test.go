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
	if JailImage("container") != "yolo-jail:latest" {
		t.Errorf("container image = %q", JailImage("container"))
	}
	if JailImage("podman") != "localhost/yolo-jail:latest" {
		t.Errorf("podman image = %q", JailImage("podman"))
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

func TestFormatProgress(t *testing.T) {
	// No estimate -> just the MB/GB string.
	if got := FormatProgress(50*1024*1024, 0); got != "50 MB" {
		t.Errorf("50MB no-est = %q", got)
	}
	// With estimate -> percentage, capped at 99.
	if got := FormatProgress(50*1024*1024, 100*1024*1024); got != "50 MB (50%)" {
		t.Errorf("50%% = %q", got)
	}
	if got := FormatProgress(100*1024*1024, 100*1024*1024); got != "100 MB (99%)" {
		t.Errorf("cap-at-99 = %q", got)
	}
	// GB threshold.
	if got := FormatProgress(2*1024*1024*1024, 0); got != "2.0 GB" {
		t.Errorf("2GB = %q", got)
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

func TestImageCachePathDeterministic(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	a, err := ImageCachePath("/nix/store/abc-jail")
	must(t, err)
	b, err := ImageCachePath("/nix/store/abc-jail")
	must(t, err)
	if a != b {
		t.Errorf("non-deterministic: %q vs %q", a, b)
	}
	if filepath.Ext(a) != ".tar" {
		t.Errorf("cache path should end .tar: %q", a)
	}
	c, _ := ImageCachePath("/nix/store/different")
	if c == a {
		t.Error("different store paths should hash differently")
	}
}

func TestSizeFileQuirk(t *testing.T) {
	// The preserved quirk: reader path has the doubled "-size" suffix.
	sentinel := SizeSentinelPath()
	sizeFile := SizeFileForSentinel(sentinel)
	if filepath.Base(sizeFile) != "last-load-size-size" {
		t.Errorf("reader path = %q, want .../last-load-size-size (preserved quirk)", sizeFile)
	}
}

func TestLinuxBuilderFromMachines(t *testing.T) {
	txt := "# a comment\n\nssh-ng://nix-builder aarch64-linux,x86_64-linux /root/.ssh/key 4\n"
	uri, host, ok := LinuxBuilderFromMachines(txt)
	if !ok || uri != "ssh-ng://nix-builder" || host != "nix-builder" {
		t.Errorf("= %q,%q,%v", uri, host, ok)
	}
	// No linux builder -> not found.
	if _, _, ok := LinuxBuilderFromMachines("ssh://mac aarch64-darwin key 2\n"); ok {
		t.Error("darwin-only should not match")
	}
	if _, _, ok := LinuxBuilderFromMachines(""); ok {
		t.Error("empty should not match")
	}
}
