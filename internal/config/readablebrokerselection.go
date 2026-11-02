package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

func validateReadableProfileSelection(value any) error {
	if value == nil {
		return nil
	}
	if selections, ok := asMap(value); ok {
		for _, receiver := range selections.Keys() {
			selected, _ := selections.Get(receiver)
			if selected == nil {
				continue
			}
			if _, problem := profileSetOf(selected); problem != "" {
				return fmt.Errorf("config.%s.%s: %s", ProfileKey, receiver, problem)
			}
		}
		return nil
	}
	if _, problem := profileSetOf(value); problem != "" {
		return fmt.Errorf("config.%s: %s", ProfileKey, problem)
	}
	return nil
}

// readableBrokerSelection resolves one strict user snapshot to selected broker declarations.
// Filtered roots stay leased through the closure and declaration reads; the returned metadata is detached.
func readableBrokerSelection(user *jsonx.OrderedMap, sources *Sources) ([]string, []ReadableBrokerDeclaration, []ReadablePackDeclaration, error) {
	entries, problems := lowerReadablePackEntries(user, sources)
	if len(problems) != 0 {
		return nil, nil, nil, fmt.Errorf("read selected user pack entries: %v", problems)
	}
	entries = appendImplicitPacks(entries)

	var temporaryRoots []string
	defer func() {
		for _, root := range temporaryRoots {
			_ = os.RemoveAll(root)
		}
	}()
	resolve := func(entry PackEntry) (*packload.Pack, error) {
		spec := ResolvePackSpec{ReadOnlyStore: true}
		if len(entry.Only) > 0 || len(entry.Exclude) > 0 {
			root, err := os.MkdirTemp("", "yolo-readable-broker-pack-")
			if err != nil {
				return nil, err
			}
			temporaryRoots = append(temporaryRoots, root)
			spec.Dest = filepath.Join(root, entry.Slug())
		}
		resolved, err := ResolvePack(entry, spec)
		if err != nil {
			return nil, err
		}
		if resolved.StagedFrom != "" {
			return nil, fmt.Errorf("pack %s only resolves through staged tree %s, whose source and filters cannot be proved for this selected entry", entry.Name, resolved.StagedFrom)
		}
		if resolved.Pack == nil || len(resolved.Problems) != 0 {
			return nil, fmt.Errorf("pack %s declaration problems: %v", entry.Name, resolved.Problems)
		}
		return resolved.Pack, nil
	}

	var profileErr, profilesErr, viaErr error
	var userProfiles map[string]packload.UserProfile
	var viaProfiles map[string]packload.UserProfile
	if _, active := user.Get("active_provider"); !active {
		if value, present := user.Get("profiles"); present && value != nil {
			var problems []string
			userProfiles, problems = checkProfiles(value)
			if len(problems) != 0 {
				profilesErr = fmt.Errorf("user profiles: %v", problems)
			}
		}
	}
	selection := packload.Selection{
		UseProfiles: func(selected []*packload.Pack) map[string]string {
			if _, active := user.Get("active_provider"); active {
				profiles, resolved, err := explicitEditViaSelection(user, selected)
				viaProfiles, viaErr = resolved, err
				return profiles
			}
			if raw, present := user.Get(ProfileKey); present {
				if err := validateReadableProfileSelection(raw); err != nil {
					profileErr = err
					return nil
				}
				if _, ok := ProfileSelectionOf(raw); !ok {
					profileErr = fmt.Errorf("config.%s: invalid profile selection", ProfileKey)
					return nil
				}
			}
			return ConfigProfileTable(user, selected)
		},
		UserProfiles: func() (map[string]packload.UserProfile, error) {
			if profileErr != nil {
				return nil, profileErr
			}
			if viaErr != nil {
				return nil, viaErr
			}
			if profilesErr != nil {
				return nil, profilesErr
			}
			if _, active := user.Get("active_provider"); active {
				return clonePackUserProfiles(viaProfiles), nil
			}
			return clonePackUserProfiles(userProfiles), nil
		},
	}
	sel, err := SelectPacks(entries, PackSelectSpec{
		Resolve: resolve,
		Join: func(pack *packload.Pack) (*packload.Pack, error) {
			return resolve(EmbeddedPackEntry(pack.Name))
		},
		FailFast:  true,
		Selection: selection,
	})
	if err != nil {
		return nil, nil, nil, err
	}
	if profileErr != nil {
		return nil, nil, nil, profileErr
	}
	if profilesErr != nil {
		return nil, nil, nil, profilesErr
	}
	if viaErr != nil {
		return nil, nil, nil, viaErr
	}
	if !sel.Complete() {
		return nil, nil, nil, fmt.Errorf("selected user pack declarations are incomplete: %v %v", sel.Unresolved, sel.ClosureErr)
	}

	packs := sel.Packs()
	selected := make([]string, 0, len(packs))
	for _, pack := range packs {
		if pack == nil || pack.Name == "" {
			return nil, nil, nil, fmt.Errorf("selected user pack declaration has no identity")
		}
		selected = append(selected, pack.Name)
	}
	declarations, settings, err := ReadableDeclarationsForPacks(packs)
	if err != nil {
		return nil, nil, nil, err
	}
	return selected, declarations, settings, nil
}
