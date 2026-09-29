package macosuser

import (
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/claudeview"
)

// TestTheCredentialViewSwitchReachesTheBootstrapEnv: the run pipeline puts the RESOLVED switch
// in the launch env of a macos-user launch that delivers the Claude credential view, and the
// bootstrap is what renders the claude pack's shared_credentials hook, which must then leave
// the view's path alone (docs/design/claude-login-without-interception.md, CL-D10, CL-D11).
// An env -i argv carries only what is relayed, so a dropped relay is a view the bootstrap links
// over.
func TestTheCredentialViewSwitchReachesTheBootstrapEnv(t *testing.T) {
	opts := optsWithTables(codexLocalTables())
	opts.PackEnv.Set(claudeview.SwitchEnv, "1")
	vars := bootstrapVars(t, buildPlan(mockDeps(nil), opts, nil).BootstrapArgv)
	if got := vars[claudeview.SwitchEnv]; got != "1" {
		t.Errorf("%s reached the bootstrap as %q, want the launch's resolved 1", claudeview.SwitchEnv, got)
	}

	opts = optsWithTables(codexLocalTables())
	vars = bootstrapVars(t, buildPlan(mockDeps(nil), opts, nil).BootstrapArgv)
	if _, ok := vars[claudeview.SwitchEnv]; ok {
		t.Errorf("%s reached a bootstrap whose launch set none", claudeview.SwitchEnv)
	}
}
