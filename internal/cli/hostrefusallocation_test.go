package cli

// hostrefusallocation_test.go pins the HOST notch naming where a refused key was written
// (userguide/reference/configuration.md#the-config-files). `yolo host`
// runs the provider and profile section of validation over the user scope alone, through its
// own call site (hostProviderSectionRefusal), so the location the launch and `yolo check` print
// has to reach this refusal separately — and this test drives the verb itself.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHostLaunchRefusalNamesTheIncludedFileAndLine(t *testing.T) {
	rc, env, errs := hostGateRunIn(t, `{`+hostProfileKeyPacks+`, "include_if_found": ["old.jsonc"]}`,
		nil, nil, "claude", func(home string) {
			p := filepath.Join(home, ".config", "yolo-jail", "old.jsonc")
			if err := os.WriteFile(p, []byte("{\n  \"use_profiles\": {\"claude\": \"zai\"}\n}\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		})
	if rc == 0 || env != nil {
		t.Fatalf("use_profiles in an include must refuse `yolo host -- claude`: rc = %d\n%s", rc, errs)
	}
	want := "~/.config/yolo-jail/old.jsonc:2:19: config.use_profiles: RENAMED"
	if !strings.Contains(errs, want) {
		t.Errorf("the host refusal does not say where use_profiles was written (want %q):\n%s", want, errs)
	}
}
