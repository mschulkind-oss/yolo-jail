package run

import (
	"bytes"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// THE SEAL THROUGH THE KEEPER: since the keeper starts the host services (keeper.go), the seal
// (seal.go) has to reach the plan the launch hands it, or a sealed fork build's keeper would start
// the loopholes and the credential view the seal forbids. keeperPlanFor carries it, and the plan
// of a sealed launch names no service.
func TestASealedLaunchsKeeperPlanIsSealedAndNamesNoService(t *testing.T) {
	for _, sealed := range []bool{true, false} {
		var stdout, stderr bytes.Buffer
		o := dispatchOptions(t, t.TempDir(), "podman", &stdout, &stderr, nil)
		o.Sealed = sealed
		plan, err := o.keeperPlanFor(jsonx.NewOrderedMap(), "podman", "yolo-seal", stagedPacks{}, nil, nil, nil, "", "", []string{"podman", "run"}, &assembleInput{})
		if err != nil {
			t.Fatal(err)
		}
		if plan.Sealed != sealed {
			t.Errorf("Sealed=%v: the keeper plan says sealed=%v", sealed, plan.Sealed)
		}
	}
}
