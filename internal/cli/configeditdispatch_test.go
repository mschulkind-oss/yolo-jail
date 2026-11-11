package cli

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/configdoc"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/testconfig"
)

func TestConfigRunWDispatchesGenericEditsToExistingTargets(t *testing.T) {
	for _, tc := range []struct {
		name, project, local string
		format               configdoc.Format
		projectName          string
		localName            string
	}{
		{name: "readable TOML", format: configdoc.TOML,
			project: "[machine.resources]\ncpus = 2\n\n[env]\nunset = [\"OLD\"]\n\n[mcp_servers.tavily]\ncommand = [\"npx\"]\n",
			local:   "[machine.resources]\ncpus = 3\n", projectName: paths.WorkspaceConfigTOMLName, localName: paths.WorkspaceLocalConfigTOMLName},
		{name: "legacy JSONC", format: configdoc.JSONC,
			project: `{"machine":{"resources":{"cpus":2}},"env":{"unset":["OLD"]}}`,
			local:   `{"machine":{"resources":{"cpus":3}}}`, projectName: paths.WorkspaceConfigLegacyJSONCName, localName: paths.WorkspaceLocalConfigLegacyJSONCName},
	} {
		t.Run(tc.name, func(t *testing.T) {
			workspace := testconfig.WorkspaceFormat(t, tc.format, tc.project)
			user := testconfig.UserFormat(t, tc.format, func() string {
				if tc.format == configdoc.JSONC {
					return "{}"
				}
				return ""
			}())
			t.Setenv("HOME", user.Root)
			t.Setenv("YOLO_VERSION", "")
			localPath := filepath.Join(workspace.Root, tc.localName)
			if err := os.WriteFile(localPath, []byte(tc.local), 0o600); err != nil {
				t.Fatal(err)
			}
			withConfigEditCWD(t, workspace.Root)

			for _, tcEdit := range []struct {
				args []string
				want string
			}{
				{[]string{"set", "machine.resources.cpus", "4", "--project"}, "4"},
				{[]string{"add", "env.unset", `"NEW"`, "--project"}, "NEW"},
			} {
				var out, errw bytes.Buffer
				rc := configRunW(tcEdit.args, &out, &errw)
				assertPublicPostWriteCheck(t, rc, out.String(), errw.String())
				if !strings.Contains(out.String(), workspace.Path) || !strings.Contains(out.String(), "@@") ||
					!strings.Contains(out.String(), tcEdit.want) {
					t.Fatalf("configRunW(%q) rc=%d stdout=%q stderr=%q", tcEdit.args, rc, out.String(), errw.String())
				}
			}
			got, err := os.ReadFile(workspace.Path)
			if err != nil {
				t.Fatal(err)
			}
			if tc.format == configdoc.TOML && (!bytes.Contains(got, []byte("cpus = 4")) || !bytes.Contains(got, []byte(`unset = ["OLD", "NEW"]`))) {
				t.Fatalf("public set/add did not update Project file: %s", got)
			}
			if tc.format == configdoc.JSONC && (!bytes.Contains(got, []byte(`"cpus":4`)) || !bytes.Contains(got, []byte(`"NEW"`))) {
				t.Fatalf("public set/add did not update legacy JSONC Project file: %s", got)
			}

			var out, errw bytes.Buffer
			rc := configRunW([]string{"set", "machine.resources.cpus", "5", "--local"}, &out, &errw)
			assertPublicPostWriteCheck(t, rc, out.String(), errw.String())
			if !strings.Contains(out.String(), localPath) {
				t.Fatalf("public Local edit rc=%d stdout=%q stderr=%q", rc, out.String(), errw.String())
			}
			gotLocal, _ := os.ReadFile(localPath)
			if tc.format == configdoc.TOML && !bytes.Contains(gotLocal, []byte("cpus = 5")) {
				t.Fatalf("TOML Local file not edited: %s", gotLocal)
			}
			if tc.format == configdoc.JSONC && !bytes.Contains(gotLocal, []byte(`"cpus":5`)) {
				t.Fatalf("legacy JSONC Local file not edited: %s", gotLocal)
			}
		})
	}
}

func TestConfigRunWGenericEditDefaultsToProjectAndUnsetWrites(t *testing.T) {
	workspace := testconfig.Workspace(t, "[machine.resources]\ncpus = 2\n\n[env]\nunset = []\n\n[mcp_servers.tavily]\ncommand = [\"npx\"]\n")
	user := testconfig.User(t, "[machine.resources]\ncpus = 1\n")
	t.Setenv("HOME", user.Root)
	t.Setenv("YOLO_VERSION", "")
	localPath := filepath.Join(workspace.Root, paths.WorkspaceLocalConfigTOMLName)
	if err := os.WriteFile(localPath, []byte("[machine.resources]\ncpus = 3\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	withConfigEditCWD(t, workspace.Root)

	var out, errw bytes.Buffer
	rc := configRunW([]string{"set", "machine.resources.cpus", "4"}, &out, &errw)
	assertPublicPostWriteCheck(t, rc, out.String(), errw.String())
	if !strings.Contains(out.String(), workspace.Path) {
		t.Fatalf("automatic public edit rc=%d stdout=%q stderr=%q", rc, out.String(), errw.String())
	}
	if got, _ := os.ReadFile(user.Path); !bytes.Contains(got, []byte("cpus = 1")) {
		t.Fatalf("automatic Project edit changed the winning User file: %s", got)
	}
	if got, _ := os.ReadFile(localPath); !bytes.Contains(got, []byte("cpus = 3")) {
		t.Fatalf("automatic Project edit changed Local: %s", got)
	}

	out.Reset()
	errw.Reset()
	rc = configRunW([]string{"unset", "mcp_servers.tavily", "--project"}, &out, &errw)
	assertPublicPostWriteCheck(t, rc, out.String(), errw.String())
	if !strings.Contains(out.String(), workspace.Path) {
		t.Fatalf("public unset rc=%d stdout=%q stderr=%q", rc, out.String(), errw.String())
	}
	if got, _ := os.ReadFile(workspace.Path); !bytes.Contains(got, []byte(`tavily = "off"`)) {
		t.Fatalf("public unset did not apply the existing removal policy: %s", got)
	}
}

func TestConfigRegistryDispatchesGenericEdit(t *testing.T) {
	workspace := testconfig.Workspace(t, "[machine.resources]\ncpus = 2\n")
	user := testconfig.User(t, "")
	t.Setenv("HOME", user.Root)
	t.Setenv("YOLO_VERSION", "")
	withConfigEditCWD(t, workspace.Root)
	rc, out, errw := dispatchConfigCapture(t, []string{"set", "machine.resources.cpus", "6", "--project"})
	assertPublicPostWriteCheck(t, rc, out, errw)
	if !strings.Contains(out, workspace.Path) || !strings.Contains(out, "@@") || !strings.Contains(out, "+6") ||
		!strings.Contains(out, "host must run `yolo config apply`") || strings.Contains(out, "host must run `yolo apply`") {
		t.Fatalf("registered public config dispatch stdout=%q stderr=%q", out, errw)
	}
	if got, _ := os.ReadFile(workspace.Path); !bytes.Contains(got, []byte("cpus = 6")) {
		t.Fatalf("registered public config dispatch did not write selected target: %s", got)
	}
}

func dispatchConfigCapture(t *testing.T, args []string) (int, string, string) {
	t.Helper()
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		outR.Close()
		outW.Close()
		t.Fatal(err)
	}
	oldOut, oldErr := os.Stdout, os.Stderr
	oldUpdateHook := updateHook
	updateHook = func(string, []string) (int, bool) { return 0, false }
	os.Stdout, os.Stderr = outW, errW
	argv := append([]string{"config"}, args...)
	rc := dispatchNative("config", argv)
	outW.Close()
	errW.Close()
	os.Stdout, os.Stderr = oldOut, oldErr
	updateHook = oldUpdateHook
	out, readErr := io.ReadAll(outR)
	outR.Close()
	stderr, errReadErr := io.ReadAll(errR)
	errR.Close()
	if readErr != nil || errReadErr != nil {
		t.Fatalf("read public dispatch output: stdout=%v stderr=%v", readErr, errReadErr)
	}
	return rc, string(out), string(stderr)
}

func TestConfigRunWGenericEditPreservesQuotedLiteralMembers(t *testing.T) {
	workspace := testconfig.Workspace(t, `[mcp_servers."server.with.dots"]
command = ["dotted-old"]

[mcp_servers."*"]
command = ["star-old"]
`)
	user := testconfig.User(t, "")
	t.Setenv("HOME", user.Root)
	t.Setenv("YOLO_VERSION", "")
	withConfigEditCWD(t, workspace.Root)
	for _, tc := range []struct{ key, value, want string }{
		{`mcp_servers."server.with.dots".command`, ` ["dotted-new"]`, `"dotted-new"`},
		{`mcp_servers."*".command`, ` ["star-new"]`, `"star-new"`},
	} {
		value := strings.TrimSpace(tc.value)
		var out, errw bytes.Buffer
		rc := configRunW([]string{"set", tc.key, value, "--project"}, &out, &errw)
		assertPublicPostWriteCheck(t, rc, out.String(), errw.String())
		if !strings.Contains(out.String(), workspace.Path) {
			t.Fatalf("public quoted-member edit %s rc=%d stdout=%q stderr=%q", tc.key, rc, out.String(), errw.String())
		}
		got, err := os.ReadFile(workspace.Path)
		if err != nil || !bytes.Contains(got, []byte(tc.want)) {
			t.Fatalf("public quoted-member edit %s = %q, err=%v", tc.key, got, err)
		}
	}
}

func TestConfigRunWGenericEditValidationDryRunNoopAndHelp(t *testing.T) {
	workspace := testconfig.Workspace(t, "[machine.resources]\ncpus = 2\n")
	user := testconfig.User(t, "")
	t.Setenv("HOME", user.Root)
	t.Setenv("YOLO_VERSION", "")
	withConfigEditCWD(t, workspace.Root)
	before, _ := os.ReadFile(workspace.Path)

	for _, args := range [][]string{
		{"set", "machine.resources.cpus", "4", "--unknown"},
		{"set", "machine.resources.cpus", "true\nother = 3", "--project"},
		{"set", "machine.resources.cpus", `"bad"`, "--project"},
	} {
		var out, errw bytes.Buffer
		if rc := configRunW(args, &out, &errw); rc == 0 || out.Len() != 0 || errw.Len() == 0 {
			t.Errorf("invalid public edit %q rc=%d stdout=%q stderr=%q", args, rc, out.String(), errw.String())
		}
		if got, _ := os.ReadFile(workspace.Path); !bytes.Equal(got, before) {
			t.Fatalf("invalid public edit %q changed bytes: %q", args, got)
		}
		if strings.Contains(out.String(), "Summary") {
			t.Fatalf("invalid public edit %q unexpectedly ran the post-write check: %q", args, out.String())
		}
	}
	for _, args := range [][]string{
		{"set", "machine.resources.cpus", "4", "--project", "--dry-run"},
		{"unset", "machine.resources.cpus", "--project", "--dry-run"},
		{"set", "machine.resources.cpus", "2", "--project"},
	} {
		var out, errw bytes.Buffer
		if rc := configRunW(args, &out, &errw); rc != 0 || errw.Len() != 0 {
			t.Fatalf("non-writing public edit %q rc=%d stderr=%q", args, rc, errw.String())
		}
		if got, _ := os.ReadFile(workspace.Path); !bytes.Equal(got, before) {
			t.Fatalf("dry-run/no-op public edit %q changed bytes: %q", args, got)
		}
		if strings.Contains(out.String(), "Summary") {
			t.Fatalf("dry-run/no-op public edit %q unexpectedly ran the post-write check: %q", args, out.String())
		}
	}
	var unsetOut, unsetErr bytes.Buffer
	rc := configRunW([]string{"unset", "machine.resources.cpus", "--project"}, &unsetOut, &unsetErr)
	assertPublicPostWriteCheck(t, rc, unsetOut.String(), unsetErr.String())
	after, readErr := os.ReadFile(workspace.Path)
	if readErr != nil || bytes.Contains(after, []byte("cpus")) || !bytes.Contains(after, []byte("[machine.resources]")) {
		t.Fatalf("valid scalar unset did not remove only the authored leaf: %q, %v", after, readErr)
	}
	var noopOut, noopErr bytes.Buffer
	if rc := configRunW([]string{"unset", "machine.resources.cpus", "--project"}, &noopOut, &noopErr); rc != 0 || noopErr.Len() != 0 || strings.Contains(noopOut.String(), "Summary") {
		t.Fatalf("absent scalar unset must be a no-op without postcheck: rc=%d out=%q err=%q", rc, noopOut.String(), noopErr.String())
	}
	if again, err := os.ReadFile(workspace.Path); err != nil || !bytes.Equal(again, after) {
		t.Fatalf("absent scalar unset changed bytes: %q, %v", again, err)
	}

	for _, verb := range []string{"set", "add", "unset"} {
		for _, flag := range []string{"--help", "-h"} {
			var out, errw bytes.Buffer
			if rc := configRunW([]string{verb, flag}, &out, &errw); rc != 0 || errw.Len() != 0 ||
				!strings.Contains(out.String(), "yolo config "+verb+" <key>") {
				t.Errorf("config %s %s help rc=%d stdout=%q stderr=%q", verb, flag, rc, out.String(), errw.String())
			}
		}
	}
	var trailingHelp, trailingErr bytes.Buffer
	if rc := configRunW([]string{"set", "machine.resources.cpus", "3", "--help"}, &trailingHelp, &trailingErr); rc != 0 || trailingErr.Len() != 0 || !strings.Contains(trailingHelp.String(), "Usage:") {
		t.Errorf("trailing edit help rc=%d stdout=%q stderr=%q", rc, trailingHelp.String(), trailingErr.String())
	}
	if subcommandHelpRequested("config", []string{"config", "set", "key", "--file", "--help"}) {
		t.Error("config shared help scanner treated the --file value as a help request")
	}
	if _, ok := registry["config"]; !ok || subcommandUsage["config"].text != configUsage {
		t.Fatal("the existing config CLI registration/help text no longer advertises its production dispatcher")
	}
	if !strings.Contains(configEditUsage, "may be created when absent") ||
		!strings.Contains(configEditUsage, "--dry-run never creates it") || strings.Contains(configEditUsage, "No config file is created") {
		t.Fatalf("config edit help misstates bounded Project/Local TOML creation: %s", configEditUsage)
	}
	for _, verb := range []string{"set", "add", "unset"} {
		if !strings.Contains(configUsage, "  "+verb+" ") {
			t.Errorf("config usage does not advertise working verb %q", verb)
		}
	}
}

func TestConfigRegistrySetLegacyResourcesUsesAuthoredCoordinatesAndEffectiveValue(t *testing.T) {
	workspace := testconfig.WorkspaceFormat(t, configdoc.JSONC, `{"resources":{"cpus":2}}`)
	user := testconfig.UserFormat(t, configdoc.JSONC, `{}`)
	t.Setenv("HOME", user.Root)
	t.Setenv("YOLO_VERSION", "")
	withConfigEditCWD(t, workspace.Root)

	rc, out, errw := dispatchConfigCapture(t, []string{"set", "machine.resources.cpus", "4", "--project"})
	assertPublicPostWriteCheck(t, rc, out, errw)
	written, err := os.ReadFile(workspace.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(written, []byte(`"resources":{"cpus":4}`)) || bytes.Contains(written, []byte(`"machine"`)) {
		t.Fatalf("canonical Set did not update the existing legacy authored coordinate: %s", written)
	}
	cfg, err := config.LoadConfig(workspace.Root, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	machine, _ := cfg.Get("machine")
	machineMap, ok := machine.(*jsonx.OrderedMap)
	if !ok {
		t.Fatalf("effective machine table missing: %#v", cfg)
	}
	resources, _ := machineMap.Get("resources")
	resourcesMap, ok := resources.(*jsonx.OrderedMap)
	if !ok {
		t.Fatalf("effective machine.resources table missing: %#v", cfg)
	}
	cpus, ok := jsonx.AsInt(mustConfigEditValue(t, resourcesMap, "cpus"))
	if !ok || cpus != 4 {
		t.Fatalf("effective machine.resources.cpus = %v (integer=%t), want 4; config=%#v", cpus, ok, cfg)
	}
}

func mustConfigEditValue(t *testing.T, m *jsonx.OrderedMap, key string) any {
	t.Helper()
	if m == nil {
		t.Fatalf("missing map while reading %s", key)
	}
	value, ok := m.Get(key)
	if !ok {
		t.Fatalf("missing config key %s", key)
	}
	return value
}

func TestConfigRunWJailedUserEditRefusesBeforeTargetEffects(t *testing.T) {
	workspaceRoot := filepath.Join(t.TempDir(), "project's config")
	if err := os.MkdirAll(workspaceRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspaceRoot, paths.WorkspaceConfigTOMLName), []byte("broken = [\\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	user := testconfig.User(t, "packs = []\\n")
	t.Setenv("HOME", user.Root)
	t.Setenv("YOLO_VERSION", "test-jail")
	t.Setenv("YOLO_WORKSPACE", workspaceRoot)
	withConfigEditCWD(t, workspaceRoot)
	before, _ := os.ReadFile(user.Path)
	for _, args := range [][]string{
		{"set", "packs", `[]`, "--user", "--dry-run"},
		{"add", "packs", `"claude"`, "--user"},
		{"unset", "packs", "--user"},
	} {
		var out, errw bytes.Buffer
		if rc := configRunW(args, &out, &errw); rc == 0 || out.Len() != 0 ||
			!strings.Contains(errw.String(), "yolo host-run -C '") || !strings.Contains(errw.String(), `project'"'"'s config`) ||
			!strings.Contains(errw.String(), "-- yolo config ") {
			t.Errorf("jailed public User edit %q rc=%d stdout=%q stderr=%q", args, rc, out.String(), errw.String())
		}
		if got, _ := os.ReadFile(user.Path); !bytes.Equal(got, before) {
			t.Fatalf("jailed public User edit %q changed target bytes: %q", args, got)
		}
	}
}

func TestConfigRunWJailedBothScopeExplicitCanonicalUserFileRefusesBeforeRead(t *testing.T) {
	workspaceRoot := filepath.Join(t.TempDir(), "project's config")
	if err := os.MkdirAll(workspaceRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspaceRoot, paths.WorkspaceConfigTOMLName), []byte("[machine.resources]\\ncpus = 2\\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	user := testconfig.User(t, "[machine.resources]\\ncpus = 1\\n")
	t.Setenv("HOME", user.Root)
	canonicalUserPath := filepath.Join(filepath.Dir(paths.UserConfigPath()), paths.UserConfigTOMLName)
	if user.Path != canonicalUserPath {
		t.Fatalf("fixture %q is not the canonical User config path %q", user.Path, canonicalUserPath)
	}
	if err := os.WriteFile(user.Path, []byte("broken = [\\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(user.Path)
	t.Setenv("YOLO_VERSION", "test-jail")
	t.Setenv("YOLO_WORKSPACE", workspaceRoot)
	withConfigEditCWD(t, workspaceRoot)
	for _, args := range [][]string{
		{"set", "machine.resources.cpus", "1", "--file", user.Path},
		{"set", "machine.resources.cpus", "1", "--file", user.Path, "--dry-run"},
	} {
		var out, errw bytes.Buffer
		if rc := configRunW(args, &out, &errw); rc == 0 || out.Len() != 0 ||
			!strings.Contains(errw.String(), "user-scope config edits must run on the host") ||
			!strings.Contains(errw.String(), `project'"'"'s config`) || !strings.Contains(errw.String(), "-- yolo config set ") {
			t.Errorf("jailed Both-scope canonical User edit %q rc=%d stdout=%q stderr=%q", args, rc, out.String(), errw.String())
		}
		if got, _ := os.ReadFile(user.Path); !bytes.Equal(got, before) {
			t.Fatalf("jailed explicit canonical User edit %q changed target: %q", args, got)
		}
	}
}

func assertPublicPostWriteCheck(t *testing.T, rc int, out, errw string) {
	t.Helper()
	if !strings.Contains(out, "Config Check") || !strings.Contains(out, "Summary") {
		t.Fatalf("public write did not emit the production post-write check report: rc=%d stdout=%q stderr=%q", rc, out, errw)
	}
	if !strings.Contains(out, "machine readiness is outside config validation") || strings.Contains(out, "Container Runtime") {
		t.Fatalf("public write did not use the config-only post-write path: stdout=%q stderr=%q", out, errw)
	}
	failed := strings.Contains(errw, "post-write check did not pass")
	if (rc == 0 && failed) || (rc != 0 && !failed) {
		t.Fatalf("public write rc=%d disagrees with its post-write check result: stdout=%q stderr=%q", rc, out, errw)
	}
}

func withConfigEditCWD(t *testing.T, cwd string) {
	t.Helper()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(cwd); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(old); err != nil {
			t.Errorf("restore cwd: %v", err)
		}
	})
}
