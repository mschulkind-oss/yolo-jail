package run

// regionpreflight_test.go pins the REGION PRE-FLIGHT at the jail notch
// (docs/design/bedrock-plumbing.md §8, OQ-BR6): a launch whose profile selects the shipped
// `bedrock` provider, with no region on the provider and no AWS_REGION or AWS_DEFAULT_REGION
// in what reaches the jail, is refused on every arm — the fresh container launch, the attach
// delivery and every macos-user invocation — and only then.
//
// The facts are packload.ProviderRegionGaps' (pinned there); what this file pins is that each
// ARM asks. Every test drives the shipped claude pack, so deleting its `region_env_name`
// fails here too. The three arms share one entry point, checkProviderCredentials, whose own
// call sites are pinned by the tests beside it (TestFreshLaunchChecksProviderCredentialsOnTheAssembledEnv,
// TestProfileChannelPreflightRefusesTheMacosUserLaunch, the attach tests); the tests here fail
// if that entry point stops asking about the region, and each drives its arm through the code a
// launch runs rather than through the callee alone.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/image"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// regionVerdict is the region pre-flight's verdict line (packload.ProviderRegionRefusal).
const regionVerdict = "Refusing to launch: a selected provider is reached through a region, and this launch names none."

// bedrockOnClaude is the shipped claude pack with claude on its `bedrock` profile, and the
// options that select it.
func bedrockOnClaude(t *testing.T, o *Options) []*packload.Pack {
	t.Helper()
	o.UseProfiles = map[string]string{"claude": "bedrock"}
	return []*packload.Pack{officialPack(t, "claude"), officialPack(t, "bedrock")}
}

// THE SHARED ENTRY POINT ASKS BOTH QUESTIONS. checkProviderCredentials is what every jail arm
// calls; deleting its call to checkProviderRegions leaves the control below with no refusal.
// Each silence is pinned against that control, one region source at a time: the provider's
// `region` from the user's config, AWS_REGION or AWS_DEFAULT_REGION from env_sources, and — on
// the container arm only — a `-e` pair of the assembled argv.
func TestCheckProviderCredentialsAsksForTheRegion(t *testing.T) {
	home := retireHome(t)
	writeUserPacks(t, home, `[]`)
	o := retireOptions(t, discardBuf())
	o.Getenv = shellWith(nil)
	packs := bedrockOnClaude(t, o)

	lines, refuse := o.checkProviderCredentials(newConfig(), packs, channelFor(t, o, newConfig(), packs, emptyEnv()), nil)
	if !refuse || len(lines) == 0 || lines[0] != regionVerdict {
		t.Fatalf("claude on bedrock with no region anywhere must refuse on the region (refuse=%v):\n%s",
			refuse, strings.Join(lines, "\n"))
	}
	got := strings.Join(lines, "\n")
	for _, want := range []string{`pack bedrock requires a region for provider "bedrock"`,
		"neither AWS_REGION nor AWS_DEFAULT_REGION is set",
		`"providers": {"bedrock": {"region": "<region>"}}`,
		packload.FromEnvSources + ": none configured", packload.FromPackEnv, packload.FromProfileEnv,
		// The region file the fill consulted (BR-DIR1): this launch environment names no HOME,
		// so it says it read none.
		"~/.aws/config (profile \"default\", since nothing names another): not read",
		paths.AllowMissingProvidersEnv + "=1"} {
		if !strings.Contains(got, want) {
			t.Errorf("the refusal must say %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "not counted") {
		t.Errorf("yolo reads the region file now, so the refusal must not say it is not counted:\n%s", got)
	}
	if strings.Contains(got, packload.FromContainerArgv) {
		t.Errorf("no argv was assembled, so the argv is not a consulted channel:\n%s", got)
	}

	cfg := newConfig()
	withBedrockRegion(cfg)
	if lines, refuse := o.checkProviderCredentials(cfg, packs, channelFor(t, o, cfg, packs, emptyEnv()), nil); refuse || len(lines) != 0 {
		t.Errorf("a region on the user's bedrock provider must satisfy the pre-flight:\n%s", strings.Join(lines, "\n"))
	}
	for _, v := range []string{"AWS_REGION", "AWS_DEFAULT_REGION"} {
		env := userEnvWith(map[string]string{v: "eu-west-1"})
		if lines, refuse := o.checkProviderCredentials(newConfig(), packs, channelFor(t, o, newConfig(), packs, env), nil); refuse || len(lines) != 0 {
			t.Errorf("%s from env_sources must satisfy the pre-flight:\n%s", v, strings.Join(lines, "\n"))
		}
	}
	argv := envPairs([]string{"-e", "AWS_REGION=eu-west-1"})
	if lines, refuse := o.checkProviderCredentials(newConfig(), packs, channelFor(t, o, newConfig(), packs, emptyEnv()), argv); refuse || len(lines) != 0 {
		t.Errorf("AWS_REGION on the assembled container argv must satisfy the pre-flight:\n%s", strings.Join(lines, "\n"))
	}
	// And with an argv assembled, the argv is named among the channels consulted.
	lines, _ = o.checkProviderCredentials(newConfig(), packs, channelFor(t, o, newConfig(), packs, emptyEnv()),
		envPairs([]string{"-e", "UNRELATED=1"}))
	if got := strings.Join(lines, "\n"); !strings.Contains(got, packload.FromContainerArgv) {
		t.Errorf("a container launch must name its argv as a consulted channel:\n%s", got)
	}
}

// A REGION IN THE INVOKING SHELL DOES NOT REACH A JAIL, so it satisfies nothing there — and the
// refusal says it saw it, because that is the one form of the mistake a user can see. Nothing
// relays AWS_REGION out of that environment (the claude derive composes it from the provider's
// `region` only), unlike a credential the derive relays, which the credential half counts.
func TestARegionOnlyInTheLaunchShellIsNamedAndNotCounted(t *testing.T) {
	home := retireHome(t)
	writeUserPacks(t, home, `[]`)
	o := retireOptions(t, discardBuf())
	o.Getenv = shellWith(map[string]string{"AWS_REGION": "us-west-2"})
	packs := bedrockOnClaude(t, o)
	lines, refuse := o.checkProviderCredentials(newConfig(), packs, channelFor(t, o, newConfig(), packs, emptyEnv()), nil)
	if !refuse {
		t.Fatalf("a region only in the shell yolo was launched from must not satisfy a jail launch:\n%s",
			strings.Join(lines, "\n"))
	}
	if got := strings.Join(lines, "\n"); !strings.Contains(got,
		"AWS_REGION is set in the environment yolo was launched from, which this launch does not deliver to the agent") {
		t.Errorf("the refusal must name the stranded AWS_REGION:\n%s", got)
	}
}

// THE HATCH is the credential pre-flight's, and it is a loud continuation here too.
func TestTheRegionRefusalHonorsTheProviderHatch(t *testing.T) {
	home := retireHome(t)
	writeUserPacks(t, home, `[]`)
	o := retireOptions(t, discardBuf())
	o.Getenv = shellWith(map[string]string{paths.AllowMissingProvidersEnv: "1"})
	packs := bedrockOnClaude(t, o)
	lines, refuse := o.checkProviderCredentials(newConfig(), packs, channelFor(t, o, newConfig(), packs, emptyEnv()), nil)
	if refuse {
		t.Errorf("the hatch must let the launch proceed:\n%s", strings.Join(lines, "\n"))
	}
	if len(lines) == 0 || !strings.HasPrefix(lines[0], "Warning: "+paths.AllowMissingProvidersEnv+" is set") ||
		!strings.Contains(strings.Join(lines, "\n"), `provider "bedrock"`) {
		t.Errorf("the held notice must say what it suppresses:\n%s", strings.Join(lines, "\n"))
	}
}

// bedrockNativeLaunch drives Run() to the macos-user arm with the shipped claude pack and
// `-p bedrock`, over userConfig and an empty invoking shell, and reports whether the handler
// ran and what it was handed.
func bedrockNativeLaunch(t *testing.T, userConfig string) (int, *nativeLaunch, string) {
	t.Helper()
	o, stderr, seen := overrideNativeLaunch(t, userConfig, shellWith(nil))
	rc := Run(*o)
	return rc, seen, stderr.String()
}

// THE macos-user ARM refuses before the backend is dispatched, and launches once the provider
// names a region, which reaches the sandbox as claude's AWS_REGION. Two launches only: each
// starts the machine's host services, so the other region sources (env_sources, the invoking
// shell) are pinned on the shared entry point above rather than by more launches here, and the
// unprofiled control is TestUnprofiledNativeLaunchStillCarriesTheEmptyWireTables, whose claude
// launch selects no bedrock and must not be refused.
func TestTheMacosUserLaunchRefusesABedrockProfileWithNoRegion(t *testing.T) {
	rc, seen, errs := bedrockNativeLaunch(t, `{"packs": ["claude"]}`)
	if rc != 1 || seen.reached {
		t.Fatalf("claude on bedrock with no region must refuse the macos-user launch before the "+
			"backend runs: rc=%d reached=%v\n%s", rc, seen.reached, errs)
	}
	if !strings.Contains(errs, regionVerdict) || !strings.Contains(errs, `provider "bedrock"`) {
		t.Errorf("the refusal must be the region pre-flight's:\n%s", errs)
	}

	rc, seen, errs = bedrockNativeLaunch(t, `{"packs": ["claude"]`+bedrockRegionMember+`}`)
	if rc != 0 || !seen.reached {
		t.Fatalf("with the provider's region set the launch must run: rc=%d reached=%v\n%s", rc, seen.reached, errs)
	}
	if got := envAt(seen.env, "AWS_REGION"); got != testBedrockRegion {
		t.Errorf("claude's launch env AWS_REGION = %q, want the provider's %q", got, testBedrockRegion)
	}
}

// THE ATTACH ARM refuses before it writes the live channel file, so a refused entry leaves the
// running jail's file as the previous entry wrote it; with a region it delivers.
func TestAnAttachSelectingBedrockWithNoRegionRefusesBeforeTheWrite(t *testing.T) {
	packs := []*packload.Pack{officialPack(t, "claude"), officialPack(t, "bedrock")}
	o, cfg, channel, stderr := attachFixture(t, currentJailEnv, packs, emptyEnv(),
		func(o *Options, _ *jsonx.OrderedMap) { o.ProfileName = "bedrock" })
	file := filepath.Join(paths.WorkspaceHomeState(o.Workspace), "yolo-user-env.sh")
	if rc := o.deliverChannelOnAttach("yolo-ws-abcd1234", "podman", cfg,
		stagedPacks{root: "/ctx/packs", packs: packs}, channel); rc != 1 {
		t.Fatalf("an attach selecting bedrock with no region must refuse: rc=%d\n%s", rc, stderr.String())
	}
	if !strings.Contains(stderr.String(), regionVerdict) {
		t.Errorf("the attach refusal must be the region pre-flight's:\n%s", stderr.String())
	}
	if _, err := os.Stat(file); err == nil {
		t.Errorf("a refused attach wrote the live channel file %s", file)
	}

	o, cfg, channel, stderr = attachFixture(t, currentJailEnv, packs, emptyEnv(),
		func(o *Options, cfg *jsonx.OrderedMap) { o.ProfileName = "bedrock"; withBedrockRegion(cfg) })
	if rc := o.deliverChannelOnAttach("yolo-ws-abcd1234", "podman", cfg,
		stagedPacks{root: "/ctx/packs", packs: packs}, channel); rc != 0 {
		t.Fatalf("with the provider's region the attach must deliver: rc=%d\n%s", rc, stderr.String())
	}
}

// THE FRESH CONTAINER ARM, driven through Run() down the podman path to the pre-flight, with
// every runtime question stubbed and no podman on PATH — so a launch that stopped refusing could
// start no container, and would fail somewhere this test tells apart. The region refusal must
// land, and no `run` may have been asked of the runtime.
func TestTheFreshPodmanLaunchRefusesABedrockProfileWithNoRegion(t *testing.T) {
	home := packHome(t)
	writeUserConfig(t, home, `{"packs": ["claude"]}`)
	t.Setenv("PATH", t.TempDir())
	ws := t.TempDir()
	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, ws, "podman", &stdout, &stderr, nil)
	o.Args = []string{"claude"}
	o.ProfileName = "bedrock"
	o.Getenv = shellWith(nil)
	repo, _ := o.RepoRoot()
	o.PathExists = func(p string) bool {
		return p == filepath.Join(prebuiltBinDir(repo.Root), "yolo-entrypoint")
	}
	var ran [][]string
	o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
		ran = append(ran, argv)
		if len(argv) >= 2 && argv[1] == "info" {
			return ExecResult{Ran: true, RC: 0, Stdout: "host: {}"}
		}
		return ExecResult{Ran: true, RC: 0}
	}
	o.autoLoad = func(image.AutoLoadOptions) image.LoadResult {
		return image.LoadResult{OK: true, Ref: goldenImageRef}
	}
	if rc := Run(*o); rc != 1 {
		t.Fatalf("Run() = %d, want 1\nstdout:\n%s\nstderr:\n%s", rc, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), regionVerdict) {
		t.Fatalf("the fresh podman launch did not refuse on the region:\nstdout:\n%s\nstderr:\n%s",
			stdout.String(), stderr.String())
	}
	for _, argv := range ran {
		if len(argv) >= 2 && argv[1] == "run" {
			t.Errorf("a refused launch asked the runtime to run a container: %v", argv)
		}
	}
}

// PER AGENT (the review's reproduction of the first build): claude on `bedrock` and codex on an
// inline pack's `local` profile, whose gated env gives codex, and only codex, an AWS_REGION. The
// launch-wide lookup counted codex's region for claude and let claude start with none; the
// region is asked of each agent on the provider, so claude is refused, and its own region then
// satisfies it.
func TestTheRegionIsAskedOfEachAgentOnTheProvider(t *testing.T) {
	home := retireHome(t)
	writeUserPacks(t, home, `[]`)
	o := retireOptions(t, discardBuf())
	o.Getenv = shellWith(nil)
	local := inlinePack(t, "local", `{"name":"local","contributes":[
	  {"kind":"provider","name":"local"},
	  {"kind":"profile","name":"local","provider":"local"},
	  {"kind":"env","profile":"local","vars":{"AWS_REGION":"eu-west-9"}}]}`)
	packs := []*packload.Pack{officialPack(t, "claude"), officialPack(t, "codex"),
		officialPack(t, "openai-auth"), officialPack(t, "bedrock"), local}
	o.UseProfiles = map[string]string{"claude": "bedrock", "codex": "local"}

	channel := channelFor(t, o, newConfig(), packs, emptyEnv())
	if v, _ := channel.scope.DeliveredTo("codex", "AWS_REGION"); v != "eu-west-9" {
		t.Fatalf("fixture: codex's own gated env must deliver it AWS_REGION, got %q", v)
	}
	if v, ok := channel.scope.DeliveredTo("claude", "AWS_REGION"); ok {
		t.Fatalf("fixture: claude must receive no AWS_REGION, got %q", v)
	}
	lines, refuse := o.checkProviderCredentials(newConfig(), packs, channel, nil)
	got := strings.Join(lines, "\n")
	if !refuse || !strings.Contains(got, `requires a region for provider "bedrock" (platform "aws-bedrock"), selected for claude`) {
		t.Fatalf("a region only codex receives must not satisfy claude's bedrock (refuse=%v):\n%s", refuse, got)
	}
	if strings.Contains(got, "selected for claude and codex") {
		t.Errorf("codex is not on bedrock and must not be named:\n%s", got)
	}

	cfg := newConfig()
	withBedrockRegion(cfg)
	if lines, refuse := o.checkProviderCredentials(cfg, packs, channelFor(t, o, cfg, packs, emptyEnv()), nil); refuse {
		t.Errorf("control: a region on claude's provider satisfies it:\n%s", strings.Join(lines, "\n"))
	}
}

// OPENCODE IS ASKED ONLY FOR AWS_REGION (BR-D18): its Bedrock loader never reads
// AWS_DEFAULT_REGION and falls back to us-east-1, so an env_sources AWS_DEFAULT_REGION that
// satisfies claude beside it is no region for opencode, and the launch is refused naming
// opencode alone and the variable that reached it unread. Through the shared entry point every
// jail arm calls, over the shipped packs, so it fails if packs/opencode stops declaring its
// variables or the pre-flight stops asking each agent for its own.
func TestOpencodeOnBedrockIsNotGivenARegionItDoesNotRead(t *testing.T) {
	home := retireHome(t)
	writeUserPacks(t, home, `[]`)
	o := retireOptions(t, discardBuf())
	o.Getenv = shellWith(nil)
	packs := []*packload.Pack{officialPack(t, "claude"), officialPack(t, "opencode"),
		officialPack(t, "openai-auth"), officialPack(t, "bedrock")}
	o.UseProfiles = map[string]string{"claude": "bedrock", "opencode": "bedrock"}

	env := userEnvWith(map[string]string{"AWS_DEFAULT_REGION": "eu-west-1"})
	lines, refuse := o.checkProviderCredentials(newConfig(), packs, channelFor(t, o, newConfig(), packs, env), nil)
	got := strings.Join(lines, "\n")
	if !refuse || !strings.Contains(got, `requires a region for provider "bedrock" (platform "aws-bedrock"), selected for opencode:`) {
		t.Fatalf("opencode given only AWS_DEFAULT_REGION must be refused (refuse=%v):\n%s", refuse, got)
	}
	for _, want := range []string{"AWS_REGION is not set in what this launch delivers to opencode",
		`AWS_DEFAULT_REGION reaches opencode, which does not read it: pack opencode says opencode reads its region on "aws-bedrock" from AWS_REGION alone`,
		"AWS_REGION=<region> in an env_sources entry"} {
		if !strings.Contains(got, want) {
			t.Errorf("the refusal must say %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "selected for claude") || strings.Contains(got, "claude and opencode") {
		t.Errorf("claude reads AWS_DEFAULT_REGION and must not be named:\n%s", got)
	}

	env = userEnvWith(map[string]string{"AWS_REGION": "eu-west-1"})
	if lines, refuse := o.checkProviderCredentials(newConfig(), packs, channelFor(t, o, newConfig(), packs, env), nil); refuse {
		t.Errorf("control: AWS_REGION satisfies opencode and claude:\n%s", strings.Join(lines, "\n"))
	}
	cfg := newConfig()
	withBedrockRegion(cfg)
	if lines, refuse := o.checkProviderCredentials(cfg, packs, channelFor(t, o, cfg, packs, emptyEnv()), nil); refuse {
		t.Errorf("control: the provider's region satisfies opencode, whose derive writes it as options.region:\n%s",
			strings.Join(lines, "\n"))
	}
}

// A REGIONAL PROVIDER ANYWHERE IN AN ACTIVE SET is asked for its region (docs/design/active-
// provider-sets.md AP-P1): pi on [zai, bedrock], the Bedrock entry second, with no region on the
// provider and none delivered, is refused naming bedrock and pi, where reading the primary alone
// sees zai and asks nothing. With the provider's region set the pre-flight passes, and pi's own
// environment carries it as AWS_REGION: the region pre-flight counts a provider's `region` as
// delivered because the agent's derive relays it, so pi's derive must relay it for a later entry
// too (packs/pi/derive.lua, piNativeBedrockEntry), or the check would pass a pi started on no
// region.
func TestTheRegionIsAskedOfALaterEntryOfPisSet(t *testing.T) {
	home := retireHome(t)
	writeUserPacks(t, home, `[]`)
	o := retireOptions(t, discardBuf())
	o.Getenv = shellWith(nil)
	packs := []*packload.Pack{officialPack(t, "pi"), officialPack(t, "zai"), officialPack(t, "bedrock")}
	o.UseProfiles = map[string]string{"pi": "zai,bedrock"}
	env := userEnvWith(map[string]string{"ZAI_API_KEY": "tok-zai"})

	lines, refuse := o.checkProviderCredentials(newConfig(), packs, channelFor(t, o, newConfig(), packs, env), nil)
	got := strings.Join(lines, "\n")
	if !refuse || !strings.Contains(got, `requires a region for provider "bedrock" (platform "aws-bedrock"), selected for pi`) {
		t.Fatalf("pi's second entry on bedrock with no region must refuse (refuse=%v):\n%s", refuse, got)
	}

	cfg := newConfig()
	withBedrockRegion(cfg)
	channel := channelFor(t, o, cfg, packs, env)
	lines, _ = o.checkProviderCredentials(cfg, packs, channel, nil)
	if got := strings.Join(lines, "\n"); strings.Contains(got, "requires a region") {
		t.Errorf("a region on the provider satisfies the pre-flight for pi's second entry:\n%s", got)
	}
	if v, _ := channel.scope.DeliveredTo("pi", "AWS_REGION"); v != testBedrockRegion {
		t.Errorf("pi on [zai, bedrock] must receive the provider's region as AWS_REGION, got %q", v)
	}
}

// THROUGH THE WIRE BRIDGE THE BRIDGE READS THE REGION (docs/design/wire-bridge-gateway.md WG-I38):
// opencode on `bedrock-bridge` sends its requests to the bridge's via route, and the bridge, not
// opencode's own Bedrock loader, reads the region from what reaches opencode, AWS_REGION then
// AWS_DEFAULT_REGION. So AWS_DEFAULT_REGION alone is a region for that launch, and refusing it as
// one "opencode does not read" refuses a launch the bridge serves. On `-p bedrock` opencode's own
// client reads the region and the refusal stands (TestOpencodeOnBedrockIsNotGivenARegionItDoesNotRead).
func TestOpencodeThroughTheBridgeIsGivenTheRegionTheBridgeReads(t *testing.T) {
	home := retireHome(t)
	writeUserPacks(t, home, `[]`)
	o := retireOptions(t, discardBuf())
	o.Getenv = shellWith(nil)
	packs := []*packload.Pack{officialPack(t, "opencode"), officialPack(t, "openai-auth"),
		officialPack(t, "bedrock"), officialPack(t, "wire-bridge")}
	o.UseProfiles = map[string]string{"opencode": "bedrock-bridge"}

	env := userEnvWith(map[string]string{"AWS_DEFAULT_REGION": "eu-west-1"})
	channel := channelFor(t, o, newConfig(), packs, env)
	if r := channel.resolvedProfiles["bedrock-bridge"]; packload.ViaURLFor(r, "opencode") == "" {
		t.Fatalf("fixture: opencode's bedrock-bridge via is not served at this notch (%+v), so this checks nothing", r)
	}
	if lines, refuse := o.checkProviderCredentials(newConfig(), packs, channel, nil); refuse {
		t.Errorf("opencode through the bridge given AWS_DEFAULT_REGION, which the bridge reads, was refused:\n%s",
			strings.Join(lines, "\n"))
	}
	env = userEnvWith(nil)
	if lines, refuse := o.checkProviderCredentials(newConfig(), packs, channelFor(t, o, newConfig(), packs, env), nil); !refuse {
		t.Errorf("control: opencode through the bridge with no region at all must still be refused:\n%s",
			strings.Join(lines, "\n"))
	}
}
