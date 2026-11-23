package skillimports

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/gitbroker"
	"github.com/mschulkind-oss/yolo-jail/internal/gitproto"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
)

// Authority is a read-only snapshot of approval and current config authority. Resolve intersects
// these inputs again before starting the existing Git broker, so the caller cannot add scope or
// a grant by passing a remote in an import declaration.
type Authority struct {
	ApprovedRemotes []string
	CurrentRemotes  []string
	ApprovedGrants  []brokerscope.Grant
	CurrentGrants   []brokerscope.Grant
}

// AuthorityFromWorkspace narrows the approval snapshot supplied by a launch or host command
// against the workspace's current remote URLs and current user-wide Git read grant. A failed
// remote read yields an empty current scope, never a wider one.
func AuthorityFromWorkspace(workspace string, approvedRemotes []string, approvedGrants []brokerscope.Grant) Authority {
	read := brokerscope.ReadAllRemotes(workspace)
	var current []string
	if read.Problem == "" {
		for _, remote := range read.Remotes {
			current = append(current, remote.Repo)
		}
	}
	return Authority{
		ApprovedRemotes: append([]string(nil), approvedRemotes...), CurrentRemotes: current,
		ApprovedGrants: append([]brokerscope.Grant(nil), approvedGrants...),
		CurrentGrants:  config.BrokeredUserGrants("git"),
	}
}

// Resolve is an explicit refresh operation. It always uses the filtered host Git broker and
// blocks on the workspace writer lock; launch cache policy is handled by ResolveLaunch.
func (s *Store) Resolve(ctx context.Context, entry config.SkillImport, authority Authority) (Resolution, error) {
	return s.resolveWithFetcher(ctx, entry, authority, gitbroker.FetchImport)
}

func (s *Store) resolveWithFetcher(ctx context.Context, entry config.SkillImport, authority Authority,
	fetch func(context.Context, gitbroker.ImportFetchRequest) (gitbroker.ImportFetchResult, error)) (Resolution, error) {
	if err := validateResolveEntry(entry); err != nil {
		return Resolution{}, err
	}
	if fetch == nil {
		return Resolution{}, errors.New("git broker fetcher is required")
	}
	if _, allowed, err := gitbroker.DecideImportAccess(entry.Git, authority.ApprovedRemotes, authority.CurrentRemotes, authority.ApprovedGrants, authority.CurrentGrants); err != nil || !allowed {
		if err != nil {
			return Resolution{}, err
		}
		return Resolution{}, errors.New("the git broker refused this import")
	}
	var record Resolution
	err := s.WithLock(func() error {
		var err error
		record, err = s.fetchAndRecord(ctx, entry, authority, fetch)
		return err
	})
	if err != nil {
		return Resolution{}, err
	}
	return record, nil
}

// ResolveLaunch refreshes a fresh launch's selection only when its record/store/access state
// requires it. Attach callers never use this method. The returned warning describes a failed
// branch refresh for which the old, still-usable commit was retained.
func (s *Store) ResolveLaunch(ctx context.Context, entry config.SkillImport, authority Authority, now time.Time) (Resolution, string, error) {
	return s.resolveLaunchWithFetcher(ctx, entry, authority, now, gitbroker.FetchImport)
}

func (s *Store) resolveLaunchWithFetcher(ctx context.Context, entry config.SkillImport, authority Authority, now time.Time,
	fetch func(context.Context, gitbroker.ImportFetchRequest) (gitbroker.ImportFetchResult, error)) (Resolution, string, error) {
	if err := validateEntry(entry); err != nil {
		return Resolution{}, "", err
	}
	if fetch == nil {
		return Resolution{}, "", errors.New("git broker fetcher is required")
	}
	access, allowed, err := gitbroker.DecideImportAccess(entry.Git, authority.ApprovedRemotes,
		authority.CurrentRemotes, authority.ApprovedGrants, authority.CurrentGrants)
	if err != nil {
		return Resolution{}, "", err
	}
	if !allowed {
		return Resolution{}, "", errors.New("the git broker refused this import")
	}
	current, currentOK := s.Resolution(entry.Handle, entry)
	usable := currentOK && s.HasCommit(current.Commit) && cacheAccessAllows(current.AccessClass, access)
	if usable && !refreshDue(entry, current) {
		return current, "", nil
	}

	// A launch with a current usable record does not queue behind a refresh it does not own.
	if usable && refreshDue(entry, current) {
		var refreshWarning string
		locked, err := s.TryWithLock(func() error {
			current, currentOK = s.Resolution(entry.Handle, entry)
			usable = currentOK && s.HasCommit(current.Commit) && cacheAccessAllows(current.AccessClass, access)
			if usable && !refreshDue(entry, current) {
				return nil
			}
			fresh, fetchErr := s.launchCheck(ctx, entry, authority, fetch)
			if fetchErr == nil {
				current, usable = fresh, true
				return nil
			}
			if usable && entry.Rev == "" && !current.ResolvedAt.IsZero() {
				refreshWarning = "the Git broker could not refresh this branch; the last usable commit is staged"
				return nil // fetchAndRecord does not mutate the previous record on failure
			}
			return fetchErr
		})
		if err != nil {
			return Resolution{}, "", err
		}
		if !locked {
			return current, "", nil
		}
		if usable {
			return current, refreshWarning, nil
		}
		return Resolution{}, "", errors.New("no usable import resolution is available")
	}

	// No usable content (including an authenticated record under anonymous-only access) waits
	// for an in-flight writer, then consumes its record or makes one attempt itself.
	var warning string
	err = s.WithLock(func() error {
		current, currentOK = s.Resolution(entry.Handle, entry)
		usable = currentOK && s.HasCommit(current.Commit) && cacheAccessAllows(current.AccessClass, access)
		if usable && !refreshDue(entry, current) {
			return nil
		}
		fresh, fetchErr := s.fetchAndRecord(ctx, entry, authority, fetch)
		if fetchErr == nil {
			current, usable = fresh, true
			return nil
		}
		if usable && entry.Rev == "" {
			warning = "the Git broker could not refresh this branch; the last usable commit is staged"
			return nil
		}
		return fetchErr
	})
	if err != nil {
		return Resolution{}, "", err
	}
	if !usable {
		return Resolution{}, "", errors.New("no usable import resolution is available")
	}
	return current, warning, nil
}

// FetchForCommand prepares a resolution while the command owns the whole config/record
// transaction. Publication is the caller's final step, after its targeted config edit succeeds.
func (s *Store) FetchForCommand(ctx context.Context, entry config.SkillImport, authority Authority) (Resolution, error) {
	if s.commandLock == nil {
		return Resolution{}, errors.New("skill command transaction lock is required")
	}
	if err := validateResolveEntry(entry); err != nil {
		return Resolution{}, err
	}
	return s.fetchResolution(ctx, entry, authority, gitbroker.FetchImport)
}

func (s *Store) fetchAndRecord(ctx context.Context, entry config.SkillImport, authority Authority,
	fetch func(context.Context, gitbroker.ImportFetchRequest) (gitbroker.ImportFetchResult, error)) (Resolution, error) {
	previous, hadPrevious := s.Resolution(entry.Handle, entry)
	record, err := s.fetchResolution(ctx, entry, authority, fetch)
	if err != nil {
		return Resolution{}, err
	}
	if err := s.WriteResolution(record); err != nil {
		return Resolution{}, err
	}
	if !hadPrevious {
		record.Change = &ResolutionChange{}
	} else if previous.Commit != record.Commit {
		record.Change = &ResolutionChange{PreviousCommit: previous.Commit}
	}
	return record, nil
}

func (s *Store) fetchResolution(ctx context.Context, entry config.SkillImport, authority Authority,
	fetch func(context.Context, gitbroker.ImportFetchRequest) (gitbroker.ImportFetchResult, error)) (Resolution, error) {
	fetched, err := fetch(ctx, gitbroker.ImportFetchRequest{
		Workspace: s.workspace, URL: entry.Git, Revision: entry.Rev, Ref: entry.Ref, StoreDir: s.ObjectDirectory(),
		ApprovedRemotes: authority.ApprovedRemotes, CurrentRemotes: authority.CurrentRemotes,
		ApprovedGrants: authority.ApprovedGrants, CurrentGrants: authority.CurrentGrants,
	})
	if err != nil {
		return Resolution{}, err
	}
	repo, err := gitproto.Identity(entry.Git)
	if err != nil {
		return Resolution{}, err
	}
	ref := entry.Ref
	if ref == "" && entry.Rev == "" {
		switch {
		case strings.HasPrefix(fetched.ResolvedRef, "refs/heads/"):
			ref = strings.TrimPrefix(fetched.ResolvedRef, "refs/heads/")
		case strings.HasPrefix(fetched.ResolvedRef, "refs/tags/"):
			ref = strings.TrimPrefix(fetched.ResolvedRef, "refs/tags/")
		}
	}
	record := Resolution{
		Version: resolutionVersion, Handle: entry.Handle, Git: entry.Git, Rev: entry.Rev, Ref: ref,
		ResolvedRef: fetched.ResolvedRef, Repo: repo, Commit: fetched.Commit,
		AccessClass: fetched.AccessClass, ResolvedAt: time.Now().UTC(),
	}
	return record, nil
}

func validateResolveEntry(entry config.SkillImport) error {
	if !validHandle(entry.Handle) || entry.Git == "" || entry.Rev != "" && !validObjectID(entry.Rev) ||
		entry.Ref != "" && !validImportRef(entry.Ref) {
		return errors.New("a valid skill import declaration is required")
	}
	if _, err := gitproto.Identity(entry.Git); err != nil {
		return errors.New("a valid broker-supported Git URL is required")
	}
	return nil
}

func validateEntry(entry config.SkillImport) error {
	if err := validateResolveEntry(entry); err != nil || entry.Rev == "" && entry.Ref == "" {
		return errors.New("a valid skill import declaration is required")
	}
	return nil
}

func cacheAccessAllows(stored, current string) bool {
	if stored == AccessAnonymous {
		return current == AccessAnonymous || current == AccessAuthenticated
	}
	return stored == AccessAuthenticated && current == AccessAuthenticated
}

// refreshDue reports whether a fresh launch checks the record: every branch import is checked at
// every fresh launch (docs/design/local-pack-clones.md LPC-D16), with no age throttle. A pinned
// rev and a tag never move, so they never refresh. The transport is the git broker's fetch:
// FetchImport lists refs internally (importfetch.go, listImportRefs) but offers no exported
// moved-or-not read, so the check is one fetch under the pack-check deadline.
func refreshDue(entry config.SkillImport, record Resolution) bool {
	return entry.Rev == "" && strings.HasPrefix(record.ResolvedRef, "refs/heads/")
}

// launchCheck runs one brokered fetch under the launch's check deadline.
func (s *Store) launchCheck(ctx context.Context, entry config.SkillImport, authority Authority,
	fetch func(context.Context, gitbroker.ImportFetchRequest) (gitbroker.ImportFetchResult, error)) (Resolution, error) {
	deadline := s.refreshDeadline
	if deadline <= 0 {
		deadline = packsrc.PackCheckDeadline
	}
	ctx, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()
	return s.fetchAndRecord(ctx, entry, authority, fetch)
}
