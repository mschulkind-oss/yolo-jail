package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/capture"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/setup"
)

// captureRunUsage is the whole surface of the inner driver. It is a HIDDEN subcommand
// (`yolo internal capture-run`) rather than a documented verb because nothing but the
// `yolo capture` host act should ever emit this argv: it runs an installer against the
// home it is pointed at and MOVES the result out of it, which is correct inside a
// throwaway capture jail and destructive anywhere else.
const captureRunUsage = "usage: yolo internal capture-run --out=DIR [--home=DIR] " +
	"[--surface-root=DIR | --surface-root-of=WORKSPACE] [--scan-content-refs] [--end-process-group] [--line-status=FILE] " +
	"[--artifact=HOME-RELATIVE-PATH ...] " +
	"-- <installer argv...>"

// runCaptureRun is the in-jail half of install-capture (docs/design/program-delivery.md
// §6.3): baseline the capture surfaces, run the installer, move the delta into --out.
//
// THE OUT PATH IS AN ARGUMENT, NOT AN ENVIRONMENT VARIABLE, and the reason is
// entrypoint.receiptsFile's: YOLO_WORKSPACE is a host-side launcher input that does not
// exist inside a live container, and macos-user execs under `env -i`. The host act bakes
// this path into the argv it emits, exactly as the generated launchers bake theirs.
//
// --home defaults to $HOME, which is the one environment variable that IS set in all three
// backends — "a process with a HOME" is the driver's entire contract with the world.
func runCaptureRun(args []string) int {
	out, home, surfaceRoot, lineStatus := "", os.Getenv("HOME"), "", ""
	scanContentRefs, endGroup := false, false
	var command []string
	var artifacts []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			command = args[i+1:]
			i = len(args)
		case strings.HasPrefix(a, "--out="):
			out = strings.TrimPrefix(a, "--out=")
		case strings.HasPrefix(a, "--home="):
			home = strings.TrimPrefix(a, "--home=")
		case a == "--scan-content-refs":
			// The FULL absolute-reference scan — every regular file in the delta read
			// looking for the capture HOME in its bytes. Passed by both host acts: the
			// macos-user one captures into a throwaway staging home whose path is not the
			// home a materialize will use, and the container one's entry is materialized
			// by the host agent floor into the floor's own prefix, not only into the
			// /home/agent it was captured in (captureJailArgv). An unrecorded reference
			// lands as a dead path. See capture.Options.ScanContentRefs for what it costs.
			scanContentRefs = true
		case a == "--end-process-group":
			// The installer's whole process group ends with it (capture.Options.EndProcessGroup):
			// the sealed macos-user build act's, so nothing its build line started outlives it.
			endGroup = true
		case strings.HasPrefix(a, "--line-status="):
			// Where the installer's own exit status is recorded when it exits
			// (capture.Options.StatusFile): a build's, so its host tells a build line that failed,
			// and its status, from this driver failing after one that succeeded.
			lineStatus = strings.TrimPrefix(a, "--line-status=")
		case strings.HasPrefix(a, "--artifact="):
			artifacts = append(artifacts, strings.TrimPrefix(a, "--artifact="))
		case strings.HasPrefix(a, "--surface-root="):
			// The SECOND path to the capture surfaces — see capture.Options.SurfaceRoot.
			// Passed by the host act, which is the only party that knows the workspace
			// bind exists and where it lands; the driver itself just uses the door it
			// is given.
			surfaceRoot = strings.TrimPrefix(a, "--surface-root=")
		case strings.HasPrefix(a, "--surface-root-of="):
			// The same door, named by the workspace bind it is under: the home is the session's
			// setup's (docs/design/named-setups.md §9), which the host act does not choose, so it
			// is resolved here from the session's own YOLO_SETUP (captureSurfaceRootOf).
			surfaceRoot = captureSurfaceRootOf(strings.TrimPrefix(a, "--surface-root-of="), os.Getenv(setup.EnvVar))
		default:
			fmt.Fprintf(os.Stderr, "capture-run: unexpected argument %q\n%s\n", a, captureRunUsage)
			return 2
		}
	}
	if out == "" || len(command) == 0 {
		fmt.Fprintln(os.Stderr, captureRunUsage)
		return 2
	}
	// This assertion prevents accidental use by the legacy unsealed capture route.
	// The host producer must still launch with run.Options.Sealed: an env marker
	// is not a sandbox and is not evidence of withheld host crossings.
	if artifacts != nil && os.Getenv(entrypoint.SealedBuildEnv) != "1" {
		fmt.Fprintln(os.Stderr, "capture-run: artifact production requires a sealed jail — keep this installer on its supported legacy route until its sealed producer is available")
		return 1
	}
	res, err := capture.Run(capture.Options{
		Artifacts:       artifacts,
		Home:            home,
		Out:             out,
		SurfaceRoot:     surfaceRoot,
		Command:         command,
		ScanContentRefs: scanContentRefs,
		EndProcessGroup: endGroup,
		StatusFile:      lineStatus,
		Stdout:          os.Stdout,
		Stderr:          os.Stderr,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "capture-run:", err)
		return 1
	}
	fmt.Fprintf(os.Stderr, "capture-run: %d paths in %s (%d renamed, %d copied)\n",
		len(res.Manifest.Entries), res.Tree, res.Renamed, res.Copied)
	return 0
}

// captureSurfaceRootOf is the capture surfaces' second door under the workspace bind at
// workspace: the session's setup's home (paths.SetupHomeState of setupName, the session's
// YOLO_SETUP) when it is a directory, else the legacy home, which a launch still binds while it
// is attributed to the setup (named-setups-plan.md SUP-D21). The driver checks the door against
// the home before it uses it (capture.Options.SurfaceRoot), so a wrong answer costs a copy for a
// rename, never a wrong capture.
func captureSurfaceRootOf(workspace, setupName string) string {
	if setup.ValidName(setupName) {
		home := paths.SetupHomeState(workspace, setupName)
		if fi, err := os.Lstat(home); err == nil && fi.IsDir() {
			return home
		}
	}
	return paths.LegacyHomeState(workspace)
}
