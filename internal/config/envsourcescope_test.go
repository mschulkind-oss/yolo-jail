package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// envScopeFixture is a host (not in-jail) home, a workspace and a directory outside it,
// all symlink-resolved where they are minted so the darwin /var → /private/var class
// cannot make a comparison pass here and fail there.
type envScopeFixture struct {
	home, ws, outside string
}

func newEnvScopeFixture(t *testing.T) envScopeFixture {
	t.Helper()
	evaled := func() string {
		d, err := filepath.EvalSymlinks(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	f := envScopeFixture{home: evaled(), ws: evaled(), outside: evaled()}
	t.Setenv("HOME", f.home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(f.home, ".config"))
	t.Setenv("YOLO_VERSION", "")
	return f
}

// envScopeFormat is one spelling of the key: the legacy JSONC `env_sources` list, or the
// readable TOML `[env] files` list.
type envScopeFormat struct {
	name, key, wsFile, userFile string
	body                        func(t *testing.T, files []string, inline bool) string
}

var envScopeFormats = []envScopeFormat{
	{
		name: "jsonc", key: "config.env_sources", wsFile: WorkspaceConfigName, userFile: "config.jsonc",
		body: func(t *testing.T, files []string, inline bool) string {
			t.Helper()
			entries := []any{}
			for _, f := range files {
				entries = append(entries, f)
			}
			if inline {
				entries = append(entries, map[string]any{"INLINE": "/somewhere/else/not-a-path"})
			}
			b, err := json.Marshal(map[string]any{"env_sources": entries})
			if err != nil {
				t.Fatal(err)
			}
			return string(b)
		},
	},
	{
		name: "toml", key: "config.env.files", wsFile: WorkspaceTOMLConfigName, userFile: "config.toml",
		body: func(t *testing.T, files []string, inline bool) string {
			t.Helper()
			quoted := make([]string, len(files))
			for i, f := range files {
				quoted[i] = "'" + f + "'"
			}
			out := "[env]\nfiles = [" + strings.Join(quoted, ", ") + "]\n"
			if inline {
				out += "[env.set]\nINLINE = '/somewhere/else/not-a-path'\n"
			}
			return out
		},
	},
}

// validateWorkspace writes the workspace config and validates the config the launch would:
// LoadConfig's merged (and anchored) map, through ValidateConfig.
func (f envScopeFixture) validateWorkspace(t *testing.T, format envScopeFormat, body string) []string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(f.ws, format.wsFile), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	merged, err := LoadConfig(f.ws, false, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	errs, _ := ValidateConfig(merged, f.ws, nil)
	return errs
}

func envScopeRefusals(errs []string) []string {
	var out []string
	for _, e := range errs {
		if strings.Contains(e, "outside the workspace") && strings.Contains(e, "env files must be inside") {
			out = append(out, e)
		}
	}
	return out
}

// TestWorkspaceEnvSourcesOutsideTheWorkspaceAreRefused is OQ-AS3's ruling (2026-10-29): a
// file entry of env_sources (env.files in TOML) in a workspace config must resolve inside
// the workspace. Each escape spelling is refused, and the refusal names the entry, the
// reason and the fix.
func TestWorkspaceEnvSourcesOutsideTheWorkspaceAreRefused(t *testing.T) {
	for _, format := range envScopeFormats {
		t.Run(format.name, func(t *testing.T) {
			f := newEnvScopeFixture(t)
			secret := filepath.Join(f.outside, "prod.env")
			if err := os.WriteFile(secret, []byte("TOKEN=x\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(secret, filepath.Join(f.ws, "link.env")); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(f.outside, filepath.Join(f.ws, "linkdir")); err != nil {
				t.Fatal(err)
			}
			escape, err := filepath.Rel(f.ws, secret)
			if err != nil {
				t.Fatal(err)
			}
			later := filepath.Join(f.outside, "later.env")
			for _, tc := range []struct {
				// shown is how the refusal names the entry: verbatim, except that the loader
				// has already anchored a relative entry beside its file (AnchorEnvSources),
				// which cleans a "../" away; the location prefix still points at the line.
				name, entry, shown, wantFix string
			}{
				{"absolute path elsewhere", secret, secret, secret},
				{"absolute path that does not exist yet", later, later, later},
				{"home-relative", "~/.aws.env", "~/.aws.env", "~/.aws.env"},
				{"dot-dot escape", escape, secret, secret},
				{"symlinked file pointing out", "link.env", "link.env", secret},
				{"file under a symlinked directory pointing out", "linkdir/prod.env", "linkdir/prod.env", secret},
			} {
				t.Run(tc.name, func(t *testing.T) {
					errs := f.validateWorkspace(t, format, format.body(t, []string{tc.entry}, false))
					got := envScopeRefusals(errs)
					if len(got) != 1 {
						t.Fatalf("refusals = %v (all errors %v), want exactly one for %q", got, errs, tc.entry)
					}
					msg := got[0]
					for _, want := range []string{
						format.wsFile, format.key + ":", tc.shown, "travels with the repository",
						filepath.Join(f.home, ".config", "yolo-jail"), `"` + tc.wantFix + `"`,
					} {
						if !strings.Contains(msg, want) {
							t.Errorf("refusal %q does not contain %q", msg, want)
						}
					}
					// The fixture writes no user config, so the destination is the TOML file a
					// first write creates, and the refusal names the verb that lists the file.
					verb := "`yolo config add env.files " + shquote.Quote(strconv.Quote(tc.wantFix)) + " --user`"
					if !strings.Contains(msg, "config.toml as [env] files") || !strings.Contains(msg, verb) {
						t.Errorf("refusal does not move the entry to config.toml with %s: %s", verb, msg)
					}
				})
			}
		})
	}
}

// TestWorkspaceEnvSourcesInsideTheWorkspaceAreAccepted: the rule constrains where a file
// is, not how it is spelled, and inline entries are not paths at all.
func TestWorkspaceEnvSourcesInsideTheWorkspaceAreAccepted(t *testing.T) {
	for _, format := range envScopeFormats {
		t.Run(format.name, func(t *testing.T) {
			f := newEnvScopeFixture(t)
			if err := os.MkdirAll(filepath.Join(f.ws, "conf"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(f.ws, "conf", "dev.env"), []byte("A=1\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(filepath.Join(f.ws, "conf", "dev.env"), filepath.Join(f.ws, "inside-link.env")); err != nil {
				t.Fatal(err)
			}
			body := format.body(t, []string{
				".env",
				"conf/dev.env",
				"conf/../conf/dev.env",
				"inside-link.env",
				filepath.Join(f.ws, "conf", "dev.env"),
			}, true)
			errs := f.validateWorkspace(t, format, body)
			if got := envScopeRefusals(errs); len(got) != 0 {
				t.Fatalf("in-workspace entries were refused: %v", got)
			}
		})
	}
}

// TestUserEnvSourcesOutsideTheWorkspaceAreAccepted: the same outside path is the user's
// own to name — the rule is about the workspace scope only.
func TestUserEnvSourcesOutsideTheWorkspaceAreAccepted(t *testing.T) {
	for _, format := range envScopeFormats {
		t.Run(format.name, func(t *testing.T) {
			f := newEnvScopeFixture(t)
			secret := filepath.Join(f.outside, "prod.env")
			if err := os.WriteFile(secret, []byte("TOKEN=x\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			userDir := filepath.Join(f.home, ".config", "yolo-jail")
			if err := os.MkdirAll(userDir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(userDir, format.userFile),
				[]byte(format.body(t, []string{secret, "~/.aws.env"}, false)), 0o644); err != nil {
				t.Fatal(err)
			}
			wsBody := "{}"
			if format.name == "toml" {
				wsBody = ""
			}
			merged := envScopeMergedHasFile(t, f, format, wsBody, secret)
			errs, _ := ValidateConfig(merged, f.ws, nil)
			if got := envScopeRefusals(errs); len(got) != 0 {
				t.Fatalf("user-scope entries were refused: %v", got)
			}
		})
	}
}

// envScopeMergedHasFile writes the workspace config, loads the merged config, and fails
// unless the user entry reached it — so the acceptance above is not vacuous.
func envScopeMergedHasFile(t *testing.T, f envScopeFixture, format envScopeFormat, wsBody, want string) *jsonx.OrderedMap {
	t.Helper()
	if err := os.WriteFile(filepath.Join(f.ws, format.wsFile), []byte(wsBody), 0o644); err != nil {
		t.Fatal(err)
	}
	merged, err := LoadConfig(f.ws, false, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(jsonx.MustDumpsCompact(merged), want) {
		t.Fatalf("merged config %s does not carry the user entry %s", jsonx.MustDumpsCompact(merged), want)
	}
	return merged
}
