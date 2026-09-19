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
// (profiles-as-pack-variants.md §2.5, §3.3): `-p <cli>=<name>` keys a profile by CLI
// name, and a name no resolvable pack installs is refused at launch. The CONFIG half
// is validated by ValidateConfig; the flags never reach a config validator, so the
// launch pipeline owns this check — the same silent-typo hole §2.5 documents,
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

// GLOBAL -p (§3.3, OQ-5): `-p bedrock` with NO command keys the name for every selected
// pack, by the CLI name each one installs. Before this the empty-argv case was a no-op
// — the target bin was "" and the assignment was silently skipped — so `yolo -p dev`
// looked accepted and selected nothing.
//
// The name is packs/claude's own `bedrock` because declaration is MANDATORY now
// (OQ-CS6): a name nothing declares refuses the launch instead of keying it, which is
// its own test below. `bedrock` is declared by only ONE of the two selected packs, so
// this also pins that a profile name is global across the selection rather than
// per-pack — pi receives a name only claude ships.
//
// Asserted on the ASSEMBLED env, not the merge, because the table is the launch's
// contract with the jail: a merge that changed and an env block that did not follow
// would pass a test on the merge alone.
func TestAssembleGlobalProfileReachesEverySelectedPack(t *testing.T) {
	packs := packsFixture(t, "claude", "pi")
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
	packs := packsFixture(t, "claude", "pi")
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
	packs := packsFixture(t, "claude", "pi")
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

// THE LAUNCH LINE (§3.3): one line per distinct name, naming what DECLARED it and who
// RECEIVED it. RECEIVED is every selected pack — the table crosses to the jail whole
// and every pack's derive sees all of it — and DECLARED is the packs shipping a
// `profile` variant with that name. `glm` is a name no shipped pack declares, so this
// pins the undeclared half of the print (packs/claude's own `bedrock` is the declared
// one, which is why these tests cannot use it).
//
// Driven through the same merge the env block consumes, so the line cannot claim
// something the table does not carry.
func TestNoteUseProfilesPrintsDeclaredAndReceived(t *testing.T) {
	packs := packsFixture(t, "claude", "pi")
	var out bytes.Buffer
	o := goldenOptions("/ws", t.TempDir())
	o.Stdout = discardBuf()
	o.Stderr = &out
	cfg := newConfig()
	profiles := jsonx.NewOrderedMap()
	profiles.Set("claude", "glm")
	profiles.Set("pi", "glm")
	cfg.Set("use_profiles", profiles)
	effective := o.effectiveUseProfiles(cfg, packs)
	o.noteUseProfiles(effective, packs)

	want := "Profile glm: declared: none; received: claude, pi"
	if !strings.Contains(out.String(), want) {
		t.Errorf("launch line %q, want it to contain %q", out.String(), want)
	}
	// OQ-10: the line may not claim the name was honored. What a derive does with the
	// string is unobservable from here, and a transparency print that overclaims is
	// the silent-skip failure wearing a badge.
	if strings.Contains(out.String(), "honored") {
		t.Errorf("the launch line must never claim a profile was honored:\n%s", out.String())
	}
}

// Two names in play print two lines, so a launch that selected differently for
// different CLIs says both rather than the winner.
func TestNoteUseProfilesPrintsOneLinePerName(t *testing.T) {
	packs := packsFixture(t, "claude", "pi")
	var out bytes.Buffer
	o := goldenOptions("/ws", t.TempDir())
	o.Stdout = discardBuf()
	o.Stderr = &out
	cfg := newConfig()
	profiles := jsonx.NewOrderedMap()
	profiles.Set("claude", "bedrock")
	profiles.Set("pi", "glm")
	cfg.Set("use_profiles", profiles)
	o.noteUseProfiles(o.effectiveUseProfiles(cfg, packs), packs)

	for _, want := range []string{
		"Profile bedrock: declared: claude; received: claude, pi",
		"Profile glm: declared: none; received: claude, pi",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("launch line missing %q:\n%s", want, out.String())
		}
	}
}

// No profile selected, no line: a plain launch is the common case, and restating the
// absence on every launch is noise rather than disclosure.
func TestNoteUseProfilesPrintsNothingWithoutAProfile(t *testing.T) {
	packs := packsFixture(t, "claude")
	var out bytes.Buffer
	o := goldenOptions("/ws", t.TempDir())
	o.Stdout = discardBuf()
	o.Stderr = &out
	o.noteUseProfiles(o.effectiveUseProfiles(newConfig(), packs), packs)
	if out.String() != "" {
		t.Errorf("an unprofiled launch must print no profile line, got:\n%s", out.String())
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
// overriding the pack's own static baseline, the shape §3.4's later-wins rule exists to
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

// DECLARED now names the packs that actually declare the variant — the half of the line
// that tells a user whether the name they typed means anything. A pack shipping no such
// variant is RECEIVED only, and stays listed there.
func TestNoteUseProfilesNamesTheDeclaringPack(t *testing.T) {
	declares := profilePackFixture(t, "acme")
	silent := packsFixture(t, "pi")
	all := append([]*packload.Pack{declares}, silent...)
	var out bytes.Buffer
	o := goldenOptions("/ws", t.TempDir())
	o.Stdout = discardBuf()
	o.Stderr = &out
	cfg := newConfig()
	profiles := jsonx.NewOrderedMap()
	profiles.Set("claude", "bedrock")
	cfg.Set("use_profiles", profiles)
	o.noteUseProfiles(o.effectiveUseProfiles(cfg, all), all)
	if !strings.Contains(out.String(), "declared: acme; received: acme, pi") {
		t.Errorf("the declaring pack must be named:\n%s", out.String())
	}

	// A name nothing declares still prints — that is the silent-typo signal the line
	// exists for — and says plainly that nothing declared it.
	profiles.Set("claude", "bedrok")
	o2 := goldenOptions("/ws", t.TempDir())
	o2.Stdout = discardBuf()
	var out2 bytes.Buffer
	o2.Stderr = &out2
	o2.noteUseProfiles(o2.effectiveUseProfiles(cfg, all), all)
	if !strings.Contains(out2.String(), "Profile bedrok: declared: none;") {
		t.Errorf("an undeclared name must say so rather than vanish:\n%s", out2.String())
	}
}
