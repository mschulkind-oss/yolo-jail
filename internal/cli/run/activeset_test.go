package run

// activeset_test.go pins the jail notch's half of docs/design/active-provider-sets.md (OQ-AP1 to
// OQ-AP3, ruled 2026-09-29; the ACTIVE SET, a term that doc coins, is the ordered list of
// profiles one agent runs on for one launch). Everything runs through the launch's own
// composition — effectiveUseProfiles, composePackChannel, checkProviderCredentials,
// noteUseProfiles, checkProfileTargets, attachContractFor — over the SHIPPED packs, so deleting
// any of their set handling fails a case here. The container vehicle is read back from the files
// deliverChannel writes, and the macos-user vehicle from launchEnv, the plan env it layers.

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// zaiAndRouterKeys hydrates both providers' keys and one variable no provider claims.
func zaiAndRouterKeys() *jsonx.OrderedMap {
	m := jsonx.NewOrderedMap()
	m.Set("ZAI_API_KEY", "tok-zai")
	m.Set("OPENROUTER_API_KEY", "tok-router")
	m.Set("GH_TOKEN", "gh-test")
	return m
}

func setPacks(t *testing.T) []*packload.Pack {
	return []*packload.Pack{officialPack(t, "claude"), officialPack(t, "pi"),
		officialPack(t, "zai"), officialPack(t, "openrouter")}
}

// §9's first two bullets at the container notch: `-p pi=zai,openrouter` puts both keys in pi's
// own env file and in no other process's, and the jail's table carries pi's whole list.
func TestAListNamedAtPiDeliversEveryKeyToPiAlone(t *testing.T) {
	home := packHome(t)
	o := goldenOptions(t.TempDir(), home)
	o.UseProfiles = map[string]string{"pi": "zai,openrouter"}
	channel := channelFor(t, o, bareConfig(), setPacks(t), zaiAndRouterKeys())

	shared, agents := deliveredFiles(t, channel)
	for _, want := range []string{"export ZAI_API_KEY=${ZAI_API_KEY:-'tok-zai'}\n",
		"export OPENROUTER_API_KEY=${OPENROUTER_API_KEY:-'tok-router'}\n"} {
		if !strings.Contains(agents["pi"], want) {
			t.Errorf("pi's own file must carry %q:\n%s", want, agents["pi"])
		}
	}
	for _, gone := range []string{"tok-zai", "tok-router"} {
		if strings.Contains(shared, gone) {
			t.Errorf("the shared file — every process's — carries a set's key %s:\n%s", gone, shared)
		}
	}
	if _, leaked := agents["claude"]; leaked {
		t.Errorf("claude selected nothing and must get no file: %s", agents["claude"])
	}
	if got := wireOf(channel.profiles); got != `{"pi":["zai","openrouter"]}` {
		t.Errorf("YOLO_USE_PROFILES = %s, want pi's list in order", got)
	}

	// The macos-user vehicle, the same channel: pi's plan env carries both keys.
	env := channel.launchEnv("pi")
	for _, k := range []string{"ZAI_API_KEY", "OPENROUTER_API_KEY"} {
		if v, _ := env.Get(k); v == nil {
			t.Errorf("pi's macos-user plan env lacks %s", k)
		}
	}
	if v, _ := channel.launchEnv("bash").Get("OPENROUTER_API_KEY"); v != nil {
		t.Error("a bare shell's plan env must not carry a set's key")
	}
	if got, _ := channel.launchEnv("pi").Get(entrypoint.UseProfilesWireEnv); strings.ReplaceAll(got.(string), " ", "") !=
		`{"pi":["zai","openrouter"]}` {
		t.Errorf("pi's plan env relays YOLO_USE_PROFILES = %v, want the list", got)
	}
}

// wireOf is a profile table as it crosses (YOLO_USE_PROFILES), its JSON spaces dropped so a case
// reads as the compact table.
func wireOf(m *jsonx.OrderedMap) string {
	return strings.ReplaceAll(jsonDumpsOrEmptyObj(m), " ", "")
}

// §9's third bullet: with OPENROUTER_API_KEY missing the launch refuses, naming openrouter as
// the second entry of pi's set, and never starts pi on zai alone.
func TestAMissingSecondEntryKeyRefusesNamingItsPosition(t *testing.T) {
	home := packHome(t)
	o := goldenOptions(t.TempDir(), home)
	o.UseProfiles = map[string]string{"pi": "zai,openrouter"}
	keys := jsonx.NewOrderedMap()
	keys.Set("ZAI_API_KEY", "tok-zai")
	packs := setPacks(t)
	lines, refuse := o.checkProviderCredentials(bareConfig(), packs,
		channelFor(t, o, bareConfig(), packs, keys), nil)
	got := strings.Join(lines, "\n")
	if !refuse {
		t.Fatalf("a set missing an entry's key must refuse:\n%s", got)
	}
	for _, want := range []string{`provider "openrouter"`, "OPENROUTER_API_KEY",
		"profile openrouter is entry 2 of pi's profiles (zai, openrouter)"} {
		if !strings.Contains(got, want) {
			t.Errorf("the refusal must name %q:\n%s", want, got)
		}
	}
}

// OQ-AP2: a list NAMED at claude, which runs one provider per session, refuses before anything
// is composed, naming the fix; the typed-pair check refuses it over the whole CLI namespace too.
func TestAListNamedAtASingleProviderAgentRefuses(t *testing.T) {
	home := packHome(t)
	o := goldenOptions(t.TempDir(), home)
	o.UseProfiles = map[string]string{"claude": "zai,openrouter"}
	_, err := o.composePackChannel(bareConfig(), setPacks(t), zaiAndRouterKeys())
	if err == nil || !strings.Contains(err.Error(), "whose pack does not declare provider_sets") ||
		!strings.Contains(err.Error(), "`-p claude=zai`") {
		t.Fatalf("composePackChannel = %v, want the single-provider refusal naming the fix", err)
	}
	if err := o.checkProfileTargets(); err == nil ||
		!strings.Contains(err.Error(), "-p claude=zai,openrouter: profiles zai, openrouter are selected for claude") {
		t.Errorf("checkProfileTargets = %v, want the typed list refused at claude", err)
	}
	o.UseProfiles = map[string]string{"pi": "zai,openrouter"}
	if err := o.checkProfileTargets(); err != nil {
		t.Errorf("a list at pi, which holds sets, must pass the typed-pair check: %v", err)
	}
}

// OQ-AP3 (option C): a BARE list goes whole to pi and its first entry to claude, and one launch
// line names claude and what it ignores.
func TestABareListGoesWholeToPiAndFirstToClaude(t *testing.T) {
	home := packHome(t)
	o := goldenOptions(t.TempDir(), home)
	var stderr bytes.Buffer
	o.Stderr = &stderr
	o.ProfileName = "zai,openrouter"
	packs := setPacks(t)
	effective := o.effectiveUseProfiles(bareConfig(), packs)
	if got := wireOf(effective); !strings.Contains(got, `"pi":["zai","openrouter"]`) ||
		!strings.Contains(got, `"claude":"zai"`) {
		t.Fatalf("effective table = %s, want pi's whole list and claude's first entry", got)
	}
	// Through the channel the launch composes, whose bareNote the disclosure prints.
	channel, err := o.composePackChannel(bareConfig(), packs, zaiAndRouterKeys())
	if err != nil {
		t.Fatalf("a bare list must compose: %v", err)
	}
	o.noteUseProfiles(channel, packs, nil)
	out := stderr.String()
	for _, want := range []string{"Profile list zai, openrouter (a bare -p, naming no agent)",
		"claude takes one profile (its pack does not declare provider_sets)", "ignores openrouter", "pi takes the whole list",
		"Active set for pi: zai, openrouter", "Profile openrouter:"} {
		if !strings.Contains(out, want) {
			t.Errorf("the launch lines must say %q:\n%s", want, out)
		}
	}
}

// A config list is the set too, and a typed pair replaces it whole for the launch (AP-D4).
func TestATypedPairReplacesAConfigListWhole(t *testing.T) {
	home := packHome(t)
	o := goldenOptions(t.TempDir(), home)
	use := jsonx.NewOrderedMap()
	use.Set("pi", []any{"zai", "openrouter"})
	cfg := bareConfig()
	cfg.Set("profile", use)
	if got := wireOf(o.effectiveUseProfiles(cfg, setPacks(t))); got != `{"pi":["zai","openrouter"]}` {
		t.Errorf("the config list = %s, want it carried in order", got)
	}
	o.UseProfiles = map[string]string{"pi": "openrouter"}
	if got := wireOf(o.effectiveUseProfiles(cfg, setPacks(t))); got != `{"pi":"openrouter"}` {
		t.Errorf("a typed pair must replace the config list whole, got %s", got)
	}
	// A config list of one crosses as the plain string (AP-D8: no tag needed for it).
	use.Set("pi", []any{"zai"})
	o.UseProfiles = nil
	if got := wireOf(o.effectiveUseProfiles(cfg, setPacks(t))); got != `{"pi":"zai"}` {
		t.Errorf("a config list of one = %s, want the plain string", got)
	}
}

// AP-D3: EVERY entry of a set must be declared, not only its primary. An undeclared SECOND entry
// refuses the launch through the composition, naming that entry, and pi never starts on the
// declared first. Deleting the declaration loop's walk over the whole set (or cutting it to
// the first entry) passes `typo` through.
func TestAnUndeclaredLaterEntryRefusesTheLaunch(t *testing.T) {
	home := packHome(t)
	o := goldenOptions(t.TempDir(), home)
	o.UseProfiles = map[string]string{"pi": "zai,typo"}
	_, err := o.composePackChannel(bareConfig(), setPacks(t), zaiAndRouterKeys())
	if err == nil || !strings.Contains(err.Error(), `profile "typo" selected for pi`) {
		t.Fatalf("composePackChannel = %v, want the refusal naming pi's second entry", err)
	}
}

// OQ-AP3 with AP-D3: a BARE list's every entry must be declared, even where no agent takes the
// list whole. claude takes a bare list's first entry alone, so with no set-capable agent
// selected the tail reaches no agent's set, and it used to go unchecked: `-p zai,typo` started
// claude on zai, where the same value refused as soon as pi was selected.
func TestAnUndeclaredEntryOfABareListRefusesWithNoSetCapableAgent(t *testing.T) {
	home := packHome(t)
	o := goldenOptions(t.TempDir(), home)
	o.ProfileName = "zai,typo"
	packs := []*packload.Pack{officialPack(t, "claude"), officialPack(t, "zai")}
	_, err := o.composePackChannel(bareConfig(), packs, zaiAndRouterKeys())
	if err == nil || !strings.Contains(err.Error(), `profile "typo" (entry 2 of the bare -p list zai,typo)`) {
		t.Fatalf("composePackChannel = %v, want the bare list's undeclared entry refused", err)
	}
	// Declared throughout, the same bare list composes and claude narrows to zai.
	o.ProfileName = "zai,openrouter"
	packs = append(packs, officialPack(t, "openrouter"))
	if _, err := o.composePackChannel(bareConfig(), packs, zaiAndRouterKeys()); err != nil {
		t.Errorf("a declared bare list must compose: %v", err)
	}
}

// A PRE-CHANGE JAIL FROZE ONE PROFILE PER CLI, so its launch-time delivery stands in for an attach
// only when the attach selects exactly that. An attach carrying pi's [zai, openrouter] into a jail
// frozen at pi=zai does not: openrouter's key and pi's list would never arrive. Through
// attachContractFor, the attach's own decision; comparing the sets by their primaries alone
// stands the frozen zai in for the list.
func TestAFrozenSingleProfileDoesNotStandInForASet(t *testing.T) {
	home := packHome(t)
	o := goldenOptions(t.TempDir(), home)
	o.UseProfiles = map[string]string{"pi": "zai,openrouter"}
	channel := channelFor(t, o, bareConfig(), setPacks(t), zaiAndRouterKeys())
	frozenAt := func(use string) []string {
		return []string{"YOLO_VERSION=0.8.0", `YOLO_PROVIDERS={"zai": {}}`, "YOLO_USE_PROFILES=" + use}
	}
	if c := attachContractFor(channel, frozenAt(`{"pi": "zai"}`)); c.standIn {
		t.Error("a jail frozen at pi=zai must not stand in for an attach selecting pi's [zai, openrouter]")
	}
	if c := attachContractFor(channel, frozenAt(`{"pi": ["openrouter", "zai"]}`)); c.standIn {
		t.Error("a set is ordered: [openrouter, zai] must not stand in for [zai, openrouter]")
	}
	if c := attachContractFor(channel, frozenAt(`{"pi": ["zai", "openrouter"]}`)); !c.standIn {
		t.Error("the same set, entry for entry, is the same selection and must stand in")
	}
}

// AP-D12: two entries on ONE REGIONAL PLATFORM refuse, naming both. pi on [bedrock, bedrock-eu]
// (a second provider of platform aws-bedrock) would read one AWS_REGION and one credential chain
// for both, and pi binds the platform to its one amazon-bedrock provider. Through the
// composition, which asks it beside the set's other rules.
func TestTwoEntriesOnOneRegionalPlatformRefuse(t *testing.T) {
	home := packHome(t)
	o := goldenOptions(t.TempDir(), home)
	eu := inlinePack(t, "bedrock-eu", `{"name":"bedrock-eu","contributes":[
	  {"kind":"provider","name":"bedrock-eu","platform":"aws-bedrock","region":"eu-west-1",
	   "models":{"m":"m"}},
	  {"kind":"profile","name":"bedrock-eu","provider":"bedrock-eu"}]}`)
	packs := []*packload.Pack{officialPack(t, "pi"), officialPack(t, "bedrock"), eu}
	o.UseProfiles = map[string]string{"pi": "bedrock,bedrock-eu"}
	_, err := o.composePackChannel(bareConfig(), packs, zaiAndRouterKeys())
	if err == nil || !strings.Contains(err.Error(),
		`profiles "bedrock" and "bedrock-eu" in pi's profiles are both on platform "aws-bedrock"`) {
		t.Fatalf("composePackChannel = %v, want the one-platform refusal naming both entries", err)
	}
	// One of them composes.
	o.UseProfiles = map[string]string{"pi": "bedrock-eu"}
	if _, err := o.composePackChannel(bareConfig(), packs, zaiAndRouterKeys()); err != nil &&
		strings.Contains(err.Error(), "both on platform") {
		t.Errorf("a set of one names its platform once: %v", err)
	}
}

// An EMPTY PAIR (`-p pi=`, alone or beside `claude=zai`) crosses as null, the selection of
// nothing the profile key's null is too (config.FoldProfiles, PP-D11), never as an empty list:
// the jail reads null as no selection, and a `[]` would be a list value no older jail was ever
// handed.
func TestAnEmptyPairCrossesAsTheEmptySelection(t *testing.T) {
	home := packHome(t)
	o := goldenOptions(t.TempDir(), home)
	o.UseProfiles = map[string]string{"pi": "", "claude": "zai"}
	effective := o.effectiveUseProfiles(bareConfig(), setPacks(t))
	if v, named := effective.Get("pi"); !named || v != nil {
		t.Errorf(`-p pi= crosses as %#v (named %v), want null`, v, named)
	}
	if v, _ := effective.Get("claude"); v != "zai" {
		t.Errorf("-p claude=zai beside it crosses as %#v, want \"zai\"", v)
	}
}

// AP-D8: an attach delivering a list needs the profile-sets tag, and a jail launched before it
// lacks the tag, so the attach takes the restart-or-refuse disposition; a set of one needs none.
func TestAnAttachDeliveringAListNeedsTheProfileSetsTag(t *testing.T) {
	home := packHome(t)
	o := goldenOptions(t.TempDir(), home)
	o.UseProfiles = map[string]string{"pi": "zai,openrouter"}
	channel := channelFor(t, o, bareConfig(), setPacks(t), zaiAndRouterKeys())
	older := []string{entrypoint.ContractTagsEnv + "=entry-channel,agent-env-files"}
	missing := attachContractFor(channel, older).missingFrom(older)
	found := false
	for _, n := range missing {
		if n.tag == contractProfileSets {
			found = true
			if len(n.withheld) != 1 || n.withheld[0] != "the profile list pi=zai,openrouter" {
				t.Errorf("the need must name the list it withholds, got %v", n.withheld)
			}
		}
	}
	if !found {
		t.Errorf("an attach delivering pi's list into a jail without %s must name it missing: %v",
			contractProfileSets, missing)
	}
	current := []string{entrypoint.ContractTagsEnv + "=" + launchContractTagsValue()}
	if missing := attachContractFor(channel, current).missingFrom(current); len(missing) != 0 {
		t.Errorf("a jail this build launched has every tag, got missing %v", missing)
	}
	o.UseProfiles = map[string]string{"pi": "zai"}
	one := channelFor(t, o, bareConfig(), setPacks(t), zaiAndRouterKeys())
	for _, n := range attachContractFor(one, older).needs {
		if n.tag == contractProfileSets {
			t.Error("a set of one crosses as a string and must need no profile-sets tag")
		}
	}
}

// OQ-AP3 ON THE PROFILE KEY (PP-D10's list form): the key's bare list — its list form, or a list
// under "*" — is a bare -p's list: whole for pi, the first entry for claude, and one launch line
// naming claude and the key's own per-agent spelling. Through composePackChannel, whose bareNote
// noteUseProfiles prints; a bare -p beats it, and then nothing is narrowed from the key.
func TestTheProfileKeysBareListGoesWholeToPiAndFirstToClaude(t *testing.T) {
	for name, key := range map[string]any{
		"the list form": []any{"zai", "openrouter"},
		`"*"`:           func() any { m := jsonx.NewOrderedMap(); m.Set("*", []any{"zai", "openrouter"}); return m }(),
	} {
		t.Run(name, func(t *testing.T) {
			home := packHome(t)
			o := goldenOptions(t.TempDir(), home)
			var stderr bytes.Buffer
			o.Stderr = &stderr
			cfg := bareConfig()
			cfg.Set("profile", key)
			packs := setPacks(t)
			channel, err := o.composePackChannel(cfg, packs, zaiAndRouterKeys())
			if err != nil {
				t.Fatalf("the key's bare list must compose: %v", err)
			}
			if got := wireOf(channel.profiles); !strings.Contains(got, `"pi":["zai","openrouter"]`) ||
				!strings.Contains(got, `"claude":"zai"`) {
				t.Fatalf("effective table = %s, want pi's whole list and claude's first entry", got)
			}
			o.noteUseProfiles(channel, packs, nil)
			for _, want := range []string{"Profile list zai, openrouter (the profile key's list, naming no agent)",
				"claude takes one profile", "ignores openrouter", "pi takes the whole list",
				`"profile": {"<agent>": ["zai", "openrouter"]}`} {
				if !strings.Contains(stderr.String(), want) {
					t.Errorf("the launch lines must say %q:\n%s", want, stderr.String())
				}
			}
			o.ProfileName = "zai"
			channel, err = o.composePackChannel(cfg, packs, zaiAndRouterKeys())
			if err != nil || channel.bareNote != "" {
				t.Errorf("a bare -p replaces the key's list everywhere, so nothing is narrowed: %v %q",
					err, channel.bareNote)
			}
		})
	}
}

// AP-D3 for the key's bare list: the entries claude ignores must be declared, as a bare -p's are,
// even with no set-capable agent selected (checkProfileDeclarations reads the fold's list).
func TestAnUndeclaredEntryOfTheProfileKeysBareListRefuses(t *testing.T) {
	home := packHome(t)
	o := goldenOptions(t.TempDir(), home)
	cfg := bareConfig()
	cfg.Set("profile", []any{"zai", "typo"})
	packs := []*packload.Pack{officialPack(t, "claude"), officialPack(t, "zai")}
	_, err := o.composePackChannel(cfg, packs, zaiAndRouterKeys())
	if err == nil || !strings.Contains(err.Error(), `profile "typo" (entry 2 of the profile key's list zai,typo)`) {
		t.Fatalf("composePackChannel = %v, want the key's undeclared entry refused", err)
	}
}
