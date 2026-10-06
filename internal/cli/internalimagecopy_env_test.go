package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// TestImageCopyRunsTheCopierWithoutTheCallersLoaderOverrides: `yolo internal
// image-copy` (what `just load` runs) starts the copier through the REAL
// imageCopyRun with neither LD_LIBRARY_PATH nor LD_PRELOAD, however the caller
// set them, and with the rest of the caller's environment. The copier is a Nix
// store closure that a foreign libc on LD_LIBRARY_PATH crashes at startup
// (image-staging-vs-baking.md, LI-D1); delete the scrub at imageCopyRun and this
// fails. The stand-in records "set" or nothing per variable, never a value.
func TestImageCopyRunsTheCopierWithoutTheCallersLoaderOverrides(t *testing.T) {
	decoy := t.TempDir()
	t.Setenv("LD_LIBRARY_PATH", decoy)
	t.Setenv("LD_PRELOAD", filepath.Join(decoy, "decoy.so"))
	t.Setenv("YOLO_COPIER_ENV_MARKER", "kept")

	dir := t.TempDir()
	rec := filepath.Join(dir, "copier.env")
	copier := filepath.Join(dir, "skopeo")
	body := "#!/bin/sh\n" +
		`{ echo "LD_LIBRARY_PATH=${LD_LIBRARY_PATH+set}"; echo "LD_PRELOAD=${LD_PRELOAD+set}"; ` +
		`echo "YOLO_COPIER_ENV_MARKER=${YOLO_COPIER_ENV_MARKER+set}"; } > '` + rec + "'\n"
	if err := os.WriteFile(copier, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	rootful := `{"host":{"security":{"rootless":false}}}`
	rc := imageCopyMain([]string{"--copier", copier, "--image", "/i.json", "--ref", "r:t"},
		&bytes.Buffer{}, "linux", func([]string) (string, bool) { return rootful, true }, imageCopyRun)
	if rc != 0 {
		t.Fatalf("the copy exited %d", rc)
	}
	got, err := os.ReadFile(rec)
	if err != nil {
		t.Fatalf("the copier was never run: %v", err)
	}
	if want := "LD_LIBRARY_PATH=\nLD_PRELOAD=\nYOLO_COPIER_ENV_MARKER=set\n"; string(got) != want {
		t.Errorf("the copier's environment (\"set\" = inherited) =\n%s\nwant\n%s", got, want)
	}
}
