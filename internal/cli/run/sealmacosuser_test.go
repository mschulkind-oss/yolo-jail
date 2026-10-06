package run

// sealmacosuser_test.go pins the seal on the macos-user arm (seal.go's runSealedMacosUser;
// docs/design/forked-programs-as-packs.md FP-D19): a sealed launch there is a fork's build, and it
// hands its backend nothing of the host's — no context mount, host service, doorway, credential view,
// jail daemon or host bytes — by leaving the arm above its first crossing site.

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/openauthclient"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// sealedNativeLaunch is a macos-user launch of codex — whose pack points every launch at the refresh
// doorway and the OpenAI credential service — with a context mount this backend cannot deliver, which
// the unsealed arm refuses the launch over before anything else.
func sealedNativeLaunch(t *testing.T, sealed bool) (*Options, *nativeLaunch, *doorwaysSeen) {
	t.Helper()
	src := t.TempDir()
	o, _, seen := overrideNativeLaunch(t, `{"packs": ["codex"], "mounts": ["`+src+`:/ctx/ref"]}`, shellWith(nil))
	o.Args = []string{"codex"}
	o.ProfileName = ""
	refusingSiting(t)(o)
	o.Sealed = sealed
	return o, seen, observeDoorways(t)
}

func TestASealedMacosUserLaunchHandsTheBuildNothingOfTheHosts(t *testing.T) {
	// THE CONTROL: unsealed, the same launch is refused over its context mount before the backend runs,
	// so a sealed launch that reaches the backend left the arm above that site.
	o, seen, _ := sealedNativeLaunch(t, false)
	if rc := Run(*o); rc == 0 || seen.reached {
		t.Fatalf("the unsealed control ran (rc %d, reached %v): the context mount it declares no longer "+
			"refuses on this backend, so this test measures nothing", rc, seen.reached)
	}

	o, seen, doors := sealedNativeLaunch(t, true)
	if rc := Run(*o); rc != 0 || !seen.reached {
		t.Fatalf("the sealed launch returned %d and reached the backend %v", rc, seen.reached)
	}
	if len(seen.hostCtx.Links) != 0 || seen.hostCtx.Tree != "" || len(seen.hostCtx.Captures) != 0 ||
		len(seen.hostCtx.Relocations) != 0 || len(seen.hostCtx.HostFiles) != 0 || seen.hostCtx.GlobalGitignore != "" {
		t.Errorf("the sealed build was handed host context %+v", seen.hostCtx)
	}
	if seen.jailDaemons.Env != nil && seen.jailDaemons.Env.Len() != 0 {
		t.Errorf("the sealed build was handed a jail-daemon environment: %v", seen.jailDaemons.Env.Keys())
	}
	if len(doors.plans) != 0 {
		t.Errorf("the sealed build opened %d doorways", len(doors.plans))
	}
	for _, k := range seen.env.Keys() {
		if strings.HasPrefix(k, paths.ServiceEnvVarPrefix) || k == "CODEX_REFRESH_TOKEN_URL_OVERRIDE" ||
			k == openauthclient.CallerTokenEnv {
			v, _ := seen.env.Get(k)
			t.Errorf("the sealed build's environment names a host crossing: %s=%v", k, v)
		}
	}
}
