package image

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/progress"
)

// shownAtOnce is the rendering these tests use: Immediate shows every step at Start,
// so a fake that returns in microseconds still leaves its start and result lines,
// and the assertion is about WHICH steps are narrated rather than about timing.
var shownAtOnce = progress.Config{Immediate: true}

// fakeSkopeo writes a stand-in copier that prints what the real one prints to a
// pipe (MEASURED 2026-09-28 with the flake's skopeo 1.24: one "Copying blob
// <digest>" line as each blob starts, then the config and manifest lines) and
// exits with rc.
func fakeSkopeo(t *testing.T, rc int, digests ...string) string {
	t.Helper()
	var body strings.Builder
	body.WriteString("echo 'Getting image source signatures'\n")
	for _, d := range digests {
		body.WriteString("echo 'Copying blob " + d + "'\n")
	}
	if rc == 0 {
		body.WriteString("echo 'Copying config sha256:cfg'\n")
		body.WriteString("echo 'Writing manifest to image destination'\n")
	} else {
		body.WriteString("echo 'copier broke' >&2\n")
	}
	body.WriteString("exit " + map[bool]string{true: "0", false: "1"}[rc == 0])
	return writeScript(t, filepath.Join(t.TempDir(), "skopeo"), body.String())
}

// coldLoadOpts is a first-run podman load with every long step real down to the
// copier exec: no LayerCopy seam, so the production copyImageLayers runs the fake
// skopeo above. Deleting a progress call site in AutoLoadImage, resolveCopier or
// copyImageLayers fails one of the tests below.
func coldLoadOpts(t *testing.T, manifest, copier string, out, report *bytes.Buffer) AutoLoadOptions {
	t.Helper()
	return AutoLoadOptions{
		Runtime:  "podman",
		Out:      out,
		Report:   report,
		Progress: shownAtOnce,
		Getpid:   func() int { return 4242 },
		BuildStorePath: func(string, []any, string) (string, []string) {
			return manifest, nil
		},
		BuildCopier:    func(string) (string, []string) { return copier, nil },
		StoreFacts:     func() PodmanStoreFacts { return PodmanStoreFacts{Rootless: RootlessNo} },
		PresentDigests: func() map[string]struct{} { return nil },
		Run:            newFakeRuntime().run,
		LockImageCopy:  func() func() { return func() {} },
		EvalIdentity:   func(string) (string, bool) { return "", false },
	}
}

// TestAColdLoadNarratesEveryLongStep is the regression for the lost image-load
// progress: after `podman image prune` a launch sat between "Image load needed" and
// "Copied image" for as long as the copy took, with nothing on the terminal. Each
// long step of a cold load now has a start line and a result line on the LAUNCH
// STREAM (Report), and none of it lands on Out, the load's general output, which a
// caller may send somewhere other than the terminal (the run path sent it to the
// jailed command's stdout until 2026-10-01).
func TestAColdLoadNarratesEveryLongStep(t *testing.T) {
	withBuildDir(t)
	manifest := storeManifest(t, "cold")
	copier := fakeSkopeo(t, 0, "sha256:cold-top", "sha256:cold-base")
	var out, report bytes.Buffer
	if !AutoLoadImage(coldLoadOpts(t, manifest, copier, &out, &report)).OK {
		t.Fatalf("cold load failed:\nout=%s\nreport=%s", out.String(), report.String())
	}
	r := report.String()
	for _, want := range []string{
		"Building the jail image with nix…\n",
		"Building the jail image with nix: done (",
		"Building the image copier with nix…\n",
		"Building the image copier with nix: done (",
		"Copying the image into podman…\n",
		// The result line carries the inventory's figures, which is the proof the
		// tracker read the manifest and saw the copier finish.
		"Copying the image into podman: done — 2 layer(s), 2.8 GB (",
	} {
		if !strings.Contains(r, want) {
			t.Errorf("launch stream is missing %q:\n%s", want, r)
		}
	}
	if strings.Contains(out.String(), "Copying the image into") || strings.Contains(out.String(), "with nix") {
		t.Errorf("progress leaked onto Out rather than the launch stream (Report):\n%s", out.String())
	}
	if !strings.Contains(out.String(), "Copied image:") {
		t.Errorf("the copied/skipped report is gone: %q", out.String())
	}
}

// TestTheCopyProgressFollowsTheCopiersBlobs pins the live half of the copy's line:
// each "Copying blob" the copier prints advances "layer N of M" and the byte figure
// against the image's own inventory. The fixture's top layer (26 MB) is printed
// first, so the first frame is a small fraction of the 2.8 GB total.
func TestTheCopyProgressFollowsTheCopiersBlobs(t *testing.T) {
	withBuildDir(t)
	manifest := storeManifest(t, "live")
	copier := fakeSkopeo(t, 0, "sha256:live-top", "sha256:live-base")
	var out, report bytes.Buffer
	o := coldLoadOpts(t, manifest, copier, &out, &report)
	o.Progress = progress.Config{Live: true, Immediate: true}
	if !AutoLoadImage(o).OK {
		t.Fatalf("load failed: %s", out.String())
	}
	r := report.String()
	// Each frame ends in its elapsed seconds, which are not matched: under a full parallel
	// `go test ./...` the fake copier took over a second, and "(0s)" became "(1s)".
	for _, want := range []string{
		"\r\x1b[KCopying the image into podman… layer 1 of 2 (26 MB of 2.8 GB) (",
		"\r\x1b[KCopying the image into podman… layer 2 of 2 (2.8 of 2.8 GB) (",
		"writing the manifest",
	} {
		if !strings.Contains(r, want) {
			t.Errorf("live frames are missing %q:\n%q", want, r)
		}
	}
}

// A failed copy closes its line as FAILED before the copier's own words are
// printed, and the retry gets a line of its own.
func TestAFailedCopyClosesItsLineBeforeTheReport(t *testing.T) {
	withBuildDir(t)
	manifest := storeManifest(t, "bad")
	copier := fakeSkopeo(t, 1, "sha256:bad-top")
	var out, report bytes.Buffer
	o := coldLoadOpts(t, manifest, copier, &out, &report)
	// One writer for both, so the ORDER of the failure line and the report is
	// observable.
	o.Out, o.Report = &out, &out
	if AutoLoadImage(o).OK {
		t.Fatal("a copier that exits 1 delivered an image")
	}
	s := out.String()
	failed := strings.Index(s, "Copying the image into podman: failed — layer 1 of 2")
	report1 := strings.Index(s, "the image copy failed")
	if failed < 0 || report1 < 0 || failed > report1 {
		t.Errorf("want the failed progress line before the copier's report:\n%s", s)
	}
	if n := strings.Count(s, "Copying the image into podman…"); n != 2 {
		t.Errorf("want one progress line per attempt (2), got %d:\n%s", n, s)
	}
}

// The archive arm (Apple Container, podman on macOS) has two more long steps after
// the copy — writing the archive and the runtime's load of it — and each is
// narrated. Before this, `container image load -i` of a multi-GB archive printed
// nothing at all while it ran.
func TestTheArchiveArmNarratesTheTarAndTheLoad(t *testing.T) {
	withBuildDir(t)
	storePath := hexManifest(t, "mac-progress", "base", "top")
	f := newFakeRuntime()
	var out, report bytes.Buffer
	o := macPodmanOpts(storePath, f, &out)
	o.Report = &report
	o.Progress = shownAtOnce
	if !AutoLoadImage(o).OK {
		t.Fatalf("archive delivery failed: %s", out.String())
	}
	r := report.String()
	for _, want := range []string{
		"Writing the image archive…\n",
		"Writing the image archive: done (",
		"Loading the 0 MB image archive into podman…\n",
		"Loading the 0 MB image archive into podman: done (",
	} {
		if !strings.Contains(r, want) {
			t.Errorf("launch stream is missing %q:\n%s", want, r)
		}
	}
}

// A warm launch prints nothing new: with the default rendering, steps that end
// inside the grace period are silent, so the byte-for-byte output of an
// already-loaded image is what it was before progress existed.
func TestAWarmLoadPrintsNoProgress(t *testing.T) {
	withBuildDir(t)
	storePath := storeManifest(t, "warm")
	var out, report bytes.Buffer
	opts := AutoLoadOptions{
		Runtime: "podman",
		Out:     &out,
		Report:  &report,
		BuildStorePath: func(string, []any, string) (string, []string) {
			return storePath, nil
		},
		Run:          newFakeRuntime(JailImageRef("podman", storePath)).run,
		EvalIdentity: func(string) (string, bool) { return "", false },
	}
	if !AutoLoadImage(opts).OK {
		t.Fatalf("warm load failed: %s", out.String())
	}
	if report.Len() != 0 || out.Len() != 0 {
		t.Errorf("a warm load printed:\nout=%q\nreport=%q", out.String(), report.String())
	}
}

// The copy-lock wait gets a line that closes when the lock is acquired, after the
// notice the concurrency integration test reads.
func TestTheCopyLockWaitIsNarrated(t *testing.T) {
	withBuildDir(t)
	lock := ImageCopyLockPath()
	if err := os.MkdirAll(filepath.Dir(lock), 0o755); err != nil {
		t.Fatal(err)
	}
	hold := lockImageCopy(lock, copyLockNotices{})
	report := &lockedBuffer{}
	o := AutoLoadOptions{Report: report, Progress: shownAtOnce}
	o.fill()
	done := make(chan func())
	go func() { done <- o.LockImageCopy() }()
	// The waiter announces itself before it blocks; release once it has.
	waitFor(t, func() bool { return strings.Contains(report.String(), "Waiting for the image-copy lock…") })
	hold()
	(<-done)()
	s := report.String()
	if !strings.Contains(s, "Waiting for another launch to finish copying an image") {
		t.Errorf("the notice the integration suite reads is gone:\n%s", s)
	}
	if !strings.Contains(s, "Waiting for the image-copy lock: acquired (") {
		t.Errorf("the wait did not close with its result:\n%s", s)
	}
}

// lockedBuffer is a bytes.Buffer safe to write from one goroutine and read from
// another.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition never held")
}
