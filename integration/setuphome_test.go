package integration

import (
	"os"
	"path/filepath"
	goruntime "runtime"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/setup"
)

// jailHome is the home a container launch in dir binds when the workspace held no legacy home:
// the home of the setup the suite's launches select, YOLO_SETUP where the job names one (the macOS
// workflows name podman-self), else the platform default (named-setups-plan.md Slice 6a-ii).
func jailHome(dir string) string {
	name := os.Getenv("YOLO_SETUP")
	if name == "" {
		name = setup.Default(goruntime.GOOS).Name
	}
	return paths.SetupHomeState(dir, name)
}

// jailPrism is the capture store beside jailHome(dir) (named-setups-plan.md SUP-D35): each home
// keeps its own baselines and overlays, so a fresh workspace's store is the setup's, not .yolo/prism.
func jailPrism(dir string) string {
	return filepath.Join(filepath.Dir(jailHome(dir)), "prism")
}

// appleContainerHome is the home an Apple Container launch (appleContainerEnv) binds in dir.
func appleContainerHome(dir string) string {
	return paths.SetupHomeState(dir, "apple-container-self")
}

// macosUserHome is the home a macos-user launch of mode (macosUserSetupFor) binds in dir.
func macosUserHome(dir, mode string) string {
	return paths.SetupHomeState(dir, macosUserSetupFor(mode))
}
