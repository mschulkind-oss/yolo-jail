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
