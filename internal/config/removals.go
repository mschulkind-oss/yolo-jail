package config

// RemovalPolicy describes an authored removal spelling. Unspecified is deliberate: legacy
// null values are path- and presence-sensitive, so an unrecorded path must not inherit a
// generic interpretation.
type RemovalPolicy uint8

const (
	RemovalUnspecified RemovalPolicy = iota
	RemovalUnsupported
	RemovalNull
	RemovalEmptyList
	RemovalOffString
	RemovalNativeString
	RemovalTombstone
	RemovalUnsetList
)

// RemovalRule attaches one explicitly known removal convention to an exact schema path.
type RemovalRule struct {
	Path   string
	Policy RemovalPolicy
}

// RemovalPolicies returns only legacy paths whose null/removal behavior is uniform and known.
// It does not convert values or collapse the legacy null/presence distinctions.
func RemovalPolicies() []RemovalRule {
	return []RemovalRule{
		{Path: "model_facts.*", Policy: RemovalNull},
		{Path: "profile", Policy: RemovalNull},
		{Path: "profile.*", Policy: RemovalNull},
		{Path: "providers.*.models.*", Policy: RemovalNull},
		{Path: "providers.*.models.*.*", Policy: RemovalTombstone},
	}
}

// ReadableRemovalPolicies lists settled removal values in the new authored vocabulary.
func ReadableRemovalPolicies() []RemovalRule {
	return []RemovalRule{
		{Path: "active_provider.default", Policy: RemovalNativeString},
		{Path: "active_provider.*", Policy: RemovalNativeString},
		{Path: "packs", Policy: RemovalEmptyList},
		{Path: "mounts", Policy: RemovalEmptyList},
		{Path: "install.nix", Policy: RemovalEmptyList},
		{Path: "install.nix.*.outputs", Policy: RemovalEmptyList},
		{Path: "install.nix.*.platforms", Policy: RemovalEmptyList},
		{Path: "machine.ports", Policy: RemovalEmptyList},
		{Path: "machine.forward", Policy: RemovalEmptyList},
		{Path: "mcp_servers.*.command", Policy: RemovalEmptyList},
		{Path: "mcp_servers.*.args", Policy: RemovalEmptyList},
		{Path: "mcp_servers.*.requires_env", Policy: RemovalEmptyList},
		{Path: "env.files", Policy: RemovalEmptyList},
		{Path: "env.unset", Policy: RemovalEmptyList},
		{Path: "env.set.*", Policy: RemovalUnsetList},
		{Path: "include_if_found", Policy: RemovalEmptyList},
		{Path: "devices", Policy: RemovalEmptyList},
		{Path: "writable_home_dirs", Policy: RemovalEmptyList},
		{Path: "required_capabilities", Policy: RemovalEmptyList},
		{Path: "providers.*.capabilities", Policy: RemovalEmptyList},
		{Path: "providers.*.variants.*.capabilities", Policy: RemovalEmptyList},
		{Path: "providers.*.every_model.thinking", Policy: RemovalEmptyList},
		{Path: "providers.*.every_model.accepts", Policy: RemovalEmptyList},
		{Path: "providers.*.models.*.thinking", Policy: RemovalEmptyList},
		{Path: "providers.*.models.*.accepts", Policy: RemovalEmptyList},
		{Path: "model_facts.*.thinking", Policy: RemovalEmptyList},
		{Path: "model_facts.*.accepts", Policy: RemovalEmptyList},
		{Path: "allow.*.always", Policy: RemovalEmptyList},
		{Path: "allow.*.on_request", Policy: RemovalEmptyList},
		{Path: "requests.git.push", Policy: RemovalEmptyList},
		{Path: "requests.git.push_tags", Policy: RemovalEmptyList},
		{Path: "requests.github.dispatch_workflows", Policy: RemovalEmptyList},
		{Path: "requests.*.repos", Policy: RemovalEmptyList},
		{Path: "requests.github.repos", Policy: RemovalEmptyList},
		{Path: "mcp_servers.*", Policy: RemovalOffString},
		{Path: "providers.*", Policy: RemovalOffString},
		{Path: "providers.*.variants.*", Policy: RemovalOffString},
	}
}

func removalPolicy(path string) RemovalPolicy {
	for _, rule := range RemovalPolicies() {
		if path == rule.Path {
			return rule.Policy
		}
	}
	return RemovalUnspecified
}
