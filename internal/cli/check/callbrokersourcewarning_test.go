package check

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// The merged-config warning is published by sectionMergedConfig. A TOML-selected pack's
// call_broker.source is live just like a legacy brokered.source, while an unrelated source
// still receives the typo warning and advice for either manifest spelling.
func TestCheckDoesNotWarnForASelectedCallBrokerSource(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("YOLO_USER_LAYER", "")
	userConfig := filepath.Join(home, ".config", "yolo-jail", paths.UserConfigLegacyJSONCName)
	writeCheckFile(t, userConfig, `{"brokered":{"local-tasks":{},"not-selected":{}}}`, 0o600)
	merged, err := config.LoadReadableUserScopeConfig(true, func(string) {})
	if err != nil {
		t.Fatal(err)
	}

	root := canonicalTempDir(t)
	module := filepath.Join(root, "loopholes", "tasks")
	classifierDir := filepath.Join(root, "tools", "classifier")
	if err := os.MkdirAll(module, 0o755); err != nil {
		t.Fatal(err)
	}
	writeCheckFile(t, filepath.Join(classifierDir, "classifier"), "#!/bin/sh\nexit 0\n", 0o755)
	writeCheckFile(t, filepath.Join(module, "manifest.jsonc"), `{"name":"tasks","transport":"loopback-tls","call_broker":{"source":"local-tasks","front":"cli","program":"tasks","classifier":["{program:classifier}"],"scope":{"roots":"declared"},"resource":[{"type":"project"}],"cell":[{"name":"rerun_workflows","tables":["working_set","all"],"default":{"working_set":false}}]}}`, 0o644)
	writeCheckFile(t, filepath.Join(root, "pack.toml"), `name = "fixture"
[[program]]
bin = "classifier"
via = "pack"
from = "tools/classifier"
run = "classifier"
[[loophole]]
from = "loopholes/tasks"
`, 0o644)
	pack, problems := packload.LoadDir(root, "fixture")
	if len(problems) != 0 {
		t.Fatalf("fixture TOML pack: %v", problems)
	}
	modules, refusals, warnings := pack.LoopholeModules()
	if len(refusals) != 0 || len(warnings) != 0 || len(modules) != 1 || modules[0].Decl == nil ||
		modules[0].Decl.CallBroker == nil || modules[0].Decl.CallBroker.Source != "local-tasks" {
		t.Fatalf("selected TOML pack call-broker modules=%+v refusals=%v warnings=%v", modules, refusals, warnings)
	}
	restore := loopholes.SnapshotPackModules()
	t.Cleanup(restore)
	loopholes.SetPackModules([]loopholes.PackModule{{Dir: modules[0].Dir, Pack: pack.Name, PackRoot: pack.Root, HostExecApproved: true}})
	known, ok := loopholes.NewResolver().Known()
	info, exists := known["tasks"]
	if !ok || !exists || info.CallBroker == nil {
		t.Fatalf("selected TOML call-broker was not discovered: %+v", known)
	}
	identity, found := info.CallBroker.ProgramIdentities["classifier"]
	if !found || identity.Path != filepath.Join(classifierDir, "classifier") {
		t.Fatalf("selected TOML call-broker has no resolved native program identity: %+v", info.CallBroker)
	}

	var out bytes.Buffer
	r := newReporter(&out, false)
	o := &Options{Getenv: func(string) string { return "" }, LookPath: func(string) (string, bool) { return "", false }}
	o.sectionMergedConfig(r, merged, t.TempDir(), merged, nil, nil, false)
	got := stripANSI(out.String())
	if strings.Contains(got, "config.brokered.local-tasks: no loophole a selected pack ships brokers") {
		t.Fatalf("the selected call-broker's active source was reported inert:\n%s", got)
	}
	for _, want := range []string{
		"config.brokered.not-selected: no loophole a selected pack ships brokers the source 'not-selected'",
		"a selected loophole manifest's `brokered.source` or `call_broker.source`",
		"select its pack with `yolo pack add <name>`",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("check output lacks %q:\n%s", want, got)
		}
	}
}
