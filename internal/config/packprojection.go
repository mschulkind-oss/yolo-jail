package config

// Architecture and invariants: docs/reference/toml-scoped-pack-imports/README.md

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
)

func validateScopedPackEntry(entry PackEntry) error {
	if entry.Subdir != "" {
		if err := packsrc.ScopedPath(entry.Subdir, false); err != nil {
			return err
		}
	}
	if entry.Skills != nil {
		if err := entry.Skills.Validate(); err != nil {
			return fmt.Errorf("%s: %w", entry.ConfigOrigin, err)
		}
	}
	if entry.Embedded() && (entry.Subdir != "" || entry.Skills != nil && entry.Skills.Dir != "") {
		return fmt.Errorf("shipped packs allow typed selectors only; remove subdir and skills.dir")
	}
	if entry.Implicit && (entry.Subdir != "" || entry.Skills != nil) {
		return fmt.Errorf("the conventional local pack cannot be scoped; select a local/name pack explicitly")
	}
	if entry.Skills != nil && (len(entry.Only) > 0 || len(entry.Exclude) > 0) {
		return fmt.Errorf("modern selection cannot use legacy file filters; remove only/exclude")
	}
	return nil
}

// confinedPackRoot resolves modern roots before whole-pack checks/discovery.
func confinedPackRoot(base, rel string, local bool) (string, error) {
	if rel == "" || rel == "." {
		return base, nil
	}
	root := filepath.Join(base, filepath.FromSlash(rel))
	if local {
		physicalBase, err := filepath.EvalSymlinks(base)
		if err != nil {
			return "", err
		}
		physical, err := filepath.EvalSymlinks(root)
		if err != nil {
			return "", err
		}
		inside, err := filepath.Rel(physicalBase, physical)
		if err != nil || !filepath.IsLocal(inside) {
			return "", fmt.Errorf("root %q escapes source root %s; choose a directory inside the source", rel, base)
		}
		info, err := os.Stat(physical)
		if err != nil || !info.IsDir() {
			return "", fmt.Errorf("root %q is not a directory; repair subdir/skills.dir", rel)
		}
		return physical, nil
	}
	current := base
	for _, component := range strings.Split(rel, "/") {
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if err != nil || !info.IsDir() {
			return "", fmt.Errorf("git root %q requires real directory ancestry; repair subdir/skills.dir", rel)
		}
	}
	return root, nil
}

func projectPackSkills(entry PackEntry, spec ResolvePackSpec, root, commit string, source fs.FS) (ResolvedPack, error) {
	name := entry.Name
	if entry.EmbeddedName != "" {
		name = entry.EmbeddedName
	}
	original := &packload.Pack{Name: name, Root: root, Official: entry.Embedded(), Local: entry.IsLocal()}
	if entry.Skills.Dir == "" {
		p, problems, err := packload.ReadSkillsProjectionSource(source, root, name)
		if err != nil {
			return ResolvedPack{}, err
		}
		if len(problems) != 0 {
			return ResolvedPack{}, fmt.Errorf("%s: invalid typed declaration: %s; repair the pack manifest", entry.ConfigOrigin, strings.Join(problems, "; "))
		}
		original = p
		original.Official, original.Local = entry.Embedded(), entry.IsLocal()
	}
	rootName := path.Base(entry.Name)
	if entry.Subdir != "" {
		rootName = path.Base(entry.Subdir)
	}
	p, err := packload.ProjectSkills(original, source, packload.SkillProjectionSpec{Selection: *entry.Skills, Subdir: entry.Subdir, Origin: entry.ConfigOrigin, ImportIdentity: entry.ImportIdentity(), Source: entry.Source, Commit: commit, RootName: rootName, Namespace: entry.Namespace, NamespaceOwner: entry.NamespaceOwner})
	if err != nil {
		if commit != "" {
			return ResolvedPack{}, fmt.Errorf("scoped source resolved to commit %s: %w; edit the selectors or select a known-good ref with yolo config set", commit, err)
		}
		return ResolvedPack{}, err
	}
	for _, row := range p.Projection.Rows {
		if row.Sourced || entry.Embedded() {
			continue
		}
		if _, err := confinedPackRoot(root, row.Path, entry.IsLocal()); err != nil {
			return ResolvedPack{}, err
		}
	}
	p.Namespace, p.NamespaceOwner, p.Landing = entry.Namespace, entry.NamespaceOwner, entry.Landing
	p.ImportIdentity = entry.ImportIdentity()
	p.KeepNamesStep, p.AcceptNamesStep, p.BareStep = entry.KeepNamesStep, entry.AcceptNamesStep, entry.BareStep
	if spec.Dest != "" {
		if err := materializeProjectedCarrier(p, entry, spec.Dest); err != nil {
			return ResolvedPack{}, err
		}
		finalRoot := spec.Dest + "-" + p.Projection.Digest[:12]
		if err := os.Rename(spec.Dest, finalRoot); err != nil {
			return ResolvedPack{}, err
		}
		p.Root = finalRoot
		if p.Projection.CopiedContentDigest != "" {
			if err := packload.BindProjectedLocalCarrier(p, filepath.Join(finalRoot, packload.ScopedCarrierDir)); err != nil {
				return ResolvedPack{}, err
			}
		}
	}
	return ResolvedPack{Pack: p}, nil
}
