package entrypoint

// jailgrantfile_test.go pins the jail's half of the --with-credentials grant file
// (docs/design/credential-sources-separation.md ES-D37): the boot's hydrate step, which every
// session's entrypoint runs too, exports the grant file's values into the process environment and
// e.Vars, never a YOLO_ name, and a jail with no file reads nothing. Driven through the boot step
// table, so dropping the step's call fails it.

import (
	"os"
	"path/filepath"
	"testing"
)

func runHydrateStep(t *testing.T, e *Env) {
	t.Helper()
	for _, s := range bootSteps() {
		if s.name == "hydrate_user_env" {
			s.run(&bootRun{e: e, target: bootContainer})
			return
		}
	}
	t.Fatal("the boot has no hydrate_user_env step")
}

func TestTheBootExportsTheJailsGrantFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("GRANT_PROBE_API_KEY", "")
	t.Setenv("YOLO_GRANT_PROBE", "")
	path := filepath.Join(home, JailGrantFileRel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	body := "# a comment\nexport GRANT_PROBE_API_KEY='tok '\\''q'\\'''\nexport YOLO_GRANT_PROBE='1'\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	e := NewEnv(map[string]string{"JAIL_HOME": home})
	runHydrateStep(t, e)
	if got := os.Getenv("GRANT_PROBE_API_KEY"); got != "tok 'q'" {
		t.Errorf("the process environment holds GRANT_PROBE_API_KEY = %q, want the grant file's value", got)
	}
	if e.Vars["GRANT_PROBE_API_KEY"] != "tok 'q'" {
		t.Errorf("e.Vars holds %q, want the grant file's value for the generators", e.Vars["GRANT_PROBE_API_KEY"])
	}
	if got := os.Getenv("YOLO_GRANT_PROBE"); got != "" {
		t.Errorf("the grant file set the launcher contract key YOLO_GRANT_PROBE = %q", got)
	}
}

func TestABootWithNoGrantFileReadsNothing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("GRANT_PROBE_API_KEY", "")
	e := NewEnv(map[string]string{"JAIL_HOME": home})
	runHydrateStep(t, e)
	if _, ok := e.Vars["GRANT_PROBE_API_KEY"]; ok {
		t.Error("a jail with no grant file gained a granted value")
	}
}
