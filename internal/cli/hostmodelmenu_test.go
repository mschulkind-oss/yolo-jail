package cli

// hostmodelmenu_test.go pins codex's model menu at `yolo host --` (docs/design/model-lists-and-
// pickers.md §14.7, MM-D24 to MM-D28) at its call site, hostExec, through hostMain with the exec
// replaced: the shipped codex pack, a stub codex on PATH that prints a catalog for `debug models
// --bundled` and counts each read, and the argv the exec was handed. Deleting the step's call in
// hostExec, the list derive, the agreement check or the lock hand-over fails one of these. No codex
// runs and nothing is sent anywhere.

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/modelmenu"
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

// A -p OVER ANOTHER PROVIDER THAN THE CONFIGURED ONE GETS NO MENU (MM-D25): at the host the -p
// does not move codex, so a menu for the -p's provider could list models codex is not running on.
// The launch says why and runs codex as typed, without reading its catalog; a -p over the
// configured provider still gets the menu.
func TestHostCodexWithAPOverAnotherProviderGetsNoMenu(t *testing.T) {
	for _, tc := range []struct {
		name, cfg, why string
	}{
		{"config names another provider", `{"packs": ["codex", "zai"], "profile": {"codex": "zai"}, ` +
			`"env_sources": [{"ZAI_API_KEY": "k"}]}`, "the profile your config names for codex (zai) selects zai"},
		{"config names none", `{"packs": ["codex"]}`, "your config names no profile for codex"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l := runHostMenu(t, "", tc.cfg, nil, []string{"-p", "codex"}, "exec", "hi")
			if l.rc != 0 || l.argv == nil {
				t.Fatalf("rc=%d, exec reached=%v\n%s", l.rc, l.argv != nil, l.errs)
			}
			if !reflect.DeepEqual(l.argv, []string{"codex", "exec", "hi"}) || l.catalogReads != 0 {
				t.Errorf("codex got %q after %d catalog reads, want its own argv and none", l.argv, l.catalogReads)
			}
			for _, want := range []string{
				"yolo host: codex gets no model menu from yolo this launch: -p codex selects openai-codex, while " + tc.why,
				"OQ-MM5",
				"codex shows its own model menu.",
			} {
				if !strings.Contains(l.errs, want) {
					t.Errorf("stderr lacks %q:\n%s", want, l.errs)
				}
			}
		})
	}
	agree := runHostMenu(t, "", codexOnTheSubscription, nil, []string{"-p", "codex"})
	if menuPathIn(agree.argv) == "" || strings.Contains(agree.errs, "gets no model menu") {
		t.Errorf("-p codex over the configured subscription exec'd %q, want the menu\n%s", agree.argv, agree.errs)
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
}
