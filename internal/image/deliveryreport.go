package image

import (
	"bytes"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

// The bounded-stderr reporting the delivery path needs, kept intact from
// streamload.go when C9 replaced the stream with a copy
// (docs/design/layer-aware-image-delivery.md). It moved rather than being
// rewritten because the job did not change: a failed delivery must print the
// tool's OWN words, and "Error loading image into podman." with no cause is the
// C1 silent-fallback defect one layer down.
//
// What DID change is how much of it is needed. A pipe had two processes, two
// exit statuses and two stderrs to reconcile — hazards streamload.go spelled out
// at length. A copy is one process, so `exitCodeOf` has one caller and
// `reportPipeEnd`'s name is now a slight anachronism: it reports ONE end,
// because there is only one.

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
