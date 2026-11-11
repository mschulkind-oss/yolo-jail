package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/configdoc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

func TestResolveEditTargetUsesSchemaDefaultsAndReaderRoles(t *testing.T) {
	home, workspace := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "")
	user := filepath.Join(home, ".config", "yolo-jail", paths.UserConfigTOMLName)
	userFragment := filepath.Join(filepath.Dir(user), "config.d", "zz.toml")
	project := filepath.Join(workspace, paths.WorkspaceConfigLegacyJSONCName)
	local := filepath.Join(workspace, paths.WorkspaceLocalConfigTOMLName)
	writeTargetFixture(t, user, "[allow.git]\nalways = ['fetch']\n[machine.resources]\ncpus = 1\n")
	writeTargetFixture(t, userFragment, "[allow.git]\non_request = ['push']\n")
	writeTargetFixture(t, project, `{"machine":{"resources":{"cpus":2}}}`)
	writeTargetFixture(t, local, "[machine.resources]\ncpus = 3\n")

	cases := []struct {
		name   string
		key    []configdoc.Step
		choice EditTargetChoice
		want   string
		role   ExplicitEditScope
		format configdoc.Format
	}{
		{"User existing section origin", []configdoc.Step{{Key: "allow"}, {Key: "git"}, {Key: "always"}}, EditTargetChoice{Kind: EditTargetAutomatic}, user, ExplicitEditUser, configdoc.TOML},
		{"User config.d precedence", []configdoc.Step{{Key: "allow"}, {Key: "git"}, {Key: "on_request"}}, EditTargetChoice{Kind: EditTargetAutomatic}, userFragment, ExplicitEditUser, configdoc.TOML},
		{"Both defaults Project over User origin", []configdoc.Step{{Key: "machine"}, {Key: "resources"}, {Key: "cpus"}}, EditTargetChoice{Kind: EditTargetAutomatic}, project, ExplicitEditProject, configdoc.JSONC},
		{"explicit Local", []configdoc.Step{{Key: "machine"}, {Key: "resources"}, {Key: "cpus"}}, EditTargetChoice{Kind: EditTargetLocal}, local, ExplicitEditLocal, configdoc.TOML},
		{"explicit User", []configdoc.Step{{Key: "allow"}, {Key: "git"}, {Key: "always"}}, EditTargetChoice{Kind: EditTargetUser}, user, ExplicitEditUser, configdoc.TOML},
		{"named consumed User file", []configdoc.Step{{Key: "allow"}, {Key: "git"}, {Key: "on_request"}}, EditTargetChoice{Kind: EditTargetFile, File: userFragment}, userFragment, ExplicitEditUser, configdoc.TOML},
		{"explicit Project", []configdoc.Step{{Key: "machine"}, {Key: "resources"}, {Key: "cpus"}}, EditTargetChoice{Kind: EditTargetProject}, project, ExplicitEditProject, configdoc.JSONC},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ResolveEditTarget(workspace, targetIntent(tc.key), tc.choice)
			if err != nil {
				t.Fatal(err)
			}
			physical, err := filepath.EvalSymlinks(tc.want)
			if err != nil {
				t.Fatal(err)
			}
			if got.LogicalPath != tc.want || got.PhysicalPath != physical || got.Role != tc.role || got.Format != tc.format {
				t.Fatalf("resolved target = (%q,%q,%v,%v), want (%q,%q,%v,%v)", got.LogicalPath, got.PhysicalPath, got.Role, got.Format, tc.want, physical, tc.role, tc.format)
			}
			if got.Target.scope != tc.role {
				t.Fatalf("bound target role = %v, want %v", got.Target.scope, tc.role)
			}
			pre := got.ExpectedBytes()
			pre[0] ^= 0xff
			actual, err := os.ReadFile(got.PhysicalPath)
			if err != nil || string(actual) == string(pre) {
				t.Fatalf("expected preimage is not detached: err=%v", err)
			}
		})
	}
}

func TestResolveEditTargetKeepsConsumedLeafSymlinkUnderAliasedParent(t *testing.T) {
	realHome := t.TempDir()
	homeAlias := filepath.Join(t.TempDir(), "home")
	if err := os.Symlink(realHome, homeAlias); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", homeAlias)
	t.Setenv("YOLO_VERSION", "")
	userDir := filepath.Join(realHome, ".config", "yolo-jail")
	real := filepath.Join(userDir, "real.toml")
	logical := filepath.Join(homeAlias, ".config", "yolo-jail", paths.UserConfigTOMLName)
	writeTargetFixture(t, real, "[allow.git]\nalways = ['fetch']\n")
	if err := os.Symlink(real, logical); err != nil {
		t.Fatal(err)
	}

	got, err := ResolveEditTarget(t.TempDir(), targetIntent([]configdoc.Step{{Key: "allow"}, {Key: "git"}, {Key: "always"}}), EditTargetChoice{Kind: EditTargetFile, File: logical})
	if err != nil {
		t.Fatalf("consumed config symlink was rejected through its aliased parent: %v", err)
	}
	physical, err := filepath.EvalSymlinks(real)
	if err != nil {
		t.Fatal(err)
	}
	if got.LogicalPath != logical || got.PhysicalPath != physical {
		t.Fatalf("target = logical %q physical %q, want %q / %q", got.LogicalPath, got.PhysicalPath, logical, physical)
	}

	unconsumed := filepath.Join(filepath.Dir(logical), "unconsumed.toml")
	if err := os.Symlink(real, unconsumed); err != nil {
		t.Fatal(err)
	}
	_, err = ResolveEditTarget(t.TempDir(), targetIntent([]configdoc.Step{{Key: "allow"}, {Key: "git"}, {Key: "always"}}), EditTargetChoice{Kind: EditTargetFile, File: unconsumed})
	if err == nil || !strings.Contains(err.Error(), "was not read as a logical file") {
		t.Fatalf("unconsumed symlink alias under the aliased parent was accepted: %v", err)
	}
}

func TestResolveEditTargetRejectsJSONCSourceChangedBeforePreimageBinding(t *testing.T) {
	for _, tc := range []struct {
		name, before, fresh, local string
	}{
		{name: "legacy to canonical", before: `{"resources":{"cpus":3}}`, fresh: `{"machine":{"resources":{"cpus":7}}}`},
		{name: "canonical to legacy", before: `{"machine":{"resources":{"cpus":3}}}`, fresh: `{"resources":{"cpus":7}}`},
		{name: "absent to legacy without local winner", before: `{"resources":{"memory":"8g"}}`, fresh: `{"resources":{"memory":"8g","cpus":7}}`},
		{name: "absent to legacy with local winner", before: `{"resources":{"memory":"8g"}}`, fresh: `{"resources":{"memory":"8g","cpus":7}}`, local: "[machine.resources]\ncpus = 3\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home, workspace := t.TempDir(), t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("YOLO_VERSION", "")
			physical := filepath.Join(workspace, "project-real.jsonc")
			logical := filepath.Join(workspace, paths.WorkspaceConfigLegacyJSONCName)
			writeTargetFixture(t, physical, tc.before)
			if tc.local != "" {
				writeTargetFixture(t, filepath.Join(workspace, paths.WorkspaceLocalConfigTOMLName), tc.local)
			}
			if err := os.Symlink(physical, logical); err != nil {
				t.Fatal(err)
			}
			modeBefore, err := os.Stat(physical)
			if err != nil {
				t.Fatal(err)
			}
			linkBefore, err := os.Readlink(logical)
			if err != nil {
				t.Fatal(err)
			}
			readCalls := 0
			key := []configdoc.Step{{Key: "machine"}, {Key: "resources"}, {Key: "cpus"}}
			_, err = resolveEditTarget(workspace, EditMutationIntent{Key: key, Kind: EditMutationRemove}, EditTargetChoice{Kind: EditTargetProject}, func(path string) ([]byte, error) {
				readCalls++
				if path != physical {
					t.Fatalf("preimage reader path=%q, want selected physical file %q", path, physical)
				}
				actualBefore, err := os.ReadFile(path)
				if err != nil || string(actualBefore) != tc.before {
					t.Fatalf("preimage hook did not start from the graph's original fixture: got=%q want=%q err=%v", actualBefore, tc.before, err)
				}
				if err := os.WriteFile(path, []byte(tc.fresh), 0o600); err != nil {
					return nil, err
				}
				return os.ReadFile(path)
			})
			if err == nil || !strings.Contains(err.Error(), "reread the config and retry") {
				t.Fatalf("stale authored mapping was accepted: %v", err)
			}
			if readCalls != 1 {
				t.Fatalf("preimage reader called %d times, want one fail-closed read", readCalls)
			}
			if got, err := os.ReadFile(physical); err != nil || string(got) != tc.fresh {
				t.Fatalf("resolver altered hook-written postimage: got=%q want=%q err=%v", got, tc.fresh, err)
			}
			modeAfter, err := os.Stat(physical)
			if err != nil || modeAfter.Mode().Perm() != modeBefore.Mode().Perm() {
				t.Fatalf("resolver changed target mode: before=%v after=%v err=%v", modeBefore.Mode(), modeAfter.Mode(), err)
			}
			linkAfter, err := os.Readlink(logical)
			if err != nil || linkAfter != linkBefore {
				t.Fatalf("resolver changed target symlink: before=%q after=%q err=%v", linkBefore, linkAfter, err)
			}
		})
	}
}

func TestResolveEditTargetUsesLiteralDottedKeyAndRejectsUnconsumedFile(t *testing.T) {
	home, workspace := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "")
	user := filepath.Join(home, ".config", "yolo-jail", paths.UserConfigTOMLName)
	project := filepath.Join(workspace, paths.WorkspaceConfigTOMLName)
	unconsumed := filepath.Join(workspace, "not-read.toml")
	unconsumedAlias := filepath.Join(workspace, "not-read-link.toml")
	writeTargetFixture(t, user, "[allow.git]\nalways = ['fetch']\n")
	writeTargetFixture(t, project, "[machine.resources]\ncpus = 2\n[providers.\"global.openai.test\"]\nregion = 'us-east-1'\n")
	writeTargetFixture(t, unconsumed, "[machine.resources]\ncpus = 4\n")
	if err := os.Symlink(project, unconsumedAlias); err != nil {
		t.Fatal(err)
	}
	key := []configdoc.Step{{Key: "providers"}, {Key: "global.openai.test"}, {Key: "region"}}
	got, err := ResolveEditTarget(workspace, targetIntent(key), EditTargetChoice{Kind: EditTargetAutomatic})
	if err != nil {
		t.Fatal(err)
	}
	if got.Role != ExplicitEditProject || got.LogicalPath != project {
		t.Fatalf("literal dotted key target = %#v", got)
	}
	if _, err := ResolveEditTarget(workspace, targetIntent([]configdoc.Step{{Key: "machine"}, {Key: "resources"}, {Key: "cpus"}}), EditTargetChoice{Kind: EditTargetFile, File: unconsumed}); err == nil {
		t.Fatal("unconsumed named config file was accepted")
	}
	if _, err := ResolveEditTarget(workspace, targetIntent([]configdoc.Step{{Key: "machine"}, {Key: "resources"}, {Key: "cpus"}}), EditTargetChoice{Kind: EditTargetFile, File: unconsumedAlias}); err == nil {
		t.Fatal("unconsumed symlink to a consumed file was accepted")
	}
}

func TestResolveEditTargetKeepsUserFragmentSymlinkPathAndPhysicalFile(t *testing.T) {
	home, workspace := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "")
	userDir := filepath.Join(home, ".config", "yolo-jail")
	user := filepath.Join(userDir, paths.UserConfigTOMLName)
	fragmentDir := filepath.Join(userDir, "config.d")
	alias := filepath.Join(fragmentDir, "a-link.toml")
	real := filepath.Join(fragmentDir, "z-real.toml")
	writeTargetFixture(t, user, "[allow.git]\nalways = ['fetch']\n")
	writeTargetFixture(t, real, "[allow.git]\non_request = ['push']\n")
	if err := os.Symlink(real, alias); err != nil {
		t.Fatal(err)
	}
	got, err := ResolveEditTarget(workspace, targetIntent([]configdoc.Step{{Key: "allow"}, {Key: "git"}, {Key: "on_request"}}), EditTargetChoice{Kind: EditTargetAutomatic})
	if err != nil {
		t.Fatal(err)
	}
	physical, err := filepath.EvalSymlinks(real)
	if err != nil {
		t.Fatal(err)
	}
	if got.LogicalPath != alias || got.PhysicalPath != physical {
		t.Fatalf("symlink target = logical %s physical %s, want %s / %s", got.LogicalPath, got.PhysicalPath, alias, physical)
	}
}

func TestResolveEditTargetUsesAuthoredFilesBehindAssembledSnapshot(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("YOLO_WORKSPACE", workspace)
	t.Setenv("YOLO_SETUP", "podman-self")
	user := filepath.Join(home, ".config", "yolo-jail", paths.UserConfigTOMLName)
	project := filepath.Join(workspace, paths.WorkspaceConfigTOMLName)
	writeTargetFixture(t, user, "[allow.git]\nalways = ['fetch']\n")
	writeTargetFixture(t, project, "[machine.resources]\ncpus = 2\n")
	assembled, sources, err := LoadReadableConfigWithSources(workspace, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(WorkspaceAssembledConfigPath(workspace, "podman-self")), 0700); err != nil {
		t.Fatal(err)
	}
	if err := WriteAssembledConfig(workspace, "podman-self", assembled); err != nil {
		t.Fatal(err)
	}
	if err := WriteAssembledOrigins(workspace, "podman-self", assembled, sources); err != nil {
		t.Fatal(err)
	}
	t.Setenv("YOLO_VERSION", "test-jail")
	cpus := targetIntent([]configdoc.Step{{Key: "machine"}, {Key: "resources"}, {Key: "cpus"}})
	got, err := ResolveEditTarget(workspace, cpus, EditTargetChoice{Kind: EditTargetAutomatic})
	if err != nil {
		t.Fatalf("authored Project was not discovered behind assembled snapshot: %v", err)
	}
	if got.Role != ExplicitEditProject || got.LogicalPath != project || got.Format != configdoc.TOML {
		t.Fatalf("authored Project target = (%s,%v,%v), want %s/TOML", got.LogicalPath, got.Role, got.Format, project)
	}
	userTarget, err := ResolveEditTarget(workspace, targetIntent([]configdoc.Step{{Key: "allow"}, {Key: "git"}, {Key: "always"}}), EditTargetChoice{Kind: EditTargetAutomatic})
	if err != nil || userTarget.LogicalPath != user || userTarget.Role != ExplicitEditUser {
		t.Fatalf("authored User target = (%#v, %v), want %s/User", userTarget, err, user)
	}
	if _, err := ResolveEditTarget(workspace, cpus, EditTargetChoice{Kind: EditTargetFile, File: WorkspaceAssembledConfigPath(workspace, "podman-self")}); err == nil || !strings.Contains(err.Error(), "not consumed") {
		t.Fatalf("delivered assembled copy was accepted as a named writable file: %v", err)
	}
	if err := os.Remove(project); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveEditTarget(workspace, cpus, EditTargetChoice{Kind: EditTargetAutomatic}); err == nil {
		t.Fatal("snapshot-only Project value supplied a writable target after authored file disappeared")
	}
	stillAssembled, err := LoadReadableConfig(workspace, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	stillAssembledJSON, err := SnapshotJSON(stillAssembled)
	wantSnapshot, wantErr := SnapshotJSON(assembled)
	if err != nil || wantErr != nil || stillAssembledJSON != wantSnapshot {
		t.Fatalf("ordinary own-jail reader did not retain delivered snapshot: got=%q want=%q errs=%v/%v", stillAssembledJSON, wantSnapshot, err, wantErr)
	}
	for path, before := range map[string]string{user: "[allow.git]\nalways = ['fetch']\n", project: "[machine.resources]\ncpus = 2\n"} {
		got, err := os.ReadFile(path)
		if os.IsNotExist(err) && path == project {
			continue
		}
		if err != nil || string(got) != before {
			t.Fatalf("snapshot refusal changed %s: %q err=%v", path, got, err)
		}
	}
}

func TestResolveEditTargetRejectsConflictingAliasedReaderRoles(t *testing.T) {
	home, workspace := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "")
	shared := filepath.Join(workspace, "shared.toml")
	writeTargetFixture(t, shared, "[machine.resources]\ncpus = 2\n")
	writeTargetFixture(t, filepath.Join(workspace, paths.WorkspaceConfigTOMLName), "include_if_found = ['shared.toml']\n")
	writeTargetFixture(t, filepath.Join(workspace, paths.WorkspaceLocalConfigTOMLName), "include_if_found = ['shared.toml']\n")
	_, err := ResolveEditTarget(workspace, targetIntent([]configdoc.Step{{Key: "machine"}, {Key: "resources"}, {Key: "cpus"}}), EditTargetChoice{Kind: EditTargetFile, File: shared})
	if err == nil || !strings.Contains(err.Error(), "conflicting reader roles") {
		t.Fatalf("aliased role conflict = %v", err)
	}
}

func TestResolveEditTargetRejectsUserOnlyBoundaryAndMissingDefault(t *testing.T) {
	home, workspace := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "")
	if _, err := ResolveEditTarget(workspace, targetIntent([]configdoc.Step{{Key: "allow"}, {Key: "git"}, {Key: "always"}}), EditTargetChoice{Kind: EditTargetProject}); err == nil {
		t.Fatal("User-only key accepted a Project override")
	}
	if _, err := ResolveEditTarget(workspace, targetIntent([]configdoc.Step{{Key: "machine"}, {Key: "resources"}, {Key: "cpus"}}), EditTargetChoice{Kind: EditTargetAutomatic}); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("missing existing-file default = %v", err)
	}
}

func targetIntent(key []configdoc.Step) EditMutationIntent {
	return EditMutationIntent{Key: key, Kind: EditMutationSet}
}

func writeTargetFixture(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
}
