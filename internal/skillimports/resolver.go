package skillimports

import (
	"context"
	"errors"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/gitbroker"
	"github.com/mschulkind-oss/yolo-jail/internal/gitproto"
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

// Resolve fetches one declared pin or moving ref through the existing Git broker, adds its
// objects to this workspace's bare store and atomically replaces its host-only resolution
// record. It is an explicit refresh operation; launch cache policy is applied by ResolveLaunch.
func (s *Store) Resolve(ctx context.Context, entry config.SkillImport, authority Authority) (Resolution, error) {
	return s.resolveWithFetcher(ctx, entry, authority, gitbroker.FetchImport)
}

func (s *Store) resolveWithFetcher(ctx context.Context, entry config.SkillImport, authority Authority,
	fetch func(context.Context, gitbroker.ImportFetchRequest) (gitbroker.ImportFetchResult, error)) (Resolution, error) {
	var record Resolution
	if entry.Handle == "" || entry.Git == "" || entry.Rev == "" && entry.Ref == "" {
		return record, errors.New("a valid skill import declaration is required")
	}
	if fetch == nil {
		return record, errors.New("Git broker fetcher is required")
	}
	err := s.WithLock(func() error {
		fetched, err := fetch(ctx, gitbroker.ImportFetchRequest{
			Workspace: s.workspace,
			URL:       entry.Git, Revision: entry.Rev, Ref: entry.Ref, StoreDir: s.ObjectDirectory(),
			ApprovedRemotes: authority.ApprovedRemotes, CurrentRemotes: authority.CurrentRemotes,
			ApprovedGrants: authority.ApprovedGrants, CurrentGrants: authority.CurrentGrants,
		})
		if err != nil {
			return err
		}
		repo, err := gitproto.Identity(entry.Git)
		if err != nil {
			return err
		}
		record = Resolution{
			Version: resolutionVersion, Handle: entry.Handle, Git: entry.Git, Rev: entry.Rev, Ref: entry.Ref,
			ResolvedRef: fetched.ResolvedRef, Repo: repo, Commit: fetched.Commit,
			AccessClass: fetched.AccessClass, ResolvedAt: time.Now().UTC(),
		}
		return s.WriteResolution(record)
	})
	if err != nil {
		return Resolution{}, err
	}
	return record, nil
}
