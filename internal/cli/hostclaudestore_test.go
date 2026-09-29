package cli

import (
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/claudeview"
)

// THE HOST NOTCH KEEPS THE USER'S OWN CLAUDE LOGIN (OQ-NC7), so CL-D22's bridge is a jail
// launch's alone (internal/cli/run/claudesecurestorage.go). The host notch runs no pack hook
// (render.HostUnimplemented names the kind) and mounts no machine-scope directory, so host
// Claude reads the user's own ~/.claude or Keychain, and pointing its credential store at a
// directory yolo owns would move the user's login out from under them. This pins the ABSENCE at
// the exec `yolo host -- claude` performs, so moving the bridge into anything the host notch
// composes too (the claude pack's `env` contribution, say) fails here.
func TestHostClaudeKeepsItsOwnCredentialStore(t *testing.T) {
	env, _ := hostGateLaunchWith(t, claudeAlone,
		map[string]string{claudeview.SecureStorageEnv: ""}, nil, "claude")
	if v := env[claudeview.SecureStorageEnv]; v != "" {
		t.Errorf("yolo host -- claude pointed the host's Claude credential store at %q: host "+
			"Claude keeps the user's own login", v)
	}
}
