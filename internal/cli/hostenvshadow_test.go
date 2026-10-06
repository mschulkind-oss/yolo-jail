package cli

// hostenvshadow_test.go pins OQ-NC12's DISCLOSURE (ruled 2026-10-05) at the host: `yolo host --
// <cmd>` names each variable for which one of yolo's own sources beat another that set it
// otherwise, one line per name, in the jail's words (packload's envshadow.go), never a value, and
// prints nothing when nothing is shadowed. The invoking shell is no losing source (OQ-NC13: it has
// no say over a name yolo composes). Each test runs the verb and reads what it printed, so deleting
// the call in hostPreflight (or in hostEnvDelta, for `yolo host env`) fails it. The jail arms' pins
// are internal/cli/run's envshadow_test.go.

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

// wantHostShadowLines is what `yolo host -- fxa` prints over writeWinnerFixture: K3, which the
// shell sets against the shape var, gets none, nor do K8 (one source) and K9 (none).
var wantHostShadowLines = []string{
	"yolo host: Shadowed FXP_KEY: the fxp profile's value wins over your env_sources value",
	"yolo host: Shadowed K1: the fxp profile's value wins over the fx pack's value",
	"yolo host: Shadowed K2: the fxp profile's value wins over your env_sources value",
	"yolo host: Shadowed K4: your env_sources value wins over the fx pack's value",
	"yolo host: Shadowed K5: your env_sources removal wins over the fx pack's value",
	"yolo host: Shadowed K6: the fxp profile's value wins over your env_sources removal",
	"yolo host: Shadowed K7: the fxp profile's removal wins over your env_sources value and the fx pack's value",
}

// hostShadowLinesIn is every shadow line of out, in order.
func hostShadowLinesIn(out string) []string {
	var lines []string
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "Shadowed ") {
			lines = append(lines, l)
		}
	}
	return lines
}

// requireNoHostValue fails for any value of writeWinnerFixture's user sources and derive the
// output carries, and for the fold's in a shadow line.
func requireNoHostValue(t *testing.T, out string) {
	t.Helper()
	for _, l := range strings.Split(out, "\n") {
		for _, v := range []string{"shape-key", "es-claimed", "es-unclaimed", "user-shell", "=shape", "'shape'"} {
			if strings.Contains(l, v) {
				t.Errorf("the host printed the value %q: %s", v, l)
			}
		}
		if strings.Contains(l, "Shadowed ") && (strings.Contains(l, "fold-static") || strings.Contains(l, "gated")) {
			t.Errorf("a shadow line printed the fold's value: %s", l)
		}
	}
}

func TestTheHostLaunchNamesWhatItShadowed(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "")
	t.Chdir(t.TempDir())
	writeWinnerFixture(t, home)
	rc, reached, errw := hostExecRun(t, "fxa")
	if rc != 0 || !reached {
		t.Fatalf("yolo host -- fxa: rc=%d reached=%v\n%s", rc, reached, errw)
	}
	if got := hostShadowLinesIn(errw); strings.Join(got, "\n") != strings.Join(wantHostShadowLines, "\n") {
		t.Errorf("the host's shadow lines:\n got:\n%s\nwant:\n%s\nall:\n%s",
			strings.Join(got, "\n"), strings.Join(wantHostShadowLines, "\n"), errw)
	}
	requireNoHostValue(t, errw)
}

// `yolo host env` serializes the same composition into the script it prints, so its stderr names
// the same shadows, under its own prefix, and stdout (what a shell evals) carries none.
func TestTheHostEnvScriptNamesWhatItShadowed(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "")
	t.Chdir(t.TempDir())
	writeWinnerFixture(t, home)
	var out, errw bytes.Buffer
	if rc := hostEnv([]string{"--agent", "fxa"}, &out, &errw); rc != 0 {
		t.Fatalf("yolo host env --agent fxa: rc=%d\n%s", rc, errw.String())
	}
	var want []string
	for _, l := range wantHostShadowLines {
		want = append(want, strings.Replace(l, "yolo host: ", "yolo host env: ", 1))
	}
	if got := hostShadowLinesIn(errw.String()); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("yolo host env's shadow lines:\n got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if strings.Contains(out.String(), "Shadowed") {
		t.Errorf("a disclosure reached the script a shell evals:\n%s", out.String())
	}
	requireNoHostValue(t, errw.String())
}

// QUIET WHEN NOTHING IS SHADOWED: a pack's env and env_sources on different names, and a shell
// value of the pack's name, which is no source of yolo's, print no shadow line.
func TestTheHostLaunchShadowingNothingSaysNothing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "")
	t.Chdir(t.TempDir())
	root := filepath.Join(home, "packs", "plain")
	writeFile(t, filepath.Join(root, "pack.json"), `{"name":"plain","contributes":[`+
		`{"kind":"program","bin":"plainbin","via":"npm","package":"@acme/plainbin"},`+
		`{"kind":"env","vars":{"PLAIN_K":"fold-static"}}]}`)
	userCfg(t, home, `{"packs": [{"source": "file://`+root+`", "name": "plain"}],
	  "env_sources": [{"OTHER_K": "es-unclaimed"}]}`)
	t.Setenv("PLAIN_K", "user-shell")
	rc, reached, errw := hostExecRun(t, "plainbin")
	if rc != 0 || !reached {
		t.Fatalf("yolo host -- plainbin: rc=%d reached=%v\n%s", rc, reached, errw)
	}
	if got := hostShadowLinesIn(errw); len(got) != 0 {
		t.Errorf("nothing of yolo's own is shadowed, yet the host said:\n%s", strings.Join(got, "\n"))
	}
}
