package run

// opencodeactiveset_test.go pins the jail notch's half of opencode holding an ACTIVE SET
// (docs/design/active-provider-sets.md §8 step 3, AP-D15; the active set, a term that doc coins,
// is the ordered list of profiles one agent runs on for one launch). Through the launch's own
// composition — composePackChannel, checkProviderCredentials, noteUseProfiles, the channel's wire
// tables — over the SHIPPED packs, so dropping `provider_sets` from packs/opencode/pack.json, or
// any call site cutting opencode's set to its first entry, fails a case here. The render half
// runs the boot (entrypoint.ConfigurePackSurfaces) on exactly the wire the channel emits.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

func opencodeSetPacks(t *testing.T) []*packload.Pack {
	return []*packload.Pack{officialPack(t, "claude"), officialPack(t, "opencode"),
		officialPack(t, "zai"), officialPack(t, "openrouter")}
}

// A SET NAMED AT opencode REACHES IT WHOLE: `-p opencode=zai,openrouter` passes the typed-pair
// check, puts both keys in opencode's own env file and in no other process's, crosses into the
// jail as opencode's list in order, and the launch names the set.
func TestAListNamedAtOpencodeDeliversEveryKeyToOpencodeAlone(t *testing.T) {
	home := packHome(t)
	o := goldenOptions(t.TempDir(), home)
	var stderr bytes.Buffer
	o.Stderr = &stderr
	o.UseProfiles = map[string]string{"opencode": "zai,openrouter"}
	if err := o.checkProfileTargets(); err != nil {
		t.Fatalf("a list at opencode, which holds sets, must pass the typed-pair check: %v", err)
	}
	packs := opencodeSetPacks(t)
	channel := channelFor(t, o, bareConfig(), packs, zaiAndRouterKeys())

	shared, agents := deliveredFiles(t, channel)
	for _, want := range []string{"export ZAI_API_KEY=${ZAI_API_KEY:-'tok-zai'}\n",
		"export OPENROUTER_API_KEY=${OPENROUTER_API_KEY:-'tok-router'}\n"} {
		if !strings.Contains(agents["opencode"], want) {
			t.Errorf("opencode's own file must carry %q:\n%s", want, agents["opencode"])
		}
	}
	for _, gone := range []string{"tok-zai", "tok-router"} {
		if strings.Contains(shared, gone) {
			t.Errorf("the shared file — every process's — carries a set's key %s:\n%s", gone, shared)
		}
	}
	if got := wireOf(channel.profiles); got != `{"opencode":["zai","openrouter"]}` {
		t.Errorf("YOLO_USE_PROFILES = %s, want opencode's list in order", got)
	}
	o.noteUseProfiles(channel, packs, nil)
	if !strings.Contains(stderr.String(), "Active set for opencode: zai, openrouter") {
		t.Errorf("the launch must name opencode's set in order:\n%s", stderr.String())
	}
}

// AP-D3 for opencode: every entry must be declared. An undeclared SECOND entry refuses the
// launch, naming it, and opencode never starts on the declared first.
func TestAnUndeclaredLaterEntryOfOpencodesSetRefuses(t *testing.T) {
	home := packHome(t)
	o := goldenOptions(t.TempDir(), home)
	o.UseProfiles = map[string]string{"opencode": "zai,typo"}
	_, err := o.composePackChannel(bareConfig(), opencodeSetPacks(t), zaiAndRouterKeys())
	if err == nil || !strings.Contains(err.Error(), `profile "typo" selected for opencode`) {
		t.Fatalf("composePackChannel = %v, want the refusal naming opencode's second entry", err)
	}
}

// A BARE list goes WHOLE to opencode now that its pack declares provider_sets (OQ-AP3): pi is not
// selected, so opencode is the set-capable receiver, and only claude takes the first entry.
func TestABareListGoesWholeToOpencode(t *testing.T) {
	home := packHome(t)
	o := goldenOptions(t.TempDir(), home)
	o.ProfileName = "zai,openrouter"
	if got := wireOf(o.effectiveUseProfiles(bareConfig(), opencodeSetPacks(t))); !strings.Contains(got, `"opencode":["zai","openrouter"]`) ||
		!strings.Contains(got, `"claude":"zai"`) {
		t.Errorf("effective table = %s, want opencode's whole list and claude's first entry", got)
	}
}

// bootOpencode runs the jail's boot over packs on exactly the wire tables channel emits, and
// returns opencode.json as it lands in the jail home.
func bootOpencode(t *testing.T, channel *packChannel, packs []*packload.Pack) []byte {
	t.Helper()
	t.Setenv("YOLO_CTX_ROOT", t.TempDir())
	var errw bytes.Buffer
	e := &entrypoint.Env{Home: t.TempDir(), Workspace: t.TempDir(), Vars: channel.wireTableValues(),
		Stderr: &errw, LogOnly: &errw}
	entrypoint.ConfigurePackSurfaces(e, packs)
	if fails := e.GenFailures(); len(fails) != 0 {
		t.Fatalf("the boot failed: %v\n%s", fails, errw.String())
	}
	b, err := os.ReadFile(filepath.Join(e.Home, ".config", "opencode", "opencode.json"))
	if err != nil {
		t.Fatalf("the boot wrote no opencode.json: %v\n%s", err, errw.String())
	}
	return b
}

// THE FLAG AND THE KEY RENDER THE SAME (PP-D11 read for opencode's set): `-p opencode=zai,openrouter`
// — through config.ProfileSelection.ApplyFlag and Options.SetProfileFlags, the two calls the
// CLI's applyProfileValue makes — and `"profile": {"opencode": ["zai", "openrouter"]}` compose the
// same channel, and the boot renders opencode.json byte for byte the same from each. The render
// is the set's, so the equality is not two empty files: both providers enabled, the first
// entry's model.
func TestOpencodesFlagAndKeyListRenderTheSame(t *testing.T) {
	home := packHome(t)
	packs := opencodeSetPacks(t)

	flag := goldenOptions(t.TempDir(), home)
	sel := flag.ProfileFlags()
	if err := sel.ApplyFlag("opencode=zai,openrouter"); err != nil {
		t.Fatalf("-p opencode=zai,openrouter did not parse: %v", err)
	}
	flag.SetProfileFlags(sel)
	fromFlag := bootOpencode(t, channelFor(t, flag, bareConfig(), packs, zaiAndRouterKeys()), packs)

	keyed := goldenOptions(t.TempDir(), home)
	cfg := bareConfig()
	v, err := jsonx.Decode([]byte(`{"opencode": ["zai", "openrouter"]}`))
	if err != nil {
		t.Fatal(err)
	}
	cfg.Set(config.ProfileKey, v)
	fromKey := bootOpencode(t, channelFor(t, keyed, cfg, packs, zaiAndRouterKeys()), packs)

	if string(fromFlag) != string(fromKey) {
		t.Errorf("opencode.json differs between the flag and the key:\n--- -p\n%s\n--- profile key\n%s", fromFlag, fromKey)
	}
	var rendered map[string]any
	if err := json.Unmarshal(fromKey, &rendered); err != nil {
		t.Fatalf("opencode.json is not JSON: %v\n%s", err, fromKey)
	}
	// Both are opencode's own providers, zai's plan its zai-coding-plan, so each is named by
	// opencode's own id (docs/design/pi-codex-provider-shadowing.md OQ-3).
	if got, _ := rendered["enabled_providers"].([]any); !reflect.DeepEqual(got, []any{"zai-coding-plan", "openrouter"}) {
		t.Errorf("enabled_providers = %v, want [zai-coding-plan openrouter]\n%s", got, fromKey)
	}
	if m, _ := rendered["model"].(string); !strings.HasPrefix(m, "zai-coding-plan/") {
		t.Errorf("model = %v, want the first entry's zai model on zai-coding-plan\n%s", rendered["model"], fromKey)
	}
}

// A BEDROCK ENTRY AFTER THE FIRST GETS ITS REGION (AP-D14 and BR-D18 read for opencode's set):
// opencode on [zai, bedrock] with no region anywhere is refused naming bedrock and opencode, where
// reading the primary alone sees zai and asks nothing; an AWS_DEFAULT_REGION, which opencode never
// reads, is still refused; the host's ~/.aws/config region is delivered into opencode's own env
// file as AWS_REGION, the one variable its Bedrock loader reads; and a region on the provider
// passes, the derive writing it as the native row's options.region (pinned by
// entrypoint.TestOpencodeOnASetWithBedrockSecondBindsItNatively).
func TestABedrockEntryAfterTheFirstOfOpencodesSetGetsItsRegion(t *testing.T) {
	home := retireHome(t)
	writeUserPacks(t, home, `[]`)
	o := retireOptions(t, discardBuf())
	o.Getenv = shellWith(map[string]string{"HOME": home})
	packs := []*packload.Pack{officialPack(t, "opencode"), officialPack(t, "zai"), officialPack(t, "bedrock")}
	o.UseProfiles = map[string]string{"opencode": "zai,bedrock"}
	zaiKey := map[string]string{"ZAI_API_KEY": "tok-zai"}

	lines, refuse := o.checkProviderCredentials(newConfig(), packs,
		channelFor(t, o, newConfig(), packs, userEnvWith(zaiKey)), nil)
	if got := strings.Join(lines, "\n"); !refuse ||
		!strings.Contains(got, `requires a region for provider "bedrock" (platform "aws-bedrock"), selected for opencode`) {
		t.Fatalf("opencode's second entry on bedrock with no region must refuse (refuse=%v):\n%s", refuse, got)
	}

	unread := userEnvWith(map[string]string{"ZAI_API_KEY": "tok-zai", "AWS_DEFAULT_REGION": "eu-west-1"})
	lines, refuse = o.checkProviderCredentials(newConfig(), packs, channelFor(t, o, newConfig(), packs, unread), nil)
	if got := strings.Join(lines, "\n"); !refuse ||
		!strings.Contains(got, "AWS_DEFAULT_REGION reaches opencode, which does not read it") {
		t.Errorf("an AWS_DEFAULT_REGION is no region for opencode's Bedrock entry (refuse=%v):\n%s", refuse, got)
	}

	writeAWSConfig(t, home, "[default]\nregion = eu-north-1\n")
	channel := channelFor(t, o, newConfig(), packs, userEnvWith(zaiKey))
	if lines, refuse := o.checkProviderCredentials(newConfig(), packs, channel, nil); refuse {
		t.Fatalf("a region in the host's ~/.aws/config must satisfy opencode's Bedrock entry:\n%s",
			strings.Join(lines, "\n"))
	}
	if got := agentEnvFileContent(channel, "opencode"); !strings.Contains(got, "AWS_REGION") ||
		!strings.Contains(got, "'eu-north-1'") {
		t.Errorf("opencode's env file must carry the file's region as AWS_REGION:\n%s", got)
	}
	if d := channel.scope.Agent("opencode"); d == nil || d.RegionFileFor("bedrock") == nil {
		t.Errorf("the region lookup must be the Bedrock entry's, the second of opencode's set")
	}

	cfg := newConfig()
	withBedrockRegion(cfg)
	if lines, refuse := o.checkProviderCredentials(cfg, packs, channelFor(t, o, cfg, packs, userEnvWith(zaiKey)), nil); refuse {
		t.Errorf("a region on the provider satisfies opencode's second entry:\n%s", strings.Join(lines, "\n"))
	}
}
