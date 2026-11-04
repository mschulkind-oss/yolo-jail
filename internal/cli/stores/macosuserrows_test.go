package stores

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/prune"
)

// macosuserrows_test.go pins `yolo stores`'s macos-user sections: the backend's host-owned staging
// root (/Users/Shared/yolo-staged) and the sandbox account's machine-tier stores, which the
// inventory used to omit entirely; the root-owned /var/yolo-jail an older yolo staged into, listed
// with the manual sudo commands only when it is present; and the not-applicable rows the
// container-only sections get on that runtime.

// productionMacosUserRoots is macosUserRoots as the binary has it, kept before TestMain points
// the variable at nothing.
var productionMacosUserRoots = macosUserRoots

// TestMain keeps every test in this package off the machine's real sandbox account: a test that
// leaves Options.MacosUser nil gets no macos-user section (macosUserRoots says why).
func TestMain(m *testing.M) {
	macosUserRoots = func() MacosUserRoots { return MacosUserRoots{} }
	os.Exit(m.Run())
}

// TestTheMacosUserSeamDefaultsToTheBackendsOwnRoots: an unset seam is the production answer, the
// staging root, the legacy state dir and the home internal/macosuser names, on macOS alone.
// Deleting fillDefaults' line, or pointing the variable anywhere else, fails here.
func TestTheMacosUserSeamDefaultsToTheBackendsOwnRoots(t *testing.T) {
	got := productionMacosUserRoots()
	want := MacosUserRoots{StateDir: macosuser.StateDir(), LegacyDir: macosuser.LegacyStateDir(),
		Home: macosuser.SandboxHome(), OK: paths.IsMacOS}
	if got != want {
		t.Errorf("production roots = %+v, want %+v", got, want)
	}
	if got.StateDir != "/Users/Shared/yolo-staged" || got.LegacyDir != "/var/yolo-jail" || got.Home != "/Users/_yolojail" {
		t.Errorf("the roots moved to %+v; the sections' notes and the help text name them", got)
	}
	called := false
	prev := macosUserRoots
	macosUserRoots = func() MacosUserRoots { called = true; return MacosUserRoots{} }
	t.Cleanup(func() { macosUserRoots = prev })
	var o Options
	fillDefaults(&o)
	o.MacosUser()
	if !called {
		t.Error("fillDefaults does not hand an unset MacosUser seam the production roots")
	}
}

// macosUserFixture lays out a staging root and a sandbox home the way a Mac that has run macos-user
// launches has them, under temp roots, and returns options pointed at both. Its legacy dir does
// not exist: a Mac set up after the one-rule contract has none.
func macosUserFixture(t *testing.T) (Options, string, string) {
	t.Helper()
	o, _ := testOptions(t)
	root := t.TempDir()
	stateDir, home := filepath.Join(root, "Users-Shared-yolo-staged"), filepath.Join(root, "Users-_yolojail")
	writeFile(t, filepath.Join(stateDir, "bin", "yolo"), 1000)
	writeFile(t, filepath.Join(stateDir, "bin", "yolo-jaild"), 500)
	writeFile(t, filepath.Join(stateDir, "packs", "yolo-a-1", "claude", "pack.json"), 30)
	writeFile(t, filepath.Join(stateDir, "packs", "yolo-b-2", "claude", "pack.json"), 30)
	writeFile(t, filepath.Join(stateDir, "home-overlay", "yolo-a-1", ".claude", "CLAUDE.md"), 70)
	writeFile(t, filepath.Join(stateDir, "ctx", "yolo-a-1", "x"), 5)
	writeFile(t, filepath.Join(stateDir, "env", "yolo-a-1.env"), 9)
	writeFile(t, filepath.Join(stateDir, "profile-yolo-a-1.sb"), 200)
	writeFile(t, filepath.Join(stateDir, "profile-yolo-b-2.sb"), 200)
	writeFile(t, filepath.Join(macosuser.SandboxMiseData(home), "installs", "node", "bin", "node"), 4000)
	writeFile(t, filepath.Join(home, ".cache", "pip", "wheel"), 300)
	// Everything else in the home is not this command's to walk.
	writeFile(t, filepath.Join(home, "Documents", "private"), 99999)
	writeFile(t, filepath.Join(home, ".claude", "settings.json"), 77)
	legacy := filepath.Join(root, "var-yolo-jail")
	o.MacosUser = func() MacosUserRoots {
		return MacosUserRoots{StateDir: stateDir, LegacyDir: legacy, Home: home, OK: true}
	}
	return o, stateDir, home
}

// legacyFixture adds to macosUserFixture's Mac the root-owned /var/yolo-jail an older yolo staged
// into, and returns its path.
func legacyFixture(t *testing.T, o *Options) string {
	t.Helper()
	legacy := o.MacosUser().LegacyDir
	writeFile(t, filepath.Join(legacy, "bin", "yolo"), 800)
	writeFile(t, filepath.Join(legacy, "packs", "yolo-old-1", "claude", "pack.json"), 40)
	writeFile(t, filepath.Join(legacy, "home-overlay", "yolo-old", ".claude", "CLAUDE.md"), 60)
	writeFile(t, filepath.Join(legacy, "env", "yolo-old.env"), 9)
	writeFile(t, filepath.Join(legacy, "profile-yolo-old.sb"), 150)
	return legacy
}

func sectionRows(rep Report, section string) map[string]Store {
	out := map[string]Store{}
	for _, s := range rep.Stores {
		if s.Section == section {
			out[s.Key] = s
		}
	}
	return out
}

// TestTheMacosUserSectionListsTheStateDirAndTheHomesStores: one row per child of the staging
// root, its loose profiles in one row, and the home's mise store and cache — each measured, none
// with a reclaimer, each the user's to decide about with a note saying how. The staging root is
// the host user's, so every command a row names runs without sudo, and a sealed tree is unsealed
// before it is removed. Only the named home stores are walked, and with no legacy dir there is no
// legacy section.
func TestTheMacosUserSectionListsTheStateDirAndTheHomesStores(t *testing.T) {
	o, stateDir, home := macosUserFixture(t)
	var walked []string
	walk := o.Walk
	o.Walk = func(root string, cutoff time.Time, budget time.Duration, now func() time.Time) (WalkResult, error) {
		walked = append(walked, root)
		return walk(root, cutoff, budget, now)
	}
	rows := sectionRows(Inventory(o), SectionMacosUser)

	want := map[string]int64{
		"macos.state.bin": 1500, "macos.state.packs": 60, "macos.state.home-overlay": 70,
		"macos.state.ctx": 5, "macos.state.env": 9, "macos.state._files": 400,
		"macos.home.mise": 4000, "macos.home.cache": 300,
	}
	for key, bytes := range want {
		s, ok := rows[key]
		if !ok {
			t.Errorf("no %s row (have %v)", key, keysIn(rows))
			continue
		}
		if s.Sizing != SizingMeasured || s.Bytes != bytes {
			t.Errorf("%s = %s %d B, want measured %d B", key, s.Sizing, s.Bytes, bytes)
		}
		if key == "macos.state.packs" {
			continue // `yolo prune --apply` reclaims it, asserted below
		}
		if s.Reclaimer.Func != "" || s.Verdict != VerdictHuman || s.Note == "" {
			t.Errorf("%s: reclaimer %+v verdict %q note %q; want no reclaimer, the user's verdict, and a note",
				key, s.Reclaimer, s.Verdict, s.Note)
		}
	}
	if len(rows) != len(want) {
		t.Errorf("the section has %d rows, want %d: %v", len(rows), len(want), keysIn(rows))
	}
	if s := rows["macos.state.packs"]; s.Count != 2 || s.CountLabel != "entries" {
		t.Errorf("packs counts %d %s, want 2 entries including retained launch trees", s.Count, s.CountLabel)
	}
	if s := rows["macos.state._files"]; s.Count != 2 || !strings.Contains(s.Note, "profile-<cname>.sb") {
		t.Errorf("the loose-files row = %+v, want the two Seatbelt profiles named", s)
	}
	for key, s := range rows {
		if strings.Contains(s.Note, "sudo") || strings.Contains(s.Note, "root-owned") {
			t.Errorf("%s names sudo or root ownership, but the staging root is the host user's: %s", key, s.Note)
		}
		if strings.HasPrefix(key, "macos.state.") && !strings.Contains(s.Note, "host-owned") {
			t.Errorf("%s does not say the staging root is host-owned: %s", key, s.Note)
		}
	}
	if legacy := sectionRows(Inventory(o), SectionMacosUserLegacy); len(legacy) != 0 {
		t.Errorf("no legacy dir exists, yet the legacy section has rows %v", keysIn(legacy))
	}
	packs := rows["macos.state.packs"]
	if packs.Reclaimer.Trigger != "yolo prune --apply" || packs.Verdict != VerdictYolo {
		t.Errorf("the packs row names no reclaimer: %+v, verdict %s", packs.Reclaimer, packs.Verdict)
	}
	for _, want := range []string{"per-launch immutable guest trees", "A launch removes its own tree once its guest has exited",
		"`yolo prune --apply`", "`ls -la -- <exact-path>`", "`chmod -R -- u+w <exact-path> && rm -rf -- <exact-path>`", "yolo yolo-account remove"} {
		if !strings.Contains(packs.Note, want) {
			t.Errorf("the packs row does not name %q: %s", want, packs.Note)
		}
	}
	if strings.Contains(packs.Note, "rm -rf -- "+filepath.Join(stateDir, "packs", "<cname>")) {
		t.Errorf("the packs row recommends workspace-wide removal of possibly held trees: %s", packs.Note)
	}
	// A per-launch tree is sealed 0555, which blocks its owner's rm too: the removal unseals
	// first, by the tree's own spelling in internal/macosuser.
	for key, tree := range map[string]string{
		"macos.state.home-overlay": macosuser.StagedHomeOverlay("<cname>", stateDir),
		"macos.state.ctx":          macosuser.StagedCtxRoot("<cname>", stateDir),
	} {
		want := "`chmod -R -- u+w " + tree + " && rm -rf -- " + tree + "`"
		if !strings.Contains(rows[key].Note, want) {
			t.Errorf("%s does not name the unseal-then-remove %s: %s", key, want, rows[key].Note)
		}
	}
	// env/ names each file literally, never a glob: the same command reads the same in either
	// root, and in the legacy one only root can list the dir, where a glob expands to nothing.
	env := filepath.Join(stateDir, "env")
	envNote := rows["macos.state.env"].Note
	wantRm := "`rm -f -- " + filepath.Join(env, "<cname>.env") + " " + filepath.Join(env, "<cname>.daemons.env") + "`"
	if !strings.Contains(envNote, wantRm) {
		t.Errorf("the env row's note does not name its per-workspace files as %s: %s", wantRm, envNote)
	}
	if strings.Contains(envNote, "*") {
		t.Errorf("the env row's note names a glob: %s", envNote)
	}
	if want := "`rm -f -- " + filepath.Join(stateDir, "profile-<cname>.sb") + "`"; !strings.Contains(rows["macos.state._files"].Note, want) {
		t.Errorf("the profiles row does not name %s: %s", want, rows["macos.state._files"].Note)
	}
	// The spellings are internal/macosuser's own, so the note cannot drift from the files.
	if got := macosuser.SandboxEnvFile("<cname>", stateDir); got != filepath.Join(env, "<cname>.env") {
		t.Errorf("SandboxEnvFile = %s; the env row's note names %s", got, filepath.Join(env, "<cname>.env"))
	}
	if got := macosuser.SandboxDaemonEnvFile("<cname>", stateDir); got != filepath.Join(env, "<cname>.daemons.env") {
		t.Errorf("SandboxDaemonEnvFile = %s; the env row's note names %s", got, filepath.Join(env, "<cname>.daemons.env"))
	}
	for _, root := range walked {
		if root == home || strings.HasPrefix(root, filepath.Join(home, "Documents")) ||
			strings.HasPrefix(root, filepath.Join(home, ".claude")) {
			t.Errorf("the inventory walked %s; only the home's named stores are its to walk", root)
		}
	}
}

// TestTheLegacyStateDirIsItsOwnSectionWithTheManualSudoCommands: a Mac that ran an older yolo
// keeps the root-owned /var/yolo-jail, which nothing in yolo removes (PL-8). `yolo stores` lists
// it in a section of its own, one row per child plus its loose profiles, each root-owned, each
// naming the sudo command the user runs by hand (this command runs none), and each saying a launch
// no longer writes there while an older yolo's running sessions may. env/ is 0700 root, so it is
// never walked: its size is unknown and the row names `sudo du -sh`.
func TestTheLegacyStateDirIsItsOwnSectionWithTheManualSudoCommands(t *testing.T) {
	o, stateDir, _ := macosUserFixture(t)
	legacy := legacyFixture(t, &o)
	legacyEnv := filepath.Join(legacy, "env")
	walk := o.Walk
	o.Walk = func(root string, cutoff time.Time, budget time.Duration, now func() time.Time) (WalkResult, error) {
		if root == legacyEnv || strings.HasPrefix(root, legacyEnv+"/") {
			t.Errorf("the inventory walked %s, a dir only root can read", root)
		}
		return walk(root, cutoff, budget, now)
	}
	rep := Inventory(o)
	rows := sectionRows(rep, SectionMacosUserLegacy)

	want := map[string]int64{
		"macos.legacy.bin": 800, "macos.legacy.packs": 40, "macos.legacy.home-overlay": 60,
		"macos.legacy._files": 150,
	}
	for key, bytes := range want {
		s, ok := rows[key]
		if !ok {
			t.Errorf("no %s row (have %v)", key, keysIn(rows))
			continue
		}
		if s.Sizing != SizingMeasured || s.Bytes != bytes {
			t.Errorf("%s = %s %d B, want measured %d B", key, s.Sizing, s.Bytes, bytes)
		}
	}
	if len(rows) != len(want)+1 {
		t.Errorf("the legacy section has %d rows, want %d: %v", len(rows), len(want)+1, keysIn(rows))
	}
	for key, s := range rows {
		if s.Reclaimer.Func != "" || s.Verdict != VerdictHuman {
			t.Errorf("%s: reclaimer %+v verdict %q; nothing in yolo removes the legacy dir", key, s.Reclaimer, s.Verdict)
		}
		for _, want := range []string{"root-owned", "launch no longer writes " + legacy,
			"an older yolo's still-running sessions may", "end them first"} {
			if !strings.Contains(s.Note, want) {
				t.Errorf("%s's note does not say %q: %s", key, want, s.Note)
			}
		}
		if !strings.Contains(s.Note, "`sudo ") {
			t.Errorf("%s's note names no sudo command, but the dir is root's: %s", key, s.Note)
		}
		if strings.Contains(s.Note, "replaced in place") || strings.Contains(s.Reclaimer.Detail, "replaces") ||
			strings.Contains(s.Reclaimer.Detail, "rewrites") {
			t.Errorf("%s says a launch replaces it, but no launch writes the legacy dir: %s / %s", key, s.Reclaimer.Detail, s.Note)
		}
	}
	env := rows["macos.legacy.env"]
	if env.Sizing != SizingUnknown || !strings.Contains(env.Note, "sudo du -sh "+legacyEnv) {
		t.Errorf("the legacy env row = %s, note %s; want unknown, naming `sudo du -sh %s`", env.Sizing, env.Note, legacyEnv)
	}
	wantRm := "`sudo rm -f " + macosuser.SandboxEnvFile("<cname>", legacy) + " " + macosuser.SandboxDaemonEnvFile("<cname>", legacy) + "`"
	if !strings.Contains(env.Note, wantRm) || strings.Contains(env.Note, "*") {
		t.Errorf("the legacy env row does not name its files literally as %s: %s", wantRm, env.Note)
	}
	if want := "`sudo rm -rf " + macosuser.StagedHomeOverlay("<cname>", legacy) + "`"; !strings.Contains(rows["macos.legacy.home-overlay"].Note, want) {
		t.Errorf("the legacy home-overlay row does not name %s: %s", want, rows["macos.legacy.home-overlay"].Note)
	}
	if want := "`sudo ls -la -- <exact-path>`"; !strings.Contains(rows["macos.legacy.packs"].Note, want) {
		t.Errorf("the legacy packs row does not name %s: %s", want, rows["macos.legacy.packs"].Note)
	}
	if want := "`sudo rm -f " + filepath.Join(legacy, "profile-<cname>.sb") + "`"; !strings.Contains(rows["macos.legacy._files"].Note, want) {
		t.Errorf("the legacy profiles row does not name %s: %s", want, rows["macos.legacy._files"].Note)
	}
	// The staging root's own rows are untouched by the legacy dir's presence.
	if s := sectionRows(rep, SectionMacosUser)["macos.state.bin"]; s.Bytes != 1500 || s.Path != filepath.Join(stateDir, "bin") {
		t.Errorf("the staging root's bin row = %+v", s)
	}

	// Rendered, in its own section after the staging root's and before the summary.
	out := new(strings.Builder)
	o.Out, o.NoRecord = out, true
	if rc := Run(o); rc != 0 {
		t.Fatalf("Run = %d", rc)
	}
	text := out.String()
	i, j := strings.Index(text, SectionMacosUser+"  "), strings.Index(text, SectionMacosUserLegacy)
	if i < 0 || j < 0 || j < i || j > strings.Index(text, "What nothing reclaims") {
		t.Errorf("the legacy section is not rendered after the staging root's and before the summary:\n%s", text)
	}
}

// TestAnUnreadableStagingRootChildIsUnknownWithoutSudo: the staging root is the host user's, so an
// unreadable child is a fault, not root's privacy. It is unknown, never "≥ 0 B", and the row
// names `ls -ld` to see who owns it, never a sudo command.
func TestAnUnreadableStagingRootChildIsUnknownWithoutSudo(t *testing.T) {
	for name, result := range map[string]func() (WalkResult, error){
		"refused": func() (WalkResult, error) {
			return WalkResult{}, &fs.PathError{Op: "open", Err: fs.ErrPermission}
		},
		"nothing read": func() (WalkResult, error) { return WalkResult{Unreadable: 1}, nil },
	} {
		t.Run(name, func(t *testing.T) {
			o, stateDir, _ := macosUserFixture(t)
			env := filepath.Join(stateDir, "env")
			walk := o.Walk
			o.Walk = func(root string, cutoff time.Time, budget time.Duration, now func() time.Time) (WalkResult, error) {
				if root == env {
					return result()
				}
				return walk(root, cutoff, budget, now)
			}
			s := sectionRows(Inventory(o), SectionMacosUser)["macos.state.env"]
			if s.Sizing != SizingUnknown {
				t.Errorf("env/ = %s %d B, want unknown", s.Sizing, s.Bytes)
			}
			if !strings.Contains(s.Note, "`ls -ld "+env+"`") || strings.Contains(s.Note, "sudo") {
				t.Errorf("env/'s note does not name `ls -ld %s` without sudo: %s", env, s.Note)
			}
		})
	}
}

// TestWithNeitherRootTheSectionIsOneAbsentRowNamingSetup, and with no macos-user backend at all
// (not a Mac) there is no section.
func TestWithNeitherRootTheSectionIsOneAbsentRowNamingSetup(t *testing.T) {
	o, _ := testOptions(t)
	gone := t.TempDir()
	o.MacosUser = func() MacosUserRoots {
		return MacosUserRoots{StateDir: filepath.Join(gone, "Users-Shared-yolo-staged"),
			LegacyDir: filepath.Join(gone, "var-yolo-jail"), Home: filepath.Join(gone, "Users-_yolojail"), OK: true}
	}
	rows := sectionRows(Inventory(o), SectionMacosUser)
	s, ok := rows["macos.absent"]
	if len(rows) != 1 || !ok || s.Sizing != SizingAbsent || !strings.Contains(s.Note, "yolo yolo-account create") {
		t.Errorf("rows = %+v, want one absent row naming `yolo yolo-account create`", rows)
	}

	o.MacosUser = func() MacosUserRoots {
		return MacosUserRoots{StateDir: "/Users/Shared/yolo-staged", LegacyDir: "/var/yolo-jail", Home: "/Users/_yolojail"}
	}
	if rows := sectionRows(Inventory(o), SectionMacosUser); len(rows) != 0 {
		t.Errorf("not a Mac, yet the section has rows %v", keysIn(rows))
	}
}

// TestTheMacosUserSectionIsRendered: the section heading and its rows reach the human report,
// placed with the other sections (renderText walks a fixed list of them).
func TestTheMacosUserSectionIsRendered(t *testing.T) {
	o, stateDir, _ := macosUserFixture(t)
	out := new(strings.Builder)
	o.Out = out
	o.NoRecord = true
	if rc := Run(o); rc != 0 {
		t.Fatalf("Run = %d", rc)
	}
	text := out.String()
	for _, want := range []string{SectionMacosUser, filepath.Join(stateDir, "packs") + "/", "yolo yolo-account remove"} {
		if !strings.Contains(text, want) {
			t.Errorf("the report lacks %q:\n%s", want, text)
		}
	}
	if strings.Index(text, SectionMacosUser) > strings.Index(text, "What nothing reclaims") {
		t.Errorf("the section is printed after the summary:\n%s", text)
	}
}

// TestContainerOnlySectionsSayTheyDoNotApplyOnMacosUser: the image store and the scratch volumes
// are one not-applicable row each on macos-user, naming how a container runtime's are listed,
// rather than three "no container runtime" rows that read as a fault.
func TestContainerOnlySectionsSayTheyDoNotApplyOnMacosUser(t *testing.T) {
	o, _ := testOptions(t)
	o.DetectRuntime = func() string { return "macos-user" }
	writeFile(t, filepath.Join(o.NixStore, "aaa-yolo-jail-install-prefix", "bin", "yolo"), 10)
	writeFile(t, filepath.Join(o.NixStore, "bbb-yolo-jail-go-0-dev", "bin", "yolo"), 10)
	rep := Inventory(o)
	for _, section := range []string{SectionImages, SectionVolumes} {
		rows := sectionRows(rep, section)
		if len(rows) != 1 {
			t.Errorf("%s has %d rows on macos-user, want 1: %v", section, len(rows), keysIn(rows))
			continue
		}
		for _, s := range rows {
			if s.Sizing != SizingAbsent || !strings.Contains(s.Reason, "not applicable on macos-user") ||
				!strings.Contains(s.Note, "YOLO_RUNTIME=container yolo stores") {
				t.Errorf("%s row = %+v", section, s)
			}
		}
	}

	// The store-output pass is a container runtime's: macos-user's own prune prints it as not
	// applicable and its launches run no post-launch slot. So the rows it covers name the
	// runtime that runs it rather than the bare `yolo prune --apply` the same Mac's macos-user
	// prune declines to run.
	for _, key := range []string{"nix.install-prefix", "nix.go-build"} {
		s := storeByKey(t, rep, key)
		if s.Reclaimer.Func != "SupersededStoreOutputs" || s.Verdict != VerdictYolo {
			t.Errorf("%s: reclaimer %+v verdict %q; a container runtime on this Mac still runs the pass", key, s.Reclaimer, s.Verdict)
		}
		if !strings.Contains(s.Reclaimer.Trigger, "YOLO_RUNTIME=container yolo prune --apply") ||
			!strings.Contains(s.Reclaimer.Trigger, "YOLO_RUNTIME=podman") ||
			!strings.Contains(s.Reclaimer.Detail, "container runtime") {
			t.Errorf("%s on macos-user names reclaimer %+v; want the container runtime that runs it", key, s.Reclaimer)
		}
		if strings.HasPrefix(s.Reclaimer.Trigger, "post-launch slot") {
			t.Errorf("%s on macos-user claims the post-launch slot, which no macos-user launch runs: %q", key, s.Reclaimer.Trigger)
		}
		if !strings.Contains(s.Note, "macos-user") {
			t.Errorf("%s's note does not say macos-user's prune and launches do not run the pass: %s", key, s.Note)
		}
	}
	// A container runtime's rows keep the reclaimer as it was.
	o.DetectRuntime = func() string { return "podman" }
	if s := storeByKey(t, Inventory(o), "nix.install-prefix"); s.Reclaimer.Trigger != "post-launch slot (24h) + yolo prune --apply" {
		t.Errorf("under podman the install-prefix trigger is %q", s.Reclaimer.Trigger)
	}
}

// TestMacosUserRowsNameTheRuntimeThatRunsTheirPass: the stopped-container, image-tarball,
// interrupted-delivery and image-root passes are a container runtime's, and macos-user's own
// `yolo prune` prints each as not applicable (prune's TestPruneOnMacosUserAsksNoRuntimeAndExitsZero
// pins that half). So on macos-user no row those passes reclaim may name the bare
// `yolo prune --apply` the same Mac's macos-user prune declines to run: each keeps its reclaimer
// and its yolo verdict, since a container runtime on this Mac runs the pass, and names that
// runtime's prune as the trigger. Under a container runtime the rows are as they were.
func TestMacosUserRowsNameTheRuntimeThatRunsTheirPass(t *testing.T) {
	o, state := testOptions(t)
	writeFile(t, filepath.Join(state, "cache", "images", "yolo-jail-abc.tar"), 5000)
	writeFile(t, filepath.Join(state, "containers", "yolo-x-1"), 10)
	writeFile(t, filepath.Join(state, "image-delivery", "d1", "x"), 10)
	writeFile(t, filepath.Join(state, "build", "roots", "0123456789abcdef"), 10)
	containerPasses := map[string]string{
		"state.containers":     "PruneStoppedContainers",
		"state.image-delivery": "PruneImageDelivery",
		"cache.images":         "PruneImageCache",
		"state.build":          "PruneOrphanImageRoots",
	}

	o.DetectRuntime = func() string { return "macos-user" }
	rep := Inventory(o)
	for key, fn := range containerPasses {
		s := storeByKey(t, rep, key)
		if s.Reclaimer.Func != fn || s.Verdict != VerdictYolo {
			t.Errorf("%s: reclaimer %+v verdict %q; want %s, yolo's, since a container runtime on this Mac runs it",
				key, s.Reclaimer, s.Verdict, fn)
		}
		if strings.HasPrefix(s.Reclaimer.Trigger, "yolo prune --apply") ||
			!strings.Contains(s.Reclaimer.Trigger, "`YOLO_RUNTIME=container yolo prune --apply`") ||
			!strings.Contains(s.Reclaimer.Trigger, "YOLO_RUNTIME=podman") {
			t.Errorf("%s on macos-user names trigger %q; macos-user's `yolo prune` does not run %s, a "+
				"container runtime's does", key, s.Reclaimer.Trigger, fn)
		}
		if !strings.Contains(s.Note, "macos-user's `yolo prune` does not run ") {
			t.Errorf("%s's note does not say macos-user's prune does not run the pass: %s", key, s.Note)
		}
	}
	// build/ is only partly a container runtime's: its dangling out-links are swept by every
	// runtime's prune, and the note says so rather than calling the whole dir unreclaimed here.
	if note := storeByKey(t, rep, "state.build").Note; !strings.Contains(note, "dangling out-links") {
		t.Errorf("state.build's note does not say macos-user's prune still sweeps its dangling out-links: %s", note)
	}
	// The tar row's keep is the container runtimes' own, each named, rather than the keep a
	// macos-user prune that runs no tar sweep would resolve.
	if d := storeByKey(t, rep, "cache.images").Reclaimer.Detail; !strings.Contains(d, fmt.Sprintf("keep %d under the container runtime",
		prune.ResolveImageCacheKeep(prune.ImageCacheKeepUnset, "container"))) ||
		!strings.Contains(d, fmt.Sprintf("%d under podman", prune.ResolveImageCacheKeep(prune.ImageCacheKeepUnset, "podman"))) {
		t.Errorf("cache.images on macos-user names detail %q; want each container runtime's keep", d)
	}

	for _, rt := range []string{"podman", "container"} {
		o.DetectRuntime = func() string { return rt }
		rep := Inventory(o)
		for key := range containerPasses {
			s := storeByKey(t, rep, key)
			if s.Reclaimer.Trigger != "yolo prune --apply" || strings.Contains(s.Note, "macos-user") {
				t.Errorf("under %s, %s names trigger %q and note %q; want the plain `yolo prune --apply`",
					rt, key, s.Reclaimer.Trigger, s.Note)
			}
		}
		want := fmt.Sprintf("keep %d", prune.ResolveImageCacheKeep(prune.ImageCacheKeepUnset, rt))
		if d := storeByKey(t, rep, "cache.images").Reclaimer.Detail; d != want {
			t.Errorf("under %s the tar row's detail is %q, want %q", rt, d, want)
		}
	}
}

func keysIn(rows map[string]Store) []string {
	var out []string
	for k := range rows {
		out = append(out, k)
	}
	return out
}
