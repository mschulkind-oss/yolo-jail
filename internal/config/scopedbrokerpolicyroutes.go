package config

import (
	"errors"
	"fmt"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerpolicy"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// BrokerRouting is one source's routes and execution contexts as the user's host
// config states them now. It carries identity metadata and nonsecret declared field
// values only: no environment is built here and nothing it holds grants authority.
type BrokerRouting struct {
	// Policy holds the user's routes under the source's own scope matcher. It is
	// consulted through RouteFor alone; its rules are not the live decision.
	Policy   brokerpolicy.Policy
	Contexts map[string]BrokerExecutionContext
}

// CurrentBrokerRouting reads the host user config afresh, resolves it through the same
// load path as `yolo config check` (selected declarations, route anchors, declared
// fields, value bounds) and returns the routes and contexts of source. The workspace is
// not read: a workspace cannot write a route or a context. Any load error is returned
// whole, so a broken route or context is a failed call and never a default-context run.
func CurrentBrokerRouting(source string) (BrokerRouting, error) {
	user, sources, err := LoadReadableUserScopeConfigWithSources(true, func(string) {})
	if err != nil {
		return BrokerRouting{}, fmt.Errorf("host user config is unreadable: %w; run yolo config check and retry", err)
	}
	packs, _, _, complete := UserScopeSelectedPacksWithNotes()
	if !complete {
		return BrokerRouting{}, errors.New("selected pack list is incomplete; run yolo check and retry")
	}
	declarations, settings, err := ReadableDeclarationsForPacks(packs)
	if err != nil {
		return BrokerRouting{}, fmt.Errorf("selected pack declaration is unreadable: %w; run yolo check and retry", err)
	}
	selected := make([]string, 0, len(packs))
	for _, pack := range packs {
		selected = append(selected, pack.Name)
	}
	plan, err := ResolveReadableAllowance(ReadableAllowanceInput{User: user, Workspace: jsonx.NewOrderedMap(), SelectedPacks: selected,
		Declarations: declarations, PackSettings: settings, UserOrigins: readableUserOrigins(user, sources), WorkspaceOrigins: map[string][]string{}})
	if err != nil {
		return BrokerRouting{}, fmt.Errorf("host routes or contexts do not load: %w", err)
	}
	policy, ok := plan.OperationPolicies[source]
	if !ok {
		return BrokerRouting{}, fmt.Errorf("source %s has no loaded policy; run yolo check and retry", source)
	}
	routing := BrokerRouting{Policy: policy, Contexts: map[string]BrokerExecutionContext{}}
	for _, c := range plan.Contexts {
		if c.Source == source && c.Selected {
			routing.Contexts[c.Name] = c
		}
	}
	return routing, nil
}
