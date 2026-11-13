package modelmenu

// sessionhome_test.go pins the model menu's half of codex's per-session-profiles unlock
// (docs/design/per-session-profiles.md §7, "the menu written under the variable"): when the
// launcher names a session home whose variable the session set, the menu reads the session's own
// list and writes its own menu there, never the shared copy; with the variable unset it reads and
// writes under the home, as before.

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const otherListed = `{"models":[{"id":"gpt-6-astra","name":"GPT-6 Astra"}]}`

func TestRunReadsAndWritesTheMenuInTheSessionHome(t *testing.T) {
	f := newFixture(t, twoListed, catalog, 0)
	session := filepath.Join(t.TempDir(), "session", ".app")
	if err := os.MkdirAll(session, 0o755); err != nil {
		t.Fatal(err)
	}
	// The session's own render lists one model; the shared copy lists two.
	if err := os.WriteFile(filepath.Join(session, "list.json"), []byte(otherListed), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("APP_HOME", session)

	var out, errw bytes.Buffer
	rc := Run([]string{"--bin=app", "--spec=" + f.specJSON, SessionHomeFlag + ".app=APP_HOME", "--", f.program},
		f.home, &out, &errw)
	if rc != 0 {
		t.Fatalf("rc = %d, stderr %s", rc, errw.String())
	}
	menu := filepath.Join(session, "menu.json")
	words := strings.Split(strings.TrimSuffix(out.String(), "\x00"), "\x00")
	if want := []string{"-c", "model_catalog_json=" + menu}; !reflect.DeepEqual(words, want) {
		t.Errorf("printed flag = %q, want %q", words, want)
	}
	data, err := os.ReadFile(menu)
	if err != nil {
		t.Fatalf("no menu in the session home: %v", err)
	}
	var slugs []any
	for _, m := range decodeMenu(t, data) {
		slugs = append(slugs, m["slug"])
	}
	if !reflect.DeepEqual(slugs, []any{"gpt-6-astra"}) {
		t.Errorf("menu slugs = %v, want the session's own list, not the shared one", slugs)
	}
	if _, err := os.Stat(f.menuPath()); !os.IsNotExist(err) {
		t.Errorf("the shared menu path was written (err=%v); a session writes its own", err)
	}
}

func TestRunUsesTheHomeWhenTheSessionSetNoVariable(t *testing.T) {
	f := newFixture(t, twoListed, catalog, 0)
	t.Setenv("APP_HOME", "")
	var out, errw bytes.Buffer
	if rc := Run([]string{"--bin=app", "--spec=" + f.specJSON, SessionHomeFlag + ".app=APP_HOME", "--", f.program},
		f.home, &out, &errw); rc != 0 {
		t.Fatalf("rc = %d, stderr %s", rc, errw.String())
	}
	if _, err := os.Stat(f.menuPath()); err != nil {
		t.Errorf("no menu at the home's path with the variable unset: %v", err)
	}
}

func TestRunRefusesAMalformedSessionHome(t *testing.T) {
	for _, bad := range []string{SessionHomeFlag + ".app", SessionHomeFlag + "=APP", SessionHomeFlag + ".app="} {
		var out, errw bytes.Buffer
		if rc := Run([]string{"--bin=app", "--spec={}", bad, "--", "/bin/true"}, t.TempDir(), &out, &errw); rc != 2 {
			t.Errorf("Run with %q = %d, want 2", bad, rc)
		}
		if out.Len() != 0 {
			t.Errorf("a misuse printed %q on stdout", out.String())
		}
	}
}

func TestResolveHomeFileOnlyRebasesFilesUnderTheDeclaredDirectory(t *testing.T) {
	homes := []sessionHome{{dir: ".app", env: "APP_HOME"}}
	env := map[string]string{"APP_HOME": "/s/.app"}
	for rel, want := range map[string]string{
		".app/menu.json":      "/s/.app/menu.json",
		".app/sub/list.json":  "/s/.app/sub/list.json",
		".application/x.json": "/h/.application/x.json",
		".other/menu.json":    "/h/.other/menu.json",
	} {
		if got := resolveHomeFile("/h", rel, homes, func(k string) string { return env[k] }); got != want {
			t.Errorf("resolveHomeFile(%q) = %q, want %q", rel, got, want)
		}
	}
}
