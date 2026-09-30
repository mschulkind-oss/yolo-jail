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
		`~/.aws/config (profile "default", since nothing names another): it has no [default] section`) {
		t.Errorf("a file giving no region must refuse, naming it (refuse=%v):\n%s", refuse, got)
	}
}
