package cli

import (
	"bytes"
	"io"
	"os"
	"slices"
	"strings"
	"testing"
)

func TestSurfaceNamespaceIsRegisteredAndDispatches(t *testing.T) {
	if !IsNative("surface") || Subcommand([]string{"surface", "ls"}) != "surface" {
		t.Fatal("surface is not a native subcommand")
	}
	sub, argv, implicit := routeArgv([]string{"surface", "render", "claude"})
	if sub != "surface" || implicit || strings.Join(argv, " ") != "surface render claude" {
		t.Fatalf("routeArgv(surface) = %q, %v, implicit=%v", sub, argv, implicit)
	}
	if _, ok := registry["config"]; !ok {
		t.Fatal("config editing command is no longer registered")
	}
	if _, ok := registry["surface"]; !ok {
		t.Fatal("surface dispatcher is missing from the production registry")
	}

	t.Setenv("HOME", t.TempDir())
	ws, _ := withWorkspaceCwd(t)
	oldUpdateHook := updateHook
	updateHook = func(string, []string) (int, bool) { return 0, false }
	t.Cleanup(func() { updateHook = oldUpdateHook })

	cases := []struct {
		verb string
		args []string
		at   bool
	}{
		{"ls", []string{"--all"}, true},
		{"render", []string{"pi/settings", "--explain"}, true},
		{"diff", []string{"claude/settings"}, true},
		{"reset", []string{"claude/settings", "--force"}, true},
		{"promote", []string{"claude/settings", "--plan"}, false},
		{"capture", []string{"claude/settings", "--force"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.verb, func(t *testing.T) {
			called := false
			saved := surfaceColorVerbs[tc.verb]
			if saved == nil {
				t.Fatalf("surfaceColorVerbs has no %q handler", tc.verb)
			}
			wantArgs := append([]string(nil), tc.args...)
			surfaceColorVerbs[tc.verb] = func(target configTarget, args []string, _, _ io.Writer, color bool) int {
				called = true
				if target.workspace != ws || target.notch.String() != "jail" {
					t.Errorf("handler target = workspace %q notch %q, want %q jail", target.workspace, target.notch, ws)
				}
				if !slices.Equal(args, wantArgs) {
					t.Errorf("handler argv = %q, want %q", args, wantArgs)
				}
				if color {
					t.Error("buffered surface dispatch unexpectedly requested color")
				}
				return 37
			}
			t.Cleanup(func() { surfaceColorVerbs[tc.verb] = saved })

			argv := append([]string{"surface", tc.verb}, tc.args...)
			if tc.at {
				argv = append(argv, "--setup", "podman-self")
			}
			var rc int
			out, stderr := captureBoth(t, func() { rc = dispatchNative("surface", argv) })
			if rc != 37 || !called {
				t.Fatalf("dispatchNative(surface %s) = %d, handler called=%v", tc.verb, rc, called)
			}
			if out != "" || !strings.Contains(stderr, "Surfaces: "+ws+" — jail notch") {
				t.Errorf("surface dispatch stdout=%q stderr=%q; missing target disclosure", out, stderr)
			}
		})
	}

	var helpRC int
	out, stderr := captureBoth(t, func() { helpRC = dispatchNative("surface", []string{"surface", "--help"}) })
	if helpRC != 0 || !strings.Contains(out, "Usage: yolo surface") || stderr == "" {
		t.Fatalf("surface --help rc=%d stdout=%q stderr=%q", helpRC, out, stderr)
	}
}

func TestSurfaceMissingAgentAdviceNamesRegisteredCommands(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	withWorkspaceCwd(t)
	for _, verb := range []string{"diff", "reset", "capture"} {
		t.Run(verb, func(t *testing.T) {
			var rc int
			out, stderr := captureBoth(t, func() {
				rc = dispatchNative("surface", []string{"surface", verb, "--setup", "podman-self"})
			})
			want := "yolo surface " + verb + " claude"
			if rc != 2 || out != "" || !strings.Contains(stderr, want) || strings.Contains(stderr, "yolo config "+verb) {
				t.Fatalf("registered `yolo surface %s` missing-agent advice: rc=%d stdout=%q stderr=%q; want %q", verb, rc, out, stderr, want)
			}
		})
	}
}

func TestRetiredConfigCompositionVerbsRefuseWithSurfaceReplacement(t *testing.T) {
	cwd := t.TempDir()
	t.Chdir(cwd)
	t.Setenv("HOME", t.TempDir())
	oldUpdateHook := updateHook
	updateHook = func(string, []string) (int, bool) { return 0, false }
	t.Cleanup(func() { updateHook = oldUpdateHook })
	before, err := os.ReadDir(cwd)
	if err != nil {
		t.Fatal(err)
	}
	verbs := []string{"ls", "render", "diff", "reset", "promote", "capture"}
	for _, verb := range verbs {
		t.Run(verb, func(t *testing.T) {
			called := false
			saved := surfaceColorVerbs[verb]
			if saved == nil {
				t.Fatalf("surfaceColorVerbs has no composition handler for %q", verb)
			}
			surfaceColorVerbs[verb] = func(_ configTarget, _ []string, _, _ io.Writer, _ bool) int {
				called = true
				return 0
			}
			t.Cleanup(func() { surfaceColorVerbs[verb] = saved })

			var rc int
			out, stderr := captureBoth(t, func() {
				rc = dispatchNative("config", []string{"config", verb, "claude/settings", "--setup", "seatbelt-self", "--help"})
			})
			want := "yolo config " + verb + " has moved to yolo surface " + verb + "; use `yolo surface " + verb + "` instead.\n"
			// dispatchNative writes its normal startup line first; the refusal itself must be
			// the exact final line, once, with no target disclosure or extra usage text.
			if rc != 2 || out != "" || !strings.HasSuffix(stderr, want) || strings.Count(stderr, want) != 1 {
				t.Errorf("retired config %s = rc %d, stdout %q, stderr %q; want exact replacement refusal %q",
					verb, rc, out, stderr, want)
			}
			if strings.Contains(stderr, "Surfaces:") || called {
				t.Errorf("retired config %s resolved target or invoked composition handler", verb)
			}
		})
	}
	after, err := os.ReadDir(cwd)
	if err != nil || len(after) != len(before) {
		t.Errorf("retired config commands changed the isolated cwd: before=%v after=%v err=%v", before, after, err)
	}
}

func TestConfigHelpKeepsEditingAndRuntimeCommands(t *testing.T) {
	var out, errw bytes.Buffer
	if rc := configRunW([]string{"--help"}, &out, &errw); rc != 0 || errw.Len() != 0 {
		t.Fatalf("config help rc=%d stderr=%q", rc, errw.String())
	}
	for _, want := range []string{"Usage: yolo config", "apply", "drift", "dump"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("config help lacks %q", want)
		}
	}
	for _, retired := range []string{"ls", "render", "diff", "reset", "promote", "capture"} {
		if strings.Contains(out.String(), "  "+retired+" ") {
			t.Errorf("config help still advertises retired composition verb %q", retired)
		}
	}
}
