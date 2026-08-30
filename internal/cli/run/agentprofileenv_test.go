package run

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// envArgValues returns every "-e KEY=…" value in an assembled argv whose key is one of
// keys, in argv order.
func envArgValues(argv []string, keys ...string) []string {
	want := map[string]bool{}
	for _, k := range keys {
		want[k] = true
	}
	var out []string
	for i := 0; i+1 < len(argv); i++ {
		if argv[i] != "-e" {
			continue
		}
		kv := argv[i+1]
		if eq := strings.IndexByte(kv, '='); eq > 0 && want[kv[:eq]] {
			out = append(out, kv)
		}
	}
	return out
}

// bedrockConfig is a config whose claude agent is on the bedrock profile with a fully
// populated provider block.
func bedrockConfig() *jsonx.OrderedMap {
	models := jsonx.NewOrderedMap()
	models.Set("default", "us.anthropic.opus")
	models.Set("haiku", "us.anthropic.haiku")
	models.Set("sonnet", "us.anthropic.sonnet")
	bedrock := jsonx.NewOrderedMap()
	bedrock.Set("region", "us-east-1")
	bedrock.Set("models", models)
	providers := jsonx.NewOrderedMap()
	providers.Set("bedrock", bedrock)
	profiles := jsonx.NewOrderedMap()
	profiles.Set("claude", "bedrock")
	sec := jsonx.NewOrderedMap()
	sec.Set("blocked_tools", []any{})
	return newConfig(
		"agents", []any{"claude"},
		"security", sec,
		"providers", providers,
		"agent_profiles", profiles,
	)
}

func assembleWithConfig(t *testing.T, cfg *jsonx.OrderedMap) []string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	emptyLoopholeDirs(t)
	o := goldenOptions("/ws", home)
	in := &assembleInput{
		cfg:          cfg,
		rt:           "podman",
		cname:        "yolo-ws-abcd1234",
		imageRef:     goldenImageRef,
		packs:        claudePackFixture(t),
		agentsPath:   "/agents/yolo-ws-abcd1234",
		wsState:      "/ws/.yolo/home",
		miseStore:    "/mise-store",
		yoloVersion:  "9.9.9-test",
		mountTargets: map[string]struct{}{},
	}
	return o.assembleRunCmd(in)
}

// TestAssembleEmitsProfileEnvForBedrock pins the CALL SITE, not the callee.
//
// internal/agentenv has its own unit tests, but a test that exercises a function while
// its production caller is unpinned is not a test (AGENTS.md: "does it fail if I delete
// the call site?"). Before this file existed, CLAUDE_CODE_USE_BEDROCK appeared exactly
// once in the whole tree — in assemble.go — and deleting the entire block would not have
// failed a single test. This is that missing test: it asserts the assembled podman argv
// actually carries the profile-derived environment.
func TestAssembleEmitsProfileEnvForBedrock(t *testing.T) {
	argv := assembleWithConfig(t, bedrockConfig())
	got := envArgValues(argv,
		"CLAUDE_CODE_USE_BEDROCK", "AWS_REGION",
		"ANTHROPIC_DEFAULT_OPUS_MODEL", "ANTHROPIC_DEFAULT_HAIKU_MODEL",
		"ANTHROPIC_DEFAULT_SONNET_MODEL")
	want := []string{
		"CLAUDE_CODE_USE_BEDROCK=1",
		"AWS_REGION=us-east-1",
		"ANTHROPIC_DEFAULT_OPUS_MODEL=us.anthropic.opus",
		"ANTHROPIC_DEFAULT_HAIKU_MODEL=us.anthropic.haiku",
		"ANTHROPIC_DEFAULT_SONNET_MODEL=us.anthropic.sonnet",
	}
	if len(got) != len(want) {
		t.Fatalf("profile env args = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("profile env arg %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestAssembleEmitsNoProfileEnvWithoutBedrock: the ordinary first-party launch must carry
// none of it. A stray CLAUDE_CODE_USE_BEDROCK would silently reroute a subscription user
// to an account they may not have.
func TestAssembleEmitsNoProfileEnvWithoutBedrock(t *testing.T) {
	sec := jsonx.NewOrderedMap()
	sec.Set("blocked_tools", []any{})
	argv := assembleWithConfig(t, newConfig("agents", []any{"claude"}, "security", sec))
	if got := envArgValues(argv, "CLAUDE_CODE_USE_BEDROCK", "AWS_REGION"); len(got) != 0 {
		t.Errorf("unprofiled launch carried profile env: %q", got)
	}
}
