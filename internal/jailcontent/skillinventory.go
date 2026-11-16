package jailcontent

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/hostskills"
)

// ImportedSkillShadow identifies the higher-precedence entry that keeps one imported name from
// becoming the winner at a destination.
type ImportedSkillShadow struct {
	Name        string
	Destination string
	By          string
}

// InspectImportedSkillShadows uses the launch's source order, pack audiences and destination
// fences to report which already-projected import names a higher layer takes. It only reads
// confined workspace paths and uses hostskills' observe posture for pack deliveries.
func InspectImportedSkillShadows(workspace *WorkspaceSkills, sources []PackSkillSource,
	targets []SkillTarget, importedNames []string) ([]ImportedSkillShadow, error) {
	if len(targets) == 0 || len(importedNames) == 0 {
		return nil, nil
	}
	if err := SkillCollisionError(sources, targets); err != nil {
		return nil, err
	}
	workspaceNames, err := confinedWorkspaceSkillNames(workspace)
	if err != nil {
		return nil, err
	}
	plan := SkillPlan(sources, targets)
	var shadows []ImportedSkillShadow
	for i, destination := range plan {
		target := targets[i]
		taken := map[string]string{}
		for _, layer := range destination.Layers {
			observeDir, err := observeSkillsDir()
			if err != nil {
				return nil, err
			}
			if layer.Unresolved || layer.Problem != "" {
				return nil, fmt.Errorf("inspect pack %s for %s: %s", layer.Pack, destination.Dir, layer.Problem)
			}
			skipSources := make([]string, 0, len(layer.Plugins))
			for _, plugin := range layer.Plugins {
				skipSources = append(skipSources, plugin.Dir)
			}
			results, err := hostskills.Deliver(hostskills.Request{
				Pack: layer.Pack, Namespace: layer.Namespace, BareStep: layer.BareStep,
				Description: layer.Description, Owner: layer.Pack, Sources: layer.Sources,
				SkipSources: skipSources, SkillsDir: observeDir, Tier: layer.Tier, Observe: true,
			})
			if err != nil {
				return nil, fmt.Errorf("inspect pack %s for %s: %w", layer.Pack, destination.Dir, err)
			}
			for _, result := range results {
				if result.Action == hostskills.ActionRefused || result.Action == hostskills.ActionReserved {
					continue
				}
				if name := observedTopLevelName(observeDir, result.Path); name != "" && !targetHasReservedName(target, name) {
					if _, exists := taken[name]; !exists {
						taken[name] = "pack " + layer.Pack
					}
				}
			}
			for _, plugin := range layer.Plugins {
				pluginResults, err := hostskills.DeliverPlugin(hostskills.PluginRequest{
					Pack: layer.Pack, Namespace: layer.Namespace, BareStep: layer.BareStep,
					Plugin: plugin, SkillsDir: observeDir, Tier: layer.Tier, Observe: true,
				})
				if err != nil {
					return nil, fmt.Errorf("inspect pack %s plugin for %s: %w", layer.Pack, destination.Dir, err)
				}
				for _, result := range pluginResults {
					if result.Action == hostskills.ActionRefused || result.Action == hostskills.ActionReserved {
						continue
					}
					if name := observedTopLevelName(observeDir, result.Path); name != "" && !targetHasReservedName(target, name) {
						if _, exists := taken[name]; !exists {
							taken[name] = "pack " + layer.Pack
						}
					}
				}
			}
		}
		for _, reserved := range target.Reserved {
			if _, exists := taken[reserved]; !exists {
				taken[reserved] = "a reserved destination name"
			}
		}
		for _, name := range builtinSkillNames(true, false) {
			if _, exists := taken[name]; !exists {
				taken[name] = "yolo's built-in skills"
			}
		}
		taken[LSPPluginDir] = "yolo's LSP plugin"
		for name, source := range workspaceNames {
			if _, exists := taken[name]; !exists {
				taken[name] = "workspace skill source " + source
			}
		}
		for _, name := range importedNames {
			if by := taken[name]; by != "" {
				shadows = append(shadows, ImportedSkillShadow{Name: name,
					Destination: destination.Dir, By: by})
			}
		}
	}
	return shadows, nil
}

func observeSkillsDir() (string, error) {
	var token [16]byte
	if _, err := rand.Read(token[:]); err != nil {
		return "", fmt.Errorf("choose a private read-only skills inventory name: %w", err)
	}
	return filepath.Join(os.TempDir(), ".yolo-skill-inventory-"+hex.EncodeToString(token[:])), nil
}

func observedTopLevelName(root, resultPath string) string {
	rel, err := filepath.Rel(root, resultPath)
	if err != nil || rel == "." || filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return ""
	}
	return strings.SplitN(filepath.ToSlash(rel), "/", 2)[0]
}

func targetHasReservedName(target SkillTarget, name string) bool {
	for _, reserved := range target.Reserved {
		if reserved == name {
			return true
		}
	}
	return false
}

func confinedWorkspaceSkillNames(workspace *WorkspaceSkills) (map[string]string, error) {
	out := map[string]string{}
	if workspace == nil || workspace.Root == "" || len(workspace.Dirs) == 0 {
		return out, nil
	}
	tree, err := openConfinedTree(workspace)
	if err != nil {
		return nil, fmt.Errorf("open workspace skill sources: %w", err)
	}
	defer tree.close()
	seenSources := map[string]bool{}
	for _, rel := range workspace.Dirs {
		real, reason := tree.resolveSource(rel)
		if reason != "" {
			return nil, fmt.Errorf("inspect workspace skill source %s: %s", rel, reason)
		}
		if real == "" || seenSources[real] {
			continue
		}
		seenSources[real] = true
		names, err := tree.listDir(real)
		if err != nil {
			return nil, fmt.Errorf("list workspace skill source %s: %w", rel, err)
		}
		for _, name := range names {
			if name == ".git" {
				continue
			}
			child, reason := tree.resolveEntry(joinRel(real, name))
			if reason != "" {
				return nil, fmt.Errorf("inspect workspace skill %s/%s: %s", rel, name, reason)
			}
			if child == "" {
				continue
			}
			info, err := tree.root.Lstat(child)
			if err != nil {
				return nil, fmt.Errorf("inspect workspace skill %s/%s: %w", rel, name, err)
			}
			if info.IsDir() {
				if _, exists := out[name]; !exists {
					out[name] = rel
				}
			}
		}
	}
	return out, nil
}
