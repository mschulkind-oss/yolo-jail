package packdecl

// builtinproviders.go is the `built_in_providers` field (Contribution.BuiltInProviders carries
// the reasoning; docs/design/pi-codex-provider-shadowing.md OQ-3 the ruling): the providers a
// PROGRAM ships its own client and model list for. A BUILT-IN PROVIDER is that doc's term: a
// provider an agent implements itself, under its own key. yolo writes no model entry over one,
// and the agent uses its own list.

import (
	"fmt"
	"sort"
	"strings"
)

// BuiltInProviders is the body of `built_in_providers`: the names of the program's own
// providers, and, for a yolo provider whose plan the program serves under another name or not at
// all, which of its own providers that is.
type BuiltInProviders struct {
	// Names are the program's own provider ids, exactly as the program keys them in its own
	// config (`zai`, `openai-codex`, `llama.cpp`). A yolo provider of one of these names gets no
	// model entry from the program's derive, and its selection selects the program's own
	// provider. Names only, never a model: the program's list is the program's.
	Names []string `json:"names"`
	// Plans maps a YOLO provider name to the program's own provider for that provider's plan,
	// for the case the name alone gets wrong: the program serves the plan under another of its
	// names (opencode serves z.ai's coding plan as `zai-coding-plan`, its own `zai` being the
	// metered API), or it has a provider of that name for another plan and none for this one,
	// which is a JSON null here. A provider a plan names must be one of Names, and the yolo
	// provider it is keyed by gets no model entry from the program's derive either.
	Plans map[string]*ProviderPlan `json:"plans,omitempty"`
	// YoloLists names the yolo providers, each one Names or a non-null Plans key, that the
	// program runs on its own client but on YOLO'S model list, which the program's pack renders
	// from yolo's declaration of the provider: pi and opencode on openai-codex, whose one list
	// packs/openai-auth declares (docs/design/model-lists-and-pickers.md ML-D1). The derive still
	// writes no catalog row over the provider, but core treats its list as yolo's: the agent's
	// children get the provider's YOLO_MODEL_<ROLE> tiers, and the launch's profile line names the
	// endpoint the list rides on rather than saying the agent uses a list of its own.
	YoloLists []string `json:"yolo_lists,omitempty"`
}

// RendersYoloList reports whether the program runs the yolo provider name on yolo's model list
// (YoloLists).
func (b *BuiltInProviders) RendersYoloList(name string) bool {
	if b == nil || name == "" {
		return false
	}
	for _, n := range b.YoloLists {
		if n == name {
			return true
		}
	}
	return false
}

// ProviderPlan is one program's own provider for a yolo provider's plan.
type ProviderPlan struct {
	// Provider is the program's own provider id, one of BuiltInProviders.Names.
	Provider string `json:"provider"`
	// APIKeyEnvName is the variable the program's own provider reads its key from, where that
	// differs from the one the yolo provider names: opencode's `zai-coding-plan` reads
	// ZHIPU_API_KEY where packs/zai names ZAI_API_KEY. Core then delivers the yolo provider's
	// key to the agent under this name too (packload.AgentEnv), so the program's own client
	// finds it. Absent when the program reads the name the SHIPPED yolo provider declares, which
	// core then relays the key under whenever the user re-points the provider's
	// api_key_env_name at a variable of their own (packload.BuiltInKeyVars).
	APIKeyEnvName string `json:"api_key_env_name,omitempty"`
}

// BuiltInProvidersFor returns what the program installing bin declares about its own providers,
// nil when it declares nothing (or the manifest installs no such program). Keyed by BIN, for
// NativeCapabilities' reason: the bin is the agent's name in this vocabulary.
func (m *Manifest) BuiltInProvidersFor(bin string) *BuiltInProviders {
	if bin == "" {
		return nil
	}
	for _, c := range m.Contributions() {
		if c.Kind == KindProgram && c.Bin == bin {
			return c.BuiltInProviders
		}
	}
	return nil
}

// builtInProvidersProblems refuses a `built_in_providers` no reader could use as written: on a
// kind other than program, with no names, a name that is empty, padded or listed twice, a plan
// keyed by an empty name, an object plan naming no provider, a plan naming a provider that is not
// one of the names, and an api_key_env_name that is no environment variable name.
func builtInProvidersProblems(label string, c Contribution) []string {
	b := c.BuiltInProviders
	if b == nil {
		return nil
	}
	if c.Kind != KindProgram {
		return []string{fmt.Sprintf("%s: kind %q does not take \"built_in_providers\" — it names the "+
			"providers a PROGRAM implements itself, so only \"program\" has an agent to say it of",
			label, c.Kind)}
	}
	var problems []string
	if len(b.Names) == 0 {
		problems = append(problems, fmt.Sprintf("%s: \"built_in_providers.names\" is empty, which "+
			"names no provider — list the program's own provider ids, or omit the field", label))
	}
	names := map[string]bool{}
	for _, n := range b.Names {
		switch {
		case n == "":
			problems = append(problems, fmt.Sprintf("%s: \"built_in_providers.names\" holds an empty "+
				"provider name", label))
		case strings.TrimSpace(n) != n || strings.ContainsAny(n, " \t\r\n"):
			problems = append(problems, fmt.Sprintf("%s: \"built_in_providers.names\" entry %q holds "+
				"whitespace, which no provider id does", label, n))
		case names[n]:
			problems = append(problems, fmt.Sprintf("%s: \"built_in_providers.names\" names %q twice",
				label, n))
		}
		names[n] = true
	}
	keys := make([]string, 0, len(b.Plans))
	for k := range b.Plans {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		plan := b.Plans[k]
		if k == "" {
			problems = append(problems, fmt.Sprintf("%s: \"built_in_providers.plans\" has an entry "+
				"keyed by an empty provider name", label))
			continue
		}
		if plan == nil {
			continue // the program has no provider of its own for k's plan
		}
		switch {
		case plan.Provider == "":
			problems = append(problems, fmt.Sprintf("%s: \"built_in_providers.plans.%s\" names no "+
				"provider — name the program's own provider for %q's plan, or write null when it "+
				"has none", label, k, k))
		case !names[plan.Provider]:
			problems = append(problems, fmt.Sprintf("%s: \"built_in_providers.plans.%s\" names %q, "+
				"which is not in \"built_in_providers.names\" — a plan is served by one of the "+
				"program's own providers, so list it there", label, k, plan.Provider))
		}
		if plan.APIKeyEnvName != "" && !ValidEnvName(plan.APIKeyEnvName) {
			problems = append(problems, fmt.Sprintf("%s: \"built_in_providers.plans.%s.api_key_env_name\" "+
				"%q is not an environment variable name", label, k, plan.APIKeyEnvName))
		}
	}
	listed := map[string]bool{}
	for _, n := range b.YoloLists {
		plan, planned := b.Plans[n]
		switch {
		case n == "":
			problems = append(problems, fmt.Sprintf("%s: \"built_in_providers.yolo_lists\" holds an "+
				"empty provider name", label))
		case listed[n]:
			problems = append(problems, fmt.Sprintf("%s: \"built_in_providers.yolo_lists\" names %q "+
				"twice", label, n))
		case planned && plan == nil:
			problems = append(problems, fmt.Sprintf("%s: \"built_in_providers.yolo_lists\" names %q, "+
				"whose plan is null: the program has no provider of its own for it to render yolo's "+
				"list on — drop it from \"yolo_lists\", or name the program's provider in its plan",
				label, n))
		case !planned && !names[n]:
			problems = append(problems, fmt.Sprintf("%s: \"built_in_providers.yolo_lists\" names %q, "+
				"which is neither in \"built_in_providers.names\" nor a key of its \"plans\" — list "+
				"only a provider the program has built in", label, n))
		}
		listed[n] = true
	}
	return problems
}
