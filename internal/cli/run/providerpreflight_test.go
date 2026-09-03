package run

import (
	"go/ast"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	officialpacks "github.com/mschulkind-oss/yolo-jail/packs"
)

// This file pins the SELECTED-PACK CREDENTIAL PRE-FLIGHT (profiles-as-pack-variants.md
// §6.2 as rescoped by OQ-13, the requirement itself re-ruled by OQ-PT4): every provider
// the composed table CATALOGS — present and carrying an endpoint — refuses the launch when
// the credential variable its api_key_env_name points at was never hydrated, and an entry
// the table does not hold is nobody's requirement.
//
// The facts live in packload.ProviderCredentialGaps, which both notches call; what this
// file pins at the run pipeline is the SEAM: that the container arm asks the question on
// the environment it actually assembled, and refuses before anything is spawned. The unit
// tests below would stay green if the call site vanished, which is why the last test
// reads the source — the same answer this package already gives for a disclosure that
// exists and is never printed. The check itself is now shared with the macos-user arm
// (profilechanneldispatch_test.go pins that half at the dispatch, where a refusal has to
// land before the backend starts); both arms answer against the SAME composed channel.

// zaiPackFixture is a real staged-shape pack (LoadDir) that ships a provider requiring a
// credential, plus a variant naming it — the shape packs/zai would have. It installs no
// CLI, which is the ordinary provider-pack shape and the reason the check is keyed on the
// PACK and not on the bin a profile is active at.
func zaiPackFixture(t *testing.T, name, provider, keyEnv string) *packload.Pack {
	t.Helper()
	root := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name":"` + name + `","contributes":[` +
		`{"kind":"provider","name":"` + provider + `","api_key_env_name":"` + keyEnv + `",` +
		`"endpoints":{"openai":{"base_url":"https://api.z.ai/api/paas/v4","wire_api":"openai-chat-completions"}}},` +
		`{"kind":"profile","name":"` + provider + `","provider":"` + provider + `"}]}`
	if err := os.WriteFile(filepath.Join(root, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	p, problems := packload.LoadDir(root, name, false)
	if len(problems) != 0 {
		t.Fatalf("loading fixture pack: %v", problems)
	}
	return p
}

// emptyEnv is a hydrated env_sources map that delivered nothing.
func emptyEnv() *jsonx.OrderedMap { return jsonx.NewOrderedMap() }

// channelFor is composePackChannel with the refusal channel turned into a test failure:
// every test below asks what the pre-flight says about a table that COMPOSED. The refusal
// the composition itself can produce — a user base_url merged over a pack that ships
// endpoints — is pinned where it acts, at the dispatch, by
// TestRunRefusesAManufacturedAddressPair.
func channelFor(t *testing.T, o *Options, cfg *jsonx.OrderedMap,
	packs []*packload.Pack, userEnv *jsonx.OrderedMap) *packChannel {
	t.Helper()
	c, err := o.composePackChannel(cfg, packs, userEnv)
	if err != nil {
		t.Fatalf("composing the provider channel: %v", err)
	}
	return c
}

// A selected provider pack whose key was never hydrated refuses, naming the variable, the
// provider, the pack that requires it and the escape hatch.
func TestCheckProviderCredentialsRefusesAnUnhydratedKey(t *testing.T) {
	home := retireHome(t)
	writeUserPacks(t, home, `[]`)
	o := retireOptions(t, discardBuf())
	o.Getenv = func(name string) string {
		if name == "ZAI_API_KEY" {
			return "" // not exported in the invoking shell either
		}
		return ""
	}
	lines, refuse := o.checkProviderCredentials(newConfig(), []*packload.Pack{zaiPackFixture(t, "zai", "zai", "ZAI_API_KEY")}, channelFor(t, o, newConfig(), []*packload.Pack{zaiPackFixture(t, "zai", "zai", "ZAI_API_KEY")}, emptyEnv()), nil)
	if len(lines) == 0 || !refuse {
		t.Fatalf("a selected provider pack with no key hydrated must refuse the launch "+
			"(lines=%d refuse=%v)", len(lines), refuse)
	}
	got := strings.Join(lines, "\n")
	for _, want := range []string{"ZAI_API_KEY", `provider "zai"`, "pack zai"} {
		if !strings.Contains(got, want) {
			t.Errorf("the refusal must name %q:\n%s", want, got)
		}
	}
	if !strings.Contains(got, paths.AllowMissingProvidersEnv+"=1") {
		t.Errorf("a refusal the user cannot get past must name the hatch:\n%s", got)
	}
}

// The requirement follows CATALOG MEMBERSHIP (docs/reference/providers.md / D4, ruled
// by OQ-PT4; docs/reference/providers.md): in a dictionary means you need the
// key; not in one means you do not. Three sides of that one sentence, pinned at the seam
// that consumes the answer:
//
//   - a provider the user null-drops left the composed table, and the requirement left
//     with it. The design measured the shape refusing before the rule moved
//     (docs/reference/providers.md: packs: ["claude"] with providers: {"bedrock":
//     null} came out as "…and the composed providers table has no entry by that name"),
//     the user's own "no" read back as a fault. This fixture is that shape on the zai pack
//     the rest of this file uses;
//   - the same pack with its entry cataloged still refuses, naming the provider and the
//     variable — which is what keeps this a refusal test rather than a delete-the-check
//     test. The ONLY thing that changed between the two halves is table membership;
//   - a variant naming a provider nothing ships and nothing catalogues delivers nothing to
//     any agent — no env derive sees an entry, so it composes nothing — so it is
//     not a requirement either.
func TestCheckProviderCredentialsFollowCatalogMembership(t *testing.T) {
	home := retireHome(t)
	writeUserPacks(t, home, `[]`)
	packs := []*packload.Pack{zaiPackFixture(t, "zai", "zai", "ZAI_API_KEY")}

	dropped := jsonx.NewOrderedMap()
	dropped.Set("zai", nil) // the user's "no" — the entry leaves the composed table
	cfg := newConfig("providers", dropped)
	o := retireOptions(t, discardBuf())
	if lines, refuse := o.checkProviderCredentials(cfg, packs, channelFor(t, o, cfg, packs, emptyEnv()), nil); len(lines) != 0 || refuse {
		t.Errorf("a provider the user null-dropped left the catalog and must not be required "+
			"(lines=%d refuse=%v):\n%s", len(lines), refuse, strings.Join(lines, "\n"))
	}

	o = retireOptions(t, discardBuf())
	lines, refuse := o.checkProviderCredentials(newConfig(), packs, channelFor(t, o, newConfig(), packs, emptyEnv()), nil)
	if len(lines) == 0 || !refuse {
		t.Fatalf("the same pack with its provider cataloged must still refuse "+
			"(lines=%d refuse=%v)", len(lines), refuse)
	}
	if got := strings.Join(lines, "\n"); !strings.Contains(got, `provider "zai"`) ||
		!strings.Contains(got, "ZAI_API_KEY") {
		t.Errorf("the refusal must name the provider and its variable:\n%s", got)
	}

	// The profile half: a selection naming a provider the catalog never held. The fixture
	// ships the profile and NO provider contribution, so nothing composes an entry for
	// "nowhere" — the delivery is nothing, and so is the demand.
	root := filepath.Join(t.TempDir(), "ghost")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name":"ghost","contributes":[` +
		`{"kind":"profile","name":"ghost","provider":"nowhere"}]}`
	if err := os.WriteFile(filepath.Join(root, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	ghost, problems := packload.LoadDir(root, "ghost", false)
	if len(problems) != 0 {
		t.Fatalf("loading fixture pack: %v", problems)
	}
	o = retireOptions(t, discardBuf())
	if lines, refuse := o.checkProviderCredentials(newConfig(), []*packload.Pack{ghost}, channelFor(t, o, newConfig(), []*packload.Pack{ghost}, emptyEnv()), nil); len(lines) != 0 || refuse {
		t.Errorf("a variant naming an uncataloged provider must demand no credential, "+
			"because it delivers none (lines=%d refuse=%v):\n%s", len(lines), refuse,
			strings.Join(lines, "\n"))
	}
}

// The escape hatch lifts the refusal — loudly. The facts stay in the output, because an
// override that goes quiet is an override nobody can audit; what goes is the remedy, which
// would now suggest doing the thing that was just done.
func TestCheckProviderCredentialsHatchLiftsTheRefusal(t *testing.T) {
	home := retireHome(t)
	writeUserPacks(t, home, `[]`)
	o := retireOptions(t, discardBuf())
	o.Getenv = func(name string) string {
		if name == paths.AllowMissingProvidersEnv {
			return "1"
		}
		return ""
	}
	lines, refuse := o.checkProviderCredentials(newConfig(), []*packload.Pack{zaiPackFixture(t, "zai", "zai", "ZAI_API_KEY")}, channelFor(t, o, newConfig(), []*packload.Pack{zaiPackFixture(t, "zai", "zai", "ZAI_API_KEY")}, emptyEnv()), nil)
	got := strings.Join(lines, "\n")
	if !strings.HasPrefix(got, "Warning: "+paths.AllowMissingProvidersEnv+" is set") {
		t.Errorf("the override must say what it is suppressing, first line:\n%s", lines[0])
	}
	if !strings.Contains(got, "ZAI_API_KEY") {
		t.Errorf("the override notice must still name the gap:\n%s", got)
	}
	if strings.Contains(got, "launch anyway with") {
		t.Errorf("the override notice must not offer the hatch it already honoured:\n%s", got)
	}
	// THE VERDICT, not the output, is what ends a launch: an override that kept the
	// caller's `len(lines) > 0` exit would refuse anyway and the hatch would be a
	// placebo. Measured — the first nested launch with the hatch set stopped right here.
	if refuse {
		t.Errorf("the hatch must let the launch proceed, not merely reword the refusal:\n%s", got)
	}
}

// The refusal is quiet once the key arrives by ANY channel the launch would deliver —
// env_sources, the assembled -e argv (a variant's own env, or a provider variable the
// agent's env derive already relayed), or the environment yolo itself was launched
// from. A check that only looked in one of the three would refuse launches that work.
func TestCheckProviderCredentialsSilentOnceTheKeyArrives(t *testing.T) {
	home := retireHome(t)
	writeUserPacks(t, home, `[]`)
	packs := []*packload.Pack{zaiPackFixture(t, "zai", "zai", "ZAI_API_KEY")}

	hydrated := jsonx.NewOrderedMap()
	hydrated.Set("ZAI_API_KEY", "sk-envsource")
	o := retireOptions(t, discardBuf())
	if lines, refuse := o.checkProviderCredentials(newConfig(), packs, channelFor(t, o, newConfig(), packs, hydrated), nil); len(lines) != 0 || refuse {
		t.Errorf("a key from env_sources must satisfy the check:\n%s", strings.Join(lines, "\n"))
	}

	assembled := envPairs([]string{"-e", "SOME_OTHER=1", "-e", "ZAI_API_KEY=sk-argv", "-e", "YOLO_VERSION=9"})
	o = retireOptions(t, discardBuf())
	if lines, refuse := o.checkProviderCredentials(newConfig(), packs, channelFor(t, o, newConfig(), packs, emptyEnv()), assembled); len(lines) != 0 || refuse {
		t.Errorf("a key already on the assembled argv must satisfy the check:\n%s", strings.Join(lines, "\n"))
	}

	o = retireOptions(t, discardBuf())
	o.Getenv = func(name string) string {
		if name == "ZAI_API_KEY" {
			return "sk-shell"
		}
		return ""
	}
	if lines, refuse := o.checkProviderCredentials(newConfig(), packs, channelFor(t, o, newConfig(), packs, emptyEnv()), nil); len(lines) != 0 || refuse {
		t.Errorf("a key exported in the invoking shell must satisfy the check — the env "+
			"derive can draw on it:\n%s", strings.Join(lines, "\n"))
	}
}

// An EMPTY value is not a credential. agentenv drops an empty {key} result rather than
// composing an empty token, so a launch carrying ZAI_API_KEY= is the failure this check
// exists to name, not an escape from it.
func TestCheckProviderCredentialsTreatsAnEmptyValueAsMissing(t *testing.T) {
	home := retireHome(t)
	writeUserPacks(t, home, `[]`)
	hydrated := jsonx.NewOrderedMap()
	hydrated.Set("ZAI_API_KEY", "")
	o := retireOptions(t, discardBuf())
	if lines, refuse := o.checkProviderCredentials(newConfig(), []*packload.Pack{zaiPackFixture(t, "zai", "zai", "ZAI_API_KEY")}, channelFor(t, o, newConfig(), []*packload.Pack{zaiPackFixture(t, "zai", "zai", "ZAI_API_KEY")}, hydrated), nil); len(lines) == 0 || !refuse {
		t.Fatal("an empty credential variable must not satisfy the check")
	}
}

// A provider that declares no api_key_env_name is checked for EXISTENCE only. This is the
// shipped packs/claude shape — bedrock's credential is the ambient AWS chain — so getting
// this wrong refuses every claude launch on every machine.
func TestCheckProviderCredentialsExistenceOnlyWithoutAKeyVariable(t *testing.T) {
	home := retireHome(t)
	writeUserPacks(t, home, `[]`)
	root := filepath.Join(t.TempDir(), "aws")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name":"aws","contributes":[` +
		`{"kind":"provider","name":"bedrock",` +
		`"endpoints":{"anthropic":{"base_url":"https://bedrock.runtime.us-east-1.amazonaws.com"}}}]}`
	if err := os.WriteFile(filepath.Join(root, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	p, problems := packload.LoadDir(root, "aws", false)
	if len(problems) != 0 {
		t.Fatalf("loading fixture pack: %v", problems)
	}
	o := retireOptions(t, discardBuf())
	if lines, refuse := o.checkProviderCredentials(newConfig(), []*packload.Pack{p}, channelFor(t, o, newConfig(), []*packload.Pack{p}, emptyEnv()), nil); len(lines) != 0 || refuse {
		t.Errorf("a provider with no credential pointer must be satisfied by existing:\n%s",
			strings.Join(lines, "\n"))
	}
}

// OQ-13's scope is the SELECTED pack. A pack that ships a provider and needs a key is
// inert when this launch does not select it — which is the ordinary case for a shared
// workspace config that names more providers than any one machine has keys for. Pinned as
// a contrast in both directions, so the silence cannot be the check being inert.
func TestCheckProviderCredentialsIgnoresAnUnselectedPack(t *testing.T) {
	home := retireHome(t)
	writeUserPacks(t, home, `[]`)
	requireing := zaiPackFixture(t, "zai", "zai", "ZAI_API_KEY")
	selected := packsFixture(t, "pi") // the shipped pi pack: no provider, no key, no claim

	o := retireOptions(t, discardBuf())
	if lines, refuse := o.checkProviderCredentials(newConfig(), selected, channelFor(t, o, newConfig(), selected, emptyEnv()), nil); len(lines) != 0 || refuse {
		t.Fatalf("a launch that does not select the requiring pack must stay silent:\n%s",
			strings.Join(lines, "\n"))
	}
	if lines, refuse := o.checkProviderCredentials(newConfig(), append(selected, requireing), channelFor(t, o, newConfig(), append(selected, requireing), emptyEnv()), nil); len(lines) == 0 || !refuse {
		t.Fatalf("the same launch with the requiring pack SELECTED must refuse (lines=%d refuse=%v)",
			len(lines), refuse)
	}
}

// THE SEAM. The unit tests above pin what the check says; nothing about them would notice
// if runContainer stopped calling it, and runContainer starts a real container, so a unit
// test has no other witness. Reading the source is this package's existing answer to that
// shape (TestFreshLaunchPrintsTheProfileLineBesideTheHostAccessLine, which this mirrors,
// including the ordering assertions): the check has to run AFTER the argv is assembled —
// that argv is the environment it answers against — and BEFORE the host daemons spawn, so
// a refusal never has to unwind a process it started.
func TestFreshLaunchChecksProviderCredentialsOnTheAssembledEnv(t *testing.T) {
	const (
		assemble = "assembleRunCmd"
		check    = "checkProviderCredentials"
		daemons  = "startLoopholesDisclosed"
	)
	fn := methodDecl(t, "run.go", "runContainer")

	pos := map[string]token.Pos{}
	ast.Inspect(fn, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if _, seen := pos[sel.Sel.Name]; !seen {
			pos[sel.Sel.Name] = call.Pos()
		}
		return true
	})

	if _, ok := pos[check]; !ok {
		t.Fatalf("runContainer no longer calls %s. The check still exists and its unit tests "+
			"still pass, so a launch with no provider credential would start a jail that fails "+
			"its first API call and say nothing — the failure §6.1 records. If the seam moved, "+
			"move this assertion with it rather than deleting it. (The macos-user arm's own "+
			"call site is pinned at the dispatch, not here — see "+
			"TestProfileChannelPreflightRefusesTheMacosUserLaunch.)", check)
	}
	if pos[check] < pos[assemble] {
		t.Errorf("runContainer calls %s BEFORE %s: the check answers against the assembled -e "+
			"argv, so it cannot run before that argv exists", check, assemble)
	}
	if pos[check] > pos[daemons] {
		t.Errorf("runContainer calls %s AFTER %s: a refusal must land before any host-side "+
			"daemon a refusal would have to clean up", check, daemons)
	}
}

// guard: the fixture above must never be able to satisfy the check by accident. The shipped
// packs were credential-silent for exactly as long as none of them shipped a provider; zai
// ends that (zai-plumbing.md §4), so the guard narrows instead of dying: every pack that
// INSTALLS something must still need no key — a golden-argv launch or an integration jail
// selects one of those, and a key-bearing program pack would refuse every one of them — and
// the exception must be a pack no launch reaches without naming it. That is what keeps "the
// shipped set" and "the set a launch is asked to deliver" different things.
func TestShippedPacksRequireNoCredential(t *testing.T) {
	home := retireHome(t)
	writeUserPacks(t, home, `[]`)
	loaded, problems := packload.MaterializeEmbedded(officialpacks.FS, t.TempDir())
	if len(problems) != 0 {
		t.Fatalf("materializing official packs: %v", problems)
	}

	var programPacks, requiring []*packload.Pack
	for _, p := range loaded {
		if len(p.Decl.InstallContributions()) > 0 {
			programPacks = append(programPacks, p)
		}
		o := retireOptions(t, discardBuf())
		if lines, refuse := o.checkProviderCredentials(newConfig(), []*packload.Pack{p}, channelFor(t, o, newConfig(), []*packload.Pack{p}, emptyEnv()), nil); len(lines) != 0 || refuse {
			requiring = append(requiring, p)
		}
	}
	if len(programPacks) == 0 {
		t.Fatal("no shipped pack installs anything — this test is not testing anything")
	}
	o := retireOptions(t, discardBuf())
	if lines, refuse := o.checkProviderCredentials(newConfig(), programPacks, channelFor(t, o, newConfig(), programPacks, emptyEnv()), nil); len(lines) != 0 || refuse {
		t.Errorf("the shipped packs that install a CLI must not require a credential on a bare "+
			"launch:\n%s", strings.Join(lines, "\n"))
	}

	// The exception, named: today that is zai alone, and it installs no CLI, so it is
	// delivered only to a launch that selected it — which is the launch that also owes it a
	// key. A second credential-bearing pack needs the same property, or this narrow guard
	// has to become an explicit allowlist instead.
	for _, p := range requiring {
		if p.Name != "zai" {
			t.Errorf("shipped pack %q requires a credential — the set of credential-bearing "+
				"shipped packs is deliberate, and a second entry is a decision to make here, "+
				"not drift", p.Name)
		}
		if len(p.Decl.InstallContributions()) > 0 {
			t.Errorf("shipped pack %q both installs a CLI and requires a credential: every "+
				"launch that selects it would refuse until the key is present, which is the "+
				"property the program-pack check above guards", p.Name)
		}
	}
}
