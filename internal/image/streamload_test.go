package image

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// cacheImagesDir is where a tar would land if anything still wrote one — derived
// from the build dir withBuildDir() returns, which is its sibling.
func cacheImagesDir(buildDir string) string {
	return filepath.Join(filepath.Dir(buildDir), "cache", "images")
}

// writeScript drops an executable at path with a /bin/sh body. Used as a stand-in
// for the nix out-link, which on Linux IS the executable streamImageCommand runs
// (streamLayeredImage: its stdout is the tar).
func writeScript(t *testing.T, path, body string) string {
	t.Helper()
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestImageLoadStdinCmd pins C3's DECISION POINT. The pipe form is available on
// podman and unrepresentable on Apple Container, and the caller branches on this
// answer rather than re-deriving the runtime name a third time.
func TestImageLoadStdinCmd(t *testing.T) {
	argv, ok := ImageLoadStdinCmd("podman")
	if !ok {
		t.Fatal("podman cannot stream; C3 has nothing left to do")
	}
	if got, want := strings.Join(argv, " "), "podman load"; got != want {
		t.Errorf("stdin load argv = %q, want %q", got, want)
	}
	for _, a := range argv {
		if a == "-i" {
			t.Errorf("the stdin load argv still names an input FILE: %v", argv)
		}
	}
	if _, ok := ImageLoadStdinCmd("container"); ok {
		t.Error("Apple Container was offered the pipe form; its converters " +
			"interpolate a path (skopeo docker-archive:, podman save -o) and cannot " +
			"consume a stream")
	}
}

// TestPodmanHappyPathStreamsAndNeverWritesATar is C3 stated as a test, and it
// pins the CALL SITE rather than the seam: reinstate materialize-then-load and
// the Materialize stub's t.Fatal fires; leave the tar creation in place beside
// the stream and the cache-dir assertion fires.
//
// The disk claim is asserted on DISK, not on the code path taken — "no file
// appears in cache/images" is the sentence OQ-DF1 ruled ("stream, keep zero
// tars"), and a test that only checked which function ran could be satisfied by
// a stream that also wrote a tar on the side.
func TestPodmanHappyPathStreamsAndNeverWritesATar(t *testing.T) {
	bd := withBuildDir(t)
	const storePath = "/nix/store/stream-me-image"
	f := newFakeRuntime()
	var out bytes.Buffer

	res := AutoLoadImage(AutoLoadOptions{
		Runtime:        "podman",
		Out:            &out,
		BuildStorePath: func(string, []any, string) (string, []string) { return storePath, nil },
		Run:            f.run,
		StreamLoad:     f.streamLoad,
		Materialize: func(_, cacheFile string) int64 {
			t.Errorf("the podman path materialized a tar at %q — C3's whole claim is "+
				"that it writes none", cacheFile)
			return 0
		},
	})
	if !res.OK {
		t.Fatalf("streamed launch failed: %s", out.String())
	}
	if res.Ref != JailImageRef("podman", storePath) {
		t.Errorf("ref = %q, want the content ref %q", res.Ref, JailImageRef("podman", storePath))
	}

	// The stream seam ran, on THIS store path, piped into a loader that reads
	// stdin — no -i, so there is no file for it to have read.
	if len(f.streamedPaths) != 1 || f.streamedPaths[0] != storePath {
		t.Fatalf("StreamLoad saw %v, want exactly [%s]", f.streamedPaths, storePath)
	}
	if got, want := strings.Join(f.streamedArgv[0], " "), "podman load"; got != want {
		t.Errorf("streamed into %q, want %q", got, want)
	}

	// And nothing landed on disk. Both spellings: the specific name this store
	// path's tar would have had, and the directory as a whole (which catches a
	// tar written under any other name).
	tarName := keyFor(storePath) + ".tar"
	if _, err := os.Stat(filepath.Join(cacheImagesDir(bd), tarName)); err == nil {
		t.Errorf("%s was written; the streamed path must retain no tar", tarName)
	}
	if entries, err := os.ReadDir(cacheImagesDir(bd)); err == nil && len(entries) != 0 {
		t.Errorf("cache/images is not empty after a streamed load: %v", entries)
	}

	// The human-visible shape is the file form's, one accurate word apart.
	if !strings.Contains(out.String(), "Streamed image: ") {
		t.Errorf("no streamed-size line: %q", out.String())
	}
	if strings.Contains(out.String(), "Cached image") {
		t.Errorf("the streamed path claimed to have cached something: %q", out.String())
	}
}

// TestTheStreamIsToldTheImageName pins C2's naming where it actually happens —
// in the argv handed to nixpkgs' streamLayeredImage script, whose `--repo_tag`
// "Override[s] the RepoTags from the configuration" so `podman load` creates the
// image under its content ref instead of under the flake's baked :latest.
//
// It pins the CALL SITE, not streamImageArgv: the stand-in script records the
// arguments it was really invoked with, so deleting the append inside
// streamImageArgv AND dropping the repoTag at streamImageToRuntime both fail
// here. The empty-tag arm is the Apple Container contract — that path names the
// image during conversion and must stream the archive unmodified.
func TestTheStreamIsToldTheImageName(t *testing.T) {
	const storePath = "/nix/store/named-going-in"

	t.Run("the content name reaches the stream process", func(t *testing.T) {
		withBuildDir(t)
		dir := t.TempDir()
		argsFile := filepath.Join(dir, "args")
		script := writeScript(t, filepath.Join(dir, "stream-yolo-jail"),
			"printf '%s\\n' \"$@\" > "+argsFile+"; head -c 1024 /dev/zero")

		// The store path IS the executable on Linux (its shebang streams the tar),
		// so the stand-in script stands in for it whole.
		var out bytes.Buffer
		if _, ok := streamImageToRuntime(script, StreamRepoTag(script),
			[]string{"sh", "-c", "cat >/dev/null"}, false, &out, false); !ok {
			t.Fatalf("stream failed: %q", out.String())
		}
		got, err := os.ReadFile(argsFile)
		if err != nil {
			t.Fatalf("the stream script was never run: %v", err)
		}
		want := "--repo_tag\n" + StreamRepoTag(script) + "\n"
		if string(got) != want {
			t.Errorf("stream argv tail = %q, want %q", string(got), want)
		}
	})

	t.Run("streamImageArgv", func(t *testing.T) {
		// The name podman will qualify the archive's RepoTags into MUST be the ref
		// the pipeline hands the run slice, or the launch names an image nobody
		// created.
		if got, want := qualifyRef(StreamRepoTag(storePath)), JailImageRef("podman", storePath); got != want {
			t.Errorf("streamed name qualifies to %q, want the run ref %q", got, want)
		}
		argv := streamImageArgv(storePath, StreamRepoTag(storePath), false)
		if got, want := strings.Join(argv, " "),
			storePath+" --repo_tag "+StreamRepoTag(storePath); got != want {
			t.Errorf("argv = %q, want %q", got, want)
		}
		// Apple Container's file form streams the archive UNMODIFIED: its
		// converters choose the name, and materializeImage's tar is read back by a
		// load whose retag reads the baked one.
		if got, want := strings.Join(streamImageArgv(storePath, "", false), " "), storePath; got != want {
			t.Errorf("untagged argv = %q, want %q", got, want)
		}
	})
}

// TestAppleContainerStillGetsAFileNotAStream pins the other arm. Apple
// Container's converters interpolate a PATH — skopeo's `docker-archive:<tar>`
// source and `podman save --format oci-archive -o <tar>` — so the file form is a
// requirement of the backend, not a preference. Hand it a stream and it breaks
// on a platform no CI runner exercises, which is why this is asserted here.
func TestAppleContainerStillGetsAFileNotAStream(t *testing.T) {
	bd := withBuildDir(t)
	const storePath = "/nix/store/ac-needs-a-file"
	var gotTar string
	var out bytes.Buffer

	res := AutoLoadImage(AutoLoadOptions{
		Runtime:        "container",
		Out:            &out,
		BuildStorePath: func(string, []any, string) (string, []string) { return storePath, nil },
		Run:            newFakeRuntime().run,
		Materialize:    writeTar,
		StreamLoad: func(string, string, []string) (int64, bool) {
			t.Error("Apple Container was handed a stream; its converters need a path")
			return 0, false
		},
		LoadAppleContainer: func(tarPath, _ string) bool {
			gotTar = tarPath
			return true
		},
	})
	if !res.OK {
		t.Fatalf("apple container load failed: %s", out.String())
	}
	want := filepath.Join(cacheImagesDir(bd), keyFor(storePath)+".tar")
	if gotTar != want {
		t.Errorf("converter got %q, want the keyed cache tar %q", gotTar, want)
	}
	// The converters run `skopeo copy docker-archive:<path>` — the file has to be
	// there when they do, not merely named.
	if _, err := os.Stat(gotTar); err != nil {
		t.Errorf("the tar handed to the converters does not exist: %v", err)
	}
}

// TestBuildFailureFallbackStillLoadsAnExistingTar: C3 removed the CREATION of
// tars on the podman happy path, not the ability to consume one. The
// build-failure fallback (newestTars → `podman load -i`) is the only thing that
// lets a jail start when the build failed and nothing is loaded, and it must keep
// working on whatever tars are already on disk — including the ~485 GiB C3
// deliberately does not sweep.
func TestBuildFailureFallbackStillLoadsAnExistingTar(t *testing.T) {
	bd := withBuildDir(t)
	cacheImages := cacheImagesDir(bd)
	if err := os.MkdirAll(cacheImages, 0o755); err != nil {
		t.Fatal(err)
	}
	tarPath := filepath.Join(cacheImages, "leftover.tar")
	if err := os.WriteFile(tarPath, []byte("tar"), 0o644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	var ran [][]string
	res := AutoLoadImage(AutoLoadOptions{
		Runtime: "podman",
		Out:     &out,
		BuildStorePath: func(string, []any, string) (string, []string) {
			return "", []string{"error: out of disk space"}
		},
		Run: func(argv []string) (int, bool) {
			ran = append(ran, append([]string(nil), argv...))
			if len(argv) >= 2 && argv[1] == "load" {
				return 0, true
			}
			return 1, true // inspect: nothing in the runtime
		},
		StreamLoad: func(string, string, []string) (int64, bool) {
			t.Error("the fallback tried to STREAM; it has no store path to stream from " +
				"— the build is what failed")
			return 0, false
		},
		LookupEnv: allowStaleEnv,
	})
	if !res.OK {
		t.Fatalf("the offline safety net did not fire: %s", out.String())
	}
	want := "podman load -i " + tarPath
	found := false
	for _, argv := range ran {
		if strings.Join(argv, " ") == want {
			found = true
		}
	}
	if !found {
		t.Errorf("no %q among %v — the cached-tar fallback must still read a FILE", want, ran)
	}
	if !strings.Contains(out.String(), "Done: loaded image from cache") {
		t.Errorf("the cache load was not announced: %q", out.String())
	}
}

// TestStreamAndLoadFailuresAreReportedDistinctly drives the real pipe with shell
// one-liners, because "which end broke" is the question a bare io.Copy cannot
// answer and the one this whole file exists for.
//
// Each subtest is one of the failure classes in streamload.go's header. They must
// be told APART, not merely both detected: a stream that died at 99 % and a
// podman that rejected a complete archive send a reader to opposite places.
func TestStreamAndLoadFailuresAreReportedDistinctly(t *testing.T) {
	// drain is a loader that consumes everything and succeeds.
	drain := []string{"sh", "-c", "cat >/dev/null"}

	t.Run("both green", func(t *testing.T) {
		var out bytes.Buffer
		total, ok := streamImageIntoLoader(
			[]string{"sh", "-c", "printf hello-tar"}, drain, 0, &out, false)
		if !ok {
			t.Fatalf("a clean pipe reported failure: %q", out.String())
		}
		if total != int64(len("hello-tar")) {
			t.Errorf("total = %d, want %d", total, len("hello-tar"))
		}
		if out.String() != "" {
			t.Errorf("a successful stream said something: %q", out.String())
		}
	})

	t.Run("stream fails after a plausible prefix", func(t *testing.T) {
		// THE class a naive io.Copy misses entirely: the loader drains happily, the
		// copy returns nil, and only the stream's exit status says the image is a
		// truncation. (podman's own manifest.json-is-last check is defense in depth;
		// this is the primary guard.)
		var out bytes.Buffer
		total, ok := streamImageIntoLoader(
			[]string{"sh", "-c", "printf partial; echo 'nix: stream exploded' >&2; exit 7"},
			drain, 0, &out, false)
		if ok {
			t.Fatalf("a stream that exited 7 was reported as a successful load (total=%d)", total)
		}
		s := out.String()
		if !strings.Contains(s, "nix image stream failed") {
			t.Errorf("the STREAM end was not named: %q", s)
		}
		if !strings.Contains(s, "exit 7") {
			t.Errorf("the stream's exit status is missing: %q", s)
		}
		if !strings.Contains(s, "nix: stream exploded") {
			t.Errorf("the stream's own words never reached the human: %q", s)
		}
		if strings.Contains(s, "rejected the streamed image") {
			t.Errorf("a stream failure was misreported as a loader rejection: %q", s)
		}
		if total != 0 {
			t.Errorf("a failed stream returned %d bytes; a byte total that survives a "+
				"failure poisons the next run's size estimate", total)
		}
	})

	t.Run("loader rejects a complete stream", func(t *testing.T) {
		var out bytes.Buffer
		_, ok := streamImageIntoLoader(
			[]string{"sh", "-c", "printf wholetar"},
			[]string{"sh", "-c", "cat >/dev/null; echo 'payload does not match any of the " +
				"supported image formats' >&2; exit 125"},
			0, &out, false)
		if ok {
			t.Fatal("a loader that exited 125 was reported as a successful load")
		}
		s := out.String()
		if !strings.Contains(s, "rejected the streamed image") {
			t.Errorf("the LOAD end was not named: %q", s)
		}
		if !strings.Contains(s, "exit 125") {
			t.Errorf("the loader's exit status is missing: %q", s)
		}
		if !strings.Contains(s, "payload does not match") {
			t.Errorf("the loader's own words never reached the human: %q", s)
		}
		if strings.Contains(s, "nix image stream failed") {
			t.Errorf("a loader rejection was misreported as a stream failure: %q", s)
		}
	})

	t.Run("loader dies mid-stream", func(t *testing.T) {
		// The loader exits without reading; the writer then gets EPIPE (Go raises
		// SIGPIPE only for fds 1 and 2). 8 MiB is far past the 64 KiB pipe buffer,
		// so the write cannot complete into the kernel and the break is certain.
		// The diagnosis must name the LOADER — the stream's own broken-pipe exit is
		// downstream of this, not its cause.
		var out bytes.Buffer
		_, ok := streamImageIntoLoader(
			[]string{"sh", "-c", "head -c 8388608 /dev/zero"},
			[]string{"sh", "-c", "exit 3"}, 0, &out, false)
		if ok {
			t.Fatal("a loader that quit mid-stream was reported as a successful load")
		}
		s := out.String()
		if !strings.Contains(s, "stopped reading before the image stream finished") {
			t.Errorf("the broken-reader case was not diagnosed as such: %q", s)
		}
		if !strings.Contains(s, "exit 3") {
			t.Errorf("the LOADER's exit status is missing — reporting the stream's "+
				"SIGPIPE status here sends the reader to the wrong end: %q", s)
		}
		if strings.Contains(s, "nix image stream failed") {
			t.Errorf("blamed the stream for the loader's exit: %q", s)
		}
	})

	t.Run("stream cannot start", func(t *testing.T) {
		var out bytes.Buffer
		if _, ok := streamImageIntoLoader(
			[]string{filepath.Join(t.TempDir(), "no-such-stream")}, drain, 0, &out, false); ok {
			t.Fatal("a stream that never started was reported as a successful load")
		}
		if !strings.Contains(out.String(), "could not start the image stream") {
			t.Errorf("unhelpful start failure: %q", out.String())
		}
	})
}

// TestStreamedBytesDriveProgressAndTheSizeSentinel pins the two things the file
// form got for free by owning a file: the progress line's source of truth, and
// the size the NEXT run estimates from.
//
// It pins CALL SITES, not the formatter: progress_test.go already drives
// newProgressLine directly and stays green if the update call inside the copy
// loop is deleted. This one does not — delete `prog.update(...)` from
// copyCounting and the percentage assertion fails.
func TestStreamedBytesDriveProgressAndTheSizeSentinel(t *testing.T) {
	const streamed = 3 * 1024 * 1024

	t.Run("both green persists the size", func(t *testing.T) {
		withBuildDir(t)
		// Seed the estimate through its (quirked) READ path — SizeFileForSentinel
		// doubles the suffix, so production always falls through to a `nix
		// path-info` probe. Writing it here keeps the test off nix AND exercises the
		// percentage, which is the half of progress the estimate feeds.
		if err := os.WriteFile(SizeFileForSentinel(SizeSentinelPath()),
			[]byte("4194304"), 0o644); err != nil { // 4 MiB → 3 MiB is 75 %
			t.Fatal(err)
		}
		script := writeScript(t, filepath.Join(t.TempDir(), "stream-yolo-jail"),
			"head -c 3145728 /dev/zero")

		var out bytes.Buffer
		total, ok := streamImageToRuntime(script, StreamRepoTag(script),
			[]string{"sh", "-c", "cat >/dev/null"},
			false /* isMacOS */, &out, true /* progressTTY */)
		if !ok {
			t.Fatalf("stream failed: %q", out.String())
		}
		if total != streamed {
			t.Errorf("total = %d, want %d — the count must come from the bytes in "+
				"transit; there is no file to stat", total, streamed)
		}
		s := out.String()
		if !strings.Contains(s, "Streaming image... ") {
			t.Errorf("no progress line: %q", s)
		}
		if !strings.Contains(s, "(75%)") {
			t.Errorf("progress carried no percentage, so the estimate never reached "+
				"FormatProgress: %q", s)
		}
		if strings.Contains(s, "Caching image") {
			t.Errorf("the streaming path claims to be caching: %q", s)
		}
		got, err := os.ReadFile(SizeSentinelPath())
		if err != nil {
			t.Fatalf("the size sentinel was not written: %v", err)
		}
		if strings.TrimSpace(string(got)) != "3145728" {
			t.Errorf("size sentinel = %q, want the streamed total 3145728", string(got))
		}
	})

	t.Run("a failed stream persists nothing", func(t *testing.T) {
		// Hazard 5: a stream that dies at 99 % would otherwise record a short size
		// and silently deflate every future percentage.
		withBuildDir(t)
		script := writeScript(t, filepath.Join(t.TempDir(), "stream-yolo-jail"),
			"head -c 1048576 /dev/zero; exit 9")
		var out bytes.Buffer
		if _, ok := streamImageToRuntime(script, StreamRepoTag(script),
			[]string{"sh", "-c", "cat >/dev/null"}, false, &out, false); ok {
			t.Fatal("a stream that exited 9 was reported as a successful load")
		}
		if _, err := os.Stat(SizeSentinelPath()); err == nil {
			t.Error("a failed stream wrote a size sentinel; the next run would estimate " +
				"from a truncated image")
		}
	})
}

// TestTailWriterKeepsTheLastLines: the stream's stderr used to be assigned nil
// and discarded, which is why a failed materialize could only ever say "Error
// streaming image to cache." with no cause. streamLayeredImage emits one
// "Creating layer N…" line per layer (up to 100), so the bound matters and the
// END is the interesting part.
func TestTailWriterKeepsTheLastLines(t *testing.T) {
	w := &tailWriter{max: 3}
	for _, chunk := range []string{"one\ntwo\n", "three\nfo", "ur\nfive\n"} {
		if _, err := w.Write([]byte(chunk)); err != nil {
			t.Fatal(err)
		}
	}
	// A line split across two Writes must survive as one line.
	if got := strings.Join(w.tail(), "|"); got != "three|four|five" {
		t.Errorf("tail = %q, want %q", got, "three|four|five")
	}

	// An unterminated remainder is still reported — a stream killed mid-line has
	// exactly one line worth reading and it has no \n on it.
	w2 := &tailWriter{max: 3}
	_, _ = w2.Write([]byte("error: killed mid-l"))
	_, _ = w2.Write([]byte("ine"))
	if got := strings.Join(w2.tail(), "|"); got != "error: killed mid-line" {
		t.Errorf("tail = %q, want the flushed remainder", got)
	}
}
