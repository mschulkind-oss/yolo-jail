package config

// awssharedconfiggrant_test.go pins the ~/.aws grant conflict — the second of the two
// `Forbidden` clauses in docs/design/sso-backed-bedrock.md §8.
//
// ⚠ EVERY TEST HERE DECLARES ITS SCOPE, and that is not ceremony. This validator is an
// ERROR ON THE HOST and a WARNING IN A JAIL, this repo is developed from inside its own
// jail, and CI is a host — so a test that asserted over `errs` alone would exercise the
// warning arm, pass locally, and take every CI runner red. hostscope_test.go carries the
// whole story and the two helpers; both arms are pinned below, because a sweep that
// applied hostScope everywhere would leave the downgrade itself untested.

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/awschain"
)

// awsServer is a resolver whose one loophole declares the container-credentials
// capability. The NAME is deliberately not "aws-auth": the rule keys on the declared
// job, never on a loophole name (AGENTS.md forbids the switch, and the plan opened a
// Blocker on it), so a fixture named after the shipped pack could not tell the two
// apart. TestSharedConfigGrantIgnoresANameItDoesNotServe is the other half of that.
func awsServer(name string, defaultEnabled bool) fakeResolver {
	return fakeResolver{name: {
		Name:           name,
		HasHostDaemon:  true,
		Serves:         []string{awschain.ContainerCredentialsCapability},
		DefaultEnabled: defaultEnabled,
	}}
}

// grantConfig is a config with one ~/.aws host_files entry and the loopholes block the
// caller supplies.
func grantConfig(t *testing.T, dest, loopholesJSON string) string {
	t.Helper()
	return `{"host_files": [{"path": "` + dest + `", "source": "~/` + dest +
		`"}], "loopholes": ` + loopholesJSON + `}`
}

func containsSub(list []string, sub string) bool {
	for _, s := range list {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// TestSharedConfigGrantIsFatalOnAHost is the arm CI runs, and the arm that makes this a
// LAUNCH REFUSAL as well as a `yolo check` line. A warning would have been only the
// second (the plan's Blockers item 4), and §8 lists the clause under *Forbidden*.
func TestSharedConfigGrantIsFatalOnAHost(t *testing.T) {
	hostScope(t)
	errs, warns := ValidateConfig(
		decode(t, grantConfig(t, ".aws", `{"creds": {"enabled": true}}`)),
		t.TempDir(), awsServer("creds", false))

	if !containsSub(errs, "~/.aws") {
		t.Fatalf("a ~/.aws grant beside an enabled container-credentials loophole must be an "+
			"ERROR on a host — it disables the loophole while looking like it works.\n"+
			"errs=%v\nwarns=%v", errs, warns)
	}
	joined := strings.Join(errs, "\n")
	for _, want := range []string{
		"config.host_files[0]",                  // which entry
		"creds",                                 // which loophole
		"config.loopholes.creds.enabled",        // what switched it on
		awschain.ContainerCredentialsCapability, // the job, which is what the rule keys on
		"DISABLES",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("the refusal does not name %q:\n%s", want, joined)
		}
	}
}

// TestSharedConfigGrantWarnsInsideAJail pins the downgrade itself. Without it a sweep
// that applied hostScope everywhere would leave the in-jail arm — the whole reason the
// downgrade exists — with no test at all.
//
// The downgrade is right here for the reason its suffix states: this conflict is normally
// declared at USER scope (a source-bearing host_files entry is user-scope only), and in a
// jail the user config is the host-generated snapshot, so erroring would refuse every
// nested launch over an entry the in-jail user cannot fix at its source.
func TestSharedConfigGrantWarnsInsideAJail(t *testing.T) {
	jailScope(t)
	errs, warns := ValidateConfig(
		decode(t, grantConfig(t, ".aws", `{"creds": {"enabled": true}}`)),
		t.TempDir(), awsServer("creds", false))

	if containsSub(errs, "~/.aws") {
		t.Errorf("in a jail the grant conflict must WARN, not error — an error refuses every "+
			"nested launch over the host's snapshot:\nerrs=%v", errs)
	}
	if !containsSub(warns, "~/.aws") {
		t.Fatalf("the warning must still be produced in a jail — a downgrade is not a "+
			"deletion:\nwarns=%v", warns)
	}
	if !containsSub(warns, "fix it host-side") {
		t.Errorf("the in-jail warning must say where the fix lives:\nwarns=%v", warns)
	}
}

// TestSharedConfigGrantNeedsTheLoopholeToBeOn covers the three ways a config has the
// grant and no conflict. `packs/aws-auth` ships `default_enabled: false`, so the middle
// case is the ordinary one: selecting the pack serves nothing, and refusing a ~/.aws
// grant beside it would refuse a launch over a loophole that never runs.
func TestSharedConfigGrantNeedsTheLoopholeToBeOn(t *testing.T) {
	cases := []struct {
		name           string
		loopholes      string
		defaultEnabled bool
	}{
		{"switched off by the user", `{"creds": {"enabled": false}}`, true},
		{"the pack author's default is off and nobody asked", `{}`, false},
		{"switched on by the user, off by default — still on", `{"creds": {"enabled": true}}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hostScope(t)
			errs, _ := ValidateConfig(
				decode(t, grantConfig(t, ".aws", tc.loopholes)),
				t.TempDir(), awsServer("creds", tc.defaultEnabled))
			refused := containsSub(errs, "~/.aws")
			want := tc.name == "switched on by the user, off by default — still on"
			if refused != want {
				t.Errorf("refused=%v want=%v — `loopholes.<name>.enabled` outranks the "+
					"manifest default in BOTH directions:\nerrs=%v", refused, want, errs)
			}
		})
	}
}

// TestSharedConfigGrantIgnoresANameItDoesNotServe is the capability half of the rule,
// stated as a test rather than as a comment: a loophole that is enabled and does NOT
// declare the job must not trigger the conflict, whatever it is called.
func TestSharedConfigGrantIgnoresANameItDoesNotServe(t *testing.T) {
	hostScope(t)
	silent := fakeResolver{"aws-auth": {
		Name:           "aws-auth",
		HasHostDaemon:  true,
		DefaultEnabled: true,
		// No `serves`: silence is never a claim (loopholedecl's whole rule).
	}}
	errs, _ := ValidateConfig(
		decode(t, grantConfig(t, ".aws", `{"aws-auth": {"enabled": true}}`)),
		t.TempDir(), silent)
	if containsSub(errs, "~/.aws") {
		t.Errorf("a loophole that declares no capability triggered the conflict — the rule "+
			"is keyed on the job, and a name check is the switch AGENTS.md forbids:\n%v", errs)
	}
}

// TestSharedConfigGrantIgnoresUnrelatedDestinations: the false-positive direction. A
// `.awsfoo` grant is not an AWS shared config, and refusing a launch over it would be a
// worse failure than the one the rule prevents — the user cannot make it go away.
func TestSharedConfigGrantIgnoresUnrelatedDestinations(t *testing.T) {
	for _, dest := range []string{".awsfoo", ".aws-backup", ".config/aws/config"} {
		t.Run(dest, func(t *testing.T) {
			hostScope(t)
			errs, _ := ValidateConfig(
				decode(t, grantConfig(t, dest, `{"creds": {"enabled": true}}`)),
				t.TempDir(), awsServer("creds", true))
			if containsSub(errs, "does not duplicate the loophole") {
				t.Errorf("a %s grant was refused as an AWS shared config:\n%v", dest, errs)
			}
		})
	}
}

// TestSharedConfigGrantCatchesASingleFileAndASourcelessEntry. The predicate is the JAIL
// DESTINATION, not the host source, and both halves of that matter: an entry seeding
// ~/.aws/config from inline `content` is exactly as disabling as one mounted from the
// host's own directory, because what decides whether `fromIni` answers is what the SDK
// finds under $HOME inside the jail.
func TestSharedConfigGrantCatchesASingleFileAndASourcelessEntry(t *testing.T) {
	hostScope(t)
	cfg := `{"host_files": [
	  {"path": ".aws/config", "content": "[default]\nregion = us-east-1\n"}
	], "loopholes": {"creds": {"enabled": true}}}`
	errs, _ := ValidateConfig(decode(t, cfg), t.TempDir(), awsServer("creds", true))
	if !containsSub(errs, "~/.aws/config") {
		t.Errorf("a source-less entry seeding ~/.aws/config did not trip the conflict:\n%v", errs)
	}
}

// TestSharedConfigGrantIndexesTheEntryAsWritten. The index in the message has to mean
// the nth entry in the file: it is what a reader greps for, and validateHostFiles' own
// messages use the same numbering. checkHostFiles DROPS rejected entries, so reading
// positions off its return would shift every index after the first bad one.
func TestSharedConfigGrantIndexesTheEntryAsWritten(t *testing.T) {
	hostScope(t)
	cfg := `{"host_files": [
	  {"path": ".config/ok.json", "content": "{}"},
	  12345,
	  {"path": ".aws", "source": "~/.aws"}
	], "loopholes": {"creds": {"enabled": true}}}`
	errs, _ := ValidateConfig(decode(t, cfg), t.TempDir(), awsServer("creds", true))
	if !containsSub(errs, "config.host_files[2]") {
		t.Errorf("the conflict must name the entry's position AS WRITTEN (index 2 here, "+
			"after a rejected entry):\n%v", errs)
	}
}

// TestSharedConfigGrantIsSilentWithoutAResolver: discovery can degrade to empty (a
// machine with no loopholes, or a resolver that errored), and "I could not look" must
// not read as "nothing conflicts" OR as a refusal. There is nothing to report, because
// nothing is known to be serving.
func TestSharedConfigGrantIsSilentWithoutAResolver(t *testing.T) {
	hostScope(t)
	errs, warns := ValidateConfig(
		decode(t, grantConfig(t, ".aws", `{"creds": {"enabled": true}}`)),
		t.TempDir(), nil)
	if containsSub(errs, "~/.aws") || containsSub(warns, "~/.aws") {
		t.Errorf("a nil resolver produced a conflict report:\nerrs=%v\nwarns=%v", errs, warns)
	}
}
