package run

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jailcontent"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	runtimeidentity "github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// SkillInventorySources is the source set from the last pack tree actually staged for this
// workspace. It contains declarations and paths only; reading it never stages packs or runs hooks.
type SkillInventorySources struct {
	WorkspaceDirs []string
	PackSources   []jailcontent.PackSkillSource
	Targets       []jailcontent.SkillTarget
}

// ReadSkillInventorySources reads the last live pack tree and derives the same skills audiences
// and workspace source order the launch uses. A missing tree is unknown, not an empty inventory.
func ReadSkillInventorySources(workspace string) (SkillInventorySources, error) {
	var out SkillInventorySources
	if workspace == "" {
		return out, errors.New("workspace is required")
	}
	name := runtimeidentity.FromWorkspace(workspace)
	record, err := os.ReadFile(paths.LivePackTreeRecord(name))
	if err != nil {
		return out, fmt.Errorf("there is no live pack-tree record for this workspace: %w", err)
	}
	treeName := strings.TrimSpace(string(record))
	if treeName == "" || !filepath.IsLocal(treeName) || filepath.Base(treeName) != treeName {
		return out, errors.New("the live pack-tree record has an invalid directory name")
	}
	tree := filepath.Join(paths.PackTreeRoot(name), treeName)
	if !isDir(tree) {
		return out, errors.New("the last live pack tree is unavailable")
	}
	packs, err := loadPackTree(tree)
	if err != nil {
		return out, fmt.Errorf("the last live pack tree could not be read safely: %w", err)
	}
	for _, pack := range packs {
		sources, _ := pack.SkillsSources()
		out.PackSources = append(out.PackSources, jailSkillSources(pack, sources)...)
	}
	workspaceDirs, err := workspaceSkillDirsForInventory(packs)
	if err != nil {
		return out, fmt.Errorf("read embedded workspace skill declarations: %w", err)
	}
	out.WorkspaceDirs = workspaceDirs
	out.Targets = packSkillTargets(packs)
	return out, nil
}
