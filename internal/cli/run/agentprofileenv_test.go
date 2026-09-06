package run

import (
	"os"
	"path/filepath"
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

// assembled is one launch's assembly: the argv plus the pair that composed it, so a
// test can assert on BOTH halves of the delivery. The provider/profile channel no
// longer rides the argv — it crosses in yolo-user-env.sh's channel section
// (writeUserEnvFile), per-entry — so the assertions that used to grep the argv for
// ANTHROPIC_*/YOLO_* now render the file from the SAME composed channel: one input,
// one writer, the bytes the jail actually sources.
type assembled struct {
	argv []string
	o    *Options
	in   *assembleInput
}

// channelFile writes the channel section exactly as the launch's lifecycle phase does
// and returns the file's bytes.
func (a assembled) channelFile(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "yolo-user-env.sh")
	writeUserEnvFile(p, nil, a.in.envChannel(a.o))
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// channelEnv is the file twin of envArgValues: the plain-form `export K='v'` values
// for keys, in file order, with the writer's '\” quoting unescaped. Def-form lines
// (env_sources defaults) never match — the channel section is plain-form only.
func (a assembled) channelEnv(t *testing.T, keys ...string) []string {
	t.Helper()
	want := map[string]bool{}
	for _, k := range keys {
		want[k] = true
	}
	var out []string
	for _, line := range strings.Split(a.channelFile(t), "\n") {
		rest, ok := strings.CutPrefix(line, "export ")
		if !ok || strings.Contains(rest, "=${") {
			continue // def-form or not an export
		}
		k, v, ok := strings.Cut(rest, "='")
		if !ok || !strings.HasSuffix(v, "'") {
			continue
		}
		v = strings.TrimSuffix(v, "'")
		if want[k] {
			out = append(out, k+"="+strings.ReplaceAll(v, `'\''`, `'`))
		}
	}
	return out
}

// bedrockConfig is a config whose claude agent is on the bedrock profile with a fully
// populated provider block. It carries the VALUES only — region and model ids — because
// the delivery SHAPE is packs/claude's own declaration, which claudePackFixture loads.
func bedrockConfig() *jsonx.OrderedMap {
	models := jsonx.NewOrderedMap()
	models.Set("default", "us.anthropic.opus")
	models.Set("fast", "us.anthropic.fast")
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
		"use_profiles", profiles,
	)
}

func assembleWithConfig(t *testing.T, cfg *jsonx.OrderedMap, hooks ...func()) []string {
	return assembleWithConfigAssembled(t, cfg, hooks...).argv
}

// assembleWithConfigAssembled is assembleWithConfig for the tests that assert the
// channel file beside the argv.
func assembleWithConfigAssembled(t *testing.T, cfg *jsonx.OrderedMap, hooks ...func()) assembled {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	// The hooks run once HOME is the assembly's own temp dir, which is what a user-scope
	// write needs: `profiles` is read off the USER FILE (config.LoadProfiles), never off
	// the merged cfg, so an option-carrying profile can only be handed in through the
	// filesystem this test controls.
	for _, hook := range hooks {
		hook()
	}
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
	return assembled{argv: o.assembleRunCmd(in), o: o, in: in}
}

// TestAssembleEmitsProfileEnvForBedrock pins the CALL SITE, not the callee.
//
// internal/agentenv has its own unit tests, but a test that exercises a function while
// its production caller is unpinned is not a test (AGENTS.md: "does it fail if I delete
// the call site?"). Before this file existed, CLAUDE_CODE_USE_BEDROCK appeared exactly
// once in the whole tree — in assemble.go — and deleting the entire block would not have
// failed a single test. This is that missing test: it asserts the assembled podman argv
// actually carries the profile-derived environment.
//
// The five vars arrive by two routes, and both are this launch's own composition: the
// env derive packs/claude ships composes AWS_REGION and the three model ids from the
// user's providers.bedrock entry (the packload.AgentEnv loop), and the
// CLAUDE_CODE_USE_BEDROCK literal rides the pack's PROFILE-GATED kind:env contribution,
// folded by the same pass that composes the derive's vars — which sits LATER in the argv,
// hence the order, and hence the two routes being pinned together here rather than in
// either of their own packages.
func TestAssembleEmitsProfileEnvForBedrock(t *testing.T) {
	la := assembleWithConfigAssembled(t, bedrockConfig())
	// File order: the pack env fold (CLAUDE_CODE_USE_BEDROCK) precedes the derive's
	// shape vars, which the writer emits sorted within their section.
	got := la.channelEnv(t,
		"CLAUDE_CODE_USE_BEDROCK", "AWS_REGION",
		"ANTHROPIC_DEFAULT_OPUS_MODEL", "ANTHROPIC_DEFAULT_HAIKU_MODEL",
		"ANTHROPIC_DEFAULT_SONNET_MODEL")
	want := []string{
		"CLAUDE_CODE_USE_BEDROCK=1",
		"ANTHROPIC_DEFAULT_HAIKU_MODEL=us.anthropic.haiku",
		"ANTHROPIC_DEFAULT_OPUS_MODEL=us.anthropic.opus",
		"ANTHROPIC_DEFAULT_SONNET_MODEL=us.anthropic.sonnet",
		"AWS_REGION=us-east-1",
	}
	if len(got) != len(want) {
		t.Fatalf("profile env = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("profile env arg %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// The OPTION half, on the same real derive: the profile's own `model` value names the
// alias, and the id under it is what ANTHROPIC_DEFAULT_OPUS_MODEL carries — not the
// provider's declared default. The user entry is the SAME NAME as the shipped profile,
// which is §5.2's "customize the pack's own profile" case: the pack's provider stands and
// the user's option rides on top. Sonnet and haiku keep their own aliases — they are
// Claude's routing names, not a selection surface, so a `model` option must not reach
// them.
func TestAssembleEmitsTheAliasTheProfileOptionNames(t *testing.T) {
	la := assembleWithConfigAssembled(t, bedrockConfig(), func() {
		writeProfilesAtHome(t, `{"bedrock": {"provider": "bedrock", "model": "fast"}}`)
	})
	got := la.channelEnv(t, "ANTHROPIC_DEFAULT_OPUS_MODEL",
		"ANTHROPIC_DEFAULT_SONNET_MODEL", "ANTHROPIC_DEFAULT_HAIKU_MODEL")
	want := []string{
		"ANTHROPIC_DEFAULT_HAIKU_MODEL=us.anthropic.haiku",
		"ANTHROPIC_DEFAULT_OPUS_MODEL=us.anthropic.fast",
		"ANTHROPIC_DEFAULT_SONNET_MODEL=us.anthropic.sonnet",
	}
	if len(got) != len(want) {
		t.Fatalf("profile env = %q, want %q", got, want)
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
