package callbroker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerpolicy"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerstore"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
)

// routedContext is one call's execution context: the declared environment edit that a
// user route selected, and the identity that binds an approval to it. Core knows no
// source: the edit is exactly what the source's own declaration named (design 7.1).
//
// The field values are the user's nonsecret selectors. They live here, in the host
// daemon's memory, for the executor and for redaction. They are never placed in a request,
// a store record, an audit event or anything the classifier is given; only Name and
// Digest are.
type routedContext struct {
	// Name is the user's context name, which the approver sees beside the request.
	Name string
	// Digest is brokerstore.ContextIdentity over source, declaration, name, fields and scope.
	Digest string
	set    []envPair
	unset  []string
}

type envPair struct{ name, value string }

// selectRoutedContext picks the one context a call runs in, or nil for the default
// context. Routing is not permission: nothing here admits an operation, and a rule never
// selects a context (design 7.1). Each operation's most specific route is chosen by the
// shared scope order (brokerpolicy.Policy.RouteFor). A call whose operations select
// different contexts is refused, and a matched route whose context cannot be built is a
// failure of the call: there is no fallback to the default context (design 7.2).
func selectRoutedContext(source, declarationHash string, declared *loopholedecl.CallBrokerContext, routing config.BrokerRouting, operations []brokerpolicy.Operation) (*routedContext, error) {
	if declared == nil || len(operations) == 0 {
		return nil, nil
	}
	var routes []brokerpolicy.Route
	for _, op := range operations {
		route, routed := routing.Policy.RouteFor(op)
		if !routed {
			route = brokerpolicy.Route{}
		}
		routes = append(routes, route)
	}
	first := routes[0]
	scopes := map[string]bool{}
	for _, route := range routes {
		if route.Context != first.Context {
			return nil, errors.New("this call's operations select different execution contexts; split it into one call per context, or ask the human to change the routes with `yolo config set`")
		}
		if route.Context != "" {
			scopes[route.Scope] = true
		}
	}
	if first.Context == "" {
		return nil, nil
	}
	context, found := routing.Contexts[first.Context]
	if !found || context.Source != source {
		return nil, fmt.Errorf("the route for this call names context %q, which is not defined for source %s; the call is not run in the default context. Ask the human to define it with `yolo config set contexts.%s.source %s --user`", first.Context, source, first.Context, source)
	}
	rc := &routedContext{Name: first.Context}
	fields := make([]string, 0, len(declared.Fields))
	for field := range declared.Fields {
		fields = append(fields, field)
	}
	sort.Strings(fields)
	for _, field := range fields {
		value, has := context.Fields[field]
		if !has {
			return nil, fmt.Errorf("context %q has no value for declared field %q; the call is not run in the default context. Ask the human to set it with `yolo config set contexts.%s.%s <value> --user`", first.Context, field, first.Context, field)
		}
		if err := brokerpolicy.ValidateText(value); err != nil {
			return nil, fmt.Errorf("context %q field %q %w", first.Context, field, err)
		}
		rc.set = append(rc.set, envPair{name: declared.Fields[field], value: value})
	}
	for field := range context.Fields {
		if _, has := declared.Fields[field]; !has {
			return nil, fmt.Errorf("context %q sets undeclared field %q; the call is not run in the default context. Ask the human to remove it with `yolo config unset contexts.%s.%s --user`", first.Context, field, first.Context, field)
		}
	}
	rc.unset = slices.Clone(declared.Unset)
	scope := ""
	switch ordered := sortedKeys(scopes); len(ordered) {
	case 1:
		scope = ordered[0]
	default:
		// More than one winning scope under one context: bind every one of them.
		encoded, _ := json.Marshal(ordered)
		scope = string(encoded)
	}
	// The declaration hash is in the identity so a changed set or unset rule changes it
	// (design 7.4). ContextIdentity has one source slot, and a hash is fixed-width.
	digest, err := brokerstore.ContextIdentity(source+"@"+declarationHash, scope, rc.Name, context.Fields)
	if err != nil {
		return nil, fmt.Errorf("context %q cannot be bound: %w", first.Context, err)
	}
	rc.Digest = digest
	return rc, nil
}

func sortedKeys(set map[string]bool) []string {
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// environment is the captured host environment of a pinned program with this context's
// edit applied to a copy: each unset name removed, then each declared field's variable
// set to its value. The pinned program's own environment is never modified.
func (rc *routedContext) environment(captured []string) []string {
	if rc == nil {
		return append([]string(nil), captured...)
	}
	drop := map[string]bool{}
	for _, name := range rc.unset {
		drop[name] = true
	}
	for _, pair := range rc.set {
		drop[pair.name] = true
	}
	env := make([]string, 0, len(captured)+len(rc.set))
	for _, entry := range captured {
		if name, _, found := strings.Cut(entry, "="); found && drop[name] {
			continue
		}
		env = append(env, entry)
	}
	for _, pair := range rc.set {
		env = append(env, pair.name+"="+pair.value)
	}
	return env
}

// redactions are the values output is scrubbed of: the pinned program's captured
// secrets plus this context's field values, longest first like pinCLI's own list.
func (rc *routedContext) redactions(secrets [][]byte) [][]byte {
	if rc == nil {
		return secrets
	}
	out := append([][]byte(nil), secrets...)
	for _, pair := range rc.set {
		if pair.value != "" {
			out = append(out, []byte(pair.value))
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return len(out[i]) > len(out[j]) })
	return out
}

type routedContextKey struct{}

// withRoutedContext hands the selected context to the executor on the call's own Go
// context, so no pinned-program field is shared between concurrent calls.
func withRoutedContext(ctx context.Context, rc *routedContext) context.Context {
	if rc == nil {
		return ctx
	}
	return context.WithValue(ctx, routedContextKey{}, rc)
}

func routedContextOf(ctx context.Context) *routedContext {
	rc, _ := ctx.Value(routedContextKey{}).(*routedContext)
	return rc
}

// routedContextFor selects this call's context from the host user config as it is NOW.
// A declaration with no context block, which is every source but one, never reads the
// user config here and runs in the default context exactly as before. The route is
// selected again, from fresh config, at decide, approve and dispatch (design 7.4).
func (h *CLIHandler) routedContextFor(call Classification) (*routedContext, error) {
	if h.start == "" {
		return nil, nil // an unbound handler has no live host authority to route by
	}
	declared := h.declaration.Load()
	if declared == nil {
		if _, _, err := h.currentAuthority(); err != nil {
			return nil, err
		}
		declared = h.declaration.Load()
	}
	if declared == nil || declared.Context == nil {
		return nil, nil
	}
	routing, err := config.CurrentBrokerRouting(h.input.Source)
	if err != nil {
		return nil, fmt.Errorf("%w; the call is not run in the default context", err)
	}
	return selectRoutedContext(h.input.Source, h.input.DeclarationHash, declared.Context, routing, call.Operations)
}

// digest is the identity the default context lacks: "" adds nothing to a digest or a key.
func (rc *routedContext) digest() string {
	if rc == nil {
		return ""
	}
	return rc.Digest
}

// sameContext reports whether two selections bind the same context identity.
func sameContext(a, b *routedContext) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Name == b.Name && a.Digest == b.Digest
}
