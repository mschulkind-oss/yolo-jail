package run

// programoverrides_test.go pins a DEV OVERRIDE (docs/design/pack-buildable-programs.md §5, OQ-BG3's
// ruling): the checkout replaces a pack-tree program's `from` and is built like it, keyed on the
// checkout's tree digest, at Run's own entry point; every refusal of the path names it and the key;
// an override of any other program refuses the launch, naming the fork pack that delivers a
// checkout; and the launch and an attach name the checkout that runs.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/hostfloor/floortest"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

func overridePacks(t *testing.T) []*packload.Pack {
	t.Helper()
	return []*packload.Pack{inlinePack(t, "fx", `{"contributes":[`+
		`{"kind":"program","bin":"board","via":"pack","from":"tools/board","build":"make","run":"dist/board.js"},`+
		`{"kind":"program","bin":"pi","via":"npm","package":"@scope/pi"}]}`)}
}

func writeConfigFile(t *testing.T, path, body string) {
	t.Helper()
	mkdirs(t, filepath.Dir(path))
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mkdirs(t *testing.T, dirs ...string) {
	t.Helper()
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

func writeFileAt(t *testing.T, path, body string) {
	t.Helper()
	mkdirs(t, filepath.Dir(path))
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// boardFork is overridePacks' pack-tree program.
func boardFork(t *testing.T) packload.Fork {
	t.Helper()
	for _, f := range packload.PackTreePrograms(overridePacks(t)) {
		if f.Bin == "board" {
			return f
		}
	}
	t.Fatal("no board in the fixture")
	return packload.Fork{}
}

// THE CHECKS, the same on every notch (ProgramCheckout): a path with no symlink in it is applied as
// written; a path through a symlink root did not place (one an agent could re-point at ~/.ssh after
// the check), a missing path, a file, and the home (the credential boundary) are each refused naming
// the path and the key, the symlink's refusal naming the resolved path to write instead (BG-D22). No
// override leaves the fork alone.
func TestProgramCheckoutResolvesThePathAndRefusesWhatItCannotBuild(t *testing.T) {
	home := packHome(t)
	dev := floortest.ResolvedTemp(t)
	link := filepath.Join(floortest.ResolvedTemp(t), "dev")
	if err := os.Symlink(dev, link); err != nil {
		t.Fatal(err)
	}
	if os.Geteuid() == 0 {
		// The user's own link: as root, hand it to someone else, as it would be on a real machine.
		if err := os.Lchown(link, 65534, 65534); err != nil {
			t.Fatal(err)
		}
	}
	file := filepath.Join(dev, "file")
	writeFileAt(t, file, "x")
	mkdirs(t, filepath.Join(dev, "sub"))
	f := boardFork(t)

	got, why := ProgramCheckout(f, map[string]config.ProgramOverride{"board": {Path: dev}})
	if why != "" || got.Checkout != dev || got.SourceDir() != dev {
		t.Errorf("board at %s = %+v (%q), want that checkout", dev, got, why)
	}
	for name, tc := range map[string]struct {
		path string
		want []string
	}{
		"a symlink":       {link, []string{link, "a symlink owned by uid", "set the path to " + dev}},
		"under a symlink": {filepath.Join(link, "sub"), []string{link + ", a symlink", "set the path to " + filepath.Join(dev, "sub")}},
		"missing":         {filepath.Join(dev, "nope"), []string{filepath.Join(dev, "nope"), "does not exist"}},
		"not a dir":       {file, []string{file, "not a directory"}},
		"the home":        {home, []string{"IS your home directory", "credential boundary"}},
	} {
		got, why := ProgramCheckout(f, map[string]config.ProgramOverride{"board": {Path: tc.path}})
		if got.Checkout != "" {
			t.Errorf("%s: the checkout was applied: %+v", name, got)
		}
		for _, w := range append(tc.want, "programs.overrides.board in ~/.config/yolo-jail/config.toml") {
			if !strings.Contains(why, w) {
				t.Errorf("%s: refusal %q does not name %q", name, why, w)
			}
		}
	}
	if got, why := ProgramCheckout(f, nil); why != "" || got.Checkout != "" {
		t.Errorf("no override changed the fork: %+v %q", got, why)
	}
}

// A SYMLINK ROOT PLACED is followed, and the checkout is the resolved path: macOS's /var →
// /private/var, Fedora Atomic's /home → /var/home, a corporate home link. Here a root-owned link in a
// directory only root can write; skipped where the test is not root and cannot make one.
func TestProgramCheckoutFollowsALinkRootPlaced(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("making a root-owned symlink needs root")
	}
	packHome(t)
	dev := floortest.ResolvedTemp(t)
	dir := floortest.ResolvedTemp(t)
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "home")
	if err := os.Symlink(dev, link); err != nil {
		t.Fatal(err)
	}
	mkdirs(t, filepath.Join(dev, "board"))
	got, why := ProgramCheckout(boardFork(t), map[string]config.ProgramOverride{"board": {Path: filepath.Join(link, "board")}})
	if why != "" || got.Checkout != filepath.Join(dev, "board") {
		t.Errorf("a checkout through root's link = %q (%q), want the resolved %s", got.Checkout, why, filepath.Join(dev, "board"))
	}
}

// THE CHECKOUT IS BUILT LIKE THE PACK'S TREE (OQ-BG3): its revision is the checkout's own tree digest
// (`.git` skipped, uncommitted edits included), its build source is its own, so it never reuses the
// pack's build, an edit moves the digest, and a checkout that is gone by resolution is a reason, never
// the prebuilt.
func TestACheckoutIsKeyedOnItsOwnTreeDigest(t *testing.T) {
	packHome(t)
	dev := floortest.ResolvedTemp(t)
	writeFileAt(t, filepath.Join(dev, "main.go"), "package main\n")
	writeFileAt(t, filepath.Join(dev, ".git", "HEAD"), "ref: refs/heads/main\n")
	f := boardFork(t)
	ov := map[string]config.ProgramOverride{"board": {Path: dev}}

	pin := ResolvePackTreeProgram(f, "linux/amd64", false, ov)
	want, _ := packload.PackTreeDigest(dev)
	if pin.Reason != "" || pin.Commit != want || pin.Fork.BuildSource() != "checkout:fx:"+dev {
		t.Fatalf("pin = %+v, want the checkout keyed on its digest %s under its own build source", pin, want)
	}
	writeFileAt(t, filepath.Join(dev, ".git", "HEAD"), "ref: refs/heads/other\n")
	if again := ResolvePackTreeProgram(f, "linux/amd64", false, ov); again.Commit != pin.Commit {
		t.Errorf("a change under .git moved the digest: %s, was %s", again.Commit, pin.Commit)
	}
	writeFileAt(t, filepath.Join(dev, "main.go"), "package main // edited\n")
	if edited := ResolvePackTreeProgram(f, "linux/amd64", false, ov); edited.Commit == pin.Commit || edited.Commit == "" {
		t.Errorf("an uncommitted edit did not move the digest: %s", edited.Commit)
	}
	gone := pin.Fork
	gone.Checkout = filepath.Join(dev, "gone")
	if p := packload.PackTreeResolve(gone, "linux/amd64", false); p.Commit != "" || !strings.Contains(p.Reason, "is gone") {
		t.Errorf("a checkout gone by resolution = %+v, want a reason", p)
	}
}

// THE CALL SITE, at Run's own entry point: a launch whose user config overrides a pack-tree program
// hands the build act that program keyed on the CHECKOUT's digest, binds no checkout into the jail,
// and names the checkout, saying it rebuilt it; a launch whose slot answers from the store says it
// reused the build. Delete packTreePins' read of config.ProgramOverrides and this goes red.
func TestALaunchBuildsAnOverriddenProgramFromItsCheckout(t *testing.T) {
	for _, reused := range []bool{false, true} {
		home := packHome(t)
		t.Setenv("YOLO_VERSION", "")
		t.Setenv("YOLO_PACK_ROOT", "")
		pack := t.TempDir()
		writeFileAt(t, filepath.Join(pack, "pack.json"), `{"contributes":[`+
			`{"kind":"program","bin":"x","via":"pack","from":"tools/x","build":"make","run":"x"}]}`)
		writeFileAt(t, filepath.Join(pack, "tools", "x", "VERSION"), "1\n")
		dev := floortest.ResolvedTemp(t)
		writeFileAt(t, filepath.Join(dev, "VERSION"), "dev\n")
		writeConfigFile(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"),
			`{"packs":[{"source":"file://`+pack+`","name":"fx"}],"programs":{"overrides":{"x":{"path":"`+dev+`"}}}}`)
		want, _ := packload.PackTreeDigest(dev)

		var built []packload.ForkPin
		argv, printed := fakePodmanLaunch(t, func(o *Options) {
			o.BuildForks = func(req ForkBuildRequest) map[string]entrypoint.ForkDelivery {
				built = append(built, req.Pins...)
				return map[string]entrypoint.ForkDelivery{"x": {Key: "k-dev", Reused: reused}}
			}
		})
		if len(built) != 1 || built[0].Fork.Checkout != dev || built[0].Commit != want {
			t.Fatalf("reused=%v: the build act was handed %+v, want x from %s at %s\n%s", reused, built, dev, want, printed)
		}
		if strings.Contains(strings.Join(argv, "\n"), dev) || strings.Contains(strings.Join(argv, "\n"), "program-overrides") {
			t.Errorf("reused=%v: the checkout reached the jail's argv:\n%s", reused, strings.Join(argv, "\n"))
		}
		how := "rebuilt at tree digest " + want[:12]
		if reused {
			how = "reused its build at tree digest " + want[:12] + ", the checkout unchanged"
		}
		if line := CheckoutLine("x", dev, how); !strings.Contains(printed, line) {
			t.Errorf("reused=%v: the launch did not say %q:\n%s", reused, line, printed)
		}
	}
}

// AN OVERRIDE OF ANY OTHER PROGRAM IS REFUSED (BG-D19), naming the program, its delivery and the fork
// pack that delivers a checkout of it; one naming a pack-tree program, or no selected program, is not.
func TestAnOverrideOfAProgramNoPackBuildsFromItsTreeIsRefused(t *testing.T) {
	packHome(t)
	dev := floortest.ResolvedTemp(t)
	lines := NonPackTreeOverrideLines(overridePacks(t), map[string]config.ProgramOverride{
		"pi": {Path: dev}, "board": {Path: dev}, "elsewhere": {Path: dev}})
	if len(lines) != 1 {
		t.Fatalf("refusals = %q, want pi's alone", lines)
	}
	for _, w := range []string{"programs.overrides.pi in ~/.config/yolo-jail/config.toml", `via "npm"`,
		`[[program]] has via = "source"`, `fork_of = "fx"`, `source = "git+file://`, "remove this setting"} {
		if !strings.Contains(lines[0], w) {
			t.Errorf("refusal %q does not name %q", lines[0], w)
		}
	}
}

// THE LAUNCH REFUSES IT, at Run's own entry point, before anything is built. Delete Run's call of
// refuseNonPackTreeOverrides and this goes red.
func TestALaunchRefusesAnOverrideOfANonPackTreeProgram(t *testing.T) {
	home := packHome(t)
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("YOLO_PACK_ROOT", "")
	pack := t.TempDir()
	writeFileAt(t, filepath.Join(pack, "pack.json"), `{"contributes":[{"kind":"program","bin":"pi","via":"npm","package":"@scope/pi"}]}`)
	dev := floortest.ResolvedTemp(t)
	writeConfigFile(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"),
		`{"packs":[{"source":"file://`+pack+`","name":"fx"}],"programs":{"overrides":{"pi":{"path":"`+dev+`"}}}}`)
	argv, printed := fakePodmanLaunch(t, func(*Options) {})
	if argv != nil || !strings.Contains(printed, "Refusing to launch: programs.overrides.pi") ||
		!strings.Contains(printed, `fork_of = "fx"`) {
		t.Errorf("the launch ran (argv %q) or did not refuse naming the fork pack:\n%s", argv, printed)
	}
}

func TestAnOverrideForAnUndeclaredBinIsReportedAsDoingNothing(t *testing.T) {
	home := packHome(t)
	dev := floortest.ResolvedTemp(t)
	writeConfigFile(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"),
		`{"programs":{"overrides":{"elsewhere":{"path":"`+dev+`"},"board":{"path":"`+dev+`"}}}}`)
	lines := UndeclaredProgramOverrideLines(overridePacks(t))
	if len(lines) != 1 || !strings.Contains(lines[0], "programs.overrides.elsewhere") || !strings.Contains(lines[0], "does nothing") {
		t.Errorf("yolo check's information = %q", lines)
	}
}

// A PREBUILT OPT-IN THAT CAN DO NOTHING (BG-D18) is `yolo check` information: one naming a bin no
// selected pack declares, and one naming a program with no `prebuilt` to opt into (here board, which
// declares none, and the npm program pi). It is never a dev override.
func TestAPrebuiltOptInThatCanDoNothingIsReported(t *testing.T) {
	home := packHome(t)
	writeConfigFile(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"),
		`{"programs":{"overrides":{"elsewhere":{"prebuilt":true},"board":{"prebuilt":true},"pi":{"prebuilt":true}}}}`)
	lines := UndeclaredProgramOverrideLines(overridePacks(t))
	if len(lines) != 3 {
		t.Fatalf("yolo check's information = %q, want three lines", lines)
	}
	for i, want := range []string{"programs.overrides.board.prebuilt: pack fx declares no prebuilt",
		"programs.overrides.elsewhere.prebuilt: no selected pack declares elsewhere",
		"programs.overrides.pi.prebuilt: pack fx declares no prebuilt"} {
		if !strings.Contains(lines[i], want) {
			t.Errorf("line %d = %q, want %q", i, lines[i], want)
		}
	}
	if got := config.ProgramOverrides(nil); len(got) != 0 {
		t.Errorf("a prebuilt opt-in read as a dev override: %+v", got)
	}
}

// AN ATTACH NAMES THE CHECKOUT the running jail's launch built, from its delivery record.
func TestAnAttachNamesTheCheckoutItsJailRuns(t *testing.T) {
	// A home with no user config, so the line names the TOML file a new one would be (LC-D12).
	t.Setenv("HOME", t.TempDir())
	var out strings.Builder
	o := &Options{Stderr: &out, Getenv: func(string) string { return "" }}
	digest := strings.Repeat("d", 64)
	o.noteAttachForkBuilds(map[string]HandedFork{"x": {Key: "k", Checkout: "/home/me/code/x", CheckoutDigest: digest}})
	if !strings.Contains(out.String(), "x: built from your checkout /home/me/code/x (programs.overrides.x in "+
		"~/.config/yolo-jail/config.toml) — the build at tree digest "+digest[:12]) {
		t.Errorf("attach printed %q", out.String())
	}
}
