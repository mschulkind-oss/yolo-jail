package capture

// autofailure.go is the store's memo of a FAILED AUTO-CAPTURE: one small record per
// (bin, platform), beside the entries the capture failed to add to.
//
// # Why the store keeps it
//
// A launch auto-captures a program on a MISS (docs/design/program-delivery.md OQ-PD18), and a
// capture that fails stores nothing, so the next launch misses again. With nothing remembered,
// a program whose capture fails every time — the measured case was every Apple Container launch
// (docs/research/macos-backend-performance.md §8) — re-runs its installer on every launch. The
// trigger reads this memo to back off (OQ-PD26); the policy, how long and what resets it, is the
// trigger's (internal/cli/autocapture.go), and this file only keeps the record.
//
// It lives in the store because it is a fact about the store's population on this machine, and
// beside entries/ rather than in it, because the resolver scans entries/ and would read a stray
// file there as a torn entry.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// autoFailuresLeaf is the memo directory under the store root.
const autoFailuresLeaf = "auto-capture-failures"

// AutoFailure records that a launch's auto-capture of Bin for Platform failed.
type AutoFailure struct {
	Bin      string `json:"bin"`
	Platform string `json:"platform"`
	// Version is the yolo that failed (version.Baked), so a different yolo is free to try again:
	// it may be the fix.
	Version string `json:"yolo_version"`
	// Failures counts the consecutive failures under Version; the back-off grows with it.
	Failures int `json:"failures"`
	// Last is when the latest failure happened.
	Last time.Time `json:"last"`
}

// autoFailurePath is the memo file for (bin, platform). The platform's slash becomes a dash so
// the record is one file, and a bin that is not a single path segment is refused, as Stage
// refuses it.
func (s *Store) autoFailurePath(bin, platform string) (string, error) {
	if err := validSegment(bin); err != nil {
		return "", fmt.Errorf("auto-capture memo: %w", err)
	}
	plat := strings.ReplaceAll(platform, "/", "-")
	if err := validSegment(plat); err != nil {
		return "", fmt.Errorf("auto-capture memo: %w", err)
	}
	return filepath.Join(s.Dir, autoFailuresLeaf, bin+"@"+plat+".json"), nil
}

// AutoFailure reads the memo for (bin, platform). A missing or unreadable memo reads as none:
// the trigger then captures, which is what it did before memos existed, rather than suppressing
// a capture on a record it cannot read.
func (s *Store) AutoFailure(bin, platform string) (AutoFailure, bool) {
	p, err := s.autoFailurePath(bin, platform)
	if err != nil {
		return AutoFailure{}, false
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return AutoFailure{}, false
	}
	var f AutoFailure
	if err := json.Unmarshal(data, &f); err != nil || f.Bin != bin || f.Platform != platform {
		return AutoFailure{}, false
	}
	return f, true
}

// RecordAutoFailure writes f as the memo for (f.Bin, f.Platform), replacing any earlier one.
// Written to a temporary file and renamed into place, so a reader never sees half a record.
func (s *Store) RecordAutoFailure(f AutoFailure) error {
	p, err := s.autoFailurePath(f.Bin, f.Platform)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".memo-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), p); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return nil
}

// ClearAutoFailure removes the memo for (bin, platform). Removing one that is not there is not
// an error.
func (s *Store) ClearAutoFailure(bin, platform string) error {
	p, err := s.autoFailurePath(bin, platform)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
