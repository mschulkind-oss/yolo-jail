package selfupdate

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

const (
	// Interval is how long a successful check is believed.
	Interval = 24 * time.Hour
	// lockTTL bounds how long a background check's lock keeps others from
	// starting: longer than any check takes, short enough that a check killed
	// mid-run does not silence updates for long.
	lockTTL = 10 * time.Minute
)

// State is one check's answer, cached at StatePath.
type State struct {
	CheckedAt time.Time `json:"checked_at"`
	// Identity is the Channel.Identity the check was made for.
	Identity string `json:"identity"`
	Kind     Kind   `json:"kind"`
	Current  string `json:"current"`
	// Latest is the newest release version, or for KindSource the upstream's
	// short commit.
	Latest string `json:"latest,omitempty"`
	// Upstream and Behind are KindSource only: the tracked branch, and how many
	// of its commits the binary was not built from.
	Upstream  string `json:"upstream,omitempty"`
	Behind    int    `json:"behind,omitempty"`
	Available bool   `json:"available"`
	Error     string `json:"error,omitempty"`
	// Disclosed records that this machine has been told, once, that yolo checks
	// for updates and how to turn it off.
	Disclosed bool `json:"disclosed,omitempty"`
	// DeclinedFor is the Latest the user said "no" to at the launch prompt. The
	// notice still shows; the prompt waits for something newer than that.
	DeclinedFor string `json:"declined_for,omitempty"`
}

// Fresh reports whether s still answers for ch at now: made for this same
// binary, and younger than Interval.
func (s State) Fresh(ch Channel, now time.Time) bool {
	if s.Identity != ch.Identity() || s.CheckedAt.IsZero() {
		return false
	}
	return now.Sub(s.CheckedAt) < Interval
}

// UpdateFor reports whether s says an update exists for ch. A cached answer for
// a different binary says nothing, however recent.
func (s State) UpdateFor(ch Channel) bool {
	return s.Identity == ch.Identity() && s.Available
}

// ShouldPrompt reports whether the launch prompt should offer this update: the
// user has not already declined this exact Latest.
func (s State) ShouldPrompt() bool { return s.Available && s.DeclinedFor != s.Latest }

// StatePath is where the cached State lives (see paths.UpdateCheckDir for why
// that directory).
func StatePath() string {
	return filepath.Join(paths.UpdateCheckDir(), "state.json")
}

// LoadState reads the cached State. A missing or unreadable file is the zero
// State, which is never Fresh.
func LoadState(path string) State {
	var s State
	data, err := os.ReadFile(path)
	if err != nil {
		return State{}
	}
	if json.Unmarshal(data, &s) != nil {
		return State{}
	}
	return s
}

// SaveState writes s atomically, so a reader never sees half a file.
func SaveState(path string, s State) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".state-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// InvalidateState drops a cached answer after an update while preserving the
// machine-wide disclosure bit. The next eligible command checks the new binary
// without repeating a disclosure the user has already seen.
func InvalidateState(path string) error {
	return SaveState(path, State{Disclosed: LoadState(path).Disclosed})
}

// LockPath is the lock a background check holds while it runs.
func LockPath(statePath string) string { return statePath + ".lock" }

// AcquireLock takes the background-check lock, reporting false when another
// check holds it. A lock older than lockTTL belongs to a check that died and is
// taken over.
func AcquireLock(path string, now time.Time) bool {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false
	}
	for attempt := 0; attempt < 2; attempt++ {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			fmt.Fprintf(f, "%d\n", os.Getpid())
			f.Close()
			return true
		}
		if !errors.Is(err, os.ErrExist) {
			return false
		}
		fi, statErr := os.Stat(path)
		if statErr != nil || now.Sub(fi.ModTime()) < lockTTL {
			return false
		}
		_ = os.Remove(path)
	}
	return false
}

// InternalCheckVerb is the `yolo internal` verb the background check runs as.
// SpawnBackgroundCheck's argv and runInternal's dispatch (internal/cli) both
// spell it through this constant, so renaming one side cannot silently turn
// every background check into a usage error nobody sees.
const InternalCheckVerb = "update-check"

// BackgroundCheckArgs is the argv, after the executable, that
// SpawnBackgroundCheck runs.
func BackgroundCheckArgs(statePath string) []string {
	return []string{"internal", InternalCheckVerb, "--state", statePath}
}

// SpawnBackgroundCheck starts `<exe> internal update-check` detached — its own
// session, no inherited stdio — so the command that noticed a stale release
// cache finishes at full speed and the next one reads the answer. Source
// channels never call it. The lock is taken HERE, before the spawn, so a burst
// of commands starts one check rather than one each; the child releases it.
func SpawnBackgroundCheck(exe, statePath string, now time.Time) error {
	lock := LockPath(statePath)
	if !AcquireLock(lock, now) {
		return nil
	}
	cmd := exec.Command(exe, BackgroundCheckArgs(statePath)...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		_ = os.Remove(lock)
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
