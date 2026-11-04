package cli

// yoloaccount_test.go pins `yolo yolo-account create | remove | share | unshare` and the
// refusal of the four macos-* verbs they replaced (OQ-MT8 of docs/design/named-setups.md;
// slice 7 of docs/design/named-setups-plan.md). No test here runs a real act: those ask for
// sudo and change the Mac, so the dispatcher is driven with stand-ins and the real wiring is
// read only where it refuses (any system but macOS).

import (
	"bytes"
	goruntime "runtime"
	"slices"
	"strings"
	"testing"
)

// TestTheMacosVerbsAreRefusedNamingTheirYoloAccountReplacement drives the front door, Main,
// so the test fails if the refusal's call site goes: the old verbs are not registry keys, and
// without it each would read as an unknown command.
func TestTheMacosVerbsAreRefusedNamingTheirYoloAccountReplacement(t *testing.T) {
	for _, c := range []struct {
		argv     []string
		want     string // the sentence the design's §10 fixes
		wantNext string // the command the refusal hands the user
	}{
		{[]string{"macos-setup"}, "yolo macos-setup is now yolo yolo-account create", "  yolo yolo-account create\n"},
		{[]string{"macos-teardown"}, "yolo macos-teardown is now yolo yolo-account remove", "  yolo yolo-account remove\n"},
		{[]string{"macos-fix-permissions"}, "yolo macos-fix-permissions is now yolo yolo-account share", "  yolo yolo-account share\n"},
		{[]string{"macos-fix-permissions", "/Users/Shared/yolo/My Project"},
			"yolo macos-fix-permissions is now yolo yolo-account share",
			"  yolo yolo-account share '/Users/Shared/yolo/My Project'\n"},
		{[]string{"macos-unshare", "/Users/Shared/yolo/p"}, "yolo macos-unshare is now yolo yolo-account unshare",
			"  yolo yolo-account unshare /Users/Shared/yolo/p\n"},
		// Asking the old verb for help is still the old verb: refused, never aliased.
		{[]string{"macos-setup", "--help"}, "yolo macos-setup is now yolo yolo-account create", "  yolo yolo-account create --help\n"},
	} {
		t.Run(strings.Join(c.argv, " "), func(t *testing.T) {
			var rc int
			stderr := captureStderr(t, func() {
				rc = Main(append([]string{"yolo"}, c.argv...))
			})
			if rc == 0 {
				t.Errorf("`yolo %s` exit 0, want non-zero", strings.Join(c.argv, " "))
			}
			if !strings.Contains(stderr, c.want) || !strings.Contains(stderr, c.wantNext) {
				t.Errorf("stderr = %q, want %q and the next step %q", stderr, c.want, c.wantNext)
			}
			if strings.Contains(stderr, "unknown command") {
				t.Errorf("the old verb read as an unknown command:\n%s", stderr)
			}
		})
	}
}

// TestEveryRenamedVerbNamesAYoloAccountVerbThatExists: the refusal's next step is a command
// this CLI runs (the Happy Path rule, docs/reference/happy-path-principle.md): the front door
// routes `yolo yolo-account` to its registry handler, and the named verb is one the dispatcher
// acts on rather than refuses.
func TestEveryRenamedVerbNamesAYoloAccountVerbThatExists(t *testing.T) {
	if sub, _, _ := routeArgv([]string{"yolo-account", "create"}); sub != "yolo-account" {
		t.Fatalf("`yolo yolo-account create` routes to %q, want the yolo-account verb", sub)
	}
	for old, verb := range renamedVerbs {
		if _, ok := registry[old]; ok {
			t.Errorf("%q is still a registry key; a removed verb is refused, never aliased", old)
		}
		var ran []string
		acts := recordingActs(&ran)
		var out, errw bytes.Buffer
		rc := yoloAccountMain([]string{"yolo-account", verb}, acts, &out, &errw)
		if rc != 0 || !slices.Equal(ran, []string{verb + ":"}) {
			t.Errorf("%s names `yolo yolo-account %s`, which ran %v (rc %d, stderr %q)", old, verb, ran, rc, errw.String())
		}
	}
	if len(renamedVerbs) != 4 {
		t.Errorf("renamedVerbs has %d entries, want the four macos-* verbs", len(renamedVerbs))
	}
}

func recordingActs(ran *[]string) yoloAccountActs {
	return yoloAccountActs{
		create: func(recheck bool) int {
			if recheck {
				*ran = append(*ran, "create:--recheck")
			} else {
				*ran = append(*ran, "create:")
			}
			return 0
		},
		remove:  func() int { *ran = append(*ran, "remove:"); return 0 },
		share:   func(p string) int { *ran = append(*ran, "share:"+p); return 0 },
		unshare: func(p string) int { *ran = append(*ran, "unshare:"+p); return 0 },
	}
}

// TestYoloAccountDispatchesEachVerbToItsAct pins the verb → act table and the path argument,
// and that a bare `yolo yolo-account`, an unknown verb or a stray argument runs nothing.
func TestYoloAccountDispatchesEachVerbToItsAct(t *testing.T) {
	for _, c := range []struct {
		args    []string
		wantRan []string
		wantRC  int
		wantErr string
	}{
		{[]string{"yolo-account", "create"}, []string{"create:"}, 0, ""},
		{[]string{"yolo-account", "create", "--recheck"}, []string{"create:--recheck"}, 0, ""},
		{[]string{"yolo-account", "create", "--recheck", "x"}, nil, 2, "yolo yolo-account create: takes no path"},
		{[]string{"yolo-account", "remove"}, []string{"remove:"}, 0, ""},
		{[]string{"yolo-account", "share"}, []string{"share:"}, 0, ""},
		{[]string{"yolo-account", "share", "/Users/Shared/yolo/p"}, []string{"share:/Users/Shared/yolo/p"}, 0, ""},
		{[]string{"yolo-account", "unshare", "/Users/Shared/yolo/p"}, []string{"unshare:/Users/Shared/yolo/p"}, 0, ""},
		{[]string{"yolo-account"}, nil, 2, "name a verb: create, remove, share or unshare"},
		{[]string{"yolo-account", "setup"}, nil, 2, `unknown verb "setup"`},
		{[]string{"yolo-account", "create", "x"}, nil, 2, "yolo yolo-account create: takes no path"},
		{[]string{"yolo-account", "share", "a", "b"}, nil, 2, "takes at most one path"},
	} {
		t.Run(strings.Join(c.args, " "), func(t *testing.T) {
			var ran []string
			var out, errw bytes.Buffer
			rc := yoloAccountMain(c.args, recordingActs(&ran), &out, &errw)
			if rc != c.wantRC || !slices.Equal(ran, c.wantRan) {
				t.Errorf("rc %d ran %v, want rc %d ran %v (stderr %q)", rc, ran, c.wantRC, c.wantRan, errw.String())
			}
			if c.wantErr != "" && !strings.Contains(errw.String(), c.wantErr) {
				t.Errorf("stderr = %q, want %q", errw.String(), c.wantErr)
			}
		})
	}
}

// TestYoloAccountWithNoVerbPrintsItsUsage: the bare command is a question, answered with the
// usage that lists every verb.
func TestYoloAccountWithNoVerbPrintsItsUsage(t *testing.T) {
	var ran []string
	var out, errw bytes.Buffer
	if rc := yoloAccountMain([]string{"yolo-account"}, recordingActs(&ran), &out, &errw); rc == 0 {
		t.Errorf("bare `yolo yolo-account` exit 0, want non-zero")
	}
	if !strings.Contains(errw.String(), yoloAccountUsage) {
		t.Errorf("bare `yolo yolo-account` did not print the usage:\n%s", errw.String())
	}
	for _, verb := range []string{"create", "remove", "share", "unshare"} {
		if !strings.Contains(yoloAccountUsage, "  "+verb+" ") {
			t.Errorf("yoloAccountUsage does not list %q", verb)
		}
	}
}

// TestEachYoloAccountVerbAnswersItsOwnHelp: each verb is a sudo-invoking or ACL-rewriting act,
// so `yolo yolo-account <verb> --help` prints that verb's usage and runs nothing.
func TestEachYoloAccountVerbAnswersItsOwnHelp(t *testing.T) {
	for verb, text := range yoloAccountVerbUsage {
		for _, flag := range []string{"--help", "-h", "help"} {
			var ran []string
			var out, errw bytes.Buffer
			rc := yoloAccountMain([]string{"yolo-account", verb, flag}, recordingActs(&ran), &out, &errw)
			if rc != 0 || len(ran) != 0 || strings.TrimSpace(out.String()) != strings.TrimSpace(text) {
				t.Errorf("`yolo yolo-account %s %s`: rc %d ran %v, stdout:\n%s", verb, flag, rc, ran, out.String())
			}
			if !strings.HasPrefix(text, "Usage: yolo yolo-account "+verb) {
				t.Errorf("%s's usage does not open with its own synopsis:\n%s", verb, text)
			}
		}
	}
}

// TestYoloAccountRunsTheMacosuserActs reads the production wiring, runYoloAccount, off a Mac,
// where each real act refuses before it touches anything: the refusal is macosuser's, so the
// test fails if a verb stops reaching its act.
func TestYoloAccountRunsTheMacosuserActs(t *testing.T) {
	if goruntime.GOOS == "darwin" {
		t.Skip("on darwin the acts are real: they ask for sudo and change the Mac")
	}
	t.Setenv("NO_COLOR", "1")
	for _, verb := range []string{"create", "remove", "share", "unshare"} {
		var rc int
		out := captureStdout(t, func() { rc = runYoloAccount([]string{"yolo-account", verb}) })
		if rc == 0 || !strings.Contains(out, "yolo yolo-account "+verb+" requires macOS.") ||
			!strings.Contains(out, "`yolo -- <cmd>`") {
			t.Errorf("`yolo yolo-account %s` off a Mac: rc %d, stdout %q", verb, rc, out)
		}
	}
}
