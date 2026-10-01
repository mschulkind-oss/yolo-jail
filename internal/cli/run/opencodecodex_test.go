package run

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	officialpacks "github.com/mschulkind-oss/yolo-jail/packs"
)

// opencodecodex_test.go pins opencode on the ChatGPT subscription (the openai-codex provider)
// through the LAUNCH's own composition, composePackChannel: the call that refused the
// maintainer's launch (2026-09-30, "there's no way to satisfy opencode with codex").
//
// The refusal was the protocol gate's (packload.refuseUnspeakableProvider, which AgentEnv runs
// for every profiled agent from the credential gate this composes). A bare `-p codex` keys the
// codex profile onto every CLI the selected packs install (docs/reference/providers.md, "bare
// -p"), and opencode declared only `openai` (chat completions), while openai-codex offers
// `openai-responses` and, through the wire bridge claude needs, `anthropic`. opencode's own
// `openai` provider speaks Responses, so packs/opencode declares it.

// codexLaunchPacks is the maintainer's selection, claude, codex, opencode and pi, closed over
// `needs` by the launch's own resolver (packload.Selection.Close, as stagePacks closes it).
func codexLaunchPacks(t *testing.T) []*packload.Pack {
	t.Helper()
	all, problems := packload.MaterializeEmbedded(officialpacks.FS, t.TempDir())
	if len(problems) != 0 {
		t.Fatalf("materializing official packs: %v", problems)
	}
	byName := map[string]*packload.Pack{}
	for _, p := range all {
		byName[p.Name] = p
	}
	var selected []*packload.Pack
	for _, name := range []string{"claude", "codex", "opencode", "pi"} {
		p, ok := byName[name]
		if !ok {
			t.Fatalf("no official pack named %q", name)
		}
		selected = append(selected, p)
	}
	added, _, err := packload.Selection{Embedded: func(name string) (*packload.Pack, bool) {
		p, ok := byName[name]
		return p, ok
	}}.Close(selected)
	if err != nil {
		t.Fatalf("closing the selection: %v", err)
	}
	return append(selected, added...)
}

// A BARE `-p codex` OVER claude, codex, opencode AND pi COMPOSES, and opencode's own env carries
// the OpenAI login prelaunch its launcher runs (packs/opencode/derive.lua's yolo.env), so
// opencode starts on the shared login rather than on nothing. Before packs/opencode declared
// `openai-responses` this refused with the maintainer's message.
func TestABareCodexProfileComposesWithOpencodeSelected(t *testing.T) {
	home := retireHome(t)
	writeUserPacks(t, home, `[]`)
	var out bytes.Buffer
	o := retireOptions(t, &out)
	o.ProfileName = "codex"
	packs := codexLaunchPacks(t)
	c, err := o.composePackChannel(newConfig(), packs, emptyEnv())
	if err != nil {
		t.Fatalf("a bare -p codex over claude, codex, opencode and pi refused:\n%v", err)
	}
	if strings.Contains(out.String(), "cannot point opencode at it") {
		t.Errorf("the launch still says opencode cannot be pointed at openai-codex:\n%s", out.String())
	}
	if got, _ := c.scope.DeliveredTo("opencode", "YOLO_AUTH_PRELAUNCH_OPENCODE_FLAG"); got != "--opencode-auth" {
		t.Errorf("opencode's prelaunch flag = %q, want --opencode-auth: opencode on codex would start "+
			"with no stored login, and its own `openai` provider would fall back to an ambient key", got)
	}
	if got, _ := c.scope.DeliveredTo("opencode", "YOLO_AUTH_PRELAUNCH_OPENCODE_PATH"); got != ".local/share/opencode/auth.json" {
		t.Errorf("opencode's prelaunch path = %q, want .local/share/opencode/auth.json, the file "+
			"opencode's Auth store reads", got)
	}
}

// THE PRELAUNCH IS KEYED ON THE PROVIDER, not on the profile's name (OQ-BR8, PP-D2, pi's rule):
// a launch whose opencode is on another provider asks for no OpenAI login.
func TestOpencodeOffCodexAsksForNoOpenAILogin(t *testing.T) {
	home := retireHome(t)
	writeUserPacks(t, home, `[]`)
	var out bytes.Buffer
	o := retireOptions(t, &out)
	o.ProfileName = "bedrock"
	c, err := o.composePackChannel(newConfig(), codexLaunchPacks(t), emptyEnv())
	if err != nil {
		t.Fatalf("composing a bedrock launch: %v", err)
	}
	if got, ok := c.scope.DeliveredTo("opencode", "YOLO_AUTH_PRELAUNCH_OPENCODE_FLAG"); ok {
		t.Errorf("opencode on bedrock carries the OpenAI login prelaunch %q", got)
	}
}
