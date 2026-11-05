// Package callbrokeridentity resolves host-side classifier and executor identities from one
// selected staged pack tree. It reads declarations and hashes executable bytes; it never runs them.
package callbrokeridentity

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

type stagedPackRootUnavailable struct{ source string }

func (e *stagedPackRootUnavailable) Error() string {
	return fmt.Sprintf("call broker %s names an external classifier or executor, but its selected staged pack root is unavailable; select and stage the owning pack", e.source)
}

// IsStagedPackRootUnavailable reports the one identity failure that a pre-stage config pass
// may defer. The launch-input publisher still refuses the unresolved declaration, and the
// staged discovery pass must resolve it from the actual selected tree before publishing.
func IsStagedPackRootUnavailable(err error) bool {
	var unavailable *stagedPackRootUnavailable
	return errors.As(err, &unavailable)
}

// Resolve validates every external classifier/executor program token against the selected staged
// pack's own program contributions and hashes the host-executable file beneath that tree.
func Resolve(packRoot, pack, moduleDir string, broker *loopholedecl.CallBroker) (map[string]loopholedecl.CallBrokerProgramIdentity, error) {
	if broker == nil {
		return nil, nil
	}
	wanted := map[string]bool{}
	if len(broker.Classifier) > 0 {
		if name := programToken(broker.Classifier[0]); name != "" {
			wanted[name] = true
		}
	}
	if name := programToken(broker.Executor); name != "" {
		wanted[name] = true
	}
	if len(wanted) == 0 {
		return nil, nil
	}
	if packRoot == "" || pack == "" {
		return nil, &stagedPackRootUnavailable{source: broker.Source}
	}
	root, err := filepath.EvalSymlinks(packRoot)
	if err != nil {
		return nil, fmt.Errorf("call broker %s selected pack %s staged root: %w", broker.Source, pack, err)
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("call broker %s selected pack %s staged root: %w", broker.Source, pack, err)
	}
	manifestPath, raw, err := readPackManifest(root)
	if err != nil {
		return nil, fmt.Errorf("call broker %s selected pack %s manifest: %w", broker.Source, pack, err)
	}
	var manifest *packdecl.Manifest
	var problems []string
	if strings.HasSuffix(manifestPath, ".toml") {
		manifest, problems = packdecl.DecodeTOML(raw)
	} else {
		manifest, problems = packdecl.Decode(raw)
	}
	if len(problems) != 0 {
		return nil, fmt.Errorf("call broker %s selected pack %s manifest is invalid: %s", broker.Source, pack, strings.Join(problems, "; "))
	}
	moduleRoot, err := filepath.EvalSymlinks(moduleDir)
	if err != nil {
		return nil, fmt.Errorf("call broker %s selected pack %s module: %w", broker.Source, pack, err)
	}
	moduleRoot, err = filepath.Abs(moduleRoot)
	if err != nil || !pathWithin(root, moduleRoot) {
		return nil, fmt.Errorf("call broker %s module is outside selected pack %s staged tree", broker.Source, pack)
	}
	moduleManifestPath := loopholedecl.ManifestPath(moduleRoot)
	moduleManifest, err := filepath.EvalSymlinks(moduleManifestPath)
	if err != nil {
		return nil, fmt.Errorf("call broker %s selected pack %s module manifest: %w", broker.Source, pack, err)
	}
	moduleManifest, err = filepath.Abs(moduleManifest)
	if err != nil || !pathWithin(root, moduleManifest) {
		return nil, fmt.Errorf("call broker %s module manifest resolves outside selected pack %s staged tree", broker.Source, pack)
	}
	if _, _, err := loopholedecl.LoadDirTolerant(moduleRoot); err != nil {
		return nil, fmt.Errorf("call broker %s selected pack %s module manifest: %w", broker.Source, pack, err)
	}
	moduleRel, err := filepath.Rel(root, moduleRoot)
	if err != nil {
		return nil, fmt.Errorf("call broker %s selected pack %s module: %w", broker.Source, pack, err)
	}
	declaredModule := false
	for _, contribution := range manifest.Contributions() {
		if contribution.Kind == packdecl.KindLoophole && filepath.ToSlash(moduleRel) == contribution.From {
			declaredModule = true
			break
		}
	}
	if !declaredModule {
		return nil, fmt.Errorf("call broker %s module is not declared by selected pack %s", broker.Source, pack)
	}
	programs := map[string]packdecl.Install{}
	for _, install := range manifest.InstallContributions() {
		if install.Bin == "" {
			continue
		}
		if _, duplicate := programs[install.Bin]; duplicate {
			return nil, fmt.Errorf("call broker %s selected pack %s declares program %q more than once", broker.Source, pack, install.Bin)
		}
		programs[install.Bin] = install
	}
	identities := make(map[string]loopholedecl.CallBrokerProgramIdentity, len(wanted))
	for name := range wanted {
		install, ok := programs[name]
		if !ok || !install.IsPackTree() {
			return nil, fmt.Errorf("call broker %s program %q must be a selected pack-tree program; add a host-executable [[program]] to %s", broker.Source, name, pack)
		}
		if filepath.IsAbs(install.PackFrom) || filepath.IsAbs(install.PackRun) || hasDotDotPath(install.PackFrom) || hasDotDotPath(install.PackRun) {
			return nil, fmt.Errorf("call broker %s selected pack %s program %q has a path outside its staged tree", broker.Source, pack, name)
		}
		candidate, err := filepath.Abs(filepath.Join(root, filepath.FromSlash(install.PackFrom), filepath.FromSlash(install.PackRun)))
		if err != nil || !pathWithin(root, candidate) {
			return nil, fmt.Errorf("call broker %s selected pack %s program %q has a path outside its staged tree", broker.Source, pack, name)
		}
		target, err := filepath.EvalSymlinks(candidate)
		if err != nil {
			return nil, fmt.Errorf("call broker %s selected pack %s program %q is missing from its staged tree: %w", broker.Source, pack, name, err)
		}
		target, err = filepath.Abs(target)
		if err != nil || !pathWithin(root, target) {
			return nil, fmt.Errorf("call broker %s selected pack %s program %q resolves outside its staged tree", broker.Source, pack, name)
		}
		file, err := os.Open(target)
		if err != nil {
			return nil, fmt.Errorf("call broker %s selected pack %s program %q: %w", broker.Source, pack, name, err)
		}
		info, statErr := file.Stat()
		if statErr != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
			_ = file.Close()
			return nil, fmt.Errorf("call broker %s selected pack %s program %q must be a regular host-executable file in its staged tree", broker.Source, pack, name)
		}
		h := sha256.New()
		if _, err := io.Copy(h, file); err != nil {
			_ = file.Close()
			return nil, fmt.Errorf("call broker %s selected pack %s program %q checksum: %w", broker.Source, pack, name, err)
		}
		if err := file.Close(); err != nil {
			return nil, fmt.Errorf("call broker %s selected pack %s program %q checksum: %w", broker.Source, pack, name, err)
		}
		rel, err := filepath.Rel(root, target)
		if err != nil {
			return nil, err
		}
		identities[name] = loopholedecl.CallBrokerProgramIdentity{Pack: pack, Bin: name,
			Path: target, SHA256: fmt.Sprintf("%x", h.Sum(nil)), RelPath: filepath.ToSlash(rel)}
	}
	return identities, nil
}

func readPackManifest(root string) (string, []byte, error) {
	for _, name := range []string{"pack.toml", "pack.json"} {
		candidate := filepath.Join(root, name)
		if _, err := os.Lstat(candidate); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return "", nil, err
		}
		resolved, err := filepath.EvalSymlinks(candidate)
		if err != nil {
			return "", nil, err
		}
		resolved, err = filepath.Abs(resolved)
		if err != nil || !pathWithin(root, resolved) {
			return "", nil, fmt.Errorf("%s resolves outside the selected staged tree", name)
		}
		info, err := os.Stat(resolved)
		if err != nil {
			return "", nil, err
		}
		if !info.Mode().IsRegular() {
			return "", nil, fmt.Errorf("%s is not a regular file", name)
		}
		data, err := os.ReadFile(resolved)
		return resolved, data, err
	}
	return "", nil, fmt.Errorf("pack.toml or pack.json is missing")
}

func programToken(token string) string {
	if !strings.HasPrefix(token, "{program:") || !strings.HasSuffix(token, "}") {
		return ""
	}
	return strings.TrimSuffix(strings.TrimPrefix(token, "{program:"), "}")
}

func hasDotDotPath(path string) bool {
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		if part == ".." {
			return true
		}
	}
	return false
}

func pathWithin(root, candidate string) bool {
	rel, err := filepath.Rel(root, candidate)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
