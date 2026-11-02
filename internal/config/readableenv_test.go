package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

func writeEnvFixture(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func envPairs(m *jsonx.OrderedMap) map[string]string {
	out := map[string]string{}
	for _, k := range m.Keys() {
		v, _ := m.Get(k)
		out[k], _ = v.(string)
	}
	return out
}

// TestReadableEnvResolvesForTheLaunch is the defect W3-O reproduced: a TOML `[env]` table
// (files, set, unset) was shown by `yolo config why` and never resolved, because the launch's
// env resolver walked `env_sources` alone. It loads the merged config the launch loads, across
// the user, project and local files, and resolves it through the resolver every launch path
// calls (ResolveEnvSourcesFull: run's profile channel, `yolo check`, `yolo host`).
func TestReadableEnvResolvesForTheLaunch(t *testing.T) {
	f := newEnvScopeFixture(t)
	userDir := filepath.Join(f.home, ".config", "yolo-jail")
	writeEnvFixture(t, filepath.Join(userDir, "user.env"), "FILE_A=user-file\nOVER=user-file\n")
	writeEnvFixture(t, filepath.Join(userDir, paths.UserConfigTOMLName),
		"[env]\nfiles = ['user.env']\nset = { OVER = 'user-set', KEEP = 'user' }\n")
	writeEnvFixture(t, filepath.Join(f.ws, "ws.env"), "FILE_B=ws-file\n")
	writeEnvFixture(t, filepath.Join(f.ws, paths.WorkspaceConfigTOMLName),
		"[env]\nfiles = ['ws.env', 'absent.env']\nset = { X = 'v' }\nunset = ['KEEP']\n")
	writeEnvFixture(t, filepath.Join(f.ws, paths.WorkspaceLocalConfigTOMLName),
		"[env]\nset = { X = 'local' }\n")

	merged, err := LoadConfig(f.ws, false, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	var warnings []string
	got, removals := ResolveEnvSourcesFull(f.ws, merged, func(m string) { warnings = append(warnings, m) })
	want := map[string]string{"FILE_A": "user-file", "OVER": "user-set", "FILE_B": "ws-file", "X": "local"}
	if !reflect.DeepEqual(envPairs(got), want) {
		t.Fatalf("resolved env = %v, want %v (merged %s)", envPairs(got), want, jsonx.MustDumpsCompact(merged))
	}
	if !reflect.DeepEqual(removals, []string{"KEEP"}) {
		t.Fatalf("removals = %v, want [KEEP]: an env.unset wins over an env.set of the same name", removals)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "dotenv file not found, skipping: ") {
		t.Fatalf("a missing env.files entry must warn: %q", warnings)
	}

	described := strings.Join(DescribeEnvSources(f.ws, merged), "\n")
	for _, part := range []string{filepath.Join(userDir, "user.env"), filepath.Join(f.ws, "ws.env"), "env.set (keys:", "env.unset (keys: KEEP)"} {
		if !strings.Contains(described, part) {
			t.Fatalf("the credential pre-flight's consulted list misses %q:\n%s", part, described)
		}
	}

	hydrated := HydrateEnvSources(f.ws, merged, nil)
	if v, present := hydrated.Get("KEEP"); !present || v != nil {
		t.Fatalf("the launch's hydrated channel must carry env.unset as a removal: %s", jsonx.MustDumpsCompact(hydrated))
	}
}

// TestMigratedEnvSourcesResolveTheSame pins the migration's promise at the level the launch
// uses: an env_sources config and its converted [env] table resolve to the same environment.
// The migrator converts a list of dotenv paths to env.files (configmigrate.lowerEnvSources);
// set and unset are the readable spellings of an inline assignment after the files and of a
// null.
func TestMigratedEnvSourcesResolveTheSame(t *testing.T) {
	for _, tc := range []struct{ name, jsonc, toml string }{
		{"files", `{"env_sources": ["a.env", "b.env"]}`, "[env]\nfiles = ['a.env', 'b.env']\n"},
		{"files then set and unset", `{"env_sources": ["a.env", "b.env", {"B": "set", "C": "new"}, {"A": null}]}`,
			"[env]\nfiles = ['a.env', 'b.env']\nset = { B = 'set', C = 'new' }\nunset = ['A']\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resolve := func(file, body string) (map[string]string, []string) {
				f := newEnvScopeFixture(t)
				writeEnvFixture(t, filepath.Join(f.ws, "a.env"), "A=a\nB=a\n")
				writeEnvFixture(t, filepath.Join(f.ws, "b.env"), "B=b\n")
				writeEnvFixture(t, filepath.Join(f.ws, file), body)
				merged, err := LoadConfig(f.ws, false, func(string) {})
				if err != nil {
					t.Fatal(err)
				}
				env, removals := ResolveEnvSourcesFull(f.ws, merged, nil)
				return envPairs(env), removals
			}
			oldEnv, oldRemovals := resolve(WorkspaceConfigName, tc.jsonc)
			newEnv, newRemovals := resolve(paths.WorkspaceConfigTOMLName, tc.toml)
			if len(oldEnv) == 0 {
				t.Fatal("the legacy fixture resolved nothing; the comparison would be vacuous")
			}
			if !reflect.DeepEqual(oldEnv, newEnv) || !reflect.DeepEqual(oldRemovals, newRemovals) {
				t.Fatalf("converted env differs:\n  env_sources: %v %v\n  [env]:       %v %v", oldEnv, oldRemovals, newEnv, newRemovals)
			}
		})
	}
}
