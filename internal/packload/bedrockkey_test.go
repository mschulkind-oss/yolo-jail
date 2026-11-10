package packload

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// bedrockkey_test.go pins THE SERVED-POINTER RULE (docs/design/bedrock-key-provider.md OQ-BK2,
// BK-D3) where it lives, in the one per-agent gate: aws-auth's pointer reaches an agent only when
// its provider claims a variable the pointer sets. Over the shipped claude, aws-auth and bedrock
// packs, so the pointer contribution and the claims are the real ones.

const awsPointer = "AWS_CONTAINER_CREDENTIALS_FULL_URI"

// bedrockKeyPacks is a launch selecting claude and codex beside bedrock, which brings aws-auth.
func bedrockKeyPacks(t *testing.T) []*Pack {
	return embeddedNamed(t, "claude", "codex", "aws-auth", "bedrock")
}

// bedrockKeyScope runs the gate over profiles with the aws-auth adapter served, as a launch does.
func bedrockKeyScope(t *testing.T, profiles map[string]string, sets map[string][]string) *CredentialScope {
	t.Helper()
	packs := bedrockKeyPacks(t)
	providers, resolved, _ := launchSelection(t, packs, nil, nil, profiles)
	served := ServedInJail([]string{"aws-auth"}).WithListen(declaredListen)
	s, err := ScopeCredentials(ScopeInput{Packs: packs, Providers: providers, Profiles: profiles, Sets: sets,
		Resolved: resolved, Served: &served, CallerTokens: awsToken})
	if err != nil {
		t.Fatalf("the gate refused: %v", err)
	}
	return s
}

func TestTheAWSAuthPointerReachesOnlyAProviderThatClaimsIt(t *testing.T) {
	s := bedrockKeyScope(t, map[string]string{"claude": "bedrock-key", "codex": "bedrock"}, nil)
	if _, ok := s.DeliveredTo("claude", awsPointer); ok {
		t.Errorf("claude on bedrock-key received %s; bedrock-key claims only the bearer", awsPointer)
	}
	if _, ok := s.DeliveredTo("codex", awsPointer); !ok {
		t.Errorf("codex on bedrock lost %s; bedrock claims it", awsPointer)
	}
}

// A set whose later entry claims the pointer still receives it: the gate reads every entry (AP-P1).
func TestTheAWSAuthPointerReachesASetWithAClaimingEntry(t *testing.T) {
	s := bedrockKeyScope(t, map[string]string{"claude": "bedrock-key"},
		map[string][]string{"claude": {"bedrock-key", "bedrock"}})
	if _, ok := s.DeliveredTo("claude", awsPointer); !ok {
		t.Errorf("claude on [bedrock-key, bedrock] lost %s; bedrock claims it", awsPointer)
	}
}

// A user provider on the platform that claims nothing at all declared no route, so the rule does
// not narrow it: it receives the pointer with no same-platform provider listing names for it.
func TestAClaimlessBedrockProviderStillReceivesThePointer(t *testing.T) {
	packs := embeddedNamed(t, "claude", "aws-auth")
	user := userProviders(t, `{"bare":{"platform":"aws-bedrock","region":"eu-west-1"}}`)
	profiles := map[string]string{"claude": "bare"}
	providers, resolved, _ := launchSelection(t, packs, user, map[string]UserProfile{"bare": {Provider: "bare"}}, profiles)
	served := ServedInJail([]string{"aws-auth"}).WithListen(declaredListen)
	s, err := ScopeCredentials(ScopeInput{Packs: packs, Providers: providers, Profiles: profiles,
		Resolved: resolved, Served: &served, CallerTokens: awsToken})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := s.DeliveredTo("claude", awsPointer); !ok {
		t.Errorf("claude on a claim-less aws-bedrock provider lost %s", awsPointer)
	}
}

// CN-D22 follows the rule: with every Bedrock agent on bedrock-key, aws-auth's daemon is one the
// launch leaves out; one agent on bedrock starts it.
func TestTheAWSAuthDaemonIsLeftOutWhenNoAgentClaimsItsPointer(t *testing.T) {
	packs := bedrockKeyPacks(t)
	for _, tc := range []struct {
		profiles map[string]string
		left     bool
	}{
		{map[string]string{"claude": "bedrock-key"}, true},
		{map[string]string{"claude": "bedrock-key", "codex": "bedrock"}, false},
	} {
		_, _, sel := launchSelection(t, packs, nil, nil, tc.profiles)
		left := false
		for _, d := range UnselectedProfileServedDaemons(packs, sel) {
			left = left || d.Name == "aws-auth"
		}
		if left != tc.left {
			t.Errorf("%v: aws-auth left out = %v, want %v", tc.profiles, left, tc.left)
		}
	}
}

// The override pre-flight evaluates only a delivered contribution, so the bearer on bedrock-key
// beside a configured SSO profile is no override: the pointer reaches nobody.
func TestABedrockKeyLaunchTripsNoOverride(t *testing.T) {
	packs := bedrockKeyPacks(t)
	_, _, sel := launchSelection(t, packs, nil, nil, map[string]string{"claude": "bedrock-key"})
	look := func(name string) (string, bool) {
		if name == "AWS_BEARER_TOKEN_BEDROCK" {
			return "~/.config/yolo-jail/bedrock-key.env", true
		}
		return "", false
	}
	if got := EnvOverrideFindings(packs, sel, look, nil, nil); len(got) != 0 {
		t.Errorf("a bedrock-key launch tripped an override:\n%s", strings.Join(got[0].Lines, "\n"))
	}
}

// OQ-BK5: one launch mixing bedrock and bedrock-key stays refused, and the remedy says one launch
// cannot mix them and names a launch on either alone — never the empty-profile step, which would
// break the SSO agent. No value is printed.
func TestAMixedBedrockLaunchNamesALaunchOnEitherAlone(t *testing.T) {
	packs := bedrockKeyPacks(t)
	_, _, sel := launchSelection(t, packs, nil, nil, map[string]string{"claude": "bedrock-key", "codex": "bedrock"})
	look := func(name string) (string, bool) {
		if name == "AWS_BEARER_TOKEN_BEDROCK" {
			return "~/.config/yolo-jail/bedrock-key.env", true
		}
		return "", false
	}
	findings := EnvOverrideFindings(packs, sel, look, nil, nil)
	if len(findings) != 1 || !findings[0].Certain {
		t.Fatalf("findings = %+v, want one certain refusal", findings)
	}
	got := strings.Join(findings[0].Lines, "\n")
	for _, want := range []string{"Refusing to launch",
		"One launch cannot mix them: codex on `bedrock` receives pack aws-auth's",
		"claude on `bedrock-key` receives AWS_BEARER_TOKEN_BEDROCK",
		"`yolo -p bedrock-key` or `yolo -p bedrock`"} {
		if !strings.Contains(got, want) {
			t.Errorf("the refusal does not say %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, `profile = ""`) {
		t.Errorf("a mixed launch must not be told to empty the SSO profile:\n%s", got)
	}

	// A set: each side is named by the entry that puts the agent there, not by its primary, so
	// the remedy never offers a launch (`-p zai`) that carries neither credential.
	setPacks := embeddedNamed(t, "claude", "codex", "aws-auth", "bedrock", "zai")
	providers, resolved, _ := launchSelection(t, setPacks, nil, nil,
		map[string]string{"claude": "zai", "codex": "bedrock"})
	setSel := SelectionOfSets(map[string][]string{"claude": {"zai", "bedrock-key"}, "codex": {"bedrock"}},
		resolved, providers)
	setFindings := EnvOverrideFindings(setPacks, setSel, look, nil, nil)
	if len(setFindings) != 1 {
		t.Fatalf("a set mixing them: findings = %+v, want one", setFindings)
	}
	setGot := strings.Join(setFindings[0].Lines, "\n")
	if !strings.Contains(setGot, "claude on `bedrock-key` receives AWS_BEARER_TOKEN_BEDROCK") ||
		!strings.Contains(setGot, "`yolo -p bedrock-key` or `yolo -p bedrock`") || strings.Contains(setGot, "zai") {
		t.Errorf("a set must be named by its claiming entry, never its primary:\n%s", setGot)
	}

	// The control: one agent holding both keeps the profile step, since emptying the profile is
	// that launch's way to keep the bearer.
	both := GateSelection{Profiles: map[string]string{"claude": "mine"},
		Platforms: map[string]string{"claude": "aws-bedrock"},
		SetClaims: map[string][][]string{"claude": {{"AWS_BEARER_TOKEN_BEDROCK", awsPointer}}}}
	all := EnvOverrideFindings(packs, both, look, nil, nil)
	if len(all) != 1 {
		t.Fatalf("one agent holding both: findings = %+v, want one", all)
	}
	single := strings.Join(all[0].Lines, "\n")
	if strings.Contains(single, "cannot mix") || !strings.Contains(single, `profile = ""`) {
		t.Errorf("one agent holding both must keep the profile step:\n%s", single)
	}
}

// A static key pair is a Bedrock key too (the owner, 2026-11-10: "why don't we support
// access/secret? I want to"): an agent on bedrock-key receives the bearer, the key pair and its
// session token from the env channel, and never the SSO profile, which is bedrock's.
func TestBedrockKeyReceivesAStaticKeyPair(t *testing.T) {
	packs := bedrockKeyPacks(t)
	sources := jsonx.NewOrderedMap()
	for _, k := range []string{"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN", "AWS_PROFILE"} {
		sources.Set(k, "value-of-"+strings.ToLower(k))
	}
	profiles := map[string]string{"claude": "bedrock-key", "codex": "bedrock-key"}
	providers, resolved, _ := launchSelection(t, packs, nil, nil, profiles)
	s, err := ScopeCredentials(ScopeInput{Packs: packs, Providers: providers, Profiles: profiles,
		Resolved: resolved, EnvSources: sources, NoDerives: true})
	if err != nil {
		t.Fatalf("the gate refused: %v", err)
	}
	for _, k := range []string{"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN"} {
		if _, ok := s.EnvSourcesFor("claude").Get(k); !ok {
			t.Errorf("claude on bedrock-key did not receive %s", k)
		}
	}
	if _, ok := s.EnvSourcesFor("claude").Get("AWS_PROFILE"); ok {
		t.Error("claude on bedrock-key received AWS_PROFILE, which only bedrock claims")
	}
}

// With every agent on bedrock-key the aws-auth pointer reaches nobody, so a key pair beside a
// configured SSO profile is no override either.
func TestABedrockKeyLaunchWithAKeyPairTripsNoOverride(t *testing.T) {
	packs := bedrockKeyPacks(t)
	_, _, sel := launchSelection(t, packs, nil, nil, map[string]string{"claude": "bedrock-key"})
	look := func(name string) (string, bool) {
		if name == "AWS_ACCESS_KEY_ID" || name == "AWS_SECRET_ACCESS_KEY" {
			return "~/.config/yolo-jail/bedrock-key.env", true
		}
		return "", false
	}
	if got := EnvOverrideFindings(packs, sel, look, nil, nil); len(got) != 0 {
		t.Errorf("a bedrock-key launch with a key pair tripped an override:\n%s", strings.Join(got[0].Lines, "\n"))
	}
}
