package packsrc

// tail.go is how a launch shows what a pack fetch or checkout is doing right now, under its
// progress line (internal/progress's tail), instead of only a timer.
//
// THE TAIL IS A WRITER A CALLER HANDS THE STORE (WithTail), never a dependency on the
// renderer: packsrc knows only io.Writer. With one set, every git run of the store also writes
// its stderr there, and a clone or a fetch is run with --progress, since git prints its
// "Receiving objects: 41% (…)" frames to a stderr that is not a terminal only when asked. The
// frames are redrawn with carriage returns, which the tail collapses to the last one. Nothing a
// run returns or logs changes: the same stderr still goes into a failed run's error, with its
// frames collapsed to the last (lastFrames), and a store with no tail is byte-for-byte what it
// was.

import (
	"io"
	"os/exec"
	"slices"
	"strings"
)

// TailOpener opens one named stream of a progress line's tail (progress.Line.Tail through a
// closure): the name says which repository or pack the rows are about, and Close removes them.
// A nil TailOpener is no tail.
type TailOpener func(name string) io.WriteCloser

// Open is t(name), or nil when t is nil, so a caller opens and closes without a branch.
func (t TailOpener) Open(name string) io.WriteCloser {
	if t == nil {
		return nil
	}
	return t(name)
}

// WithTail returns a copy of the store whose git runs also write their stderr to w, and whose
// clones and fetches report progress. A nil w is the store itself.
func (s *Store) WithTail(w io.Writer) *Store {
	if w == nil {
		return s
	}
	c := *s
	c.tail = w
	return &c
}

// Say writes one status line to the store's tail: the per-item line a step with no subprocess
// output of its own shows ("fetching ssh://…"). No tail discards it.
func (s *Store) Say(line string) {
	if s.tail != nil {
		_, _ = io.WriteString(s.tail, line+"\n")
	}
}

// teeStderr is where a run's stderr goes: its own buffer, and the tail's writer too when set.
func (s *Store) teeStderr(buf *strings.Builder) io.Writer {
	if s.tail == nil {
		return buf
	}
	return io.MultiWriter(buf, s.tail)
}

// progressArgs is a clone's or fetch's arguments with --progress after the subcommand when the
// store has a tail, and args as given otherwise.
func (s *Store) progressArgs(args ...string) []string {
	if s.tail == nil || len(args) == 0 {
		return args
	}
	return slices.Insert(slices.Clone(args), 1, "--progress")
}

// tailRun points a command a run helper does not run (a fetch whose output is dropped) at the
// tail, with --progress after its `fetch`. No tail leaves it alone.
func (s *Store) tailRun(cmd *exec.Cmd) {
	if s.tail == nil {
		return
	}
	cmd.Stderr = s.tail
	if i := slices.Index(cmd.Args, "fetch"); i >= 0 {
		cmd.Args = slices.Insert(cmd.Args, i+1, "--progress")
	}
}

// lastFrames collapses each line of git's stderr to its last carriage-return frame, which is
// all a progress meter's redraws mean once they are text: "Receiving objects: 1%\r…\r100%".
func lastFrames(s string) string {
	if !strings.Contains(s, "\r") {
		return s
	}
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		l = strings.TrimRight(l, "\r")
		if j := strings.LastIndex(l, "\r"); j >= 0 {
			l = l[j+1:]
		}
		lines[i] = l
	}
	return strings.Join(lines, "\n")
}

// RepoLabel is the short name a stream for repo carries: its last two path elements, without a
// trailing .git ("ssh://git@host/yolo-packs/matt.git" is "yolo-packs/matt").
func RepoLabel(repo string) string {
	r := strings.TrimSuffix(strings.TrimRight(repo, "/"), ".git")
	r = strings.ReplaceAll(r, ":", "/")
	var parts []string
	for _, p := range strings.Split(r, "/") {
		if p != "" {
			parts = append(parts, p)
		}
	}
	if len(parts) > 2 {
		parts = parts[len(parts)-2:]
	}
	return strings.Join(parts, "/")
}
