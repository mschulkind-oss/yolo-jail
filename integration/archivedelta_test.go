package integration

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/image"
)

// THE DELTA ARCHIVE AGAINST THE REAL COPIER AND A REAL REMOTE PODMAN
// (docs/research/macos-layer-reusing-image-delivery.md, OQ-LR1).
//
// The unit tests in internal/image drive deliverViaArchive against a fake copier
// and a fake loader, which is how every branch is reachable; what they cannot do
// is notice when the two real programs stop behaving the way the fakes model.
// These two tests are that notice:
//
//   - TestPlaceholderSeedingIsHonoredByTheRealCopier pins the one copier
//     behaviour the delta rests on — a zero-byte file at a blob's path makes
//     `skopeo copy … oci:` treat the blob as written — through the production
//     delivery path, so a copier upgrade that starts overwriting placeholders
//     fails here rather than silently shipping full-size archives.
//   - TestArchiveDeltaRetryRecoversAnOverClaim drives AutoLoadImage's macOS-podman
//     arm against `podman system service` over a unix socket — a REMOTE client,
//     which is what podman on macOS always is — with a present set that claims
//     every layer of an image the store has never seen. The loader must refuse
//     the delta, and the single retry with the full archive must land the image
//     under its content ref.
//
// Both are Linux-only: they build `.#ociImage`, which is a Linux image, and the
// second runs a private podman service, which the Mac's podman (a client of a VM)
// cannot. The Mac half is docs/research/macos-layer-reusing-image-delivery.md's
// "What only a Mac can confirm".

// deltaFixture is the real image manifest and copier for the tree under test.
type deltaFixture struct {
	manifest string
	copier   string
	layers   []image.LayerInfo
}

func buildDeltaFixture(t *testing.T) deltaFixture {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("the delta-archive tests build .#ociImage (a Linux image) and run a private " +
			"podman service; on macOS they are the research doc's Mac commands instead")
	}
	outLink := filepath.Join(t.TempDir(), "r")
	build := exec.Command("nix", "--extra-experimental-features", "nix-command flakes",
		"build", ".#ociImage", ".#imageCopier", "--impure", "--out-link", outLink)
	build.Dir = repoRoot
	// The image must be the harness's own: an inherited `packages:` list would
	// build a different one.
	build.Env = append(os.Environ(), "YOLO_EXTRA_PACKAGES=")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("nix build .#ociImage .#imageCopier: %v\n%s", err, out)
	}
	manifest, err := filepath.EvalSymlinks(outLink)
	if err != nil {
		t.Fatal(err)
	}
	copierOut, err := filepath.EvalSymlinks(outLink + "-1")
	if err != nil {
		t.Fatal(err)
	}
	layers, err := image.ReadLayerInventory(manifest)
	if err != nil || len(layers) < 2 {
		t.Fatalf("image inventory: %d layers, err=%v", len(layers), err)
	}
	// A PRIVATE HOME from here on, AFTER the nix build (which wants the shared nix
	// cache). requireJail's home links ~/.local/share/yolo-jail back to the
	// machine's real state dir (packHomeSharedStores), which a test that LAUNCHES a
	// jail needs. These two call AutoLoadImage in-process and launch nothing, so
	// through that link they would write the delivery's directories, the load
	// sentinel and a GC root for the fixture image into the maintainer's real state
	// dir. Nothing they run reads it: the build seams are stubbed, and the podman
	// they reach is either stubbed or a private service with its own --root.
	t.Setenv("HOME", resolvedTempDir(t))
	return deltaFixture{manifest: manifest, copier: image.ImageCopierBinary(copierOut), layers: layers}
}

// macPodmanOptions is AutoLoadImage wired as a macOS podman launch, with the
// build seams answered by the fixture. Every runtime call is the REAL exec of
// `podman`; the caller decides which podman that reaches (CONTAINER_HOST).
func (f deltaFixture) macPodmanOptions(out *bytes.Buffer) image.AutoLoadOptions {
	return image.AutoLoadOptions{
		Runtime:        "podman",
		RepoRoot:       repoRoot,
		IsMacOS:        true,
		Out:            out,
		BuildStorePath: func(string, []any, string) (string, []string) { return f.manifest, nil },
		BuildOffload:   func(string, []any, string) (string, []string) { return "", nil },
		BuildCopier:    func(string) (string, []string) { return f.copier, nil },
		// No stock-tag short-circuit: this test is about delivery.
		EvalIdentity: func(string) (string, bool) { return "", false },
	}
}

func (f deltaFixture) allLayers() map[string]struct{} {
	out := map[string]struct{}{}
	for _, l := range f.layers {
		out[l.Digest] = struct{}{}
	}
	return out
}

// archiveContents reads an oci-archive yolo wrote: its blob digests, and the
// image manifest its index names.
func archiveContents(t *testing.T, path string) (blobs map[string]int64, manifest []byte) {
	t.Helper()
	fh, err := os.Open(path)
	if err != nil {
		t.Fatalf("the loader was handed %s and it is not there: %v", path, err)
	}
	defer fh.Close()
	blobs = map[string]int64{}
	files := map[string][]byte{}
	tr := tar.NewReader(fh)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if h.Typeflag != tar.TypeReg {
			continue
		}
		if strings.HasPrefix(h.Name, "blobs/sha256/") {
			d := "sha256:" + strings.TrimPrefix(h.Name, "blobs/sha256/")
			blobs[d] = h.Size
			if h.Size < 1<<20 { // manifests and configs; never read a layer into memory
				b, _ := io.ReadAll(tr)
				files[d] = b
			}
			continue
		}
		b, _ := io.ReadAll(tr)
		files[h.Name] = b
	}
	var idx struct {
		Manifests []struct {
			Digest string `json:"digest"`
		} `json:"manifests"`
	}
	if err := json.Unmarshal(files["index.json"], &idx); err != nil || len(idx.Manifests) != 1 {
		t.Fatalf("archive index.json: %v (%s)", err, files["index.json"])
	}
	return blobs, files[idx.Manifests[0].Digest]
}

// TestPlaceholderSeedingIsHonoredByTheRealCopier: with every layer but the top
// one claimed present, the archive the real copier and yolo's tar produce carries
// exactly the top layer — and its manifest still names every layer at its REAL
// size, not the placeholder's zero (so the manifest, and the image ID, are the
// full image's).
//
// If containers/image's oci layout destination stops treating a bare file as a
// present blob, the copier writes the seeded blobs and this fails on the blob set.
// That is the trigger for the research doc's "robust variant" (full layout,
// present blobs deleted before tarring).
func TestPlaceholderSeedingIsHonoredByTheRealCopier(t *testing.T) {
	requireJail(t)
	f := buildDeltaFixture(t)
	top := f.layers[len(f.layers)-1]
	present := f.allLayers()
	delete(present, top.Digest)

	var out bytes.Buffer
	o := f.macPodmanOptions(&out)
	o.PresentDigests = func() map[string]struct{} { return present }
	// Nothing is run; the image is never loaded. The inspect that decides "load
	// needed" answers absent, and the loader is the assertion.
	o.Run = func([]string) (int, bool) { return 1, true }
	var blobs map[string]int64
	var manifest []byte
	o.LoadArchive = func(path string) bool {
		blobs, manifest = archiveContents(t, path)
		return true
	}
	start := time.Now()
	if res := image.AutoLoadImage(o); !res.OK {
		t.Fatalf("delivery failed: %s", out.String())
	}
	t.Logf("delta build (real copier, %d placeholders) took %s", len(present), time.Since(start))

	for _, l := range f.layers {
		size, in := blobs[l.Digest]
		switch {
		case l.Digest == top.Digest && (!in || size != top.Size):
			t.Errorf("the one layer the store lacks is missing or truncated in the archive (in=%v size=%d want %d)",
				in, size, top.Size)
		case l.Digest != top.Digest && in:
			t.Errorf("the copier wrote %s although a placeholder claimed it (size %d) — "+
				"placeholder seeding is no longer honoured", l.Digest, size)
		}
	}
	var m struct {
		Layers []struct {
			Digest string `json:"digest"`
			Size   int64  `json:"size"`
		} `json:"layers"`
	}
	if err := json.Unmarshal(manifest, &m); err != nil {
		t.Fatalf("manifest: %v", err)
	}
	if len(m.Layers) != len(f.layers) {
		t.Fatalf("manifest names %d layers, want every one of the image's %d", len(m.Layers), len(f.layers))
	}
	for i, l := range m.Layers {
		if l.Digest != f.layers[i].Digest || l.Size != f.layers[i].Size {
			t.Errorf("manifest layer %d = %s/%d, want %s/%d (a placeholder's size leaked into the manifest?)",
				i, l.Digest, l.Size, f.layers[i].Digest, f.layers[i].Size)
		}
	}
	if strings.Contains(out.String(), "the image copier wrote") {
		t.Errorf("the delivery reported ignored placeholders: %q", out.String())
	}
}

// remotePodman is a private `podman system service` on a unix socket, over a
// store of its own, so the test can load into an EMPTY store without touching
// the runtime the rest of the suite uses.
type remotePodman struct {
	sock, root, runRoot string
	svc                 *exec.Cmd
}

func startRemotePodman(t *testing.T) *remotePodman {
	t.Helper()
	if _, err := exec.LookPath("podman"); err != nil {
		t.Skip("no podman on PATH")
	}
	dir := t.TempDir()
	p := &remotePodman{
		sock:    filepath.Join(dir, "api.sock"),
		root:    filepath.Join(dir, "store"),
		runRoot: filepath.Join(dir, "run"),
	}
	var logBuf bytes.Buffer
	p.svc = exec.Command("podman", "--root", p.root, "--runroot", p.runRoot,
		"system", "service", "--time=0", "unix://"+p.sock)
	p.svc.Stdout, p.svc.Stderr = &logBuf, &logBuf
	p.svc.Env = localPodmanEnv()
	if err := p.svc.Start(); err != nil {
		t.Fatalf("starting podman system service: %v", err)
	}
	t.Cleanup(func() {
		_ = p.svc.Process.Kill()
		_ = p.svc.Wait()
		// The store holds layer files owned by mapped IDs on a rootless host,
		// which t.TempDir's cleanup could not remove; podman can.
		reset := exec.Command("podman", "--root", p.root, "--runroot", p.runRoot,
			"system", "reset", "--force")
		reset.Env = localPodmanEnv()
		_ = reset.Run()
	})
	deadline := time.Now().Add(30 * time.Second)
	for {
		if err := exec.Command("podman", "--url", "unix://"+p.sock, "info").Run(); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("podman system service did not answer on %s within 30s:\n%s", p.sock, logBuf.String())
		}
		time.Sleep(200 * time.Millisecond)
	}
	return p
}

// localPodmanEnv is the environment minus CONTAINER_HOST: the service and its
// cleanup are LOCAL podman commands (`--root` is a local-only flag), and a test
// that has already pointed CONTAINER_HOST at some service would otherwise turn
// them into remote clients that refuse it.
func localPodmanEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "CONTAINER_HOST=") {
			env = append(env, kv)
		}
	}
	return env
}

// TestArchiveDeltaRetryRecoversAnOverClaim is the fail-closed rule against the
// real loader: an over-claimed present set (here, every layer, into an empty
// store — the extreme of "a layer was pruned between the probe and the load")
// makes `podman load` refuse the delta and write nothing, and the one retry with
// the full archive delivers the image under the ref the launch runs.
func TestArchiveDeltaRetryRecoversAnOverClaim(t *testing.T) {
	requireJail(t)
	f := buildDeltaFixture(t)
	p := startRemotePodman(t)
	// Every `podman` AutoLoadImage runs is now a remote client of the private
	// service — the probe, the load, the inspect, the tag.
	t.Setenv("CONTAINER_HOST", "unix://"+p.sock)

	var out bytes.Buffer
	o := f.macPodmanOptions(&out)
	o.PresentDigests = f.allLayers
	start := time.Now()
	res := image.AutoLoadImage(o)
	t.Logf("over-claimed delivery (delta refused, full archive loaded) took %s", time.Since(start))
	if !res.OK {
		t.Fatalf("the retry did not recover the over-claim:\n%s", out.String())
	}
	s := out.String()
	if n := strings.Count(s, "retrying once with the full archive"); n != 1 {
		t.Errorf("retry announced %d time(s), want exactly once:\n%s", n, s)
	}
	if !strings.Contains(s, "0 layer(s), 0 MB reused") {
		t.Errorf("the report does not describe the full archive that landed:\n%s", s)
	}
	// The image is in the REMOTE store, under the content ref, with every layer.
	got, err := exec.Command("podman", "image", "inspect", "--format",
		"{{len .RootFS.Layers}}", res.Ref).CombinedOutput()
	if err != nil {
		t.Fatalf("%s is not in the remote store: %v\n%s", res.Ref, err, got)
	}
	if want := len(f.layers); strings.TrimSpace(string(got)) != strconv.Itoa(want) {
		t.Errorf("%s has %s layers, want %d", res.Ref, strings.TrimSpace(string(got)), want)
	}
}
