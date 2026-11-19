package packload

// Architecture and invariants: docs/reference/toml-scoped-pack-imports/README.md

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/url"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/pluginpack"
)

// SkillProjection records candidate selection, not a filtered checkout.
// Rows are an inert plan until the common carrier boundary validates their bytes.
type SkillProjection struct {
	Mode                string                 `json:"mode"`
	Namespace           string                 `json:"namespace,omitempty"`
	NamespaceOwner      string                 `json:"namespace_owner,omitempty"`
	Subdir              string                 `json:"subdir,omitempty"`
	Selection           packsrc.SkillSelection `json:"selection"`
	Origin              string                 `json:"origin"`
	ImportIdentity      string                 `json:"import_identity"`
	Source              string                 `json:"source"`
	Commit              string                 `json:"commit,omitempty"`
	Digest              string                 `json:"digest"`
	CopiedContentDigest string                 `json:"copied_content_digest,omitempty"`
	Candidates          []string               `json:"candidates"`
	Selected            []string               `json:"selected"`
	Excluded            []string               `json:"excluded,omitempty"`
	Plugins             []string               `json:"plugins,omitempty"`
	Omitted             map[string]int         `json:"omitted,omitempty"`
	Rows                []ProjectedSkillRow    `json:"rows"`
}

// ProjectedSkillRow carries resolver-established typed-directory eligibility.
// No manifest or config key decodes this authority.
type ProjectedSkillRow struct {
	Name            string                `json:"name"`
	Path            string                `json:"path"`
	SourceDirectory string                `json:"source_directory,omitempty"`
	ResolvedCommit  string                `json:"resolved_commit,omitempty"`
	Kind            string                `json:"kind"`
	MarkerState     string                `json:"marker_state"`
	Governor        string                `json:"governor,omitempty"`
	Sourced         bool                  `json:"sourced,omitempty"`
	By              packdecl.Contribution `json:"by"`
}

type SkillProjectionSpec struct {
	Selection                                                                           packsrc.SkillSelection
	Subdir, Origin, ImportIdentity, Source, Commit, RootName, Namespace, NamespaceOwner string
}

// ReadSkillsProjectionSource reads only a declaration. It does not discover home,
// overlay, briefing, plugin or executable content at the original root.
func ReadSkillsProjectionSource(source fs.FS, diagnosticRoot, name string) (*Pack, []string, error) {
	data, manifestName, problem := discoverManifestWith(".",
		func(p string) (fs.FileInfo, error) { return fs.Stat(source, p) },
		func(p string) ([]byte, error) { return fs.ReadFile(source, p) }, func(a, b string) string { return path.Join(a, b) })
	if problem != "" {
		return nil, nil, fmt.Errorf("pack %s: %s", name, problem)
	}
	decl, problems, notes := decodeManifest(data, manifestName, name, true)
	return &Pack{Name: name, Root: diagnosticRoot, Decl: decl, SkewNotes: notes, projectionSource: source}, problems, nil
}

// ProjectSkills enumerates ordinary and sourced rows before any closure or
// payload publication. Failure has no whole-pack or partial-selection fallback.
func ProjectSkills(p *Pack, source fs.FS, spec SkillProjectionSpec) (*Pack, error) {
	if err := spec.Selection.Validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", spec.Origin, err)
	}
	projection := &SkillProjection{Namespace: spec.Namespace, NamespaceOwner: spec.NamespaceOwner, Mode: "typed", Subdir: spec.Subdir, Selection: spec.Selection, Origin: spec.Origin, ImportIdentity: spec.ImportIdentity, Source: spec.Source, Commit: spec.Commit, Omitted: map[string]int{}}
	candidates := map[string]ProjectedSkillRow{}
	plugins := map[string]bool{}
	marker := func(dir string) (string, error) {
		entries, err := fs.ReadDir(source, dir)
		if err != nil {
			return "", err
		}
		for _, e := range entries {
			if e.Name() != "SKILL.md" {
				continue
			}
			if !e.Type().IsRegular() {
				return "", fmt.Errorf("%s/SKILL.md is not a direct regular marker; replace it with a regular file", dir)
			}
			info, err := e.Info()
			if err != nil || !info.Mode().IsRegular() {
				return "", fmt.Errorf("%s/SKILL.md is not a direct regular marker; repair the marker", dir)
			}
			return "present", nil
		}
		return "absent", nil
	}
	if spec.Selection.Dir != "" {
		projection.Mode = "raw"
		dir := spec.Selection.Dir
		state, err := marker(dir)
		if err != nil {
			return nil, err
		}
		if state == "present" {
			name := path.Base(dir)
			if dir == "." {
				name = spec.RootName
			}
			candidates[name] = ProjectedSkillRow{Name: name, Path: dir, Kind: "marker", MarkerState: state, By: packdecl.Contribution{Kind: packdecl.KindSkills, As: name}}
		} else {
			entries, err := fs.ReadDir(source, dir)
			if err != nil {
				return nil, err
			}
			for _, e := range entries {
				child := path.Join(dir, e.Name())
				info, err := fs.Stat(source, child)
				if err != nil || !info.IsDir() {
					continue
				}
				state, err := marker(child)
				if err != nil {
					continue
				} // nonregular markers are not raw candidates
				if state == "present" {
					candidates[e.Name()] = ProjectedSkillRow{Name: e.Name(), Path: child, Kind: "marker", MarkerState: state, By: packdecl.Contribution{Kind: packdecl.KindSkills, As: e.Name()}}
				}
			}
		}
		if len(candidates) == 0 {
			var deeper []string
			visited := 0
			_ = fs.WalkDir(source, dir, func(p string, e fs.DirEntry, err error) error {
				if err != nil {
					return nil
				}
				visited++
				if len(deeper) >= 8 || visited > 256 {
					return fs.SkipAll
				}
				if e.Name() == "SKILL.md" && e.Type().IsRegular() && path.Dir(p) != dir {
					deeper = append(deeper, path.Dir(p))
				}
				return nil
			})
			return nil, fmt.Errorf("%s: no skill directories with SKILL.md directly under %q; use skills.dir for a deeper marker directory %v", spec.Origin, dir, deeper)
		}
	} else {
		if p == nil || p.Decl == nil {
			return nil, fmt.Errorf("typed selection requires a pack declaration")
		}
		p.projectionSource = source
		governed, problems := p.GovernedSources(packdecl.KindSkills)
		if len(problems) > 0 {
			return nil, fmt.Errorf("%s: %s; repair skills from", spec.Origin, strings.Join(problems, "; "))
		}
		var roots []string
		by := map[string]packdecl.Contribution{}
		for _, src := range governed {
			roots = append(roots, src.Rel)
			by[src.Rel] = src.By
		}
		directories, err := ordinarySkillDirectoriesFS(source, roots)
		if err != nil {
			return nil, err
		}
		for name, dir := range directories {
			if _, plugin := pluginpack.LoadFS(source, dir); plugin {
				plugins[name] = true
				continue
			}
			governor := path.Dir(dir)
			c := by[governor]
			c.From, c.Into, c.Agent = "", "", ""
			c.As = name
			candidates[name] = ProjectedSkillRow{Name: name, Path: dir, Kind: "directory", Governor: governor, By: c}
		}
		projection.Omitted["needs"] = len(p.Decl.DeclaredNeeds())
		for _, c := range p.declaration() {
			if c.IsSourcedSkill() {
				name := c.SkillBaseName()
				if _, duplicate := candidates[name]; duplicate {
					return nil, fmt.Errorf("%s: duplicate ordinary/sourced candidate %q; repair the pack's skill names", spec.Origin, name)
				}
				candidates[name] = ProjectedSkillRow{Name: name, Kind: "marker", MarkerState: "present", Sourced: true, By: c}
			} else if c.Kind != packdecl.KindSkills {
				projection.Omitted[string(c.Kind)]++
			}
		}
		if len(candidates) == 0 {
			for _, selector := range append(append([]string(nil), spec.Selection.Only...), spec.Selection.Exclude...) {
				if plugins[selector] {
					return nil, fmt.Errorf("%s: %q is a plugin; import the pack whole to use it", spec.Origin, selector)
				}
			}
			return nil, fmt.Errorf("%s: pack has no skill contributions; use skills.dir for a raw collection", spec.Origin)
		}
	}
	for name := range candidates {
		projection.Candidates = append(projection.Candidates, name)
	}
	for name := range plugins {
		projection.Plugins = append(projection.Plugins, name)
	}
	sort.Strings(projection.Candidates)
	sort.Strings(projection.Plugins)
	selected, excluded, err := selectSkillCandidates(projection.Candidates, plugins, spec.Selection, spec.Origin)
	if err != nil {
		return nil, err
	}
	projection.Selected, projection.Excluded = selected, excluded
	effective := &Pack{Name: p.Name, Root: p.Root, Decl: &packdecl.Manifest{}, Official: p.Official, Local: p.Local, Projection: projection, projectionSource: source}
	for _, name := range selected {
		row := candidates[name]
		if !row.Sourced {
			state, err := marker(row.Path)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", spec.Origin, err)
			}
			if row.Kind == "marker" && state != "present" {
				return nil, fmt.Errorf("%s: selected raw marker changed; repair skills.dir", spec.Origin)
			}
			row.MarkerState = state
			row.By.Source = "file://" + filepath.ToSlash(filepath.Join(p.Root, filepath.FromSlash(row.Path)))
			if spec.Commit != "" {
				address, err := packsrc.ParsePackAddress(spec.Source)
				if err != nil {
					return nil, err
				}
				row.SourceDirectory = path.Join(address.Path, spec.Subdir, row.Path)
				row.ResolvedCommit = spec.Commit
				row.By.Source = "git+" + address.Repo + "?ref=" + url.QueryEscape(spec.Commit)
			}
		}
		effective.Decl.Contributes = append(effective.Decl.Contributes, row.By)
		projection.Rows = append(projection.Rows, row)
	}
	if err := projection.RefreshDigest(); err != nil {
		return nil, err
	}
	return effective, nil
}

// RefreshDigest hashes semantic projection state, never a source reread. Origin
// is diagnostic provenance, not a change to the delivered selection.
func (p *SkillProjection) RefreshDigest() error {
	copy := *p
	copy.Digest, copy.Origin = "", ""
	raw, err := json.Marshal(copy)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(raw)
	p.Digest = hex.EncodeToString(digest[:])
	return nil
}

func selectSkillCandidates(names []string, plugins map[string]bool, selection packsrc.SkillSelection, origin string) ([]string, []string, error) {
	fail := func(problem string) ([]string, []string, error) {
		return nil, nil, fmt.Errorf("%s: %s; available names: %v; edit skills.only/skills.exclude with yolo config set or remove skills for the whole pack", origin, problem, names)
	}
	matched := func(selector string) []string {
		var out []string
		for _, name := range names {
			if ok, _ := path.Match(selector, name); ok {
				out = append(out, name)
			}
		}
		return out
	}
	for _, selectors := range [][]string{selection.Only, selection.Exclude} {
		for _, selector := range selectors {
			if err := packsrc.SkillSelector(selector); err != nil {
				return fail(err.Error())
			}
			if len(matched(selector)) == 0 {
				if plugins[selector] {
					return fail(fmt.Sprintf("%q is a plugin; import the pack whole to use it", selector))
				}
				return fail(fmt.Sprintf("selector %q matches no candidate", selector))
			}
		}
	}
	excluded := map[string]string{}
	for _, selector := range selection.Exclude {
		for _, name := range matched(selector) {
			excluded[name] = selector
		}
	}
	included := map[string]bool{}
	if len(selection.Only) == 0 {
		for _, name := range names {
			included[name] = true
		}
	}
	for _, selector := range selection.Only {
		remaining := false
		var conflicts []string
		for _, name := range matched(selector) {
			included[name] = true
			if ex := excluded[name]; ex == "" {
				remaining = true
			} else {
				conflicts = append(conflicts, ex)
			}
		}
		if !remaining {
			return fail(fmt.Sprintf("only selector %q is entirely excluded by %v", selector, conflicts))
		}
	}
	var keep, drop []string
	for _, name := range names {
		if included[name] && excluded[name] == "" {
			keep = append(keep, name)
		} else {
			drop = append(drop, name)
		}
	}
	if len(keep) == 0 {
		return fail("skill selection is empty")
	}
	return keep, drop, nil
}
