package entrypoint

// floorlaunchers.go is docs/reference/agent-program-runtimes.md AR-L5: after the provisioning stage
// installs a node that meets a declared floor, the launchers of the programs declaring that floor
// are regenerated, so the launch that installed the interpreter already execs its program under it.
//
// # Why this is needed
//
// The launcher's interpreter is resolved once, when the launcher is generated, and generation is a
// boot step that runs BEFORE the stage. So on the launch whose stage installs the node, the floor
// check passes while the launcher was written with a plain `exec "$REAL_BIN"`, and the program runs
// under whatever node PATH gives it, the failure the floor exists to prevent, until the next boot.
//
// # How
//
// A launcher whose declared floor resolved to nothing at generation leaves a FLOOR-PENDING RECORD
// (a term this file coins: the launcher's render split at every place the interpreter goes, plus
// the floor) in Env.FloorPendingDir. The bootstrap, after a floor is met, hands the bins declaring
// it to `yolo internal node-floor-launchers`, which resolves the floor again and joins the record's
// segments with the interpreter it found. Every other byte is the boot's render, so the regenerated
// launcher is the one the next boot would write. Resolution stays at generation time and nothing
// here fetches: a launch that installs nothing never runs it.
//
// The record lives in the jail's own home, which anything in the jail can write, and the stage
// that reads it runs with the same privileges as the agent, so a forged record can do nothing the
// jail could not already do; the verb still writes only a valid bin name inside the launch dir it
// is handed.

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// floorPendingRecord is one floor-pending record (see the file comment), as JSON.
type floorPendingRecord struct {
	// Floor is the declared node_floor the launcher could not meet at generation.
	Floor string `json:"floor"`
	// Segments is the launcher split at the exec prefix: joined with a prefix they are the
	// launcher rendered with it (npmAgentLauncherSegments).
	Segments []string `json:"segments"`
}

// execPrefixToken is a fresh split token for npmAgentLauncherSegments: sixteen random bytes,
// hex-encoded, inside the sentinel's own shape. A read failure falls back to a fixed spelling,
// which is only as safe as the pack's values are free of it.
func execPrefixToken() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "__YOLO_EXEC_PREFIX_SPLIT__"
	}
	return "__YOLO_EXEC_PREFIX_" + hex.EncodeToString(b[:]) + "__"
}

// writeFloorPending records bin's launcher render for the stage to finish (the file comment).
// GenerateAgentLaunchers calls it only for an npm launcher whose declared floor resolved to
// nothing, after resetting the directory, so a record exists exactly while a launcher waits.
func writeFloorPending(e *Env, bin, floor string, segments []string) error {
	raw, err := json.Marshal(floorPendingRecord{Floor: floor, Segments: segments})
	if err != nil {
		return err
	}
	dir := e.FloorPendingDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return WriteStringInPlace(filepath.Join(dir, bin), string(raw), 0o644)
}

// RegenerateFloorLaunchers is `yolo internal node-floor-launchers`: for each bin with a
// floor-pending record in pendingDir, it resolves the record's floor again and, when a node meets
// it now, rewrites launchDir/<bin> as the record's segments joined with that interpreter and
// removes the record. A bin with no record is skipped silently (its launcher already named an
// interpreter), and one whose floor still resolves to nothing is said and left, since the
// bootstrap refuses that case on its own. Progress lines go to out.
//
// It returns an error when any record could not be read or any launcher could not be written,
// naming each; the bootstrap treats that as a warning, because the floor IS met and the one-boot
// lag is what remains.
func RegenerateFloorLaunchers(pendingDir, launchDir string, bins []string, out io.Writer) error {
	var failed []string
	for _, bin := range bins {
		if !packdecl.ValidBinName(bin) {
			// The launcher is FILED at launchDir/<bin>: a traversal name would write outside it.
			failed = append(failed, fmt.Sprintf("%q is not a program name", bin))
			continue
		}
		pending := filepath.Join(pendingDir, bin)
		raw, err := os.ReadFile(pending)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			failed = append(failed, bin+": "+err.Error())
			continue
		}
		var rec floorPendingRecord
		if err := json.Unmarshal(raw, &rec); err != nil || !packdecl.ValidNodeFloor(rec.Floor) ||
			len(rec.Segments) < 2 {
			failed = append(failed, bin+": its floor-pending record "+pending+" is not one this build wrote")
			continue
		}
		node := ResolveNodeForFloor(rec.Floor)
		if node == "" {
			fmt.Fprintf(out, "  ⚠ %s: no Node >=%s is available, so its launcher still runs it "+
				"under the node PATH finds\n", bin, rec.Floor)
			continue
		}
		launcher := filepath.Join(launchDir, bin)
		if err := writeExecutable(launcher, strings.Join(rec.Segments, execPrefixFor(node))); err != nil {
			failed = append(failed, bin+": "+err.Error())
			continue
		}
		if err := os.Remove(pending); err != nil && !errors.Is(err, fs.ErrNotExist) {
			failed = append(failed, bin+": "+err.Error())
			continue
		}
		fmt.Fprintf(out, "  ↳ %s: launcher regenerated to run under %s (Node >=%s)\n", bin, node, rec.Floor)
	}
	if len(failed) > 0 {
		return errors.New(strings.Join(failed, "; "))
	}
	return nil
}
