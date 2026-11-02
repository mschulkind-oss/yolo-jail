package config

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// explicitEditProfileNamespaces reads the same all-shipped-plus-configured CLI universe as
// UseProfileCLINames without materializing embedded packs or rereading the user's on-disk config.
// The invocation owns the embedded source and every configured declaration it resolves. A source
// it cannot prove makes profile validation fail closed rather than validating an unknown namespace.
func explicitEditProfileNamespaces(ctx *editValidationContext) (map[string]bool, map[string]bool, error) {
	if ctx == nil || ctx.rawUser == nil {
		return nil, nil, fmt.Errorf("planned user config is unavailable")
	}
	fsys := ctx.embeddedFS
	if fsys == nil {
		return nil, nil, fmt.Errorf("embedded pack source is unavailable; rerun with a complete yolo build")
	}
	if ctx.embeddedNamesErr != nil {
		return nil, nil, fmt.Errorf("enumerate embedded pack source: %w; rerun with a complete yolo build", ctx.embeddedNamesErr)
	}
	names := ctx.embeddedNames
	clis := make(map[string]bool)
	for _, name := range names {
		info, err := fs.Stat(fsys, name)
		if err != nil || !info.IsDir() {
			return nil, nil, fmt.Errorf("embedded pack %s is not present in the captured immutable source; rerun with a complete yolo build", name)
		}
		root := filepath.Join(string(filepath.Separator), "embedded", name)
		pack, problems, err := packload.ReadSelectedPackFS(fsys, name, root, name, nil, nil)
		if err != nil || len(problems) != 0 || pack == nil || pack.Decl == nil {
			return nil, nil, fmt.Errorf("read embedded pack %s from the captured source: %v %v; rerun with a complete yolo build", name, err, problems)
		}
		addProfilePackCLIs(clis, pack)
	}

	entries, problems := lowerReadablePackEntriesFromEmbeddedNames(ctx.rawUser, ctx.userOrigins, ctx.embeddedNames)
	if len(problems) != 0 {
		return nil, nil, fmt.Errorf("read planned configured pack entries: %v; repair config.packs before validating profile names", problems)
	}
	entries = appendImplicitPacks(entries)
	resolved := make(map[string]*packload.Pack, len(ctx.packs))
	for _, pack := range ctx.packs {
		if pack != nil {
			resolved[pack.Name] = pack
		}
	}
	for _, entry := range entries {
		if entry.Embedded() {
			continue
		}
		pack := resolved[entry.Name]
		if pack == nil {
			root, err := ctx.configuredPackRoot(entry)
			if err != nil {
				if errors.Is(err, errPackUnverifiableInJail) {
					// The namespace is unknowable from this jail; the caller steps its half
					// aside, as validateProfile does when UseProfileCLINames cannot answer.
					return nil, nil, err
				}
				return nil, nil, fmt.Errorf("resolve configured pack %s for profile namespace: %w; repair the pack source and retry", entry.Name, err)
			}
			packRead, problems, err := packload.ReadSelectedPackPath(root, entry.Name, entry.Only, entry.Exclude, entry.IsLocal())
			if err != nil {
				return nil, nil, fmt.Errorf("read configured pack %s for profile namespace: %w; repair the pack source and retry", entry.Name, err)
			}
			if len(problems) != 0 || packRead == nil || packRead.Decl == nil {
				return nil, nil, fmt.Errorf("read configured pack %s for profile namespace: declaration problems %v; repair the pack source and retry", entry.Name, problems)
			}
			pack = packRead
			resolved[entry.Name] = pack
		}
		addProfilePackCLIs(clis, pack)
	}
	return clis, profileSetCapableCLIs(clis), nil
}

func addProfilePackCLIs(namespace map[string]bool, pack *packload.Pack) {
	for _, bin := range pack.InstallBins() {
		namespace[bin] = pack.Decl.HoldsProviderSets(bin)
	}
}

func profileSetCapableCLIs(namespace map[string]bool) map[string]bool {
	capable := make(map[string]bool)
	for name, holds := range namespace {
		if holds {
			capable[name] = true
		}
	}
	return capable
}

func explicitEditProfileNames(names map[string]bool) []string {
	out := make([]string, 0, len(names))
	for name := range names {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
