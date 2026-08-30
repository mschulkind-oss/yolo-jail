package agentenv

import (
	"reflect"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// cfgFrom builds a merged-config shape from JSON, the way LoadConfig would hand it over.
func cfgFrom(t *testing.T, text string) *jsonx.OrderedMap {
	t.Helper()
	v, err := jsonx.Decode([]byte(text))
	if err != nil {
		t.Fatalf("decoding test config: %v", err)
	}
	m, ok := v.(*jsonx.OrderedMap)
	if !ok {
		t.Fatalf("test config is not an object: %T", v)
	}
	return m
}

func profiles(t *testing.T, text string) *jsonx.OrderedMap { return cfgFrom(t, text) }

// TestResolveBedrockFullBlock pins the exact vars AND their order for the one profile
// that implies environment today. The order is the jail's frozen podman argv order.
func TestResolveBedrockFullBlock(t *testing.T) {
	cfg := cfgFrom(t, `{"providers": {"bedrock": {
		"region": "us-east-1",
		"models": {"default": "opus-x", "haiku": "haiku-x", "sonnet": "sonnet-x"}
	}}}`)
	got := Resolve(cfg, "claude", profiles(t, `{"claude": "bedrock"}`))
	want := []Var{
		{Key: "CLAUDE_CODE_USE_BEDROCK", Value: "1"},
		{Key: "AWS_REGION", Value: "us-east-1"},
		{Key: "ANTHROPIC_DEFAULT_OPUS_MODEL", Value: "opus-x"},
		{Key: "ANTHROPIC_DEFAULT_HAIKU_MODEL", Value: "haiku-x"},
		{Key: "ANTHROPIC_DEFAULT_SONNET_MODEL", Value: "sonnet-x"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Resolve = %+v\nwant %+v", got, want)
	}
}

// TestResolveBedrockWithoutProviderEntry covers the case the flag alone must survive:
// the profile says bedrock but no provider block configures it. Claude Code still has to
// be told to USE bedrock — the region and models simply go undeclared.
func TestResolveBedrockWithoutProviderEntry(t *testing.T) {
	got := Resolve(cfgFrom(t, `{}`), "claude", profiles(t, `{"claude": "bedrock"}`))
	want := []Var{{Key: "CLAUDE_CODE_USE_BEDROCK", Value: "1"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Resolve with no providers = %+v, want %+v", got, want)
	}
}

// TestResolveQuietCases: a shared config routinely names providers a machine cannot use.
// None of these may error or emit anything.
func TestResolveQuietCases(t *testing.T) {
	cfg := cfgFrom(t, `{"providers": {"bedrock": {"region": "us-east-1"}}}`)
	cases := []struct {
		name     string
		agent    string
		profiles string
	}{
		{"no profile for this agent", "claude", `{"codex": "bedrock"}`},
		{"unknown profile name", "claude", `{"claude": "not-a-provider"}`},
		{"empty profile value", "claude", `{"claude": ""}`},
		{"another agent on bedrock carries no claude flag", "codex", `{"codex": "bedrock"}`},
		{"empty agent name", "", `{"claude": "bedrock"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Resolve(cfg, tc.agent, profiles(t, tc.profiles)); len(got) != 0 {
				t.Errorf("Resolve = %+v, want nothing", got)
			}
		})
	}
	if got := Resolve(nil, "claude", profiles(t, `{"claude": "bedrock"}`)); got != nil {
		t.Errorf("Resolve(nil cfg) = %+v, want nil", got)
	}
	if got := Resolve(cfg, "claude", nil); got != nil {
		t.Errorf("Resolve(nil profiles) = %+v, want nil", got)
	}
}

// TestResolveSkipsNonStringAndEmpty: a wrong-typed or empty model must be dropped rather
// than emitted as an empty assignment, which would override the agent's own default.
func TestResolveSkipsNonStringAndEmpty(t *testing.T) {
	cfg := cfgFrom(t, `{"providers": {"bedrock": {
		"region": "",
		"models": {"default": 42, "haiku": "", "sonnet": "sonnet-x"}
	}}}`)
	got := Resolve(cfg, "claude", profiles(t, `{"claude": "bedrock"}`))
	want := []Var{
		{Key: "CLAUDE_CODE_USE_BEDROCK", Value: "1"},
		{Key: "ANTHROPIC_DEFAULT_SONNET_MODEL", Value: "sonnet-x"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Resolve = %+v\nwant %+v", got, want)
	}
}

func TestApplyOverlaysInPlaceAndAppends(t *testing.T) {
	environ := []string{"PATH=/bin", "AWS_REGION=old", "HOME=/home/a"}
	got := Apply(environ, []Var{
		{Key: "AWS_REGION", Value: "us-east-1"},
		{Key: "CLAUDE_CODE_USE_BEDROCK", Value: "1"},
	})
	want := []string{"PATH=/bin", "AWS_REGION=us-east-1", "HOME=/home/a", "CLAUDE_CODE_USE_BEDROCK=1"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Apply = %q\nwant %q", got, want)
	}
	// The input must not be mutated — callers pass os.Environ() and may reuse it.
	if environ[1] != "AWS_REGION=old" {
		t.Errorf("Apply mutated its input: %q", environ)
	}
}

// TestApplyUnsetRemovesRatherThanEmpties is the §2.2 `unset AWS_PROFILE` case, and the
// distinction is the whole reason Var has an Unset field: AWS_PROFILE= is not the same
// as no AWS_PROFILE, and no config surface can express the latter at all.
func TestApplyUnsetRemovesRatherThanEmpties(t *testing.T) {
	got := Apply([]string{"PATH=/bin", "AWS_PROFILE=work", "HOME=/home/a"},
		[]Var{{Key: "AWS_PROFILE", Unset: true}})
	want := []string{"PATH=/bin", "HOME=/home/a"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Apply unset = %q\nwant %q", got, want)
	}
	for _, kv := range got {
		if envKey(kv) == "AWS_PROFILE" {
			t.Fatalf("AWS_PROFILE survived as %q", kv)
		}
	}
}

func TestApplyUnsetOfAbsentKeyIsQuiet(t *testing.T) {
	got := Apply([]string{"PATH=/bin"}, []Var{{Key: "AWS_PROFILE", Unset: true}})
	if !reflect.DeepEqual(got, []string{"PATH=/bin"}) {
		t.Errorf("Apply = %q", got)
	}
}

// TestApplyUnsetThenSetEndsSet: order within vars is honored, so a removal followed by an
// assignment of the same key leaves it SET. Without this, "unset then set" would silently
// lose the assignment.
func TestApplyUnsetThenSetEndsSet(t *testing.T) {
	got := Apply([]string{"AWS_PROFILE=work"}, []Var{
		{Key: "AWS_PROFILE", Unset: true},
		{Key: "AWS_PROFILE", Value: "fresh"},
	})
	if !reflect.DeepEqual(got, []string{"AWS_PROFILE=fresh"}) {
		t.Errorf("Apply = %q, want [AWS_PROFILE=fresh]", got)
	}
}

// TestApplyDuplicateInheritedKey: execve semantics are last-wins, so a duplicated key in
// the inherited environ must collapse to one slot before the overlay targets it.
func TestApplyDuplicateInheritedKey(t *testing.T) {
	got := Apply([]string{"A=1", "A=2", "B=3"}, []Var{{Key: "A", Value: "3"}})
	want := []string{"A=3", "B=3"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Apply = %q\nwant %q", got, want)
	}
}

func TestApplyHandlesBareKeyEntry(t *testing.T) {
	got := Apply([]string{"WEIRD"}, []Var{{Key: "WEIRD", Value: "now-set"}})
	if !reflect.DeepEqual(got, []string{"WEIRD=now-set"}) {
		t.Errorf("Apply = %q", got)
	}
}
