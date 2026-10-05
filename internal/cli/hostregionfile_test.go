package cli

// hostregionfile_test.go pins THE REGION FILL at the host notch (docs/design/bedrock-plumbing.md
// BR-DIR1, "the same on the host in the jail"): `yolo host -p bedrock -- claude` with no region
// on the provider, in env_sources or in the invoking shell is given the region the host's AWS
// config holds for its profile, as a jail launch is — so it is not refused, and it says where
// the region came from. Driven through hostMain to the exec (hostGateRunIn), so deleting the
// composition's region source, or the disclosure block, fails here; and through `yolo host env`
// (hostEnvDelta), the observe verb, which must say the same.
//
// The AWS config is an invented fixture under the cell's temp HOME: no test reads the machine's
// ~/.aws, and hostGateHome blanks AWS_PROFILE and AWS_CONFIG_FILE in the inherited shell.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// awsConfigIn returns a prepare hook writing body as home/.aws/config.
func awsConfigIn(body string) func(home string) {
	return func(home string) {
		if err := os.MkdirAll(filepath.Join(home, ".aws"), 0o755); err != nil {
			panic(err)
		}
		if err := os.WriteFile(filepath.Join(home, ".aws", "config"), []byte(body), 0o600); err != nil {
			panic(err)
		}
	}
}

const hostAWSConfig = "[default]\nregion = eu-north-1\n\n[profile team]\nregion = ap-northeast-1\n"

func TestHostLaunchTakesTheRegionOfTheHostsAWSConfig(t *testing.T) {
	noRegion := map[string]string{"AWS_REGION": ""}
	rc, env, errs := hostGateRunIn(t, claudeAlone, noRegion, []string{"-p", "bedrock"}, "claude",
		awsConfigIn(hostAWSConfig))
	if rc != 0 || env == nil {
		t.Fatalf("a region in ~/.aws/config must let yolo host -p bedrock -- claude run: rc=%d\n%s", rc, errs)
	}
	if env["AWS_REGION"] != "eu-north-1" {
		t.Errorf("claude was handed AWS_REGION=%q, want the default profile's eu-north-1", env["AWS_REGION"])
	}
	if want := `yolo host: Region: AWS_REGION=eu-north-1 for claude on provider "bedrock", read from ~/.aws/config [default]`; !strings.Contains(errs, want) {
		t.Errorf("the host launch must say where the region came from, %q:\n%s", want, errs)
	}

	// THE PROFILE THE AGENT INHERITS picks the section: an AWS_PROFILE in the invoking shell,
	// which the exec'd agent receives.
	rc, env, errs = hostGateRunIn(t, claudeAlone, map[string]string{"AWS_REGION": "", "AWS_PROFILE": "team"},
		[]string{"-p", "bedrock"}, "claude", awsConfigIn(hostAWSConfig))
	if rc != 0 || env["AWS_REGION"] != "ap-northeast-1" {
		t.Errorf("claude on AWS_PROFILE=team must get [profile team]'s region: rc=%d AWS_REGION=%q\n%s",
			rc, env["AWS_REGION"], errs)
	}

	// A REGION THE AGENT ALREADY INHERITS wins, and the file is not named.
	rc, env, errs = hostGateRunIn(t, claudeAlone, map[string]string{"AWS_REGION": "us-west-2"},
		[]string{"-p", "bedrock"}, "claude", awsConfigIn(hostAWSConfig))
	if rc != 0 || env["AWS_REGION"] != "us-west-2" || strings.Contains(errs, "Region: ") {
		t.Errorf("the shell's AWS_REGION must win over the file, with nothing disclosed: rc=%d AWS_REGION=%q\n%s",
			rc, env["AWS_REGION"], errs)
	}

	// AN ENV_SOURCES NULL removing AWS_REGION removes the shell's region, and the file's region
	// fills the gap. The null ranks with env_sources in the one ordered composition (packload's
	// envcompose.go) and never removes a shape var, which the filled region is, so BR-D26's one
	// gap — the null removing the filled region and the launch refusing — is closed.
	rc, env, errs = hostGateRunIn(t, `{"packs": ["claude"], "env_sources": [{"AWS_REGION": null}]}`,
		map[string]string{"AWS_REGION": "us-west-2"}, []string{"-p", "bedrock"}, "claude", awsConfigIn(hostAWSConfig))
	if rc != 0 || env["AWS_REGION"] != "eu-north-1" {
		t.Errorf("a null removing the shell's AWS_REGION must leave the file's region to fill it: rc=%d AWS_REGION=%q\n%s",
			rc, env["AWS_REGION"], errs)
	}

	// A FILE WITH NO REGION FOR THE PROFILE is the refusal, naming the file and the profile.
	rc, env, errs = hostGateRunIn(t, claudeAlone, noRegion, []string{"-p", "bedrock"}, "claude",
		awsConfigIn("[profile team]\nregion = ap-northeast-1\n"))
	if rc != 1 || env != nil || !strings.Contains(errs,
		`~/.aws/config (profile "default", since nothing names another): it has no [profile default] or [default] section`) {
		t.Errorf("a file giving no region must refuse, naming it: rc=%d\n%s", rc, errs)
	}
}

// `yolo host env`, the observe verb, composes the same region and discloses it the same way.
func TestHostEnvShowsTheRegionOfTheHostsAWSConfig(t *testing.T) {
	home := hostGateHome(t, claudeAlone, map[string]string{"AWS_REGION": ""})
	awsConfigIn(hostAWSConfig)(home)
	vars, disclosure, err := hostEnvDelta("claude", "bedrock", nil, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	region := ""
	for _, v := range vars {
		if v.Key == "AWS_REGION" && !v.Unset {
			region = v.Value
		}
	}
	if region != "eu-north-1" {
		t.Errorf("yolo host env must export AWS_REGION=eu-north-1, got %q", region)
	}
	if joined := strings.Join(disclosure, "\n"); !strings.Contains(joined, "Region: AWS_REGION=eu-north-1 for claude") {
		t.Errorf("yolo host env must disclose the region's source:\n%s", joined)
	}
}

// A PROFILE THE AGENT DOES NOT RECEIVE chooses nothing: an env_sources null removing AWS_PROFILE
// takes the shell's value out of the exec'd agent's environment, so its SDK resolves the default
// profile, and the region the fill reads is the default profile's. Reading the shell's profile
// there handed claude [profile team]'s region beside the default profile's credential.
func TestHostRegionFillReadsNoProfileTheAgentDoesNotReceive(t *testing.T) {
	rc, env, errs := hostGateRunIn(t, `{"packs": ["claude"], "env_sources": [{"AWS_PROFILE": null}]}`,
		map[string]string{"AWS_REGION": "", "AWS_PROFILE": "team"}, []string{"-p", "bedrock"}, "claude",
		awsConfigIn(hostAWSConfig))
	if rc != 0 || env == nil {
		t.Fatalf("the default profile's region must let the launch run: rc=%d\n%s", rc, errs)
	}
	if _, found := env["AWS_PROFILE"]; found {
		t.Fatalf("the null must remove AWS_PROFILE from claude's environment, got %q", env["AWS_PROFILE"])
	}
	if env["AWS_REGION"] != "eu-north-1" {
		t.Errorf("claude, which receives no AWS_PROFILE, was handed AWS_REGION=%q, want the default profile's eu-north-1",
			env["AWS_REGION"])
	}
	if want := `[default] (profile "default", since nothing names another)`; !strings.Contains(errs, want) {
		t.Errorf("the disclosure must name the default profile, %q:\n%s", want, errs)
	}
}

// OVER THE ACTIVE SET (docs/design/active-provider-sets.md AP-P1): `yolo host -p pi=zai,bedrock
// -- pi` gives pi the file's region for its Bedrock entry, and a file giving none refuses, naming
// it — the lookup the fill read for that entry is the one the host's region ask for it carries
// (regionGaps' RegionFileFor).
func TestHostLaunchTakesTheRegionOfTheHostsAWSConfigForALaterEntry(t *testing.T) {
	const cfg = `{"packs": ["pi", "zai", "bedrock"], "env_sources": [{"ZAI_API_KEY": "tok-zai"}]}`
	noRegion := map[string]string{"AWS_REGION": ""}
	rc, env, errs := hostGateRunIn(t, cfg, noRegion, []string{"-p", "pi=zai,bedrock"}, "pi",
		awsConfigIn(hostAWSConfig))
	if rc != 0 || env["AWS_REGION"] != "eu-north-1" {
		t.Fatalf("pi's Bedrock entry must take the default profile's region: rc=%d AWS_REGION=%q\n%s",
			rc, env["AWS_REGION"], errs)
	}
	rc, env, errs = hostGateRunIn(t, cfg, noRegion, []string{"-p", "pi=zai,bedrock"}, "pi",
		awsConfigIn("[profile team]\nregion = ap-northeast-1\n"))
	if rc != 1 || env != nil || !strings.Contains(errs,
		`~/.aws/config (profile "default", since nothing names another): it has no [profile default] or [default] section`) {
		t.Errorf("a file giving no region must refuse pi's Bedrock entry, naming it: rc=%d\n%s", rc, errs)
	}
}
