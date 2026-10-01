package macosuser

import (
	"bytes"
	"runtime"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/storage"
)

// TestMacosSetupNamesThisMacsNixInstall: on a Mac with no nix, setup's warning names the
// getting-started guide's install for this Mac's chip, as `yolo check` does. It used to end at
// "install it (https://nixos.org/download)", a page that leaves the choice of installer to the
// reader (docs/reference/happy-path-principle.md, rule 3).
func TestMacosSetupNamesThisMacsNixInstall(t *testing.T) {
	d := mockDeps(nil)
	d.Which = func(n string) bool { return n != "nix" }
	var buf bytes.Buffer
	d.Out = &buf
	if rc := MacosSetup(d); rc != 0 {
		t.Fatalf("rc = %d, want 0\n%s", rc, buf.String())
	}
	got := buf.String()
	_, cmds := storage.NixInstall(runtime.GOARCH == "amd64")
	for _, want := range append(append([]string{}, cmds...), "open a new terminal") {
		if !strings.Contains(got, want) {
			t.Errorf("setup's nix warning lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "nixos.org/download") {
		t.Errorf("setup still sends the reader to choose an installer:\n%s", got)
	}
}
