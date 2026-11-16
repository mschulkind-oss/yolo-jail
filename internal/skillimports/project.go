package skillimports

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/gitbroker"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

const (
	maxImportSkillBytes   = 32 << 20
	maxImportSkillEntries = 4096
	maxTreeListingBytes   = 8 << 20
	maxLinkTargetBytes    = 4096
)

// ProjectedSkill is one selected immediate-child skill materialized from immutable Git objects.
// Root is that skill's own directory, deliberately forming the confinement root for its links.
type ProjectedSkill struct {
	Handle string
	Name   string
	Root   string
}

// Projection is a temporary, read-only input to the confined workspace-skill staging path.
type Projection struct {
	Root    string
	Skills  []ProjectedSkill
	Missing []string
	Refused []ProjectionRefusal
}

// ProjectionBudget shares the bounded object-to-filesystem projection across imports in one
// launch. Its counters are private so callers can pass one budget without changing its policy.
type ProjectionBudget struct {
	bytes   int64
	entries int
}

// Inspection is the host-only, read-only summary used by `yolo skills list`.
type Inspection struct {
	CurrentAccess string
	Allowed       bool
	Resolution    Resolution
	HasResolution bool
	CommitStored  bool
	Skills        []string
	Unimported    []string
}

// Inspect reports what a valid current record would project without creating scratch files,
// changing records, or contacting a remote.
func (s *Store) Inspect(entry config.SkillImport, authority Authority) (Inspection, error) {
	var out Inspection
	if err := validateEntry(entry); err != nil {
		return out, err
	}
	access, allowed, err := gitbroker.DecideImportAccess(entry.Git, authority.ApprovedRemotes,
		authority.CurrentRemotes, authority.ApprovedGrants, authority.CurrentGrants)
	if err != nil {
		return out, err
	}
	out.CurrentAccess, out.Allowed = access, allowed
	record, ok := s.Resolution(entry.Handle, entry)
	if !ok {
		return out, nil
	}
	out.Resolution, out.HasResolution = record, true
	out.CommitStored = s.HasCommit(record.Commit)
	if !allowed || !out.CommitStored || !cacheAccessAllows(record.AccessClass, access) {
		return out, nil
	}
	treeID, err := s.gitOutput("rev-parse", "--verify", record.Commit+":"+entry.Path)
	if err != nil {
		return out, nil
	}
	listing, err := s.gitLimited(maxTreeListingBytes, "ls-tree", "-z", strings.TrimSpace(treeID))
	if err != nil {
		return out, nil
	}
	directories, _, err := immediateSkillTrees(listing)
	if err != nil {
		return out, nil
	}
	entries := map[string]bool{}
	for _, raw := range bytes.Split(listing, []byte{0}) {
		if len(raw) == 0 {
			continue
		}
		item, ok := parseTreeEntry(string(raw))
		if !ok {
			return out, nil
		}
		entries[safeProjectionName(item.name)] = true
	}
	selected := map[string]bool{}
	if entry.OnlySet {
		for _, name := range entry.Only {
			if _, ok := directories[name]; ok {
				selected[name] = true
			}
		}
	} else {
		for name := range directories {
			selected[name] = true
		}
	}
	for name := range selected {
		out.Skills = append(out.Skills, name)
	}
	sort.Strings(out.Skills)
	for name := range entries {
		if !selected[name] {
			out.Unimported = append(out.Unimported, name)
		}
	}
	sort.Strings(out.Unimported)
	return out, nil
}

// ProjectionRefusal is a fixed reason and safe source label suitable for a launch disclosure.
type ProjectionRefusal struct {
	Path   string
	Reason string
}

// Close removes this call's private projection scratch tree.
func (p *Projection) Close() {
	if p != nil && p.Root != "" {
		_ = os.RemoveAll(p.Root)
		p.Root = ""
	}
}

// Project reads only the selected commit's configured skill directory from the existing bare
// object store. It never fetches, checks out, interprets pack metadata or mutates the resolution
// record. Current authority is checked before any stored object is used.
func (s *Store) Project(entry config.SkillImport, authority Authority) (*Projection, error) {
	return s.ProjectWithBudget(entry, authority, &ProjectionBudget{})
}

// ProjectWithBudget is Project with one caller-owned budget shared across the other workspace
// imports projected during this launch.
func (s *Store) ProjectWithBudget(entry config.SkillImport, authority Authority, budget *ProjectionBudget) (*Projection, error) {
	projection := &Projection{}
	if budget == nil {
		budget = &ProjectionBudget{}
	}
	if !validProjectionPath(entry.Path) {
		projection.Refused = append(projection.Refused, ProjectionRefusal{
			Path: "skill_imports." + entry.Handle, Reason: "the configured skill path is invalid",
		})
		return projection, nil
	}
	access, allowed, err := gitbroker.DecideImportAccess(entry.Git, authority.ApprovedRemotes,
		authority.CurrentRemotes, authority.ApprovedGrants, authority.CurrentGrants)
	if err != nil || !allowed {
		projection.Refused = append(projection.Refused, ProjectionRefusal{
			Path: "skill_imports." + entry.Handle, Reason: "the current Git broker decision does not allow this import",
		})
		return projection, nil
	}
	record, ok := s.Resolution(entry.Handle, entry)
	if !ok || !s.HasCommit(record.Commit) {
		projection.Refused = append(projection.Refused, ProjectionRefusal{
			Path: "skill_imports." + entry.Handle, Reason: "no valid resolution and stored commit are available",
		})
		return projection, nil
	}
	if !cacheAccessAllows(record.AccessClass, access) {
		projection.Refused = append(projection.Refused, ProjectionRefusal{
			Path: "skill_imports." + entry.Handle, Reason: "the stored commit was obtained with a higher access class than is currently allowed",
		})
		return projection, nil
	}
	if err := os.MkdirAll(paths.GlobalStorage(), 0o700); err != nil {
		projection.Refused = append(projection.Refused, ProjectionRefusal{
			Path: "skill_imports." + entry.Handle, Reason: "host projection storage is unavailable",
		})
		return projection, nil
	}
	root, err := os.MkdirTemp(paths.GlobalStorage(), "skill-import-projection-")
	if err != nil {
		projection.Refused = append(projection.Refused, ProjectionRefusal{
			Path: "skill_imports." + entry.Handle, Reason: "host projection storage is unavailable",
		})
		return projection, nil
	}
	projection.Root = root
	pathTree, err := s.gitOutput("rev-parse", "--verify", record.Commit+":"+entry.Path)
	if err != nil {
		projection.Refused = append(projection.Refused, ProjectionRefusal{
			Path: "skill_imports." + entry.Handle + "/" + entry.Path, Reason: "the configured path is absent or is not a Git tree",
		})
		return projection, nil
	}
	pathTree = strings.TrimSpace(pathTree)
	if !validObjectID(pathTree) {
		projection.Refused = append(projection.Refused, ProjectionRefusal{
			Path: "skill_imports." + entry.Handle + "/" + entry.Path, Reason: "the configured path is not a Git tree",
		})
		return projection, nil
	}
	listing, err := s.gitLimited(maxTreeListingBytes, "ls-tree", "-z", pathTree)
	if err != nil {
		projection.Refused = append(projection.Refused, ProjectionRefusal{
			Path: "skill_imports." + entry.Handle + "/" + entry.Path, Reason: "the selected Git tree could not be read safely",
		})
		return projection, nil
	}
	directories, invalidNames, err := immediateSkillTrees(listing)
	if err != nil {
		projection.Refused = append(projection.Refused, ProjectionRefusal{
			Path: "skill_imports." + entry.Handle + "/" + entry.Path, Reason: "the selected Git tree contains malformed entries",
		})
		return projection, nil
	}
	for _, name := range invalidNames {
		projection.Refused = append(projection.Refused, ProjectionRefusal{
			Path:   "skill_imports." + entry.Handle + "/" + entry.Path + "/" + name,
			Reason: "the Git tree has an invalid skill directory name",
		})
	}
	selected := make([]string, 0, len(directories))
	if entry.OnlySet {
		for _, name := range entry.Only {
			if _, ok := directories[name]; !ok {
				projection.Missing = append(projection.Missing, name)
				continue
			}
			selected = append(selected, name)
		}
	} else {
		for name := range directories {
			selected = append(selected, name)
		}
		sort.Strings(selected)
	}
	for _, name := range selected {
		skillRoot := filepath.Join(root, name)
		if err := os.Mkdir(skillRoot, 0o700); err != nil {
			projection.Refused = append(projection.Refused, ProjectionRefusal{
				Path: "skill_imports." + entry.Handle + "/" + name, Reason: "the selected skill could not be projected safely",
			})
			continue
		}
		used, err := s.materializeSkill(directories[name], skillRoot, *budget)
		if err != nil {
			_ = os.RemoveAll(skillRoot)
			projection.Refused = append(projection.Refused, ProjectionRefusal{
				Path: "skill_imports." + entry.Handle + "/" + name, Reason: projectionReason(err),
			})
			continue
		}
		budget.bytes += used.bytes
		budget.entries += used.entries
		projection.Skills = append(projection.Skills, ProjectedSkill{Handle: entry.Handle, Name: name, Root: skillRoot})
	}
	sort.Strings(projection.Missing)
	return projection, nil
}

type treeEntry struct {
	mode     string
	typeName string
	object   string
	name     string
}

type projectedLink struct {
	path   string
	target string
}

func immediateSkillTrees(listing []byte) (map[string]string, []string, error) {
	directories := map[string]string{}
	var invalid []string
	for _, raw := range bytes.Split(listing, []byte{0}) {
		if len(raw) == 0 {
			continue
		}
		entry, ok := parseTreeEntry(string(raw))
		if !ok {
			return nil, nil, errors.New("malformed tree entry")
		}
		if entry.mode != "040000" || entry.typeName != "tree" {
			continue
		}
		if !validProjectionName(entry.name) {
			invalid = append(invalid, safeProjectionName(entry.name))
			continue
		}
		directories[entry.name] = entry.object
	}
	sort.Strings(invalid)
	return directories, invalid, nil
}

func parseTreeEntry(raw string) (treeEntry, bool) {
	meta, name, ok := strings.Cut(raw, "\t")
	if !ok {
		return treeEntry{}, false
	}
	fields := strings.Split(meta, " ")
	if len(fields) != 3 || !validObjectID(fields[2]) || name == "" {
		return treeEntry{}, false
	}
	return treeEntry{mode: fields[0], typeName: fields[1], object: fields[2], name: name}, true
}

func (s *Store) materializeSkill(treeID, root string, budget ProjectionBudget) (ProjectionBudget, error) {
	listing, err := s.gitLimited(maxTreeListingBytes, "ls-tree", "-r", "-z", treeID)
	if err != nil {
		return ProjectionBudget{}, errors.New("tree listing is unreadable or exceeds the projection bound")
	}
	var entries []treeEntry
	directories := map[string]bool{}
	for _, raw := range bytes.Split(listing, []byte{0}) {
		if len(raw) == 0 {
			continue
		}
		entry, ok := parseTreeEntry(string(raw))
		if !ok || !validTreePath(entry.name) {
			return ProjectionBudget{}, errors.New("tree contains an invalid path entry")
		}
		if entry.mode == "160000" || entry.typeName == "commit" {
			return ProjectionBudget{}, errors.New("skill contains a submodule entry")
		}
		if entry.typeName != "blob" || entry.mode != "100644" && entry.mode != "100755" && entry.mode != "120000" {
			return ProjectionBudget{}, errors.New("skill contains an unsupported Git object mode")
		}
		entries = append(entries, entry)
		components := strings.Split(entry.name, "/")
		for i := 1; i < len(components); i++ {
			directories[strings.Join(components[:i], "/")] = true
		}
		if len(entries)+len(directories)+1 > maxImportSkillEntries-budget.entries {
			return ProjectionBudget{}, errors.New("skill exceeds the projection entry bound")
		}
	}
	used := ProjectionBudget{}
	var links []projectedLink
	for _, entry := range entries {
		sizeOutput, err := s.gitOutput("cat-file", "-s", entry.object)
		if err != nil {
			return ProjectionBudget{}, errors.New("skill blob could not be read")
		}
		size, err := strconv.ParseInt(strings.TrimSpace(sizeOutput), 10, 64)
		if err != nil || size < 0 {
			return ProjectionBudget{}, errors.New("skill blob has an invalid size")
		}
		maxSize := int64(maxImportSkillBytes) - budget.bytes - used.bytes
		if entry.mode == "120000" && size > maxLinkTargetBytes || size > maxSize {
			return ProjectionBudget{}, errors.New("skill exceeds the projection byte or link bound")
		}
		data, err := s.gitLimited(int(size)+1, "cat-file", "blob", entry.object)
		if err != nil || int64(len(data)) != size {
			return ProjectionBudget{}, errors.New("skill blob could not be read within its declared size")
		}
		if entry.mode == "120000" {
			if bytes.IndexByte(data, 0) >= 0 {
				return ProjectionBudget{}, errors.New("skill symlink target contains a NUL byte")
			}
			links = append(links, projectedLink{path: entry.name, target: string(data)})
			used.bytes += int64(len(data))
			continue
		}
		name := filepath.Join(root, filepath.FromSlash(entry.name))
		if err := os.MkdirAll(filepath.Dir(name), 0o700); err != nil {
			return ProjectionBudget{}, errors.New("skill directory could not be created")
		}
		mode := os.FileMode(0o644)
		if entry.mode == "100755" {
			mode = 0o755
		}
		file, err := os.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY|syscall.O_NOFOLLOW, mode)
		if err != nil {
			return ProjectionBudget{}, errors.New("skill file could not be created safely")
		}
		_, writeErr := file.Write(data)
		closeErr := file.Close()
		if writeErr != nil || closeErr != nil {
			return ProjectionBudget{}, errors.New("skill file could not be written safely")
		}
		used.bytes += size
	}
	for _, link := range links {
		name := filepath.Join(root, filepath.FromSlash(link.path))
		if err := os.MkdirAll(filepath.Dir(name), 0o700); err != nil {
			return ProjectionBudget{}, errors.New("skill symlink parent could not be created")
		}
		if err := os.Symlink(link.target, name); err != nil {
			return ProjectionBudget{}, errors.New("skill symlink could not be created safely")
		}
	}
	used.entries = 1 + len(entries) + len(directories)
	if used.entries+budget.entries > maxImportSkillEntries || used.bytes+budget.bytes > maxImportSkillBytes {
		return ProjectionBudget{}, errors.New("projection budget exceeded")
	}
	return used, nil
}

func validProjectionPath(value string) bool {
	if value == "" || value == "." || strings.HasPrefix(value, "/") || strings.Contains(value, `\\`) || path.Clean(value) != value {
		return false
	}
	for _, component := range strings.Split(value, "/") {
		if component == ".." || component == ".git" {
			return false
		}
	}
	return true
}

func validTreePath(name string) bool {
	if name == "" || strings.HasPrefix(name, "/") || strings.Contains(name, "\\") {
		return false
	}
	for _, component := range strings.Split(name, "/") {
		if component == "" || component == "." || component == ".." || component == ".git" {
			return false
		}
	}
	return len(name) <= 512
}

func validProjectionName(name string) bool {
	return name != "" && name != "." && name != ".." && !strings.ContainsAny(name, `/\\`)
}

func safeProjectionName(name string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || r == '/' || r == '\\' {
			return '_'
		}
		return r
	}, name)
}

func projectionReason(err error) string {
	if err == nil {
		return "the selected skill could not be projected safely"
	}
	return err.Error()
}

func (s *Store) gitOutput(args ...string) (string, error) {
	data, err := s.gitLimited(1<<20, args...)
	return string(data), err
}

func (s *Store) gitLimited(maxBytes int, args ...string) ([]byte, error) {
	if maxBytes < 0 {
		return nil, errors.New("invalid Git output bound")
	}
	git, err := exec.LookPath("git")
	if err != nil {
		return nil, err
	}
	fullArgs := append([]string{"--git-dir", s.ObjectDirectory()}, args...)
	cmd := exec.Command(git, fullArgs...)
	cmd.Env = isolatedGitEnv()
	cmd.Dir = s.directory
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	data, readErr := io.ReadAll(io.LimitReader(stdout, int64(maxBytes)+1))
	if len(data) > maxBytes {
		_ = cmd.Process.Kill()
	}
	waitErr := cmd.Wait()
	if readErr != nil {
		return nil, readErr
	}
	if len(data) > maxBytes {
		return nil, errors.New("git output exceeds the projection bound")
	}
	if waitErr != nil {
		return nil, fmt.Errorf("git %v: %w: %s", args, waitErr, strings.TrimSpace(stderr.String()))
	}
	return data, nil
}
