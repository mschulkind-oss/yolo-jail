package resourcegroups

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
)

// CheckDelegatedIO is a selected-only, read-only authority preflight: the mapped
// unit must be in the user manager, and the io controller must reach its cgroup
// (listed in its cgroup.controllers, which is its parent enabling io for it).
// It does NOT require io in the unit's own cgroup.subtree_control: systemd
// enables a controller there only when a child UNIT needs it, which is what
// creating the two child slices does (EnsureChildSlices reads that back). It is
// not evidence that the limits will be enforced or the work placed.
func CheckDelegatedIO(paths IOPaths, read IOReadFile) error {
	if !paths.UserManager || read == nil || paths.Assigned == "" || filepath.Base(paths.Assigned) != "io.max" || !filepath.IsAbs(paths.Assigned) {
		return errors.New("selected unit is not verified in the user manager; run yolo check --no-build")
	}
	controllers, err := read(filepath.Join(filepath.Dir(paths.Assigned), "cgroup.controllers"))
	if err != nil {
		return fmt.Errorf("assigned controllers unavailable: %w", err)
	}
	if !slices.Contains(strings.Fields(string(controllers)), "io") {
		return errors.New("io is not delegated to your user manager for the selected group (its cgroup.controllers lacks io): add io to Delegate= in a user@.service drop-in, restart your user manager, and give the selected slice a finite IOReadBandwidthMax or IOWriteBandwidthMax, then run yolo check --no-build")
	}
	return nil
}
