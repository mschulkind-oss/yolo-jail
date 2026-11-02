package loopholegrant

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"syscall"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerlock"
)

// live.go is the host side of a live grant change (design §4.6, "Live apply"): `yolo config
// apply` finds the grant files of a running jail's mediated loopholes (LiveFiles) and rewrites
// their grant block (Replace), and the daemon takes the new standing at its next request, as a
// broker takes a scope at its next call (brokerscope.LiveFiles, ReplaceScopeContext). The block
// is still written only from a gate's approved result, never from re-reading the config.

// LiveFile is one grant file a running launch's daemon is bound to, as read.
type LiveFile struct {
	Path  string
	Block Block
}

// LiveFiles is every grant file in stateDir, the loophole's state dir, whose block names this
// container and workspace and whose launching process is alive. A missing state dir, a file
// with no grant block and a file that does not read are no live file.
func LiveFiles(ctx context.Context, stateDir, container, workspace string) ([]LiveFile, error) {
	if ctx == nil {
		return nil, errors.New("grant file discovery requires a context")
	}
	entries, err := os.ReadDir(stateDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []LiveFile
	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !e.Type().IsRegular() {
			continue
		}
		p := filepath.Join(stateDir, e.Name())
		_, b, err := Read(p)
		if err != nil || b == nil || b.Container != container || b.Workspace != workspace || !processAlive(b.PID) {
			continue
		}
		out = append(out, LiveFile{Path: p, Block: *b})
	}
	return out, nil
}

// processAlive is kill(pid, 0) answering anything but ESRCH: a process this user may not
// signal still exists.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	return !errors.Is(syscall.Kill(pid, 0), syscall.ESRCH)
}

// Replace rewrites the grant block of the file at path to next and keeps its settings as
// written, atomically, under the state dir's lock so two applies never interleave. It refuses
// a symlink or other non-regular file, a file whose block is not expect's identity (the jail
// this was approved for is not the one running), and a next that changes that identity.
func Replace(ctx context.Context, path string, expect Block, next Block) error {
	if ctx == nil {
		return errors.New("grant file replacement requires a context")
	}
	if !next.sameIdentity(expect) {
		return errors.New("grant file: the new grants name another launch")
	}
	if err := next.check(); err != nil {
		return err
	}
	dir := filepath.Dir(path)
	return brokerlock.WithTransactionContext(ctx, dir, func() error {
		data, err := readCapped(path, fileCap)
		if err != nil {
			return err
		}
		body, raw, err := split(data)
		if err != nil {
			return err
		}
		if raw == nil {
			return errors.New("grant file holds no grants; relaunch the jail")
		}
		var cur Block
		if err := json.Unmarshal(raw, &cur); err != nil {
			return err
		}
		if !cur.sameIdentity(expect) {
			return errors.New("grant identity changed; the jail these grants were approved for is not the one running")
		}
		out, err := join(body, next)
		if err != nil {
			return err
		}
		tmp, err := os.CreateTemp(dir, ".grants-*")
		if err != nil {
			return err
		}
		defer func() { _ = os.Remove(tmp.Name()) }()
		if err := tmp.Chmod(0o600); err != nil {
			_ = tmp.Close()
			return err
		}
		if _, err := tmp.Write(out); err != nil {
			_ = tmp.Close()
			return err
		}
		if err := tmp.Close(); err != nil {
			return err
		}
		return os.Rename(tmp.Name(), path)
	})
}
