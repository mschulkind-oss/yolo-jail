package run

import (
	"fmt"
	"runtime"
	"strings"
)

// hostPlatform returns "<goos>/<machine>" matching Python's
// f"{sys.platform}/{platform.machine()}", using the running GOOS/GOARCH.
func hostPlatform() string {
	return runtime.GOOS + "/" + platformMachine(runtime.GOOS, runtime.GOARCH)
}

// platformMachine maps Go's GOARCH to Python's platform.machine() spelling for
// the given GOOS. It is a pure function of (goos, goarch) so every OS/arch combo
// is unit-testable, not just the one the tests happen to run on. The spelling is
// Python's, NOT Go's amd64/arm64: amd64→x86_64 everywhere; arm64→aarch64 ONLY on
// Linux — on macOS/Apple Silicon platform.machine() is "arm64" (audit 2026-07-18
// §C: the unconditional arm64→aarch64 map was wrong on macOS and a test locked
// the bug). Any other GOARCH passes through unchanged.
func platformMachine(goos, goarch string) string {
	switch goarch {
	case "amd64":
		return "x86_64"
	case "arm64":
		if goos != "darwin" {
			return "aarch64" // Linux uname; macOS keeps arm64
		}
		return "arm64"
	default:
		return goarch
	}
}

// StartupBanner formats the start-of-run banner line(s) exactly as
// _print_startup_banner writes them to stderr. jailVersion is shown only when
// it differs from version. resParts, if non-empty, adds the resource-limits line.
func StartupBanner(version, runtime_, cname string, resParts []string, jailVersion string) string {
	var verPart string
	if jailVersion != "" && jailVersion != version {
		verPart = fmt.Sprintf("yolo-jail %s (attached to jail built at %s)", version, jailVersion)
	} else {
		verPart = "yolo-jail " + version
	}
	parts := []string{verPart, hostPlatform(), runtime_, cname}
	line := strings.Join(parts, " | ")
	if len(resParts) > 0 {
		line += "\nResource limits: " + strings.Join(resParts, ", ")
	}
	return line
}
