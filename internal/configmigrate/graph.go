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
	// Layer is one old-format file a migration converts on its own, the file a launch reads with
	// `--user-layer` (RC-D46, amended 2026-11-01): it and what its include_if_found entries reach,
	// nothing else. Set, no user or workspace file is converted. The user config is read as the
	// base the layer applies over, from UserConfigDir, or in a jail (UserConfigDir "") from the
	// jail's own; UserConfigDir and Workspace then only place the journal, as they do without it.
	Layer string
	// Only converts the Layer file alone (RC-D51, `yolo config migrate --only FILE`): the old
	// files it includes stay as they are, and the converted file keeps naming them, since a TOML
	// file may include an old one (RC-D50).
	Only bool
}

// baseUserDir is the user config directory a layer applies over.
func (r Request) baseUserDir() string {
	if r.UserConfigDir != "" {
		return r.UserConfigDir
	}
	return filepath.Dir(paths.UserConfigPath())
}

// baseUserTop is a path in baseUserDir whose directory names the user config: the reader selects
// the file there (config.toml, else config.jsonc or config.json).
func (r Request) baseUserTop() string {
	return filepath.Join(r.baseUserDir(), paths.UserConfigLegacyJSONCName)
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
	layer   *File   // the --user-layer file a Request.Layer migration converts
}

// discoverLayer reads the graph of a Request.Layer migration: the named file and every file its
// include_if_found entries reach, by the rules the user config's graph is read by. A file the
// user config or the workspace's files also reach is refused: converted on its own, it would
// change what they read.
func (g *Graph) discoverLayer() error {
	path := g.Request.Layer
	switch strings.ToLower(filepath.Ext(path)) {
	case ".jsonc", ".json":
	case ".toml":
		return fmt.Errorf("%s is already TOML, so there is nothing to convert; launch with `--user-layer %s`", path, path)
	default:
		return fmt.Errorf("%s is not an old-format config file (.jsonc or .json); name the file a launch reads with --user-layer", path)
	}
	if _, err := os.Lstat(path); err != nil {
		if twin := twinPath(path); errors.Is(err, os.ErrNotExist) && lexists(twin) {
			return fmt.Errorf("%s is not there, and %s is: it looks converted already; launch with `--user-layer %s`", path, twin, twin)
		}
		return fmt.Errorf("%s: %w; name the file a launch reads with --user-layer", path, err)
	}
	reached := map[string]string{}
	if base, err := Discover(Request{UserConfigDir: g.Request.baseUserDir(), Workspace: g.Request.Workspace}); err == nil {
		for _, f := range base.Files {
			reached[f.Physical] = f.Logical
		}
	}
	seen := map[string]*File{}
	top, err := g.read(path, "", ScopeUser)
	if err != nil {
		return err
	}
	top.topName = filepath.Base(path)
	g.layer = top
	if err := g.walk(top, seen); err != nil {
		return err
	}
	if g.Request.Only {
		// What it includes is read, never written: each stays an old file, and the converted
		// file names it as the old one did (RC-D51).
		for _, f := range g.Files {
			if f != top {
				f.Role = RoleRead
			}
		}
	}
	for _, f := range g.Files {
		if g.Request.Only && f != top {
			continue // read, never written, so what else reads it reads the same
		}
		if by, ok := reached[f.Physical]; ok {
			return fmt.Errorf("%s is read by your user config or this workspace's config too (as %s), so converting it on its own "+
				"would change what they read; nothing was changed. Run `yolo config migrate` with no file to convert it with them", f.Logical, by)
		}
	}
	return nil
}

// Discover reads the graph a request covers: the top-level files, every file their
// include_if_found entries reach, and the user's config.d fragments, without merging any of them.
func Discover(req Request) (*Graph, error) {
	g := &Graph{Request: req}
	if req.Layer != "" {
		if err := g.discoverLayer(); err != nil {
			return nil, err
		}
		g.markRespelled()
		return g, nil
	}
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
	g.markRespelled()
	return g, nil
}

// markRespelled marks each TOML file that includes an old file: its include entry is respelled.
func (g *Graph) markRespelled() {
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

// IsSymlink is whether the name the file is reached by is itself a symlink. A conversion never
// writes through it: the link moves to its backup name and the converted file is a regular file
// beside it (RC-D41).
func (f *File) IsSymlink() bool {
	info, err := os.Lstat(f.Logical)
	return err == nil && info.Mode()&os.ModeSymlink != 0
}

// oldPath is the name a conversion moves to its backup: the physical file, or for a linked name
// the link itself (its directory resolved), never the file it points at.
func (f *File) oldPath() string {
	if f.IsSymlink() {
		return filepath.Join(resolvedDir(f.Logical), filepath.Base(f.Logical))
	}
	return f.Physical
}

// newPath is where the file is once the migration is done: a converted file's twin beside
// oldPath, a respelled file at oldPath, and any other file where it is.
func (f *File) newPath() string {
	switch f.Role {
	case RoleConvert:
		return twinPath(f.oldPath())
	case RoleRespell:
		return f.oldPath()
	}
	return f.Physical
}

// LinkedFile is a config file reached through a symlink that a migration converts or respells:
// Link is the user's link, moved to Backup with its text unchanged; Target is the file it points
// at, never changed; New is the regular file written in its place (RC-D41).
type LinkedFile struct{ Link, Backup, Target, New string }

// Linked lists the files the migration writes whose names are symlinks, in discovery order.
func (g *Graph) Linked() []LinkedFile {
	var out []LinkedFile
	for _, f := range g.Files {
		if (f.Role == RoleConvert || f.Role == RoleRespell) && f.IsSymlink() {
			out = append(out, LinkedFile{Link: f.oldPath(), Backup: f.oldPath() + ".pre-migrate", Target: f.Physical, New: f.newPath()})
		}
	}
	return out
}

// LinkWarning is what is said before a migration asks, for files reached through symlinks: each
// link and the file it points at, which is not changed, and that a dotfiles manager needs the new
// file copied in by hand. "" without links.
func LinkWarning(links []LinkedFile) string {
	if len(links) == 0 {
		return ""
	}
	var b strings.Builder
	for _, l := range links {
		fmt.Fprintf(&b, "%s is a link to %s. The converted file is written as a regular file at %s, and %s is not changed.\n",
			l.Link, l.Target, l.New, l.Target)
	}
	if len(links) == 1 {
		b.WriteString("If a dotfiles manager keeps this file, copy the new file into it yourself.")
	} else {
		b.WriteString("If a dotfiles manager keeps these files, copy the new files into it yourself.")
	}
	return b.String()
}

// LinkReport is the closing report's line for one moved link.
func LinkReport(l LinkedFile) string {
	return fmt.Sprintf("%s was a link to %s and is kept as %s. The converted file is the regular file %s, and %s was not changed; "+
		"if a dotfiles manager keeps it, copy the new file into it yourself.", l.Link, l.Target, l.Backup, l.New, l.Target)
}

// resolvedDir is path's directory with every symlink in it resolved.
func resolvedDir(path string) string {
	dir, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		return filepath.Dir(path)
	}
	abs, _ := filepath.Abs(dir)
	return abs
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
