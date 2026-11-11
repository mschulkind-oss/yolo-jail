package run

import (
	"bytes"
	"os"
	"path/filepath"
	goruntime "runtime"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/helperpin"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// Exercise dispatchOptions' bundle fixture, not a separately provisioned pin fixture: the
// real Run must resolve its host helper before it can reach the backend on either host OS.
func TestDispatchBundleFixturePinsBothHostPlatforms(t *testing.T) {
	packHome(t)
	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, t.TempDir(), "macos-user", &stdout, &stderr, nil)
	root, _ := o.RepoRoot()
	for _, goos := range []string{"linux", "darwin"} {
		pin, err := helperpin.ResolveFor(root.Root, goos, goruntime.GOARCH)
		if err != nil {
			t.Fatalf("dispatch bundle's %s host helper: %v", goos, err)
		}
		body, err := os.ReadFile(pin.Path)
		if err != nil || !bytes.Equal(body, yoloStandInScript()) {
			t.Fatalf("%s host helper is not the executable test-binary stand-in: %v", goos, err)
		}
		fi, err := os.Stat(pin.Path)
		if err != nil || fi.Mode().Perm()&0o111 == 0 {
			t.Fatalf("%s host helper is not executable: %v", goos, err)
		}
	}
	o.MacosUserArm = NewMacosUserArm()
	reached := false
	o.MacosUserRun = func(*jsonx.OrderedMap, string, []string, []string, string, string, macosuser.HomeOverlay, macosuser.HostContext, bool, *jsonx.OrderedMap, []packload.BlockedTool, macosuser.JailDaemons) int {
		reached = true
		return 0
	}
	if rc := Run(*o); rc != 0 || !reached {
		t.Fatalf("Run() = %d, backend reached = %v\n%s", rc, reached, stderr.String())
	}
	pin := o.MacosUserArm.HelperPin()
	if pin == nil || pin.Path != filepath.Join(root.Root, "bin", goruntime.GOOS+"-"+goruntime.GOARCH, "yolo") {
		t.Fatalf("Run did not pin the actual host helper: %v", pin)
	}
}

// ResolveFor exercises the exact darwin-arm64 pin path reported by Mac CI on Linux too.
func TestBundleFixtureProvisionsArm64HostAndDeliveryHelpers(t *testing.T) {
	root := resolveSymlinks(t.TempDir())
	stageBundleAtForArch(t, root, true, "arm64")
	for _, goos := range []string{"linux", "darwin"} {
		pin, err := helperpin.ResolveFor(root, goos, "arm64")
		if err != nil {
			t.Fatal(err)
		}
		if want := filepath.Join(root, "bin", goos+"-arm64", "yolo"); pin.Path != want {
			t.Errorf("pin path = %q, want %q", pin.Path, want)
		}
		body, err := os.ReadFile(pin.Path)
		if err != nil || !bytes.Equal(body, yoloStandInScript()) {
			t.Fatalf("%s-arm64 host helper is not the test-binary stand-in: %v", goos, err)
		}
	}
	for goos, names := range map[string][]string{"linux": requiredJailBinaries, "darwin": macosuser.GuestBinaries} {
		for _, name := range names {
			path := filepath.Join(root, "bin", goos+"-arm64", name)
			fi, err := os.Stat(path)
			if err != nil || fi.Mode().Perm()&0o111 == 0 {
				t.Fatalf("delivery helper %s is missing or not executable: %v", path, err)
			}
		}
	}
}
