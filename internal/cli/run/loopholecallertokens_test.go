package run

// loopholecallertokens_test.go pins the launcher half of caller tokens for LOOPHOLE jail daemons
// (docs/plans/notch-convergence.md §2.3, NC-D3): the OpenAI and AWS credential adapters declare
// `jail_daemon.caller_token`, so a launch whose payload runs them mints a token for each and
// carries it in the 0600 channel; the Claude OAuth terminator declares none (its client cannot
// carry one), so it gets none. Driven through the real composePackChannel over the shipped packs,
// so deleting the manifest declaration, the JailDaemonSpec field's copy, or the payload argument
// in composePackChannel fails it.

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/svcendpoint"
)

func TestTheCredentialAdaptersGetACallerTokenAndTheTerminatorDoesNot(t *testing.T) {
	packs := []*packload.Pack{
		officialPack(t, "claude"), officialPack(t, "codex"),
		officialPack(t, "openai-auth"), officialPack(t, "aws-auth"), officialPack(t, "bedrock"),
	}
	enableAWS := func(o *Options, cfg *jsonx.OrderedMap) {
		// The adapter starts only when an agent's profile selects what it serves (OQ-CN7 (b)).
		o.UseProfiles = map[string]string{"codex": "bedrock"}
		loopholes.SetPackModules(packLoopholeModules(packs))
		block, entry := jsonx.NewOrderedMap(), jsonx.NewOrderedMap()
		entry.Set("enabled", true)
		block.Set("aws-auth", entry)
		cfg.Set("loopholes", block)
	}
	_, _, channel, _ := attachFixture(t, currentJailEnv, packs, emptyEnv(), enableAWS)

	openai := paths.ServiceCallerTokenEnv("openai-auth-broker")
	aws := paths.ServiceCallerTokenEnv("aws-auth")
	for _, v := range []string{openai, aws} {
		if !svcendpoint.IsToken(channel.callerTokens[v]) {
			t.Errorf("no caller token for %s: the channel carries %q", v, channel.callerTokens)
		}
	}
	if channel.callerTokens[openai] == channel.callerTokens[aws] {
		t.Error("two daemons share one caller token")
	}
	if _, ok := channel.callerTokens[paths.ServiceCallerTokenEnv("claude-oauth-broker")]; ok {
		t.Error("the Claude OAuth terminator was minted a caller token its client cannot send")
	}
	shared, agents := deliveredFiles(t, channel)
	section := channelSection(t, shared)
	if !strings.Contains(section, "export "+openai+"='"+channel.callerTokens[openai]+"'\n") {
		t.Errorf("the shared channel section does not carry %s:\n%s", openai, section)
	}
	// THE AWS TOKEN IS SCOPED (OQ-CN7 (c)): no export of it in the shared file, which every
	// process inherits — only the record the adapter and the next attach read — and the one
	// exported copy is AWS_CONTAINER_AUTHORIZATION_TOKEN in codex's own file.
	awsTok := channel.callerTokens[aws]
	if strings.Contains(shared, "export "+aws) || strings.Contains(shared, "export AWS_CONTAINER_AUTHORIZATION_TOKEN") {
		t.Errorf("the shared file exports the aws-auth adapter's scoped caller token:\n%s", shared)
	}
	if !strings.Contains(section, entrypoint.ScopedCallerTokenRecord(aws, awsTok)) {
		t.Errorf("the shared channel section does not record the scoped %s:\n%s", aws, section)
	}
	if got := entrypoint.ParseScopedCallerTokens([]byte(shared))[aws]; got != awsTok {
		t.Errorf("the adapter's reader finds %q in the shared file, want this launch's token", got)
	}
	if !strings.Contains(agents["codex"], "AWS_CONTAINER_AUTHORIZATION_TOKEN") ||
		!strings.Contains(agents["codex"], "'"+awsTok+"'") {
		t.Errorf("codex selected bedrock and its file must carry the adapter's token as "+
			"AWS_CONTAINER_AUTHORIZATION_TOKEN:\n%s", agents["codex"])
	}
	for agent, body := range agents {
		if agent != "codex" && strings.Contains(body, awsTok) {
			t.Errorf("%s, which did not select bedrock, received the adapter's token:\n%s", agent, body)
		}
	}

	// ENABLED BUT UNSELECTED (OQ-CN7 (b)): the loophole on and no agent on bedrock starts no
	// adapter, so it has no token to demand.
	_, _, unselected, _ := attachFixture(t, currentJailEnv, packs, emptyEnv(),
		func(o *Options, cfg *jsonx.OrderedMap) {
			enableAWS(o, cfg)
			o.UseProfiles = nil
		})
	if _, ok := unselected.callerTokens[aws]; ok {
		t.Error("an aws-auth adapter no profile selects was minted a caller token")
	}

	// A loophole that is not in the payload — aws-auth left at its default, off — gets none.
	_, _, off, _ := attachFixture(t, currentJailEnv, packs, emptyEnv(),
		func(*Options, *jsonx.OrderedMap) { loopholes.SetPackModules(packLoopholeModules(packs)) })
	if _, ok := off.callerTokens[aws]; ok {
		t.Error("a disabled aws-auth loophole was minted a caller token")
	}
	if !svcendpoint.IsToken(off.callerTokens[openai]) {
		t.Error("the default-on OpenAI adapter lost its caller token")
	}
}

// The spec a loophole's jail daemon composes to carries the manifest's declaration, and a
// service's always demands one.
func TestJailDaemonSpecsCarryTheCallerTokenDeclaration(t *testing.T) {
	specs := []loopholes.JailDaemonSpec{
		{Name: "openai-auth-broker", CallerToken: true},
		{Name: "claude-oauth-broker"},
		{Name: "wire-bridge", CallerToken: true},
	}
	got := callerTokenVars(specs)
	want := []string{"YOLO_SERVICE_OPENAI_AUTH_BROKER_TOKEN", "YOLO_SERVICE_WIRE_BRIDGE_TOKEN"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("callerTokenVars = %v, want %v", got, want)
	}
	for _, s := range serviceJailDaemons([]*packload.Pack{officialPack(t, "wire-bridge")}) {
		if !s.CallerToken {
			t.Errorf("the %s service's jail daemon does not demand a caller token", s.Name)
		}
	}
}
