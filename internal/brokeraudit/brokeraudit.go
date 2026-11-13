// Package brokeraudit is the brokered sources' audit log: one JSON line per brokered call,
// appended by every broker on the machine to one host file
// (docs/design/boundary-broker.md §8, BB-D15).
//
// It is SERVICE-NEUTRAL by construction (`service` in every record, §13), so a second
// broker writes the same log with the same writer, and `yolo audit` reads it.
//
// # What it promises
//
//   - One line per event, appended under one lock, so lines from several brokers never
//     interleave mid-line.
//   - 0600, in a 0700 directory, beside the brokers' store rather than in logs/: a `mounts`
//     entry commonly exposes logs/, and a line carries argv values in full from every
//     workspace on the machine. BB-D26's mount fence covers this directory.
//   - Bounded, not pruned: rotated at RotateBytes, Archives kept, so at most
//     (Archives+1)×RotateBytes on disk. `yolo prune` never touches it.
//   - Standing-read Append is best-effort and warns once on failure. Required write
//     authorization evidence fails closed and is synced before executor dispatch.
package brokeraudit

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"
)

// RotateBytes is the size past which the log rotates; Archives is how many rotated files
// are kept (audit.jsonl.1 is the newest).
const (
	RotateBytes = 8 << 20
	Archives    = 4
)

// Stdin is a digest of the bytes a call read from the jail: their count and SHA-256,
// never the bytes.
type Stdin struct {
	Bytes  int    `json:"bytes"`
	SHA256 string `json:"sha256"`
}

// RefUpdate is one ref command of a git push: the ref, its old and new object ids, and
// what covered it (a grant kind or a working-set `w-` id), empty when nothing did.
type RefUpdate struct {
	Ref       string `json:"ref"`
	Old       string `json:"old"`
	New       string `json:"new"`
	CoveredBy string `json:"covered_by,omitempty"`
}

// Event is one audit line. The delivery-gated handler also records request, decision
// and grant-used authorization evidence; lifetime teardown records grant-ended.
type Event struct {
	Time    string `json:"time"`
	Event   string `json:"event"`
	Service string `json:"service"`
	// Server and Generation identify versioned MCP sign-in events without storing entry data.
	Server     string `json:"server,omitempty"`
	Generation int    `json:"generation,omitempty"`
	// Jail and Workspace are HOST-ASSERTED: the jail id from the connection preamble and the
	// workspace the launch recorded in its scope file, never a field the jail sent.
	Jail      string `json:"jail"`
	Workspace string `json:"workspace,omitempty"`
	// Agent is what the forwarder reported, and is marked so.
	Agent         string `json:"agent,omitempty"`
	AgentReported bool   `json:"agent_reported,omitempty"`
	// Argv is the canonical argv the broker built, values in full; for a call that did
	// not parse, the argv the jail sent.
	Argv  []string `json:"argv"`
	Stdin *Stdin   `json:"stdin,omitempty"`
	// CommandDigest binds a request or grant-use event to the canonical argv/repository/stdin
	// action digest held by the broker store. StdinDigest names the exact stdin bytes' digest.
	CommandDigest string `json:"command_digest,omitempty"`
	StdinDigest   string `json:"stdin_digest,omitempty"`
	// Repo is the repository the command touched, when it names one. For git it is the
	// repository identity (host/path, docs/reference/git-broker.md ID-3).
	Repo string `json:"repo,omitempty"`
	// Remote is the approved remote URL a git call connected to, verbatim.
	Remote string `json:"remote,omitempty"`
	// Refs is one entry per ref command of a git push, in the push's order.
	Refs []RefUpdate `json:"refs,omitempty"`
	// Set is the permission set the classifier assigned, or "refused" or "out-of-scope".
	Set string `json:"set"`
	// Cell is the GitHub cell whose working-set line covered the write, as its path
	// (`working_set.comment_prs`; docs/design/github-write-scopes.md GW-D13), "" for any other cover.
	Cell string `json:"cell,omitempty"`
	// Outcome is `ran`, `canceled` (the host command may have partially completed), `pending`,
	// `denied` or `refused`.
	Outcome string `json:"outcome"`
	// Reason says why a call did not run.
	Reason     string `json:"reason,omitempty"`
	Request    string `json:"request,omitempty"`
	Grant      string `json:"grant,omitempty"`
	Exit       *int   `json:"exit,omitempty"`
	BytesOut   int64  `json:"bytes_out"`
	ElapsedMs  int64  `json:"elapsed_ms"`
	Redactions int    `json:"redactions"`
	GHVersion  string `json:"gh_version,omitempty"`
}

// Log appends events to one file.
type Log struct {
	path string
	warn func(string)

	mu     sync.Mutex
	warned bool
	now    func() time.Time
	// rotateAt is RotateBytes outside tests.
	rotateAt int64
}

// Open returns a Log writing to path. warn receives the one message a failing write
// produces per process; nil discards it. The file is created on the first write.
func Open(path string, warn func(string)) *Log {
	if warn == nil {
		warn = func(string) {}
	}
	return &Log{path: path, warn: warn, now: time.Now, rotateAt: RotateBytes}
}

// Append writes one event, stamping Time when it is empty. It never returns an error: a
// failure is warned about once and the call goes on.
func (l *Log) Append(e Event) {
	if e.Time == "" {
		e.Time = l.now().UTC().Format(time.RFC3339)
	}
	line, err := json.Marshal(e)
	if err != nil {
		l.fail(err)
		return
	}
	line = append(line, '\n')
	if err := l.write(line); err != nil {
		l.fail(err)
	}
}

// AppendContext keeps a best-effort post-call event from extending a canceled wait.
func (l *Log) AppendContext(ctx context.Context, e Event) { _ = l.RequiredContext(ctx, e) }

// Required durably appends authorization evidence. A failing caller must not
// dispatch a host write. Standing read audit remains best-effort via Append.
func (l *Log) Required(e Event) error { return l.RequiredContext(context.Background(), e) }

// RequiredContext also cancels while waiting for another process's audit lock.
func (l *Log) RequiredContext(ctx context.Context, e Event) error {
	if e.Time == "" {
		e.Time = l.now().UTC().Format(time.RFC3339)
	}
	line, err := json.Marshal(e)
	if err != nil {
		return err
	}
	if err = l.writeContext(ctx, append(line, '\n')); err != nil {
		l.fail(err)
		return err
	}
	return nil
}

func (l *Log) fail(err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.warned {
		return
	}
	l.warned = true
	l.warn(fmt.Sprintf("broker audit log %s: %v (further failures are not reported; calls are not blocked)", l.path, err))
}

// write appends line under the directory's lock, rotating first when the line would take
// the file past RotateBytes. The lock is a separate file so that a rotation never leaves a
// writer holding a lock on a file that has since been renamed to an archive.
func (l *Log) write(line []byte) error { return l.writeContext(context.Background(), line) }
func (l *Log) writeContext(ctx context.Context, line []byte) error {
	dir := filepath.Dir(l.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	lock, err := os.OpenFile(filepath.Join(dir, ".audit.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Millisecond):
		}
	}
	// Released by the Close above as well; unlocking first keeps the order readable.
	defer func() { _ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN) }()

	if fi, err := os.Stat(l.path); err == nil && fi.Size()+int64(len(line)) > l.rotateAt && fi.Size() > 0 {
		rotate(l.path)
	}
	f, err := os.OpenFile(l.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	// A file created by an older writer, or by hand, keeps its mode on O_APPEND; the log
	// carries every workspace's argv, so it is held to owner-only either way.
	_ = f.Chmod(0o600)
	if _, err = f.Write(line); err != nil {
		return err
	}
	return f.Sync()
}

// rotate shifts audit.jsonl → .1 → … → .Archives, dropping the oldest.
func rotate(path string) {
	_ = os.Remove(archive(path, Archives))
	for i := Archives - 1; i >= 1; i-- {
		_ = os.Rename(archive(path, i), archive(path, i+1))
	}
	_ = os.Rename(path, archive(path, 1))
}

func archive(path string, n int) string { return path + "." + strconv.Itoa(n) }

// Files lists the log and its archives that exist, oldest first.
func Files(path string) []string {
	var out []string
	for i := Archives; i >= 1; i-- {
		if _, err := os.Stat(archive(path, i)); err == nil {
			out = append(out, archive(path, i))
		}
	}
	if _, err := os.Stat(path); err == nil {
		out = append(out, path)
	}
	return out
}

// Read returns every event in the log and its archives, oldest first. A line that does not
// decode is skipped and counted, so one torn line never hides the rest.
func Read(path string) (events []Event, skipped int, err error) {
	for _, f := range Files(path) {
		fh, oerr := os.Open(f)
		if oerr != nil {
			return events, skipped, oerr
		}
		sc := bufio.NewScanner(fh)
		sc.Buffer(make([]byte, 0, 64<<10), 32<<20)
		for sc.Scan() {
			var e Event
			if json.Unmarshal(sc.Bytes(), &e) != nil {
				skipped++
				continue
			}
			events = append(events, e)
		}
		serr := sc.Err()
		fh.Close()
		if serr != nil {
			return events, skipped, serr
		}
	}
	return events, skipped, nil
}
