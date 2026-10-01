package run

import (
	"bytes"
	"go/ast"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	officialpacks "github.com/mschulkind-oss/yolo-jail/packs"
)

// This file pins the FLAG half of the CLI-name namespace
// (docs/reference/providers.md#declaring-and-selecting-a-profile): `-p <cli>=<name>` keys a profile by CLI
// name, and a name no resolvable pack installs is refused at launch. The CONFIG half
// is validated by ValidateConfig; the flags never reach a config validator, so the
// launch pipeline owns this check — the same silent-typo hole the profile-variant design documented,
// arriving through argv.
//
// THE BARE `-p <name>` IS NOT IN THAT NAMESPACE and is not checked against it. It
// names no CLI; it is the selection for every bin every selected pack installs. Until
// 2026-09-19 the pre-flight paired it with the `--` command's basename instead, which
// refused `yolo -p kilo -- sleep 60` for a keying effectiveUseProfiles has never
// performed — so the tests below pin BOTH directions of that split, and the delivery
// tests further down pin that the bare+non-agent launch really does carry the profile
// to the jail's CLIs.
//
// Driven through stageRunPacks (the launch path, above the backend dispatch and
// covering attach too), not the checker, so a test fails if the check is unwired
// from staging.

// A -p <cli>=<name> naming a CLI no pack installs is fatal, naming the CLI and the
// installed names.
func TestStageRunPacksRefusesAnUnknownUseProfileCLI(t *testing.T) {
	home := retireHome(t)
	writeUserPacks(t, home, `[]`)
	var out bytes.Buffer
	o := retireOptions(t, &out)
	// retireOptions points the buffer at STDERR, because that is where retirement's notices
	// belong and its own tests assert the stream that way. The refusal asserted here is
	// stageRunPacks', which still writes to stdout (see loopholeretire.go's header on the ~50
	// sites left alone), so this test names the stream it actually reads.
	o.Stdout = &out
	o.UseProfiles = map[string]string{"cloude": "bedrock"}
	if _, ok := o.stageRunPacks("yolo-profile-target-cli"); ok {
		t.Fatalf("a -p <cli>=<name> naming a CLI no pack installs staged cleanly — " +
			"the typo passes silently")
	}
	if !strings.Contains(out.String(), `no pack installs a CLI named "cloude"`) {
		t.Errorf("the refusal must name the unknown CLI:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "claude") {
		t.Errorf("the refusal must list the installed CLI names, including claude:\n%s",
			out.String())
	}
}

// A BARE -p WITH A NON-AGENT COMMAND LAUNCHES. `yolo -p kilo -- sleep 60` is a shell, a
// script or a probe in a jail that has a profile active, and until 2026-09-19 it was
// refused as `no pack installs a CLI named "sleep"` — a check on the token after `--`,
// which is not a profile target and never was (effectiveUseProfiles keys every selected
// pack's bin, whatever the command). The whole point of the in-jail launcher shims is
// that yolo stops caring which CLI the launch happens to run.
//
// `sleep` rather than a made-up name on purpose: the refusal that was here fired on any
// basename outside the installed set, so a real, ordinary command is the case that was
// actually being blocked. Two args, because the argv after `--` carries the command's own
// arguments and only Args[0] was ever read.
func TestStageRunPacksAcceptsABareProfileWithANonAgentCommand(t *testing.T) {
	home := retireHome(t)
	writeUserPacks(t, home, `[]`)
	var out bytes.Buffer
	o := retireOptions(t, &out)
	// retireOptions points the buffer at STDERR; stageRunPacks' refusal goes to stdout
	// (see loopholeretire.go's header on the ~50 sites left alone), so this test names the
	// stream it actually reads.
	o.Stdout = &out
	o.ProfileName = "kilo"
	o.Args = []string{"sleep", "60"}
	if _, ok := o.stageRunPacks("yolo-profile-target-nonagent"); !ok {
		t.Fatalf("a bare -p with a non-agent command must launch — the profile is for the "+
			"CLIs the jail installs, not for the token after `--`:\n%s", out.String())
	}
	if strings.Contains(out.String(), "no pack installs a CLI named") {
		t.Errorf("the dropped bare-form refusal is back:\n%s", out.String())
	}
}

// THE EXPLICIT FORM STILL REFUSES WITH A NON-AGENT COMMAND IN PLAY. `-p claud=kilo` means
// the user believes they configured claude and did not, and nothing downstream would tell
// them — that is the case checkProfileTargets exists for, and dropping the bare-form
// branch must not take it along. Both spellings are set here, so a fix that keyed the
// check off "is any profile flag present" instead of off the explicit map would pass the
// test above and fail this one.
func TestStageRunPacksStillRefusesAnUnknownCLIBesideANonAgentCommand(t *testing.T) {
	home := retireHome(t)
	writeUserPacks(t, home, `[]`)
	var out bytes.Buffer
	o := retireOptions(t, &out)
	o.Stdout = &out
	o.ProfileName = "kilo"
	o.Args = []string{"sleep", "60"}
	o.UseProfiles = map[string]string{"claud": "kilo"}
	if _, ok := o.stageRunPacks("yolo-profile-target-typo"); ok {
		t.Fatalf("-p claud=kilo staged cleanly — the explicit form names a CLI, so a typo "+
			"in it is still worth refusing:\n%s", out.String())
	}
	if !strings.Contains(out.String(), `no pack installs a CLI named "claud"`) {
		t.Errorf("the refusal must name the misspelled CLI, not the command:\n%s", out.String())
	}
	if strings.Contains(out.String(), `"sleep"`) {
		t.Errorf("the command after `--` must not appear in the refusal at all:\n%s",
			out.String())
	}
}

// The positive direction: selectors naming CLIs the embedded packs install stage
// cleanly. Without this, the check could refuse everything and the two tests above
// would still pass.
func TestStageRunPacksAcceptsProfileTargetsThePacksInstall(t *testing.T) {
	home := retireHome(t)
	writeUserPacks(t, home, `[]`)
	var out bytes.Buffer
	o := retireOptions(t, &out)
	// retireOptions points the buffer at STDERR, because that is where retirement's notices
	// belong and its own tests assert the stream that way. The refusal asserted here is
	// stageRunPacks', which still writes to stdout (see loopholeretire.go's header on the ~50
	// sites left alone), so this test names the stream it actually reads.
	o.Stdout = &out
	o.ProfileName = "dev"
	o.Args = []string{"claude"}
	o.UseProfiles = map[string]string{"pi": "glm"}
	if _, ok := o.stageRunPacks("yolo-profile-target-known"); !ok {
		t.Fatalf("selectors naming installed CLIs must stage cleanly:\n%s", out.String())
	}
}

// --- GLOBAL -p, and the launch line ---

// packsFixture loads the shipped packs named, in the order given — the selected set
// assembly and the launch line both read. Two packs, because the point of the global
// form is that MORE THAN ONE pack receives the name.
func packsFixture(t *testing.T, names ...string) []*packload.Pack {
	t.Helper()
	loaded, problems := packload.MaterializeEmbedded(officialpacks.FS, t.TempDir())
	if len(problems) != 0 {
		t.Fatalf("materializing official packs: %v", problems)
	}
	var out []*packload.Pack
	for _, name := range names {
		for _, p := range loaded {
			if p.Name == name {
				out = append(out, p)
				break
			}
		}
	}
	if len(out) != len(names) {
		t.Fatalf("official packs %v not all found (loaded %d)", names, len(loaded))
	}
	return out
}

// assembleWithProfilesAssembled is assembleWithConfig with an options hook and the
// channel file input beside the argv, so a test can drive a launch-time flag
// through the real env block and assert what the same launch delivers.
func assembleWithProfilesAssembled(t *testing.T, cfg *jsonx.OrderedMap, packs []*packload.Pack,
	set func(*Options)) assembled {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	emptyLoopholeDirs(t)
	o := goldenOptions("/ws", home)
	if set != nil {
		set(o)
	}
	in := &assembleInput{
		cfg:          cfg,
		rt:           "podman",
		cname:        "yolo-ws-abcd1234",
		imageRef:     goldenImageRef,
		jailPrefix:   goldenJailPrefix,
		packs:        packs,
		agentsPath:   "/agents/yolo-ws-abcd1234",
		wsState:      "/ws/.yolo/home",
		miseStore:    "/mise-store",
		yoloVersion:  "9.9.9-test",
		mountTargets: map[string]struct{}{},
	}
	return assembled{argv: o.assembleRunCmd(in), o: o, in: in}
}

// GLOBAL -p (providers.md#what-the-launch-checks-and-prints, #pv-oq-5): `-p bedrock` with NO command keys the name for every selected
// pack, by the CLI name each one installs. Before this the empty-argv case was a no-op
// — the target bin was "" and the assignment was silently skipped — so `yolo -p dev`
// looked accepted and selected nothing.
//
// The name is packs/bedrock's own `bedrock` because declaration is MANDATORY now
// (OQ-CS6): a name nothing declares refuses the launch instead of keying it, which is
// its own test below. `bedrock` is declared by neither agent pack, only by the provider
// pack both need, so this also pins that a profile name is global across the selection
// rather than per-pack — claude and pi each receive a name a pack installing no CLI ships.
//
// Asserted on the ASSEMBLED env, not the merge, because the table is the launch's
// contract with the jail: a merge that changed and an env block that did not follow
// would pass a test on the merge alone.
func TestAssembleGlobalProfileReachesEverySelectedPack(t *testing.T) {
	packs := packsFixture(t, "claude", "bedrock", "pi")
	la := assembleWithProfilesAssembled(t, newConfig(), packs, func(o *Options) { o.ProfileName = "bedrock" })
	got := la.channelEnv(t, "YOLO_USE_PROFILES")
	if len(got) != 1 {
		t.Fatalf("YOLO_USE_PROFILES emitted %q, want exactly one", got)
	}
	if got[0] != `YOLO_USE_PROFILES={"claude": "bedrock", "pi": "bedrock"}` {
		t.Errorf("global -p must key every selected pack by the CLI it installs, got %s", got[0])
	}
}

// -p WITH a command keeps the bin keying, and only the pack owning that bin gets the
// name — the other selected packs are untouched. Pinned beside the global form so the
// two branches cannot quietly converge.
func TestAssembleBareProfileKeysEveryBinEvenWithACommand(t *testing.T) {
	// 2026-09-03 ruling: a bare -p <name> is the selection for EVERY selected pack,
	// uniformly — it never keys on the command after `--`. A short option whose
	// meaning depends on a token further down the argv is the confusion this
	// deleted; the per-CLI spelling is -p <cli>=<name>.
	packs := packsFixture(t, "claude", "bedrock", "pi")
	la := assembleWithProfilesAssembled(t, newConfig(), packs, func(o *Options) {
		o.ProfileName = "bedrock"
		o.Args = []string{"claude"}
	})
	got := la.channelEnv(t, "YOLO_USE_PROFILES")
	if len(got) != 1 {
		t.Fatalf("YOLO_USE_PROFILES crossed %q, want exactly one line", got)
	}
	if got[0] != `YOLO_USE_PROFILES={"claude": "bedrock", "pi": "bedrock"}` {
		t.Errorf("bare -p must key every selected pack's bin, got %s", got[0])
	}
}

// THE THING THE DROPPED REFUSAL WAS STANDING IN FRONT OF: with a NON-AGENT command the
// profile still reaches every CLI the jail installs. The test above uses `claude`, which
// is itself an installed bin, so it cannot tell a table keyed off the command from one
// keyed off the pack set; `sleep` can, because nothing installs it. Asserted on the
// DELIVERED channel rather than on the merge — a launch that staged cleanly and then
// carried nothing would be the same silence in a later place.
func TestAssembleBareProfileReachesTheCLIsWithANonAgentCommand(t *testing.T) {
	packs := packsFixture(t, "claude", "bedrock", "pi")
	la := assembleWithProfilesAssembled(t, newConfig(), packs, func(o *Options) {
		o.ProfileName = "bedrock"
		o.Args = []string{"sleep", "60"}
	})
	got := la.channelEnv(t, "YOLO_USE_PROFILES")
	if len(got) != 1 {
		t.Fatalf("YOLO_USE_PROFILES crossed %q, want exactly one line", got)
	}
	if got[0] != `YOLO_USE_PROFILES={"claude": "bedrock", "pi": "bedrock"}` {
		t.Errorf("a non-agent command must not narrow the table — every selected pack's "+
			"bin gets the name, got %s", got[0])
	}
	if strings.Contains(got[0], "sleep") {
		t.Errorf("the `--` command is not a profile key, got %s", got[0])
	}
}

// profileLineChannel composes the real channel for packs with useProfiles as the launch's
// `-p cli=name` pairs and prints the launch's profile line over it, returning what it printed.
// region is set on the bedrock provider, as a Bedrock launch needs one.
func profileLineChannel(t *testing.T, packs []*packload.Pack, useProfiles map[string]string,
	userEnv *jsonx.OrderedMap) string {
	t.Helper()
	return profileLineChannelArgv(t, packs, useProfiles, userEnv, nil)
}

// profileLineChannelArgv is profileLineChannel with argvPairs as the container argv's `-e` pairs
// the line reads beside the channel.
func profileLineChannelArgv(t *testing.T, packs []*packload.Pack, useProfiles map[string]string,
	userEnv *jsonx.OrderedMap, argvPairs map[string]string) string {
	t.Helper()
	home := retireHome(t)
	writeUserPacks(t, home, `[]`)
	var out bytes.Buffer
	o := retireOptions(t, &out)
	o.UseProfiles = useProfiles
	cfg := newConfig()
	providers := jsonx.NewOrderedMap()
	bedrock := jsonx.NewOrderedMap()
	bedrock.Set("region", "eu-west-1")
	providers.Set("bedrock", bedrock)
	cfg.Set("providers", providers)
	channel := channelFor(t, o, cfg, packs, userEnv)
	out.Reset()
	o.noteUseProfiles(channel, packs, argvPairs)
	return out.String()
}

// THE LAUNCH LINE (providers.md#what-the-launch-checks-and-prints): one line per distinct name,
// naming the packs that DECLARE it and, for each agent the table keys to it, the provider its
// selection resolved to and how that agent reaches it in this jail. claude and pi on the
// shipped bedrock each reach it through their own Bedrock client, which their packs bind by
// needing packs/bedrock. The every-pack "received" list it replaced said nothing a user could act
// on. Driven through the channel the jail receives, so the line cannot claim a composition the
// jail did not get.
func TestNoteUseProfilesSaysWhatEachAgentsSelectionReaches(t *testing.T) {
	packs := []*packload.Pack{officialPack(t, "claude"), officialPack(t, "pi"),
		officialPack(t, "bedrock"), officialPack(t, "aws-auth")}
	key := jsonx.NewOrderedMap()
	key.Set("AWS_BEARER_TOKEN_BEDROCK", "bearer-for-bedrock-agents")
	got := profileLineChannel(t, packs, map[string]string{"claude": "bedrock", "pi": "bedrock"}, key)
	want := `Profile bedrock: declared by bedrock; claude → provider "bedrock", through claude's ` +
		`own "aws-bedrock" client; pi → provider "bedrock", through pi's own "aws-bedrock" client`
	if !strings.Contains(got, want) {
		t.Errorf("launch line\n%s\nwant it to contain\n%s", got, want)
	}
	if strings.Contains(got, "received:") || strings.Contains(got, "Warning:") {
		t.Errorf("a selection both agents reach, with a credential, warns or lists receivers:\n%s", got)
	}
	// providers.md#pv-oq-10: the line may not claim the name was honored. What a derive does
	// with the string is unobservable from here, and a transparency print that overclaims is
	// the silent-skip failure wearing a badge.
	if strings.Contains(got, "honored") {
		t.Errorf("the launch line must never claim a profile was honored:\n%s", got)
	}
}

// WHERE THE SELECTION REACHES NOTHING, THE LINE SAYS SO AND NAMES THE FIX: copilot has no Bedrock
// client of its own (its pack binds no aws-bedrock provider), so in a launch with no wire bridge to
// carry it (the pack set below is not closed over claude's needs; with the bridge, see
// TestPlainBedrockCarriesTheClientlessAgentsThroughTheBridge) a bare `-p bedrock` keyed to it
// configures nothing for it, which the line used to hide behind "received: …copilot…". And an
// agent none of whose provider's credential variables reaches it is told so, naming the withheld
// aws-auth pointer (its loophole is disabled here) as the channel that would carry one.
func TestNoteUseProfilesWarnsWhereTheSelectionReachesNothing(t *testing.T) {
	packs := []*packload.Pack{officialPack(t, "claude"), officialPack(t, "copilot"),
		officialPack(t, "bedrock"), officialPack(t, "aws-auth")}
	got := profileLineChannel(t, packs, map[string]string{"claude": "bedrock", "copilot": "bedrock"},
		emptyEnv())
	for _, want := range []string{
		`copilot → provider "bedrock", which it cannot use here (below)`,
		`Warning: profile "bedrock" reaches nothing for copilot: provider "bedrock" names no ` +
			`endpoint, only platform "aws-bedrock", and no selected pack gives copilot a client`,
		"(`-p copilot=<name>`)",
		`Warning: profile "bedrock" delivers claude no credential for provider "bedrock" at this notch`,
		`AWS_CONTAINER_CREDENTIALS_FULL_URI`, `The "aws-auth" jail daemon's pointer would carry`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the profile disclosure must say %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "reaches nothing for claude") {
		t.Errorf("claude, whose pack binds Bedrock, is warned as reaching nothing:\n%s", got)
	}
}

// A CREDENTIAL ON THE CONTAINER'S ARGV REACHES THE AGENT, so the line does not claim none does.
// A container jail's agent receives the `-e` pairs as well as its own delivery, and the fresh
// container launch hands the line those pairs (envPairs(runCmd)); a claimed variable that crosses
// only there, here AWS_PROFILE for claude on bedrock, silences the "delivers claude no
// credential" warning the same launch prints without it.
func TestNoteUseProfilesCountsTheContainerArgvAsReachingTheAgent(t *testing.T) {
	packs := []*packload.Pack{officialPack(t, "claude"), officialPack(t, "bedrock"),
		officialPack(t, "aws-auth")}
	sel := map[string]string{"claude": "bedrock"}
	const warned = `delivers claude no credential for provider "bedrock"`
	if got := profileLineChannelArgv(t, packs, sel, emptyEnv(), nil); !strings.Contains(got, warned) {
		t.Fatalf("with nothing on the argv the line must warn %q:\n%s", warned, got)
	}
	got := profileLineChannelArgv(t, packs, sel, emptyEnv(), map[string]string{"AWS_PROFILE": "on-the-argv"})
	if strings.Contains(got, warned) {
		t.Errorf("an AWS_PROFILE on the container argv reaches claude, and the line still warns:\n%s", got)
	}
}

// THE CALL SITE THE TEST ABOVE STANDS FOR: runContainer is the one arm with a container argv, and
// it hands the line that argv's pairs. The unit above cannot reach runContainer's call (it runs
// after the image and prefix builds), so its shape is pinned here: an argument of nil there
// passes every other test and warns every container launch whose credential crosses as `-e`.
func TestRunContainerHandsTheProfileLineItsArgvPairs(t *testing.T) {
	fn := methodDecl(t, "run.go", "runContainer")
	found := false
	ast.Inspect(fn, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "noteUseProfiles" {
			return true
		}
		found = true
		if len(call.Args) != 3 {
			t.Errorf("runContainer's noteUseProfiles call has %d arguments, want 3", len(call.Args))
			return true
		}
		if arg, ok := call.Args[2].(*ast.CallExpr); !ok || !isIdentNamed(arg.Fun, "envPairs") {
			t.Errorf("runContainer hands the profile line something other than envPairs(<argv>): %T",
				call.Args[2])
		}
		return true
	})
	if !found {
		t.Fatal("runContainer no longer prints the profile line")
	}
}

// No profile selected, no line: a plain launch is the common case, and restating the
// absence on every launch is noise rather than disclosure.
func TestNoteUseProfilesPrintsNothingWithoutAProfile(t *testing.T) {
	if got := profileLineChannel(t, packsFixture(t, "claude"), nil, emptyEnv()); got != "" {
		t.Errorf("an unprofiled launch must print no profile line, got:\n%s", got)
	}
}

// The launch line is called from the fresh-launch notice block, and AFTER the host-access
// disclosure — beside it, in the block that is the last host-side output before the
// container takes the terminal.
//
// The tests above pin what the line SAYS; nothing about them would notice if runContainer
// stopped calling it, and runContainer starts a real container, so a unit test has no other
// witness. Reading the source is the repo's existing answer to that shape
// (TestFreshLaunchCallsTheConfigArtifactWriter, which this mirrors, including the ordering
// assertion): a disclosure that exists and is never printed is the silent-skip failure with
// an extra step.
func TestFreshLaunchPrintsTheProfileLineBesideTheHostAccessLine(t *testing.T) {
	const (
		hostAccess = "notePackHostAccess"
		profiles   = "noteUseProfiles"
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

	if _, ok := pos[profiles]; !ok {
		t.Fatalf("runContainer no longer calls %s. The launch line still exists and its test "+
			"still passes, so a profile selection would be invisible at every launch — the "+
			"name the derives receive is then something the user infers rather than reads. "+
			"If the notice block moved, move this check with it rather than deleting it.", profiles)
	}
	if _, ok := pos[hostAccess]; !ok {
		t.Fatalf("runContainer no longer calls %s — a larger regression than the one this "+
			"test was written for", hostAccess)
	}
	if pos[profiles] < pos[hostAccess] {
		t.Errorf("runContainer calls %s BEFORE %s: the profile line belongs beside the other "+
			"pack disclosures, after the host-access half", profiles, hostAccess)
	}
}

// --- kind "profile": DECLARED, and the env the selected variant contributes ---

// profilePackFixture is a real staged-shape pack (LoadDir, not a hand-built struct) that
// installs `claude`, declares the `bedrock` selection, and gates one env entry on it —
// overriding the pack's own static baseline, the shape providers.md#pv-oq-8's later-wins rule exists to
// resolve.
func profilePackFixture(t *testing.T, name string) *packload.Pack {
	t.Helper()
	root := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name":"` + name + `","contributes":[` +
		`{"kind":"program","bin":"claude","via":"npm","package":"@acme/claude"},` +
		`{"kind":"provider","name":"bedrock"},` +
		`{"kind":"profile","name":"bedrock","provider":"bedrock"},` +
		`{"kind":"env","vars":{"SHARED":"static","BASE":"static"}},` +
		`{"kind":"env","profile":"bedrock","vars":{"PROFILE_ONLY":"from-profile"}}]}`
	if err := os.WriteFile(filepath.Join(root, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	p, problems := packload.LoadDir(root, name)
	if len(problems) != 0 {
		t.Fatalf("loading fixture pack: %v", problems)
	}
	return p
}

// A selected variant's env reaches the DELIVERED channel: the yolo-user-env.sh
// section and the YOLO_USE_PROFILES table must describe the same launch. This is the
// pin on the call site, not on the fold — packload.EnvVarsFor is covered in
// packload's own tests, and nothing there would notice if the channel went back to a
// static-only fold, which would ship every profile env silently missing from the jail.
func TestAssembleSelectedProfileEnvReachesTheJailArgv(t *testing.T) {
	packs := []*packload.Pack{profilePackFixture(t, "acme")}
	la := assembleWithProfilesAssembled(t, newConfig(), packs, func(o *Options) {
		o.UseProfiles = map[string]string{"claude": "bedrock"}
	})
	env := strings.Join(la.channelEnv(t, "PROFILE_ONLY", "BASE", "SHARED"), " ")
	if !strings.Contains(env, "PROFILE_ONLY=from-profile") {
		t.Errorf("the selected profile's gated env must reach the jail, got %s", env)
	}
	if !strings.Contains(env, "BASE=static") {
		t.Errorf("a key the gated entry does not name keeps the static value, got %s", env)
	}
	// The gated entry overrides by ASSIGNMENT: the static value of a key it names is
	// replaced, never removed, because the fold's removal spelling died with the profile
	// body (OQ-PT8).
	if !strings.Contains(env, "SHARED=static") {
		t.Errorf("the gated entry does not name SHARED, so the static value must stand, got %s", env)
	}

	// No profile selected: the static baseline, unchanged.
	la = assembleWithProfilesAssembled(t, newConfig(), packs, nil)
	env = strings.Join(la.channelEnv(t, "PROFILE_ONLY", "SHARED"), " ")
	if strings.Contains(env, "PROFILE_ONLY=") || !strings.Contains(env, "SHARED=static") {
		t.Errorf("without a selection the static env must stand, got %s", env)
	}
}

// DECLARED names the packs that actually declare the variant — the half of the line that tells a
// user whether the name they typed means anything — and a pack shipping no such variant is not
// named at all. A name only the user's own `profiles` declares says that instead.
func TestNoteUseProfilesNamesTheDeclaringPack(t *testing.T) {
	declares := profilePackFixture(t, "acme")
	silent := packsFixture(t, "pi")
	all := append([]*packload.Pack{declares}, silent...)
	got := profileLineChannel(t, all, map[string]string{"claude": "bedrock"}, emptyEnv())
	if !strings.Contains(got, "Profile bedrock: declared by acme; claude → ") || strings.Contains(got, "pi") {
		t.Errorf("the declaring pack, and no other, must be named:\n%s", got)
	}

	d := packload.ProfileDisclosures(packload.ProfileDisclosureInput{
		Table: map[string]string{"pi": "mine"}, Packs: silent,
		Resolved: map[string]packload.ResolvedProfile{"mine": {Provider: "nowhere"}},
	})
	if len(d) != 1 || !strings.Contains(d[0].Line(), "Profile mine: declared by your config's `profiles`") {
		t.Errorf("a name only the user declares must say so: %+v", d)
	}
}
