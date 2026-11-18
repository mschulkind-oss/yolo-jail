package callbroker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"slices"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerlock"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerstore"
	"github.com/mschulkind-oss/yolo-jail/internal/callbrokerinput"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
)

type snapshotTransactionKey struct{}

func snapshotTransactionContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, snapshotTransactionKey{}, true)
}

func snapshotTransactionHeld(ctx context.Context) bool {
	held, _ := ctx.Value(snapshotTransactionKey{}).(bool)
	return held
}

// Snapshot reads keep current roots/source floor and the supported read's send in
// one source transaction. Store revalidation marks only callbacks already under it.
func (h *CLIHandler) snapshotReadTransaction(ctx context.Context, effect func(context.Context) error) error {
	if h.input.Version != callbrokerinput.SnapshotLaunchVersion || snapshotTransactionHeld(ctx) {
		return effect(ctx)
	}
	return brokerlock.WithTransactionContext(ctx, h.store.Dir, func() error {
		return effect(snapshotTransactionContext(ctx))
	})
}

func (h *CLIHandler) validateSnapshotReadRoots(call Classification) error {
	if h.input.Version != callbrokerinput.SnapshotLaunchVersion {
		return nil
	}
	sf, _, err := h.currentAuthority()
	if err != nil {
		return err
	}
	for _, chain := range call.Reads {
		if len(chain) == 0 || !slices.Contains(sf.Repos, chain[len(chain)-1].ID) {
			return errors.New("read left current snapshot roots; approve a complete replacement before retrying")
		}
	}
	return nil
}

func (h *CLIHandler) prevalidate(q brokerstore.Request) (brokerstore.Policy, error) {
	if h.input.Version != callbrokerinput.SnapshotLaunchVersion {
		return h.revalidate(q)
	}
	var policy brokerstore.Policy
	err := h.snapshotReadTransaction(q.ExecutionContext(), func(ctx context.Context) error {
		var err error
		policy, err = h.revalidate(q.WithExecutionContext(ctx))
		return err
	})
	return policy, err
}

func snapshotInputDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// currentLegacySnapshot validates the complete format but adopts only already-
// executable legacy asks. Unsupported scoped asks must not be ignored as a
// narrowing or become dispatch permission. This adapter produces NO consumer proof.
func (h *CLIHandler) currentLegacySnapshot(sf brokerscope.File) (brokerscope.PolicySnapshot, error) {
	p, err := brokerscope.ReadPolicy(sf, h.input.DeclarationHash, snapshotInputDigest(h.original))
	if err != nil {
		return p, err
	}
	if p.Model != "legacy/1" || len(p.Asks.Scopes) != 0 {
		return p, errors.New("scoped snapshot execution is not active in this build; stop this jail and relaunch after the complete reader is installed")
	}
	requests, err := jsonx.Decode(p.Asks.Requests)
	if err != nil {
		return p, errors.New("snapshot requests are invalid; stop this jail and relaunch")
	}
	if m, ok := requests.(*jsonx.OrderedMap); ok {
		if read, present := m.Get("read"); present {
			if value, scalar := read.(bool); !scalar || !value && p.Asks.SourceRead != "false" {
				return p, errors.New("snapshot named/scoped read execution is not active or source read disagrees; stop this jail and relaunch")
			}
		}
	}
	return p, nil
}

func (h *CLIHandler) legacySnapshotHostBundle(user *jsonx.OrderedMap) error {
	unsupported := errors.New("scoped ceilings or routes require the complete operation reader; stop this jail and relaunch after that reader is installed")
	if allows, ok := user.Get("allow"); ok {
		if sources, ok := allows.(*jsonx.OrderedMap); ok {
			for _, source := range []string{h.input.Source, h.input.Pack} {
				if raw, ok := sources.Get(source); ok {
					if table, ok := raw.(*jsonx.OrderedMap); ok {
						if never, ok := table.Get("never"); ok {
							if values, ok := never.([]any); !ok || len(values) != 0 {
								return unsupported
							}
						}
					}
				}
			}
		}
	}
	for _, key := range []string{"scopes", "contexts"} {
		if raw, ok := user.Get(key); ok {
			if scopes, ok := raw.(*jsonx.OrderedMap); ok {
				for _, alias := range scopes.Keys() {
					raw, _ := scopes.Get(alias)
					if scope, ok := raw.(*jsonx.OrderedMap); ok {
						if source, _ := scope.Get("source"); source == h.input.Source {
							return unsupported
						}
					}
				}
			}
		}
	}
	return nil
}

func (h *CLIHandler) cellsFromAuthority(sf brokerscope.File, user *jsonx.OrderedMap) (map[string]config.CallBrokerCellPolicy, error) {
	if h.input.Version != callbrokerinput.SnapshotLaunchVersion {
		return h.input.EffectiveCells, nil
	}
	p, err := h.currentLegacySnapshot(sf)
	if err != nil {
		return nil, err
	}
	requests, err := jsonx.Decode(p.Asks.Requests)
	if err != nil {
		return nil, err
	}
	workspace := jsonx.NewOrderedMap()
	sources := jsonx.NewOrderedMap()
	sources.Set(h.input.Source, requests)
	workspace.Set("requests", sources)
	declaration := &loopholedecl.CallBroker{Source: h.input.Source, Cells: h.input.Cells}
	// Only current host ceilings and gate-approved snapshot requests are supplied;
	// no mutable workspace reader or cached/frozen effective-cell pair participates.
	return config.EffectiveCallBrokerCells(h.input.Source, declaration, user, workspace)
}

func (h *CLIHandler) currentCellPolicies() (map[string]config.CallBrokerCellPolicy, error) {
	if h.input.Version != callbrokerinput.SnapshotLaunchVersion {
		return h.input.EffectiveCells, nil
	}
	sf, user, err := h.currentAuthority()
	if err != nil {
		return nil, err
	}
	return h.cellsFromAuthority(sf, user)
}
