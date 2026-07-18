package config

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// SnapshotJSON returns the config-snapshot bytes: json.dumps(config, indent=2,
// sort_keys=True) with ensure_ascii (Python's default). This is the highest-
// priority byte-parity gate — a single byte of drift fires a spurious config-
// approval prompt on every Python<->Go switch.
//
// Note config.py uses json.dumps(config, indent=2, sort_keys=True) WITHOUT an
// explicit ensure_ascii, so it defaults to True — exactly jsonx.DumpsSnapshot.
func SnapshotJSON(config *jsonx.OrderedMap) (string, error) {
	return jsonx.DumpsSnapshot(config)
}

// ConfigSnapshotPath ports _config_snapshot_path: <workspace>/.yolo/config-snapshot.json.
func ConfigSnapshotPath(workspace string) string {
	return filepath.Join(workspace, ".yolo", "config-snapshot.json")
}

// ChangePrompter decides interactive config-change acceptance. It receives the
// rendered unified diff lines (fromfile "previous config", tofile "current
// config", lineterm "") and returns true to accept. It is only invoked on a
// TTY; the non-tty auto-accept path never calls it.
type ChangePrompter interface {
	// Prompt renders the diff and asks "Accept these config changes? [y/N]".
	// Returns accept=true iff the user answered y/yes.
	Prompt(diffLines []string) bool
}

// CheckConfigChanges ports _check_config_changes. Compares config against the
// last-seen snapshot; returns true to proceed, false to abort.
//
//   - First run / no snapshot: write current + "\n", return true.
//   - Unchanged (old.rstrip() == current, no trailing "\n" on the compare):
//     return true.
//   - Changed + non-tty (isTTY false): auto-accept, rewrite snapshot, true.
//   - Changed + tty: delegate to prompter; on accept rewrite snapshot + return
//     true, else return false (snapshot NOT rewritten).
//
// The rstrip-compare asymmetry is preserved: the stored file has a trailing
// "\n" (written as current+"\n"), but the comparison rstrips the OLD text and
// compares to current (which has NO trailing "\n"). isTTY and prompter are
// injected so this is testable without a real terminal.
func CheckConfigChanges(workspace string, config *jsonx.OrderedMap, isTTY bool, prompter ChangePrompter) (bool, error) {
	snapshotPath := ConfigSnapshotPath(workspace)
	currentJSON, err := SnapshotJSON(config)
	if err != nil {
		return false, err
	}

	oldBytes, readErr := os.ReadFile(snapshotPath)
	if readErr != nil {
		if os.IsNotExist(readErr) {
			// First run or no snapshot — accept and save.
			if err := writeSnapshot(snapshotPath, currentJSON); err != nil {
				return false, err
			}
			return true, nil
		}
		return false, readErr
	}

	// Python: old_json = snapshot_path.read_text().rstrip()
	oldJSON := pyRstrip(string(oldBytes))
	if oldJSON == currentJSON {
		return true, nil
	}

	diffLines := unifiedDiff(
		splitLines(oldJSON), splitLines(currentJSON),
		"previous config", "current config")

	if !isTTY {
		// Non-interactive: accept automatically, rewrite snapshot.
		if err := writeSnapshot(snapshotPath, currentJSON); err != nil {
			return false, err
		}
		return true, nil
	}

	accept := false
	if prompter != nil {
		accept = prompter.Prompt(diffLines)
	}
	if accept {
		if err := writeSnapshot(snapshotPath, currentJSON); err != nil {
			return false, err
		}
		return true, nil
	}
	return false, nil
}

// writeSnapshot writes currentJSON + "\n", creating .yolo/ as needed
// (parents=True, exist_ok=True).
func writeSnapshot(path, currentJSON string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(currentJSON+"\n"), 0o644)
}

// pyRstrip mirrors str.rstrip() with no args: strip trailing whitespace
// (Python's str.isspace set — the ASCII set plus a few unicode spaces). For the
// snapshot file the only trailing whitespace is the "\n" we wrote, but match
// Python's full set to be faithful.
func pyRstrip(s string) string {
	return strings.TrimRightFunc(s, isPySpace)
}

func isPySpace(r rune) bool {
	switch r {
	case ' ', '\t', '\n', '\r', '\v', '\f',
		0x1c, 0x1d, 0x1e, 0x1f, 0x85, 0xa0,
		0x2028, 0x2029:
		return true
	}
	// Broader unicode whitespace Python's str.strip() also removes.
	switch {
	case r >= 0x2000 && r <= 0x200a:
		return true
	case r == 0x1680 || r == 0x202f || r == 0x205f || r == 0x3000:
		return true
	}
	return false
}

// splitLines mirrors str.splitlines() for the JSON snapshot text (only "\n"
// occurs, but be faithful to Python's line boundaries used by difflib).
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	var out []string
	var cur strings.Builder
	rs := []rune(s)
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		if isLineBoundary(r) {
			if r == '\r' && i+1 < len(rs) && rs[i+1] == '\n' {
				i++
			}
			out = append(out, cur.String())
			cur.Reset()
			continue
		}
		cur.WriteRune(r)
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

func isLineBoundary(r rune) bool {
	switch r {
	case '\n', '\r', '\v', '\f', 0x1c, 0x1d, 0x1e, 0x85, 0x2028, 0x2029:
		return true
	}
	return false
}
