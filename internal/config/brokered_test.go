package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// brokered_test.go covers the `brokered` key (docs/design/boundary-broker.md §5.6, OQ-BB6,
// BB-D33): the widening entry, read from user scope alone and matched to a workspace by its
// resolved host path, and its validation through ValidateConfig, the call site `yolo check` and
// a launch reach. The launch's half, the scope file and the disclosure, is
// run.TestAWideningEntryJoinsTheScopeFileAndIsDisclosed.

// TestBrokeredWideningIsTheUserEntryForThisWorkspaceAlone: an entry keyed by the workspace, as
// `~/…`, as an absolute path or through a symlink, adds its repositories to that workspace and to
// no other; a refused key or repository is left out; another source's entry is not this one's;
// and a workspace config's spelling is never read, since a workspace may not widen itself.
func TestBrokeredWideningIsTheUserEntryForThisWorkspaceAlone(t *testing.T) {
	userCfg := hostFloorHome(t)
	home, err := filepath.EvalSymlinks(paths.Home())
	if err != nil {
		t.Fatal(err)
	}
	app := filepath.Join(home, "code", "app")
	other := filepath.Join(home, "code", "other")
	for _, d := range []string{app, other} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(home, "app-link")
	if err := os.Symlink(app, link); err != nil {
		t.Fatal(err)
	}
	// A relative key would name app from here, were it read.
	t.Chdir(home)

	write(t, filepath.Join(app, WorkspaceConfigName),
		`{"brokered": {"github": {"workspaces": {"`+app+`": {"repos": ["evil/from-the-workspace"]}}}}}`)
	if got := BrokeredWidening("github", app); len(got) != 0 {
		t.Fatalf("with no user config = %v, want none: a workspace value must never be read", got)
	}

	write(t, userCfg, `{"brokered": {
	  "github": {"workspaces": {
	    "~/code/app": {"repos": ["org/lib", "Org/Lib", "x/y/z", "github.com/a/b", "", 7, "acme/tools"]},
	    "`+link+`": {"repos": ["via/symlink"]},
	    "`+app+`/": {"repos": ["trailing/slash"]},
	    "~/code/other": {"repos": ["other/only"]},
	    "code/app": {"repos": ["relative/refused"]},
	    "$HOME/code/app": {"repos": ["variable/refused"]}
	  }},
	  "gitlab": {"workspaces": {"~/code/app": {"repos": ["wrong/source"]}}}
	}}`)
	want := "acme/tools,org/lib,trailing/slash,via/symlink"
	if got := strings.Join(BrokeredWidening("github", app), ","); got != want {
		t.Errorf("BrokeredWidening(app) = %s, want %s: every key resolving to app, each repository "+
			"once whatever its case, the refused ones left out", got, want)
	}
	if got := strings.Join(BrokeredWidening("github", link), ","); got != want {
		t.Errorf("the workspace spelled through its symlink = %s, want %s", got, want)
	}
	if got := strings.Join(BrokeredWidening("github", other), ","); got != "other/only" {
		t.Errorf("BrokeredWidening(other) = %s, want other/only alone: an entry widens its own workspace", got)
	}
	if got := BrokeredWidening("nosuch", app); len(got) != 0 {
		t.Errorf("a source with no entry = %v, want none", got)
	}
	if got := BrokeredWidening("github", filepath.Join(home, "code")); len(got) != 0 {
		t.Errorf("the workspace's parent = %v, want none: an entry is not inherited by a folder above it", got)
	}
}

// TestAWideningKeyThatRunsThroughTheWorkspaceNeverMatchesIt: BB-P9 against symlinks. What lies
// inside a workspace is its agent's to change, so a key whose resolution passes through the
// workspace's own tree must not match the workspace, however the agent points the links there.
// An entry for `app/sub`, or for a link of the user's own reaching `sub`, stays `sub`'s once the
// agent in `app` turns `sub` into a link back to `app`; a link the user made outside `app`,
// pointing at it, still matches `app`.
func TestAWideningKeyThatRunsThroughTheWorkspaceNeverMatchesIt(t *testing.T) {
	userCfg := hostFloorHome(t)
	home, err := filepath.EvalSymlinks(paths.Home())
	if err != nil {
		t.Fatal(err)
	}
	app := filepath.Join(home, "code", "app")
	sub := filepath.Join(app, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	for link, target := range map[string]string{"via-sub": sub, "app-link": app} {
		if err := os.Symlink(target, filepath.Join(home, link)); err != nil {
			t.Fatal(err)
		}
	}
	write(t, userCfg, `{"brokered": {"github": {"workspaces": {
	  "~/code/app": {"repos": ["org/app"]},
	  "~/app-link": {"repos": ["org/app-link"]},
	  "~/code/app/sub": {"repos": ["org/sub"]},
	  "~/via-sub": {"repos": ["org/via-sub"]}
	}}}}`)
	if got := strings.Join(BrokeredWidening("github", sub), ","); got != "org/sub,org/via-sub" {
		t.Fatalf("BrokeredWidening(sub) = %s, want org/sub,org/via-sub", got)
	}
	if got := strings.Join(BrokeredWidening("github", app), ","); got != "org/app,org/app-link" {
		t.Fatalf("BrokeredWidening(app) = %s, want org/app,org/app-link", got)
	}

	// The agent in app points sub back at its own workspace.
	if err := os.Remove(sub); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(".", sub); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(BrokeredWidening("github", app), ","); got != "org/app,org/app-link" {
		t.Errorf("after the agent linked sub to its own workspace, BrokeredWidening(app) = %s, want "+
			"org/app,org/app-link: an entry for sub, or reaching sub, must not widen app", got)
	}
}

// TestValidateBrokeredRefusesEachBadShapeAndAWorkspaceValue: a well-formed entry passes; each shape
// the reader would leave out is a named error at its path; and a workspace spelling is refused by
// name with the user config it belongs in (BB-P9).
func TestValidateBrokeredRefusesEachBadShapeAndAWorkspaceValue(t *testing.T) {
	hostFloorHome(t)
	clean := t.TempDir()
	for _, ok := range []string{
		`{"brokered": {"github": {"workspaces": {"~/code/app": {"repos": ["org/lib", "a.b/c_d-e"]}, "/srv/x": {"repos": []}}}}}`,
		`{"brokered": {"github": {"workspaces": {}}}}`,
		`{"brokered": {"github": null}}`,
	} {
		errs, _ := ValidateConfig(decode(t, ok), clean, nil)
		for _, e := range errs {
			if strings.Contains(e, "brokered") {
				t.Errorf("%s refused: %s", ok, e)
			}
		}
	}
	entry := func(key, body string) string {
		return `{"brokered": {"github": {"workspaces": {"` + key + `": ` + body + `}}}}`
	}
	for bad, want := range map[string]string{
		`{"brokered": []}`:                             "config.brokered: expected an object of source name",
		`{"brokered": {"github": "x"}}`:                `config.brokered.github: expected an object holding "workspaces"`,
		`{"brokered": {"github": {"sets": []}}}`:       "config.brokered.github.sets: unknown key",
		`{"brokered": {"github": {"workspaces": []}}}`: "config.brokered.github.workspaces: expected an object of workspace path",
		entry("code/app", `{"repos": []}`):             "config.brokered.github.workspaces.code/app: it is relative",
		entry("~matt/app", `{"repos": []}`):            "`~user/` is not expanded",
		entry("$HOME/app", `{"repos": []}`):            "carries a `$`",
		entry("~", `{"repos": []}`):                    "`~` is your home",
		entry("", `{"repos": []}`):                     "an empty key names no workspace",
		entry("/w", `"org/lib"`):                       `config.brokered.github.workspaces./w: expected an object holding "repos"`,
		entry("/w", `{"read": ["org/lib"]}`):           "config.brokered.github.workspaces./w.read: unknown key",
		entry("/w", `{"repos": "org/lib"}`):            "config.brokered.github.workspaces./w.repos: expected a list of OWNER/REPO strings",
		entry("/w", `{"repos": ["github.com/o/r"]}`):   "config.brokered.github.workspaces./w.repos[0]: 'github.com/o/r' is not OWNER/REPO",
		entry("/w", `{"repos": ["ok/one", 3]}`):        "config.brokered.github.workspaces./w.repos[1]: 3 is not OWNER/REPO",
		entry("/w", `{"repos": ["../x"]}`):             "'../x' is not OWNER/REPO",
		entry("/w", `{"repos": ["o/r x"]}`):            "'o/r x' is not OWNER/REPO",
	} {
		errs, _ := ValidateConfig(decode(t, bad), clean, nil)
		if joined := strings.Join(errs, "\n"); !strings.Contains(joined, want) {
			t.Errorf("%s: errors lack %q:\n%s", bad, want, joined)
		}
	}

	ws := t.TempDir()
	write(t, filepath.Join(ws, WorkspaceConfigName), entry(ws, `{"repos": ["org/lib"]}`))
	errs, _ := ValidateConfig(decode(t, entry(ws, `{"repos": ["org/lib"]}`)), ws, nil)
	joined := strings.Join(errs, "\n")
	for _, want := range []string{"config.brokered: user-scope only", "a workspace may never widen its own reach",
		paths.UserConfigPath()} {
		if !strings.Contains(joined, want) {
			t.Errorf("a workspace brokered: errors lack %q:\n%s", want, joined)
		}
	}
}

// TestValidateBrokeredWarnsOfASourceNoSelectedPackBrokers: an entry for a source that no selected
// pack's loophole brokers is inert, and says so, naming the sources that are brokered; an entry
// for a brokered source says nothing.
func TestValidateBrokeredWarnsOfASourceNoSelectedPackBrokers(t *testing.T) {
	hostFloorHome(t)
	cfg := `{"brokered": {"github": {"workspaces": {"/w": {"repos": ["org/lib"]}}}, "githb": {"workspaces": {}}}}`
	_, warns := ValidateConfig(decode(t, cfg), t.TempDir(), brokeredResolver)
	joined := strings.Join(warns, "\n")
	if !strings.Contains(joined, "config.brokered.githb: no loophole a selected pack ships brokers the source "+
		"'githb', so this entry has no effect (brokered sources here: github)") {
		t.Errorf("no warning for the unbrokered source:\n%s", joined)
	}
	if strings.Contains(joined, "config.brokered.github:") {
		t.Errorf("a warning for a brokered source:\n%s", joined)
	}
	_, warns = ValidateConfig(decode(t, cfg), t.TempDir(), fenceResolver{})
	if joined := strings.Join(warns, "\n"); !strings.Contains(joined, "config.brokered.github: no loophole") ||
		!strings.Contains(joined, "(brokered sources here: none)") {
		t.Errorf("with no brokered loophole selected, every source is inert:\n%s", joined)
	}
}
