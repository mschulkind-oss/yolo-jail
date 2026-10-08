package image

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// loaderEnvRecorder is a /bin/sh body that writes to rec, one line each, whether
// LD_LIBRARY_PATH, LD_PRELOAD and YOLO_COPIER_ENV_MARKER reached it — "set" or
// nothing, and the synthetic private storage config path. No host credential is
// read or recorded.
func loaderEnvRecorder(rec string) string {
	return `{ echo "LD_LIBRARY_PATH=${LD_LIBRARY_PATH+set}"; echo "LD_PRELOAD=${LD_PRELOAD+set}"; ` +
		`echo "YOLO_COPIER_ENV_MARKER=${YOLO_COPIER_ENV_MARKER+set}"; ` +
		`echo "CONTAINERS_STORAGE_CONF=${CONTAINERS_STORAGE_CONF}"; } > '` + rec + `'`
}

// loaderEnvClean is what loaderEnvRecorder writes for a copier started without
// either loader variable and with the caller's synthetic marker.
const loaderEnvClean = "LD_LIBRARY_PATH=\nLD_PRELOAD=\nYOLO_COPIER_ENV_MARKER=set\n"

// TestTheCopierRunsWithoutTheCallersLoaderOverrides drives a rootful launch's
// copy through the REAL LayerCopy (copyImageLayers -> copyImageWatched), with a
// stand-in copier that records its effective environment at the production exec
// site. It also pins the private storage config that the rootful bare destination
// depends on.
func TestTheCopierRunsWithoutTheCallersLoaderOverrides(t *testing.T) {
	withBuildDir(t)
	decoy := t.TempDir()
	t.Setenv("LD_LIBRARY_PATH", decoy)
	t.Setenv("LD_PRELOAD", filepath.Join(decoy, "decoy.so"))
	t.Setenv("YOLO_COPIER_ENV_MARKER", "kept")

	dir := t.TempDir()
	rec := filepath.Join(dir, "copier.env")
	copier := writeScript(t, filepath.Join(dir, "skopeo"), loaderEnvRecorder(rec))
	storageConf := filepath.Join(dir, "storage.conf")
	if err := os.WriteFile(storageConf, []byte("owned inert fixture config\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CONTAINERS_STORAGE_CONF", storageConf)

	storePath := storeManifest(t, "loader-env-image")
	f := newFakeRuntime()
	var out bytes.Buffer
	opts := c2Opts("podman", storePath, f, &out)
	opts.LayerCopy = nil // the real copy, which is the production call site under test
	opts.BuildCopier = func(string) (string, []string) { return copier, nil }
	opts.Run = func(argv []string) (int, bool) {
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
	want := loaderEnvClean + "CONTAINERS_STORAGE_CONF=" + storageConf + "\n"
	if string(got) != want {
		t.Errorf("the copier's effective environment =\n%s\nwant\n%s", got, want)
	}
}

// TestTheCopierRootlessPrefixKeepsPrivateStorageConf covers the real LayerCopy
// copier exec through a recording fake `podman unshare` prefix and fake copier.
// AutoLoad's runtime operations remain on the in-memory fake runtime.
func TestTheCopierRootlessPrefixKeepsPrivateStorageConf(t *testing.T) {
	withBuildDir(t)
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
	t.Setenv("YOLO_COPIER_ENV_MARKER", "kept")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	wrapperRecord := filepath.Join(dir, "podman.env")
	wrapper := `printf 'CONTAINERS_STORAGE_CONF=%s\n' "${CONTAINERS_STORAGE_CONF-}" > '` + wrapperRecord + "'\n" +
		`[ "$1" = unshare ] && [ "$2" = -- ] || exit 64` + "\n" +
		`shift 2` + "\n" +
		`exec "$@"` + "\n"
	writeScript(t, filepath.Join(binDir, "podman"), wrapper)

	copierRecord := filepath.Join(dir, "copier.argv-env")
	copierBody := loaderEnvRecorder(copierRecord) + "\n" +
		`printf 'argv=' >> '` + copierRecord + `'; printf '<%s>' "$@" >> '` + copierRecord + `'; printf '\n' >> '` + copierRecord + `'`
	copier := writeScript(t, filepath.Join(dir, "skopeo"), copierBody)
	storePath := storeManifest(t, "rootless-loader-env-image")
	f := newFakeRuntime()
	var out bytes.Buffer
	opts := c2Opts("podman", storePath, f, &out)
	opts.LayerCopy = nil
	opts.BuildCopier = func(string) (string, []string) { return copier, nil }
	opts.StoreFacts = func() PodmanStoreFacts { return PodmanStoreFacts{Rootless: RootlessYes} }
	opts.Run = func(argv []string) (int, bool) {
		if len(argv) >= 4 && argv[1] == "image" && argv[2] == "inspect" {
			if _, err := os.Stat(copierRecord); err == nil {
				return 0, true
			}
		}
		return f.run(argv)
	}
	AutoLoadImage(opts)

	gotWrapper, err := os.ReadFile(wrapperRecord)
	if err != nil {
		t.Fatalf("the fake podman prefix was not run (%v):\n%s", err, out.String())
	}
	if want := "CONTAINERS_STORAGE_CONF=" + storageConf + "\n"; string(gotWrapper) != want {
		t.Errorf("the fake podman prefix environment = %q, want %q", gotWrapper, want)
	}
	gotCopier, err := os.ReadFile(copierRecord)
	if err != nil {
		t.Fatalf("the fake copier leaf was not run (%v):\n%s", err, out.String())
	}
	wantPrefix := loaderEnvClean + "CONTAINERS_STORAGE_CONF=" + storageConf + "\n" +
		"argv=<--insecure-policy><copy><nix:" + storePath + "><containers-storage:"
	if !strings.HasPrefix(string(gotCopier), wantPrefix) || !strings.HasSuffix(string(gotCopier), ">\n") {
		t.Errorf("the fake rootless copier leaf environment/argv =\n%s\nwant prefix %q and a destination suffix", gotCopier, wantPrefix)
	}
}
