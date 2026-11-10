package config

// Provider/model key inventories stay beside the extracted validators so schema ownership
// does not remain split across the common config parser.
var (
	knownEndpointKeys    = set("base_url", "wire_api")
	knownProviderKeys    = set("base_url", "endpoints", "wire_api", "api_key_env_name", "platform", "models", "region", "capabilities", "options", "inherits")
	knownModelKeys       = set("id", "name", "description", "vendor", "reasoning", "input", "cost", "context_window", "max_tokens", "base", "openrouter_routing", "extends", "wire_id")
	unknownModelKeyHints = map[string]string{
		"wireId":   "the wire id is spelled `wire_id`, like every other key here",
		"variants": "declare each variant as its own alias under this provider's `models`, with `wire_id` naming the id it sends and `base` the catalog row it inherits from",
	}
	knownSharedModelKeys = set("name", "description", "reasoning", "input", "cost", "context_window", "max_tokens", "base", "openrouter_routing")
	knownModelCostKeys   = set("input", "output", "cache_read", "cache_write")
)

// providersKey is the top-level config key validateProviders reads.
const providersKey = "providers"

// modelFactsKey is the top-level map of the facts a model's routes share (validateModelFacts).
const modelFactsKey = "model_facts"
