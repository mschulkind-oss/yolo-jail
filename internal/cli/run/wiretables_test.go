package run

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// Both vehicles of the channel carry every wire table entrypoint.WireTables names, with the
// same value: the per-launch env the macos-user arm consumes (launchEnv) and the container's
// yolo-user-env.sh channel section (writeUserEnvFile). The boot's channel reader
// (entrypoint.ParseEntryChannel) refuses a section missing any name on the list, so a name
// added there and not composed here would fail every container boot; this is where that
// shows up first. docs/plans/notch-convergence.md row D2.
func TestBothChannelVehiclesCarryEveryWireTable(t *testing.T) {
	c := testChannel(t)
	env := c.launchEnv("claude")
	p := filepath.Join(t.TempDir(), "yolo-user-env.sh")
	writeUserEnvFile(p, jsonx.NewOrderedMap(), c)
	body, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	section, ok := entrypoint.ParseEntryChannel(body)
	if !ok {
		t.Fatalf("the channel section the boot reads is incomplete:\n%s", body)
	}
	for _, k := range entrypoint.WireTables() {
		v, inEnv := env.Get(k)
		if !inEnv {
			t.Errorf("launchEnv does not carry %s", k)
			continue
		}
		if section[k] != v {
			t.Errorf("%s differs between the vehicles: launch env %q, user env file %q", k, v, section[k])
		}
	}
}
