package run

// keeperframe.go is the KEEPER'S OUTPUT: the progress pipe back to the fresh launch that spawned
// it, the launch's relay of that pipe, and the keeper's own log
// (docs/design/jail-lifetime-last-session-wins.md §9.6, JL-D19, JL-D29).
//
// UNTIL READY, everything the keeper prints crosses the pipe, and the launch prints it through its
// own writers, so the terminal and launch.log have every line a launch printed before there was a
// keeper: the services' warnings, the launch checks, and pid 1's boot, which the launch writes to
// the process's own streams as it always did (what the jail prints is not what the launcher said,
// launchlog.go). The pipe also carries the four moments the launch acts on: the keeper started,
// the main process spawned, the container is running (the launch lock is released), and the boot
// is done. Framed, one tag byte and a length, so a partial line or a byte the boot printed never
// splits a frame or reads as an event.
//
// AFTER READY, the keeper writes its host-only log and mirrors its own lines into the workspace's
// launch.log, and writes nothing to the pipe: the first terminal's launch stops reading it when its
// session ends, and a closed pipe must never be what ends a keeper. Its descriptors make a failed
// write EPIPE rather than SIGPIPE (keeper.go), and a write that fails turns the pipe off.
//
// ONE SWITCHABLE WRITER carries the keeper's o.Stdout and o.Stderr (keeperSink), because reused
// code captures its writer when a service starts: startExternalService hands `o.pr(o.Stdout)` to
// the stop closure that runs at the teardown, so pointing o.Stdout elsewhere after ready would not
// move the teardown's lines. Only a writer that switches underneath does.

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// The frame tags. The four text tags carry bytes to write; the four event tags carry a moment.
const (
	frameStdout     byte = 'o' // the keeper's own stdout: the launch's o.Stdout
	frameStderr     byte = 'e' // the keeper's own stderr: the launch's o.Stderr
	frameJailStdout byte = 'J' // pid 1's stdout: the launch process's own stdout
	frameJailStderr byte = 'j' // pid 1's stderr: the launch process's own stderr
	frameStarted    byte = 'S' // the keeper holds its liveness lock; payload is its pid
	frameSpawned    byte = 'C' // the main process's runtime client is started
	frameRunning    byte = 'V' // the container is seen running, and the launch lock is released
	frameReady      byte = 'R' // pid 1's boot is done: the relay ends here
)

// maxFramePayload bounds one frame, so a corrupt length never makes the relay allocate without
// bound. A text write longer than this is split into several frames.
const maxFramePayload = 1 << 20

// writeFrame writes one frame to w.
func writeFrame(w io.Writer, tag byte, payload []byte) error {
	var hdr [5]byte
	hdr[0] = tag
	binary.BigEndian.PutUint32(hdr[1:], uint32(len(payload)))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	if len(payload) == 0 {
		return nil
	}
	_, err := w.Write(payload)
	return err
}

// readFrame reads one frame from r.
func readFrame(r *bufio.Reader) (tag byte, payload []byte, err error) {
	var hdr [5]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return 0, nil, err
	}
	n := binary.BigEndian.Uint32(hdr[1:])
	if n > maxFramePayload {
		return 0, nil, fmt.Errorf("a keeper frame of %d bytes is over the bound", n)
	}
	payload = make([]byte, n)
	if _, err := io.ReadFull(r, payload); err != nil {
		return 0, nil, err
	}
	return hdr[0], payload, nil
}

// keeperEvents are the launch's reactions to the keeper's moments. Any may be nil.
type keeperEvents struct {
	started func(pid int)
	spawned func()
	running func()
	ready   func()
}

// relayKeeper reads the keeper's frames from r until the ready frame or the pipe's end, writing
// the keeper's lines to out/errOut (the launch's teed writers) and pid 1's to jailOut/jailErr (the
// process's own streams), and calling ev at each moment. It returns true when it saw ready.
func relayKeeper(r io.Reader, out, errOut, jailOut, jailErr io.Writer, ev keeperEvents) (ready bool) {
	br := bufio.NewReader(r)
	for {
		tag, payload, err := readFrame(br)
		if err != nil {
			return false
		}
		switch tag {
		case frameStdout:
			_, _ = out.Write(payload)
		case frameStderr:
			_, _ = errOut.Write(payload)
		case frameJailStdout:
			_, _ = jailOut.Write(payload)
		case frameJailStderr:
			_, _ = jailErr.Write(payload)
		case frameStarted:
			if n, err := strconv.Atoi(string(payload)); err == nil && ev.started != nil {
				ev.started(n)
			}
		case frameSpawned:
			if ev.spawned != nil {
				ev.spawned()
			}
		case frameRunning:
			if ev.running != nil {
				ev.running()
			}
		case frameReady:
			if ev.ready != nil {
				ev.ready()
			}
			return true
		}
	}
}

// keeperSink is the keeper's output: the progress pipe while it relays, its host-only log always,
// and after ready a mirror of its own lines in the workspace's launch.log.
type keeperSink struct {
	mu       sync.Mutex
	pipe     io.Writer // nil once the relay is over, or after a write to it failed
	log      io.Writer // the keeper's log; nil when it could not be opened
	mirror   io.Writer // launch.log, after ready; nil before, or when it could not be opened
	logLines lineBuffer
	mirLines lineBuffer
}

// stream is one of the sink's four text streams.
type keeperStream struct {
	s   *keeperSink
	tag byte
}

func (k keeperStream) Write(p []byte) (int, error) {
	k.s.write(k.tag, p)
	return len(p), nil
}

// write sends p to the pipe while it relays and to the log always; the keeper's own lines also
// go to the mirror once there is one. pid 1's lines go to the pipe and the log, never the mirror:
// launch.log is what the launcher said.
func (s *keeperSink) write(tag byte, p []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pipe != nil {
		for rest := p; len(rest) > 0; {
			n := min(len(rest), maxFramePayload)
			if err := writeFrame(s.pipe, tag, rest[:n]); err != nil {
				s.pipe = nil // EPIPE: the launch has stopped reading, which ends nothing here
				break
			}
			rest = rest[n:]
		}
	}
	if s.log != nil {
		s.logLines.add(s.log, stripANSI(p))
	}
	if s.mirror != nil && (tag == frameStdout || tag == frameStderr) {
		s.mirLines.add(s.mirror, stripANSI(p))
	}
}

// event sends one moment to the launch, while the relay is on.
func (s *keeperSink) event(tag byte, payload string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pipe == nil {
		return
	}
	if err := writeFrame(s.pipe, tag, []byte(payload)); err != nil {
		s.pipe = nil
	}
}

// endRelay turns the pipe off and the launch.log mirror on: the jail is ready, and from here on the
// keeper's lines are read from its log, by the session that ends the jail (JL-D11).
func (s *keeperSink) endRelay(mirror io.Writer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pipe = nil
	s.mirror = mirror
}

// setLog gives the sink the keeper's log, once the keeper holds its jail's name (keeper.run).
func (s *keeperSink) setLog(w io.Writer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.log = w
}

// logf writes one line of the keeper's own, on its stderr stream.
func (s *keeperSink) logf(format string, args ...any) {
	s.write(frameStderr, []byte(fmt.Sprintf(format, args...)+"\n"))
}

// logOnlyf writes one line to the keeper's log and nowhere else: a fact about the keeper's own
// process (which scope it runs in, the moment it became ready) that a launch's terminal need not
// carry, and that no session's quit should replay.
func (s *keeperSink) logOnlyf(format string, args ...any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.log != nil {
		s.logLines.add(s.log, []byte(fmt.Sprintf(format, args...)+"\n"))
	}
}

// lineBuffer writes whole lines to w, each prefixed with the time, so a log reads as a timeline and
// a streamed teardown line is never half of one. A partial line waits for its end.
type lineBuffer struct{ pending []byte }

func (b *lineBuffer) add(w io.Writer, p []byte) {
	b.pending = append(b.pending, p...)
	for {
		i := indexNewline(b.pending)
		if i < 0 {
			return
		}
		line := b.pending[:i+1]
		_, _ = fmt.Fprintf(w, "%s %s", time.Now().Format("15:04:05.000"), line)
		b.pending = b.pending[i+1:]
	}
}

func indexNewline(p []byte) int {
	for i, c := range p {
		if c == '\n' {
			return i
		}
	}
	return -1
}

// keeperLogPath is cname's keeper log: <global storage>/logs/jail-keeper-<cname>.log, beside the
// host services' own logs, in host state no jail mounts. One per container name; each keeper
// truncates it at its start, so it holds the current keeper's life.
func keeperLogPath(cname string) string {
	return logsDirPath("jail-keeper-" + cname + ".log")
}

// logsDirPath is a file in the machine's host-service log directory.
func logsDirPath(name string) string {
	return filepath.Join(paths.GlobalStorage(), "logs", name)
}

// openKeeperLog opens (truncating) cname's keeper log, 0600: it names the jail's services, its
// paths and why it ended.
func openKeeperLog(cname string) (*os.File, error) {
	path := keeperLogPath(cname)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	return os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC|os.O_APPEND, 0o600)
}

// keeperLogSize is the keeper log's length now, which a session notes when it begins so its quit
// prints only what the keeper recorded while it was in. 0 when there is none.
func keeperLogSize(cname string) int64 {
	fi, err := os.Stat(keeperLogPath(cname))
	if err != nil {
		return 0
	}
	return fi.Size()
}

// keeperLogFollower reads a keeper log from an offset, whole lines only.
type keeperLogFollower struct {
	path    string
	offset  int64
	pending []byte
}

// next returns the whole lines written since the last call. A log that went away or was
// truncated under it (a new keeper) returns nothing more.
func (f *keeperLogFollower) next() []string {
	file, err := os.Open(f.path)
	if err != nil {
		return nil
	}
	defer file.Close()
	fi, err := file.Stat()
	if err != nil || fi.Size() < f.offset {
		return nil
	}
	if _, err := file.Seek(f.offset, io.SeekStart); err != nil {
		return nil
	}
	buf, err := io.ReadAll(io.LimitReader(file, maxFramePayload))
	if err != nil && !errors.Is(err, io.EOF) {
		return nil
	}
	f.offset += int64(len(buf))
	f.pending = append(f.pending, buf...)
	var lines []string
	for {
		i := indexNewline(f.pending)
		if i < 0 {
			return lines
		}
		lines = append(lines, string(f.pending[:i]))
		f.pending = f.pending[i+1:]
	}
}
