package config

import (
	"fmt"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/configdoc"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

func checkReadablePacksFromEmbeddedNames(raw any, definitions map[string]packsrc.SourceDefinition, sources *Sources, embeddedNames []string) ([]PackEntry, []string) {
	list, ok := asList(raw)
	if !ok {
		return nil, []string{"config.packs: expected a list of pack handles"}
	}
	shipped := make(map[string]bool)
	for _, name := range embeddedNames {
		shipped[name] = true
	}
	type previousEntry struct {
		index int
		where string
		entry PackEntry
	}
	entries := make([]PackEntry, 0, len(list))
	problems := make([]string, 0)
	seenName := make(map[string][]previousEntry)
	seenLocal := make(map[string][]previousEntry)
	for i, rawEntry := range list {
		item := fmt.Sprintf("config.packs[%d]", i)
		loc := packEntryOrigin(sources, item)
		legacy := !packEntryIsReadable(sources, item)
		var entry PackEntry
		localPath := ""
		if legacy {
			var problem string
			entry, problem = checkPackEntryWithEmbeddedNames(rawEntry, item, embeddedNames)
			if problem != "" {
				problems = append(problems, problem)
				continue
			}
			if entry.IsLocal() {
				if parsed, err := url.Parse(entry.Source); err == nil {
					localPath = parsed.Path
				}
			}
		} else {
			ref, err := packsrc.ParsePackReference(rawEntry, definitions, readableLocalPackRoot(), paths.Home(), item, shipped)
			if err != nil {
				problems = append(problems, item+" ("+loc+"): "+err.Error())
				continue
			}
			entry = PackEntry{
				Subdir: ref.Subdir, Skills: ref.Skills,
				Source: ref.Source, Name: ref.Name, SourceID: ref.SourceID,
				LocalPath: ref.LocalPath, EmbeddedName: ref.EmbeddedName,
				AllowDefaultRef: ref.AllowDefaultRef,
				IsEmbedded:      ref.EmbeddedName != "",
				Namespace:       ref.Namespace, NamespaceOwner: ref.NamespaceOwner, Landing: ref.Landing,
				KeepNamesStep: ref.KeepNamesStep, AcceptNamesStep: ref.AcceptNamesStep, BareStep: ref.BareStep,
			}
			entry.ConfigOrigin = item + " (" + loc + ")"
			localPath = ref.LocalPath
		}
		conflict := false
		for _, previous := range seenName[entry.Name] {
			if entry.Source != previous.entry.Source || entry.AllowDefaultRef != previous.entry.AllowDefaultRef {
				problems = append(problems, fmt.Sprintf("%s (%s): entries sharing handle %q must share the authored ref (also at %s); use one ref", item, loc, entry.Name, previous.where))
				conflict = true
				break
			}
			if legacy || importRootsConflict(entry, previous.entry) {
				if legacy {
					problems = append(problems, fmt.Sprintf("%s: duplicate pack name %q (also at config.packs[%d]) — give one an explicit \"name\"", item, entry.Name, previous.index))
				} else {
					problems = append(problems, fmt.Sprintf("%s (%s): duplicate pack handle %q import roots (also at %s); remove one entry or use different subdir/skills.dir", item, loc, entry.Name, previous.where))
				}
				conflict = true
				break
			}
		}
		if conflict {
			continue
		}
		if localPath != "" {
			identity, identityErr := packsrc.LocalDirectoryIdentity(localPath)
			if identityErr == nil {
				entry.PhysicalPath = identity
				for _, previous := range seenLocal[identity] {
					if legacy || importRootsConflict(entry, previous.entry) {
						problems = append(problems, fmt.Sprintf("%s (%s): duplicate local pack directory %q (also at %s) — list the physical directory once or choose different import roots", item, loc, localPath, previous.where))
						conflict = true
						break
					}
				}
				if conflict {
					continue
				}
				seenLocal[identity] = append(seenLocal[identity], previousEntry{index: i, where: item + " (" + loc + ")", entry: entry})
			}
		}
		seenName[entry.Name] = append(seenName[entry.Name], previousEntry{index: i, where: item + " (" + loc + ")", entry: entry})
		entries = append(entries, entry)
	}
	return entries, problems
}

func importRootsConflict(a, b PackEntry) bool {
	rootA, rootB := a.Subdir, b.Subdir
	if a.PhysicalPath != "" && b.PhysicalPath != "" {
		if actual, err := filepath.EvalSymlinks(filepath.Join(a.PhysicalPath, a.Subdir)); err == nil {
			rootA = actual
		}
		if actual, err := filepath.EvalSymlinks(filepath.Join(b.PhysicalPath, b.Subdir)); err == nil {
			rootB = actual
		}
	}
	if rootA != rootB {
		return false
	}
	if a.Skills == nil || b.Skills == nil {
		return true
	}
	dirA, dirB := a.Skills.Dir, b.Skills.Dir
	if a.PhysicalPath != "" && b.PhysicalPath != "" && dirA != "" && dirB != "" {
		if actual, err := filepath.EvalSymlinks(filepath.Join(rootA, dirA)); err == nil {
			dirA = actual
		}
		if actual, err := filepath.EvalSymlinks(filepath.Join(rootB, dirB)); err == nil {
			dirB = actual
		}
	}
	return dirA == dirB
}

// ReadOnlyUserPackEntries lowers an already-read user snapshot without pack
// resolution, staging or custom-tree reads. It retains EmbeddedName provenance.
func ReadOnlyUserPackEntries(user *jsonx.OrderedMap) ([]PackEntry, []string) {
	return lowerReadablePackEntries(user, nil)
}

func packEntryIsReadable(sources *Sources, item string) bool {
	if sources == nil || sources.root == nil {
		return true
	}
	rest, ok := strings.CutPrefix(item, "config.")
	if !ok {
		return true
	}
	node := sources.root.follow(rest)
	if node == nil {
		return true
	}
	origins := node.origins()
	if len(origins) == 0 || origins[0].file == nil {
		return true
	}
	return origins[0].format() == configdoc.TOML
}

// readableLocalPackRoot is the named local source. It intentionally differs from
// paths.LocalPackDir, the separate conventional content-only pack appended after selection.
func readableLocalPackRoot() string {
	return filepath.Join(filepath.Dir(paths.UserConfigPath()), "packs")
}

func packEntryOrigin(sources *Sources, item string) string {
	if locations := sources.Locations(item); len(locations) > 0 {
		return locations[0]
	}
	return item
}

// PackAddress parses this entry with only the opt-in granted by its authored format-2
// producer. Legacy entries retain the mandatory-ref Parse contract.
func (entry PackEntry) PackAddress() (packsrc.Addr, error) {
	if entry.AllowDefaultRef {
		return packsrc.ParsePackAddress(entry.Source)
	}
	return packsrc.Parse(entry.Source)
}

// RefreshSpec carries the pack's lock address and the authored omitted-ref permission
// to the store. updateDefaultRef is reserved for an explicit update verb.
func (entry PackEntry) RefreshSpec(updateDefaultRef bool) packsrc.RefreshPack {
	return packsrc.RefreshPack{
		Name: entry.Name, Source: entry.Source, AllowDefaultRef: entry.AllowDefaultRef,
		PreserveDefaultPin: !updateDefaultRef && entry.AllowDefaultRef,
	}
}
