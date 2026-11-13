package cli

// hostpin.go is `yolo host`'s use of the PINNED HELPER (docs/reference/pinned-host-helpers.md, PH-D3,
// PH-D4, PH-D6): the one host yolo a launch starts its helpers from. `yolo host` resolves it
// lazily, at the first moment it is about to start a helper (a doorway, a launch-owned service or
// worker, a private daemon), so a binary-only launch that starts none resolves none and keeps
// working without a bundle. A launch that cannot resolve one refuses before it spawns that helper,
// naming the reinstall step. It never falls back to os.Executable().

import (
	"fmt"
	"os"

	"github.com/mschulkind-oss/yolo-jail/internal/helperpin"
	"github.com/mschulkind-oss/yolo-jail/internal/launchservice"
	"github.com/mschulkind-oss/yolo-jail/internal/reporoot"
)

// hostBundleRoot is the bundle root a `yolo host` launch pins its helpers from: the one reporoot
// resolves for the running yolo. A var so a test can hand a fixture bundle.
var hostBundleRoot = func() (string, error) {
	res, ok := reporoot.Resolve(os.Getenv)
	if !ok {
		if res.Error != nil {
			return "", res.Error
		}
		return "", fmt.Errorf("no yolo-jail bundle was found for this yolo. %s", reporoot.ReinstallSteps)
	}
	return res.Root, nil
}

// hostStopRemedy ends a `yolo host` launch's give-up line: its helpers end with it, and there is
// no jail to stop.
const hostStopRemedy = "end this `yolo host` session, then run it again"

// helperPin is this launch's pinned yolo, resolved on the first call and kept (an error too, so a
// refusal is not retried into a second, different answer). Asked only where a helper is about to
// start.
func (c *hostComposition) helperPin() (*helperpin.Pin, error) {
	if c.pinDone {
		return c.pin, c.pinErr
	}
	c.pinDone = true
	root, err := hostBundleRoot()
	if err != nil {
		c.pinErr = err
		return nil, err
	}
	pin, err := helperpin.Resolve(root)
	if err != nil {
		c.pinErr = err
		return nil, err
	}
	c.pin = &pin
	return c.pin, nil
}

// helperJail is the label this launch's helpers log as the jail they serve: its workspace, which
// is what `yolo host` keys its launch state by.
func (c *hostComposition) helperJail() string { return c.workspace }

// pinPlan hands a launch-owned service's plan the pin, jail label and stop step it is started with.
func (c *hostComposition) pinPlan(plan *launchservice.Plan, pin *helperpin.Pin) {
	plan.Pin, plan.Jail, plan.StopRemedy = pin, c.helperJail(), hostStopRemedy
}
