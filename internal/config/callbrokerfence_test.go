package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

type callBrokerFenceResolver map[string]LoopholeInfo

func (r callBrokerFenceResolver) Known() (map[string]LoopholeInfo, bool) { return r, true }

func TestValidateConfigFencesSelectedCallBrokerCredentialPathWithoutLegacyBrokeredBlock(t *testing.T) {
	t.Setenv("YOLO_VERSION", "")
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	workspace, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	credential := filepath.Join(home, ".config", "tasks")
	if err := os.MkdirAll(credential, 0o700); err != nil {
		t.Fatal(err)
	}
	mount := credential + ":/ctx/secret"
	if err := os.WriteFile(filepath.Join(workspace, WorkspaceConfigName), []byte(`{"mounts":["`+mount+`"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	pack := selectedCallBrokerFixturePack(t, t.TempDir(), "~/.config/tasks", []string{"working_set", "all"})
	resolver := explicitPackLoopholeResolver{packs: []*packload.Pack{pack}}
	configuration := decode(t, `{"mounts":["`+mount+`"]}`)
	var directErrors, directWarnings []string
	validateBrokerMountFence(configuration, workspace, resolver, &directErrors, &directWarnings)
	if len(directErrors) == 0 {
		t.Fatalf("selected call-broker credential path was not fenced without Brokered metadata: warnings=%v", directWarnings)
	}
	errs, _ := ValidateConfig(configuration, workspace, resolver)
	joined := strings.Join(errs, "\n")
	if !strings.Contains(joined, "A workspace config may not mount it") || !strings.Contains(joined, "tasks") {
		t.Fatalf("workspace credential mount was not refused: %v", errs)
	}
}

func TestValidateConfigFencesSelectedCallBrokerStateDirectory(t *testing.T) {
	t.Setenv("YOLO_VERSION", "")
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	workspace, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	state, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	mount := state + ":/ctx/secret"
	if err := os.WriteFile(filepath.Join(workspace, WorkspaceConfigName), []byte(`{"mounts":["`+mount+`"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	resolver := callBrokerFenceResolver{"local-tasks-broker": {Name: "local-tasks-broker", CallBroker: &loopholedecl.CallBroker{Source: "local-tasks"}, StateDir: state}}
	if fences := brokerMountFences(resolver); len(fences) == 0 {
		t.Fatal("selected call broker did not arm state-directory mount fence")
	}
	configuration := decode(t, `{"mounts":["`+mount+`"]}`)
	if len(mountHostSources(configuration)) == 0 {
		t.Fatal("fixture mount did not parse")
	}
	if _, ok := brokerMountFences(resolver).reach(state); !ok {
		t.Fatal("fixture mount source did not reach call-broker fence")
	}
	var directErrors, directWarnings []string
	validateBrokerMountFence(configuration, workspace, resolver, &directErrors, &directWarnings)
	if len(directErrors) == 0 {
		t.Fatalf("direct mount-fence consumer did not refuse: warnings=%v mounts=%v", directWarnings, mountHostSources(configuration))
	}
	errs, _ := ValidateConfig(configuration, workspace, resolver)
	joined := strings.Join(errs, "\n")
	if !strings.Contains(joined, "A workspace config may not mount it") || !strings.Contains(joined, "local-tasks-broker") {
		t.Fatalf("read-only mount into selected call-broker state was not fenced: %v", errs)
	}
}
