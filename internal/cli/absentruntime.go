package cli

// absentruntime.go is the next step the listing verbs (`yolo ps`, `yolo stores`, `yolo prune`)
// name when the runtime of the setup a launch from here selects is not installed: on a Mac with
// Podman and no Apple Container, the default setup's runtime is absent, and each verb would
// otherwise only report that it could not ask it.

import (
	"fmt"
	"io"
	"os/exec"
	"sync"

	"github.com/mschulkind-oss/yolo-jail/internal/setup"
)

// absentRuntimeNote is that next step for verb, "" when rt is installed or is no container
// runtime (macos-user runs none): it names the runtime that is missing and where to install it,
// and, when the other container setup runs here, the YOLO_SETUP that asks it instead.
func absentRuntimeNote(verb, rt, goos string, hasBinary func(string) bool) string {
	var mine, other string
	switch rt {
	case "podman":
		mine, other = "podman-self", "apple-container-self"
	case "container":
		mine, other = "apple-container-self", "podman-self"
	default:
		return ""
	}
	err := setup.Buildable(setup.MustBuiltIn(mine), goos, hasBinary)
	missing, ok := err.(*setup.MissingTechnologyError)
	if !ok {
		return ""
	}
	note := fmt.Sprintf("yolo %s: %s, the runtime of setup %s, is not installed here, so it has no jails to ask. Install it (%s)",
		verb, missing.Technology, mine, missing.Install)
	if setup.Buildable(setup.MustBuiltIn(other), goos, hasBinary) == nil {
		note += fmt.Sprintf(", or ask the runtime that is:\n    YOLO_SETUP=%s yolo %s", other, verb)
	} else {
		note += "."
	}
	return note
}

// listingDetect is the DetectRuntime a listing verb is handed: detectListingRuntime for ws, and,
// the first time it is asked, absentRuntimeNote on errw when that runtime is not installed.
func listingDetect(verb, ws string, errw io.Writer) func() string {
	var once sync.Once
	return func() string {
		rt := detectListingRuntime(ws)
		once.Do(func() {
			if n := absentRuntimeNote(verb, rt, platformGOOS(), func(bin string) bool {
				_, err := exec.LookPath(bin)
				return err == nil
			}); n != "" {
				fmt.Fprintln(errw, n)
			}
		})
		return rt
	}
}
