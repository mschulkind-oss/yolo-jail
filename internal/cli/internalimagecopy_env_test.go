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
	storageConf := filepath.Join(dir, "storage.conf")
	if err := os.WriteFile(storageConf, []byte("owned inert fixture config\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CONTAINERS_STORAGE_CONF", storageConf)
	copier := filepath.Join(dir, "skopeo")
	body := "#!/bin/sh\n" +
		`{ echo "LD_LIBRARY_PATH=${LD_LIBRARY_PATH+set}"; echo "LD_PRELOAD=${LD_PRELOAD+set}"; ` +
		`echo "YOLO_COPIER_ENV_MARKER=${YOLO_COPIER_ENV_MARKER+set}"; ` +
		`echo "CONTAINERS_STORAGE_CONF=${CONTAINERS_STORAGE_CONF}"; } > '` + rec + "'\n"
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
	if want := "LD_LIBRARY_PATH=\nLD_PRELOAD=\nYOLO_COPIER_ENV_MARKER=set\nCONTAINERS_STORAGE_CONF=" + storageConf + "\n"; string(got) != want {
		t.Errorf("the copier's environment (\"set\" = inherited) =\n%s\nwant\n%s", got, want)
	}
}

// TestImageCopyRootlessPrefixAndCopierKeepPrivateStorageConf exercises the production
// imageCopyMain -> imageCopyRun path through recording fake executables. The fake runtime
// stands in only for `podman unshare`; no runtime or storage store is opened.
func TestImageCopyRootlessPrefixAndCopierKeepPrivateStorageConf(t *testing.T) {
	dir := t.TempDir()
	binDir := filepath.Join(dir, "bin")
	if err := os.Mkdir(binDir, 0o700); err != nil {
		t.Fatal(err)
	}
	storageConf := filepath.Join(dir, "storage.conf")
	if err := os.WriteFile(storageConf, []byte("owned inert fixture config\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CONTAINERS_STORAGE_CONF", storageConf)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	wrapperRecord := filepath.Join(dir, "podman.env")
	wrapper := "#!/bin/sh\n" +
		`printf 'CONTAINERS_STORAGE_CONF=%s\n' "${CONTAINERS_STORAGE_CONF-}" > '` + wrapperRecord + "'\n" +
		`[ "$1" = unshare ] && [ "$2" = -- ] || exit 64` + "\n" +
		`shift 2` + "\n" +
		`exec "$@"` + "\n"
	if err := os.WriteFile(filepath.Join(binDir, "podman"), []byte(wrapper), 0o755); err != nil {
		t.Fatal(err)
	}
	copierRecord := filepath.Join(dir, "copier.argv-env")
	copier := filepath.Join(dir, "skopeo")
	copierBody := "#!/bin/sh\n" +
		`{ printf 'CONTAINERS_STORAGE_CONF=%s\n' "${CONTAINERS_STORAGE_CONF-}"; ` +
		`printf 'argv='; printf '<%s>' "$@"; printf '\n'; } > '` + copierRecord + "'\n"
	if err := os.WriteFile(copier, []byte(copierBody), 0o755); err != nil {
		t.Fatal(err)
	}

	rootless := `{"host":{"security":{"rootless":true}}}`
	rc := imageCopyMain([]string{"--copier", copier, "--image", "/owned/image.json", "--ref", "owned:tag"},
		&bytes.Buffer{}, "linux", func([]string) (string, bool) { return rootless, true }, imageCopyRun)
	if rc != 0 {
		t.Fatalf("the inert rootless-prefix copy exited %d", rc)
	}
	wrapperEnv, err := os.ReadFile(wrapperRecord)
	if err != nil {
		t.Fatalf("the fake podman prefix was not run: %v", err)
	}
	if want := "CONTAINERS_STORAGE_CONF=" + storageConf + "\n"; string(wrapperEnv) != want {
		t.Errorf("the fake podman prefix env = %q, want %q", wrapperEnv, want)
	}
	copierEnv, err := os.ReadFile(copierRecord)
	if err != nil {
		t.Fatalf("the fake copier leaf was not run: %v", err)
	}
	want := "CONTAINERS_STORAGE_CONF=" + storageConf + "\n" +
		"argv=<--insecure-policy><copy><nix:/owned/image.json><containers-storage:owned:tag>\n"
	if string(copierEnv) != want {
		t.Errorf("the fake rootless copier leaf env/argv = %q, want %q", copierEnv, want)
	}
}
