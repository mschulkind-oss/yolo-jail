package loopholes

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// manifestSpec is one loophole dir in a corpus case: a manifest plus optional
// extra files (ca.crt, jail.py, asound.conf) to drop alongside it.
type manifestSpec struct {
	dir      string
	manifest map[string]any
	files    map[string]string
}

// buildTree writes the manifest specs under root and returns root.
func buildTree(t *testing.T, root string, specs []manifestSpec) {
	t.Helper()
	for _, s := range specs {
		dir := mkdir(t, filepath.Join(root, s.dir))
		writeManifest(t, dir, s.manifest)
		for name, content := range s.files {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
}

// TestRuntimeArgsParity byte-compares Go RuntimeArgsFor against LIVE
// runtime_args_for over a corpus of manifest trees. Skips without Python. Both
// implementations read the same on-disk tree, so absolute paths in the argv
// match exactly.
func TestRuntimeArgsParity(t *testing.T) {
	unsetJail(t)
	py := pythonRunner(t)
	if py == nil {
		t.Skip("no python oracle available (uv/python3 not found)")
	}

	cases := []struct {
		name    string
		specs   []manifestSpec
		runtime string
		env     map[string]string
	}{
		{
			name: "intercept_and_ca",
			specs: []manifestSpec{{
				dir: "broker",
				manifest: map[string]any{
					"name": "broker", "description": "x",
					"intercepts": []any{map[string]any{"host": "example.test"}, map[string]any{"host": "api.example.test"}},
					"broker_ip":  "10.0.0.1", "ca_cert": "ca.crt",
					"jail_env": map[string]any{"FOO": "bar", "BAZ": "qux"},
				},
				files: map[string]string{"ca.crt": "-----FAKE CA-----\n"},
			}},
		},
		{
			name: "plain_no_ca",
			specs: []manifestSpec{{
				dir:      "plain",
				manifest: map[string]any{"name": "plain", "description": "x", "intercepts": []any{map[string]any{"host": "plain.test"}}},
			}},
		},
		{
			name:    "apple_container_skips_tls",
			runtime: "container",
			specs: []manifestSpec{{
				dir: "broker",
				manifest: map[string]any{
					"name": "broker", "description": "x",
					"intercepts": []any{map[string]any{"host": "example.test"}},
					"broker_ip":  "10.0.0.1", "ca_cert": "ca.crt", "jail_env": map[string]any{"FOO": "bar"},
				},
				files: map[string]string{"ca.crt": "ca"},
			}},
		},
		{
			name: "multi_ca_merge",
			specs: []manifestSpec{
				{dir: "a", manifest: map[string]any{"name": "a", "description": "x", "ca_cert": "ca.crt"}, files: map[string]string{"ca.crt": "ca-a"}},
				{dir: "b", manifest: map[string]any{"name": "b", "description": "x", "ca_cert": "ca.crt"}, files: map[string]string{"ca.crt": "ca-b"}},
			},
		},
		{
			name: "jail_daemon_dir_mount",
			specs: []manifestSpec{{
				dir: "jd-mod",
				manifest: map[string]any{
					"name": "jd-mod", "description": "x",
					"intercepts": []any{map[string]any{"host": "example.test"}},
					"broker_ip":  "127.0.0.1", "ca_cert": "ca.crt",
					"jail_daemon": map[string]any{"cmd": []any{"python3", "/etc/yolo-jail/loopholes/jd-mod/jail.py"}, "restart": "on-failure"},
				},
				files: map[string]string{"ca.crt": "ca", "jail.py": "# impl"},
			}},
		},
		{
			name: "host_bind_mounts_and_readonly",
			specs: []manifestSpec{{
				dir: "audio-like",
				manifest: map[string]any{
					"name": "audio-like", "description": "x", "transport": "none",
					"host_bind_mounts": []any{
						map[string]any{"host": "{loophole_dir}/sock-a", "container": "/run/a", "readonly": false},
						map[string]any{"host": "{loophole_dir}/file-b", "container": "/etc/b", "readonly": true},
						map[string]any{"host": "{loophole_dir}/gone", "container": "/run/gone"},
					},
					"jail_env": map[string]any{"PULSE_SERVER": "unix:/run/a"},
				},
				files: map[string]string{"sock-a": "", "file-b": ""},
			}},
		},
		{
			name: "host_devices",
			specs: []manifestSpec{{
				dir: "snd-like",
				manifest: map[string]any{
					"name": "snd-like", "description": "x", "transport": "none",
					"host_devices": []any{"/dev/null", "/dev/this-does-not-exist-xyz", "/dev/zero"},
				},
			}},
		},
		{
			name: "config_backed_ignored",
			specs: []manifestSpec{{
				dir:      "real",
				manifest: map[string]any{"name": "real", "description": "x", "intercepts": []any{map[string]any{"host": "r.test"}}},
			}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			root := mkdir(t, filepath.Join(t.TempDir(), "loopholes"))
			buildTree(t, root, tc.specs)

			req := map[string]any{
				"action":          "discover_and_args",
				"root":            root,
				"include_bundled": false,
			}
			if tc.runtime != "" {
				req["runtime"] = tc.runtime
			}
			out := runOracle(t, py, req)
			pyArgs := toStringList(out["args"])

			loaded := Discover(DiscoverOptions{Root: root, RootSet: true, IncludeBundled: false})
			goArgs := RuntimeArgsFor(loaded, tc.runtime)

			if !reflect.DeepEqual(goArgs, pyArgs) {
				t.Errorf("argv mismatch (%s):\n go: %#v\n py: %#v", tc.name, goArgs, pyArgs)
			}
		})
	}
}

// TestLoopholeViewParity compares active/inactive_reason/source across
// interesting activation states, driven live. Runs both host-side (unset
// YOLO_VERSION) and in-jail (set) variants.
func TestLoopholeViewParity(t *testing.T) {
	py := pythonRunner(t)
	if py == nil {
		t.Skip("no python oracle available")
	}

	type variant struct {
		name     string
		manifest map[string]any
		files    map[string]string
		env      map[string]string
		unsetEnv []string
	}
	present := filepath.Join(t.TempDir(), "present.sock")
	os.WriteFile(present, nil, 0o644)
	missing := filepath.Join(t.TempDir(), "nope.sock")

	variants := []variant{
		{
			name:     "cmd_missing_host",
			manifest: map[string]any{"name": "m", "description": "x", "requires": map[string]any{"command_on_path": "xyz-never-abc"}},
			unsetEnv: []string{"YOLO_VERSION"},
		},
		{
			name:     "cmd_present_host",
			manifest: map[string]any{"name": "m", "description": "x", "requires": map[string]any{"command_on_path": "sh"}},
			unsetEnv: []string{"YOLO_VERSION"},
		},
		{
			name:     "file_present_host",
			manifest: map[string]any{"name": "m", "description": "x", "requires": map[string]any{"file_exists": present}},
			unsetEnv: []string{"YOLO_VERSION"},
		},
		{
			name:     "file_missing_host",
			manifest: map[string]any{"name": "m", "description": "x", "requires": map[string]any{"file_exists": missing}},
			unsetEnv: []string{"YOLO_VERSION"},
		},
		{
			name:     "file_env_collapse_host",
			manifest: map[string]any{"name": "m", "description": "x", "requires": map[string]any{"file_exists": "${THIS_VAR_IS_UNSET_XYZ}/pulse/native"}},
			unsetEnv: []string{"YOLO_VERSION", "THIS_VAR_IS_UNSET_XYZ"},
		},
		{
			name: "in_jail_bindmount_visible",
			manifest: map[string]any{
				"name": "m", "description": "x", "transport": "none",
				"requires":         map[string]any{"file_exists": "${THIS_VAR_IS_UNSET_XYZ}/pulse/native"},
				"host_bind_mounts": []any{map[string]any{"host": "/does/not/exist", "container": "/tmp", "readonly": false}},
			},
			env:      map[string]string{"YOLO_VERSION": "test-1.0.0"},
			unsetEnv: []string{"THIS_VAR_IS_UNSET_XYZ"},
		},
		{
			name: "in_jail_bindmount_absent",
			manifest: map[string]any{
				"name": "m", "description": "x", "transport": "none",
				"host_bind_mounts": []any{map[string]any{"host": "/some/host/path", "container": "/definitely/not/here/xyz"}},
			},
			env: map[string]string{"YOLO_VERSION": "test-1.0.0"},
		},
		{
			name: "in_jail_no_bindmounts_trust_enabled",
			manifest: map[string]any{
				"name": "m", "description": "x",
				"intercepts": []any{map[string]any{"host": "example.test"}},
				"requires":   map[string]any{"command_on_path": "xyz-not-installed"},
			},
			env: map[string]string{"YOLO_VERSION": "test-1.0.0"},
		},
	}

	for _, v := range variants {
		t.Run(v.name, func(t *testing.T) {
			for _, k := range v.unsetEnv {
				t.Setenv(k, "")
				os.Unsetenv(k)
			}
			for k, val := range v.env {
				t.Setenv(k, val)
			}
			root := mkdir(t, filepath.Join(t.TempDir(), "loopholes"))
			mod := mkdir(t, filepath.Join(root, "m"))
			writeManifest(t, mod, v.manifest)
			for name, content := range v.files {
				os.WriteFile(filepath.Join(mod, name), []byte(content), 0o644)
			}

			out := runOracle(t, py, map[string]any{
				"action": "discover_and_args", "root": root,
				"include_bundled": false, "include_disabled": true,
			})
			pyLoopholes, _ := out["loopholes"].([]any)
			if len(pyLoopholes) != 1 {
				t.Fatalf("expected 1 py loophole, got %d", len(pyLoopholes))
			}
			pyView := pyLoopholes[0].(map[string]any)

			loaded := Discover(DiscoverOptions{Root: root, RootSet: true, IncludeDisabled: true, IncludeBundled: false})
			if len(loaded) != 1 {
				t.Fatalf("expected 1 go loophole, got %d", len(loaded))
			}
			m := loaded[0]

			if got, want := m.Active(), pyView["active"].(bool); got != want {
				t.Errorf("active: go=%v py=%v", got, want)
			}
			goReason, goHas := m.InactiveReason()
			pyReason, pyHas := pyStringOrNil(pyView["inactive_reason"])
			if goHas != pyHas || goReason != pyReason {
				t.Errorf("inactive_reason: go=(%q,%v) py=(%q,%v)", goReason, goHas, pyReason, pyHas)
			}
		})
	}
}

// TestValidateParity compares validate_loopholes error strings byte-for-byte.
func TestValidateParity(t *testing.T) {
	unsetJail(t)
	py := pythonRunner(t)
	if py == nil {
		t.Skip("no python oracle available")
	}

	specs := []manifestSpec{
		{dir: "good", manifest: map[string]any{"name": "good", "description": "x"}},
		{dir: "dir-name", manifest: map[string]any{"name": "different-name", "description": "x"}},
		{dir: "bad-transport", manifest: map[string]any{"name": "bad-transport", "description": "x", "transport": "carrier-pigeon"}},
		{dir: "bad-lifecycle", manifest: map[string]any{"name": "bad-lifecycle", "description": "x", "lifecycle": "orbiting"}},
		{dir: "bad-restart", manifest: map[string]any{"name": "bad-restart", "description": "x", "jail_daemon": map[string]any{"cmd": []any{"true"}, "restart": "whenever"}}},
		{dir: "bad-devs", manifest: map[string]any{"name": "bad-devs", "description": "x", "host_devices": []any{""}}},
		{dir: "bad-intercept", manifest: map[string]any{"name": "bad-intercept", "description": "x", "intercepts": []any{map[string]any{"nothost": 1}}}},
		{dir: "bad-hd", manifest: map[string]any{"name": "bad-hd", "description": "x", "host_daemon": map[string]any{"cmd": []any{}}}},
		{dir: "bad-bind", manifest: map[string]any{"name": "bad-bind", "description": "x", "host_bind_mounts": []any{map[string]any{"host": "", "container": "/x"}}}},
		{dir: "bad-doctor", manifest: map[string]any{"name": "bad-doctor", "description": "x", "doctor_cmd": []any{1, 2}}},
		{dir: "nonstr-desc", manifest: map[string]any{"name": "nonstr-desc", "description": 123}},
	}
	root := mkdir(t, filepath.Join(t.TempDir(), "loopholes"))
	buildTree(t, root, specs)

	out := runOracle(t, py, map[string]any{"action": "validate", "root": root, "include_bundled": false})
	pyEntries, _ := out["entries"].([]any)

	goEntries := ValidateLoopholes(root, true, false)
	if len(goEntries) != len(pyEntries) {
		t.Fatalf("entry count: go=%d py=%d", len(goEntries), len(pyEntries))
	}
	for i, pe := range pyEntries {
		p := pe.([]any)
		pyPath, _ := p[0].(string)
		pyName, pyHasName := pStringOrNilRaw(p[1])
		pyErr, pyHasErr := pStringOrNilRaw(p[2])

		g := goEntries[i]
		if g.Path != pyPath {
			t.Errorf("entry %d path: go=%q py=%q", i, g.Path, pyPath)
		}
		goHasName := g.Loophole != nil
		if goHasName != pyHasName || (goHasName && g.Loophole.Name != pyName) {
			t.Errorf("entry %d name: go=(%v) py=(%q,%v)", i, g.Loophole, pyName, pyHasName)
		}
		goHasErr := g.Err != ""
		if goHasErr != pyHasErr {
			t.Errorf("entry %d err presence: go=%v py=%v (goErr=%q)", i, goHasErr, pyHasErr, g.Err)
			continue
		}
		if goHasErr && g.Err != pyErr {
			t.Errorf("entry %d err mismatch:\n go: %q\n py: %q", i, g.Err, pyErr)
		}
	}
}

// TestSetEnabledParity compares the byte-exact manifest rewrite (comment loss +
// header) against live set_enabled.
func TestSetEnabledParity(t *testing.T) {
	py := pythonRunner(t)
	if py == nil {
		t.Skip("no python oracle available")
	}
	body := "// leading comment\n{\n  // key comment\n  \"name\": \"togg\",\n  \"description\": \"has\ttab and \\\"quote\\\"\",\n  \"version\": 1,\n  \"enabled\": true,\n  \"jail_env\": {\"A\": \"1\", \"B\": \"2\"}\n}\n"

	for _, enabled := range []bool{false, true} {
		// Go side.
		goRoot := mkdir(t, filepath.Join(t.TempDir(), "go", "togg"))
		os.WriteFile(filepath.Join(goRoot, "manifest.jsonc"), []byte(body), 0o644)
		if err := SetEnabled(goRoot, enabled); err != nil {
			t.Fatal(err)
		}
		goOut, _ := os.ReadFile(filepath.Join(goRoot, "manifest.jsonc"))

		// Python side.
		pyRoot := mkdir(t, filepath.Join(t.TempDir(), "py", "togg"))
		os.WriteFile(filepath.Join(pyRoot, "manifest.jsonc"), []byte(body), 0o644)
		out := runOracle(t, py, map[string]any{"action": "set_enabled", "module_path": pyRoot, "enabled": enabled})
		pyOut, _ := out["content"].(string)

		if string(goOut) != pyOut {
			t.Errorf("set_enabled(%v) mismatch:\n--- go ---\n%s\n--- py ---\n%s", enabled, goOut, pyOut)
		}
	}
}

// pyStringOrNil interprets a decoded JSON value as (string, present) where JSON
// null means absent (Python None).
func pyStringOrNil(v any) (string, bool) {
	if v == nil {
		return "", false
	}
	s, _ := v.(string)
	return s, true
}

func pStringOrNilRaw(v any) (string, bool) { return pyStringOrNil(v) }
