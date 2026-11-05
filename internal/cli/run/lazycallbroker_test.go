package run

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
)

func TestLazySelectedCallBrokerResolverRetainsSelectedProgramRootBeforeStaging(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	packRoot := filepath.Join(home, "fixture")
	module := filepath.Join(packRoot, "loopholes", "tasks")
	executable := filepath.Join(packRoot, "tools", "classifier", "classifier")
	for _, dir := range []string{module, filepath.Dir(executable), filepath.Join(home, ".config", "yolo-jail")} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	pack := "name = \"fixture\"\n[[loophole]]\nfrom = \"loopholes/tasks\"\n[[program]]\nbin = \"classifier\"\nvia = \"pack\"\nfrom = \"tools/classifier\"\nrun = \"classifier\"\n"
	if err := os.WriteFile(filepath.Join(packRoot, "pack.toml"), []byte(pack), 0600); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name":"tasks","access":"read fixture tasks","transport":"loopback-tls","grants":[{"kind":"read","text":"read fixture tasks"}],"call_broker":{"source":"fixture-tasks","front":"cli","program":"tasks","classifier":["{program:classifier}"],"scope":{"roots":"declared"},"resource":[{"type":"project"}],"cell":[{"name":"update","tables":["all"]}]}}`
	if err := os.WriteFile(filepath.Join(module, "manifest.jsonc"), []byte(manifest), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(executable, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	user := "packs = ['src/fixture']\n[pack_sources]\nsrc = '" + home + "'\n[allow.fixture-tasks]\non_request = ['all.update']\n"
	if err := os.WriteFile(filepath.Join(home, ".config", "yolo-jail", "config.toml"), []byte(user), 0600); err != nil {
		t.Fatal(err)
	}
	if len(config.AllowPackSwitchErrors()) != 0 {
		t.Fatalf("selected call-broker source rejected as unknown pack: %v", config.AllowPackSwitchErrors())
	}
	loopholes.ResetPackModules()
	t.Cleanup(loopholes.ResetPackModules)
	known, complete := loopholes.NewResolver().Known()
	if !complete || known["tasks"].CallBroker == nil || known["tasks"].CallBroker.ProgramIdentities["classifier"].SHA256 == "" {
		t.Fatalf("pre-staging selected pack lost its call-broker declaration/identity: complete=%t known=%+v", complete, known)
	}
}
