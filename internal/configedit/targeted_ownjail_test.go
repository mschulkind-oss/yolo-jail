package configedit

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/configdoc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

func TestResolveAndApplyTargetedOwnJailUsesAuthoredProjectAndLocal(t *testing.T) {
	for _, tc := range []struct {
		name, userName, projectName, localName string
		format                                 configdoc.Format
		user, override, project, local         string
		path                                   []configdoc.Step
		valuePath                              []string
	}{
		{
			name: "JSONC", userName: paths.UserConfigLegacyJSONCName,
			projectName: paths.WorkspaceConfigLegacyJSONCName, localName: paths.WorkspaceLocalConfigLegacyJSONCName,
			format:   configdoc.JSONC,
			user:     `{"include_if_found":["host-overrides.jsonc"]}`,
			override: `{"mcp_servers":{"host_only":{"command":["host-tool"]}}}`,
			project:  `{"machine":{"resources":{"cpus":2}}} // project comment`, local: `{"machine":{"resources":{"cpus":3}}} // local comment`,
			path: []configdoc.Step{{Key: "machine"}, {Key: "resources"}, {Key: "cpus"}}, valuePath: []string{"machine", "resources", "cpus"},
		},
		{
			name: "TOML", userName: paths.UserConfigTOMLName,
			projectName: paths.WorkspaceConfigTOMLName, localName: paths.WorkspaceLocalConfigTOMLName,
			format:   configdoc.TOML,
			user:     "include_if_found = ['host-overrides.toml']\n",
			override: "[mcp_servers.host_only]\ncommand = ['host-tool']\n",
			project:  "[machine.resources]\ncpus = 2 # project comment\n", local: "[machine.resources]\ncpus = 3 # local comment\n",
			path: []configdoc.Step{{Key: "machine"}, {Key: "resources"}, {Key: "cpus"}}, valuePath: []string{"machine", "resources", "cpus"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home, workspace := matrixCanonicalTempDir(t), matrixCanonicalTempDir(t)
			t.Setenv("HOME", home)
			t.Setenv("YOLO_VERSION", "")
			t.Setenv("YOLO_WORKSPACE", "")
			t.Setenv("YOLO_SETUP", "podman-self")
			t.Setenv(config.UserLayerEnv, "")
			user := filepath.Join(home, ".config", "yolo-jail", tc.userName)
			var userOverride string
			// Keep the include spelling identical to the authored filename.
			if tc.format == configdoc.JSONC {
				userOverride = filepath.Join(filepath.Dir(user), "host-overrides.jsonc")
			} else {
				userOverride = filepath.Join(filepath.Dir(user), "host-overrides.toml")
			}
			project := filepath.Join(workspace, tc.projectName)
			local := filepath.Join(workspace, tc.localName)
			neighbor := filepath.Join(workspace, "neighbor.txt")
			writeEditFixture(t, user, tc.user)
			writeEditFixture(t, userOverride, tc.override)
			writeEditFixture(t, project, tc.project)
			writeEditFixture(t, local, tc.local)
			writeEditFixture(t, neighbor, "neighbor bytes\n")
			if err := os.Chmod(project, 0640); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(local, 0640); err != nil {
				t.Fatal(err)
			}

			assembled, sources, err := config.LoadReadableConfigWithSources(workspace, true, nil)
			if err != nil {
				t.Fatalf("host-side assembly: %v", err)
			}
			if err := config.WriteAssembledConfig(workspace, "podman-self", assembled); err != nil {
				t.Fatal(err)
			}
			if err := config.WriteAssembledOrigins(workspace, "podman-self", assembled, sources); err != nil {
				t.Fatal(err)
			}
			assembledBytes, err := config.SnapshotJSON(assembled)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(userOverride); err != nil {
				t.Fatal(err)
			}
			t.Setenv("YOLO_VERSION", "test-jail")
			t.Setenv("YOLO_WORKSPACE", workspace)
			t.Setenv("YOLO_SETUP", "podman-self")

			ordinaryBefore, err := config.LoadReadableConfig(workspace, true, nil)
			if err != nil {
				t.Fatalf("ordinary assembled read before edit: %v", err)
			}
			ordinaryBeforeJSON, err := config.SnapshotJSON(ordinaryBefore)
			if err != nil || ordinaryBeforeJSON != assembledBytes {
				t.Fatalf("ordinary own-jail read did not return delivered snapshot before edit: equal=%v err=%v", ordinaryBeforeJSON == assembledBytes, err)
			}
			hostOnly, found := effectiveMap(t, ordinaryBefore, "mcp_servers").Get("host_only")
			if !found || hostOnly == nil {
				t.Fatalf("delivered host-only include value is absent before edit: %#v", ordinaryBefore)
			}

			userBefore, err := os.ReadFile(user)
			if err != nil {
				t.Fatal(err)
			}
			projectBefore, err := os.ReadFile(project)
			if err != nil {
				t.Fatal(err)
			}
			localBefore, err := os.ReadFile(local)
			if err != nil {
				t.Fatal(err)
			}
			neighborBefore, err := os.ReadFile(neighbor)
			if err != nil {
				t.Fatal(err)
			}
			mutation := Mutation{Action: Set, Path: tc.path, Value: int64(4)}
			target, err := ResolveTarget(workspace, mutation, config.EditTargetChoice{Kind: config.EditTargetAutomatic})
			if err != nil {
				t.Fatalf("resolve authored own-jail Project target: %v", err)
			}
			if target.LogicalPath != project || target.Role != config.ExplicitEditProject || target.Format != tc.format {
				t.Fatalf("automatic target = (%s,%v,%v), want authored Project %s in %v", target.LogicalPath, target.Role, target.Format, project, tc.format)
			}
			result, err := ApplyTargeted(workspace, target, mutation, ApplyOptions{})
			if err != nil || result.Path != project || result.Plan == nil || result.Plan.Noop {
				t.Fatalf("ApplyTargeted Project = (%#v, %v), want a changing disclosed Project edit", result, err)
			}
			projectAfter, err := os.ReadFile(project)
			if err != nil {
				t.Fatal(err)
			}
			assertMatrixDocumentValue(t, projectAfter, tc.format, tc.valuePath, matrixInteger(t, tc.format, 4))
			if bytes.Equal(projectAfter, projectBefore) || !bytes.Contains(projectAfter, []byte("comment")) {
				t.Fatalf("authored Project bytes did not change with comment preserved: %q", projectAfter)
			}
			projectInfo, err := os.Stat(project)
			if err != nil || projectInfo.Mode().Perm() != 0640 {
				t.Fatalf("Project mode = %v err=%v, want 0640", projectInfo, err)
			}
			if got, err := os.ReadFile(local); err != nil || !bytes.Equal(got, localBefore) {
				t.Fatalf("Local changed during Project edit: %q err=%v", got, err)
			}

			projectInput, err := config.NewConfigReadInput(project, projectAfter)
			if err != nil {
				t.Fatal(err)
			}
			afterProjectEffective, err := config.LoadConfigWithReadInput(workspace, true, nil, projectInput)
			if err != nil {
				t.Fatalf("independent authored effective read after Project edit: %v", err)
			}
			afterProjectResources := effectiveMap(t, afterProjectEffective, tc.valuePath[:len(tc.valuePath)-1]...)
			winningProjectValue, found := afterProjectResources.Get(tc.valuePath[len(tc.valuePath)-1])
			if !found || !reflect.DeepEqual(winningProjectValue, matrixEffectiveResourceInteger(t, tc.format, 3)) {
				t.Fatalf("Local did not mask authored Project edit: effective=%#v present=%v, want exact Local value 3", winningProjectValue, found)
			}

			explicitProjectMutation := Mutation{Action: Set, Path: tc.path, Value: int64(7)}
			explicitProjectTarget, err := ResolveTarget(workspace, explicitProjectMutation, config.EditTargetChoice{Kind: config.EditTargetProject})
			if err != nil || explicitProjectTarget.LogicalPath != project || explicitProjectTarget.Role != config.ExplicitEditProject || explicitProjectTarget.Format != tc.format {
				t.Fatalf("explicit Project target=(%#v,%v), want %s/Project/%v", explicitProjectTarget, err, project, tc.format)
			}
			explicitProjectResult, err := ApplyTargeted(workspace, explicitProjectTarget, explicitProjectMutation, ApplyOptions{})
			if err != nil || explicitProjectResult.Path != project || explicitProjectResult.Plan == nil || explicitProjectResult.Plan.Noop {
				t.Fatalf("ApplyTargeted explicit Project = (%#v,%v), want changing %s", explicitProjectResult, err, project)
			}
			explicitProjectAfter, err := os.ReadFile(project)
			if err != nil {
				t.Fatal(err)
			}
			assertMatrixDocumentValue(t, explicitProjectAfter, tc.format, tc.valuePath, matrixInteger(t, tc.format, 7))
			namedMutation := Mutation{Action: Set, Path: tc.path, Value: int64(6)}
			namedTarget, err := ResolveTarget(workspace, namedMutation, config.EditTargetChoice{Kind: config.EditTargetFile, File: project})
			if err != nil || namedTarget.LogicalPath != project || namedTarget.Role != config.ExplicitEditProject || namedTarget.Format != tc.format {
				t.Fatalf("named consumed Project target=(%#v,%v), want %s/Project/%v", namedTarget, err, project, tc.format)
			}
			namedProjectResult, err := ApplyTargeted(workspace, namedTarget, namedMutation, ApplyOptions{})
			if err != nil || namedProjectResult.Path != project || namedProjectResult.Plan == nil || namedProjectResult.Plan.Noop {
				t.Fatalf("ApplyTargeted named Project = (%#v,%v), want changing %s", namedProjectResult, err, project)
			}
			projectAfterNamed, err := os.ReadFile(project)
			if err != nil {
				t.Fatal(err)
			}
			assertMatrixDocumentValue(t, projectAfterNamed, tc.format, tc.valuePath, matrixInteger(t, tc.format, 6))

			localMutation := Mutation{Action: Set, Path: tc.path, Value: int64(5)}
			localTarget, err := ResolveTarget(workspace, localMutation, config.EditTargetChoice{Kind: config.EditTargetLocal})
			if err != nil {
				t.Fatalf("resolve authored own-jail Local target: %v", err)
			}
			if localTarget.LogicalPath != local || localTarget.Role != config.ExplicitEditLocal || localTarget.Format != tc.format {
				t.Fatalf("explicit Local target = (%s,%v,%v), want %s in %v", localTarget.LogicalPath, localTarget.Role, localTarget.Format, local, tc.format)
			}
			localResult, err := ApplyTargeted(workspace, localTarget, localMutation, ApplyOptions{})
			if err != nil || localResult.Path != local || localResult.Plan == nil || localResult.Plan.Noop {
				t.Fatalf("ApplyTargeted Local = (%#v, %v), want changing disclosed Local edit", localResult, err)
			}
			localAfter, err := os.ReadFile(local)
			if err != nil {
				t.Fatal(err)
			}
			assertMatrixDocumentValue(t, localAfter, tc.format, tc.valuePath, matrixInteger(t, tc.format, 5))
			if !bytes.Contains(localAfter, []byte("comment")) {
				t.Fatalf("Local comment was not preserved: %q", localAfter)
			}
			localInfo, err := os.Stat(local)
			if err != nil || localInfo.Mode().Perm() != 0640 {
				t.Fatalf("Local mode = %v err=%v, want 0640", localInfo, err)
			}

			namedLocalMutation := Mutation{Action: Set, Path: tc.path, Value: int64(6)}
			namedLocalTarget, err := ResolveTarget(workspace, namedLocalMutation, config.EditTargetChoice{Kind: config.EditTargetFile, File: local})
			if err != nil || namedLocalTarget.LogicalPath != local || namedLocalTarget.Role != config.ExplicitEditLocal || namedLocalTarget.Format != tc.format {
				t.Fatalf("named Local target = (%#v,%v), want %s/Local/%v", namedLocalTarget, err, local, tc.format)
			}
			namedLocalResult, err := ApplyTargeted(workspace, namedLocalTarget, namedLocalMutation, ApplyOptions{})
			if err != nil || namedLocalResult.Path != local || namedLocalResult.Plan == nil || namedLocalResult.Plan.Noop {
				t.Fatalf("ApplyTargeted named Local = (%#v,%v), want changing %s", namedLocalResult, err, local)
			}
			localNamedAfter, err := os.ReadFile(local)
			if err != nil {
				t.Fatal(err)
			}
			assertMatrixDocumentValue(t, localNamedAfter, tc.format, tc.valuePath, matrixInteger(t, tc.format, 6))
			if !bytes.Contains(localNamedAfter, []byte("comment")) {
				t.Fatalf("named Local comment was not preserved: %q", localNamedAfter)
			}

			// Consume the authored graph independently of LoadReadableConfig's own-jail delivery shortcut.
			input, err := config.NewConfigReadInput(local, localNamedAfter)
			if err != nil {
				t.Fatal(err)
			}
			authoredEffective, err := config.LoadConfigWithReadInput(workspace, true, nil, input)
			if err != nil {
				t.Fatalf("independent authored effective read: %v", err)
			}
			got := effectiveMap(t, authoredEffective, tc.valuePath[:len(tc.valuePath)-1]...)
			value, present := got.Get(tc.valuePath[len(tc.valuePath)-1])
			if !present || !reflect.DeepEqual(value, matrixEffectiveResourceInteger(t, tc.format, 6)) {
				t.Fatalf("authored effective Local winner = %#v present=%v, want exact 6", value, present)
			}

			ordinaryAfter, err := config.LoadReadableConfig(workspace, true, nil)
			if err != nil {
				t.Fatalf("ordinary assembled read after edit: %v", err)
			}
			ordinaryAfterJSON, err := config.SnapshotJSON(ordinaryAfter)
			if err != nil || ordinaryAfterJSON != assembledBytes {
				t.Fatalf("ordinary own-jail read stopped returning delivered snapshot after edit: equal=%v err=%v", ordinaryAfterJSON == assembledBytes, err)
			}
			hostOnlyAfter, found := effectiveMap(t, ordinaryAfter, "mcp_servers").Get("host_only")
			if !found || hostOnlyAfter == nil {
				t.Fatalf("delivered host-only include value disappeared after edit: %#v", ordinaryAfter)
			}
			if got, err := os.ReadFile(neighbor); err != nil || !bytes.Equal(got, neighborBefore) {
				t.Fatalf("neighbor bytes changed: %q err=%v", got, err)
			}
			assertNoTargetArtifacts(t, project)
			assertNoTargetArtifacts(t, local)
			if got, err := os.ReadFile(user); err != nil || !bytes.Equal(got, userBefore) {
				t.Fatalf("User scope changed during Project/Local edits: %q err=%v", got, err)
			}
		})
	}
}

func TestResolveTargetOwnJailOriginsDoNotAuthorizeUnconsumedOrHostFilesBothFormats(t *testing.T) {
	for _, tc := range []struct {
		name, userName, projectName, localName string
		format                                 configdoc.Format
		user, project, local, unconsumed       string
	}{
		{"JSONC", paths.UserConfigLegacyJSONCName, paths.WorkspaceConfigLegacyJSONCName, paths.WorkspaceLocalConfigLegacyJSONCName, configdoc.JSONC,
			`{"packs":["git"]}`, `{"machine":{"resources":{"cpus":2}}}`, `{"machine":{"resources":{"cpus":3}}}`, `{"machine":{"resources":{"cpus":9}}}`},
		{"TOML", paths.UserConfigTOMLName, paths.WorkspaceConfigTOMLName, paths.WorkspaceLocalConfigTOMLName, configdoc.TOML,
			"packs=['git']\n", "[machine.resources]\ncpus=2\n", "[machine.resources]\ncpus=3\n", "[machine.resources]\ncpus=9\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home, workspace := matrixCanonicalTempDir(t), matrixCanonicalTempDir(t)
			t.Setenv("HOME", home)
			t.Setenv("YOLO_VERSION", "")
			t.Setenv("YOLO_WORKSPACE", "")
			t.Setenv("YOLO_SETUP", "podman-self")
			t.Setenv(config.UserLayerEnv, "")
			user := filepath.Join(home, ".config", "yolo-jail", tc.userName)
			project := filepath.Join(workspace, tc.projectName)
			local := filepath.Join(workspace, tc.localName)
			unconsumed := filepath.Join(workspace, "not-read"+filepath.Ext(tc.projectName))
			alias := filepath.Join(workspace, "not-read-link"+filepath.Ext(tc.projectName))
			hostOnly := filepath.Join(home, ".config", "yolo-jail", "host-origin-not-read"+filepath.Ext(tc.projectName))
			writeEditFixture(t, user, tc.user)
			writeEditFixture(t, project, tc.project)
			writeEditFixture(t, local, tc.local)
			writeEditFixture(t, unconsumed, tc.unconsumed)
			writeEditFixture(t, hostOnly, tc.unconsumed)
			if err := os.Symlink(unconsumed, alias); err != nil {
				t.Fatal(err)
			}
			wantSnapshot := writeOwnJailDelivery(t, workspace)
			// The digest-valid record carries only an attacker-chosen host filename. The label is
			// a string, never a file read, and cannot make either file a consumed writer.
			forgedOrigins := forgeOwnJailOriginTree(t, workspace, unconsumed, hostOnly)
			t.Setenv("YOLO_VERSION", "test-jail")
			t.Setenv("YOLO_WORKSPACE", workspace)
			t.Setenv("YOLO_SETUP", "podman-self")
			ordinary, err := config.LoadReadableConfig(workspace, true, nil)
			if err != nil {
				t.Fatal(err)
			}
			gotSnapshot, err := config.SnapshotJSON(ordinary)
			if err != nil || gotSnapshot != wantSnapshot {
				t.Fatalf("valid delivered own-jail snapshot changed after forging diagnostics: equal=%v err=%v", gotSnapshot == wantSnapshot, err)
			}

			before := map[string][]byte{}
			modes := map[string]os.FileMode{}
			for _, path := range []string{user, project, local, unconsumed, hostOnly, forgedOrigins} {
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
			linkBefore, err := os.Readlink(alias)
			if err != nil {
				t.Fatal(err)
			}
			mutation := Mutation{Action: Set, Path: matrixPath("machine", "resources", "cpus"), Value: int64(7)}
			for _, named := range []struct {
				name, path string
			}{
				{"unconsumed file with forged origin", unconsumed},
				{"alias to unconsumed file", alias},
				{"host file named only by origin label", hostOnly},
			} {
				t.Run(named.name, func(t *testing.T) {
					_, err := ResolveTarget(workspace, mutation, config.EditTargetChoice{Kind: config.EditTargetFile, File: named.path})
					if err == nil || !strings.Contains(err.Error(), "not consumed") {
						t.Fatalf("forged host-origin label authorized %s: %v", named.path, err)
					}
				})
			}
			for path, want := range before {
				got, readErr := os.ReadFile(path)
				info, statErr := os.Stat(path)
				if readErr != nil || statErr != nil || !bytes.Equal(got, want) || info.Mode().Perm() != modes[path] {
					t.Fatalf("refusal changed %s bytes/mode: %q info=%v errs=%v/%v", path, got, info, readErr, statErr)
				}
				assertNoTargetArtifacts(t, path)
			}
			linkInfo, err := os.Lstat(alias)
			linkAfter, linkErr := os.Readlink(alias)
			if err != nil || linkErr != nil || linkInfo.Mode()&os.ModeSymlink == 0 || linkAfter != linkBefore {
				t.Fatalf("unconsumed alias changed: lstat=%v link=%q errs=%v/%v", linkInfo, linkAfter, err, linkErr)
			}
			ordinaryAfter, err := config.LoadReadableConfig(workspace, true, nil)
			if err != nil {
				t.Fatal(err)
			}
			afterSnapshot, err := config.SnapshotJSON(ordinaryAfter)
			if err != nil || afterSnapshot != wantSnapshot {
				t.Fatalf("own-jail assembled snapshot changed after refusals: equal=%v err=%v", afterSnapshot == wantSnapshot, err)
			}
		})
	}
}

func TestResolveTargetOwnJailRejectsConflictingProjectLocalPhysicalAliasesBothFormats(t *testing.T) {
	for _, tc := range []struct {
		name, userName, projectName, localName, sharedName string
		format                                             configdoc.Format
		user, project, local, shared                       string
	}{
		{"JSONC", paths.UserConfigLegacyJSONCName, paths.WorkspaceConfigLegacyJSONCName, paths.WorkspaceLocalConfigLegacyJSONCName, "shared.jsonc", configdoc.JSONC,
			`{"packs":["git"]}`, `{"include_if_found":["shared.jsonc"]}`, `{"include_if_found":["shared.jsonc"]}`, `{"machine":{"resources":{"cpus":2}}}`},
		{"TOML", paths.UserConfigTOMLName, paths.WorkspaceConfigTOMLName, paths.WorkspaceLocalConfigTOMLName, "shared.toml", configdoc.TOML,
			"packs=['git']\n", "include_if_found=['shared.toml']\n", "include_if_found=['shared.toml']\n", "[machine.resources]\ncpus=2\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home, workspace := matrixCanonicalTempDir(t), matrixCanonicalTempDir(t)
			t.Setenv("HOME", home)
			t.Setenv("YOLO_VERSION", "")
			user := filepath.Join(home, ".config", "yolo-jail", tc.userName)
			project := filepath.Join(workspace, tc.projectName)
			local := filepath.Join(workspace, tc.localName)
			shared := filepath.Join(workspace, tc.sharedName)
			alias := filepath.Join(workspace, "shared-alias"+filepath.Ext(tc.sharedName))
			writeEditFixture(t, user, tc.user)
			writeEditFixture(t, project, tc.project)
			writeEditFixture(t, local, tc.local)
			writeEditFixture(t, shared, tc.shared)
			if err := os.Symlink(shared, alias); err != nil {
				t.Fatal(err)
			}
			wantSnapshot := writeOwnJailDelivery(t, workspace)
			forgedOrigins := forgeOwnJailOriginTree(t, workspace, filepath.Join(home, "host-not-read.toml"))
			linkBefore, err := os.Readlink(alias)
			if err != nil {
				t.Fatal(err)
			}
			before := map[string][]byte{}
			modes := map[string]os.FileMode{}
			for _, path := range []string{user, project, local, shared, forgedOrigins} {
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
			t.Setenv("YOLO_VERSION", "test-jail")
			t.Setenv("YOLO_WORKSPACE", workspace)
			t.Setenv("YOLO_SETUP", "podman-self")
			for _, named := range []string{shared, alias} {
				_, err := ResolveTarget(workspace, Mutation{Action: Set, Path: matrixPath("machine", "resources", "cpus"), Value: int64(4)}, config.EditTargetChoice{Kind: config.EditTargetFile, File: named})
				if err == nil || !strings.Contains(err.Error(), "conflicting reader roles") {
					t.Fatalf("Project/Local physical alias %s was authorized: %v", named, err)
				}
			}
			for path, want := range before {
				got, readErr := os.ReadFile(path)
				info, statErr := os.Stat(path)
				if readErr != nil || statErr != nil || !bytes.Equal(got, want) || info.Mode().Perm() != modes[path] {
					t.Fatalf("role-conflict refusal changed %s bytes/mode: %q info=%v errs=%v/%v", path, got, info, readErr, statErr)
				}
				assertNoTargetArtifacts(t, path)
			}
			linkInfo, err := os.Lstat(alias)
			linkAfter, linkErr := os.Readlink(alias)
			if err != nil || linkErr != nil || linkInfo.Mode()&os.ModeSymlink == 0 || linkAfter != linkBefore {
				t.Fatalf("shared physical alias changed: info=%v link=%q errs=%v/%v", linkInfo, linkAfter, err, linkErr)
			}
			ordinary, err := config.LoadReadableConfig(workspace, true, nil)
			if err != nil {
				t.Fatal(err)
			}
			snapshot, err := config.SnapshotJSON(ordinary)
			if err != nil || snapshot != wantSnapshot {
				t.Fatalf("ordinary delivered snapshot changed after conflict refusals: equal=%v err=%v", snapshot == wantSnapshot, err)
			}
		})
	}
}

func TestResolveTargetOwnJailRejectsGeneratedAndInheritedUserCopiesBothSourceFormats(t *testing.T) {
	for _, tc := range []struct {
		name, userName, projectName, userBody, projectBody string
	}{
		{"JSONC source", paths.UserConfigLegacyJSONCName, paths.WorkspaceConfigLegacyJSONCName, `{"packs":["git"]}`, `{"machine":{"resources":{"cpus":2}}}`},
		// A TOML-written pack entry never crosses as legacy JSONC (config.RenderFilteredWithOrigins),
		// so the TOML source writes a key both formats read alike.
		{"TOML source", paths.UserConfigTOMLName, paths.WorkspaceConfigTOMLName, "[install]\nnix=['strace']\n", "[machine.resources]\ncpus=2\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home, workspace := matrixCanonicalTempDir(t), matrixCanonicalTempDir(t)
			t.Setenv("HOME", home)
			t.Setenv("YOLO_VERSION", "")
			t.Setenv("YOLO_WORKSPACE", "")
			t.Setenv("YOLO_SETUP", "podman-self")
			t.Setenv(config.UserLayerEnv, "")
			sourceUser := filepath.Join(home, ".config", "yolo-jail", tc.userName)
			project := filepath.Join(workspace, tc.projectName)
			writeEditFixture(t, sourceUser, tc.userBody)
			writeEditFixture(t, project, tc.projectBody)
			userConfig, userSources, err := config.LoadReadableUserScopeConfigWithSources(true, nil)
			if err != nil {
				t.Fatalf("read fixture User layer: %v", err)
			}
			assembledMap, assembledSources, err := config.LoadReadableConfigWithSources(workspace, true, nil)
			if err != nil {
				t.Fatal(err)
			}
			if err := config.WriteAssembledConfig(workspace, "podman-self", assembledMap); err != nil {
				t.Fatal(err)
			}
			if err := config.WriteAssembledOrigins(workspace, "podman-self", assembledMap, assembledSources); err != nil {
				t.Fatal(err)
			}
			assembled, err := config.SnapshotJSON(assembledMap)
			if err != nil {
				t.Fatal(err)
			}
			preflight, _ := config.FilterInherit(userConfig, config.InheritPreflight)
			generatedBody, err := config.RenderFilteredWithOrigins(preflight, userSources, config.InheritPreflight, "fixture")
			if err != nil {
				t.Fatal(err)
			}
			nested, _ := config.FilterInherit(userConfig, config.InheritNested)
			inheritedBody, err := config.RenderFilteredWithOrigins(nested, userSources, config.InheritNested, "fixture")
			if err != nil {
				t.Fatal(err)
			}
			generatedUser := filepath.Join(filepath.Dir(sourceUser), paths.UserConfigLegacyJSONCName)
			if sourceUser != generatedUser {
				if err := os.Remove(sourceUser); err != nil {
					t.Fatal(err)
				}
			}
			writeEditFixture(t, generatedUser, forgeGeneratedUserOrigin(t, generatedBody, filepath.Join(home, "host-stale-not-read.toml")))
			if err := os.Chmod(generatedUser, 0640); err != nil {
				t.Fatal(err)
			}
			t.Setenv("YOLO_VERSION", "test-jail")
			t.Setenv("YOLO_WORKSPACE", workspace)
			t.Setenv("YOLO_SETUP", "podman-self")
			inherited := config.InheritedLaunchPath()
			if inherited == "" || inherited == generatedUser {
				t.Fatalf("in-jail inherited path = %q, want a distinct generated path", inherited)
			}
			writeEditFixture(t, inherited, forgeGeneratedUserOrigin(t, inheritedBody, filepath.Join(home, "host-stale-not-read.toml")))
			if err := os.Chmod(inherited, 0640); err != nil {
				t.Fatal(err)
			}
			before := map[string][]byte{}
			modes := map[string]os.FileMode{}
			for _, path := range []string{generatedUser, inherited, project} {
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
			mutation := Mutation{Action: Set, Path: matrixPath("packs"), Value: []any{"other"}}
			for _, choice := range []struct {
				name   string
				choice config.EditTargetChoice
			}{
				{"automatic generated User discovery", config.EditTargetChoice{Kind: config.EditTargetAutomatic}},
				{"named generated User file", config.EditTargetChoice{Kind: config.EditTargetFile, File: generatedUser}},
				{"named inherited User file", config.EditTargetChoice{Kind: config.EditTargetFile, File: inherited}},
			} {
				t.Run(choice.name, func(t *testing.T) {
					target, resolveErr := ResolveTarget(workspace, mutation, choice.choice)
					if resolveErr == nil {
						_, applyErr := ApplyTargeted(workspace, target, mutation, ApplyOptions{})
						if applyErr == nil {
							t.Fatal("generated User target was discovered and applied")
						}
						t.Fatalf("generated User target was discoverable (%v); ApplyTargeted refusal=%v", resolveErr, applyErr)
					}
					if !strings.Contains(resolveErr.Error(), "not consumed") {
						t.Fatalf("generated User refusal did not distinguish consumption from origin labels: %v", resolveErr)
					}
					for path, want := range before {
						got, readErr := os.ReadFile(path)
						info, statErr := os.Stat(path)
						if readErr != nil || statErr != nil || !bytes.Equal(got, want) || info.Mode().Perm() != modes[path] {
							t.Fatalf("%s refusal changed generated %s: %q info=%v errs=%v/%v", choice.name, path, got, info, readErr, statErr)
						}
						assertNoTargetArtifacts(t, path)
					}
				})
			}
			ordinary, err := config.LoadReadableConfig(workspace, true, nil)
			if err != nil {
				t.Fatal(err)
			}
			snapshot, err := config.SnapshotJSON(ordinary)
			if err != nil || snapshot != assembled {
				t.Fatalf("generated User fixtures displaced delivered snapshot: equal=%v err=%v", snapshot == assembled, err)
			}
		})
	}
}

func TestApplyTargetedOwnJailDisclosesAndPreservesProjectLocalFileSymlinksBothFormats(t *testing.T) {
	for _, format := range []struct {
		name, userName, projectName, localName string
		format                                 configdoc.Format
		user, project, local                   string
	}{
		{"JSONC", paths.UserConfigLegacyJSONCName, paths.WorkspaceConfigLegacyJSONCName, paths.WorkspaceLocalConfigLegacyJSONCName, configdoc.JSONC,
			`{"packs":["git"]}`, `{"machine":{"resources":{"cpus":2}}} // Project comment`, `{"machine":{"resources":{"cpus":3}}} // Local comment`},
		{"TOML", paths.UserConfigTOMLName, paths.WorkspaceConfigTOMLName, paths.WorkspaceLocalConfigTOMLName, configdoc.TOML,
			"packs=['git']\n", "[machine.resources]\ncpus=2 # Project comment\n", "[machine.resources]\ncpus=3 # Local comment\n"},
	} {
		for _, scope := range []string{"Project", "Local"} {
			t.Run(format.name+"/"+scope, func(t *testing.T) {
				home, workspace := matrixCanonicalTempDir(t), matrixCanonicalTempDir(t)
				t.Setenv("HOME", home)
				t.Setenv("YOLO_VERSION", "")
				t.Setenv("YOLO_WORKSPACE", "")
				t.Setenv("YOLO_SETUP", "podman-self")
				user := filepath.Join(home, ".config", "yolo-jail", format.userName)
				projectLogical := filepath.Join(workspace, format.projectName)
				localLogical := filepath.Join(workspace, format.localName)
				projectPhysical := filepath.Join(workspace, "scope-files", "project"+filepath.Ext(format.projectName))
				localPhysical := filepath.Join(workspace, "scope-files", "local"+filepath.Ext(format.localName))
				projectTarget, localTarget := projectLogical, localLogical
				if scope == "Project" {
					projectTarget = projectPhysical
				} else {
					localTarget = localPhysical
				}
				writeEditFixture(t, user, format.user)
				writeEditFixture(t, projectTarget, format.project)
				writeEditFixture(t, localTarget, format.local)
				link := projectLogical
				physical := projectPhysical
				if scope == "Local" {
					link, physical = localLogical, localPhysical
				}
				if err := os.Symlink(physical, link); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(physical, 0640); err != nil {
					t.Fatal(err)
				}
				neighbor := filepath.Join(workspace, "neighbor.txt")
				writeEditFixture(t, neighbor, "neighbor stays\n")
				other := localTarget
				if scope == "Local" {
					other = projectTarget
				}
				otherBefore, err := os.ReadFile(other)
				if err != nil {
					t.Fatal(err)
				}
				neighborBefore, err := os.ReadFile(neighbor)
				if err != nil {
					t.Fatal(err)
				}
				before, err := os.ReadFile(physical)
				if err != nil {
					t.Fatal(err)
				}
				info, err := os.Stat(physical)
				if err != nil {
					t.Fatal(err)
				}
				linkBefore, err := os.Readlink(link)
				if err != nil {
					t.Fatal(err)
				}
				wantSnapshot := writeOwnJailDelivery(t, workspace)
				t.Setenv("YOLO_VERSION", "test-jail")
				t.Setenv("YOLO_WORKSPACE", workspace)
				t.Setenv("YOLO_SETUP", "podman-self")
				mutation := Mutation{Action: Set, Path: matrixPath("machine", "resources", "cpus"), Value: int64(4)}
				choice := config.EditTargetChoice{Kind: config.EditTargetAutomatic}
				if scope == "Local" {
					choice = config.EditTargetChoice{Kind: config.EditTargetFile, File: link}
				}
				target, err := ResolveTarget(workspace, mutation, choice)
				if err != nil {
					t.Fatalf("ResolveTarget %s symlink: %v", scope, err)
				}
				wantRole, wantLogical := config.ExplicitEditProject, projectLogical
				if scope == "Local" {
					wantRole, wantLogical = config.ExplicitEditLocal, localLogical
				}
				resolvedPhysical, err := filepath.EvalSymlinks(link)
				if err != nil {
					t.Fatal(err)
				}
				if target.LogicalPath != wantLogical || target.PhysicalPath != resolvedPhysical || target.Role != wantRole || target.Format != format.format {
					t.Fatalf("%s symlink target=(%s,%s,%v,%v), want logical=%s physical=%s role=%v format=%v", scope, target.LogicalPath, target.PhysicalPath, target.Role, target.Format, wantLogical, resolvedPhysical, wantRole, format.format)
				}
				result, err := ApplyTargeted(workspace, target, mutation, ApplyOptions{})
				if err != nil || result.Path != wantLogical || result.Plan == nil || result.Plan.Noop {
					t.Fatalf("ApplyTargeted %s symlink=(%#v,%v), want changing disclosed %s", scope, result, err, wantLogical)
				}
				after, err := os.ReadFile(physical)
				if err != nil || bytes.Equal(after, before) {
					t.Fatalf("physical %s target did not change: %q err=%v", scope, after, err)
				}
				assertMatrixDocumentValue(t, after, format.format, []string{"machine", "resources", "cpus"}, matrixInteger(t, format.format, 4))
				if !bytes.Contains(after, []byte("comment")) {
					t.Fatalf("physical %s target comment was not preserved: %q", scope, after)
				}
				afterInfo, err := os.Stat(physical)
				linkInfo, linkErr := os.Lstat(link)
				linkAfter, readlinkErr := os.Readlink(link)
				if err != nil || afterInfo.Mode().Perm() != info.Mode().Perm() || linkErr != nil || linkInfo.Mode()&os.ModeSymlink == 0 || readlinkErr != nil || linkAfter != linkBefore {
					t.Fatalf("%s physical/link changed: mode=%v linkinfo=%v link=%q errs=%v/%v/%v", scope, afterInfo, linkInfo, linkAfter, err, linkErr, readlinkErr)
				}
				if got, err := os.ReadFile(other); err != nil || !bytes.Equal(got, otherBefore) {
					t.Fatalf("other scope changed during %s symlink edit: %q err=%v", scope, got, err)
				}
				if got, err := os.ReadFile(neighbor); err != nil || !bytes.Equal(got, neighborBefore) {
					t.Fatalf("neighbor changed during %s symlink edit: %q err=%v", scope, got, err)
				}
				ordinary, err := config.LoadReadableConfig(workspace, true, nil)
				if err != nil {
					t.Fatal(err)
				}
				snapshot, err := config.SnapshotJSON(ordinary)
				if err != nil || snapshot != wantSnapshot {
					t.Fatalf("symlink edit changed the own-jail delivered snapshot: equal=%v err=%v", snapshot == wantSnapshot, err)
				}
				assertNoTargetArtifacts(t, physical)
			})
		}
	}
}

func writeOwnJailDelivery(t *testing.T, workspace string) string {
	t.Helper()
	assembled, sources, err := config.LoadReadableConfigWithSources(workspace, true, nil)
	if err != nil {
		t.Fatalf("assemble own-jail delivery fixture: %v", err)
	}
	if err := config.WriteAssembledConfig(workspace, "podman-self", assembled); err != nil {
		t.Fatal(err)
	}
	if err := config.WriteAssembledOrigins(workspace, "podman-self", assembled, sources); err != nil {
		t.Fatal(err)
	}
	snapshot, err := config.SnapshotJSON(assembled)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func forgeOwnJailOriginTree(t *testing.T, workspace string, labels ...string) string {
	t.Helper()
	path := config.WorkspaceAssembledOriginsPath(workspace, "podman-self")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var record map[string]json.RawMessage
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatal(err)
	}
	tree, err := json.Marshal(map[string]any{"w": labels})
	if err != nil {
		t.Fatal(err)
	}
	record["origins"] = tree
	forged, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, forged, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func forgeGeneratedUserOrigin(t *testing.T, body, label string) string {
	t.Helper()
	const prefix = "// yolo-origins: "
	start := strings.Index(body, prefix)
	if start < 0 {
		t.Fatal("format-aware generated User fixture omitted its origins record")
	}
	start += len(prefix)
	end := strings.IndexByte(body[start:], '\n')
	if end < 0 {
		t.Fatal("generated User origins record has no line terminator")
	}
	end += start
	var tree any
	if err := json.Unmarshal([]byte(body[start:end]), &tree); err != nil {
		t.Fatal(err)
	}
	var replace func(any)
	replace = func(node any) {
		switch x := node.(type) {
		case map[string]any:
			x["w"] = []any{label}
			for _, child := range x {
				replace(child)
			}
		case []any:
			for _, child := range x {
				replace(child)
			}
		}
	}
	replace(tree)
	forged, err := json.Marshal(tree)
	if err != nil {
		t.Fatal(err)
	}
	return body[:start] + string(forged) + body[end:]
}

func TestApplyTargetedOwnJailRollsBackLostAuthoredConsumptionWhenLocalMasksChange(t *testing.T) {
	home, workspace := matrixCanonicalTempDir(t), matrixCanonicalTempDir(t)
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "")
	base := filepath.Join(workspace, paths.WorkspaceConfigTOMLName)
	baseBefore := []byte("include_if_found = ['parts/edit.toml']\n")
	writeEditFixture(t, base, string(baseBefore))
	project := filepath.Join(workspace, "parts", "edit.toml")
	projectBefore := []byte("[machine.resources]\ncpus = 2 # retain project bytes\n")
	writeEditFixture(t, project, string(projectBefore))
	local := filepath.Join(workspace, paths.WorkspaceLocalConfigTOMLName)
	writeEditFixture(t, local, "[machine.resources]\ncpus = 4\n")
	writeEditFixture(t, filepath.Join(workspace, "neighbor.txt"), "neighbor stays\n")
	projectInfo, err := os.Stat(project)
	if err != nil {
		t.Fatal(err)
	}
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
	t.Setenv("YOLO_VERSION", "test-jail")
	t.Setenv("YOLO_WORKSPACE", workspace)
	t.Setenv("YOLO_SETUP", "podman-self")
	mutation := Mutation{Action: Set, Path: matrixPath("machine", "resources", "cpus"), Value: int64(4)}
	target, err := ResolveTarget(workspace, mutation, config.EditTargetChoice{Kind: config.EditTargetFile, File: project})
	if err != nil || target.Role != config.ExplicitEditProject || target.LogicalPath != project {
		t.Fatalf("resolve consumed own-jail Project include=(%#v,%v)", target, err)
	}
	originalInput, err := config.NewConfigReadInput(project, projectBefore)
	if err != nil {
		t.Fatal(err)
	}
	beforeEffective, err := config.LoadConfigWithReadInput(workspace, true, nil, originalInput)
	if err != nil {
		t.Fatal(err)
	}
	beforeSnapshot, err := config.SnapshotJSON(beforeEffective)
	if err != nil {
		t.Fatal(err)
	}
	ops := systemFileOps
	writeCount := 0
	canonicalSame, lostConsumption := false, false
	ops.WriteAtomic = func(path string, data []byte, mode os.FileMode) error {
		writeCount++
		if err := writeAtomic(path, data, mode); err != nil {
			return err
		}
		if writeCount == 1 {
			if err := os.WriteFile(base, nil, 0600); err != nil {
				return err
			}
			plannedInput, err := config.NewConfigReadInput(project, data)
			if err != nil {
				return err
			}
			afterEffective, consumed, err := config.LoadConfigWithReadInputResult(workspace, true, nil, plannedInput)
			if err != nil {
				return err
			}
			afterSnapshot, err := config.SnapshotJSON(afterEffective)
			if err != nil {
				return err
			}
			canonicalSame = beforeSnapshot == afterSnapshot
			lostConsumption = !consumed
		}
		return nil
	}
	originalOps := systemFileOps
	systemFileOps = ops
	t.Cleanup(func() { systemFileOps = originalOps })
	_, err = ApplyTargeted(workspace, target, mutation, ApplyOptions{})
	if !canonicalSame || !lostConsumption {
		t.Fatalf("lost include did not retain equal effective Local winner while dropping consumption: same=%v lost=%v", canonicalSame, lostConsumption)
	}
	if err == nil || !strings.Contains(err.Error(), "not consumed by the standard config graph") || !strings.Contains(err.Error(), "original bytes restored") {
		t.Fatalf("lost authored consumption was not rejected with rollback: %v", err)
	}
	got, readErr := os.ReadFile(project)
	gotInfo, statErr := os.Stat(project)
	if readErr != nil || statErr != nil || !bytes.Equal(got, projectBefore) || gotInfo.Mode().Perm() != projectInfo.Mode().Perm() || writeCount != 2 {
		t.Fatalf("rollback did not restore exact Project bytes/mode: bytes=%q info=%v writes=%d errs=%v/%v", got, gotInfo, writeCount, readErr, statErr)
	}
	if err := os.WriteFile(base, baseBefore, 0600); err != nil {
		t.Fatal(err)
	}
	assertNoTargetArtifacts(t, project)
}
