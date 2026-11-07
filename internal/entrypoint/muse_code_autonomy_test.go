package entrypoint

// muse_code_autonomy_test.go is the autonomy half of muse_code_test.go's header: Muse's own
// `--yolo` on a bare `muse` in a jail, the guarded prompts-on flag at the host, and NOTHING in
// front of a Muse subcommand at either notch — Muse reads root options only before its TUI, so
// the old unconditional prefix broke `muse exec` (and the muse-planner wrapper that runs it).

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/render"
)

// museSubcommandSample is a cut of the subcommands `muse --help` (1.4.14) lists that take no
// root option in front of them: `muse --yolo exec …` exits 2 with `invalid TUI options:
// unknown argument 'exec'`. `exec` is the one the muse-planner pack's wrapper runs.
var museSubcommandSample = []string{"exec", "login", "skills", "auth", "usage"}

// TestMuseCodeAutonomyIsMusesYoloModeAtJailAndPromptsAtHost drives the injector every carrier
// folds through: a bare `muse` in a jail must get Muse's own `--yolo` (approval off, sandbox
// off, the workspace trusted for the run from any directory), and the host the guarded
// posture's prompts-on flag instead.
func TestMuseCodeAutonomyIsMusesYoloModeAtJailAndPromptsAtHost(t *testing.T) {
	p, err := embeddedPack("muse-code")
	if err != nil {
		t.Fatal(err)
	}
	packs := []*packload.Pack{p}

	jailAutonomy := render.ProfileFor(render.KindJail).AgentAutonomy
	jail, inj := packload.InjectLaunchFlags(packs, jailAutonomy, []string{"muse"})
	if got := strings.Join(jail, " "); got != "muse --yolo" {
		t.Errorf("a bare `muse` in a jail dispatches %q, want %q — without Muse's yolo mode the "+
			"agent asks for approval, and a `muse` started outside /workspace parks on the trust "+
			"dialog", got, "muse --yolo")
	}
	if inj == nil || len(inj.Subcommands) == 0 {
		t.Errorf("the bare-argv record carries no subcommand table, so no generated carrier can "+
			"spare `muse exec` the prefix: %+v", inj)
	}

	hostAutonomy := render.ProfileFor(render.KindHost).AgentAutonomy
	host, _ := packload.InjectLaunchFlags(packs, hostAutonomy, []string{"muse"})
	if got := strings.Join(host, " "); got != "muse --approval-mode on-request" {
		t.Errorf("the HOST launch dispatches %q, want the guarded posture's prompts-on flag and "+
			"never the jail's --yolo", got)
	}
}

// TestMuseCodeSubcommandsGetNoRootFlags is the regression cell for the prefix that broke every
// `muse <subcommand>` in a jail: a subcommand must reach Muse bare at both notches, while
// `resume` — an interactive session whose help says root options "may appear on either side"
// — keeps the posture.
func TestMuseCodeSubcommandsGetNoRootFlags(t *testing.T) {
	p, err := embeddedPack("muse-code")
	if err != nil {
		t.Fatal(err)
	}
	packs := []*packload.Pack{p}
	for _, kind := range []render.Kind{render.KindJail, render.KindHost} {
		autonomy := render.ProfileFor(kind).AgentAutonomy
		for _, sub := range museSubcommandSample {
			argv := []string{"muse", sub, "--help"}
			got, inj := packload.InjectLaunchFlags(packs, autonomy, argv)
			if strings.Join(got, " ") != strings.Join(argv, " ") || inj != nil {
				t.Errorf("%s: `muse %s` dispatches %q; Muse refuses a root option in front of a "+
					"subcommand (`unknown argument '%s'`), so it must pass through untouched",
					kind, sub, strings.Join(got, " "), sub)
			}
		}
	}
	got, _ := packload.InjectLaunchFlags(packs, render.ProfileFor(render.KindJail).AgentAutonomy,
		[]string{"muse", "resume", "--last"})
	if strings.Join(got, " ") != "muse --yolo resume --last" {
		t.Errorf("`muse resume --last` in a jail dispatches %q; resume is an interactive session "+
			"that takes root options before it, so it keeps the yolo posture", strings.Join(got, " "))
	}
}

// TestMuseCodeGeneratedLauncherYolosTheTUIAndSparesSubcommands runs the launcher the boot
// writes for the SHIPPED pack against a fake `muse` at the path the vendor installer fills,
// because the question is what the exec line does in a jail: `muse` typed at the prompt reaches
// this script (the alias is withheld for this bin), and so do an agent's `bash -c "muse exec
// …"` and the muse-planner wrapper.
func TestMuseCodeGeneratedLauncherYolosTheTUIAndSparesSubcommands(t *testing.T) {
	p, err := embeddedPack("muse-code")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.CopyFS(filepath.Join(root, "muse-code"), os.DirFS(p.Root)); err != nil {
		t.Fatalf("staging the shipped muse-code pack: %v", err)
	}
	home := t.TempDir()
	e := NewEnv(map[string]string{
		"JAIL_HOME": home,
		// Updates OFF, so the launcher's refresh branch cannot reach the network.
		AgentUpdatesEnv:  "false",
		"YOLO_PACK_ROOT": root,
	})
	runLaunchDirPasses(t, e)

	logPath := filepath.Join(home, "argv.log")
	fakeTarget(t, filepath.Join(home, ".local", "bin"), "muse", logPath)

	for _, c := range []struct {
		argv []string
		want string
	}{
		{nil, "--yolo"},
		{[]string{"fix the bug"}, "--yolo|fix the bug"},
		{[]string{"resume", "--last"}, "--yolo|resume|--last"},
		{[]string{"exec", "--prompt-file", "p"}, "exec|--prompt-file|p"},
		{[]string{"login"}, "login"},
		{[]string{"skills", "list"}, "skills|list"},
		{[]string{"--yolo"}, "--yolo"}, // a host `yolo -- muse` already injected it
	} {
		if err := os.Remove(logPath); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		out, rc := runScript(t, filepath.Join(e.LaunchDir(), "muse"), c.argv,
			[]string{"HOME=" + home, "PATH=/usr/bin:/bin"})
		if rc != 0 {
			t.Fatalf("muse %q: the launcher exited %d:\n%s", c.argv, rc, out)
		}
		if got := strings.Join(logLines(t, logPath), "|"); got != c.want {
			t.Errorf("muse %q: the launcher ran muse with %q, want %q\n%s", c.argv, got, c.want, out)
		}
	}

	if aliases := packAliases(e); strings.Contains(aliases, "alias muse=") {
		t.Errorf("an interactive alias was written for muse — a fixed prefix that turns `muse "+
			"exec` into `muse --yolo exec` before the launcher can decide:\n%s", aliases)
	}
}
