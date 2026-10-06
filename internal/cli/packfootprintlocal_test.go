package cli

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

// A LOCAL PACK'S DOORWAY HOST ARGV IS IN ITS FOOTPRINT (HS-D27): `yolo pack footprint <dir>` names
// the argv a launch selecting the same path runs outside every sandbox, as it names the loophole's
// host daemon.
func TestPackFootprintOfAPathNamesALocalDoorwaysHostArgv(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	pack := filepath.Join(t.TempDir(), "acme")
	writeFile(t, filepath.Join(pack, "pack.json"), `{"name": "acme", "contributes": [
		{"kind": "loophole", "from": "loopholes/acme-door"}]}`)
	writeFile(t, filepath.Join(pack, "loopholes", "acme-door", "manifest.jsonc"), `{"name": "acme-door",
		"description": "acme door", "default_enabled": true, "transport": "loopback-tls",
		"jail_daemon": {"cmd": ["yolo-jaild", "acme-adapter", "--listen", "{listen}"],
		"listen": "127.0.0.1:1999", "caller_token": true,
		"host_cmd": ["yolo", "internal", "daemon", "acme-adapter", "--listen", "{listen}"]}}`)
	for _, arg := range []string{pack, "file://" + pack} {
		var out, errw bytes.Buffer
		if rc := packMain([]string{"footprint", arg}, &out, &errw, false); rc != 0 {
			t.Fatalf("yolo pack footprint %s rc=%d\n%s%s", arg, rc, out.String(), errw.String())
		}
		if want := "yolo internal daemon acme-adapter --listen '{listen}'"; !strings.Contains(out.String(), want) {
			t.Errorf("yolo pack footprint %s does not name the doorway's host argv %q:\n%s", arg, want, out.String())
		}
	}
}
