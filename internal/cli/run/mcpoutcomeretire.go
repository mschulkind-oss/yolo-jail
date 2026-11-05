package run

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

func mcpOutcomeGenerationDir(cname string) string {
	return filepath.Join(ownerPIDDir(), cname+".mcp-outcomes")
}

func openMCPOutcomeGenerations(cname string) (*os.Root, error) {
	root, err := paths.OpenStateDirRoot(mcpOutcomeGenerationDir(cname))
	if err != nil {
		return nil, err
	}
	info, err := root.Stat(".")
	if err != nil || info.Mode().Perm() != 0o700 {
		root.Close()
		return nil, os.ErrInvalid
	}
	return root, nil
}

const mcpGenerationMaxBytes = 16 * 1024 // bound host paths, not retention time

type mcpOutcomeGeneration struct {
	ID        string `json:"id"`
	Cname     string `json:"cname"`
	Workspace string `json:"workspace"`
	Runtime   string `json:"runtime"`
}

func readMCPOutcomeGeneration(root *os.Root, cname, id, workspace, rt string) bool {
	g, ok := loadMCPOutcomeGeneration(root, cname, id, workspace)
	return ok && g.Runtime == rt
}

func loadMCPOutcomeGeneration(root *os.Root, cname, id, workspace string) (mcpOutcomeGeneration, bool) {
	file, err := paths.OpenRegularFileBeneath(root, mcpOutcomeGenerationName(cname, id))
	if err != nil {
		return mcpOutcomeGeneration{}, false
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.Mode().Perm() != 0o600 || info.Size() > mcpGenerationMaxBytes || info.Sys().(*syscall.Stat_t).Nlink != 1 {
		return mcpOutcomeGeneration{}, false
	}
	body, err := io.ReadAll(io.LimitReader(file, mcpGenerationMaxBytes+1))
	if err != nil {
		return mcpOutcomeGeneration{}, false
	}
	var g mcpOutcomeGeneration
	if json.Unmarshal(body, &g) != nil || g.ID != id || g.Cname != cname || g.Workspace != workspace || g.Runtime == "" {
		return mcpOutcomeGeneration{}, false
	}
	canonical, _ := json.Marshal(g)
	return g, bytes.Equal(canonical, body)
}

// Fresh IDs must own their marker exclusively; an attach may only reuse the
// current generation's exact marker. The marker survives a killed reader even
// when its publisher has not made a claim yet.
func createMCPOutcomeGeneration(cname, id, workspace, rt string) error {
	if mcpTeardownOutcomePath(cname, id) == "" || workspace == "" || rt == "" {
		return os.ErrInvalid
	}
	if err := os.MkdirAll(mcpOutcomeGenerationDir(cname), 0o700); err != nil {
		return err
	}
	root, err := openMCPOutcomeGenerations(cname)
	if err != nil {
		return err
	}
	defer root.Close()
	name := mcpOutcomeGenerationName(cname, id)
	f, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	owned := true
	defer func() {
		if owned {
			_ = root.Remove(name)
		}
	}()
	body, _ := json.Marshal(mcpOutcomeGeneration{ID: id, Cname: cname, Workspace: workspace, Runtime: rt})
	if _, err := f.Write(body); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	owned = false
	return nil
}

func ensureMCPOutcomeGeneration(cname, id, workspace, rt string) error {
	root, err := openMCPOutcomeGenerations(cname)
	if err != nil {
		return err
	}
	defer root.Close()
	if readMCPOutcomeGeneration(root, cname, id, workspace, rt) {
		return nil
	}
	return os.ErrInvalid
}

// Readers register while still counted as sessions, before releasing the
// session lock that lets the keeper publish. A live reader's shared flock is
// held through its LAST result check, including a delayed second reader.
func holdMCPOutcomeReader(cname, id string) (*os.File, error) {
	if mcpTeardownOutcomePath(cname, id) == "" {
		return nil, os.ErrInvalid
	}
	if err := os.MkdirAll(ownerPIDDir(), 0o755); err != nil {
		return nil, err
	}
	root, err := paths.OpenStateDirRoot(ownerPIDDir())
	if err != nil {
		return nil, err
	}
	defer root.Close()
	name := mcpOutcomeLeaseName(cname, id)
	f, err := root.OpenFile(name, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		f, err = paths.OpenRegularFileBeneath(root, name)
	}
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil || info.Mode().Perm() != 0o600 || info.Sys().(*syscall.Stat_t).Nlink != 1 {
		f.Close()
		return nil, os.ErrInvalid
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_SH); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

func (o *Options) closeMCPOutcomeReader() {
	if o.mcpOutcomeReader != nil {
		releaseLock(o.mcpOutcomeReader)
		o.mcpOutcomeReader = nil
	}
}

func (o *Options) registerMCPOutcomeReader(cname, id, rt string) error {
	if o.mcpOutcomeReader != nil {
		return nil
	}
	if err := ensureMCPOutcomeGeneration(cname, id, o.Workspace, rt); err != nil {
		return err
	}
	reader, err := holdMCPOutcomeReader(cname, id)
	if err == nil {
		o.mcpOutcomeReader = reader
	}
	return err
}

func validMCPOutcomeBody(data []byte, id string) bool {
	var outcome mcpTeardownOutcome
	if json.Unmarshal(data, &outcome) != nil || outcome.ID != id || (outcome.Result != "ok" && outcome.Result != "failed") {
		return false
	}
	canonical, _ := json.Marshal(outcome)
	return bytes.Equal(canonical, data)
}

func validMCPOutcomeTemp(name, resultName string) bool {
	prefix := "." + resultName + "."
	if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, ".tmp") {
		return false
	}
	nonce := strings.TrimSuffix(strings.TrimPrefix(name, prefix), ".tmp")
	if len(nonce) != 32 {
		return false
	}
	for _, c := range nonce {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func mcpOutcomeInode(info os.FileInfo, claim mcpOutcomeClaim, links uint64) bool {
	st, ok := info.Sys().(*syscall.Stat_t)
	return ok && info.Mode().IsRegular() && info.Mode().Perm() == 0o600 &&
		uint64(st.Dev) == claim.Device && st.Ino == claim.Inode && uint64(st.Nlink) == links
}

// retireMCPOutcome does not infer death from a PID, elapsed time, or a failed
// runtime query. It needs BOTH kernel locks (no ending readers or sessions),
// the gone keeper, and a successful runtime answer for this workspace's name.
// Claim + inode + exact outcome identify ONLY the result this generation wrote.
func (o *Options) retireMCPOutcome(cname, rt, id string) bool {
	if mcpTeardownOutcomePath(cname, id) == "" || probeKeeper(cname) != keeperGone {
		return false
	}
	sessions, ok := tryExclusiveSessionLock(cname)
	if !ok {
		return false
	}
	defer sessions.release()
	root, err := paths.OpenStateDirRoot(ownerPIDDir())
	if err != nil {
		return false
	}
	defer root.Close()
	lease, err := paths.OpenRegularFileBeneath(root, mcpOutcomeLeaseName(cname, id))
	if errors.Is(err, os.ErrNotExist) {
		lease, err = root.OpenFile(mcpOutcomeLeaseName(cname, id), os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	}
	if err != nil {
		return false
	}
	defer lease.Close()
	info, err := lease.Stat()
	if err != nil || info.Mode().Perm() != 0o600 || info.Sys().(*syscall.Stat_t).Nlink != 1 ||
		syscall.Flock(int(lease.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
		return false
	}
	// The runtime query is AFTER both locks, so no reader can have registered
	// before it. New arrivals are excluded by the gone keeper and absent jail.
	if existing, known := o.probeExistingContainer(cname, rt, trackingProbeTimeout); !known || existing != "" || probeKeeper(cname) != keeperGone {
		return false
	}
	genRoot, err := openMCPOutcomeGenerations(cname)
	if err != nil {
		return false
	}
	defer genRoot.Close()
	if !readMCPOutcomeGeneration(genRoot, cname, id, o.Workspace, rt) {
		return false
	}
	claimFile, err := paths.OpenRegularFileBeneath(root, mcpOutcomeClaimName(cname, id))
	if errors.Is(err, os.ErrNotExist) {
		// No publisher made a claim. Only the exact owned generation and
		// absence of ANY result permit its orphaned lease to be removed.
		if _, resultErr := root.Lstat(filepath.Base(mcpTeardownOutcomePath(cname, id))); !errors.Is(resultErr, os.ErrNotExist) {
			return false
		}
		if root.Remove(mcpOutcomeLeaseName(cname, id)) != nil {
			return false
		}
		return genRoot.Remove(mcpOutcomeGenerationName(cname, id)) == nil
	}
	if err != nil {
		return false
	}
	defer claimFile.Close()
	claimInfo, err := claimFile.Stat()
	if err != nil || claimInfo.Mode().Perm() != 0o600 || claimInfo.Size() > mcpGenerationMaxBytes || claimInfo.Sys().(*syscall.Stat_t).Nlink != 1 {
		return false
	}
	claimBytes, err := io.ReadAll(io.LimitReader(claimFile, mcpGenerationMaxBytes+1))
	if err != nil {
		return false
	}
	var claim mcpOutcomeClaim
	if json.Unmarshal(claimBytes, &claim) != nil || claim.ID != id || claim.Cname != cname ||
		claim.Workspace != o.Workspace || claim.Runtime != rt || claim.Device == 0 || claim.Inode == 0 {
		return false
	}
	canonicalClaim, _ := json.Marshal(claim)
	if !bytes.Equal(canonicalClaim, claimBytes) {
		return false
	}
	if !validMCPOutcomeTemp(claim.Temp, filepath.Base(mcpTeardownOutcomePath(cname, id))) {
		return false
	}
	name := filepath.Base(mcpTeardownOutcomePath(cname, id))
	temp, tempErr := paths.OpenRegularFileBeneath(root, claim.Temp)
	if tempErr != nil && !errors.Is(tempErr, os.ErrNotExist) {
		return false
	}
	if temp != nil {
		defer temp.Close()
	}
	result, err := paths.OpenRegularFileBeneath(root, name)
	if err != nil {
		// A publisher may have failed before its exclusive link. Only its
		// complete claim is retired; unknown or unreadable results are kept.
		if !errors.Is(err, os.ErrNotExist) {
			return false
		}
		if temp != nil {
			info, statErr := temp.Stat()
			if statErr != nil || !mcpOutcomeInode(info, claim, 1) || root.Remove(claim.Temp) != nil {
				return false
			}
		}
	} else {
		resultInfo, statErr := result.Stat()
		if statErr != nil || resultInfo.Size() > 256 ||
			!mcpOutcomeInode(resultInfo, claim, 1) && !mcpOutcomeInode(resultInfo, claim, 2) {
			result.Close()
			return false
		}
		if temp != nil {
			tempInfo, tempStatErr := temp.Stat()
			if tempStatErr != nil || !mcpOutcomeInode(tempInfo, claim, 2) || !mcpOutcomeInode(resultInfo, claim, 2) {
				result.Close()
				return false
			}
		} else if !mcpOutcomeInode(resultInfo, claim, 1) {
			result.Close()
			return false
		}
		body, readErr := io.ReadAll(io.LimitReader(result, 257))
		if readErr != nil || !validMCPOutcomeBody(body, id) {
			result.Close()
			return false
		}
		result.Close()
		if err := root.Remove(name); err != nil {
			return false
		}
		if temp != nil && root.Remove(claim.Temp) != nil {
			return false
		}
	}
	// Leave the claim until last so an incomplete unlink can be retried
	if err := root.Remove(mcpOutcomeLeaseName(cname, id)); err != nil {
		return false
	}
	if err := root.Remove(mcpOutcomeClaimName(cname, id)); err != nil {
		return false
	}
	return genRoot.Remove(mcpOutcomeGenerationName(cname, id)) == nil
}

// Retry only THIS workspace's exact claimed generations when it next launches.
// No host-wide scan, no guessed age, and no legacy/unclaimed result adoption.
func (o *Options) retireWorkspaceMCPOutcomes(cname string) {
	root, err := openMCPOutcomeGenerations(cname)
	if err != nil {
		return
	}
	defer root.Close()
	dir, err := root.Open(".")
	if err != nil {
		return
	}
	entries, err := dir.ReadDir(-1)
	dir.Close()
	if err != nil {
		return
	}
	prefix := cname + ".mcp-teardown-"
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, ".generation") {
			continue
		}
		id := strings.TrimSuffix(strings.TrimPrefix(name, prefix), ".generation")
		if mcpOutcomeGenerationName(cname, id) == name && mcpTeardownOutcomePath(cname, id) != "" {
			generation, ok := loadMCPOutcomeGeneration(root, cname, id, o.Workspace)
			if ok {
				_ = o.retireMCPOutcome(cname, generation.Runtime, id)
			}
		}
	}
}
