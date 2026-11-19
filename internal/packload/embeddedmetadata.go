package packload

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
	"github.com/mschulkind-oss/yolo-jail/internal/mcpsignin"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packstage"
)

type embeddedMetadataBinding struct {
	fsys           fs.FS
	selectedFS     fs.FS
	sourceRoot     string
	sourceIdentity string
	loadedIdentity string
	loadedDecl     *packdecl.Manifest
	selectedName   string
	selectedRoot   string
	only           []string
	exclude        []string
}

// ReadSelectedPackFS constructs a read-only selected pack view from one immutable, symlink-free
// filesystem (the embedded source). Path-backed packs remain on LoadDir and its whole-tree checks.
// The manifest, conventional content, filters, root identity, and nested declarations all share
// the same filtered reader; no disk tree is created or consulted.
func ReadSelectedPackFS(fsys fs.FS, sourceRoot, diagnosticRoot, name string, only, exclude []string) (*Pack, []string, error) {
	if fsys == nil || sourceRoot == "" || diagnosticRoot == "" {
		return nil, nil, fmt.Errorf("selected pack FS requires source, source root, and diagnostic root")
	}
	source, err := fs.Sub(fsys, sourceRoot)
	if err != nil {
		return nil, nil, fmt.Errorf("pack %s embedded source %s: %w", name, sourceRoot, err)
	}
	selectedFS := filteredSourceFS{source: source, only: append([]string(nil), only...), exclude: append([]string(nil), exclude...)}
	p, problems, err := readSelectedPackView(fsys, sourceRoot, diagnosticRoot, name, selectedFS, fsPackTreeOps(selectedFS), false, only, exclude)
	if p != nil {
		p.projectionSource = selectedFS
	}
	return p, problems, err
}

// ReadSelectedPackPath reads a disk-backed source without staging it. The whole source is checked
// before its exact filter view is constructed, matching packstage's no-escape policy without a copy.
func ReadSelectedPackPath(root, name string, only, exclude []string, followLocalLinks bool) (*Pack, []string, error) {
	physicalRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, nil, err
	}
	result, err := packstage.Check(packstage.Spec{
		Root: physicalRoot, Only: only, Exclude: exclude, FollowSymlinks: followLocalLinks,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("pack %s source check: %w", name, err)
	}
	source := os.DirFS(physicalRoot)
	selectedFS := newSelectedPathFS(physicalRoot, result.Staged)
	return readSelectedPackView(source, ".", physicalRoot, name, selectedFS, fsPackTreeOps(selectedFS), false, only, exclude)
}

func readSelectedPackView(fsys fs.FS, sourceRoot, diagnosticRoot, name string, selectedFS fs.FS, ops packTreeOps, followLocalLinks bool, only, exclude []string) (*Pack, []string, error) {
	data, manifestName, problem := discoverManifestWith(".",
		func(name string) (fs.FileInfo, error) { return fs.Stat(selectedFS, name) },
		func(name string) ([]byte, error) { return fs.ReadFile(selectedFS, name) },
		func(a, b string) string { return path.Join(a, b) })
	if problem != "" {
		return nil, nil, fmt.Errorf("pack %s: %s", name, problem)
	}
	decl, problems, skewNotes := decodeManifest(data, manifestName, name, true)
	if name == "" {
		name = decl.Name
	}
	if name == "" {
		name = filepath.Base(diagnosticRoot)
	}
	var homeFiles []packdecl.Contribution
	var homeProblems []string
	if followLocalLinks {
		homeFiles, homeProblems = discoverLocalHomeFiles(diagnosticRoot, only, exclude)
	} else {
		homeFiles, homeProblems = discoverHomeFilesWithOps(".", ops, only, exclude)
	}
	overlayFiles, overlayProblems := discoverOverlayFilesWithOps(".", followLocalLinks, ops, only, exclude)
	description := ""
	if manifestName == "" {
		if readme, err := fs.ReadFile(selectedFS, "README.md"); err == nil {
			description = firstLineDescription(readme)
		}
	}
	_, conventionProblems := finishPackMetadata(name, decl, manifestName, homeFiles, homeProblems, overlayFiles,
		overlayProblems, description, reservedBriefingFilesFS(selectedFS, name))
	problems = append(problems, conventionProblems...)
	problems = append(problems, validateSignInBundles(name, decl, mcpsignin.FSReader{FS: selectedFS})...)
	selected := &Pack{
		Name: name, OverlayFiles: overlayFiles, Root: diagnosticRoot, Decl: decl, SkewNotes: skewNotes,
	}
	identity, err := declarationIdentity(decl)
	if err != nil {
		return nil, nil, fmt.Errorf("pack %s declaration identity: %w", name, err)
	}
	selected.embeddedMetadata = &embeddedMetadataBinding{
		fsys: fsys, selectedFS: selectedFS, sourceRoot: sourceRoot,
		sourceIdentity: identity, loadedIdentity: identity, loadedDecl: decl, selectedName: name, selectedRoot: diagnosticRoot,
		only: append([]string(nil), only...), exclude: append([]string(nil), exclude...),
	}
	return selected, problems, nil
}

func declarationIdentity(decl *packdecl.Manifest) (string, error) {
	raw, err := json.Marshal(struct {
		Declaration *packdecl.Manifest
		Bundles     []packdecl.MCPSignInBundle
		Formats     []string
		HeaderForm  string
		Paste       bool
	}{decl, decl.SignInBundles(), decl.MCPSignInFormats, decl.MCPHeaderForm, decl.MCPSignInPaste})
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func preserveEmbeddedMetadataBinding(source, clone *Pack) {
	binding := source.embeddedMetadata
	if binding == nil || source.Decl == nil || clone.Decl == nil || binding.loadedDecl != source.Decl ||
		binding.selectedName != source.Name || clone.Name != source.Name || binding.selectedRoot != source.Root || binding.sourceIdentity == "" {
		clone.embeddedMetadata = nil
		return
	}
	sourceIdentity, err := declarationIdentity(source.Decl)
	if err != nil || sourceIdentity != binding.loadedIdentity {
		clone.embeddedMetadata = nil
		return
	}
	cloneIdentity, err := declarationIdentity(clone.Decl)
	if err != nil || cloneIdentity != sourceIdentity {
		clone.embeddedMetadata = nil
		return
	}
	copied := *binding
	copied.loadedDecl = clone.Decl
	copied.loadedIdentity = cloneIdentity
	clone.embeddedMetadata = &copied
}

func refreshEmbeddedMetadataBinding(p *Pack) {
	if p == nil || p.embeddedMetadata == nil {
		return
	}
	binding := p.embeddedMetadata
	if p.Decl == nil || binding.loadedDecl != p.Decl || binding.selectedName != p.Name || binding.selectedRoot != p.Root || binding.sourceIdentity == "" {
		p.embeddedMetadata = nil
		return
	}
	identity, err := declarationIdentity(p.Decl)
	if err != nil {
		p.embeddedMetadata = nil
		return
	}
	binding.loadedIdentity = identity
}

// ReadableLoopholeModules reads from the selected pack's source-bound view when present. Ordinary
// host readers without such a view remain path-backed through the actual Pack.Root.
func ReadableLoopholeModules(p *Pack) ([]LoopholeModule, []string, []string, error) {
	if p == nil {
		return nil, nil, nil, fmt.Errorf("readable loophole declarations require a loaded pack")
	}
	binding := p.embeddedMetadata
	if binding == nil {
		mods, refusals, warnings := p.LoopholeModules()
		return mods, refusals, warnings, nil
	}
	identity, err := declarationIdentity(p.Decl)
	if err != nil || binding.fsys == nil || binding.selectedFS == nil || binding.loadedDecl != p.Decl || binding.selectedName != p.Name || binding.selectedRoot != p.Root ||
		binding.sourceIdentity == "" || identity != binding.loadedIdentity {
		return nil, nil, nil, fmt.Errorf("pack %s selected metadata no longer matches its parsed declaration and root", p.Name)
	}
	root := filepath.Clean(p.Root)
	mods, refusals, warnings := p.loopholeModules(root,
		func(rel string) (string, bool) {
			modulePath := filepath.ToSlash(rel)
			fi, err := fs.Stat(binding.selectedFS, modulePath)
			return modulePath, err == nil && fi.IsDir()
		},
		func(modulePath, rel string) (*loopholedecl.Manifest, error) {
			diagnosticDir := filepath.Join(p.Root, filepath.FromSlash(rel))
			decl, _, err := loopholedecl.LoadDirTolerantFS(binding.selectedFS, modulePath, diagnosticDir)
			return decl, err
		})
	return mods, refusals, warnings, nil
}

func reservedBriefingFilesFS(fsys fs.FS, name string) []string {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil
	}
	for _, entry := range entries {
		if entry.Name() != packdecl.DefaultBriefingDir || !entry.IsDir() {
			continue
		}
		if info, err := fs.Stat(fsys, entry.Name()); err != nil || !info.IsDir() {
			return nil
		}
		briefingEntries, err := fs.ReadDir(fsys, entry.Name())
		if err != nil {
			return nil
		}
		return reservedBriefingFileProblems(name, briefingEntries)
	}
	return nil
}

// filteredSourceFS presents exactly the files a packstage filter keeps without copying them.
type filteredSourceFS struct {
	source  fs.FS
	only    []string
	exclude []string
}

func (f filteredSourceFS) selected(name string) (bool, error) {
	if name == "." {
		return true, nil
	}
	info, err := fs.Stat(f.source, name)
	if err != nil {
		return false, err
	}
	if !info.IsDir() {
		return packstage.Matches(name, f.only, f.exclude), nil
	}
	entries, err := fs.ReadDir(f.source, name)
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		if entry.Name() == ".git" && entry.IsDir() {
			continue
		}
		child := path.Join(name, entry.Name())
		selected, err := f.selected(child)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return false, err
		}
		if selected {
			return true, nil
		}
	}
	return false, fs.ErrNotExist
}

func (f filteredSourceFS) Open(name string) (fs.File, error) {
	selected, err := f.selected(name)
	if err != nil || !selected {
		if err == nil {
			err = fs.ErrNotExist
		}
		return nil, &fs.PathError{Op: "open", Path: name, Err: err}
	}
	file, err := f.source.Open(name)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil || !info.IsDir() {
		return file, err
	}
	entries, err := f.ReadDir(name)
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	return &filteredDir{File: file, entries: entries}, nil
}

func (f filteredSourceFS) Stat(name string) (fs.FileInfo, error) {
	selected, err := f.selected(name)
	if err != nil || !selected {
		if err == nil {
			err = fs.ErrNotExist
		}
		return nil, &fs.PathError{Op: "stat", Path: name, Err: err}
	}
	return fs.Stat(f.source, name)
}

func (f filteredSourceFS) ReadDir(name string) ([]fs.DirEntry, error) {
	selected, err := f.selected(name)
	if err != nil || !selected {
		if err == nil {
			err = fs.ErrNotExist
		}
		return nil, &fs.PathError{Op: "readdir", Path: name, Err: err}
	}
	entries, err := fs.ReadDir(f.source, name)
	if err != nil {
		return nil, err
	}
	out := make([]fs.DirEntry, 0, len(entries))
	for _, entry := range entries {
		if entry.Name() == ".git" && entry.IsDir() {
			continue
		}
		child := path.Join(name, entry.Name())
		keep, err := f.selected(child)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
		if keep {
			out = append(out, entry)
		}
	}
	return out, nil
}

type filteredDir struct {
	fs.File
	entries []fs.DirEntry
	index   int
}

func (d *filteredDir) ReadDir(n int) ([]fs.DirEntry, error) {
	if d.index >= len(d.entries) && n > 0 {
		return nil, io.EOF
	}
	end := len(d.entries)
	if n > 0 && d.index+n < end {
		end = d.index + n
	}
	out := append([]fs.DirEntry(nil), d.entries[d.index:end]...)
	d.index = end
	return out, nil
}
