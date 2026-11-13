package configedit

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/configdoc"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

func TestApplyTargetedResolvesAndWritesExplicitRolesInBothFormats(t *testing.T) {
	for _, format := range []struct {
		name, userName, projectName, localName string
		format                                 configdoc.Format
		user                                   string
		project, local                         string
	}{
		{
			name: "JSONC", userName: paths.UserConfigLegacyJSONCName,
			projectName: paths.WorkspaceConfigLegacyJSONCName, localName: paths.WorkspaceLocalConfigLegacyJSONCName,
			format:  configdoc.JSONC,
			user:    `{"packs":["zai"],"providers":{"zai":{"variants":{"fast":{"model":"glm-5.3"}}}}}`,
			project: `{"machine":{"resources":{"cpus":2}}}`,
			local:   `{"machine":{"resources":{"cpus":3}}}`,
		},
		{
			name: "TOML", userName: paths.UserConfigTOMLName,
			projectName: paths.WorkspaceConfigTOMLName, localName: paths.WorkspaceLocalConfigTOMLName,
			format:  configdoc.TOML,
			user:    "packs=['zai']\n[providers.zai.variants.fast]\nmodel='glm-5.3' # keep user comment\n",
			project: "[machine.resources]\ncpus=2 # keep project comment\n",
			local:   "[machine.resources]\ncpus=3 # keep local comment\n",
		},
	} {
		t.Run(format.name, func(t *testing.T) {
			cases := []struct {
				name   string
				choice func(user, project, local string) config.EditTargetChoice
				path   []configdoc.Step
				value  any
				want   string
			}{
				{"explicit User", func(string, string, string) config.EditTargetChoice {
					return config.EditTargetChoice{Kind: config.EditTargetUser}
				}, matrixPath("providers", "zai", "variants", "fast", "model"), "glm-5.3-flash", "user"},
				{"named consumed User", func(user, _, _ string) config.EditTargetChoice {
					return config.EditTargetChoice{Kind: config.EditTargetFile, File: user}
				}, matrixPath("providers", "zai", "variants", "fast", "model"), "glm-5.3-flash", "user"},
				{"explicit Project", func(string, string, string) config.EditTargetChoice {
					return config.EditTargetChoice{Kind: config.EditTargetProject}
				}, matrixPath("machine", "resources", "cpus"), 4, "project"},
				{"named consumed Project", func(_, project, _ string) config.EditTargetChoice {
					return config.EditTargetChoice{Kind: config.EditTargetFile, File: project}
				}, matrixPath("machine", "resources", "cpus"), 4, "project"},
				{"explicit Local", func(string, string, string) config.EditTargetChoice {
					return config.EditTargetChoice{Kind: config.EditTargetLocal}
				}, matrixPath("machine", "resources", "cpus"), 4, "local"},
				{"named consumed Local", func(_, _, local string) config.EditTargetChoice {
					return config.EditTargetChoice{Kind: config.EditTargetFile, File: local}
				}, matrixPath("machine", "resources", "cpus"), 4, "local"},
			}
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					home, workspace := matrixCanonicalTempDir(t), matrixCanonicalTempDir(t)
					t.Setenv("HOME", home)
					t.Setenv("YOLO_VERSION", "")
					userPath := filepath.Join(home, ".config", "yolo-jail", format.userName)
					projectPath := filepath.Join(workspace, format.projectName)
					localPath := filepath.Join(workspace, format.localName)
					writeEditFixture(t, userPath, format.user)
					writeEditFixture(t, projectPath, format.project)
					writeEditFixture(t, localPath, format.local)
					neighbor := filepath.Join(workspace, "neighbor.txt")
					writeEditFixture(t, neighbor, "neighbor bytes\n")
					before := map[string][]byte{}
					modes := map[string]os.FileMode{}
					for _, path := range []string{userPath, projectPath, localPath, neighbor} {
						before[path], _ = os.ReadFile(path)
						info, err := os.Stat(path)
						if err != nil {
							t.Fatal(err)
						}
						modes[path] = info.Mode().Perm()
					}
					mutation := Mutation{Action: Set, Path: tc.path, Value: tc.value}
					choice := tc.choice(userPath, projectPath, localPath)
					target, err := ResolveTarget(workspace, mutation, choice)
					if err != nil {
						t.Fatalf("ResolveTarget: %v", err)
					}
					wantPath := map[string]string{"user": userPath, "project": projectPath, "local": localPath}[tc.want]
					if target.LogicalPath != wantPath {
						t.Fatalf("resolved target = %s, want %s", target.LogicalPath, wantPath)
					}
					if target.Format != format.format {
						t.Fatalf("format = %v, want %v", target.Format, format.format)
					}
					result, err := ApplyTargeted(workspace, target, mutation, ApplyOptions{})
					if err != nil {
						t.Fatalf("ApplyTargeted: %v", err)
					}
					if result.Path != wantPath || result.Plan == nil || result.Plan.Noop {
						t.Fatalf("result = %#v", result)
					}
					for _, path := range []string{userPath, projectPath, localPath, neighbor} {
						got, err := os.ReadFile(path)
						if err != nil {
							t.Fatal(err)
						}
						info, err := os.Stat(path)
						if err != nil {
							t.Fatal(err)
						}
						if path == wantPath {
							if bytes.Equal(got, before[path]) {
								t.Fatalf("selected target %s was not edited", path)
							}
							if tc.want == "user" {
								assertMatrixDocumentValue(t, got, format.format, []string{"providers", "zai", "variants", "fast", "model"}, "glm-5.3-flash")
							} else {
								assertMatrixDocumentValue(t, got, format.format, []string{"machine", "resources", "cpus"}, matrixInteger(t, format.format, 4))
							}
							if info.Mode().Perm() != modes[path] {
								t.Fatalf("selected target mode = %v, want %v", info.Mode().Perm(), modes[path])
							}
							if format.format == configdoc.TOML && !bytes.Contains(got, []byte("# keep "+tc.want+" comment")) {
								t.Fatalf("selected comment was not preserved: %q", got)
							}
						} else if !bytes.Equal(got, before[path]) || info.Mode().Perm() != modes[path] {
							t.Fatalf("non-target %s changed bytes/mode: %q mode=%v", path, got, info.Mode().Perm())
						}
					}
					assertNoTargetArtifacts(t, wantPath)
				})
			}
		})
	}
}

func TestApplyTargetedDefaultsBothScopeToProjectWhenUserWins(t *testing.T) {
	for _, tc := range []struct {
		name, userName, projectName, localName, user, project, local string
		format                                                       configdoc.Format
	}{
		{"JSONC", paths.UserConfigLegacyJSONCName, paths.WorkspaceConfigLegacyJSONCName, paths.WorkspaceLocalConfigLegacyJSONCName,
			`{"machine":{"resources":{"cpus":1}}}`, `{"machine":{"resources":{}}}`, `{}`, configdoc.JSONC},
		{"TOML", paths.UserConfigTOMLName, paths.WorkspaceConfigTOMLName, paths.WorkspaceLocalConfigTOMLName,
			"[machine.resources]\ncpus=1\n", "[machine.resources]\n", "", configdoc.TOML},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home, workspace := matrixCanonicalTempDir(t), matrixCanonicalTempDir(t)
			t.Setenv("HOME", home)
			t.Setenv("YOLO_VERSION", "")
			user := filepath.Join(home, ".config", "yolo-jail", tc.userName)
			project := filepath.Join(workspace, tc.projectName)
			local := filepath.Join(workspace, tc.localName)
			writeEditFixture(t, user, tc.user)
			writeEditFixture(t, project, tc.project)
			writeEditFixture(t, local, tc.local)
			userBefore, _ := os.ReadFile(user)
			mutation := Mutation{Action: Add, Path: matrixPath("machine", "resources", "cpus"), Value: 2}
			target, err := ResolveTarget(workspace, mutation, config.EditTargetChoice{Kind: config.EditTargetAutomatic})
			if err != nil {
				t.Fatal(err)
			}
			if target.Role != config.ExplicitEditProject || target.LogicalPath != project {
				t.Fatalf("Both-scope User winner default = (%s,%v), want Project %s", target.LogicalPath, target.Role, project)
			}
			result, err := ApplyTargeted(workspace, target, mutation, ApplyOptions{})
			if err != nil {
				t.Fatalf("ApplyTargeted: %v", err)
			}
			if result.Path != project {
				t.Fatalf("reported target = %s, want %s", result.Path, project)
			}
			projectAfter, _ := os.ReadFile(project)
			assertMatrixDocumentValue(t, projectAfter, tc.format, []string{"machine", "resources", "cpus"}, matrixInteger(t, tc.format, 2))
			if got, _ := os.ReadFile(user); !bytes.Equal(got, userBefore) {
				t.Fatalf("winning User source was modified: %q", got)
			}
			effective, err := config.LoadReadableConfig(workspace, true, nil)
			if err != nil {
				t.Fatal(err)
			}
			got, present := effectiveMap(t, effective, "machine", "resources").Get("cpus")
			if !present || !reflect.DeepEqual(got, matrixEffectiveResourceInteger(t, tc.format, 2)) {
				t.Fatalf("effective Project value = %#v present=%v, want exact %v integer 2", got, present, tc.format)
			}
			assertNoTargetArtifacts(t, project)
		})
	}
}

func TestApplyTargetedDefaultsWorkspaceOnlyEditToProjectWhenLocalWins(t *testing.T) {
	for _, tc := range []struct {
		name, user, project, local       string
		format                           configdoc.Format
		userName, projectName, localName string
	}{
		{"JSONC", `{"packs":["git"],"allow":{"git":{"on_request":["new_branches"]}}}`, `{"requests":{"git":{"new_branches":false}}}`, `{"requests":{"git":{"new_branches":true}}}`, configdoc.JSONC, paths.UserConfigLegacyJSONCName, paths.WorkspaceConfigLegacyJSONCName, paths.WorkspaceLocalConfigLegacyJSONCName},
		{"TOML", "packs=['git']\n[allow.git]\non_request=['new_branches']\n", "[requests.git]\nnew_branches=false\n", "[requests.git]\nnew_branches=true\n", configdoc.TOML, paths.UserConfigTOMLName, paths.WorkspaceConfigTOMLName, paths.WorkspaceLocalConfigTOMLName},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home, workspace := matrixCanonicalTempDir(t), matrixCanonicalTempDir(t)
			t.Setenv("HOME", home)
			t.Setenv("YOLO_VERSION", "")
			user := filepath.Join(home, ".config", "yolo-jail", tc.userName)
			project := filepath.Join(workspace, tc.projectName)
			local := filepath.Join(workspace, tc.localName)
			writeEditFixture(t, user, tc.user)
			writeEditFixture(t, project, tc.project)
			writeEditFixture(t, local, tc.local)
			localBefore, _ := os.ReadFile(local)
			userBefore, _ := os.ReadFile(user)
			beforeEffective, err := config.LoadReadableConfig(workspace, true, nil)
			if err != nil {
				t.Fatal(err)
			}
			if got, _ := effectiveMap(t, beforeEffective, "requests", "git").Get("new_branches"); got != true {
				t.Fatalf("expected Local to win before edit, got %#v", got)
			}
			mutation := Mutation{Action: Set, Path: matrixPath("requests", "git", "new_branches"), Value: true}
			target, err := ResolveTarget(workspace, mutation, config.EditTargetChoice{Kind: config.EditTargetAutomatic})
			if err != nil {
				t.Fatal(err)
			}
			if target.Role != config.ExplicitEditProject || target.LogicalPath != project {
				t.Fatalf("workspace-only default = (%s,%v), want Project %s", target.LogicalPath, target.Role, project)
			}
			result, err := ApplyTargeted(workspace, target, mutation, ApplyOptions{})
			if err != nil {
				t.Fatalf("ApplyTargeted: %v", err)
			}
			if result.Path != project {
				t.Fatalf("reported target = %s, want %s", result.Path, project)
			}
			projectAfter, _ := os.ReadFile(project)
			assertMatrixDocumentValue(t, projectAfter, tc.format, []string{"requests", "git", "new_branches"}, true)
			effective, err := config.LoadReadableConfig(workspace, true, nil)
			if err != nil {
				t.Fatal(err)
			}
			if got, _ := effectiveMap(t, effective, "requests", "git").Get("new_branches"); got != true {
				t.Fatalf("effective Local winner = %#v, want true", got)
			}
			got, _ := os.ReadFile(local)
			if !bytes.Equal(got, localBefore) {
				t.Fatalf("effective Local winner was modified: %q", got)
			}
			got, _ = os.ReadFile(user)
			if !bytes.Equal(got, userBefore) {
				t.Fatalf("non-target User file was modified: %q", got)
			}
			assertNoTargetArtifacts(t, project)
		})
	}
}

func TestApplyTargetedEmptyListRemovalUsesFormatSpecificMergeSemantics(t *testing.T) {
	// Splicing is shared across formats; an empty list is not. Format 1 JSONC keeps legacy list
	// union, while format 2 TOML uses the documented empty-list reset. See config-migration.md §2–3
	// and docs/reference/config/project-config.md#the-sections; do not let either behavior stand in for the other.
	for _, tc := range []struct {
		name, userName, projectName, localName string
		format                                 configdoc.Format
		user, project, local                   string
		wantEffective                          []any
	}{
		{"JSONC legacy empty list preserves union", paths.UserConfigLegacyJSONCName, paths.WorkspaceConfigLegacyJSONCName, paths.WorkspaceLocalConfigLegacyJSONCName, configdoc.JSONC,
			`{"mcp_servers":{"demo":{"command":["user"]}}}`,
			"{\n  \"mcp_servers\": {\n    \"demo\": {\n      \"command\": [\"project\"] // keep JSONC command comment\n    }\n  }\n}\n", `{}`, []any{"user"}},
		{"TOML format-2 empty list resets", paths.UserConfigTOMLName, paths.WorkspaceConfigTOMLName, paths.WorkspaceLocalConfigTOMLName, configdoc.TOML,
			"[mcp_servers.demo]\ncommand=['user']\n", "[mcp_servers.demo]\ncommand=['project'] # keep TOML command comment\n", "", []any{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home, workspace := matrixCanonicalTempDir(t), matrixCanonicalTempDir(t)
			t.Setenv("HOME", home)
			t.Setenv("YOLO_VERSION", "")
			user := filepath.Join(home, ".config", "yolo-jail", tc.userName)
			project := filepath.Join(workspace, tc.projectName)
			local := filepath.Join(workspace, tc.localName)
			neighbor := filepath.Join(workspace, "neighbor.txt")
			writeEditFixture(t, user, tc.user)
			writeEditFixture(t, project, tc.project)
			writeEditFixture(t, local, tc.local)
			writeEditFixture(t, neighbor, "neighbor bytes\n")
			userBefore, _ := os.ReadFile(user)
			localBefore, _ := os.ReadFile(local)
			neighborBefore, _ := os.ReadFile(neighbor)
			projectInfo, err := os.Stat(project)
			if err != nil {
				t.Fatal(err)
			}
			neighborInfo, err := os.Stat(neighbor)
			if err != nil {
				t.Fatal(err)
			}

			mutation := Mutation{Action: Set, Path: matrixPath("mcp_servers", "demo", "command"), Value: []any{}}
			target, err := ResolveTarget(workspace, mutation, config.EditTargetChoice{Kind: config.EditTargetAutomatic})
			if err != nil {
				t.Fatal(err)
			}
			if target.Role != config.ExplicitEditProject || target.LogicalPath != project || target.Format != tc.format {
				t.Fatalf("target = (%s,%v,%v), want Project %s in %v", target.LogicalPath, target.Role, target.Format, project, tc.format)
			}
			result, err := ApplyTargeted(workspace, target, mutation, ApplyOptions{})
			if err != nil {
				t.Fatalf("ApplyTargeted: %v", err)
			}
			if result.Path != project || result.Plan == nil || result.Plan.Noop {
				t.Fatalf("result = %#v", result)
			}

			projectAfter, err := os.ReadFile(project)
			if err != nil {
				t.Fatal(err)
			}
			projectDoc, err := configdoc.Parse(projectAfter, tc.format)
			if err != nil {
				t.Fatalf("parse selected Project bytes: %v", err)
			}
			command, present := effectiveMap(t, projectDoc.Values, "mcp_servers", "demo").Get("command")
			if !present || !reflect.DeepEqual(command, []any{}) {
				t.Fatalf("selected Project authored command = %#v present=%v, want exact []", command, present)
			}
			wantComment := map[configdoc.Format]string{configdoc.JSONC: "keep JSONC command comment", configdoc.TOML: "keep TOML command comment"}[tc.format]
			if !bytes.Contains(projectAfter, []byte(wantComment)) {
				t.Fatalf("selected Project comment was not preserved: %q", projectAfter)
			}
			projectInfoAfter, err := os.Stat(project)
			if err != nil || projectInfoAfter.Mode().Perm() != projectInfo.Mode().Perm() {
				t.Fatalf("selected Project mode changed: before=%v after=%v err=%v", projectInfo.Mode().Perm(), projectInfoAfter, err)
			}

			effective, err := config.LoadReadableConfig(workspace, true, nil)
			if err != nil {
				t.Fatalf("read effective config: %v", err)
			}
			command, present = effectiveMap(t, effective, "mcp_servers", "demo").Get("command")
			if !present || !reflect.DeepEqual(command, tc.wantEffective) {
				t.Fatalf("effective command = %#v present=%v, want %#v", command, present, tc.wantEffective)
			}
			if got, _ := os.ReadFile(user); !bytes.Equal(got, userBefore) {
				t.Fatalf("non-target User file changed: %q", got)
			}
			if got, _ := os.ReadFile(local); !bytes.Equal(got, localBefore) {
				t.Fatalf("non-target Local file changed: %q", got)
			}
			if got, err := os.ReadFile(neighbor); err != nil || !bytes.Equal(got, neighborBefore) {
				t.Fatalf("neighbor changed: %q err=%v", got, err)
			}
			neighborInfoAfter, err := os.Stat(neighbor)
			if err != nil || neighborInfoAfter.Mode().Perm() != neighborInfo.Mode().Perm() {
				t.Fatalf("neighbor mode changed: before=%v after=%v err=%v", neighborInfo.Mode().Perm(), neighborInfoAfter, err)
			}
			assertNoTargetArtifacts(t, target.PhysicalPath)
		})
	}
}

func TestApplyTargetedAppliesAcceptedSchemaRemovalValuesThroughPublicConsumer(t *testing.T) {
	for _, format := range []struct {
		name, userName, projectName string
		format                      configdoc.Format
		user, project               string
	}{
		{"JSONC", paths.UserConfigLegacyJSONCName, paths.WorkspaceConfigLegacyJSONCName, configdoc.JSONC,
			`{"mcp_servers":{"demo":{"command":["user"]}},"env":{"set":{"AWS_PROFILE":"work"}}}`,
			`{"mcp_servers":{"demo":{"command":["project"]}},"env":{"unset":[]}}`},
		{"TOML", paths.UserConfigTOMLName, paths.WorkspaceConfigTOMLName, configdoc.TOML,
			"[mcp_servers.demo]\ncommand=['user']\n[env.set]\nAWS_PROFILE='work'\n",
			"[mcp_servers.demo]\ncommand=['project']\n[env]\nunset=[]\n"},
	} {
		t.Run(format.name, func(t *testing.T) {
			for _, removal := range []struct {
				name  string
				path  []configdoc.Step
				value any
				role  config.EditTargetChoiceKind
				check func(*testing.T, *jsonx.OrderedMap)
			}{
				{"table-off", matrixPath("mcp_servers", "demo"), "off", config.EditTargetAutomatic, func(t *testing.T, cfg *jsonx.OrderedMap) {
					servers := effectiveMap(t, cfg, "mcp_servers")
					if got, ok := servers.Get("demo"); !ok || got != "off" {
						t.Fatalf("table-valued off = %#v, present=%v", got, ok)
					}
				}},
				{"env-unset", matrixPath("env", "unset"), []any{"AWS_PROFILE"}, config.EditTargetAutomatic, func(t *testing.T, cfg *jsonx.OrderedMap) {
					env := effectiveMap(t, cfg, "env")
					got, _ := env.Get("unset")
					if !reflect.DeepEqual(got, []any{"AWS_PROFILE"}) {
						t.Fatalf("env.unset = %#v", got)
					}
				}},
				{"native", matrixPath("active_provider", "default"), "native", config.EditTargetUser, func(t *testing.T, cfg *jsonx.OrderedMap) {
					if got, _ := effectiveMap(t, cfg, "active_provider").Get("default"); got != "native" {
						t.Fatalf("active_provider.default = %#v", got)
					}
				}},
			} {
				t.Run(removal.name, func(t *testing.T) {
					home, workspace := matrixCanonicalTempDir(t), matrixCanonicalTempDir(t)
					t.Setenv("HOME", home)
					t.Setenv("YOLO_VERSION", "")
					user := filepath.Join(home, ".config", "yolo-jail", format.userName)
					project := filepath.Join(workspace, format.projectName)
					writeEditFixture(t, user, format.user)
					if removal.name == "native" {
						if format.format == configdoc.JSONC {
							writeEditFixture(t, user, `{"active_provider":{"default":"native"}}`)
						} else {
							writeEditFixture(t, user, "[active_provider]\ndefault='native'\n")
						}
					}
					writeEditFixture(t, project, format.project)
					mutation := Mutation{Action: Set, Path: removal.path, Value: removal.value}
					choice := config.EditTargetChoice{Kind: removal.role}
					target, err := ResolveTarget(workspace, mutation, choice)
					if err != nil {
						t.Fatalf("ResolveTarget: %v", err)
					}
					result, err := ApplyTargeted(workspace, target, mutation, ApplyOptions{})
					if err != nil {
						t.Fatalf("ApplyTargeted: %v", err)
					}
					if result.Path != target.LogicalPath {
						t.Fatalf("result path = %s, target = %s", result.Path, target.LogicalPath)
					}
					effective, err := config.LoadReadableConfig(workspace, true, nil)
					if err != nil {
						t.Fatalf("read effective config: %v", err)
					}
					removal.check(t, effective)
					assertNoTargetArtifacts(t, target.PhysicalPath)
				})
			}
		})
	}
}

func TestApplyTargetedPreservesNamedUserSymlinkAndPhysicalTargetBothFormats(t *testing.T) {
	for _, tc := range []struct {
		name, userName, projectName, localName, fragmentRel string
		format                                              configdoc.Format
		userBody, fragment, projectBody, localBody          string
	}{
		{"JSONC user config symlink", paths.UserConfigLegacyJSONCName, paths.WorkspaceConfigLegacyJSONCName, paths.WorkspaceLocalConfigLegacyJSONCName,
			"parts/real.jsonc", configdoc.JSONC,
			"", `{"packs":["zai"],"providers":{"zai":{"variants":{"fast":{"model":"glm-5.3"}}}}} // keep JSONC value comment`, `{}`, `{}`},
		{"TOML config.d symlink", paths.UserConfigTOMLName, paths.WorkspaceConfigTOMLName, paths.WorkspaceLocalConfigTOMLName,
			"parts/real.toml", configdoc.TOML,
			"packs=['zai']\n", "[providers.zai.variants.fast]\nmodel='glm-5.3' # keep TOML value comment\n", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home, workspace := matrixCanonicalTempDir(t), matrixCanonicalTempDir(t)
			t.Setenv("HOME", home)
			t.Setenv("YOLO_VERSION", "")
			userDir := filepath.Join(home, ".config", "yolo-jail")
			user := filepath.Join(userDir, tc.userName)
			project := filepath.Join(workspace, tc.projectName)
			local := filepath.Join(workspace, tc.localName)
			physical := filepath.Join(userDir, tc.fragmentRel)
			writeEditFixture(t, physical, tc.fragment)
			if tc.format == configdoc.JSONC {
				if err := os.Symlink(physical, user); err != nil {
					t.Fatal(err)
				}
			} else {
				writeEditFixture(t, user, tc.userBody)
			}
			writeEditFixture(t, project, tc.projectBody)
			writeEditFixture(t, local, tc.localBody)
			alias := user
			if tc.format == configdoc.TOML {
				alias = filepath.Join(userDir, "config.d", "10-alias.toml")
				if err := os.MkdirAll(filepath.Dir(alias), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(physical, alias); err != nil {
					t.Fatal(err)
				}
			}
			linkBefore, err := os.Readlink(alias)
			if err != nil {
				t.Fatal(err)
			}
			before, _ := os.ReadFile(physical)
			info, _ := os.Stat(physical)
			mutation := Mutation{Action: Set, Path: matrixPath("providers", "zai", "variants", "fast", "model"), Value: "glm-5.3-flash"}
			target, err := ResolveTarget(workspace, mutation, config.EditTargetChoice{Kind: config.EditTargetFile, File: alias})
			if err != nil {
				t.Fatalf("ResolveTarget named alias: %v", err)
			}
			resolvedPhysical, err := filepath.EvalSymlinks(alias)
			if err != nil {
				t.Fatal(err)
			}
			if target.LogicalPath != alias || target.PhysicalPath != resolvedPhysical {
				t.Fatalf("logical/physical target = %s / %s, want %s / %s", target.LogicalPath, target.PhysicalPath, alias, resolvedPhysical)
			}
			result, err := ApplyTargeted(workspace, target, mutation, ApplyOptions{})
			if err != nil {
				t.Fatalf("ApplyTargeted: %v", err)
			}
			if result.Path != alias {
				t.Fatalf("reported path = %s, want logical symlink %s", result.Path, alias)
			}
			after, err := os.ReadFile(physical)
			if err != nil || bytes.Equal(after, before) {
				t.Fatalf("physical target not edited: %q err=%v", after, err)
			}
			assertMatrixDocumentValue(t, after, tc.format, []string{"providers", "zai", "variants", "fast", "model"}, "glm-5.3-flash")
			afterInfo, err := os.Stat(physical)
			if err != nil || afterInfo.Mode().Perm() != info.Mode().Perm() {
				t.Fatalf("physical target mode changed: %v err=%v", afterInfo, err)
			}
			linkInfo, err := os.Lstat(alias)
			linkAfter, linkErr := os.Readlink(alias)
			if err != nil || linkInfo.Mode()&os.ModeSymlink == 0 || linkErr != nil || linkAfter != linkBefore {
				t.Fatalf("logical link changed: mode=%v link=%q err=%v/%v", linkInfo, linkAfter, err, linkErr)
			}
			if !bytes.Contains(after, []byte("keep "+map[configdoc.Format]string{configdoc.JSONC: "JSONC", configdoc.TOML: "TOML"}[tc.format]+" value comment")) {
				t.Fatalf("symlink target comment was not preserved: %q", after)
			}
			assertNoTargetArtifacts(t, physical)
		})
	}
}

func TestResolveTargetRefusesWrongScopeUnconsumedAliasAndAssembledWitness(t *testing.T) {
	for _, format := range []struct {
		name, userName, projectName string
		userFormat, workspaceFormat configdoc.Format
		user, project               string
	}{
		{"JSONC", paths.UserConfigLegacyJSONCName, paths.WorkspaceConfigLegacyJSONCName, configdoc.JSONC, configdoc.JSONC, `{"packs":["git"],"allow":{"git":{"always":["fetch"]}}}`, `{"machine":{"resources":{"cpus":2}}}`},
		{"TOML", paths.UserConfigTOMLName, paths.WorkspaceConfigTOMLName, configdoc.TOML, configdoc.TOML, "packs=['git']\n[allow.git]\nalways=['fetch']\n", "[machine.resources]\ncpus=2\n"},
	} {
		t.Run(format.name, func(t *testing.T) {
			home, workspace := matrixCanonicalTempDir(t), matrixCanonicalTempDir(t)
			t.Setenv("HOME", home)
			t.Setenv("YOLO_VERSION", "")
			user := filepath.Join(home, ".config", "yolo-jail", format.userName)
			project := filepath.Join(workspace, format.projectName)
			writeEditFixture(t, user, format.user)
			writeEditFixture(t, project, format.project)
			unconsumed := filepath.Join(workspace, "not-read.toml")
			alias := filepath.Join(workspace, "not-read-link.toml")
			writeEditFixture(t, unconsumed, format.project)
			if err := os.Symlink(unconsumed, alias); err != nil {
				t.Fatal(err)
			}
			cases := []struct {
				name   string
				path   []configdoc.Step
				choice config.EditTargetChoice
				want   string
			}{
				{"User-only key in Project", matrixPath("allow", "git", "always"), config.EditTargetChoice{Kind: config.EditTargetProject}, "user-scope only"},
				{"Workspace-only key in User", matrixPath("requests", "git", "push"), config.EditTargetChoice{Kind: config.EditTargetUser}, "workspace-only"},
				{"named unconsumed file", matrixPath("machine", "resources", "cpus"), config.EditTargetChoice{Kind: config.EditTargetFile, File: unconsumed}, "not consumed"},
				{"alias to unconsumed file", matrixPath("machine", "resources", "cpus"), config.EditTargetChoice{Kind: config.EditTargetFile, File: alias}, "not consumed"},
				{"named wrong-role file", matrixPath("allow", "git", "always"), config.EditTargetChoice{Kind: config.EditTargetFile, File: project}, "not User config"},
			}
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					mutation := Mutation{Action: Set, Path: tc.path, Value: []any{"fetch"}}
					_, err := ResolveTarget(workspace, mutation, tc.choice)
					if err == nil || !strings.Contains(err.Error(), tc.want) {
						t.Fatalf("ResolveTarget accepted/refused unexpectedly: %v, want %q", err, tc.want)
					}
					assertNoTargetArtifacts(t, user)
					assertNoTargetArtifacts(t, project)
				})
			}
		})
	}

	t.Run("assembled jail snapshot remains ordinary view, authored Project is writable", func(t *testing.T) {
		home, workspace := matrixCanonicalTempDir(t), matrixCanonicalTempDir(t)
		t.Setenv("HOME", home)
		t.Setenv("YOLO_VERSION", "")
		user := filepath.Join(home, ".config", "yolo-jail", paths.UserConfigTOMLName)
		project := filepath.Join(workspace, paths.WorkspaceConfigTOMLName)
		writeEditFixture(t, user, "packs=['git']\n[allow.git]\nalways=['fetch']\n")
		writeEditFixture(t, project, "[machine.resources]\ncpus=2 # authored project\n")
		assembled, sources, err := config.LoadReadableConfigWithSources(workspace, true, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := config.WriteAssembledConfig(workspace, "podman-self", assembled); err != nil {
			t.Fatal(err)
		}
		if err := config.WriteAssembledOrigins(workspace, "podman-self", assembled, sources); err != nil {
			t.Fatal(err)
		}
		workspaceEffective, workspaceSources, err := config.LoadReadableWorkspaceConfigWithSources(workspace, true, nil)
		if err != nil {
			t.Fatalf("exported workspace-only authored reader: %v", err)
		}
		if _, ok := workspaceEffective.Get("machine"); !ok || len(workspaceSources.Locations("config.machine.resources.cpus")) == 0 {
			t.Fatalf("exported scoped reader did not expose authored workspace location: %#v", workspaceSources)
		}
		projectBefore, _ := os.ReadFile(project)
		t.Setenv("YOLO_VERSION", "test-jail")
		t.Setenv("YOLO_WORKSPACE", workspace)
		t.Setenv("YOLO_SETUP", "podman-self")
		ordinaryBefore, err := config.LoadReadableConfig(workspace, true, nil)
		if err != nil {
			t.Fatal(err)
		}
		mutation := Mutation{Action: Set, Path: matrixPath("machine", "resources", "cpus"), Value: 4}
		target, err := ResolveTarget(workspace, mutation, config.EditTargetChoice{Kind: config.EditTargetAutomatic})
		if err != nil {
			t.Fatalf("authored Project was not discovered: %v", err)
		}
		if target.Role != config.ExplicitEditProject || target.LogicalPath != project {
			t.Fatalf("authored target=(%s,%v), want Project %s", target.LogicalPath, target.Role, project)
		}
		result, err := ApplyTargeted(workspace, target, mutation, ApplyOptions{})
		if err != nil || result.Path != project {
			t.Fatalf("ApplyTargeted authored Project=(%#v,%v)", result, err)
		}
		projectAfter, err := os.ReadFile(project)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Equal(projectAfter, projectBefore) || !bytes.Contains(projectAfter, []byte("# authored project")) {
			t.Fatalf("authored Project bytes did not change with comment preserved: %q", projectAfter)
		}
		ordinaryAfter, err := config.LoadReadableConfig(workspace, true, nil)
		if err != nil {
			t.Fatal(err)
		}
		for label, cfg := range map[string]*jsonx.OrderedMap{"before": ordinaryBefore, "after": ordinaryAfter} {
			cpus, present := effectiveMap(t, cfg, "machine", "resources").Get("cpus")
			if !present || !reflect.DeepEqual(cpus, matrixEffectiveResourceInteger(t, configdoc.TOML, 2)) {
				t.Fatalf("ordinary assembled snapshot %s cpus=%#v (%T) present=%v, want exact integer 2", label, cpus, cpus, present)
			}
		}
		assertNoTargetArtifacts(t, project)
	})
}

func TestApplyTargetedPrevalidatesRawScopeOrInvalidSemanticsBeforeDryRunAndNoopBothFormats(t *testing.T) {
	for _, tc := range []struct {
		name, filename, body, noopBody, want string
		format                               configdoc.Format
	}{
		{"JSONC", paths.WorkspaceConfigLegacyJSONCName, `{"machine":{"resources":{"cpus":2}},"allow":{"git":{"always":["fetch"]}}}`, `{"machine":{"resources":{"cpus":2}},"allow":{"git":{"always":["fetch"]}}}`, "user-scope only", configdoc.JSONC},
		{"TOML", paths.WorkspaceConfigTOMLName, "[machine.resources]\ncpus=2\n", "[machine.resources]\ncpus=-1\n", "cpus", configdoc.TOML},
	} {
		for _, mode := range []string{"dry-run", "noop"} {
			t.Run(tc.name+"/"+mode, func(t *testing.T) {
				home, workspace := matrixCanonicalTempDir(t), matrixCanonicalTempDir(t)
				t.Setenv("HOME", home)
				t.Setenv("YOLO_VERSION", "")
				path := filepath.Join(workspace, tc.filename)
				body := tc.body
				value := any(-1)
				if mode == "noop" {
					body = tc.noopBody
				}
				if tc.name == "JSONC" {
					value = 2
				}
				writeEditFixture(t, path, body)
				before, _ := os.ReadFile(path)
				info, _ := os.Stat(path)
				mutation := Mutation{Action: Set, Path: matrixPath("machine", "resources", "cpus"), Value: value}
				target, err := ResolveTarget(workspace, mutation, config.EditTargetChoice{Kind: config.EditTargetAutomatic})
				if err != nil {
					t.Fatal(err)
				}
				writes := spySystemWrites(t)
				_, err = ApplyTargeted(workspace, target, mutation, ApplyOptions{DryRun: mode == "dry-run"})
				if err == nil || !strings.Contains(err.Error(), tc.want) || *writes != 0 {
					t.Fatalf("raw-scope/semantic validation did not precede %s transaction: err=%v writes=%d", mode, err, *writes)
				}
				after, readErr := os.ReadFile(path)
				afterInfo, statErr := os.Stat(path)
				if readErr != nil || statErr != nil || !bytes.Equal(after, before) || afterInfo.Mode().Perm() != info.Mode().Perm() {
					t.Fatalf("scope refusal changed target bytes/mode: %q err=%v stat=%v", after, readErr, statErr)
				}
				assertNoTargetArtifacts(t, path)
			})
		}
	}
}

func TestApplyTargetedOwnJailRejectsInvalidAuthoredResourceAndScopeBeforeEveryModeBothFormats(t *testing.T) {
	for _, format := range []struct {
		name, userName, projectName, localName string
		format                                 configdoc.Format
		user, project, local                   string
	}{
		{"JSONC", paths.UserConfigLegacyJSONCName, paths.WorkspaceConfigLegacyJSONCName, paths.WorkspaceLocalConfigLegacyJSONCName, configdoc.JSONC,
			`{"packs":["git"],"allow":{"git":{"always":["fetch"]}}}`, `{"machine":{"resources":{"cpus":2}}}`, `{"machine":{"resources":{"cpus":3}}}`},
		{"TOML", paths.UserConfigTOMLName, paths.WorkspaceConfigTOMLName, paths.WorkspaceLocalConfigTOMLName, configdoc.TOML,
			"packs=['git']\n[allow.git]\nalways=['fetch']\n", "[machine.resources]\ncpus=2\n", "[machine.resources]\ncpus=3\n"},
	} {
		for _, invalid := range []struct{ name, want string }{
			{"negative masked cpus", "cpus"}, {"resource type", "memory"}, {"unknown resource", "mystery"}, {"user-only raw scope", "user-scope only"},
		} {
			for _, mode := range []string{"edit", "dry-run", "noop"} {
				t.Run(format.name+"/"+invalid.name+"/"+mode, func(t *testing.T) {
					home, workspace := matrixCanonicalTempDir(t), matrixCanonicalTempDir(t)
					t.Setenv("HOME", home)
					t.Setenv("YOLO_VERSION", "")
					t.Setenv("YOLO_WORKSPACE", "")
					t.Setenv("YOLO_SETUP", "podman-self")
					user := filepath.Join(home, ".config", "yolo-jail", format.userName)
					project := filepath.Join(workspace, format.projectName)
					local := filepath.Join(workspace, format.localName)
					neighbor := filepath.Join(workspace, "neighbor.txt")
					writeEditFixture(t, user, format.user)
					writeEditFixture(t, project, format.project)
					writeEditFixture(t, local, format.local)
					writeEditFixture(t, neighbor, "neighbor bytes\n")
					if err := os.Chmod(project, 0640); err != nil {
						t.Fatal(err)
					}
					wantDelivered := writeOwnJailDelivery(t, workspace)
					value := int64(4)
					if mode == "noop" {
						value = 3
					}
					mutation := Mutation{Action: Set, Path: matrixPath("machine", "resources", "cpus"), Value: value}
					target, err := ResolveTarget(workspace, mutation, config.EditTargetChoice{Kind: config.EditTargetLocal})
					if err != nil || target.Role != config.ExplicitEditLocal || target.LogicalPath != local || target.Format != format.format {
						t.Fatalf("ResolveTarget Local=(%#v,%v), want %s/%v", target, err, local, format.format)
					}
					beforeEffective, err := config.LoadConfig(workspace, true, nil)
					if err != nil {
						t.Fatal(err)
					}
					beforeEffectiveJSON, err := config.SnapshotJSON(beforeEffective)
					if err != nil {
						t.Fatal(err)
					}
					var badProject string
					if format.format == configdoc.JSONC {
						switch invalid.name {
						case "negative masked cpus":
							badProject = `{"machine":{"resources":{"cpus":-1}}}`
						case "resource type":
							badProject = `{"machine":{"resources":{"cpus":2,"memory":8}}}`
						case "unknown resource":
							badProject = `{"machine":{"resources":{"cpus":2,"mystery":true}}}`
						case "user-only raw scope":
							badProject = `{"machine":{"resources":{"cpus":2}},"allow":{"git":{"always":["fetch"]}}}`
						}
					} else {
						switch invalid.name {
						case "negative masked cpus":
							badProject = "[machine.resources]\ncpus=-1\n"
						case "resource type":
							badProject = "[machine.resources]\ncpus=2\nmemory=8\n"
						case "unknown resource":
							badProject = "[machine.resources]\ncpus=2\nmystery=true\n"
						case "user-only raw scope":
							badProject = "[machine.resources]\ncpus=2\n[allow.git]\nalways=['fetch']\n"
						}
					}
					writeEditFixture(t, project, badProject)
					if err := os.Chmod(project, 0640); err != nil {
						t.Fatal(err)
					}
					if invalid.name == "negative masked cpus" {
						afterEffective, err := config.LoadConfig(workspace, true, nil)
						if err != nil {
							t.Fatal(err)
						}
						afterEffectiveJSON, err := config.SnapshotJSON(afterEffective)
						if err != nil || afterEffectiveJSON != beforeEffectiveJSON {
							t.Fatalf("invalid Project cpus ceased being masked by Local: equal=%v err=%v", afterEffectiveJSON == beforeEffectiveJSON, err)
						}
					}
					t.Setenv("YOLO_VERSION", "test-jail")
					t.Setenv("YOLO_WORKSPACE", workspace)
					t.Setenv("YOLO_SETUP", "podman-self")
					ordinary, err := config.LoadReadableConfig(workspace, true, nil)
					if err != nil {
						t.Fatal(err)
					}
					delivered, err := config.SnapshotJSON(ordinary)
					if err != nil || delivered != wantDelivered {
						t.Fatalf("delivered snapshot did not mask invalid authored source: equal=%v err=%v", delivered == wantDelivered, err)
					}
					before, modes := map[string][]byte{}, map[string]os.FileMode{}
					for _, path := range []string{user, project, local, neighbor} {
						before[path], err = os.ReadFile(path)
						if err != nil {
							t.Fatal(err)
						}
						info, statErr := os.Stat(path)
						if statErr != nil {
							t.Fatal(statErr)
						}
						modes[path] = info.Mode().Perm()
					}
					writes := spySystemWrites(t)
					_, err = ApplyTargeted(workspace, target, mutation, ApplyOptions{DryRun: mode == "dry-run"})
					if err == nil || !strings.Contains(err.Error(), invalid.want) || *writes != 0 {
						t.Fatalf("%s invalid graph not refused before %s write: err=%v writes=%d want=%q", invalid.name, mode, err, *writes, invalid.want)
					}
					for path, want := range before {
						got, readErr := os.ReadFile(path)
						info, statErr := os.Stat(path)
						if readErr != nil || statErr != nil || !bytes.Equal(got, want) || info.Mode().Perm() != modes[path] {
							t.Fatalf("refusal changed %s bytes/mode: %q info=%v errs=%v/%v", path, got, info, readErr, statErr)
						}
					}
					assertNoTargetArtifacts(t, project)
					assertNoTargetArtifacts(t, local)
					ordinaryAfter, err := config.LoadReadableConfig(workspace, true, nil)
					if err != nil {
						t.Fatal(err)
					}
					after, err := config.SnapshotJSON(ordinaryAfter)
					if err != nil || after != wantDelivered {
						t.Fatalf("refusal changed delivered snapshot: equal=%v err=%v", after == wantDelivered, err)
					}
				})
			}
		}
	}
}

func TestApplyTargetedOwnJailPostwriteMaskedRawResourceRollbackBothFormats(t *testing.T) {
	for _, format := range []struct {
		name, userName, projectName, localName, fragmentName string
		format                                               configdoc.Format
		user, project, fragment, local, invalidFragment      string
	}{
		{"JSONC", paths.UserConfigLegacyJSONCName, paths.WorkspaceConfigLegacyJSONCName, paths.WorkspaceLocalConfigLegacyJSONCName, "parts/limits.jsonc", configdoc.JSONC,
			`{"packs":["git"]}`, `{"include_if_found":["parts/limits.jsonc"],"machine":{"resources":{"memory":"8g"}}} // project`, `{"machine":{"resources":{"cpus":2}}}`, `{"machine":{"resources":{"cpus":3}}}`, `{"machine":{"resources":{"cpus":-1}}}`},
		{"TOML", paths.UserConfigTOMLName, paths.WorkspaceConfigTOMLName, paths.WorkspaceLocalConfigTOMLName, "parts/limits.toml", configdoc.TOML,
			"packs=['git']\n", "include_if_found=['parts/limits.toml']\n[machine.resources]\nmemory='8g' # project\n", "[machine.resources]\ncpus=2\n", "[machine.resources]\ncpus=3\n", "[machine.resources]\ncpus=-1\n"},
	} {
		t.Run(format.name, func(t *testing.T) {
			home, workspace := matrixCanonicalTempDir(t), matrixCanonicalTempDir(t)
			t.Setenv("HOME", home)
			t.Setenv("YOLO_VERSION", "")
			t.Setenv("YOLO_WORKSPACE", "")
			t.Setenv("YOLO_SETUP", "podman-self")
			user := filepath.Join(home, ".config", "yolo-jail", format.userName)
			project := filepath.Join(workspace, format.projectName)
			fragment := filepath.Join(workspace, format.fragmentName)
			local := filepath.Join(workspace, format.localName)
			neighbor := filepath.Join(workspace, "neighbor.txt")
			projectBefore := []byte(format.project)
			writeEditFixture(t, user, format.user)
			writeEditFixture(t, project, string(projectBefore))
			writeEditFixture(t, fragment, format.fragment)
			writeEditFixture(t, local, format.local)
			writeEditFixture(t, neighbor, "neighbor bytes\n")
			userBefore, err := os.ReadFile(user)
			if err != nil {
				t.Fatal(err)
			}
			fragmentBefore, err := os.ReadFile(fragment)
			if err != nil {
				t.Fatal(err)
			}
			fragmentInfo, err := os.Stat(fragment)
			if err != nil {
				t.Fatal(err)
			}
			localBefore, err := os.ReadFile(local)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(project, 0640); err != nil {
				t.Fatal(err)
			}
			wantDelivered := writeOwnJailDelivery(t, workspace)
			projectInfo, err := os.Stat(project)
			if err != nil {
				t.Fatal(err)
			}
			t.Setenv("YOLO_VERSION", "test-jail")
			t.Setenv("YOLO_WORKSPACE", workspace)
			t.Setenv("YOLO_SETUP", "podman-self")
			beforeInput, err := config.NewConfigReadInput(project, projectBefore)
			if err != nil {
				t.Fatal(err)
			}
			beforeEffective, err := config.LoadConfigWithReadInput(workspace, true, nil, beforeInput)
			if err != nil {
				t.Fatal(err)
			}
			resources := effectiveMap(t, beforeEffective, "machine", "resources")
			cpusBefore, present := resources.Get("cpus")
			wantCPUs := matrixEffectiveResourceInteger(t, format.format, 3)
			if !present || !reflect.DeepEqual(cpusBefore, wantCPUs) {
				t.Fatalf("Local did not win before transaction: cpus=%#v (%T) present=%v, want %#v (%T)", cpusBefore, cpusBefore, present, wantCPUs, wantCPUs)
			}
			mutation := Mutation{Action: Set, Path: matrixPath("machine", "resources", "memory"), Value: "16g"}
			target, err := ResolveTarget(workspace, mutation, config.EditTargetChoice{Kind: config.EditTargetAutomatic})
			if err != nil || target.Role != config.ExplicitEditProject || target.LogicalPath != project || target.Format != format.format {
				t.Fatalf("ResolveTarget=(%#v,%v), want %s/Project/%v", target, err, project, format.format)
			}
			ops := systemFileOps
			writes := 0
			canonicalSame, deliveredSame := false, false
			ops.WriteAtomic = func(path string, data []byte, mode os.FileMode) error {
				writes++
				if err := writeAtomic(path, data, mode); err != nil {
					return err
				}
				if writes == 1 {
					plannedInput, err := config.NewConfigReadInput(project, data)
					if err != nil {
						return err
					}
					plannedEffective, err := config.LoadConfigWithReadInput(workspace, true, nil, plannedInput)
					if err != nil {
						return err
					}
					plannedSnapshot, err := config.SnapshotJSON(plannedEffective)
					if err != nil {
						return err
					}
					if err := os.WriteFile(fragment, []byte(format.invalidFragment), fragmentInfo.Mode().Perm()); err != nil {
						return err
					}
					afterInput, err := config.NewConfigReadInput(project, data)
					if err != nil {
						return err
					}
					afterEffective, err := config.LoadConfigWithReadInput(workspace, true, nil, afterInput)
					if err != nil {
						return err
					}
					afterSnapshot, err := config.SnapshotJSON(afterEffective)
					if err != nil {
						return err
					}
					canonicalSame = plannedSnapshot == afterSnapshot
					afterResources := effectiveMap(t, afterEffective, "machine", "resources")
					cpus, present := afterResources.Get("cpus")
					if !present || !reflect.DeepEqual(cpus, matrixEffectiveResourceInteger(t, format.format, 3)) {
						return fmt.Errorf("Local ceased to mask invalid Project cpus: %#v", cpus)
					}
					ordinary, err := config.LoadReadableConfig(workspace, true, nil)
					if err != nil {
						return err
					}
					delivered, err := config.SnapshotJSON(ordinary)
					if err != nil {
						return err
					}
					deliveredSame = delivered == wantDelivered
				}
				if writes == 2 {
					if err := os.WriteFile(fragment, fragmentBefore, fragmentInfo.Mode().Perm()); err != nil {
						return err
					}
				}
				return nil
			}
			originalOps := systemFileOps
			systemFileOps = ops
			t.Cleanup(func() { systemFileOps = originalOps })
			_, err = ApplyTargeted(workspace, target, mutation, ApplyOptions{})
			if !canonicalSame || !deliveredSame {
				t.Fatalf("postwrite invalid Project resource was not masked by Local while deliveries held: effective=%v delivered=%v", canonicalSame, deliveredSame)
			}
			if err == nil || !strings.Contains(err.Error(), "validate actual config") || !strings.Contains(err.Error(), "cpus") || !strings.Contains(err.Error(), "original bytes restored") {
				t.Fatalf("public own-jail targeted caller did not reject postwrite raw resource invalidity and rollback: %v", err)
			}
			projectAfter, projectReadErr := os.ReadFile(project)
			projectAfterInfo, projectStatErr := os.Stat(project)
			if projectReadErr != nil || projectStatErr != nil || !bytes.Equal(projectAfter, projectBefore) || projectAfterInfo.Mode().Perm() != projectInfo.Mode().Perm() || writes != 2 {
				t.Fatalf("rollback did not restore exact Project bytes/mode: bytes=%q info=%v writes=%d errs=%v/%v", projectAfter, projectAfterInfo, writes, projectReadErr, projectStatErr)
			}
			fragmentAfter, fragmentReadErr := os.ReadFile(fragment)
			fragmentAfterInfo, fragmentStatErr := os.Stat(fragment)
			if fragmentReadErr != nil || fragmentStatErr != nil || !bytes.Equal(fragmentAfter, fragmentBefore) || fragmentAfterInfo.Mode().Perm() != fragmentInfo.Mode().Perm() {
				t.Fatalf("fragment was not restored after postwrite refusal: %q info=%v errs=%v/%v", fragmentAfter, fragmentAfterInfo, fragmentReadErr, fragmentStatErr)
			}
			if got, err := os.ReadFile(user); err != nil || !bytes.Equal(got, userBefore) {
				t.Fatalf("User scope changed: %q err=%v", got, err)
			}
			if got, err := os.ReadFile(local); err != nil || !bytes.Equal(got, localBefore) {
				t.Fatalf("Local changed: %q err=%v", got, err)
			}
			if got, err := os.ReadFile(neighbor); err != nil || string(got) != "neighbor bytes\n" {
				t.Fatalf("sibling changed: %q err=%v", got, err)
			}
			assertNoTargetArtifacts(t, project)
			assertNoTargetArtifacts(t, fragment)
		})
	}
}

func TestApplyTargetedRollsBackWhenConsumedIncludeDisappearsDespiteEqualEffectiveSnapshot(t *testing.T) {
	home, workspace := matrixCanonicalTempDir(t), matrixCanonicalTempDir(t)
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "")
	base := filepath.Join(workspace, paths.WorkspaceConfigTOMLName)
	baseBefore := []byte("include_if_found=['parts/edit.toml']\n")
	writeEditFixture(t, base, string(baseBefore))
	physical := filepath.Join(workspace, "parts", "edit.toml")
	before := []byte("[machine.resources]\ncpus=2 # retained\n")
	writeEditFixture(t, physical, string(before))
	writeEditFixture(t, filepath.Join(workspace, paths.WorkspaceLocalConfigTOMLName), "[machine.resources]\ncpus=4\n")
	info, err := os.Stat(physical)
	if err != nil {
		t.Fatal(err)
	}
	mutation := Mutation{Action: Set, Path: matrixPath("machine", "resources", "cpus"), Value: 4}
	target, err := ResolveTarget(workspace, mutation, config.EditTargetChoice{Kind: config.EditTargetFile, File: physical})
	if err != nil {
		t.Fatalf("ResolveTarget consumed include: %v", err)
	}
	if target.Role != config.ExplicitEditProject {
		t.Fatalf("included target role = %v, want Project", target.Role)
	}
	beforeEffective, err := config.LoadConfig(workspace, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	beforeSnapshot, err := config.SnapshotJSON(beforeEffective)
	if err != nil {
		t.Fatal(err)
	}
	ops := systemFileOps
	writes := 0
	canonicalSame := false
	ops.WriteAtomic = func(path string, data []byte, mode os.FileMode) error {
		writes++
		if err := writeAtomic(path, data, mode); err != nil {
			return err
		}
		if writes == 1 {
			if err := os.WriteFile(base, nil, 0600); err != nil {
				return err
			}
			afterEffective, err := config.LoadConfig(workspace, true, nil)
			if err != nil {
				return err
			}
			afterSnapshot, err := config.SnapshotJSON(afterEffective)
			if err != nil {
				return err
			}
			canonicalSame = beforeSnapshot == afterSnapshot
		}
		return nil
	}
	originalOps := systemFileOps
	systemFileOps = ops
	t.Cleanup(func() { systemFileOps = originalOps })
	_, err = ApplyTargeted(workspace, target, mutation, ApplyOptions{})
	if !canonicalSame {
		t.Fatal("removing the include changed the canonical effective snapshot")
	}
	if err == nil || !strings.Contains(err.Error(), "not consumed by the standard config graph") || !strings.Contains(err.Error(), "original bytes restored") {
		t.Fatalf("post-write consumption loss was not rejected and rolled back: %v", err)
	}
	got, readErr := os.ReadFile(physical)
	gotInfo, statErr := os.Stat(physical)
	if readErr != nil || statErr != nil || !bytes.Equal(got, before) || gotInfo.Mode().Perm() != info.Mode().Perm() || writes != 2 {
		t.Fatalf("rollback did not restore exact target bytes/mode: bytes=%q info=%v writes=%d errs=%v/%v", got, gotInfo, writes, readErr, statErr)
	}
	if err := os.WriteFile(base, baseBefore, 0600); err != nil {
		t.Fatal(err)
	}
	assertNoTargetArtifacts(t, physical)
}

func TestApplyTargetedCanonicalizesFixturesUnderLinkedTempParent(t *testing.T) {
	parent := matrixCanonicalTempDir(t)
	physicalTemp := filepath.Join(parent, "physical-temp")
	aliasedTemp := filepath.Join(parent, "aliased-temp")
	if err := os.Mkdir(physicalTemp, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(physicalTemp, aliasedTemp); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name, userName, projectName, localName string
		format                                 configdoc.Format
		user, project, local                   string
	}{
		{"JSONC", paths.UserConfigLegacyJSONCName, paths.WorkspaceConfigLegacyJSONCName, paths.WorkspaceLocalConfigLegacyJSONCName, configdoc.JSONC,
			`{"machine":{"resources":{"cpus":1}}}`, `{"machine":{"resources":{"cpus":2}}} // keep linked-temp comment`, `{"machine":{"resources":{"cpus":3}}}`},
		{"TOML", paths.UserConfigTOMLName, paths.WorkspaceConfigTOMLName, paths.WorkspaceLocalConfigTOMLName, configdoc.TOML,
			"[machine.resources]\ncpus=1\n", "[machine.resources]\ncpus=2 # keep linked-temp comment\n", "[machine.resources]\ncpus=3\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("TMPDIR", aliasedTemp)
			t.Setenv("GOTMPDIR", aliasedTemp)
			homeLogical, workspaceLogical := t.TempDir(), t.TempDir()
			home := matrixCanonicalizeFixturePath(t, homeLogical)
			workspace := matrixCanonicalizeFixturePath(t, workspaceLogical)
			if home == homeLogical || workspace == workspaceLogical {
				t.Fatalf("linked TMPDIR did not distinguish fixture aliases: home=%q/%q workspace=%q/%q", homeLogical, home, workspaceLogical, workspace)
			}
			if !strings.HasPrefix(workspaceLogical, aliasedTemp+string(os.PathSeparator)) || !strings.HasPrefix(homeLogical, aliasedTemp+string(os.PathSeparator)) {
				t.Fatalf("Go fixtures were not minted under linked TMPDIR %q: home=%q workspace=%q", aliasedTemp, homeLogical, workspaceLogical)
			}
			t.Setenv("HOME", home)
			t.Setenv("YOLO_VERSION", "")

			user := filepath.Join(home, ".config", "yolo-jail", tc.userName)
			project := filepath.Join(workspace, tc.projectName)
			local := filepath.Join(workspace, tc.localName)
			neighbor := filepath.Join(workspace, "neighbor.txt")
			writeEditFixture(t, user, tc.user)
			writeEditFixture(t, project, tc.project)
			writeEditFixture(t, local, tc.local)
			writeEditFixture(t, neighbor, "neighbor bytes\n")
			userBefore, _ := os.ReadFile(user)
			localBefore, _ := os.ReadFile(local)
			neighborBefore, _ := os.ReadFile(neighbor)
			projectInfo, err := os.Stat(project)
			if err != nil {
				t.Fatal(err)
			}

			mutation := Mutation{Action: Set, Path: matrixPath("machine", "resources", "cpus"), Value: 4}
			target, err := ResolveTarget(workspace, mutation, config.EditTargetChoice{Kind: config.EditTargetAutomatic})
			if err != nil {
				t.Fatalf("ResolveTarget: %v", err)
			}
			if target.LogicalPath != project || target.PhysicalPath != project || target.Role != config.ExplicitEditProject {
				t.Fatalf("linked-TMPDIR target = (%s,%s,%v), want canonical Project %s", target.LogicalPath, target.PhysicalPath, target.Role, project)
			}
			result, err := ApplyTargeted(workspace, target, mutation, ApplyOptions{})
			if err != nil {
				t.Fatalf("ApplyTargeted: %v", err)
			}
			if result.Path != project || result.Plan == nil || result.Plan.Noop {
				t.Fatalf("result = %#v", result)
			}
			projectAfter, err := os.ReadFile(project)
			if err != nil {
				t.Fatal(err)
			}
			assertMatrixDocumentValue(t, projectAfter, tc.format, []string{"machine", "resources", "cpus"}, matrixInteger(t, tc.format, 4))
			if !bytes.Contains(projectAfter, []byte("keep linked-temp comment")) {
				t.Fatalf("selected Project comment changed: %q", projectAfter)
			}
			projectInfoAfter, err := os.Stat(project)
			if err != nil || projectInfoAfter.Mode().Perm() != projectInfo.Mode().Perm() {
				t.Fatalf("selected Project mode changed: before=%v after=%v err=%v", projectInfo.Mode().Perm(), projectInfoAfter, err)
			}

			effective, err := config.LoadReadableConfig(workspace, true, nil)
			if err != nil {
				t.Fatalf("read effective config: %v", err)
			}
			got, present := effectiveMap(t, effective, "machine", "resources").Get("cpus")
			if !present || !reflect.DeepEqual(got, matrixEffectiveResourceInteger(t, tc.format, 3)) {
				t.Fatalf("effective Local winner = %#v (%T) present=%v, want exact 3", got, got, present)
			}
			if got, _ := os.ReadFile(user); !bytes.Equal(got, userBefore) {
				t.Fatalf("non-target User file changed: %q", got)
			}
			if got, _ := os.ReadFile(local); !bytes.Equal(got, localBefore) {
				t.Fatalf("effective Local winner was modified: %q", got)
			}
			if got, err := os.ReadFile(neighbor); err != nil || !bytes.Equal(got, neighborBefore) {
				t.Fatalf("neighbor changed: %q err=%v", got, err)
			}
			assertNoTargetArtifacts(t, project)
		})
	}
}

func matrixCanonicalTempDir(t *testing.T) string {
	t.Helper()
	return matrixCanonicalizeFixturePath(t, t.TempDir())
}

func matrixCanonicalizeFixturePath(t *testing.T, path string) string {
	t.Helper()
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("canonicalize minted test fixture %s: %v", path, err)
	}
	return canonical
}

func matrixInteger(t *testing.T, format configdoc.Format, value int64) any {
	t.Helper()
	if format == configdoc.JSONC {
		integer, ok := jsonx.NumberValue(fmt.Sprint(value))
		if !ok {
			t.Fatalf("parse JSONC integer expectation %d", value)
		}
		return integer
	}
	return value
}

func matrixEffectiveResourceInteger(t *testing.T, format configdoc.Format, value int64) any {
	t.Helper()
	integer, ok := jsonx.NumberValue(fmt.Sprint(value))
	if !ok {
		t.Fatalf("parse effective resource integer expectation %d for %v", value, format)
	}
	return integer
}

func assertMatrixDocumentValue(t *testing.T, data []byte, format configdoc.Format, path []string, want any) {
	t.Helper()
	if len(path) == 0 {
		t.Fatal("document value assertion requires a path")
	}
	doc, err := configdoc.Parse(data, format)
	if err != nil {
		t.Fatalf("parse %v document: %v\n%s", format, err, data)
	}
	parent := effectiveMap(t, doc.Values, path[:len(path)-1]...)
	got, present := parent.Get(path[len(path)-1])
	if !present || !reflect.DeepEqual(got, want) {
		t.Fatalf("parsed authored value at %v = %#v present=%v, want exact %#v", path, got, present, want)
	}
}

func matrixPath(keys ...string) []configdoc.Step {
	path := make([]configdoc.Step, len(keys))
	for i, key := range keys {
		path[i] = configdoc.Step{Key: key}
	}
	return path
}
