package runtime

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/setup"
)

// LaunchRecord is the host-side record a container launch leaves under CONTAINER_DIR, named for
// its jail (named-setups.md SU-D11, the container half; named-setups-plan.md slice 11b). It is
// what `yolo ps` reads to map a container to its workspace, and it names what the runtime cannot
// tell: the setup, the selected packs and the yolo that launched it.
//
// Every field but Name and Workspace may be UNKNOWN: a record an older yolo wrote held the
// workspace alone. An unknown string is "", an unknown Started is the zero time, and unknown Packs
// is nil; a launch that selected no pack records an empty, non-nil list. On disk an unknown is
// JSON null (named-setups.md §12.4).
type LaunchRecord struct {
	Name        string
	Setup       string
	Confinement setup.Confinement
	RunAs       setup.RunAs
	Workspace   string
	Started     time.Time
	Packs       []string
	YoloVersion string
}

// launchRecordJSON is the record's file shape: the keys of named-setups.md §12.4, null for unknown.
type launchRecordJSON struct {
	Name        string   `json:"name"`
	Setup       *string  `json:"setup"`
	Confinement *string  `json:"confinement"`
	RunAs       *string  `json:"run_as"`
	Workspace   string   `json:"workspace"`
	Started     *string  `json:"started"`
	Packs       []string `json:"packs"`
	YoloVersion *string  `json:"yolo_version"`
}

func nullable(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// WriteLaunchRecord writes rec to CONTAINER_DIR/<rec.Name>, replacing any record of that name.
// rec.Workspace must already be the resolved (absolute, symlinks-followed) path. The write goes
// through a temporary file and a rename, so a reader never sees half a record.
func WriteLaunchRecord(rec LaunchRecord) error {
	dir := paths.ContainerDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	wire := launchRecordJSON{
		Name:        rec.Name,
		Setup:       nullable(rec.Setup),
		Confinement: nullable(string(rec.Confinement)),
		RunAs:       nullable(string(rec.RunAs)),
		Workspace:   rec.Workspace,
		Packs:       rec.Packs,
		YoloVersion: nullable(rec.YoloVersion),
	}
	if !rec.Started.IsZero() {
		wire.Started = nullable(rec.Started.UTC().Format(time.RFC3339))
	}
	data, err := json.MarshalIndent(wire, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+rec.Name+".tmp-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Chmod(0o644); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), filepath.Join(dir, rec.Name)); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return nil
}

// ReadLaunchRecord reads the record of the jail name, or false when there is none, it is empty,
// or it is a JSON document that does not parse. A file an older yolo wrote, the workspace path on
// one line, reads as a record of that workspace with every other field unknown.
func ReadLaunchRecord(name string) (LaunchRecord, bool) {
	data, err := os.ReadFile(filepath.Join(paths.ContainerDir(), name))
	if err != nil {
		return LaunchRecord{}, false
	}
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return LaunchRecord{}, false
	}
	if trimmed[0] != '{' {
		return LaunchRecord{Name: name, Workspace: trimTrailingSpace(string(data))}, true
	}
	var wire launchRecordJSON
	if err := json.Unmarshal(trimmed, &wire); err != nil || wire.Workspace == "" {
		return LaunchRecord{}, false
	}
	rec := LaunchRecord{
		Name:        name,
		Setup:       deref(wire.Setup),
		Confinement: setup.Confinement(deref(wire.Confinement)),
		RunAs:       setup.RunAs(deref(wire.RunAs)),
		Workspace:   wire.Workspace,
		Packs:       wire.Packs,
		YoloVersion: deref(wire.YoloVersion),
	}
	if s := deref(wire.Started); s != "" {
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			rec.Started = t.UTC()
		}
	}
	return rec, true
}

// CleanupContainerTracking removes a container's launch record (missing_ok).
func CleanupContainerTracking(name string) {
	_ = os.Remove(filepath.Join(paths.ContainerDir(), name))
}

// ReadContainerWorkspace returns the workspace a container's launch record names, or ("", false)
// when it has none. The inspect fallback stays in the caller since it execs the runtime.
func ReadContainerWorkspace(name string) (string, bool) {
	rec, ok := ReadLaunchRecord(name)
	if !ok {
		return "", false
	}
	return rec.Workspace, true
}

// SourceState is how one source of running jails answered "what is running"
// (named-setups.md §12.1): a technology's listing, kept per source so one that did not
// answer is never read as "no jails" (D11).
type SourceState string

const (
	// SourceListed: asked, and it answered; its Names are every yolo jail it runs.
	SourceListed SourceState = "listed"
	// SourceNotInstalled: its binary is not on this machine, so it runs nothing.
	SourceNotInstalled SourceState = "not-installed"
	// SourceUnreachable: installed, and the ask failed or was never made. Its jails are unknown.
	SourceUnreachable SourceState = "unreachable"
	// SourceTimedOut: installed, asked, and no answer came within its budget. Unknown too.
	SourceTimedOut SourceState = "timed-out"
)

// SourceAnswer is one source's answer. Confinement is the setup vocabulary
// ("podman", "apple-container"); Names holds the jails it listed, meaningful only when
// State is SourceListed.
type SourceAnswer struct {
	Confinement string
	State       SourceState
	Names       map[string]struct{}
}

// PruneStaleTrackingFiles removes the launch records under CONTAINER_DIR that the answers prove
// belong to no running jail, and returns the names removed (in directory order). A missing
// CONTAINER_DIR is a no-op. answers must hold one entry per container source of this OS
// (named-setups-plan.md SUP-D22). The rule has two halves (SU-D10):
//
//   - A record that names its technology (Confinement, written since slice 11b) is judged by that
//     technology alone: it goes when that source is `listed` and did not list it. Any other answer
//     keeps it, `not-installed` included: that is a PATH miss, and a runtime installed off this
//     PATH still runs its jails, so a miss alone never authorizes deleting a record that names it.
//   - A record that names none (an older yolo's, or one that does not parse) goes only when EVERY
//     container source LISTED and none listed it. A source that did not answer keeps every such
//     record, and so does one that is not installed (named-setups-plan.md slice 11d, SUP-D52):
//     that too is a PATH miss, and the record cannot say it was not on that runtime. Its jails are unknown, and a record
//     pruned here is a live jail's record lost — the Mac defect where a Podman-only `yolo ps`
//     deleted every running Apple Container jail's file.
//
// A name any listed source runs is never removed, whatever its record says.
func PruneStaleTrackingFiles(answers []SourceAnswer) []string {
	running := map[string]struct{}{}
	byTech := map[string]SourceAnswer{}
	everyAnswered := true
	for _, a := range answers {
		byTech[a.Confinement] = a
		switch a.State {
		case SourceListed:
			for n := range a.Names {
				running[n] = struct{}{}
			}
		default:
			everyAnswered = false
		}
	}
	dir := paths.ContainerDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var removed []string
	for _, e := range entries {
		name := e.Name()
		if _, live := running[name]; live || strings.HasPrefix(name, ".") {
			continue // running, or a record WriteLaunchRecord is still writing
		}
		rec, _ := ReadLaunchRecord(name)
		if rec.Confinement != "" {
			a, asked := byTech[string(rec.Confinement)]
			if !asked || a.State != SourceListed {
				continue
			}
		} else if !everyAnswered {
			continue
		}
		if err := os.Remove(filepath.Join(dir, name)); err == nil {
			removed = append(removed, name)
		}
	}
	return removed
}

// trimTrailingSpace strips surrounding ASCII whitespace from the tracking-file
// contents; only trailing matters in practice since the file is "<path>\n".
func trimTrailingSpace(s string) string {
	end := len(s)
	for end > 0 {
		c := s[end-1]
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' || c == '\v' {
			end--
			continue
		}
		break
	}
	// Also strip leading whitespace for a full strip.
	start := 0
	for start < end {
		c := s[start]
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' || c == '\v' {
			start++
			continue
		}
		break
	}
	return s[start:end]
}
