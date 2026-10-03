package run

// pluginhooksmodule_test.go pins the launch's jail-code line for a HOOKS MODULE: a JavaScript or
// TypeScript file a plugin's hooks file names under `modules`, which Claude Code (2.1.287 and
// later, its "mods") runs inside its own process rather than as a command it starts
// (docs/research/claude-code-mods-management.md, G1). Counted only as "hooks (1)", a mod read
// exactly like a plugin with one shell command hook.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// jailCodeLineFor writes one plugin at the root of a pack, from files, and returns what the real
// spawn boundary (startLoopholesDisclosed) printed for it, plus the plugin's directory.
func jailCodeLineFor(t *testing.T, files map[string]string) (said, dir string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	emptyLoopholeDirs(t)

	dir = filepath.Join(t.TempDir(), "first-mod")
	files[".claude-plugin/plugin.json"] = strings.TrimSpace(files[".claude-plugin/plugin.json"])
	if files[".claude-plugin/plugin.json"] == "" {
		files[".claude-plugin/plugin.json"] = `{"name":"first-mod"}`
	}
	files["pack.json"] = `{"name":"first-mod","skills_tier":"namespaced"}`
	for rel, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	p, probs := packload.LoadDir(dir, "first-mod")
	if len(probs) > 0 {
		t.Fatalf("the mod pack fixture does not load: %v", probs)
	}
	cname := "yolo-hooksmodule-" + strings.ReplaceAll(t.Name(), "/", "-")
	t.Cleanup(func() { _ = os.RemoveAll(hostServiceSocketsDir(cname, false)) })
	var errBuf bytes.Buffer
	o := &Options{}
	fillDefaults(o)
	o.Stderr = &errBuf
	o.Stdout = discardBuf()
	o.PathExists = func(string) bool { return false }
	o.startLoopholesDisclosed(cname, "podman", newConfig(), []*packload.Pack{p}, nil)
	return errBuf.String(), dir
}

const hooksModuleJS = "export function register(on) {\n  on(\"session.start\", ($) => {\n" +
	"    $.ui.status(\"loaded\");\n  });\n}\n"

// A mod's line says its hooks include a hooks module, that Claude Code runs it in its own
// process, and the one command that lists what it calls — for each form Claude Code 2.1.288's
// `claude plugin validate` reads a module from: the default hooks/hooks.json, and a hooks file
// the manifest's `hooks` names by path, as a string or in an array.
func TestTheLaunchNamesAHooksModuleAndHowToSeeWhatItCalls(t *testing.T) {
	for _, tc := range []struct {
		name  string
		files map[string]string
	}{
		{"the default hooks/hooks.json", map[string]string{
			"hooks/hooks.json": `{"hooks":{"PostToolUse":[{"matcher":"Write","hooks":[` +
				`{"type":"command","command":"echo written"}]}]},"modules":["./register.js"]}`,
			"hooks/register.js": hooksModuleJS,
		}},
		{"a hooks file the manifest names", map[string]string{
			".claude-plugin/plugin.json": `{"name":"first-mod","hooks":"./hooks/extra.json"}`,
			"hooks/extra.json":           `{"modules":["./register.js"]}`,
			"hooks/register.js":          hooksModuleJS,
		}},
		{"a hooks file the manifest lists", map[string]string{
			".claude-plugin/plugin.json": `{"name":"first-mod","hooks":["./hooks/extra.json"]}`,
			"hooks/extra.json":           `{"modules":["./register.js"]}`,
			"hooks/register.js":          hooksModuleJS,
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			said, dir := jailCodeLineFor(t, tc.files)
			for _, want := range []string{
				"first-mod: 1 wrapped plugin runs code in the jail — hooks (1)",
				"the hooks include a hooks module",
				"JavaScript or TypeScript that Claude Code runs inside its own process",
				"`claude plugin validate " + dir + "` lists what it calls",
			} {
				if !strings.Contains(said, want) {
					t.Errorf("the jail-code line for a mod does not say %q:\n%s", want, said)
				}
			}
		})
	}
}

// Hooks with no module keep the counted line alone: a command hook, a manifest's inline
// `modules` (which Claude Code 2.1.288 ignores as "unknown hook event"), and an empty `modules`
// (which `claude plugin validate` rejects) load no module.
func TestTheLaunchNamesNoHooksModuleWhereNoneLoads(t *testing.T) {
	for _, tc := range []struct {
		name  string
		files map[string]string
	}{
		{"a command hook", map[string]string{
			"hooks/hooks.json": `{"hooks":{"PostToolUse":[{"matcher":"Write","hooks":[` +
				`{"type":"command","command":"echo written"}]}]}}`,
		}},
		{"inline modules in the manifest", map[string]string{
			".claude-plugin/plugin.json": `{"name":"first-mod","hooks":{"modules":["./hooks/register.js"]}}`,
			"hooks/register.js":          hooksModuleJS,
		}},
		{"an empty modules list", map[string]string{
			"hooks/hooks.json": `{"modules":[]}`,
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			said, _ := jailCodeLineFor(t, tc.files)
			if !strings.Contains(said, "first-mod: 1 wrapped plugin runs code in the jail — hooks (1)") {
				t.Fatalf("the hooks were not counted:\n%s", said)
			}
			if strings.Contains(said, "hooks module") {
				t.Errorf("the line names a hooks module for hooks that load none:\n%s", said)
			}
		})
	}
}

// Two plugins of one pack carrying a module share the pack's one line, with one command each.
func TestTheLaunchNamesEachPluginsHooksModuleOnThePacksOneLine(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	emptyLoopholeDirs(t)
	root := filepath.Join(t.TempDir(), "mods")
	var dirs []string
	for _, name := range []string{"a-mod", "b-mod"} {
		dir := filepath.Join(root, "skills", name)
		dirs = append(dirs, dir)
		for rel, body := range map[string]string{
			".claude-plugin/plugin.json": `{"name":"` + name + `"}`,
			"hooks/hooks.json":           `{"modules":["./register.js"]}`,
			"hooks/register.js":          hooksModuleJS,
		} {
			p := filepath.Join(dir, filepath.FromSlash(rel))
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	p, probs := packload.LoadDir(root, "mods")
	if len(probs) > 0 {
		t.Fatalf("the pack fixture does not load: %v", probs)
	}
	cname := "yolo-hooksmodule-" + t.Name()
	t.Cleanup(func() { _ = os.RemoveAll(hostServiceSocketsDir(cname, false)) })
	var errBuf bytes.Buffer
	o := &Options{}
	fillDefaults(o)
	o.Stderr = &errBuf
	o.Stdout = discardBuf()
	o.PathExists = func(string) bool { return false }
	o.startLoopholesDisclosed(cname, "podman", newConfig(), []*packload.Pack{p}, nil)

	said := errBuf.String()
	for _, want := range []string{
		"mods: 2 wrapped plugins run code in the jail — hooks (2); the hooks of 2 plugins include a hooks module",
		"`claude plugin validate " + dirs[0] + "`, `claude plugin validate " + dirs[1] + "` list what each calls",
	} {
		if !strings.Contains(said, want) {
			t.Errorf("the pack's line does not say %q:\n%s", want, said)
		}
	}
}
