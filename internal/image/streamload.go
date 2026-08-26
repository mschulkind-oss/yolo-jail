package image

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

// C3 — DELIVER THE IMAGE AS A PIPE, NOT AS A FILE.
//
// The nix out-link for `.#ociImage` is not a tar; it is an executable
// (`streamLayeredImage`) whose stdout IS the tar. `podman load` reads a tar from
// stdin when given no `-i`. So the two fit together directly, and the
// cache/images/<key>.tar that used to sit between them was a redundant third
// copy of an image the nix store and podman's store both already hold. `just
// load` has demonstrated the shell form all along (`./result | podman load`,
// Justfile); this is that, in Go, with the failure detection a shell pipeline
// does not give you.
//
// WHAT A NAIVE io.Copy MISSES, and therefore what this file is mostly about.
// There are two processes, and every interesting failure is a disagreement
// between them:
//
//  1. The STREAM exits nonzero after emitting a plausible prefix. io.Copy sees
//     EOF, returns nil, and the pipeline looks fine. Only Wait()-ing the stream
//     catches it. materializeImage already guarded this for the file form; a pipe
//     rewrite is exactly where such a guard gets dropped.
//  2. The LOADER exits early, so our write gets EPIPE. (Go raises SIGPIPE only
//     for fds 1 and 2, so on a pipe this surfaces as an error, not a signal.)
//     The right diagnosis then names the LOAD end — the stream's own
//     broken-pipe status is a consequence, not a cause.
//  3. Deadlock. Hold the pipe's write end open and the loader never sees EOF;
//     Wait() the stream before the loader has drained and a full pipe buffer
//     stalls. Hence the fixed order below: finish the copy, close the write end,
//     wait on the READER, then on the WRITER.
//  4. A truncated stream the loader ACCEPTS. Measured: podman exits 125 with
//     `docker-archive: loading tar component "manifest.json": unexpected EOF`,
//     because streamLayeredImage writes manifest.json last. That is defense in
//     depth and an ordering property of nixpkgs, not a promise from podman — so
//     the primary check stays (1), the stream's own exit status.
//  5. A byte total that lies. If only the loader's status were checked, a stream
//     that died at 99 % could be recorded as this image's size. The sentinel is
//     written on a both-green outcome and never otherwise.
//
//     Be precise about what that currently buys, because the obvious reading is
//     wrong: NOTHING READS THE FILE THIS WRITES. The writer targets
//     BUILD_DIR/last-load-size and the only reader, estimateImageSize, reads
//     SizeFileForSentinel(...) = BUILD_DIR/last-load-size-size — the doubled
//     suffix is a deliberately preserved Python quirk, documented on
//     SizeFileForSentinel, so every load falls through to a `nix path-info
//     --closure-size` probe instead. The write is kept correct rather than
//     deleted because closing that loop is a live option (it would drop one
//     subprocess per load); until someone takes it, this guard protects a file
//     that is not yet consulted.
//
// The one thing this does NOT remove is a transient full-size write: `podman
// load` spools stdin before parsing (a `-i <file>` load does not). It spools to
// its own `image_copy_tmp_dir`, NOT to $TMPDIR — measured 2026-08-25 by sampling
// during a real streamed load: /var/tmp/podman* grew to 3554600960 B and was
// then unlinked, while nothing appeared under /tmp. The lever is containers.conf
// `image_copy_tmp_dir` (`podman info --format '{{.Store.ImageCopyTmpDir}}'`);
// exporting TMPDIR does nothing. What C3 deletes is the RETAINED copy, which is
// the artifact OQ-5 ruled a bug.

// streamTailLines is how many stderr lines each end of the pipe retains for a
// failure report. streamLayeredImage's stderr is bounded and mostly progress
// ("Creating layer N from paths: …", one per layer, maxLayers = 100), so the
// interesting part is always at the end; podman's is a handful of lines.
const streamTailLines = 12

// streamImageToRuntime is the StreamLoad seam's real implementation: resolve the
// stream argv for this platform, NAME the image inside the archive, join it to
// loadArgv with a pipe, and — only on a both-green outcome — persist the
// streamed size (see hazard 5 on why that file has no reader today).
func streamImageToRuntime(storePath, repoTag string, loadArgv []string, isMacOS bool, out io.Writer, progressTTY bool) (int64, bool) {
	sentinel := SizeSentinelPath()
	total, ok := streamImageIntoLoader(
		streamImageArgv(storePath, repoTag, isMacOS), loadArgv,
		estimateImageSize(storePath, sentinel), out, progressTTY)
	if !ok {
		return 0, false
	}
	// Guarded on total>0 rather than folded into ok: a legitimately empty stream
	// and a failure must not be the same value (the sentinel-vs-error confusion
	// materializeImage's `0 means failure` return still carries). A zero here
	// would be podman accepting an empty archive, which it does not do — but
	// writing 0 would silently disable every future estimate, so it is not
	// written.
	if total > 0 {
		_ = os.WriteFile(sentinel, []byte(strconv.FormatInt(total, 10)), 0o644)
	}
	return total, true
}

// streamImageIntoLoader runs streamArgv | loadArgv and reports (bytes, ok),
// printing the actionable reason itself when ok is false — because only here is
// it known which end broke, and "Error loading image into podman." with no cause
// is the C1 defect one layer down.
//
// estimate feeds the progress percentage (0 => a bare byte counter, which
// FormatProgress degrades to cleanly).
//
// Taking both argvs as parameters is what makes this testable without nix or
// podman: any pair of processes exercises the same mechanics, so the four
// failure classes above are pinned by shell one-liners rather than by a 3.28 GiB
// image.
func streamImageIntoLoader(streamArgv, loadArgv []string, estimate int64, out io.Writer, progressTTY bool) (int64, bool) {
	if len(streamArgv) == 0 || len(loadArgv) == 0 {
		fmt.Fprintln(out, "Error: no image stream or loader command to run.")
		return 0, false
	}
	loaderName := strings.Join(loadArgv, " ")

	stream := exec.Command(streamArgv[0], streamArgv[1:]...)
	streamTail := &tailWriter{max: streamTailLines}
	stream.Stderr = streamTail
	streamOut, err := stream.StdoutPipe()
	if err != nil {
		fmt.Fprintln(out, "Error: could not pipe the image stream's output: "+err.Error())
		return 0, false
	}

	load := exec.Command(loadArgv[0], loadArgv[1:]...)
	loadTail := &tailWriter{max: streamTailLines}
	load.Stderr = loadTail
	// nil Stdout is /dev/null: podman's "Loaded image: …" line duplicates the
	// "Done: loaded image" the caller prints, and the file form discarded it too.
	load.Stdout = nil
	loadIn, err := load.StdinPipe()
	if err != nil {
		fmt.Fprintln(out, "Error: could not pipe into "+loaderName+": "+err.Error())
		return 0, false
	}

	// The LOADER starts first so it is already waiting on the pipe; a stream that
	// cannot start then costs nothing but a wait we immediately unblock.
	if err := load.Start(); err != nil {
		fmt.Fprintln(out, "Error: could not start "+loaderName+": "+err.Error())
		return 0, false
	}
	if err := stream.Start(); err != nil {
		_ = loadIn.Close()
		_ = load.Wait()
		fmt.Fprintln(out, "Error: could not start the image stream ("+streamArgv[0]+"): "+err.Error())
		return 0, false
	}

	prog := newProgressLine(out, progressTTY, "Streaming image... ")
	total, copyErr := copyCounting(loadIn, streamOut, prog, estimate)
	prog.done()

	// ORDER IS LOAD-BEARING (hazard 3), and BOTH closes are mandatory. This is the
	// shape that deadlocked when it was first written — the loader-dies-mid-stream
	// test below hung the package for 142 s until the second close was added:
	//
	//   - Close the WRITE end or the loader never sees EOF and load.Wait() blocks.
	//   - Close the READ end BEFORE stream.Wait(). exec.Cmd.Wait waits for the
	//     process FIRST and closes the parent's pipes only after it exits, so a
	//     stream still blocked writing into a pipe nobody is draining (every
	//     aborted-copy path) never exits and Wait never returns. Closing it here
	//     breaks the stream's next write, which is the only signal it has. On the
	//     happy path the copy already read to EOF, so this is a no-op.
	_ = loadIn.Close()
	_ = load.Wait()
	_ = streamOut.Close()
	_ = stream.Wait()

	streamCode, streamKnown := exitCodeOf(stream)
	loadCode, loadKnown := exitCodeOf(load)

	switch {
	case copyErr != nil && errors.Is(copyErr, syscall.EPIPE):
		// Hazard 2: the reader died first. Report the LOAD end; the stream's
		// broken-pipe exit is downstream of this, not the cause of it.
		reportPipeEnd(out, loaderName+" stopped reading before the image stream finished",
			loadCode, loadKnown, loadTail.tail())
	case copyErr != nil:
		fmt.Fprintln(out, "Error: could not pipe the image into "+loaderName+": "+copyErr.Error())
		printTail(out, "the image stream said", streamTail.tail())
	case !streamKnown || streamCode != 0:
		// Hazard 1 (and 4): the stream failed, possibly after emitting enough for
		// the loader to look happy. This is the check a bare io.Copy has no way to
		// make, and the one that keeps a truncated image from loading silently.
		reportPipeEnd(out, "the nix image stream failed; no image was loaded",
			streamCode, streamKnown, streamTail.tail())
	case !loadKnown || loadCode != 0:
		reportPipeEnd(out, loaderName+" rejected the streamed image",
			loadCode, loadKnown, loadTail.tail())
	default:
		return total, true
	}
	return 0, false
}

// copyCounting copies src→dst in 1 MiB chunks, tallying the bytes AS THEY PASS
// and driving the progress line from that tally.
//
// The tally is the whole reason this is not io.Copy. materializeImage could
// measure its own output file; a pipe has no file to measure, so the count has
// to happen in transit or the byte-progress UI and the size sentinel both lose
// their source of truth. io.MultiWriter would double the work and swallow which
// writer failed.
func copyCounting(dst io.Writer, src io.Reader, prog *progressLine, estimate int64) (int64, error) {
	buf := make([]byte, 1024*1024)
	var total int64
	for {
		n, rerr := src.Read(buf)
		if n > 0 {
			if _, werr := dst.Write(buf[:n]); werr != nil {
				return total, werr
			}
			total += int64(n)
			prog.update(total, estimate)
		}
		if rerr != nil {
			if errors.Is(rerr, io.EOF) {
				return total, nil
			}
			return total, rerr
		}
	}
}

// exitCodeOf reports a finished command's exit code, ok=false when it produced
// no status at all. Mirrors the `cmd.ProcessState == nil || ExitCode() != 0`
// guard materializeImage has always made, kept explicit because a pipe has two
// of them and "no status" must not read as "exited 0".
func exitCodeOf(cmd *exec.Cmd) (int, bool) {
	if cmd.ProcessState == nil {
		return 0, false
	}
	return cmd.ProcessState.ExitCode(), true
}

// reportPipeEnd prints one end's failure with its exit status and its own words.
func reportPipeEnd(out io.Writer, what string, code int, known bool, tail []string) {
	if known {
		fmt.Fprintf(out, "Error: %s (exit %d).\n", what, code)
	} else {
		fmt.Fprintf(out, "Error: %s (no exit status).\n", what)
	}
	printTail(out, "it said", tail)
}

func printTail(out io.Writer, lead string, tail []string) {
	if len(tail) == 0 {
		return
	}
	fmt.Fprintf(out, "  What %s (last %d line(s)):\n", lead, len(tail))
	for _, line := range tail {
		fmt.Fprintln(out, "    | "+line)
	}
}

// tailWriter keeps the LAST max lines written to it and discards the rest — the
// bounded-stderr idiom buildImageStorePathArgs uses for nix, applied one layer
// down. Before C3 the stream's stderr was assigned to nil and thrown away, which
// is why a failed materialize could only ever say "Error streaming image to
// cache." with no cause.
//
// No locking: exec.Cmd's copy goroutine is the only writer and Wait() joins it,
// so tail() is safe to call after Wait() and only after.
type tailWriter struct {
	max   int
	lines []string
	part  []byte
}

func (w *tailWriter) Write(p []byte) (int, error) {
	w.part = append(w.part, p...)
	for {
		i := bytes.IndexByte(w.part, '\n')
		if i < 0 {
			break
		}
		w.push(string(w.part[:i]))
		w.part = append(w.part[:0], w.part[i+1:]...)
	}
	// A single unterminated line must not grow without bound (a stream that
	// writes progress with \r and never a \n would otherwise buffer forever).
	if len(w.part) > 64*1024 {
		w.push(string(w.part))
		w.part = w.part[:0]
	}
	return len(p), nil
}

func (w *tailWriter) push(line string) {
	line = strings.TrimRight(line, " \t\r")
	if line == "" {
		return
	}
	w.lines = append(w.lines, line)
	if len(w.lines) > w.max {
		w.lines = w.lines[len(w.lines)-w.max:]
	}
}

// tail flushes any unterminated remainder and returns the retained lines. Call
// it only AFTER Wait().
func (w *tailWriter) tail() []string {
	if len(w.part) > 0 {
		w.push(string(w.part))
		w.part = w.part[:0]
	}
	return w.lines
}
