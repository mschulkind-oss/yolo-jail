package cli

// hostregion_test.go pins the REGION PRE-FLIGHT at the host notch
// (docs/design/bedrock-plumbing.md §8, OQ-BR6): `yolo host -p bedrock -- claude` with no region
// on the provider and none in the environment it would exec is refused before the exec, with the
// same facts and verdict a jail launch prints (packload.ProviderRegionRefusal). Driven through
// hostMain to the exec (hostGateRun), so deleting the call in hostMain fails the refusal half.

import (
	"strings"
	"testing"
)

// hostRegionVerdict is the jail's verdict line, behind this notch's name.
const hostRegionVerdict = "yolo host: Refusing to launch: a selected provider is reached through a " +
	"region, and this launch names none."

// A HOST LAUNCH WITH NO REGION ANYWHERE IS REFUSED, and each place a region can come from at this
// notch lets it through: the invoking shell (which the agent `yolo host` execs inherits, so it
// counts here and never in a jail), env_sources, and the provider's `region` — which the claude
// derive then hands claude as AWS_REGION. An env_sources null removing the shell's AWS_REGION
// removes the region too: the pre-flight reads the environment the exec would hand the agent.
func TestHostLaunchRefusesABedrockProfileWithNoRegion(t *testing.T) {
	noRegion := map[string]string{"AWS_REGION": ""}
	rc, env, errs := hostGateRun(t, claudeAlone, noRegion, []string{"-p", "bedrock"}, "claude")
	if rc != 1 || env != nil {
		t.Fatalf("yolo host -p bedrock -- claude with no region must refuse before the exec: rc=%d reached=%v\n%s",
			rc, env != nil, errs)
	}
	for _, want := range []string{hostRegionVerdict, `pack bedrock requires a region for provider "bedrock"`,
		"neither AWS_REGION nor AWS_DEFAULT_REGION is set", "the environment yolo was launched from",
		"launch anyway with YOLO_ALLOW_MISSING_PROVIDERS=1"} {
		if !strings.Contains(errs, want) {
			t.Errorf("the host refusal must say %q:\n%s", want, errs)
		}
	}
	if strings.Contains(errs, "which this launch does not deliver") {
		t.Errorf("the host execs the invoking shell, so nothing is stranded there:\n%s", errs)
	}

	for _, tc := range []struct {
		name, cfg  string
		shell      map[string]string
		wantRegion string
	}{
		{"the invoking shell", claudeAlone, map[string]string{"AWS_REGION": "us-west-2"}, "us-west-2"},
		{"AWS_DEFAULT_REGION in the shell", claudeAlone, map[string]string{"AWS_REGION": "", "AWS_DEFAULT_REGION": "eu-central-1"}, ""},
		{"env_sources", `{"packs": ["claude"], "env_sources": [{"AWS_REGION": "eu-west-1"}]}`, noRegion, "eu-west-1"},
		{"the provider's region", `{"packs": ["claude"], "providers": {"bedrock": {"region": "ap-south-1"}}}`, noRegion, "ap-south-1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rc, env, errs := hostGateRun(t, tc.cfg, tc.shell, []string{"-p", "bedrock"}, "claude")
			if rc != 0 || env == nil {
				t.Fatalf("a region from %s must let the launch run: rc=%d\n%s", tc.name, rc, errs)
			}
			if strings.Contains(errs, "Refusing to launch") {
				t.Errorf("the launch printed a refusal:\n%s", errs)
			}
			if tc.wantRegion != "" && env["AWS_REGION"] != tc.wantRegion {
				t.Errorf("claude was handed AWS_REGION=%q, want %q", env["AWS_REGION"], tc.wantRegion)
			}
		})
	}

	rc, env, errs = hostGateRun(t, `{"packs": ["claude"], "env_sources": [{"AWS_REGION": null}]}`,
		map[string]string{"AWS_REGION": "us-west-2"}, []string{"-p", "bedrock"}, "claude")
	if rc != 1 || env != nil || !strings.Contains(errs, hostRegionVerdict) {
		t.Errorf("an env_sources null removing the shell's AWS_REGION leaves the agent no region, "+
			"so the launch must refuse: rc=%d reached=%v\n%s", rc, env != nil, errs)
	}
}

// OQ-BR8 AT THE HOST: a user's own profile over the shipped `bedrock` provider and a user's own
// provider declaring "platform": "aws-bedrock" launch claude in its native Bedrock mode exactly
// as `-p bedrock` does — CLAUDE_CODE_USE_BEDROCK from claude's derive, keyed on the provider —
// and the user's provider with no region is refused like the shipped one (the region
// requirement keys on the platform too). Through hostMain to the exec.
func TestHostBedrockFactsFollowTheProviderNotTheProfileName(t *testing.T) {
	const cfg = `{"packs": ["claude"],
	  "providers": {"bedrock": {"region": "us-west-2"},
	                "bedrock-eu": {"platform": "aws-bedrock", "region": "eu-west-1"},
	                "bedrock-bare": {"platform": "aws-bedrock"}},
	  "profiles": {"bedrock-sso": {"provider": "bedrock"}, "eu": {"provider": "bedrock-eu"},
	               "bare": {"provider": "bedrock-bare"}}}`
	noRegion := map[string]string{"AWS_REGION": "", "AWS_DEFAULT_REGION": ""}
	for _, tc := range []struct{ profile, region string }{
		{"bedrock", "us-west-2"}, {"bedrock-sso", "us-west-2"}, {"eu", "eu-west-1"},
	} {
		rc, env, errs := hostGateRun(t, cfg, noRegion, []string{"-p", tc.profile}, "claude")
		if rc != 0 || env == nil {
			t.Fatalf("yolo host -p %s -- claude must run: rc=%d\n%s", tc.profile, rc, errs)
		}
		if env["CLAUDE_CODE_USE_BEDROCK"] != "1" || env["AWS_REGION"] != tc.region {
			t.Errorf("-p %s: claude must run in Bedrock mode in %s, got CLAUDE_CODE_USE_BEDROCK=%q AWS_REGION=%q",
				tc.profile, tc.region, env["CLAUDE_CODE_USE_BEDROCK"], env["AWS_REGION"])
		}
	}
	rc, env, errs := hostGateRun(t, cfg, noRegion, []string{"-p", "bare"}, "claude")
	if rc != 1 || env != nil || !strings.Contains(errs, `requires a region for provider "bedrock-bare" (platform "aws-bedrock")`) {
		t.Errorf("a user Bedrock provider with no region must be refused like the shipped one: rc=%d\n%s", rc, errs)
	}
}

// THE HATCH lets the host launch through, loudly, as it does a jail's; and a launch that selects
// no bedrock owes no region at all.
func TestHostRegionRefusalHatchAndTheUnprofiledControl(t *testing.T) {
	rc, env, errs := hostGateRun(t, claudeAlone,
		map[string]string{"AWS_REGION": "", "YOLO_ALLOW_MISSING_PROVIDERS": "1"}, []string{"-p", "bedrock"}, "claude")
	if rc != 0 || env == nil {
		t.Fatalf("the hatch must let the host launch proceed: rc=%d\n%s", rc, errs)
	}
	if !strings.Contains(errs, "yolo host: Warning: YOLO_ALLOW_MISSING_PROVIDERS is set — CONTINUING, with a "+
		"selected provider's region still unset") {
		t.Errorf("the held notice must say what it suppresses:\n%s", errs)
	}

	rc, env, errs = hostGateRun(t, claudeAlone, map[string]string{"AWS_REGION": ""}, nil, "claude")
	if rc != 0 || env == nil || strings.Contains(errs, "reached through a region") {
		t.Errorf("an unprofiled host launch owes no region: rc=%d\n%s", rc, errs)
	}
}
