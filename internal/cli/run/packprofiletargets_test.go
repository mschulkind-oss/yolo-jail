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
// (profiles-as-pack-variants.md §2.5, §3.3): `--pack-profile <cli>=<name>` and
// `-p <name> -- <bin>` key a profile by CLI name, and a name no resolvable pack
// installs is refused at launch. The CONFIG half is validated by ValidateConfig;
// the flags never reach a config validator, so the launch pipeline owns this
// check — the same silent-typo hole §2.5 documents, arriving through argv.
//
// Driven through stageRunPacks (the launch path, above the backend dispatch and
// covering attach too), not the checker, so a test fails if the check is unwired
// from staging.

// A --pack-profile naming a CLI no pack installs is fatal, naming the CLI and the
// installed names.
func TestStageRunPacksRefusesAnUnknownPackProfileCLI(t *testing.T) {
	home := retireHome(t)
	writeUserPacks(t, home, `[]`)
	var out bytes.Buffer
	o := retireOptions(t, &out)
	o.PackProfiles = map[string]string{"cloude": "bedrock"}
	if _, ok := o.stageRunPacks("yolo-profile-target-cli"); ok {
		t.Fatalf("a --pack-profile naming a CLI no pack installs staged cleanly — " +
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

// -p with a command keys the profile by the command's binary name; an unknown one
// is the same refusal.
func TestStageRunPacksRefusesAProfileNameKeyedToAnUnknownCommand(t *testing.T) {
	home := retireHome(t)
	writeUserPacks(t, home, `[]`)
	var out bytes.Buffer
	o := retireOptions(t, &out)
	o.ProfileName = "dev"
	o.Args = []string{"cloude"}
	if _, ok := o.stageRunPacks("yolo-profile-target-bin"); ok {
		t.Fatalf("-p keying a profile to a command no pack installs staged cleanly")
	}
	if !strings.Contains(out.String(), `no pack installs a CLI named "cloude"`) {
		t.Errorf("the refusal must name the unknown command binary:\n%s", out.String())
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
	o.ProfileName = "dev"
	o.Args = []string{"claude"}
	o.PackProfiles = map[string]string{"pi": "glm"}
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

// assembleWithProfiles is assembleWithConfig with an options hook, so a test can drive
// a launch-time flag through the real env block rather than the merge in isolation.
func assembleWithProfiles(t *testing.T, cfg *jsonx.OrderedMap, packs []*packload.Pack,
	set func(*Options)) []string {
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
		packs:        packs,
		agentsPath:   "/agents/yolo-ws-abcd1234",
		wsState:      "/ws/.yolo/home",
		miseStore:    "/mise-store",
		yoloVersion:  "9.9.9-test",
		mountTargets: map[string]struct{}{},
	}
	return o.assembleRunCmd(in)
}

// GLOBAL -p (§3.3, OQ-5): `-p dev` with NO command keys the name for every selected
// pack, by the CLI name each one installs. Before this the empty-argv case was a no-op
// — the target bin was "" and the assignment was silently skipped — so `yolo -p dev`
// looked accepted and selected nothing.
//
// Asserted on the ASSEMBLED env, not the merge, because the table is the launch's
// contract with the jail: a merge that changed and an env block that did not follow
// would pass a test on the merge alone.
func TestAssembleGlobalProfileReachesEverySelectedPack(t *testing.T) {
	packs := packsFixture(t, "claude", "pi")
	argv := assembleWithProfiles(t, newConfig(), packs, func(o *Options) { o.ProfileName = "dev" })
	got := envArgValues(argv, "YOLO_PACK_PROFILES")
	if len(got) != 1 {
		t.Fatalf("YOLO_PACK_PROFILES emitted %q, want exactly one", got)
	}
	if got[0] != `YOLO_PACK_PROFILES={"claude": "dev", "pi": "dev"}` {
		t.Errorf("global -p must key every selected pack by the CLI it installs, got %s", got[0])
	}
}

// -p WITH a command keeps the bin keying, and only the pack owning that bin gets the
// name — the other selected packs are untouched. Pinned beside the global form so the
// two branches cannot quietly converge.
func TestAssembleProfileWithACommandKeysOnlyThatBin(t *testing.T) {
	packs := packsFixture(t, "claude", "pi")
	argv := assembleWithProfiles(t, newConfig(), packs, func(o *Options) {
		o.ProfileName = "dev"
		o.Args = []string{"claude"}
	})
	got := envArgValues(argv, "YOLO_PACK_PROFILES")
	if len(got) != 1 {
		t.Fatalf("YOLO_PACK_PROFILES emitted %q, want exactly one", got)
	}
	if got[0] != `YOLO_PACK_PROFILES={"claude": "dev"}` {
		t.Errorf("-p with a command must key only that bin, got %s", got[0])
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
func TestNotePackProfilesPrintsDeclaredAndReceived(t *testing.T) {
	packs := packsFixture(t, "claude", "pi")
	var out bytes.Buffer
	o := goldenOptions("/ws", t.TempDir())
	o.Stdout = discardBuf()
	o.Stderr = &out
	cfg := newConfig()
	profiles := jsonx.NewOrderedMap()
	profiles.Set("claude", "glm")
	profiles.Set("pi", "glm")
	cfg.Set("pack_profiles", profiles)
	effective := o.effectivePackProfiles(cfg, packs)
	o.notePackProfiles(effective, packs)

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
func TestNotePackProfilesPrintsOneLinePerName(t *testing.T) {
	packs := packsFixture(t, "claude", "pi")
	var out bytes.Buffer
	o := goldenOptions("/ws", t.TempDir())
	o.Stdout = discardBuf()
	o.Stderr = &out
	cfg := newConfig()
	profiles := jsonx.NewOrderedMap()
	profiles.Set("claude", "bedrock")
	profiles.Set("pi", "glm")
	cfg.Set("pack_profiles", profiles)
	o.notePackProfiles(o.effectivePackProfiles(cfg, packs), packs)

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
func TestNotePackProfilesPrintsNothingWithoutAProfile(t *testing.T) {
	packs := packsFixture(t, "claude")
	var out bytes.Buffer
	o := goldenOptions("/ws", t.TempDir())
	o.Stdout = discardBuf()
	o.Stderr = &out
	o.notePackProfiles(o.effectivePackProfiles(newConfig(), packs), packs)
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
		profiles   = "notePackProfiles"
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
// installs `claude` and declares one `bedrock` variant overriding its own static env
// baseline — the shape §3.4's later-wins rule exists to resolve.
func profilePackFixture(t *testing.T, name string) *packload.Pack {
	t.Helper()
	root := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name":"` + name + `","contributes":[` +
		`{"kind":"program","bin":"claude","via":"npm","package":"@acme/claude"},` +
		`{"kind":"env","vars":{"SHARED":"static","BASE":"static"}},` +
		`{"kind":"profile","name":"bedrock",` +
		`"env":{"PROFILE_ONLY":"from-profile","SHARED":null}}]}`
	if err := os.WriteFile(filepath.Join(root, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	p, problems := packload.LoadDir(root, name, false)
	if len(problems) != 0 {
		t.Fatalf("loading fixture pack: %v", problems)
	}
	return p
}

// A selected variant's env reaches the ASSEMBLED argv: the -e block and the
// YOLO_PACK_PROFILES table must describe the same launch. This is the pin on the call
// site, not on the fold — packload.EnvVarsFor is covered in packload's own tests, and
// nothing there would notice if assemble went back to a static-only fold, which
// would ship every profile env silently missing from the jail.
func TestAssembleSelectedProfileEnvReachesTheJailArgv(t *testing.T) {
	packs := []*packload.Pack{profilePackFixture(t, "acme")}
	argv := assembleWithProfiles(t, newConfig(), packs, func(o *Options) {
		o.PackProfiles = map[string]string{"claude": "bedrock"}
	})
	env := strings.Join(envArgValues(argv, "PROFILE_ONLY", "BASE", "SHARED"), " ")
	if !strings.Contains(env, "PROFILE_ONLY=from-profile") {
		t.Errorf("the selected variant's env must be in the jail argv, got %s", env)
	}
	if !strings.Contains(env, "BASE=static") {
		t.Errorf("a key the variant does not name keeps the static value, got %s", env)
	}
	// OQ-7: a null in the variant UNSETS the key, so the static value must not survive
	// as an -e at all — a jail starts from an empty env, so absent IS removed.
	if strings.Contains(env, "SHARED=") {
		t.Errorf("the variant's null must remove the key from the argv, got %s", env)
	}

	// No profile selected: the static baseline, unchanged.
	argv = assembleWithProfiles(t, newConfig(), packs, nil)
	env = strings.Join(envArgValues(argv, "PROFILE_ONLY", "SHARED"), " ")
	if strings.Contains(env, "PROFILE_ONLY=") || !strings.Contains(env, "SHARED=static") {
		t.Errorf("without a selection the static env must stand, got %s", env)
	}
}

// DECLARED now names the packs that actually declare the variant — the half of the line
// that tells a user whether the name they typed means anything. A pack shipping no such
// variant is RECEIVED only, and stays listed there.
func TestNotePackProfilesNamesTheDeclaringPack(t *testing.T) {
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
	cfg.Set("pack_profiles", profiles)
	o.notePackProfiles(o.effectivePackProfiles(cfg, all), all)
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
	o2.notePackProfiles(o2.effectivePackProfiles(cfg, all), all)
	if !strings.Contains(out2.String(), "Profile bedrok: declared: none;") {
		t.Errorf("an undeclared name must say so rather than vanish:\n%s", out2.String())
	}
}
