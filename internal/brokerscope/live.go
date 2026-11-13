package brokerscope

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerlock"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// live.go is the host side of a live scope change (docs/design/live-config-apply.md §7): `yolo
// config apply` finds the scope files of the brokers serving a running jail (LiveFiles) and
// rewrites only their repositories (ReplaceRepos), inside the source's lock transaction that
// every broker decision already takes, so a broker sees the old scope or the new one and never
// a torn file. The scope is still written only from a gate's approved result, never from
// re-reading the config.

// LiveFile is one scope file a running broker is bound to, as read.
type LiveFile struct {
	Path string
	File File // as read, identity fields included
}

// LiveFiles reads under the source's lock transaction. A missing scope directory is an empty set.
func LiveFiles(source, container, workspace string) ([]LiveFile, error) {
	return LiveFilesContext(context.Background(), source, container, workspace)
}

// LiveFilesContext is LiveFiles with context-bounded source lock acquisition.
func LiveFilesContext(ctx context.Context, source, container, workspace string) ([]LiveFile, error) {
	return liveFiles(ctx, source, func(f File) bool { return f.Container == container && f.Workspace == workspace })
}

// EveryLiveFile is every scope file of source a running broker is bound to, whatever its
// container or workspace: `yolo approve` lists the working-set branches each one's approved
// grants declare (docs/reference/git-broker.md GB-D13).
func EveryLiveFile(ctx context.Context, source string) ([]LiveFile, error) {
	return liveFiles(ctx, source, func(File) bool { return true })
}

// liveFiles reads, under the source's lock transaction, the scope files of source whose
// launch and broker are both alive and that match keeps.
func liveFiles(ctx context.Context, source string, keep func(File) bool) ([]LiveFile, error) {
	if ctx == nil {
		return nil, errors.New("scope discovery requires a context")
	}
	if source == "" || strings.ContainsAny(source, "/\\.") {
		return nil, errors.New("scope file: bad source")
	}
	var out []LiveFile
	err := brokerlock.WithTransactionContext(ctx, paths.BrokerSourceDir(source), func() error {
		dir := filepath.Dir(paths.BrokerScopeFile(source, "x"))
		entries, err := os.ReadDir(dir)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		for _, e := range entries {
			if !e.Type().IsRegular() || !strings.HasSuffix(e.Name(), ".json") {
				continue
			}
			p := filepath.Join(dir, e.Name())
			f, err := ReadFile(p)
			if err != nil || f.Source != source || !keep(f) {
				continue
			}
			if !processAlive(f.PID) || !processAlive(f.BrokerPID) {
				continue
			}
			out = append(out, LiveFile{Path: p, File: f})
		}
		return nil
	})
	return out, err
}

// processAlive is kill(pid, 0) answering anything but ESRCH: a process this user may not
// signal still exists.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	return !errors.Is(syscall.Kill(pid, 0), syscall.ESRCH)
}

// ReplaceRepos rewrites the Repos of the scope file at path and nothing else, under the
// source's transaction. It refuses a path that is not expect's launch path, a symlink, an
// entry the file's form rejects (ValidPattern, or ValidRemoteURL for FormRemoteURL), and a file
// whose identity differs from expect.
func ReplaceRepos(path string, expect File, repos []string) error {
	return ReplaceReposContext(context.Background(), path, expect, repos)
}

// ReplaceReposContext is ReplaceRepos with context-bounded source lock acquisition. The file's
// grants are left as they are.
func ReplaceReposContext(ctx context.Context, path string, expect File, repos []string) error {
	return replaceScope(ctx, path, expect, repos, nil, false)
}

// ReplaceScopeContext is ReplaceReposContext that also replaces the file's grants
// (docs/reference/git-broker.md ID-10): `yolo config apply` hands a running broker the
// repositories and the grants its gate approved together. A grant ValidGrant refuses is refused
// with the file left as it was.
func ReplaceScopeContext(ctx context.Context, path string, expect File, repos []string, grants []Grant) error {
	return replaceScope(ctx, path, expect, repos, grants, true)
}

func replaceScope(ctx context.Context, path string, expect File, repos []string, grants []Grant, setGrants bool) error {
	if ctx == nil {
		return errors.New("scope replacement requires a context")
	}
	if expect.Source == "" || expect.LaunchID == "" || strings.ContainsAny(expect.Source+expect.LaunchID, "/\\.") {
		return errors.New("scope file: bad source or launch id")
	}
	if path != paths.BrokerScopeFile(expect.Source, expect.LaunchID) {
		return errors.New("scope path is not the host launch path")
	}
	// The entries are checked against the form of the file as read under the lock, never the
	// caller's idea of it: the file is what its broker will read them as.
	checkEntries := func(form string) error {
		for _, r := range repos {
			if validEntry(form, r) {
				continue
			}
			if form == FormCallRoot {
				return errors.New("scope file: invalid call-broker root; check the selected pack")
			}
			if form == FormRemoteURL {
				return errors.New("scope file: an entry is not a remote URL the remote-url scope can hold " +
					"(empty, with a space or control character, or carrying a credential)")
			}
			return fmt.Errorf("scope file: %q is not an OWNER/REPO or a whole-segment wildcard", r)
		}
		return nil
	}
	if setGrants {
		if err := checkGrants(grants); err != nil {
			return err
		}
	}
	return brokerlock.WithTransactionContext(ctx, paths.BrokerSourceDir(expect.Source), func() error {
		fi, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if !fi.Mode().IsRegular() {
			return errors.New("scope file is not a regular file")
		}
		f, err := ReadFile(path)
		if err != nil {
			return err
		}
		if f.Source != expect.Source || f.LaunchID != expect.LaunchID || f.Container != expect.Container ||
			f.Workspace != expect.Workspace || f.PID != expect.PID {
			return errors.New("scope identity changed; the jail this scope was approved for is not the one running")
		}
		if f.Form != "" && f.Form != FormRemoteURL && f.Form != FormCallRoot {
			return errors.New("scope file of an unknown form; relaunch the jail")
		}
		if err := checkEntries(f.Form); err != nil {
			return err
		}
		f.Repos = append([]string{}, repos...)
		if setGrants {
			f.Grants = append([]Grant(nil), grants...)
		}
		got, err := write(f)
		if err != nil {
			return err
		}
		if got != path {
			return errors.New("scope path is not its host launch path")
		}
		return nil
	})
}
