// Package nixstderr reads a nix child's stderr to the end of the stream, however long its lines.
//
// EVERY LINE IS READ TO THE END OF THE STREAM. Each reader this replaced used a bufio.Scanner
// capped at 1 MiB a line, which ended the read at the first longer line; the Wait after it then
// waited on a nix blocked writing into a pipe nothing read any more, and the launch, `yolo check`
// or macos-user package build hung for good, printing nothing. Every nix stderr reader goes
// through Read, so none can get that loop back on its own.
package nixstderr

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"strings"
)

// MaxLine is how much of one line Read keeps.
const MaxLine = 1024 * 1024

// Read reads stderr to the end of the stream, hands each non-blank line to onLine (when non-nil),
// and returns the last keep of them for failure diagnosis. A line longer than MaxLine is cut to it,
// with a note saying so.
func Read(stderr io.Reader, keep int, onLine func(clean string)) []string {
	var tail []string
	r := bufio.NewReaderSize(stderr, 64*1024)
	for {
		line, long, err := readLineCapped(r, MaxLine)
		clean := strings.TrimRight(string(line), " \t\r\n")
		if long {
			clean += fmt.Sprintf(" … (nix printed a line longer than %d bytes; the rest of it is not kept)", MaxLine)
		}
		if clean != "" {
			tail = append(tail, clean)
			if len(tail) > keep {
				tail = tail[1:]
			}
			if onLine != nil {
				onLine(clean)
			}
		}
		if err != nil {
			return tail
		}
	}
}

// readLineCapped reads one line from r, keeping at most max bytes of it (its newline dropped) and
// consuming the rest, so a line of any length is read whole. long reports a line cut short; err is
// r's: io.EOF at the end of the stream, with whatever an unterminated last line held.
func readLineCapped(r *bufio.Reader, max int) (line []byte, long bool, err error) {
	for {
		frag, err := r.ReadSlice('\n')
		frag = bytes.TrimSuffix(frag, []byte{'\n'})
		if room := max - len(line); len(frag) > room {
			long = true
			frag = frag[:room]
		}
		line = append(line, frag...)
		if err != bufio.ErrBufferFull {
			return line, long, err
		}
	}
}
