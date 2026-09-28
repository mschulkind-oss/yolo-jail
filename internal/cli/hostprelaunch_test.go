package cli

// hostprelaunch_test.go pins notch-convergence item 15 at the host's call site: `yolo host --`
// hands the OpenAI prelaunch what the launched command's pack DECLARES in the composed
// environment (YOLO_AUTH_PRELAUNCH_<BIN>_*, the values a jail's launcher reads), and whether a
// human can answer a login. It used to hand it the command's name, so any `pi` or `codex`
// reached the broker's browser login, `yolo host -p zai -- pi </dev/null` included (MEASURED
// 2026-09-27). openaiauthhost's own tests pin what each prelaunch then does.

import (
	"io"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/openaiauthhost"
)

// hostPrelaunchRun runs `yolo host [flags] -- <agent>` and returns the prelaunch the launch
// handed to the OpenAI preparation.
func hostPrelaunchRun(t *testing.T, cfg string, tty bool, flags []string, agent string) (hostPrelaunch, bool) {
	t.Helper()
	hostGateHome(t, cfg, nil)
	setGateTTY(t, tty)
	var got hostPrelaunch
	called := false
	orig := prepareOpenAIAuthHost
	prepareOpenAIAuthHost = func(p hostPrelaunch, _ io.Writer) (managedOpenAIHostLaunch, error) {
		got, called = p, true
		return nil, nil
	}
	t.Cleanup(func() { prepareOpenAIAuthHost = orig })
	if rc, _, errw := hostExecRun(t, agent, flags...); rc != 0 {
		t.Fatalf("yolo host %v -- %s: rc=%d\n%s", flags, agent, rc, errw)
	}
	return got, called
}

// THE DONE-WHEN: pi on zai declares no prelaunch, since pi's pack gates its view on the codex
// profile, so the host starts no login (and no broker) for it, terminal or not.
func TestHostPiOnZaiDeclaresNoPrelaunch(t *testing.T) {
	for _, tty := range []bool{false, true} {
		p, called := hostPrelaunchRun(t, `{"packs": ["pi", "zai"], "env_sources": [{"ZAI_API_KEY": "k"}]}`,
			tty, []string{"-p", "zai"}, "pi")
		if !called {
			t.Fatal("the prelaunch call site was not reached")
		}
		if p.Declared() {
			t.Errorf("tty=%v: yolo host -p zai -- pi declared a prelaunch %+v; a jail's pi on zai logs "+
				"in to nothing", tty, p)
		}
	}
}

// What the shipped packs declare reaches the prelaunch as the jail's launcher reads it: codex's
// view unconditionally, pi's on the codex profile only, each keyed on its declaring pack, and the
// terminal probe as Interactive.
func TestHostPrelaunchIsWhatThePackDeclares(t *testing.T) {
	for _, tc := range []struct {
		name  string
		cfg   string
		flags []string
		agent string
		tty   bool
		want  hostPrelaunch
	}{
		{"codex", `{"packs": ["codex"]}`, nil, "codex", false,
			hostPrelaunch{Bin: "codex", Flag: openaiauthhost.CodexViewFlag, Pack: "codex"}},
		{"codex at a terminal", `{"packs": ["codex"]}`, nil, "codex", true,
			hostPrelaunch{Bin: "codex", Flag: openaiauthhost.CodexViewFlag, Pack: "codex", Interactive: true}},
		{"pi on codex", `{"packs": ["pi"]}`, []string{"-p", "codex"}, "pi", false,
			hostPrelaunch{Bin: "pi", Flag: openaiauthhost.PiViewFlag, Pack: "pi"}},
		{"pi on no profile", `{"packs": ["pi"]}`, nil, "pi", true,
			hostPrelaunch{Bin: "pi", Interactive: true}},
		{"a command no pack declares for", `{"packs": ["codex"]}`, nil, "true", false,
			hostPrelaunch{Bin: "true"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, _ := hostPrelaunchRun(t, tc.cfg, tc.tty, tc.flags, tc.agent)
			if got != tc.want {
				t.Errorf("prelaunch = %+v, want %+v", got, tc.want)
			}
		})
	}
}
