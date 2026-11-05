package loopholes

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func callBrokerProgramFixture(t *testing.T, classifier, executor string) (string, string) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	module := filepath.Join(root, "loopholes", "tasks")
	if err := os.MkdirAll(module, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := fmt.Sprintf(`{"name":"tasks","call_broker":{"source":"tasks","front":"cli","program":"tasks","classifier":[%q],"executor":%q,"scope":{"roots":"declared"},"resource":[{"type":"project"}],"cell":[{"name":"tasks","tables":["working_set"],"default":{"working_set":false}}]}}`, classifier, executor)
	if err := os.WriteFile(filepath.Join(module, "manifest.jsonc"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	pack := `name = "fixture"
[[program]]
bin = "classifier"
via = "pack"
from = "tools/classifier"
run = "classifier"

[[program]]
bin = "executor"
via = "pack"
from = "tools/executor"
run = "executor"

[[loophole]]
from = "loopholes/tasks"
`
	if err := os.WriteFile(filepath.Join(root, "pack.toml"), []byte(pack), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"classifier": "#!/bin/sh\nexit 0\n", "executor": "#!/bin/sh\nexit 0\n"} {
		dir := filepath.Join(root, "tools", name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return root, module
}

func TestCallBrokerProgramIdentitiesResolveFromSelectedPackTree(t *testing.T) {
	root, module := callBrokerProgramFixture(t, "{program:classifier}", "{program:executor}")
	items := Discover(DiscoverOptions{PackModules: []PackModule{{Dir: module, Pack: "fixture", PackRoot: root, HostExecApproved: true}}})
	if len(items) != 1 || items[0].CallBroker == nil {
		t.Fatalf("selected broker did not load: %+v", items)
	}
	identities := items[0].CallBroker.ProgramIdentities
	for name, rel := range map[string]string{"classifier": "tools/classifier/classifier", "executor": "tools/executor/executor"} {
		identity, ok := identities[name]
		if !ok || identity.Pack != "fixture" || identity.Bin != name || identity.RelPath != rel || identity.Path != filepath.Join(root, filepath.FromSlash(rel)) {
			t.Fatalf("identity %q = %+v, want selected staged path %q", name, identity, rel)
		}
		data, err := os.ReadFile(identity.Path)
		if err != nil {
			t.Fatal(err)
		}
		want := fmt.Sprintf("%x", sha256.Sum256(data))
		if identity.SHA256 != want {
			t.Errorf("identity %q checksum = %s, want %s", name, identity.SHA256, want)
		}
	}
}

func TestCallBrokerProgramIdentitiesResolveFromSelectedTOMLPackTree(t *testing.T) {
	root, module := callBrokerProgramFixture(t, "{program:classifier}", "{program:executor}")
	if err := os.Remove(filepath.Join(module, "manifest.jsonc")); err != nil {
		t.Fatal(err)
	}
	manifest := `name = "tasks"
transport = "loopback-tls"
[call_broker]
source = "tasks"
front = "cli"
program = "tasks"
classifier = ["{program:classifier}"]
executor = "{program:executor}"
[call_broker.scope]
roots = "declared"
[[call_broker.resource]]
type = "project"
[[call_broker.cell]]
name = "tasks"
tables = ["working_set"]
default = { working_set = false }
`
	if err := os.WriteFile(filepath.Join(module, "loophole.toml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	items := Discover(DiscoverOptions{PackModules: []PackModule{{Dir: module, Pack: "fixture", PackRoot: root, HostExecApproved: true}}})
	if len(items) != 1 || items[0].CallBroker == nil {
		t.Fatalf("selected TOML call broker was dropped before use: %+v", items)
	}
	for _, name := range []string{"classifier", "executor"} {
		identity, ok := items[0].CallBroker.ProgramIdentities[name]
		if !ok || identity.Pack != "fixture" || identity.Bin != name || identity.RelPath != filepath.ToSlash(filepath.Join("tools", name, name)) || identity.Path != filepath.Join(root, "tools", name, name) {
			t.Fatalf("selected TOML program identity %q = %+v", name, identity)
		}
		data, err := os.ReadFile(identity.Path)
		if err != nil || identity.SHA256 != fmt.Sprintf("%x", sha256.Sum256(data)) {
			t.Fatalf("selected TOML program checksum %q = %q, read err=%v", name, identity.SHA256, err)
		}
	}
}

func TestCallBrokerProgramIdentityRefusesTOMLModuleManifestSymlinkOutOfSelectedTree(t *testing.T) {
	root, module := callBrokerProgramFixture(t, "{program:classifier}", "core")
	if err := os.Remove(filepath.Join(module, "manifest.jsonc")); err != nil {
		t.Fatal(err)
	}
	outsideRoot, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(outsideRoot, "loophole.toml")
	manifest := `name = "tasks"
transport = "none"
[call_broker]
source = "tasks"
front = "cli"
program = "tasks"
classifier = ["{program:classifier}"]
[call_broker.scope]
roots = "declared"
[[call_broker.resource]]
type = "project"
[[call_broker.cell]]
name = "tasks"
tables = ["working_set"]
default = { working_set = false }
`
	if err := os.WriteFile(outside, []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(module, "loophole.toml")); err != nil {
		t.Fatal(err)
	}
	items := Discover(DiscoverOptions{PackModules: []PackModule{{Dir: module, Pack: "fixture", PackRoot: root, HostExecApproved: true}}})
	if len(items) != 0 {
		t.Fatalf("TOML manifest symlink escaping the selected staged tree was accepted: %+v", items)
	}
}

func TestCallBrokerProgramIdentityRefusesUnselectedOrNonExecutablePrograms(t *testing.T) {
	for _, tc := range []struct {
		name, classifier string
		chmod            os.FileMode
	}{
		{name: "undeclared program", classifier: "{program:unselected}", chmod: 0o755},
		{name: "non-executable file", classifier: "{program:classifier}", chmod: 0o644},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, module := callBrokerProgramFixture(t, tc.classifier, "core")
			if tc.chmod != 0o755 {
				if err := os.Chmod(filepath.Join(root, "tools", "classifier", "classifier"), tc.chmod); err != nil {
					t.Fatal(err)
				}
			}
			items := Discover(DiscoverOptions{PackModules: []PackModule{{Dir: module, Pack: "fixture", PackRoot: root, HostExecApproved: true}}})
			if len(items) != 0 {
				t.Fatalf("invalid selected program was accepted: %+v", items)
			}
		})
	}
}

func TestCallBrokerProgramIdentityRefusesSymlinkOutOfSelectedTree(t *testing.T) {
	root, module := callBrokerProgramFixture(t, "{program:classifier}", "core")
	external := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(external, []byte("#!/bin/sh\\nexit 0\\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	program := filepath.Join(root, "tools", "classifier", "classifier")
	if err := os.Remove(program); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, program); err != nil {
		t.Fatal(err)
	}
	items := Discover(DiscoverOptions{PackModules: []PackModule{{Dir: module, Pack: "fixture", PackRoot: root, HostExecApproved: true}}})
	if len(items) != 0 {
		t.Fatalf("program symlink escaping the selected staged tree was accepted: %+v", items)
	}
}

func TestCallBrokerProgramIdentityRefusesPackManifestSymlinkOutsideTree(t *testing.T) {
	root, module := callBrokerProgramFixture(t, "{program:classifier}", "core")
	outsideRoot, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(outsideRoot, "pack.toml")
	packManifest, err := os.ReadFile(filepath.Join(root, "pack.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outside, packManifest, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "pack.toml")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "pack.toml")); err != nil {
		t.Fatal(err)
	}
	items := Discover(DiscoverOptions{PackModules: []PackModule{{Dir: module, Pack: "fixture", PackRoot: root, HostExecApproved: true}}})
	if len(items) != 0 {
		t.Fatalf("pack manifest symlink escaping the selected staged tree was accepted: %+v", items)
	}
}

func TestCallBrokerYoloClassifierDoesNotRequireManifestBinary(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	module := filepath.Join(root, "loopholes", "tasks")
	if err := os.MkdirAll(module, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "pack.toml"), []byte("name = \"fixture\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// This is the built-in classifier route: it names yolo, not a pack-installed host binary.
	manifest := `{"name":"tasks","call_broker":{"source":"tasks","front":"cli","program":"tasks","classifier":["yolo","internal","call-broker-classify","circleci"],"scope":{"roots":"declared"},"resource":[{"type":"project"}],"cell":[{"name":"tasks","tables":["working_set"],"default":{"working_set":false}}]}}`
	if err := os.WriteFile(filepath.Join(module, "manifest.jsonc"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	items := Discover(DiscoverOptions{PackModules: []PackModule{{Dir: module, Pack: "fixture", HostExecApproved: true}}})
	if len(items) != 1 || items[0].CallBroker == nil || len(items[0].CallBroker.ProgramIdentities) != 0 {
		t.Fatalf("built-in yolo classifier was not kept distinct from manifest binaries: %+v", items)
	}
}
