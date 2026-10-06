package run

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// THE macos-user GUEST SUPERVISOR TAKES WHAT A CONTAINER'S TAKES FROM THE CHANNEL SECTION. A
// container's supervisor inherits the shared file's channel section (the boot's
// hydrateEnvFromUserEnvFile), and jailDaemonEnv composes the guest's from the same channel, so the
// two must name the same pack env with the same values: the fold's winners in the shared
// composition, never a fold value env_sources beats (K4, K7) or removes (K5), and never a pack's
// value for a wire table, which stays the launch's. Before, jailDaemonEnv wrote the shared pack
// env whole over the tables, so the guest's daemons got the pack's K4, K5 and YOLO_PROFILES where
// a container's got env_sources' K4, no K5 and the launch's table.
func TestTheGuestDaemonEnvCarriesTheContainersChannelSection(t *testing.T) {
	o := goldenOptions(t.TempDir(), packHome(t))
	o.UseProfiles = map[string]string{"fxa": "fxp"}
	tablePack := inlinePack(t, "fy", `{"name":"fy","contributes":[`+
		`{"kind":"env","vars":{"`+entrypoint.ProfilesWireEnv+`":"fold-static"}}]}`)
	channel := channelFor(t, o, winnerFixtureConfig(), []*packload.Pack{winnerFixturePack(t), tablePack}, nil)
	ws := t.TempDir()
	deliverChannel(ws, "podman", channel)
	body, err := os.ReadFile(filepath.Join(ws, "yolo-user-env.sh"))
	if err != nil {
		t.Fatal(err)
	}
	section, ok := entrypoint.ParseEntryChannel(body)
	if !ok {
		t.Fatalf("the shared file has no complete channel section:\n%s", body)
	}
	env := channel.jailDaemonEnv(nil, jsonx.NewOrderedMap())
	skip := func(k string) bool {
		_, token := channel.callerTokens[k]
		return token || k == paths.ServedAddressesEnv || k == "YOLO_JAIL_DAEMONS"
	}
	for k, want := range section {
		if skip(k) {
			continue
		}
		if got, _ := env.Get(k); got != want {
			t.Errorf("%s: the guest's supervisor gets %v, a container's %q", k, got, want)
		}
	}
	for _, k := range env.Keys() {
		if skip(k) {
			continue
		}
		if _, ok := section[k]; !ok {
			got, _ := env.Get(k)
			t.Errorf("%s: the guest's supervisor gets %v, which a container's channel section does not carry", k, got)
		}
	}
	for _, k := range []string{"K4", "K5", "K7"} {
		if _, ok := env.Get(k); ok {
			t.Errorf("%s: a fold value env_sources beats or removes reached the guest's supervisor", k)
		}
	}
	if got, _ := env.Get("K8"); got != "fold-static" {
		t.Errorf("K8: the fold's winner must reach the guest's supervisor, got %v", got)
	}
}
