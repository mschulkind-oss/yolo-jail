package run

import (
	"slices"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// bedrockclaudemodel_test.go pins claude's half of the one Bedrock provider
// (docs/design/bedrock-plumbing.md OQ-BR9, ruled 2026-09-29): packs/bedrock lists models of
// several makers, and claude's own Bedrock client can call Anthropic's alone, so claude's env
// derive picks among the entries whose `vendor` is anthropic. With no model named it pins
// none, because Claude Code starts on an Anthropic model of its own there, a valid session yolo
// does not steer (docs/design/model-lists-and-pickers.md OQ-ML2). Driven through the assembled
// launch channel, so it fails if the derive's call site or the pack's list moves.

// bedrockRegionOnly is a claude launch on the shipped `bedrock` profile whose only user
// provider fact is the region, so the model list is the pack's own.
func bedrockRegionOnly(models *jsonx.OrderedMap) *jsonx.OrderedMap {
	bedrock := jsonx.NewOrderedMap()
	bedrock.Set("region", "us-east-1")
	if models != nil {
		bedrock.Set("models", models)
	}
	providers := jsonx.NewOrderedMap()
	providers.Set("bedrock", bedrock)
	profiles := jsonx.NewOrderedMap()
	profiles.Set("claude", "bedrock")
	sec := jsonx.NewOrderedMap()
	sec.Set("blocked_tools", []any{})
	return newConfig("agents", []any{"claude"}, "security", sec,
		"providers", providers, "use_profiles", profiles)
}

var claudeModelKeys = []string{"ANTHROPIC_MODEL", "ANTHROPIC_DEFAULT_OPUS_MODEL",
	"ANTHROPIC_DEFAULT_SONNET_MODEL", "ANTHROPIC_DEFAULT_HAIKU_MODEL", "ANTHROPIC_SMALL_FAST_MODEL"}

func TestClaudeOnBedrockStartsOnlyOnAnAnthropicModel(t *testing.T) {
	const opus = "global.anthropic.claude-opus-5-5"
	const sol = "us.openai.gpt-6.1-sol"
	pinned := func(id string) []string {
		return []string{"ANTHROPIC_MODEL=" + id, "ANTHROPIC_DEFAULT_OPUS_MODEL=" + id,
			"ANTHROPIC_DEFAULT_SONNET_MODEL=" + id, "ANTHROPIC_DEFAULT_HAIKU_MODEL=" + id,
			"ANTHROPIC_SMALL_FAST_MODEL=" + id}
	}
	userDefault := func(id string) *jsonx.OrderedMap {
		m := jsonx.NewOrderedMap()
		m.Set("default", id)
		return m
	}
	for _, tc := range []struct {
		name    string
		models  *jsonx.OrderedMap
		profile string // a user profile over bedrock, "" for the shipped one
		want    []string
	}{
		// The shipped list names no `default`, and its first entry is Anthropic's; claude
		// still pins nothing, since its own Bedrock default is valid.
		{"the shipped list pins nothing", nil, "", nil},
		// A profile naming an entry claude can call gets it, on every tier.
		{"a profile naming the Anthropic entry", nil, `{"bedrock": {"provider": "bedrock", "model": "` + opus + `"}}`, pinned(opus)},
		// A profile naming an entry of another maker is skipped rather than sent: Messages
		// serves Claude only, so claude would start on a model it cannot call.
		{"a profile naming the OpenAI entry", nil, `{"bedrock": {"provider": "bedrock", "model": "` + sol + `"}}`, nil},
		// The user's `default` alias steers it when it names a model claude can call, which
		// is the configuration opting in; one naming another maker's model does not.
		{"a user default claude can call", userDefault(opus), "", pinned(opus)},
		{"a user default of another maker", userDefault(sol), "", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var hooks []func()
			if tc.profile != "" {
				profile := tc.profile
				hooks = append(hooks, func() { writeProfilesAtHome(t, profile) })
			}
			la := assembleWithPacksAssembled(t, bedrockRegionOnly(tc.models),
				[]string{"claude", "bedrock", "aws-auth", "openai-auth", "wire-bridge"}, hooks...)
			got := la.channelEnv(t, claudeModelKeys...)
			slices.Sort(got)
			want := slices.Clone(tc.want)
			slices.Sort(want)
			if !slices.Equal(got, want) {
				t.Errorf("claude's model env = %q, want %q", got, want)
			}
			if sw := la.channelEnv(t, "CLAUDE_CODE_USE_BEDROCK"); !slices.Equal(sw, []string{"CLAUDE_CODE_USE_BEDROCK=1"}) {
				t.Errorf("claude on bedrock must still run its own Bedrock client: %q", sw)
			}
		})
	}
}

// WHAT CLAUDE'S OWN BEDROCK CLIENT CANNOT USE STAYS OUT OF ITS ENVIRONMENT. Three branches of
// packs/claude's env derive, each driven through the assembled launch channel with the pack's
// own list:
//
//   - an anthropic endpoint on a Bedrock provider is the wire bridge's twin of an `openai`
//     endpoint the user gave it, and claude's own client composes its URL from the region, so
//     under the native profile no ANTHROPIC_BASE_URL (and no bridge caller token) is written;
//   - a tier alias the user names on the provider is held to the makers claude can call, so a
//     `sonnet` naming an OpenAI id leaves that tier on the pinned Anthropic model;
//   - a profile routed through the bridge, on a provider with no anthropic endpoint to carry
//     claude, runs claude on its own login, so the profile's model (an OpenAI id here) is not
//     handed to it.
func TestClaudeOnBedrockTakesNothingItsOwnClientCannotUse(t *testing.T) {
	const opus = "global.anthropic.claude-opus-5-5"
	const sol = "us.openai.gpt-6.1-sol"
	packs := []string{"claude", "bedrock", "aws-auth", "openai-auth", "wire-bridge"}
	withProfile := func(profile string) func() {
		return func() { writeProfilesAtHome(t, profile) }
	}

	t.Run("an openai endpoint's bridge twin is not claude's native address", func(t *testing.T) {
		cfg := bedrockRegionOnly(nil)
		prov, _ := cfg.Get("providers")
		bedrock, _ := prov.(*jsonx.OrderedMap).Get("bedrock")
		ep := jsonx.NewOrderedMap()
		ep.Set("base_url", "https://bedrock-runtime.us-east-1.amazonaws.com/openai/v1")
		ep.Set("wire_api", "openai-chat-completions")
		eps := jsonx.NewOrderedMap()
		eps.Set("openai", ep)
		bedrock.(*jsonx.OrderedMap).Set("endpoints", eps)
		la := assembleWithPacksAssembled(t, cfg, packs)
		if got := la.channelEnv(t, "ANTHROPIC_BASE_URL", "ANTHROPIC_AUTH_TOKEN"); len(got) != 0 {
			t.Errorf("claude on its own Bedrock client was pointed at the bridge: %q", got)
		}
		if sw := la.channelEnv(t, "CLAUDE_CODE_USE_BEDROCK"); !slices.Equal(sw, []string{"CLAUDE_CODE_USE_BEDROCK=1"}) {
			t.Errorf("claude on bedrock must still run its own Bedrock client: %q", sw)
		}
	})

	t.Run("a tier alias of another maker leaves the tier on the pinned model", func(t *testing.T) {
		models := jsonx.NewOrderedMap()
		models.Set("sonnet", sol)
		models.Set("haiku", sol)
		la := assembleWithPacksAssembled(t, bedrockRegionOnly(models), packs,
			withProfile(`{"bedrock": {"provider": "bedrock", "model": "`+opus+`"}}`))
		got := la.channelEnv(t, claudeModelKeys...)
		slices.Sort(got)
		want := []string{"ANTHROPIC_DEFAULT_HAIKU_MODEL=" + opus, "ANTHROPIC_DEFAULT_OPUS_MODEL=" + opus,
			"ANTHROPIC_DEFAULT_SONNET_MODEL=" + opus, "ANTHROPIC_MODEL=" + opus, "ANTHROPIC_SMALL_FAST_MODEL=" + opus}
		if !slices.Equal(got, want) {
			t.Errorf("claude's model env = %q, want every tier on %s", got, opus)
		}
	})

	t.Run("a bridged profile's model is not handed to claude on its own login", func(t *testing.T) {
		la := assembleWithPacksAssembled(t, bedrockRegionOnly(nil), packs,
			withProfile(`{"bedrock": {"provider": "bedrock", "via": "wire-bridge", "model": "`+sol+`"}}`))
		if got := la.channelEnv(t, claudeModelKeys...); len(got) != 0 {
			t.Errorf("claude on its own login was handed a Bedrock model: %q", got)
		}
		if sw := la.channelEnv(t, "CLAUDE_CODE_USE_BEDROCK"); len(sw) != 0 {
			t.Errorf("a bridged profile must not switch claude to its own Bedrock client: %q", sw)
		}
	})
}
