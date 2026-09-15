package run

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/mschulkind-oss/yolo-jail/internal/broker"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/openaiauth"
)

// prepareLegacyOpenAIAuthState recovers the one released build that passed the literal
// relative path "{state}/credentials.json" to the OpenAI singleton. Candidates are bounded
// to the current workspace and, on Linux, the recorded singleton process's cwd. We never
// search arbitrary workspaces for credential-looking files.
func prepareLegacyOpenAIAuthState(workspace string, deps broker.Deps) func() (func() error, error) {
	return func() (func() error, error) {
		canonical := filepath.Join(loopholes.StateDirFor(openaiauth.LoopholeName), openaiauth.StateFileName)
		candidates := legacyOpenAIStateCandidates(workspace, deps)
		legacyDaemon := singletonUsesLegacyOpenAIStatePath(deps)
		var found []string
		for _, candidate := range candidates {
			if _, err := os.Lstat(candidate); err == nil {
				found = append(found, candidate)
			} else if !os.IsNotExist(err) {
				return nil, fmt.Errorf("inspect legacy OpenAI credential %s: %w", candidate, err)
			}
		}
		if len(found) == 0 {
			if legacyDaemon {
				// No login completed through the bad path, but the living old daemon
				// would still create it on the next login. A no-op action requests the
				// same stop-and-respawn transition without manufacturing state.
				return func() error { return nil }, nil
			}
			return nil, nil
		}
		if len(found) > 1 {
			return func() error {
				return fmt.Errorf("multiple legacy OpenAI credential files found; refusing to guess: %v", found)
			}, nil
		}
		if _, err := os.Lstat(canonical); err == nil {
			return func() error {
				return fmt.Errorf("both canonical and legacy OpenAI credentials exist; legacy file left at %s", found[0])
			}, nil
		} else if !os.IsNotExist(err) {
			return nil, fmt.Errorf("inspect canonical OpenAI credential %s: %w", canonical, err)
		}
		return func() error { return relocateLegacyOpenAIState(found[0], canonical) }, nil
	}
}

func singletonUsesLegacyOpenAIStatePath(deps broker.Deps) bool {
	pid, ok := broker.BrokerReadPID(deps)
	if !ok {
		return false
	}
	if raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid)); err == nil {
		return strings.Contains(string(raw), "{state}")
	}
	raw, err := exec.Command("ps", "-p", fmt.Sprint(pid), "-o", "command=").Output()
	return err == nil && strings.Contains(string(raw), "{state}")
}

func legacyOpenAIStateCandidates(workspace string, deps broker.Deps) []string {
	dirs := []string{workspace}
	if pid, ok := broker.BrokerReadPID(deps); ok {
		if cwd, err := os.Readlink(fmt.Sprintf("/proc/%d/cwd", pid)); err == nil {
			dirs = append(dirs, cwd)
		}
	}
	seen := map[string]bool{}
	var out []string
	for _, dir := range dirs {
		abs, err := filepath.Abs(dir)
		if err != nil {
			continue
		}
		candidate := filepath.Join(abs, "{state}", openaiauth.StateFileName)
		if !seen[candidate] {
			seen[candidate] = true
			out = append(out, candidate)
		}
	}
	return out
}

func relocateLegacyOpenAIState(from, to string) error {
	dirInfo, err := os.Lstat(filepath.Dir(from))
	if err != nil || !dirInfo.IsDir() {
		return fmt.Errorf("legacy OpenAI credential directory is not a real directory: %s", filepath.Dir(from))
	}
	info, err := os.Lstat(from)
	if err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("legacy OpenAI credential is not a regular file: %s", from)
	}
	if _, err := openaiauth.ReadState(from); err != nil {
		return fmt.Errorf("validate legacy OpenAI credential: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o700); err != nil {
		return fmt.Errorf("create canonical OpenAI credential directory: %w", err)
	}
	if err := os.Chmod(filepath.Dir(to), 0o700); err != nil {
		return fmt.Errorf("secure canonical OpenAI credential directory: %w", err)
	}
	if err := os.Rename(from, to); err == nil {
		if err := os.Chmod(to, 0o600); err != nil {
			return fmt.Errorf("secure relocated OpenAI credential: %w", err)
		}
		removeLegacyOpenAIStateDir(filepath.Dir(from))
		return nil
	} else if !errors.Is(err, syscall.EXDEV) {
		return fmt.Errorf("relocate legacy OpenAI credential: %w", err)
	}
	return copyThenRemoveLegacyOpenAIState(from, to)
}

func copyThenRemoveLegacyOpenAIState(from, to string) error {
	src, err := os.Open(from)
	if err != nil {
		return err
	}
	defer src.Close()
	tmp, err := os.CreateTemp(filepath.Dir(to), ".openai-auth-recovery.*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := io.Copy(tmp, src); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, to); err != nil {
		return err
	}
	if err := os.Remove(from); err != nil {
		return fmt.Errorf("remove legacy OpenAI credential after relocation: %w", err)
	}
	removeLegacyOpenAIStateDir(filepath.Dir(from))
	return nil
}

func removeLegacyOpenAIStateDir(dir string) {
	// The buggy daemon placed its per-refresh flock beside the credential. The
	// lifecycle has stopped that daemon before this migration action runs, so no
	// process can still rely on this inode for serialization.
	_ = os.Remove(filepath.Join(dir, "refresh.lock"))
	_ = os.Remove(dir)
}
