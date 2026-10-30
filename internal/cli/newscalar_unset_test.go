package cli

import (
	"bytes"
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

func TestConfigRegistryUnsetScalarRemovesNamedLocalValueInBothFormats(t *testing.T) {
	for _, tc := range []struct {
		name, key, userText, projectText, localText string
		format                                      configdoc.Format
		localName                                   string
		wantLower                                   any
		wantHunk                                    string
	}{
		{
			name: "TOML integer", key: "machine.resources.cpus", format: configdoc.TOML,
			localName: paths.WorkspaceLocalConfigTOMLName,
			userText:  "[machine.resources]\ncpus = 1\n", projectText: "[machine.resources]\ncpus = 2\n", localText: "[machine.resources]\ncpus = 3\n",
			wantLower: int64(2), wantHunk: "-cpus = 3",
		},
		{
			name: "JSONC canonical integer", key: "machine.resources.cpus", format: configdoc.JSONC,
			localName: paths.WorkspaceLocalConfigLegacyJSONCName,
			userText:  `{"machine":{"resources":{"cpus":1}}}`, projectText: `{"machine":{"resources":{"cpus":2}}}`, localText: `{"machine":{"resources":{"cpus":3}}}`,
			wantLower: int64(2), wantHunk: `-"cpus":3`,
		},
		{
			name: "JSONC legacy string", key: "machine.resources.cpus", format: configdoc.JSONC,
			localName: paths.WorkspaceLocalConfigLegacyJSONCName,
			userText:  `{"resources":{"cpus":"1"}}`, projectText: `{"resources":{"cpus":"2"}}`, localText: `{"resources":{"cpus":3}}`,
			wantLower: "2", wantHunk: `-"cpus":3`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			workspace := testconfig.WorkspaceFormat(t, tc.format, tc.projectText)
			user := testconfig.UserFormat(t, tc.format, tc.userText)
			t.Setenv("HOME", user.Root)
			t.Setenv("YOLO_VERSION", "")
			localPath := filepath.Join(workspace.Root, tc.localName)
			if err := os.WriteFile(localPath, []byte(tc.localText), 0o600); err != nil {
				t.Fatal(err)
			}
			withConfigEditCWD(t, workspace.Root)

			beforeUser, _ := os.ReadFile(user.Path)
			beforeProject, _ := os.ReadFile(workspace.Path)
			beforeStat, err := os.Stat(localPath)
			if err != nil {
				t.Fatal(err)
			}
			rc, out, errw := dispatchConfigCapture(t, []string{"unset", tc.key, "--local"})
			if !strings.Contains(out, localPath) || !strings.Contains(out, tc.wantHunk) {
				t.Fatalf("registered unset rc=%d stdout=%q stderr=%q", rc, out, errw)
			}
			assertPublicPostWriteCheck(t, rc, out, errw)
			gotLocal, err := os.ReadFile(localPath)
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(gotLocal, []byte("cpus")) {
				t.Fatalf("named Local scalar remains after unset: %s", gotLocal)
			}
			if after, _ := os.ReadFile(user.Path); !bytes.Equal(after, beforeUser) {
				t.Fatalf("unset changed User bytes: before=%q after=%q", beforeUser, after)
			}
			if after, _ := os.ReadFile(workspace.Path); !bytes.Equal(after, beforeProject) {
				t.Fatalf("Local unset changed Project bytes: before=%q after=%q", beforeProject, after)
			}
			afterStat, err := os.Stat(localPath)
			if err != nil || afterStat.Mode().Perm() != beforeStat.Mode().Perm() {
				t.Fatalf("unset changed Local mode: before=%v after=%v err=%v", beforeStat.Mode(), afterStat.Mode(), err)
			}
			effective, err := config.LoadReadableConfig(workspace.Root, true, nil)
			if err != nil {
				t.Fatal(err)
			}
			got := readableValue(t, effective, "machine", "resources", "cpus")
			if want, ok := tc.wantLower.(string); ok {
				if got != want {
					t.Fatalf("effective lower-layer cpus = %#v (%T), want string %q", got, got, want)
				}
			} else if gotInt, ok := jsonx.AsInt(got); !ok || gotInt != 2 {
				t.Fatalf("effective lower-layer cpus = %#v (%T), want integer 2", got, got)
			}
		})
	}
}

func TestConfigRegistryUnsetScalarUsesMostSpecificOriginalSchemaRule(t *testing.T) {
	for _, dryRun := range []bool{false, true} {
		t.Run(map[bool]string{false: "write", true: "dry-run"}[dryRun], func(t *testing.T) {
			workspace := testconfig.Workspace(t, "[machine.resources.io]\npriority = 'bogus'\n")
			user := testconfig.User(t, "")
			t.Setenv("HOME", user.Root)
			t.Setenv("YOLO_VERSION", "")
			localPath := filepath.Join(workspace.Root, paths.WorkspaceLocalConfigTOMLName)
			localBefore := []byte("[machine.resources.io]\npriority = 'low'\n")
			if err := os.WriteFile(localPath, localBefore, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(workspace.Path, 0o640); err != nil {
				t.Fatal(err)
			}
			before := mustReadScalarUnset(t, workspace.Path)
			beforeInfo, err := os.Stat(workspace.Path)
			if err != nil {
				t.Fatal(err)
			}
			withConfigEditCWD(t, workspace.Root)
			args := []string{"unset", "machine.resources.io.priority", "--project"}
			if dryRun {
				args = append(args, "--dry-run")
			}
			rc, out, errw := dispatchConfigCapture(t, args)
			if rc == 0 || out != "" || !strings.Contains(errw, "priority") || strings.Contains(errw, "Summary") {
				t.Fatalf("masked invalid exact-string removal %q rc=%d stdout=%q stderr=%q", args, rc, out, errw)
			}
			if after := mustReadScalarUnset(t, workspace.Path); !bytes.Equal(after, before) {
				t.Fatalf("refusal changed Project bytes: before=%q after=%q", before, after)
			}
			afterInfo, err := os.Stat(workspace.Path)
			if err != nil || afterInfo.Mode().Perm() != beforeInfo.Mode().Perm() {
				t.Fatalf("refusal changed Project mode: before=%v after=%v err=%v", beforeInfo.Mode(), afterInfo.Mode(), err)
			}
			if after := mustReadScalarUnset(t, localPath); !bytes.Equal(after, localBefore) {
				t.Fatalf("refusal changed Local bytes: %q", after)
			}
		})
	}
}

func TestConfigRegistryUnsetScalarUsesCanonicalSchemaForLegacyAuthoredMiseValue(t *testing.T) {
	for _, tc := range []struct {
		name, project, user string
		format              configdoc.Format
		projectPath         []configdoc.Step
	}{
		{
			name: "TOML canonical", format: configdoc.TOML,
			project:     "[install.mise]\npython = '3.12'\nnode = '20'\n",
			user:        "[install.mise]\npython = '3.11'\n",
			projectPath: []configdoc.Step{{Key: "install"}, {Key: "mise"}, {Key: "python"}},
		},
		{
			name: "JSONC canonical", format: configdoc.JSONC,
			project:     `{"install":{"mise":{"python":"3.12","node":"20"}}}`,
			user:        `{"install":{"mise":{"python":"3.11"}}}`,
			projectPath: []configdoc.Step{{Key: "install"}, {Key: "mise"}, {Key: "python"}},
		},
		{
			name: "JSONC legacy-authored", format: configdoc.JSONC,
			project:     `{"mise_tools":{"python":"3.12","node":"20"}}`,
			user:        `{"mise_tools":{"python":"3.11"}}`,
			projectPath: []configdoc.Step{{Key: "mise_tools"}, {Key: "python"}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			workspace := testconfig.WorkspaceFormat(t, tc.format, tc.project)
			user := testconfig.UserFormat(t, tc.format, tc.user)
			t.Setenv("HOME", user.Root)
			t.Setenv("YOLO_VERSION", "")
			withConfigEditCWD(t, workspace.Root)
			before := mustReadScalarUnset(t, workspace.Path)
			rc, out, errw := dispatchConfigCapture(t, []string{"unset", "install.mise.python", "--project"})
			if !strings.Contains(out, workspace.Path) || !strings.Contains(out, "python") {
				t.Fatalf("registered canonical mise unset rc=%d stdout=%q stderr=%q", rc, out, errw)
			}
			assertPublicPostWriteCheck(t, rc, out, errw)
			after := mustReadScalarUnset(t, workspace.Path)
			if bytes.Equal(after, before) || bytes.Contains(after, []byte("python")) || !bytes.Contains(after, []byte("node")) {
				t.Fatalf("canonical mise unset did not remove only Project version: before=%q after=%q", before, after)
			}
			doc, err := configdoc.Parse(after, tc.format)
			if err != nil {
				t.Fatal(err)
			}
			if _, found, err := doc.Index.Value(tc.projectPath...); err != nil || found {
				t.Fatalf("Project version remains at authored path: found=%v err=%v", found, err)
			}
			effective, err := config.LoadReadableConfig(workspace.Root, true, nil)
			if err != nil {
				t.Fatal(err)
			}
			if got := readableValue(t, effective, "install", "mise", "python"); got != "3.11" {
				t.Fatalf("effective lower-layer Python version = %#v (%T), want string 3.11", got, got)
			}
		})
	}
}

func TestConfigRegistryUnsetScalarPrevalidatesMaskedLegacyMiseVersion(t *testing.T) {
	for _, dryRun := range []bool{false, true} {
		t.Run(map[bool]string{false: "write", true: "dry-run"}[dryRun], func(t *testing.T) {
			workspace := testconfig.WorkspaceFormat(t, configdoc.JSONC, `{"mise_tools":{"python":312}}`)
			user := testconfig.UserFormat(t, configdoc.JSONC, `{"mise_tools":{"python":"3.11"}}`)
			t.Setenv("HOME", user.Root)
			t.Setenv("YOLO_VERSION", "")
			localPath := filepath.Join(workspace.Root, paths.WorkspaceLocalConfigLegacyJSONCName)
			localBefore := []byte(`{"mise_tools":{"python":"3.13"}}`)
			if err := os.WriteFile(localPath, localBefore, 0o600); err != nil {
				t.Fatal(err)
			}
			before := mustReadScalarUnset(t, workspace.Path)
			beforeInfo, err := os.Stat(workspace.Path)
			if err != nil {
				t.Fatal(err)
			}
			withConfigEditCWD(t, workspace.Root)
			args := []string{"unset", "install.mise.python", "--project"}
			if dryRun {
				args = append(args, "--dry-run")
			}
			rc, out, errw := dispatchConfigCapture(t, args)
			if rc == 0 || out != "" || !strings.Contains(errw, "python") || strings.Contains(errw, "Summary") {
				t.Fatalf("masked invalid legacy mise removal %q rc=%d stdout=%q stderr=%q", args, rc, out, errw)
			}
			if after := mustReadScalarUnset(t, workspace.Path); !bytes.Equal(after, before) {
				t.Fatalf("refusal changed Project bytes: before=%q after=%q", before, after)
			}
			afterInfo, err := os.Stat(workspace.Path)
			if err != nil || afterInfo.Mode().Perm() != beforeInfo.Mode().Perm() {
				t.Fatalf("refusal changed Project mode: before=%v after=%v err=%v", beforeInfo.Mode(), afterInfo.Mode(), err)
			}
			if after := mustReadScalarUnset(t, localPath); !bytes.Equal(after, localBefore) {
				t.Fatalf("refusal changed Local bytes: %q", after)
			}
		})
	}
}

func TestConfigRegistryUnsetScalarExposesLowerLayerAndRemovesProjectBytesUnderLocalWinner(t *testing.T) {
	for _, tc := range []struct {
		name, project, user, local, localName, key      string
		format                                          configdoc.Format
		oldPath                                         []configdoc.Step
		wantProjectComment, wantValueComment, wantOther string
		wantRemoved                                     string
	}{
		{
			name: "TOML", format: configdoc.TOML,
			project: "# project-only comment survives\n[machine.resources]\ncpus = 2 # value comment stays unattached\n\n[env]\nset = { PAGER = 'project' }\n",
			user:    "[machine.resources]\ncpus = 1\n", local: "[machine.resources]\ncpus = 3\n",
			localName: paths.WorkspaceLocalConfigTOMLName, key: "machine.resources.cpus",
			oldPath:            []configdoc.Step{{Key: "machine"}, {Key: "resources"}, {Key: "cpus"}},
			wantProjectComment: "# project-only comment survives", wantValueComment: "# value comment stays unattached",
			wantOther: "set = { PAGER = 'project' }", wantRemoved: "cpus = 2",
		},
		{
			name: "legacy JSONC", format: configdoc.JSONC,
			project: "{\n  // project-only comment survives\n  \"resources\": {\n    \"cpus\": 2 // value comment stays unattached\n  },\n  \"env\": {\"set\": {\"PAGER\": \"project\"}}\n}\n",
			user:    `{"resources":{"cpus":1}}`, local: `{"resources":{"cpus":3}}`,
			localName: paths.WorkspaceLocalConfigLegacyJSONCName, key: "machine.resources.cpus",
			oldPath:            []configdoc.Step{{Key: "resources"}, {Key: "cpus"}},
			wantProjectComment: "// project-only comment survives", wantValueComment: "// value comment stays unattached",
			wantOther: `"PAGER": "project"`, wantRemoved: `"cpus": 2`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			workspace := testconfig.WorkspaceFormat(t, tc.format, tc.project)
			user := testconfig.UserFormat(t, tc.format, tc.user)
			t.Setenv("HOME", user.Root)
			t.Setenv("YOLO_VERSION", "")
			localPath := filepath.Join(workspace.Root, tc.localName)
			if err := os.WriteFile(localPath, []byte(tc.local), 0o600); err != nil {
				t.Fatal(err)
			}
			withConfigEditCWD(t, workspace.Root)
			beforeProject, _ := os.ReadFile(workspace.Path)
			beforeUser, _ := os.ReadFile(user.Path)
			beforeLocal, _ := os.ReadFile(localPath)

			rc, out, errw := dispatchConfigCapture(t, []string{"unset", tc.key})
			if !strings.Contains(out, workspace.Path) {
				t.Fatalf("registered scalar unset rc=%d stdout=%q stderr=%q", rc, out, errw)
			}
			assertPublicPostWriteCheck(t, rc, out, errw)
			afterProject := mustReadScalarUnset(t, workspace.Path)
			if bytes.Contains(afterProject, []byte(tc.wantRemoved)) || !bytes.Contains(afterProject, []byte(tc.wantProjectComment)) ||
				!bytes.Contains(afterProject, []byte(tc.wantValueComment)) || !bytes.Contains(afterProject, []byte(tc.wantOther)) {
				t.Fatalf("unset did not remove only the Project scalar bytes: %s", afterProject)
			}
			if bytes.Equal(beforeProject, afterProject) || !bytes.Equal(mustReadScalarUnset(t, user.Path), beforeUser) ||
				!bytes.Equal(mustReadScalarUnset(t, localPath), beforeLocal) {
				t.Fatalf("unset changed wrong source: project=%s user=%s local=%s", afterProject, mustReadScalarUnset(t, user.Path), mustReadScalarUnset(t, localPath))
			}
			doc, err := configdoc.Parse(afterProject, tc.format)
			if err != nil {
				t.Fatal(err)
			}
			if _, found, err := doc.Index.Value(tc.oldPath...); err != nil || found {
				t.Fatalf("independent document parser found removed Project scalar: found=%v err=%v", found, err)
			}
			effective, err := config.LoadReadableConfig(workspace.Root, true, nil)
			if err != nil {
				t.Fatal(err)
			}
			got := readableValue(t, effective, "machine", "resources", "cpus")
			if n, ok := jsonx.AsInt(got); !ok || n != 3 {
				t.Fatalf("Local winner after Project unset = %#v (%T), want integer 3", got, got)
			}
		})
	}
}

func TestConfigRegistryUnsetScalarDryRunInvalidInputAbsentAndNonScalarPreserveFiles(t *testing.T) {
	workspace := testconfig.Workspace(t, "[machine.resources]\ncpus = 2\nmemory = '8g'\n\n[machine.gpu]\nenabled = true\n")
	user := testconfig.User(t, "[machine.resources]\ncpus = 1\nmemory = '16g'\n")
	t.Setenv("HOME", user.Root)
	t.Setenv("YOLO_VERSION", "")
	localPath := filepath.Join(workspace.Root, paths.WorkspaceLocalConfigTOMLName)
	if err := os.WriteFile(localPath, []byte("[machine.resources]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	withConfigEditCWD(t, workspace.Root)
	before, err := os.ReadFile(workspace.Path)
	if err != nil {
		t.Fatal(err)
	}
	{
		args := []string{"unset", "machine.resources.cpus", "--project", "--dry-run"}
		var out, errw bytes.Buffer
		rc, output, errOutput := dispatchConfigCapture(t, args)
		out.WriteString(output)
		errw.WriteString(errOutput)
		if rc != 0 || strings.Contains(errw.String(), "config edit refused") || strings.Contains(out.String(), "Summary") ||
			!strings.Contains(out.String(), "Dry run; nothing was written.") ||
			!strings.Contains(out.String(), "cpus = 2") {
			t.Fatalf("scalar dry-run rc=%d stdout=%q stderr=%q", rc, out.String(), errw.String())
		}
		if got := mustReadScalarUnset(t, workspace.Path); !bytes.Equal(got, before) {
			t.Fatalf("dry-run changed bytes: before=%q after=%q", before, got)
		}
	}

	for _, args := range [][]string{
		{"unset", "unknown.key", "--project"},
		{"unset", "machine.gpu", "--project"},
	} {
		var out, errw bytes.Buffer
		rc, output, errOutput := dispatchConfigCapture(t, args)
		out.WriteString(output)
		errw.WriteString(errOutput)
		if rc == 0 || out.Len() != 0 || errw.Len() == 0 {
			t.Errorf("invalid/non-scalar unset %q rc=%d stdout=%q stderr=%q", args, rc, out.String(), errw.String())
		}
		if args[1] == "machine.gpu" && (!strings.Contains(errw.String(), workspace.Path) ||
			!strings.Contains(errw.String(), "machine.gpu") || !strings.Contains(errw.String(), "edit the value manually") ||
			!strings.Contains(errw.String(), "yolo check")) {
			t.Errorf("non-scalar refusal lacks selected path, canonical key, or next step: %q", errw.String())
		}
		if got := mustReadScalarUnset(t, workspace.Path); !bytes.Equal(got, before) {
			t.Fatalf("invalid/non-scalar unset %q changed bytes: %q", args, got)
		}
	}

	// An independently absent valid scalar key remains a successful no-op, even with a lower value.
	var out, errw bytes.Buffer
	rc, output, errOutput := dispatchConfigCapture(t, []string{"unset", "machine.resources.memory", "--local"})
	out.WriteString(output)
	errw.WriteString(errOutput)
	if rc != 0 || strings.Contains(errw.String(), "config edit refused") || strings.Contains(out.String(), "Summary") ||
		!strings.Contains(out.String(), "No change; no write.") {
		t.Fatalf("absent scalar unset rc=%d stdout=%q stderr=%q", rc, out.String(), errw.String())
	}
	if got := mustReadScalarUnset(t, workspace.Path); !bytes.Equal(got, before) {
		t.Fatalf("absent local noop changed project bytes: %q", got)
	}
}

func TestConfigRegistryUnsetScalarRefusalQuotesSelectedFileAndGivesNextStep(t *testing.T) {
	// Resolved where minted: the edit resolver names the selected file by its physical path,
	// and macOS's temp directory is under /var, a link to /private/var.
	tmp, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	workspace := filepath.Join(tmp, "workspace with spaces")
	if err := os.Mkdir(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	project := filepath.Join(workspace, paths.WorkspaceConfigTOMLName)
	if err := os.WriteFile(project, []byte("[machine.gpu]\nenabled = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("YOLO_VERSION", "")
	withConfigEditCWD(t, workspace)

	rc, out, errw := dispatchConfigCapture(t, []string{"unset", "machine.gpu", "--project"})
	if rc == 0 || out != "" || !strings.Contains(errw, `"`+project+`"`) ||
		!strings.Contains(errw, `"machine.gpu"`) || !strings.Contains(errw, "edit the value manually") ||
		!strings.Contains(errw, "yolo check") || !strings.Contains(errw, "yolo config apply") {
		t.Fatalf("non-scalar refusal is not quoted/actionable: rc=%d stdout=%q stderr=%q", rc, out, errw)
	}
	if got, err := os.ReadFile(project); err != nil || string(got) != "[machine.gpu]\nenabled = true\n" {
		t.Fatalf("refusal changed selected Project file: got=%q err=%v", got, err)
	}
}

func TestConfigRegistryUnsetScalarDoesNotAcceptLegacyPathAsPublicAlias(t *testing.T) {
	workspace := testconfig.WorkspaceFormat(t, configdoc.JSONC, `{"resources":{"cpus":3}}`)
	user := testconfig.UserFormat(t, configdoc.JSONC, "{}")
	t.Setenv("HOME", user.Root)
	t.Setenv("YOLO_VERSION", "")
	withConfigEditCWD(t, workspace.Root)
	before := mustReadScalarUnset(t, workspace.Path)
	rc, out, errw := dispatchConfigCapture(t, []string{"unset", "resources.cpus", "--project"})
	if rc == 0 || out != "" || !strings.Contains(errw, "has no readable schema target") {
		t.Fatalf("legacy path alias rc=%d stdout=%q stderr=%q", rc, out, errw)
	}
	if after := mustReadScalarUnset(t, workspace.Path); !bytes.Equal(after, before) {
		t.Fatalf("unsupported legacy CLI alias changed JSONC bytes: before=%q after=%q", before, after)
	}
}

func TestConfigRegistryUnsetScalarHelpDescribesPhysicalRemoval(t *testing.T) {
	rc, out, errw := dispatchConfigCapture(t, []string{"unset", "--help"})
	if rc != 0 || !strings.Contains(out, "physically removes an ordinary recognized scalar") ||
		!strings.Contains(out, "lower layer or default may reappear") || !strings.Contains(out, "Table, list, native, and null removals") ||
		!strings.Contains(out, "unsupported removals refuse without writing") {
		t.Fatalf("public unset help rc=%d stdout=%q stderr=%q", rc, out, errw)
	}
}

func TestConfigRegistryUnsetScalarPrevalidatesInvalidRawResourceUnderLocalMask(t *testing.T) {
	for _, tc := range []struct {
		name, path, projectText string
		format                  configdoc.Format
	}{
		{name: "TOML", path: "machine.resources.cpus", format: configdoc.TOML, projectText: "[machine.resources]\ncpus = 'not-a-number'\n"},
		{name: "JSONC", path: "machine.resources.cpus", format: configdoc.JSONC, projectText: `{"resources":{"cpus":"not-a-number"}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			workspace := testconfig.WorkspaceFormat(t, tc.format, tc.projectText)
			userText := ""
			localText := "[machine.resources]\ncpus = 3\n"
			if tc.format == configdoc.JSONC {
				userText = "{}"
				localText = `{"resources":{"cpus":3}}`
			}
			user := testconfig.UserFormat(t, tc.format, userText)
			t.Setenv("HOME", user.Root)
			t.Setenv("YOLO_VERSION", "")
			localPath := filepath.Join(workspace.Root, paths.WorkspaceLocalConfigTOMLName)
			if tc.format == configdoc.JSONC {
				localPath = filepath.Join(workspace.Root, paths.WorkspaceLocalConfigLegacyJSONCName)
			}
			if err := os.WriteFile(localPath, []byte(localText), 0o600); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(workspace.Path)
			if err != nil {
				t.Fatal(err)
			}
			beforeLocal, err := os.ReadFile(localPath)
			if err != nil {
				t.Fatal(err)
			}
			withConfigEditCWD(t, workspace.Root)
			for _, args := range [][]string{
				{"unset", tc.path, "--project", "--dry-run"},
				{"unset", tc.path, "--project"},
				{"unset", "machine.resources.memory", "--local"}, // absent target still validates the original graph
			} {
				rc, out, errw := dispatchConfigCapture(t, args)
				if rc == 0 || out != "" || !strings.Contains(errw, "validate raw workspace resource config") || strings.Contains(out, "Summary") {
					t.Fatalf("unset masked invalid authored scalar %q rc=%d stdout=%q stderr=%q", args, rc, out, errw)
				}
				if after, _ := os.ReadFile(workspace.Path); !bytes.Equal(after, before) {
					t.Fatalf("invalid masked source was removed: before=%q after=%q", before, after)
				}
				if after, _ := os.ReadFile(localPath); !bytes.Equal(after, beforeLocal) {
					t.Fatalf("invalid-source refusal changed Local bytes: before=%q after=%q", beforeLocal, after)
				}
			}
		})
	}
}

func TestConfigRegistryUnsetScalarPreservesSelectedSymlink(t *testing.T) {
	workspace := testconfig.Workspace(t, "[machine.resources]\ncpus = 2\n")
	user := testconfig.User(t, "")
	t.Setenv("HOME", user.Root)
	t.Setenv("YOLO_VERSION", "")
	physical := filepath.Join(workspace.Root, "physical-project.toml")
	if err := os.Rename(workspace.Path, physical); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Base(physical), workspace.Path); err != nil {
		t.Fatal(err)
	}
	linkBefore, err := os.Readlink(workspace.Path)
	if err != nil {
		t.Fatal(err)
	}
	linkInfo, err := os.Lstat(workspace.Path)
	if err != nil {
		t.Fatal(err)
	}
	withConfigEditCWD(t, workspace.Root)
	rc, out, errw := dispatchConfigCapture(t, []string{"unset", "machine.resources.cpus", "--project"})
	if !strings.Contains(out, workspace.Path) || strings.Contains(string(mustReadScalarUnset(t, physical)), "cpus") {
		t.Fatalf("symlink-target unset rc=%d stdout=%q stderr=%q bytes=%q", rc, out, errw, mustReadScalarUnset(t, physical))
	}
	assertPublicPostWriteCheck(t, rc, out, errw)
	linkAfter, err := os.Readlink(workspace.Path)
	if err != nil {
		t.Fatalf("config symlink disappeared: %v", err)
	}
	linkInfoAfter, err := os.Lstat(workspace.Path)
	if err != nil {
		t.Fatalf("inspect config symlink after unset: %v", err)
	}
	if linkAfter != linkBefore || linkInfoAfter.Mode()&os.ModeSymlink == 0 || linkInfoAfter.Mode().Perm() != linkInfo.Mode().Perm() {
		t.Fatalf("config symlink identity changed: before=%q/%v after=%q/%v", linkBefore, linkInfo.Mode(), linkAfter, linkInfoAfter.Mode())
	}
}

func readableValue(t *testing.T, root *jsonx.OrderedMap, keys ...string) any {
	t.Helper()
	var value any = root
	for _, key := range keys {
		object, ok := value.(*jsonx.OrderedMap)
		if !ok {
			t.Fatalf("%s is not an object: %#v", key, value)
		}
		value, ok = object.Get(key)
		if !ok {
			t.Fatalf("%s is absent", key)
		}
	}
	return value
}

func mustReadScalarUnset(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
