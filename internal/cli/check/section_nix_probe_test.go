package check

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/containerbuilder"
)

// The extra-platforms footgun WARNING must name this host's own linux double, not a
// hardcoded `aarch64-linux`. The detector matches any `<arch>-linux`, so on an Intel Mac it
// fires for x86_64-linux and used to tell the user to remove a line that is not in their
// nix.conf — a real problem with an unfollowable remedy. BACKLOG E8's bug class, and it
// survived because nothing tested this remedy string at all.
func TestExtraPlatformsRemedyNamesThisHostsSystem(t *testing.T) {
	var out bytes.Buffer
	o := &Options{
		Stdout:      &out,
		IsTTYStdout: func() bool { return false },
		Exec: func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
			if strings.Join(argv, " ") == "nix config show" {
				return ExecResult{Ran: true, RC: 0, Stdout: "extra-platforms = x86_64-linux aarch64-linux\n"}
			}
			return ExecResult{Ran: false}
		},
	}
	fillDefaults(o)
	o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
		if strings.Join(argv, " ") == "nix config show" {
			return ExecResult{Ran: true, RC: 0, Stdout: "extra-platforms = x86_64-linux aarch64-linux\n"}
		}
		return ExecResult{Ran: false}
	}
	r := newReporter(&out, false)
	o.nixExtraPlatformsAndBuilder(r)

	want := containerbuilder.BuilderSystem()
	if !strings.Contains(out.String(), "Remove '"+want+"'") {
		t.Errorf("the remedy must name THIS host's linux double (%q), or it asks the user to "+
			"delete a line they do not have:\n%s", want, out.String())
	}
}
