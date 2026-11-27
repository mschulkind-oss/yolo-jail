package run

// capturejailcredentials_test.go pins what the INSTALLER capture jail (`yolo capture <bin>`,
// internal/cli's runCaptureJail) hands the vendor installer it runs. That installer is an unpinned
// script fetched from the internet, so the jail it runs in must carry no credential and no
// credential-bearing channel: no provider secret in its environment or its readable files, no host
// service (broker, adapter, intercept) started for it, and no machine-wide shared-credentials
// directory bound into it.
//
// The fixture is seal_test.go's end-to-end launch, run as runCaptureJail drives it (Options.CapturesDir
// returning "", the `capture-run` argv), over a config selecting the claude, codex, pi, copilot and
// bedrock packs with a static Bedrock key pair in the private env set. internal/cli's
// TestTheInstallerCaptureJailIsSealed pins that the installer capture asks for the seal; this file
// asks what the launch then hands the jail. TestTheCredentialFixtureCrossesUnsealed is the
// fixture's own proof that the same launch without the seal crosses.

import (
	"os"
	"strings"
	"testing"

	yoloruntime "github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// capJailSecrets are the values of the fixture's provider key pair: they must be in no byte the
// jail can read and in no argv element.
var capJailSecrets = []string{"capjail-akia-key-id-probe", "capjail-secret-access-key-probe"}

// capJailConfig selects the packs the owner's launch showed (claude, codex, pi, copilot and the
// Bedrock provider), with the static key pair private, as a user's own config holds it.
const capJailConfig = `packs = ["claude", "codex", "pi", "copilot", "bedrock"]

[active_provider]
claude = "bedrock-key"
codex = "bedrock-key"
pi = "bedrock-key"

[providers.bedrock-key]
region = "us-east-1"

[env.secrets]
set = { AWS_ACCESS_KEY_ID = "capjail-akia-key-id-probe", AWS_SECRET_ACCESS_KEY = "capjail-secret-access-key-probe" }
`

// capJailFixture is the capture jail's launch: sealFixture over capJailConfig, the selection whole
// (an installer capture does not narrow it).
func capJailFixture() sealFixture {
	return sealFixture{
		moduleNames: []string{"claude", "codex-auth", "wire-bridge"},
		dirs:        []string{".claude", ".pi"},
		config:      capJailConfig,
	}
}

// TestTheCredentialFixtureCrossesUnsealed is the fixture's own proof: the capture-shaped launch
// with no seal DOES hand the jail the key pair and start host services, so the sealed test below
// fails if a withholding is removed rather than passing over a fixture that crossed nothing.
func TestTheCredentialFixtureCrossesUnsealed(t *testing.T) {
	argv, ws, _, printed := capJailFixture().launch(t, false)
	// The per-agent env files are the provider key's vehicle, removed at teardown: the fixture's
	// fake runtime copies them as the container starts (agentEnvAtRun).
	if hits := grepTree(t, agentEnvAtRun(ws), capJailSecrets...); len(hits) == 0 {
		t.Errorf("the unsealed capture-shaped launch hands the jail no provider key (argv %d words), so that site is unexercised", len(argv))
	}
	bound := sealedBindSources(argv)
	for _, dest := range []string{"/home/agent/.claude-shared-credentials", "/home/agent/.pi-shared-credentials"} {
		if _, ok := bound[dest]; !ok {
			t.Errorf("the unsealed capture-shaped launch does not bind %s, so that site is unexercised", dest)
		}
	}
	if !strings.Contains(printed, keeperLineHolding+"this jail's host services (") {
		t.Errorf("the unsealed capture-shaped launch starts no host service, so that site is unexercised:\n%s", printed)
	}
}

// TestACaptureJailsLaunchCrossesNoCredential is the property: with the seal
// runCaptureJail applies, the container argv, the env the jail reads and every file it can read
// carry no provider secret; no host service (and so no broker socket or adapter) is started; and
// no shared-credentials directory is bound in.
func TestACaptureJailsLaunchCrossesNoCredential(t *testing.T) {
	argv, ws, _, printed := capJailFixture().launch(t, true)
	cname := yoloruntime.JailName(ws, "podman-self")

	for _, hit := range argvCarrying(argv, capJailSecrets...) {
		t.Errorf("the capture jail's argv carries a provider secret: %s", hit)
	}
	for _, root := range sealedJailReadable(ws) {
		for _, hit := range grepTree(t, root, capJailSecrets...) {
			t.Errorf("the capture jail can read a provider secret: %s", hit)
		}
	}
	jail := launchedJailEnv(t, argv, ws)
	for _, name := range []string{"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN",
		"AWS_BEARER_TOKEN_BEDROCK", "AWS_PROFILE", "AWS_CONTAINER_CREDENTIALS_FULL_URI"} {
		if v, ok := jail[name]; ok {
			t.Errorf("the capture jail's environment carries %s=%q", name, v)
		}
	}
	// No host service: no broker socket, no adapter, no intercept CA.
	if strings.Contains(printed, "Starting host services") {
		t.Errorf("the capture jail's launch started host services:\n%s", printed)
	}
	if !strings.Contains(printed, keeperLineHolding+"this jail's container until") {
		t.Errorf("the capture jail's keeper holds more than its container:\n%s", printed)
	}
	if _, err := os.Stat(hostServiceSocketsDir(cname, false)); !os.IsNotExist(err) {
		t.Errorf("the capture jail's launch made the host-services dir (err %v)", err)
	}
	if _, err := os.Stat(servicesAtRun(ws)); err == nil {
		t.Error("host services were running when the capture jail's container started")
	}
	// No shared-credentials directory, and no MCP sign-in share.
	for dest, src := range sealedBindSources(argv) {
		if strings.Contains(src, "shared-credentials") || strings.Contains(dest, "shared-credentials") ||
			strings.Contains(src, "mcp-auth") || strings.Contains(dest, "mcp-auth") {
			t.Errorf("the capture jail binds a credential directory: %s -> %s", src, dest)
		}
	}
	// The channel's disclosure of a credential scope or a shared credential is for a jail that
	// has one: this one has none, and says so only by saying nothing of them.
	for _, line := range []string{"Credential scope:", "shared_credentials[", "MCP sign-ins ("} {
		if strings.Contains(printed, line) {
			t.Errorf("the capture jail's launch discloses %q for a jail that gets none:\n%s", line, printed)
		}
	}
}
