package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

func TestInitAlreadyExists(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "yolo-jail.jsonc")
	must(t, os.WriteFile(existing, []byte("{ existing }"), 0o644))
	var buf bytes.Buffer
	Init(dir, nil, &buf, false)
	if !strings.Contains(buf.String(), "yolo-jail.jsonc already exists.") {
		t.Errorf("expected already-exists message, got %q", buf.String())
	}
	// The existing file is NOT overwritten.
	if data, _ := os.ReadFile(existing); string(data) != "{ existing }" {
		t.Error("existing config was clobbered")
	}
}

func TestInitGitignore(t *testing.T) {
	dir := t.TempDir()
	Init(dir, nil, &bytes.Buffer{}, false)
	gi, _ := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if !strings.Contains(string(gi), ".yolo/") {
		t.Errorf(".gitignore missing .yolo/: %q", gi)
	}
	// Re-running doesn't duplicate the entry (config now exists → early return,
	// but even the append guard checks containment).
	before := string(gi)
	Init(dir, nil, &bytes.Buffer{}, false)
	after, _ := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if string(after) != before {
		t.Errorf(".gitignore changed on re-run:\n%q\n->\n%q", before, after)
	}
}

var initStatusANSI = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// TestInitStatusColorsThroughDispatch drives both scaffolders through their
// registered command handlers with a terminal stood in. Status color must be
// decided at the real print call site, and removing ANSI must leave the same
// bytes as NO_COLOR output, including errors and user-controlled paths.
func TestInitStatusColorsThroughDispatch(t *testing.T) {
	cases := []struct {
		name     string
		command  string
		setup    func(t *testing.T, cwd, home string) string
		wantRC   int
		wantANSI string
	}{
		{
			name:    "init create",
			command: "init",
			setup: func(t *testing.T, cwd, _ string) string {
				p := filepath.Join(cwd, "yolo-jail.jsonc")
				if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
					t.Fatal(err)
				}
				return "Created yolo-jail.jsonc\n"
			},
			wantANSI: "\x1b[32mCreated yolo-jail.jsonc\x1b[0m",
		},
		{
			name:    "init existing",
			command: "init",
			setup: func(t *testing.T, cwd, _ string) string {
				must(t, os.WriteFile(filepath.Join(cwd, "yolo-jail.jsonc"), []byte("{}"), 0o644))
				return "yolo-jail.jsonc already exists.\n"
			},
			wantANSI: "\x1b[33malready exists.\x1b[0m",
		},
		{
			name:    "init error",
			command: "init",
			setup: func(t *testing.T, cwd, _ string) string {
				p := filepath.Join(cwd, "yolo-jail.jsonc")
				if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
					t.Fatal(err)
				}
				must(t, os.Symlink(p, p))
				return "Error writing config: "
			},
			wantRC:   1,
			wantANSI: "\x1b[1m\x1b[31mError writing config: ",
		},
		{
			name:    "init-user-config create",
			command: "init-user-config",
			setup: func(t *testing.T, _, _ string) string {
				p := paths.UserConfigPath()
				must(t, os.MkdirAll(filepath.Dir(p), 0o755))
				if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
					t.Fatal(err)
				}
				return "Created " + p + "\n"
			},
			wantANSI: "\x1b[32mCreated ",
		},
		{
			name:    "init-user-config existing",
			command: "init-user-config",
			setup: func(t *testing.T, _, _ string) string {
				p := paths.UserConfigPath()
				must(t, os.MkdirAll(filepath.Dir(p), 0o755))
				must(t, os.WriteFile(p, []byte("{}"), 0o644))
				return p + " already exists.\n"
			},
			wantANSI: "\x1b[33malready exists.\x1b[0m",
		},
		{
			name:    "init-user-config error",
			command: "init-user-config",
			setup: func(t *testing.T, _, home string) string {
				p := filepath.Join(home, ".config")
				if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
					t.Fatal(err)
				}
				must(t, os.WriteFile(p, []byte("not a directory"), 0o644))
				return "Error creating config dir: "
			},
			wantRC:   1,
			wantANSI: "\x1b[1m\x1b[31mError creating config dir: ",
		},
		{
			name:    "init-user-config write error",
			command: "init-user-config",
			setup: func(t *testing.T, _, _ string) string {
				p := paths.UserConfigPath()
				must(t, os.MkdirAll(filepath.Dir(p), 0o755))
				if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
					t.Fatal(err)
				}
				must(t, os.Symlink(p, p))
				return "Error writing config: "
			},
			wantRC:   1,
			wantANSI: "\x1b[1m\x1b[31mError writing config: ",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var plainOut, plainErr, colorOut, colorErr string
			root := t.TempDir()
			home := filepath.Join(root, "[bold]home")
			cwd := filepath.Join(root, "[bold]workspace")
			must(t, os.MkdirAll(home, 0o755))
			must(t, os.MkdirAll(cwd, 0o755))
			for _, mode := range []struct {
				name, noColor string
			}{{name: "color", noColor: ""}, {name: "NO_COLOR", noColor: "1"}} {
				t.Run(mode.name, func(t *testing.T) {
					t.Setenv("HOME", home)
					t.Setenv("NO_COLOR", mode.noColor)
					t.Chdir(cwd)
					want := tc.setup(t, cwd, home)
					standInTerminal(t)
					previousUpdateHook := updateHook
					updateHook = func(string, []string) (int, bool) { return 0, false }
					t.Cleanup(func() { updateHook = previousUpdateHook })
					rc, stdout, stderr := captureDispatchRC(t, []string{tc.command})
					if rc != tc.wantRC {
						t.Fatalf("dispatchNative(%q) = %d, want %d\nstdout:\n%s\nstderr:\n%s",
							tc.command, rc, tc.wantRC, stdout, stderr)
					}
					plainStdout := initStatusANSI.ReplaceAllString(stdout, "")
					if !strings.Contains(plainStdout, want) {
						t.Fatalf("status %q missing from stdout after ANSI strip:\n%s", want, plainStdout)
					}
					if tc.command == "init" && !strings.Contains(plainStdout,
						filepath.Join(cwd, "yolo-jail.jsonc")) {
						t.Errorf("the user-controlled config path was altered by markup rendering:\n%s", plainStdout)
					}
					if mode.noColor == "" {
						if !strings.Contains(stdout, tc.wantANSI) {
							t.Errorf("status did not use its expected color %q:\n%s", tc.wantANSI, stdout)
						}
					} else if strings.Contains(stdout, "\x1b[") {
						t.Errorf("NO_COLOR=1 still colored the status:\n%s", stdout)
					}
					if mode.noColor == "" {
						colorOut, colorErr = stdout, stderr
					} else {
						plainOut, plainErr = stdout, stderr
					}
				})
			}
			if got := initStatusANSI.ReplaceAllString(colorOut, ""); got != plainOut {
				t.Errorf("ANSI-stripped dispatch output differs from NO_COLOR output:\n--- stripped ---\n%s\n--- plain ---\n%s",
					got, plainOut)
			}
			if colorErr != plainErr {
				t.Errorf("color changed stderr:\n--- color ---\n%s\n--- NO_COLOR ---\n%s", colorErr, plainErr)
			}
		})
	}
}
