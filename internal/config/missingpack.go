package config

// missingpack.go is the rule for a selected pack whose BYTES are not there, in the readers of
// broker authority only (readableBrokerSelection, UserScopeSelectedPacksWithNotes): the git and
// GitHub brokers' policy, the approval hook and the timed-offer cap. Launches, `yolo check` and
// every other reader resolve the selection strictly and are untouched.
//
// The owner's ruling (2026-11-25): "I don't think I want to be skipping any packs, although I
// could be convinced that just for these approvals that maybe you could skip a pack, because
// that seems safe." One pack that cannot be read must not withdraw Git's own broker authority
// (docs/reference/git-broker.md ID-14). The failure stays loud: the pack and the resolver's own
// error are named in a note.

import (
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
)

// missingArtifactKinds are the resolution failures that mean "the store does not hold it yet",
// the ones a fetch repairs. Anything else (an unreadable manifest that IS present, a permission
// error, git itself missing) is not an absent artifact and fails the read.
var missingArtifactKinds = []error{packsrc.ErrNotFetched, packsrc.ErrNotCheckedOut, packsrc.ErrRevisionMissing, packsrc.ErrDefaultRefUnavailable}

// missingPackNote is the disclosure for a remote pack skipped because the store lacks it: which
// pack, the resolver's own error, the fetch's own error when one was tried and failed, why the
// grant is unaffected, and the next steps.
func missingPackNote(entry PackEntry, err, fetchErr error) string {
	tried := ""
	if fetchErr != nil {
		tried = fmt.Sprintf("; fetching it failed: %s", oneLineErr(fetchErr))
	}
	return fmt.Sprintf("selected pack %s was skipped for broker approvals: %v%s. It is not in the pack store, so it declares nothing here "+
		"and the broker's grants are unaffected. The broker fetches a missing pack each time it checks, so this clears once the fetch works; "+
		"to fetch it now run `yolo pack install`, or drop it with `yolo pack remove %s`", entry.Name, unwrapResolve(err), tried, entry.Name)
}

// skippableMissingPack reports whether entry, which failed to resolve with err, may be left out
// of a broker-authority read, and the note that says so. fetchErr is why the in-line fetch of
// it failed, nil when none was tried. fetchable is true for a remote a fetch can repair. entries is every selected entry, for the duplicate-name rule.
//
// It never skips: an embedded or clone-backed entry; an entry named like a shipped pack or like
// another entry (a present copy would shadow or collide); a failure that is not an absent
// artifact. A vanished LOCAL source (vanishedLocalPack) is skipped whatever err says.
func skippableMissingPack(entry PackEntry, err, fetchErr error, entries []PackEntry, embeddedNames []string) (note string, fetchable, ok bool) {
	if missing, source := vanishedLocalPack(entry, embeddedNames); missing {
		return vanishedPackNote(entry, source), false, true
	}
	if err == nil || entry.Embedded() || entry.Local != "" || entry.EmbeddedName != "" {
		return "", false, false
	}
	absent := false
	for _, kind := range missingArtifactKinds {
		absent = absent || errors.Is(err, kind)
	}
	if !absent {
		return "", false, false
	}
	addr, perr := entry.PackAddress()
	if perr != nil || addr.IsLocal() {
		return "", false, false
	}
	if namedLikeAShippedPack(entry, addr, embeddedNames) {
		return "", false, false
	}
	same := 0
	for _, other := range entries {
		if other.Name == entry.Name {
			same++
		}
	}
	if same > 1 {
		return "", false, false
	}
	return missingPackNote(entry, err, fetchErr), true, true
}

// namedLikeAShippedPack reports whether a present copy of entry could carry a shipped pack's
// name: the entry's, its last segment, its repository's or its subdirectory's.
func namedLikeAShippedPack(entry PackEntry, addr packsrc.Addr, embeddedNames []string) bool {
	candidates := []string{entry.Name, path.Base(entry.Name), path.Base(strings.TrimSuffix(addr.Repo, ".git")),
		path.Base(addr.Path), path.Base(entry.Subdir), filepath.Base(addr.Path)}
	for _, name := range candidates {
		if slices.Contains(embeddedNames, name) {
			return true
		}
	}
	return false
}

// skippedProfileProblem is the profile rule for a read that skipped a pack: a profile the
// selection names that no readable pack and no user profile declares may live in the pack that
// was left out, so the read refuses naming it. With nothing skipped it is nil, and an unknown
// profile stays what it was.
func skippedProfileProblem(user *jsonx.OrderedMap, packs []*packload.Pack, userProfiles map[string]packload.UserProfile, skipped []string) error {
	if len(skipped) == 0 || user == nil {
		return nil
	}
	selection := ConfigActiveSelection(user)
	if selection.Key == ActiveProviderKey {
		return nil // resolved against the readable packs' providers, which refuses an unknown one itself
	}
	names := slices.Clone(selection.Default)
	for _, set := range selection.Named {
		names = append(names, set...)
	}
	for _, name := range names {
		if _, own := userProfiles[name]; own || packload.DeclaresProfile(packs, name) {
			continue
		}
		return fmt.Errorf("the selected profile %q is declared by no readable pack, and %s could not be read, so it may be declared there; "+
			"fetch it with `yolo pack install` or select a profile with `yolo config set profile`", name, strings.Join(skipped, ", "))
	}
	return nil
}

// unwrapResolve is err without the resolver's "packs: <name>: " prefix, since the note already
// starts with the name.
func unwrapResolve(err error) error {
	var rerr *PackResolveError
	if errors.As(err, &rerr) {
		return rerr.Err
	}
	return err
}
