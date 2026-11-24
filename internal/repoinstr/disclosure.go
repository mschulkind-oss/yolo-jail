package repoinstr

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// The scan's bounds (IF-D12): enough for any real repository's instruction files, and a hard stop
// for a workspace that is a whole home directory. A scan that reaches one says so.
const (
	scanMaxDepth = 6
	scanMaxDirs  = 5000
)

// Scan is the instruction files found in a workspace, per agent.
type Scan struct {
	// Files maps an agent to the workspace-relative files its `reads` patterns matched, sorted.
	Files map[string][]string
	// Incomplete is true when the scan stopped at a bound before it had seen every directory.
	Incomplete bool
}

// ScanWorkspace walks the workspace once, skipping .git and node_modules and following no link, and
// matches each agent's declared `reads` patterns against what it sees.
func ScanWorkspace(workspace string, reads map[string][]string) Scan {
	res := Scan{Files: map[string][]string{}}
	matchers := map[string][]*regexp.Regexp{}
	for agent, patterns := range reads {
		for _, p := range patterns {
			matchers[agent] = append(matchers[agent], patternRegexp(p))
		}
	}
	dirs := 0
	_ = filepath.WalkDir(workspace, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, rerr := filepath.Rel(workspace, path)
		if rerr != nil || rel == "." {
			return nil
		}
		if d.IsDir() {
			if d.Name() == ".git" || d.Name() == "node_modules" {
				return filepath.SkipDir
			}
			depth := strings.Count(rel, string(filepath.Separator)) + 1
			if depth > scanMaxDepth {
				res.Incomplete = true
				return filepath.SkipDir
			}
			dirs++
			if dirs > scanMaxDirs {
				res.Incomplete = true
				return filepath.SkipAll
			}
			return nil
		}
		slash := filepath.ToSlash(rel)
		for agent, res2 := range matchers {
			for _, re := range res2 {
				if re.MatchString(slash) {
					res.Files[agent] = append(res.Files[agent], slash)
					break
				}
			}
		}
		return nil
	})
	for agent := range res.Files {
		sort.Strings(res.Files[agent])
	}
	return res
}

// patternRegexp turns one declared `reads` pattern into a matcher over a slash-separated
// workspace-relative path. A pattern matches at the workspace root or under any directory (an
// instruction file is found in subdirectories too); `**` crosses directories and `*` does not.
func patternRegexp(pattern string) *regexp.Regexp {
	var b strings.Builder
	b.WriteString("^(?:.*/)?")
	for i := 0; i < len(pattern); i++ {
		switch {
		case strings.HasPrefix(pattern[i:], "**/"):
			b.WriteString("(?:.*/)?")
			i += 2
		case strings.HasPrefix(pattern[i:], "**"):
			b.WriteString(".*")
			i++
		case pattern[i] == '*':
			b.WriteString("[^/]*")
		default:
			b.WriteString(regexp.QuoteMeta(string(pattern[i])))
		}
	}
	b.WriteString("$")
	return regexp.MustCompile(b.String())
}

// Disclosure is the launch's repo-instructions lines, one per selected agent that declares what it
// reads, for a launch that ignores or replaces (IF-D5, IF-D15). Empty for a launch that keeps the
// files. It is never suppressible by the callers and names a next step on every line that reports a
// problem: an agent that still reads the files names the agents that can ignore them and the
// command that sets the mode back to keep.
//
// packs is the selected set; shipped is the set yolo ships, read only to name the agents that CAN
// ignore the files (never to decide what a launch selects). host says whether the line is for the
// unconfined path, whose mechanism is the launch flag. onlyBin, when set, limits the lines to the
// agent whose program that is (`yolo host -- claude` says nothing about the others).
func Disclosure(packs, shipped []*packload.Pack, res *Resolved, workspace string, host bool, onlyBin string) []string {
	if !res.Ignoring() {
		return nil
	}
	agents := packload.InstructionsAgents(packs)
	if len(agents) == 0 {
		return nil
	}
	reads := map[string][]string{}
	for _, a := range agents {
		if onlyBin == "" || a.Bin == onlyBin {
			reads[a.Agent] = a.Reads
		}
	}
	scan := ScanWorkspace(workspace, reads)
	capable := supportedAgents(shipped, packs)
	sel := &packdecl.InstructionsSelection{Mode: res.Mode, Host: host}
	var lines []string
	for _, a := range agents {
		if onlyBin != "" && a.Bin != onlyBin {
			continue
		}
		lines = append(lines, agentLine(a.Instructions, scan, capable, res, sel))
	}
	return lines
}

func supportedAgents(sets ...[]*packload.Pack) []string {
	seen := map[string]bool{}
	var out []string
	for _, set := range sets {
		for _, a := range packload.InstructionsAgents(set) {
			if a.Supported() && !seen[a.Agent] {
				seen[a.Agent] = true
				out = append(out, a.Agent)
			}
		}
	}
	sort.Strings(out)
	return out
}

func agentLine(d packdecl.Instructions, scan Scan, capable []string, res *Resolved, sel *packdecl.InstructionsSelection) string {
	const lead = "repo instructions: "
	files := scan.Files[d.Agent]
	what := describeFiles(d, files, scan.Incomplete)
	if !d.Supported() {
		others := make([]string, 0, len(capable))
		for _, c := range capable {
			if c != d.Agent {
				others = append(others, c)
			}
		}
		line := lead + d.Agent + ": NOT ignored, it still reads " + what
		if res.Mode == config.RepoInstructionsModeReplace {
			line += "; the replacement text still joins its briefing"
		}
		if len(others) > 0 {
			line += "; agents that can ignore them: " + strings.Join(others, ", ")
		}
		return line + "; to stop this notice: yolo config set repo_instructions.mode keep"
	}
	line := lead + d.Agent + ": ignoring " + what
	if m := d.Mechanism(sel); m != "" {
		line += " through " + m
	}
	if d.Ignore != nil && len(d.Ignore.StillRead) > 0 {
		line += "; still read: " + strings.Join(d.Ignore.StillRead, ", ")
	}
	if res.Mode == config.RepoInstructionsModeReplace {
		line += "; replaced by " + res.ReplaceWith
	}
	return line
}

// describeFiles names what an agent reads from the repository: the files found, a count with the
// first few names, or, when none were found or the scan could not finish, the patterns.
func describeFiles(d packdecl.Instructions, files []string, incomplete bool) string {
	if len(files) == 0 || incomplete {
		patterns := d.Reads
		if len(patterns) > 3 {
			patterns = append(append([]string{}, patterns[:3]...), fmt.Sprintf("+%d more patterns", len(d.Reads)-3))
		}
		suffix := " (none present now)"
		if incomplete {
			suffix = " (the scan stopped at its bounds, so these are the patterns, not a count)"
		}
		return strings.Join(patterns, ", ") + suffix
	}
	names := files
	extra := ""
	if len(names) > 2 {
		extra = fmt.Sprintf(", +%d more", len(names)-2)
		names = names[:2]
	}
	noun := "files"
	if len(files) == 1 {
		noun = "file"
	}
	return fmt.Sprintf("%d %s (%s%s)", len(files), noun, strings.Join(names, ", "), extra)
}
