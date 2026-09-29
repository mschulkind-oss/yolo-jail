package cli

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/launchservice"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// EVERY SHIPPED SERVICE'S HOST HALF IS ONE THIS BINARY RUNS (docs/design/host-notch-services.md
// OQ-HS4): its argv is `yolo internal daemon <name>`, admitted by the launch's gate, and the
// daemon group knows the name. A host half naming a daemon the group lacks would pass validation
// and then refuse every bridged host launch at its start.
func TestEveryShippedHostHalfIsAYoloInternalDaemon(t *testing.T) {
	found := 0
	for _, p := range packload.Embedded() {
		for _, s := range p.Decl.Services() {
			if s.HostDaemon == nil {
				continue
			}
			found++
			d, err := launchservice.Admit([]*packload.Pack{p}, s.Name)
			if err != nil {
				t.Errorf("pack %s: the shipped host half is not admitted: %v", p.Name, err)
				continue
			}
			if len(d.Cmd) != 4 || strings.Join(d.Cmd[:3], " ") != "yolo internal daemon" {
				t.Errorf("pack %s: host_daemon %q is not `yolo internal daemon <name>`", p.Name, d.Cmd)
				continue
			}
			// With no input it must fail as a host half does (rc 1), not as an unknown daemon (2).
			t.Setenv(launchservice.InputEnv, "")
			if rc := runInternalDaemon(d.Cmd[3:]); rc != 1 {
				t.Errorf("pack %s: `yolo internal daemon %s` with no input = %d, want 1 (a known daemon "+
					"refusing a launch that handed it nothing)", p.Name, d.Cmd[3], rc)
			}
		}
	}
	if found == 0 {
		t.Fatal("no shipped pack declares a service host half; packs/wire-bridge must")
	}
}
