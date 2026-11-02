package configedit

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/configdoc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

func TestApplyTargetedValidatesResourcesFromGeneratedInheritedOrigins(t *testing.T) {
	for _, tc := range []struct {
		name, hostConfig, wantExt string
	}{
		{name: "TOML", hostConfig: `{"resources":{"memory":"8g"}}`, wantExt: ".toml"},
		{name: "JSONC fallback", hostConfig: `{"resources":{"memory":"8g"},"model_facts":{"m1":{"context_window":1000}}}`, wantExt: ".jsonc"},
	} {
		for _, create := range []bool{false, true} {
			caseName := "edit"
			if create {
				caseName = "create"
			}
			t.Run(tc.name+"/"+caseName, func(t *testing.T) {
				hostHome, workspace := matrixCanonicalTempDir(t), matrixCanonicalTempDir(t)
				t.Setenv("HOME", hostHome)
				t.Setenv("YOLO_VERSION", "")
				t.Setenv(config.UserLayerEnv, "")
				userDir := filepath.Join(hostHome, ".config", "yolo-jail")
				writeEditFixture(t, filepath.Join(userDir, paths.UserConfigLegacyJSONCName), tc.hostConfig)
				effective, sources, err := config.LoadConfigWithoutWorkspaceFileWithSources(workspace, false, nil)
				if err != nil {
					t.Fatal(err)
				}
				filtered, unknown, err := config.FilterInheritErr(effective, config.InheritNested)
				if err != nil || len(unknown) != 0 {
					t.Fatalf("inherit filter unknown=%v err=%v", unknown, err)
				}
				body, format, fallback, err := config.RenderFilteredForDelivery(filtered, sources, config.InheritNested, "")
				if err != nil {
					t.Fatal(err)
				}
				wantFormat := configdoc.TOML
				if strings.HasSuffix(tc.wantExt, ".jsonc") {
					wantFormat = configdoc.JSONC
				}
				if format != wantFormat || (wantFormat == configdoc.JSONC && fallback == "") || (wantFormat == configdoc.TOML && fallback != "") {
					t.Fatalf("renderer format=%v fallback=%q; want %v", format, fallback, wantFormat)
				}

				jailHome := matrixCanonicalTempDir(t)
				t.Setenv("HOME", jailHome)
				t.Setenv("YOLO_VERSION", "9.9.9-test")
				jailDir := filepath.Join(jailHome, ".config", "yolo-jail")
				writeEditFixture(t, filepath.Join(jailDir, paths.UserConfigLegacyJSONCName), `{}`)
				generatedName := "inherited-launch" + tc.wantExt
				writeEditFixture(t, filepath.Join(jailDir, generatedName), body)

				project := filepath.Join(workspace, paths.WorkspaceConfigTOMLName)
				if !create {
					writeEditFixture(t, project, "[machine.resources]\ncpus = 2\n")
				}
				action := Set
				if create {
					action = Add
				}
				mutation := Mutation{Action: action, Path: []configdoc.Step{{Key: "machine"}, {Key: "resources"}, {Key: "cpus"}}, Value: 4}
				target, err := ResolveTarget(workspace, mutation, config.EditTargetChoice{Kind: config.EditTargetAutomatic})
				if err != nil {
					t.Fatal(err)
				}
				if _, err := ApplyTargeted(workspace, target, mutation, ApplyOptions{}); err != nil {
					t.Fatalf("valid Project %s was rejected with real generated inherited %s resources: %v", caseName, tc.name, err)
				}
				got, err := config.LoadReadableConfig(workspace, true, nil)
				if err != nil {
					t.Fatal(err)
				}
				resources, ok := got.Get("machine")
				if !ok {
					t.Fatal("public workspace reader lost the edited machine config")
				}
				machine, ok := resources.(interface{ Get(string) (any, bool) })
				if !ok {
					t.Fatalf("machine = %T, want mapping", resources)
				}
				resourcesValue, ok := machine.Get("resources")
				if !ok {
					t.Fatalf("machine resources missing: %#v", resources)
				}
				resourceMap, ok := resourcesValue.(interface{ Get(string) (any, bool) })
				if !ok {
					t.Fatalf("resources = %T, want mapping", resourcesValue)
				}
				if cpus, _ := resourceMap.Get("cpus"); cpus == nil {
					t.Fatalf("public reader lost the Project cpus value: %#v", resourcesValue)
				}
				if memory, _ := resourceMap.Get("memory"); memory != "8g" {
					t.Fatalf("public reader lost inherited memory: %#v", resourcesValue)
				}
			})
		}
	}
}
