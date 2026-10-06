package image

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// loaderEnvRecorder is a /bin/sh body that writes to rec, one line each, whether
// LD_LIBRARY_PATH, LD_PRELOAD and YOLO_COPIER_ENV_MARKER reached it — "set" or
// nothing, never the value, so a failure prints no part of the environment the
// test ran in.
func loaderEnvRecorder(rec string) string {
	return `{ echo "LD_LIBRARY_PATH=${LD_LIBRARY_PATH+set}"; echo "LD_PRELOAD=${LD_PRELOAD+set}"; ` +
		`echo "YOLO_COPIER_ENV_MARKER=${YOLO_COPIER_ENV_MARKER+set}"; } > '` + rec + `'`
}

// loaderEnvClean is what loaderEnvRecorder writes for a copier started without
// either loader variable and with the rest of the caller's environment.
const loaderEnvClean = "LD_LIBRARY_PATH=\nLD_PRELOAD=\nYOLO_COPIER_ENV_MARKER=set\n"

// TestTheCopierRunsWithoutTheCallersLoaderOverrides drives a launch's copy
// through the REAL LayerCopy (copyImageLayers → copyImageWatched), with a
// stand-in copier that records the environment it was started in, while the
// caller exports LD_LIBRARY_PATH and LD_PRELOAD at a decoy directory.
//
// The copier is a Nix store closure; the caller's loader settings can only swap
// its libc for one it was not built against. That is what a jail's baked
// LD_LIBRARY_PATH did once flake.lock moved the copier to a newer glibc: skopeo
// died with "*** stack smashing detected ***" and no image could be delivered
// (image-staging-vs-baking.md, LI-D1). Delete the scrub at the exec and this
// fails on the recorded environment.
func TestTheCopierRunsWithoutTheCallersLoaderOverrides(t *testing.T) {
	withBuildDir(t)
	decoy := t.TempDir()
	t.Setenv("LD_LIBRARY_PATH", decoy)
	t.Setenv("LD_PRELOAD", filepath.Join(decoy, "decoy.so"))
	t.Setenv("YOLO_COPIER_ENV_MARKER", "kept")

	dir := t.TempDir()
	rec := filepath.Join(dir, "copier.env")
	copier := writeScript(t, filepath.Join(dir, "skopeo"), loaderEnvRecorder(rec))

	storePath := storeManifest(t, "loader-env-image")
	f := newFakeRuntime()
	var out bytes.Buffer
	opts := c2Opts("podman", storePath, f, &out)
	opts.LayerCopy = nil // the real copy, which is the call site under test
	opts.BuildCopier = func(string) (string, []string) { return copier, nil }
	opts.Run = func(argv []string) (int, bool) {
		// The stand-in writes no image, so the confirming inspect answers from
		// whether the copier ran at all.
		if len(argv) >= 4 && argv[1] == "image" && argv[2] == "inspect" {
			if _, err := os.Stat(rec); err == nil {
				return 0, true
			}
		}
		return f.run(argv)
	}
	AutoLoadImage(opts)

	got, err := os.ReadFile(rec)
	if err != nil {
		t.Fatalf("the copier was never run (%v):\n%s", err, out.String())
	}
	if string(got) != loaderEnvClean {
		t.Errorf("the copier's environment (\"set\" = inherited) =\n%s\nwant\n%s"+
			"(neither loader variable, and the rest of the caller's environment kept)", got, loaderEnvClean)
	}
}
