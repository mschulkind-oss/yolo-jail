package run

// callertokens_test.go pins the launcher half of a pack service's caller token
// (callertokens.go; docs/reference/wire-bridge.md WB-D18): a launch that selects the wire bridge
// mints one, carries it in the 0600 channel and in no argv, composes it into claude's
// ANTHROPIC_AUTH_TOKEN, mints a fresh one per launch, and an attach delivers the RUNNING jail's
// token rather than its own.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/svcendpoint"
)

const bridgeTokenVar = "YOLO_SERVICE_WIRE_BRIDGE_TOKEN"

func bridgedPacks(t *testing.T) []*packload.Pack {
	return []*packload.Pack{
		officialPack(t, "claude"), officialPack(t, "cerebras"), officialPack(t, "wire-bridge"),
	}
}

func selectCerebras(o *Options, _ *jsonx.OrderedMap) { o.ProfileName = "cerebras" }

// channelSection is the per-entry channel section of a yolo-user-env.sh body.
func channelSection(t *testing.T, shared string) string {
	t.Helper()
	_, section, ok := strings.Cut(shared, channelSectionHeader+"\n")
	if !ok {
		t.Fatalf("no channel section in:\n%s", shared)
	}
	return section
}

func TestABridgedLaunchCarriesItsCallerTokenInTheChannelAndNoArgv(t *testing.T) {
	o, _, channel, _ := attachFixture(t, currentJailEnv, bridgedPacks(t), cerebrasKey(), selectCerebras)
	tok := o.callerTokens[bridgeTokenVar]
	if !svcendpoint.IsToken(tok) || channel.callerTokens[bridgeTokenVar] != tok {
		t.Fatalf("the launch minted no usable caller token: settled %q, channel %q", o.callerTokens, channel.callerTokens)
	}
	shared, agents := deliveredFiles(t, channel)
	if !strings.Contains(channelSection(t, shared), "export "+bridgeTokenVar+"='"+tok+"'\n") {
		t.Errorf("the shared channel section does not carry the token the daemon demands:\n%s", shared)
	}
	claude := agents["claude"]
	if !strings.Contains(claude, "export ANTHROPIC_AUTH_TOKEN='"+tok+"'\n") {
		t.Errorf("claude's env file does not send the bridge's caller token as ANTHROPIC_AUTH_TOKEN:\n%s", claude)
	}
	if strings.Contains(claude, "ANTHROPIC_AUTH_TOKEN='csk-test'") {
		t.Errorf("claude sends the cerebras key to the bridge's loopback port:\n%s", claude)
	}

	// The argv is what `podman inspect` prints to anyone who can ask: the token is never on it.
	la := zaiLaunchAssembled(t, bridgedPacks(t), bareConfig(), cerebrasKey(),
		func(o *Options) { o.ProfileName = "cerebras" })
	argvTok := la.o.callerTokens[bridgeTokenVar]
	if !svcendpoint.IsToken(argvTok) {
		t.Fatalf("the assembled launch minted no caller token: %q", la.o.callerTokens)
	}
	for _, a := range la.argv {
		if strings.Contains(a, argvTok) {
			t.Errorf("the caller token is on the container argv: %q", a)
		}
	}
}

func TestEveryLaunchMintsItsOwnCallerToken(t *testing.T) {
	o1, cfg, c1, _ := attachFixture(t, currentJailEnv, bridgedPacks(t), cerebrasKey(), selectCerebras)
	_, _, c2, _ := attachFixture(t, currentJailEnv, bridgedPacks(t), cerebrasKey(), selectCerebras)
	if c1.callerTokens[bridgeTokenVar] == c2.callerTokens[bridgeTokenVar] {
		t.Errorf("two launches share caller token %q: it must rotate per launch", c1.callerTokens[bridgeTokenVar])
	}
	// ONE launch that composes twice (Run's composition, then an attach's over the running
	// jail's packs) settles on one token, so a jail is never handed two.
	again := channelFor(t, o1, cfg, bridgedPacks(t), cerebrasKey())
	if again.callerTokens[bridgeTokenVar] != c1.callerTokens[bridgeTokenVar] {
		t.Errorf("one launch composed two tokens: %q then %q",
			c1.callerTokens[bridgeTokenVar], again.callerTokens[bridgeTokenVar])
	}
}

func TestALaunchWithoutAServiceDaemonMintsNoCallerToken(t *testing.T) {
	o, _, channel, _ := attachFixture(t, currentJailEnv, zaiSelected(t), hydratedKey(),
		func(o *Options, _ *jsonx.OrderedMap) { o.ProfileName = "zai" })
	if len(channel.callerTokens) != 0 || len(o.callerTokens) != 0 {
		t.Errorf("a launch with no pack service minted %q", channel.callerTokens)
	}
	shared, _ := deliveredFiles(t, channel)
	if strings.Contains(shared, "_TOKEN=") {
		t.Errorf("a launch with no pack service writes a caller token line:\n%s", shared)
	}
}

// THE ATTACH REUSES THE RUNNING JAIL'S TOKEN, through the real attachExisting: the daemon in
// that jail read its token once at boot, so what an attach delivers — the channel line and
// claude's ANTHROPIC_AUTH_TOKEN — must be that one, not the token this process minted when it
// composed. Deleting the rekey in attachExisting fails it.
func TestAnAttachDeliversTheRunningJailsCallerToken(t *testing.T) {
	packs := bridgedPacks(t)
	o, cfg, channel, stderr := attachFixture(t, currentJailEnv, packs, cerebrasKey(), selectCerebras)
	minted := o.callerTokens[bridgeTokenVar]

	// The running jail's own launch: another process, same workspace, its own token. Only its
	// shared file is left behind, so claude's env file exists afterwards only if this attach
	// delivered.
	running := strings.Repeat("12", 32)
	launcher := *o
	launcher.callerTokens = map[string]string{bridgeTokenVar: running}
	wsState := paths.WorkspaceHomeState(o.Workspace)
	prior := channelFor(t, &launcher, cfg, packs, cerebrasKey())
	writeUserEnvFile(filepath.Join(wsState, "yolo-user-env.sh"), prior.scope.SharedEnvSources(), prior)
	if _, err := os.Stat(filepath.Join(wsState, agentEnvStateDir, "claude.sh")); !os.IsNotExist(err) {
		t.Fatalf("fixture: claude's env file exists before the attach: %v", err)
	}

	rc, restarted, execed := attachToExec(t, o, cfg, packs, channel)
	if rc != 0 || restarted || !execed {
		t.Fatalf("attach rc=%d restarted=%v execed=%v\n%s", rc, restarted, execed, stderr.String())
	}
	shared, err := os.ReadFile(filepath.Join(wsState, "yolo-user-env.sh"))
	if err != nil {
		t.Fatal(err)
	}
	section := channelSection(t, string(shared))
	if !strings.Contains(section, "export "+bridgeTokenVar+"='"+running+"'\n") {
		t.Errorf("the attach did not deliver the running jail's token:\n%s", section)
	}
	claude, err := os.ReadFile(filepath.Join(wsState, agentEnvStateDir, "claude.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(claude), "export ANTHROPIC_AUTH_TOKEN='"+running+"'\n") {
		t.Errorf("claude's env file after the attach does not carry the running jail's token:\n%s", claude)
	}
	for name, body := range map[string]string{"shared": string(shared), "claude": string(claude)} {
		if strings.Contains(body, minted) {
			t.Errorf("the %s file carries the token this attach minted, which the running daemon refuses", name)
		}
	}
}

// The running jail's tokens are read from a file the jail can write, so the read refuses a
// link (the jail cannot turn it into a read of a host file) and keeps only well-formed tokens.
func TestRunningCallerTokensReadsOnlyTheJailsOwnWellFormedTokens(t *testing.T) {
	good := strings.Repeat("ab", 32)
	body := "# header\n" + channelSectionHeader + "\n" +
		exportPlain("YOLO_PROVIDERS", "{}") + exportPlain("YOLO_PROFILES", "{}") +
		exportPlain("YOLO_USE_PROFILES", "{}") +
		exportPlain(bridgeTokenVar, good) + exportPlain("YOLO_SERVICE_OTHER_TOKEN", "local") +
		exportPlain("SOME_TOKEN", good)

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "yolo-user-env.sh"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	got := runningCallerTokens(dir)
	if len(got) != 1 || got[bridgeTokenVar] != good {
		t.Errorf("runningCallerTokens = %q, want only %s", got, bridgeTokenVar)
	}

	host := filepath.Join(t.TempDir(), "host-file")
	if err := os.WriteFile(host, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	linked := t.TempDir()
	if err := os.Symlink(host, filepath.Join(linked, "yolo-user-env.sh")); err != nil {
		t.Fatal(err)
	}
	if got := runningCallerTokens(linked); got != nil {
		t.Errorf("a linked channel file was followed: %q", got)
	}
	if got := runningCallerTokens(t.TempDir()); got != nil {
		t.Errorf("no channel file, yet tokens %q", got)
	}
}
