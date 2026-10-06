package integration

import (
	"archive/tar"
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/execx"
	"github.com/mschulkind-oss/yolo-jail/internal/image"
)

// storespec_copier_test.go is the REAL copier's half of issue #47's fix
// (internal/image/storespec.go). The unit tests there pin the argv; nothing there
// can show what the copier DOES with it, and the fix rests on two behaviors of
// the copier's containers/storage that a flake.lock bump could change with every
// unit gate green:
//
//  1. A bare `containers-storage:<ref>` takes its store from storage.conf — the
//     half that broke on the reporter's host, where the file named root paths.
//  2. A named `[driver@graphroot+runroot:opts]` store takes NO path and NO driver
//     option from any storage.conf, and does take the `:opts` suffix.
//
// CONTAINERS_STORAGE_CONF stands in for the reporter's distro file: the copier's
// library reads ONE main storage.conf, the env var's when it is set, a search
// path's first hit otherwise (go.podman.io/storage pkg/configfile), and that one
// file's runroot is what sent the copier to /run/containers.

// realCopierEnv opts TestTheCopierObeysANamedStoreOverStorageConf in under
// -short. The test launches no jail and needs no loaded image, only nix and the
// `.#imageCopier` build, so it can run where the suite's own setup (which launches
// a warmup jail) cannot. The full suite runs it unconditionally.
const realCopierEnv = "YOLO_TEST_REAL_COPIER"

// hostileStorageConf names a store no process can create and a driver option the
// vfs driver refuses, so a copy that consulted it for either fails.
const hostileStorageConf = "[storage]\n" +
	"driver = \"vfs\"\n" +
	"runroot = \"/proc/yolo-issue-47/run\"\n" +
	"graphroot = \"/proc/yolo-issue-47/graph\"\n" +
	"[storage.options]\n" +
	"mountopt = \"nodev\"\n"

// TestTheHarnessLoadsTheImageTheWayALaunchDoes pins ensureJailImage's own load to
// the launch's argv: the harness is a delivery path, and a harness that went back
// to a bare destination would fail only on a host already set up like #47's, as a
// "degraded" line rather than a test failure.
func TestTheHarnessLoadsTheImageTheWayALaunchDoes(t *testing.T) {
	rootless := `{"host":{"security":{"rootless":true}},"store":{"graphDriverName":"overlay",` +
		`"graphRoot":"/home/u/.local/share/containers/storage","runRoot":"/run/user/1000/containers"}}`
	for _, info := range []string{
		rootless,
		`{"host":{"security":{"rootless":false}},"store":{"graphDriverName":"overlay",` +
			`"graphRoot":"/var/lib/containers/storage","runRoot":"/run/containers/storage"}}`,
		`{"host":{"security":{"rootless":true}}}`,
	} {
		capture := func([]string) (string, bool) { return info, true }
		got := harnessCopyArgv("podman", capture, "/nix/store/c-copier", "/nix/store/m.json")
		want := image.DeliveryCopyArgvFor("podman", image.ReadPodmanStoreFacts("podman", capture),
			image.ImageCopierBinary("/nix/store/c-copier"), "/nix/store/m.json", "localhost/"+jailImage)
		if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
			t.Errorf("harness copy =\n  %q\nwant the launch's\n  %q", got, want)
		}
		if info == rootless {
			line := strings.Join(got, " ")
			if !strings.HasPrefix(line, "podman unshare -- ") ||
				!strings.Contains(line, " containers-storage:[overlay@/home/u/") {
				t.Errorf("the rootless harness copy neither enters podman's namespace nor names "+
					"its store: %q", line)
			}
		}
	}
}

// TestTheCopierObeysANamedStoreOverStorageConf runs the REAL `.#imageCopier` with a
// storage.conf that names an uncreatable store, and proves both behaviors above
// against a throwaway vfs store: the bare destination fails in the issue's shape,
// the named store lands exactly where it was named and reads back, and the named
// store's options reach the driver while the config's do not.
//
// It runs as the copier does inside `podman unshare`: as root with the markers
// podman sets there (so containers/storage treats itself as rootless), or, for a
// non-root user, under a real rootless `podman unshare`.
func TestTheCopierObeysANamedStoreOverStorageConf(t *testing.T) {
	if testing.Short() {
		if os.Getenv(realCopierEnv) == "" {
			t.Skipf("needs nix and the .#imageCopier build; set %s=1 to run it under -short", realCopierEnv)
		}
	} else {
		requireJail(t)
	}
	copier := buildImageCopier(t)
	run := copierRunner(t, copier)
	dir := resolvedTempDir(t)
	if os.Geteuid() != 0 {
		// The stores below are written from inside podman's namespace, so some of
		// what they hold is owned by a subordinate uid. Registered after the temp
		// dir's own removal, so it runs first.
		t.Cleanup(func() { _ = unshareRemoveAll("podman", dir) })
	}
	src := writeTinyOCILayout(t, filepath.Join(dir, "layout"))
	conf := filepath.Join(dir, "storage.conf")
	if err := os.WriteFile(conf, []byte(hostileStorageConf), 0o644); err != nil {
		t.Fatal(err)
	}
	const ref = "localhost/yolo-issue47-probe:t"
	named := func(sub string, opts ...string) string {
		return image.ContainersStorageDestFor(image.PodmanStoreFacts{
			Rootless: image.RootlessYes, StoreKnown: true,
			Store: image.PodmanStore{Driver: "vfs", GraphRoot: filepath.Join(dir, sub, "graph"),
				RunRoot: filepath.Join(dir, sub, "run"), Options: opts},
		}, ref)
	}

	t.Run("a bare destination takes its store from storage.conf", func(t *testing.T) {
		out, err := run(conf, "copy", src, image.ContainersStorageDest(ref))
		if err == nil || !strings.Contains(out, "Invalid destination name containers-storage:"+ref) {
			t.Fatalf("the bare copy did not fail on the storage.conf's store (err=%v), so this "+
				"test no longer reproduces issue #47's shape:\n%s", err, out)
		}
		t.Logf("the bare copy was refused, as on the reporter's host:\n%s", strings.TrimSpace(out))
	})
	t.Run("a named store takes no path and no option from storage.conf", func(t *testing.T) {
		dest := named("ok", "vfs.ignore_chown_errors=true")
		if out, err := run(conf, "copy", src, dest); err != nil {
			t.Fatalf("copy into the named store failed: %v\n%s", err, out)
		}
		if _, err := os.Stat(filepath.Join(dir, "ok", "graph", "vfs-images")); err != nil {
			t.Errorf("the image is not in the named graph root: %v", err)
		}
		if out, err := run(conf, "inspect", "--raw", dest); err != nil || !strings.Contains(out, "schemaVersion") {
			t.Errorf("the image does not read back through the same store (err=%v):\n%s", err, out)
		}
	})
	t.Run("the named store's options reach the driver", func(t *testing.T) {
		out, err := run(conf, "copy", src, named("opts", "vfs.mountopt=nodev"))
		if err == nil || !strings.Contains(out, "does not support mount options") {
			t.Fatalf("an option the vfs driver refuses was not refused (err=%v), so the spec's "+
				"suffix no longer reaches the driver:\n%s", err, out)
		}
	})
}

// TestARootlessCopyLandsInPodmansStoreWhateverStorageConfSays is issue #47 end to
// end on a ROOTLESS podman (CI's runners): the launch's own argv, built from this
// host's real `podman info`, copies an image into podman's store while the copier
// is handed a storage.conf naming a store it cannot create — and podman then sees
// the image. The same copy with the bare destination fails, which is what proves
// the setup reproduces the bug. A rootful podman skips: its launch keeps the bare
// destination by design (storespec.go).
func TestARootlessCopyLandsInPodmansStoreWhateverStorageConfSays(t *testing.T) {
	requireJail(t)
	rt := detectRuntime()
	if goruntime.GOOS != "linux" || rt != "podman" {
		t.Skipf("podman on Linux only (runtime %q on %s takes an archive)", rt, goruntime.GOOS)
	}
	facts := image.ReadPodmanStoreFacts(rt, func(argv []string) (string, bool) {
		out, err := exec.Command(argv[0], argv[1:]...).Output()
		return string(out), err == nil
	})
	if facts.Rootless != image.RootlessYes {
		t.Skip("podman is not rootless here; a rootful launch names no store, so there is no " +
			"#47 path to exercise (CI's rootless runners run this)")
	}
	if !facts.NamesStore() {
		t.Fatalf("a rootless podman reported no store a copy can name (%s): every launch here "+
			"leaves the store to the copier's own storage.conf lookup, which is issue #47", facts.Unknown)
	}
	copier := buildImageCopier(t)
	dir := resolvedTempDir(t)
	src := writeTinyOCILayout(t, filepath.Join(dir, "layout"))
	conf := filepath.Join(dir, "storage.conf")
	if err := os.WriteFile(conf, []byte(hostileStorageConf), 0o644); err != nil {
		t.Fatal(err)
	}
	ref := "localhost/yolo-issue47-probe:" + randomHex(t, 6)
	t.Cleanup(func() { _ = exec.Command(rt, "rmi", "-f", ref).Run() })

	// The launch's argv, with two substitutions a test needs: the tiny layout for
	// the nix image (the destination is what is under test), and the hostile
	// storage.conf for the COPIER alone — podman itself must keep reading its own,
	// or `podman unshare` would fail before the copier ran.
	launchArgv := func(dest string) []string {
		base := image.DeliveryCopyArgvFor(rt, facts, copier, "/unused.json", ref)
		var argv []string
		for _, a := range base {
			switch {
			case a == copier:
				argv = append(argv, "env", "CONTAINERS_STORAGE_CONF="+conf, a)
			case strings.HasPrefix(a, "nix:"):
				argv = append(argv, src)
			case strings.HasPrefix(a, "containers-storage:") && dest != "":
				argv = append(argv, dest)
			default:
				argv = append(argv, a)
			}
		}
		return argv
	}

	bare := launchArgv(image.ContainersStorageDest(ref))
	if out, err := execx.NixClosureCommand(bare[0], bare[1:]...).CombinedOutput(); err == nil ||
		!strings.Contains(string(out), "Invalid destination name") {
		t.Fatalf("the bare destination did not fail on the storage.conf's store (err=%v), so "+
			"this setup does not reproduce issue #47:\n%s", err, out)
	}
	argv := launchArgv("")
	if !strings.Contains(strings.Join(argv, " "), "containers-storage:[") {
		t.Fatalf("the launch's argv names no store: %q", argv)
	}
	if out, err := execx.NixClosureCommand(argv[0], argv[1:]...).CombinedOutput(); err != nil {
		t.Fatalf("the launch's copy failed with a hostile storage.conf: %v\nargv: %q\n%s", err, argv, out)
	}
	if err := exec.Command(rt, "image", "exists", ref).Run(); err != nil {
		t.Errorf("podman does not see %s after the launch's copy: %v", ref, err)
	}
}

// buildImageCopier realizes `.#imageCopier` for this tree and returns its skopeo.
func buildImageCopier(t *testing.T) string {
	t.Helper()
	if goruntime.GOOS != "linux" {
		t.Skip("the copier writes containers-storage on Linux only")
	}
	root := repoRoot
	if root == "" {
		var err error
		if root, err = moduleRoot(); err != nil {
			t.Fatal(err)
		}
	}
	outLink := filepath.Join(t.TempDir(), "copier")
	build := exec.Command("nix", "--extra-experimental-features", "nix-command flakes",
		"build", image.ImageCopierAttr, "--out-link", outLink)
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("nix build %s: %v\n%s", image.ImageCopierAttr, err, out)
	}
	target, err := filepath.EvalSymlinks(outLink)
	if err != nil {
		t.Fatal(err)
	}
	return image.ImageCopierBinary(target)
}

// copierRunner returns a function that runs the copier (with `--insecure-policy`)
// in the namespace a launch's copy has — handed storageConf as its only config —
// and returns its combined output.
func copierRunner(t *testing.T, copier string) func(storageConf string, args ...string) (string, error) {
	t.Helper()
	var prefix []string
	var markers []string
	switch {
	case os.Geteuid() == 0:
		// What `podman unshare` sets for the process it starts: the namespace is
		// done, and the uid it maps is not root — so containers/storage treats this
		// as a rootless process, the reporter's copier's condition.
		markers = []string{"_CONTAINERS_USERNS_CONFIGURED=done", "_CONTAINERS_ROOTLESS_UID=1000",
			"XDG_RUNTIME_DIR=" + resolvedTempDir(t)}
	default:
		out, err := exec.Command("podman", "info", "--format", "{{.Host.Security.Rootless}}").Output()
		if err != nil || strings.TrimSpace(string(out)) != "true" {
			t.Skip("neither root nor a rootless podman: no namespace the copier can write a store in")
		}
		prefix = []string{"podman", "unshare", "--"}
	}
	return func(storageConf string, args ...string) (string, error) {
		argv := append([]string{}, prefix...)
		argv = append(argv, "env")
		argv = append(argv, markers...)
		argv = append(argv, "CONTAINERS_STORAGE_CONF="+storageConf, copier, "--insecure-policy")
		argv = append(argv, args...)
		out, err := execx.NixClosureCommand(argv[0], argv[1:]...).CombinedOutput()
		return string(out), err
	}
}

// writeTinyOCILayout writes a one-layer, one-file OCI image layout at dir and
// returns its `oci:` source reference. The destination is what these tests are
// about, so the image is the smallest one a copy can land.
func writeTinyOCILayout(t *testing.T, dir string) string {
	t.Helper()
	blobs := filepath.Join(dir, "blobs", "sha256")
	if err := os.MkdirAll(blobs, 0o755); err != nil {
		t.Fatal(err)
	}
	blob := func(b []byte) (string, int) {
		sum := sha256.Sum256(b)
		h := hex.EncodeToString(sum[:])
		if err := os.WriteFile(filepath.Join(blobs, h), b, 0o644); err != nil {
			t.Fatal(err)
		}
		return "sha256:" + h, len(b)
	}
	var layer bytes.Buffer
	tw := tar.NewWriter(&layer)
	body := []byte("issue 47\n")
	if err := tw.WriteHeader(&tar.Header{Name: "probe", Mode: 0o644, Size: int64(len(body)),
		Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	layerDigest, layerSize := blob(layer.Bytes())
	mustJSON := func(v any) []byte {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	configDigest, configSize := blob(mustJSON(map[string]any{
		"architecture": goruntime.GOARCH, "os": "linux", "config": map[string]any{},
		"rootfs": map[string]any{"type": "layers", "diff_ids": []string{layerDigest}},
	}))
	const manifestType = "application/vnd.oci.image.manifest.v1+json"
	manifestDigest, manifestSize := blob(mustJSON(map[string]any{
		"schemaVersion": 2, "mediaType": manifestType,
		"config": map[string]any{"mediaType": "application/vnd.oci.image.config.v1+json",
			"digest": configDigest, "size": configSize},
		"layers": []any{map[string]any{"mediaType": "application/vnd.oci.image.layer.v1.tar",
			"digest": layerDigest, "size": layerSize}},
	}))
	for name, v := range map[string]any{
		"oci-layout": map[string]any{"imageLayoutVersion": "1.0.0"},
		"index.json": map[string]any{"schemaVersion": 2, "manifests": []any{map[string]any{
			"mediaType": manifestType, "digest": manifestDigest, "size": manifestSize,
			"annotations": map[string]string{"org.opencontainers.image.ref.name": "t"}}}},
	} {
		if err := os.WriteFile(filepath.Join(dir, name), mustJSON(v), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return "oci:" + dir + ":t"
}

func randomHex(t *testing.T, n int) string {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(b)
}
