package configedit

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/configdoc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

func TestApplyTargetedEditsOnlyResolvedFileInBothFormats(t *testing.T) {
	for _, tc := range []struct {
		name, filename, before string
		format                 configdoc.Format
	}{
		{"JSONC", paths.WorkspaceConfigLegacyJSONCName, `{"machine":{"resources":{"cpus":2}}}`, configdoc.JSONC},
		{"TOML", paths.WorkspaceConfigTOMLName, "[machine.resources]\ncpus = 2\n", configdoc.TOML},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home, workspace := t.TempDir(), t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("YOLO_VERSION", "")
			path := filepath.Join(workspace, tc.filename)
			writeEditFixture(t, path, tc.before)
			neighbor := filepath.Join(workspace, "neighbor.txt")
			writeEditFixture(t, neighbor, "unchanged\n")
			beforeNeighbor, _ := os.ReadFile(neighbor)
			target, err := ResolveTarget(workspace, Mutation{Action: Set, Path: []configdoc.Step{{Key: "machine"}, {Key: "resources"}, {Key: "cpus"}}}, config.EditTargetChoice{Kind: config.EditTargetAutomatic})
			if err != nil {
				t.Fatal(err)
			}
			if target.LogicalPath != path {
				t.Fatalf("disclosed path = %q, want %q", target.LogicalPath, path)
			}
			result, err := ApplyTargeted(workspace, target, Mutation{Action: Set, Path: []configdoc.Step{{Key: "machine"}, {Key: "resources"}, {Key: "cpus"}}, Value: 4}, ApplyOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if result.Path != path || result.Plan == nil {
				t.Fatalf("result = %#v", result)
			}
			after, err := os.ReadFile(path)
			if err != nil || string(after) == tc.before {
				t.Fatalf("target was not edited: %q err=%v", after, err)
			}
			afterNeighbor, err := os.ReadFile(neighbor)
			if err != nil || string(afterNeighbor) != string(beforeNeighbor) {
				t.Fatalf("neighbor changed: %q err=%v", afterNeighbor, err)
			}
		})
	}
}

func TestApplyTargetedRejectsMutationDifferentFromResolvedIntent(t *testing.T) {
	home, workspace := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "")
	path := filepath.Join(workspace, paths.WorkspaceConfigTOMLName)
	before := "[machine.resources]\ncpus = 2\n"
	writeEditFixture(t, path, before)
	intent := Mutation{Action: Set, Path: []configdoc.Step{{Key: "machine"}, {Key: "resources"}, {Key: "cpus"}}, Value: 4}
	target, err := ResolveTarget(workspace, intent, config.EditTargetChoice{Kind: config.EditTargetAutomatic})
	if err != nil {
		t.Fatal(err)
	}
	writes := spySystemWrites(t)
	_, err = ApplyTargeted(workspace, target, Mutation{Action: Set, Path: []configdoc.Step{{Key: "machine"}, {Key: "resources"}, {Key: "memory"}}, Value: 8}, ApplyOptions{})
	if err == nil || !strings.Contains(err.Error(), "does not match the resolved target intent") || *writes != 0 {
		t.Fatalf("mismatched mutation result err=%v writes=%d", err, *writes)
	}
	got, readErr := os.ReadFile(path)
	if readErr != nil || string(got) != before {
		t.Fatalf("mismatched mutation changed target: %q err=%v", got, readErr)
	}
}

func TestApplyTargetedPassesTOMLEmptyListResetThroughSchema(t *testing.T) {
	home, workspace := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "")
	path := filepath.Join(workspace, paths.WorkspaceConfigTOMLName)
	before := "[machine]\nports = [{ host = 8080, jail = 80 }]\n"
	writeEditFixture(t, path, before)
	mutation := Mutation{Action: Set, Path: []configdoc.Step{{Key: "machine"}, {Key: "ports"}}, Value: []any{}}
	target, err := ResolveTarget(workspace, mutation, config.EditTargetChoice{Kind: config.EditTargetAutomatic})
	if err != nil {
		t.Fatal(err)
	}
	result, err := ApplyTargeted(workspace, target, mutation, ApplyOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Path != path {
		t.Fatalf("reported target %s, want %s", result.Path, path)
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) == before {
		t.Fatalf("TOML reset was not applied: %q err=%v", after, err)
	}
}

func TestApplyTargetedUsesProjectForBothScopeDespiteUserAndLocalOrigins(t *testing.T) {
	home, workspace := matrixCanonicalTempDir(t), matrixCanonicalTempDir(t)
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "")
	user := filepath.Join(home, ".config", "yolo-jail", paths.UserConfigTOMLName)
	project := filepath.Join(workspace, paths.WorkspaceConfigTOMLName)
	local := filepath.Join(workspace, paths.WorkspaceLocalConfigTOMLName)
	writeEditFixture(t, user, "[machine.resources]\ncpus = 1\n")
	writeEditFixture(t, project, "[machine.resources]\ncpus = 2\n")
	writeEditFixture(t, local, "[machine.resources]\ncpus = 3\n")
	mutation := Mutation{Action: Set, Path: []configdoc.Step{{Key: "machine"}, {Key: "resources"}, {Key: "cpus"}}, Value: 4}
	target, err := ResolveTarget(workspace, mutation, config.EditTargetChoice{Kind: config.EditTargetAutomatic})
	if err != nil {
		t.Fatal(err)
	}
	if target.LogicalPath != project {
		t.Fatalf("Both-scope target = %s, want project file %s", target.LogicalPath, project)
	}
	result, err := ApplyTargeted(workspace, target, mutation, ApplyOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Path != project {
		t.Fatalf("reported path = %s, want %s", result.Path, project)
	}
	projectAfter, err := os.ReadFile(project)
	if err != nil {
		t.Fatal(err)
	}
	assertMatrixDocumentValue(t, projectAfter, configdoc.TOML, []string{"machine", "resources", "cpus"}, int64(4))
	effective, err := config.LoadReadableConfig(workspace, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := effectiveMap(t, effective, "machine", "resources").Get("cpus")
	if fmt.Sprint(got) != "3" {
		t.Fatalf("effective Local winner = %#v, want 3", got)
	}
	for _, path := range []string{user, local} {
		got, readErr := os.ReadFile(path)
		want := "[machine.resources]\ncpus = " + map[string]string{user: "1", local: "3"}[path] + "\n"
		if readErr != nil || string(got) != want {
			t.Fatalf("non-target %s changed: %q err=%v", path, got, readErr)
		}
	}
}

func TestApplyTargetedRejectsJailedUserTargetsBeforeEveryMode(t *testing.T) {
	for _, mode := range []string{"edit", "dry-run", "noop"} {
		t.Run(mode, func(t *testing.T) {
			home, workspace := t.TempDir(), t.TempDir()
			t.Setenv("HOME", home)
			user := filepath.Join(home, ".config", "yolo-jail", paths.UserConfigTOMLName)
			before := "[allow.git]\nalways = ['fetch']\n"
			writeEditFixture(t, user, before)
			choices := []config.EditTargetChoice{{Kind: config.EditTargetAutomatic}, {Kind: config.EditTargetUser}, {Kind: config.EditTargetFile, File: user}}
			for _, choice := range choices {
				t.Setenv("YOLO_VERSION", "")
				t.Setenv("YOLO_WORKSPACE", "")
				t.Setenv("YOLO_SETUP", "podman-self")
				mutation := Mutation{Action: Set, Path: []configdoc.Step{{Key: "allow"}, {Key: "git"}, {Key: "always"}}, Value: []any{"fetch"}}
				if mode != "noop" {
					mutation = Mutation{Action: Append, Path: []configdoc.Step{{Key: "allow"}, {Key: "git"}, {Key: "always"}}, Value: "push"}
				}
				target, err := ResolveTarget(workspace, mutation, choice)
				if err != nil {
					t.Fatal(err)
				}
				writes := spySystemWrites(t)
				if err := os.MkdirAll(filepath.Dir(config.WorkspaceAssembledConfigPath(workspace, "podman-self")), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(config.WorkspaceAssembledConfigPath(workspace, "podman-self"), []byte(`{"allow":{"git":{"always":["fetch"]}}}`), 0600); err != nil {
					t.Fatal(err)
				}
				t.Setenv("YOLO_VERSION", "test-jail")
				t.Setenv("YOLO_WORKSPACE", workspace)
				t.Setenv("YOLO_SETUP", "podman-self")
				_, err = ApplyTargeted(workspace, target, mutation, ApplyOptions{DryRun: mode == "dry-run"})
				assertJailedUserRefusal(t, err, user, *writes)
				got, readErr := os.ReadFile(user)
				if readErr != nil || string(got) != before {
					t.Fatalf("jailed refusal changed user file: %q err=%v", got, readErr)
				}
			}
		})
	}
}

func TestApplyTargetedRejectsStalePreimageAndUserRequestsBoundary(t *testing.T) {
	home, workspace := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "")
	project := filepath.Join(workspace, config.WorkspaceConfigName)
	writeEditFixture(t, project, `{}`)
	target, err := ResolveTarget(workspace, Mutation{Action: Set, Path: []configdoc.Step{{Key: "machine"}, {Key: "resources"}, {Key: "cpus"}}}, config.EditTargetChoice{Kind: config.EditTargetAutomatic})
	if err != nil {
		t.Fatal(err)
	}
	writeEditFixture(t, project, `{"machine":{"resources":{"cpus":2}}}`)
	if _, err := ApplyTargeted(workspace, target, Mutation{Action: Set, Path: []configdoc.Step{{Key: "machine"}, {Key: "resources"}, {Key: "cpus"}}, Value: 4}, ApplyOptions{}); err == nil || !strings.Contains(err.Error(), "changed since it was planned") {
		t.Fatalf("stale expected preimage result = %v", err)
	}
	if _, err := ResolveTarget(workspace, Mutation{Action: Set, Path: []configdoc.Step{{Key: "requests"}, {Key: "git"}}}, config.EditTargetChoice{Kind: config.EditTargetUser}); err == nil || !strings.Contains(err.Error(), "move requests to the workspace config") {
		t.Fatalf("User requests were not refused with the next step: %v", err)
	}
}
