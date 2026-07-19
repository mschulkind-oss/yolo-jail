package checkcmd

import (
	"net"
	"os"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/image"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/storage"
)

// Builder constants (.py).
const (
	builderSSHHost  = "linux-builder"
	builderPort     = 31022
	builderKeyPath  = "/etc/nix/builder_ed25519"
	builderSSHConf  = "/etc/ssh/ssh_config.d/100-linux-builder.conf"
	nixConfPath     = "/etc/nix/nix.conf"
	nixCustomConf   = "/etc/nix/nix.custom.conf"
	nixDaemonLabelD = "org.nixos.nix-daemon"
)

// builderConfPath ports _builder_conf_path: nix.custom.conf when nix.conf
// includes it (Determinate layout), else nix.conf.
func builderConfPath() string {
	if included, ok := storage.NixCustomConfIncluded(); ok && included {
		return nixCustomConf
	}
	return nixConfPath
}

// nixConfHasBuilder ports _nix_conf_has_builder: does the daemon's conf already
// offload aarch64-linux builds to linux-builder?
func nixConfHasBuilder() bool {
	conf := builderConfPath()
	data, err := os.ReadFile(conf)
	if err != nil {
		return false
	}
	for _, raw := range strings.Split(string(data), "\n") {
		s := strings.TrimSpace(raw)
		if strings.HasPrefix(s, "#") || !strings.HasPrefix(s, "builders") {
			continue
		}
		if strings.Contains(s, "aarch64-linux") && strings.Contains(s, builderSSHHost) {
			return true
		}
	}
	return false
}

// builderSetupState ports builder_setup_state["done"]: ssh_config block present
// AND nix.conf offloads. Real default for Options.BuilderSetupDone.
func builderSetupDone() bool {
	sshOK := fileExists(builderSSHConf)
	nixOK := nixConfHasBuilder()
	return sshOK && nixOK
}

// builderKeyInstalled reports whether the VM's first-boot ssh key exists.
func builderKeyInstalled() bool {
	return fileExists(builderKeyPath)
}

// builderReachable ports builder_reachable: something accepting TCP on the
// builder SSH port.
func builderReachable() bool {
	conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", itoa(builderPort)), time.Second)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// ensureBuilderReal ports ensure_builder for the macOS check path: already
// reachable → (true,""); setup not done → (false,"not set up"); key missing →
// (false,"needs first-boot"). The actual VM start (start_builder + poll) is
// deferred to the run slice — check only needs the reachable/setup/first-boot
// verdicts to gate the doomed-build skip. When setup is done and the key is
// present but the VM isn't up, this returns (false,"not started") so the caller
// emits the "wouldn't start" FAIL rather than blocking on a headless boot.
func ensureBuilderReal(onProgress func(string)) (bool, string) {
	if !paths.IsMacOS {
		return false, "not macOS"
	}
	if builderReachable() {
		return true, ""
	}
	if !builderSetupDone() {
		return false, "not set up"
	}
	if !builderKeyInstalled() {
		return false, "needs first-boot"
	}
	// Setup done, key present, but not reachable. Starting the VM headlessly is
	// the run slice's job; report "not started" so check surfaces the actionable
	// FAIL. (This is a deliberate, documented narrowing — see the handoff.)
	if onProgress != nil {
		onProgress("Linux builder is set up but not running")
	}
	return false, "not reachable"
}

// buildImageReal runs the _build_image_store_path call check() makes: run the
// real nix build and return (storePath, stderrTail). The out-link + streaming
// spinner are elided (check only consumes the result); the store path is the
// resolved out-link.
func buildImageReal(repoRoot string, extraPackages []any) (string, []string) {
	return image.BuildOCIImage(repoRoot, extraPackages)
}

// itoa avoids strconv import churn in this file.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
