package packdecl

// platform_test.go pins a provider's `platform` (docs/design/providers-and-profiles-redesign.md
// OQ-BR2, ruled 2026-09-29) on the manifest decoder a launch reads packs through: what service
// a provider is, in an open vocabulary whose only rule is its shape.

import (
	"strconv"
	"strings"
	"testing"
)

// The value decodes onto the provider projection a launch composes (Providers), which is what
// packload's composer reads: a field that decoded onto Contribution and stopped there would be
// a declaration no derive ever sees.
func TestAProviderMayDeclareItsPlatform(t *testing.T) {
	m, problems := Decode([]byte(`{"contributes":[
	  {"kind":"provider","name":"b","platform":"aws-bedrock"},
	  {"kind":"provider","name":"plain"}]}`))
	if len(problems) != 0 {
		t.Fatalf("a platform is legal on a provider: %v", problems)
	}
	provs := m.Providers()
	if provs[0].Platform != "aws-bedrock" {
		t.Errorf("platform must reach the provider projection, got %q", provs[0].Platform)
	}
	if provs[1].Platform != "" {
		t.Errorf("a provider that declares none has none, got %q", provs[1].Platform)
	}
}

// OPEN VOCABULARY: a value no consumer knows is legal, because it is inert — a newer pack
// naming a platform an older build has never heard of must not refuse the launch.
func TestAnUnknownPlatformIsAccepted(t *testing.T) {
	if _, problems := Decode([]byte(`{"contributes":[
	  {"kind":"provider","name":"b","platform":"some-cloud-nobody-ships"}]}`)); len(problems) != 0 {
		t.Errorf("an unknown platform must decode clean: %v", problems)
	}
}

// Only the SHAPE is checked: a value carrying whitespace could never equal a platform a derive
// compares against, so it would be a declaration that silently does nothing.
func TestAPlatformWithWhitespaceIsRefused(t *testing.T) {
	for _, v := range []string{" aws-bedrock", "aws bedrock", "aws-bedrock\n"} {
		_, problems := Decode([]byte(`{"contributes":[{"kind":"provider","name":"b","platform":` +
			strconv.Quote(v) + `}]}`))
		if got := strings.Join(problems, "\n"); !strings.Contains(got, `"platform"`) ||
			!strings.Contains(got, "whitespace") {
			t.Errorf("platform %q must be refused for its whitespace, got %v", v, problems)
		}
	}
}

// ON `env`, THE PLATFORM IS A GATE (OQ-BR8): the contribution comes back from
// GatedEnvContributions carrying it, and from neither unconditional accessor, so no fold delivers
// it without asking the gate. A caller token may ride it, since it is per agent.
func TestAnEnvContributionMayGateOnAPlatform(t *testing.T) {
	m, problems := Decode([]byte(`{"contributes":[
	  {"kind":"env","platform":"aws-bedrock","served_by":"d","vars":{"TOK":"{caller_token}","URI":"u"}}]}`))
	if len(problems) != 0 {
		t.Fatalf("a platform-gated env is legal: %v", problems)
	}
	gated := m.GatedEnvContributions()
	if len(gated) != 1 || gated[0].Platform != "aws-bedrock" || gated[0].Profile != "" || !gated[0].Gated() {
		t.Errorf("GatedEnvContributions = %+v, want the one platform-gated contribution", gated)
	}
	if got := m.EnvContributions(); len(got) != 0 {
		t.Errorf("a gated contribution must not fold unconditionally: %v", got)
	}
	if got := m.EnvServedBy(); len(got) != 0 {
		t.Errorf("a gated contribution's served_by is on its EnvContribution, not the unconditional map: %v", got)
	}
}

// ONE GATE: a profile name and a platform together would need a rule for which wins, and
// "both" is the name gate D5 falls into again.
func TestAnEnvContributionTakesOneGate(t *testing.T) {
	_, problems := Decode([]byte(`{"contributes":[
	  {"kind":"env","profile":"bedrock","platform":"aws-bedrock","vars":{"A":"1"}}]}`))
	if got := strings.Join(problems, "\n"); !strings.Contains(got, `one gate, "profile" or "platform", not both`) {
		t.Errorf("both gates must be refused, got %v", problems)
	}
}

// A PLATFORM SWITCH (PP-D1) is a program's: it decodes onto the program and names its key, and
// each malformed spelling is refused, as is the list on a kind with no config of its own.
func TestAProgramMayDeclareItsPlatformSwitches(t *testing.T) {
	m, problems := Decode([]byte(`{"contributes":[
	  {"kind":"program","bin":"claude","via":"npm","package":"x","platform_switches":[
	    {"platform":"aws-bedrock","surface":"claude/settings","pointer":"/env/CLAUDE_CODE_USE_BEDROCK"}]}]}`))
	if len(problems) != 0 {
		t.Fatalf("a platform switch on a program is legal: %v", problems)
	}
	sw := m.PlatformSwitches("claude")
	if len(sw) != 1 || sw[0].Platform != "aws-bedrock" || sw[0].Key() != "CLAUDE_CODE_USE_BEDROCK" {
		t.Errorf("PlatformSwitches(claude) = %+v, want the one switch, keyed CLAUDE_CODE_USE_BEDROCK", sw)
	}
	for want, body := range map[string]string{
		`needs the "platform"`:             `{"surface":"claude/settings","pointer":"/env/X"}`,
		`must be "<agent>/<name>"`:         `{"platform":"p","surface":"settings","pointer":"/env/X"}`,
		`is agent "codex"'s`:               `{"platform":"p","surface":"codex/config","pointer":"/env/X"}`,
		`must be an RFC 6901 pointer`:      `{"platform":"p","surface":"claude/settings","pointer":""}`,
		`"aws bedrock" carries whitespace`: `{"platform":"aws bedrock","surface":"claude/settings","pointer":"/a"}`,
	} {
		_, problems := Decode([]byte(`{"contributes":[{"kind":"program","bin":"claude","via":"npm",` +
			`"package":"x","platform_switches":[` + body + `]}]}`))
		if got := strings.Join(problems, "\n"); !strings.Contains(got, want) {
			t.Errorf("%s must be refused with %q, got:\n%s", body, want, got)
		}
	}
	_, problems = Decode([]byte(`{"contributes":[{"kind":"env","vars":{"A":"1"},"platform_switches":[
	  {"platform":"p","surface":"claude/settings","pointer":"/a"}]}]}`))
	if got := strings.Join(problems, "\n"); !strings.Contains(got, `does not take "platform_switches"`) {
		t.Errorf("platform_switches on env must be refused, got %v", problems)
	}
}

// A platform on a kind that has no service to name is refused, as `profile` is on a kind no
// consumer reads it on.
func TestPlatformIsRefusedOnAKindWithNoServiceToName(t *testing.T) {
	_, problems := Decode([]byte(`{"contributes":[
	  {"kind":"profile","name":"p","provider":"b","platform":"aws-bedrock"}]}`))
	if got := strings.Join(problems, "\n"); !strings.Contains(got, `does not take "platform"`) {
		t.Errorf("platform on a profile must be refused, got %v", problems)
	}
}
