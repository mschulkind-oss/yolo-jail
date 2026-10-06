package cli

// hostmodelmenu_test.go pins codex's model menu at `yolo host --` (docs/design/model-lists-and-
// pickers.md §14.7, MM-D24 to MM-D28) at its call site, hostExec, through hostMain with the exec
// replaced: the shipped codex pack, a stub codex on PATH that prints a catalog for `debug models
// --bundled` and counts each read, and the argv the exec was handed. With them, codex's LAUNCH
// SELECTION (MM-D30): what a host -p hands codex in place of the file it cannot write. Deleting
// either step's call in hostExec, the list derive, the selection's comparison or the lock
// hand-over fails one of these. No codex runs and nothing is sent anywhere.

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/modelmenu"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"golang.org/x/sys/unix"
)

// hostMenuCatalog is a catalog of the shape codex 0.159.2's `debug models --bundled` prints: two
// of the subscription's three ids, each with prompt text, one with an upgrade the menu clears.
const hostMenuCatalog = `{"models":[
 {"slug":"gpt-6-astra","priority":2,"display_name":"gpt-6-astra","visibility":"list",
  "model_messages":{"instructions_template":"astra"},"upgrade":{"model":"x"}},
 {"slug":"gpt-6.1-sol","priority":1,"display_name":"gpt-6.1-sol","visibility":"list",
  "model_messages":{"instructions_template":"sol"}},
 {"slug":"gpt-5.5","priority":13,"display_name":"GPT-5.5","visibility":"list",
  "model_messages":{"instructions_template":"old"}}]}`

// hostMenuLaunch is one `yolo host [flags] -- codex <args>`: the exit code, the argv the exec was
// handed, stderr, how often the stub's catalog was read, and what the exec saw of the menu's lock.
type hostMenuLaunch struct {
	rc           int
	argv         []string
	errs         string
	catalogReads int
	// lockHeld is whether, at the exec, a second descriptor could not take the menu directory's
	// live lock exclusively; lockInherited whether a descriptor holding it was not close-on-exec
	// (Linux only, read from /proc/self/fd; true elsewhere).
	lockHeld, lockInherited bool
	home                    string
}

// runHostMenu runs one launch in a fresh temp HOME with cfg as its user config, or, when home is
// given, in that one (its config already written).
func runHostMenu(t *testing.T, home, cfg string, env map[string]string, flags []string, args ...string) hostMenuLaunch {
	t.Helper()
	if home == "" {
		home = hostGateHome(t, cfg, nil)
	}
	t.Setenv(entrypoint.NoLaunchFlagsEnv, "")
	for k, v := range env {
		t.Setenv(k, v)
	}
	bin := filepath.Join(home, "stub-bin")
	reads := filepath.Join(home, "catalog.reads")
	catalog := filepath.Join(home, "catalog.json")
	writeFile(t, catalog, hostMenuCatalog)
	stub := "#!/bin/sh\nif [ \"$*\" = 'debug models --bundled' ]; then echo read >> '" + reads +
		"'; cat '" + catalog + "'; exit 0; fi\nexit 0\n"
	if _, err := os.Stat(filepath.Join(bin, "codex")); os.IsNotExist(err) {
		writeFile(t, filepath.Join(bin, "codex"), stub)
		if err := os.Chmod(filepath.Join(bin, "codex"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	l := hostMenuLaunch{home: home}
	origExec := hostSyscallExec
	hostSyscallExec = func(_ string, argv, _ []string) error {
		l.argv = append([]string{}, argv...)
		if menu := menuPathIn(argv); menu != "" {
			l.lockHeld, l.lockInherited = probeMenuLock(t, filepath.Join(filepath.Dir(menu), modelmenu.LiveLockFile))
		}
		return nil
	}
	t.Cleanup(func() { hostSyscallExec = origExec })
	var out, errw bytes.Buffer
	l.rc = hostMain(append(append(append([]string{}, flags...), "--", "codex"), args...), &out, &errw, false, nil)
	l.errs = errw.String()
	if data, err := os.ReadFile(reads); err == nil {
		l.catalogReads = strings.Count(string(data), "\n")
	}
	return l
}

// menuPathIn is the file a `model_catalog_json=` word of argv names, "" when none does.
func menuPathIn(argv []string) string {
	for _, a := range argv {
		if p, ok := strings.CutPrefix(a, "model_catalog_json="); ok {
			return p
		}
	}
	return ""
}

// probeMenuLock asks, from a descriptor of its own, whether some holder keeps lock, and whether a
// descriptor of this process holding it would survive an exec.
func probeMenuLock(t *testing.T, lock string) (held, inherited bool) {
	t.Helper()
	f, err := os.OpenFile(lock, os.O_RDWR, 0)
	if err != nil {
		t.Errorf("the menu's live lock %s: %v", lock, err)
		return false, false
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err == nil {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	} else {
		held = errors.Is(err, syscall.EWOULDBLOCK)
	}
	if runtime.GOOS != "linux" {
		return held, true
	}
	want, _ := filepath.EvalSymlinks(lock)
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		return held, true
	}
	for _, e := range entries {
		fd, err := strconv.Atoi(e.Name())
		if err != nil || uintptr(fd) == f.Fd() {
			continue
		}
		if target, err := os.Readlink(filepath.Join("/proc/self/fd", e.Name())); err == nil && target == want {
			if flags, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0); err == nil && flags&unix.FD_CLOEXEC == 0 {
				inherited = true
			}
		}
	}
	return held, inherited
}

// menuSlugs is the slugs of the menu at path, in order.
func menuSlugs(t *testing.T, path string) []any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("no menu at %s: %v", path, err)
	}
	var doc struct {
		Models []map[string]any `json:"models"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	var slugs []any
	for _, m := range doc.Models {
		slugs = append(slugs, m["slug"])
		if _, has := m["upgrade"]; has {
			t.Errorf("%s keeps its upgrade", m["slug"])
		}
	}
	return slugs
}

const codexOnTheSubscription = `{"packs": ["codex"], "profile": {"codex": "codex"}}`

// THE DONE-WHEN: `yolo host -- codex` with the subscription configured for codex hands codex
// yolo's menu, built from its own catalog for the subscription's list, named by the flag right
// after argv[0], kept in yolo's state and never in ~/.codex, disclosed in the pack flags' words,
// with the missing id said in the host's; and the program holds the menu's lock across the exec.
func TestHostCodexOnTheConfiguredSubscriptionGetsYolosMenu(t *testing.T) {
	l := runHostMenu(t, "", codexOnTheSubscription, nil, nil, "exec", "hi")
	if l.rc != 0 || l.argv == nil {
		t.Fatalf("yolo host -- codex exec hi: rc=%d, exec reached=%v\n%s", l.rc, l.argv != nil, l.errs)
	}
	menu := menuPathIn(l.argv)
	if want := []string{"codex", "-c", "model_catalog_json=" + menu, "exec", "hi"}; menu == "" ||
		!reflect.DeepEqual(l.argv, want) {
		t.Fatalf("codex was exec'd with %q, want the menu's flag right after argv[0] (%q)\n%s", l.argv, want, l.errs)
	}
	if dir := filepath.Join(paths.HostModelMenusDirUnder(l.home), "codex", "codex"); filepath.Dir(menu) != dir {
		t.Errorf("the menu is at %s, want it in yolo's state, %s", menu, dir)
	}
	if slugs := menuSlugs(t, menu); !reflect.DeepEqual(slugs, []any{"gpt-6.1-sol", "gpt-6-astra"}) {
		t.Errorf("menu = %v, want the subscription's listed ids codex's catalog has, in list order", slugs)
	}
	for _, want := range []string{
		"yolo host: yolo CHANGED the command you asked for:",
		"  you asked for: codex exec hi",
		"  added by pack codex: -c model_catalog_json=" + menu + " (codex's model menu: yolo's models for openai-codex",
		"yolo host: codex's own model catalog has no gpt-6-luna",
	} {
		if !strings.Contains(l.errs, want) {
			t.Errorf("stderr lacks %q:\n%s", want, l.errs)
		}
	}
	if !l.lockHeld || !l.lockInherited {
		t.Errorf("at the exec the menu's lock was held=%v and would cross the exec=%v, want both: a later "+
			"launch could remove the menu codex reads", l.lockHeld, l.lockInherited)
	}
	if _, err := os.Stat(filepath.Join(l.home, ".codex", "yolo-model-menu.json")); !os.IsNotExist(err) {
		t.Errorf("the host wrote a menu into the user's ~/.codex (%v)", err)
	}

	// The next launch reuses the menu: codex's catalog is not read again.
	again := runHostMenu(t, l.home, "", nil, nil)
	if menuPathIn(again.argv) != menu || again.catalogReads != 1 {
		t.Errorf("a second launch exec'd %q after %d catalog reads, want the same menu read once", again.argv,
			again.catalogReads)
	}
}

// A -p OVER ANOTHER PROVIDER MOVES CODEX ONTO IT, AND THE MENU FOLLOWS (MM-D30, OQ-MM5 on its
// leaning B; this inverts MM-D25's no-menu rule): `yolo host -p codex -- codex` with the config on
// zai, or naming no profile, hands codex the subscription's selection as `-c` right after argv[0],
// the declared `openai` standing for the model_provider a jail's render would clear, and the
// subscription's menu after it, both disclosed; and writes none of it: the user's
// ~/.codex/config.toml is byte-identical, and no file in the home but the menu names the model.
func TestHostCodexMovesOntoAPOverAnotherProviderAndGetsItsMenu(t *testing.T) {
	for _, tc := range []struct{ name, cfg string }{
		{"config names another provider", `{"packs": ["codex", "zai"], "profile": {"codex": "zai"}, ` +
			`"env_sources": [{"ZAI_API_KEY": "k"}]}`},
		{"config names none", `{"packs": ["codex"]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := hostGateHome(t, tc.cfg, nil)
			own := "model_provider = \"zai\"\nmodel = \"glm-5.3\"\n"
			writeFile(t, filepath.Join(home, ".codex", "config.toml"), own)
			l := runHostMenu(t, home, "", nil, []string{"-p", "codex"}, "exec", "hi")
			if l.rc != 0 || l.argv == nil {
				t.Fatalf("rc=%d, exec reached=%v\n%s", l.rc, l.argv != nil, l.errs)
			}
			menu := menuPathIn(l.argv)
			want := []string{"codex", "-c", "model_catalog_json=" + menu, "-c", `model="gpt-6.1-sol"`,
				"-c", `model_provider="openai"`, "exec", "hi"}
			if menu == "" || !reflect.DeepEqual(l.argv, want) || l.catalogReads != 1 {
				t.Fatalf("codex got %q after %d catalog reads, want %q\n%s", l.argv, l.catalogReads, want, l.errs)
			}
			for _, want := range []string{
				"  added by pack codex: -c model=\"gpt-6.1-sol\" -c model_provider=\"openai\" (-p codex's " +
					"selection for codex, for this launch only, its config files untouched: model=gpt-6.1-sol, " +
					"model_provider=openai)",
				"(codex's model menu: yolo's models for openai-codex",
			} {
				if !strings.Contains(l.errs, want) {
					t.Errorf("stderr lacks %q:\n%s", want, l.errs)
				}
			}
			if strings.Contains(l.errs, "gets no model menu") {
				t.Errorf("the menu was refused for a -p that moved codex:\n%s", l.errs)
			}
			if got, _ := os.ReadFile(filepath.Join(home, ".codex", "config.toml")); string(got) != own {
				t.Errorf("~/.codex/config.toml changed:\n%s", got)
			}
			// The menu names the model, the stub's catalog and the launch log's disclosure do too;
			// nothing else may.
			assertNoFileNames(t, home, "gpt-6.1-sol", filepath.Dir(menu), filepath.Join(home, "catalog.json"),
				filepath.Join(home, ".local", "share", "yolo-jail", "logs"))
		})
	}
}

// assertNoFileNames fails for any file under root, outside skip, whose content holds word: a launch
// that hands a selection must not have written it anywhere, a managed CODEX_HOME included.
func assertNoFileNames(t *testing.T, root, word string, skip ...string) {
	t.Helper()
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		for _, s := range skip {
			if path == s || strings.HasPrefix(path, s+string(os.PathSeparator)) {
				return nil
			}
		}
		if data, err := os.ReadFile(path); err == nil && strings.Contains(string(data), word) {
			t.Errorf("%s holds %q: the launch wrote its selection to a file", path, word)
		}
		return nil
	})
}

// A -p OVER A PROVIDER OF THE USER'S OWN hands codex its provider, model and row, every key of the
// row as one `-c`, so codex finds the row whether or not `yolo host apply` wrote it; no list, so
// no menu, and codex's catalog is not read.
func TestHostCodexMovesOntoAUserProviderWithItsRow(t *testing.T) {
	cfg := `{"packs": ["codex"], "profile": {"codex": "codex"}, ` +
		`"providers": {"acme": {"endpoints": {"openai": {"base_url": "https://acme.example/v1"}}, ` +
		`"api_key_env_name": "ACME_KEY", "models": {"default": "acme-1"}}}, ` +
		`"profiles": {"acme": {"provider": "acme"}}, "env_sources": [{"ACME_KEY": "k"}]}`
	l := runHostMenu(t, "", cfg, nil, []string{"-p", "acme"}, "exec")
	if l.rc != 0 || l.argv == nil {
		t.Fatalf("rc=%d, exec reached=%v\n%s", l.rc, l.argv != nil, l.errs)
	}
	for _, want := range []string{`model_provider="acme"`, `model="acme-1"`,
		`model_providers.acme.base_url="https://acme.example/v1"`, `model_providers.acme.env_key="ACME_KEY"`,
		`model_providers.acme.wire_api="responses"`, `model_providers.acme.name="acme"`} {
		if i := slices.Index(l.argv, want); i < 1 || l.argv[i-1] != "-c" {
			t.Errorf("codex got %q, which lacks -c %s\n%s", l.argv, want, l.errs)
		}
	}
	if menuPathIn(l.argv) != "" || l.catalogReads != 0 || l.argv[len(l.argv)-1] != "exec" {
		t.Errorf("codex on acme got %q after %d catalog reads, want no menu and its own argv last",
			l.argv, l.catalogReads)
	}
}

// NO -p, OR ONE THAT AGREES, HANDS NOTHING: codex starts on its file, so a model the user chose in
// codex since `yolo host apply` wrote it stands. A -p codex over the configured subscription runs
// exactly what a bare launch does.
func TestHostCodexOnAnAgreeingPGetsNoSelection(t *testing.T) {
	agree := runHostMenu(t, "", codexOnTheSubscription, nil, []string{"-p", "codex"}, "exec")
	menu := menuPathIn(agree.argv)
	if want := []string{"codex", "-c", "model_catalog_json=" + menu, "exec"}; menu == "" ||
		!reflect.DeepEqual(agree.argv, want) {
		t.Errorf("-p codex over the configured subscription exec'd %q, want %q\n%s", agree.argv, want, agree.errs)
	}
	if strings.Contains(agree.errs, "selection for codex") {
		t.Errorf("an agreeing -p disclosed a selection:\n%s", agree.errs)
	}
}

// A USER'S OWN LATER -c STILL WINS (MM-D22's rule, MM-D30): the selection's words go right after
// argv[0], so the user's `-c model=...` typed after them is the one codex applies.
func TestHostCodexLetsTheUsersOwnLaterDashCWin(t *testing.T) {
	l := runHostMenu(t, "", `{"packs": ["codex"]}`, nil, []string{"-p", "codex"}, "-c", `model="mine"`, "exec")
	yolo, user := slices.Index(l.argv, `model="gpt-6.1-sol"`), slices.Index(l.argv, `model="mine"`)
	if yolo < 0 || user < 0 || yolo > user {
		t.Errorf("codex got %q: the selection's model must come before the user's own\n%s", l.argv, l.errs)
	}
}

// WHERE A -p CANNOT MOVE CODEX IT SAYS SO and codex starts on its file: zai composes no selection
// codex can run on (chat completions only), whether the config names another profile for codex or
// none, and YOLO_NO_LAUNCH_FLAGS=1 skips the handoff. Each line names a next step that works: for
// zai, not naming it in the config, since `yolo host apply` runs the same derive and composes
// nothing either, but the profiles that do move codex; and neither launch reads a catalog for a
// provider codex is not on.
func TestHostCodexSaysWhenAPCannotMoveIt(t *testing.T) {
	for _, tc := range []struct{ name, cfg string }{
		{"config names another profile", `{"packs": ["codex", "zai"], "profile": {"codex": "codex"}, ` +
			`"env_sources": [{"ZAI_API_KEY": "k"}]}`},
		{"config names none", `{"packs": ["codex", "zai"], "env_sources": [{"ZAI_API_KEY": "k"}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			zai := runHostMenu(t, "", tc.cfg, nil, []string{"-p", "zai"}, "exec")
			if !reflect.DeepEqual(zai.argv, []string{"codex", "exec"}) || zai.catalogReads != 0 {
				t.Errorf("-p zai exec'd %q after %d catalog reads, want codex's own argv", zai.argv, zai.catalogReads)
			}
			lead := "Profiles that do move codex, for one launch with `yolo host -p <profile> -- codex`: "
			for _, want := range []string{"yolo host: -p zai composes no selection for codex, so codex starts on " +
				"the selection its own config holds (~/.codex/config.toml)",
				"naming that profile in your config would not move it either", lead} {
				if !strings.Contains(zai.errs, want) {
					t.Errorf("stderr lacks %q:\n%s", want, zai.errs)
				}
			}
			// The profiles named are ones a -p moves codex onto: the subscription's, never zai.
			_, rest, _ := strings.Cut(zai.errs, lead)
			named, _, _ := strings.Cut(rest, ".\n")
			if movers := strings.Split(named, ", "); !slices.Contains(movers, "codex") || slices.Contains(movers, "zai") {
				t.Errorf("the profiles named as moving codex are %q, want codex's among them and never zai", movers)
			}
			for _, not := range []string{`"profile": {"codex": "zai"}`, "cannot reach"} {
				if strings.Contains(zai.errs, not) {
					t.Errorf("stderr names %q, a step that cannot move codex:\n%s", not, zai.errs)
				}
			}
		})
	}
	off := runHostMenu(t, "", `{"packs": ["codex"]}`, map[string]string{entrypoint.NoLaunchFlagsEnv: "1"},
		[]string{"-p", "codex"}, "exec")
	if !reflect.DeepEqual(off.argv, []string{"codex", "exec"}) || off.catalogReads != 0 {
		t.Errorf("with %s=1 codex got %q after %d catalog reads, want its own argv", entrypoint.NoLaunchFlagsEnv,
			off.argv, off.catalogReads)
	}
	if !strings.Contains(off.errs, entrypoint.NoLaunchFlagsEnv+"=1 is set") {
		t.Errorf("the skipped handoff was not said:\n%s", off.errs)
	}
}

// A PROGRAM WHOSE PACK DECLARES NO LAUNCH SELECTION keeps MM-D25's rule for its menu: a -p over
// another provider than its configured one does not move it, so no menu is built for the -p, and
// the line names why and the next step; over the same provider, and with no -p, the menu builds.
func TestTheMenuOfAProgramWithNoLaunchSelectionFollowsOnlyItsFile(t *testing.T) {
	resolved := map[string]packload.ResolvedProfile{"sub": {Provider: "openai-codex"}, "z": {Provider: "zai"}}
	none := &hostSelection{bin: "tool"}
	c := &hostComposition{agent: "tool", resolved: resolved, typedProfile: "sub", profile: "sub",
		configuredSet: []string{"z"}}
	line, follows := c.menuFollowsTheLaunch(none)
	if follows || !strings.Contains(line, "tool's pack declares no launch_selection") ||
		!strings.Contains(line, "run `yolo host apply`") {
		t.Errorf("a -p over another provider: follows=%v, line %q", follows, line)
	}
	c.configuredSet = []string{"sub"}
	if _, follows := c.menuFollowsTheLaunch(none); !follows {
		t.Error("a -p over the configured provider must build the menu")
	}
	c.typedProfile = ""
	if _, follows := c.menuFollowsTheLaunch(nil); !follows {
		t.Error("no -p must build the menu")
	}
	c.typedProfile, c.configuredSet = "sub", []string{"z"}
	if line, follows := c.menuFollowsTheLaunch(&hostSelection{declared: true}); follows || line != "" {
		t.Errorf("a declared selection that did not move the program: follows=%v line=%q, want neither", follows, line)
	}
}

// THE MENU STEP ITSELF ASKS WHETHER THE PROGRAM FOLLOWS THE LAUNCH (MM-D30): modelMenu builds no
// menu for a -p its program was not moved onto, so deleting the step's call to the rule fails here
// and not only in the rule's own test. Over a codex composition whose -p is the subscription while
// the config names zai, with the subscription's list there to build from: a selection its pack does
// not declare (MM-D25's rule) or one that did not move codex builds none and reads no catalog, the
// first saying why; one that moved codex builds the menu from the same composition.
func TestTheMenuStepBuildsNoMenuForAPTheProgramWasNotMovedOnto(t *testing.T) {
	home := hostGateHome(t, `{"packs": ["codex", "zai"], "profile": {"codex": "zai"}, `+
		`"env_sources": [{"ZAI_API_KEY": "k"}]}`, nil)
	t.Setenv(entrypoint.NoLaunchFlagsEnv, "")
	reads := filepath.Join(home, "catalog.reads")
	catalog := filepath.Join(home, "catalog.json")
	writeFile(t, catalog, hostMenuCatalog)
	target := filepath.Join(home, "stub-bin", "codex")
	writeFile(t, target, "#!/bin/sh\nif [ \"$*\" = 'debug models --bundled' ]; then echo read >> '"+reads+
		"'; cat '"+catalog+"'; exit 0; fi\nexit 0\n")
	if err := os.Chmod(target, 0o755); err != nil {
		t.Fatal(err)
	}
	c := composeHostLaunch("codex", "codex", nil, func(string) {})
	step := func(sel *hostSelection) (*hostMenu, string, int) {
		t.Helper()
		var errw bytes.Buffer
		menu := c.modelMenu(target, c.environ(), sel, &errw)
		t.Cleanup(menu.Close)
		data, _ := os.ReadFile(reads)
		return menu, errw.String(), strings.Count(string(data), "\n")
	}
	menu, said, n := step(&hostSelection{bin: "codex"})
	if menu != nil || n != 0 {
		t.Errorf("a -p codex never moved onto got a menu (%v) after %d catalog reads\n%s", menu != nil, n, said)
	}
	if !strings.Contains(said, "yolo host: codex gets no model menu from yolo this launch") ||
		!strings.Contains(said, "codex's pack declares no launch_selection") {
		t.Errorf("the menu step did not say why it built none:\n%s", said)
	}
	if menu, said, n := step(&hostSelection{bin: "codex", declared: true}); menu != nil || n != 0 || said != "" {
		t.Errorf("a declared selection that did not move codex: menu=%v after %d catalog reads, said %q",
			menu != nil, n, said)
	}
	if menu, said, n := step(&hostSelection{bin: "codex", declared: true, follows: true}); menu == nil || n != 1 {
		t.Errorf("a selection that moved codex built no menu (%v) after %d catalog reads\n%s", menu != nil, n, said)
	}
}

// NO LIST, NO MENU, AND NOTHING SAID: codex configured on a provider whose ids codex's catalog does
// not carry has no list, so its catalog is not read and no flag is added; YOLO_NO_LAUNCH_FLAGS=1
// skips the step on the subscription too, as it does in a jail.
func TestHostCodexGetsNoMenuWhereThereIsNone(t *testing.T) {
	zai := runHostMenu(t, "", `{"packs": ["codex", "zai"], "profile": {"codex": "zai"}, `+
		`"env_sources": [{"ZAI_API_KEY": "k"}]}`, nil, nil, "exec")
	if !reflect.DeepEqual(zai.argv, []string{"codex", "exec"}) || zai.catalogReads != 0 {
		t.Errorf("codex on zai got %q after %d catalog reads, want its own argv and none\n%s", zai.argv,
			zai.catalogReads, zai.errs)
	}
	if strings.Contains(zai.errs, "model menu") {
		t.Errorf("a launch with no list said something about a menu:\n%s", zai.errs)
	}
	off := runHostMenu(t, "", codexOnTheSubscription, map[string]string{entrypoint.NoLaunchFlagsEnv: "1"}, nil, "exec")
	if !reflect.DeepEqual(off.argv, []string{"codex", "exec"}) || off.catalogReads != 0 {
		t.Errorf("with %s=1 codex got %q after %d catalog reads, want its own argv", entrypoint.NoLaunchFlagsEnv,
			off.argv, off.catalogReads)
	}
}

// ON THE RESIDENT PATH THE LAUNCH HOLDS THE LOCK ITSELF: a managed launch runs codex as a child, so
// yolo host keeps the menu's lock until the child exits and gives it back after, and the managed
// rewrite still runs last, after the menu's flag.
func TestHostManagedCodexHoldsTheMenusLockWhileItRuns(t *testing.T) {
	home := hostGateHome(t, codexOnTheSubscription, nil)
	fake := &lockProbingManagedLaunch{}
	orig := prepareOpenAIAuthHost
	prepareOpenAIAuthHost = func(hostPrelaunch, io.Writer) (managedOpenAIHostLaunch, error) { return fake, nil }
	t.Cleanup(func() { prepareOpenAIAuthHost = orig })
	fake.t = t
	l := runHostMenu(t, home, "", nil, nil, "exec")
	if l.rc != 23 || !fake.ran {
		t.Fatalf("the managed launch was not reached: rc=%d\n%s", l.rc, l.errs)
	}
	menu := menuPathIn(fake.argv)
	if menu == "" || len(fake.argv) < 4 || fake.argv[1] != fakeManagedArgvFlag || fake.argv[2] != "-c" {
		t.Fatalf("the managed launch ran %q, want its rewrite first and then the menu's flag", fake.argv)
	}
	if !fake.lockHeld {
		t.Error("while the managed codex ran, nothing held its menu's lock")
	}
	if held, _ := probeMenuLock(t, filepath.Join(filepath.Dir(menu), modelmenu.LiveLockFile)); held {
		t.Error("the lock outlived the managed launch")
	}
}

// A MANAGED LAUNCH IS MOVED BY ITS ARGV TOO: the managed CODEX_HOME's config.toml is rebuilt from
// the user's own (openaiauthhost, unchanged by MM-D30), so the -p's selection reaches a managed codex
// only through the words the launch hands it, which run ahead of the managed rewrite's.
func TestHostManagedCodexGetsTheSelectionInItsArgv(t *testing.T) {
	home := hostGateHome(t, `{"packs": ["codex", "zai"], "profile": {"codex": "zai"}, `+
		`"env_sources": [{"ZAI_API_KEY": "k"}]}`, nil)
	fake := &lockProbingManagedLaunch{t: t}
	orig := prepareOpenAIAuthHost
	prepareOpenAIAuthHost = func(hostPrelaunch, io.Writer) (managedOpenAIHostLaunch, error) { return fake, nil }
	t.Cleanup(func() { prepareOpenAIAuthHost = orig })
	l := runHostMenu(t, home, "", nil, []string{"-p", "codex"}, "exec")
	if l.rc != 23 || !fake.ran {
		t.Fatalf("the managed launch was not reached: rc=%d\n%s", l.rc, l.errs)
	}
	if !slices.Contains(fake.argv, `model_provider="openai"`) || !slices.Contains(fake.argv, `model="gpt-6.1-sol"`) ||
		fake.argv[1] != fakeManagedArgvFlag {
		t.Errorf("the managed codex ran %q, want the managed rewrite first and the selection's -c after it", fake.argv)
	}
}

// lockProbingManagedLaunch is fakeManagedHostLaunch whose Run also asks whether the menu its argv
// names is locked while it "runs".
type lockProbingManagedLaunch struct {
	fakeManagedHostLaunch
	t        *testing.T
	lockHeld bool
}

func (f *lockProbingManagedLaunch) Run(target string, argv, env []string, in io.Reader, out, errw io.Writer) (int, bool) {
	if menu := menuPathIn(argv); menu != "" {
		f.lockHeld, _ = probeMenuLock(f.t, filepath.Join(filepath.Dir(menu), modelmenu.LiveLockFile))
	}
	return f.fakeManagedHostLaunch.Run(target, argv, env, in, out, errw)
}

// THE LIST IS THE LAUNCH'S (MM-D24), AND THE FLAG LEADS THE PACK'S (MM-D26): a pack's `only` on the
// subscription reaches the menu through the table this launch composed, and a guarded launch flag
// another pack declares for codex comes after the menu's flag, as the jail's launcher orders them.
func TestHostCodexMenuFollowsTheLaunchsListAndLeadsThePacksFlags(t *testing.T) {
	home := hostGateHome(t, `{"packs": ["codex"], "profile": {"codex": "codex"}}`, nil)
	writeFile(t, filepath.Join(home, ".config", "yolo-jail", "local", "pack.json"), `{"name":"acme","contributes":[`+
		`{"kind":"models","provider":"openai-codex","only":["gpt-6-astra"]},`+
		`{"kind":"autonomy","guarded":{"launch":[{"bin":"codex","flags":["--ask-first"]}]}}]}`)
	l := runHostMenu(t, home, "", nil, nil, "exec")
	menu := menuPathIn(l.argv)
	if want := []string{"codex", "-c", "model_catalog_json=" + menu, "--ask-first", "exec"}; menu == "" ||
		!reflect.DeepEqual(l.argv, want) {
		t.Fatalf("codex was exec'd with %q, want %q\n%s", l.argv, want, l.errs)
	}
	if slugs := menuSlugs(t, menu); !reflect.DeepEqual(slugs, []any{"gpt-6-astra"}) {
		t.Errorf("menu = %v, want the pack's only, gpt-6-astra alone", slugs)
	}
	if strings.Contains(l.errs, "has no gpt-6-luna") {
		t.Errorf("the launch warned about an id the only dropped:\n%s", l.errs)
	}
}

// THE CATALOG RUN GETS THE LAUNCH'S ENVIRONMENT (MM-D28 (2)): codex is asked for its catalog in
// the environment the launch composed for codex itself — the pack's env and the child's PATH, the
// floor's bin/ on it — not in yolo host's own, so a codex that needs either to start prints its
// catalog here as it will run. The stub records what its catalog run saw.
func TestHostCodexReadsItsCatalogInTheEnvironmentTheLaunchComposed(t *testing.T) {
	home := hostGateHome(t, codexOnTheSubscription, nil)
	// Absent from yolo host's own environment, so only the composed one can carry it.
	t.Setenv("CODEX_NON_INTERACTIVE", "")
	os.Unsetenv("CODEX_NON_INTERACTIVE")
	seen := filepath.Join(home, "catalog.env")
	writeFile(t, filepath.Join(home, "stub-bin", "codex"), "#!/bin/sh\nif [ \"$*\" = 'debug models --bundled' ]; then "+
		"printf 'CODEX_NON_INTERACTIVE=%s\\nPATH=%s\\n' \"$CODEX_NON_INTERACTIVE\" \"$PATH\" > '"+seen+"'; "+
		"cat '"+filepath.Join(home, "catalog.json")+"'; exit 0; fi\nexit 0\n")
	if err := os.Chmod(filepath.Join(home, "stub-bin", "codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	l := runHostMenu(t, home, "", nil, nil, "exec")
	if menuPathIn(l.argv) == "" {
		t.Fatalf("no menu: codex got %q\n%s", l.argv, l.errs)
	}
	raw, err := os.ReadFile(seen)
	if err != nil {
		t.Fatalf("the catalog run recorded nothing: %v\n%s", err, l.errs)
	}
	got := string(raw)
	if !strings.Contains(got, "CODEX_NON_INTERACTIVE=1\n") {
		t.Errorf("the catalog run lacked the codex pack's env, so it ran in yolo host's own environment:\n%s", got)
	}
	if !strings.Contains(got, hostFloorBinDir()) {
		t.Errorf("the catalog run's PATH lacks the floor's bin/ %s, the child's PATH:\n%s", hostFloorBinDir(), got)
	}
}

// IN A JAIL THE JAIL'S OWN LAUNCHER ANSWERS FOR THE MENU (MM-D28 (9)), as it does for the binary
// (TestInAJailThereIsNoHostFloor): `yolo host -- codex` there runs the PATH copy, the jail's
// launcher, which builds codex's menu from the list the jail's boot rendered for the jail's own
// selection. A host menu composed from the user scope's `profile` would come later in the argv and
// win, `-c` being last-wins, so it adds none, reads no catalog and creates no store in the jail.
func TestHostCodexInAJailLeavesTheMenuToTheJailsLauncher(t *testing.T) {
	home := hostGateHome(t, codexOnTheSubscription, nil)
	t.Setenv("YOLO_VERSION", "0.0.0-test")
	l := runHostMenu(t, home, "", nil, nil, "exec")
	if l.rc != 0 || !reflect.DeepEqual(l.argv, []string{"codex", "exec"}) || l.catalogReads != 0 {
		t.Errorf("in a jail codex got %q (rc=%d) after %d catalog reads, want its own argv and none\n%s",
			l.argv, l.rc, l.catalogReads, l.errs)
	}
	if _, err := os.Stat(paths.HostModelMenusDirUnder(home)); !os.IsNotExist(err) {
		t.Errorf("an in-jail launch created %s (%v)", paths.HostModelMenusDirUnder(home), err)
	}
	// Nor a launch selection (MM-D30): the jail's own render wrote the jail launch's selection into
	// codex's file, and a host-composed one would come later in the argv and win. Over a config
	// naming no profile for codex, so the -p's selection differs from the configured one and only
	// the jail's own guard keeps it from being handed.
	bare := hostGateHome(t, `{"packs": ["codex"]}`, nil)
	t.Setenv("YOLO_VERSION", "0.0.0-test")
	moved := runHostMenu(t, bare, "", nil, []string{"-p", "codex"}, "exec")
	if moved.rc != 0 || !reflect.DeepEqual(moved.argv, []string{"codex", "exec"}) {
		t.Errorf("in a jail -p codex handed codex %q (rc=%d), want its own argv\n%s", moved.argv, moved.rc, moved.errs)
	}
	if strings.Contains(moved.errs, "selection for codex") {
		t.Errorf("in a jail -p codex disclosed a selection:\n%s", moved.errs)
	}
}
