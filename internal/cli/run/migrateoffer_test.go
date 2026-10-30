package run

// migrateoffer_test.go pins the launch's offer to convert an old-format config
// (docs/design/config-migration.md §1, RC-D19, RC-D21) through Run itself, so each test fails if
// the call in run.go is removed: y converts and the launch continues on the new file; n and no
// terminal both launch on the old file; "convert_later": true in the old user file silences it.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/configmigrate"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	yoloruntime "github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

type offerLaunch struct {
	home, userDir string
	stderr        bytes.Buffer
	launched      bool
	rc            int
}

// runOfferLaunch writes userConfig as the old user file and runs a macos-user launch whose
// backend call only records that the launch got there.
func runOfferLaunch(t *testing.T, userConfig string, terminal bool, answer string) *offerLaunch {
	t.Helper()
	return runOfferLaunchWith(t, userConfig, terminal, answer, nil)
}

// runOfferLaunchWith is runOfferLaunch with setup run on the options just before Run.
func runOfferLaunchWith(t *testing.T, userConfig string, terminal bool, answer string, setup func(*Options)) *offerLaunch {
	t.Helper()
	l := &offerLaunch{home: hostRefreshEnv(t)}
	t.Setenv("YOLO_USER_LAYER", "")
	l.userDir = filepath.Join(l.home, ".config", "yolo-jail")
	if err := os.MkdirAll(l.userDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(l.userDir, "config.jsonc"), []byte(userConfig), 0o644); err != nil {
		t.Fatal(err)
	}
	ws := t.TempDir()
	var stdout bytes.Buffer
	o := dispatchOptions(t, ws, "macos-user", &stdout, &l.stderr, nil)
	o.IsTTYStdin = func() bool { return terminal }
	o.IsTTYStderr = func() bool { return terminal }
	o.Stdin = strings.NewReader(answer)
	o.MacosUserRun = func(_ *jsonx.OrderedMap, _ string, _, _ []string, _, _ string, _ macosuser.HomeOverlay, _ macosuser.HostContext, _ bool, _ *jsonx.OrderedMap, _ []packload.BlockedTool, _ macosuser.JailDaemons) int {
		l.launched = true
		return 0
	}
	if setup != nil {
		setup(o)
	}
	l.rc = Run(*o)
	return l
}

func (l *offerLaunch) exists(name string) bool {
	_, err := os.Lstat(filepath.Join(l.userDir, name))
	return err == nil
}

func (l *offerLaunch) mustLaunch(t *testing.T) {
	t.Helper()
	if l.rc != 0 || !l.launched {
		t.Fatalf("Run() = %d, launched = %v; want the launch to go on\nstderr:\n%s", l.rc, l.launched, l.stderr.String())
	}
}

const oldUserConfig = "{\n  // my packs\n  \"packs\": []\n}\n"

func TestLaunchOfferYesConvertsThenLaunches(t *testing.T) {
	l := runOfferLaunch(t, oldUserConfig, true, "y\n")
	l.mustLaunch(t)
	if !l.exists("config.toml") || !l.exists("config.jsonc.pre-migrate") || l.exists("config.jsonc") {
		t.Fatalf("y did not convert the user file (toml %v, backup %v, old %v)\nstderr:\n%s",
			l.exists("config.toml"), l.exists("config.jsonc.pre-migrate"), l.exists("config.jsonc"), l.stderr.String())
	}
	s := l.stderr.String()
	for _, want := range []string{"Deprecated config format:", "this file is still JSONC", "Convert to TOML now? [y/N]",
		"Converted. The original is next to the new file, as config.jsonc.pre-migrate."} {
		if !strings.Contains(s, want) {
			t.Errorf("stderr lacks %q:\n%s", want, s)
		}
	}
}

func TestLaunchOfferNoLaunchesOnOldFile(t *testing.T) {
	l := runOfferLaunch(t, oldUserConfig, true, "n\n")
	l.mustLaunch(t)
	if l.exists("config.toml") || !l.exists("config.jsonc") {
		t.Fatalf("n changed the user file\nstderr:\n%s", l.stderr.String())
	}
	s := l.stderr.String()
	for _, want := range []string{"[y/N]", "Keeping the JSONC file for now."} {
		if !strings.Contains(s, want) {
			t.Errorf("stderr lacks %q:\n%s", want, s)
		}
	}
	// Everything needed to decide is said BEFORE the question, in this order: what is deprecated,
	// which files, that the old format keeps working, where the originals go, how to look first,
	// and how to stop being asked (RC-D35).
	ask := strings.Index(s, "[y/N]")
	prev := -1
	for _, want := range []string{"Deprecated config format:", filepath.Join(l.userDir, "config.jsonc") + "  →  config.toml",
		"keep working for a few more releases", "as config.jsonc.pre-migrate", "Preview:      yolo config migrate --dry-run",
		`Stop asking:  add "convert_later": true to ` + filepath.Join(l.userDir, "config.jsonc")} {
		i := strings.Index(s, want)
		if i < 0 || i > ask || i < prev {
			t.Errorf("%q is not said in order before the question (at %d, previous at %d, question at %d):\n%s", want, i, prev, ask, s)
		}
		prev = i
	}
}

func TestLaunchOfferNoTTYPrintsOneLineAndContinues(t *testing.T) {
	l := runOfferLaunch(t, oldUserConfig, false, "y\n")
	l.mustLaunch(t)
	if l.exists("config.toml") {
		t.Fatalf("a launch with no terminal converted the file\nstderr:\n%s", l.stderr.String())
	}
	s := l.stderr.String()
	if strings.Contains(s, "[y/N]") {
		t.Fatalf("a launch with no terminal asked:\n%s", s)
	}
	var lines []string
	for _, line := range strings.Split(s, "\n") {
		if strings.Contains(line, "Deprecated config format") {
			lines = append(lines, line)
		}
	}
	if len(lines) != 1 {
		t.Fatalf("want one notice line, got %d:\n%s", len(lines), s)
	}
	for _, want := range []string{"yolo config migrate --dry-run", `"convert_later": true`} {
		if !strings.Contains(s, want) {
			t.Errorf("the notice lacks %q:\n%s", want, s)
		}
	}
}

func TestConvertLaterSilencesTheOffer(t *testing.T) {
	// The control: the same file without the key is offered, so the silence below is the key's.
	if control := runOfferLaunch(t, "{\n  \"packs\": []\n}\n", false, ""); !strings.Contains(control.stderr.String(), "Deprecated config format") {
		t.Fatalf("the control launch made no offer:\n%s", control.stderr.String())
	}
	for _, terminal := range []bool{true, false} {
		l := runOfferLaunch(t, "{\n  \"packs\": [],\n  \"convert_later\": true\n}\n", terminal, "y\n")
		l.mustLaunch(t)
		if l.exists("config.toml") {
			t.Fatalf("terminal=%v: convert_later did not stop the conversion", terminal)
		}
		if s := l.stderr.String(); strings.Contains(s, "Deprecated config format") {
			t.Fatalf("terminal=%v: convert_later did not silence the offer:\n%s", terminal, s)
		}
	}
}

// An attach to a running jail is never asked: the running jail's config was frozen at its boot, so
// a conversion under it is a change it does not see, and every extra terminal would be asked again.
// A held liveness lock is what a running jail's keeper leaves, and it is what the launch reads
// before it reads the config. Fails if the guard in offerConfigMigration is deleted.
func TestAnAttachIsNotOfferedTheConversion(t *testing.T) {
	var held *os.File
	l := runOfferLaunchWith(t, oldUserConfig, true, "y\n", func(o *Options) {
		live, err := holdLivenessLock(yoloruntime.FromWorkspace(o.Workspace))
		if err != nil {
			t.Fatal(err)
		}
		held = live
	})
	releaseLock(held)
	if s := l.stderr.String(); strings.Contains(s, "[y/N]") || strings.Contains(s, "Deprecated config format") {
		t.Fatalf("an attach to a running jail was offered the conversion:\n%s", s)
	}
	if l.exists("config.toml") || !l.exists("config.jsonc") {
		t.Fatalf("an attach converted the user file\nstderr:\n%s", l.stderr.String())
	}
}

// The control for the attach test above, through the same seam: with --never-attach the held
// lock is not an attach, so the offer is made.
func TestANeverAttachLaunchIsOfferedBesideARunningJail(t *testing.T) {
	var held *os.File
	l := runOfferLaunchWith(t, oldUserConfig, true, "n\n", func(o *Options) {
		o.NeverAttach = true
		live, err := holdLivenessLock(yoloruntime.FromWorkspace(o.Workspace))
		if err != nil {
			t.Fatal(err)
		}
		held = live
	})
	releaseLock(held)
	if s := l.stderr.String(); !strings.Contains(s, "[y/N]") {
		t.Fatalf("a launch that never attaches was not offered the conversion:\n%s", s)
	}
}

// A setting with no TOML spelling yet is not a yolo bug: the offer lists it with its own next step
// and writes nothing. The person asked for a conversion that did not happen, so the launch stops
// and says how to launch on the old file instead (RC-D35).
func TestLaunchOfferYesWithAnUnconvertibleSettingNamesItAndStops(t *testing.T) {
	old := "{\n  \"packs\": [],\n  \"lsp_servers\": {\"rust\": {\"command\": \"rust-analyzer\", \"fileExtensions\": {\".rs\": \"rust\"}}}\n}\n"
	l := runOfferLaunch(t, old, true, "y\n")
	if l.rc == 0 || l.launched {
		t.Fatalf("Run() = %d, launched = %v; want a failed requested conversion to stop the launch\nstderr:\n%s", l.rc, l.launched, l.stderr.String())
	}
	if l.exists("config.toml") || !l.exists("config.jsonc") {
		t.Fatalf("an unconvertible setting still converted the file\nstderr:\n%s", l.stderr.String())
	}
	s := l.stderr.String()
	for _, want := range []string{"Some settings can't be converted to TOML yet:", "lsp_servers", "Nothing was changed.", "answer n to launch with your current files."} {
		if !strings.Contains(s, want) {
			t.Errorf("stderr lacks %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, "yolo bug") {
		t.Errorf("an unconvertible setting was called a yolo bug:\n%s", s)
	}
}

// A conversion that would change the config's meaning IS a yolo bug, and says where to report it.
func TestAConversionThatChangesMeaningIsReportedAsABug(t *testing.T) {
	var stderr bytes.Buffer
	o := &Options{Stderr: &stderr, Getenv: func(string) string { return "" }}
	plan := &configmigrate.MigrationPlan{Differences: []configmigrate.Difference{{Layer: "user", Path: "packs", Before: `["a"]`, After: `["b"]`}}}
	o.reportMigrationRefusal(plan, &configmigrate.InequalityError{Plan: plan}, []string{"/h/.config/yolo-jail/work.jsonc"})
	s := stderr.String()
	for _, want := range []string{"Couldn't convert work.jsonc", "this is a yolo bug", "Nothing was changed.", "answer n to launch with your current files.",
		"Please file it at " + migrationBugURL + " with the output above."} {
		if !strings.Contains(s, want) {
			t.Errorf("stderr lacks %q:\n%s", want, s)
		}
	}
}

// A linked user config is named before the question, set apart by a blank line on each side, and
// renamed with the rest; the closing report says so again (RC-D41).
func TestLaunchOfferWarnsAboutASymlinkBeforeTheQuestion(t *testing.T) {
	l := runOfferLaunchWith(t, oldUserConfig, true, "y\n", func(o *Options) {
		dir := filepath.Join(filepath.Dir(o.Workspace), "dotfiles")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(os.Getenv("HOME"), ".config", "yolo-jail", "config.jsonc")
		if err := os.Rename(link, filepath.Join(dir, "config.jsonc")); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(dir, "config.jsonc"), link); err != nil {
			t.Fatal(err)
		}
	})
	l.mustLaunch(t)
	s := l.stderr.String()
	warning := "config.jsonc is a symlink; it will be renamed to config.toml. If a dotfiles manager made it, you may need to update that too.\n\nConvert to TOML now?"
	if !strings.Contains(s, warning) || !strings.Contains(s, "Stop asking:") || strings.Index(s, "Stop asking:") > strings.Index(s, warning) {
		t.Fatalf("the warning is not after Stop asking and a blank line before the question:\n%s", s)
	}
	if !strings.Contains(s, "config.jsonc was a symlink and is now config.toml; if a dotfiles manager made it, you may need to update that too.") {
		t.Errorf("the closing report does not name the renamed link:\n%s", s)
	}
	if info, err := os.Lstat(filepath.Join(l.userDir, "config.toml")); err != nil || info.Mode()&os.ModeSymlink == 0 || l.exists("config.jsonc") {
		t.Fatalf("the link was not renamed (config.toml: %v, config.jsonc still there: %v)", err, l.exists("config.jsonc"))
	}
}

// With no linked file there is no warning.
func TestLaunchOfferHasNoSymlinkWarningWithoutALink(t *testing.T) {
	l := runOfferLaunch(t, oldUserConfig, true, "n\n")
	if s := l.stderr.String(); strings.Contains(s, "symlink") {
		t.Fatalf("a launch with no linked file warned about one:\n%s", s)
	}
}
