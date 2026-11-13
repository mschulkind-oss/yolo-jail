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
	kept, none := ViaServedAt(in, packs, ServedInJail([]string{"wire-bridge"}))
	if kept["pz"].ViaBase == "" || len(none) != 0 {
		t.Errorf("a container launch serving the bridge lost the via: %+v, cleared %v", kept["pz"], none)
	}
	if out, cleared := ViaServedAt(nil, packs, NothingServed()); out != nil || cleared != nil {
		t.Error("ViaServedAt(nil) must stay nil")
	}
}

// A jail serves exactly the names it is handed (the caller's split, loopholes.JailDaemonsRunIn),
// and macos-user's set is its guest's daemons Plus its launch-owned services: every name of
// either, each listen address, both rebind maps.
func TestServedInJailAndPlus(t *testing.T) {
	guest := ServedInJail([]string{"openai-auth-broker"}).
		WithListen(map[string]string{"openai-auth-broker": "127.0.0.1:50001"})
	if !guest.Serves("openai-auth-broker") || guest.Serves("wire-bridge") || !guest.RunsDaemons() {
		t.Errorf("ServedInJail served the wrong set: %v", guest.Names())
	}
	both := guest.Plus(ServedByLaunch([]string{"wire-bridge"}).
		WithRebind(map[string]string{"127.0.0.1:8214": "127.0.0.1:50002"}))
	if !both.Serves("openai-auth-broker") || !both.Serves("wire-bridge") {
		t.Errorf("Plus lost a name: %v", both.Names())
	}
	if both.Listen("openai-auth-broker") != "127.0.0.1:50001" {
		t.Errorf("Plus lost the guest daemon's listen address: %q", both.Listen("openai-auth-broker"))
	}
	if got := both.ServedURL("http://127.0.0.1:8214/v1"); got != "http://127.0.0.1:50002/v1" {
		t.Errorf("Plus lost the launch service's rebind: %q", got)
	}
	if NothingServed().Serves("wire-bridge") || ServedInJail(nil).Serves("") {
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
	// claude's pack ships the `bedrock` provider whose platform the pointer's gate keys on.
	packs := embeddedNamed(t, "codex", "codex-auth", "aws-auth", "bedrock", "claude")
	profiles := map[string]string{"codex": "bedrock"}
	providers, resolved, _ := launchSelection(t, packs, nil, nil, profiles)
	compose := func(served *ServedDaemons) *CredentialScope {
		t.Helper()
		s, err := ScopeCredentials(ScopeInput{Packs: packs, Profiles: profiles, NoDerives: true, Served: served,
			Providers: providers, Resolved: resolved,
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

	container := ServedInJail([]string{"openai-auth-broker", "aws-auth"}).WithListen(declaredListen)
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
		`profile "pz"'s via`, "never at the host"} {
		if !strings.Contains(lines, want) {
			t.Errorf("the unserved disclosure does not name %s:\n%s", want, lines)
		}
	}

	// A container launch whose payload lacks the daemon (aws-auth left disabled) withholds its
	// pointer too, saying why in that notch's terms.
	partial := ServedInJail([]string{"openai-auth-broker"}).WithListen(declaredListen)
	s = compose(&partial)
	if foldHas(s.FoldFor("codex"), awsURI) {
		t.Error("a launch that does not run aws-auth delivered its pointer")
	}
	if l := strings.Join(UnservedLines(s, nil, nil), "\n"); !strings.Contains(l, "this launch does not run") {
		t.Errorf("the partial disclosure = %s", l)
	}

	// THE HOST NOTCH (AtHost, host-notch-services.md HS-D21) opens a jail daemon's doorway, so
	// what it withholds is worded as a doorway this launch did not open, never as a daemon only a
	// jail runs, and a launch that knows why (WithNotServedWhy) says that instead. With no reason
	// given, the clause is HS-D22's: no selection opens a doorway whose pointer is not gated on
	// the agent's selection, the Codex refresh adapter's. A daemon it serves keeps its pointer, at
	// the address it settled, beside a Plus of the services it runs.
	host := NothingServed().AtHost().WithNotServedWhy(map[string]string{
		"aws-auth": "which this launch does not open, because loophole \"aws-auth\" is disabled"})
	s = compose(&host)
	l := strings.Join(UnservedLines(s, nil, nil), "\n")
	for _, want := range []string{`loophole "aws-auth" is disabled`,
		`"openai-auth-broker" jail daemon, which ` + "`yolo host --`" + ` opens for no selection`, "HS-D22"} {
		if !strings.Contains(l, want) {
			t.Errorf("the host's disclosure does not say %q:\n%s", want, l)
		}
	}
	if strings.Contains(l, "never at the host") {
		t.Errorf("the host's disclosure says no jail daemon runs at the host:\n%s", l)
	}
	// THE LAUNCH'S OWN WORD (LaunchServes), per variable: one it serves itself goes unnamed, one
	// whose own server did not start carries that reason on a line of its own, and a variable of
	// the same daemon it says nothing of keeps the served set's clause.
	const launchWhy = "which this launch's own server serves, and it did not start"
	byLaunch := func(name string) (bool, string) {
		switch name {
		case refresh:
			return true, ""
		case awsURI:
			return false, launchWhy
		}
		return false, ""
	}
	launchLines := UnservedLines(s, nil, byLaunch)
	var uriLine, tokenLine string
	for _, line := range launchLines {
		if strings.Contains(line, refresh) {
			t.Errorf("a variable the launch serves itself is named: %s", line)
		}
		if strings.Contains(line, awsURI) {
			uriLine = line
		}
		if strings.Contains(line, awsToken) {
			tokenLine = line
		}
	}
	if !strings.Contains(uriLine, launchWhy) || strings.Contains(uriLine, awsToken) {
		t.Errorf("%s must carry the launch's reason on a line of its own: %q", awsURI, uriLine)
	}
	if !strings.Contains(tokenLine, `loophole "aws-auth" is disabled`) || strings.Contains(tokenLine, launchWhy) {
		t.Errorf("%s must keep the served set's clause: %q", awsToken, tokenLine)
	}
	doorway := ServedByLaunch([]string{"aws-auth"}).
		WithListen(map[string]string{"aws-auth": "127.0.0.1:40123"}).AtHost()
	served := ServedByLaunch(nil).Plus(doorway)
	s = compose(&served)
	if fold := s.FoldFor("codex"); !foldHas(fold, awsURI) || foldHas(fold, refresh) {
		t.Errorf("a host launch serving the aws-auth doorway: fold %+v, want the AWS pointer "+
			"and not the refresh URL", fold)
	}
	if l := strings.Join(UnservedLines(s, nil, nil), "\n"); strings.Contains(l, awsURI) ||
		!strings.Contains(l, refresh) || !strings.Contains(l, "opens for no selection") {
		t.Errorf("the host's disclosure beside a served doorway = %s", l)
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
	packs := embeddedNamed(t, "claude", "aws-auth", "bedrock")
	profiles := map[string]string{"claude": "bedrock"}
	look := func(name string) (string, bool) {
		if name == "AWS_BEARER_TOKEN_BEDROCK" {
			return FromEnvSources, true
		}
		return "", false
	}
	_, _, sel := launchSelection(t, packs, nil, nil, profiles)
	served := ServedInJail([]string{"aws-auth"})
	if f := EnvOverrideFindings(packs, sel, look, nil, &served); len(f) == 0 {
		t.Fatal("a container launch serving aws-auth did not refuse the bearer beside its pointer")
	}
	nothing := NothingServed()
	if f := EnvOverrideFindings(packs, sel, look, nil, &nothing); len(f) != 0 {
		t.Errorf("a notch that withholds the pointer refused over it: %+v", f)
	}
}

// THE GIT REWRITE EXISTS ONLY WHERE THE GIT-BROKER RUNS (docs/reference/git-broker.md ID-6):
// packs/git's GIT_CONFIG_* contribution is `served_by` its loophole, so the host notch, which
// serves nothing, folds none of it, and a jail serving git-broker folds all eleven keys. Deleting
// `served_by` from the pack's env contribution fails the first half.
func TestGitRewriteWithheldWhereUnserved(t *testing.T) {
	packs := embeddedNamed(t, "git")
	keys := map[string]string{
		"GIT_CONFIG_COUNT":   "5",
		"GIT_CONFIG_KEY_0":   "url.yolo::ssh://.insteadOf",
		"GIT_CONFIG_VALUE_0": "ssh://",
		"GIT_CONFIG_KEY_1":   "url.yolo::https://.insteadOf",
		"GIT_CONFIG_VALUE_1": "https://",
		"GIT_CONFIG_KEY_2":   "url.yolo::git@.insteadOf",
		"GIT_CONFIG_VALUE_2": "git@",
		"GIT_CONFIG_KEY_3":   "url.yolo::git+ssh://.insteadOf",
		"GIT_CONFIG_VALUE_3": "git+ssh://",
		"GIT_CONFIG_KEY_4":   "url.yolo::ssh+git://.insteadOf",
		"GIT_CONFIG_VALUE_4": "ssh+git://",
	}
	compose := func(served ServedDaemons) *CredentialScope {
		t.Helper()
		s, err := ScopeCredentials(ScopeInput{Packs: packs, NoDerives: true, Served: &served})
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	host := compose(NothingServed())
	for k := range keys {
		if v, ok := host.DeliveredPackEnv(k); ok {
			t.Errorf("the host notch folded %s=%q; nothing serves git-broker there", k, v)
		}
	}
	if lines := strings.Join(UnservedLines(host, nil, nil), "\n"); !strings.Contains(lines, "GIT_CONFIG_COUNT") ||
		!strings.Contains(lines, `"git-broker"`) {
		t.Errorf("the withheld rewrite is not named:\n%s", lines)
	}
	// git-broker is a HOST daemon the jail reaches through yolo's front, so its line says so and
	// carries the launch's reason (WithNotServedWhy) as given, next step included.
	const why = "whose loophole is off: run the fix"
	off := strings.Join(UnservedLines(compose(ServedInJail(nil).WithNotServedWhy(
		map[string]string{"git-broker": why})), nil, nil), "\n")
	if !strings.Contains(off, `points at the "git-broker" loophole's host daemon, `+why) ||
		strings.Contains(off, "jail daemon") {
		t.Errorf("the withheld rewrite's line does not name the host daemon and the reason:\n%s", off)
	}
	jail := compose(ServedInJail([]string{"git-broker"}))
	for k, want := range keys {
		if v, ok := jail.DeliveredPackEnv(k); !ok || v != want {
			t.Errorf("a jail serving git-broker folded %s=%q (%v), want %q", k, v, ok, want)
		}
	}
}

// An unconfigured LOGIN LOOPHOLE serves nothing (ServedDaemons.WithoutUnconfiguredLogins): the
// shipped aws-auth, whose env contribution names its `profile` setting, leaves the served set
// with no profile, naming the setting and the verb that writes it, and stays with one. A daemon
// no login contribution points at is untouched, and so is one served by inheritance.
func TestAnUnconfiguredLoginServiceServesNothing(t *testing.T) {
	packs := embeddedNamed(t, "aws-auth")
	served := ServedInJail([]string{"aws-auth", "other"})
	none := func(string, string) string { return "" }

	got := served.WithoutUnconfiguredLogins(packs, none)
	if got.Serves("aws-auth") || !got.Serves("other") {
		t.Fatalf("serves aws-auth=%v other=%v; want only the unconfigured login service dropped",
			got.Serves("aws-auth"), got.Serves("other"))
	}
	if why := got.notServedWhy("aws-auth"); !strings.Contains(why, "`profile`") ||
		!strings.Contains(why, "yolo config set allow.aws-auth.profile") {
		t.Errorf("the withheld pointer's clause does not name the setting and its verb: %q", why)
	}
	if !served.Serves("aws-auth") {
		t.Error("WithoutUnconfiguredLogins modified its receiver")
	}

	profile := func(loophole, key string) string {
		if loophole == "aws-auth" && key == "profile" {
			return "bedrock"
		}
		return ""
	}
	if !served.WithoutUnconfiguredLogins(packs, profile).Serves("aws-auth") {
		t.Error("a login service with a profile was dropped")
	}
	inherited := served.WithInherited(map[string]map[string]string{"aws-auth": {"AWS_CONTAINER_CREDENTIALS_FULL_URI": "http://x"}})
	if !inherited.WithoutUnconfiguredLogins(packs, none).Serves("aws-auth") {
		t.Error("a pointer the launching jail served was dropped")
	}
}
