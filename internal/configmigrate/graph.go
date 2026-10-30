package configmigrate

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/configdoc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// Request names what one migration covers. On the host it is the user files and the current
// workspace's; in a jail only the project's (config-migration.md §4 item 6), which the caller
// says by leaving UserConfigDir empty.
type Request struct {
	// UserConfigDir is the directory holding the user config (~/.config/yolo-jail); "" leaves the
	// user files out.
	UserConfigDir string
	// Workspace is the workspace whose project and local files migrate; "" leaves them out.
	Workspace string
}

// Role is what a migration does to one file of the graph.
type Role uint8

const (
	// RoleConvert is an old file: it becomes its .toml twin, beside its physical file.
	RoleConvert Role = iota + 1
	// RoleRespell is a TOML file that includes an old file: its include entry is respelled.
	RoleRespell
	// RoleRead is a TOML file the migration reads and leaves alone.
	RoleRead
)

// File is one file of the old graph.
type File struct {
	// Logical is the path as reached: the top-level name, or the include entry joined to the
	// directory of the file that names it.
	Logical string
	// Physical is Logical with every symlink resolved: the file that is read and replaced.
	Physical string
	// Via names the include entry that reached it, "" for a top-level file.
	Via   string
	Scope Scope
	Role  Role
	// Bytes are the file as read; Apply refuses to write over a file that changed since.
	Bytes []byte

	doc      *configdoc.Document
	includes []includeEdge
	topName  string // the top-level name this file is read under, "" for an include or fragment
	fragment bool   // a config.d fragment
}

type includeEdge struct {
	index int
	entry string
	child *File
}

// Missing is an include the graph lists but this machine does not have.
type Missing struct {
	Path, ListedIn string
}

// Graph is the complete old graph of one request.
type Graph struct {
	Request     Request
	Files       []*File
	Unreachable []Missing

	userTop *File
	project []*File // the project and local top-level files, in that order
}

// Discover reads the graph a request covers: the top-level files, every file their
// include_if_found entries reach, and the user's config.d fragments, without merging any of them.
func Discover(req Request) (*Graph, error) {
	g := &Graph{Request: req}
	seen := map[string]*File{}
	if req.UserConfigDir != "" {
		top, err := g.topLevel(req.UserConfigDir, paths.UserConfigTOMLName,
			[]string{paths.UserConfigLegacyJSONCName, paths.UserConfigLegacyJSONName}, ScopeUser)
		if err != nil {
			return nil, err
		}
		if top != nil {
			g.userTop = top
			if err := g.walk(top, seen); err != nil {
				return nil, err
			}
			if top.doc.Format == configdoc.TOML {
				if err := g.fragments(req.UserConfigDir, seen); err != nil {
					return nil, err
				}
			}
		}
	}
	if req.Workspace != "" {
		for _, names := range [][]string{
			{paths.WorkspaceConfigTOMLName, paths.WorkspaceConfigLegacyJSONCName, paths.WorkspaceConfigLegacyJSONName},
			{paths.WorkspaceLocalConfigTOMLName, paths.WorkspaceLocalConfigLegacyJSONCName, paths.WorkspaceLocalConfigLegacyJSONName},
		} {
			top, err := g.topLevel(req.Workspace, names[0], names[1:], ScopeProject)
			if err != nil {
				return nil, err
			}
			if top == nil {
				continue
			}
			g.project = append(g.project, top)
			if err := g.walk(top, seen); err != nil {
				return nil, err
			}
		}
	}
	for _, f := range g.Files {
		if f.Role == RoleRead {
			for _, edge := range f.includes {
				if edge.child != nil && edge.child.Role == RoleConvert {
					f.Role = RoleRespell
					break
				}
			}
		}
	}
	return g, nil
}

// topLevel finds the one top-level file among a TOML name and its old names, refusing two.
func (g *Graph) topLevel(dir, tomlName string, oldNames []string, scope Scope) (*File, error) {
	var found []string
	for _, name := range append([]string{tomlName}, oldNames...) {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			found = append(found, name)
		}
	}
	if len(found) == 0 {
		return nil, nil
	}
	if len(found) > 1 {
		return nil, fmt.Errorf("%s and %s are both here; config formats are never merged. Keep one active (move the other aside, for example to <name>.backup), then run `yolo config migrate` again",
			filepath.Join(dir, found[0]), filepath.Join(dir, found[1]))
	}
	f, err := g.read(filepath.Join(dir, found[0]), "", scope)
	if err != nil {
		return nil, err
	}
	f.topName = found[0]
	return f, nil
}

func (g *Graph) read(logical, via string, scope Scope) (*File, error) {
	physical, err := filepath.EvalSymlinks(logical)
	if err != nil {
		return nil, fmt.Errorf("resolve %s: %w", logical, err)
	}
	physical, err = filepath.Abs(physical)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(physical)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", logical, err)
	}
	format := configdoc.JSONC
	role := RoleConvert
	if strings.EqualFold(filepath.Ext(physical), ".toml") || strings.EqualFold(filepath.Ext(logical), ".toml") {
		format, role = configdoc.TOML, RoleRead
	}
	doc, err := configdoc.Parse(data, format)
	if err != nil {
		return nil, fmt.Errorf("%s: %w; fix it, then run `yolo config migrate` again", logical, err)
	}
	f := &File{Logical: logical, Physical: physical, Via: via, Scope: scope, Role: role, Bytes: data, doc: doc}
	g.Files = append(g.Files, f)
	return f, nil
}

// walk follows a file's include_if_found entries the way the reader does: each relative entry
// joined to the directory of the file's physical path, through symlinks.
func (g *Graph) walk(f *File, seen map[string]*File) error {
	if prior, ok := seen[f.Physical]; ok && prior != f {
		return nil
	}
	seen[f.Physical] = f
	value, ok := f.doc.Values.Get("include_if_found")
	if !ok {
		return nil
	}
	list, ok := value.([]any)
	if !ok {
		return nil
	}
	dir := f.includeBase()
	for i, item := range list {
		entry, ok := item.(string)
		if !ok || entry == "" || strings.HasPrefix(entry, "/") || strings.HasPrefix(entry, "~") {
			continue
		}
		joined := filepath.Join(dir, entry)
		edge := includeEdge{index: i, entry: entry}
		if _, err := os.Stat(joined); err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("%s (listed in %s): %w", joined, f.Logical, err)
			}
			g.Unreachable = append(g.Unreachable, Missing{Path: joined, ListedIn: f.Logical})
			f.includes = append(f.includes, edge)
			continue
		}
		physical, err := filepath.EvalSymlinks(joined)
		if err == nil {
			physical, _ = filepath.Abs(physical)
		}
		if prior, ok := seen[physical]; ok {
			if prior.Scope != f.Scope {
				return fmt.Errorf("%s is included by both the user and the project config; a migration converts it once for one of them. Give each its own copy, then run `yolo config migrate` again", joined)
			}
			edge.child = prior
			f.includes = append(f.includes, edge)
			continue
		}
		child, err := g.read(joined, fmt.Sprintf("include_if_found[%d] in %s", i, f.Logical), f.Scope)
		if err != nil {
			return err
		}
		edge.child = child
		f.includes = append(f.includes, edge)
		if err := g.walk(child, seen); err != nil {
			return err
		}
	}
	return nil
}

// includeBase is the directory the reader joins this file's include entries to: a top-level
// file's or fragment's own directory as named, an include's physical directory (the reader
// resolves an include's path before reading it).
func (f *File) includeBase() string {
	if f.Via == "" {
		return filepath.Dir(f.Logical)
	}
	return filepath.Dir(f.Physical)
}

// fragments adds the user's config.d/*.toml fragments, which a TOML user file merges.
func (g *Graph) fragments(dir string, seen map[string]*File) error {
	entries, err := os.ReadDir(filepath.Join(dir, "config.d"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	var names []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".toml") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		logical := filepath.Join(dir, "config.d", name)
		if info, err := os.Stat(logical); err != nil || !info.Mode().IsRegular() {
			continue
		}
		f, err := g.read(logical, "", ScopeUser)
		if err != nil {
			return err
		}
		f.fragment = true
		if err := g.walk(f, seen); err != nil {
			return err
		}
	}
	return nil
}

// Old is every file the migration converts, in discovery order.
func (g *Graph) Old() []*File {
	var out []*File
	for _, f := range g.Files {
		if f.Role == RoleConvert {
			out = append(out, f)
		}
	}
	return out
}

// NewName is the name the file has once converted, in the same directory: config.jsonc →
// config.toml.
func (f *File) NewName() string { return twinName(filepath.Base(f.Logical)) }

// IsSymlink is whether the name the file is reached by is itself a symlink, which a conversion
// renames to NewName (RC-D41).
func (f *File) IsSymlink() bool {
	info, err := os.Lstat(f.Logical)
	return err == nil && info.Mode()&os.ModeSymlink != 0
}

// twinName is a file name's TOML twin: config.jsonc → config.toml.
func twinName(name string) string {
	for _, ext := range []string{".jsonc", ".json"} {
		if strings.HasSuffix(strings.ToLower(name), ext) {
			return name[:len(name)-len(ext)] + ".toml"
		}
	}
	return name + ".toml"
}

func twinPath(path string) string {
	return filepath.Join(filepath.Dir(path), twinName(filepath.Base(path)))
}
