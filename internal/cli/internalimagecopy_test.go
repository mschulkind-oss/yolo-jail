package cli

import (
	"bytes"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/image"
)

const imageCopyRootlessInfo = `{"host":{"security":{"rootless":true}},"store":{"graphDriverName":"overlay",` +
	`"graphRoot":"/home/u/.local/share/containers/storage","runRoot":"/run/user/1000/containers",` +
	`"graphOptions":{"overlay.mount_program":{"Executable":"/usr/bin/fuse-overlayfs"}}}}`

// TestImageCopyRunsTheLaunchsArgv: `yolo internal image-copy` runs exactly
// image.DeliveryCopyArgvFor for the one `podman info` it reads, for every answer
// podman can give, so `just load` and the stale-image fix copy the way a launch
// does — including the driver options and the fallback a shell spelling lacked.
func TestImageCopyRunsTheLaunchsArgv(t *testing.T) {
	for _, info := range []string{
		imageCopyRootlessInfo,
		`{"host":{"security":{"rootless":false}},"store":{"graphDriverName":"overlay",` +
			`"graphRoot":"/var/lib/containers/storage","runRoot":"/run/containers/storage"}}`,
		`{"host":{"security":{"rootless":true}}}`, // no store: the bare ref, never `[@+]`
		``,
	} {
		capture := func(argv []string) (string, bool) { return info, info != "" }
		var ran [][]string
		var stderr bytes.Buffer
		rc := imageCopyMain([]string{"--copier", "/c/skopeo", "--image", "/i.json",
			"--ref", "localhost/yolo-jail:latest"}, &stderr, "linux", capture,
			func(argv []string) int { ran = append(ran, argv); return 7 })
		if rc != 7 {
			t.Errorf("rc = %d, want the copy's own status", rc)
		}
		want := image.DeliveryCopyArgvFor("podman", image.ReadPodmanStoreFacts("podman", capture),
			"/c/skopeo", "/i.json", "localhost/yolo-jail:latest")
		if len(ran) != 1 || strings.Join(ran[0], "\x00") != strings.Join(want, "\x00") {
			t.Errorf("info %q: ran %q, want [%q]", info, ran, want)
		}
		if len(ran) == 1 && strings.Contains(strings.Join(ran[0], " "), "[@+]") {
			t.Errorf("an empty store spec reached the copy: %q", ran[0])
		}
		if !strings.Contains(stderr.String(), "Image store:") {
			t.Errorf("the verb does not say which store it writes:\n%s", stderr.String())
		}
	}
	// The rootless case in full, so the equality above is not vacuous.
	var ran []string
	imageCopyMain([]string{"--copier", "/c/skopeo", "--image", "/i.json", "--ref", "r:t"},
		&bytes.Buffer{}, "linux", func([]string) (string, bool) { return imageCopyRootlessInfo, true },
		func(argv []string) int { ran = argv; return 0 })
	if got, want := strings.Join(ran, " "), "podman unshare -- /c/skopeo --insecure-policy copy nix:/i.json "+
		"containers-storage:[overlay@/home/u/.local/share/containers/storage+/run/user/1000/containers"+
		":overlay.mount_program=/usr/bin/fuse-overlayfs]r:t"; got != want {
		t.Errorf("rootless copy =\n  %q\nwant\n  %q", got, want)
	}
}

// TestImageCopyRefusesWhatALaunchDoesNotWrite: only podman on Linux has a
// containers-storage a launch writes; the other backends take an archive.
func TestImageCopyRefusesWhatALaunchDoesNotWrite(t *testing.T) {
	args := []string{"--copier", "/c", "--image", "/i", "--ref", "r:t"}
	never := func([]string) int { t.Error("a refused copy ran"); return 0 }
	for _, tc := range []struct {
		name, goos string
		extra      []string
	}{
		{"podman on macOS", "darwin", nil},
		{"apple container", "linux", []string{"--runtime", "container"}},
	} {
		var stderr bytes.Buffer
		if rc := imageCopyMain(append(tc.extra, args...), &stderr, tc.goos,
			func([]string) (string, bool) { return imageCopyRootlessInfo, true }, never); rc != 2 {
			t.Errorf("%s: rc = %d, want 2", tc.name, rc)
		}
	}
	if rc := imageCopyMain([]string{"--copier", "/c"}, &bytes.Buffer{}, "linux", nil, never); rc != 2 {
		t.Errorf("missing flags: rc = %d, want 2", rc)
	}
}

// TestJustLoadCopiesThroughTheImageCopyVerb is the cross-language call-site pin:
// the verb is worthless if `just load` goes back to spelling the copy in shell,
// which is how its store spec lost the driver options the launch carries.
func TestJustLoadCopiesThroughTheImageCopyVerb(t *testing.T) {
	_, thisFile, _, ok := goruntime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller gave no source path")
	}
	body, err := os.ReadFile(filepath.Join(filepath.Dir(thisFile), "..", "..", "Justfile"))
	if err != nil {
		t.Fatal(err)
	}
	src := string(body)
	start := strings.Index(src, "\nload: ")
	if start < 0 {
		t.Fatal("the Justfile has no `load` recipe")
	}
	recipe := src[start+1:]
	if end := strings.Index(recipe, "\n\n"); end >= 0 {
		recipe = recipe[:end]
	}
	if !strings.Contains(recipe, "internal "+imageCopyVerb) {
		t.Errorf("`just load` no longer copies through `yolo internal %s`:\n%s", imageCopyVerb, recipe)
	}
	if strings.Contains(recipe, "containers-storage:") {
		t.Errorf("`just load` spells a containers-storage destination itself again:\n%s", recipe)
	}
}
