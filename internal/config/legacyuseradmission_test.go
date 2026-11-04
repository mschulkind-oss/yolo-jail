package config

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// A legacy user config admits a TOML project's request for a legacy_enable kind (LC-D14), and the
// validator reads it as the launch does: no "this project requests" warning. With
// `LegacyUser: UserConfigIsLegacy()` deleted from launchLoopholeGrantInput, the validator reads the
// user as TOML, finds no on_request line, and warns that the request is skipped.
func TestTheValidatorAdmitsATOMLProjectsRequestUnderALegacyUser(t *testing.T) {
	ws := approvalWorkspace(t)
	writeLegacyUserConfig(t, `{"packs": ["host-processes"]}`)
	write(t, filepath.Join(ws, paths.WorkspaceConfigTOMLName), "[requests.host-processes]\nview = [\"postgres\"]\n")
	config, err := LoadConfig(ws, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, warns := ValidateConfig(config, ws, nil)
	for _, w := range warns {
		if strings.Contains(w, "this project requests host-processes view") {
			t.Fatalf("the validator skipped a request a legacy user config admits: %v", warns)
		}
	}
	if !LoopholeRequestAdmitted(ws, mustShippedDeclaration(t, "host-processes"), "view") {
		t.Fatalf("`yolo request`'s ceiling check refuses what the validator admits")
	}

	// Control: under a TOML user config with no ceiling line, the same request is skipped, in the
	// validator and in `yolo request`'s check alike.
	ws = approvalWorkspace(t)
	writeGrantUserConfig(t, "packs = [\"host-processes\"]\n")
	write(t, filepath.Join(ws, paths.WorkspaceConfigTOMLName), "[requests.host-processes]\nview = [\"postgres\"]\n")
	config, err = LoadConfig(ws, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, warns = ValidateConfig(config, ws, nil)
	found := false
	for _, w := range warns {
		found = found || strings.Contains(w, "this project requests host-processes view")
	}
	if !found {
		t.Fatalf("control: a TOML user config with no ceiling line did not skip the request: %v", warns)
	}
	if LoopholeRequestAdmitted(ws, mustShippedDeclaration(t, "host-processes"), "view") {
		t.Fatalf("control: `yolo request`'s ceiling check admits what the validator skips")
	}
}

// mustShippedDeclaration is the shipped pack's one grant-declaring loophole.
func mustShippedDeclaration(t *testing.T, pack string) LoopholeGrantDeclaration {
	t.Helper()
	decls := LoopholeGrantDeclarationsForPacks([]*packload.Pack{selectedEmbeddedPackForReadableDeclarations(t, pack)})
	if len(decls) != 1 {
		t.Fatalf("the shipped %s pack declares %d grant-declaring loopholes, want 1", pack, len(decls))
	}
	return decls[0]
}
