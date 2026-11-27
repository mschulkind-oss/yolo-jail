package config

// repoinstructions.go is the `repo_instructions` table: what a launch does about the instruction
// files a repository carries (AGENTS.md, CLAUDE.md and their kin) for the agents it selects.
// docs/reference/workspace-instruction-files.md is the authority; the resolved value a launch uses is
// internal/repoinstr's.
//
//	[repo_instructions]
//	mode = "replace"                  # keep (default) | ignore | replace
//	replace_with = "~/rules/mine.md"  # required when mode is replace; refused otherwise
//
// Both scopes may set it (IF-D4) and the ordinary merge order applies, later layer winning, `mode`
// and `replace_with` independently (IF-D10).

import (
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

const (
	repoInstructionsKey = "repo_instructions"
	// RepoInstructionsModeKeep is the default: the launch does nothing about the repository's files.
	RepoInstructionsModeKeep = "keep"
	// RepoInstructionsModeIgnore stops each selected agent that has a setting for it from reading them.
	RepoInstructionsModeIgnore = "ignore"
	// RepoInstructionsModeReplace is ignore, plus the text of replace_with joined to the briefing.
	RepoInstructionsModeReplace = "replace"
)

// knownRepoInstructionsKeys is the census for the `repo_instructions` table.
var knownRepoInstructionsKeys = set("mode", "replace_with")

// RepoInstructionsKey is the top-level key's spelling, for the callers that name it in advice.
func RepoInstructionsKey() string { return repoInstructionsKey }

// RepoInstructions reads the table out of cfg: the mode (keep when absent or not a string, since
// the validator reports a wrong shape and a reader that guessed would be a second place the shape
// rule lives) and the replace_with path as written.
func RepoInstructions(cfg *jsonx.OrderedMap) (mode, replaceWith string) {
	mode = RepoInstructionsModeKeep
	if cfg == nil {
		return mode, ""
	}
	v, ok := cfg.Get(repoInstructionsKey)
	if !ok {
		return mode, ""
	}
	table, ok := asMap(v)
	if !ok {
		return mode, ""
	}
	if m, ok := table.Get("mode"); ok {
		if s, ok := m.(string); ok && s != "" {
			mode = s
		}
	}
	if r, ok := table.Get("replace_with"); ok {
		replaceWith, _ = r.(string)
	}
	return mode, replaceWith
}

// RepoInstructionsUser is RepoInstructions over the user-scope config alone, the host notch's reading
// (`yolo host apply` writes a real home shared by every workspace, so a workspace layer never
// reaches it) and the layer a launch tells a user-written replace_with path from a project's by.
func RepoInstructionsUser() (mode, replaceWith string) {
	return RepoInstructions(UserScopeConfigOrEmpty())
}

func validateRepoInstructions(config *jsonx.OrderedMap, errs *[]string) {
	v, present := config.Get(repoInstructionsKey)
	if !present || v == nil {
		return
	}
	table, ok := asMap(v)
	if !ok {
		add(errs, "config.repo_instructions: expected a table with `mode` and `replace_with`")
		return
	}
	reportUnknownKeys(table, knownRepoInstructionsKeys, "config.repo_instructions", errs)
	mode := ""
	if raw, ok := table.Get("mode"); ok && raw != nil {
		s, isString := raw.(string)
		switch {
		case !isString:
			add(errs, "config.repo_instructions.mode: expected one of keep, ignore, replace (got "+pyReprValue(raw)+")")
		case s != RepoInstructionsModeKeep && s != RepoInstructionsModeIgnore && s != RepoInstructionsModeReplace:
			add(errs, "config.repo_instructions.mode: expected one of keep, ignore, replace (got "+pyReprValue(raw)+
				"); `yolo config set repo_instructions.mode ignore` sets it")
		default:
			mode = s
		}
	}
	raw, ok := table.Get("replace_with")
	if !ok || raw == nil {
		return
	}
	path, isString := raw.(string)
	switch {
	case !isString || strings.TrimSpace(path) == "":
		add(errs, "config.repo_instructions.replace_with: expected the path of a markdown file (got "+pyReprValue(raw)+")")
	case mode == RepoInstructionsModeKeep || mode == RepoInstructionsModeIgnore:
		add(errs, "config.repo_instructions.replace_with: only the `replace` mode reads it, and this table sets mode "+
			pyReprValue(mode)+"; `yolo config unset repo_instructions.replace_with` removes it, or "+
			"`yolo config set repo_instructions.mode replace` uses it")
	}
}
