package luahook

// modelfor.go is yolo.model_for, the one derive helper that is not a registration: it
// resolves a tier alias for the SELECTED provider to `provider/id`
// (docs/research/extension-model-defaults.md, OQ-XM1, ruled 2026-09-28).
//
// It exists so a derive stops re-implementing the alias lookup by hand, and so an ADAPTER
// PACK (a pack whose only job is to render yolo's resolved aliases into one extension's own
// config file, a term that research doc coins) can be about ten lines of Lua. It names no
// agent and no extension.

import (
	"fmt"
	"strings"

	lua "github.com/yuin/gopher-lua"
)

// ConventionalModelAliases are the tier aliases every provider is expected to declare in its
// `models` map (docs/design/model-lists-and-pickers.md §6): `default` (what you get when
// nothing is said), `fast` (cheap and quick), `balanced` (the middle tier) and `frontier`
// (the most capable tier, added by OQ-XM2 on 2026-09-28). It is a CONVENTION WITH A WARNING,
// not an enum: a provider may declare any other alias, and one missing a name here is warned
// about when a derive asks for it, never refused.
var ConventionalModelAliases = []string{"default", "fast", "balanced", "frontier"}

// modelForName is the yolo.* member the helper is registered under.
const modelForName = "model_for"

// isConventionalAlias reports whether alias is one of ConventionalModelAliases.
func isConventionalAlias(alias string) bool {
	for _, a := range ConventionalModelAliases {
		if a == alias {
			return true
		}
	}
	return false
}

// MissingTierAliasNote is the warning yolo.model_for reports when the selected provider does
// not declare a conventional alias a derive asked for. It names the provider, the alias, the
// whole convention and the fix, because the reader is a user whose child agent or tier just
// fell back to something else.
func MissingTierAliasNote(provider, alias string) string {
	return fmt.Sprintf("provider %q declares no %q model alias, so nothing is resolved for it "+
		"(the conventional tier aliases are %s; a missing one is a warning, never a refusal) — "+
		"add providers.%s.models.%s to your config to name one",
		provider, alias, strings.Join(ConventionalModelAliases, ", "), provider, alias)
}

// installModelFor registers yolo.model_for(alias[, provider]) on the yolo table, closed over
// the ctx the session was built for.
//
//	local qualified, id = yolo.model_for("fast")  -- "zai/glm-5.3-flash", "glm-5.3-flash"
//	local q2, id2 = yolo.model_for("default", "openrouter") -- one entry of the active set
//
// It returns two strings, the model as `<selected provider>/<id>` and the bare id, or nil
// when nothing resolves. The bare id is returned too because agents disagree about the
// spelling (pi's own defaultModel is bare, pi-subagents' and opencode's are qualified) and a
// derive may normalize an id before qualifying it; the id is never parsed, so an id that
// carries slashes of its own (kilo's `vendor/model`) survives whole.
//
// The rules, each chosen so that a derive can call it unconditionally:
//
//   - ONLY THE SELECTED PROVIDER is consulted, or, given the optional second argument, that
//     provider when it is an entry of the agent's active set (DeriveCtx.ActiveSet). Another
//     provider declaring the alias is never borrowed from, because a child agent handed that
//     model would cross providers; a provider outside the set answers nil.
//   - No provider selected, or a selected provider the table has no row for: nil, silently.
//     There is no provider to warn about, and the launch's own gate reports an unknown one.
//   - A CONVENTIONAL alias the provider does not declare: nil, and ctx.Warn is told once per
//     message (the boot's warnOnce dedups). An open-vocabulary alias that is absent is nil
//     and silent, since a derive may probe a name no provider is expected to declare, such
//     as an exact id a profile states.
//
// ⚠ A VERSION BOUNDARY like every yolo.* member (DeriveCtx.UnknownAPI): a build older than
// this helper reads it through the tolerant guard, which yields a stub returning nothing, so
// a shipped derive calling it sees nil there and must treat nil as "resolved nothing".
func installModelFor(L *lua.LState, yolo *lua.LTable, ctx *DeriveCtx) {
	L.SetField(yolo, modelForName, L.NewFunction(func(L *lua.LState) int {
		alias := L.CheckString(1)
		provider := ""
		if ctx != nil {
			provider = ctx.SelectedProvider
		}
		// THE OPTIONAL PROVIDER (docs/design/active-provider-sets.md §4.3): a set-capable derive
		// asks for one entry of its agent's active set by name. A provider outside the set
		// answers nil, so XM-D1's "never borrow another provider's alias" holds for the set.
		if L.GetTop() >= 2 && L.Get(2) != lua.LNil {
			provider = L.CheckString(2)
			if !inActiveSet(ctx, provider) {
				L.Push(lua.LNil)
				return 1
			}
		}
		id, ok := resolveModelAlias(ctx, provider, alias)
		if !ok {
			L.Push(lua.LNil)
			return 1
		}
		L.Push(lua.LString(provider + "/" + id))
		L.Push(lua.LString(id))
		return 2
	}))
}

// inActiveSet reports whether provider is the selected provider or the provider of some entry
// of ctx's active set: the providers yolo.model_for may answer for.
func inActiveSet(ctx *DeriveCtx, provider string) bool {
	if ctx == nil || provider == "" {
		return false
	}
	if provider == ctx.SelectedProvider {
		return true
	}
	for _, e := range ctx.ActiveSet {
		if e.Provider == provider {
			return true
		}
	}
	return false
}

// resolveModelAlias is model_for's lookup for one provider, over the same providers table the
// derive's ctx.providers is built from.
func resolveModelAlias(ctx *DeriveCtx, provider, alias string) (string, bool) {
	if ctx == nil || provider == "" {
		return "", false
	}
	if id, ok := ModelAliasID(ctx.Tables[sourceProviders], provider, alias); ok {
		return id, true
	}
	if _, isRow := ctx.Tables[sourceProviders][provider].(map[string]any); !isRow {
		return "", false
	}
	if isConventionalAlias(alias) && ctx.Warn != nil {
		ctx.Warn(MissingTierAliasNote(provider, alias))
	}
	return "", false
}

// ModelAliasID is the one alias lookup, warning nobody: the id provider's `models` map names
// under alias, in providers (a plain providers table, the shape ctx.providers is built in), or
// ok=false when the provider has no row, no models or no such alias. yolo.model_for reads it,
// and so does the role environment the env-derive runner composes for each agent
// (packload.ModelRoleVars, OQ-XM4), so the two cannot answer an alias differently.
func ModelAliasID(providers map[string]any, provider, alias string) (string, bool) {
	if provider == "" {
		return "", false
	}
	row, isRow := providers[provider].(map[string]any)
	if !isRow {
		return "", false
	}
	models, _ := row["models"].(map[string]any)
	if id, isString := models[alias].(string); isString && id != "" {
		return id, true
	}
	return "", false
}
