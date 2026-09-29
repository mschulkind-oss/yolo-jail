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

// A platform on a kind that has no service to name is refused, as `profile` is on a kind no
// consumer reads it on.
func TestPlatformIsRefusedOnAKindWithNoServiceToName(t *testing.T) {
	_, problems := Decode([]byte(`{"contributes":[
	  {"kind":"profile","name":"p","provider":"b","platform":"aws-bedrock"}]}`))
	if got := strings.Join(problems, "\n"); !strings.Contains(got, `does not take "platform"`) {
		t.Errorf("platform on a profile must be refused, got %v", problems)
	}
}
