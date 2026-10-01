package packload

// modelroles.go is the ROLE ENVIRONMENT (docs/research/extension-model-defaults.md, OQ-XM4,
// ruled 2026-09-28: "not until per-agent environment exists; then as YOLO_MODEL_<ROLE>, set per
// agent"). The per-agent vehicle exists since OQ-CN6's env files (2026-09-26), so each agent's
// environment now carries the model its provider names for each conventional tier alias, for
// any program that agent starts to read: an extension, a workflow runner, a script.
//
// It is the env projection of yolo.model_for (OQ-XM1). A pi extension that wants "the fast
// model" today needs an adapter pack rendering yolo's aliases into its own config file; one that
// reads YOLO_MODEL_FAST needs nothing, and it follows the profile because the variable is
// composed from the agent's own selection, never the launch's.
//
// CORE COMPOSES IT, for every agent with a profile, with no pack's help: the vocabulary is core's
// (luahook.ConventionalModelAliases) and so is the lookup (luahook.ModelAliasID, the one
// yolo.model_for reads), so no agent pack has to remember to relay it. AgentEnv adds it beside
// its pack's env derive output, which therefore delivers it on every vehicle that output already
// reaches: the per-agent env file on the container backends and macos-user, and the exec'd
// process at the host notch. An agent with no profile has no delivery, so it gets no role
// variable and removes none: started by an agent that has them, it keeps that agent's.

import (
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/luahook"
	"github.com/mschulkind-oss/yolo-jail/internal/agentenv"
)

// ModelRoleEnvPrefix is the prefix of every role variable: YOLO_MODEL_ then the alias in upper
// case, so YOLO_MODEL_DEFAULT, YOLO_MODEL_FAST, YOLO_MODEL_BALANCED and YOLO_MODEL_FRONTIER.
const ModelRoleEnvPrefix = "YOLO_MODEL_"

// ModelRoleEnv is the variable that carries one tier alias's model.
func ModelRoleEnv(alias string) string { return ModelRoleEnvPrefix + strings.ToUpper(alias) }

// ModelRoleVars is one agent's role environment: for each conventional tier alias, in the
// convention's order, the model its selected provider names for it, as `<provider>/<id>` —
// yolo.model_for's qualified spelling, the one pi-subagents and opencode read; the id is never
// parsed, so an id with slashes of its own (kilo's `vendor/model`) survives whole.
//
// providers is the plain providers table a derive's ctx.providers is built from, and selected
// the provider the agent's profile resolves to — its PRIMARY's, never another entry of its active
// set, as yolo.model_for answers with no provider named.
//
// An alias the selected provider does not name is REMOVED rather than left out, but only when
// some provider in the table names it: an agent started by another agent inherits that one's
// environment, and a YOLO_MODEL_FAST left over from an agent on another provider would hand a
// child a model across providers, which XM-D1 forbids. An alias no provider in the table names
// is not touched, since nothing this launch composes could have set it. And a missing alias is
// SILENT here, unlike yolo.model_for's warning: no derive asked for it, so a warning would name
// a gap nobody is relying on, at every launch.
func ModelRoleVars(providers map[string]any, selected string) []agentenv.Var {
	var out []agentenv.Var
	for _, alias := range luahook.ConventionalModelAliases {
		if id, ok := luahook.ModelAliasID(providers, selected, alias); ok {
			out = append(out, agentenv.Var{Key: ModelRoleEnv(alias), Value: selected + "/" + id})
			continue
		}
		if anyProviderNames(providers, alias) {
			out = append(out, agentenv.Var{Key: ModelRoleEnv(alias), Unset: true})
		}
	}
	return out
}

// anyProviderNames reports whether some provider in the table names alias.
func anyProviderNames(providers map[string]any, alias string) bool {
	for name := range providers {
		if _, ok := luahook.ModelAliasID(providers, name, alias); ok {
			return true
		}
	}
	return false
}
