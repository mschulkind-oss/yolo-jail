package packload

// served_test.go pins the "served at this notch" predicate and what it drives
// (docs/plans/notch-convergence.md §4 item 2): a via this notch does not serve is cleared and
// named, a pack env variable declared `served_by` a daemon this notch does not serve is withheld
// from every process and named, and the served set comes from one place for the jail notches.

import (
	"strings"
	"testing"
)

func TestViaServedAtClearsOnlyAViaThisNotchDoesNotServe(t *testing.T) {
	packs := embeddedNamed(t, "wire-bridge")
	in := map[string]ResolvedProfile{
		"pz":    {Provider: "zai", Via: "wire-bridge", ViaBase: "http://127.0.0.1:8216"},
		"plain": {Provider: "zai"},
	}
	out, cleared := ViaServedAt(in, packs, NothingServed())
	if p := out["pz"]; p.Via != "wire-bridge" || p.ViaBase != "" || ViaURLFor(p, "pi") != "" {
		t.Errorf("pz = %+v, want its via kept and no address", p)
	}
	if strings.Join(cleared, ",") != "pz" {
		t.Errorf("cleared = %v, want [pz] named", cleared)
	}
	if in["pz"].ViaBase == "" {
		t.Error("ViaServedAt modified its input")
	}
	kept, none := ViaServedAt(in, packs, ServedAtContainer([]string{"wire-bridge"}))
	if kept["pz"].ViaBase == "" || len(none) != 0 {
		t.Errorf("a container launch serving the bridge lost the via: %+v, cleared %v", kept["pz"], none)
	}
	if out, cleared := ViaServedAt(nil, packs, NothingServed()); out != nil || cleared != nil {
		t.Error("ViaServedAt(nil) must stay nil")
	}
}

func TestServedAtRuntimeIsNothingOnMacosUser(t *testing.T) {
	names := []string{"wire-bridge", "openai-auth-broker"}
	for rt, want := range map[string]bool{"podman": true, "container": true, "": true, "macos-user": false} {
		s := ServedAtRuntime(rt, names)
		if s.Serves("wire-bridge") != want || s.RunsDaemons() != want {
			t.Errorf("runtime %q: serves the bridge = %v, runs daemons = %v; want %v",
				rt, s.Serves("wire-bridge"), s.RunsDaemons(), want)
		}
	}
	if NothingServed().Serves("wire-bridge") || ServedAtContainer(nil).Serves("") {
		t.Error("an empty set or an empty name was served")
	}
	if got := ServiceJailDaemonNames(embeddedNamed(t, "wire-bridge")); strings.Join(got, ",") != "wire-bridge" {
		t.Errorf("ServiceJailDaemonNames = %v", got)
	}
}

// THE SHIPPED POINTERS ARE WITHHELD WHERE NOTHING SERVES THEM, through the credential gate
// every vehicle reads. MEASURED 2026-09-27 before this: `yolo host env --agent claude -p bedrock`
// exported AWS_CONTAINER_CREDENTIALS_FULL_URI=http://127.0.0.1:1461/credentials, and codex's
// CODEX_REFRESH_TOKEN_URL_OVERRIDE pointed at :1460 at every notch. Now each is delivered where
// its daemon runs and withheld, and named, everywhere else. Deleting `served_by` from either
// pack, or the gate's filter, fails this.
// declaredListen is the two shipped adapters' declared listen addresses, the served addresses a
// private-namespace launch hands the served set.
var declaredListen = map[string]string{
	"openai-auth-broker": "127.0.0.1:1460",
	"aws-auth":           "127.0.0.1:1461",
}

func TestAPointerIsDeliveredOnlyWhereItsDaemonIsServed(t *testing.T) {
	packs := embeddedNamed(t, "codex", "openai-auth", "aws-auth")
	profiles := map[string]string{"codex": "bedrock"}
	compose := func(served *ServedDaemons) *CredentialScope {
		t.Helper()
		s, err := ScopeCredentials(ScopeInput{Packs: packs, Profiles: profiles, NoDerives: true, Served: served,
			// The token aws-auth's pointer names ({caller_token}, OQ-CN7 (c)), as a launch mints it.
			CallerTokens: map[string]string{"YOLO_SERVICE_AWS_AUTH_TOKEN": strings.Repeat("ab", 32)}})
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	foldHas := func(fold []EnvFoldEntry, key string) bool {
		for _, e := range fold {
			if e.Key == key {
				return true
			}
		}
		return false
	}
	const refresh, awsURI, awsToken = "CODEX_REFRESH_TOKEN_URL_OVERRIDE",
		"AWS_CONTAINER_CREDENTIALS_FULL_URI", "AWS_CONTAINER_AUTHORIZATION_TOKEN"

	container := ServedAtContainer([]string{"openai-auth-broker", "aws-auth"}).WithListen(declaredListen)
	s := compose(&container)
	if _, ok := s.DeliveredPackEnv(refresh); !ok {
		t.Errorf("a container launch serving the OpenAI adapter lost %s", refresh)
	}
	if fold := s.FoldFor("codex"); !foldHas(fold, awsURI) || !foldHas(fold, awsToken) {
		t.Errorf("a container launch serving aws-auth lost the bedrock pointer: %+v", fold)
	}
	if lines := UnservedLines(s, nil, nil); lines != nil {
		t.Errorf("a container launch serving both named something unserved: %v", lines)
	}

	nothing := NothingServed()
	s = compose(&nothing)
	if _, ok := s.DeliveredPackEnv(refresh); ok {
		t.Errorf("%s is delivered at a notch that serves no jail daemon", refresh)
	}
	fold := s.FoldFor("codex")
	for _, k := range []string{refresh, awsURI, awsToken} {
		if foldHas(fold, k) {
			t.Errorf("%s is in codex's fold at a notch that serves no jail daemon", k)
		}
	}
	if !foldHas(fold, "CODEX_NON_INTERACTIVE") {
		t.Error("a variable that points at no daemon was withheld too")
	}
	lines := strings.Join(UnservedLines(s, []string{"pz"}, nil), "\n")
	for _, want := range []string{refresh, awsURI, awsToken, `"openai-auth-broker"`, `"aws-auth"`,
		`profile "pz"'s via`, "never at the host or on macos-user"} {
		if !strings.Contains(lines, want) {
			t.Errorf("the unserved disclosure does not name %s:\n%s", want, lines)
		}
	}

	// A container launch whose payload lacks the daemon (aws-auth left disabled) withholds its
	// pointer too, saying why in that notch's terms.
	partial := ServedAtContainer([]string{"openai-auth-broker"}).WithListen(declaredListen)
	s = compose(&partial)
	if foldHas(s.FoldFor("codex"), awsURI) {
		t.Error("a launch that does not run aws-auth delivered its pointer")
	}
	if l := strings.Join(UnservedLines(s, nil, nil), "\n"); !strings.Contains(l, "this launch does not run") {
		t.Errorf("the partial disclosure = %s", l)
	}

	// No served set composes as declared, which is every caller that is not a launch.
	s = compose(nil)
	if _, ok := s.DeliveredPackEnv(refresh); !ok {
		t.Error("composing as declared withheld a pointer")
	}
}

// The override check asks the same question: a pointer this notch withholds has nothing to be
// overridden, so a bearer beside it is no refusal there.
func TestAnOverrideOfAnUnservedPointerIsNoFinding(t *testing.T) {
	packs := embeddedNamed(t, "claude", "aws-auth")
	profiles := map[string]string{"claude": "bedrock"}
	look := func(name string) (string, bool) {
		if name == "AWS_BEARER_TOKEN_BEDROCK" {
			return FromEnvSources, true
		}
		return "", false
	}
	served := ServedAtContainer([]string{"aws-auth"})
	if f := EnvOverrideFindings(packs, profiles, look, nil, &served); len(f) == 0 {
		t.Fatal("a container launch serving aws-auth did not refuse the bearer beside its pointer")
	}
	nothing := NothingServed()
	if f := EnvOverrideFindings(packs, profiles, look, nil, &nothing); len(f) != 0 {
		t.Errorf("a notch that withholds the pointer refused over it: %+v", f)
	}
}
