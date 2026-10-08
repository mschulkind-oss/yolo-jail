package run

// sealneeds_test.go pins THE SEAL (seal.go, docs/design/forked-programs-as-packs.md FP-D9) for
// the packs a fork's BASE brings in. A build jail's selection is narrowed to the fork and its base
// (Options.OnlyPacks), and the base's `needs` still join under it: pi on Bedrock needs bedrock,
// openai-auth and, through bedrock, aws-auth, each of which declares env, host reads or a
// credential pointer. The fixture is the shape the fork build act hands a fork of pi: OnlyPacks
// names the fork and its base, and the fork pack itself is absent from this config, so only the
// base survives the narrowing, as it does in the real one.

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	yoloruntime "github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// sealNeedsFixture selects pi on Bedrock beside claude, with a ~/.aws holding a region and a
// secret, pi's host settings and host briefing holding secrets, an inline env_sources value and
// the aws-auth loophole enabled.
var sealNeedsFixture = sealFixture{
	moduleNames: []string{"pi", "claude", "bedrock", "openai-auth", "aws-auth", "wire-bridge"},
	files: map[string]string{
		".aws/config":             "[profile sealtest-profile]\nregion = us-sealtest-8\n",
		".aws/credentials":        "[default]\naws_access_key_id = AKIASEALTEST\naws_secret_access_key = sealtest-aws-secret\n",
		".pi/agent/settings.json": `{"SEALTEST_PI_SETTINGS": "sealtest-pi-settings-secret"}`,
		".pi/agent/AGENTS.md":     "the user's own pi house rules\n",
	},
	config: `{
  "packs": ["pi", "claude"],
  "profile": {"pi": "bedrock"},
  "host_files": ["~/.pi/agent/AGENTS.md"],
  "env_sources": [{"AWS_PROFILE": "sealtest-profile", "AWS_REGION": "us-sealtest-9"}],
  "loopholes": {"aws-auth": {"enabled": true, "settings": {"profile": "sealtest-profile", "unnarrowed": true}}}
}
`,
	only: []string{"pi-fork", "pi"},
}

// sealNeedsSecrets is every value of the needs fixture's that is the user's own.
var sealNeedsSecrets = []string{
	"us-sealtest-8", "us-sealtest-9", "AKIASEALTEST", "sealtest-aws-secret", "sealtest-profile",
	"sealtest-pi-settings-secret", "the user's own pi house rules",
}

// sealNeedsClosure is what the launch prints for the packs pi's needs bring in.
var sealNeedsClosure = []string{"+ bedrock (needed by pi)", "+ openai-auth (needed by pi)", "+ aws-auth (needed by bedrock)"}

// sealNeedsWithheld is the line a sealed launch of the needs fixture prints for what it withheld
// (sealedWithheldLine): counts of the env vars and host reads, and the three packs declaring them.
var sealNeedsWithheld = regexp.MustCompile(`Sealed build: [1-9][0-9]* pack env vars? and [1-9][0-9]* host reads? ` +
	`declared by pi, bedrock, aws-auth (is|are) withheld \(FP-D9`)

func TestASealedBuildIsHandedNothingItsBasesNeedsDeclare(t *testing.T) {
	argv, ws, home, printed := sealNeedsFixture.launch(t, true)
	cname := yoloruntime.FromWorkspace(ws)

	// THE CLOSURE RUNS UNDER THE SEAL, so each joined pack's declarations are in the launch; claude
	// was narrowed away, and its own need on wire-bridge with it.
	for _, want := range sealNeedsClosure {
		if !strings.Contains(printed, want) {
			t.Errorf("the closure line %q is not printed, so the fixture does not exercise it:\n%s", want, printed)
		}
	}
	if strings.Contains(printed, "+ wire-bridge") {
		t.Errorf("wire-bridge joined a sealed build narrowed to pi:\n%s", printed)
	}

	// THE DISCLOSURE DESCRIBES WHAT CROSSES, and none of those packs' claims do: no env claim, no
	// host read, no credential pointer with its {caller_token} and {listen} left unrendered. The
	// launch says in one line what it withheld instead.
	for _, never := range []string{"SETS an environment variable", "READS a file from YOUR HOME",
		"{caller_token}", "{listen}", "AWS_CONTAINER_"} {
		if strings.Contains(printed, never) {
			t.Errorf("a sealed launch discloses %q, which the seal withholds:\n%s", never, printed)
		}
	}
	if !sealNeedsWithheld.MatchString(printed) {
		t.Errorf("a sealed launch does not say what it withheld (want a line matching %s):\n%s", sealNeedsWithheld, printed)
	}

	// NOTHING OF THEIRS CROSSES: the -e allowlist, no bind of the host home, and no value of the
	// user's in any byte the jail can read or in the argv.
	for _, e := range argvValues(argv, "-e") {
		name, _, _ := strings.Cut(e, "=")
		if !sealedEnvAllowlist[name] {
			t.Errorf("a sealed launch hands the jail -e %s, which is not on the seal's allowlist", name)
		}
	}
	for dest, src := range sealedBindSources(argv) {
		if sealWithin(home, src) && !sealWithin(ws, src) && !sealWithin(filepath.Join(paths.AgentsDir(), cname), src) {
			t.Errorf("a sealed launch binds the host home's %s at %s", src, dest)
		}
	}
	for _, root := range sealedJailReadable(ws) {
		for _, hit := range grepTree(t, root, sealNeedsSecrets...) {
			t.Errorf("a sealed launch wrote the user's own value where its jail reads it: %s", hit)
		}
	}
	for _, hit := range argvCarrying(argv, sealNeedsSecrets...) {
		t.Errorf("a sealed launch's argv carries the user's own value: %s", hit)
	}
	if strings.Contains(printed, "runs pack code on your machine") {
		t.Errorf("a sealed launch disclosed a host-service start:\n%s", printed)
	}
}

// TestTheNeedsFixtureCrossesUnsealed is the needs fixture's own proof: unsealed, the same
// selection discloses those packs' env, host reads and credential pointers, and hands the jail the
// user's values, so the sealed test's silence and absence are the seal's doing.
func TestTheNeedsFixtureCrossesUnsealed(t *testing.T) {
	argv, ws, home, printed := sealNeedsFixture.launch(t, false)
	for _, want := range sealNeedsClosure {
		if !strings.Contains(printed, want) {
			t.Errorf("the unsealed fixture does not print %q:\n%s", want, printed)
		}
	}
	for _, want := range []string{"Pack environment this launch:", "AWS_CONTAINER_AUTHORIZATION_TOKEN=",
		"AWS_CONTAINER_CREDENTIALS_FULL_URI=", ".aws/config", ".pi/agent/settings.json", ".pi/agent/AGENTS.md"} {
		if !strings.Contains(printed, want) {
			t.Errorf("the unsealed fixture does not disclose %q, so that claim is unexercised:\n%s", want, printed)
		}
	}
	if strings.Contains(printed, "Sealed build:") {
		t.Errorf("an unsealed launch prints the seal's withheld line:\n%s", printed)
	}
	var crossed []string
	for _, root := range sealedJailReadable(ws) {
		crossed = append(crossed, grepTree(t, root, sealNeedsSecrets...)...)
	}
	crossed = append(crossed, argvCarrying(argv, sealNeedsSecrets...)...)
	for _, secret := range []string{"sealtest-profile", "us-sealtest-9"} {
		if !strings.Contains(strings.Join(crossed, "\n"), secret) {
			t.Errorf("the unsealed fixture hands the jail no %q, so that crossing is unexercised:\n%s",
				secret, strings.Join(crossed, "\n"))
		}
	}
	// pi's reads-host settings cross as a bind of the host file, not as bytes the launch writes.
	settings := filepath.Join(home, ".pi", "agent", "settings.json")
	houseRules := filepath.Join(home, ".pi", "agent", "AGENTS.md")
	var settingsBound, houseRulesBound bool
	for _, src := range sealedBindSources(argv) {
		settingsBound = settingsBound || src == settings
		houseRulesBound = houseRulesBound || src == houseRules
	}
	if !settingsBound {
		t.Errorf("the unsealed fixture does not bind %s, so the reads-host site is unexercised: %v",
			settings, sealedBindSources(argv))
	}
	if !houseRulesBound {
		t.Errorf("the unsealed fixture does not bind %s, so the user's host file is unexercised: %v",
			houseRules, sealedBindSources(argv))
	}
}
