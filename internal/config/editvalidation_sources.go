package config

import (
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// embeddedSourceNextStep is the next step for a shipped pack this yolo build could not read: the
// fault is the installed build's, not the user's config.
const embeddedSourceNextStep = "the packs this yolo build ships could not be read — reinstall yolo, then rerun the command"

func (ctx *editValidationContext) selectedPacks() ([]*packload.Pack, error) {
	if ctx.rawUser == nil {
		return nil, fmt.Errorf("planned user config source is missing from explicit edit context")
	}
	if ctx.embeddedNamesErr != nil {
		return nil, fmt.Errorf("enumerate the captured embedded pack source: %w; rerun with a complete yolo build", ctx.embeddedNamesErr)
	}
	entries, problems := lowerReadablePackEntriesFromEmbeddedNames(ctx.rawUser, ctx.userOrigins, ctx.embeddedNames)
	if len(problems) != 0 {
		return nil, fmt.Errorf("read planned pack entries: %v", problems)
	}
	entries = appendImplicitPacks(entries)
	fsys := ctx.embeddedFS
	embedded := map[string]*packload.Pack{}
	var embeddedErr error
	// lookup is the closure's universe, the shipped packs by name, as the launch's
	// embeddedPackLookup is: a name yolo does not ship is a MISS, never a failure. The closure
	// asks for names it may not find on purpose — a fork's base it does not see among the
	// selected packs by Name (ResolveNeeds; a configured base's Name is its handle), or a need —
	// and the closure itself refuses, naming the pack and the fix, when the miss matters
	// (ApplyForks, ResolveNeeds). Only a shipped pack that could not be READ is embeddedErr.
	lookup := func(name string) (*packload.Pack, bool) {
		if pack, ok := embedded[name]; ok {
			return pack, true
		}
		if fsys == nil {
			embeddedErr = fmt.Errorf("embedded pack source is unavailable for %s", name)
			return nil, false
		}
		info, err := fs.Stat(fsys, name)
		if errors.Is(err, fs.ErrNotExist) || (err == nil && !info.IsDir()) {
			return nil, false
		}
		if err != nil {
			embeddedErr = fmt.Errorf("read embedded pack %s: %w", name, err)
			return nil, false
		}
		root := filepath.Join(string(filepath.Separator), "embedded", name)
		pack, packProblems, err := packload.ReadSelectedPackFS(fsys, name, root, name, nil, nil)
		if err != nil {
			embeddedErr = fmt.Errorf("read embedded pack %s: %w", name, err)
			return nil, false
		}
		if len(packProblems) != 0 {
			embeddedErr = fmt.Errorf("embedded pack %s has declaration problems: %v", name, packProblems)
			return nil, false
		}
		pack.Official = true
		embedded[name] = pack
		return pack, true
	}
	resolve := func(entry PackEntry) (*packload.Pack, error) {
		var pack *packload.Pack
		var problems []string
		var err error
		if entry.Embedded() {
			if fsys == nil {
				return nil, fmt.Errorf("embedded pack source is unavailable for %s", entry.Name)
			}
			sourceName := entry.EmbeddedName
			if sourceName == "" {
				sourceName = entry.Name
			}
			root := filepath.Join(string(filepath.Separator), "embedded", sourceName)
			pack, problems, err = packload.ReadSelectedPackFS(fsys, sourceName, root, entry.Name, entry.Only, entry.Exclude)
			if err != nil {
				return nil, err
			}
			pack.Official = true
		} else {
			root, err := ctx.configuredPackRoot(entry)
			if errors.Is(err, errPackUnverifiableInJail) {
				// Unverifiable here, as `yolo check` in the jail leaves it
				// (SelectedPackDeclarations' partial answer): the edit cannot touch `packs`, so
				// it changes nothing this pack declares, and the host checks it at its launch.
				ctx.packNotes = append(ctx.packNotes, err.Error())
				return nil, nil
			}
			if err != nil {
				return nil, err
			}
			pack, problems, err = packload.ReadSelectedPackPath(root, entry.Name, entry.Only, entry.Exclude, entry.IsLocal())
			if err != nil {
				return nil, err
			}
			pack.Local = entry.IsLocal()
		}
		if len(problems) != 0 {
			return nil, fmt.Errorf("pack %s declaration problems: %v", entry.Name, problems)
		}
		return pack, nil
	}
	selection := packload.Selection{
		UseProfiles: func(set []*packload.Pack) map[string]string {
			if _, active := ctx.rawUser.Get("active_provider"); !active {
				return ConfigProfileTable(ctx.rawUser, set)
			}
			var closureProfiles map[string]packload.UserProfile
			var closureErr error
			active, closureProfiles, closureErr := explicitEditViaSelection(ctx.rawUser, set)
			ctx.viaSelectionErr = closureErr
			ctx.viaProfiles = closureProfiles
			return active
		},
		UserProfiles: func() (map[string]packload.UserProfile, error) {
			if ctx.viaSelectionErr != nil {
				return nil, ctx.viaSelectionErr
			}
			if _, active := ctx.rawUser.Get("active_provider"); active {
				return clonePackUserProfiles(ctx.viaProfiles), nil
			}
			value, present := ctx.rawUser.Get("profiles")
			if !present || value == nil {
				return map[string]packload.UserProfile{}, nil
			}
			profiles, problems := checkProfiles(value)
			if len(problems) != 0 {
				return nil, fmt.Errorf("user profiles: %v", problems)
			}
			return profiles, nil
		},
	}
	selected, err := selectPacksWithEmbeddedLookup(entries, PackSelectSpec{Resolve: resolve, FailFast: true, Selection: selection}, lookup)
	if err != nil {
		if embeddedErr != nil {
			return nil, fmt.Errorf("planned pack closure source: %w (selection: %v); %s", embeddedErr, err, embeddedSourceNextStep)
		}
		return nil, fmt.Errorf("resolve planned pack selection: %w", err)
	}
	if ctx.viaSelectionErr != nil {
		return nil, fmt.Errorf("resolve planned active provider selection for pack closure: %w", ctx.viaSelectionErr)
	}
	if embeddedErr != nil {
		return nil, fmt.Errorf("planned pack closure source: %w; %s", embeddedErr, embeddedSourceNextStep)
	}
	if !selected.Complete() {
		return nil, fmt.Errorf("planned pack selection is incomplete: %v %v", selected.Unresolved, selected.ClosureErr)
	}
	return selected.Packs(), nil
}

func explicitEditViaSelection(config *jsonx.OrderedMap, packs []*packload.Pack) (map[string]string, map[string]packload.UserProfile, error) {
	var authored *jsonx.OrderedMap
	if raw, present := config.Get(providersKey); present {
		var ok bool
		authored, ok = raw.(*jsonx.OrderedMap)
		if !ok {
			return nil, nil, fmt.Errorf("config.providers: expected a table")
		}
	}
	declarations, err := packload.ComposeProviderDeclarations(authored, packs)
	if err != nil {
		return nil, nil, fmt.Errorf("compose selected provider declarations: %w", err)
	}
	selection, err := ConfigActiveProviderSelection(config)
	if err != nil {
		return nil, nil, err
	}
	resolved, err := ResolveActiveProviders(declarations, selection, ProviderReceivers(packs))
	if err != nil {
		return nil, nil, err
	}
	active := make(map[string]string)
	profiles := make(map[string]packload.UserProfile)
	for receiver, targets := range resolved.ByReceiver {
		if len(targets) == 0 || targets[0].Target.Native {
			continue
		}
		// ResolveVias consumes the existing singular profile table. The F selector result
		// is already in precedence order; use its primary provider just as the legacy table
		// exposes its primary profile, without guessing from authored aliases.
		target := targets[0]
		profile := "\x00readable-provider:" + target.Provider
		if target.Target.Variant != "" {
			profile += "/" + target.Target.Variant
		}
		active[receiver] = profile
		profiles[profile] = packload.UserProfile{Provider: target.Provider, Via: target.Via}
	}
	return active, profiles, nil
}

func clonePackUserProfiles(source map[string]packload.UserProfile) map[string]packload.UserProfile {
	cloned := make(map[string]packload.UserProfile, len(source))
	for name, profile := range source {
		cloned[name] = profile
	}
	return cloned
}

func selectedPackRoot(entry PackEntry) (string, error) {
	if entry.PhysicalPath != "" {
		return entry.PhysicalPath, nil
	}
	if entry.LocalPath != "" {
		return entry.LocalPath, nil
	}
	address, err := url.Parse(entry.Source)
	if err != nil || address.Scheme != "file" || address.Path == "" {
		return "", fmt.Errorf("pack %s has no readable local source path", entry.Name)
	}
	return filepath.FromSlash(address.Path), nil
}

// errPackUnverifiableInJail marks a configured pack a jail's workspace-config edit cannot read:
// its source is a host path or the host's pack store, and the launch delivered no copy.
var errPackUnverifiableInJail = errors.New("pack source is not visible in this jail")

type unverifiableInJailError struct {
	name string
	err  error
}

func (e *unverifiableInJailError) Error() string {
	return fmt.Sprintf("pack %s was not checked: %v — its source is not visible in this jail and "+
		"no copy was delivered; `yolo check` on the host checks it", e.name, e.err)
}

func (e *unverifiableInJailError) Is(target error) bool { return target == errPackUnverifiableInJail }

// isJailEdit says this edit runs in a jail. A jail sees only the copies of the user's packs its
// launch delivered (YOLO_PACK_ROOT), never the host paths and pack store they came from.
// readEditContext refuses a user-scope edit in a jail, so such an edit is a project or local
// one, which cannot change `packs`; the scope test keeps that true by construction.
func (ctx *editValidationContext) isJailEdit() bool {
	return inJail() && ctx.target.scope != ExplicitEditUser
}

// configuredPackRoot is the directory a non-embedded configured pack is read from. On the host
// it is the configured path or the pack store's tree, and any failure refuses. In a jail it falls
// back to the delivered copy, as packsrc.Store.Resolve does for `yolo check`, and a pack with
// neither is errPackUnverifiableInJail for the caller to step aside from.
func (ctx *editValidationContext) configuredPackRoot(entry PackEntry) (string, error) {
	jailEdit := ctx.isJailEdit()
	var root string
	var err error
	if entry.IsLocal() {
		root, err = selectedPackRoot(entry)
		if err == nil && jailEdit {
			root, err = jailLocalPackRoot(entry, root)
		} else if err == nil {
			if _, statErr := os.Stat(root); errors.Is(statErr, fs.ErrNotExist) {
				// The host sees every local pack, so a missing directory is a real fault.
				return "", fmt.Errorf("pack %s: its directory %s does not exist — restore it, or take the pack "+
					"out of `packs` with `yolo pack remove %s`, then retry", entry.Name, root, entry.Name)
			}
		}
	} else {
		root, err = selectedFetchedPackRoot(entry, jailEdit)
	}
	if err != nil && jailEdit {
		return "", &unverifiableInJailError{name: entry.Name, err: err}
	}
	return root, err
}

// jailLocalPackRoot is a local pack's root as a jail can read it: the configured path when it
// exists here, else the copy the launch delivered under YOLO_PACK_ROOT.
func jailLocalPackRoot(entry PackEntry, root string) (string, error) {
	_, statErr := os.Stat(root)
	if statErr == nil {
		return root, nil
	}
	store := &packsrc.Store{Dir: paths.PacksDir()}
	if delivered, ok := store.DeliveredPackDir(entry.Slug()); ok {
		return delivered, nil
	}
	return "", fmt.Errorf("pack root %s: %w", root, statErr)
}

// selectedFetchedPackRoot resolves a fetched pack read-only. A delivered (staged) tree is
// refused on the host, where its source and filters cannot be proved for the planned entry;
// jailEdit accepts it, since in a jail that tree is the pack this jail runs and the edit cannot
// change the entry.
func selectedFetchedPackRoot(entry PackEntry, jailEdit bool) (string, error) {
	address, err := packsrc.Parse(entry.Source)
	if entry.AllowDefaultRef {
		address, err = packsrc.ParsePackAddress(entry.Source)
	}
	if err != nil {
		return "", fmt.Errorf("pack %s source address: %w", entry.Name, err)
	}
	store := &packsrc.Store{Dir: paths.PacksDir()}
	var resolved *packsrc.Resolved
	if entry.AllowDefaultRef && address.Ref == "" {
		resolved, err = store.ResolvePackDefault(address, entry.Name, entry.Slug(), entry.Source,
			packsrc.LockPath(paths.UserConfigPath()), true)
	} else {
		resolved, err = store.ResolveExisting(address, entry.Slug())
	}
	if err != nil {
		return "", fmt.Errorf("pack %s read-only source lookup: %w", entry.Name, err)
	}
	if resolved.StagedFrom != "" && !jailEdit {
		return "", fmt.Errorf("pack %s only resolves through staged tree %s, whose source and filters cannot be proved for this planned entry; rerun the host launch or `yolo pack install` before editing", entry.Name, resolved.StagedFrom)
	}
	return resolved.Root, nil
}
