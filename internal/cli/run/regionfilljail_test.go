package run

// regionfilljail_test.go pins THE REGION FILL at the jail notch (docs/design/bedrock-plumbing.md
// BR-DIR1): the channel every arm composes (composePackChannel, for the fresh container launch,
// the attach and every macos-user invocation) hands the credential gate the launcher's region
// file, so claude on `bedrock` with no region anywhere else is given the one the host's AWS
// config holds for its profile — in its own env file on a container, in the macos-user session —
// the region pre-flight counts it, and the launch says where it came from. The gate's own rule is
// pinned in packload (regionfill_test.go); this file fails if the channel stops handing the gate
// a source, or the disclosure stops being printed.
//
// The AWS config is an invented fixture under the test's temp HOME, reached through the launch
// environment the Options carry: no test reads the machine's ~/.aws.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeAWSConfig writes body as home/.aws/config.
func writeAWSConfig(t *testing.T, home, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(home, ".aws"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".aws", "config"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestAJailLaunchDeliversTheRegionOfTheHostsAWSConfig(t *testing.T) {
	home := retireHome(t)
	writeUserPacks(t, home, `[]`)
	writeAWSConfig(t, home, "[default]\nregion = eu-north-1\n\n[profile team]\nregion = ap-northeast-1\n")
	var stderr bytes.Buffer
	o := retireOptions(t, &stderr)
	o.Getenv = shellWith(map[string]string{"HOME": home})
	packs := bedrockOnClaude(t, o)

	channel := channelFor(t, o, newConfig(), packs, emptyEnv())
	if lines, refuse := o.checkProviderCredentials(newConfig(), packs, channel, nil); refuse || len(lines) != 0 {
		t.Fatalf("a region in the host's ~/.aws/config must satisfy the pre-flight:\n%s", strings.Join(lines, "\n"))
	}
	if got := agentEnvFileContent(channel, "claude"); !strings.Contains(got, "AWS_REGION") ||
		!strings.Contains(got, "'eu-north-1'") {
		t.Errorf("claude's env file must carry the default profile's region:\n%s", got)
	}
	if v, _ := channel.launchEnv("claude").Get("AWS_REGION"); v != "eu-north-1" {
		t.Errorf("the macos-user session must carry claude's AWS_REGION=eu-north-1, got %v", v)
	}
	if _, found := channel.launchEnv("bash").Get("AWS_REGION"); found {
		t.Error("the region is claude's, not every process's")
	}
	o.noteCredentialScope(channel)
	if want := `Region: AWS_REGION=eu-north-1 for claude on provider "bedrock", read from ~/.aws/config [default]`; !strings.Contains(stderr.String(), want) {
		t.Errorf("the launch must say where the region came from, %q:\n%s", want, stderr.String())
	}

	// THE PROFILE THE AGENT RECEIVES picks the section: an env_sources AWS_PROFILE, which the
	// provider claims, so it reaches claude.
	withProfile := channelFor(t, o, newConfig(), packs, userEnvWith(map[string]string{"AWS_PROFILE": "team"}))
	if got := agentEnvFileContent(withProfile, "claude"); !strings.Contains(got, "'ap-northeast-1'") {
		t.Errorf("claude on AWS_PROFILE=team must get [profile team]'s region:\n%s", got)
	}

	// A FILE WITH NO REGION FOR THE PROFILE is the refusal, naming the file and the profile.
	writeAWSConfig(t, home, "[profile team]\nregion = ap-northeast-1\n")
	lines, refuse := o.checkProviderCredentials(newConfig(), packs, channelFor(t, o, newConfig(), packs, emptyEnv()), nil)
	got := strings.Join(lines, "\n")
	if !refuse || !strings.Contains(got,
		`~/.aws/config (profile "default", since nothing names another): it has no [profile default] or [default] section`) {
		t.Errorf("a file giving no region must refuse, naming it (refuse=%v):\n%s", refuse, got)
	}
}

// WHAT THE LAUNCHING SHELL CHOSE AND THIS LAUNCH DOES NOT DELIVER is never replaced by the file's
// answer for another profile (BR-D2 kept): a jail's agent receives nothing from the shell yolo was
// launched from, so an AWS_REGION exported there, or an AWS_PROFILE naming the profile the user
// meant, stays the refusal it was, naming it, rather than a launch in the default profile's region
// with a line saying nothing named another. The file is found from that shell (its HOME, its
// AWS_CONFIG_FILE), which is where the host's own config is, and is not read for the agent here.
func TestAJailLaunchDoesNotReplaceARegionOrProfileLeftInTheLaunchShell(t *testing.T) {
	home := retireHome(t)
	writeUserPacks(t, home, `[]`)
	writeAWSConfig(t, home, "[default]\nregion = us-east-2\n\n[profile prod]\nregion = eu-west-1\n")
	o := retireOptions(t, discardBuf())
	packs := bedrockOnClaude(t, o)
	for _, tc := range []struct {
		name  string
		shell map[string]string
		want  []string
	}{
		{"an AWS_REGION exported there", map[string]string{"HOME": home, "AWS_REGION": "ap-south-1"}, []string{
			"AWS_REGION is set in the environment yolo was launched from, which this launch does not deliver to the agent",
			"~/.aws/config was not read: AWS_REGION, set in the environment yolo was launched from, names the region you chose"}},
		{"an AWS_PROFILE exported there", map[string]string{"HOME": home, "AWS_PROFILE": "prod"}, []string{
			`~/.aws/config was not read: AWS_PROFILE=prod is set in the environment yolo was launched from`,
			"AWS_PROFILE=prod in an env_sources entry"}},
		{"both", map[string]string{"HOME": home, "AWS_REGION": "ap-south-1", "AWS_PROFILE": "prod"}, []string{
			"~/.aws/config was not read: AWS_REGION, set in the environment yolo was launched from, names the region you chose"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o.Getenv = shellWith(tc.shell)
			channel := channelFor(t, o, newConfig(), packs, emptyEnv())
			if got := agentEnvFileContent(channel, "claude"); strings.Contains(got, "AWS_REGION") {
				t.Errorf("claude must not be given the file's region over the shell's choice:\n%s", got)
			}
			lines, refuse := o.checkProviderCredentials(newConfig(), packs, channel, nil)
			got := strings.Join(lines, "\n")
			if !refuse {
				t.Fatalf("the launch must be refused:\n%s", got)
			}
			for _, want := range tc.want {
				if !strings.Contains(got, want) {
					t.Errorf("the refusal must say %q:\n%s", want, got)
				}
			}
			if strings.Contains(got, "under [default] in ~/.aws/config") {
				t.Errorf("the file is not offered as a remedy while the shell's choice keeps it unread:\n%s", got)
			}
		})
	}

	// DELIVERED, it chooses: an env_sources AWS_PROFILE, which the provider claims, reaches claude.
	o.Getenv = shellWith(map[string]string{"HOME": home, "AWS_PROFILE": "prod"})
	channel := channelFor(t, o, newConfig(), packs, userEnvWith(map[string]string{"AWS_PROFILE": "prod"}))
	if got := agentEnvFileContent(channel, "claude"); !strings.Contains(got, "'eu-west-1'") {
		t.Errorf("an AWS_PROFILE delivered through env_sources must choose [profile prod]:\n%s", got)
	}
	// AND THE DEFAULT PROFILE NAMED THERE is the one read anyway.
	o.Getenv = shellWith(map[string]string{"HOME": home, "AWS_PROFILE": "default"})
	if got := agentEnvFileContent(channelFor(t, o, newConfig(), packs, emptyEnv()), "claude"); !strings.Contains(got, "'us-east-2'") {
		t.Errorf("AWS_PROFILE=default in the launch shell names the profile the jail reads anyway:\n%s", got)
	}
}
