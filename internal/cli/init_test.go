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
				p := filepath.Join(cwd, "yolo-jail.toml")
				if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
					t.Fatal(err)
				}
				return "Created yolo-jail.toml\n"
			},
			wantANSI: "\x1b[32mCreated yolo-jail.toml\x1b[0m",
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
				p := filepath.Join(cwd, "yolo-jail.toml")
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
				p := filepath.Join(filepath.Dir(paths.UserConfigPath()), paths.UserConfigTOMLName)
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
				p := filepath.Join(filepath.Dir(paths.UserConfigPath()), paths.UserConfigTOMLName)
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
					t.Setenv("GIT_CEILING_DIRECTORIES", root)
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
					if tc.command == "init" && !strings.Contains(plainStdout, filepath.Join(cwd, "yolo-jail.toml")) &&
						!strings.Contains(plainStdout, filepath.Join(cwd, "yolo-jail.jsonc")) {
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
			// The startup banner now colors stderr on a terminal — dispatch writes it
			// through the one color gate (documented in startupbanner.go) — so this guard
			// is on the ANSI-STRIPPED stderr: init's own stderr, if any, must be
			// unchanged by color. The banner's color is pinned by
			// TestStartupBannerColorsOnATerminal.
			if got := stripANSI(colorErr); got != plainErr {
				t.Errorf("color changed stderr beyond the banner's escapes:\n--- color (stripped) ---\n%s\n--- NO_COLOR ---\n%s", got, plainErr)
			}
		})
	}
}
