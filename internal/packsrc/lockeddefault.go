package packsrc

import "fmt"

// ResolvePackDefault resolves an omitted-ref readable pack only through its durable
// lock pin. A delivered pack tree is the fallback when the host lock is unavailable,
// as in a nested jail; it never guesses the remote or mirror HEAD.
func (s *Store) ResolvePackDefault(addr Addr, name, stagedName, source, lockPath string, readOnly bool) (*Resolved, error) {
	if addr.Kind != KindGit || addr.Ref != "" {
		return nil, fmt.Errorf("pack %s: default-ref resolution requires an omitted-ref git address", name)
	}
	lock, err := LoadLock(lockPath)
	if err != nil {
		if delivered, ok := s.stagedPackDir(stagedName); ok {
			return &Resolved{Root: delivered, StagedFrom: delivered}, nil
		}
		return nil, fmt.Errorf("pack %s: could not read its default-branch lock: %w — run `yolo pack install` or write an explicit ?ref=", name, err)
	}
	pin, ok := lock.Get(name)
	if !ok {
		// A pack whose handle was renamed since its pin was written (a source key renamed, before
		// the next launch moves its records): the pin is found by where the pack lives
		// (docs/reference/pack-system.md#pack-identity), so a rename never reads as an unpinned pack.
		pin, ok = lock.BySource(source)
	}
	if !ok || pin.Source != source || pin.Ref == "" || !isFullSHA(pin.Commit) {
		if delivered, ok := s.stagedPackDir(stagedName); ok {
			return &Resolved{Root: delivered, StagedFrom: delivered}, nil
		}
		return nil, fmt.Errorf("pack %s: no matching default-branch pin in the pack lock — run `yolo pack install` or write an explicit ?ref=", name)
	}
	lockedAddress, err := ParsePackAddress(pin.Source)
	if err != nil || lockedAddress.Ref != "" || lockedAddress.Repo != addr.Repo || lockedAddress.Path != addr.Path {
		if delivered, ok := s.stagedPackDir(stagedName); ok {
			return &Resolved{Root: delivered, StagedFrom: delivered}, nil
		}
		return nil, fmt.Errorf("pack %s: its default-branch lock no longer matches its source — run `yolo pack install` or write an explicit ?ref=", name)
	}
	addr.Ref = pin.Commit
	if readOnly {
		return s.ResolveExisting(addr, stagedName)
	}
	return s.Resolve(addr, stagedName)
}
