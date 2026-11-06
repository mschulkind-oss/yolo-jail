package run

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// The selected keeper publishes a token-free outcome for its exact launch.
// The log is best-effort presentation, never confirmation of final harvest.
type mcpTeardownOutcome struct {
	ID     string `json:"id"`
	Result string `json:"result"`
}

// A claim binds the exclusively published result's inode to this launch's
// random ID. Neither this bookkeeping nor the outcome conveys an auth grant.
//
// Device and inode NUMBERS do not name one file over time: ext4 and XFS hand a
// freed inode number straight to the next file created, so a file put at the
// result path after ours was removed can carry our numbers. Generation is the
// inode's generation number where the filesystem reports one (absent where it
// does not), and it differs between two files that shared an inode number.
type mcpOutcomeClaim struct {
	ID         string  `json:"id"`
	Cname      string  `json:"cname"`
	Workspace  string  `json:"workspace"`
	Runtime    string  `json:"runtime"`
	Device     uint64  `json:"device"`
	Inode      uint64  `json:"inode"`
	Generation *uint64 `json:"generation,omitempty"`
	Temp       string  `json:"temp"`
}

func mcpOutcomeClaimName(cname, id string) string {
	if mcpTeardownOutcomePath(cname, id) == "" {
		return ""
	}
	return cname + ".mcp-teardown-" + id + ".claim"
}

func mcpOutcomeLeaseName(cname, id string) string {
	return cname + ".mcp-teardown-" + id + ".lease"
}

func mcpOutcomeGenerationName(cname, id string) string {
	return cname + ".mcp-teardown-" + id + ".generation"
}

func mcpTeardownOutcomePath(cname, id string) string {
	if cname == "" || filepath.Base(cname) != cname || cname == "." || cname == ".." || len(id) != 32 {
		return ""
	}
	for _, c := range id {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return ""
		}
	}
	return filepath.Join(ownerPIDDir(), cname+".mcp-teardown-"+id+".json")
}

var (
	closeMCPTeardownTemp   = (*os.File).Close
	linkMCPTeardownFinal   = (*os.Root).Link
	removeMCPTeardownTemp  = (*os.Root).Remove
	removeMCPTeardownFinal = (*os.Root).Remove
)

func writeMCPTeardownOutcome(cname, id, result string) error {
	return writeMCPTeardownOutcomeFor(cname, id, result, "", "")
}

func writeMCPTeardownOutcomeFor(cname, id, result, workspace, runtime string) error {
	path := mcpTeardownOutcomePath(cname, id)
	if path == "" || (result != "ok" && result != "failed") {
		return os.ErrInvalid
	}
	data, err := json.Marshal(mcpTeardownOutcome{ID: id, Result: result})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(ownerPIDDir(), 0o755); err != nil {
		return err
	}
	if workspace != "" {
		if err := ensureMCPOutcomeGeneration(cname, id, workspace, runtime); err != nil {
			return err
		}
	}
	root, err := paths.OpenStateDirRoot(ownerPIDDir())
	if err != nil {
		return err
	}
	defer root.Close()
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return err
	}
	tmp := "." + filepath.Base(path) + "." + hex.EncodeToString(nonce[:]) + ".tmp"
	file, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = root.Remove(tmp) // only our exclusively created name
		}
	}()
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return err
	}
	gen, genOK := mcpOutcomeInodeGeneration(file)
	if err := closeMCPTeardownTemp(file); err != nil {
		return err
	}
	stat := info.Sys().(*syscall.Stat_t)
	claim := mcpOutcomeClaim{ID: id, Cname: cname, Workspace: workspace, Runtime: runtime, Device: uint64(stat.Dev), Inode: stat.Ino, Temp: tmp}
	if genOK {
		claim.Generation = &gen
	}
	claimName := mcpOutcomeClaimName(cname, id)
	claimFile, err := root.OpenFile(claimName, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	claimOwned := true
	defer func() {
		if claimOwned {
			_ = root.Remove(claimName)
		}
	}()
	claimBytes, _ := json.Marshal(claim)
	if _, err := claimFile.Write(claimBytes); err != nil {
		_ = claimFile.Close()
		return err
	}
	if err := claimFile.Sync(); err != nil {
		_ = claimFile.Close()
		return err
	}
	if err := claimFile.Close(); err != nil {
		return err
	}
	// A link publishes only the fully closed file, and never overwrites a
	// prior result. Until the temp link is removed nlink=2 rejects readers.
	if err := linkMCPTeardownFinal(root, tmp, filepath.Base(path)); err != nil {
		return err
	}
	if err := removeMCPTeardownTemp(root, tmp); err != nil {
		// If retraction also fails, NEVER remove the temporary link in
		// the deferred cleanup: that would turn rejected nlink=2 into
		// readable nlink=1 success. Keep both links and their claim for
		// later retirement when the keeper and readers are gone.
		if removeMCPTeardownFinal(root, filepath.Base(path)) != nil {
			cleanup = false
			claimOwned = false
		}
		return err
	}
	cleanup = false
	claimOwned = false
	return nil
}

func readMCPTeardownOutcome(cname, id string) string {
	path := mcpTeardownOutcomePath(cname, id)
	if path == "" {
		return ""
	}
	root, err := paths.OpenStateDirRoot(ownerPIDDir())
	if err != nil {
		return ""
	}
	defer root.Close()
	file, err := paths.OpenRegularFileBeneath(root, filepath.Base(path))
	if err != nil {
		return ""
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.Mode().Perm() != 0o600 || info.Size() > 256 || info.Sys().(*syscall.Stat_t).Nlink != 1 {
		return ""
	}
	data, err := io.ReadAll(io.LimitReader(file, 256))
	if err != nil {
		return ""
	}
	var outcome mcpTeardownOutcome
	if json.Unmarshal(data, &outcome) != nil || outcome.ID != id || (outcome.Result != "ok" && outcome.Result != "failed") {
		return ""
	}
	canonical, _ := json.Marshal(outcome)
	if !bytes.Equal(canonical, data) {
		return ""
	}
	return outcome.Result
}
