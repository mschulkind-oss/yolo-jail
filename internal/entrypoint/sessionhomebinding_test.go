package entrypoint

// sessionhomebinding_test.go pins the SESSION-HOME BINDINGS (per-session-profiles.md SP-D36):
// two selected packs that declare ONE relocation variable each get their own session home, and
// each program's launcher points the variable at its own pack's home, so neither agent reads, or
// writes its state into, the other's config directory. The shipped pi and omp packs are the case
// (both read stock pi's PI_CODING_AGENT_DIR); the variable is read off their declarations.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/sessionhome"
)

// Both homes are made, each pack's surfaces render into its own, and the bindings name each
// pack's programs with their own home. Fails if prepareSessionHomes stops exporting the bindings
// or the shipped omp pack stops declaring its directory.
func TestTwoPacksOnOneVariableEachGetTheirOwnHome(t *testing.T) {
	j := newSessionHomeJail(t)
	omp, err := embeddedPack("omp")
	if err != nil {
		t.Fatal(err)
	}
	ompHomes := omp.Decl.SessionHomes()
	if len(ompHomes) != 1 || ompHomes[0].Env != j.env {
		t.Fatalf("the omp pack declares %+v; this test needs one home on pi's variable %s", ompHomes, j.env)
	}
	j.packs = append(j.packs, omp)
	j.cleanPass(t, "", "acme", false)
	e, errw := j.pass(t, sessionA, "beta", true)
	mustBootClean(t, e, errw)

	piHome := j.sessionHome(t, sessionA)
	ompShared := filepath.Join(j.home, ".oh-omp", "agent")
	ompHome, err := sessionhome.Home(ompShared, sessionA)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"models.yml", "config.yml"} {
		if fi, err := os.Lstat(filepath.Join(ompHome, name)); err != nil || !fi.Mode().IsRegular() {
			t.Errorf("omp's %s in its session home is not a regular file: %v", name, err)
		}
		if _, err := os.Lstat(filepath.Join(piHome, name)); !os.IsNotExist(err) {
			t.Errorf("omp's %s landed in pi's session home: %v", name, err)
		}
	}
	// omp's login store is the shared one, linked dangling until the first write creates it.
	if got, err := os.Readlink(filepath.Join(ompHome, "agent.db")); err != nil || got != filepath.Join(ompShared, "agent.db") {
		t.Errorf("omp's agent.db -> %q (%v), want a link to the shared %s", got, err, filepath.Join(ompShared, "agent.db"))
	}
	want := map[string]string{"pi": piHome, "oh-omp": ompHome}
	got := map[string]string{}
	for _, line := range strings.Split(e.Vars[SessionHomesEnv], "\n") {
		f := strings.Split(line, "\t")
		if len(f) != 3 || f[1] != j.env {
			t.Fatalf("binding line %q is not `<bin>\\t%s\\t<home>`", line, j.env)
		}
		got[f[0]] = f[2]
	}
	for bin, home := range want {
		if got[bin] != home {
			t.Errorf("%s binds %s to %q, want its own pack's home %s (bindings %q)", bin, j.env, got[bin], home, e.Vars[SessionHomesEnv])
		}
	}
	if os.Getenv(SessionHomesEnv) != e.Vars[SessionHomesEnv] {
		t.Errorf("the bindings reached e.Vars but not the process environment the shell inherits")
	}
}

// No session home, no bindings: the jail's own boot exports nothing.
func TestNoSessionHomeExportsNoBindings(t *testing.T) {
	j := newSessionHomeJail(t)
	t.Setenv(SessionHomesEnv, "")
	_ = os.Unsetenv(SessionHomesEnv)
	e, errw := j.pass(t, "", "acme", false)
	mustBootClean(t, e, errw)
	if v, ok := e.Vars[SessionHomesEnv]; ok {
		t.Errorf("a pass with no session exported %s=%q", SessionHomesEnv, v)
	}
}

// Every agent launcher (both installer templates) binds the variables its own program's lines
// name, over the session-wide value; a program with no line keeps what it inherited; a value the
// launch scoped to the agent in its env file still wins. SCOPED stands in for the declared
// variable because the probe program reports it.
func TestEachLauncherBindsItsOwnProgramsSessionHome(t *testing.T) {
	cases := []struct {
		name, bindings, envFile, want string
	}{
		{"its own line wins over the session-wide value",
			"other\tSCOPED\t/h/other\nprobe\tSCOPED\t/h/probe\n", "", "SCOPED=/h/probe OTHER="},
		{"another program's line alone changes nothing",
			"other\tSCOPED\t/h/other", "", "SCOPED=/h/session OTHER="},
		{"an unsafe variable name is skipped",
			"probe\tBAD-NAME\t/h/probe", "", "SCOPED=/h/session OTHER="},
		{"the agent's own env file still wins",
			"probe\tSCOPED\t/h/probe", "export SCOPED='/h/scoped'\n", "SCOPED=/h/scoped OTHER="},
		{"no bindings", "", "", "SCOPED=/h/session OTHER="},
	}
	for _, kind := range []string{"npm", "native"} {
		for _, c := range cases {
			t.Run(kind+"/"+c.name, func(t *testing.T) {
				home := t.TempDir()
				logPath := filepath.Join(home, "seen.log")
				launcher := probeLauncher(t, home, kind, logPath, launcherServers{})
				if c.envFile != "" {
					writeAgentEnvFile(t, home, "probe", c.envFile)
				}
				env := []string{"HOME=" + home, "PATH=/usr/bin:/bin", "SCOPED=/h/session"}
				if c.bindings != "" {
					env = append(env, SessionHomesEnv+"="+c.bindings)
				}
				out, rc := runScript(t, launcher, nil, env)
				if rc != 0 {
					t.Fatalf("the %s launcher exited %d:\n%s", kind, rc, out)
				}
				if got := strings.Join(logLines(t, logPath), " "); got != c.want {
					t.Errorf("the %s launcher handed its program %q, want %q\n%s", kind, got, c.want, out)
				}
			})
		}
	}
}
