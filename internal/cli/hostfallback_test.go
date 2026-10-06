package cli

// hostfallback_test.go pins an UNMODIFIED EXTENSION's FALLBACK at the host notch
// (docs/design/pi-extension-store-builds.md §4.3, XB-D7): where the host renders no tree — a macOS
// host, which builds none, or a Linux host with no good build — `yolo host apply` writes the author's
// raw entry into the agent's list in the tree's place and says so, the owning agent is never stopped
// for it, and once a good build serves the tree's own entry is written again.

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const hostFallback = "git:example.com/tool-ext"

// fallbackHostFixture is the tree fixture with its extension unmodified, a fallback declared, and
// an agent pack owning the list entry that names it.
func fallbackHostFixture(t *testing.T) *treeFixture {
	t.Helper()
	fx := newTreeFixture(t, "")
	writeFile(t, filepath.Join(fx.treeDir, "pack.json"), `{"name":"treepack","contributes":[{"kind":"files",`+
		`"into":".tool/ext/tool-ext","source":"git+file://`+fx.repo+`?ref=main","fallback":"`+hostFallback+`"},`+
		`{"kind":"config-list","surface":"tool/settings","path":"/packages","add":["npm:other","~/.tool/ext/tool-ext"]}]}`)
	agent := filepath.Join(fx.packs, "agentpack")
	writeFile(t, filepath.Join(agent, "pack.json"), `{"name":"agentpack","contributes":[`+
		`{"kind":"program","bin":"tool","via":"npm","package":"tool"},`+
		`{"kind":"config","config":[{"agent":"tool","name":"settings","codec":"json","path":"~/.tool/settings.json"}]}]}`)
	fx.writeHostConfig(t, treeHostOwn)
	// The agent's program is on PATH, so the apply's dependency gate has nothing to stop.
	stubBins(t, "tool")
	return fx
}

func (fx *treeFixture) hostPackages(t *testing.T) []any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(fx.home, ".tool", "settings.json"))
	if err != nil {
		t.Fatalf("the agent's settings were not rendered: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	got, _ := m["packages"].([]any)
	return got
}

func TestTheHostApplyWritesTheFallbackWhereItRendersNoTree(t *testing.T) {
	for _, macOS := range []bool{true, false} {
		name := "a Linux host with no good build"
		if macOS {
			name = "a macOS host"
		}
		t.Run(name, func(t *testing.T) {
			fx := fallbackHostFixture(t)
			if macOS {
				prev := hostTreesBuild
				hostTreesBuild = func() bool { return false }
				t.Cleanup(func() { hostTreesBuild = prev })
			}
			// The dry run previews it, and builds nothing.
			var errw bytes.Buffer
			hostApply(nil, &errw, &errw, false, nil)
			if !strings.Contains(errw.String(), "the agent installs "+hostFallback+" itself") {
				t.Errorf("the dry run does not say the fallback is taken:\n%s", errw.String())
			}
			if !macOS {
				// A build the admit refuses (a path outside the tree): nothing serves.
				fx.stray = true
			}
			errw.Reset()
			hostApply([]string{"--assert"}, &errw, &errw, false, strings.NewReader(""))
			if got := fx.hostPackages(t); len(got) != 2 || got[0] != "npm:other" || got[1] != hostFallback {
				t.Errorf("the agent's list = %v, want the fallback in the tree's place:\n%s", got, errw.String())
			}
			if _, err := os.Lstat(fx.link()); err == nil {
				t.Error("a link was rendered with no tree to name")
			}
			var gate bytes.Buffer
			if !hostTreeGate(&gate, "tool", fx.home) {
				t.Errorf("the owner was stopped for an extension with a fallback:\n%s", gate.String())
			}
		})
	}
}

// ONCE A GOOD BUILD SERVES at a Linux host, the apply links it and writes the tree's own entry.
func TestTheHostApplyWritesTheTreesEntryOnceItsBuildServes(t *testing.T) {
	fx := fallbackHostFixture(t)
	var errw bytes.Buffer
	hostApply([]string{"--assert"}, io.Discard, &errw, false, strings.NewReader(""))
	if got := fx.hostPackages(t); len(got) != 2 || got[1] != "~/.tool/ext/tool-ext" {
		t.Errorf("the agent's list = %v, want the tree's entry:\n%s", got, errw.String())
	}
	if target, err := os.Readlink(fx.link()); err != nil || !isDir(target) {
		t.Errorf("the tree was not linked (%q, %v)", target, err)
	}
}

// A MACOS HOST LAUNCH of the owning agent says it installs the fallback itself, once, and starts.
func TestAMacOSHostLaunchSaysTheAgentInstallsTheFallback(t *testing.T) {
	fallbackHostFixture(t)
	prev := hostTreesBuild
	hostTreesBuild = func() bool { return false }
	t.Cleanup(func() { hostTreesBuild = prev })
	got := captureHostExec(t)
	var errw bytes.Buffer
	if rc := hostExec(nil, []string{"tool"}, io.Discard, &errw, nil); rc != 0 || !got.execed {
		t.Fatalf("rc=%d, execed %v\n%s", rc, got.execed, errw.String())
	}
	if n := strings.Count(errw.String(), "tool installs "+hostFallback+" itself"); n != 1 {
		t.Errorf("the macOS launch names the fallback %d times, want once:\n%s", n, errw.String())
	}
	if strings.Contains(errw.String(), "starts without it") {
		t.Errorf("the macOS launch says the agent starts without an extension it installs itself:\n%s", errw.String())
	}
}

// THE `yolo config render` HOST PREVIEW takes the fallbacks the apply would (XB-D38), so the
// preview is the write's bytes: with no good build at a Linux host, the agent's list shows the raw
// entry in the tree's place. Red if configRenderHost stops calling hostTreeFallbacks.
func TestTheHostRenderPreviewShowsTheFallbackWithNoGoodBuild(t *testing.T) {
	fallbackHostFixture(t)
	var out, errw bytes.Buffer
	if rc := configRenderHost("tool", "settings", false, &out, &errw, false); rc != 0 {
		t.Fatalf("rc=%d\n%s\n%s", rc, out.String(), errw.String())
	}
	if !strings.Contains(out.String(), `"`+hostFallback+`"`) || strings.Contains(out.String(), `"~/.tool/ext/tool-ext"`) {
		t.Errorf("the preview does not show the fallback in the tree's place:\n%s\n%s", out.String(), errw.String())
	}
}

// A LINUX HOST LAUNCH of the owning agent with no good build says, once, that the agent installs
// the fallback itself, and why there is no tree, and starts. Red if noteHostTreeLines stops saying
// the fallback at a host that builds trees.
func TestALinuxHostLaunchWithNoGoodBuildSaysTheAgentInstallsTheFallback(t *testing.T) {
	fx := fallbackHostFixture(t)
	fx.stray = true // the build leaves a path outside its tree, so the admit refuses it: nothing serves
	got := captureHostExec(t)
	var errw bytes.Buffer
	if rc := hostExec(nil, []string{"tool"}, io.Discard, &errw, nil); rc != 0 || !got.execed {
		t.Fatalf("rc=%d, execed %v\n%s", rc, got.execed, errw.String())
	}
	want := "yolo host: extension treepack/tool-ext: no tree at the host — "
	tail := "; the agent installs " + hostFallback + " itself, from its raw entry"
	if n := strings.Count(errw.String(), want); n != 1 || !strings.Contains(errw.String(), tail) {
		t.Errorf("the Linux host launch names the fallback %d times, want once (%q … %q):\n%s", n, want, tail, errw.String())
	}
}
